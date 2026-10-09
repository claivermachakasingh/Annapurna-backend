package redisx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const dedupeTTL = 24 * time.Hour

// dedupeKey builds the Redis key for a (userID, clientEventID) pair.
// Format: dedupe:{userID}:{clientEventID}
func dedupeKey(userID int, clientEventID string) string {
	return fmt.Sprintf("dedupe:%d:%s", userID, clientEventID)
}

// SetDedupeResult stores the resultRef (e.g. an order ID or idempotency token)
// for a (userID, clientEventID) pair using SET NX with a 24-hour TTL.
//
// Returns (true, nil)  — first time this event was seen; caller should process it.
// Returns (false, nil) — duplicate; caller should replay resultRef.
// Returns (false, err) — Redis error; caller should treat as non-duplicate to be safe.
func SetDedupeResult(ctx context.Context, c *redis.Client, userID int, clientEventID, resultRef string) (bool, error) {
	if GracefulDegrade.Load() == 1 {
		// Fail open — process the event; risk of duplicate is preferable to data loss.
		return true, nil
	}
	key := dedupeKey(userID, clientEventID)
	ok, err := c.SetNX(ctx, key, resultRef, dedupeTTL).Result()
	if err != nil {
		return false, fmt.Errorf("redisx: set dedupe %q: %w", key, err)
	}
	return ok, nil
}

// GetDedupeResult retrieves the resultRef previously stored for (userID, clientEventID).
// Returns ("", false, nil) on cache miss.
func GetDedupeResult(ctx context.Context, c *redis.Client, userID int, clientEventID string) (resultRef string, found bool, err error) {
	if GracefulDegrade.Load() == 1 {
		return "", false, nil
	}
	key := dedupeKey(userID, clientEventID)
	val, err := c.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("redisx: get dedupe %q: %w", key, err)
	}
	return val, true, nil
}
