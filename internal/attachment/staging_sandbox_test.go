package attachment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateSandboxStaging(t *testing.T) {
	sessionID := []byte("test-sandbox-session-id-001")
	staging, err := CreateSandboxStaging(sessionID)
	if err != nil {
		t.Fatalf("CreateSandboxStaging: %v", err)
	}
	defer staging.Cleanup()

	root := staging.Root()
	if root == "" {
		t.Fatal("Root() returned empty string")
	}
	if !filepath.IsAbs(root) {
		t.Fatalf("Root() = %q, want absolute path", root)
	}

	if staging.Path() == "" {
		t.Fatal("Path() returned empty string")
	}

	info, err := os.Lstat(staging.Path())
	if err != nil {
		t.Fatalf("Lstat staging path: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("staging path is not a directory")
	}
}

func TestSandboxStagingRootIsTempBased(t *testing.T) {
	sessionID := []byte("test-sandbox-root-002")
	staging, err := CreateSandboxStaging(sessionID)
	if err != nil {
		t.Fatalf("CreateSandboxStaging: %v", err)
	}
	defer staging.Cleanup()

	root := staging.Root()
	if !filepath.IsAbs(root) {
		t.Fatalf("Root() = %q, want absolute path", root)
	}

	// The root should be under the OS temp directory (after symlink resolution)
	tempDir, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks temp dir: %v", err)
	}
	if !filepath.IsAbs(root) || root != filepath.Clean(root) {
		t.Fatalf("Root() = %q, want canonical absolute path", root)
	}
	// Verify the root is within the temp directory hierarchy
	rel, err := filepath.Rel(tempDir, root)
	if err != nil {
		t.Fatalf("Rel(tempDir, root): %v", err)
	}
	if strings.HasPrefix(rel, "..") {
		t.Fatalf("root %q is not under temp dir %q", root, tempDir)
	}
}

func TestSandboxStagingCleanup(t *testing.T) {
	sessionID := []byte("test-sandbox-cleanup-003")
	staging, err := CreateSandboxStaging(sessionID)
	if err != nil {
		t.Fatalf("CreateSandboxStaging: %v", err)
	}

	path := staging.Path()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("staging dir not created: %v", err)
	}

	if err := staging.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("after cleanup, Lstat: %v, want ErrNotExist", err)
	}
}

func TestSandboxStagingRejectsInvalidSessionID(t *testing.T) {
	if _, err := CreateSandboxStaging(nil); err == nil {
		t.Fatal("CreateSandboxStaging(nil) should fail")
	}
	longID := make([]byte, maxSessionIDBytes+1)
	if _, err := CreateSandboxStaging(longID); err == nil {
		t.Fatal("CreateSandboxStaging(oversized) should fail")
	}
}

func TestCleanupStaleSandboxStagingNoDir(t *testing.T) {
	// When the sandbox dir does not exist, cleanup should be a no-op
	// First remove any existing sandbox dir
	sandboxRoot := filepath.Join(os.TempDir(), sandboxDirName)
	_ = os.RemoveAll(sandboxRoot)
	if err := CleanupStaleSandboxStaging(); err != nil {
		t.Fatalf("CleanupStaleSandboxStaging on missing dir: %v", err)
	}
}

func TestCleanupStaleSandboxStagingRemovesOrphans(t *testing.T) {
	sandboxRoot := filepath.Join(os.TempDir(), sandboxDirName)
	_ = os.RemoveAll(sandboxRoot)
	defer os.RemoveAll(sandboxRoot)

	// Create a staging session, then simulate a crash by not cleaning it up
	sessionID := []byte("test-stale-orphan-004")
	staging, err := CreateSandboxStaging(sessionID)
	if err != nil {
		t.Fatalf("CreateSandboxStaging: %v", err)
	}
	stalePath := staging.Path()

	// Put a file in it to simulate real usage
	if err := os.WriteFile(filepath.Join(stalePath, "dummy"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write dummy file: %v", err)
	}

	if err := CleanupStaleSandboxStaging(); err != nil {
		t.Fatalf("CleanupStaleSandboxStaging: %v", err)
	}

	if _, err := os.Lstat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("after cleanup, stale path Lstat: %v, want ErrNotExist", err)
	}
}

func TestSandboxStagingWorksTransferManager(t *testing.T) {
	sessionID := []byte("test-sandbox-transfer-005")
	staging, err := CreateSandboxStaging(sessionID)
	if err != nil {
		t.Fatalf("CreateSandboxStaging: %v", err)
	}
	defer staging.Cleanup()

	// A sandbox staging should work with a transfer manager
	manager, err := NewManager(staging)
	if err != nil {
		t.Fatalf("NewManager with sandbox staging: %v", err)
	}
	defer manager.Close()
}

func TestSessionStagingRootNil(t *testing.T) {
	var s *SessionStaging
	if got := s.Root(); got != "" {
		t.Fatalf("nil Root() = %q, want empty", got)
	}
}
