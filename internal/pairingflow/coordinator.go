package pairingflow

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"sync"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/e2ee"
	"github.com/zzemy/VibeBridge/internal/pairing"
	"google.golang.org/protobuf/proto"
)

const (
	DefaultApprovalTimeout = 2 * time.Minute
	WebSocketSubprotocol   = "vibebridge.pairing.v1"
	ApprovalAssociatedData = "VibeBridge pairing approval v1\x00"
	flowIDBytes            = 16
)

var (
	ErrInvalidFlow     = errors.New("pairing flow is invalid")
	ErrFlowInProgress  = errors.New("another pairing flow is already in progress")
	ErrFlowUnavailable = errors.New("pairing flow is not available")
	ErrFlowState       = errors.New("pairing flow is in an invalid state")
	ErrFlowExpired     = errors.New("pairing flow has expired")
	ErrFlowCanceled    = errors.New("pairing flow was canceled")
)

type deviceIdentity interface {
	Descriptor() (*vibebridgev1.SignedDeviceDescriptor, error)
	KeyAgreementPrivateKey() (*ecdh.PrivateKey, error)
	Authorize(*vibebridgev1.SignedDeviceDescriptor) (*vibebridgev1.AuthorizedDevice, error)
}

// Config supplies the durable identity and single-use invitation manager.
type Config struct {
	Invitations     *pairing.Manager
	Identity        deviceIdentity
	Now             func() time.Time
	Random          io.Reader
	ApprovalTimeout time.Duration
}

// State is the locally visible stage of one pairing flow.
type State string

const (
	StateHandshaking State = "handshaking"
	StatePending     State = "pending"
)

