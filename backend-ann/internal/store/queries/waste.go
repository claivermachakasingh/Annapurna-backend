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

// Waste mirrors the waste_records table.
type Waste struct {
	ID         uuid.UUID
	KitchenID  uuid.UUID
	FoodType   string
	QuantityKg float64
	WasteType  string // preparation | plate | spoilage | other
	RecordedAt time.Time
	CreatedAt  time.Time
}

// WasteAnalyticsRow is the result of the waste analytics aggregation query.
type WasteAnalyticsRow struct {
	FoodType    string
	WasteType   string
	TotalKg     float64
	RecordCount int64
}

// CreateWasteParams holds insert fields.
type CreateWasteParams struct {
	ID         uuid.UUID
	KitchenID  uuid.UUID
	FoodType   string
	QuantityKg float64
	WasteType  string
	RecordedAt time.Time
}

// ────────────────────────────────────────────────────────────────────────────
// Queries
// ────────────────────────────────────────────────────────────────────────────

const createWasteSQL = `
INSERT INTO waste_records
    (id, kitchen_id, food_type, quantity_kg, waste_type, recorded_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, NOW())
RETURNING id, kitchen_id, food_type, quantity_kg, waste_type, recorded_at, created_at`

// CreateWaste inserts a new waste record.
func CreateWaste(ctx context.Context, db DBTX, p CreateWasteParams) (Waste, error) {
	row := db.QueryRow(ctx, createWasteSQL,
		p.ID, p.KitchenID, p.FoodType,
		p.QuantityKg, p.WasteType, p.RecordedAt,
	)
	return scanWaste(row)
}

const listWasteByKitchenSQL = `
SELECT id, kitchen_id, food_type, quantity_kg, waste_type, recorded_at, created_at
FROM waste_records
WHERE kitchen_id = $1
  AND ($2::uuid IS NULL OR id > $2)
ORDER BY id ASC
LIMIT $3`

// ListWasteByKitchen returns cursor-paginated waste records for a kitchen.
func ListWasteByKitchen(ctx context.Context, db DBTX, kitchenID, afterID uuid.UUID, limit int) ([]Waste, error) {
	var afterArg interface{} = afterID
	if afterID == uuid.Nil {
		afterArg = nil
	}
	rows, err := db.Query(ctx, listWasteByKitchenSQL, kitchenID, afterArg, limit)
	if err != nil {
		return nil, fmt.Errorf("queries.ListWasteByKitchen: %w", err)
	}
	defer rows.Close()
	return collectWaste(rows)
}

const wasteAnalyticsSQL = `
SELECT food_type, waste_type,
       SUM(quantity_kg)  AS total_kg,
       COUNT(*)          AS record_count
FROM waste_records
WHERE kitchen_id = $1
  AND recorded_at BETWEEN $2 AND $3
GROUP BY food_type, waste_type
ORDER BY food_type, waste_type`

// WasteAnalytics aggregates waste by food_type and waste_type for a kitchen
// within a time window.
func WasteAnalytics(ctx context.Context, db DBTX, kitchenID uuid.UUID, from, to time.Time) ([]WasteAnalyticsRow, error) {
	rows, err := db.Query(ctx, wasteAnalyticsSQL, kitchenID, from, to)
	if err != nil {
		return nil, fmt.Errorf("queries.WasteAnalytics: %w", err)
	}
	defer rows.Close()

	var results []WasteAnalyticsRow
	for rows.Next() {
		var r WasteAnalyticsRow
		if err := rows.Scan(&r.FoodType, &r.WasteType, &r.TotalKg, &r.RecordCount); err != nil {
			return nil, fmt.Errorf("queries.WasteAnalytics scan: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func scanWaste(row pgx.Row) (Waste, error) {
	var w Waste
	err := row.Scan(
		&w.ID, &w.KitchenID, &w.FoodType,
		&w.QuantityKg, &w.WasteType,
		&w.RecordedAt, &w.CreatedAt,
	)
	if err != nil {
		return Waste{}, fmt.Errorf("queries.scanWaste: %w", err)
	}
	return w, nil
}

func collectWaste(rows pgx.Rows) ([]Waste, error) {
	var ws []Waste
	for rows.Next() {
		var w Waste
		if err := rows.Scan(
			&w.ID, &w.KitchenID, &w.FoodType,
			&w.QuantityKg, &w.WasteType,
			&w.RecordedAt, &w.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("queries.collectWaste: %w", err)
		}
		ws = append(ws, w)
	}
	return ws, rows.Err()
}
