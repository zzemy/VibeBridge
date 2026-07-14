package pairing

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/deviceidentity"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	CurrentInvitationVersion = 1
	InvitationIDBytes        = 16
	BootstrapSecretBytes     = 32
	DefaultTTL               = 5 * time.Minute
	maxConnectionHints       = 8
	maxConnectionHintBytes   = 512
	maxInvitationBytes       = 16 * 1024
)

var (
	ErrInvitationUnavailable = errors.New("pairing invitation is not active")
	ErrInvitationExpired     = errors.New("pairing invitation has expired")
	ErrInvitationRejected    = errors.New("pairing invitation credentials were rejected")
)

type descriptorSource interface {
	Descriptor() (*vibebridgev1.SignedDeviceDescriptor, error)
}

// Config supplies the Agent identity and bounded test seams for invitations.
type Config struct {
	Agent  descriptorSource
	TTL    time.Duration
	Now    func() time.Time
	Random io.Reader
}

// Manager owns the single active in-memory bootstrap capability.
type Manager struct {
	mu     sync.Mutex
	agent  descriptorSource
	ttl    time.Duration
	now    func() time.Time
	random io.Reader
	active *activeInvitation
}

type activeInvitation struct {
	invitation *vibebridgev1.PairingInvitation
	secret     []byte
}

// Status deliberately omits the bootstrap secret.
type Status struct {
	InvitationID     []byte
	CreatedAt        time.Time
	ExpiresAt        time.Time
	VerificationCode string
}

func New(config Config) (*Manager, error) {
	if config.Agent == nil {
		return nil, errors.New("pairing Agent identity is required")
	}
	ttl := config.TTL
	if ttl == 0 {
		ttl = DefaultTTL
	}
	if ttl < 30*time.Second || ttl > 15*time.Minute {
		return nil, errors.New("pairing invitation TTL must be between 30 seconds and 15 minutes")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	random := config.Random
	if random == nil {
		random = rand.Reader
	}
	return &Manager{agent: config.Agent, ttl: ttl, now: now, random: random}, nil
}

// Create supersedes any previous invitation and returns a defensive copy for QR encoding.
func (manager *Manager) Create(connectionHints []string) (*vibebridgev1.PairingInvitation, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	if err := validateConnectionHints(connectionHints); err != nil {
		return nil, err
	}
	descriptor, err := manager.agent.Descriptor()
	if err != nil {
		return nil, fmt.Errorf("read Agent descriptor: %w", err)
	}
	if err := deviceidentity.VerifySignedDescriptor(descriptor); err != nil {
		return nil, fmt.Errorf("verify Agent descriptor: %w", err)
	}
	if descriptor.DeviceDescriptor.DeviceClass != vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT {
		return nil, errors.New("pairing identity is not an Agent")
	}
	invitationID := make([]byte, InvitationIDBytes)
	secret := make([]byte, BootstrapSecretBytes)
	if _, err := io.ReadFull(manager.random, invitationID); err != nil {
		return nil, fmt.Errorf("generate pairing invitation ID: %w", err)
	}
	if _, err := io.ReadFull(manager.random, secret); err != nil {
		zero(secret)
		return nil, fmt.Errorf("generate pairing bootstrap secret: %w", err)
	}
	now := manager.now().UTC()
	invitation := &vibebridgev1.PairingInvitation{
		Version:         CurrentInvitationVersion,
		InvitationId:    invitationID,
		Agent:           descriptor,
		BootstrapSecret: append([]byte(nil), secret...),
		CreatedAt:       timestamppb.New(now),
		ExpiresAt:       timestamppb.New(now.Add(manager.ttl)),
		ConnectionHints: append([]string(nil), connectionHints...),
	}
	code, err := verificationCode(invitation)
	if err != nil {
		zero(secret)
		return nil, err
	}
	invitation.VerificationCode = code

	manager.clearActiveLocked()
	stored := proto.Clone(invitation).(*vibebridgev1.PairingInvitation)
	zero(stored.BootstrapSecret)
	stored.BootstrapSecret = nil
	manager.active = &activeInvitation{invitation: stored, secret: secret}
	return proto.Clone(invitation).(*vibebridgev1.PairingInvitation), nil
}

// ActiveStatus returns the current non-secret invitation state.
func (manager *Manager) ActiveStatus() (Status, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.active == nil {
		return Status{}, ErrInvitationUnavailable
	}
	if !manager.now().Before(manager.active.invitation.ExpiresAt.AsTime()) {
		manager.clearActiveLocked()
		return Status{}, ErrInvitationExpired
	}
	invitation := manager.active.invitation
	return Status{
		InvitationID:     append([]byte(nil), invitation.InvitationId...),
		CreatedAt:        invitation.CreatedAt.AsTime(),
		ExpiresAt:        invitation.ExpiresAt.AsTime(),
		VerificationCode: invitation.VerificationCode,
	}, nil
}

// Authenticate verifies bootstrap credentials without consuming Agent approval state.
func (manager *Manager) Authenticate(invitationID []byte, secret []byte) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.authenticateLocked(invitationID, secret)
}

