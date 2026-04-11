package unit_test

import (
	"sync"
	"testing"
	"time"

	"github.com/nicedavid98/api-gateway/internal/ratelimit"
)

func TestTokenBucket_Allow(t *testing.T) {
	tests := []struct {
		name     string
		rate     float64
		burst    int
		requests int
		wantAllow int
	}{
		{
			name:      "allows up to burst",
			rate:      10,
			burst:     5,
			requests:  5,
			wantAllow: 5,
		},
		{
			name:      "rejects beyond burst",
			rate:      10,
			burst:     3,
			requests:  5,
			wantAllow: 3,
		},
		{
			name:      "allows single request",
			rate:      100,
			burst:     1,
			requests:  1,
			wantAllow: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bucket := ratelimit.NewTokenBucket(tc.rate, tc.burst)
			allowed := 0
			for i := 0; i < tc.requests; i++ {
				if bucket.Allow(1) {
					allowed++
				}
			}
			if allowed != tc.wantAllow {
				t.Errorf("expected %d allowed, got %d", tc.wantAllow, allowed)
			}
		})
	}
}

func TestTokenBucket_Refill(t *testing.T) {
	bucket := ratelimit.NewTokenBucket(1000, 1) // 1000 tokens/sec, burst=1

	// Drain the bucket.
	if !bucket.Allow(1) {
		t.Fatal("expected first request to be allowed")
	}
	if bucket.Allow(1) {
		t.Fatal("expected second request to be rejected (bucket empty)")
	}

	// Wait for refill: 1ms should give ~1 token at 1000/sec.
	time.Sleep(2 * time.Millisecond)

	if !bucket.Allow(1) {
		t.Error("expected request to be allowed after refill")
	}
}

func TestTokenBucket_Concurrency(t *testing.T) {
	const (
		burst       = 100
		goroutines  = 50
		reqsPerGoroutine = 4
	)

	bucket := ratelimit.NewTokenBucket(1, burst)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < reqsPerGoroutine; j++ {
				if bucket.Allow(1) {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}
		}()
	}

	wg.Wait()

	if allowed > burst {
		t.Errorf("allowed %d requests but burst is %d", allowed, burst)
	}
}

func TestTokenBucket_Reset(t *testing.T) {
	bucket := ratelimit.NewTokenBucket(1, 3)

	// Drain all tokens.
	for i := 0; i < 3; i++ {
		bucket.Allow(1)
	}

	if bucket.Allow(1) {
		t.Error("bucket should be empty before reset")
	}

	bucket.Reset()

	if !bucket.Allow(1) {
		t.Error("bucket should have tokens after reset")
	}
}

func TestTokenBucket_Reserve(t *testing.T) {
	bucket := ratelimit.NewTokenBucket(10, 5)

	// Drain all tokens.
	for i := 0; i < 5; i++ {
		bucket.Allow(1)
	}

	// Reserve should succeed but return a wait duration.
	ok, wait := bucket.Reserve(1)
	if !ok {
		t.Error("reserve should return ok=true")
	}
	if wait <= 0 {
		t.Error("wait duration should be positive when bucket is empty")
	}
}
