package promptaction

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"unicode/utf8"
)

const (
	defaultMaxTrackedActions     = 4096
	defaultMaxPendingActions     = 16
	defaultMaxActionIDBytes      = 64
	defaultMaxPreviewBytes       = 48 * 1024
	defaultMaxTerminalInputBytes = defaultMaxPreviewBytes + 1
)

var (
	ErrInvalidAction      = errors.New("prompt action is invalid")
	ErrPendingActionLimit = errors.New("pending prompt action limit exceeded")
	ErrTrackedActionLimit = errors.New("tracked prompt action limit exceeded")
	ErrActionConflict     = errors.New("prompt action ID conflicts with an existing action")
	ErrActionNotFound     = errors.New("prompt action was not found")
	ErrActionCancelled    = errors.New("prompt action was cancelled")
	ErrActionCommitted    = errors.New("prompt action was already committed")
	ErrActionCommitFailed = errors.New("prompt action commit previously failed")
	ErrRegistryClosed     = errors.New("prompt action registry is closed")
)

// State is the durable session-local outcome for an action ID.
type State uint8

const (
	StatePrepared State = iota + 1
	StateCommitted
	stateCancelled
	stateCommitFailed
)

// Action contains the exact user preview and terminal bytes produced by a
// trusted local adapter.
type Action struct {
	Preview       string
	TerminalInput []byte
}

// PrepareResult reports whether the action still awaits confirmation or has
// already been committed. A committed tombstone intentionally omits preview.
type PrepareResult struct {
	State   State
	Preview string
}

// Registry owns bounded idempotency state for one PTY session.
type Registry struct {
	mu sync.Mutex

	entries map[string]*registryEntry
	pending int
	closed  bool
}

type registryEntry struct {
	digest        [sha256.Size]byte
	state         State
	preview       string
	terminalInput []byte
}

// NewRegistry creates a registry with production session limits.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]*registryEntry)}
}

// Prepare registers immutable bytes or returns the matching action's durable
// state. Reusing an ID for different content is always rejected.
func (r *Registry) Prepare(actionID []byte, action Action) (PrepareResult, error) {
	if r == nil {
		return PrepareResult{}, ErrRegistryClosed
	}
	if !validActionID(actionID) || !validAction(action) {
		return PrepareResult{}, ErrInvalidAction
	}
	digest := actionDigest(action)
	key := string(actionID)

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return PrepareResult{}, ErrRegistryClosed
	}
	if entry, exists := r.entries[key]; exists {
		if entry.digest != digest {
			return PrepareResult{}, ErrActionConflict
		}
		switch entry.state {
		case StatePrepared:
			return PrepareResult{State: StatePrepared, Preview: entry.preview}, nil
		case StateCommitted:
			return PrepareResult{State: StateCommitted}, nil
		case stateCancelled:
			return PrepareResult{}, ErrActionCancelled
		case stateCommitFailed:
			return PrepareResult{}, ErrActionCommitFailed
		default:
			return PrepareResult{}, ErrInvalidAction
		}
	}
	if len(r.entries) >= defaultMaxTrackedActions {
		return PrepareResult{}, ErrTrackedActionLimit
	}
	if r.pending >= defaultMaxPendingActions {
		return PrepareResult{}, ErrPendingActionLimit
	}

	r.entries[key] = &registryEntry{
		digest:        digest,
		state:         StatePrepared,
		preview:       action.Preview,
		terminalInput: append([]byte(nil), action.TerminalInput...),
	}
	r.pending++
	return PrepareResult{State: StatePrepared, Preview: action.Preview}, nil
}

// Commit invokes writer at most once for an action ID. A failed write is
// tombstoned because a partial PTY write cannot safely be retried.
func (r *Registry) Commit(actionID []byte, writer func([]byte) error) error {
	if r == nil {
		return ErrRegistryClosed
	}
	if !validActionID(actionID) || writer == nil {
		return ErrInvalidAction
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrRegistryClosed
	}
	entry, exists := r.entries[string(actionID)]
	if !exists {
		return ErrActionNotFound
	}
	switch entry.state {
	case StateCommitted:
		return nil
	case stateCancelled:
		return ErrActionCancelled
	case stateCommitFailed:
		return ErrActionCommitFailed
	case StatePrepared:
		input := append([]byte(nil), entry.terminalInput...)
		if err := writer(input); err != nil {
			r.finalizeEntry(entry, stateCommitFailed)
			return fmt.Errorf("commit prompt action: %w", err)
		}
		r.finalizeEntry(entry, StateCommitted)
		return nil
	default:
		return ErrInvalidAction
	}
}

// Cancel removes pending bytes while retaining a tombstone that prevents later
// commit or conflicting ID reuse.
func (r *Registry) Cancel(actionID []byte) error {
	if r == nil {
		return ErrRegistryClosed
	}
	if !validActionID(actionID) {
		return ErrInvalidAction
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrRegistryClosed
	}
	entry, exists := r.entries[string(actionID)]
	if !exists {
		return ErrActionNotFound
	}
	switch entry.state {
	case StatePrepared:
		r.finalizeEntry(entry, stateCancelled)
		return nil
	case stateCancelled:
		return nil
	case StateCommitted:
		return ErrActionCommitted
	case stateCommitFailed:
		return ErrActionCommitFailed
	default:
		return ErrInvalidAction
	}
}

// Close clears all prepared bytes and tombstones. It is idempotent.
func (r *Registry) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.entries = nil
	r.pending = 0
	r.closed = true
}

func validActionID(actionID []byte) bool {
	return len(actionID) > 0 && len(actionID) <= defaultMaxActionIDBytes
}

func (r *Registry) finalizeEntry(entry *registryEntry, state State) {
	entry.state = state
	entry.preview = ""
	entry.terminalInput = nil
	if r.pending > 0 {
		r.pending--
	}
}

func validAction(action Action) bool {
	return action.Preview != "" && utf8.ValidString(action.Preview) && len(action.Preview) <= defaultMaxPreviewBytes &&
		len(action.TerminalInput) > 0 && len(action.TerminalInput) <= defaultMaxTerminalInputBytes
}

func actionDigest(action Action) [sha256.Size]byte {
	hasher := sha256.New()
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(action.Preview)))
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write([]byte(action.Preview))
	binary.BigEndian.PutUint64(length[:], uint64(len(action.TerminalInput)))
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write(action.TerminalInput)
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return digest
}
