//go:build !windows

package securestore

import (
	"fmt"
	"os"
)

func protectionName() string { return "file-permissions" }

func protect(plaintext []byte, _ string) ([]byte, error) {
	return append([]byte(nil), plaintext...), nil
}

func unprotect(ciphertext []byte, _ string) ([]byte, error) {
	return append([]byte(nil), ciphertext...), nil
}

func replaceFile(source string, destination string) error {
	return os.Rename(source, destination)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open secure-store directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("flush secure-store directory: %w", err)
	}
	return nil
}
