package middleware

import (
	"errors"
	"fmt"
	"net/http"

	"go.uber.org/zap"

	"github.com/nicedavid98/api-gateway/internal/circuitbreaker"
	"github.com/nicedavid98/api-gateway/internal/router"
)

// CircuitBreakerMiddleware wraps proxy calls with circuit breaker protection.
type CircuitBreakerMiddleware struct {
	manager *circuitbreaker.Manager
	logger  *zap.Logger
}

// NewCircuitBreakerMiddleware creates a new CircuitBreakerMiddleware.
func NewCircuitBreakerMiddleware(manager *circuitbreaker.Manager, logger *zap.Logger) *CircuitBreakerMiddleware {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &CircuitBreakerMiddleware{
		manager: manager,
		logger:  logger,
	}
}

// Handler returns an http.Handler middleware that enforces circuit breaking per service.
func (m *CircuitBreakerMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := router.RouteFromContext(r.Context())
		if route == nil {
			next.ServeHTTP(w, r)
			return
		}

		serviceName := route.ServiceName
		if serviceName == "" || serviceName == "gateway" {
			next.ServeHTTP(w, r)
			return
		}

		var breaker *circuitbreaker.Breaker
		if route.CircuitBreaker != nil {
			s := circuitbreaker.Settings{
				MaxFailures:         route.CircuitBreaker.Threshold,
				Timeout:             route.CircuitBreaker.Timeout,
				MaxHalfOpenRequests: route.CircuitBreaker.MaxHalfOpen,
			}
			breaker = m.manager.GetWithSettings(serviceName, s)
		} else {
			breaker = m.manager.Get(serviceName)
		}

		if err := breaker.Allow(); err != nil {
			if errors.Is(err, circuitbreaker.ErrCircuitOpen) {
				m.logger.Warn("circuit breaker open, rejecting request",
					zap.String("service", serviceName),
					zap.String("path", r.URL.Path),
				)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprintf(w, `{"error":"service temporarily unavailable","service":%q}`, serviceName)
				return
			}
		}

		// Wrap the ResponseWriter to capture the status code.
		rec := newStatusRecorder(w)
		next.ServeHTTP(rec, r)

		// Record outcome based on response status.
		if rec.statusCode >= http.StatusInternalServerError {
			breaker.RecordFailure()
			m.logger.Debug("circuit breaker recorded failure",
				zap.String("service", serviceName),
				zap.Int("status", rec.statusCode),
			)
		} else {
			breaker.RecordSuccess()
		}
	})
}

// statusRecorder wraps http.ResponseWriter to capture the written status code.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func newStatusRecorder(w http.ResponseWriter) *statusRecorder {
	return &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
}

func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}
