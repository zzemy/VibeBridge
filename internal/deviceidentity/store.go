package deviceidentity

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/protocol"
	"github.com/zzemy/VibeBridge/internal/securestore"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	currentStateVersion      = 1
	secureStorePurpose       = "vibebridge-device-identity-v1"
	maxAuthorizedDevices     = 256
	maxSignedDescriptorBytes = 64 * 1024
)

var loadCreateLock sync.Mutex

// Options configures first-run identity creation. Existing identity metadata wins.
type Options struct {
	Path        string
	DisplayName string
	Platform    string
	Now         func() time.Time
	Random      io.Reader
}

// Store owns one durable Agent identity and its local client authorization graph.
type Store struct {
	mu    sync.RWMutex
	path  string
	state persistedState
	now   func() time.Time
}

type persistedState struct {
	Version                int                      `json:"version"`
	DeviceID               []byte                   `json:"device_id"`
	SigningPrivateKey      []byte                   `json:"signing_private_key"`
	KeyAgreementPrivateKey []byte                   `json:"key_agreement_private_key"`
	DisplayName            string                   `json:"display_name"`
	Platform               string                   `json:"platform"`
	DeviceClass            int32                    `json:"device_class"`
	CreatedAt              time.Time                `json:"created_at"`
	KeyVersion             uint32                   `json:"key_version"`
	AuthorizationCounter   uint64                   `json:"authorization_counter"`
	RevocationEpoch        uint64                   `json:"revocation_epoch"`
	Authorized             []persistedAuthorization `json:"authorized"`
}

type persistedAuthorization struct {
	Device               []byte     `json:"device"`
	AuthorizationVersion uint64     `json:"authorization_version"`
	State                int32      `json:"state"`
	AuthorizedAt         time.Time  `json:"authorized_at"`
	RevokedAt            *time.Time `json:"revoked_at,omitempty"`
	RevocationEpoch      uint64     `json:"revocation_epoch"`
}

// LoadOrCreate opens an existing identity or atomically creates the first one.
// Any existing corrupt or undecryptable state fails closed and is never replaced.
func LoadOrCreate(options Options) (*Store, error) {
	if !filepath.IsAbs(options.Path) {
		return nil, errors.New("device identity path must be absolute")
	}
	loadCreateLock.Lock()
	defer loadCreateLock.Unlock()

	release, err := acquireCreationLock(options.Path)
	if err != nil {
		return nil, err
	}
	state, stateErr := loadOrCreateState(options)
	releaseErr := release()
	if stateErr != nil {
		return nil, stateErr
	}
	if releaseErr != nil {
		zeroState(&state)
		return nil, releaseErr
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Store{path: options.Path, state: state, now: now}, nil
}

func loadOrCreateState(options Options) (persistedState, error) {
	state, err := loadState(options.Path)
	if errors.Is(err, os.ErrNotExist) {
		state, err = generateState(options)
		if err != nil {
			return persistedState{}, err
		}
		if err := validateState(&state); err != nil {
			zeroState(&state)
			return persistedState{}, fmt.Errorf("validate new device identity: %w", err)
		}
		if err := saveState(options.Path, &state); err != nil {
			zeroState(&state)
			return persistedState{}, fmt.Errorf("persist new device identity: %w", err)
		}
		return state, nil
	}
	if err != nil {
		return persistedState{}, fmt.Errorf("load device identity: %w", err)
	}
	return state, nil
}

func loadState(path string) (persistedState, error) {
	encoded, err := securestore.Read(path, secureStorePurpose)
	if err != nil {
		return persistedState{}, err
	}
	defer zeroBytes(encoded)
	var state persistedState
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return persistedState{}, fmt.Errorf("decode protected identity state: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return persistedState{}, errors.New("decode protected identity state: multiple JSON values are not allowed")
		}
		return persistedState{}, fmt.Errorf("decode protected identity state: %w", err)
	}
	if err := validateState(&state); err != nil {
		zeroState(&state)
		return persistedState{}, err
	}
	return state, nil
}

