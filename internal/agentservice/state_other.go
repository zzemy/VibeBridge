//go:build !windows

package agentservice

import (
	"os"
	"path/filepath"
	"sync"
)

var runtimeStateLock sync.Mutex

func DefaultRuntimeStatePath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "vibebridge", "runtime.json"), nil
}

func replaceFile(source string, destination string) error {
	return os.Rename(source, destination)
}

func withRuntimeStateLock(_ string, action func() error) error {
	runtimeStateLock.Lock()
	defer runtimeStateLock.Unlock()
	return action()
}
