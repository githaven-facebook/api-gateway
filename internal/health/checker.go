// Package health provides downstream service health checking and aggregation.
package health

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Status represents the health status of a service.
type Status string

const (
	// StatusHealthy indicates the service is fully operational.
	StatusHealthy Status = "healthy"

	// StatusDegraded indicates the service is operational but has some issues.
	StatusDegraded Status = "degraded"

	// StatusUnhealthy indicates the service is not operational.
	StatusUnhealthy Status = "unhealthy"

	// StatusUnknown indicates the health of the service has not been determined.
	StatusUnknown Status = "unknown"
)

// ServiceHealth holds the health information for a single service.
type ServiceHealth struct {
	ServiceName string        `json:"service_name"`
	Status      Status        `json:"status"`
	Latency     time.Duration `json:"latency_ms"`
	Error       string        `json:"error,omitempty"`
	CheckedAt   time.Time     `json:"checked_at"`
}

// AggregateHealth represents the overall gateway health.
type AggregateHealth struct {
	Status    Status                    `json:"status"`
	Services  map[string]*ServiceHealth `json:"services"`
	CheckedAt time.Time                 `json:"checked_at"`
}

// ServiceEndpoint defines a downstream service and its health endpoint.
type ServiceEndpoint struct {
	Name      string
	HealthURL string
	Timeout   time.Duration
}

// cachedResult holds a cached health result with expiry.
type cachedResult struct {
	health    *ServiceHealth
	expiresAt time.Time
}

// Checker performs parallel health checks against downstream services.
type Checker struct {
	client    *http.Client
	endpoints []ServiceEndpoint
	logger    *zap.Logger

	mu       sync.RWMutex
	cache    map[string]*cachedResult
	cacheTTL time.Duration
}

// NewChecker creates a new Checker.
func NewChecker(endpoints []ServiceEndpoint, cacheTTL time.Duration, logger *zap.Logger) *Checker {
	if cacheTTL <= 0 {
		cacheTTL = 15 * time.Second
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Checker{
		client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		endpoints: endpoints,
		logger:    logger,
		cache:     make(map[string]*cachedResult),
		cacheTTL:  cacheTTL,
	}
}

// CheckAll performs parallel health checks on all registered endpoints.
// Cached results are returned for recently checked services.
func (c *Checker) CheckAll(ctx context.Context) *AggregateHealth {
	results := make(map[string]*ServiceHealth, len(c.endpoints))
	var mu sync.Mutex

	var wg sync.WaitGroup
	for _, ep := range c.endpoints {
		ep := ep
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := c.checkService(ctx, ep)
			mu.Lock()
			results[ep.Name] = h
			mu.Unlock()
		}()
	}
	wg.Wait()

	aggregate := &AggregateHealth{
		Status:    StatusHealthy,
		Services:  results,
		CheckedAt: time.Now(),
	}

	unhealthyCount := 0
	degradedCount := 0
	for _, h := range results {
		switch h.Status {
		case StatusUnhealthy:
			unhealthyCount++
		case StatusDegraded:
			degradedCount++
		case StatusHealthy, StatusUnknown:
			// no action needed.
		}
	}

	total := len(results)
	if total > 0 {
		switch {
		case unhealthyCount == total:
			aggregate.Status = StatusUnhealthy
		case unhealthyCount > 0 || degradedCount > 0:
			aggregate.Status = StatusDegraded
		}
	}

	return aggregate
}

// checkService checks a single service, using cache when fresh.
func (c *Checker) checkService(ctx context.Context, ep ServiceEndpoint) *ServiceHealth {
	c.mu.RLock()
	cached, ok := c.cache[ep.Name]
	c.mu.RUnlock()

	if ok && time.Now().Before(cached.expiresAt) {
		return cached.health
	}

	timeout := ep.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	health := c.probe(checkCtx, ep)
	health.Latency = time.Since(start)
	health.CheckedAt = time.Now()

	c.mu.Lock()
	c.cache[ep.Name] = &cachedResult{
		health:    health,
		expiresAt: time.Now().Add(c.cacheTTL),
	}
	c.mu.Unlock()

	return health
}

// probe makes an HTTP GET request to the service health endpoint.
func (c *Checker) probe(ctx context.Context, ep ServiceEndpoint) *ServiceHealth {
	h := &ServiceHealth{ServiceName: ep.Name}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.HealthURL, nil)
	if err != nil {
		h.Status = StatusUnhealthy
		h.Error = fmt.Sprintf("building request: %v", err)
		return h
	}

	resp, err := c.client.Do(req)
	if err != nil {
		h.Status = StatusUnhealthy
		h.Error = fmt.Sprintf("HTTP request failed: %v", err)
		return h
	}
	defer resp.Body.Close() //nolint:errcheck

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		h.Status = StatusHealthy
	case resp.StatusCode >= 500:
		h.Status = StatusUnhealthy
		h.Error = fmt.Sprintf("upstream returned %d", resp.StatusCode)
	default:
		h.Status = StatusDegraded
		h.Error = fmt.Sprintf("upstream returned %d", resp.StatusCode)
	}

	return h
}

// GatewayHealth returns a simple health check for the gateway itself.
func GatewayHealth() *ServiceHealth {
	return &ServiceHealth{
		ServiceName: "api-gateway",
		Status:      StatusHealthy,
		CheckedAt:   time.Now(),
	}
}