func saveState(path string, state *persistedState) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode protected identity state: %w", err)
	}
	defer zeroBytes(encoded)
	return securestore.Write(path, secureStorePurpose, encoded)
}

func validateState(state *persistedState) error {
	if state.Version != currentStateVersion {
		return fmt.Errorf("unsupported device identity version %d", state.Version)
	}
	if len(state.DeviceID) != DeviceIDBytes {
		return fmt.Errorf("stored device ID must be %d bytes", DeviceIDBytes)
	}
	if len(state.SigningPrivateKey) != ed25519.PrivateKeySize {
		return errors.New("stored signing private key has an invalid size")
	}
	derivedSigningKey := ed25519.NewKeyFromSeed(state.SigningPrivateKey[:ed25519.SeedSize])
	signingKeyMatches := bytes.Equal(derivedSigningKey, state.SigningPrivateKey)
	zeroBytes(derivedSigningKey)
	if !signingKeyMatches {
		return errors.New("stored signing private key is inconsistent")
	}
	if len(state.KeyAgreementPrivateKey) != KeyAgreementBytes {
		return errors.New("stored key-agreement private key has an invalid size")
	}
	if _, err := ecdh.X25519().NewPrivateKey(state.KeyAgreementPrivateKey); err != nil {
		return errors.New("stored key-agreement private key is invalid")
	}
	if state.DeviceClass != int32(vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT) {
		return errors.New("stored identity is not an Agent")
	}
	if state.CreatedAt.IsZero() || state.KeyVersion == 0 {
		return errors.New("stored identity metadata is invalid")
	}
	if len(state.Authorized) > maxAuthorizedDevices {
		return fmt.Errorf("stored identity has more than %d authorized devices", maxAuthorizedDevices)
	}

	store := Store{state: *state}
	if _, err := store.signedDescriptorLocked(); err != nil {
		return fmt.Errorf("stored Agent descriptor is invalid: %w", err)
	}
	seen := make(map[string]struct{}, len(state.Authorized))
	var highestVersion uint64
	for index := range state.Authorized {
		record := &state.Authorized[index]
		if len(record.Device) == 0 || len(record.Device) > maxSignedDescriptorBytes {
			return fmt.Errorf("authorized device %d has an invalid descriptor size", index)
		}
		signed := new(vibebridgev1.SignedDeviceDescriptor)
		if err := proto.Unmarshal(record.Device, signed); err != nil {
			return fmt.Errorf("decode authorized device %d: %w", index, err)
		}
		if err := VerifySignedDescriptor(signed); err != nil {
			return fmt.Errorf("authorized device %d is invalid: %w", index, err)
		}
		if signed.DeviceDescriptor.DeviceClass != vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT {
			return fmt.Errorf("authorized device %d is not a client", index)
		}
		key := string(signed.DeviceDescriptor.DeviceId)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("authorized device %d duplicates a device ID", index)
		}
		seen[key] = struct{}{}
		if record.AuthorizationVersion == 0 || record.AuthorizationVersion > state.AuthorizationCounter || record.AuthorizedAt.IsZero() {
			return fmt.Errorf("authorized device %d has invalid authorization metadata", index)
		}
		if record.AuthorizationVersion > highestVersion {
			highestVersion = record.AuthorizationVersion
		}
		switch vibebridgev1.DeviceAuthorizationState(record.State) {
		case vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_AUTHORIZED:
			if record.RevokedAt != nil || record.RevocationEpoch != 0 {
				return fmt.Errorf("authorized device %d has invalid revocation metadata", index)
			}
		case vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_REVOKED:
			if record.RevokedAt == nil || record.RevokedAt.IsZero() || record.RevocationEpoch == 0 || record.RevocationEpoch > state.RevocationEpoch {
				return fmt.Errorf("revoked device %d has invalid revocation metadata", index)
			}
		default:
			return fmt.Errorf("authorized device %d has invalid state", index)
		}
	}
	if highestVersion > state.AuthorizationCounter {
		return errors.New("stored authorization counter is invalid")
	}
	return nil
}

