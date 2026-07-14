//go:build windows

package deviceidentity

import (
	"errors"
	"os"
	"path/filepath"
)

func DefaultPath() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return "", errors.New("LOCALAPPDATA is not set")
	}
	return filepath.Join(base, "VibeBridge", "identity.json"), nil
}
