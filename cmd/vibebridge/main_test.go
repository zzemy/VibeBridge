package main

import "testing"

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
