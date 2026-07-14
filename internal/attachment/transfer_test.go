package attachment

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTransferManagerCompletesVerifiedTextAttachment(t *testing.T) {
	manager, workspaceRoot := newTestTransferManager(t, managerLimits{
		maxFileBytes:    32,
		maxSessionBytes: 64,
		maxChunkBytes:   8,
		maxActive:       2,
	})
	content := []byte("hello world")
	beginTransfer(t, manager, BeginRequest{
		TransferID:          []byte("text-transfer"),
		DisplayName:         "notes.txt",
		DeclaredContentType: "text/plain; charset=utf-8",
		DeclaredExtension:   ".txt",
		TotalSizeBytes:      uint64(len(content)),
		TotalSHA256:         hashBytes(content),
	})

	writeTransferChunk(t, manager, []byte("text-transfer"), 0, content[:6])
	writeTransferChunk(t, manager, []byte("text-transfer"), 6, content[6:])
	completed, err := manager.Complete([]byte("text-transfer"))
	if err != nil {
		t.Fatalf("complete attachment: %v", err)
	}
	if completed.DisplayName != "notes.txt" || completed.ContentType != "text/plain" || completed.SizeBytes != uint64(len(content)) {
		t.Fatalf("unexpected completed attachment: %+v", completed)
	}
	if completed.Extension != ".txt" {
		t.Fatalf("completed extension = %q, want .txt", completed.Extension)
	}
	if !filepath.IsLocal(completed.RelativePath) {
		t.Fatalf("completed path is not local: %q", completed.RelativePath)
	}
	if strings.Contains(completed.RelativePath, "notes") {
		t.Fatalf("completed path exposed display metadata: %q", completed.RelativePath)
	}
	if !bytes.Equal(completed.SHA256, hashBytes(content)) {
		t.Fatalf("completed checksum = %x, want %x", completed.SHA256, hashBytes(content))
	}
	stored, err := os.ReadFile(filepath.Join(workspaceRoot, completed.RelativePath))
	if err != nil {
		t.Fatalf("read completed attachment: %v", err)
	}
	if string(stored) != string(content) {
		t.Fatalf("stored content = %q, want %q", stored, content)
	}

	completed.TransferID[0] ^= 0xff
	completed.SHA256[0] ^= 0xff
	repeated, err := manager.Complete([]byte("text-transfer"))
	if err != nil {
		t.Fatalf("repeat complete: %v", err)
	}
	if repeated.RelativePath != completed.RelativePath {
		t.Fatalf("repeat complete path = %q, want %q", repeated.RelativePath, completed.RelativePath)
	}
	if !bytes.Equal(repeated.TransferID, []byte("text-transfer")) || !bytes.Equal(repeated.SHA256, hashBytes(content)) {
		t.Fatalf("repeat completion returned caller-mutated slices: %+v", repeated)
	}
}

func TestTransferManagerReportsSessionLocalOutcomes(t *testing.T) {
	manager, _ := newTestTransferManager(t, managerLimits{
		maxFileBytes:    32,
		maxSessionBytes: 64,
		maxChunkBytes:   8,
		maxActive:       2,
	})
	content := []byte("resume me")
	request := validBeginRequest([]byte("active"), content)
	beginTransfer(t, manager, request)
	writeTransferChunk(t, manager, request.TransferID, 0, content[:4])

	active, err := manager.Outcome(request.TransferID)
	if err != nil {
		t.Fatalf("query active transfer: %v", err)
	}
	if active.Disposition != TransferDispositionActive || active.NextOffsetBytes != 4 {
		t.Fatalf("active outcome = %+v, want active at offset 4", active)
	}

	writeTransferChunk(t, manager, request.TransferID, 4, content[4:])
	if _, err := manager.Complete(request.TransferID); err != nil {
		t.Fatalf("complete transfer: %v", err)
	}
	completed, err := manager.Outcome(request.TransferID)
	if err != nil {
		t.Fatalf("query completed transfer: %v", err)
	}
	if completed.Disposition != TransferDispositionCompleted || completed.NextOffsetBytes != 0 {
		t.Fatalf("completed outcome = %+v, want completed without an offset", completed)
	}

	cancelledID := []byte("cancelled")
	beginTransfer(t, manager, validBeginRequest(cancelledID, []byte("partial")))
	if err := manager.Cancel(cancelledID); err != nil {
		t.Fatalf("cancel transfer: %v", err)
	}
	cancelled, err := manager.Outcome(cancelledID)
	if err != nil {
		t.Fatalf("query cancelled transfer: %v", err)
	}
	if cancelled.Disposition != TransferDispositionCancelled || cancelled.NextOffsetBytes != 0 {
		t.Fatalf("cancelled outcome = %+v, want cancelled without an offset", cancelled)
	}

	unknown, err := manager.Outcome([]byte("unknown"))
	if err != nil {
		t.Fatalf("query unknown transfer: %v", err)
	}
	if unknown.Disposition != TransferDispositionUnknown || unknown.NextOffsetBytes != 0 {
		t.Fatalf("unknown outcome = %+v, want unknown without an offset", unknown)
	}
	if _, err := manager.Outcome(nil); !errors.Is(err, ErrInvalidTransfer) {
		t.Fatalf("invalid outcome query error = %v, want %v", err, ErrInvalidTransfer)
	}
}

