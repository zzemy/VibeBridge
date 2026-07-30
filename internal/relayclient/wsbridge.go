package relayclient

import (
	"errors"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
)

// BridgeWebSocket bridges a local WebSocket connection to a relay Stream,
// preserving WebSocket message boundaries. Unlike Bridge (which uses io.Copy
// and treats both sides as byte streams), BridgeWebSocket reads one complete
// WebSocket message from each side and writes it as one message to the other.
//
// This is required when the local side is a WebSocket server: the V1 protocol
// sends one protobuf envelope per WebSocket binary message, and message
// boundaries must be preserved end-to-end through the relay.
//
// BridgeWebSocket blocks until either side closes or errors. It closes both
// connections before returning. The returned error is the first non-benign
// error from either side, or nil if both closed cleanly.
func BridgeWebSocket(local *websocket.Conn, remote *Stream) error {
	if local == nil {
		return errors.New("relay wsbridge: local must not be nil")
	}
	if remote == nil {
		return errors.New("relay wsbridge: remote must not be nil")
	}

	var (
		wg    sync.WaitGroup
		errCh = make(chan error, 2)
	)

	wg.Add(2)

	// local → remote: read WebSocket messages, write to relay Stream.
	go func() {
		defer wg.Done()
		for {
			messageType, payload, err := local.ReadMessage()
			if err != nil {
				errCh <- fmt.Errorf("local read: %w", err)
				_ = remote.Close()
				return
			}
			if messageType != websocket.BinaryMessage {
				continue
			}
			if _, err := remote.Write(payload); err != nil {
				errCh <- fmt.Errorf("remote write: %w", err)
				_ = local.Close()
				return
			}
		}
	}()

	// remote → local: read relay Stream messages, write as WebSocket messages.
	go func() {
		defer wg.Done()
		buf := make([]byte, MaxFrameBytes)
		for {
			n, err := remote.Read(buf)
			if err != nil {
				errCh <- fmt.Errorf("remote read: %w", err)
				_ = local.Close()
				return
			}
			if err := local.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
				errCh <- fmt.Errorf("local write: %w", err)
				_ = remote.Close()
				return
			}
		}
	}()

	wg.Wait()
	close(errCh)

	var first error
	for err := range errCh {
		if isBenignClose(err) {
			continue
		}
		if first == nil {
			first = err
		}
	}
	if first != nil {
		return fmt.Errorf("relay wsbridge: %w", first)
	}
	return nil
}
