// Package redisx provides a thin wrapper around go-redis for the SIH26234
// food-waste-redistribution platform. It handles connection bootstrapping,
// health monitoring, and the GracefulDegrade flag that downstream callers
// inspect before deciding whether to fall back to in-memory storage.
package redisx

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// GracefulDegrade is set to 1 atomically when Redis is unreachable so that
// callers can switch to the in-memory fallback without a separate health probe.
var GracefulDegrade atomic.Int32

// NewClient dials the default DB (DB 0) used for cache, rate-limit, locks,
// and pub/sub. It validates connectivity immediately and sets GracefulDegrade
// if Redis is not reachable (so the caller can still start).
func NewClient(redisURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("redisx: parse URL: %w", err)
	}
	opts.DialTimeout = 3 * time.Second
	opts.ReadTimeout = 2 * time.Second
	opts.WriteTimeout = 2 * time.Second
	opts.PoolSize = 20
	opts.MinIdleConns = 3
	opts.MaxRetries = 3

	c := redis.NewClient(opts)
	if err := ping(c); err != nil {
		GracefulDegrade.Store(1)
		// Return client anyway — the app degrades gracefully.
		return c, nil
	}
	GracefulDegrade.Store(0)
	return c, nil
}

// NewQueueClient dials DB 1, which is reserved for the job/sync queue so that
// cache evictions never accidentally drop enqueued work items.
func NewQueueClient(redisURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("redisx: parse queue URL: %w", err)
	}
	opts.DB = 1
	opts.DialTimeout = 3 * time.Second
	opts.ReadTimeout = 2 * time.Second
	opts.WriteTimeout = 2 * time.Second
	opts.PoolSize = 10
	opts.MinIdleConns = 2
	opts.MaxRetries = 3

	c := redis.NewClient(opts)
	if err := ping(c); err != nil {
		// Queue unavailable is still degradable — callers persist to DB instead.
		GracefulDegrade.Store(1)
		return c, nil
	}
	return c, nil
}

// Ping exposes a health check suitable for readiness probes.
func Ping(ctx context.Context, c *redis.Client) error {
	return ping(c)
}

func ping(c *redis.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return c.Ping(ctx).Err()
}
