package workspace

import (
	"path/filepath"
	"testing"
)

func TestCanonicalContainmentPreservesPathCase(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "workspace", "CaseSensitive")
	caseOnlyPeer := filepath.Join(string(filepath.Separator), "workspace", "casesensitive")

	if containsCanonicalPath(root, caseOnlyPeer) {
		t.Fatal("case-only peer was treated as the canonical workspace root")
	}
	if !containsCanonicalPath(root, filepath.Join(root, "child")) {
		t.Fatal("canonical child was rejected")
	}
}
