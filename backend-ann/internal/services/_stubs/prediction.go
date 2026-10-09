package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// PredictDemandRequest contains inputs for demand prediction.
type PredictDemandRequest struct {
	KitchenID  string    `json:"kitchen_id"`
	TargetDate time.Time `json:"target_date"`
	MenuItems  []string  `json:"menu_items,omitempty"`
}

// PredictDemandResponse holds the predicted demand result.
type PredictDemandResponse struct {
	KitchenID    string           `json:"kitchen_id"`
	TargetDate   time.Time        `json:"target_date"`
	Predictions  []ItemPrediction `json:"predictions"`
	ModelVersion string           `json:"model_version"`
	CachedAt     *time.Time       `json:"cached_at,omitempty"`
	WhatIf       bool             `json:"what_if"`
}

// ItemPrediction is a per-item quantity prediction.
type ItemPrediction struct {
	MenuItem        string  `json:"menu_item"`
	PredictedQty    float64 `json:"predicted_qty"`
	ConfidenceLower float64 `json:"confidence_lower"`
	ConfidenceUpper float64 `json:"confidence_upper"`
}

// WhatIfRequest mirrors PredictDemandRequest but results are never persisted.
type WhatIfRequest = PredictDemandRequest

// PredictionService handles ML-based demand prediction with statistical fallback.
type PredictionService struct {
	db       *sql.DB
	rdb      *redis.Client
	ml       *mlclient.Client
	log      *zap.Logger
	cacheTTL time.Duration
}

// NewPredictionService constructs a PredictionService.
func NewPredictionService(db *sql.DB, rdb *redis.Client, ml *mlclient.Client, log *zap.Logger) *PredictionService {
	return &PredictionService{
		db:       db,
		rdb:      rdb,
		ml:       ml,
		log:      log,
		cacheTTL: 60 * time.Second,
	}
}

// PredictDemand validates the request, optionally fetches history from DB,
// calls mlclient.PredictDemand, falls back to statistical median×1.03,
// logs to prediction_log, and caches result for 60s.
func (s *PredictionService) PredictDemand(ctx context.Context, req PredictDemandRequest) (*PredictDemandResponse, error) {
	if err := s.validateRequest(req); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}

	cacheKey := s.cacheKey(req.KitchenID, req.TargetDate)
	if cached, err := s.fromCache(ctx, cacheKey); err == nil {
		return cached, nil
	}

	history, err := s.fetchHistory(ctx, req)
	if err != nil {
		s.log.Warn("failed to fetch history, proceeding without it", zap.Error(err))
	}

	mlReq := mlclient.PredictDemandRequest{
		KitchenID:  req.KitchenID,
		TargetDate: req.TargetDate,
		MenuItems:  req.MenuItems,
		History:    history,
	}

	var resp *PredictDemandResponse
	mlResp, mlErr := s.ml.PredictDemand(ctx, mlReq)
	if mlErr != nil {
		s.log.Warn("mlclient.PredictDemand failed, using statistical fallback", zap.Error(mlErr))
		resp, err = s.statisticalFallback(req, history)
		if err != nil {
			return nil, fmt.Errorf("statistical fallback: %w", err)
		}
	} else {
		resp = s.mapMLResponse(req, mlResp)
	}

	if logErr := s.logPrediction(ctx, resp); logErr != nil {
		s.log.Error("failed to log prediction", zap.Error(logErr))
	}

	if cacheErr := s.toCache(ctx, cacheKey, resp); cacheErr != nil {
		s.log.Warn("failed to cache prediction", zap.Error(cacheErr))
	}

	return resp, nil
}

// WhatIf runs prediction without persisting to prediction_log or cache.
func (s *PredictionService) WhatIf(ctx context.Context, req WhatIfRequest) (*PredictDemandResponse, error) {
	if err := s.validateRequest(req); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}

	history, err := s.fetchHistory(ctx, req)
	if err != nil {
		s.log.Warn("WhatIf: failed to fetch history", zap.Error(err))
	}

	mlReq := mlclient.PredictDemandRequest{
		KitchenID:  req.KitchenID,
		TargetDate: req.TargetDate,
		MenuItems:  req.MenuItems,
		History:    history,
	}

	mlResp, mlErr := s.ml.PredictDemand(ctx, mlReq)
	if mlErr != nil {
		s.log.Warn("WhatIf: mlclient failed, using statistical fallback", zap.Error(mlErr))
		resp, fbErr := s.statisticalFallback(req, history)
		if fbErr != nil {
			return nil, fmt.Errorf("statistical fallback: %w", fbErr)
		}
		resp.WhatIf = true
		return resp, nil
	}

	resp := s.mapMLResponse(req, mlResp)
	resp.WhatIf = true
	return resp, nil
}

