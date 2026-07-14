package promptaction

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRegistryCommitsPreparedBytesOnce(t *testing.T) {
	registry := NewRegistry()
	actionID := []byte("action-1")
	action := Action{Preview: "Review `file.txt`", TerminalInput: []byte("Review `file.txt`\r")}

	prepared, err := registry.Prepare(actionID, action)
	if err != nil {
		t.Fatalf("prepare action: %v", err)
	}
	if prepared.State != StatePrepared || prepared.Preview != action.Preview {
		t.Fatalf("prepare result = %+v, want prepared preview", prepared)
	}
	actionID[0] = 'X'
	action.TerminalInput[0] = 'X'

	var writes [][]byte
	writer := func(input []byte) error {
		writes = append(writes, append([]byte(nil), input...))
		input[0] = 'X'
		return nil
	}
	if err := registry.Commit([]byte("action-1"), writer); err != nil {
		t.Fatalf("commit action: %v", err)
	}
	if err := registry.Commit([]byte("action-1"), writer); err != nil {
		t.Fatalf("repeat commit action: %v", err)
	}
	if len(writes) != 1 || !bytes.Equal(writes[0], []byte("Review `file.txt`\r")) {
		t.Fatalf("PTY writes = %q, want one exact prepared input", writes)
	}

	repeated, err := registry.Prepare([]byte("action-1"), Action{Preview: "Review `file.txt`", TerminalInput: []byte("Review `file.txt`\r")})
	if err != nil {
		t.Fatalf("repeat prepare after commit: %v", err)
	}
	if repeated.State != StateCommitted || repeated.Preview != "" {
		t.Fatalf("repeat prepare result = %+v, want committed tombstone", repeated)
	}
}