// Descriptor returns a defensive copy of the Agent's signed public descriptor.
func (store *Store) Descriptor() (*vibebridgev1.SignedDeviceDescriptor, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.signedDescriptorLocked()
}

func (store *Store) signedDescriptorLocked() (*vibebridgev1.SignedDeviceDescriptor, error) {
	signingKey := ed25519.PrivateKey(store.state.SigningPrivateKey)
	agreementKey, err := ecdh.X25519().NewPrivateKey(store.state.KeyAgreementPrivateKey)
	if err != nil {
		return nil, errors.New("load Agent key-agreement key")
	}
	return newSignedDescriptor(DescriptorOptions{
		DeviceID:              store.state.DeviceID,
		DisplayName:           store.state.DisplayName,
		Platform:              store.state.Platform,
		DeviceClass:           vibebridgev1.DeviceClass(store.state.DeviceClass),
		SigningPublicKey:      signingKey.Public().(ed25519.PublicKey),
		KeyAgreementPublicKey: agreementKey.PublicKey().Bytes(),
		CreatedAt:             store.state.CreatedAt,
		KeyVersion:            store.state.KeyVersion,
		ProtocolMajor:         protocol.CurrentMajor,
		ProtocolMinor:         protocol.CurrentMinor,
	}, signingKey)
}

// Sign signs a domain-separated protocol transcript without exporting the key.
func (store *Store) Sign(message []byte) []byte {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return ed25519.Sign(ed25519.PrivateKey(store.state.SigningPrivateKey), message)
}

// KeyAgreementPrivateKey returns a parsed copy for an authenticated handshake.
func (store *Store) KeyAgreementPrivateKey() (*ecdh.PrivateKey, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	key, err := ecdh.X25519().NewPrivateKey(store.state.KeyAgreementPrivateKey)
	if err != nil {
		return nil, errors.New("load Agent key-agreement key")
	}
	return key, nil
}

// RevocationEpoch returns the latest Agent-local revocation epoch.
func (store *Store) RevocationEpoch() uint64 {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.state.RevocationEpoch
}

// Close clears in-memory private key buffers. The Store must not be reused.
func (store *Store) Close() {
	store.mu.Lock()
	defer store.mu.Unlock()
	zeroState(&store.state)
}

func authorizationView(record persistedAuthorization) (*vibebridgev1.AuthorizedDevice, error) {
	signed := new(vibebridgev1.SignedDeviceDescriptor)
	if err := proto.Unmarshal(record.Device, signed); err != nil {
		return nil, err
	}
	view := &vibebridgev1.AuthorizedDevice{
		Device:               signed,
		AuthorizationVersion: record.AuthorizationVersion,
		State:                vibebridgev1.DeviceAuthorizationState(record.State),
		AuthorizedAt:         timestamppb.New(record.AuthorizedAt),
		RevocationEpoch:      record.RevocationEpoch,
	}
	if record.RevokedAt != nil {
		view.RevokedAt = timestamppb.New(*record.RevokedAt)
	}
	return view, nil
}

func cloneState(source persistedState) persistedState {
	clone := source
	clone.DeviceID = append([]byte(nil), source.DeviceID...)
	clone.SigningPrivateKey = append([]byte(nil), source.SigningPrivateKey...)
	clone.KeyAgreementPrivateKey = append([]byte(nil), source.KeyAgreementPrivateKey...)
	clone.Authorized = make([]persistedAuthorization, len(source.Authorized))
	for index, record := range source.Authorized {
		clone.Authorized[index] = record
		clone.Authorized[index].Device = append([]byte(nil), record.Device...)
		if record.RevokedAt != nil {
			value := *record.RevokedAt
			clone.Authorized[index].RevokedAt = &value
		}
	}
	return clone
}

func zeroState(state *persistedState) {
	zeroBytes(state.DeviceID)
	zeroBytes(state.SigningPrivateKey)
	zeroBytes(state.KeyAgreementPrivateKey)
	for index := range state.Authorized {
		zeroBytes(state.Authorized[index].Device)
	}
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
