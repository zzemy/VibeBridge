package relay

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"google.golang.org/protobuf/proto"
)

// newTestAdmin returns an Admin backed by a freshly generated Issuer.
// Each call gets its own key pair so tests are independent.
func newTestAdmin(t *testing.T) (*Admin, ed25519.PublicKey) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	issuer, err := NewIssuer(private)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	admin, err := NewAdmin(issuer)
	if err != nil {
		t.Fatalf("new admin: %v", err)
	}
	return admin, issuer.PublicKey()
}

func TestAdminIssueAcceptsValidRequest(t *testing.T) {
	t.Parallel()
	admin, public := newTestAdmin(t)
	server := httptest.NewServer(admin.Handler(""))
	defer server.Close()

	body := mustEncode(t, issueRequest{
		RouteID:         hex.EncodeToString(bytes.Repeat([]byte{0x11}, routeIDBytes)),
		DeviceID:        hex.EncodeToString(bytes.Repeat([]byte{0x22}, 16)),
		Endpoint:        "agent",
		MaxConnections:  2,
		LifetimeSeconds: 90,
	})
	response, err := http.Post(server.URL+"/v1/tickets", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}
	var payload issueResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Ticket == "" {
		t.Fatalf("expected hex ticket, got empty string")
	}
	if payload.ExpiresAt.IsZero() {
		t.Fatalf("expected non-zero expires_at, got %v", payload.ExpiresAt)
	}
	// Round-trip the ticket through the Verifier with the issuer's
	// public key. This proves the admin handler emitted a wire
	// format the relay would actually accept.
	wire, err := hex.DecodeString(payload.Ticket)
	if err != nil {
		t.Fatalf("ticket is not valid hex: %v", err)
	}
	ticket := new(vibebridgev1.RelayTicket)
	if err := proto.Unmarshal(wire, ticket); err != nil {
		t.Fatalf("decode ticket: %v", err)
	}
	verifier := NewVerifier(public)
	verified, err := verifier.Verify(ticket)
	if err != nil {
		t.Fatalf("verify ticket: %v", err)
	}
	if verified.Endpoint() != vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT {
		t.Fatalf("endpoint round-trip lost: got %v", verified.Endpoint())
	}
	if verified.MaxConnections() != 2 {
		t.Fatalf("max connections round-trip lost: got %d", verified.MaxConnections())
	}
}

func TestAdminIssueRejectsNonPost(t *testing.T) {
	t.Parallel()
	admin, _ := newTestAdmin(t)
	server := httptest.NewServer(admin.Handler(""))
	defer server.Close()

	response, err := http.Get(server.URL + "/v1/tickets")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", response.StatusCode)
	}
}

func TestAdminIssueRejectsMissingFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body issueRequest
	}{
		{
			name: "missing route id",
			body: issueRequest{DeviceID: hex.EncodeToString(bytes.Repeat([]byte{1}, 16)), Endpoint: "agent", MaxConnections: 1, LifetimeSeconds: 60},
		},
		{
			name: "missing device id",
			body: issueRequest{RouteID: hex.EncodeToString(bytes.Repeat([]byte{1}, routeIDBytes)), Endpoint: "agent", MaxConnections: 1, LifetimeSeconds: 60},
		},
		{
			name: "missing endpoint",
			body: issueRequest{RouteID: hex.EncodeToString(bytes.Repeat([]byte{1}, routeIDBytes)), DeviceID: hex.EncodeToString(bytes.Repeat([]byte{1}, 16)), MaxConnections: 1, LifetimeSeconds: 60},
		},
		{
			name: "zero max connections",
			body: issueRequest{RouteID: hex.EncodeToString(bytes.Repeat([]byte{1}, routeIDBytes)), DeviceID: hex.EncodeToString(bytes.Repeat([]byte{1}, 16)), Endpoint: "agent", LifetimeSeconds: 60},
		},
		{
			name: "non-positive lifetime",
			body: issueRequest{RouteID: hex.EncodeToString(bytes.Repeat([]byte{1}, routeIDBytes)), DeviceID: hex.EncodeToString(bytes.Repeat([]byte{1}, 16)), Endpoint: "agent", MaxConnections: 1},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			admin, _ := newTestAdmin(t)
			server := httptest.NewServer(admin.Handler(""))
			defer server.Close()
			body := mustEncode(t, tc.body)
			response, err := http.Post(server.URL+"/v1/tickets", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatalf("post: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", response.StatusCode)
			}
		})
	}
}

func TestAdminIssueRejectsBadShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body issueRequest
	}{
		{
			name: "endpoint typo",
			body: issueRequest{RouteID: hex.EncodeToString(bytes.Repeat([]byte{1}, routeIDBytes)), DeviceID: hex.EncodeToString(bytes.Repeat([]byte{1}, 16)), Endpoint: "agnet", MaxConnections: 1, LifetimeSeconds: 60},
		},
		{
			name: "route id wrong length",
			body: issueRequest{RouteID: hex.EncodeToString(bytes.Repeat([]byte{1}, 4)), DeviceID: hex.EncodeToString(bytes.Repeat([]byte{1}, 16)), Endpoint: "agent", MaxConnections: 1, LifetimeSeconds: 60},
		},
		{
			name: "device id wrong length",
			body: issueRequest{RouteID: hex.EncodeToString(bytes.Repeat([]byte{1}, routeIDBytes)), DeviceID: hex.EncodeToString(bytes.Repeat([]byte{1}, 4)), Endpoint: "agent", MaxConnections: 1, LifetimeSeconds: 60},
		},
		{
			name: "non-hex route id",
			body: issueRequest{RouteID: strings.Repeat("z", 32), DeviceID: hex.EncodeToString(bytes.Repeat([]byte{1}, 16)), Endpoint: "agent", MaxConnections: 1, LifetimeSeconds: 60},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			admin, _ := newTestAdmin(t)
			server := httptest.NewServer(admin.Handler(""))
			defer server.Close()
			body := mustEncode(t, tc.body)
			response, err := http.Post(server.URL+"/v1/tickets", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatalf("post: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", response.StatusCode)
			}
		})
	}
}

func TestAdminIssueRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	admin, _ := newTestAdmin(t)
	server := httptest.NewServer(admin.Handler(""))
	defer server.Close()

	raw := []byte(`{"route_id":"` + hex.EncodeToString(bytes.Repeat([]byte{1}, routeIDBytes)) +
		`","device_id":"` + hex.EncodeToString(bytes.Repeat([]byte{1}, 16)) +
		`","endpoint":"agent","max_connections":1,"lifetime_seconds":60,"unexpected":"x"}`)
	response, err := http.Post(server.URL+"/v1/tickets", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.StatusCode)
	}
}

func TestAdminIssueEnforcesBearerToken(t *testing.T) {
	t.Parallel()
	admin, _ := newTestAdmin(t)
	const token = "super-secret-token"
	server := httptest.NewServer(admin.Handler(token))
	defer server.Close()

	body := mustEncode(t, issueRequest{
		RouteID:         hex.EncodeToString(bytes.Repeat([]byte{1}, routeIDBytes)),
		DeviceID:        hex.EncodeToString(bytes.Repeat([]byte{1}, 16)),
		Endpoint:        "agent",
		MaxConnections:  1,
		LifetimeSeconds: 60,
	})

	// Missing header -> 401.
	response, err := http.Post(server.URL+"/v1/tickets", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post without auth: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", response.StatusCode)
	}

	// Wrong token -> 401.
	wrong, err := http.NewRequest(http.MethodPost, server.URL+"/v1/tickets", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new wrong-auth request: %v", err)
	}
	wrong.Header.Set("Authorization", "Bearer not-the-token")
	wrong.Header.Set("Content-Type", "application/json")
	response2, err := http.DefaultClient.Do(wrong)
	if err != nil {
		t.Fatalf("send wrong auth: %v", err)
	}
	response2.Body.Close()
	if response2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", response2.StatusCode)
	}

	// Right token -> 200.
	authed, err := http.NewRequest(http.MethodPost, server.URL+"/v1/tickets", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new authed request: %v", err)
	}
	authed.Header.Set("Authorization", "Bearer "+token)
	authed.Header.Set("Content-Type", "application/json")
	response3, err := http.DefaultClient.Do(authed)
	if err != nil {
		t.Fatalf("send authed: %v", err)
	}
	defer response3.Body.Close()
	if response3.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with token, got %d", response3.StatusCode)
	}
}

func TestAdminIssueRespectsLifetime(t *testing.T) {
	t.Parallel()
	admin, _ := newTestAdmin(t)
	server := httptest.NewServer(admin.Handler(""))
	defer server.Close()

	const lifetime = 45 * time.Second
	before := time.Now().UTC()
	body := mustEncode(t, issueRequest{
		RouteID:         hex.EncodeToString(bytes.Repeat([]byte{1}, routeIDBytes)),
		DeviceID:        hex.EncodeToString(bytes.Repeat([]byte{1}, 16)),
		Endpoint:        "client",
		MaxConnections:  1,
		LifetimeSeconds: int(lifetime / time.Second),
	})
	response, err := http.Post(server.URL+"/v1/tickets", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}
	var payload issueResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// expires_at should be close to before + lifetime (allow 5s slack
	// for the clock advance between call and decode).
	delta := payload.ExpiresAt.Sub(before)
	if delta < lifetime-time.Second || delta > lifetime+5*time.Second {
		t.Fatalf("expires_at out of range: before=%v expires_at=%v delta=%v want ~%v",
			before, payload.ExpiresAt, delta, lifetime)
	}
}

func mustEncode(t *testing.T, body issueRequest) []byte {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}
	return encoded
}
