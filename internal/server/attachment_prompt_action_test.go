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
	protocolv1 "github.com/zzemy/VibeBridge/internal/protocol"
	"google.golang.org/protobuf/proto"
)

func TestProtocolV1PreparesAndCommitsTrustedAttachmentPromptExactlyOnce(t *testing.T) {
	workspaceRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspaceRoot, "nested"), 0o700); err != nil {
		t.Fatalf("create nested working directory: %v", err)
	}
	canonicalRoot, canonicalWorkingDirectory := validatedWorkspacePaths(t, workspaceRoot, "nested")
	wait := make(chan struct{})
	var stopOnce sync.Once
	stopProcess := func() { stopOnce.Do(func() { close(wait) }) }
	t.Cleanup(stopProcess)
	terminal := &recordingPTY{writes: make(chan []byte, 1)}
	app := New(Config{
		SessionToken:     "expected-token",
		Command:          []string{"fake"},
		WorkspaceRoot:    canonicalRoot,
		WorkingDirectory: canonicalWorkingDirectory,
	})
	app.launcher = &fakeTerminalLauncher{launch: terminalLaunch{
		terminal: terminal, processTree: &countingProcessTree{}, cancel: func() {}, waiter: blockingWaiter{done: wait},
	}}
	testServer := httptest.NewServer(app.Handler())
	t.Cleanup(testServer.Close)

	capabilities := []string{
		protocolv1.CapabilityTerminalBinaryOutput,
		protocolv1.CapabilityTerminalSequencedIO,
		protocolv1.CapabilityControlError,
		protocolv1.CapabilityAttachmentTransfer,
		protocolv1.CapabilityAttachmentPromptAction,
	}
	connection, initialAcknowledge := openAttachmentPromptConnection(t, testServer.URL, capabilities, true)
	content := []byte("trusted attachment\n")
	transferID := []byte("prompt-transfer")
	totalHash := sha256.Sum256(content)
	begin := newClientProtocolEnvelope(nil, 0, 2, initialAcknowledge)
	begin.Payload = &vibebridgev1.Envelope_AttachmentBegin{AttachmentBegin: &vibebridgev1.AttachmentBegin{
		TransferId: transferID, DisplayName: "private host name.txt", DeclaredContentType: "text/plain",
		DeclaredExtension: "txt", TotalSizeBytes: uint64(len(content)), TotalSha256: totalHash[:],
	}}
	writeProtocolAttachmentMessage(t, connection, begin)
	assertAttachmentAcknowledgement(t, connection, 3, 2)

	chunkHash := sha256.Sum256(content)
	chunk := newClientProtocolEnvelope(nil, 0, 3, 3)
	chunk.Payload = &vibebridgev1.Envelope_AttachmentChunk{AttachmentChunk: &vibebridgev1.AttachmentChunk{
		TransferId: transferID, Data: content, ChunkSha256: chunkHash[:],
	}}
	writeProtocolAttachmentMessage(t, connection, chunk)
	assertAttachmentAcknowledgement(t, connection, 4, 3)

	complete := newClientProtocolEnvelope(nil, 0, 4, 4)
	complete.Payload = &vibebridgev1.Envelope_AttachmentComplete{AttachmentComplete: &vibebridgev1.AttachmentComplete{TransferId: transferID}}
	writeProtocolAttachmentMessage(t, connection, complete)
	assertAttachmentAcknowledgement(t, connection, 5, 4)

	app.mu.Lock()
	session := app.session
	app.mu.Unlock()
	if session == nil || session.staging == nil {
		t.Fatal("workspace session has no attachment staging")
	}
	entries, err := os.ReadDir(session.staging.Path())
	if err != nil || len(entries) != 1 {
		t.Fatalf("published attachment entries = %v, err = %v; want one", entries, err)
	}
	relativePath, err := filepath.Rel(canonicalWorkingDirectory, filepath.Join(session.staging.Path(), entries[0].Name()))
	if err != nil {
		t.Fatalf("derive expected terminal reference: %v", err)
	}
	prompt := "Inspect the uploaded evidence"
	wantPreview := prompt + "\n\nUse the following local files:\n- `" + relativePath + "`"
	actionID := []byte("trusted-action")
	prepare := newClientProtocolEnvelope(nil, 0, 5, 5)
	prepare.Payload = &vibebridgev1.Envelope_AttachmentPromptPrepare{AttachmentPromptPrepare: &vibebridgev1.AttachmentPromptPrepare{
		ActionId: actionID, TransferIds: [][]byte{transferID}, Prompt: prompt, AppendEnter: true,
	}}
	writeProtocolAttachmentMessage(t, connection, prepare)
	previewEnvelope := readProtocolEnvelope(t, connection)
	preview := previewEnvelope.GetAttachmentPromptPreview()
	if previewEnvelope.Sequence != 6 || previewEnvelope.Acknowledge != 5 || !bytes.Equal(preview.GetActionId(), actionID) ||
		preview.GetDisposition() != vibebridgev1.AttachmentPromptDisposition_ATTACHMENT_PROMPT_DISPOSITION_PREPARED || preview.GetPreview() != wantPreview || !preview.GetAppendEnter() {
		t.Fatalf("prompt preview envelope = sequence/ack %d/%d payload %#v", previewEnvelope.Sequence, previewEnvelope.Acknowledge, preview)
	}
	if strings.Contains(preview.GetPreview(), canonicalRoot) || strings.Contains(preview.GetPreview(), "private host name") {
		t.Fatalf("prompt preview exposed host path or display metadata: %q", preview.GetPreview())
	}
	assertNoTerminalWrite(t, terminal.writes)

	commit := newClientProtocolEnvelope(nil, 0, 6, 6)
	commit.Payload = &vibebridgev1.Envelope_AttachmentPromptCommit{AttachmentPromptCommit: &vibebridgev1.AttachmentPromptCommit{ActionId: actionID}}
	writeProtocolAttachmentMessage(t, connection, commit)
	select {
	case written := <-terminal.writes:
		if string(written) != wantPreview+"\r" {
			t.Fatalf("committed PTY bytes = %q, want exact preview plus Enter", written)
		}
	case <-time.After(time.Second):
		t.Fatal("committed prompt action was not written to the PTY")
	}
	// Simulate a lost acknowledgement: close without reading it, then retry the
	// same durable action on a new physical connection.
	_ = connection.Close()
	waitForSessionState(t, app, sessionStateDetached)

	reconnected, reconnectAcknowledge := openAttachmentPromptConnection(t, testServer.URL, capabilities, false)
	t.Cleanup(func() { _ = reconnected.Close() })
	retryCommit := newClientProtocolEnvelope(nil, 0, 2, reconnectAcknowledge)
	retryCommit.Payload = &vibebridgev1.Envelope_AttachmentPromptCommit{AttachmentPromptCommit: &vibebridgev1.AttachmentPromptCommit{ActionId: actionID}}
	writeProtocolAttachmentMessage(t, reconnected, retryCommit)
	assertAttachmentAcknowledgement(t, reconnected, 2, 2)
	assertNoTerminalWrite(t, terminal.writes)

	duplicatePrepare := newClientProtocolEnvelope(nil, 0, 3, 2)
	duplicatePrepare.Payload = &vibebridgev1.Envelope_AttachmentPromptPrepare{AttachmentPromptPrepare: &vibebridgev1.AttachmentPromptPrepare{
		ActionId: actionID, TransferIds: [][]byte{transferID}, Prompt: prompt, AppendEnter: true,
	}}
	writeProtocolAttachmentMessage(t, reconnected, duplicatePrepare)
	committedPreviewEnvelope := readProtocolEnvelope(t, reconnected)
	committedPreview := committedPreviewEnvelope.GetAttachmentPromptPreview()
	if committedPreviewEnvelope.Sequence != 3 || committedPreviewEnvelope.Acknowledge != 3 ||
		committedPreview.GetDisposition() != vibebridgev1.AttachmentPromptDisposition_ATTACHMENT_PROMPT_DISPOSITION_COMMITTED || committedPreview.GetPreview() != "" || committedPreview.GetAppendEnter() {
		t.Fatalf("duplicate committed prepare response = sequence/ack %d/%d payload %#v", committedPreviewEnvelope.Sequence, committedPreviewEnvelope.Acknowledge, committedPreview)
	}
	assertNoTerminalWrite(t, terminal.writes)

	cancelledActionID := []byte("cancelled-action")
	cancelledPrepare := newClientProtocolEnvelope(nil, 0, 4, 3)
	cancelledPrepare.Payload = &vibebridgev1.Envelope_AttachmentPromptPrepare{AttachmentPromptPrepare: &vibebridgev1.AttachmentPromptPrepare{
		ActionId: cancelledActionID, TransferIds: [][]byte{transferID}, Prompt: "Do not submit", AppendEnter: false,
	}}
	writeProtocolAttachmentMessage(t, reconnected, cancelledPrepare)
	if got := readProtocolEnvelope(t, reconnected).GetAttachmentPromptPreview().GetDisposition(); got != vibebridgev1.AttachmentPromptDisposition_ATTACHMENT_PROMPT_DISPOSITION_PREPARED {
		t.Fatalf("cancelled action prepare disposition = %v", got)
	}
	cancel := newClientProtocolEnvelope(nil, 0, 5, 4)
	cancel.Payload = &vibebridgev1.Envelope_AttachmentPromptCancel{AttachmentPromptCancel: &vibebridgev1.AttachmentPromptCancel{ActionId: cancelledActionID}}
	writeProtocolAttachmentMessage(t, reconnected, cancel)
	assertAttachmentAcknowledgement(t, reconnected, 5, 5)
	cancelledCommit := newClientProtocolEnvelope(nil, 0, 6, 5)
	cancelledCommit.Payload = &vibebridgev1.Envelope_AttachmentPromptCommit{AttachmentPromptCommit: &vibebridgev1.AttachmentPromptCommit{ActionId: cancelledActionID}}
	writeProtocolAttachmentMessage(t, reconnected, cancelledCommit)
	failure := readProtocolEnvelope(t, reconnected)
	if failure.Sequence != 6 || failure.Acknowledge != 5 || failure.GetError().GetCode() != vibebridgev1.ErrorCode_ERROR_CODE_ATTACHMENT_PROMPT_ACTION_FAILED {
		t.Fatalf("cancelled commit Error sequence/ack/code = %d/%d/%v", failure.Sequence, failure.Acknowledge, failure.GetError().GetCode())
	}
	assertNoTerminalWrite(t, terminal.writes)

	stopProcess()
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("workspace session did not end")
	}
}

