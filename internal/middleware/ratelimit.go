package middleware

import (
	"fmt"
	"net"
	"net/http"
	"strconv"

	"go.uber.org/zap"

	"github.com/nicedavid98/api-gateway/internal/ratelimit"
	"github.com/nicedavid98/api-gateway/internal/router"
)

// RateLimitMiddleware enforces per-user, per-IP, and per-route rate limits.
type RateLimitMiddleware struct {
	limiter *ratelimit.RateLimiter
	logger  *zap.Logger
}

// NewRateLimitMiddleware creates a new RateLimitMiddleware.
func NewRateLimitMiddleware(limiter *ratelimit.RateLimiter, logger *zap.Logger) *RateLimitMiddleware {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &RateLimitMiddleware{
		limiter: limiter,
		logger:  logger,
	}
}

// Handler returns an http.Handler middleware that enforces rate limits.
func (m *RateLimitMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := router.RouteFromContext(r.Context())

		var (
			routeID    string
			routeRPS   float64
			routeBurst int
		)

		if route != nil {
			routeID = route.ID
			if route.RateLimit != nil {
				routeRPS = route.RateLimit.RPS
				routeBurst = route.RateLimit.Burst
			}
		}

		claims := ClaimsFromContext(r.Context())
		userID := ""
		if claims != nil {
			userID = claims.UserID
		}

		clientIP := extractClientIP(r)

		result, err := m.limiter.CheckRequest(r.Context(), routeID, userID, clientIP, routeRPS, routeBurst)
		if err != nil {
			m.logger.Error("rate limit check error",
				zap.String("route", routeID),
				zap.String("ip", clientIP),
				zap.Error(err),
			)
			// On Redis failure, allow the request through (fail-open).
			next.ServeHTTP(w, r)
			return
		}

		// Always set rate limit headers.
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(result.Limit))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(result.ResetAt.Unix(), 10))

		if !result.Allowed {
			m.logger.Info("rate limit exceeded",
				zap.String("route", routeID),
				zap.String("ip", clientIP),
				zap.String("user", userID),
				zap.String("scope", string(result.Scope)),
			)
			w.Header().Set("Retry-After", strconv.FormatInt(int64(result.RetryIn.Seconds())+1, 10))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, `{"error":"rate limit exceeded","retry_after":%d}`,
				int64(result.RetryIn.Seconds())+1)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// extractClientIP extracts the real client IP, checking X-Forwarded-For and X-Real-IP.
func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP (leftmost = original client).
		if idx := len(xff); idx > 0 {
			for i := 0; i < len(xff); i++ {
				if xff[i] == ',' {
					return xff[:i]
				}
			}
		}
		return xff
	}

	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
