package server

import (
	"context"
	"errors"
	"io"
	"os"

	pty "github.com/aymanbagabas/go-pty"
)

type terminal interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}

type commandWaiter interface {
	Wait() error
}

type processTree interface {
	Close() error
}

type terminalLaunch struct {
	terminal    terminal
	processTree processTree
	cancel      context.CancelFunc
	waiter      commandWaiter
}

type terminalLauncher interface {
	Start(command []string) (terminalLaunch, error)
}

type ptyTerminalLauncher struct{}

func (ptyTerminalLauncher) Start(command []string) (terminalLaunch, error) {
	if len(command) == 0 {
		return terminalLaunch{}, errors.New("terminal command must not be empty")
	}

	instance, err := pty.New()
	if err != nil {
		return terminalLaunch{}, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := instance.CommandContext(ctx, command[0], command[1:]...)
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		cancel()
		_ = instance.Close()
		return terminalLaunch{}, err
	}

	processTree, err := newProcessTree(cmd.Process)
	if err != nil {
		cancel()
		_ = instance.Close()
		_ = cmd.Wait()
		return terminalLaunch{}, err
	}

	return terminalLaunch{
		terminal:    instance,
		processTree: processTree,
		cancel:      cancel,
		waiter:      cmd,
	}, nil
}
