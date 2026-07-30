package attachment_test

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/zzemy/VibeBridge/internal/attachment"
)

func TestCleanupStaleStagingRemovesOrphanedDirectories(t *testing.T) {
	workspaceRoot := canonicalTestDirectory(t, t.TempDir())

	sessionIDs := [][]byte{
		[]byte("stale-session-1"),
		[]byte("stale-session-2"),
		[]byte{0x00, 0x11, 0x7f, 0x80, 0xff},
	}

	for _, id := range sessionIDs {
		staging, err := attachment.CreateSessionStaging(workspaceRoot, id)
		if err != nil {
			t.Fatalf("create session staging for %x: %v", id, err)
		}
		if err := staging.Cleanup(); err != nil {
			t.Fatalf("cleanup staging for %x: %v", id, err)
		}
	}

	uploadsDir := filepath.Join(workspaceRoot, ".vibebridge", "uploads")
	entries, err := os.ReadDir(uploadsDir)
	if err != nil {
		t.Fatalf("read uploads dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after cleanup, got %d", len(entries))
	}
}

func TestCleanupStaleStagingRemovesDirectoriesWithContent(t *testing.T) {
	workspaceRoot := canonicalTestDirectory(t, t.TempDir())

	sessionID := []byte("crashed-session")
	staging, err := attachment.CreateSessionStaging(workspaceRoot, sessionID)
	if err != nil {
		t.Fatalf("create session staging: %v", err)
	}

	dummyFile := filepath.Join(staging.Path(), "data.txt")
	if err := os.WriteFile(dummyFile, []byte("orphaned content"), 0o600); err != nil {
		t.Fatalf("write dummy file: %v", err)
	}

	if err := attachment.CleanupStaleStaging(workspaceRoot); err != nil {
		t.Fatalf("cleanup stale staging: %v", err)
	}

	if _, err := os.Stat(staging.Path()); !os.IsNotExist(err) {
		t.Fatalf("staging directory still exists after cleanup: %v", err)
	}
}

func TestCleanupStaleStagingNoOpWhenNoUploadsDir(t *testing.T) {
	workspaceRoot := canonicalTestDirectory(t, t.TempDir())

	if err := attachment.CleanupStaleStaging(workspaceRoot); err != nil {
		t.Fatalf("cleanup on empty workspace: %v", err)
	}
}

func TestCleanupStaleStagingIdempotent(t *testing.T) {
	workspaceRoot := canonicalTestDirectory(t, t.TempDir())

	sessionID := []byte("session-a")
	_, err := attachment.CreateSessionStaging(workspaceRoot, sessionID)
	if err != nil {
		t.Fatalf("create session staging: %v", err)
	}

	if err := attachment.CleanupStaleStaging(workspaceRoot); err != nil {
		t.Fatalf("first cleanup: %v", err)
	}
	if err := attachment.CleanupStaleStaging(workspaceRoot); err != nil {
		t.Fatalf("second cleanup: %v", err)
	}

	hexID := hex.EncodeToString(sessionID)
	stagingPath := filepath.Join(workspaceRoot, ".vibebridge", "uploads", hexID)
	if _, err := os.Stat(stagingPath); !os.IsNotExist(err) {
		t.Fatalf("staging directory still exists after double cleanup")
	}
}

func TestCleanupStaleStagingRemovesNonDirectoryEntries(t *testing.T) {
	workspaceRoot := canonicalTestDirectory(t, t.TempDir())

	uploadsDir := filepath.Join(workspaceRoot, ".vibebridge", "uploads")
	if err := os.MkdirAll(uploadsDir, 0o700); err != nil {
		t.Fatalf("create uploads dir: %v", err)
	}

	rogueFile := filepath.Join(uploadsDir, "rogue-file.txt")
	if err := os.WriteFile(rogueFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write rogue file: %v", err)
	}

	if err := attachment.CleanupStaleStaging(workspaceRoot); err != nil {
		t.Fatalf("cleanup with rogue file: %v", err)
	}

	if _, err := os.Stat(rogueFile); !os.IsNotExist(err) {
		t.Fatalf("rogue file still exists after cleanup")
	}
}
