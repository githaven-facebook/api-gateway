// Package ratelimit provides token bucket rate limiting with Redis backing.
package ratelimit

import (
	"sync"
	"time"
)

// TokenBucket implements a thread-safe token bucket rate limiter.
// Tokens are added at a fixed rate up to a configurable burst capacity.
type TokenBucket struct {
	mu       sync.Mutex
	tokens   float64
	rate     float64 // tokens per second
	burst    float64
	lastTime time.Time
}

// NewTokenBucket creates a new TokenBucket with the given rate (tokens/sec) and burst size.
func NewTokenBucket(rate float64, burst int) *TokenBucket {
	if rate <= 0 {
		rate = 1
	}
	if burst <= 0 {
		burst = 1
	}
	return &TokenBucket{
		tokens:   float64(burst),
		rate:     rate,
		burst:    float64(burst),
		lastTime: time.Now(),
	}
}

// Allow reports whether n tokens are available and, if so, consumes them.
// Returns true if the request is allowed.
func (b *TokenBucket) Allow(n float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	b.refill(now)

	if b.tokens < n {
		return false
	}
	b.tokens -= n
	return true
}

// AllowN is an alias for Allow(float64(n)).
func (b *TokenBucket) AllowN(n int) bool {
	return b.Allow(float64(n))
}

// Tokens returns the current number of available tokens (snapshot, without refill).
func (b *TokenBucket) Tokens() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill(time.Now())
	return b.tokens
}

// Reserve attempts to reserve n tokens. Returns true and the wait duration if
// the request can proceed; returns false if burst is exceeded.
func (b *TokenBucket) Reserve(n float64) (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	b.refill(now)

	if n > b.burst {
		return false, 0
	}

	if b.tokens >= n {
		b.tokens -= n
		return true, 0
	}

	// Calculate how long to wait for n tokens to become available.
	deficit := n - b.tokens
	wait := time.Duration(deficit/b.rate*float64(time.Second)) + time.Millisecond
	return true, wait
}

// refill adds tokens based on elapsed time since the last call.
// Caller must hold b.mu.
func (b *TokenBucket) refill(now time.Time) {
	elapsed := now.Sub(b.lastTime).Seconds()
	if elapsed <= 0 {
		return
	}
	b.tokens += elapsed * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.lastTime = now
}

// Reset restores the bucket to its full burst capacity.
func (b *TokenBucket) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tokens = b.burst
	b.lastTime = time.Now()
}