func TestRegistrySerializesConcurrentDuplicateCommits(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.Prepare([]byte("concurrent"), Action{Preview: "Review", TerminalInput: []byte("Review\r")}); err != nil {
		t.Fatalf("prepare action: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	writer := func([]byte) error {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return nil
	}
	results := make(chan error, 2)
	go func() { results <- registry.Commit([]byte("concurrent"), writer) }()
	<-started
	go func() { results <- registry.Commit([]byte("concurrent"), writer) }()
	if calls.Load() != 1 {
		t.Fatalf("writer calls before release = %d, want 1", calls.Load())
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent commit: %v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("writer calls = %d, want 1", calls.Load())
	}
}

func TestRegistryMakesPrepareIdempotentAndRejectsConflictingReuse(t *testing.T) {
	registry := NewRegistry()
	action := Action{Preview: "Review file", TerminalInput: []byte("Review file")}
	first, err := registry.Prepare([]byte("same-id"), action)
	if err != nil {
		t.Fatalf("prepare action: %v", err)
	}
	second, err := registry.Prepare([]byte("same-id"), action)
	if err != nil {
		t.Fatalf("repeat prepare action: %v", err)
	}
	if first != second || second.State != StatePrepared {
		t.Fatalf("idempotent prepare results = %+v/%+v", first, second)
	}
	if _, err := registry.Prepare([]byte("same-id"), Action{Preview: "Different", TerminalInput: []byte("Different")}); !errors.Is(err, ErrActionConflict) {
		t.Fatalf("conflicting Prepare() error = %v, want %v", err, ErrActionConflict)
	}
}

func TestRegistryCancellationLeavesNonReusableTombstone(t *testing.T) {
	registry := NewRegistry()
	action := Action{Preview: "Review file", TerminalInput: []byte("Review file")}
	if _, err := registry.Prepare([]byte("cancelled"), action); err != nil {
		t.Fatalf("prepare action: %v", err)
	}
	if err := registry.Cancel([]byte("cancelled")); err != nil {
		t.Fatalf("cancel action: %v", err)
	}
	if err := registry.Cancel([]byte("cancelled")); err != nil {
		t.Fatalf("repeat cancel action: %v", err)
	}
	if err := registry.Commit([]byte("cancelled"), func([]byte) error { t.Fatal("cancelled action wrote input"); return nil }); !errors.Is(err, ErrActionCancelled) {
		t.Fatalf("cancelled Commit() error = %v, want %v", err, ErrActionCancelled)
	}
	if _, err := registry.Prepare([]byte("cancelled"), action); !errors.Is(err, ErrActionCancelled) {
		t.Fatalf("same cancelled Prepare() error = %v, want %v", err, ErrActionCancelled)
	}
	if _, err := registry.Prepare([]byte("cancelled"), Action{Preview: "Different", TerminalInput: []byte("Different")}); !errors.Is(err, ErrActionConflict) {
		t.Fatalf("conflicting cancelled Prepare() error = %v, want %v", err, ErrActionConflict)
	}
}

func TestRegistryDoesNotRetryAmbiguousFailedWrite(t *testing.T) {
	registry := NewRegistry()
	action := Action{Preview: "Review file", TerminalInput: []byte("Review file\r")}
	if _, err := registry.Prepare([]byte("failed"), action); err != nil {
		t.Fatalf("prepare action: %v", err)
	}
	writeErr := errors.New("partial PTY write")
	calls := 0
	writer := func([]byte) error { calls++; return writeErr }
	if err := registry.Commit([]byte("failed"), writer); !errors.Is(err, writeErr) {
		t.Fatalf("first Commit() error = %v, want wrapped writer error", err)
	}
	if err := registry.Commit([]byte("failed"), writer); !errors.Is(err, ErrActionCommitFailed) {
		t.Fatalf("repeat Commit() error = %v, want %v", err, ErrActionCommitFailed)
	}
	if calls != 1 {
		t.Fatalf("writer calls = %d, want 1", calls)
	}
}

func TestRegistryEnforcesActionAndMemoryBounds(t *testing.T) {
	valid := Action{Preview: "prompt", TerminalInput: []byte("prompt\r")}

	pendingRegistry := NewRegistry()
	for index := range defaultMaxPendingActions {
		if _, err := pendingRegistry.Prepare([]byte(fmt.Sprintf("pending-%d", index)), valid); err != nil {
			t.Fatalf("prepare pending action %d: %v", index, err)
		}
	}
	if _, err := pendingRegistry.Prepare([]byte("pending-overflow"), valid); !errors.Is(err, ErrPendingActionLimit) {
		t.Fatalf("pending-limit Prepare() error = %v, want %v", err, ErrPendingActionLimit)
	}

	trackedRegistry := NewRegistry()
	for index := range defaultMaxTrackedActions {
		id := []byte(fmt.Sprintf("tracked-%d", index))
		if _, err := trackedRegistry.Prepare(id, valid); err != nil {
			t.Fatalf("prepare tracked action %d: %v", index, err)
		}
		if err := trackedRegistry.Cancel(id); err != nil {
			t.Fatalf("cancel tracked action %d: %v", index, err)
		}
	}
	if _, err := trackedRegistry.Prepare([]byte("tracked-overflow"), valid); !errors.Is(err, ErrTrackedActionLimit) {
		t.Fatalf("tracked-limit Prepare() error = %v, want %v", err, ErrTrackedActionLimit)
	}

	tests := []struct {
		name   string
		id     []byte
		action Action
	}{
		{name: "missing id", id: nil, action: valid},
		{name: "oversized id", id: []byte(strings.Repeat("x", defaultMaxActionIDBytes+1)), action: valid},
		{name: "blank preview", id: []byte("id"), action: Action{Preview: "", TerminalInput: []byte("x")}},
		{name: "invalid preview", id: []byte("id"), action: Action{Preview: string([]byte{0xff}), TerminalInput: []byte("x")}},
		{name: "oversized preview", id: []byte("id"), action: Action{Preview: strings.Repeat("x", defaultMaxPreviewBytes+1), TerminalInput: []byte("x")}},
		{name: "missing input", id: []byte("id"), action: Action{Preview: "x"}},
		{name: "oversized input", id: []byte("id"), action: Action{Preview: "x", TerminalInput: []byte(strings.Repeat("x", defaultMaxTerminalInputBytes+1))}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewRegistry().Prepare(test.id, test.action); !errors.Is(err, ErrInvalidAction) {
				t.Fatalf("Prepare() error = %v, want %v", err, ErrInvalidAction)
			}
		})
	}
}

func TestRegistryCloseClearsActionsAndRejectsFurtherOperations(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.Prepare([]byte("action"), Action{Preview: "prompt", TerminalInput: []byte("prompt")}); err != nil {
		t.Fatalf("prepare action: %v", err)
	}
	registry.Close()
	registry.Close()
	if _, err := registry.Prepare([]byte("new"), Action{Preview: "prompt", TerminalInput: []byte("prompt")}); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("Prepare() after close error = %v, want %v", err, ErrRegistryClosed)
	}
	if err := registry.Commit([]byte("action"), func([]byte) error { return nil }); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("Commit() after close error = %v, want %v", err, ErrRegistryClosed)
	}
	if err := registry.Cancel([]byte("action")); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("Cancel() after close error = %v, want %v", err, ErrRegistryClosed)
	}
}
