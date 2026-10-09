package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// maxRetries is the maximum number of times RunTx will attempt a
	// transaction when PostgreSQL returns a serialization failure.
	maxRetries = 3

	// pgErrCodeSerializationFailure is the SQLSTATE code for
	// ERROR: could not serialize access due to concurrent update (40001).
	pgErrCodeSerializationFailure = "40001"

	// pgErrCodeDeadlockDetected is the SQLSTATE for deadlock (40P01).
	// We also retry on deadlock so callers don't need to handle it separately.
	pgErrCodeDeadlockDetected = "40P01"
)

// TxFn is the user-supplied callback executed inside a transaction.
// If it returns a non-nil error the transaction is rolled back; on nil it is
// committed.  The callback must use the provided pgx.Tx for all DB access so
// the work participates in the transaction.
type TxFn func(ctx context.Context, tx pgx.Tx) error

// RunTx runs fn inside a serializable transaction, retrying automatically on
// PostgreSQL serialization-failure (SQLSTATE 40001) and deadlock (40P01) up
// to maxRetries times.
//
// Usage:
//
//	err := store.RunTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
//	    // use tx for all queries
//	    return nil
//	})
func RunTx(ctx context.Context, pool *pgxpool.Pool, fn TxFn) error {
	txOpts := pgx.TxOptions{
		IsoLevel:   pgx.Serializable,
		AccessMode: pgx.ReadWrite,
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := runOnce(ctx, pool, txOpts, fn); err != nil {
			if isRetryable(err) {
				lastErr = err
				continue
			}
			return err
		}
		return nil // committed successfully
	}
	return fmt.Errorf("store.RunTx: all %d attempts failed: %w", maxRetries, lastErr)
}

// runOnce executes fn in a single transaction attempt.
func runOnce(ctx context.Context, pool *pgxpool.Pool, opts pgx.TxOptions, fn TxFn) (retErr error) {
	tx, err := pool.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("store.RunTx: begin: %w", err)
	}

	defer func() {
		if retErr != nil {
			// Best-effort rollback; ignore its error to preserve the original.
			_ = tx.Rollback(ctx)
		}
	}()

	if err := fn(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store.RunTx: commit: %w", err)
	}
	return nil
}

// isRetryable reports whether err is a PostgreSQL error that the caller can
// safely retry (serialization failure or deadlock detected).
func isRetryable(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgErrCodeSerializationFailure, pgErrCodeDeadlockDetected:
			return true
		}
	}
	return false
}
