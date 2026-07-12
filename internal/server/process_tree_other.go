//go:build !windows

package server

import "os"

type unmanagedProcessTree struct{}

func newProcessTree(_ *os.Process) (processTree, error) {
	return unmanagedProcessTree{}, nil
}

func (unmanagedProcessTree) Close() error {
	return nil
}
