//go:build windows

package main

import (
	"context"
	_ "embed"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"fyne.io/systray"
	"golang.org/x/sys/windows"
)

//go:embed assets/tray.ico
var trayIcon []byte

const messageBoxYes = 6

var trayQuitOnce sync.Once

func agentTraySupported() bool {
	return true
}

func runAgentTray(options agentTrayOptions) error {
	if err := options.validate(); err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTooltip("VibeBridge · Local Agent")

		statusItem := systray.AddMenuItem("Agent online", "Current Local Agent status")
		statusItem.Disable()
		systray.AddSeparator()
		openItem := systray.AddMenuItem("Open VibeBridge", "Open the local terminal workspace")
		pairItem := systray.AddMenuItem("Pair a phone…", "Show the local pairing QR code")
		systray.AddSeparator()
		transportItem := systray.AddMenuItem("Remote access · local only", "Relay access is not configured yet")
		transportItem.Disable()
		systray.AddSeparator()
		exitItem := systray.AddMenuItem("Exit Agent…", "End active sessions and stop VibeBridge")

		systray.SetOnTapped(func() {
			_ = openTrayURL(options.AppURL)
		})

		go func() {
			ticker := time.NewTicker(options.StatusPeriod)
			defer ticker.Stop()
			refresh := func() {
				ctx, cancel := context.WithTimeout(context.Background(), trayStatusTimeout)
				status, err := queryTrayStatus(ctx, options.StatusURL)
				cancel()
				if err != nil {
					statusItem.SetTitle("Agent unavailable")
					return
				}
				statusItem.SetTitle(status)
			}
			refresh()
			for {
				select {
				case <-ticker.C:
					refresh()
				case <-openItem.ClickedCh:
					_ = openTrayURL(options.AppURL)
				case <-pairItem.ClickedCh:
					_ = openTrayURL(options.PairingURL)
				case <-exitItem.ClickedCh:
					if confirmAgentExit() {
						options.RequestStop()
					}
				}
			}
		}()
	}, options.RequestStop)
	return nil
}

func requestAgentTrayQuit() {
	trayQuitOnce.Do(systray.Quit)
}

func openTrayURL(target string) error {
	command := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", target)
	command.SysProcAttr = hiddenWindowProcessAttributes()
	return command.Start()
}

func confirmAgentExit() bool {
	message, _ := windows.UTF16PtrFromString("Exit VibeBridge?\n\nAny active Codex or terminal session will be ended.")
	caption, _ := windows.UTF16PtrFromString("VibeBridge")
	result, err := windows.MessageBox(0, message, caption, windows.MB_YESNO|windows.MB_ICONQUESTION|windows.MB_DEFBUTTON2|windows.MB_SETFOREGROUND)
	return err == nil && result == messageBoxYes
}