func TestProtocolV1AttachmentPromptFailureDoesNotWritePTYOrExposeHostDetails(t *testing.T) {
	workspaceRoot := t.TempDir()
	canonicalRoot, canonicalWorkingDirectory := validatedWorkspacePaths(t, workspaceRoot, "")
	wait := make(chan struct{})
	var stopOnce sync.Once
	stopProcess := func() { stopOnce.Do(func() { close(wait) }) }
	t.Cleanup(stopProcess)
	terminal := &recordingPTY{writes: make(chan []byte, 1)}
	app := New(Config{SessionToken: "expected-token", Command: []string{"fake"}, WorkspaceRoot: canonicalRoot, WorkingDirectory: canonicalWorkingDirectory})
	app.launcher = &fakeTerminalLauncher{launch: terminalLaunch{
		terminal: terminal, processTree: &countingProcessTree{}, cancel: func() {}, waiter: blockingWaiter{done: wait},
	}}
	testServer := httptest.NewServer(app.Handler())
	t.Cleanup(testServer.Close)
	connection, initialAcknowledge := openAttachmentPromptConnection(t, testServer.URL, []string{
		protocolv1.CapabilityTerminalBinaryOutput,
		protocolv1.CapabilityTerminalSequencedIO,
		protocolv1.CapabilityControlError,
		protocolv1.CapabilityAttachmentTransfer,
		protocolv1.CapabilityAttachmentPromptAction,
	}, true)
	t.Cleanup(func() { _ = connection.Close() })

	prepare := newClientProtocolEnvelope(nil, 0, 2, initialAcknowledge)
	prepare.Payload = &vibebridgev1.Envelope_AttachmentPromptPrepare{AttachmentPromptPrepare: &vibebridgev1.AttachmentPromptPrepare{
		ActionId: []byte("unknown-action"), TransferIds: [][]byte{[]byte("unknown-transfer")}, Prompt: "inspect",
	}}
	writeProtocolAttachmentMessage(t, connection, prepare)
	messageType, encoded, err := connection.ReadMessage()
	if err != nil {
		t.Fatalf("read attachment prompt Error: %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("attachment prompt Error message type = %d, want binary", messageType)
	}
	failure := new(vibebridgev1.Envelope)
	if err := proto.Unmarshal(encoded, failure); err != nil {
		t.Fatalf("decode attachment prompt Error: %v", err)
	}
	if failure.Sequence != 3 || failure.Acknowledge != 1 || failure.GetError().GetCode() != vibebridgev1.ErrorCode_ERROR_CODE_ATTACHMENT_PROMPT_ACTION_FAILED {
		t.Fatalf("attachment prompt Error sequence/ack/code = %d/%d/%v", failure.Sequence, failure.Acknowledge, failure.GetError().GetCode())
	}
	if bytes.Contains(encoded, []byte(canonicalRoot)) || bytes.Contains(encoded, []byte("unknown-transfer")) {
		t.Fatal("attachment prompt Error exposed host path or transfer ID")
	}
	assertNoTerminalWrite(t, terminal.writes)
}

func openAttachmentPromptConnection(t *testing.T, serverURL string, capabilities []string, expectInitialOutput bool) (*websocket.Conn, uint64) {
	t.Helper()
	connection := dialProtocolV1(t, serverURL)
	if err := connection.WriteMessage(websocket.BinaryMessage, marshalClientHello(t, 1, 0, capabilities)); err != nil {
		t.Fatalf("send client Hello: %v", err)
	}
	agentHello := readProtocolEnvelope(t, connection)
	for _, capability := range agentHello.GetHello().GetCapabilities() {
		if capability == protocolv1.CapabilityAttachmentTransfer || capability == protocolv1.CapabilityAttachmentPromptAction {
			t.Fatalf("Agent advertised dark attachment capability %q", capability)
		}
	}
	if !expectInitialOutput {
		return connection, agentHello.Sequence
	}
	initialOutput := readProtocolEnvelope(t, connection)
	if initialOutput.Sequence != 2 || initialOutput.GetTerminalOutput() == nil {
		t.Fatalf("initial output envelope = sequence %d payload %T", initialOutput.Sequence, initialOutput.Payload)
	}
	return connection, initialOutput.Sequence
}

func assertNoTerminalWrite(t *testing.T, writes <-chan []byte) {
	t.Helper()
	select {
	case written := <-writes:
		t.Fatalf("unexpected PTY write: %q", written)
	case <-time.After(50 * time.Millisecond):
	}
}
