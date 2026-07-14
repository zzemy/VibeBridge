package attachment

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	testPartialName = "00112233445566778899aabbccddeeff.partial"
	testFinalName   = "00112233445566778899aabbccddeeff.txt"
	testOtherName   = "ffeeddccbbaa99887766554433221100.txt"
)

func TestOpenStagingDirectoryLeavesNoPublicationProbe(t *testing.T) {
	_, staging := newTestStagingDirectory(t)
	entries, err := os.ReadDir(staging.Path())
	if err != nil {
		t.Fatalf("read staging directory after open: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("publication probe left %d staging entries", len(entries))
	}
}

func TestStagingDirectoryCreatesExclusiveFiles(t *testing.T) {
	directory, staging := newTestStagingDirectory(t)

	if err := directory.create(testPartialName); err != nil {
		t.Fatalf("create staged file: %v", err)
	}
	if err := directory.writeAt(testPartialName, 0, []byte("first")); err != nil {
		t.Fatalf("write staged file: %v", err)
	}

	if err := directory.create(testPartialName); err == nil {
		t.Fatal("duplicate staged file creation was accepted")
	} else {
		assertErrorDoesNotContainPaths(t, err, staging.Path())
	}
	content, err := os.ReadFile(filepath.Join(staging.Path(), testPartialName))
	if err != nil {
		t.Fatalf("read staged file: %v", err)
	}
	if string(content) != "first" {
		t.Fatalf("duplicate creation changed staged content: %q", content)
	}
}

func TestStagingDirectoryRenamesWithoutOverwriting(t *testing.T) {
	directory, staging := newTestStagingDirectory(t)
	writeStagedFile(t, directory, testPartialName, "complete")

	if err := directory.rename(testPartialName, testFinalName); err != nil {
		t.Fatalf("rename staged file: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(staging.Path(), testPartialName)); !os.IsNotExist(err) {
		t.Fatalf("partial file remains after rename: %v", err)
	}
	assertFileContent(t, filepath.Join(staging.Path(), testFinalName), "complete")

	writeStagedFile(t, directory, testPartialName, "new")
	writeStagedFile(t, directory, testOtherName, "existing")
	if err := directory.rename(testPartialName, testOtherName); err == nil {
		t.Fatal("rename over an existing final file was accepted")
	}
	assertFileContent(t, filepath.Join(staging.Path(), testPartialName), "new")
	assertFileContent(t, filepath.Join(staging.Path(), testOtherName), "existing")
}

func TestStagingDirectoryPublishDoesNotRaceDestinationCreation(t *testing.T) {
	directory, staging := newTestStagingDirectory(t)

	for iteration := 1; iteration <= 25; iteration++ {
		identifier := fmt.Sprintf("%032x", iteration)
		partialName := identifier + ".partial"
		finalName := identifier + ".txt"
		writeStagedFile(t, directory, partialName, "staged")

		start := make(chan struct{})
		createResult := make(chan error, 1)
		go func() {
			<-start
			file, err := os.OpenFile(filepath.Join(staging.Path(), finalName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err == nil {
				_, writeErr := file.WriteString("concurrent")
				closeErr := file.Close()
				if writeErr != nil {
					err = writeErr
				} else if closeErr != nil {
					err = closeErr
				}
			}
			createResult <- err
		}()
		close(start)
		publishErr := directory.rename(partialName, finalName)
		createErr := <-createResult

		switch {
		case createErr == nil:
			if publishErr == nil {
				t.Fatal("publish replaced a concurrently created destination")
			}
			assertFileContent(t, filepath.Join(staging.Path(), finalName), "concurrent")
			assertFileContent(t, filepath.Join(staging.Path(), partialName), "staged")
		case publishErr == nil:
			assertFileContent(t, filepath.Join(staging.Path(), finalName), "staged")
			if _, err := os.Lstat(filepath.Join(staging.Path(), partialName)); !os.IsNotExist(err) {
				t.Fatalf("published partial remains: %v", err)
			}
		default:
			t.Fatalf("both destination creation and publish failed: create=%v publish=%v", createErr, publishErr)
		}
		if err := directory.remove(partialName); err != nil {
			t.Fatalf("remove partial after race: %v", err)
		}
		if err := directory.remove(finalName); err != nil {
			t.Fatalf("remove final after race: %v", err)
		}
	}
}

func TestStagingDirectoryRemoveIsIdempotent(t *testing.T) {
	directory, staging := newTestStagingDirectory(t)
	writeStagedFile(t, directory, testPartialName, "partial")

	if err := directory.remove(testPartialName); err != nil {
		t.Fatalf("remove staged file: %v", err)
	}
	if err := directory.remove(testPartialName); err != nil {
		t.Fatalf("repeat staged file removal: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(staging.Path(), testPartialName)); !os.IsNotExist(err) {
		t.Fatalf("staged file remains after removal: %v", err)
	}
}

func TestStagingDirectoryRejectsNonGeneratedNames(t *testing.T) {
	directory, _ := newTestStagingDirectory(t)
	invalidNames := []string{
		"",
		".",
		"..",
		"../" + testPartialName,
		"..\\" + testPartialName,
		"child/" + testPartialName,
		"child\\" + testPartialName,
		"00112233445566778899aabbccddeef.partial",
		"00112233445566778899AABBCCDDEEFF.partial",
		"00112233445566778899aabbccddeeff.exe",
		"nul.txt",
		"CON.pdf",
		"文件.txt",
	}

	for _, name := range invalidNames {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			if err := directory.create(name); err == nil {
				t.Fatal("invalid staged filename was accepted by create")
			}
			if err := directory.writeAt(name, 0, []byte("data")); err == nil {
				t.Fatal("invalid staged filename was accepted by write")
			}
			if err := directory.remove(name); err == nil {
				t.Fatal("invalid staged filename was accepted by remove")
			}
			if err := directory.rename(testPartialName, name); err == nil {
				t.Fatal("invalid staged filename was accepted by rename destination")
			}
			if err := directory.rename(name, testFinalName); err == nil {
				t.Fatal("invalid staged filename was accepted by rename source")
			}
		})
	}
}

func TestStagingDirectoryDoesNotFollowReplacementFileSymlink(t *testing.T) {
	directory, staging := newTestStagingDirectory(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatalf("create outside file: %v", err)
	}
	if err := directory.create(testPartialName); err != nil {
		t.Fatalf("create partial file: %v", err)
	}
	if err := os.Remove(filepath.Join(staging.Path(), testPartialName)); err != nil {
		t.Fatalf("remove partial before replacement: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(staging.Path(), testPartialName)); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation is unavailable for this Windows user: %v", err)
		}
		t.Fatalf("replace partial with symlink: %v", err)
	}

	if err := directory.writeAt(testPartialName, 0, []byte("overwrite")); err == nil {
		t.Fatal("write followed a replacement file symlink")
	} else {
		assertErrorDoesNotContainPaths(t, err, staging.Path(), outside)
	}
	if err := directory.rename(testPartialName, testFinalName); err == nil {
		t.Fatal("rename published a replacement file symlink")
	} else {
		assertErrorDoesNotContainPaths(t, err, staging.Path(), outside)
	}
	if err := directory.remove(testPartialName); err != nil {
		t.Fatalf("remove replacement file symlink: %v", err)
	}
	assertFileContent(t, outside, "keep")
}

func TestStagingDirectoryFailsClosedAfterStagingDirectoryMoves(t *testing.T) {
	directory, staging := newTestStagingDirectory(t)
	if err := directory.create(testPartialName); err != nil {
		t.Fatalf("create partial before move: %v", err)
	}
	movedPath := staging.Path() + "-moved"
	if err := os.Rename(staging.Path(), movedPath); err != nil {
		if runtime.GOOS == "windows" {
			if err := directory.create(testOtherName); err != nil {
				t.Fatalf("blocked move left staging unusable: %v", err)
			}
			return // The open os.Root handle prevents the boundary from moving.
		}
		t.Fatalf("move staging directory: %v", err)
	}
	t.Cleanup(func() {
		if _, err := os.Lstat(staging.Path()); os.IsNotExist(err) {
			_ = os.Rename(movedPath, staging.Path())
		}
	})

	if err := directory.create(testOtherName); err == nil {
		t.Fatal("create succeeded after staging directory moved")
	} else {
		assertErrorDoesNotContainPaths(t, err, staging.Path(), movedPath)
	}
	if err := directory.writeAt(testPartialName, 0, []byte("escaped")); err == nil {
		t.Fatal("write succeeded after staging directory moved")
	} else {
		assertErrorDoesNotContainPaths(t, err, staging.Path(), movedPath)
	}
	assertFileContent(t, filepath.Join(movedPath, testPartialName), "")
	if _, err := os.Lstat(filepath.Join(movedPath, testOtherName)); !os.IsNotExist(err) {
		t.Fatalf("operation created a file in the moved staging directory: %v", err)
	}
}

func TestStagingDirectoryFailsClosedAfterReplacementSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows replacement junction coverage is in fileops_windows_test.go")
	}
	directory, staging := newTestStagingDirectory(t)
	outside := t.TempDir()
	movedPath := staging.Path() + "-moved"
	if err := os.Rename(staging.Path(), movedPath); err != nil {
		t.Fatalf("move staging directory: %v", err)
	}
	if err := os.Symlink(outside, staging.Path()); err != nil {
		t.Fatalf("replace staging directory with symlink: %v", err)
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
		t.Fatalf("operation wrote through replacement symlink: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(movedPath, testPartialName)); !os.IsNotExist(err) {
		t.Fatalf("operation wrote through bound moved directory: %v", err)
	}
}

func TestStagingDirectoryFailsClosedAfterWorkspaceMoves(t *testing.T) {
	parent := t.TempDir()
	workspaceRoot := filepath.Join(parent, "workspace")
	if err := os.Mkdir(workspaceRoot, 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	staging, err := CreateSessionStaging(workspaceRoot, []byte("file-operation-session"))
	if err != nil {
		t.Fatalf("create session staging: %v", err)
	}
	directory, err := openStagingDirectory(staging)
	if err != nil {
		t.Fatalf("open staging directory: %v", err)
	}
	t.Cleanup(func() { _ = directory.close() })

	movedWorkspace := filepath.Join(parent, "moved-workspace")
	if err := os.Rename(workspaceRoot, movedWorkspace); err != nil {
		if runtime.GOOS == "windows" {
			if err := directory.create(testPartialName); err != nil {
				t.Fatalf("blocked workspace move left staging unusable: %v", err)
			}
			return // The nested open os.Root handle prevents the workspace from moving.
		}
		t.Fatalf("move workspace: %v", err)
	}
	t.Cleanup(func() {
		if _, err := os.Lstat(workspaceRoot); os.IsNotExist(err) {
			_ = os.Rename(movedWorkspace, workspaceRoot)
		}
	})

	if err := directory.create(testPartialName); err == nil {
		t.Fatal("create succeeded after workspace moved")
	} else {
		assertErrorDoesNotContainPaths(t, err, workspaceRoot, movedWorkspace, staging.Path())
	}
	movedStagingPath := filepath.Join(movedWorkspace, ".vibebridge", "uploads", filepath.Base(staging.Path()))
	if _, err := os.Lstat(filepath.Join(movedStagingPath, testPartialName)); !os.IsNotExist(err) {
		t.Fatalf("operation wrote into moved workspace: %v", err)
	}
}

func TestStagingDirectoryCloseIsIdempotentAndBlocksOperations(t *testing.T) {
	directory, _ := newTestStagingDirectory(t)
	if err := directory.close(); err != nil {
		t.Fatalf("close staging directory: %v", err)
	}
	if err := directory.close(); err != nil {
		t.Fatalf("repeat staging directory close: %v", err)
	}
	if err := directory.create(testPartialName); err == nil {
		t.Fatal("create succeeded after staging directory close")
	}
}

func newTestStagingDirectory(t *testing.T) (*stagingDirectory, *SessionStaging) {
	t.Helper()
	staging, err := CreateSessionStaging(t.TempDir(), []byte("file-operation-session"))
	if err != nil {
		t.Fatalf("create session staging: %v", err)
	}
	directory, err := openStagingDirectory(staging)
	if err != nil {
		t.Fatalf("open staging directory: %v", err)
	}
	t.Cleanup(func() { _ = directory.close() })
	return directory, staging
}

func writeStagedFile(t *testing.T, directory *stagingDirectory, name string, content string) {
	t.Helper()
	if err := directory.create(name); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	if err := directory.writeAt(name, 0, []byte(content)); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func assertFileContent(t *testing.T, path string, expected string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read staged file: %v", err)
	}
	if string(content) != expected {
		t.Fatalf("staged content = %q, want %q", content, expected)
	}
}

func assertErrorDoesNotContainPaths(t *testing.T, err error, paths ...string) {
	t.Helper()
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		t.Fatalf("error chain exposed os.PathError path %q", pathError.Path)
	}
	for current := err; current != nil; current = errors.Unwrap(current) {
		for _, path := range paths {
			if path != "" && strings.Contains(current.Error(), path) {
				t.Fatalf("error chain leaked local path %q: %v", path, current)
			}
		}
	}
}

func TestSessionStagingCleanupWaitsForOpenDirectory(t *testing.T) {
	staging, err := CreateSessionStaging(t.TempDir(), []byte("open-directory-session"))
	if err != nil {
		t.Fatalf("create session staging: %v", err)
	}
	directory, err := openStagingDirectory(staging)
	if err != nil {
		t.Fatalf("open staging directory: %v", err)
	}

	if err := staging.Cleanup(); err == nil {
		t.Fatal("cleanup succeeded while the safe directory was open")
	}
	if _, err := os.Stat(staging.Path()); err != nil {
		t.Fatalf("failed cleanup removed staging directory: %v", err)
	}
	if err := directory.close(); err != nil {
		t.Fatalf("close staging directory: %v", err)
	}
	if err := staging.Cleanup(); err != nil {
		t.Fatalf("retry cleanup after close: %v", err)
	}
}