func TestTransferManagerBoundsCancelledOutcomeTombstones(t *testing.T) {
	manager, _ := newTestTransferManager(t, managerLimits{
		maxFileBytes:    8,
		maxSessionBytes: 8,
		maxChunkBytes:   8,
		maxActive:       1,
	})
	for index := 0; index <= maxCancelledOutcomes; index++ {
		if err := manager.Cancel([]byte(fmt.Sprintf("cancel-%d", index))); err != nil {
			t.Fatalf("cancel transfer %d: %v", index, err)
		}
	}
	oldest, err := manager.Outcome([]byte("cancel-0"))
	if err != nil {
		t.Fatalf("query evicted cancellation: %v", err)
	}
	if oldest.Disposition != TransferDispositionUnknown {
		t.Fatalf("evicted cancellation = %+v, want unknown", oldest)
	}
	newest, err := manager.Outcome([]byte(fmt.Sprintf("cancel-%d", maxCancelledOutcomes)))
	if err != nil {
		t.Fatalf("query retained cancellation: %v", err)
	}
	if newest.Disposition != TransferDispositionCancelled {
		t.Fatalf("retained cancellation = %+v, want cancelled", newest)
	}
}

func TestTransferManagerResolvesOnlyUniqueCompletedAttachments(t *testing.T) {
	manager, _ := newTestTransferManager(t, managerLimits{
		maxFileBytes:    32,
		maxSessionBytes: 64,
		maxChunkBytes:   32,
		maxActive:       2,
	})
	firstContent := []byte("first")
	secondContent := []byte("second")
	for _, fixture := range []struct {
		id      []byte
		content []byte
	}{
		{id: []byte("first"), content: firstContent},
		{id: []byte("second"), content: secondContent},
	} {
		request := validBeginRequest(fixture.id, fixture.content)
		beginTransfer(t, manager, request)
		writeTransferChunk(t, manager, request.TransferID, 0, fixture.content)
		if _, err := manager.Complete(request.TransferID); err != nil {
			t.Fatalf("complete %q: %v", fixture.id, err)
		}
	}
	beginTransfer(t, manager, validBeginRequest([]byte("active"), []byte("pending")))

	completed, err := manager.CompletedAttachments([][]byte{[]byte("second"), []byte("first")})
	if err != nil {
		t.Fatalf("resolve completed attachments: %v", err)
	}
	if len(completed) != 2 || !bytes.Equal(completed[0].TransferID, []byte("second")) || !bytes.Equal(completed[1].TransferID, []byte("first")) {
		t.Fatalf("completed attachment order = %+v, want second then first", completed)
	}
	completed[0].TransferID[0] = 'X'
	completed[0].SHA256[0] ^= 0xff
	repeated, err := manager.CompletedAttachments([][]byte{[]byte("second")})
	if err != nil {
		t.Fatalf("repeat completed lookup: %v", err)
	}
	if !bytes.Equal(repeated[0].TransferID, []byte("second")) || !bytes.Equal(repeated[0].SHA256, hashBytes(secondContent)) {
		t.Fatalf("lookup returned caller-mutated attachment: %+v", repeated[0])
	}

	tests := []struct {
		name string
		ids  [][]byte
		want error
	}{
		{name: "empty selection", ids: nil, want: ErrInvalidAttachmentSelection},
		{name: "over limit", ids: makeTransferIDs(maxPromptActionAttachments + 1), want: ErrAttachmentSelectionLimit},
		{name: "duplicate", ids: [][]byte{[]byte("first"), []byte("first")}, want: ErrDuplicateAttachment},
		{name: "active", ids: [][]byte{[]byte("active")}, want: ErrAttachmentNotCompleted},
		{name: "unknown", ids: [][]byte{[]byte("unknown")}, want: ErrAttachmentNotCompleted},
		{name: "invalid id", ids: [][]byte{nil}, want: ErrInvalidTransfer},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := manager.CompletedAttachments(test.ids); !errors.Is(err, test.want) {
				t.Fatalf("CompletedAttachments() error = %v, want %v", err, test.want)
			}
		})
	}
}

