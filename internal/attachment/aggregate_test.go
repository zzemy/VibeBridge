package attachment

import (
	"errors"
	"sync"
	"testing"
)

func TestAggregateTrackerReserveAndRelease(t *testing.T) {
	tracker := NewAggregateTracker(1000)

	if err := tracker.Reserve(400); err != nil {
		t.Fatalf("reserve 400: %v", err)
	}
	if got := tracker.TotalBytes(); got != 400 {
		t.Fatalf("total = %d, want 400", got)
	}

	if err := tracker.Reserve(500); err != nil {
		t.Fatalf("reserve 500: %v", err)
	}
	if got := tracker.TotalBytes(); got != 900 {
		t.Fatalf("total = %d, want 900", got)
	}

	tracker.Release(300)
	if got := tracker.TotalBytes(); got != 600 {
		t.Fatalf("after release 300, total = %d, want 600", got)
	}
}

func TestAggregateTrackerQuotaExceeded(t *testing.T) {
	tracker := NewAggregateTracker(1000)
	if err := tracker.Reserve(600); err != nil {
		t.Fatalf("reserve 600: %v", err)
	}
	err := tracker.Reserve(500)
	if !errors.Is(err, ErrAggregateQuotaExceeded) {
		t.Fatalf("reserve 500 over ceiling: err = %v, want ErrAggregateQuotaExceeded", err)
	}
	// Failed reserve must not change the total
	if got := tracker.TotalBytes(); got != 600 {
		t.Fatalf("after failed reserve, total = %d, want 600", got)
	}
}

func TestAggregateTrackerExactFit(t *testing.T) {
	tracker := NewAggregateTracker(1000)
	if err := tracker.Reserve(1000); err != nil {
		t.Fatalf("reserve exact ceiling: %v", err)
	}
	err := tracker.Reserve(1)
	if !errors.Is(err, ErrAggregateQuotaExceeded) {
		t.Fatalf("reserve 1 over ceiling: err = %v, want ErrAggregateQuotaExceeded", err)
	}
}

func TestAggregateTrackerReleaseClampsAtZero(t *testing.T) {
	tracker := NewAggregateTracker(1000)
	tracker.Reserve(100)
	tracker.Release(200) // over-release
	if got := tracker.TotalBytes(); got != 0 {
		t.Fatalf("after over-release, total = %d, want 0", got)
	}
}

func TestAggregateTrackerNilIsNoOp(t *testing.T) {
	var tracker *AggregateTracker
	if err := tracker.Reserve(1000); err != nil {
		t.Fatalf("nil tracker reserve: %v", err)
	}
	tracker.Release(1000)
	if got := tracker.TotalBytes(); got != 0 {
		t.Fatalf("nil tracker total = %d, want 0", got)
	}
	if got := tracker.MaxBytes(); got != 0 {
		t.Fatalf("nil tracker max = %d, want 0", got)
	}
}

func TestAggregateTrackerZeroMaxBytesDisables(t *testing.T) {
	tracker := NewAggregateTracker(0)
	if err := tracker.Reserve(999999999); err != nil {
		t.Fatalf("disabled tracker reserve: %v", err)
	}
	if got := tracker.TotalBytes(); got != 0 {
		t.Fatalf("disabled tracker total = %d, want 0", got)
	}
}

func TestAggregateTrackerConcurrent(t *testing.T) {
	tracker := NewAggregateTracker(10000)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tracker.Reserve(50)
			tracker.Release(50)
		}()
	}
	wg.Wait()
	if got := tracker.TotalBytes(); got != 0 {
		t.Fatalf("after concurrent reserve+release, total = %d, want 0", got)
	}
}

func TestNewDefaultAggregateTracker(t *testing.T) {
	tracker := NewDefaultAggregateTracker()
	if got := tracker.MaxBytes(); got != DefaultMaxAggregateBytes {
		t.Fatalf("default max = %d, want %d", got, DefaultMaxAggregateBytes)
	}
}
