package server

import (
	"bytes"
	"crypto/sha256"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/agentlog"
	attachmentv1 "github.com/zzemy/VibeBridge/internal/attachment"
	protocolv1 "github.com/zzemy/VibeBridge/internal/protocol"
)

func TestPTYSessionRejectsAttachmentManagerAfterShutdownStarts(t *testing.T) {
	tests := []struct {
		name       string
		transition func(*sessionLifecycle) bool
	}{
		{name: "ending", transition: func(lifecycle *sessionLifecycle) bool { return lifecycle.beginEnding() }},
		{name: "ended", transition: func(lifecycle *sessionLifecycle) bool { return lifecycle.finish(nil) }},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			workspaceRoot, _ := validatedWorkspacePaths(t, t.TempDir(), "")
			staging, err := attachmentv1.CreateSessionStaging(workspaceRoot, []byte(testCase.name))
			if err != nil {
				t.Fatalf("create session staging: %v", err)
			}
			t.Cleanup(func() { _ = staging.Cleanup() })

			session := &ptySession{lifecycle: newSessionLifecycle(), staging: staging}
			if !session.lifecycle.started() || !testCase.transition(&session.lifecycle) {
				t.Fatal("prepare session shutdown state")
			}
			if _, err := session.transferManager(); err == nil {
				t.Fatal("attachment manager was created after session shutdown started")
			}
			if session.attachmentManager != nil {
				t.Fatal("attachment manager remains after rejected lazy creation")
			}
			if err := staging.Cleanup(); err != nil {
				t.Fatalf("cleanup unused session staging: %v", err)
			}
		})
	}
}

func TestPTYSessionAttachmentOutcomeDoesNotCreateTransferManager(t *testing.T) {
	session := &ptySession{lifecycle: newSessionLifecycle()}
	if !session.lifecycle.started() {
		t.Fatal("start session lifecycle")
	}
	outcome, err := session.attachmentOutcome([]byte("unknown-transfer"))
	if err != nil {
		t.Fatalf("query attachment outcome: %v", err)
	}
	if outcome.Disposition != attachmentv1.TransferDispositionUnknown || outcome.NextOffsetBytes != 0 {
		t.Fatalf("attachment outcome = %+v, want unknown", outcome)
	}
	if session.attachmentManager != nil {
		t.Fatal("status query lazily created an attachment manager")
	}
}

func TestPTYSessionReportsAttachmentCleanupFailure(t *testing.T) {
	workspaceRoot, _ := validatedWorkspacePaths(t, t.TempDir(), "")
	staging, err := attachmentv1.CreateSessionStaging(workspaceRoot, []byte("cleanup-failure"))
	if err != nil {
		t.Fatalf("create session staging: %v", err)
	}
	wait := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(wait) }) }
	t.Cleanup(release)
	logger := &recordingEventLogger{}
	session, err := newPTYSession(
		terminalLaunchRequest{Command: []string{"fake"}},
		0,
		systemClock{},
		&fakeTerminalLauncher{launch: terminalLaunch{
			terminal:    &countingPTY{},
			processTree: &countingProcessTree{},
			cancel:      func() {},
			waiter:      blockingWaiter{done: wait},
		}},
		staging,
		nil,
		sessionTelemetry{correlationID: "opaque-session-id", logger: logger},
	)
	if err != nil {
		t.Fatalf("start PTY session: %v", err)
	}

	manager, err := session.transferManager()
	if err != nil {
		t.Fatalf("create attachment manager: %v", err)
	}
	content := []byte("x")
	totalHash := sha256.Sum256(content)
	if err := manager.Begin(attachmentv1.BeginRequest{
		TransferID:          []byte("cleanup-transfer"),
		DisplayName:         "cleanup.txt",
		DeclaredContentType: "text/plain",
		DeclaredExtension:   "txt",
		TotalSizeBytes:      uint64(len(content)),
		TotalSHA256:         totalHash[:],
	}); err != nil {
		t.Fatalf("begin attachment: %v", err)
	}
	entries, err := os.ReadDir(staging.Path())
	if err != nil || len(entries) != 1 {
		t.Fatalf("partial entries = %v, err = %v; want one", entries, err)
	}
	partialPath := filepath.Join(staging.Path(), entries[0].Name())
	if err := os.Remove(partialPath); err != nil {
		t.Fatalf("remove partial outside manager: %v", err)
	}
	if err := os.Mkdir(partialPath, 0o700); err != nil {
		t.Fatalf("replace partial with directory: %v", err)
	}

	release()
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("session did not finish after cleanup failure")
	}

	events := logger.snapshot()
	cleanupEvent := events[len(events)-1]
	if cleanupEvent.Name != agentlog.EventSessionCleanupFailed || cleanupEvent.State != agentlog.StateEnded || cleanupEvent.Reason != agentlog.ReasonProcessExit || cleanupEvent.Outcome != agentlog.OutcomeFailure {
		t.Fatalf("cleanup failure event = %#v", cleanupEvent)
	}
	if _, err := os.Lstat(staging.Path()); err != nil {
		t.Fatalf("staging was removed after manager close failed: %v", err)
	}

	if err := os.RemoveAll(partialPath); err != nil {
		t.Fatalf("remove failed-close fixture: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("retry attachment manager close: %v", err)
	}
	if err := staging.Cleanup(); err != nil {
		t.Fatalf("cleanup staging after manager close retry: %v", err)
	}
}

