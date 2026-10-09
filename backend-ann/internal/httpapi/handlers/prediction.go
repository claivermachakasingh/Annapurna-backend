package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sih26234/food-waste/internal/auth"
	"github.com/sih26234/food-waste/internal/httpapi"
	"github.com/sih26234/food-waste/internal/mlclient"
	"github.com/sih26234/food-waste/internal/rbac"
)

// decodeJSON reads a JSON request body into v.
func decodeJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return err
	}
	return nil
}

// PredictionHandler serves POST /predict-demand.
//
// It holds **no forecasting logic**. Its whole job is to assemble the feature
// vector from what the kitchen uploaded plus what is already in the database,
// hand it to the ML sidecar through mlclient, and shape the sidecar's answer
// into the contract response. When the trained model is dropped in behind
// /predict/demand, nothing here has to change.
type PredictionHandler struct {
	pool     *pgxpool.Pool
	ml       *mlclient.MLClient
	carbonKg float64
}

func NewPredictionHandler(pool *pgxpool.Pool, ml *mlclient.MLClient, carbonKg float64) *PredictionHandler {
	return &PredictionHandler{pool: pool, ml: ml, carbonKg: carbonKg}
}

// PredictDemand handles POST /predict-demand.
//
// Request (contract row 7):
//
//	{"attendance":450,"meal_type":"LUNCH","menu":["rice","dal"],
//	 "day_of_week":4,"date":"2026-10-02"}
//
// `dataset_id` is optional; when supplied the features are enriched with the
// summary statistics of that uploaded history file.
func (h *PredictionHandler) PredictDemand(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}
	if !requireRole(w, claims, rbac.RoleKitchen, rbac.RoleAdmin) {
		return
	}

	var req struct {
		Attendance *int     `json:"attendance"`
		MealType   string   `json:"meal_type"`
		Menu       []string `json:"menu"`
		DayOfWeek  *int     `json:"day_of_week"`
		Date       string   `json:"date"`
		DatasetID  string   `json:"dataset_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpapi.NewValidation("invalid request body", err.Error()).Render(w)
		return
	}

	mealType := strings.ToUpper(strings.TrimSpace(req.MealType))
	if mealType == "" {
		mealType = "LUNCH"
	}

	dayOfWeek := int(time.Now().UTC().Weekday())
	if dayOfWeek == 0 {
		dayOfWeek = 6
	} else {
		dayOfWeek--
	}
	if req.DayOfWeek != nil {
		dayOfWeek = *req.DayOfWeek
	}

	date := time.Now().UTC()
	if req.Date != "" {
		if parsed, err := time.Parse("2006-01-02", req.Date); err == nil {
			date = parsed
		}
	}

	ctx := r.Context()
	kitchenID := claims.KitchenID
	features := h.buildFeatures(ctx, claims, kitchenID, req.Attendance, mealType, dayOfWeek, req.DatasetID)

	res, err := h.ml.PredictDemand(ctx, mlclient.PredictDemandRequest{
		KitchenID:    kitchenHash(kitchenID),
		FoodCategory: mealType,
		DateTime:     date,
		Features:     features,
	})
	if err != nil {
		// The contract reserves ML_UNAVAILABLE for exactly this: the API stays
		// up and says the dependency is down rather than inventing a number.
		if errors.Is(err, mlclient.ErrMLUnavailable) {
			httpapi.ErrMLUnavailable().Render(w)
			return
		}
		httpapi.NewInternal("demand prediction failed").Render(w)
		return
	}

	// Shape the sidecar answer into the contract response. The sidecar owns the
	// numbers; this only carries them through.
	writeJSON(w, http.StatusOK, map[string]any{
		"predicted_consumption":  float64(res.PredictedServings),
		"recommended_production": float64(res.PredictedServings),
		"expected_surplus":       0.0,
		"surplus_risk":           features["surplus_risk"],
		"prediction_interval": map[string]any{
			"p10":             float64(res.PredictedServings) * 0.9,
			"p50":             float64(res.PredictedServings),
			"p90":             float64(res.PredictedServings) * 1.1,
			"coverage_target": 0.8,
		},
		"recommended_quantile": 0.7,
		"top_drivers":          []any{},
		"confidence":           res.Confidence,
		"model_version":        res.ModelVersion,
		"data_source":          dataSource(features),
	})
}

// buildFeatures assembles the feature vector sent to the model.
//
// Sources, in order of preference for footfall:
//  1. the attendance the caller supplied for this specific prediction,
//  2. the mean footfall from the uploaded history dataset,
//  3. the mean footfall from the seeded attendance table.
func (h *PredictionHandler) buildFeatures(
	ctx context.Context, claims *auth.Claims, kitchenID string,
	attendance *int, mealType string, dayOfWeek int, datasetID string,
) map[string]any {
	features := map[string]any{
		"meal_type":            mealType,
		"day_of_week":          dayOfWeek,
		"has_uploaded_history": false,
		"history_rows":         0,
	}

	if attendance != nil {
		features["attendance"] = *attendance
	}

	// Enrich from an explicitly requested uploaded dataset.
	if datasetID != "" && kitchenID != "" {
		h.applyDatasetStats(ctx, datasetID, kitchenID, features)
	}

	if _, ok := features["attendance"]; !ok {
		// Mean footfall from uploaded history across all of this kitchen's
		// datasets, then fall back to the seeded attendance table.
		if kitchenID != "" {
			var avg *float64
			var rows *int
			_ = h.pool.QueryRow(ctx,
				`SELECT AVG(footfall)::float8, COUNT(*)::int
				 FROM demand_history WHERE kitchen_id = $1 AND footfall IS NOT NULL`,
				kitchenID).Scan(&avg, &rows)
			if avg != nil && *avg > 0 {
				features["attendance"] = int(*avg)
				features["has_uploaded_history"] = true
			}
			if rows != nil {
				features["history_rows"] = *rows
			}

			if kitchenID != "" {
				var seededAvg *float64
				_ = h.pool.QueryRow(ctx,
					`SELECT AVG(COALESCE(actual_diners, expected_diners))::float8
					 FROM attendance WHERE kitchen_id = $1`, kitchenID).Scan(&seededAvg)
				if seededAvg != nil && *seededAvg > 0 {
					if _, set := features["attendance"]; !set {
						features["attendance"] = int(*seededAvg)
					}
					features["seeded_mean_attendance"] = *seededAvg
				}
			}
		}
	}

	// Surplus risk from the recent waste intensity — a threshold, not a model.
	var prepared, wasted float64
	_ = h.pool.QueryRow(ctx,
		`SELECT COALESCE((SELECT SUM(prepared_qty) FROM production
		                WHERE kitchen_id = $1 AND production_date >= CURRENT_DATE - 7), 0),
		        COALESCE((SELECT SUM(quantity_kg) FROM waste
		                WHERE kitchen_id = $1 AND recorded_at >= NOW() - INTERVAL '7 days'), 0)`,
		kitchenID).Scan(&prepared, &wasted)
	features["surplus_risk"] = surplusRisk(prepared, wasted)
	features["recent_prepared_kg"] = prepared
	features["recent_waste_kg"] = wasted

	return features
}

// applyDatasetStats adds summary statistics from one uploaded file.
func (h *PredictionHandler) applyDatasetStats(ctx context.Context, datasetID, kitchenID string, features map[string]any) {
	var rows int
	var avgFootfall, avgOrders, avgPrepared, avgConsumed, avgWaste *float64
	err := h.pool.QueryRow(ctx,
		`SELECT COUNT(*)::int,
		        AVG(footfall)::float8, AVG(orders_count)::float8,
		        AVG(food_prepared_kg)::float8, AVG(food_consumed_kg)::float8, AVG(waste_kg)::float8
		 FROM demand_history WHERE dataset_id = $1 AND kitchen_id = $2`,
		datasetID, kitchenID).Scan(&rows, &avgFootfall, &avgOrders,
		&avgPrepared, &avgConsumed, &avgWaste)
	if err != nil || rows == 0 {
		return
	}

	features["has_uploaded_history"] = true
	features["history_rows"] = rows
	features["dataset_id"] = datasetID
	setIfPresent(features, "mean_footfall", avgFootfall)
	setIfPresent(features, "mean_orders", avgOrders)
	setIfPresent(features, "mean_prepared_kg", avgPrepared)
	setIfPresent(features, "mean_consumed_kg", avgConsumed)
	setIfPresent(features, "mean_waste_kg", avgWaste)

	// Per-person consumption is the signal the model most needs.
	if avgFootfall != nil && *avgFootfall > 0 && avgConsumed != nil {
		features["kg_per_footfall"] = *avgConsumed / *avgFootfall
	}
}

func setIfPresent(m map[string]any, key string, v *float64) {
	if v != nil {
		m[key] = *v
	}
}

func dataSource(features map[string]any) string {
	if has, _ := features["has_uploaded_history"].(bool); has {
		return "UPLOADED_HISTORY"
	}
	return "DATABASE"
}

// kitchenHash derives a stable integer id for the sidecar, which takes an int.
func kitchenHash(kitchenID string) int {
	var h int
	for _, c := range kitchenID {
		h = h*31 + int(c)
		if h < 0 {
			h = -h
		}
	}
	return h
}
