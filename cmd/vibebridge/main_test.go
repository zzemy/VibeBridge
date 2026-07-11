package main

import (
	"os"
	"testing"
)

func TestIsWildcardAddress(t *testing.T) {
	cases := []struct {
		address string
		want    bool
	}{
		{address: "0.0.0.0:8787", want: true},
		{address: "[::]:8787", want: true},
		{address: "127.0.0.1:8787", want: false},
		{address: "not-an-address", want: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.address, func(t *testing.T) {
			if got := isWildcardAddress(testCase.address); got != testCase.want {
				t.Fatalf("isWildcardAddress(%q) = %t, want %t", testCase.address, got, testCase.want)
			}
		})
	}
}

func TestValidateCommand(t *testing.T) {
	if err := validateCommand([]string{os.Args[0]}); err != nil {
		t.Fatalf("validate current executable: %v", err)
	}
	if err := validateCommand([]string{"vibebridge-command-that-does-not-exist"}); err == nil {
		t.Fatal("missing command passed validation")
	}
}

func TestRunDiagnosticsWithEphemeralPort(t *testing.T) {
	if err := runDiagnostics("127.0.0.1:0", t.TempDir(), false); err != nil {
		t.Fatalf("run diagnostics: %v", err)
	}
}