func makeTransferIDs(count int) [][]byte {
	ids := make([][]byte, count)
	for index := range ids {
		ids[index] = []byte(fmt.Sprintf("transfer-%d", index))
	}
	return ids
}

func TestTransferManagerRejectsInvalidBeginWithoutCreatingFiles(t *testing.T) {
	limits := managerLimits{maxFileBytes: 16, maxSessionBytes: 24, maxChunkBytes: 8, maxActive: 1}
	tests := []struct {
		name    string
		request BeginRequest
		want    error
	}{
		{name: "missing transfer id", request: validBeginRequest(nil, []byte("data")), want: ErrInvalidTransfer},
		{name: "oversized transfer id", request: validBeginRequest(make([]byte, maxTransferIDBytes+1), []byte("data")), want: ErrInvalidTransfer},
		{name: "empty display name", request: withDisplayName(validBeginRequest([]byte("id"), []byte("data")), ""), want: ErrInvalidMetadata},
		{name: "control display name", request: withDisplayName(validBeginRequest([]byte("id"), []byte("data")), "bad\nname.txt"), want: ErrInvalidMetadata},
		{name: "format control display name", request: withDisplayName(validBeginRequest([]byte("id"), []byte("data")), "bad\u202ename.txt"), want: ErrInvalidMetadata},
		{name: "invalid UTF-8 display name", request: withDisplayName(validBeginRequest([]byte("id"), []byte("data")), string([]byte{0xff})), want: ErrInvalidMetadata},
		{name: "oversized display name", request: withDisplayName(validBeginRequest([]byte("id"), []byte("data")), strings.Repeat("a", maxDisplayNameBytes+1)), want: ErrInvalidMetadata},
		{name: "zero size", request: withSize(validBeginRequest([]byte("id"), []byte("data")), 0), want: ErrFileLimitExceeded},
		{name: "file too large", request: withSize(validBeginRequest([]byte("id"), []byte("data")), 17), want: ErrFileLimitExceeded},
		{name: "invalid total hash", request: withTotalHash(validBeginRequest([]byte("id"), []byte("data")), []byte("short")), want: ErrInvalidTransfer},
		{name: "unsupported extension", request: withExtension(validBeginRequest([]byte("id"), []byte("data")), ".exe"), want: ErrUnsupportedContent},
		{name: "mismatched MIME", request: withContentType(validBeginRequest([]byte("id"), []byte("data")), "image/png"), want: ErrUnsupportedContent},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, staging := newTestTransferManagerWithStaging(t, limits)
			if err := manager.Begin(test.request); !errors.Is(err, test.want) {
				t.Fatalf("Begin() error = %v, want %v", err, test.want)
			}
			assertStagingEntryCount(t, staging, 0)
		})
	}
}

