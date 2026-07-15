package pairingflow

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/deviceidentity"
	"github.com/zzemy/VibeBridge/internal/e2ee"
	"github.com/zzemy/VibeBridge/internal/pairing"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var testNow = time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)

func TestApprovedPairingIsEncryptedPersistedAndSingleUse(t *testing.T) {
	coordinator, invitations, identity := testCoordinator(t)
	invitation, err := invitations.Create([]string{"http://192.168.20.5:8787/pairing/v1"})
	if err != nil {
		t.Fatalf("create invitation: %v", err)
	}
	initiator, start := testInitiator(t, invitation)

	started, err := coordinator.Start(start)
	if err != nil {
		t.Fatalf("start pairing flow: %v", err)
	}
	phoneResult, finish, err := initiator.Finish(started.Response)
	if err != nil {
		t.Fatalf("finish phone handshake: %v", err)
	}
	defer phoneResult.Transport.Close()
	session, err := coordinator.Finish(started.FlowID, finish)
	if err != nil {
		t.Fatalf("finish Agent handshake: %v", err)
	}
	defer session.Close()
	if session.Snapshot().SAS != phoneResult.SAS || session.Snapshot().State != StatePending {
		t.Fatalf("pending snapshot = %#v, phone SAS %q", session.Snapshot(), phoneResult.SAS)
	}

	pendingFrame, err := session.Encrypt(&vibebridgev1.PairingApproval{Status: vibebridgev1.PairingApprovalStatus_PAIRING_APPROVAL_STATUS_PENDING})
	if err != nil {
		t.Fatalf("encrypt pending approval: %v", err)
	}
	assertEncryptedApproval(t, phoneResult.Transport, pendingFrame, vibebridgev1.PairingApprovalStatus_PAIRING_APPROVAL_STATUS_PENDING, 0)

	if err := coordinator.Approve(started.FlowID); err != nil {
		t.Fatalf("approve pairing: %v", err)
	}
	decision, err := session.Wait(context.Background())
	if err != nil {
		t.Fatalf("wait for approval: %v", err)
	}
	if decision.Status != vibebridgev1.PairingApprovalStatus_PAIRING_APPROVAL_STATUS_APPROVED || decision.AuthorizationVersion == 0 {
		t.Fatalf("approval decision = %v", decision)
	}
	finalFrame, err := session.Encrypt(decision)
	if err != nil {
		t.Fatalf("encrypt final approval: %v", err)
	}
	assertEncryptedApproval(t, phoneResult.Transport, finalFrame, vibebridgev1.PairingApprovalStatus_PAIRING_APPROVAL_STATUS_APPROVED, decision.AuthorizationVersion)

	deviceID := session.Snapshot().DeviceID
	record, err := identity.AuthorizedDevice(deviceID)
	if err != nil || record.AuthorizationVersion != decision.AuthorizationVersion {
		t.Fatalf("persisted authorization = %v/%v", record, err)
	}
	if _, err := invitations.ActiveStatus(); !errors.Is(err, pairing.ErrInvitationUnavailable) {
		t.Fatalf("invitation after approval = %v, want unavailable", err)
	}
	if err := coordinator.Approve(started.FlowID); !errors.Is(err, ErrFlowUnavailable) {
		t.Fatalf("replayed approval = %v, want unavailable", err)
	}
}

func TestRejectedAndDisconnectedFlowsNeverAuthorize(t *testing.T) {
	coordinator, invitations, identity := testCoordinator(t)
	invitation, err := invitations.Create(nil)
	if err != nil {
		t.Fatalf("create invitation: %v", err)
	}
	initiator, start := testInitiator(t, invitation)
	started, err := coordinator.Start(start)
	if err != nil {
		t.Fatalf("start pairing flow: %v", err)
	}
	_, finish, err := initiator.Finish(started.Response)
	if err != nil {
		t.Fatalf("finish phone handshake: %v", err)
	}
	session, err := coordinator.Finish(started.FlowID, finish)
	if err != nil {
		t.Fatalf("finish Agent handshake: %v", err)
	}
	defer session.Close()
	if err := coordinator.Reject(started.FlowID); err != nil {
		t.Fatalf("reject pairing: %v", err)
	}
	decision, err := session.Wait(context.Background())
	if err != nil || decision.Status != vibebridgev1.PairingApprovalStatus_PAIRING_APPROVAL_STATUS_REJECTED {
		t.Fatalf("rejection decision = %v/%v", decision, err)
	}
	if devices, err := identity.AuthorizedDevices(false); err != nil || len(devices) != 0 {
		t.Fatalf("authorized devices after rejection = %v/%v", devices, err)
	}
	if _, err := invitations.ActiveStatus(); !errors.Is(err, pairing.ErrInvitationUnavailable) {
		t.Fatalf("rejected invitation status = %v", err)
	}

	secondInvitation, err := invitations.Create(nil)
	if err != nil {
		t.Fatalf("create second invitation: %v", err)
	}
	_, secondStart := testInitiator(t, secondInvitation)
	second, err := coordinator.Start(secondStart)
	if err != nil {
		t.Fatalf("start second flow: %v", err)
	}
	coordinator.Cancel(second.FlowID)
	status, err := invitations.ActiveStatus()
	if err != nil || status.Reserved {
		t.Fatalf("disconnected invitation = %#v/%v", status, err)
	}
	if _, err := coordinator.Start(secondStart); err != nil {
		t.Fatalf("released invitation could not be retried: %v", err)
	}
}

type nilAuthorizationIdentity struct {
	*deviceidentity.Store
}

func (identity nilAuthorizationIdentity) Authorize(*vibebridgev1.SignedDeviceDescriptor) (*vibebridgev1.AuthorizedDevice, error) {
	return nil, nil
}

