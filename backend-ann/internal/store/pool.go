// Package store provides PostgreSQL connectivity and query helpers for the
// SIH26234 food-waste redistribution platform.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// poolMinConns is the minimum number of idle connections kept alive.
	poolMinConns = 10
	// poolMaxConns is the maximum number of open connections in the pool.
	poolMaxConns = 20
	// poolMaxConnLifetime caps how long a connection may live before forced
	// recycling.  Helps avoid stale connections after DB maintenance events.
	poolMaxConnLifetime = 30 * time.Minute
	// poolMaxConnIdleTime evicts connections that have been idle longer than
	// this duration.
	poolMaxConnIdleTime = 5 * time.Minute
	// poolHealthCheckPeriod is how frequently the pool checks idle
	// connections against the server.
	poolHealthCheckPeriod = 1 * time.Minute
	// connectTimeout caps the initial connect attempt.
	connectTimeout = 10 * time.Second
	// statementTimeout enforces a server-side query limit for the API role.
	// Value is in milliseconds.
	statementTimeout = "5000"
)

// NewPool creates and validates a pgxpool.Pool using the supplied DATABASE_URL.
//
// The pool is configured with:
//   - min 10 / max 20 connections
//   - statement_timeout = 5 s applied via an AfterConnect hook so every
//     connection carries the restriction automatically
//   - max connection lifetime 30 min, idle eviction after 5 min
//
// The caller is responsible for calling pool.Close() when the process exits.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store.NewPool: parse config: %w", err)
	}

	cfg.MinConns = poolMinConns
	cfg.MaxConns = poolMaxConns
	cfg.MaxConnLifetime = poolMaxConnLifetime
	cfg.MaxConnIdleTime = poolMaxConnIdleTime
	cfg.HealthCheckPeriod = poolHealthCheckPeriod

	// AfterConnect runs once per new physical connection.  We set
	// statement_timeout here so the restriction follows each connection
	// independently of the application-level role used to connect.
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx,
			"SET statement_timeout = '"+statementTimeout+"'",
		)
		if err != nil {
			return fmt.Errorf("store.NewPool: set statement_timeout: %w", err)
		}
		return nil
	}

	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(connectCtx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store.NewPool: create pool: %w", err)
	}

	if err := ping(connectCtx, pool); err != nil {
		pool.Close()
		return nil, err
	}

	return pool, nil
}

// ping verifies the pool can reach the server within the given context.
func ping(ctx context.Context, pool *pgxpool.Pool) error {
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("store.NewPool: ping: %w", err)
	}
	return nil
}
