// Package discovery provides service registry abstractions for the API gateway.
package discovery

import (
	"context"
	"time"
)

// Instance represents a single backend service instance.
type Instance struct {
	// ID is a unique identifier for this instance.
	ID string

	// ServiceName is the logical name of the service this instance belongs to.
	ServiceName string

	// Host is the hostname or IP address.
	Host string

	// Port is the TCP port.
	Port int

	// Weight is used by weighted load balancing (default 1).
	Weight int

	// Healthy indicates whether the instance passed its last health check.
	Healthy bool

	// Metadata holds arbitrary key/value pairs associated with the instance.
	Metadata map[string]string

	// RegisteredAt is when the instance was registered.
	RegisteredAt time.Time
}

// Address returns the host:port string for this instance.
func (i *Instance) Address() string {
	if i.Port == 0 {
		return i.Host
	}
	return i.Host + ":" + itoa(i.Port)
}

// ServiceRegistry defines the interface for service instance management.
type ServiceRegistry interface {
	// Register adds an instance to the registry.
	Register(ctx context.Context, instance Instance) error

	// Deregister removes an instance from the registry by service name and instance ID.
	Deregister(ctx context.Context, serviceName, instanceID string) error

	// GetInstances returns all healthy instances for the given service name.
	GetInstances(ctx context.Context, serviceName string) ([]Instance, error)

	// Watch returns a channel that receives instance updates for a service.
	// The caller is responsible for draining the channel; close ctx to stop watching.
	Watch(ctx context.Context, serviceName string) (<-chan []Instance, error)
}

// itoa converts an int to its decimal string representation without fmt.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
