// Package proxy provides reverse proxy and load balancing functionality.
package proxy

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/nicedavid98/api-gateway/internal/discovery"
)

// Strategy defines the load balancing strategy.
type Strategy string

const (
	// StrategyRoundRobin distributes requests equally across instances.
	StrategyRoundRobin Strategy = "round-robin"

	// StrategyWeighted distributes requests proportionally by instance weight.
	StrategyWeighted Strategy = "weighted"

	// StrategyLeastConnections routes to the instance with fewest active connections.
	StrategyLeastConnections Strategy = "least-connections"
)

// LoadBalancer selects a backend instance for each incoming request.
type LoadBalancer interface {
	// Next selects the next instance from the provided list.
	Next(instances []discovery.Instance) (*discovery.Instance, error)
}

// RoundRobinBalancer distributes requests evenly using an atomic counter.
type RoundRobinBalancer struct {
	counter uint64
}

// NewRoundRobinBalancer creates a new RoundRobinBalancer.
func NewRoundRobinBalancer() *RoundRobinBalancer {
	return &RoundRobinBalancer{}
}

// Next selects the next instance in round-robin order.
func (r *RoundRobinBalancer) Next(instances []discovery.Instance) (*discovery.Instance, error) {
	if len(instances) == 0 {
		return nil, fmt.Errorf("no instances available")
	}
	idx := atomic.AddUint64(&r.counter, 1) - 1
	inst := instances[idx%uint64(len(instances))]
	return &inst, nil
}

// WeightedBalancer selects instances proportionally based on their weights.
type WeightedBalancer struct {
	mu      sync.Mutex
	current int
	cw      int // current weight
	gcd     int // greatest common divisor of all weights
	maxW    int // maximum weight
}

// NewWeightedBalancer creates a new WeightedBalancer.
func NewWeightedBalancer() *WeightedBalancer {
	return &WeightedBalancer{current: -1}
}

// Next implements the Nginx smooth weighted round-robin algorithm.
func (w *WeightedBalancer) Next(instances []discovery.Instance) (*discovery.Instance, error) {
	if len(instances) == 0 {
		return nil, fmt.Errorf("no instances available")
	}
	if len(instances) == 1 {
		return &instances[0], nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	maxW := 0
	gcdW := 0
	for _, inst := range instances {
		weight := inst.Weight
		if weight <= 0 {
			weight = 1
		}
		if weight > maxW {
			maxW = weight
		}
		gcdW = gcd(gcdW, weight)
	}

	n := len(instances)
	for {
		w.current = (w.current + 1) % n
		if w.current == 0 {
			w.cw -= gcdW
			if w.cw <= 0 {
				w.cw = maxW
			}
		}
		instWeight := instances[w.current].Weight
		if instWeight <= 0 {
			instWeight = 1
		}
		if instWeight >= w.cw {
			inst := instances[w.current]
			return &inst, nil
		}
	}
}

// LeastConnectionsBalancer routes to the instance with the fewest active connections.
type LeastConnectionsBalancer struct {
	mu          sync.Mutex
	connections map[string]int64
}

// NewLeastConnectionsBalancer creates a new LeastConnectionsBalancer.
func NewLeastConnectionsBalancer() *LeastConnectionsBalancer {
	return &LeastConnectionsBalancer{
		connections: make(map[string]int64),
	}
}

// Next selects the instance with the least active connections.
func (l *LeastConnectionsBalancer) Next(instances []discovery.Instance) (*discovery.Instance, error) {
	if len(instances) == 0 {
		return nil, fmt.Errorf("no instances available")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	var selected *discovery.Instance
	minConns := int64(-1)

	for i := range instances {
		inst := &instances[i]
		conns := l.connections[inst.ID]
		if minConns == -1 || conns < minConns {
			minConns = conns
			selected = inst
		}
	}

	if selected == nil {
		inst := instances[0]
		return &inst, nil
	}

	l.connections[selected.ID]++
	result := *selected
	return &result, nil
}

// Done decrements the connection count for an instance after request completion.
func (l *LeastConnectionsBalancer) Done(instanceID string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.connections[instanceID] > 0 {
		l.connections[instanceID]--
	}
}

// NewLoadBalancer creates a LoadBalancer for the given strategy.
func NewLoadBalancer(strategy Strategy) LoadBalancer {
	switch strategy {
	case StrategyWeighted:
		return NewWeightedBalancer()
	case StrategyLeastConnections:
		return NewLeastConnectionsBalancer()
	default:
		return NewRoundRobinBalancer()
	}
}

// gcd computes the greatest common divisor of two non-negative integers.
func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
