//go:build windows

package main

import (
	"syscall"

	"golang.org/x/sys/windows"
)

func hiddenWindowProcessAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
}
