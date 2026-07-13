package attachment_test

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zzemy/VibeBridge/internal/attachment"
)

func TestCreateSessionStagingUsesOpaqueSessionDirectory(t *testing.T) {
	workspaceRoot := t.TempDir()
	sessionID := []byte{0x00, 0x11, 0x7f, 0x80, 0xff}

	staging, err := attachment.CreateSessionStaging(workspaceRoot, sessionID)
	if err != nil {
		t.Fatalf("create session staging: %v", err)
	}
	want := filepath.Join(workspaceRoot, ".vibebridge", "uploads", hex.EncodeToString(sessionID))
	if staging.Path() != want {
		t.Fatalf("staging path = %q, want %q", staging.Path(), want)
	}
	info, err := os.Stat(staging.Path())
	if err != nil {
		t.Fatalf("inspect staging directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("staging path is not a directory")
	}
}

func TestCreateSessionStagingRejectsDuplicateSessionDirectory(t *testing.T) {
	workspaceRoot := t.TempDir()
	sessionID := []byte("unique-session")
	first, err := attachment.CreateSessionStaging(workspaceRoot, sessionID)
	if err != nil {
		t.Fatalf("create first session staging: %v", err)
	}

	if _, err := attachment.CreateSessionStaging(workspaceRoot, sessionID); err == nil {
		t.Fatal("duplicate session staging directory was accepted")
	}
	if _, err := os.Stat(first.Path()); err != nil {
		t.Fatalf("duplicate attempt changed first staging directory: %v", err)
	}
}

func TestSessionStagingCleanupRemovesContentsAndIsIdempotent(t *testing.T) {
	staging, err := attachment.CreateSessionStaging(t.TempDir(), []byte("cleanup-session"))
	if err != nil {
		t.Fatalf("create session staging: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging.Path(), "partial.upload"), []byte("partial"), 0o600); err != nil {
		t.Fatalf("create staged file: %v", err)
	}

	if err := staging.Cleanup(); err != nil {
		t.Fatalf("first cleanup: %v", err)
	}
	if _, err := os.Lstat(staging.Path()); !os.IsNotExist(err) {
		t.Fatalf("staging directory remains after cleanup: %v", err)
	}
	if err := staging.Cleanup(); err != nil {
		t.Fatalf("repeated cleanup: %v", err)
	}
}

func TestCreateSessionStagingRejectsSymlinkedMetadataDirectory(t *testing.T) {
	workspaceRoot := t.TempDir()
	outside := t.TempDir()
	metadataDirectory := filepath.Join(workspaceRoot, ".vibebridge")
	if err := os.Symlink(outside, metadataDirectory); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation is unavailable for this Windows user: %v", err)
		}
		t.Fatalf("create metadata symlink: %v", err)
	}

	_, err := attachment.CreateSessionStaging(workspaceRoot, []byte("link-session"))
	if err == nil {
		t.Fatal("symlinked metadata directory was accepted")
	}
	if strings.Contains(err.Error(), workspaceRoot) || strings.Contains(err.Error(), outside) {
		t.Fatalf("staging error leaked a local path: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "uploads")); !os.IsNotExist(err) {
		t.Fatalf("staging creation wrote through metadata symlink: %v", err)
	}
}

func TestSessionStagingCleanupDoesNotFollowReplacementSymlink(t *testing.T) {
	workspaceRoot := t.TempDir()
	outside := t.TempDir()
	marker := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatalf("create outside marker: %v", err)
	}
	staging, err := attachment.CreateSessionStaging(workspaceRoot, []byte("replacement-session"))
	if err != nil {
		t.Fatalf("create session staging: %v", err)
	}
	if err := os.Remove(staging.Path()); err != nil {
		t.Fatalf("remove empty staging directory: %v", err)
	}
	if err := os.Symlink(outside, staging.Path()); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation is unavailable for this Windows user: %v", err)
		}
		t.Fatalf("replace staging with symlink: %v", err)
	}

	if err := staging.Cleanup(); err != nil {
		t.Fatalf("cleanup replacement symlink: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("cleanup followed replacement symlink: %v", err)
	}
	if _, err := os.Lstat(staging.Path()); !os.IsNotExist(err) {
		t.Fatalf("replacement symlink remains after cleanup: %v", err)
	}
}

func TestSessionStagingCleanupCanRetryAfterWorkspaceIsRestored(t *testing.T) {
	parent := t.TempDir()
	workspaceRoot := filepath.Join(parent, "workspace")
	if err := os.Mkdir(workspaceRoot, 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	staging, err := attachment.CreateSessionStaging(workspaceRoot, []byte("retry-session"))
	if err != nil {
		t.Fatalf("create session staging: %v", err)
	}
	marker := filepath.Join(staging.Path(), "partial.upload")
	if err := os.WriteFile(marker, []byte("partial"), 0o600); err != nil {
		t.Fatalf("create staged file: %v", err)
	}

	movedWorkspace := filepath.Join(parent, "moved-workspace")
	if err := os.Rename(workspaceRoot, movedWorkspace); err != nil {
		t.Fatalf("move workspace before cleanup: %v", err)
	}
	if err := staging.Cleanup(); err == nil {
		t.Fatal("cleanup reported success while the workspace was moved")
	} else if strings.Contains(err.Error(), workspaceRoot) || strings.Contains(err.Error(), movedWorkspace) {
		t.Fatalf("cleanup error leaked a local path: %v", err)
	}
	if err := os.Rename(movedWorkspace, workspaceRoot); err != nil {
		t.Fatalf("restore workspace for cleanup retry: %v", err)
	}

	if err := staging.Cleanup(); err != nil {
		t.Fatalf("retry cleanup after workspace restore: %v", err)
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("retry left staged content behind: %v", err)
	}
}