func TestTransferManagerEnforcesActiveAndSessionQuota(t *testing.T) {
	manager, staging := newTestTransferManagerWithStaging(t, managerLimits{
		maxFileBytes:    16,
		maxSessionBytes: 12,
		maxChunkBytes:   8,
		maxActive:       1,
	})
	first := validBeginRequest([]byte("first"), []byte("12345678"))
	if err := manager.Begin(first); err != nil {
		t.Fatalf("begin first transfer: %v", err)
	}
	if err := manager.Begin(validBeginRequest([]byte("second"), []byte("data"))); !errors.Is(err, ErrActiveTransferLimit) {
		t.Fatalf("active transfer error = %v, want %v", err, ErrActiveTransferLimit)
	}
	if err := manager.Cancel(first.TransferID); err != nil {
		t.Fatalf("cancel first transfer: %v", err)
	}
	if err := manager.Begin(validBeginRequest([]byte("quota"), []byte("1234567890123"))); !errors.Is(err, ErrSessionQuotaExceeded) {
		t.Fatalf("session quota error = %v, want %v", err, ErrSessionQuotaExceeded)
	}
	assertStagingEntryCount(t, staging, 0)
}

func TestTransferManagerRejectsDuplicateAndInvalidChunksWithoutMutation(t *testing.T) {
	manager, _ := newTestTransferManager(t, managerLimits{
		maxFileBytes:    32,
		maxSessionBytes: 64,
		maxChunkBytes:   4,
		maxActive:       2,
	})
	content := []byte("abcdefgh")
	request := validBeginRequest([]byte("chunks"), content)
	beginTransfer(t, manager, request)
	if err := manager.Begin(request); !errors.Is(err, ErrTransferExists) {
		t.Fatalf("duplicate begin error = %v, want %v", err, ErrTransferExists)
	}

	invalidHash := hashBytes([]byte("other"))
	if err := manager.Chunk(ChunkRequest{TransferID: request.TransferID, OffsetBytes: 0, Data: content[:4], ChunkSHA256: invalidHash}); !errors.Is(err, ErrChunkChecksumMismatch) {
		t.Fatalf("chunk hash error = %v, want %v", err, ErrChunkChecksumMismatch)
	}
	if err := manager.Chunk(ChunkRequest{TransferID: request.TransferID, OffsetBytes: 1, Data: content[:4], ChunkSHA256: hashBytes(content[:4])}); !errors.Is(err, ErrChunkOffsetMismatch) {
		t.Fatalf("chunk offset error = %v, want %v", err, ErrChunkOffsetMismatch)
	}
	if err := manager.Chunk(ChunkRequest{TransferID: request.TransferID, OffsetBytes: 0, Data: content[:5], ChunkSHA256: hashBytes(content[:5])}); !errors.Is(err, ErrChunkLimitExceeded) {
		t.Fatalf("chunk limit error = %v, want %v", err, ErrChunkLimitExceeded)
	}
	writeTransferChunk(t, manager, request.TransferID, 0, content[:4])
	if err := manager.Chunk(ChunkRequest{TransferID: request.TransferID, OffsetBytes: 0, Data: content[:4], ChunkSHA256: hashBytes(content[:4])}); !errors.Is(err, ErrChunkOffsetMismatch) {
		t.Fatalf("duplicate chunk error = %v, want %v", err, ErrChunkOffsetMismatch)
	}
	writeTransferChunk(t, manager, request.TransferID, 4, content[4:])
}

func TestTransferManagerRejectsChunkPastDeclaredSizeWithoutMutation(t *testing.T) {
	manager, _ := newTestTransferManager(t, managerLimits{maxFileBytes: 16, maxSessionBytes: 16, maxChunkBytes: 8, maxActive: 1})
	content := []byte("data")
	request := validBeginRequest([]byte("size-bound"), content)
	beginTransfer(t, manager, request)
	extra := []byte("data!")
	if err := manager.Chunk(ChunkRequest{TransferID: request.TransferID, Data: extra, ChunkSHA256: hashBytes(extra)}); !errors.Is(err, ErrTotalSizeMismatch) {
		t.Fatalf("oversized chunk error = %v, want %v", err, ErrTotalSizeMismatch)
	}
	writeTransferChunk(t, manager, request.TransferID, 0, content)
	if _, err := manager.Complete(request.TransferID); err != nil {
		t.Fatalf("complete after corrected chunk: %v", err)
	}
}

