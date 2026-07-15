package main

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/deviceidentity"
	"github.com/zzemy/VibeBridge/internal/pairing"
	"github.com/zzemy/VibeBridge/internal/pairingflow"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestLocalPairingPageRequiresLocalMachineAndAgentToken(t *testing.T) {
	application := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTeapot)
	})
	handler, identity := testAgentHTTPHandler(t, application, "192.168.20.5:8787")
	defer identity.Close()

	request := httptest.NewRequest(http.MethodGet, "http://localhost/agent/pair?token=secret-token", nil)
	request.RemoteAddr = "127.0.0.1:49152"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("pairing status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "data:image/png;base64,") || !strings.Contains(body, "http://192.168.20.5:8787/#/pair/v1/") {
		t.Fatalf("pairing page does not contain a fragment-only invitation: %s", body)
	}
	if strings.Contains(body, "http://192.168.20.5:8787/?token=secret-token") {
		t.Fatal("pairing QR still contains the runtime session bearer token")
	}
	if !strings.Contains(body, "Verification code") || !strings.Contains(body, "Agent fingerprint") {
		t.Fatal("pairing page omits verification metadata")
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
	policy := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "frame-ancestors 'none'") || !strings.Contains(policy, "form-action 'self'") {
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
	handler, identity := testAgentHTTPHandler(t, http.NotFoundHandler(), "127.0.0.1:8787")
	defer identity.Close()
	request := httptest.NewRequest(http.MethodGet, "http://localhost/agent/pair?token=secret-token", nil)
	request.RemoteAddr = "[::1]:49152"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "No private-network address is available") {
		t.Fatalf("missing-network response = %d/%q", response.Code, response.Body.String())
	}
}

func TestLocalPairingPageOnlyAcceptsGet(t *testing.T) {
	handler, identity := testAgentHTTPHandler(t, http.NotFoundHandler(), "127.0.0.1:8787")
	defer identity.Close()
	request := httptest.NewRequest(http.MethodPost, "http://localhost/agent/pair?token=secret-token", nil)
	request.RemoteAddr = "127.0.0.1:49152"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("method response = %d/%q", response.Code, response.Header().Get("Allow"))
	}
}

func TestLocalDeviceRevokePersistsAndRequiresLocalAuthenticatedPost(t *testing.T) {
	handler, identity := testAgentHTTPHandler(t, http.NotFoundHandler(), "192.168.20.5:8787")
	defer identity.Close()
	client := testSignedClientDescriptor(t)
	if _, err := identity.Authorize(client); err != nil {
		t.Fatalf("authorize test phone: %v", err)
	}
	deviceID := base64.RawURLEncoding.EncodeToString(client.DeviceDescriptor.DeviceId)
	form := url.Values{"device_id": []string{deviceID}}
	request := httptest.NewRequest(http.MethodPost, "http://localhost/agent/devices/revoke?token=secret-token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.RemoteAddr = "127.0.0.1:49152"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/agent/pair?token=secret-token" {
		t.Fatalf("revoke response = %d/%q", response.Code, response.Header().Get("Location"))
	}
	record, err := identity.AuthorizedDevice(client.DeviceDescriptor.DeviceId)
	if err != nil || record.State != vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_REVOKED || identity.RevocationEpoch() != 1 {
		t.Fatalf("revoked record = %v/%v epoch %d", record, err, identity.RevocationEpoch())
	}

	remote := httptest.NewRequest(http.MethodPost, "http://localhost/agent/devices/revoke?token=secret-token", strings.NewReader(form.Encode()))
	remote.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	remote.RemoteAddr = "192.168.20.8:49152"
	remoteResponse := httptest.NewRecorder()
	handler.ServeHTTP(remoteResponse, remote)
	if remoteResponse.Code != http.StatusForbidden {
		t.Fatalf("remote revoke status = %d, want 403", remoteResponse.Code)
	}
}

func TestMatchesLocalMachineIPAllowsLoopbackAndAssignedAddresses(t *testing.T) {
	addresses := []net.Addr{&net.IPNet{IP: net.ParseIP("192.168.20.5"), Mask: net.CIDRMask(24, 32)}}
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

func testAgentHTTPHandler(t *testing.T, application http.Handler, address string) (http.Handler, *deviceidentity.Store) {
	t.Helper()
	identity, err := deviceidentity.LoadOrCreate(deviceidentity.Options{
		Path:        filepath.Join(t.TempDir(), "identity.json"),
		DisplayName: "Home PC",
		Platform:    "windows",
	})
	if err != nil {
		t.Fatalf("create test identity: %v", err)
	}
	pairingManager, err := pairing.New(pairing.Config{Agent: identity})
	if err != nil {
		identity.Close()
		t.Fatalf("create pairing manager: %v", err)
	}
	t.Cleanup(pairingManager.Close)
	flows, err := pairingflow.New(pairingflow.Config{Invitations: pairingManager, Identity: identity})
	if err != nil {
		identity.Close()
		t.Fatalf("create pairing flow coordinator: %v", err)
	}
	handler, err := newAgentHTTPHandler(application, address, "secret-token", pairingManager, identity, flows)
	if err != nil {
		identity.Close()
		t.Fatalf("create Agent handler: %v", err)
	}
	return handler, identity
}

func testSignedClientDescriptor(t *testing.T) *vibebridgev1.SignedDeviceDescriptor {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	agreementKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate agreement key: %v", err)
	}
	descriptor := &vibebridgev1.DeviceDescriptor{
		DeviceId:              []byte("phone-device-id!"),
		DisplayName:           "My Phone",
		Platform:              "web",
		DeviceClass:           vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT,
		SigningPublicKey:      publicKey,
		KeyAgreementPublicKey: agreementKey.PublicKey().Bytes(),
		CreatedAt:             timestamppb.New(time.Now().UTC()),
		KeyVersion:            1,
		SupportedVersions: &vibebridgev1.ProtocolVersionRange{
			Minimum: &vibebridgev1.ProtocolVersion{Major: 1},
			Maximum: &vibebridgev1.ProtocolVersion{Major: 1},
		},
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(descriptor)
	if err != nil {
		t.Fatalf("encode client descriptor: %v", err)
	}
	message := append([]byte("VibeBridge device descriptor v1\x00"), encoded...)
	return &vibebridgev1.SignedDeviceDescriptor{DeviceDescriptor: descriptor, Signature: ed25519.Sign(privateKey, message)}
}
