//go:build windows

package attachment_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzemy/VibeBridge/internal/attachment"
)

func TestCreateSessionStagingRejectsWindowsJunctionMetadataDirectory(t *testing.T) {
	workspaceRoot := canonicalTestDirectory(t, t.TempDir())
	outside := t.TempDir()
	metadataDirectory := filepath.Join(workspaceRoot, ".vibebridge")
	output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/j", metadataDirectory, outside).CombinedOutput()
	if err != nil {
		t.Fatalf("create metadata junction: %v: %s", err, output)
	}

	_, err = attachment.CreateSessionStaging(workspaceRoot, []byte("junction-session"))
	if err == nil {
		t.Fatal("junction metadata directory was accepted")
	}
	if strings.Contains(err.Error(), workspaceRoot) || strings.Contains(err.Error(), outside) {
		t.Fatalf("staging error leaked a local path: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "uploads")); !os.IsNotExist(err) {
		t.Fatalf("staging creation wrote through metadata junction: %v", err)
	}
}

func TestSessionStagingCleanupDoesNotFollowReplacementWindowsJunction(t *testing.T) {
	workspaceRoot := canonicalTestDirectory(t, t.TempDir())
	outside := t.TempDir()
	marker := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatalf("create outside marker: %v", err)
	}
	staging, err := attachment.CreateSessionStaging(workspaceRoot, []byte("junction-cleanup"))
	if err != nil {
		t.Fatalf("create session staging: %v", err)
	}
	if err := os.Remove(staging.Path()); err != nil {
		t.Fatalf("remove empty staging directory: %v", err)
	}
	output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/j", staging.Path(), outside).CombinedOutput()
	if err != nil {
		t.Fatalf("replace staging with junction: %v: %s", err, output)
	}

	if err := staging.Cleanup(); err != nil {
		t.Fatalf("cleanup replacement junction: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("cleanup followed replacement junction: %v", err)
	}
	if _, err := os.Lstat(staging.Path()); !os.IsNotExist(err) {
		t.Fatalf("replacement junction remains after cleanup: %v", err)
	}
}