// Consume atomically spends the invitation after the encrypted, approved pairing flow.
func (manager *Manager) Consume(invitationID []byte, secret []byte) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := manager.authenticateLocked(invitationID, secret); err != nil {
		return err
	}
	manager.clearActiveLocked()
	return nil
}

func (manager *Manager) authenticateLocked(invitationID []byte, secret []byte) error {
	if manager.active == nil {
		return ErrInvitationUnavailable
	}
	if !manager.now().Before(manager.active.invitation.ExpiresAt.AsTime()) {
		manager.clearActiveLocked()
		return ErrInvitationExpired
	}
	if len(invitationID) != InvitationIDBytes || len(secret) != BootstrapSecretBytes ||
		subtle.ConstantTimeCompare(invitationID, manager.active.invitation.InvitationId) != 1 ||
		subtle.ConstantTimeCompare(secret, manager.active.secret) != 1 {
		return ErrInvitationRejected
	}
	return nil
}

// Close clears any active bootstrap secret.
func (manager *Manager) Close() {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.clearActiveLocked()
}

func (manager *Manager) clearActiveLocked() {
	if manager.active == nil {
		return
	}
	zero(manager.active.secret)
	zero(manager.active.invitation.BootstrapSecret)
	manager.active = nil
}

// ValidateInvitation checks a scanned invitation before opening a transport.
func ValidateInvitation(invitation *vibebridgev1.PairingInvitation, now time.Time) error {
	if invitation == nil {
		return errors.New("pairing invitation is required")
	}
	if proto.Size(invitation) > maxInvitationBytes {
		return errors.New("pairing invitation is too large")
	}
	if invitation.Version != CurrentInvitationVersion {
		return fmt.Errorf("unsupported pairing invitation version %d", invitation.Version)
	}
	if len(invitation.InvitationId) != InvitationIDBytes || len(invitation.BootstrapSecret) != BootstrapSecretBytes {
		return errors.New("pairing invitation credentials have an invalid size")
	}
	if err := deviceidentity.VerifySignedDescriptor(invitation.Agent); err != nil {
		return fmt.Errorf("pairing Agent descriptor is invalid: %w", err)
	}
	if invitation.Agent.DeviceDescriptor.DeviceClass != vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT {
		return errors.New("pairing invitation descriptor is not an Agent")
	}
	if invitation.CreatedAt == nil || invitation.ExpiresAt == nil || !invitation.CreatedAt.IsValid() || !invitation.ExpiresAt.IsValid() {
		return errors.New("pairing invitation timestamps are invalid")
	}
	createdAt, expiresAt := invitation.CreatedAt.AsTime(), invitation.ExpiresAt.AsTime()
	if createdAt.After(now.UTC().Add(5 * time.Minute)) {
		return errors.New("pairing invitation creation time is too far in the future")
	}
	if !expiresAt.After(createdAt) || expiresAt.Sub(createdAt) > 15*time.Minute {
		return errors.New("pairing invitation lifetime is invalid")
	}
	if !now.UTC().Before(expiresAt) {
		return ErrInvitationExpired
	}
	if err := validateConnectionHints(invitation.ConnectionHints); err != nil {
		return err
	}
	expected, err := verificationCode(invitation)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(expected), []byte(invitation.VerificationCode)) != 1 {
		return errors.New("pairing invitation verification code is invalid")
	}
	return nil
}

