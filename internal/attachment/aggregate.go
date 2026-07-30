package attachment

import (
	"errors"
	"sync"
)

// ErrAggregateQuotaExceeded indicates that the cross-session aggregate byte
// ceiling has been exceeded.
var ErrAggregateQuotaExceeded = errors.New("attachment aggregate quota exceeded")

// DefaultMaxAggregateBytes is the combined byte ceiling across all transfer
// managers sharing one AggregateTracker when no explicit limit is configured.
const DefaultMaxAggregateBytes = 500 * 1024 * 1024 // 500 MB

// AggregateTracker enforces a combined byte ceiling across all transfer
// managers that share it. A zero-value tracker (maxBytes == 0) disables
// aggregate limiting — all Reserve calls succeed and Release is a no-op.
// It is safe for concurrent use.
type AggregateTracker struct {
	mu         sync.Mutex
	totalBytes uint64
	maxBytes   uint64
}

// NewAggregateTracker creates a tracker with the given byte ceiling. A zero
// maxBytes disables aggregate limiting.
func NewAggregateTracker(maxBytes uint64) *AggregateTracker {
	return &AggregateTracker{maxBytes: maxBytes}
}

// NewDefaultAggregateTracker creates a tracker with the default 500 MB ceiling.
func NewDefaultAggregateTracker() *AggregateTracker {
	return NewAggregateTracker(DefaultMaxAggregateBytes)
}

// Reserve attempts to add bytes to the aggregate. It returns
// ErrAggregateQuotaExceeded if the ceiling would be exceeded. A nil receiver
// or zero maxBytes makes this a no-op that always succeeds.
func (t *AggregateTracker) Reserve(bytes uint64) error {
	if t == nil || t.maxBytes == 0 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.totalBytes+bytes > t.maxBytes {
		return ErrAggregateQuotaExceeded
	}
	t.totalBytes += bytes
	return nil
}

// Release subtracts bytes from the aggregate, clamped at zero.
func (t *AggregateTracker) Release(bytes uint64) {
	if t == nil || t.maxBytes == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if bytes <= t.totalBytes {
		t.totalBytes -= bytes
	} else {
		t.totalBytes = 0
	}
}

// TotalBytes returns the current aggregate usage.
func (t *AggregateTracker) TotalBytes() uint64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.totalBytes
}

// MaxBytes returns the configured ceiling. Zero means unlimited.
func (t *AggregateTracker) MaxBytes() uint64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.maxBytes
}
