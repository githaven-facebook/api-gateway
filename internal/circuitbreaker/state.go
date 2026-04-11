// Package circuitbreaker implements the circuit breaker pattern for backend service protection.
package circuitbreaker

import (
	"fmt"
	"time"
)

// State represents the current state of a circuit breaker.
type State int32

const (
	// StateClosed means the circuit is healthy and requests pass through.
	StateClosed State = iota

	// StateOpen means the circuit has tripped and requests are rejected.
	StateOpen

	// StateHalfOpen means the circuit is testing recovery with limited requests.
	StateHalfOpen
)

// String returns a human-readable representation of the state.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// Counts tracks request outcomes for a circuit breaker.
type Counts struct {
	// Requests is the total number of requests attempted.
	Requests uint32

	// TotalSuccesses is the total number of successful requests.
	TotalSuccesses uint32

	// TotalFailures is the total number of failed requests.
	TotalFailures uint32

	// ConsecutiveSuccesses is the number of consecutive successes (reset on failure).
	ConsecutiveSuccesses uint32

	// ConsecutiveFailures is the number of consecutive failures (reset on success).
	ConsecutiveFailures uint32

	// LastStateChange records when the state last changed.
	LastStateChange time.Time

	// OpenedAt records when the circuit opened (zero if closed).
	OpenedAt time.Time
}

// onSuccess updates counts for a successful request.
func (c *Counts) onSuccess() {
	c.Requests++
	c.TotalSuccesses++
	c.ConsecutiveSuccesses++
	c.ConsecutiveFailures = 0
}

// onFailure updates counts for a failed request.
func (c *Counts) onFailure() {
	c.Requests++
	c.TotalFailures++
	c.ConsecutiveFailures++
	c.ConsecutiveSuccesses = 0
}

// reset clears all transient counts.
func (c *Counts) reset() {
	c.Requests = 0
	c.ConsecutiveSuccesses = 0
	c.ConsecutiveFailures = 0
}

// Settings defines the thresholds and timing for a circuit breaker.
type Settings struct {
	// MaxFailures is the number of consecutive failures before opening.
	MaxFailures uint32

	// Timeout is how long to wait in Open before transitioning to HalfOpen.
	Timeout time.Duration

	// MaxHalfOpenRequests is the number of trial requests allowed in HalfOpen.
	MaxHalfOpenRequests uint32
}

// defaultSettings returns sensible defaults.
func defaultSettings() Settings {
	return Settings{
		MaxFailures:         5,
		Timeout:             30 * time.Second,
		MaxHalfOpenRequests: 2,
	}
}
