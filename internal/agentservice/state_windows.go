//go:build windows

package agentservice

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/windows"
)

func DefaultRuntimeStatePath() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return "", errors.New("LOCALAPPDATA is not set")
	}
	return filepath.Join(base, "VibeBridge", "runtime.json"), nil
}

func replaceFile(source string, destination string) error {
	return windows.MoveFileEx(
		windows.StringToUTF16Ptr(source),
		windows.StringToUTF16Ptr(destination),
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}

func withRuntimeStateLock(path string, action func() error) (returnErr error) {
	canonicalPath := strings.ToLower(filepath.Clean(path))
	digest := sha256.Sum256([]byte(canonicalPath))
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`Local\VibeBridgeRuntimeState-%x`, digest))
	if err != nil {
		return fmt.Errorf("build runtime state lock name: %w", err)
	}

	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return fmt.Errorf("open runtime state lock: %w", err)
	}
	if handle == 0 {
		return errors.New("open runtime state lock: Windows returned an invalid handle")
	}
	defer windows.CloseHandle(handle)

	// Windows mutex ownership follows the OS thread, not the goroutine.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	waitResult, err := windows.WaitForSingleObject(handle, windows.INFINITE)
	if err != nil {
		return fmt.Errorf("wait for runtime state lock: %w", err)
	}
	if waitResult != windows.WAIT_OBJECT_0 && waitResult != windows.WAIT_ABANDONED {
		return fmt.Errorf("wait for runtime state lock: unexpected result %#x", waitResult)
	}
	defer func() {
		if err := windows.ReleaseMutex(handle); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("release runtime state lock: %w", err)
		}
	}()

	return action()
}
