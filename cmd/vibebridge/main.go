package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/mdp/qrterminal/v3"
	"github.com/zzemy/VibeBridge/internal/server"
)

func main() {
	addr := flag.String("addr", "0.0.0.0:8787", "HTTP listen address")
	webDir := flag.String("web-dir", "web/dist", "frontend static build directory")
	commandLine := flag.String("cmd", defaultCommandLine(), "command to run for each WebSocket session")
	reconnectTimeout := flag.Duration("reconnect-timeout", 90*time.Second, "how long to keep a detached PTY session alive")
	flag.Parse()

	command := strings.Fields(*commandLine)
	if len(command) == 0 {
		log.Fatal("cmd must not be empty")
	}

	token, err := newSessionToken()
	if err != nil {
		log.Fatalf("create session token: %v", err)
	}

	app := server.New(server.Config{
		SessionToken:     token,
		WebDir:           *webDir,
		Command:          command,
		ReconnectTimeout: *reconnectTimeout,
	})

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		printStartup(*addr, token)
		errCh <- httpServer.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-stop:
		fmt.Printf("\nreceived %s, shutting down\n", sig)
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown server: %v", err)
	}
}

func newSessionToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func defaultCommandLine() string {
	if runtime.GOOS == "windows" {
		return "powershell.exe -NoLogo -NoExit"
	}
	return "/bin/sh"
}

func printStartup(addr string, token string) {
	urls, err := accessURLs(addr, token)
	if err != nil {
		fmt.Printf("VibeBridge listening on %s\n", addr)
		fmt.Printf("Could not build access URL: %v\n", err)
		return
	}

	fmt.Printf("VibeBridge listening on %s\n", addr)
	for _, url := range urls {
		fmt.Printf("Open: %s\n", url)
	}
	if len(urls) == 0 {
		return
	}

	fmt.Println("Scan this QR code from your phone:")
	qrterminal.GenerateHalfBlock(urls[0], qrterminal.L, os.Stdout)
}

func accessURLs(addr string, token string) ([]string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if host == "" {
		host = "0.0.0.0"
	}

	hosts := make([]string, 0, 3)
	if host == "0.0.0.0" || host == "::" {
		hosts = append(hosts, lanIPv4Hosts()...)
		hosts = append(hosts, "127.0.0.1")
	} else {
		hosts = append(hosts, host)
	}

	seen := make(map[string]bool, len(hosts))
	urls := make([]string, 0, len(hosts))
	for _, candidate := range hosts {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		urls = append(urls, "http://"+net.JoinHostPort(candidate, port)+"/?token="+token)
	}
	return urls, nil
}

func lanIPv4Hosts() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var hosts []string
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip := addressIP(addr)
			if ip == nil {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil || ip4.IsLoopback() || !ip4.IsPrivate() {
				continue
			}
			hosts = append(hosts, ip4.String())
		}
	}
	return hosts
}

func addressIP(addr net.Addr) net.IP {
	switch value := addr.(type) {
	case *net.IPNet:
		return value.IP
	case *net.IPAddr:
		return value.IP
	default:
		return nil
	}
}
