package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/zzemy/VibeBridge/internal/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8787", "HTTP listen address")
	webDir := flag.String("web-dir", "web/dist", "frontend static build directory")
	commandLine := flag.String("cmd", defaultCommandLine(), "command to run for each WebSocket session")
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
		SessionToken: token,
		WebDir:       *webDir,
		Command:      command,
	})

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		fmt.Printf("VibeBridge listening on http://%s/?token=%s\n", *addr, token)
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
