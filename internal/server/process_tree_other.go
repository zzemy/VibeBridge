//go:build !windows && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package server

import (
	"fmt"
	"os"
	"runtime"
)

func newProcessTree(_ *os.Process) (processTree, error) {
	return nil, fmt.Errorf("PTY process-tree cleanup is not supported on %s", runtime.GOOS)
}
