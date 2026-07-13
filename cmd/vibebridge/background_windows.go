//go:build windows

package main

import "golang.org/x/sys/windows"

var (
	getConsoleWindow = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")
	showWindow       = windows.NewLazySystemDLL("user32.dll").NewProc("ShowWindow")
)

func hideBackgroundWindow() {
	window, _, _ := getConsoleWindow.Call()
	if window != 0 {
		_, _, _ = showWindow.Call(window, 0)
	}
}
