//go:build windows

package main

import (
	"context"
	_ "embed"
	"os"
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
		pairItem := systray.AddMenuItem("Pair a phone...", "Show the local pairing QR code")
		pairingRequestItem := systray.AddMenuItem("No pairing request", "Authenticated phone awaiting local approval")
		pairingRequestItem.Disable()
		pairingCodeItem := systray.AddMenuItem("Verification code · —", "Compare this SAS with the phone")
		pairingCodeItem.Disable()
		allowPairingItem := systray.AddMenuItem("Allow phone", "Authorize the pending phone")
		allowPairingItem.Disable()
		rejectPairingItem := systray.AddMenuItem("Reject phone", "Reject and consume the pairing invitation")
		rejectPairingItem.Disable()
		systray.AddSeparator()
		transportItem := systray.AddMenuItem("Remote access · local only", "Relay access is not configured yet")
		transportItem.Disable()
		systray.AddSeparator()
		exitItem := systray.AddMenuItem("Exit Agent…", "End active sessions and stop VibeBridge")

		systray.SetOnTapped(func() {
			_ = openTrayURL(options.AppURL)
		})

		// Desktop runs as tray-only. No auto-open browser — the terminal UI is for mobile devices.

		go func() {
			ticker := time.NewTicker(options.StatusPeriod)
			defer ticker.Stop()
			currentFlowID := ""
			setPairingIdle := func(title string) {
				currentFlowID = ""
				pairingRequestItem.SetTitle(title)
				pairingCodeItem.SetTitle("Verification code · —")
				allowPairingItem.Disable()
				rejectPairingItem.Disable()
			}
			refresh := func() {
				ctx, cancel := context.WithTimeout(context.Background(), trayStatusTimeout)
				status, statusErr := queryTrayStatus(ctx, options.StatusURL)
				cancel()
				if statusErr != nil {
					statusItem.SetTitle("Agent unavailable")
				} else {
					statusItem.SetTitle(status)
				}

				ctx, cancel = context.WithTimeout(context.Background(), trayStatusTimeout)
				pairingStatus, pairingErr := queryTrayPairingStatus(ctx, options.PairingStatusURL)
				cancel()
				if pairingErr != nil {
					setPairingIdle("Pairing status unavailable")
					return
				}
				switch pairingStatus.State {
				case "idle":
					setPairingIdle("No pairing request")
				case "handshaking":
					currentFlowID = pairingStatus.FlowID
					pairingRequestItem.SetTitle("Pairing · " + pairingStatus.DisplayName)
					pairingCodeItem.SetTitle("Completing encrypted handshake...")
					allowPairingItem.Disable()
					rejectPairingItem.Disable()
				case "pending":
					currentFlowID = pairingStatus.FlowID
					pairingRequestItem.SetTitle("Approve · " + pairingStatus.DisplayName)
					pairingCodeItem.SetTitle("Verification code · " + pairingStatus.SAS)
					allowPairingItem.Enable()
					rejectPairingItem.Enable()
				}
			}
			postDecision := func(endpoint string) {
				if currentFlowID == "" {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), trayStatusTimeout)
				err := postTrayPairingDecision(ctx, endpoint, currentFlowID)
				cancel()
				if err != nil {
					pairingRequestItem.SetTitle("Pairing decision failed")
					return
				}
				refresh()
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
				case <-allowPairingItem.ClickedCh:
					postDecision(options.PairingApproveURL)
				case <-rejectPairingItem.ClickedCh:
					postDecision(options.PairingRejectURL)
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
	if exe := findAppBrowserExe(); exe != "" {
		cmd := exec.Command(exe, "--app="+target, "--no-default-browser-check")
		cmd.SysProcAttr = hiddenWindowProcessAttributes()
		return cmd.Start()
	}
	command := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", target)
	command.SysProcAttr = hiddenWindowProcessAttributes()
	return command.Start()
}

// findAppBrowserExe looks for Microsoft Edge (pre-installed on Windows 10+)
// to launch the UI in app mode, which provides a native-like window without
// browser chrome (no tabs, no URL bar). Falls back to the default browser
// if Edge is not found.
func findAppBrowserExe() string {
	candidates := []string{
		os.Getenv("ProgramFiles(x86)") + `\Microsoft\Edge\Application\msedge.exe`,
		os.Getenv("ProgramFiles") + `\Microsoft\Edge\Application\msedge.exe`,
		os.Getenv("LocalAppData") + `\Microsoft\Edge\Application\msedge.exe`,
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

func confirmAgentExit() bool {
	message, _ := windows.UTF16PtrFromString("Exit VibeBridge?\n\nAny active Codex or terminal session will be ended.")
	caption, _ := windows.UTF16PtrFromString("VibeBridge")
	result, err := windows.MessageBox(0, message, caption, windows.MB_YESNO|windows.MB_ICONQUESTION|windows.MB_DEFBUTTON2|windows.MB_SETFOREGROUND)
	return err == nil && result == messageBoxYes
}
