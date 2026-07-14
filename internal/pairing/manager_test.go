package pairing

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/deviceidentity"
	"google.golang.org/protobuf/proto"
)

var fixedNow = time.Date(2026, 7, 15, 4, 0, 0, 0, time.UTC)

func TestInvitationHasStrongBoundedSingleUseCredentials(t *testing.T) {
	agent := testAgent(t)
	defer agent.Close()
	entropy := make([]byte, (InvitationIDBytes+BootstrapSecretBytes)*2)
	for index := range entropy {
		entropy[index] = byte(index + 1)
	}
	manager, err := New(Config{Agent: agent, Now: func() time.Time { return fixedNow }, Random: bytes.NewReader(entropy)})
	if err != nil {
		t.Fatalf("create pairing manager: %v", err)
	}
	defer manager.Close()

	first, err := manager.Create([]string{"http://192.168.1.20:8787/pair"})
	if err != nil {
		t.Fatalf("create first invitation: %v", err)
	}
	if len(first.InvitationId) != InvitationIDBytes || len(first.BootstrapSecret) != BootstrapSecretBytes {
		t.Fatalf("credential sizes = %d/%d", len(first.InvitationId), len(first.BootstrapSecret))
	}
	if first.ExpiresAt.AsTime().Sub(first.CreatedAt.AsTime()) != DefaultTTL {
		t.Fatalf("invitation TTL = %s, want %s", first.ExpiresAt.AsTime().Sub(first.CreatedAt.AsTime()), DefaultTTL)
	}
	if err := ValidateInvitation(first, fixedNow); err != nil {
		t.Fatalf("validate first invitation: %v", err)
	}
	if len(first.VerificationCode) != 7 || first.VerificationCode[3] != '-' {
		t.Fatalf("verification code = %q, want NNN-NNN", first.VerificationCode)
	}
	status, err := manager.ActiveStatus()
	if err != nil || !bytes.Equal(status.InvitationID, first.InvitationId) || status.VerificationCode != first.VerificationCode {
		t.Fatalf("active status = %#v/%v", status, err)
	}

	second, err := manager.Create(nil)
	if err != nil {
		t.Fatalf("create superseding invitation: %v", err)
	}
	if err := manager.Authenticate(first.InvitationId, first.BootstrapSecret); !errors.Is(err, ErrInvitationRejected) {
		t.Fatalf("superseded invitation error = %v, want rejected", err)
	}
	if err := manager.Authenticate(second.InvitationId, bytes.Repeat([]byte{0xff}, BootstrapSecretBytes)); !errors.Is(err, ErrInvitationRejected) {
		t.Fatalf("wrong-secret error = %v, want rejected", err)
	}
	if err := manager.Authenticate(second.InvitationId, second.BootstrapSecret); err != nil {
		t.Fatalf("authenticate active invitation: %v", err)
	}
	if err := manager.Consume(second.InvitationId, second.BootstrapSecret); err != nil {
		t.Fatalf("consume active invitation: %v", err)
	}
	if err := manager.Consume(second.InvitationId, second.BootstrapSecret); !errors.Is(err, ErrInvitationUnavailable) {
		t.Fatalf("replay consume error = %v, want unavailable", err)
	}
}

func TestInvitationExpiresAndClearsActiveCapability(t *testing.T) {
	agent := testAgent(t)
	defer agent.Close()
	now := fixedNow
	manager, err := New(Config{
		Agent:  agent,
		TTL:    time.Minute,
		Now:    func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x42}, InvitationIDBytes+BootstrapSecretBytes)),
	})
	if err != nil {
		t.Fatalf("create pairing manager: %v", err)
	}
	invitation, err := manager.Create(nil)
	if err != nil {
		t.Fatalf("create invitation: %v", err)
	}
	now = fixedNow.Add(time.Minute)
	if err := manager.Authenticate(invitation.InvitationId, invitation.BootstrapSecret); !errors.Is(err, ErrInvitationExpired) {
		t.Fatalf("expired authentication error = %v, want expired", err)
	}
	if _, err := manager.ActiveStatus(); !errors.Is(err, ErrInvitationUnavailable) {
		t.Fatalf("status after expiry = %v, want unavailable", err)
	}
	if err := ValidateInvitation(invitation, now); !errors.Is(err, ErrInvitationExpired) {
		t.Fatalf("scanned expiry error = %v, want expired", err)
	}
}

