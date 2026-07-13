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

func createJunction(t *testing.T, link string, target string) {
	t.Helper()
	output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/j", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("create test junction: %v: %s", err, output)
	}
}
