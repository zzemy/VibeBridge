package main

import (
	"net/http"
	"sync"
	"time"
)

// rateLimiter implements a simple per-IP sliding-window rate limiter.
// It caps the number of requests in a rolling window, rejecting with
// 429 when the limit is exceeded. Entries are cleaned up lazily.
type rateLimiter struct {
	mu       sync.Mutex
	window   time.Duration
	maxReqs  int
	entries  map[string]*rateEntry
}

type rateEntry struct {
	count     int
	windowEnd time.Time
}

func newRateLimiter(maxReqs int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		window:  window,
		maxReqs: maxReqs,
		entries: make(map[string]*rateEntry),
	}
}

// allow checks if the given IP is within the rate limit. Returns true
// if allowed, false if rate-limited. The IP should be extracted from
// the request by the caller (e.g. via RemoteAddr or X-Forwarded-For).
func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	entry, exists := rl.entries[ip]
	if !exists || now.After(entry.windowEnd) {
		rl.entries[ip] = &rateEntry{count: 1, windowEnd: now.Add(rl.window)}
		// Lazy cleanup: occasionally sweep expired entries.
		if len(rl.entries) > 1000 {
			for k, v := range rl.entries {
				if now.After(v.windowEnd) {
					delete(rl.entries, k)
				}
			}
		}
		return true
	}
	entry.count++
	return entry.count <= rl.maxReqs
}

// rateLimitMiddleware wraps an http.Handler with per-IP rate limiting.
func rateLimitMiddleware(limiter *rateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !limiter.allow(ip) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		// Use the first IP in the chain.
		if idx := indexOfByte(fwd, ','); idx > 0 {
			return fwd[:idx]
		}
		return fwd
	}
	host := r.RemoteAddr
	if idx := indexOfByte(host, ':'); idx > 0 {
		return host[:idx]
	}
	return host
}

func indexOfByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
