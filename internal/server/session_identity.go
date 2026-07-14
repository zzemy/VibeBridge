package server

import "crypto/rand"

const protocolSessionIDBytes = 16

// newProtocolSessionID is intentionally separate from the logging correlation
// ID: protocol identity is returned to clients, while correlation IDs are not.
func newProtocolSessionID() ([]byte, error) {
	value := make([]byte, protocolSessionIDBytes)
	if _, err := rand.Read(value); err != nil {
		return nil, err
	}
	return value, nil
}