func TestConcurrentConsumeHasExactlyOneWinner(t *testing.T) {
	agent := testAgent(t)
	defer agent.Close()
	manager, err := New(Config{Agent: agent, Now: func() time.Time { return fixedNow }, Random: bytes.NewReader(bytes.Repeat([]byte{0x33}, InvitationIDBytes+BootstrapSecretBytes))})
	if err != nil {
		t.Fatalf("create pairing manager: %v", err)
	}
	invitation, err := manager.Create(nil)
	if err != nil {
		t.Fatalf("create invitation: %v", err)
	}
	var successes atomic.Int32
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if manager.Consume(invitation.InvitationId, invitation.BootstrapSecret) == nil {
				successes.Add(1)
			}
		}()
	}
	wait.Wait()
	if successes.Load() != 1 {
		t.Fatalf("successful concurrent consumes = %d, want 1", successes.Load())
	}
}

func TestPairingURLKeepsSecretOutOfHTTPRequestAndRoundTrips(t *testing.T) {
	agent := testAgent(t)
	defer agent.Close()
	manager, err := New(Config{Agent: agent, Now: func() time.Time { return fixedNow }, Random: bytes.NewReader(bytes.Repeat([]byte{0x55}, InvitationIDBytes+BootstrapSecretBytes))})
	if err != nil {
		t.Fatalf("create pairing manager: %v", err)
	}
	invitation, err := manager.Create([]string{"wss://relay.example/pair/opaque"})
	if err != nil {
		t.Fatalf("create invitation: %v", err)
	}
	value, err := FragmentURL("http://192.168.1.20:8787/", invitation)
	if err != nil {
		t.Fatalf("create fragment URL: %v", err)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatalf("parse fragment URL: %v", err)
	}
	secretText := base64.RawURLEncoding.EncodeToString(invitation.BootstrapSecret)
	if parsed.RawQuery != "" || strings.Contains(parsed.RequestURI(), secretText) || !strings.Contains(parsed.Fragment, "/pair/v1/") {
		t.Fatalf("pairing URL leaked secret outside fragment: %s", value)
	}
	decoded, err := ParseFragmentURL(value, fixedNow)
	if err != nil {
		t.Fatalf("parse pairing URL: %v", err)
	}
	if !proto.Equal(decoded, invitation) {
		t.Fatalf("decoded invitation changed\n got: %v\nwant: %v", decoded, invitation)
	}
	withQuery := *parsed
	withQuery.RawQuery = "tracking=1"
	if _, err := ParseFragmentURL(withQuery.String(), fixedNow); err == nil {
		t.Fatal("pairing URL with a query was accepted")
	}
	withCredentials := *parsed
	withCredentials.User = url.UserPassword("user", "password")
	if _, err := ParseFragmentURL(withCredentials.String(), fixedNow); err == nil {
		t.Fatal("pairing URL with credentials was accepted")
	}
}

func TestValidateInvitationRejectsTamperingAndBadHints(t *testing.T) {
	agent := testAgent(t)
	defer agent.Close()
	manager, err := New(Config{Agent: agent, Now: func() time.Time { return fixedNow }, Random: bytes.NewReader(bytes.Repeat([]byte{0x77}, InvitationIDBytes+BootstrapSecretBytes))})
	if err != nil {
		t.Fatalf("create pairing manager: %v", err)
	}
	invitation, err := manager.Create(nil)
	if err != nil {
		t.Fatalf("create invitation: %v", err)
	}
	codeTamper := proto.Clone(invitation).(*vibebridgev1.PairingInvitation)
	codeTamper.VerificationCode = "000-000"
	if err := ValidateInvitation(codeTamper, fixedNow); err == nil {
		t.Fatal("tampered verification code accepted")
	}
	descriptorTamper := proto.Clone(invitation).(*vibebridgev1.PairingInvitation)
	descriptorTamper.Agent.DeviceDescriptor.DisplayName = "Attacker"
	if err := ValidateInvitation(descriptorTamper, fixedNow); err == nil {
		t.Fatal("tampered Agent descriptor accepted")
	}
	if _, err := manager.Create([]string{"http://user:password@example.com/pair"}); err == nil {
		t.Fatal("connection hint containing credentials accepted")
	}
}

func testAgent(t *testing.T) *deviceidentity.Store {
	t.Helper()
	agent, err := deviceidentity.LoadOrCreate(deviceidentity.Options{
		Path:        filepath.Join(t.TempDir(), "identity.json"),
		DisplayName: "Home PC",
		Platform:    "windows",
		Now:         func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatalf("create test Agent identity: %v", err)
	}
	return agent
}
