package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
)

const (
	tokenCachePrefix     = "gw:token:valid:"
	blacklistPrefix      = "gw:token:blacklist:"
	defaultTokenCacheTTL = 5 * time.Minute
)

// TokenCache provides Redis-backed caching for validated tokens and a blacklist
// for revoked tokens.
type TokenCache struct {
	client   *redis.Client
	cacheTTL time.Duration
}

// NewTokenCache creates a new TokenCache backed by the given Redis client.
func NewTokenCache(client *redis.Client, cacheTTL time.Duration) *TokenCache {
	if cacheTTL <= 0 {
		cacheTTL = defaultTokenCacheTTL
	}
	return &TokenCache{
		client:   client,
		cacheTTL: cacheTTL,
	}
}

// IsBlacklisted reports whether the given token (by its jti or hash) has been
// revoked.
func (c *TokenCache) IsBlacklisted(ctx context.Context, tokenID string) (bool, error) {
	exists, err := c.client.Exists(ctx, blacklistPrefix+tokenID).Result()
	if err != nil {
		return false, fmt.Errorf("checking blacklist for token %q: %w", tokenID, err)
	}
	return exists > 0, nil
}

// Blacklist marks a token as revoked until the given expiry time.
func (c *TokenCache) Blacklist(ctx context.Context, tokenID string, until time.Time) error {
	ttl := time.Until(until)
	if ttl <= 0 {
		// Token already expired; no need to blacklist.
		return nil
	}
	if err := c.client.Set(ctx, blacklistPrefix+tokenID, "1", ttl).Err(); err != nil {
		return fmt.Errorf("blacklisting token %q: %w", tokenID, err)
	}
	return nil
}

// SetValid caches a valid token's user ID for quick lookup.
func (c *TokenCache) SetValid(ctx context.Context, tokenHash, userID string) error {
	if err := c.client.Set(ctx, tokenCachePrefix+tokenHash, userID, c.cacheTTL).Err(); err != nil {
		return fmt.Errorf("caching valid token: %w", err)
	}
	return nil
}

// GetValid returns the user ID for a previously cached valid token.
// Returns ("", false, nil) when the token is not in the cache.
func (c *TokenCache) GetValid(ctx context.Context, tokenHash string) (string, bool, error) {
	userID, err := c.client.Get(ctx, tokenCachePrefix+tokenHash).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading token cache: %w", err)
	}
	return userID, true, nil
}

// Invalidate removes a token from the valid cache.
func (c *TokenCache) Invalidate(ctx context.Context, tokenHash string) error {
	if err := c.client.Del(ctx, tokenCachePrefix+tokenHash).Err(); err != nil {
		return fmt.Errorf("invalidating token cache entry: %w", err)
	}
	return nil
}