// Snapshot contains only information safe for the local approval UI and tray.
type Snapshot struct {
	FlowID       string
	InvitationID []byte
	DeviceID     []byte
	DisplayName  string
	Platform     string
	SAS          string
	State        State
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

// StartResult is the response to the first authenticated Noise frame.
type StartResult struct {
	FlowID   string
	Response *vibebridgev1.PairingHandshakeResponse
}

type flow struct {
	id          string
	reservation *pairing.Reservation
	responder   *e2ee.PairingResponder
	peer        *vibebridgev1.SignedDeviceDescriptor
	transport   *e2ee.Transport
	decision    chan decision
	snapshot    Snapshot
}

type decision struct {
	approval *vibebridgev1.PairingApproval
	err      error
}

// Coordinator owns the single pairing handshake and its explicit local decision.
type Coordinator struct {
	mu              sync.Mutex
	invitations     *pairing.Manager
	identity        deviceIdentity
	now             func() time.Time
	random          io.Reader
	approvalTimeout time.Duration
	active          *flow
}

func New(config Config) (*Coordinator, error) {
	if config.Invitations == nil || config.Identity == nil {
		return nil, errors.New("pairing flow dependencies are required")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	randomSource := config.Random
	if randomSource == nil {
		randomSource = rand.Reader
	}
	timeout := config.ApprovalTimeout
	if timeout == 0 {
		timeout = DefaultApprovalTimeout
	}
	if timeout < 30*time.Second || timeout > 5*time.Minute {
		return nil, errors.New("pairing approval timeout must be between 30 seconds and 5 minutes")
	}
	return &Coordinator{
		invitations:     config.Invitations,
		identity:        config.Identity,
		now:             now,
		random:          randomSource,
		approvalTimeout: timeout,
	}, nil
}

// Start accepts a direct (non-Relay) Noise message one and reserves its invitation.
func (coordinator *Coordinator) Start(start *vibebridgev1.PairingHandshakeStart) (*StartResult, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	coordinator.expireLocked()
	if coordinator.active != nil {
		return nil, ErrFlowInProgress
	}
	if start == nil || start.Context == nil {
		return nil, ErrInvalidFlow
	}
	agent, err := coordinator.identity.Descriptor()
	if err != nil || agent == nil || agent.DeviceDescriptor == nil {
		return nil, ErrInvalidFlow
	}
	expectedContext, err := e2ee.NewPairingContext(
		start.Context.InitiatorDeviceId,
		agent.DeviceDescriptor.DeviceId,
		start.Context.InvitationId,
		nil,
	)
	if err != nil {
		return nil, ErrInvalidFlow
	}
	privateKey, err := coordinator.identity.KeyAgreementPrivateKey()
	if err != nil {
		return nil, ErrInvalidFlow
	}
	privateBytes := privateKey.Bytes()
	defer clearBytes(privateBytes)

	flowIDRaw := make([]byte, flowIDBytes)
	if _, err := io.ReadFull(coordinator.random, flowIDRaw); err != nil {
		return nil, errors.New("generate pairing flow ID")
	}
	flowID := base64.RawURLEncoding.EncodeToString(flowIDRaw)
	clearBytes(flowIDRaw)

	var responder *e2ee.PairingResponder
	var response *vibebridgev1.PairingHandshakeResponse
	var peer *vibebridgev1.SignedDeviceDescriptor
	reservation, invitationStatus, err := coordinator.invitations.Begin(start.Context.InvitationId, func(secret []byte) error {
		var handshakeErr error
		responder, response, peer, handshakeErr = e2ee.AcceptPairingStart(e2ee.PairingResponderConfig{
			ExpectedContext:  expectedContext,
			Agent:            agent,
			StaticPrivateKey: privateBytes,
			BootstrapSecret:  secret,
			Random:           coordinator.random,
		}, start)
		return handshakeErr
	})
	if err != nil {
		if errors.Is(err, pairing.ErrInvitationInUse) {
			return nil, ErrFlowInProgress
		}
		return nil, ErrInvalidFlow
	}

	now := coordinator.now().UTC()
	expiresAt := invitationStatus.ExpiresAt.UTC()
	if approvalExpiry := now.Add(coordinator.approvalTimeout); approvalExpiry.Before(expiresAt) {
		expiresAt = approvalExpiry
	}
	coordinator.active = &flow{
		id:          flowID,
		reservation: reservation,
		responder:   responder,
		peer:        proto.Clone(peer).(*vibebridgev1.SignedDeviceDescriptor),
		decision:    make(chan decision, 1),
		snapshot: Snapshot{
			FlowID:       flowID,
			InvitationID: append([]byte(nil), invitationStatus.InvitationID...),
			DeviceID:     append([]byte(nil), peer.DeviceDescriptor.DeviceId...),
			DisplayName:  peer.DeviceDescriptor.DisplayName,
			Platform:     peer.DeviceDescriptor.Platform,
			State:        StateHandshaking,
			CreatedAt:    now,
			ExpiresAt:    expiresAt,
		},
	}
	return &StartResult{FlowID: flowID, Response: proto.Clone(response).(*vibebridgev1.PairingHandshakeResponse)}, nil
}

// Finish completes Noise message three and exposes the flow for local approval.
func (coordinator *Coordinator) Finish(flowID string, finish *vibebridgev1.PairingHandshakeFinish) (*Session, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	coordinator.expireLocked()
	current, err := coordinator.flowLocked(flowID)
	if err != nil {
		return nil, err
	}
	if current.snapshot.State != StateHandshaking || current.responder == nil {
		return nil, ErrFlowState
	}
	result, err := current.responder.Finish(finish)
	current.responder = nil
	if err != nil {
		coordinator.cancelLocked(current, ErrInvalidFlow)
		return nil, ErrInvalidFlow
	}
	current.transport = result.Transport
	current.snapshot.SAS = result.SAS
	current.snapshot.State = StatePending
	return &Session{
		flowID:    current.id,
		snapshot:  cloneSnapshot(current.snapshot),
		transport: current.transport,
		decision:  current.decision,
	}, nil
}

// Current returns the flow visible to the local management page and tray.
func (coordinator *Coordinator) Current() (Snapshot, bool) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	coordinator.expireLocked()
	if coordinator.active == nil {
		return Snapshot{}, false
	}
	return cloneSnapshot(coordinator.active.snapshot), true
}

// Approve durably authorizes the authenticated phone before consuming the invitation.
func (coordinator *Coordinator) Approve(flowID string) error {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	coordinator.expireLocked()
	current, err := coordinator.flowLocked(flowID)
	if err != nil {
		return err
	}
	if current.snapshot.State != StatePending || current.transport == nil {
		return ErrFlowState
	}
	var authorization *vibebridgev1.AuthorizedDevice
	if err := coordinator.invitations.Approve(current.reservation, func() error {
		var authorizeErr error
		authorization, authorizeErr = coordinator.identity.Authorize(current.peer)
		if authorizeErr == nil && (authorization == nil || authorization.AuthorizationVersion == 0) {
			return ErrInvalidFlow
		}
		return authorizeErr
	}); err != nil {
		if errors.Is(err, pairing.ErrInvitationUnavailable) || errors.Is(err, pairing.ErrInvitationExpired) || errors.Is(err, pairing.ErrInvitationRejected) {
			coordinator.cancelLocked(current, ErrFlowCanceled)
		}
		return err
	}
	approval := &vibebridgev1.PairingApproval{
		Status:               vibebridgev1.PairingApprovalStatus_PAIRING_APPROVAL_STATUS_APPROVED,
		AuthorizationVersion: authorization.AuthorizationVersion,
	}
	coordinator.completeLocked(current, approval)
	return nil
}

// Reject consumes the invitation and reports the encrypted local rejection.
func (coordinator *Coordinator) Reject(flowID string) error {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	coordinator.expireLocked()
	current, err := coordinator.flowLocked(flowID)
	if err != nil {
		return err
	}
	if current.snapshot.State != StatePending || current.transport == nil {
		return ErrFlowState
	}
	if err := coordinator.invitations.Reject(current.reservation); err != nil {
		coordinator.cancelLocked(current, ErrFlowCanceled)
		return err
	}
	coordinator.completeLocked(current, &vibebridgev1.PairingApproval{
		Status: vibebridgev1.PairingApprovalStatus_PAIRING_APPROVAL_STATUS_REJECTED,
	})
	return nil
}

// Cancel releases a disconnected flow without spending an otherwise valid invitation.
func (coordinator *Coordinator) Cancel(flowID string) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.active == nil || coordinator.active.id != flowID {
		return
	}
	coordinator.cancelLocked(coordinator.active, ErrFlowCanceled)
}

