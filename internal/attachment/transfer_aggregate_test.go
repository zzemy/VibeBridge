package attachment

import (
	"errors"
	"testing"
)

func newTestManagerWithAggregate(t *testing.T, limits managerLimits, aggregate *AggregateTracker) (*Manager, *SessionStaging) {
	t.Helper()
	staging, err := CreateSessionStaging(canonicalTestDirectory(t, t.TempDir()), []byte("aggregate-test"))
	if err != nil {
		t.Fatalf("create staging: %v", err)
	}
	manager, err := newTransferManager(staging, limits)
	if err != nil {
		t.Fatalf("create transfer manager: %v", err)
	}
	manager.aggregate = aggregate
	if aggregate != nil {
		if err := aggregate.Reserve(staging.completedBytes()); err != nil {
			_ = manager.Close()
			t.Fatalf("reserve inherited bytes: %v", err)
		}
	}
	t.Cleanup(func() {
		_ = manager.Close()
		_ = staging.Cleanup()
	})
	return manager, staging
}

func TestAggregateBlocksBeginWhenExceeded(t *testing.T) {
	tracker := NewAggregateTracker(100)
	limits := managerLimits{
		maxFileBytes:    100,
		maxSessionBytes: 200,
		maxChunkBytes:   64,
		maxActive:       4,
	}

	mgr1, _ := newTestManagerWithAggregate(t, limits, tracker)
	beginTransfer(t, mgr1, validBeginRequest([]byte("t1"), make([]byte, 80)))

	mgr2, _ := newTestManagerWithAggregate(t, limits, tracker)
	err := mgr2.Begin(validBeginRequest([]byte("t2"), make([]byte, 50)))
	if !errors.Is(err, ErrAggregateQuotaExceeded) {
		t.Fatalf("Begin over aggregate: err = %v, want ErrAggregateQuotaExceeded", err)
	}
}

func TestAggregateReleasesOnCancel(t *testing.T) {
	tracker := NewAggregateTracker(100)
	limits := managerLimits{
		maxFileBytes:    80,
		maxSessionBytes: 200,
		maxChunkBytes:   64,
		maxActive:       4,
	}

	manager, _ := newTestManagerWithAggregate(t, limits, tracker)
	beginTransfer(t, manager, validBeginRequest([]byte("t1"), make([]byte, 60)))

	if got := tracker.TotalBytes(); got != 60 {
		t.Fatalf("after begin, aggregate = %d, want 60", got)
	}

	if err := manager.Cancel([]byte("t1")); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if got := tracker.TotalBytes(); got != 0 {
		t.Fatalf("after cancel, aggregate = %d, want 0", got)
	}
}

func TestAggregateReleasesOnDiscard(t *testing.T) {
	tracker := NewAggregateTracker(200)
	limits := managerLimits{
		maxFileBytes:    80,
		maxSessionBytes: 200,
		maxChunkBytes:   64,
		maxActive:       4,
	}

	manager, _ := newTestManagerWithAggregate(t, limits, tracker)
	content := []byte("hello world test content")
	beginTransfer(t, manager, validBeginRequest([]byte("t1"), content))
	writeTransferChunk(t, manager, []byte("t1"), 0, content)
	if _, err := manager.Complete([]byte("t1")); err != nil {
		t.Fatalf("complete: %v", err)
	}

	if got := tracker.TotalBytes(); got != uint64(len(content)) {
		t.Fatalf("after complete, aggregate = %d, want %d", got, len(content))
	}

	if err := manager.Discard([][]byte{[]byte("t1")}); err != nil {
		t.Fatalf("discard: %v", err)
	}

	if got := tracker.TotalBytes(); got != 0 {
		t.Fatalf("after discard, aggregate = %d, want 0", got)
	}
}

