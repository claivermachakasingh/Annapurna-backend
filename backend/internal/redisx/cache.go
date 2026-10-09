package redisx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Set stores value under key with the given TTL. A TTL of 0 means no expiry.
// If GracefulDegrade is active the call is silently no-oped (caller should
// write to InMemoryCache instead).
func Set(ctx context.Context, c *redis.Client, key string, value string, ttl time.Duration) error {
	if GracefulDegrade.Load() == 1 {
		return nil
	}
	return c.Set(ctx, key, value, ttl).Err()
}

// Get retrieves value for key. Returns ("", false) on cache miss or when
// GracefulDegrade is active so the caller falls through to the DB.
func Get(ctx context.Context, c *redis.Client, key string) (string, bool) {
	if GracefulDegrade.Load() == 1 {
		return "", false
	}
	val, err := c.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false
	}
	if err != nil {
		return "", false
	}
	return val, true
}

// Delete removes one or more keys.
func Delete(ctx context.Context, c *redis.Client, keys ...string) error {
	if GracefulDegrade.Load() == 1 {
		return nil
	}
	if len(keys) == 0 {
		return nil
	}
	return c.Del(ctx, keys...).Err()
}

// InvalidatePrefix scans for all keys matching prefix* and deletes them in
// batches of 100. It uses cursor-based SCAN to avoid blocking Redis.
func InvalidatePrefix(ctx context.Context, c *redis.Client, prefix string) error {
	if GracefulDegrade.Load() == 1 {
		return nil
	}
	pattern := prefix + "*"
	var cursor uint64
	for {
		keys, nextCursor, err := c.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return fmt.Errorf("redisx: scan %q: %w", pattern, err)
		}
		if len(keys) > 0 {
			if err := c.Del(ctx, keys...).Err(); err != nil {
				return fmt.Errorf("redisx: del batch %q: %w", pattern, err)
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return nil
}
