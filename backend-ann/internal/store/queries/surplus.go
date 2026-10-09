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

// SurplusBatch mirrors the surplus_batches table.
type SurplusBatch struct {
	ID            uuid.UUID
	KitchenID     uuid.UUID
	FoodType      string
	QuantityKg    float64
	ExpiresAt     time.Time
	PickupAddress string
	Status        string // available | matched | collected | expired
	SafetyStatus  string // pending | approved | rejected
	ApprovedBy    *uuid.UUID
	ApprovedAt    *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// CreateSurplusBatchParams holds the fields required when inserting.
type CreateSurplusBatchParams struct {
	ID            uuid.UUID
	KitchenID     uuid.UUID
	FoodType      string
	QuantityKg    float64
	ExpiresAt     time.Time
	PickupAddress string
}

// UpdateSurplusStatusParams holds params for a status update.
type UpdateSurplusStatusParams struct {
	ID     uuid.UUID
	Status string
}

// UpdateSurplusSafetyStatusParams holds params for safety status update.
type UpdateSurplusSafetyStatusParams struct {
	ID           uuid.UUID
	SafetyStatus string
	ApprovedBy   *uuid.UUID
}

// ────────────────────────────────────────────────────────────────────────────
// Queries
// ────────────────────────────────────────────────────────────────────────────

const createSurplusBatchSQL = `
INSERT INTO surplus_batches
    (id, kitchen_id, food_type, quantity_kg, expires_at, pickup_address,
     status, safety_status, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, 'available', 'pending', NOW(), NOW())
RETURNING id, kitchen_id, food_type, quantity_kg, expires_at, pickup_address,
          status, safety_status, approved_by, approved_at, created_at, updated_at`

// CreateSurplusBatch inserts a new surplus batch and returns the persisted row.
func CreateSurplusBatch(ctx context.Context, db DBTX, p CreateSurplusBatchParams) (SurplusBatch, error) {
	row := db.QueryRow(ctx, createSurplusBatchSQL,
		p.ID, p.KitchenID, p.FoodType, p.QuantityKg, p.ExpiresAt, p.PickupAddress,
	)
	return scanSurplusBatch(row)
}

const getSurplusBatchSQL = `
SELECT id, kitchen_id, food_type, quantity_kg, expires_at, pickup_address,
       status, safety_status, approved_by, approved_at, created_at, updated_at
FROM surplus_batches
WHERE id = $1
LIMIT 1`

// GetSurplusBatch returns the batch with the given ID.
func GetSurplusBatch(ctx context.Context, db DBTX, id uuid.UUID) (SurplusBatch, error) {
	row := db.QueryRow(ctx, getSurplusBatchSQL, id)
	return scanSurplusBatch(row)
}

const updateSurplusStatusSQL = `
UPDATE surplus_batches
SET status = $2, updated_at = NOW()
WHERE id = $1
RETURNING id, kitchen_id, food_type, quantity_kg, expires_at, pickup_address,
          status, safety_status, approved_by, approved_at, created_at, updated_at`

// UpdateSurplusStatus transitions a batch to a new lifecycle status.
func UpdateSurplusStatus(ctx context.Context, db DBTX, p UpdateSurplusStatusParams) (SurplusBatch, error) {
	row := db.QueryRow(ctx, updateSurplusStatusSQL, p.ID, p.Status)
	return scanSurplusBatch(row)
}

const updateSurplusSafetyStatusSQL = `
UPDATE surplus_batches
SET safety_status = $2,
    approved_by   = $3,
    approved_at   = CASE WHEN $2 = 'approved' THEN NOW() ELSE NULL END,
    updated_at    = NOW()
WHERE id = $1
RETURNING id, kitchen_id, food_type, quantity_kg, expires_at, pickup_address,
          status, safety_status, approved_by, approved_at, created_at, updated_at`

// UpdateSurplusSafetyStatus sets the safety_status (and approved_by / approved_at).
func UpdateSurplusSafetyStatus(ctx context.Context, db DBTX, p UpdateSurplusSafetyStatusParams) (SurplusBatch, error) {
	row := db.QueryRow(ctx, updateSurplusSafetyStatusSQL, p.ID, p.SafetyStatus, p.ApprovedBy)
	return scanSurplusBatch(row)
}

// MarkSurplusApproved is a convenience wrapper that sets safety_status = 'approved'.
func MarkSurplusApproved(ctx context.Context, db DBTX, batchID, approverID uuid.UUID) (SurplusBatch, error) {
	return UpdateSurplusSafetyStatus(ctx, db, UpdateSurplusSafetyStatusParams{
		ID:           batchID,
		SafetyStatus: "approved",
		ApprovedBy:   &approverID,
	})
}

const listSurplusBatchesSQL = `
SELECT id, kitchen_id, food_type, quantity_kg, expires_at, pickup_address,
       status, safety_status, approved_by, approved_at, created_at, updated_at
FROM surplus_batches
WHERE ($1::uuid IS NULL OR id > $1)
  AND ($2::text  IS NULL OR status = $2)
  AND ($3::uuid  IS NULL OR kitchen_id = $3)
ORDER BY id ASC
LIMIT $4`

// ListSurplusBatchesParams parameterises the cursor-paginated list query.
type ListSurplusBatchesParams struct {
	AfterID   uuid.UUID  // uuid.Nil = start from beginning
	Status    *string    // nil = all statuses
	KitchenID *uuid.UUID // nil = all kitchens
	Limit     int
}

// ListSurplusBatches returns a cursor-paginated slice of surplus batches.
func ListSurplusBatches(ctx context.Context, db DBTX, p ListSurplusBatchesParams) ([]SurplusBatch, error) {
	var afterArg interface{} = p.AfterID
	if p.AfterID == uuid.Nil {
		afterArg = nil
	}
	rows, err := db.Query(ctx, listSurplusBatchesSQL,
		afterArg, p.Status, p.KitchenID, p.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("queries.ListSurplusBatches: %w", err)
	}
	defer rows.Close()
	return collectSurplusBatches(rows)
}

const expireSurplusBatchesSQL = `
UPDATE surplus_batches
SET status = 'expired', updated_at = NOW()
WHERE status = 'available'
  AND expires_at < NOW()`

// ExpireSurplusBatches bulk-transitions all overdue available batches to
// 'expired'.  Intended to be called by a background scheduler.
func ExpireSurplusBatches(ctx context.Context, db DBTX) (int64, error) {
	tag, err := db.Exec(ctx, expireSurplusBatchesSQL)
	if err != nil {
		return 0, fmt.Errorf("queries.ExpireSurplusBatches: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func scanSurplusBatch(row pgx.Row) (SurplusBatch, error) {
	var b SurplusBatch
	err := row.Scan(
		&b.ID, &b.KitchenID, &b.FoodType, &b.QuantityKg,
		&b.ExpiresAt, &b.PickupAddress,
		&b.Status, &b.SafetyStatus,
		&b.ApprovedBy, &b.ApprovedAt,
		&b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		return SurplusBatch{}, fmt.Errorf("queries.scanSurplusBatch: %w", err)
	}
	return b, nil
}

func collectSurplusBatches(rows pgx.Rows) ([]SurplusBatch, error) {
	var batches []SurplusBatch
	for rows.Next() {
		var b SurplusBatch
		if err := rows.Scan(
			&b.ID, &b.KitchenID, &b.FoodType, &b.QuantityKg,
			&b.ExpiresAt, &b.PickupAddress,
			&b.Status, &b.SafetyStatus,
			&b.ApprovedBy, &b.ApprovedAt,
			&b.CreatedAt, &b.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("queries.collectSurplusBatches: %w", err)
		}
		batches = append(batches, b)
	}
	return batches, rows.Err()
}
