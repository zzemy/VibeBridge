package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/pairingflow"
	"github.com/zzemy/VibeBridge/internal/protocol"
	"google.golang.org/protobuf/proto"
)

const (
	pairingTransportPath    = "/pairing/v1"
	pairingHandshakeTimeout = 15 * time.Second
	pairingWriteTimeout     = 5 * time.Second
	maxPairingFrameBytes    = 64 * 1024
)

func (management *agentManagement) pairingTransportHandler() http.Handler {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		Subprotocols:    []string{pairingflow.WebSocketSubprotocol},
		CheckOrigin:     samePairingOrigin,
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if request.URL.RawQuery != "" || !offersPairingSubprotocol(request) {
			writer.Header().Set("Upgrade", "websocket")
			http.Error(writer, "pairing WebSocket protocol required", http.StatusUpgradeRequired)
			return
		}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		connection.SetReadLimit(maxPairingFrameBytes)
		_ = connection.SetReadDeadline(time.Now().Add(pairingHandshakeTimeout))

		start := new(vibebridgev1.PairingHandshakeStart)
		if err := readPairingFrame(connection, start); err != nil {
			writePairingClose(connection, websocket.CloseProtocolError)
			return
		}
		started, err := management.flows.Start(start)
		if err != nil {
			writePairingClose(connection, websocket.ClosePolicyViolation)
			return
		}
		defer management.flows.Cancel(started.FlowID)
		if err := writePairingFrame(connection, started.Response); err != nil {
			return
		}

		finish := new(vibebridgev1.PairingHandshakeFinish)
		if err := readPairingFrame(connection, finish); err != nil {
			writePairingClose(connection, websocket.CloseProtocolError)
			return
		}
		session, err := management.flows.Finish(started.FlowID, finish)
		if err != nil {
			writePairingClose(connection, websocket.ClosePolicyViolation)
			return
		}
		defer session.Close()
		pending, err := session.Encrypt(&vibebridgev1.PairingApproval{
			Status: vibebridgev1.PairingApprovalStatus_PAIRING_APPROVAL_STATUS_PENDING,
		})
		if err != nil || writePairingBytes(connection, pending) != nil {
			return
		}

		expiresAt := session.Snapshot().ExpiresAt
		_ = connection.SetReadDeadline(expiresAt)
		decisionResult := make(chan pairingDecisionResult, 1)
		go func() {
			approval, waitErr := session.Wait(context.Background())
			decisionResult <- pairingDecisionResult{approval: approval, err: waitErr}
		}()
		peerActivity := make(chan error, 1)
		go func() {
			_, _, readErr := connection.ReadMessage()
			if readErr == nil {
				readErr = errors.New("unexpected client frame while awaiting approval")
			}
			peerActivity <- readErr
		}()
		timer := time.NewTimer(time.Until(expiresAt))
		defer timer.Stop()

		var decision pairingDecisionResult
		select {
		case decision = <-decisionResult:
			if decision.err != nil {
				writePairingClose(connection, websocket.ClosePolicyViolation)
				return
			}
		case <-peerActivity:
			management.flows.Cancel(started.FlowID)
			return
		case <-timer.C:
			management.flows.Cancel(started.FlowID)
			writePairingClose(connection, websocket.ClosePolicyViolation)
			return
		}
		finalFrame, err := session.Encrypt(decision.approval)
		if err != nil || writePairingBytes(connection, finalFrame) != nil {
			return
		}
		writePairingClose(connection, websocket.CloseNormalClosure)
	})
}

type pairingDecisionResult struct {
	approval *vibebridgev1.PairingApproval
	err      error
}

func readPairingFrame(connection *websocket.Conn, message proto.Message) error {
	messageType, encoded, err := connection.ReadMessage()
	if err != nil {
		return err
	}
	if messageType != websocket.BinaryMessage || len(encoded) == 0 || len(encoded) > maxPairingFrameBytes {
		return errors.New("pairing frame must be bounded binary protobuf")
	}
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(encoded, message); err != nil || protocol.HasUnknownFields(message) {
		return errors.New("pairing frame is invalid")
	}
	return nil
}

func writePairingFrame(connection *websocket.Conn, message proto.Message) error {
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil || len(encoded) == 0 || len(encoded) > maxPairingFrameBytes {
		return errors.New("pairing frame is invalid")
	}
	return writePairingBytes(connection, encoded)
}

func writePairingBytes(connection *websocket.Conn, encoded []byte) error {
	if len(encoded) == 0 || len(encoded) > maxPairingFrameBytes {
		return errors.New("pairing frame is invalid")
	}
	_ = connection.SetWriteDeadline(time.Now().Add(pairingWriteTimeout))
	return connection.WriteMessage(websocket.BinaryMessage, encoded)
}

func writePairingClose(connection *websocket.Conn, code int) {
	_ = connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, "pairing ended"),
		time.Now().Add(pairingWriteTimeout),
	)
}

func offersPairingSubprotocol(request *http.Request) bool {
	for _, offered := range websocket.Subprotocols(request) {
		if offered == pairingflow.WebSocketSubprotocol {
			return true
		}
	}
	return false
}

func samePairingOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") &&
		parsed.User == nil && parsed.Host == request.Host && parsed.RawQuery == "" && parsed.Fragment == "" &&
		(parsed.Path == "" || parsed.Path == "/") && !strings.Contains(origin, "\\")
}
