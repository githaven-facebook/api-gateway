// Package middleware provides HTTP middleware components for the API gateway.
package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/nicedavid98/api-gateway/internal/auth"
	"github.com/nicedavid98/api-gateway/internal/router"
)

type authContextKey int

const (
	claimsContextKey authContextKey = iota
)

// AuthMiddleware validates JWT Bearer tokens and injects user context.
type AuthMiddleware struct {
	validator  *auth.Validator
	tokenCache *auth.TokenCache
	logger     *zap.Logger
}

// NewAuthMiddleware creates a new AuthMiddleware.
func NewAuthMiddleware(validator *auth.Validator, tokenCache *auth.TokenCache, logger *zap.Logger) *AuthMiddleware {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &AuthMiddleware{
		validator:  validator,
		tokenCache: tokenCache,
		logger:     logger,
	}
}

// Handler returns an http.Handler middleware that validates JWT tokens.
func (m *AuthMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := router.RouteFromContext(r.Context())

		tokenStr := extractBearerToken(r)

		if tokenStr == "" {
			if route != nil && route.AuthRequired {
				http.Error(w, `{"error":"missing authorization token"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		claims, err := m.validateToken(r.Context(), tokenStr)
		if err != nil {
			m.logger.Debug("token validation failed",
				zap.String("path", r.URL.Path),
				zap.Error(err),
			)
			if route != nil && route.AuthRequired {
				http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		// Inject user identity into request headers for downstream services.
		r.Header.Set("X-User-Id", claims.UserID)
		r.Header.Set("X-User-Email", claims.Email)
		if len(claims.Roles) > 0 {
			r.Header.Set("X-User-Roles", strings.Join(claims.Roles, ","))
		}
		if len(claims.Permissions) > 0 {
			r.Header.Set("X-User-Permissions", strings.Join(claims.Permissions, ","))
		}

		ctx := WithClaims(r.Context(), claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// validateToken checks the token cache before performing full JWT validation.
func (m *AuthMiddleware) validateToken(ctx context.Context, tokenStr string) (*auth.Claims, error) {
	tokenHash := hashToken(tokenStr)

	if m.tokenCache != nil {
		// Check blacklist first.
		blacklisted, err := m.tokenCache.IsBlacklisted(ctx, tokenHash)
		if err != nil {
			m.logger.Warn("blacklist check failed, proceeding with validation", zap.Error(err))
		} else if blacklisted {
			return nil, ErrTokenRevoked
		}
	}

	claims, err := m.validator.Validate(ctx, tokenStr)
	if err != nil {
		return nil, err
	}

	// Cache the validated token.
	if m.tokenCache != nil {
		if cacheErr := m.tokenCache.SetValid(ctx, tokenHash, claims.UserID); cacheErr != nil {
			m.logger.Warn("failed to cache token", zap.Error(cacheErr))
		}
	}

	return claims, nil
}

// extractBearerToken extracts the Bearer token from the Authorization header.
func extractBearerToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// hashToken returns a short SHA-256 hex hash of the token for use as a cache key.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:16])
}

// WithClaims stores JWT claims in the request context.
func WithClaims(ctx context.Context, claims *auth.Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey, claims)
}

// ClaimsFromContext retrieves JWT claims from the request context.
func ClaimsFromContext(ctx context.Context) *auth.Claims {
	claims, _ := ctx.Value(claimsContextKey).(*auth.Claims)
	return claims
}

// ErrTokenRevoked indicates the token has been revoked.
var ErrTokenRevoked = &tokenError{"token has been revoked"}

type tokenError struct{ msg string }

func (e *tokenError) Error() string { return e.msg }