func (coordinator *Coordinator) flowLocked(flowID string) (*flow, error) {
	if coordinator.active == nil || flowID == "" || coordinator.active.id != flowID {
		return nil, ErrFlowUnavailable
	}
	return coordinator.active, nil
}

func (coordinator *Coordinator) expireLocked() {
	if coordinator.active == nil || coordinator.now().UTC().Before(coordinator.active.snapshot.ExpiresAt) {
		return
	}
	coordinator.cancelLocked(coordinator.active, ErrFlowExpired)
}

func (coordinator *Coordinator) cancelLocked(current *flow, reason error) {
	_ = coordinator.invitations.Release(current.reservation)
	if current.responder != nil {
		current.responder.Close()
		current.responder = nil
	}
	// A completed Session owns its transport and closes it after Wait observes
	// this cancellation, avoiding a race with a final WebSocket write.
	select {
	case current.decision <- decision{err: reason}:
	default:
	}
	if coordinator.active == current {
		coordinator.active = nil
	}
}

func (coordinator *Coordinator) completeLocked(current *flow, approval *vibebridgev1.PairingApproval) {
	select {
	case current.decision <- decision{approval: proto.Clone(approval).(*vibebridgev1.PairingApproval)}:
	default:
	}
	if coordinator.active == current {
		coordinator.active = nil
	}
}

// Session owns the post-handshake encrypted transport for one WebSocket.
type Session struct {
	flowID    string
	snapshot  Snapshot
	transport *e2ee.Transport
	decision  <-chan decision
	closeOnce sync.Once
}

func (session *Session) Snapshot() Snapshot {
	if session == nil {
		return Snapshot{}
	}
	return cloneSnapshot(session.snapshot)
}

// Encrypt serializes and encrypts an approval status with fixed associated data.
func (session *Session) Encrypt(approval *vibebridgev1.PairingApproval) ([]byte, error) {
	if session == nil || session.transport == nil || approval == nil {
		return nil, ErrFlowState
	}
	switch approval.Status {
	case vibebridgev1.PairingApprovalStatus_PAIRING_APPROVAL_STATUS_PENDING,
		vibebridgev1.PairingApprovalStatus_PAIRING_APPROVAL_STATUS_REJECTED:
		if approval.AuthorizationVersion != 0 {
			return nil, ErrInvalidFlow
		}
	case vibebridgev1.PairingApprovalStatus_PAIRING_APPROVAL_STATUS_APPROVED:
		if approval.AuthorizationVersion == 0 {
			return nil, ErrInvalidFlow
		}
	default:
		return nil, ErrInvalidFlow
	}
	plaintext, err := proto.MarshalOptions{Deterministic: true}.Marshal(approval)
	if err != nil {
		return nil, ErrInvalidFlow
	}
	return session.transport.Encrypt(plaintext, []byte(ApprovalAssociatedData))
}

// Wait returns the one local approval decision or cancellation reason.
func (session *Session) Wait(ctx context.Context) (*vibebridgev1.PairingApproval, error) {
	if session == nil || session.decision == nil || ctx == nil {
		return nil, ErrFlowState
	}
	select {
	case result := <-session.decision:
		if result.err != nil {
			return nil, result.err
		}
		return proto.Clone(result.approval).(*vibebridgev1.PairingApproval), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (session *Session) Close() {
	if session == nil {
		return
	}
	session.closeOnce.Do(func() {
		if session.transport != nil {
			session.transport.Close()
		}
	})
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	clone := snapshot
	clone.InvitationID = append([]byte(nil), snapshot.InvitationID...)
	clone.DeviceID = append([]byte(nil), snapshot.DeviceID...)
	return clone
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
