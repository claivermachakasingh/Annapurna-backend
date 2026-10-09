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

// QualityCheck mirrors the quality_checks table.
type QualityCheck struct {
	ID          uuid.UUID
	BatchID     uuid.UUID
	InspectorID uuid.UUID
	Score       float64
	Notes       *string
	ImageURLs   []string
	CheckedAt   time.Time
	CreatedAt   time.Time
}

// CreateQualityCheckParams holds insert fields.
type CreateQualityCheckParams struct {
	ID          uuid.UUID
	BatchID     uuid.UUID
	InspectorID uuid.UUID
	Score       float64
	Notes       *string
	ImageURLs   []string
	CheckedAt   time.Time
}

// ────────────────────────────────────────────────────────────────────────────
// Queries
// ────────────────────────────────────────────────────────────────────────────

const createQualityCheckSQL = `
INSERT INTO quality_checks
    (id, batch_id, inspector_id, score, notes, image_urls, checked_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
RETURNING id, batch_id, inspector_id, score, notes, image_urls, checked_at, created_at`

// CreateQualityCheck inserts a quality check record.
func CreateQualityCheck(ctx context.Context, db DBTX, p CreateQualityCheckParams) (QualityCheck, error) {
	row := db.QueryRow(ctx, createQualityCheckSQL,
		p.ID, p.BatchID, p.InspectorID,
		p.Score, p.Notes, p.ImageURLs, p.CheckedAt,
	)
	return scanQualityCheck(row)
}

const getLatestQualityCheckSQL = `
SELECT id, batch_id, inspector_id, score, notes, image_urls, checked_at, created_at
FROM quality_checks
WHERE batch_id = $1
ORDER BY checked_at DESC
LIMIT 1`

// GetLatestQualityCheck returns the most recent quality check for a batch.
// Returns pgx.ErrNoRows when no check exists.
func GetLatestQualityCheck(ctx context.Context, db DBTX, batchID uuid.UUID) (QualityCheck, error) {
	row := db.QueryRow(ctx, getLatestQualityCheckSQL, batchID)
	return scanQualityCheck(row)
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func scanQualityCheck(row pgx.Row) (QualityCheck, error) {
	var q QualityCheck
	err := row.Scan(
		&q.ID, &q.BatchID, &q.InspectorID,
		&q.Score, &q.Notes, &q.ImageURLs,
		&q.CheckedAt, &q.CreatedAt,
	)
	if err != nil {
		return QualityCheck{}, fmt.Errorf("queries.scanQualityCheck: %w", err)
	}
	return q, nil
}