func TestTransferManagerIntegrityFailureDeletesPartialAndReleasesQuota(t *testing.T) {
	tests := []struct {
		name       string
		content    []byte
		beginBytes []byte
		writeBytes []byte
		want       error
	}{
		{name: "size mismatch", content: []byte("1234"), beginBytes: []byte("1234"), writeBytes: []byte("12"), want: ErrTotalSizeMismatch},
		{name: "total hash mismatch", content: []byte("1234"), beginBytes: []byte("xxxx"), writeBytes: []byte("1234"), want: ErrTotalChecksumMismatch},
		{name: "content mismatch", content: []byte("plain text"), beginBytes: []byte("plain text"), writeBytes: []byte("plain text"), want: ErrContentTypeMismatch},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, staging := newTestTransferManagerWithStaging(t, managerLimits{
				maxFileBytes:    16,
				maxSessionBytes: 16,
				maxChunkBytes:   16,
				maxActive:       1,
			})
			request := validBeginRequest([]byte("integrity"), test.beginBytes)
			if test.want == ErrContentTypeMismatch {
				request.DeclaredContentType = "image/png"
				request.DeclaredExtension = ".png"
			}
			beginTransfer(t, manager, request)
			if len(test.writeBytes) > 0 {
				writeTransferChunk(t, manager, request.TransferID, 0, test.writeBytes)
			}
			if _, err := manager.Complete(request.TransferID); !errors.Is(err, test.want) {
				t.Fatalf("Complete() error = %v, want %v", err, test.want)
			}
			assertStagingEntryCount(t, staging, 0)
			if err := manager.Begin(validBeginRequest([]byte("retry"), make([]byte, 16))); err != nil {
				t.Fatalf("quota was not released after failed completion: %v", err)
			}
		})
	}
}

func TestTransferManagerCancelIsIdempotentAndAllowsRestart(t *testing.T) {
	manager, staging := newTestTransferManagerWithStaging(t, managerLimits{
		maxFileBytes:    16,
		maxSessionBytes: 16,
		maxChunkBytes:   8,
		maxActive:       1,
	})
	request := validBeginRequest([]byte("cancel"), []byte("12345678"))
	beginTransfer(t, manager, request)
	writeTransferChunk(t, manager, request.TransferID, 0, []byte("1234"))
	if err := manager.Cancel(request.TransferID); err != nil {
		t.Fatalf("cancel transfer: %v", err)
	}
	if err := manager.Cancel(request.TransferID); err != nil {
		t.Fatalf("repeat cancel transfer: %v", err)
	}
	assertStagingEntryCount(t, staging, 0)
	if err := manager.Begin(request); err != nil {
		t.Fatalf("restart cancelled transfer: %v", err)
	}
}

func TestTransferManagerCompletedBytesRemainReserved(t *testing.T) {
	manager, _ := newTestTransferManager(t, managerLimits{
		maxFileBytes:    8,
		maxSessionBytes: 8,
		maxChunkBytes:   8,
		maxActive:       1,
	})
	content := []byte("12345678")
	request := validBeginRequest([]byte("complete"), content)
	beginTransfer(t, manager, request)
	writeTransferChunk(t, manager, request.TransferID, 0, content)
	if _, err := manager.Complete(request.TransferID); err != nil {
		t.Fatalf("complete transfer: %v", err)
	}
	if err := manager.Cancel(request.TransferID); err != nil {
		t.Fatalf("cancel completed transfer should be idempotent: %v", err)
	}
	if err := manager.Begin(validBeginRequest([]byte("next"), []byte("x"))); !errors.Is(err, ErrSessionQuotaExceeded) {
		t.Fatalf("completed quota error = %v, want %v", err, ErrSessionQuotaExceeded)
	}
}

func TestTransferManagerCompletedQuotaSurvivesManagerReopen(t *testing.T) {
	staging, err := CreateSessionStaging(canonicalTestDirectory(t, t.TempDir()), []byte("quota-reopen"))
	if err != nil {
		t.Fatalf("create staging: %v", err)
	}
	limits := managerLimits{maxFileBytes: 8, maxSessionBytes: 8, maxChunkBytes: 8, maxActive: 1}
	first, err := newTransferManager(staging, limits)
	if err != nil {
		t.Fatalf("create first manager: %v", err)
	}
	content := []byte("12345678")
	request := validBeginRequest([]byte("first"), content)
	beginTransfer(t, first, request)
	writeTransferChunk(t, first, request.TransferID, 0, content)
	if _, err := first.Complete(request.TransferID); err != nil {
		t.Fatalf("complete first attachment: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first manager: %v", err)
	}

	second, err := newTransferManager(staging, limits)
	if err != nil {
		t.Fatalf("reopen manager: %v", err)
	}
	if err := second.Begin(validBeginRequest([]byte("second"), []byte("x"))); !errors.Is(err, ErrSessionQuotaExceeded) {
		t.Fatalf("reopened manager quota error = %v, want %v", err, ErrSessionQuotaExceeded)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("close second manager: %v", err)
	}
	if err := staging.Cleanup(); err != nil {
		t.Fatalf("cleanup staging: %v", err)
	}
}

