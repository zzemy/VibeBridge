package attachment

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/zzemy/VibeBridge/internal/workspace"
)

const (
	metadataDirectoryName = ".vibebridge"
	uploadsDirectoryName  = "uploads"
	maxSessionIDBytes     = 64
)

// SessionStaging owns one session's temporary attachment directory inside a
// validated workspace. Physical directory names are derived only from opaque
// Agent-generated session IDs.
type SessionStaging struct {
	workspaceRoot string
	path          string

	mu                     sync.Mutex
	cleaned                bool
	openDirectories        int
	transferManagerOpen    bool
	completedBytesReserved uint64
}

// CreateSessionStaging creates a new, empty staging directory beneath the
// validated workspace root. Existing links and non-directory path components
// are rejected before any child directory is created.
func CreateSessionStaging(workspaceRoot string, sessionID []byte) (*SessionStaging, error) {
	if len(sessionID) == 0 || len(sessionID) > maxSessionIDBytes {
		return nil, errors.New("session ID length is invalid for staging")
	}
	currentRoot, err := workspace.RevalidateDirectory(workspaceRoot, "")
	if err != nil {
		return nil, errors.New("validate staging workspace failed")
	}

	metadataDirectory := filepath.Join(currentRoot, metadataDirectoryName)
	if err := ensureCanonicalDirectory(currentRoot, metadataDirectory); err != nil {
		return nil, fmt.Errorf("prepare staging metadata directory: %w", err)
	}
	uploadsDirectory := filepath.Join(metadataDirectory, uploadsDirectoryName)
	if err := ensureCanonicalDirectory(currentRoot, uploadsDirectory); err != nil {
		return nil, fmt.Errorf("prepare staging uploads directory: %w", err)
	}

	path := filepath.Join(uploadsDirectory, hex.EncodeToString(sessionID))
	if err := os.Mkdir(path, 0o700); err != nil {
		return nil, newPathOperationError("create session staging directory", err)
	}
	if err := validateCanonicalDirectory(currentRoot, path); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("validate session staging directory: %w", err)
	}
	return &SessionStaging{workspaceRoot: currentRoot, path: path}, nil
}

// Path returns the canonical local directory reserved for this session.
func (s *SessionStaging) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Cleanup removes this session's staging entry without following a replacement
// symlink. Successful and already-complete cleanup calls are idempotent;
// failures remain retryable.
func (s *SessionStaging) Cleanup() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleaned {
		return nil
	}
	if s.openDirectories > 0 || s.transferManagerOpen {
		return errors.New("session staging directory is still in use")
	}

	uploadsDirectory := filepath.Dir(s.path)
	if err := validateCanonicalDirectory(s.workspaceRoot, uploadsDirectory); err != nil {
		return fmt.Errorf("validate staging cleanup boundary: %w", err)
	}
	info, err := os.Lstat(s.path)
	if os.IsNotExist(err) {
		s.cleaned = true
		s.completedBytesReserved = 0
		return nil
	}
	if err != nil {
		return newPathOperationError("inspect session staging directory", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return newPathOperationError("remove replaced session staging entry", err)
		}
		s.cleaned = true
		s.completedBytesReserved = 0
		return nil
	}
	if err := validateCanonicalDirectory(s.workspaceRoot, s.path); err != nil {
		return fmt.Errorf("validate session staging cleanup target: %w", err)
	}
	if err := os.RemoveAll(s.path); err != nil {
		return newPathOperationError("remove session staging directory", err)
	}
	s.cleaned = true
	s.completedBytesReserved = 0
	return nil
}

func (s *SessionStaging) completedBytes() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completedBytesReserved
}

func (s *SessionStaging) recordCompletedBytes(size uint64) {
	s.mu.Lock()
	s.completedBytesReserved += size
	s.mu.Unlock()
}

func (s *SessionStaging) releaseCompletedBytes(size uint64) {
	s.mu.Lock()
	if size <= s.completedBytesReserved {
		s.completedBytesReserved -= size
	} else {
		s.completedBytesReserved = 0
	}
	s.mu.Unlock()
}

