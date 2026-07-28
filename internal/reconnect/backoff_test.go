package reconnect

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"
)

// deterministicFloat64 returns a Float64Source that always emits
// the given value. Useful for asserting that the wait schedule
// honours the math without depending on the local RNG.
func deterministicFloat64(v float64) Float64Source {
	return func() float64 { return v }
}

// scriptedFloat64 returns a Float64Source that emits the values
// in seq in order, looping back to the start once exhausted. Used
// to walk Delay through a known sequence.
func scriptedFloat64(seq ...float64) Float64Source {
	idx := 0
	return func() float64 {
		v := seq[idx%len(seq)]
		idx++
		return v
	}
}

func TestConfigZeroValueUsesDefaults(t *testing.T) {
	var c Config
	if c.baseDelay() != DefaultBaseDelay {
		t.Fatalf("baseDelay: got %v, want %v", c.baseDelay(), DefaultBaseDelay)
	}
	if c.maxDelay() != DefaultMaxDelay {
		t.Fatalf("maxDelay: got %v, want %v", c.maxDelay(), DefaultMaxDelay)
	}
	if c.multiplier() != DefaultMultiplier {
		t.Fatalf("multiplier: got %v, want %v", c.multiplier(), DefaultMultiplier)
	}
}

func TestDelayFirstAttemptEqualsBase(t *testing.T) {
	c := Config{
		BaseDelay: 200 * time.Millisecond,
		MaxDelay:  10 * time.Second,
		Rand:      deterministicFloat64(0.5),
	}
	if got := c.Delay(1); got != 200*time.Millisecond {
		t.Fatalf("first attempt: got %v, want 200ms", got)
	}
}

func TestDelayDecorrelatedJitterFormula(t *testing.T) {
	c := Config{
		BaseDelay:  100 * time.Millisecond,
		MaxDelay:   10 * time.Second,
		Multiplier: 3.0,
		Rand:       deterministicFloat64(0.5),
	}
	// delay(1) = 100ms
	// delay(2) = min(10s, 0.5 * 100ms * 3) = 150ms
	// delay(3) = min(10s, 0.5 * 150ms * 3) = 225ms
	// delay(4) = min(10s, 0.5 * 225ms * 3) = 337.5ms
	want := []time.Duration{
		100 * time.Millisecond,
		150 * time.Millisecond,
		225 * time.Millisecond,
		337500 * time.Microsecond,
	}
	for i, w := range want {
		if got := c.Delay(i + 1); got != w {
			t.Fatalf("delay(%d): got %v, want %v", i+1, got, w)
		}
	}
}

func TestDelayClampsAtMaxDelay(t *testing.T) {
	c := Config{
		BaseDelay:  1 * time.Second,
		MaxDelay:   2 * time.Second,
		Multiplier: 3.0,
		Rand:       deterministicFloat64(1.0),
	}
	// Even with the largest possible jitter (rand=1) the delay
	// must never exceed MaxDelay.
	for n := 1; n < 20; n++ {
		if got := c.Delay(n); got > 2*time.Second {
			t.Fatalf("delay(%d) = %v, want <= 2s", n, got)
		}
	}
}

func TestDelayZeroOrNegativeIsImmediate(t *testing.T) {
	c := Config{BaseDelay: 100 * time.Millisecond, Rand: deterministicFloat64(0.5)}
	if got := c.Delay(0); got != 0 {
		t.Fatalf("delay(0): got %v, want 0", got)
	}
	if got := c.Delay(-1); got != 0 {
		t.Fatalf("delay(-1): got %v, want 0", got)
	}
}

func TestDelayClampsMultiplier(t *testing.T) {
	c := Config{
		BaseDelay:  100 * time.Millisecond,
		MaxDelay:   10 * time.Second,
		Multiplier: 0.5, // below 1 must be clamped to DefaultMultiplier
		Rand:       deterministicFloat64(0.5),
	}
	// With clamped multiplier=3 the schedule matches TestDelayDecorrelatedJitterFormula.
	if got := c.Delay(2); got != 150*time.Millisecond {
		t.Fatalf("clamped multiplier delay(2): got %v, want 150ms", got)
	}
}

func TestWaitRespectsContextCancel(t *testing.T) {
	c := Config{
		BaseDelay: 5 * time.Second,
		Rand:      deterministicFloat64(0.0),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- c.Wait(ctx, 1)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("Wait did not return on cancel")
	}
}

func TestWaitReturnsImmediatelyOnZero(t *testing.T) {
	c := Config{BaseDelay: 0, Rand: deterministicFloat64(0.5)}
	// Delay(0) is 0; Wait must still honor cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Wait(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if err := c.Wait(context.Background(), 0); err != nil {
		t.Fatalf("expected nil from clean zero-wait, got %v", err)
	}
}

