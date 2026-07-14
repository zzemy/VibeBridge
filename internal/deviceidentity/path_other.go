//go:build !windows

package deviceidentity

import (
	"os"
	"path/filepath"
)

func DefaultPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "vibebridge", "identity.json"), nil
}
