//go:build windows

package workspace_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/zzemy/VibeBridge/internal/workspace"
)

func TestRegistryRejectsWindowsJunctionEscape(t *testing.T) {
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
	createJunction(t, link, outside)

	registry, err := workspace.NewRegistry([]workspace.Definition{{ID: "repo", Label: "Repo", Root: root}}, "")
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	if _, err := registry.ResolveDirectory("repo", "escape"); err == nil {
		t.Fatal("junction escape outside the workspace was accepted")
	}
}

func TestRegistryRejectsDuplicateWindowsJunctionRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	alias := filepath.Join(parent, "workspace-alias")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	createJunction(t, alias, root)

	_, err := workspace.NewRegistry([]workspace.Definition{
		{ID: "one", Label: "One", Root: root},
		{ID: "two", Label: "Two", Root: alias},
	}, "")
	if err == nil {
		t.Fatal("junction alias of a registered canonical root was accepted")
	}
}

func TestRegistryKeepsDistinctRootsInWindowsCaseSensitiveDirectory(t *testing.T) {
	parent := t.TempDir()
	output, err := exec.Command(
		"fsutil.exe",
		"file",
		"SetCaseSensitiveInfo",
		parent,
		"enable",
	).CombinedOutput()
	if err != nil {
		t.Skipf("per-directory case sensitivity is unavailable: %v: %s", err, output)
	}

	upperRoot := filepath.Join(parent, "Foo")
	lowerRoot := filepath.Join(parent, "foo")
	if err := os.Mkdir(upperRoot, 0o700); err != nil {
		t.Fatalf("create uppercase workspace: %v", err)
	}
	if err := os.Mkdir(lowerRoot, 0o700); err != nil {
		t.Fatalf("create lowercase workspace: %v", err)
	}

	registry, err := workspace.NewRegistry([]workspace.Definition{
		{ID: "upper", Label: "Upper", Root: upperRoot},
		{ID: "lower", Label: "Lower", Root: lowerRoot},
	}, "")
	if err != nil {
		t.Fatalf("register distinct case-sensitive roots: %v", err)
	}
	definitions := registry.Definitions()
	if len(definitions) != 2 {
		t.Fatalf("got %d workspace definitions, want 2", len(definitions))
	}
	if definitions[0].Root == definitions[1].Root {
		t.Fatalf("case-sensitive roots collapsed to the same canonical path %q", definitions[0].Root)
	}
}

func createJunction(t *testing.T, link string, target string) {
	t.Helper()
	output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/j", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("create test junction: %v: %s", err, output)
	}
}
