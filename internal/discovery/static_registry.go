package discovery

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// StaticRegistry is a config-based service registry that does not require
// an external service discovery system. Instances are registered at startup
// and can be updated via Register/Deregister at runtime.
type StaticRegistry struct {
	mu        sync.RWMutex
	instances map[string]map[string]Instance // serviceName -> instanceID -> Instance
	watchers  map[string][]chan []Instance
}

// NewStaticRegistry creates a new StaticRegistry.
func NewStaticRegistry() *StaticRegistry {
	return &StaticRegistry{
		instances: make(map[string]map[string]Instance),
		watchers:  make(map[string][]chan []Instance),
	}
}

// Register adds or updates an instance in the registry.
func (r *StaticRegistry) Register(_ context.Context, inst Instance) error {
	if inst.ServiceName == "" {
		return fmt.Errorf("service name is required")
	}
	if inst.ID == "" {
		return fmt.Errorf("instance ID is required")
	}
	if inst.Host == "" {
		return fmt.Errorf("instance host is required")
	}

	if inst.Weight <= 0 {
		inst.Weight = 1
	}
	if inst.RegisteredAt.IsZero() {
		inst.RegisteredAt = time.Now()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.instances[inst.ServiceName] == nil {
		r.instances[inst.ServiceName] = make(map[string]Instance)
	}
	r.instances[inst.ServiceName][inst.ID] = inst
	r.notifyWatchers(inst.ServiceName)
	return nil
}

// Deregister removes an instance from the registry.
func (r *StaticRegistry) Deregister(_ context.Context, serviceName, instanceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	svc, ok := r.instances[serviceName]
	if !ok {
		return fmt.Errorf("service %q not found", serviceName)
	}
	if _, ok := svc[instanceID]; !ok {
		return fmt.Errorf("instance %q not found in service %q", instanceID, serviceName)
	}

	delete(svc, instanceID)
	r.notifyWatchers(serviceName)
	return nil
}

// GetInstances returns all healthy instances for the given service.
func (r *StaticRegistry) GetInstances(_ context.Context, serviceName string) ([]Instance, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	svc, ok := r.instances[serviceName]
	if !ok {
		return nil, fmt.Errorf("service %q not registered", serviceName)
	}

	result := make([]Instance, 0, len(svc))
	for _, inst := range svc {
		if inst.Healthy {
			result = append(result, inst)
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no healthy instances for service %q", serviceName)
	}
	return result, nil
}

// Watch returns a channel that receives instance list updates for a service.
func (r *StaticRegistry) Watch(ctx context.Context, serviceName string) (<-chan []Instance, error) {
	ch := make(chan []Instance, 8)

	r.mu.Lock()
	r.watchers[serviceName] = append(r.watchers[serviceName], ch)
	// Send the current snapshot immediately.
	svc := r.instances[serviceName]
	snapshot := make([]Instance, 0, len(svc))
	for _, inst := range svc {
		snapshot = append(snapshot, inst)
	}
	r.mu.Unlock()

	// Push initial snapshot.
	if len(snapshot) > 0 {
		ch <- snapshot
	}

	// Clean up when context is cancelled.
	go func() {
		<-ctx.Done()
		r.mu.Lock()
		defer r.mu.Unlock()

		watchers := r.watchers[serviceName]
		for i, w := range watchers {
			if w == ch {
				r.watchers[serviceName] = append(watchers[:i], watchers[i+1:]...)
				break
			}
		}
		close(ch)
	}()

	return ch, nil
}

// SetHealthy updates the health state of an instance.
func (r *StaticRegistry) SetHealthy(serviceName, instanceID string, healthy bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	svc, ok := r.instances[serviceName]
	if !ok {
		return
	}
	inst, ok := svc[instanceID]
	if !ok {
		return
	}
	inst.Healthy = healthy
	svc[instanceID] = inst
	r.notifyWatchers(serviceName)
}

// ListServices returns all registered service names.
func (r *StaticRegistry) ListServices() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.instances))
	for name := range r.instances {
		names = append(names, name)
	}
	return names
}

// notifyWatchers sends the current instance list to all watchers for a service.
// Caller must hold r.mu (write lock).
func (r *StaticRegistry) notifyWatchers(serviceName string) {
	svc := r.instances[serviceName]
	snapshot := make([]Instance, 0, len(svc))
	for _, inst := range svc {
		snapshot = append(snapshot, inst)
	}

	for _, ch := range r.watchers[serviceName] {
		select {
		case ch <- snapshot:
		default:
			// Drop if watcher is not keeping up.
		}
	}
}