// validateRequest checks required fields.
func (s *PredictionService) validateRequest(req PredictDemandRequest) error {
	if req.KitchenID == "" {
		return fmt.Errorf("kitchen_id is required")
	}
	if req.TargetDate.IsZero() {
		return fmt.Errorf("target_date is required")
	}
	return nil
}

// cacheKey builds a deterministic Redis key for the request.
func (s *PredictionService) cacheKey(kitchenID string, date time.Time) string {
	return fmt.Sprintf("pred:%s:%s", kitchenID, date.Format("2006-01-02"))
}

// fromCache retrieves a cached response.
func (s *PredictionService) fromCache(ctx context.Context, key string) (*PredictDemandResponse, error) {
	raw, err := s.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}
	var resp PredictDemandResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	resp.CachedAt = &now
	return &resp, nil
}

// toCache stores a response in Redis with TTL.
func (s *PredictionService) toCache(ctx context.Context, key string, resp *PredictDemandResponse) error {
	raw, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, key, raw, s.cacheTTL).Err()
}

// fetchHistory retrieves historical demand records from the DB.
func (s *PredictionService) fetchHistory(ctx context.Context, req PredictDemandRequest) ([]models.DemandHistory, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT menu_item, served_date, quantity_prepared, quantity_served
		FROM demand_history
		WHERE kitchen_id = $1
		  AND served_date >= $2
		ORDER BY served_date DESC
		LIMIT 90`,
		req.KitchenID, req.TargetDate.AddDate(0, -3, 0),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []models.DemandHistory
	for rows.Next() {
		var h models.DemandHistory
		if err := rows.Scan(&h.MenuItem, &h.ServedDate, &h.QuantityPrepared, &h.QuantityServed); err != nil {
			return nil, err
		}
		history = append(history, h)
	}
	return history, rows.Err()
}

// statisticalFallback computes a median of same-weekday quantities × 1.03
// and sets model_version = "fallback-stat".
func (s *PredictionService) statisticalFallback(req PredictDemandRequest, history []models.DemandHistory) (*PredictDemandResponse, error) {
	targetWeekday := req.TargetDate.Weekday()

	byItem := make(map[string][]float64)
	for _, h := range history {
		if h.ServedDate.Weekday() == targetWeekday {
			byItem[h.MenuItem] = append(byItem[h.MenuItem], h.QuantityServed)
		}
	}

	items := req.MenuItems
	if len(items) == 0 {
		for k := range byItem {
			items = append(items, k)
		}
	}

	preds := make([]ItemPrediction, 0, len(items))
	for _, item := range items {
		vals := byItem[item]
		var median float64
		if len(vals) > 0 {
			sort.Float64s(vals)
			n := len(vals)
			if n%2 == 0 {
				median = (vals[n/2-1] + vals[n/2]) / 2.0
			} else {
				median = vals[n/2]
			}
		}
		predicted := math.Round(median*1.03*10) / 10
		preds = append(preds, ItemPrediction{
			MenuItem:        item,
			PredictedQty:    predicted,
			ConfidenceLower: math.Round(predicted*0.85*10) / 10,
			ConfidenceUpper: math.Round(predicted*1.15*10) / 10,
		})
	}

	return &PredictDemandResponse{
		KitchenID:    req.KitchenID,
		TargetDate:   req.TargetDate,
		Predictions:  preds,
		ModelVersion: "fallback-stat",
	}, nil
}

// mapMLResponse converts the mlclient response to the service response.
func (s *PredictionService) mapMLResponse(req PredictDemandRequest, ml *mlclient.PredictDemandResponse) *PredictDemandResponse {
	preds := make([]ItemPrediction, 0, len(ml.Items))
	for _, it := range ml.Items {
		preds = append(preds, ItemPrediction{
			MenuItem:        it.MenuItem,
			PredictedQty:    it.PredictedQty,
			ConfidenceLower: it.ConfidenceLower,
			ConfidenceUpper: it.ConfidenceUpper,
		})
	}
	return &PredictDemandResponse{
		KitchenID:    req.KitchenID,
		TargetDate:   req.TargetDate,
		Predictions:  preds,
		ModelVersion: ml.ModelVersion,
	}
}

// logPrediction inserts a row into prediction_log.
func (s *PredictionService) logPrediction(ctx context.Context, resp *PredictDemandResponse) error {
	payload, err := json.Marshal(resp.Predictions)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO prediction_log (kitchen_id, target_date, model_version, predictions, created_at)
		VALUES ($1, $2, $3, $4, NOW())`,
		resp.KitchenID, resp.TargetDate, resp.ModelVersion, payload,
	)
	return err
}