func TestTransferManagerDiscardsMixedBatchAndReleasesQuota(t *testing.T) {
	manager, staging := newTestTransferManagerWithStaging(t, managerLimits{
		maxFileBytes:    8,
		maxSessionBytes: 8,
		maxChunkBytes:   8,
		maxActive:       2,
	})
	completedID := []byte("completed")
	completedContent := []byte("done")
	beginTransfer(t, manager, validBeginRequest(completedID, completedContent))
	writeTransferChunk(t, manager, completedID, 0, completedContent)
	if _, err := manager.Complete(completedID); err != nil {
		t.Fatalf("complete attachment: %v", err)
	}
	activeID := []byte("active")
	beginTransfer(t, manager, validBeginRequest(activeID, []byte("work")))
	cancelledID := []byte("cancelled")
	if err := manager.Cancel(cancelledID); err != nil {
		t.Fatalf("seed cancelled outcome: %v", err)
	}

	if err := manager.Discard([][]byte{completedID, activeID, cancelledID, []byte("unknown")}); err != nil {
		t.Fatalf("discard attachment batch: %v", err)
	}
	if err := manager.Discard([][]byte{completedID, activeID, cancelledID, []byte("unknown")}); err != nil {
		t.Fatalf("repeat discard attachment batch: %v", err)
	}
	assertStagingEntryCount(t, staging, 0)
	if got := staging.completedBytes(); got != 0 {
		t.Fatalf("completed staged bytes = %d, want 0", got)
	}
	for _, transferID := range [][]byte{completedID, activeID, cancelledID, []byte("unknown")} {
		outcome, err := manager.Outcome(transferID)
		if err != nil {
			t.Fatalf("query discarded transfer %q: %v", transferID, err)
		}
		if outcome.Disposition != TransferDispositionCancelled || outcome.NextOffsetBytes != 0 {
			t.Fatalf("discarded transfer %q outcome = %+v, want cancelled", transferID, outcome)
		}
	}

	fullQuotaContent := []byte("12345678")
	if err := manager.Begin(validBeginRequest([]byte("replacement"), fullQuotaContent)); err != nil {
		t.Fatalf("begin after discard released quota: %v", err)
	}
}

func TestTransferManagerDiscardRetainsEveryBatchTombstoneAtCapacity(t *testing.T) {
	manager, _ := newTestTransferManager(t, managerLimits{
		maxFileBytes:    8,
		maxSessionBytes: 8,
		maxChunkBytes:   8,
		maxActive:       1,
	})
	for index := 0; index < maxCancelledOutcomes; index++ {
		if err := manager.Cancel([]byte(fmt.Sprintf("cancel-%d", index))); err != nil {
			t.Fatalf("seed cancelled transfer %d: %v", index, err)
		}
	}
	oldestID := []byte("cancel-0")
	newID := []byte("discard-new")

	if err := manager.Discard([][]byte{oldestID, newID}); err != nil {
		t.Fatalf("discard at tombstone capacity: %v", err)
	}
	for _, transferID := range [][]byte{oldestID, newID} {
		outcome, err := manager.Outcome(transferID)
		if err != nil {
			t.Fatalf("query discarded transfer %q: %v", transferID, err)
		}
		if outcome.Disposition != TransferDispositionCancelled {
			t.Fatalf("discarded transfer %q outcome = %+v, want cancelled", transferID, outcome)
		}
	}
}

