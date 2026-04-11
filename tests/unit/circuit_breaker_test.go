package unit_test

import (
	"testing"
	"time"

	"github.com/nicedavid98/api-gateway/internal/circuitbreaker"
)

func TestCircuitBreaker_InitiallyClosed(t *testing.T) {
	b := circuitbreaker.New("test-service", circuitbreaker.Settings{
		MaxFailures:         3,
		Timeout:             100 * time.Millisecond,
		MaxHalfOpenRequests: 1,
	})

	if b.State() != circuitbreaker.StateClosed {
		t.Errorf("expected initial state Closed, got %s", b.State())
	}

	if err := b.Allow(); err != nil {
		t.Errorf("expected Allow() to succeed in closed state: %v", err)
	}
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	b := circuitbreaker.New("test-service", circuitbreaker.Settings{
		MaxFailures:         3,
		Timeout:             time.Second,
		MaxHalfOpenRequests: 1,
	})

	for i := 0; i < 3; i++ {
		b.RecordFailure()
	}

	if b.State() != circuitbreaker.StateOpen {
		t.Errorf("expected state Open after %d failures, got %s", 3, b.State())
	}

	if err := b.Allow(); err == nil {
		t.Error("expected Allow() to fail in open state")
	}
}

func TestCircuitBreaker_TransitionsToHalfOpen(t *testing.T) {
	b := circuitbreaker.New("test-service", circuitbreaker.Settings{
		MaxFailures:         1,
		Timeout:             50 * time.Millisecond,
		MaxHalfOpenRequests: 1,
	})

	b.RecordFailure()
	if b.State() != circuitbreaker.StateOpen {
		t.Fatal("expected Open state")
	}

	// Wait for timeout to elapse.
	time.Sleep(100 * time.Millisecond)

	// Allow() should transition to HalfOpen.
	if err := b.Allow(); err != nil {
		t.Errorf("expected Allow() to succeed after timeout: %v", err)
	}

	if b.State() != circuitbreaker.StateHalfOpen {
		t.Errorf("expected HalfOpen state, got %s", b.State())
	}
}

func TestCircuitBreaker_ClosesOnSuccessInHalfOpen(t *testing.T) {
	b := circuitbreaker.New("test-service", circuitbreaker.Settings{
		MaxFailures:         1,
		Timeout:             50 * time.Millisecond,
		MaxHalfOpenRequests: 1,
	})

	b.RecordFailure()
	time.Sleep(100 * time.Millisecond)

	// Probe request.
	if err := b.Allow(); err != nil {
		t.Fatalf("expected Allow() in half-open: %v", err)
	}

	b.RecordSuccess()

	if b.State() != circuitbreaker.StateClosed {
		t.Errorf("expected Closed state after success in HalfOpen, got %s", b.State())
	}
}

func TestCircuitBreaker_ReopensOnFailureInHalfOpen(t *testing.T) {
	b := circuitbreaker.New("test-service", circuitbreaker.Settings{
		MaxFailures:         1,
		Timeout:             50 * time.Millisecond,
		MaxHalfOpenRequests: 2,
	})

	b.RecordFailure()
	time.Sleep(100 * time.Millisecond)
	b.Allow() //nolint:errcheck

	b.RecordFailure()

	if b.State() != circuitbreaker.StateOpen {
		t.Errorf("expected Open state after failure in HalfOpen, got %s", b.State())
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	b := circuitbreaker.New("test-service", circuitbreaker.Settings{
		MaxFailures:         1,
		Timeout:             time.Hour,
		MaxHalfOpenRequests: 1,
	})

	b.RecordFailure()
	if b.State() != circuitbreaker.StateOpen {
		t.Fatal("expected Open state")
	}

	b.Reset()

	if b.State() != circuitbreaker.StateClosed {
		t.Errorf("expected Closed state after reset, got %s", b.State())
	}

	if err := b.Allow(); err != nil {
		t.Errorf("expected Allow() after reset: %v", err)
	}
}

func TestCircuitBreakerManager(t *testing.T) {
	manager := circuitbreaker.NewManager(circuitbreaker.Settings{
		MaxFailures:         5,
		Timeout:             30 * time.Second,
		MaxHalfOpenRequests: 2,
	})

	b1 := manager.Get("service-a")
	b2 := manager.Get("service-b")
	b1Again := manager.Get("service-a")

	if b1 != b1Again {
		t.Error("Get should return the same Breaker instance for the same service")
	}
	if b1 == b2 {
		t.Error("Get should return different Breaker instances for different services")
	}

	all := manager.All()
	if len(all) != 2 {
		t.Errorf("expected 2 breakers, got %d", len(all))
	}

	if !manager.Reset("service-a") {
		t.Error("expected Reset to return true for existing service")
	}
	if manager.Reset("nonexistent") {
		t.Error("expected Reset to return false for nonexistent service")
	}
}
