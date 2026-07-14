package workspace_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zzemy/VibeBridge/internal/workspace"
)

func TestRegistryCanonicalizesRelativeRootsAndResolvesDirectories(t *testing.T) {
	configDirectory := t.TempDir()
	root := filepath.Join(configDirectory, "项目")
	workingDirectory := filepath.Join(root, "src")
	if err := os.MkdirAll(workingDirectory, 0o700); err != nil {
		t.Fatalf("create workspace directories: %v", err)
	}

	registry, err := workspace.NewRegistry([]workspace.Definition{{
		ID:    "main",
		Label: "  主项目  ",
		Root:  "项目",
	}}, configDirectory)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}

	definition, ok := registry.Lookup("main")
	if !ok {
		t.Fatal("registered workspace was not found")
	}
	wantRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonicalize expected root: %v", err)
	}
	wantRoot, err = filepath.Abs(wantRoot)
	if err != nil {
		t.Fatalf("make expected root absolute: %v", err)
	}
	if definition.Root != filepath.Clean(wantRoot) {
		t.Fatalf("workspace root = %q, want %q", definition.Root, filepath.Clean(wantRoot))
	}
	if definition.Label != "主项目" {
		t.Fatalf("workspace label = %q, want trimmed Unicode label", definition.Label)
	}

	resolved, err := registry.ResolveDirectory("main", "src")
	if err != nil {
		t.Fatalf("resolve workspace directory: %v", err)
	}
	wantWorkingDirectory, err := filepath.EvalSymlinks(workingDirectory)
	if err != nil {
		t.Fatalf("canonicalize expected working directory: %v", err)
	}
	if resolved != filepath.Clean(wantWorkingDirectory) {
		t.Fatalf("resolved directory = %q, want %q", resolved, filepath.Clean(wantWorkingDirectory))
	}

	resolved, err = registry.ResolveDirectory("main", "")
	if err != nil {
		t.Fatalf("resolve default workspace directory: %v", err)
	}
	if resolved != definition.Root {
		t.Fatalf("default directory = %q, want workspace root %q", resolved, definition.Root)
	}
}

func TestRegistryRejectsInvalidDefinitions(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("write regular file: %v", err)
	}

	tests := map[string][]workspace.Definition{
		"invalid id":     {{ID: "Bad ID", Label: "Repo", Root: root}},
		"empty label":    {{ID: "repo", Label: "  ", Root: root}},
		"empty root":     {{ID: "repo", Label: "Repo"}},
		"missing root":   {{ID: "repo", Label: "Repo", Root: filepath.Join(root, "missing")}},
		"non-directory":  {{ID: "repo", Label: "Repo", Root: file}},
		"duplicate id":   {{ID: "repo", Label: "Repo", Root: root}, {ID: "repo", Label: "Other", Root: t.TempDir()}},
		"duplicate root": {{ID: "repo", Label: "Repo", Root: root}, {ID: "other", Label: "Other", Root: filepath.Join(root, ".")}},
	}

	for name, definitions := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := workspace.NewRegistry(definitions, ""); err == nil {
				t.Fatal("invalid workspace definitions were accepted")
			}
		})
	}
}

func TestRegistryPathErrorsDoNotExposeLocalPaths(t *testing.T) {
	secretRoot := filepath.Join(t.TempDir(), "private-workspace-root")
	_, err := workspace.NewRegistry([]workspace.Definition{{ID: "repo", Label: "Repo", Root: secretRoot}}, "")
	if err == nil {
		t.Fatal("missing workspace root was accepted")
	}
	if strings.Contains(err.Error(), secretRoot) || strings.Contains(err.Error(), "private-workspace-root") {
		t.Fatalf("workspace root leaked in error: %v", err)
	}

	root := t.TempDir()
	registry, err := workspace.NewRegistry([]workspace.Definition{{ID: "repo", Label: "Repo", Root: root}}, "")
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	secretCandidate := filepath.Join(root, "private-working-directory")
	_, err = registry.ResolveDirectory("repo", secretCandidate)
	if err == nil {
		t.Fatal("missing workspace directory was accepted")
	}
	if strings.Contains(err.Error(), secretCandidate) || strings.Contains(err.Error(), "private-working-directory") {
		t.Fatalf("workspace candidate leaked in error: %v", err)
	}
}

func TestRegistryRejectsTraversalOutsideWorkspace(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatalf("create outside directory: %v", err)
	}

	registry, err := workspace.NewRegistry([]workspace.Definition{{ID: "repo", Label: "Repo", Root: root}}, "")
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	if _, err := registry.ResolveDirectory("repo", filepath.Join("..", "outside")); err == nil {
		t.Fatal("parent traversal outside the workspace was accepted")
	}
	if _, err := registry.ResolveDirectory("repo", outside); err == nil {
		t.Fatal("absolute directory outside the workspace was accepted")
	}
}

func TestRegistryRejectsSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatalf("create outside directory: %v", err)
	}
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation is unavailable for this Windows user: %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}

	registry, err := workspace.NewRegistry([]workspace.Definition{{ID: "repo", Label: "Repo", Root: root}}, "")
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	if _, err := registry.ResolveDirectory("repo", "escape"); err == nil {
		t.Fatal("symlink escape outside the workspace was accepted")
	}
}

func TestRegistryTreatsWindowsRootCaseAsEquivalent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path comparison policy")
	}
	root := t.TempDir()
	alternateCase := strings.ToUpper(root)
	_, err := workspace.NewRegistry([]workspace.Definition{
		{ID: "one", Label: "One", Root: root},
		{ID: "two", Label: "Two", Root: alternateCase},
	}, "")
	if err == nil {
		t.Fatal("case-only duplicate Windows roots were accepted")
	}
}
