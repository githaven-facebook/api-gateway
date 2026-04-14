package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
)

// luaAllowScript atomically checks and decrements a rate limit counter in Redis.
// Keys[1] = rate limit key
// ARGV[1] = max tokens (burst)
// ARGV[2] = refill rate (tokens per second, as float string)
// ARGV[3] = current time (unix nanoseconds)
// ARGV[4] = requested tokens
// Returns: [allowed (0/1), remaining, reset_at (unix seconds)].
const luaAllowScript = `
local key = KEYS[1]
local burst = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local requested = tonumber(ARGV[4])

local data = redis.call('HMGET', key, 'tokens', 'last_time')
local tokens = tonumber(data[1]) or burst
local last_time = tonumber(data[2]) or now

local elapsed = (now - last_time) / 1e9
local new_tokens = math.min(burst, tokens + elapsed * rate)

local allowed = 0
local remaining = math.floor(new_tokens)

if new_tokens >= requested then
    new_tokens = new_tokens - requested
    allowed = 1
    remaining = math.floor(new_tokens)
end

redis.call('HSET', key, 'tokens', new_tokens, 'last_time', now)
redis.call('EXPIRE', key, math.ceil(burst / rate) + 1)

local wait_secs = 0
if allowed == 0 then
    local deficit = requested - new_tokens
    wait_secs = math.ceil(deficit / rate)
end

return {allowed, remaining, wait_secs}
`

// RedisStore implements rate limiting backed by Redis using a Lua script for
// atomic check-and-decrement operations.
type RedisStore struct {
	client *redis.Client
	script *redis.Script
}

// NewRedisStore creates a new RedisStore.
func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{
		client: client,
		script: redis.NewScript(luaAllowScript),
	}
}

// KeyScope defines the scope of a rate limit key.
type KeyScope string

const (
	// ScopeUser limits by authenticated user ID.
	ScopeUser KeyScope = "user"

	// ScopeIP limits by client IP address.
	ScopeIP KeyScope = "ip"

	// ScopeRoute limits by route ID.
	ScopeRoute KeyScope = "route"

	// ScopeGlobal is a global limit.
	ScopeGlobal KeyScope = "global"
)

// AllowResult holds the result of a rate limit check.
type AllowResult struct {
	Allowed   bool
	Remaining int64
	ResetIn   time.Duration
	Scope     KeyScope
}

// Allow checks whether the request with the given key and scope is within limits.
// burst and rate define the bucket parameters.
func (s *RedisStore) Allow(
	ctx context.Context,
	scope KeyScope,
	identifier string,
	burst int,
	rate float64,
) (*AllowResult, error) {
	key := fmt.Sprintf("gw:rl:%s:%s", scope, identifier)
	now := time.Now().UnixNano()

	result, err := s.script.Run(ctx, s.client,
		[]string{key},
		burst, rate, now, 1,
	).Int64Slice()
	if err != nil {
		return nil, fmt.Errorf("running rate limit script for key %q: %w", key, err)
	}

	if len(result) < 3 {
		return nil, fmt.Errorf("unexpected result length from rate limit script: %d", len(result))
	}

	return &AllowResult{
		Allowed:   result[0] == 1,
		Remaining: result[1],
		ResetIn:   time.Duration(result[2]) * time.Second,
		Scope:     scope,
	}, nil
}

// BuildKey constructs the Redis key for the given scope and identifier.
func BuildKey(scope KeyScope, identifier string) string {
	return fmt.Sprintf("gw:rl:%s:%s", scope, identifier)
}
