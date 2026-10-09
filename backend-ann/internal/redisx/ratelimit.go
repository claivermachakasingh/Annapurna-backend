package redisx

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// slidingWindowScript implements a token-bucket-free sliding window using a
// sorted set. Score = arrival timestamp in milliseconds. Members are unique
// per-request UUIDs generated inside Lua using redis.call("TIME").
//
// KEYS[1] = rate-limit key
// ARGV[1] = window width in milliseconds
// ARGV[2] = max allowed requests per window
// ARGV[3] = current time in milliseconds (injected from Go for testability)
// ARGV[4] = request UUID (injected from Go for testability)
//
// Returns {1, 0} if allowed, {0, retryAfterMs} if denied.
const slidingWindowScript = `
local key      = KEYS[1]
local now      = tonumber(ARGV[1])
local window   = tonumber(ARGV[2])
local limit    = tonumber(ARGV[3])
local uuid     = ARGV[4]
local cutoff   = now - window

-- Drop elements outside the window.
redis.call("ZREMRANGEBYSCORE", key, "-inf", cutoff)

local count = redis.call("ZCARD", key)
if count < limit then
    redis.call("ZADD", key, now, uuid)
    redis.call("PEXPIRE", key, window)
    return {1, 0}
end

-- Find oldest entry to calculate retry-after.
local oldest = redis.call("ZRANGE", key, 0, 0, "WITHSCORES")
local retryAfter = 0
if #oldest >= 2 then
    retryAfter = tonumber(oldest[2]) + window - now
end
return {0, retryAfter}
`

var slidingWindowLua = redis.NewScript(slidingWindowScript)

// SlidingWindowLimit enforces a sliding-window rate limit.
//   - key    — unique rate-limit identifier (e.g. "rl:user:42:endpoint:/orders")
//   - limit  — maximum number of requests allowed within window
//   - window — time window duration
//
// Returns:
//   - allowed    true if the request is within the limit
//   - retryAfter how long the caller should wait before retrying (only meaningful when !allowed)
//   - err        non-nil only on Redis error; callers should allow on error to avoid DoS
func SlidingWindowLimit(ctx context.Context, c *redis.Client, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error) {
	if GracefulDegrade.Load() == 1 {
		// Fail open: Redis is down, don't block users.
		return true, 0, nil
	}

	nowMs := time.Now().UnixMilli()
	windowMs := window.Milliseconds()
	// Simple UUID-like member: timestamp+nanoseconds suffix is unique enough at
	// the rates we expect; a proper UUID import would add a dependency for
	// minimal benefit here.
	member := fmt.Sprintf("%d-%d", nowMs, time.Now().UnixNano())

	res, err := slidingWindowLua.Run(ctx, c,
		[]string{key},
		nowMs,
		windowMs,
		limit,
		member,
	).Int64Slice()
	if err != nil {
		// On error, fail open.
		return true, 0, fmt.Errorf("redisx: sliding window: %w", err)
	}

	if res[0] == 1 {
		return true, 0, nil
	}
	retryMs := res[1]
	if retryMs < 0 {
		retryMs = 0
	}
	return false, time.Duration(retryMs) * time.Millisecond, nil
}