func TestProtocolV1TransfersAttachmentIntoWorkspaceSession(t *testing.T) {
	workspaceRoot := t.TempDir()
	canonicalRoot, canonicalWorkingDirectory := validatedWorkspacePaths(t, workspaceRoot, "")
	wait := make(chan struct{})
	var stopOnce sync.Once
	stopProcess := func() { stopOnce.Do(func() { close(wait) }) }
	t.Cleanup(stopProcess)

	launcher := &fakeTerminalLauncher{launch: terminalLaunch{
		terminal:    &countingPTY{},
		processTree: &countingProcessTree{},
		cancel:      func() {},
		waiter:      blockingWaiter{done: wait},
	}}
	app := New(Config{
		SessionToken:     "expected-token",
		Command:          []string{"fake"},
		WorkspaceRoot:    canonicalRoot,
		WorkingDirectory: canonicalWorkingDirectory,
	})
	app.launcher = launcher
	testServer := httptest.NewServer(app.Handler())
	t.Cleanup(testServer.Close)

	connection := dialProtocolV1(t, testServer.URL)
	t.Cleanup(func() { _ = connection.Close() })
	capabilities := []string{
		protocolv1.CapabilityTerminalBinaryOutput,
		protocolv1.CapabilityTerminalSequencedIO,
		protocolv1.CapabilityControlError,
		protocolv1.CapabilityAttachmentTransfer,
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, marshalClientHello(t, 1, 0, capabilities)); err != nil {
		t.Fatalf("send client Hello: %v", err)
	}
	agentHello := readProtocolEnvelope(t, connection)
	for _, capability := range agentHello.GetHello().GetCapabilities() {
		if capability == protocolv1.CapabilityAttachmentTransfer {
			t.Fatal("Agent advertised attachment transfer before the client flow is complete")
		}
	}
	initialOutput := readProtocolEnvelope(t, connection)

	content := []byte("hello attachment\n")
	totalHash := sha256.Sum256(content)
	transferID := []byte("phone-transfer-1")
	begin := newClientProtocolEnvelope(nil, 0, 2, initialOutput.Sequence)
	begin.Payload = &vibebridgev1.Envelope_AttachmentBegin{AttachmentBegin: &vibebridgev1.AttachmentBegin{
		TransferId:          transferID,
		DisplayName:         "phone notes.txt",
		DeclaredContentType: "text/plain; charset=utf-8",
		DeclaredExtension:   "txt",
		TotalSizeBytes:      uint64(len(content)),
		TotalSha256:         totalHash[:],
	}}
	writeProtocolAttachmentMessage(t, connection, begin)
	assertAttachmentAcknowledgement(t, connection, 3, 2)

	chunkHash := sha256.Sum256(content)
	chunk := newClientProtocolEnvelope(nil, 0, 3, 3)
	chunk.Payload = &vibebridgev1.Envelope_AttachmentChunk{AttachmentChunk: &vibebridgev1.AttachmentChunk{
		TransferId:  transferID,
		Data:        content,
		ChunkSha256: chunkHash[:],
	}}
	writeProtocolAttachmentMessage(t, connection, chunk)
	assertAttachmentAcknowledgement(t, connection, 4, 3)

	complete := newClientProtocolEnvelope(nil, 0, 4, 4)
	complete.Payload = &vibebridgev1.Envelope_AttachmentComplete{AttachmentComplete: &vibebridgev1.AttachmentComplete{TransferId: transferID}}
	writeProtocolAttachmentMessage(t, connection, complete)
	assertAttachmentAcknowledgement(t, connection, 5, 4)

	statusRequest := newClientProtocolEnvelope(nil, 0, 5, 5)
	statusRequest.Payload = &vibebridgev1.Envelope_AttachmentTransferStatusRequest{AttachmentTransferStatusRequest: &vibebridgev1.AttachmentTransferStatusRequest{TransferId: transferID}}
	writeProtocolAttachmentMessage(t, connection, statusRequest)
	status := readProtocolEnvelope(t, connection)
	if status.Sequence != 6 || status.Acknowledge != 5 ||
		status.GetAttachmentTransferStatus().GetDisposition() != vibebridgev1.AttachmentTransferDisposition_ATTACHMENT_TRANSFER_DISPOSITION_COMPLETED ||
		status.GetAttachmentTransferStatus().GetNextOffsetBytes() != 0 ||
		!bytes.Equal(status.GetAttachmentTransferStatus().GetTransferId(), transferID) {
		t.Fatalf("attachment status sequence/ack/payload = %d/%d/%+v", status.Sequence, status.Acknowledge, status.GetAttachmentTransferStatus())
	}

	cancel := newClientProtocolEnvelope(nil, 0, 6, 6)
	cancel.Payload = &vibebridgev1.Envelope_AttachmentCancel{AttachmentCancel: &vibebridgev1.AttachmentCancel{TransferId: transferID}}
	writeProtocolAttachmentMessage(t, connection, cancel)
	assertAttachmentAcknowledgement(t, connection, 7, 6)

	app.mu.Lock()
	session := app.session
	app.mu.Unlock()
	if session == nil || session.staging == nil {
		t.Fatal("workspace session has no attachment staging")
	}
	entries, err := os.ReadDir(session.staging.Path())
	if err != nil {
		t.Fatalf("read attachment staging: %v", err)
	}
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".txt") || strings.HasSuffix(entries[0].Name(), ".partial") {
		t.Fatalf("published staging entries = %v, want one generated .txt file", entries)
	}
	publishedPath := filepath.Join(session.staging.Path(), entries[0].Name())
	published, err := os.ReadFile(publishedPath)
	if err != nil {
		t.Fatalf("read published attachment: %v", err)
	}
	if string(published) != string(content) {
		t.Fatalf("published attachment = %q, want %q", published, content)
	}

	activeID := []byte("phone-transfer-2")
	activeContent := []byte("active")
	activeHash := sha256.Sum256(activeContent)
	activeBegin := newClientProtocolEnvelope(nil, 0, 7, 7)
	activeBegin.Payload = &vibebridgev1.Envelope_AttachmentBegin{AttachmentBegin: &vibebridgev1.AttachmentBegin{
		TransferId:          activeID,
		DisplayName:         "active.txt",
		DeclaredContentType: "text/plain",
		DeclaredExtension:   "txt",
		TotalSizeBytes:      uint64(len(activeContent)),
		TotalSha256:         activeHash[:],
	}}
	writeProtocolAttachmentMessage(t, connection, activeBegin)
	assertAttachmentAcknowledgement(t, connection, 8, 7)

	unknownID := []byte("phone-transfer-unknown")
	discard := newClientProtocolEnvelope(nil, 0, 8, 8)
	discard.Payload = &vibebridgev1.Envelope_AttachmentDiscard{AttachmentDiscard: &vibebridgev1.AttachmentDiscard{
		TransferIds: [][]byte{transferID, activeID, unknownID},
	}}
	writeProtocolAttachmentMessage(t, connection, discard)
	assertAttachmentAcknowledgement(t, connection, 9, 8)

	for index, discardedID := range [][]byte{transferID, activeID, unknownID} {
		clientSequence := uint64(9 + index)
		statusRequest := newClientProtocolEnvelope(nil, 0, clientSequence, clientSequence)
		statusRequest.Payload = &vibebridgev1.Envelope_AttachmentTransferStatusRequest{AttachmentTransferStatusRequest: &vibebridgev1.AttachmentTransferStatusRequest{TransferId: discardedID}}
		writeProtocolAttachmentMessage(t, connection, statusRequest)
		status := readProtocolEnvelope(t, connection)
		if status.Sequence != clientSequence+1 || status.Acknowledge != clientSequence ||
			status.GetAttachmentTransferStatus().GetDisposition() != vibebridgev1.AttachmentTransferDisposition_ATTACHMENT_TRANSFER_DISPOSITION_CANCELLED ||
			!bytes.Equal(status.GetAttachmentTransferStatus().GetTransferId(), discardedID) {
			t.Fatalf("discarded attachment status sequence/ack/payload = %d/%d/%+v", status.Sequence, status.Acknowledge, status.GetAttachmentTransferStatus())
		}
	}
	entries, err = os.ReadDir(session.staging.Path())
	if err != nil {
		t.Fatalf("read discarded attachment staging: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("discarded attachment entries = %v, want none", entries)
	}

	stagingPath := session.staging.Path()
	_ = connection.Close()
	stopProcess()
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("workspace session did not end")
	}
	if _, err := os.Lstat(stagingPath); !os.IsNotExist(err) {
		t.Fatalf("session staging remains after process exit: %v", err)
	}
}