func TestApprovalDoesNotConsumeInvitationWhenIdentityReturnsNoRecord(t *testing.T) {
	identity, err := deviceidentity.LoadOrCreate(deviceidentity.Options{
		Path:        filepath.Join(t.TempDir(), "identity.json"),
		DisplayName: "Home PC",
		Platform:    "windows",
		Now:         func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("create Agent identity: %v", err)
	}
	defer identity.Close()
	invitations, err := pairing.New(pairing.Config{Agent: identity, Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("create invitation manager: %v", err)
	}
	coordinator, err := New(Config{Invitations: invitations, Identity: nilAuthorizationIdentity{Store: identity}, Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("create pairing coordinator: %v", err)
	}
	invitation, err := invitations.Create(nil)
	if err != nil {
		t.Fatalf("create invitation: %v", err)
	}
	initiator, start := testInitiator(t, invitation)
	started, err := coordinator.Start(start)
	if err != nil {
		t.Fatalf("start pairing: %v", err)
	}
	_, finish, err := initiator.Finish(started.Response)
	if err != nil {
		t.Fatalf("finish phone handshake: %v", err)
	}
	if _, err := coordinator.Finish(started.FlowID, finish); err != nil {
		t.Fatalf("finish Agent handshake: %v", err)
	}
	if err := coordinator.Approve(started.FlowID); !errors.Is(err, ErrInvalidFlow) {
		t.Fatalf("nil authorization error = %v, want invalid flow", err)
	}
	status, err := invitations.ActiveStatus()
	if err != nil || !status.Reserved {
		t.Fatalf("invitation after invalid persistence = %#v/%v", status, err)
	}
}

func testCoordinator(t *testing.T) (*Coordinator, *pairing.Manager, *deviceidentity.Store) {
	t.Helper()
	identity, err := deviceidentity.LoadOrCreate(deviceidentity.Options{
		Path:        filepath.Join(t.TempDir(), "identity.json"),
		DisplayName: "Home PC",
		Platform:    "windows",
		Now:         func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("create Agent identity: %v", err)
	}
	t.Cleanup(identity.Close)
	invitations, err := pairing.New(pairing.Config{Agent: identity, Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("create invitation manager: %v", err)
	}
	t.Cleanup(invitations.Close)
	coordinator, err := New(Config{
		Invitations: invitations,
		Identity:    identity,
		Now:         func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("create pairing coordinator: %v", err)
	}
	return coordinator, invitations, identity
}

func testInitiator(t *testing.T, invitation *vibebridgev1.PairingInvitation) (*e2ee.PairingInitiator, *vibebridgev1.PairingHandshakeStart) {
	t.Helper()
	phone, privateKey := testPhoneDescriptor(t)
	contextMessage, err := e2ee.NewPairingContext(
		phone.DeviceDescriptor.DeviceId,
		invitation.Agent.DeviceDescriptor.DeviceId,
		invitation.InvitationId,
		nil,
	)
	if err != nil {
		t.Fatalf("create pairing context: %v", err)
	}
	initiator, err := e2ee.NewPairingInitiator(e2ee.PairingInitiatorConfig{
		Context:          contextMessage,
		Client:           phone,
		Agent:            invitation.Agent,
		StaticPrivateKey: privateKey,
		BootstrapSecret:  invitation.BootstrapSecret,
	})
	if err != nil {
		t.Fatalf("create pairing initiator: %v", err)
	}
	start, err := initiator.Start()
	if err != nil {
		t.Fatalf("start pairing initiator: %v", err)
	}
	return initiator, start
}

func testPhoneDescriptor(t *testing.T) (*vibebridgev1.SignedDeviceDescriptor, []byte) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	agreementKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate agreement key: %v", err)
	}
	deviceID := make([]byte, pairing.InvitationIDBytes)
	if _, err := rand.Read(deviceID); err != nil {
		t.Fatalf("generate device ID: %v", err)
	}
	descriptor := &vibebridgev1.DeviceDescriptor{
		DeviceId:              deviceID,
		DisplayName:           "My Phone",
		Platform:              "web",
		DeviceClass:           vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT,
		SigningPublicKey:      publicKey,
		KeyAgreementPublicKey: agreementKey.PublicKey().Bytes(),
		CreatedAt:             timestamppb.New(testNow),
		KeyVersion:            1,
		SupportedVersions: &vibebridgev1.ProtocolVersionRange{
			Minimum: &vibebridgev1.ProtocolVersion{Major: 1},
			Maximum: &vibebridgev1.ProtocolVersion{Major: 1},
		},
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(descriptor)
	if err != nil {
		t.Fatalf("encode phone descriptor: %v", err)
	}
	message := append([]byte("VibeBridge device descriptor v1\x00"), encoded...)
	return &vibebridgev1.SignedDeviceDescriptor{DeviceDescriptor: descriptor, Signature: ed25519.Sign(privateKey, message)}, agreementKey.Bytes()
}

func assertEncryptedApproval(t *testing.T, transport *e2ee.Transport, ciphertext []byte, wantStatus vibebridgev1.PairingApprovalStatus, wantVersion uint64) {
	t.Helper()
	plaintext, err := transport.Decrypt(ciphertext, []byte(ApprovalAssociatedData))
	if err != nil {
		t.Fatalf("decrypt approval frame: %v", err)
	}
	approval := new(vibebridgev1.PairingApproval)
	if err := proto.Unmarshal(plaintext, approval); err != nil {
		t.Fatalf("decode approval frame: %v", err)
	}
	if approval.Status != wantStatus || approval.AuthorizationVersion != wantVersion {
		t.Fatalf("approval frame = %v, want %s/%d", approval, wantStatus, wantVersion)
	}
}
