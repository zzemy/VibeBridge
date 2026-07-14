package deviceidentity

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	DeviceIDBytes             = 16
	KeyAgreementBytes         = 32
	CurrentKeyVersion         = 1
	descriptorSignatureDomain = "VibeBridge device descriptor v1\x00"
	maxDisplayNameBytes       = 128
	maxPlatformBytes          = 32
)

// DescriptorOptions contains public, user-visible identity metadata.
type DescriptorOptions struct {
	DeviceID              []byte
	DisplayName           string
	Platform              string
	DeviceClass           vibebridgev1.DeviceClass
	SigningPublicKey      ed25519.PublicKey
	KeyAgreementPublicKey []byte
	CreatedAt             time.Time
	KeyVersion            uint32
	ProtocolMajor         uint32
	ProtocolMinor         uint32
}

func newSignedDescriptor(options DescriptorOptions, signingKey ed25519.PrivateKey) (*vibebridgev1.SignedDeviceDescriptor, error) {
	descriptor := &vibebridgev1.DeviceDescriptor{
		DeviceId:              append([]byte(nil), options.DeviceID...),
		DisplayName:           options.DisplayName,
		Platform:              options.Platform,
		DeviceClass:           options.DeviceClass,
		SigningPublicKey:      append([]byte(nil), options.SigningPublicKey...),
		KeyAgreementPublicKey: append([]byte(nil), options.KeyAgreementPublicKey...),
		CreatedAt:             timestamppb.New(options.CreatedAt.UTC()),
		KeyVersion:            options.KeyVersion,
		SupportedVersions: &vibebridgev1.ProtocolVersionRange{
			Minimum: &vibebridgev1.ProtocolVersion{Major: options.ProtocolMajor, Minor: options.ProtocolMinor},
			Maximum: &vibebridgev1.ProtocolVersion{Major: options.ProtocolMajor, Minor: options.ProtocolMinor},
		},
	}
	if err := ValidateDescriptor(descriptor); err != nil {
		return nil, err
	}
	if len(signingKey) != ed25519.PrivateKeySize || !signingKey.Public().(ed25519.PublicKey).Equal(options.SigningPublicKey) {
		return nil, errors.New("descriptor signing key does not match its public key")
	}
	message, err := descriptorSignatureMessage(descriptor)
	if err != nil {
		return nil, err
	}
	return &vibebridgev1.SignedDeviceDescriptor{DeviceDescriptor: descriptor, Signature: ed25519.Sign(signingKey, message)}, nil
}

// VerifySignedDescriptor validates all public bounds and its Ed25519 signature.
func VerifySignedDescriptor(signed *vibebridgev1.SignedDeviceDescriptor) error {
	if signed == nil || signed.DeviceDescriptor == nil {
		return errors.New("signed device descriptor is required")
	}
	if err := ValidateDescriptor(signed.DeviceDescriptor); err != nil {
		return err
	}
	if len(signed.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("device descriptor signature must be %d bytes", ed25519.SignatureSize)
	}
	message, err := descriptorSignatureMessage(signed.DeviceDescriptor)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(signed.DeviceDescriptor.SigningPublicKey), message, signed.Signature) {
		return errors.New("device descriptor signature is invalid")
	}
	return nil
}

func descriptorSignatureMessage(descriptor *vibebridgev1.DeviceDescriptor) ([]byte, error) {
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(descriptor)
	if err != nil {
		return nil, fmt.Errorf("encode device descriptor: %w", err)
	}
	message := make([]byte, 0, len(descriptorSignatureDomain)+len(encoded))
	message = append(message, descriptorSignatureDomain...)
	message = append(message, encoded...)
	return message, nil
}

// ValidateDescriptor checks the canonical identity statement without granting access.
func ValidateDescriptor(descriptor *vibebridgev1.DeviceDescriptor) error {
	if descriptor == nil {
		return errors.New("device descriptor is required")
	}
	if len(descriptor.DeviceId) != DeviceIDBytes {
		return fmt.Errorf("device ID must be %d bytes", DeviceIDBytes)
	}
	if len(descriptor.SigningPublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("device signing public key must be %d bytes", ed25519.PublicKeySize)
	}
	if len(descriptor.KeyAgreementPublicKey) != KeyAgreementBytes {
		return fmt.Errorf("device key-agreement public key must be %d bytes", KeyAgreementBytes)
	}
	if _, err := ecdh.X25519().NewPublicKey(descriptor.KeyAgreementPublicKey); err != nil {
		return errors.New("device key-agreement public key is invalid")
	}
	if strings.TrimSpace(descriptor.DisplayName) != descriptor.DisplayName || descriptor.DisplayName == "" || len(descriptor.DisplayName) > maxDisplayNameBytes {
		return errors.New("device display name is invalid")
	}
	if strings.TrimSpace(descriptor.Platform) != descriptor.Platform || descriptor.Platform == "" || len(descriptor.Platform) > maxPlatformBytes {
		return errors.New("device platform is invalid")
	}
	if descriptor.DeviceClass != vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT && descriptor.DeviceClass != vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT {
		return errors.New("device class is invalid")
	}
	if descriptor.CreatedAt == nil || !descriptor.CreatedAt.IsValid() || descriptor.CreatedAt.AsTime().IsZero() {
		return errors.New("device creation time is invalid")
	}
	if descriptor.KeyVersion == 0 {
		return errors.New("device key version must be positive")
	}
	versions := descriptor.SupportedVersions
	if versions == nil || versions.Minimum == nil || versions.Maximum == nil {
		return errors.New("device protocol version range is required")
	}
	if versions.Minimum.Major == 0 || versions.Minimum.Major != versions.Maximum.Major || versions.Minimum.Minor > versions.Maximum.Minor {
		return errors.New("device protocol version range is invalid")
	}
	return nil
}

// Fingerprint returns a short display-only fingerprint of a signed descriptor.
func Fingerprint(signed *vibebridgev1.SignedDeviceDescriptor) (string, error) {
	if err := VerifySignedDescriptor(signed); err != nil {
		return "", err
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(signed.DeviceDescriptor)
	if err != nil {
		return "", fmt.Errorf("encode device descriptor: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return strings.ToUpper(hex.EncodeToString(digest[:6])), nil
}

func generateState(options Options) (persistedState, error) {
	random := options.Random
	if random == nil {
		random = rand.Reader
	}
	deviceID := make([]byte, DeviceIDBytes)
	if _, err := io.ReadFull(random, deviceID); err != nil {
		return persistedState{}, fmt.Errorf("generate device ID: %w", err)
	}
	_, signingPrivateKey, err := ed25519.GenerateKey(random)
	if err != nil {
		return persistedState{}, fmt.Errorf("generate signing key: %w", err)
	}
	keyAgreementPrivateKey, err := ecdh.X25519().GenerateKey(random)
	if err != nil {
		return persistedState{}, fmt.Errorf("generate key-agreement key: %w", err)
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	displayName := strings.TrimSpace(options.DisplayName)
	if displayName == "" {
		displayName = "VibeBridge Agent"
	}
	platform := strings.TrimSpace(options.Platform)
	if platform == "" {
		platform = runtime.GOOS
	}
	return persistedState{
		Version:                currentStateVersion,
		DeviceID:               deviceID,
		SigningPrivateKey:      append([]byte(nil), signingPrivateKey...),
		KeyAgreementPrivateKey: keyAgreementPrivateKey.Bytes(),
		DisplayName:            displayName,
		Platform:               platform,
		DeviceClass:            int32(vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT),
		CreatedAt:              now().UTC(),
		KeyVersion:             CurrentKeyVersion,
	}, nil
}
