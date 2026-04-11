// Package metrics provides Prometheus metrics for the API gateway.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/nicedavid98/api-gateway/internal/router"
)

const namespace = "gateway"

// Metrics holds all Prometheus metric collectors for the gateway.
type Metrics struct {
	requestsTotal       *prometheus.CounterVec
	requestDuration     *prometheus.HistogramVec
	activeConnections   prometheus.Gauge
	circuitBreakerState *prometheus.GaugeVec
	rateLimitExceeded   *prometheus.CounterVec
	upstreamLatency     *prometheus.HistogramVec
}

// New creates and registers all gateway Prometheus metrics.
func New(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	factory := promauto.With(reg)

	return &Metrics{
		requestsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "requests_total",
				Help:      "Total number of HTTP requests processed by the gateway.",
			},
			[]string{"service", "method", "status"},
		),

		requestDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "request_duration_seconds",
				Help:      "HTTP request duration in seconds.",
				Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
			},
			[]string{"service", "method", "status"},
		),

		activeConnections: factory.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "active_connections",
				Help:      "Number of active connections currently handled by the gateway.",
			},
		),

		circuitBreakerState: factory.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "circuit_breaker_state",
				Help:      "Circuit breaker state: 0=closed, 1=open, 2=half-open.",
			},
			[]string{"service"},
		),

		rateLimitExceeded: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "rate_limit_exceeded_total",
				Help:      "Total number of requests rejected due to rate limiting.",
			},
			[]string{"route", "scope"},
		),

		upstreamLatency: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "upstream_latency_seconds",
				Help:      "Latency of upstream service requests in seconds.",
				Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
			},
			[]string{"service"},
		),
	}
}

// RecordRequest records a completed HTTP request.
func (m *Metrics) RecordRequest(service, method string, statusCode int, duration time.Duration) {
	status := strconv.Itoa(statusCode)
	m.requestsTotal.WithLabelValues(service, method, status).Inc()
	m.requestDuration.WithLabelValues(service, method, status).Observe(duration.Seconds())
}

// RecordUpstreamLatency records the latency of an upstream service call.
func (m *Metrics) RecordUpstreamLatency(service string, duration time.Duration) {
	m.upstreamLatency.WithLabelValues(service).Observe(duration.Seconds())
}

// SetCircuitBreakerState updates the circuit breaker state gauge for a service.
// state: 0=closed, 1=open, 2=half-open.
func (m *Metrics) SetCircuitBreakerState(service string, state float64) {
	m.circuitBreakerState.WithLabelValues(service).Set(state)
}

// IncRateLimitExceeded increments the rate limit exceeded counter.
func (m *Metrics) IncRateLimitExceeded(route, scope string) {
	m.rateLimitExceeded.WithLabelValues(route, scope).Inc()
}

// TrackActiveConnections returns a middleware that tracks active connections.
func (m *Metrics) TrackActiveConnections(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.activeConnections.Inc()
		defer m.activeConnections.Dec()
		next.ServeHTTP(w, r)
	})
}

// InstrumentHandler returns a middleware that records request metrics.
func (m *Metrics) InstrumentHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := newStatusRecorder(w)
		next.ServeHTTP(rec, r)

		duration := time.Since(start)
		route := router.RouteFromContext(r.Context())
		service := "unknown"
		if route != nil && route.ServiceName != "" {
			service = route.ServiceName
		}

		m.RecordRequest(service, r.Method, rec.statusCode, duration)
	})
}

// newStatusRecorder creates a ResponseWriter wrapper that captures status codes.
func newStatusRecorder(w http.ResponseWriter) *metricsResponseWriter {
	return &metricsResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
}

type metricsResponseWriter struct {
	http.ResponseWriter
	statusCode    int
	headerWritten bool
}

func (m *metricsResponseWriter) WriteHeader(code int) {
	if !m.headerWritten {
		m.statusCode = code
		m.headerWritten = true
		m.ResponseWriter.WriteHeader(code)
	}
}

func (m *metricsResponseWriter) Write(b []byte) (int, error) {
	if !m.headerWritten {
		m.WriteHeader(http.StatusOK)
	}
	return m.ResponseWriter.Write(b)
}

// Handler returns the Prometheus HTTP handler for the /metrics endpoint.
func Handler() http.Handler {
	return promhttp.Handler()
}
