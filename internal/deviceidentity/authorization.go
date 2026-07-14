package deviceidentity

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"google.golang.org/protobuf/proto"
)

var ErrDeviceNotFound = errors.New("authorized device not found")

// AuthorizedDevices returns defensive public views in stable authorization order.
func (store *Store) AuthorizedDevices(includeRevoked bool) ([]*vibebridgev1.AuthorizedDevice, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	devices := make([]*vibebridgev1.AuthorizedDevice, 0, len(store.state.Authorized))
	for _, record := range store.state.Authorized {
		if !includeRevoked && vibebridgev1.DeviceAuthorizationState(record.State) == vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_REVOKED {
			continue
		}
		view, err := authorizationView(record)
		if err != nil {
			return nil, errors.New("decode authorized device")
		}
		devices = append(devices, view)
	}
	return devices, nil
}

// AuthorizedDevice returns one record by opaque device ID.
func (store *Store) AuthorizedDevice(deviceID []byte) (*vibebridgev1.AuthorizedDevice, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, record := range store.state.Authorized {
		view, err := authorizationView(record)
		if err != nil {
			return nil, errors.New("decode authorized device")
		}
		if bytes.Equal(view.Device.DeviceDescriptor.DeviceId, deviceID) {
			return view, nil
		}
	}
	return nil, ErrDeviceNotFound
}

// Authorize records an explicitly approved, signed client descriptor.
// Repeating an identical active authorization is idempotent.
func (store *Store) Authorize(signed *vibebridgev1.SignedDeviceDescriptor) (*vibebridgev1.AuthorizedDevice, error) {
	if err := VerifySignedDescriptor(signed); err != nil {
		return nil, err
	}
	if signed.DeviceDescriptor.DeviceClass != vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT {
		return nil, errors.New("only client devices can be authorized")
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(signed)
	if err != nil {
		return nil, fmt.Errorf("encode authorized device: %w", err)
	}
	if len(encoded) > maxSignedDescriptorBytes {
		return nil, errors.New("signed device descriptor is too large")
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if bytes.Equal(signed.DeviceDescriptor.DeviceId, store.state.DeviceID) {
		return nil, errors.New("Agent cannot authorize its own device ID")
	}

	next := cloneState(store.state)
	index := -1
	for candidate := range next.Authorized {
		existing, viewErr := authorizationView(next.Authorized[candidate])
		if viewErr != nil {
			zeroState(&next)
			return nil, errors.New("decode existing authorized device")
		}
		if !bytes.Equal(existing.Device.DeviceDescriptor.DeviceId, signed.DeviceDescriptor.DeviceId) {
			continue
		}
		index = candidate
		if !bytes.Equal(existing.Device.DeviceDescriptor.SigningPublicKey, signed.DeviceDescriptor.SigningPublicKey) {
			zeroState(&next)
			return nil, errors.New("device ID is already bound to a different signing key")
		}
		if existing.State == vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_AUTHORIZED && proto.Equal(existing.Device, signed) {
			zeroState(&next)
			return existing, nil
		}
		if signed.DeviceDescriptor.KeyVersion < existing.Device.DeviceDescriptor.KeyVersion {
			zeroState(&next)
			return nil, errors.New("device descriptor key version cannot move backwards")
		}
		break
	}
	if index < 0 && len(next.Authorized) >= maxAuthorizedDevices {
		zeroState(&next)
		return nil, fmt.Errorf("cannot authorize more than %d devices", maxAuthorizedDevices)
	}

	next.AuthorizationCounter++
	if next.AuthorizationCounter == 0 {
		zeroState(&next)
		return nil, errors.New("authorization version is exhausted")
	}
	now := store.now().UTC()
	record := persistedAuthorization{
		Device:               append([]byte(nil), encoded...),
		AuthorizationVersion: next.AuthorizationCounter,
		State:                int32(vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_AUTHORIZED),
		AuthorizedAt:         now,
	}
	if index < 0 {
		next.Authorized = append(next.Authorized, record)
		index = len(next.Authorized) - 1
	} else {
		zeroBytes(next.Authorized[index].Device)
		next.Authorized[index] = record
	}
	if err := validateState(&next); err != nil {
		zeroState(&next)
		return nil, fmt.Errorf("validate authorization update: %w", err)
	}
	if err := saveState(store.path, &next); err != nil {
		zeroState(&next)
		return nil, fmt.Errorf("persist authorization update: %w", err)
	}
	view, err := authorizationView(next.Authorized[index])
	if err != nil {
		zeroState(&next)
		return nil, errors.New("decode persisted authorization")
	}
	zeroState(&store.state)
	store.state = next
	return view, nil
}

// Revoke immediately removes a client's authority and advances the revocation epoch.
// Repeating a revocation is idempotent.
func (store *Store) Revoke(deviceID []byte) (*vibebridgev1.AuthorizedDevice, error) {
	if len(deviceID) != DeviceIDBytes {
		return nil, fmt.Errorf("device ID must be %d bytes", DeviceIDBytes)
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	next := cloneState(store.state)
	index := -1
	for candidate := range next.Authorized {
		view, err := authorizationView(next.Authorized[candidate])
		if err != nil {
			zeroState(&next)
			return nil, errors.New("decode existing authorized device")
		}
		if bytes.Equal(view.Device.DeviceDescriptor.DeviceId, deviceID) {
			index = candidate
			if view.State == vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_REVOKED {
				zeroState(&next)
				return view, nil
			}
			break
		}
	}
	if index < 0 {
		zeroState(&next)
		return nil, ErrDeviceNotFound
	}
	if next.AuthorizationCounter == ^uint64(0) || next.RevocationEpoch == ^uint64(0) {
		zeroState(&next)
		return nil, errors.New("authorization revocation counters are exhausted")
	}
	next.AuthorizationCounter++
	next.RevocationEpoch++
	now := store.now().UTC()
	record := &next.Authorized[index]
	record.AuthorizationVersion = next.AuthorizationCounter
	record.State = int32(vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_REVOKED)
	record.RevokedAt = timePointer(now)
	record.RevocationEpoch = next.RevocationEpoch
	if err := validateState(&next); err != nil {
		zeroState(&next)
		return nil, fmt.Errorf("validate revocation update: %w", err)
	}
	if err := saveState(store.path, &next); err != nil {
		zeroState(&next)
		return nil, fmt.Errorf("persist revocation update: %w", err)
	}
	view, err := authorizationView(*record)
	if err != nil {
		zeroState(&next)
		return nil, errors.New("decode persisted revocation")
	}
	zeroState(&store.state)
	store.state = next
	return view, nil
}

func timePointer(value time.Time) *time.Time { return &value }