func TestTransferManagerRetriesDiscardAfterPartialFilesystemFailure(t *testing.T) {
	manager, staging := newTestTransferManagerWithStaging(t, managerLimits{
		maxFileBytes:    8,
		maxSessionBytes: 8,
		maxChunkBytes:   8,
		maxActive:       2,
	})
	completedID := []byte("completed")
	content := []byte("done")
	beginTransfer(t, manager, validBeginRequest(completedID, content))
	writeTransferChunk(t, manager, completedID, 0, content)
	completed, err := manager.Complete(completedID)
	if err != nil {
		t.Fatalf("complete attachment: %v", err)
	}
	activeID := []byte("active")
	beginTransfer(t, manager, validBeginRequest(activeID, content))

	completedPath := filepath.Join(staging.workspaceRoot, completed.RelativePath)
	if err := os.Remove(completedPath); err != nil {
		t.Fatalf("replace completed file for failure test: %v", err)
	}
	if err := os.Mkdir(completedPath, 0o700); err != nil {
		t.Fatalf("create replacement directory for failure test: %v", err)
	}

	if err := manager.Discard([][]byte{activeID, completedID}); err == nil {
		t.Fatal("discard with replaced completed entry succeeded, want failure")
	}
	activeOutcome, err := manager.Outcome(activeID)
	if err != nil {
		t.Fatalf("query active transfer after partial failure: %v", err)
	}
	if activeOutcome.Disposition != TransferDispositionCancelled {
		t.Fatalf("active transfer after partial failure = %+v, want cancelled", activeOutcome)
	}
	completedOutcome, err := manager.Outcome(completedID)
	if err != nil {
		t.Fatalf("query completed transfer after partial failure: %v", err)
	}
	if completedOutcome.Disposition != TransferDispositionCompleted {
		t.Fatalf("completed transfer after partial failure = %+v, want completed", completedOutcome)
	}
	if got := staging.completedBytes(); got != uint64(len(content)) {
		t.Fatalf("completed quota after failed removal = %d, want %d", got, len(content))
	}

	replacementID := []byte("replacement")
	beginTransfer(t, manager, validBeginRequest(replacementID, content))
	if err := manager.Cancel(replacementID); err != nil {
		t.Fatalf("cancel replacement transfer: %v", err)
	}
	if err := manager.Begin(validBeginRequest([]byte("over-quota"), []byte("12345"))); !errors.Is(err, ErrSessionQuotaExceeded) {
		t.Fatalf("begin while completed quota remains error = %v, want %v", err, ErrSessionQuotaExceeded)
	}

	if err := os.Remove(completedPath); err != nil {
		t.Fatalf("remove replacement directory before retry: %v", err)
	}
	if err := manager.Discard([][]byte{activeID, completedID}); err != nil {
		t.Fatalf("retry discard after filesystem recovery: %v", err)
	}
	if got := staging.completedBytes(); got != 0 {
		t.Fatalf("completed quota after retry = %d, want 0", got)
	}
	if err := manager.Begin(validBeginRequest([]byte("full-quota"), []byte("12345678"))); err != nil {
		t.Fatalf("begin after retry released quota: %v", err)
	}
}

func TestTransferManagerValidatesDiscardBatchBeforeMutation(t *testing.T) {
	manager, staging := newTestTransferManagerWithStaging(t, managerLimits{
		maxFileBytes:    8,
		maxSessionBytes: 8,
		maxChunkBytes:   8,
		maxActive:       1,
	})
	transferID := []byte("completed")
	content := []byte("data")
	beginTransfer(t, manager, validBeginRequest(transferID, content))
	writeTransferChunk(t, manager, transferID, 0, content)
	if _, err := manager.Complete(transferID); err != nil {
		t.Fatalf("complete attachment: %v", err)
	}

	tests := []struct {
		name string
		ids  [][]byte
		want error
	}{
		{name: "empty", want: ErrInvalidAttachmentSelection},
		{name: "over limit", ids: makeTransferIDs(maxDiscardAttachments + 1), want: ErrAttachmentSelectionLimit},
		{name: "invalid ID", ids: [][]byte{transferID, nil}, want: ErrInvalidTransfer},
		{name: "duplicate", ids: [][]byte{transferID, transferID}, want: ErrDuplicateAttachment},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := manager.Discard(test.ids); !errors.Is(err, test.want) {
				t.Fatalf("Discard() error = %v, want %v", err, test.want)
			}
			outcome, err := manager.Outcome(transferID)
			if err != nil {
				t.Fatalf("query completed transfer: %v", err)
			}
			if outcome.Disposition != TransferDispositionCompleted {
				t.Fatalf("completed transfer mutated after invalid discard: %+v", outcome)
			}
			assertStagingEntryCount(t, staging, 1)
		})
	}
}

