package circuitbreaker

import (
	"fmt"
	"sync"
	"time"
)

// ErrCircuitOpen is returned when a request is rejected because the circuit is open.
var ErrCircuitOpen = fmt.Errorf("circuit breaker is open")

// Breaker implements the circuit breaker pattern for a single backend service.
// It is thread-safe using RWMutex.
type Breaker struct {
	name     string
	settings Settings

	mu     sync.RWMutex
	state  State
	counts Counts
}

// New creates a new Breaker with the given name and settings.
// Zero-value settings fields are replaced with defaults.
func New(name string, s Settings) *Breaker {
	d := defaultSettings()
	if s.MaxFailures == 0 {
		s.MaxFailures = d.MaxFailures
	}
	if s.Timeout == 0 {
		s.Timeout = d.Timeout
	}
	if s.MaxHalfOpenRequests == 0 {
		s.MaxHalfOpenRequests = d.MaxHalfOpenRequests
	}

	return &Breaker{
		name:     name,
		settings: s,
		state:    StateClosed,
		counts:   Counts{LastStateChange: time.Now()},
	}
}

// Allow reports whether a new request may proceed. It returns ErrCircuitOpen
// when the circuit is open. In HalfOpen state it enforces the max-probe limit.
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return nil

	case StateOpen:
		if time.Since(b.counts.OpenedAt) > b.settings.Timeout {
			b.toHalfOpen()
			return nil
		}
		return ErrCircuitOpen

	case StateHalfOpen:
		if b.counts.Requests >= b.settings.MaxHalfOpenRequests {
			return ErrCircuitOpen
		}
		return nil

	default:
		return ErrCircuitOpen
	}
}

// RecordSuccess records a successful request and potentially closes the circuit.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.counts.onSuccess()

	if b.state == StateHalfOpen && b.counts.ConsecutiveSuccesses >= b.settings.MaxHalfOpenRequests {
		b.toClose()
	}
}

// RecordFailure records a failed request and potentially opens the circuit.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.counts.onFailure()

	switch b.state {
	case StateClosed:
		if b.counts.ConsecutiveFailures >= b.settings.MaxFailures {
			b.toOpen()
		}
	case StateHalfOpen:
		b.toOpen()
	case StateOpen:
		// already open; nothing to do.
	}
}

// Reset forces the circuit back to Closed state and clears all counters.
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.toClose()
}

// State returns the current state of the circuit.
func (b *Breaker) State() State {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.state
}

// Counts returns a snapshot of the current request counts.
func (b *Breaker) Counts() Counts {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.counts
}

// Name returns the name of this circuit breaker (typically the service name).
func (b *Breaker) Name() string {
	return b.name
}

// toOpen transitions the circuit to Open. Caller must hold b.mu (write).
func (b *Breaker) toOpen() {
	b.state = StateOpen
	b.counts.LastStateChange = time.Now()
	b.counts.OpenedAt = time.Now()
	b.counts.reset()
}

// toHalfOpen transitions the circuit to HalfOpen. Caller must hold b.mu (write).
func (b *Breaker) toHalfOpen() {
	b.state = StateHalfOpen
	b.counts.LastStateChange = time.Now()
	b.counts.reset()
}

// toClose transitions the circuit to Closed. Caller must hold b.mu (write).
func (b *Breaker) toClose() {
	b.state = StateClosed
	b.counts.LastStateChange = time.Now()
	b.counts.OpenedAt = time.Time{}
	b.counts.reset()
}

// Manager manages multiple Breaker instances, one per service.
type Manager struct {
	mu       sync.RWMutex
	breakers map[string]*Breaker
	defaults Settings
}

// NewManager creates a new Manager with the given default settings.
func NewManager(defaults Settings) *Manager {
	return &Manager{
		breakers: make(map[string]*Breaker),
		defaults: defaults,
	}
}

// Get returns the Breaker for the given service name, creating it if needed.
func (m *Manager) Get(serviceName string) *Breaker {
	m.mu.RLock()
	if b, ok := m.breakers[serviceName]; ok {
		m.mu.RUnlock()
		return b
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after upgrade.
	if b, ok := m.breakers[serviceName]; ok {
		return b
	}

	b := New(serviceName, m.defaults)
	m.breakers[serviceName] = b
	return b
}

// GetWithSettings returns a Breaker with specific settings, replacing the existing one if settings differ.
func (m *Manager) GetWithSettings(serviceName string, s Settings) *Breaker {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Always create with provided settings.
	if _, exists := m.breakers[serviceName]; !exists {
		b := New(serviceName, s)
		m.breakers[serviceName] = b
	}
	return m.breakers[serviceName]
}

// All returns a snapshot of all breakers keyed by service name.
func (m *Manager) All() map[string]*Breaker {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*Breaker, len(m.breakers))
	for k, v := range m.breakers {
		result[k] = v
	}
	return result
}

// Reset resets the circuit breaker for the given service.
func (m *Manager) Reset(serviceName string) bool {
	m.mu.RLock()
	b, ok := m.breakers[serviceName]
	m.mu.RUnlock()

	if !ok {
		return false
	}
	b.Reset()
	return true
}
