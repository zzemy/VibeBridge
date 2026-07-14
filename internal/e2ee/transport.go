package e2ee

import (
	"errors"
	"sync"

	"github.com/flynn/noise"
)

const maxTransportPlaintextBytes = noise.MaxMsgLen - 16

var ErrTransportClosed = errors.New("encrypted transport is closed")

// Transport owns independent direction-specific Noise cipher counters. Calls
// are serialized so counter use cannot race. Authentication failure closes both
// directions; callers must perform a fresh handshake rather than retry bytes at
// the same nonce.
type Transport struct {
	mu      sync.Mutex
	send    *noise.CipherState
	receive *noise.CipherState
}

func newTransport(send, receive *noise.CipherState) *Transport {
	return &Transport{send: send, receive: receive}
}

func (transport *Transport) Encrypt(plaintext, associatedData []byte) ([]byte, error) {
	if transport == nil {
		return nil, ErrTransportClosed
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.send == nil || transport.receive == nil {
		return nil, ErrTransportClosed
	}
	if len(plaintext) > maxTransportPlaintextBytes {
		return nil, errors.New("encrypted transport plaintext is too large")
	}
	return transport.send.Encrypt(nil, associatedData, plaintext)
}

func (transport *Transport) Decrypt(ciphertext, associatedData []byte) ([]byte, error) {
	if transport == nil {
		return nil, ErrTransportClosed
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.send == nil || transport.receive == nil {
		return nil, ErrTransportClosed
	}
	if len(ciphertext) < 16 || len(ciphertext) > noise.MaxMsgLen {
		transport.send = nil
		transport.receive = nil
		return nil, errors.New("encrypted transport ciphertext has invalid size")
	}
	plaintext, err := transport.receive.Decrypt(nil, associatedData, ciphertext)
	if err != nil {
		transport.send = nil
		transport.receive = nil
		return nil, errors.New("encrypted transport authentication failed")
	}
	return plaintext, nil
}

func (transport *Transport) SendCounter() (uint64, error) {
	if transport == nil {
		return 0, ErrTransportClosed
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.send == nil || transport.receive == nil {
		return 0, ErrTransportClosed
	}
	return transport.send.Nonce(), nil
}

func (transport *Transport) ReceiveCounter() (uint64, error) {
	if transport == nil {
		return 0, ErrTransportClosed
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.send == nil || transport.receive == nil {
		return 0, ErrTransportClosed
	}
	return transport.receive.Nonce(), nil
}

// Close prevents further use. Go and the Noise dependency do not guarantee
// deterministic heap zeroization, so callers must also discard all references.
func (transport *Transport) Close() {
	if transport == nil {
		return
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.send = nil
	transport.receive = nil
}