func TestTransferManagerCloseRemovesActiveTransfersBeforeStagingCleanup(t *testing.T) {
	workspaceRoot := canonicalTestDirectory(t, t.TempDir())
	staging, err := CreateSessionStaging(workspaceRoot, []byte("manager-close"))
	if err != nil {
		t.Fatalf("create staging: %v", err)
	}
	manager, err := newTransferManager(staging, managerLimits{
		maxFileBytes:    16,
		maxSessionBytes: 16,
		maxChunkBytes:   8,
		maxActive:       1,
	})
	if err != nil {
		t.Fatalf("create transfer manager: %v", err)
	}
	beginTransfer(t, manager, validBeginRequest([]byte("active"), []byte("data")))

	if err := manager.Close(); err != nil {
		t.Fatalf("close transfer manager: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("repeat close transfer manager: %v", err)
	}
	if err := manager.Begin(validBeginRequest([]byte("closed"), []byte("data"))); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("begin after close error = %v, want %v", err, ErrManagerClosed)
	}
	if err := staging.Cleanup(); err != nil {
		t.Fatalf("cleanup staging after manager close: %v", err)
	}
}

func newTestTransferManager(t *testing.T, limits managerLimits) (*Manager, string) {
	t.Helper()
	manager, staging := newTestTransferManagerWithStaging(t, limits)
	return manager, staging.workspaceRoot
}

func newTestTransferManagerWithStaging(t *testing.T, limits managerLimits) (*Manager, *SessionStaging) {
	t.Helper()
	staging, err := CreateSessionStaging(canonicalTestDirectory(t, t.TempDir()), []byte("transfer-manager"))
	if err != nil {
		t.Fatalf("create staging: %v", err)
	}
	manager, err := newTransferManager(staging, limits)
	if err != nil {
		t.Fatalf("create transfer manager: %v", err)
	}
	t.Cleanup(func() {
		_ = manager.Close()
		_ = staging.Cleanup()
	})
	return manager, staging
}

func beginTransfer(t *testing.T, manager *Manager, request BeginRequest) {
	t.Helper()
	if err := manager.Begin(request); err != nil {
		t.Fatalf("begin transfer: %v", err)
	}
}

func writeTransferChunk(t *testing.T, manager *Manager, transferID []byte, offset uint64, data []byte) {
	t.Helper()
	if err := manager.Chunk(ChunkRequest{
		TransferID:  transferID,
		OffsetBytes: offset,
		Data:        data,
		ChunkSHA256: hashBytes(data),
	}); err != nil {
		t.Fatalf("write transfer chunk at %d: %v", offset, err)
	}
}

func validBeginRequest(transferID []byte, content []byte) BeginRequest {
	return BeginRequest{
		TransferID:          transferID,
		DisplayName:         "file.txt",
		DeclaredContentType: "text/plain",
		DeclaredExtension:   ".txt",
		TotalSizeBytes:      uint64(len(content)),
		TotalSHA256:         hashBytes(content),
	}
}

func withDisplayName(request BeginRequest, displayName string) BeginRequest {
	request.DisplayName = displayName
	return request
}

func withSize(request BeginRequest, size uint64) BeginRequest {
	request.TotalSizeBytes = size
	return request
}

func withTotalHash(request BeginRequest, hash []byte) BeginRequest {
	request.TotalSHA256 = hash
	return request
}

func withExtension(request BeginRequest, extension string) BeginRequest {
	request.DeclaredExtension = extension
	return request
}

func withContentType(request BeginRequest, contentType string) BeginRequest {
	request.DeclaredContentType = contentType
	return request
}

func hashBytes(data []byte) []byte {
	hash := sha256.Sum256(data)
	return hash[:]
}

func assertStagingEntryCount(t *testing.T, staging *SessionStaging, want int) {
	t.Helper()
	entries, err := os.ReadDir(staging.Path())
	if err != nil {
		t.Fatalf("read staging directory: %v", err)
	}
	if len(entries) != want {
		t.Fatalf("staging entry count = %d, want %d", len(entries), want)
	}
}
