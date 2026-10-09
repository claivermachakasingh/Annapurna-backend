package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sih26234/food-waste/internal/qrchain"
)

type QRService struct {
	pool    *pgxpool.Pool
	qrchain *qrchain.Service
}

func NewQRService(pool *pgxpool.Pool, chain *qrchain.Service) *QRService {
	return &QRService{pool: pool, qrchain: chain}
}

func (s *QRService) ResolveBatchID(ctx context.Context, input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", &NotFoundError{Resource: "surplus_batch", ID: ""}
	}

	var realID string
	// 1. Check surplus_batches directly (id or batch_code)
	err := s.pool.QueryRow(ctx,
		`SELECT id::text FROM surplus_batches WHERE id::text = $1 OR batch_code = $1 LIMIT 1`, input).Scan(&realID)
	if err == nil {
		return realID, nil
	}

	// 2. Check matches table (match_id)
	err = s.pool.QueryRow(ctx,
		`SELECT batch_id::text FROM matches WHERE id::text = $1 LIMIT 1`, input).Scan(&realID)
	if err == nil {
		return realID, nil
	}

	// 3. Check routes table (route_id)
	err = s.pool.QueryRow(ctx,
		`SELECT batch_id::text FROM routes WHERE id::text = $1 LIMIT 1`, input).Scan(&realID)
	if err == nil {
		return realID, nil
	}

	return "", &NotFoundError{Resource: "surplus_batch", ID: input}
}

func (s *QRService) RecordEvent(
	ctx context.Context,
	batchID, actorID, actorRole, eventType string,
	lat, lng *float64,
	evidenceHash, clientEventID *string,
	clientTS *time.Time,
) (*qrchain.EventResult, error) {
	if s.qrchain == nil {
		return nil, fmt.Errorf("qrchain service not initialized")
	}
	
	if realID, err := s.ResolveBatchID(ctx, batchID); err == nil {
		batchID = realID
	}
	
	res, err := s.qrchain.RecordEvent(ctx, batchID, actorID, actorRole, eventType, lat, lng, evidenceHash, clientEventID, clientTS)
	if err != nil {
		return nil, err
	}
	
	// Transition status based on the scan
	if eventType == "PICKED_UP" {
		_, _ = s.pool.Exec(ctx, `UPDATE surplus_batches SET status = 'IN_TRANSIT', updated_at = NOW() WHERE id = $1`, batchID)
		_, _ = s.pool.Exec(ctx, `UPDATE routes SET status = 'in_transit', updated_at = NOW() WHERE batch_id = $1`, batchID)
	} else if eventType == "HANDED_OFF" || eventType == "RECEIVED" {
		_, _ = s.pool.Exec(ctx, `UPDATE surplus_batches SET status = 'DELIVERED', updated_at = NOW() WHERE id = $1`, batchID)
		_, _ = s.pool.Exec(ctx, `UPDATE routes SET status = 'completed', updated_at = NOW() WHERE batch_id = $1`, batchID)
	}
	
	return res, nil
}

func (s *QRService) GetTimeline(ctx context.Context, batchID string) ([]qrchain.QREvent, error) {
	realID, err := s.ResolveBatchID(ctx, batchID)
	if err != nil {
		return nil, err
	}
	batchID = realID

	// actor_id is a nullable uuid and `hash` is a legacy column that RecordEvent
	// does not populate (the live chain hash lives in event_hash), so both are
	// COALESCEd rather than scanned into non-nullable Go strings.
	const q = `
		SELECT id, batch_id, event_type, COALESCE(actor_id::text, ''), lat, lng,
		       evidence_hash, prev_hash, COALESCE(hash, ''), event_hash,
		       server_ts, client_ts, created_at
		FROM qr_events WHERE batch_id = $1 ORDER BY created_at ASC, id ASC`

	rows, err := s.pool.Query(ctx, q, batchID)
	if err != nil {
		return nil, fmt.Errorf("query qr events: %w", err)
	}
	defer rows.Close()

	var events []qrchain.QREvent
	for rows.Next() {
		var (
			e        qrchain.QREvent
			serverTS time.Time
		)
		if err := rows.Scan(&e.ID, &e.BatchID, &e.EventType, &e.ActorID, &e.Lat, &e.Lng,
			&e.EvidenceHash, &e.PrevHash, &e.Hash, &e.EventHash, &serverTS,
			&e.ClientTS, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan qr event: %w", err)
		}
		e.ServerTSIso = serverTS.UTC().Format(time.RFC3339)
		events = append(events, e)
	}
	return events, rows.Err()
}

// BatchExists reports whether the batch exists, so timeline reads can answer
// 404 for an unknown id instead of an empty (and misleading) 200.
func (s *QRService) BatchExists(ctx context.Context, batchID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM surplus_batches WHERE id::text = $1 OR batch_code = $1)`, batchID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check batch exists: %w", err)
	}
	return exists, nil
}

func (s *QRService) VerifyChain(ctx context.Context, batchID string) (bool, *int, error) {
	events, err := s.GetTimeline(ctx, batchID)
	if err != nil {
		return false, nil, err
	}
	if len(events) == 0 {
		return true, nil, nil
	}

	valid, brokenAt := qrchain.VerifyChain(events)
	if !valid {
		return false, &brokenAt, nil
	}
	return true, nil, nil
}
