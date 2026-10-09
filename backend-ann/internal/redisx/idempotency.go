package redisx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const idempotencyTTL = 24 * time.Hour

// idempotencyPayload is the value stored for each idempotency key.
type idempotencyPayload struct {
	StatusCode int    `json:"status_code"`
	Body       []byte `json:"body"`
}

// idempotencyKey builds the Redis key namespace.
// Format: idem:{userID}:{key}
func idempotencyKey(userID int, key string) string {
	return fmt.Sprintf("idem:%d:%s", userID, key)
}

// StoreIdempotency persists the HTTP response (status code + body bytes) for
// the given (userID, idempotency-key) pair. The entry expires after 24 h as
// per RFC 8594 / Stripe convention.
//
// It returns an error only on Redis write failure; a duplicate store (i.e. the
// key already exists) is silently accepted.
func StoreIdempotency(ctx context.Context, c *redis.Client, userID int, key string, statusCode int, body []byte) error {
	if GracefulDegrade.Load() == 1 {
		return nil
	}
	pl := idempotencyPayload{StatusCode: statusCode, Body: body}
	data, err := json.Marshal(pl)
	if err != nil {
		return fmt.Errorf("redisx: marshal idempotency payload: %w", err)
	}
	rk := idempotencyKey(userID, key)
	// SET NX: only set if not already present (first write wins).
	return c.SetNX(ctx, rk, data, idempotencyTTL).Err()
}

// GetIdempotency retrieves the cached response for the given (userID, key).
// Returns (0, nil, false, nil) on cache miss.
func GetIdempotency(ctx context.Context, c *redis.Client, userID int, key string) (statusCode int, body []byte, found bool, err error) {
	if GracefulDegrade.Load() == 1 {
		return 0, nil, false, nil
	}
	rk := idempotencyKey(userID, key)
	data, err := c.Get(ctx, rk).Bytes()
	if errors.Is(err, redis.Nil) {
		return 0, nil, false, nil
	}
	if err != nil {
		return 0, nil, false, fmt.Errorf("redisx: get idempotency: %w", err)
	}
	var pl idempotencyPayload
	if err := json.Unmarshal(data, &pl); err != nil {
		return 0, nil, false, fmt.Errorf("redisx: unmarshal idempotency payload: %w", err)
	}
	return pl.StatusCode, pl.Body, true, nil
}
