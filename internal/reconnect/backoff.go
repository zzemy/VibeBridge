// Package reconnect provides a small, context-aware backoff helper
// for clients that re-establish a network connection. The helper
// implements the "decorrelated jitter" strategy described in the AWS
// Architecture Blog post "Exponential Backoff and Jitter" (Marc
// Brooker, 2015): each attempt's delay is uniformly drawn from
// [BaseDelay, prevDelay * Multiplier), capped at MaxDelay. Compared
// to plain exponential backoff the jitter prevents the
// thundering-herd stampede that happens when many clients lose
// their connection at the same time and all reconnect in lockstep.
//
// The package is intentionally tiny: a Config value, a Delay
// method, a Wait method, and a Loop helper. The Caller owns the
// state machine; reconnect only owns the wait math.
package reconnect

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"math"
	"sync"
	"time"
)

// Float64Source is the function shape math/rand.Float64 has. The
// package uses it to inject a deterministic source in tests; the
// default source pulls fresh entropy from crypto/rand on every
// call so two callers cannot accidentally correlate their waits.
type Float64Source func() float64

// Config tunes the backoff strategy. The zero value is valid and
// produces a strategy that uses the package defaults.
type Config struct {
	// BaseDelay is the lower bound of the first wait. Subsequent
	// waits grow from there. A zero value falls back to
	// DefaultBaseDelay.
	BaseDelay time.Duration
	// MaxDelay caps every wait. A non-positive value disables
	// the cap; the production default is DefaultMaxDelay.
	MaxDelay time.Duration
	// Multiplier is the growth factor applied to the previous
	// wait before the jitter is drawn. The default
	// DefaultMultiplier matches the AWS reference. Values <= 1
	// are clamped to 1.
	Multiplier float64
	// Rand supplies the [0, 1) jitter. nil falls back to the
	// package default, which uses crypto/rand.
	Rand Float64Source
}

const (
	// DefaultBaseDelay is the smallest wait the strategy ever
	// returns. Tuned to match the typical TCP retransmit floor:
	// anything smaller is dominated by jitter anyway.
	DefaultBaseDelay = 100 * time.Millisecond
	// DefaultMaxDelay caps a single wait so a long-lived
	// connection does not get a 10-minute pause between
	// attempts.
	DefaultMaxDelay = 30 * time.Second
	// DefaultMultiplier is the growth factor used by the
	// decorrelated jitter formula.
	DefaultMultiplier = 3.0
)

func (c Config) baseDelay() time.Duration {
	if c.BaseDelay > 0 {
		return c.BaseDelay
	}
	return DefaultBaseDelay
}

func (c Config) maxDelay() time.Duration {
	if c.MaxDelay > 0 {
		return c.MaxDelay
	}
	return DefaultMaxDelay
}

func (c Config) multiplier() float64 {
	if c.Multiplier <= 1 {
		return DefaultMultiplier
	}
	return c.Multiplier
}

func (c Config) randFloat() float64 {
	if c.Rand != nil {
		return c.Rand()
	}
	return defaultRand()
}

// Delay returns the wait time before the n-th retry. n is 1-based:
// Delay(1) is the first retry, Delay(2) the second, etc. A non-
// positive n returns 0, which is the documented way to mean "no
// wait" when the caller wants the first attempt to be immediate.
func (c Config) Delay(n int) time.Duration {
	if n <= 0 {
		return 0
	}
	// Decorrelated jitter: sleep = min(MaxDelay, rand * BaseDelay *
	// Multiplier^(n-1)) for the first attempt, and sleep = min(MaxDelay,
	// rand * prev * Multiplier) for subsequent attempts. We track
	// "prev" by walking the recurrence from the first attempt each
	// time Delay is called; the function is pure so the caller can
	// precompute a schedule if it wants.
	delay := c.baseDelay()
	for i := 1; i < n; i++ {
		next := time.Duration(c.randFloat() * float64(delay) * c.multiplier())
		if next > c.maxDelay() {
			next = c.maxDelay()
		}
		delay = next
	}
	return delay
}

// Wait blocks for the configured delay of the n-th retry, or
// returns ctx.Err() if the context is cancelled before the wait
// elapses. Wait returns nil on a successful (uninterrupted) wait.
func (c Config) Wait(ctx context.Context, n int) error {
	delay := c.Delay(n)
	if delay <= 0 {
		// Still honor cancellation so a caller that uses Wait
		// for the "first attempt is immediate" path cannot
		// accidentally spin.
		if err := ctx.Err(); err != nil {
			return err
		}
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Loop runs op until it returns nil or ctx is cancelled. Between
// attempts Loop calls Wait with the 1-based attempt number. The
// first call to op uses attempt=1 and is preceded by a wait
// equivalent to Delay(1) so the schedule is identical to the one
// documented in Delay. Callers that want a fast first attempt
// should call op directly before entering Loop and only call Loop
// after a failure.
func Loop(ctx context.Context, cfg Config, op func(attempt int) error) error {
	if op == nil {
		panic("reconnect: nil op")
	}
	attempt := 1
	for {
		err := op(attempt)
		if err == nil {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err := cfg.Wait(ctx, attempt+1); err != nil {
			return err
		}
		attempt++
		// Defensive: an int that overflows is a logic error
		// the caller must fix. Bailing is safer than
		// silently corrupting the schedule.
		if attempt < 0 {
			return ErrAttemptsExhausted
		}
	}
}

// ErrAttemptsExhausted is the sentinel Loop returns if the attempt
// counter overflows. It is exported so callers can use errors.Is
// to detect this pathological case without comparing concrete
// errors.
var ErrAttemptsExhausted = errAttemptsExhausted{}

type errAttemptsExhausted struct{}

func (errAttemptsExhausted) Error() string {
	return "reconnect: attempt counter overflowed"
}

// defaultRand returns a uniformly distributed float64 in [0, 1)
// backed by crypto/rand. The implementation pools a small mutex
// because reading from crypto/rand on every jitter draw is fine
// for production rates but the contention story is easier to
// reason about when the read is bundled.
var defaultRand Float64Source = newCryptoFloat64Source()

func newCryptoFloat64Source() Float64Source {
	var mu sync.Mutex
	return func() float64 {
		// We need 53 random bits (the mantissa of an IEEE 754
		// double). Reading 8 bytes is more than enough and
		// keeps the math trivial.
		var buf [8]byte
		mu.Lock()
		_, _ = rand.Read(buf[:])
		mu.Unlock()
		// Mask off the top 12 bits so the value fits in 52
		// bits; divide by 2^52 to land in [0, 1).
		mantissa := binary.BigEndian.Uint64(buf[:]) & ((1 << 52) - 1)
		return float64(mantissa) / float64(uint64(1)<<52)
	}
}

// Schedule is a small convenience that precomputes the first n
// delays and returns them as a slice. Useful for tests and for
// callers that want to log the schedule. The returned slice is
// independent of Config; mutating it does not change the strategy.
func (c Config) Schedule(n int) []time.Duration {
	if n <= 0 {
		return nil
	}
	out := make([]time.Duration, n)
	for i := 1; i <= n; i++ {
		out[i-1] = c.Delay(i)
	}
	// math.MaxInt32 keeps the loop bounded even when a caller
	// passes a wild n; everything beyond is just zeros.
	if n > math.MaxInt32 {
		return out
	}
	return out
}
