//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package server

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type unixProcessTree struct {
	processGroupID int
}

func newProcessTree(process *os.Process) (processTree, error) {
	if process == nil || process.Pid <= 0 {
		return nil, errors.New("create Unix process tree: process ID is not available")
	}

	// go-pty starts Unix commands in a new session, making the process ID the
	// process-group ID. Killing that group also reaches descendants which keep
	// running after the PTY leader exits.
	return &unixProcessTree{processGroupID: process.Pid}, nil
}

func (tree *unixProcessTree) Close() error {
	if tree == nil || tree.processGroupID <= 0 {
		return nil
	}
	if err := unix.Kill(-tree.processGroupID, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
		return fmt.Errorf("kill Unix PTY process group %d: %w", tree.processGroupID, err)
	}
	return nil
}
