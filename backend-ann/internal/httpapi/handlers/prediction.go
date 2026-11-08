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
type PredictionHandler struct {
	pool     *pgxpool.Pool
	ml       *mlclient.MLClient
	carbonKg float64
}

func NewPredictionHandler(pool *pgxpool.Pool, ml *mlclient.MLClient, carbonKg float64) *PredictionHandler {
	return &PredictionHandler{
		pool:     pool,
		ml:       ml,
		carbonKg: carbonKg,
	}
}

// PredictDemand handles POST /predict-demand.
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

	switch mealType {
	case "BREAKFAST", "LUNCH", "SNACK", "DINNER":
	default:
		httpapi.NewValidation(
			"invalid meal_type",
			"meal_type must be BREAKFAST, LUNCH, SNACK, or DINNER",
		).Render(w)
		return
	}

	// The model uses Monday=0 through Sunday=6.
	dayOfWeek := int(time.Now().UTC().Weekday())
	if dayOfWeek == 0 {
		dayOfWeek = 6
	} else {
		dayOfWeek--
	}

	if req.DayOfWeek != nil {
		dayOfWeek = *req.DayOfWeek
	}

	if dayOfWeek < 0 || dayOfWeek > 6 {
		httpapi.NewValidation(
			"invalid day_of_week",
			"day_of_week must be between 0 and 6, with Monday=0",
		).Render(w)
		return
	}

	date := time.Now().UTC()
	if req.Date != "" {
		parsed, err := time.Parse("2006-01-02", req.Date)
		if err != nil {
			httpapi.NewValidation(
				"invalid date",
				"date must use YYYY-MM-DD format",
			).Render(w)
			return
		}
		date = parsed
	}

	ctx := r.Context()
	kitchenID := claims.KitchenID

	features := h.buildFeatures(
		ctx,
		claims,
		kitchenID,
		req.Attendance,
		mealType,
		req.Menu,
		dayOfWeek,
		req.DatasetID,
	)

	res, err := h.ml.PredictDemand(ctx, mlclient.PredictDemandRequest{
		KitchenID:    kitchenHash(kitchenID),
		FoodCategory: mealType,
		DateTime:     date,
		Features:     features,
	})

	if err != nil {
		if errors.Is(err, mlclient.ErrMLUnavailable) {
			httpapi.ErrMLUnavailable().Render(w)
			return
		}

		httpapi.NewInternal("demand prediction failed").Render(w)
		return
	}

	// Forward the complete model response without manufacturing values.
	writeJSON(w, http.StatusOK, res)
}

// buildFeatures assembles the feature vector sent to the model.
func (h *PredictionHandler) buildFeatures(
	ctx context.Context,
	claims *auth.Claims,
	kitchenID string,
	attendance *int,
	mealType string,
	menu []string,
	dayOfWeek int,
	datasetID string,
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

	if len(menu) > 0 {
		features["menu"] = menu
	}

	if datasetID != "" && kitchenID != "" {
		h.applyDatasetStats(ctx, datasetID, kitchenID, features)
	}

	if _, ok := features["attendance"]; !ok {
		if kitchenID != "" {
			var avg *float64
			var rows *int

			_ = h.pool.QueryRow(
				ctx,
				`SELECT AVG(footfall)::float8, COUNT(*)::int
				 FROM demand_history
				 WHERE kitchen_id = $1 AND footfall IS NOT NULL`,
				kitchenID,
			).Scan(&avg, &rows)

			if avg != nil && *avg > 0 {
				features["attendance"] = int(*avg)
				features["has_uploaded_history"] = true
			}

			if rows != nil {
				features["history_rows"] = *rows
			}

			var seededAvg *float64

			_ = h.pool.QueryRow(
				ctx,
				`SELECT AVG(COALESCE(actual_diners, expected_diners))::float8
				 FROM attendance
				 WHERE kitchen_id = $1`,
				kitchenID,
			).Scan(&seededAvg)

			if seededAvg != nil && *seededAvg > 0 {
				if _, set := features["attendance"]; !set {
					features["attendance"] = int(*seededAvg)
				}
				features["seeded_mean_attendance"] = *seededAvg
			}
		}
	}

	// Keep recent waste information as context.
	// Do not turn summary statistics into fabricated historical observations.
	var prepared, wasted float64

	_ = h.pool.QueryRow(
		ctx,
		`SELECT
			COALESCE(
				(SELECT SUM(prepared_qty)
				 FROM production
				 WHERE kitchen_id = $1
				   AND production_date >= CURRENT_DATE - 7),
				0
			),
			COALESCE(
				(SELECT SUM(quantity_kg)
				 FROM waste
				 WHERE kitchen_id = $1
				   AND recorded_at >= NOW() - INTERVAL '7 days'),
				0
			)`,
		kitchenID,
	).Scan(&prepared, &wasted)

	features["surplus_risk"] = surplusRisk(prepared, wasted)
	features["recent_prepared_kg"] = prepared
	features["recent_waste_kg"] = wasted

	return features
}

// applyDatasetStats adds summary statistics from one uploaded file.
func (h *PredictionHandler) applyDatasetStats(
	ctx context.Context,
	datasetID string,
	kitchenID string,
	features map[string]any,
) {
	var rows int
	var avgFootfall, avgOrders, avgPrepared, avgConsumed, avgWaste *float64

	err := h.pool.QueryRow(
		ctx,
		`SELECT
			COUNT(*)::int,
			AVG(footfall)::float8,
			AVG(orders_count)::float8,
			AVG(food_prepared_kg)::float8,
			AVG(food_consumed_kg)::float8,
			AVG(waste_kg)::float8
		 FROM demand_history
		 WHERE dataset_id = $1 AND kitchen_id = $2`,
		datasetID,
		kitchenID,
	).Scan(
		&rows,
		&avgFootfall,
		&avgOrders,
		&avgPrepared,
		&avgConsumed,
		&avgWaste,
	)

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

// kitchenHash derives a stable integer ID for the sidecar.
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
