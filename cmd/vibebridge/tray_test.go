package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTrayURLsUseLoopbackEvenWhenAgentListensOnLAN(t *testing.T) {
	appURL, pairingURL, statusURL, err := trayURLs("0.0.0.0:8787", "a token")
	if err != nil {
		t.Fatalf("create tray URLs: %v", err)
	}
	if appURL != "http://127.0.0.1:8787/?token=a+token" {
		t.Fatalf("app URL = %q", appURL)
	}
	if pairingURL != "http://127.0.0.1:8787/agent/pair?token=a+token" {
		t.Fatalf("pairing URL = %q", pairingURL)
	}
	if statusURL != "http://127.0.0.1:8787/status?token=a+token" {
		t.Fatalf("status URL = %q", statusURL)
	}
}

func TestQueryTrayStatusMapsPublicSessionStates(t *testing.T) {
	for _, test := range []struct {
		state string
		want  string
	}{
		{state: "idle", want: "Agent online · no active session"},
		{state: "connected", want: "Agent online · session connected"},
		{state: "detached", want: "Agent online · session waiting"},
		{state: "ended", want: "Agent online"},
	} {
		t.Run(test.state, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(writer, `{"state":%q,"reconnect_timeout_seconds":90,"idle_timeout_seconds":1800}`, test.state)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			got, err := queryTrayStatus(ctx, server.URL)
			if err != nil {
				t.Fatalf("query tray status: %v", err)
			}
			if got != test.want {
				t.Fatalf("status = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTrayOptionsRejectUnsafeOrIncompleteConfiguration(t *testing.T) {
	valid := agentTrayOptions{
		AppURL:       "http://127.0.0.1:8787/?token=secret",
		PairingURL:   "http://127.0.0.1:8787/agent/pair?token=secret",
		StatusURL:    "http://127.0.0.1:8787/status?token=secret",
		RequestStop:  func() {},
		StatusPeriod: time.Second,
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid tray options: %v", err)
	}
	for name, target := range map[string]string{
		"external HTTP":  "http://203.0.113.10/",
		"external HTTPS": "https://example.com/",
	} {
		t.Run(name, func(t *testing.T) {
			invalid := valid
			invalid.AppURL = target
			if err := invalid.validate(); err == nil {
				t.Fatalf("unsafe tray URL %q was accepted", target)
			}
		})
	}
	invalid := valid
	invalid.RequestStop = nil
	if err := invalid.validate(); err == nil {
		t.Fatal("nil stop callback was accepted")
	}
}
