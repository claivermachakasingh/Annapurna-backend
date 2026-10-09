package queries

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ────────────────────────────────────────────────────────────────────────────
// Domain types — kitchen overview, meals, attendance, production
// ────────────────────────────────────────────────────────────────────────────

// KitchenOverview aggregates dashboard metrics for a kitchen.
type KitchenOverview struct {
	KitchenID        uuid.UUID
	TotalSurplusKg   float64
	TotalWasteKg     float64
	OpenBatches      int64
	MatchedBatches   int64
	CollectedBatches int64
}

// Meal mirrors the meals table.
type Meal struct {
	ID          uuid.UUID
	KitchenID   uuid.UUID
	Name        string
	Description *string
	CreatedAt   time.Time
}

// Attendance mirrors the attendance table.
type Attendance struct {
	ID        uuid.UUID
	KitchenID uuid.UUID
	MealDate  time.Time
	HeadCount int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Production mirrors the production table.
type Production struct {
	ID             uuid.UUID
	KitchenID      uuid.UUID
	MealID         uuid.UUID
	ProductionDate time.Time
	QuantityKg     float64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Recipient holds basic info for an NGO/recipient.
type Recipient struct {
	ID        uuid.UUID
	OrgName   string
	Email     string
	IsActive  bool
	CreatedAt time.Time
}

// Route mirrors the delivery_routes table.
type Route struct {
	ID        uuid.UUID
	DriverID  uuid.UUID
	Status    string // planned | in_progress | completed | cancelled
	CreatedAt time.Time
	UpdatedAt time.Time
}

// RouteStop mirrors route_stops.
type RouteStop struct {
	ID       uuid.UUID
	RouteID  uuid.UUID
	BatchID  uuid.UUID
	Sequence int
	Address  string
}

// ProcessingMetrics mirrors the processing_metrics table.
type ProcessingMetrics struct {
	ID              uuid.UUID
	KitchenID       uuid.UUID
	PeriodStart     time.Time
	PeriodEnd       time.Time
	RawMaterialKg   float64
	OutputKg        float64
	WasteKg         float64
	DowntimeMin     float64
	RuntimeMin      float64
	EnergyKwh       float64
	MaterialLossPct float64
	DowntimePct     float64
	EnergyPerKg     float64
	EfficiencyScore float64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Alert mirrors the alerts table.
type Alert struct {
	ID        uuid.UUID
	KitchenID uuid.UUID
	AlertType string
	Severity  string // info | warning | critical
	Message   string
	IsAcked   bool
	AckedBy   *uuid.UUID
	AckedAt   *time.Time
	CreatedAt time.Time
}

// AuditLog mirrors the audit_logs table.
type AuditLog struct {
	ID           uuid.UUID
	ActorID      uuid.UUID
	Action       string
	ResourceType string
	ResourceID   uuid.UUID
	Metadata     map[string]any
	CreatedAt    time.Time
}

// Diversion mirrors the diversions table.
type Diversion struct {
	ID         uuid.UUID
	BatchID    uuid.UUID
	FromStatus string
	ToStatus   string
	Reason     string
	ActorID    uuid.UUID
	CreatedAt  time.Time
}

// SensorReading mirrors the sensor_readings table.
type SensorReading struct {
	ID         uuid.UUID
	DeviceID   string
	KitchenID  uuid.UUID
	SensorType string // temperature | humidity | co2
	Value      float64
	Unit       string
	RecordedAt time.Time
	CreatedAt  time.Time
}

// PredictionLog mirrors the prediction_logs table.
type PredictionLog struct {
	ID           uuid.UUID
	KitchenID    uuid.UUID
	ModelVersion string
	PredictedKg  float64
	ActualKg     *float64
	PredictedFor time.Time
	CreatedAt    time.Time
}

// OutcomeFeedback mirrors the outcome_feedback table.
type OutcomeFeedback struct {
	ID          uuid.UUID
	MatchID     uuid.UUID
	RecipientID uuid.UUID
	Rating      int
	Comment     *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ────────────────────────────────────────────────────────────────────────────
// Params structs
// ────────────────────────────────────────────────────────────────────────────

// CreateMealParams holds insert fields for meals.
type CreateMealParams struct {
	ID          uuid.UUID
	KitchenID   uuid.UUID
	Name        string
	Description *string
}

// UpsertAttendanceParams holds upsert fields for attendance.
type UpsertAttendanceParams struct {
	ID        uuid.UUID
	KitchenID uuid.UUID
	MealDate  time.Time
	HeadCount int
}

// UpsertProductionParams holds upsert fields for production.
type UpsertProductionParams struct {
	ID             uuid.UUID
	KitchenID      uuid.UUID
	MealID         uuid.UUID
	ProductionDate time.Time
	QuantityKg     float64
}

// CreateRouteParams holds insert fields for a delivery route.
type CreateRouteParams struct {
	ID       uuid.UUID
	DriverID uuid.UUID
}

// CreateRouteStopsParams holds insert data for a single stop.
type CreateRouteStopsParams struct {
	ID       uuid.UUID
	RouteID  uuid.UUID
	BatchID  uuid.UUID
	Sequence int
	Address  string
}

// UpsertProcessingMetricsParams holds all raw + computed fields.
type UpsertProcessingMetricsParams struct {
	ID              uuid.UUID
	KitchenID       uuid.UUID
	PeriodStart     time.Time
	PeriodEnd       time.Time
	RawMaterialKg   float64
	OutputKg        float64
	WasteKg         float64
	DowntimeMin     float64
	RuntimeMin      float64
	EnergyKwh       float64
	MaterialLossPct float64
	DowntimePct     float64
	EnergyPerKg     float64
	EfficiencyScore float64
}

// CreateAlertParams holds insert fields for an alert.
type CreateAlertParams struct {
	ID        uuid.UUID
	KitchenID uuid.UUID
	AlertType string
	Severity  string
	Message   string
}

// CreateAuditLogParams holds insert fields for an audit event.
type CreateAuditLogParams struct {
	ID           uuid.UUID
	ActorID      uuid.UUID
	Action       string
	ResourceType string
	ResourceID   uuid.UUID
	Metadata     map[string]any
}

// CreateDiversionParams holds insert fields for a diversion event.
type CreateDiversionParams struct {
	ID         uuid.UUID
	BatchID    uuid.UUID
	FromStatus string
	ToStatus   string
	Reason     string
	ActorID    uuid.UUID
}

// UpsertSensorReadingParams holds insert fields for a sensor reading.
type UpsertSensorReadingParams struct {
	ID         uuid.UUID
	DeviceID   string
	KitchenID  uuid.UUID
	SensorType string
	Value      float64
	Unit       string
	RecordedAt time.Time
}

// CreatePredictionLogParams holds insert fields for a prediction log.
type CreatePredictionLogParams struct {
	ID           uuid.UUID
	KitchenID    uuid.UUID
	ModelVersion string
	PredictedKg  float64
	PredictedFor time.Time
}

// UpsertOutcomeFeedbackParams holds upsert fields for outcome feedback.
type UpsertOutcomeFeedbackParams struct {
	ID          uuid.UUID
	MatchID     uuid.UUID
	RecipientID uuid.UUID
	Rating      int
	Comment     *string
}

// ────────────────────────────────────────────────────────────────────────────
// Queries — Kitchen Overview
// ────────────────────────────────────────────────────────────────────────────

const getKitchenOverviewSQL = `
SELECT
    $1::uuid                                                          AS kitchen_id,
    COALESCE(SUM(sb.quantity_kg) FILTER (WHERE sb.status != 'expired'), 0) AS total_surplus_kg,
    COALESCE(SUM(wr.quantity_kg), 0)                                  AS total_waste_kg,
    COUNT(sb.id)    FILTER (WHERE sb.status = 'available')            AS open_batches,
    COUNT(sb.id)    FILTER (WHERE sb.status = 'matched')              AS matched_batches,
    COUNT(sb.id)    FILTER (WHERE sb.status = 'collected')            AS collected_batches
FROM surplus_batches sb
FULL OUTER JOIN waste_records wr ON wr.kitchen_id = $1
WHERE sb.kitchen_id = $1 OR wr.kitchen_id = $1`

// GetKitchenOverview returns aggregated dashboard metrics for a kitchen.
func GetKitchenOverview(ctx context.Context, db DBTX, kitchenID uuid.UUID) (KitchenOverview, error) {
	row := db.QueryRow(ctx, getKitchenOverviewSQL, kitchenID)
	var o KitchenOverview
	err := row.Scan(
		&o.KitchenID,
		&o.TotalSurplusKg,
		&o.TotalWasteKg,
		&o.OpenBatches,
		&o.MatchedBatches,
		&o.CollectedBatches,
	)
	if err != nil {
		return KitchenOverview{}, fmt.Errorf("queries.GetKitchenOverview: %w", err)
	}
	return o, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Queries — Meals
// ────────────────────────────────────────────────────────────────────────────

const createMealSQL = `
INSERT INTO meals (id, kitchen_id, name, description, created_at)
VALUES ($1, $2, $3, $4, NOW())
RETURNING id, kitchen_id, name, description, created_at`

// CreateMeal inserts a new meal definition.
func CreateMeal(ctx context.Context, db DBTX, p CreateMealParams) (Meal, error) {
	row := db.QueryRow(ctx, createMealSQL,
		p.ID, p.KitchenID, p.Name, p.Description,
	)
	var m Meal
	if err := row.Scan(&m.ID, &m.KitchenID, &m.Name, &m.Description, &m.CreatedAt); err != nil {
		return Meal{}, fmt.Errorf("queries.CreateMeal: %w", err)
	}
	return m, nil
}

const getOrCreateMealSQL = `
WITH existing AS (
    SELECT id, kitchen_id, name, description, created_at
    FROM meals
    WHERE kitchen_id = $2 AND name = $3
    LIMIT 1
),
inserted AS (
    INSERT INTO meals (id, kitchen_id, name, description, created_at)
    SELECT $1, $2, $3, $4, NOW()
    WHERE NOT EXISTS (SELECT 1 FROM existing)
    RETURNING id, kitchen_id, name, description, created_at
)
SELECT * FROM existing
UNION ALL
SELECT * FROM inserted`

// GetOrCreateMeal returns an existing meal (by kitchen+name) or inserts one.
func GetOrCreateMeal(ctx context.Context, db DBTX, p CreateMealParams) (Meal, error) {
	row := db.QueryRow(ctx, getOrCreateMealSQL,
		p.ID, p.KitchenID, p.Name, p.Description,
	)
	var m Meal
	if err := row.Scan(&m.ID, &m.KitchenID, &m.Name, &m.Description, &m.CreatedAt); err != nil {
		return Meal{}, fmt.Errorf("queries.GetOrCreateMeal: %w", err)
	}
	return m, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Queries — Attendance
// ────────────────────────────────────────────────────────────────────────────

const upsertAttendanceSQL = `
INSERT INTO attendance (id, kitchen_id, meal_date, head_count, created_at, updated_at)
VALUES ($1, $2, $3, $4, NOW(), NOW())
ON CONFLICT (kitchen_id, meal_date) DO UPDATE
    SET head_count = EXCLUDED.head_count,
        updated_at = NOW()
RETURNING id, kitchen_id, meal_date, head_count, created_at, updated_at`

// UpsertAttendance inserts or updates an attendance record.
func UpsertAttendance(ctx context.Context, db DBTX, p UpsertAttendanceParams) (Attendance, error) {
	row := db.QueryRow(ctx, upsertAttendanceSQL,
		p.ID, p.KitchenID, p.MealDate, p.HeadCount,
	)
	var a Attendance
	if err := row.Scan(
		&a.ID, &a.KitchenID, &a.MealDate,
		&a.HeadCount, &a.CreatedAt, &a.UpdatedAt,
	); err != nil {
		return Attendance{}, fmt.Errorf("queries.UpsertAttendance: %w", err)
	}
	return a, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Queries — Production
// ────────────────────────────────────────────────────────────────────────────

const upsertProductionSQL = `
INSERT INTO production (id, kitchen_id, meal_id, production_date, quantity_kg, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
ON CONFLICT (kitchen_id, meal_id, production_date) DO UPDATE
    SET quantity_kg = EXCLUDED.quantity_kg,
        updated_at  = NOW()
RETURNING id, kitchen_id, meal_id, production_date, quantity_kg, created_at, updated_at`

// UpsertProduction inserts or updates a production record.
func UpsertProduction(ctx context.Context, db DBTX, p UpsertProductionParams) (Production, error) {
	row := db.QueryRow(ctx, upsertProductionSQL,
		p.ID, p.KitchenID, p.MealID,
		p.ProductionDate, p.QuantityKg,
	)
	var pr Production
	if err := row.Scan(
		&pr.ID, &pr.KitchenID, &pr.MealID,
		&pr.ProductionDate, &pr.QuantityKg,
		&pr.CreatedAt, &pr.UpdatedAt,
	); err != nil {
		return Production{}, fmt.Errorf("queries.UpsertProduction: %w", err)
	}
	return pr, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Queries — Recipients
// ────────────────────────────────────────────────────────────────────────────

const listRecipientsSQL = `
SELECT u.id, o.name AS org_name, u.email, u.is_active, u.created_at
FROM users u
JOIN organisations o ON o.id = u.org_id
WHERE u.role = 'ngo'
  AND u.is_active = TRUE
  AND ($1::uuid IS NULL OR u.id > $1)
ORDER BY u.id ASC
LIMIT $2`

// ListRecipients returns cursor-paginated active NGO/recipient users.
func ListRecipients(ctx context.Context, db DBTX, afterID uuid.UUID, limit int) ([]Recipient, error) {
	var afterArg interface{} = afterID
	if afterID == uuid.Nil {
		afterArg = nil
	}
	rows, err := db.Query(ctx, listRecipientsSQL, afterArg, limit)
	if err != nil {
		return nil, fmt.Errorf("queries.ListRecipients: %w", err)
	}
	defer rows.Close()

	var recipients []Recipient
	for rows.Next() {
		var r Recipient
		if err := rows.Scan(
			&r.ID, &r.OrgName, &r.Email,
			&r.IsActive, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("queries.ListRecipients scan: %w", err)
		}
		recipients = append(recipients, r)
	}
	return recipients, rows.Err()
}

// ────────────────────────────────────────────────────────────────────────────
// Queries — Delivery Routes
// ────────────────────────────────────────────────────────────────────────────

const createRouteSQL = `
INSERT INTO delivery_routes (id, driver_id, status, created_at, updated_at)
VALUES ($1, $2, 'planned', NOW(), NOW())
RETURNING id, driver_id, status, created_at, updated_at`

// CreateRoute inserts a new delivery route.
func CreateRoute(ctx context.Context, db DBTX, p CreateRouteParams) (Route, error) {
	row := db.QueryRow(ctx, createRouteSQL, p.ID, p.DriverID)
	var r Route
	if err := row.Scan(&r.ID, &r.DriverID, &r.Status, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return Route{}, fmt.Errorf("queries.CreateRoute: %w", err)
	}
	return r, nil
}

const createRouteStopSQL = `
INSERT INTO route_stops (id, route_id, batch_id, sequence, address)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, route_id, batch_id, sequence, address`

// CreateRouteStops inserts multiple route stops in sequence.
func CreateRouteStops(ctx context.Context, db DBTX, stops []CreateRouteStopsParams) ([]RouteStop, error) {
	results := make([]RouteStop, 0, len(stops))
	for _, s := range stops {
		row := db.QueryRow(ctx, createRouteStopSQL,
			s.ID, s.RouteID, s.BatchID, s.Sequence, s.Address,
		)
		var rs RouteStop
		if err := row.Scan(&rs.ID, &rs.RouteID, &rs.BatchID, &rs.Sequence, &rs.Address); err != nil {
			return nil, fmt.Errorf("queries.CreateRouteStops: %w", err)
		}
		results = append(results, rs)
	}
	return results, nil
}

const getRouteSQL = `
SELECT r.id, r.driver_id, r.status, r.created_at, r.updated_at
FROM delivery_routes r
WHERE r.id = $1
LIMIT 1`

// GetRoute returns a delivery route by ID.
func GetRoute(ctx context.Context, db DBTX, id uuid.UUID) (Route, error) {
	row := db.QueryRow(ctx, getRouteSQL, id)
	var r Route
	if err := row.Scan(&r.ID, &r.DriverID, &r.Status, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return Route{}, fmt.Errorf("queries.GetRoute: %w", err)
	}
	return r, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Queries — Processing Metrics
// ────────────────────────────────────────────────────────────────────────────

const upsertProcessingMetricsSQL = `
INSERT INTO processing_metrics
    (id, kitchen_id, period_start, period_end,
     raw_material_kg, output_kg, waste_kg,
     downtime_min, runtime_min, energy_kwh,
     material_loss_pct, downtime_pct, energy_per_kg, efficiency_score,
     created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NOW(), NOW())
ON CONFLICT (kitchen_id, period_start, period_end) DO UPDATE
    SET raw_material_kg   = EXCLUDED.raw_material_kg,
        output_kg         = EXCLUDED.output_kg,
        waste_kg          = EXCLUDED.waste_kg,
        downtime_min      = EXCLUDED.downtime_min,
        runtime_min       = EXCLUDED.runtime_min,
        energy_kwh        = EXCLUDED.energy_kwh,
        material_loss_pct = EXCLUDED.material_loss_pct,
        downtime_pct      = EXCLUDED.downtime_pct,
        energy_per_kg     = EXCLUDED.energy_per_kg,
        efficiency_score  = EXCLUDED.efficiency_score,
        updated_at        = NOW()
RETURNING id, kitchen_id, period_start, period_end,
          raw_material_kg, output_kg, waste_kg,
          downtime_min, runtime_min, energy_kwh,
          material_loss_pct, downtime_pct, energy_per_kg, efficiency_score,
          created_at, updated_at`

// UpsertProcessingMetrics inserts or updates processing KPIs for a period.
func UpsertProcessingMetrics(ctx context.Context, db DBTX, p UpsertProcessingMetricsParams) (ProcessingMetrics, error) {
	row := db.QueryRow(ctx, upsertProcessingMetricsSQL,
		p.ID, p.KitchenID, p.PeriodStart, p.PeriodEnd,
		p.RawMaterialKg, p.OutputKg, p.WasteKg,
		p.DowntimeMin, p.RuntimeMin, p.EnergyKwh,
		p.MaterialLossPct, p.DowntimePct, p.EnergyPerKg, p.EfficiencyScore,
	)
	return scanProcessingMetrics(row)
}

const getProcessingMetricsSQL = `
SELECT id, kitchen_id, period_start, period_end,
       raw_material_kg, output_kg, waste_kg,
       downtime_min, runtime_min, energy_kwh,
       material_loss_pct, downtime_pct, energy_per_kg, efficiency_score,
       created_at, updated_at
FROM processing_metrics
WHERE kitchen_id = $1
  AND period_start = $2
  AND period_end   = $3
LIMIT 1`

// GetProcessingMetrics retrieves KPIs for a specific period.
func GetProcessingMetrics(ctx context.Context, db DBTX, kitchenID uuid.UUID, start, end time.Time) (ProcessingMetrics, error) {
	row := db.QueryRow(ctx, getProcessingMetricsSQL, kitchenID, start, end)
	return scanProcessingMetrics(row)
}

func scanProcessingMetrics(row pgx.Row) (ProcessingMetrics, error) {
	var m ProcessingMetrics
	err := row.Scan(
		&m.ID, &m.KitchenID, &m.PeriodStart, &m.PeriodEnd,
		&m.RawMaterialKg, &m.OutputKg, &m.WasteKg,
		&m.DowntimeMin, &m.RuntimeMin, &m.EnergyKwh,
		&m.MaterialLossPct, &m.DowntimePct, &m.EnergyPerKg, &m.EfficiencyScore,
		&m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return ProcessingMetrics{}, fmt.Errorf("queries.scanProcessingMetrics: %w", err)
	}
	return m, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Queries — Alerts
// ────────────────────────────────────────────────────────────────────────────

const createAlertSQL = `
INSERT INTO alerts (id, kitchen_id, alert_type, severity, message, is_acked, created_at)
VALUES ($1, $2, $3, $4, $5, FALSE, NOW())
RETURNING id, kitchen_id, alert_type, severity, message, is_acked, acked_by, acked_at, created_at`

// CreateAlert inserts a new alert.
func CreateAlert(ctx context.Context, db DBTX, p CreateAlertParams) (Alert, error) {
	row := db.QueryRow(ctx, createAlertSQL,
		p.ID, p.KitchenID, p.AlertType, p.Severity, p.Message,
	)
	return scanAlert(row)
}

const listAlertsSQL = `
SELECT id, kitchen_id, alert_type, severity, message, is_acked, acked_by, acked_at, created_at
FROM alerts
WHERE kitchen_id = $1
  AND ($2::bool IS NULL OR is_acked = $2)
  AND ($3::uuid IS NULL OR id > $3)
ORDER BY id DESC
LIMIT $4`

// ListAlertsParams parameterises the alert list query.
type ListAlertsParams struct {
	KitchenID uuid.UUID
	IsAcked   *bool
	AfterID   uuid.UUID
	Limit     int
}

// ListAlerts returns cursor-paginated alerts for a kitchen.
func ListAlerts(ctx context.Context, db DBTX, p ListAlertsParams) ([]Alert, error) {
	var afterArg interface{} = p.AfterID
	if p.AfterID == uuid.Nil {
		afterArg = nil
	}
	rows, err := db.Query(ctx, listAlertsSQL, p.KitchenID, p.IsAcked, afterArg, p.Limit)
	if err != nil {
		return nil, fmt.Errorf("queries.ListAlerts: %w", err)
	}
	defer rows.Close()

	var alerts []Alert
	for rows.Next() {
		var a Alert
		if err := rows.Scan(
			&a.ID, &a.KitchenID, &a.AlertType,
			&a.Severity, &a.Message, &a.IsAcked,
			&a.AckedBy, &a.AckedAt, &a.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("queries.ListAlerts scan: %w", err)
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

const ackAlertSQL = `
UPDATE alerts
SET is_acked = TRUE, acked_by = $2, acked_at = NOW()
WHERE id = $1
RETURNING id, kitchen_id, alert_type, severity, message, is_acked, acked_by, acked_at, created_at`

// AckAlert marks an alert as acknowledged.
func AckAlert(ctx context.Context, db DBTX, alertID, ackerID uuid.UUID) (Alert, error) {
	row := db.QueryRow(ctx, ackAlertSQL, alertID, ackerID)
	return scanAlert(row)
}

func scanAlert(row pgx.Row) (Alert, error) {
	var a Alert
	if err := row.Scan(
		&a.ID, &a.KitchenID, &a.AlertType,
		&a.Severity, &a.Message, &a.IsAcked,
		&a.AckedBy, &a.AckedAt, &a.CreatedAt,
	); err != nil {
		return Alert{}, fmt.Errorf("queries.scanAlert: %w", err)
	}
	return a, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Queries — Audit Log
// ────────────────────────────────────────────────────────────────────────────

const createAuditLogSQL = `
INSERT INTO audit_logs
    (id, actor_id, action, resource_type, resource_id, metadata, created_at)
VALUES ($1, $2, $3, $4, $5, $6, NOW())`

// CreateAuditLog appends a new audit event.  Fire-and-forget; returns error
// only on DB failure.
func CreateAuditLog(ctx context.Context, db DBTX, p CreateAuditLogParams) error {
	_, err := db.Exec(ctx, createAuditLogSQL,
		p.ID, p.ActorID, p.Action,
		p.ResourceType, p.ResourceID, p.Metadata,
	)
	if err != nil {
		return fmt.Errorf("queries.CreateAuditLog: %w", err)
	}
	return nil
}

// ────────────────────────────────────────────────────────────────────────────
// Queries — Diversions
// ────────────────────────────────────────────────────────────────────────────

const createDiversionSQL = `
INSERT INTO diversions
    (id, batch_id, from_status, to_status, reason, actor_id, created_at)
VALUES ($1, $2, $3, $4, $5, $6, NOW())
RETURNING id, batch_id, from_status, to_status, reason, actor_id, created_at`

// CreateDiversion records a batch diversion event.
func CreateDiversion(ctx context.Context, db DBTX, p CreateDiversionParams) (Diversion, error) {
	row := db.QueryRow(ctx, createDiversionSQL,
		p.ID, p.BatchID, p.FromStatus, p.ToStatus, p.Reason, p.ActorID,
	)
	var d Diversion
	if err := row.Scan(
		&d.ID, &d.BatchID, &d.FromStatus,
		&d.ToStatus, &d.Reason, &d.ActorID, &d.CreatedAt,
	); err != nil {
		return Diversion{}, fmt.Errorf("queries.CreateDiversion: %w", err)
	}
	return d, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Queries — Sensor Readings
// ────────────────────────────────────────────────────────────────────────────

const upsertSensorReadingSQL = `
INSERT INTO sensor_readings
    (id, device_id, kitchen_id, sensor_type, value, unit, recorded_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
ON CONFLICT (device_id, recorded_at) DO UPDATE
    SET value      = EXCLUDED.value,
        unit       = EXCLUDED.unit,
        kitchen_id = EXCLUDED.kitchen_id
RETURNING id, device_id, kitchen_id, sensor_type, value, unit, recorded_at, created_at`

// UpsertSensorReading inserts or updates a sensor reading.
func UpsertSensorReading(ctx context.Context, db DBTX, p UpsertSensorReadingParams) (SensorReading, error) {
	row := db.QueryRow(ctx, upsertSensorReadingSQL,
		p.ID, p.DeviceID, p.KitchenID,
		p.SensorType, p.Value, p.Unit, p.RecordedAt,
	)
	var s SensorReading
	if err := row.Scan(
		&s.ID, &s.DeviceID, &s.KitchenID,
		&s.SensorType, &s.Value, &s.Unit,
		&s.RecordedAt, &s.CreatedAt,
	); err != nil {
		return SensorReading{}, fmt.Errorf("queries.UpsertSensorReading: %w", err)
	}
	return s, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Queries — Prediction Logs
// ────────────────────────────────────────────────────────────────────────────

const createPredictionLogSQL = `
INSERT INTO prediction_logs
    (id, kitchen_id, model_version, predicted_kg, predicted_for, created_at)
VALUES ($1, $2, $3, $4, $5, NOW())
RETURNING id, kitchen_id, model_version, predicted_kg, actual_kg, predicted_for, created_at`

// CreatePredictionLog records an ML model prediction.
func CreatePredictionLog(ctx context.Context, db DBTX, p CreatePredictionLogParams) (PredictionLog, error) {
	row := db.QueryRow(ctx, createPredictionLogSQL,
		p.ID, p.KitchenID, p.ModelVersion,
		p.PredictedKg, p.PredictedFor,
	)
	var pl PredictionLog
	if err := row.Scan(
		&pl.ID, &pl.KitchenID, &pl.ModelVersion,
		&pl.PredictedKg, &pl.ActualKg, &pl.PredictedFor, &pl.CreatedAt,
	); err != nil {
		return PredictionLog{}, fmt.Errorf("queries.CreatePredictionLog: %w", err)
	}
	return pl, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Queries — Outcome Feedback
// ────────────────────────────────────────────────────────────────────────────

const upsertOutcomeFeedbackSQL = `
INSERT INTO outcome_feedback
    (id, match_id, recipient_id, rating, comment, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
ON CONFLICT (match_id, recipient_id) DO UPDATE
    SET rating     = EXCLUDED.rating,
        comment    = EXCLUDED.comment,
        updated_at = NOW()
RETURNING id, match_id, recipient_id, rating, comment, created_at, updated_at`

// UpsertOutcomeFeedback inserts or updates feedback for a completed match.
func UpsertOutcomeFeedback(ctx context.Context, db DBTX, p UpsertOutcomeFeedbackParams) (OutcomeFeedback, error) {
	row := db.QueryRow(ctx, upsertOutcomeFeedbackSQL,
		p.ID, p.MatchID, p.RecipientID, p.Rating, p.Comment,
	)
	var f OutcomeFeedback
	if err := row.Scan(
		&f.ID, &f.MatchID, &f.RecipientID,
		&f.Rating, &f.Comment,
		&f.CreatedAt, &f.UpdatedAt,
	); err != nil {
		return OutcomeFeedback{}, fmt.Errorf("queries.UpsertOutcomeFeedback: %w", err)
	}
	return f, nil
}
