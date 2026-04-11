package ratelimit

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// RateLimitResult contains the outcome of a rate limit evaluation.
type RateLimitResult struct {
	Allowed   bool
	Limit     int
	Remaining int
	ResetAt   time.Time
	RetryIn   time.Duration
	Scope     KeyScope
}

// RateLimiter composes global, per-user, and per-route limits.
type RateLimiter struct {
	store      *RedisStore
	logger     *zap.Logger
	defaultRPS float64
	burstSize  int
}

// Config holds RateLimiter configuration.
type Config struct {
	DefaultRPS float64
	BurstSize  int
	Logger     *zap.Logger
}

// NewRateLimiter creates a new RateLimiter backed by the provided RedisStore.
func NewRateLimiter(store *RedisStore, cfg Config) *RateLimiter {
	if cfg.DefaultRPS <= 0 {
		cfg.DefaultRPS = 1000
	}
	if cfg.BurstSize <= 0 {
		cfg.BurstSize = 2000
	}
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	return &RateLimiter{
		store:      store,
		logger:     cfg.Logger,
		defaultRPS: cfg.DefaultRPS,
		burstSize:  cfg.BurstSize,
	}
}

// CheckRequest evaluates rate limits for the given request context.
// routeRPS/routeBurst override the global defaults when non-zero.
// userID and clientIP are used for per-identity limits.
func (l *RateLimiter) CheckRequest(
	ctx context.Context,
	routeID string,
	userID string,
	clientIP string,
	routeRPS float64,
	routeBurst int,
) (*RateLimitResult, error) {
	rps := l.defaultRPS
	burst := l.burstSize
	if routeRPS > 0 {
		rps = routeRPS
	}
	if routeBurst > 0 {
		burst = routeBurst
	}

	// Check global limit first.
	globalResult, err := l.store.Allow(ctx, ScopeGlobal, "gateway", l.burstSize, l.defaultRPS)
	if err != nil {
		l.logger.Warn("global rate limit check failed", zap.Error(err))
	} else if !globalResult.Allowed {
		return toResult(globalResult, l.burstSize), nil
	}

	// Check per-IP limit.
	if clientIP != "" {
		ipResult, err := l.store.Allow(ctx, ScopeIP, clientIP, burst, rps)
		if err != nil {
			l.logger.Warn("IP rate limit check failed", zap.String("ip", clientIP), zap.Error(err))
		} else if !ipResult.Allowed {
			return toResult(ipResult, burst), nil
		}
	}

	// Check per-user limit for authenticated requests.
	if userID != "" {
		userResult, err := l.store.Allow(ctx, ScopeUser, userID, burst, rps)
		if err != nil {
			l.logger.Warn("user rate limit check failed", zap.String("user", userID), zap.Error(err))
		} else if !userResult.Allowed {
			return toResult(userResult, burst), nil
		}
	}

	// Check per-route limit.
	if routeID != "" {
		routeResult, err := l.store.Allow(ctx, ScopeRoute, routeID, burst, rps)
		if err != nil {
			l.logger.Warn("route rate limit check failed", zap.String("route", routeID), zap.Error(err))
		} else if !routeResult.Allowed {
			return toResult(routeResult, burst), nil
		}
	}

	return &RateLimitResult{
		Allowed:   true,
		Limit:     burst,
		Remaining: burst, // approximate; last check wins
		ResetAt:   time.Now().Add(time.Second),
	}, nil
}

// toResult converts an AllowResult into a RateLimitResult.
func toResult(ar *AllowResult, limit int) *RateLimitResult {
	return &RateLimitResult{
		Allowed:   ar.Allowed,
		Limit:     limit,
		Remaining: int(ar.Remaining),
		ResetAt:   time.Now().Add(ar.ResetIn),
		RetryIn:   ar.ResetIn,
		Scope:     ar.Scope,
	}
}
