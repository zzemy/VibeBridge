package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadOrCreateIssuerKeyCreatesWhenMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "issuer.key")

	key, err := loadOrCreateIssuerKey(path, false)
	if err != nil {
		t.Fatalf("loadOrCreateIssuerKey: %v", err)
	}
	if len(key) != 64 {
		t.Fatalf("expected 64-byte key, got %d bytes", len(key))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected key file to exist, got %v", err)
	}
	// Windows does not honour POSIX mode bits, so the perm check
	// is only meaningful on POSIX builds.
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("expected key file mode 0600, got %o", got)
		}
	}
}

func TestLoadOrCreateIssuerKeyReusesExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "issuer.key")

	first, err := loadOrCreateIssuerKey(path, false)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	second, err := loadOrCreateIssuerKey(path, false)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("expected the same key on subsequent reads")
	}
}

func TestLoadOrCreateIssuerKeyRejectsWrongSize(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "issuer.key")
	if err := os.WriteFile(path, []byte("too short"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadOrCreateIssuerKey(path, false); err == nil {
		t.Fatalf("expected an error for a too-short key file")
	}
}

func TestBuildOriginAllowList(t *testing.T) {
	t.Parallel()

	if got := buildOriginAllowList(nil); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}

	values := []string{
		"https://app.example.com",
		"",
		"http://localhost:5173",
	}
	got := buildOriginAllowList(values)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries (empty dropped), got %d (%v)", len(got), got)
	}
	if got[0] != values[0] || got[1] != values[2] {
		t.Fatalf("unexpected origin list: %v", got)
	}
}

func TestResolveIssuerKeyPathDefaultsToHome(t *testing.T) {
	// t.Setenv is incompatible with t.Parallel, so this test does
	// not opt into the parallel runner.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	got, err := resolveIssuerKeyPath("")
	if err != nil {
		t.Fatalf("resolveIssuerKeyPath: %v", err)
	}
	want := filepath.Join(home, ".viberelay", "issuer.key")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestResolveIssuerKeyPathReturnsAbsoluteForExplicitInput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	input := filepath.Join(dir, "my.key")
	got, err := resolveIssuerKeyPath(input)
	if err != nil {
		t.Fatalf("resolveIssuerKeyPath: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute path, got %q", got)
	}
}

func TestIsWildcardAddress(t *testing.T) {
	t.Parallel()

	cases := []struct {
		addr  string
		want  bool
	}{
		{"0.0.0.0:8788", true},
		{":8788", true},
		{"[::]:8788", true},
		{"127.0.0.1:8788", false},
		{"192.168.1.10:8788", false},
	}
	for _, tc := range cases {
		if got := isWildcardAddress(tc.addr); got != tc.want {
			t.Errorf("isWildcardAddress(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}