// FragmentURL encodes the secret invitation entirely after '#', outside HTTP logs and referrers.
func FragmentURL(baseURL string, invitation *vibebridgev1.PairingInvitation) (string, error) {
	if invitation == nil || invitation.CreatedAt == nil {
		return "", errors.New("pairing invitation is required")
	}
	if err := ValidateInvitation(invitation, invitation.CreatedAt.AsTime()); err != nil {
		return "", err
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("pairing base URL must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("pairing base URL must not contain credentials, query, or fragment")
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(invitation)
	if err != nil {
		return "", fmt.Errorf("encode pairing invitation: %w", err)
	}
	parsed.Fragment = "/pair/v1/" + base64.RawURLEncoding.EncodeToString(encoded)
	return parsed.String(), nil
}

// ParseFragmentURL decodes and validates a QR URL at scan time.
func ParseFragmentURL(value string, now time.Time) (*vibebridgev1.PairingInvitation, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" {
		return nil, errors.New("pairing URL is invalid")
	}
	const prefix = "/pair/v1/"
	if !strings.HasPrefix(parsed.Fragment, prefix) {
		return nil, errors.New("pairing URL fragment is invalid")
	}
	payload := strings.TrimPrefix(parsed.Fragment, prefix)
	if len(payload) > base64.RawURLEncoding.EncodedLen(maxInvitationBytes) {
		return nil, errors.New("pairing URL payload is too large")
	}
	encoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, errors.New("pairing URL payload is invalid")
	}
	invitation := new(vibebridgev1.PairingInvitation)
	if err := proto.Unmarshal(encoded, invitation); err != nil {
		return nil, errors.New("pairing URL payload is invalid")
	}
	if err := ValidateInvitation(invitation, now); err != nil {
		return nil, err
	}
	return invitation, nil
}

func verificationCode(invitation *vibebridgev1.PairingInvitation) (string, error) {
	clone := proto.Clone(invitation).(*vibebridgev1.PairingInvitation)
	clone.VerificationCode = ""
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(clone)
	if err != nil {
		return "", fmt.Errorf("encode pairing verification transcript: %w", err)
	}
	digest := sha256.Sum256(append([]byte("VibeBridge pairing invitation v1\x00"), encoded...))
	value := (uint32(digest[0])<<16 | uint32(digest[1])<<8 | uint32(digest[2])) % 1_000_000
	return fmt.Sprintf("%03d-%03d", value/1000, value%1000), nil
}

func validateConnectionHints(hints []string) error {
	if len(hints) > maxConnectionHints {
		return fmt.Errorf("pairing invitation has more than %d connection hints", maxConnectionHints)
	}
	seen := make(map[string]struct{}, len(hints))
	for _, hint := range hints {
		if hint == "" || len(hint) > maxConnectionHintBytes || strings.TrimSpace(hint) != hint {
			return errors.New("pairing invitation contains an invalid connection hint")
		}
		parsed, err := url.Parse(hint)
		if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" ||
			(parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "ws" && parsed.Scheme != "wss") {
			return errors.New("pairing invitation contains an invalid connection hint")
		}
		if _, exists := seen[hint]; exists {
			return errors.New("pairing invitation contains a duplicate connection hint")
		}
		seen[hint] = struct{}{}
	}
	return nil
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