func TestProtocolV1AttachmentFailureAbandonsPartialAndDoesNotCommitClientSequence(t *testing.T) {
	workspaceRoot := t.TempDir()
	canonicalRoot, canonicalWorkingDirectory := validatedWorkspacePaths(t, workspaceRoot, "")
	wait := make(chan struct{})
	var stopOnce sync.Once
	stopProcess := func() { stopOnce.Do(func() { close(wait) }) }
	t.Cleanup(stopProcess)

	app := New(Config{
		SessionToken:     "expected-token",
		Command:          []string{"fake"},
		WorkspaceRoot:    canonicalRoot,
		WorkingDirectory: canonicalWorkingDirectory,
	})
	app.launcher = &fakeTerminalLauncher{launch: terminalLaunch{
		terminal:    &countingPTY{},
		processTree: &countingProcessTree{},
		cancel:      func() {},
		waiter:      blockingWaiter{done: wait},
	}}
	testServer := httptest.NewServer(app.Handler())
	t.Cleanup(testServer.Close)

	connection := dialProtocolV1(t, testServer.URL)
	t.Cleanup(func() { _ = connection.Close() })
	capabilities := []string{
		protocolv1.CapabilityTerminalBinaryOutput,
		protocolv1.CapabilityTerminalSequencedIO,
		protocolv1.CapabilityControlError,
		protocolv1.CapabilityAttachmentTransfer,
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, marshalClientHello(t, 1, 0, capabilities)); err != nil {
		t.Fatalf("send client Hello: %v", err)
	}
	_ = readProtocolEnvelope(t, connection) // Agent Hello.
	initialOutput := readProtocolEnvelope(t, connection)

	content := []byte("retryable bytes\n")
	totalHash := sha256.Sum256(content)
	transferID := []byte("retry-transfer")
	begin := newClientProtocolEnvelope(nil, 0, 2, initialOutput.Sequence)
	begin.Payload = &vibebridgev1.Envelope_AttachmentBegin{AttachmentBegin: &vibebridgev1.AttachmentBegin{
		TransferId:          transferID,
		DisplayName:         "retry.txt",
		DeclaredContentType: "text/plain",
		DeclaredExtension:   "txt",
		TotalSizeBytes:      uint64(len(content)),
		TotalSha256:         totalHash[:],
	}}
	writeProtocolAttachmentMessage(t, connection, begin)
	assertAttachmentAcknowledgement(t, connection, 3, 2)

	badChunk := newClientProtocolEnvelope(nil, 0, 3, 3)
	badChunk.Payload = &vibebridgev1.Envelope_AttachmentChunk{AttachmentChunk: &vibebridgev1.AttachmentChunk{
		TransferId:  transferID,
		Data:        content,
		ChunkSha256: make([]byte, sha256.Size),
	}}
	writeProtocolAttachmentMessage(t, connection, badChunk)
	failure := readProtocolEnvelope(t, connection)
	if failure.Sequence != 4 || failure.Acknowledge != 2 || failure.GetError().GetCode() != vibebridgev1.ErrorCode_ERROR_CODE_ATTACHMENT_TRANSFER_FAILED {
		t.Fatalf("attachment Error sequence/ack/code = %d/%d/%v, want 4/2/ATTACHMENT_TRANSFER_FAILED", failure.Sequence, failure.Acknowledge, failure.GetError().GetCode())
	}

	app.mu.Lock()
	session := app.session
	app.mu.Unlock()
	if session == nil || session.staging == nil {
		t.Fatal("workspace session has no attachment staging")
	}
	entries, err := os.ReadDir(session.staging.Path())
	if err != nil {
		t.Fatalf("read failed-transfer staging: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed-transfer entries = %v, want abandoned staging", entries)
	}

	stagingPath := session.staging.Path()
	_ = connection.Close()
	stopProcess()
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("workspace session did not end")
	}
	if _, err := os.Lstat(stagingPath); !os.IsNotExist(err) {
		t.Fatalf("failed-transfer staging remains after process exit: %v", err)
	}
}

func writeProtocolAttachmentMessage(t *testing.T, connection *websocket.Conn, envelope *vibebridgev1.Envelope) {
	t.Helper()
	if err := connection.WriteMessage(websocket.BinaryMessage, marshalClientProtocolEnvelope(t, envelope)); err != nil {
		t.Fatalf("send %T: %v", envelope.Payload, err)
	}
}

func assertAttachmentAcknowledgement(t *testing.T, connection *websocket.Conn, sequence, acknowledge uint64) {
	t.Helper()
	envelope := readProtocolEnvelope(t, connection)
	if envelope.Sequence != sequence || envelope.Acknowledge != acknowledge || envelope.GetAcknowledgement() == nil {
		t.Fatalf("attachment ack sequence/ack/payload = %d/%d/%T, want %d/%d/Acknowledgement", envelope.Sequence, envelope.Acknowledge, envelope.Payload, sequence, acknowledge)
	}
}
