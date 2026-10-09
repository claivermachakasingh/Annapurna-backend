package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sih26234/food-waste/internal/qrchain"
)

type ApproveRequest struct {
	Decision       string  `json:"decision"`
	Note           string  `json:"note,omitempty"`
	OverrideReason *string `json:"override_reason,omitempty"`
}

type ApproveService struct {
	pool    *pgxpool.Pool
	qrchain *qrchain.Service
}

func NewApproveService(pool *pgxpool.Pool, chain *qrchain.Service) *ApproveService {
	return &ApproveService{pool: pool, qrchain: chain}
}

func (s *ApproveService) Approve(
	ctx context.Context,
	batchID, userID, role string,
	req ApproveRequest,
) error {
	var latestSafetyDecision string
	var checkID string
	err := s.pool.QueryRow(ctx,
		`SELECT id, safety_decision FROM quality_checks WHERE batch_id = $1 ORDER BY created_at DESC LIMIT 1`,
		batchID,
	).Scan(&checkID, &latestSafetyDecision)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &ConflictError{Code: "NO_QUALITY_CHECK", Message: "quality check required before approval"}
		}
		return fmt.Errorf("fetch latest quality check: %w", err)
	}

	if latestSafetyDecision == "REJECTED" {
		if role != "ADMIN" || req.OverrideReason == nil || *req.OverrideReason == "" {
			return &ConflictError{
				Code:    "SAFETY_REJECTED_CANNOT_APPROVE",
				Message: "cannot approve batch marked REJECTED by safety check; ADMIN override_reason required",
			}
		}
		auditID := uuid.NewString()
		_, _ = s.pool.Exec(ctx,
			`INSERT INTO audit_log (id, user_id, action, entity, entity_id, before, after, created_at)
			 VALUES ($1, $2, 'SAFETY_OVERRIDE', 'surplus_batch', $3, '{"decision":"REJECTED"}', $4, $5)`,
			auditID, userID, batchID, fmt.Sprintf(`{"override_reason":"%s"}`, *req.OverrideReason), time.Now().UTC(),
		)
	}

	// Decision → (batch status, safety status). The safety status vocabulary is
	// fixed by the API contract (PENDING|ELIGIBLE|HOLD|REJECTED); the raw
	// decision verb must never be written to the column.
	var newStatus SurplusStatus
	var safetyStatus string
	switch strings.ToUpper(req.Decision) {
	case "APPROVE":
		newStatus, safetyStatus = StatusAvailable, "ELIGIBLE"
	case "HOLD":
		newStatus, safetyStatus = StatusHold, "HOLD"
	case "REJECT":
		newStatus, safetyStatus = StatusDiverted, "REJECTED"
	default:
		return &ConflictError{
			Code:    "INVALID_DECISION",
			Message: fmt.Sprintf("decision must be APPROVE, HOLD or REJECT (got %q)", req.Decision),
		}
	}

	now := time.Now().UTC()
	_, err = s.pool.Exec(ctx,
		`UPDATE surplus_batches SET status=$1, safety_status=$2, approved_by=$3, approved_at=$4, updated_at=$4 WHERE id=$5`,
		string(newStatus), safetyStatus, userID, now, batchID,
	)
	if err != nil {
		return fmt.Errorf("update surplus approval status: %w", err)
	}

	if req.Decision == "APPROVE" && s.qrchain != nil {
		evidenceRaw := fmt.Sprintf("%s:%s:%s", checkID, latestSafetyDecision, now.Format(time.RFC3339))
		h := sha256.Sum256([]byte(evidenceRaw))
		evidenceHash := hex.EncodeToString(h[:])
		_, _ = s.qrchain.RecordEvent(ctx, batchID, userID, role, "APPROVED", nil, nil, &evidenceHash, nil, nil)
	}

	return nil
}
