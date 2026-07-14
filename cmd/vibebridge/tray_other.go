//go:build !windows

package main

import "errors"

func agentTraySupported() bool {
	return false
}

func runAgentTray(agentTrayOptions) error {
	return errors.New("system tray is only supported on Windows")
}

func requestAgentTrayQuit() {}