func claimTransferManager(staging *SessionStaging) error {
	if staging == nil {
		return errors.New("session staging is required")
	}
	staging.mu.Lock()
	defer staging.mu.Unlock()
	if staging.cleaned {
		return errors.New("session staging is already cleaned")
	}
	if staging.transferManagerOpen {
		return errors.New("session staging already has a transfer manager")
	}
	staging.transferManagerOpen = true
	return nil
}

func releaseTransferManager(staging *SessionStaging) {
	if staging == nil {
		return
	}
	staging.mu.Lock()
	staging.transferManagerOpen = false
	staging.mu.Unlock()
}

func ensureCanonicalDirectory(workspaceRoot string, path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
			return newPathOperationError("create staging directory", err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return newPathOperationError("inspect staging directory", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("staging path component must not be a link")
	}
	if !info.IsDir() {
		return errors.New("staging path component must be a directory")
	}
	return validateCanonicalDirectory(workspaceRoot, path)
}

func validateCanonicalDirectory(workspaceRoot string, path string) error {
	canonical, err := workspace.RevalidateDirectory(workspaceRoot, path)
	if err != nil {
		return errors.New("staging path boundary is invalid")
	}
	if filepath.Clean(canonical) != filepath.Clean(path) {
		return errors.New("staging path component must be canonical")
	}
	return nil
}

type pathOperationError struct {
	operation string
	cause     error
}

func newPathOperationError(operation string, err error) error {
	return pathOperationError{operation: operation, cause: safePathOperationCause(err)}
}

func (e pathOperationError) Error() string {
	return e.operation + " failed"
}

// Unwrap preserves only path-free error classification. In particular, it
// never exposes the Path field from os.PathError to callers or logs.
func (e pathOperationError) Unwrap() error {
	return e.cause
}

// CleanupStaleStaging removes all orphaned session staging directories beneath a
// workspace root. It is safe to call on Agent startup: if no sessions are active,
// every directory under .vibebridge/uploads/ is a crash-recovery candidate.
// Symlinks and non-directory entries are rejected; only canonical subdirectories
// of the validated workspace are removed.
func CleanupStaleStaging(workspaceRoot string) error {
	currentRoot, err := workspace.RevalidateDirectory(workspaceRoot, "")
	if err != nil {
		return errors.New("validate staging workspace failed")
	}
	uploadsDir := filepath.Join(currentRoot, metadataDirectoryName, uploadsDirectoryName)
	info, err := os.Lstat(uploadsDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return newPathOperationError("inspect uploads directory", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("uploads path must be a directory")
	}
	if err := validateCanonicalDirectory(currentRoot, uploadsDir); err != nil {
		return fmt.Errorf("validate uploads directory boundary: %w", err)
	}
	entries, err := os.ReadDir(uploadsDir)
	if err != nil {
		return newPathOperationError("read uploads directory", err)
	}
	for _, entry := range entries {
		sessionPath := filepath.Join(uploadsDir, entry.Name())
		entryInfo, err := os.Lstat(sessionPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return newPathOperationError("inspect stale session staging", err)
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(sessionPath); err != nil && !os.IsNotExist(err) {
				return newPathOperationError("remove stale symlink staging entry", err)
			}
			continue
		}
		if !entryInfo.IsDir() {
			if err := os.Remove(sessionPath); err != nil && !os.IsNotExist(err) {
				return newPathOperationError("remove stale non-directory staging entry", err)
			}
			continue
		}
		if err := validateCanonicalDirectory(currentRoot, sessionPath); err != nil {
			return fmt.Errorf("validate stale session staging boundary: %w", err)
		}
		if err := os.RemoveAll(sessionPath); err != nil {
			return newPathOperationError("remove stale session staging directory", err)
		}
	}
	return nil
}

func safePathOperationCause(err error) error {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno
	}
	for _, sentinel := range []error{
		fs.ErrInvalid,
		fs.ErrPermission,
		fs.ErrExist,
		fs.ErrNotExist,
		fs.ErrClosed,
		io.ErrShortWrite,
	} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return nil
}
