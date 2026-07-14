package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalPairingPageRequiresLocalMachineAndAgentToken(t *testing.T) {
	application := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTeapot)
	})
	handler, err := newAgentHTTPHandler(application, "192.168.20.5:8787", "secret-token")
	if err != nil {
		t.Fatalf("create Agent handler: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost/agent/pair?token=secret-token", nil)
	request.RemoteAddr = "127.0.0.1:49152"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("pairing status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "data:image/png;base64,") || !strings.Contains(body, "http://192.168.20.5:8787/?token=secret-token") {
		t.Fatalf("pairing page does not contain its QR and target URL: %s", body)
	}
	for name, expected := range map[string]string{
		"Cache-Control":          "no-store",
		"Referrer-Policy":        "no-referrer",
		"X-Content-Type-Options": "nosniff",
	} {
		if value := response.Header().Get(name); value != expected {
			t.Fatalf("%s = %q, want %q", name, value, expected)
		}
	}
	if policy := response.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "frame-ancestors 'none'") {
		t.Fatalf("Content-Security-Policy = %q", policy)
	}

	unauthorized := httptest.NewRequest(http.MethodGet, "http://localhost/agent/pair?token=wrong", nil)
	unauthorized.RemoteAddr = "127.0.0.1:49152"
	unauthorizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("invalid-token status = %d, want 401", unauthorizedResponse.Code)
	}

	remote := httptest.NewRequest(http.MethodGet, "http://localhost/agent/pair?token=secret-token", nil)
	remote.RemoteAddr = "192.168.20.8:49152"
	remoteResponse := httptest.NewRecorder()
	handler.ServeHTTP(remoteResponse, remote)
	if remoteResponse.Code != http.StatusForbidden {
		t.Fatalf("remote pairing status = %d, want 403", remoteResponse.Code)
	}

	fallback := httptest.NewRequest(http.MethodGet, "http://localhost/healthz", nil)
	fallbackResponse := httptest.NewRecorder()
	handler.ServeHTTP(fallbackResponse, fallback)
	if fallbackResponse.Code != http.StatusTeapot {
		t.Fatalf("application fallback status = %d, want 418", fallbackResponse.Code)
	}
}

func TestLocalPairingPageExplainsMissingPrivateNetworkAddress(t *testing.T) {
	handler, err := newAgentHTTPHandler(http.NotFoundHandler(), "127.0.0.1:8787", "secret-token")
	if err != nil {
		t.Fatalf("create Agent handler: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://localhost/agent/pair?token=secret-token", nil)
	request.RemoteAddr = "[::1]:49152"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "No private-network address is available") {
		t.Fatalf("missing-network response = %d/%q", response.Code, response.Body.String())
	}
}

func TestLocalPairingPageOnlyAcceptsGet(t *testing.T) {
	handler, err := newAgentHTTPHandler(http.NotFoundHandler(), "127.0.0.1:8787", "secret-token")
	if err != nil {
		t.Fatalf("create Agent handler: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://localhost/agent/pair?token=secret-token", nil)
	request.RemoteAddr = "127.0.0.1:49152"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("method response = %d/%q", response.Code, response.Header().Get("Allow"))
	}
}

func TestMatchesLocalMachineIPAllowsLoopbackAndAssignedAddresses(t *testing.T) {
	addresses := []net.Addr{
		&net.IPNet{IP: net.ParseIP("192.168.20.5"), Mask: net.CIDRMask(24, 32)},
	}
	for name, test := range map[string]struct {
		candidate string
		want      bool
	}{
		"loopback": {candidate: "127.0.0.1", want: true},
		"assigned": {candidate: "192.168.20.5", want: true},
		"remote":   {candidate: "192.168.20.8", want: false},
		"invalid":  {candidate: "not-an-ip", want: false},
	} {
		t.Run(name, func(t *testing.T) {
			got := matchesLocalMachineIP(net.ParseIP(test.candidate), addresses)
			if got != test.want {
				t.Fatalf("matchesLocalMachineIP(%q) = %t, want %t", test.candidate, got, test.want)
			}
		})
	}
}
