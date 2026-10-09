package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/sih26234/food-waste/internal/mlclient"
)

type QualityCheckResult struct {
	ID                    string    `json:"id"`
	BatchID               string    `json:"batch_id"`
	VisualStatus          string    `json:"visual_status"`
	RiskLevel             string    `json:"risk_level"`
	Confidence            float64   `json:"confidence"`
	Reason                string    `json:"reason"`
	SafetyDecision        string    `json:"safety_decision"`
	DangerZoneMinutes     float64   `json:"danger_zone_minutes"`
	RequiresHumanApproval bool      `json:"requires_human_approval"`
	CreatedAt             time.Time `json:"created_at"`
}

type QualityService struct {
	pool     *pgxpool.Pool
	redis    *redis.Client
	mlclient *mlclient.MLClient
}

func NewQualityService(pool *pgxpool.Pool, rdb *redis.Client, ml *mlclient.MLClient) *QualityService {
	return &QualityService{pool: pool, redis: rdb, mlclient: ml}
}

func (s *QualityService) CheckQuality(
	ctx context.Context,
	batchID, userID string,
	imagePath string,
	temperatureC *float64,
	devicePreview *json.RawMessage,
) (*QualityCheckResult, error) {
	var batchExists bool
	err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM surplus_batches WHERE id = $1)", batchID).Scan(&batchExists)
	if err != nil || !batchExists {
		return nil, &NotFoundError{Resource: "surplus_batch", ID: batchID}
	}

	cvCtx, cvCancel := context.WithTimeout(ctx, 8*time.Second)
	defer cvCancel()

	visualStatus := "RISK"
	riskLevel := "MEDIUM"
	confidence := 0.5
	reason := "CV unavailable, manual inspection required"

	if s.mlclient != nil {
		cvResp, cvErr := s.mlclient.CvPredict(cvCtx, imagePath, batchID)
		if cvErr == nil && cvResp != nil {
			if cvResp.FreshnessScore >= 0.7 {
				visualStatus = "GOOD"
				riskLevel = "LOW"
				reason = "High freshness score"
			} else if cvResp.FreshnessScore < 0.4 {
				visualStatus = "REJECTED"
				riskLevel = "HIGH"
				reason = "Low freshness score"
			} else {
				visualStatus = "RISK"
				riskLevel = "MEDIUM"
				reason = "Moderate freshness score"
			}
			confidence = cvResp.Confidence
		}
	}

	dangerMinutes := s.getDangerMinutes(ctx, batchID, temperatureC)

	safetyDecision := "HOLD"
	if visualStatus == "GOOD" && dangerMinutes < 120 {
		safetyDecision = "ELIGIBLE"
	} else if visualStatus == "REJECTED" || dangerMinutes >= 240 {
		safetyDecision = "REJECTED"
	}

	now := time.Now().UTC()
	checkID := uuid.NewString()

	var devPreviewBytes []byte
	if devicePreview != nil {
		devPreviewBytes = *devicePreview
	}

	const insertSQL = `
		INSERT INTO quality_checks
			(id, batch_id, image_path, visual_status, risk_level, cv_confidence,
			 cv_model_version, safety_decision, safety_reasons, fusion_model_version,
			 danger_zone_minutes, temperature_c, device_preview, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`

	_, err = s.pool.Exec(ctx, insertSQL,
		checkID, batchID, imagePath, visualStatus, riskLevel, confidence,
		"cv-v1", safetyDecision, []string{reason}, "fusion-v1",
		dangerMinutes, temperatureC, devPreviewBytes, userID, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert quality_check: %w", err)
	}

	return &QualityCheckResult{
		ID:                    checkID,
		BatchID:               batchID,
		VisualStatus:          visualStatus,
		RiskLevel:             riskLevel,
		Confidence:            confidence,
		Reason:                reason,
		SafetyDecision:        safetyDecision,
		DangerZoneMinutes:     dangerMinutes,
		RequiresHumanApproval: true,
		CreatedAt:             now,
	}, nil
}

func (s *QualityService) getDangerMinutes(ctx context.Context, batchID string, currentTemp *float64) float64 {
	if s.redis != nil {
		key := fmt.Sprintf("dz:%s", batchID)
		val, err := s.redis.HGet(ctx, key, "minutes").Float64()
		if err == nil {
			return val
		}
	}

	const q = `
		SELECT COALESCE(
			COUNT(*) * 1.0, 0
		)
		FROM sensor_readings
		WHERE batch_id = $1 AND temperature_c > 8`

	var dm float64
	_ = s.pool.QueryRow(ctx, q, batchID).Scan(&dm)
	return dm
}
