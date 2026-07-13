package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zzemy/VibeBridge/internal/workspace"
)

func TestWorkspaceSessionRevalidatesWorkingDirectoryBeforePTYStart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	workingDirectory := filepath.Join(root, "src")
	if err := os.MkdirAll(workingDirectory, 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	canonicalRoot, canonicalWorkingDirectory := validatedWorkspacePaths(t, root, "src")

	wait := make(chan struct{})
	launcher := &fakeTerminalLauncher{launch: terminalLaunch{
		terminal:    &countingPTY{},
		processTree: &countingProcessTree{},
		cancel:      func() {},
		waiter:      blockingWaiter{done: wait},
	}}
	server := New(Config{
		Command:          []string{"fake"},
		WorkspaceRoot:    canonicalRoot,
		WorkingDirectory: canonicalWorkingDirectory,
	})
	server.launcher = launcher

	session, created, err := server.getOrCreateSession()
	if err != nil || !created {
		t.Fatalf("create workspace session = %t/%v", created, err)
	}
	if launcher.calls.Load() != 1 {
		t.Fatalf("PTY launcher calls = %d, want 1", launcher.calls.Load())
	}
	if launcher.request.WorkingDirectory != canonicalWorkingDirectory {
		t.Fatalf("PTY working directory = %q, want %q", launcher.request.WorkingDirectory, canonicalWorkingDirectory)
	}

	session.terminateWithReason("test cleanup")
	close(wait)
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("workspace PTY did not end")
	}
}

func TestWorkspaceSessionRejectsReplacedPathsBeforePTYStart(t *testing.T) {
	tests := map[string]func(t *testing.T, root string, workingDirectory string, outside string){
		"root replaced": func(t *testing.T, root string, workingDirectory string, outside string) {
			if err := os.Remove(workingDirectory); err != nil {
				t.Fatalf("remove working directory before root replacement: %v", err)
			}
			replaceDirectoryWithLink(t, root, outside)
		},
		"working directory replaced": func(t *testing.T, _ string, workingDirectory string, outside string) {
			replaceDirectoryWithLink(t, workingDirectory, outside)
		},
	}

	for name, replace := range tests {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "private-workspace")
			workingDirectory := filepath.Join(root, "src")
			outside := filepath.Join(parent, "outside")
			if err := os.MkdirAll(workingDirectory, 0o700); err != nil {
				t.Fatalf("create workspace: %v", err)
			}
			if err := os.MkdirAll(filepath.Join(outside, "src"), 0o700); err != nil {
				t.Fatalf("create outside directories: %v", err)
			}
			canonicalRoot, canonicalWorkingDirectory := validatedWorkspacePaths(t, root, "src")
			replace(t, root, workingDirectory, outside)

			launcher := &fakeTerminalLauncher{}
			server := New(Config{
				Command:          []string{"fake"},
				WorkspaceRoot:    canonicalRoot,
				WorkingDirectory: canonicalWorkingDirectory,
			})
			server.launcher = launcher

			session, created, err := server.getOrCreateSession()
			if err == nil || session != nil || created {
				t.Fatalf("replaced workspace path created session = %v/%t/%v", session, created, err)
			}
			if launcher.calls.Load() != 0 {
				t.Fatalf("PTY launcher calls = %d, want 0", launcher.calls.Load())
			}
			if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), workingDirectory) || strings.Contains(err.Error(), outside) {
				t.Fatalf("workspace path leaked in session error: %v", err)
			}
		})
	}
}

func validatedWorkspacePaths(t *testing.T, root string, relativeWorkingDirectory string) (string, string) {
	t.Helper()
	registry, err := workspace.NewRegistry([]workspace.Definition{{ID: "repo", Label: "Repo", Root: root}}, "")
	if err != nil {
		t.Fatalf("create workspace registry: %v", err)
	}
	definition, ok := registry.Lookup("repo")
	if !ok {
		t.Fatal("workspace was not found")
	}
	workingDirectory, err := registry.ResolveDirectory("repo", relativeWorkingDirectory)
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	return definition.Root, workingDirectory
}

func replaceDirectoryWithLink(t *testing.T, path string, target string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove directory before replacement: %v", err)
	}
	if runtime.GOOS == "windows" {
		output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/j", path, target).CombinedOutput()
		if err != nil {
			t.Fatalf("create replacement junction: %v: %s", err, output)
		}
		return
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("create replacement symlink: %v", err)
	}
}
