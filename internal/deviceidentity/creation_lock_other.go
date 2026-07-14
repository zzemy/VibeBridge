//go:build !windows

package deviceidentity

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func acquireCreationLock(identityPath string) (func() error, error) {
	if err := os.MkdirAll(filepath.Dir(identityPath), 0o700); err != nil {
		return nil, fmt.Errorf("create identity directory: %w", err)
	}
	file, err := os.OpenFile(identityPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open identity creation lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("acquire identity creation lock: %w", err)
	}
	return func() error {
		unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN)
		closeErr := file.Close()
		if unlockErr != nil {
			return fmt.Errorf("release identity creation lock: %w", unlockErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close identity creation lock: %w", closeErr)
		}
		return nil
	}, nil
}
