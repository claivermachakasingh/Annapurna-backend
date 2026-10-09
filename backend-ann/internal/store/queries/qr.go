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

// QREvent mirrors the qr_events table.
type QREvent struct {
	ID           uuid.UUID
	BatchID      uuid.UUID
	EventType    string // created | picked_up | delivered | safety_check | rejected
	ActorID      uuid.UUID
	PrevHash     string
	EventHash    string
	EvidenceHash *string
	ServerTS     time.Time
	CreatedAt    time.Time
}

// CreateQREventParams holds insert fields.
type CreateQREventParams struct {
	ID           uuid.UUID
	BatchID      uuid.UUID
	EventType    string
	ActorID      uuid.UUID
	PrevHash     string
	EventHash    string
	EvidenceHash *string
	ServerTS     time.Time
}

// ────────────────────────────────────────────────────────────────────────────
// Queries
// ────────────────────────────────────────────────────────────────────────────

const createQREventSQL = `
INSERT INTO qr_events
    (id, batch_id, event_type, actor_id, prev_hash, event_hash, evidence_hash,
     server_ts, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
RETURNING id, batch_id, event_type, actor_id, prev_hash, event_hash,
          evidence_hash, server_ts, created_at`

// CreateQREvent inserts a new QR chain event.
func CreateQREvent(ctx context.Context, db DBTX, p CreateQREventParams) (QREvent, error) {
	row := db.QueryRow(ctx, createQREventSQL,
		p.ID, p.BatchID, p.EventType, p.ActorID,
		p.PrevHash, p.EventHash, p.EvidenceHash, p.ServerTS,
	)
	return scanQREvent(row)
}

const listQREventsSQL = `
SELECT id, batch_id, event_type, actor_id, prev_hash, event_hash,
       evidence_hash, server_ts, created_at
FROM qr_events
WHERE ($1::uuid IS NULL OR batch_id = $1)
  AND ($2::uuid IS NULL OR id > $2)
ORDER BY created_at ASC
LIMIT $3`

// ListQREventsParams parameterises the paginated list.
type ListQREventsParams struct {
	BatchID *uuid.UUID
	AfterID uuid.UUID // uuid.Nil = start from beginning
	Limit   int
}

// ListQREvents returns QR events optionally filtered by batch, ordered by
// created_at ascending.
func ListQREvents(ctx context.Context, db DBTX, p ListQREventsParams) ([]QREvent, error) {
	var afterArg interface{} = p.AfterID
	if p.AfterID == uuid.Nil {
		afterArg = nil
	}
	rows, err := db.Query(ctx, listQREventsSQL, p.BatchID, afterArg, p.Limit)
	if err != nil {
		return nil, fmt.Errorf("queries.ListQREvents: %w", err)
	}
	defer rows.Close()
	return collectQREvents(rows)
}

const getQREventsForBatchSQL = `
SELECT id, batch_id, event_type, actor_id, prev_hash, event_hash,
       evidence_hash, server_ts, created_at
FROM qr_events
WHERE batch_id = $1
ORDER BY created_at ASC`

// GetQREventsForBatch returns all events for a batch ordered by created_at.
// The full ordered chain is required for hash verification.
func GetQREventsForBatch(ctx context.Context, db DBTX, batchID uuid.UUID) ([]QREvent, error) {
	rows, err := db.Query(ctx, getQREventsForBatchSQL, batchID)
	if err != nil {
		return nil, fmt.Errorf("queries.GetQREventsForBatch: %w", err)
	}
	defer rows.Close()
	return collectQREvents(rows)
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func scanQREvent(row pgx.Row) (QREvent, error) {
	var e QREvent
	err := row.Scan(
		&e.ID, &e.BatchID, &e.EventType, &e.ActorID,
		&e.PrevHash, &e.EventHash, &e.EvidenceHash,
		&e.ServerTS, &e.CreatedAt,
	)
	if err != nil {
		return QREvent{}, fmt.Errorf("queries.scanQREvent: %w", err)
	}
	return e, nil
}

func collectQREvents(rows pgx.Rows) ([]QREvent, error) {
	var events []QREvent
	for rows.Next() {
		var e QREvent
		if err := rows.Scan(
			&e.ID, &e.BatchID, &e.EventType, &e.ActorID,
			&e.PrevHash, &e.EventHash, &e.EvidenceHash,
			&e.ServerTS, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("queries.collectQREvents: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
