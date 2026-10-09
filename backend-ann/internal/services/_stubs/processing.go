package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/sih26234/food-waste/internal/mlclient"
	"github.com/sih26234/food-waste/internal/processing"
)

// SubmitMetricsRequest holds raw processing data from the kitchen.
type SubmitMetricsRequest struct {
	KitchenID       string                   `json:"kitchen_id"`
	PeriodStart     time.Time                `json:"period_start"`
	PeriodEnd       time.Time                `json:"period_end"`
	RawMeasurements []processing.Measurement `json:"raw_measurements"`
}

// AnalyticsQuery specifies filters for GetAnalytics.
type AnalyticsQuery struct {
	KitchenID string
	From      time.Time
	To        time.Time
}

// AnalyticsResult holds aggregated processing analytics.
type AnalyticsResult struct {
	KitchenID     string             `json:"kitchen_id"`
	From          time.Time          `json:"from"`
	To            time.Time          `json:"to"`
	TotalInputKg  float64            `json:"total_input_kg"`
	TotalWasteKg  float64            `json:"total_waste_kg"`
	EfficiencyPct float64            `json:"efficiency_pct"`
	AnomalyCount  int                `json:"anomaly_count"`
	Breakdown     map[string]float64 `json:"breakdown,omitempty"`
}

// ProcessingService manages kitchen processing metrics and anomaly detection.
type ProcessingService struct {
	db           *sql.DB
	ml           *mlclient.Client
	alertService *AlertService
	log          *zap.Logger
}

// NewProcessingService constructs a ProcessingService.
func NewProcessingService(db *sql.DB, ml *mlclient.Client, alerts *AlertService, log *zap.Logger) *ProcessingService {
	return &ProcessingService{
		db:           db,
		ml:           ml,
		alertService: alerts,
		log:          log,
	}
}

// SubmitMetrics computes derived metrics via internal/processing.Compute,
// UPSERTs the processing_metrics row, then asynchronously runs anomaly detection.
func (s *ProcessingService) SubmitMetrics(ctx context.Context, req SubmitMetricsRequest) (*models.ProcessingMetrics, error) {
	if req.KitchenID == "" {
		return nil, fmt.Errorf("kitchen_id is required")
	}
	if req.PeriodStart.IsZero() || req.PeriodEnd.IsZero() {
		return nil, fmt.Errorf("period_start and period_end are required")
	}
	if !req.PeriodEnd.After(req.PeriodStart) {
		return nil, fmt.Errorf("period_end must be after period_start")
	}

	computed, err := processing.Compute(req.RawMeasurements)
	if err != nil {
		return nil, fmt.Errorf("compute metrics: %w", err)
	}

	metrics := &models.ProcessingMetrics{
		KitchenID:    req.KitchenID,
		PeriodStart:  req.PeriodStart,
		PeriodEnd:    req.PeriodEnd,
		TotalInputKg: computed.TotalInputKg,
		TotalWasteKg: computed.TotalWasteKg,
		Breakdown:    computed.Breakdown,
		ComputedAt:   time.Now().UTC(),
	}

	if err := s.upsertMetrics(ctx, metrics); err != nil {
		return nil, fmt.Errorf("upsert metrics: %w", err)
	}

	// Run anomaly detection in background to avoid blocking the caller.
	go s.runAnomalyDetection(context.Background(), req.KitchenID, metrics)

	return metrics, nil
}

// upsertMetrics inserts or updates a processing_metrics row.
func (s *ProcessingService) upsertMetrics(ctx context.Context, m *models.ProcessingMetrics) error {
	breakdown, err := json.Marshal(m.Breakdown)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO processing_metrics
		  (kitchen_id, period_start, period_end, total_input_kg, total_waste_kg, breakdown, computed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (kitchen_id, period_start, period_end)
		DO UPDATE SET
		  total_input_kg = EXCLUDED.total_input_kg,
		  total_waste_kg = EXCLUDED.total_waste_kg,
		  breakdown      = EXCLUDED.breakdown,
		  computed_at    = EXCLUDED.computed_at`,
		m.KitchenID, m.PeriodStart, m.PeriodEnd,
		m.TotalInputKg, m.TotalWasteKg, breakdown, m.ComputedAt,
	)
	return err
}

// runAnomalyDetection calls mlclient.DetectAnomalies and creates alerts for findings.
func (s *ProcessingService) runAnomalyDetection(ctx context.Context, kitchenID string, metrics *models.ProcessingMetrics) {
	anomalies, err := s.ml.DetectAnomalies(ctx, mlclient.AnomalyRequest{
		KitchenID:    kitchenID,
		TotalInputKg: metrics.TotalInputKg,
		TotalWasteKg: metrics.TotalWasteKg,
		PeriodStart:  metrics.PeriodStart,
		PeriodEnd:    metrics.PeriodEnd,
	})
	if err != nil {
		s.log.Error("anomaly detection failed", zap.String("kitchen_id", kitchenID), zap.Error(err))
		return
	}

	for _, a := range anomalies.Anomalies {
		_, err := s.alertService.Create(ctx, models.CreateAlertRequest{
			KitchenID: kitchenID,
			Source:    "processing-anomaly",
			Severity:  a.Severity,
			Message:   a.Description,
		})
		if err != nil {
			s.log.Error("failed to create anomaly alert", zap.Error(err))
		}
	}
}

// GetAnalytics aggregates processing_metrics rows for the query window.
func (s *ProcessingService) GetAnalytics(ctx context.Context, q AnalyticsQuery) (*AnalyticsResult, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(total_input_kg), 0), COALESCE(SUM(total_waste_kg), 0)
		FROM processing_metrics
		WHERE kitchen_id = $1
		  AND period_start >= $2
		  AND period_end   <= $3`,
		q.KitchenID, q.From, q.To,
	)
	var totalInput, totalWaste float64
	if err := row.Scan(&totalInput, &totalWaste); err != nil {
		return nil, err
	}

	var effPct float64
	if totalInput > 0 {
		effPct = ((totalInput - totalWaste) / totalInput) * 100
	}

	anomalyCount, _ := s.countAnomalyAlerts(ctx, q)

	return &AnalyticsResult{
		KitchenID:     q.KitchenID,
		From:          q.From,
		To:            q.To,
		TotalInputKg:  totalInput,
		TotalWasteKg:  totalWaste,
		EfficiencyPct: effPct,
		AnomalyCount:  anomalyCount,
	}, nil
}

// countAnomalyAlerts counts anomaly alerts in the query window.
func (s *ProcessingService) countAnomalyAlerts(ctx context.Context, q AnalyticsQuery) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM alerts
		WHERE kitchen_id = $1
		  AND source = 'processing-anomaly'
		  AND created_at BETWEEN $2 AND $3`,
		q.KitchenID, q.From, q.To,
	).Scan(&n)
	return n, err
}
