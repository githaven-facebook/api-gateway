package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/nicedavid98/api-gateway/internal/discovery"
	"github.com/nicedavid98/api-gateway/internal/router"
)

const (
	defaultTimeout      = 30 * time.Second
	defaultMaxRetries   = 2
	retryBackoffBase    = 50 * time.Millisecond
	maxIdleConns        = 100
	maxIdleConnsPerHost = 20
	idleConnTimeout     = 90 * time.Second
)

// ReverseProxy forwards incoming requests to a selected backend instance.
type ReverseProxy struct {
	registry     discovery.ServiceRegistry
	balancers    map[string]LoadBalancer
	transport    http.RoundTripper
	logger       *zap.Logger
	defaultLB    Strategy
	maxRetries   int
}

// Options configures the ReverseProxy.
type Options struct {
	Registry   discovery.ServiceRegistry
	Logger     *zap.Logger
	DefaultLB  Strategy
	MaxRetries int
	Transport  http.RoundTripper
}

// New creates a new ReverseProxy with the given options.
func New(opts Options) *ReverseProxy {
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}
	if opts.DefaultLB == "" {
		opts.DefaultLB = StrategyRoundRobin
	}
	if opts.MaxRetries <= 0 {
		opts.MaxRetries = defaultMaxRetries
	}

	transport := opts.Transport
	if transport == nil {
		transport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          maxIdleConns,
			MaxIdleConnsPerHost:   maxIdleConnsPerHost,
			IdleConnTimeout:       idleConnTimeout,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
		}
	}

	return &ReverseProxy{
		registry:   opts.Registry,
		balancers:  make(map[string]LoadBalancer),
		transport:  transport,
		logger:     opts.Logger,
		defaultLB:  opts.DefaultLB,
		maxRetries: opts.MaxRetries,
	}
}

// ServeHTTP implements http.Handler; it forwards the request to a backend instance.
func (p *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route := router.RouteFromContext(r.Context())
	if route == nil {
		http.Error(w, "no route in context", http.StatusInternalServerError)
		return
	}

	timeout := route.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	var lastErr error
	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := retryBackoffBase * time.Duration(1<<uint(attempt-1))
			select {
			case <-ctx.Done():
				break
			case <-time.After(backoff):
			}
		}

		instance, err := p.selectInstance(ctx, route)
		if err != nil {
			lastErr = fmt.Errorf("selecting backend instance: %w", err)
			break
		}

		resp, err := p.forward(ctx, w, r, route, instance)
		if err != nil {
			lastErr = err
			p.logger.Warn("proxy request failed",
				zap.String("service", route.ServiceName),
				zap.String("instance", instance.Address()),
				zap.Int("attempt", attempt+1),
				zap.Error(err),
			)
			continue
		}

		if resp == http.StatusBadGateway || resp == http.StatusServiceUnavailable {
			lastErr = fmt.Errorf("backend returned %d", resp)
			continue
		}

		return
	}

	p.logger.Error("all proxy attempts failed",
		zap.String("service", route.ServiceName),
		zap.Error(lastErr),
	)

	if ctx.Err() == context.DeadlineExceeded {
		http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
		return
	}
	http.Error(w, "bad gateway", http.StatusBadGateway)
}

// forward performs a single proxy attempt to the given instance.
// It returns the response status code and any error.
func (p *ReverseProxy) forward(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	route *router.Route,
	instance *discovery.Instance,
) (int, error) {
	targetURL := p.buildTargetURL(r, route, instance)

	outReq, err := http.NewRequestWithContext(ctx, r.Method, targetURL, r.Body)
	if err != nil {
		return 0, fmt.Errorf("building upstream request: %w", err)
	}

	// Copy original headers and add forwarding headers.
	copyHeaders(outReq.Header, r.Header)
	setForwardingHeaders(outReq, r)

	resp, err := p.transport.RoundTrip(outReq)
	if err != nil {
		return 0, fmt.Errorf("upstream round trip: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	// Copy response headers.
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	w.WriteHeader(resp.StatusCode)

	if _, err := io.Copy(w, resp.Body); err != nil {
		p.logger.Warn("error copying response body", zap.Error(err))
	}

	return resp.StatusCode, nil
}

// selectInstance picks a backend instance for the given route.
func (p *ReverseProxy) selectInstance(ctx context.Context, route *router.Route) (*discovery.Instance, error) {
	// If the route has a direct URL, use it as a synthetic instance.
	if route.ServiceURL != "" {
		u, err := url.Parse(route.ServiceURL)
		if err != nil {
			return nil, fmt.Errorf("parsing service URL %q: %w", route.ServiceURL, err)
		}
		host := u.Hostname()
		port := 0
		if p := u.Port(); p != "" {
			fmt.Sscanf(p, "%d", &port) //nolint:errcheck
		}
		return &discovery.Instance{
			ID:          route.ServiceName,
			ServiceName: route.ServiceName,
			Host:        host,
			Port:        port,
			Healthy:     true,
			Weight:      1,
		}, nil
	}

	instances, err := p.registry.GetInstances(ctx, route.ServiceName)
	if err != nil {
		return nil, err
	}

	strategy := Strategy(route.LoadBalance)
	if strategy == "" {
		strategy = p.defaultLB
	}

	lb := p.getOrCreateBalancer(route.ServiceName, strategy)
	return lb.Next(instances)
}

// getOrCreateBalancer returns or creates a load balancer for the given service.
func (p *ReverseProxy) getOrCreateBalancer(serviceName string, strategy Strategy) LoadBalancer {
	key := string(strategy) + ":" + serviceName
	if lb, ok := p.balancers[key]; ok {
		return lb
	}
	lb := NewLoadBalancer(strategy)
	p.balancers[key] = lb
	return lb
}

// buildTargetURL constructs the upstream URL from route rules and instance address.
func (p *ReverseProxy) buildTargetURL(r *http.Request, route *router.Route, instance *discovery.Instance) string {
	scheme := "http"
	host := instance.Address()

	path := r.URL.Path
	if route.StripPrefix != "" {
		path = strings.TrimPrefix(path, route.StripPrefix)
		if path == "" {
			path = "/"
		}
	}

	targetURL := scheme + "://" + host + path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}
	return targetURL
}

// copyHeaders copies all headers from src to dst, skipping hop-by-hop headers.
func copyHeaders(dst, src http.Header) {
	hopByHop := map[string]bool{
		"Connection":          true,
		"Keep-Alive":          true,
		"Proxy-Authenticate":  true,
		"Proxy-Authorization": true,
		"Te":                  true,
		"Trailers":            true,
		"Transfer-Encoding":   true,
		"Upgrade":             true,
	}
	for k, vv := range src {
		if hopByHop[k] {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// setForwardingHeaders adds X-Forwarded-* headers to the upstream request.
func setForwardingHeaders(outReq *http.Request, origReq *http.Request) {
	clientIP, _, err := net.SplitHostPort(origReq.RemoteAddr)
	if err != nil {
		clientIP = origReq.RemoteAddr
	}

	if prior := outReq.Header.Get("X-Forwarded-For"); prior != "" {
		outReq.Header.Set("X-Forwarded-For", prior+", "+clientIP)
	} else {
		outReq.Header.Set("X-Forwarded-For", clientIP)
	}

	if origReq.TLS != nil {
		outReq.Header.Set("X-Forwarded-Proto", "https")
	} else {
		outReq.Header.Set("X-Forwarded-Proto", "http")
	}

	outReq.Header.Set("X-Forwarded-Host", origReq.Host)
}
