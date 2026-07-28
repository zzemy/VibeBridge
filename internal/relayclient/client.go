package relayclient

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
)

const (
	// DefaultDialTimeout bounds the WebSocket upgrade. It is short enough
	// to fail fast on a misconfigured relay but long enough to ride out
	// a transient mobile network blip.
	DefaultDialTimeout = 15 * time.Second
	// DefaultWriteTimeout bounds a single WebSocket frame write so a
	// stuck peer cannot pin the writer indefinitely.
	DefaultWriteTimeout = 5 * time.Second
	// MaxFrameBytes is the largest single binary frame the Stream will
	// accept from the relay. The relay server caps writes at 1 MiB; we
	// cap reads at the same size to keep both sides symmetric.
	MaxFrameBytes = 1 * 1024 * 1024
	// readPumpBuffer is the per-Stream channel depth. Four slots is
	// enough to keep a small interactive pipe flowing without giving a
	// single slow consumer an unbounded queue to fill.
	readPumpBuffer = 4
)

// ErrFrameTooLarge is returned by Stream.Read when the next forwarded
// frame is larger than the caller's buffer. The frame is dropped; the
// caller must retry with a larger buffer.
var ErrFrameTooLarge = errors.New("relay frame is larger than the caller's buffer")

// Dialer configures an outbound connection. The zero value is valid
// and uses the package defaults.
type Dialer struct {
	// DialTimeout bounds the WebSocket handshake. Zero falls back to
	// DefaultDialTimeout. The negotiated session is not bounded by
	// this value; callers use ctx to control that.
	DialTimeout time.Duration
	// WriteTimeout bounds a single frame write. Zero falls back to
	// DefaultWriteTimeout.
	WriteTimeout time.Duration
	// HTTPHeader is appended to the upgrade request verbatim.
	// Callers typically leave it nil.
	HTTPHeader http.Header
}

// defaultDialer is used by the package-level Dial helper.
var defaultDialer = Dialer{}

// Dial dials the relay at url with a short-lived Agent ticket and
// returns the Stream for the resulting route. The supplied ticket is
// the protobuf-marshalled RelayTicket for the Agent endpoint; the
// client prepends the 4-byte big-endian length prefix the relay
// expects on the first frame.
//
// ctx cancels the dial. Once the dial succeeds, the returned Stream
// stays open until Stream.Close is called or the relay closes the
// underlying WebSocket; ctx cancellation does not by itself close the
// Stream.
func Dial(ctx context.Context, target string, ticket *vibebridgev1.RelayTicket) (*Stream, error) {
	return defaultDialer.Dial(ctx, target, ticket)
}

// Dial is the configured variant of the package-level Dial.
func (d Dialer) Dial(ctx context.Context, target string, ticket *vibebridgev1.RelayTicket) (*Stream, error) {
	if ticket == nil {
		return nil, errors.New("relay ticket must not be nil")
	}
	if len(ticket.RouteId) == 0 {
		return nil, errors.New("relay ticket is missing a route id")
	}
	encoded, err := proto.Marshal(ticket)
	if err != nil {
		return nil, fmt.Errorf("encode relay ticket: %w", err)
	}
	if len(encoded) > MaxFrameBytes {
		return nil, fmt.Errorf("relay ticket is %d bytes, exceeds %d limit", len(encoded), MaxFrameBytes)
	}
	frame := make([]byte, 4+len(encoded))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(encoded)))
	copy(frame[4:], encoded)

	dialTimeout := d.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = DefaultDialTimeout
	}
	writeTimeout := d.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = DefaultWriteTimeout
	}
	upgrader := websocket.Dialer{
		HandshakeTimeout: dialTimeout,
		Subprotocols:     nil,
	}
	conn, _, err := upgrader.DialContext(ctx, target, d.HTTPHeader)
	if err != nil {
		return nil, fmt.Errorf("dial relay: %w", err)
	}
	// Bound the first frame write to the dial timeout so a peer that
	// accepts the upgrade but never reads the ticket still fails fast.
	_ = conn.SetWriteDeadline(time.Now().Add(dialTimeout))
	if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("send relay ticket: %w", err)
	}
	// Hand control to the read pump immediately so the server's
	// handshake deadline is honoured without further writes from us.
	_ = conn.SetWriteDeadline(time.Time{})
	_ = conn.SetReadDeadline(time.Time{})

	stream := &Stream{
		conn:       conn,
		writeWait:  writeTimeout,
		readCh:   make(chan frameDelivery, readPumpBuffer),
		closed:     make(chan struct{}),
	}
	go stream.readPump()
	return stream, nil
}