func TestAggregateReleasesOnClose(t *testing.T) {
	tracker := NewAggregateTracker(200)
	limits := managerLimits{
		maxFileBytes:    80,
		maxSessionBytes: 200,
		maxChunkBytes:   64,
		maxActive:       4,
	}

	staging, err := CreateSessionStaging(canonicalTestDirectory(t, t.TempDir()), []byte("close-test"))
	if err != nil {
		t.Fatalf("create staging: %v", err)
	}
	defer staging.Cleanup()

	manager, err := newTransferManager(staging, limits)
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	manager.aggregate = tracker
	if err := tracker.Reserve(staging.completedBytes()); err != nil {
		_ = manager.Close()
		t.Fatalf("reserve inherited: %v", err)
	}

	content := []byte("hello world test")
	beginTransfer(t, manager, validBeginRequest([]byte("t1"), content))
	writeTransferChunk(t, manager, []byte("t1"), 0, content)
	if _, err := manager.Complete([]byte("t1")); err != nil {
		t.Fatalf("complete: %v", err)
	}

	if got := tracker.TotalBytes(); got != uint64(len(content)) {
		t.Fatalf("after complete, aggregate = %d, want %d", got, len(content))
	}

	if err := manager.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if got := tracker.TotalBytes(); got != 0 {
		t.Fatalf("after close, aggregate = %d, want 0", got)
	}
}

func TestAggregateNilTrackerNoOp(t *testing.T) {
	limits := managerLimits{
		maxFileBytes:    80,
		maxSessionBytes: 200,
		maxChunkBytes:   64,
		maxActive:       4,
	}

	manager, _ := newTestManagerWithAggregate(t, limits, nil)
	beginTransfer(t, manager, validBeginRequest([]byte("t1"), make([]byte, 50)))
	if err := manager.Cancel([]byte("t1")); err != nil {
		t.Fatalf("cancel with nil tracker: %v", err)
	}
}

func TestAggregateMultipleManagersShareTracker(t *testing.T) {
	tracker := NewAggregateTracker(150)
	limits := managerLimits{
		maxFileBytes:    80,
		maxSessionBytes: 200,
		maxChunkBytes:   64,
		maxActive:       4,
	}

	mgr1, _ := newTestManagerWithAggregate(t, limits, tracker)
	mgr2, _ := newTestManagerWithAggregate(t, limits, tracker)

	beginTransfer(t, mgr1, validBeginRequest([]byte("a1"), make([]byte, 50)))
	beginTransfer(t, mgr2, validBeginRequest([]byte("a2"), make([]byte, 50)))

	if got := tracker.TotalBytes(); got != 100 {
		t.Fatalf("aggregate = %d, want 100", got)
	}

	if err := mgr1.Cancel([]byte("a1")); err != nil {
		t.Fatalf("cancel a1: %v", err)
	}

	if got := tracker.TotalBytes(); got != 50 {
		t.Fatalf("after cancel a1, aggregate = %d, want 50", got)
	}

	beginTransfer(t, mgr2, validBeginRequest([]byte("a3"), make([]byte, 50)))

	if got := tracker.TotalBytes(); got != 100 {
		t.Fatalf("after begin a3, aggregate = %d, want 100", got)
	}
}

func TestNewManagerWithAggregateInheritsCompletedBytes(t *testing.T) {
	tracker := NewAggregateTracker(500)
	limits := managerLimits{
		maxFileBytes:    80,
		maxSessionBytes: 200,
		maxChunkBytes:   64,
		maxActive:       4,
	}

	staging, err := CreateSessionStaging(canonicalTestDirectory(t, t.TempDir()), []byte("inherit-test"))
	if err != nil {
		t.Fatalf("create staging: %v", err)
	}
	defer staging.Cleanup()

	mgr1, err := newTransferManager(staging, limits)
	if err != nil {
		t.Fatalf("create mgr1: %v", err)
	}
	content := []byte("inherited content data!!")
	beginTransfer(t, mgr1, validBeginRequest([]byte("t1"), content))
	writeTransferChunk(t, mgr1, []byte("t1"), 0, content)
	if _, err := mgr1.Complete([]byte("t1")); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := mgr1.Close(); err != nil {
		t.Fatalf("close mgr1: %v", err)
	}

	mgr2, err := NewManagerWithAggregate(staging, tracker)
	if err != nil {
		t.Fatalf("NewManagerWithAggregate: %v", err)
	}
	defer mgr2.Close()

	if got := tracker.TotalBytes(); got != uint64(len(content)) {
		t.Fatalf("after inherit, aggregate = %d, want %d", got, len(content))
	}
}