func TestLoopStopsOnSuccess(t *testing.T) {
	c := Config{
		BaseDelay: 1 * time.Millisecond,
		MaxDelay:  10 * time.Millisecond,
		Rand:      deterministicFloat64(0.5),
	}
	var calls atomic.Int32
	err := Loop(context.Background(), c, func(attempt int) error {
		calls.Add(1)
		if calls.Load() == 3 {
			return nil
		}
		return errors.New("not yet")
	})
	if err != nil {
		t.Fatalf("Loop returned %v, want nil", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("op called %d times, want 3", got)
	}
}

func TestLoopStopsOnContextCancel(t *testing.T) {
	c := Config{
		BaseDelay: 100 * time.Millisecond,
		MaxDelay:  500 * time.Millisecond,
		Rand:      deterministicFloat64(0.5),
	}
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	err := Loop(ctx, c, func(attempt int) error {
		calls.Add(1)
		// Cancel after the second call.
		if calls.Load() == 2 {
			cancel()
		}
		return errors.New("nope")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if got := calls.Load(); got < 2 {
		t.Fatalf("op called %d times, want >= 2", got)
	}
}

func TestLoopPanicsOnNilOp(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on nil op")
		}
	}()
	Loop(context.Background(), Config{BaseDelay: time.Millisecond}, nil)
}

func TestScheduleReturnsExpectedLength(t *testing.T) {
	c := Config{
		BaseDelay: 50 * time.Millisecond,
		MaxDelay:  5 * time.Second,
		Rand:      deterministicFloat64(0.4),
	}
	got := c.Schedule(5)
	if len(got) != 5 {
		t.Fatalf("Schedule(5) returned %d entries, want 5", len(got))
	}
	for i, d := range got {
		if d <= 0 {
			t.Fatalf("Schedule[%d] = %v, want positive", i, d)
		}
		if d > 5*time.Second {
			t.Fatalf("Schedule[%d] = %v, want <= 5s", i, d)
		}
	}
	// 0 or negative input must return nil.
	if got := c.Schedule(0); got != nil {
		t.Fatalf("Schedule(0) = %v, want nil", got)
	}
	if got := c.Schedule(-3); got != nil {
		t.Fatalf("Schedule(-3) = %v, want nil", got)
	}
}

func TestScheduleMonotonicallyIncreasesWhenRandIsOne(t *testing.T) {
	c := Config{
		BaseDelay:  10 * time.Millisecond,
		MaxDelay:   math.MaxInt64,
		Multiplier: 3.0,
		Rand:       deterministicFloat64(1.0),
	}
	got := c.Schedule(4)
	// With rand=1.0 the recurrence is delay(n+1) = 3 * delay(n)
	// so 10ms, 30ms, 90ms, 270ms.
	want := []time.Duration{
		10 * time.Millisecond,
		30 * time.Millisecond,
		90 * time.Millisecond,
		270 * time.Millisecond,
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("Schedule[%d] = %v, want %v", i, got[i], w)
		}
	}
}

func TestDefaultRandReturnsValuesInRange(t *testing.T) {
	src := newCryptoFloat64Source()
	for i := 0; i < 1000; i++ {
		v := src()
		if v < 0 || v >= 1 {
			t.Fatalf("defaultRand returned %v outside [0, 1)", v)
		}
	}
}

func TestConfigWithNilRandUsesDefault(t *testing.T) {
	// We cannot directly observe the default source, but we can
	// confirm that Delay(1) returns a positive duration even
	// when no Rand is configured. The math collapses to
	// BaseDelay on the first attempt, so a missing Rand cannot
	// surface here.
	c := Config{BaseDelay: 25 * time.Millisecond, MaxDelay: 1 * time.Second}
	if got := c.Delay(1); got != 25*time.Millisecond {
		t.Fatalf("Delay(1) with nil rand: got %v, want 25ms", got)
	}
}

func TestErrAttemptsExhaustedIsExported(t *testing.T) {
	// Sentinels must satisfy errors.Is; the smoke test here is
	// that the error string and identity are stable.
	if ErrAttemptsExhausted.Error() == "" {
		t.Fatalf("ErrAttemptsExhausted has empty Error()")
	}
	// Round-trip via errors.Is to confirm the value is the same.
	if !errors.Is(ErrAttemptsExhausted, ErrAttemptsExhausted) {
		t.Fatalf("errors.Is(ErrAttemptsExhausted, ErrAttemptsExhausted) = false")
	}
}
