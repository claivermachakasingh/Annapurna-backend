package queries

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ────────────────────────────────────────────────────────────────────────────
// Domain types
// ────────────────────────────────────────────────────────────────────────────

// Match mirrors the matches table.
type Match struct {
	ID          uuid.UUID
	BatchID     uuid.UUID
	RecipientID uuid.UUID
	Status      string // pending | accepted | rejected | expired
	OfferedAt   time.Time
	RespondedAt *time.Time
	ExpiresAt   time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreateMatchParams holds insert fields.
type CreateMatchParams struct {
	ID          uuid.UUID
	BatchID     uuid.UUID
	RecipientID uuid.UUID
	OfferedAt   time.Time
	ExpiresAt   time.Time
}

// UpdateMatchStatusParams holds fields for status transition.
type UpdateMatchStatusParams struct {
	ID     uuid.UUID
	Status string
}

// ────────────────────────────────────────────────────────────────────────────
// Queries
// ────────────────────────────────────────────────────────────────────────────

const createMatchSQL = `
INSERT INTO matches
    (id, batch_id, recipient_id, status, offered_at, expires_at, created_at, updated_at)
VALUES ($1, $2, $3, 'pending', $4, $5, NOW(), NOW())
RETURNING id, batch_id, recipient_id, status, offered_at, responded_at, expires_at, created_at, updated_at`

// CreateMatch inserts a new match offer.
func CreateMatch(ctx context.Context, db DBTX, p CreateMatchParams) (Match, error) {
	row := db.QueryRow(ctx, createMatchSQL,
		p.ID, p.BatchID, p.RecipientID, p.OfferedAt, p.ExpiresAt,
	)
	return scanMatch(row)
}

const getMatchSQL = `
SELECT id, batch_id, recipient_id, status, offered_at, responded_at, expires_at, created_at, updated_at
FROM matches
WHERE id = $1
LIMIT 1`

// GetMatch returns the match with the given ID.
func GetMatch(ctx context.Context, db DBTX, id uuid.UUID) (Match, error) {
	row := db.QueryRow(ctx, getMatchSQL, id)
	return scanMatch(row)
}

const updateMatchStatusSQL = `
UPDATE matches
SET status       = $2,
    responded_at = CASE WHEN $2 IN ('accepted','rejected') THEN NOW() ELSE responded_at END,
    updated_at   = NOW()
WHERE id = $1
RETURNING id, batch_id, recipient_id, status, offered_at, responded_at, expires_at, created_at, updated_at`

// UpdateMatchStatus transitions a match to a new status.
func UpdateMatchStatus(ctx context.Context, db DBTX, p UpdateMatchStatusParams) (Match, error) {
	row := db.QueryRow(ctx, updateMatchStatusSQL, p.ID, p.Status)
	return scanMatch(row)
}

const listMatchesForBatchSQL = `
SELECT id, batch_id, recipient_id, status, offered_at, responded_at, expires_at, created_at, updated_at
FROM matches
WHERE batch_id = $1
  AND ($2::uuid IS NULL OR id > $2)
ORDER BY id ASC
LIMIT $3`

// ListMatchesForBatch returns cursor-paginated matches for a given batch.
func ListMatchesForBatch(ctx context.Context, db DBTX, batchID, afterID uuid.UUID, limit int) ([]Match, error) {
	var afterArg interface{} = afterID
	if afterID == uuid.Nil {
		afterArg = nil
	}
	rows, err := db.Query(ctx, listMatchesForBatchSQL, batchID, afterArg, limit)
	if err != nil {
		return nil, fmt.Errorf("queries.ListMatchesForBatch: %w", err)
	}
	defer rows.Close()
	return collectMatches(rows)
}

const listMatchesForRecipientSQL = `
SELECT id, batch_id, recipient_id, status, offered_at, responded_at, expires_at, created_at, updated_at
FROM matches
WHERE recipient_id = $1
  AND ($2::uuid IS NULL OR id > $2)
ORDER BY id ASC
LIMIT $3`

// ListMatchesForRecipient returns cursor-paginated matches for a given recipient.
func ListMatchesForRecipient(ctx context.Context, db DBTX, recipientID, afterID uuid.UUID, limit int) ([]Match, error) {
	var afterArg interface{} = afterID
	if afterID == uuid.Nil {
		afterArg = nil
	}
	rows, err := db.Query(ctx, listMatchesForRecipientSQL, recipientID, afterArg, limit)
	if err != nil {
		return nil, fmt.Errorf("queries.ListMatchesForRecipient: %w", err)
	}
	defer rows.Close()
	return collectMatches(rows)
}

const expireUnansweredMatchesSQL = `
UPDATE matches
SET status = 'expired', updated_at = NOW()
WHERE status = 'pending'
  AND expires_at < NOW()`

// ExpireUnansweredMatches bulk-expires all pending matches whose deadline has
// passed.  Intended for background scheduler invocation.
func ExpireUnansweredMatches(ctx context.Context, db DBTX) (int64, error) {
	tag, err := db.Exec(ctx, expireUnansweredMatchesSQL)
	if err != nil {
		return 0, fmt.Errorf("queries.ExpireUnansweredMatches: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func scanMatch(row pgx.Row) (Match, error) {
	var m Match
	err := row.Scan(
		&m.ID, &m.BatchID, &m.RecipientID,
		&m.Status, &m.OfferedAt, &m.RespondedAt,
		&m.ExpiresAt, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return Match{}, fmt.Errorf("queries.scanMatch: %w", err)
	}
	return m, nil
}

func collectMatches(rows pgx.Rows) ([]Match, error) {
	var matches []Match
	for rows.Next() {
		var m Match
		if err := rows.Scan(
			&m.ID, &m.BatchID, &m.RecipientID,
			&m.Status, &m.OfferedAt, &m.RespondedAt,
			&m.ExpiresAt, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("queries.collectMatches: %w", err)
		}
		matches = append(matches, m)
	}
	return matches, rows.Err()
}
