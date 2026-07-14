//go:build windows

package attachment

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStagingDirectoryFailsClosedAfterReplacementWindowsJunction(t *testing.T) {
	directory, staging := newTestStagingDirectory(t)
	outside := t.TempDir()
	movedPath := staging.Path() + "-moved"
	if err := os.Rename(staging.Path(), movedPath); err != nil {
		// Windows denied moving the open os.Root boundary, so the operating
		// system itself prevented replacement before the identity check.
		if err := directory.create(testPartialName); err != nil {
			t.Fatalf("blocked junction replacement left staging unusable: %v", err)
		}
		if _, statErr := os.Lstat(filepath.Join(outside, testPartialName)); !os.IsNotExist(statErr) {
			t.Fatalf("blocked replacement changed the outside directory: %v", statErr)
		}
		return
	}
	output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/j", staging.Path(), outside).CombinedOutput()
	if err != nil {
		_ = os.Rename(movedPath, staging.Path())
		t.Fatalf("replace staging directory with junction: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = os.Remove(staging.Path())
		_ = os.Rename(movedPath, staging.Path())
	})

	if err := directory.create(testPartialName); err == nil {
		t.Fatal("create succeeded after staging path was replaced")
	} else {
		assertErrorDoesNotContainPaths(t, err, staging.Path(), movedPath, outside)
	}
	if _, err := os.Lstat(filepath.Join(outside, testPartialName)); !os.IsNotExist(err) {
		t.Fatalf("operation wrote through replacement junction: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(movedPath, testPartialName)); !os.IsNotExist(err) {
		t.Fatalf("operation wrote through bound moved directory: %v", err)
	}
}
