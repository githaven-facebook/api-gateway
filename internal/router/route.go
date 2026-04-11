// Package router provides HTTP routing and route management for the API gateway.
package router

import (
	"time"

	"github.com/nicedavid98/api-gateway/internal/config"
)

// Route represents a fully resolved routing rule.
type Route struct {
	// ID is a unique identifier for this route.
	ID string

	// Path is the URL path pattern (supports chi-style path params and wildcards).
	Path string

	// Methods lists the HTTP methods this route handles. Empty means all methods.
	Methods []string

	// ServiceName identifies the backend service.
	ServiceName string

	// ServiceURL is the direct URL of the backend (used in static configs).
	ServiceURL string

	// StripPrefix removes this prefix from the path before forwarding.
	StripPrefix string

	// Timeout overrides the global proxy timeout for this route.
	Timeout time.Duration

	// AuthRequired indicates the request must carry a valid JWT.
	AuthRequired bool

	// RateLimit provides route-specific rate limit overrides.
	RateLimit *RouteRateLimit

	// CircuitBreaker provides route-specific circuit breaker overrides.
	CircuitBreaker *RouteCircuitBreaker

	// Transform defines request/response transformation rules.
	Transform *RouteTransform

	// LoadBalance specifies the load balancing strategy.
	LoadBalance string
}

// RouteRateLimit holds per-route rate limit settings.
type RouteRateLimit struct {
	RPS   float64
	Burst int
}

// RouteCircuitBreaker holds per-route circuit breaker settings.
type RouteCircuitBreaker struct {
	Threshold   uint32
	Timeout     time.Duration
	MaxHalfOpen uint32
}

// RouteTransform holds request/response transformation rules for a route.
type RouteTransform struct {
	AddRequestHeaders     map[string]string
	RemoveRequestHeaders  []string
	AddResponseHeaders    map[string]string
	RemoveResponseHeaders []string
	RewritePath           string
}

// FromConfig converts a RouteConfig into a Route.
func FromConfig(rc config.RouteConfig) Route {
	r := Route{
		ID:           rc.ID,
		Path:         rc.Path,
		Methods:      rc.Methods,
		ServiceName:  rc.ServiceName,
		ServiceURL:   rc.ServiceURL,
		StripPrefix:  rc.StripPrefix,
		Timeout:      rc.Timeout,
		AuthRequired: rc.AuthRequired,
		LoadBalance:  rc.LoadBalance,
	}

	if rc.RateLimit != nil {
		r.RateLimit = &RouteRateLimit{
			RPS:   rc.RateLimit.RPS,
			Burst: rc.RateLimit.Burst,
		}
	}

	if rc.CircuitBreaker != nil {
		r.CircuitBreaker = &RouteCircuitBreaker{
			Threshold:   rc.CircuitBreaker.Threshold,
			Timeout:     rc.CircuitBreaker.Timeout,
			MaxHalfOpen: rc.CircuitBreaker.MaxHalfOpen,
		}
	}

	if rc.Transform != nil {
		r.Transform = &RouteTransform{
			AddRequestHeaders:     rc.Transform.AddRequestHeaders,
			RemoveRequestHeaders:  rc.Transform.RemoveRequestHeaders,
			AddResponseHeaders:    rc.Transform.AddResponseHeaders,
			RemoveResponseHeaders: rc.Transform.RemoveResponseHeaders,
			RewritePath:           rc.Transform.RewritePath,
		}
	}

	return r
}