// Stream is one half of a relay route. It is the byte pipe the rest of
// the Agent uses to talk to the peer on the other side of the relay.
// Closing the Stream tears down the underlying WebSocket so the relay
// route is reaped.
//
// The zero value is not usable; construct one with Dial. Stream is
// safe for concurrent use: Write is serialized with a mutex, Read
// drains the read pump goroutine, and Close is idempotent.
type Stream struct {
	conn      *websocket.Conn
	writeMu   sync.Mutex
	writeWait time.Duration
	readCh  chan frameDelivery
	closed    chan struct{}
	closeOnce sync.Once
	closeErr  error
}

type frameDelivery struct {
	payload []byte
	err     error
}

// Read returns the next binary frame the relay forwarded. A single
// call returns at most one frame; callers needing a continuous byte
// stream should use io.ReadFull or copy into a buffer at least as
// large as MaxFrameBytes.
//
// If the next frame is larger than len(p), the frame is dropped and
// ErrFrameTooLarge is returned. The caller should retry with a buffer
// at least MaxFrameBytes in size.
func (s *Stream) Read(p []byte) (int, error) {
	if s == nil {
		return 0, io.ErrClosedPipe
	}
	if len(p) == 0 {
		return 0, nil
	}
	select {
	case delivery, ok := <-s.readCh:
		if !ok {
			return 0, io.EOF
		}
		if delivery.err != nil {
			return 0, delivery.err
		}
		if len(delivery.payload) > len(p) {
			return 0, ErrFrameTooLarge
		}
		copy(p, delivery.payload)
		return len(delivery.payload), nil
	case <-s.closed:
		return 0, io.ErrClosedPipe
	}
}

// Write sends a single binary frame to the relay. The supplied slice
// is written verbatim; the client does not split large writes into
// multiple frames. Callers that need framing should chunk their input
// before calling Write.
func (s *Stream) Write(p []byte) (int, error) {
	if s == nil {
		return 0, io.ErrClosedPipe
	}
	if len(p) == 0 {
		return 0, nil
	}
	if len(p) > MaxFrameBytes {
		return 0, fmt.Errorf("relay frame is %d bytes, exceeds %d limit", len(p), MaxFrameBytes)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	select {
	case <-s.closed:
		return 0, io.ErrClosedPipe
	default:
	}
	_ = s.conn.SetWriteDeadline(time.Now().Add(s.writeWait))
	if err := s.conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		s.markClosed(err)
		return 0, err
	}
	return len(p), nil
}

// Close tears down the underlying WebSocket. The first call returns
// the close error from gorilla/websocket; subsequent calls return
// nil. Close unblocks any pending Read with io.ErrClosedPipe.
func (s *Stream) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		close(s.closed)
		if s.conn != nil {
			s.closeErr = s.conn.Close()
		}
	})
	return s.closeErr
}

func (s *Stream) markClosed(err error) {
	s.closeOnce.Do(func() {
		s.closeErr = err
		close(s.closed)
		if s.conn != nil {
			_ = s.conn.Close()
		}
	})
}

func (s *Stream) readPump() {
	for {
		messageType, payload, err := s.conn.ReadMessage()
		if err != nil {
			select {
			case s.readCh <- frameDelivery{err: err}:
			case <-s.closed:
			}
			return
		}
		if messageType != websocket.BinaryMessage {
			// The relay protocol only forwards binary frames.
			// Non-binary frames are a contract violation; close
			// the stream so the caller surfaces the error.
			select {
			case s.readCh <- frameDelivery{err: errors.New("relay forwarded a non-binary frame")}:
			case <-s.closed:
			}
			s.markClosed(errors.New("relay forwarded a non-binary frame"))
			return
		}
		if len(payload) > MaxFrameBytes {
			select {
			case s.readCh <- frameDelivery{err: fmt.Errorf("relay frame is %d bytes, exceeds %d limit", len(payload), MaxFrameBytes)}:
			case <-s.closed:
			}
			s.markClosed(errors.New("relay frame exceeds limit"))
			return
		}
		select {
		case s.readCh <- frameDelivery{payload: payload}:
		case <-s.closed:
			return
		}
	}
}
