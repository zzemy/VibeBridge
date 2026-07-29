package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/deviceidentity"
)

const (
	pairedDeviceHeader         = "VibeBridge-Device"
	pairedDeviceSignatureHeader = "VibeBridge-Device-Signature"
	pairedSessionNonceHeader    = "VibeBridge-Session-Nonce"
	sessionNonceBytes          = 32
	sessionNonceDefaultTTL     = 30 * time.Second
	sessionSignatureDomain     = "VibeBridge session upgrade v1\x00"
	maxActiveSessionNonces     = 4096
)

var (
	errPairedHeadersMissing   = errors.New("paired session headers are required")
	errPairedDeviceMalformed  = errors.New("paired session device header is malformed")
	errPairedSignatureMalformed = errors.New("paired session signature is malformed")
	errPairedNonceMalformed   = errors.New("paired session nonce is malformed")
	errPairedNonceUnknown     = errors.New("paired session nonce is unknown or expired")
	errPairedNonceReplayed    = errors.New("paired session nonce has already been used")
	errPairedNonceRemote      = errors.New("paired session nonce was not issued for this host")
	errPairedDeviceUnknown    = errors.New("paired session device is not authorized")
	errPairedDeviceRevoked    = errors.New("paired session device has been revoked")
	errPairedSignatureInvalid = errors.New("paired session device signature is invalid")
)

// nonceStore issues single-use nonces, each bound to a remote address and TTL.
type nonceStore struct {
	mu    sync.Mutex
	now   func() time.Time
	rand  io.Reader
	ttl   time.Duration
	items map[string]nonceRecord
}

type nonceRecord struct {
	expiresAt time.Time
	host      string
	consumed  bool
}

func newNonceStore(now func() time.Time, ttl time.Duration) *nonceStore {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = sessionNonceDefaultTTL
	}
	return &nonceStore{now: now, rand: rand.Reader, ttl: ttl, items: make(map[string]nonceRecord)}
}

func (s *nonceStore) issue(remoteAddr string) (nonceHex string, expiresIn time.Duration, err error) {
	raw := make([]byte, sessionNonceBytes)
	if _, readErr := io.ReadFull(s.rand, raw); readErr != nil {
		return "", 0, readErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictLocked(s.now())
	if len(s.items) >= maxActiveSessionNonces {
		return "", 0, errors.New("paired session nonce store is full")
	}
	key := hex.EncodeToString(raw)
	s.items[key] = nonceRecord{expiresAt: s.now().Add(s.ttl), host: hostFromAddr(remoteAddr)}
	return key, s.ttl, nil
}

func (s *nonceStore) consume(nonceHex, remoteAddr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictLocked(s.now())
	rec, ok := s.items[nonceHex]
	if !ok {
		return errPairedNonceUnknown
	}
	if rec.consumed {
		return errPairedNonceReplayed
	}
	if rec.host != hostFromAddr(remoteAddr) {
		return errPairedNonceRemote
	}
	rec.consumed = true
	s.items[nonceHex] = rec
	return nil
}

func (s *nonceStore) evictLocked(now time.Time) {
	for key, record := range s.items {
		if record.expiresAt.Before(now) {
			delete(s.items, key)
		}
	}
}

// hostFromAddr strips the port from a remote address so that a single-use nonce
// remains valid for the browser that fetched it, even when the WebSocket upgrade
// opens a fresh TCP connection with a different ephemeral source port. The host
// comparison is sufficient: a different network host never receives the nonce in
// the first place, and the same host is allowed to open multiple parallel upgrades.
func hostFromAddr(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// pairedSessionGate binds a WebSocket upgrade to an authorized paired device.
type pairedSessionGate struct {
	devices *deviceidentity.Store
	nonces  *nonceStore
	now     func() time.Time
}

func newPairedSessionGate(devices *deviceidentity.Store, nonces *nonceStore) *pairedSessionGate {
	return &pairedSessionGate{devices: devices, nonces: nonces, now: time.Now}
}

// pairedSessionResult is the identity binding for an accepted upgrade.
type pairedSessionResult struct {
	DeviceID    []byte
	Fingerprint string
	Epoch       uint64
}

func (g *pairedSessionGate) verify(r *http.Request) (*pairedSessionResult, error) {
	if g == nil || g.devices == nil || g.nonces == nil {
		return nil, errPairedHeadersMissing
	}
	deviceHex := r.Header.Get(pairedDeviceHeader)
	nonceHex := r.Header.Get(pairedSessionNonceHeader)
	signatureB64 := r.Header.Get(pairedDeviceSignatureHeader)
	if deviceHex == "" || nonceHex == "" || signatureB64 == "" {
		return nil, errPairedHeadersMissing
	}
	deviceID, err := hex.DecodeString(deviceHex)
	if err != nil || len(deviceID) != deviceidentity.DeviceIDBytes {
		return nil, errPairedDeviceMalformed
	}
	if len(nonceHex) != sessionNonceBytes*2 {
		return nil, errPairedNonceMalformed
	}
	if _, err := hex.DecodeString(nonceHex); err != nil {
		return nil, errPairedNonceMalformed
	}
	signature, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, errPairedSignatureMalformed
	}
	if err := g.nonces.consume(nonceHex, r.RemoteAddr); err != nil {
		return nil, err
	}
	authorized, err := g.devices.AuthorizedDevice(deviceID)
	if err != nil {
		return nil, errPairedDeviceUnknown
	}
	if authorized.State != vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_AUTHORIZED {
		return nil, errPairedDeviceRevoked
	}
	message := sessionSignatureMessage(nonceHex)
	if !ed25519.Verify(authorized.Device.DeviceDescriptor.SigningPublicKey, message, signature) {
		return nil, errPairedSignatureInvalid
	}
	fingerprint, err := deviceidentity.Fingerprint(authorized.Device)
	if err != nil {
		return nil, errPairedSignatureInvalid
	}
	return &pairedSessionResult{
		DeviceID:    append([]byte(nil), deviceID...),
		Fingerprint: fingerprint,
		Epoch:       g.devices.RevocationEpoch(),
	}, nil
}

func sessionSignatureMessage(nonceHex string) []byte {
	decoded, _ := hex.DecodeString(nonceHex)
	message := make([]byte, 0, len(sessionSignatureDomain)+len(decoded))
	message = append(message, sessionSignatureDomain...)
	message = append(message, decoded...)
	return message
}

// handleSessionNonce issues a 30s single-use nonce bound to the upgrade remote address.
func (s *Server) handleSessionNonce(w http.ResponseWriter, r *http.Request) {
	if s.gate == nil || s.gate.nonces == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "paired session is not configured"})
		return
	}
	nonce, expiresIn, err := s.gate.nonces.issue(r.RemoteAddr)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nonce":      nonce,
		"expires_in": int64(expiresIn.Seconds()),
	})
}

// signSessionChallenge returns the canonical signature for a session nonce.
// Exposed for test clients and the tray management page.
func signSessionChallenge(signingKey ed25519.PrivateKey, nonceHex string) ([]byte, error) {
	if len(signingKey) != ed25519.PrivateKeySize {
		return nil, errors.New("session signing key is invalid")
	}
	decoded, err := hex.DecodeString(nonceHex)
	if err != nil || len(decoded) != sessionNonceBytes {
		return nil, errors.New("session nonce is malformed")
	}
	return ed25519.Sign(signingKey, sessionSignatureMessage(nonceHex)), nil
}
