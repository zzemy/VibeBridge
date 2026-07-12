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

type terminalLaunchRequest struct {
	Command          []string
	WorkingDirectory string
	Environment      []string
}

type terminalLauncher interface {
	Start(terminalLaunchRequest) (terminalLaunch, error)
}

type ptyTerminalLauncher struct{}

func (ptyTerminalLauncher) Start(request terminalLaunchRequest) (terminalLaunch, error) {
	if len(request.Command) == 0 {
		return terminalLaunch{}, errors.New("terminal command must not be empty")
	}

	instance, err := pty.New()
	if err != nil {
		return terminalLaunch{}, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := instance.CommandContext(ctx, request.Command[0], request.Command[1:]...)
	cmd.Dir = request.WorkingDirectory
	if request.Environment == nil {
		cmd.Env = os.Environ()
	} else {
		cmd.Env = append([]string(nil), request.Environment...)
	}
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
