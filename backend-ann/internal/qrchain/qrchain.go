// Package qrchain implements the canonical SHA-256 hash chain for QR code
// events in the SIH26234 food-waste redistribution platform.
package qrchain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GenesisHash is the prevHash value used for the very first event in any
// batch's QR chain.
const GenesisHash = "GENESIS"

// QREvent is the view of a QR chain event required for verification and timeline display.
type QREvent struct {
	ID           string     `json:"id,omitempty"`
	BatchID      string     `json:"batch_id"`
	EventType    string     `json:"event_type"`
	ActorID      string     `json:"actor_id"`
	Lat          *float64   `json:"lat,omitempty"`
	Lng          *float64   `json:"lng,omitempty"`
	EvidenceHash *string    `json:"evidence_hash,omitempty"`
	PrevHash     string     `json:"prev_hash"`
	Hash         string     `json:"hash,omitempty"`
	EventHash    string     `json:"event_hash,omitempty"`
	ServerTSIso  string     `json:"server_ts_iso,omitempty"`
	ClientTS     *time.Time `json:"client_ts,omitempty"`
	CreatedAt    time.Time  `json:"created_at,omitempty"`
}

// EventResult is returned when an event is successfully recorded.
type EventResult struct {
	ID        string    `json:"id"`
	BatchID   string    `json:"batch_id"`
	EventType string    `json:"event_type"`
	ActorID   string    `json:"actor_id"`
	PrevHash  string    `json:"prev_hash"`
	Hash      string    `json:"hash"`
	Timestamp string    `json:"timestamp"`
	ServerTS  string    `json:"server_ts"`
	CreatedAt time.Time `json:"created_at"`
}

// Service provides database-backed QR chain operations.
type Service struct {
	pool *pgxpool.Pool
}

// NewService constructs a new Service.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// RecordEvent appends a new event to the QR chain for batchID.
func (s *Service) RecordEvent(
	ctx context.Context,
	batchID, actorID, actorRole, eventType string,
	lat, lng *float64,
	evidenceHash, clientEventID *string,
	clientTS *time.Time,
) (*EventResult, error) {
	now := time.Now().UTC()
	nowISO := now.Format(time.RFC3339)
	evHash := ""
	if evidenceHash != nil {
		evHash = *evidenceHash
	}

	if s.pool == nil {
		computedHash := Hash(GenesisHash, batchID, eventType, actorID, nowISO, evHash)
		return &EventResult{
			ID:        uuid.NewString(),
			BatchID:   batchID,
			EventType: eventType,
			ActorID:   actorID,
			PrevHash:  GenesisHash,
			Hash:      computedHash,
			Timestamp: nowISO,
			ServerTS:  nowISO,
			CreatedAt: now,
		}, nil
	}

	var prevHash string
	err := s.pool.QueryRow(ctx,
		`SELECT event_hash FROM qr_events WHERE batch_id = $1 ORDER BY created_at DESC LIMIT 1`,
		batchID,
	).Scan(&prevHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			prevHash = GenesisHash
		} else {
			return nil, fmt.Errorf("fetch prev hash: %w", err)
		}
	}

	computedHash := Hash(prevHash, batchID, eventType, actorID, nowISO, evHash)
	eventID := uuid.NewString()

	// client_event_id / client_ts are provenance only and are never part of the
	// hash input (D24), but they MUST be persisted: the unique index on
	// client_event_id is what makes an offline replay idempotent when the
	// Redis dedupe cache is cold or unavailable.
	_, err = s.pool.Exec(ctx,
		`INSERT INTO qr_events (id, batch_id, event_type, actor_id, prev_hash, event_hash, evidence_hash,
		                       server_ts, client_event_id, client_ts, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $8)`,
		eventID, batchID, eventType, actorID, prevHash, computedHash, evidenceHash, now,
		clientEventID, clientTS,
	)
	if err != nil {
		// A replayed offline event carries the same client_event_id; return the
		// previously recorded event instead of failing the sync item.
		if isUniqueViolation(err) && clientEventID != nil {
			if existing, qerr := s.eventByClientID(ctx, *clientEventID); qerr == nil {
				return existing, nil
			}
		}
		return nil, fmt.Errorf("insert qr event: %w", err)
	}

	return &EventResult{
		ID:        eventID,
		BatchID:   batchID,
		EventType: eventType,
		ActorID:   actorID,
		PrevHash:  prevHash,
		Hash:      computedHash,
		Timestamp: nowISO,
		ServerTS:  nowISO,
		CreatedAt: now,
	}, nil
}

// eventByClientID loads an already-recorded event for an offline replay.
func (s *Service) eventByClientID(ctx context.Context, clientEventID string) (*EventResult, error) {
	var e EventResult
	err := s.pool.QueryRow(ctx,
		`SELECT id, batch_id, event_type, actor_id, prev_hash, event_hash, created_at
		 FROM qr_events WHERE client_event_id = $1 LIMIT 1`,
		clientEventID,
	).Scan(&e.ID, &e.BatchID, &e.EventType, &e.ActorID, &e.PrevHash, &e.Hash, &e.CreatedAt)
	if err != nil {
		return nil, err
	}
	e.Timestamp = e.CreatedAt.UTC().Format(time.RFC3339)
	e.ServerTS = e.Timestamp
	return &e, nil
}

// isUniqueViolation reports whether the error is a PostgreSQL unique-violation
// (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// Hash computes the canonical event hash.
func Hash(prevHash, batchID, eventType, actorID, serverTSiso, evidenceHash string) string {
	h := sha256.New()
	_, _ = fmt.Fprint(h, prevHash)
	_, _ = fmt.Fprint(h, batchID)
	_, _ = fmt.Fprint(h, eventType)
	_, _ = fmt.Fprint(h, actorID)
	_, _ = fmt.Fprint(h, serverTSiso)
	_, _ = fmt.Fprint(h, evidenceHash)
	return hex.EncodeToString(h.Sum(nil))
}

// ComputeHash is an alias for Hash for compatibility with cmd/seedcheck.
func ComputeHash(prevHash, batchID, eventType, actorID, serverTSiso, evidenceHash string) string {
	return Hash(prevHash, batchID, eventType, actorID, serverTSiso, evidenceHash)
}

// EvidenceHash computes the SHA-256 hash of a serialized quality-check JSON.
func EvidenceHash(qualityCheckJSON []byte) string {
	if len(qualityCheckJSON) == 0 {
		return ""
	}
	sum := sha256.Sum256(qualityCheckJSON)
	return hex.EncodeToString(sum[:])
}

// VerifyChain walks events and verifies each hash link.
func VerifyChain(events []QREvent) (valid bool, brokenAt int) {
	for i, e := range events {
		expectedHash := e.Hash
		if expectedHash == "" {
			expectedHash = e.EventHash
		}
		ts := e.ServerTSIso
		if ts == "" && !e.CreatedAt.IsZero() {
			ts = e.CreatedAt.Format(time.RFC3339)
		}
		ev := ""
		if e.EvidenceHash != nil {
			ev = *e.EvidenceHash
		}

		computed := Hash(e.PrevHash, e.BatchID, e.EventType, e.ActorID, ts, ev)
		if expectedHash != "" && computed != expectedHash {
			return false, i
		}

		if i > 0 {
			prevExpected := events[i-1].Hash
			if prevExpected == "" {
				prevExpected = events[i-1].EventHash
			}
			if e.PrevHash != prevExpected {
				return false, i
			}
		}
	}
	return true, -1
}
