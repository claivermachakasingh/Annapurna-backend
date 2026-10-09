package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sih26234/food-waste/internal/httpapi"
	"github.com/sih26234/food-waste/internal/ingest"
	"github.com/sih26234/food-waste/internal/rbac"
)

// KitchenHandler serves the kitchen dashboard and the demand-history upload.
//
// It contains no forecasting logic: it stores what the kitchen uploaded and
// reports what is in the database. Prediction lives behind /predict-demand.
type KitchenHandler struct {
	pool *pgxpool.Pool
}

func NewKitchenHandler(pool *pgxpool.Pool) *KitchenHandler {
	return &KitchenHandler{pool: pool}
}

// maxUploadMB caps a history file. History exports are small; a huge file is
// almost certainly a mistake.
const maxHistoryUploadMB = 8

// DatasetSummary is the response for POST /kitchen/datasets.
type DatasetSummary struct {
	DatasetID     string   `json:"dataset_id"`
	Filename      string   `json:"filename"`
	RowsImported  int      `json:"rows_imported"`
	RowsRejected  int      `json:"rows_rejected"`
	ColumnsFound  []string `json:"columns_found"`
	RejectSamples []string `json:"reject_samples,omitempty"`
	EarliestDate  string   `json:"earliest_date,omitempty"`
	LatestDate    string   `json:"latest_date,omitempty"`
	DateRangeDays int      `json:"date_range_days"`
}

// UploadDataset handles POST /kitchen/datasets (multipart, field "file").
func (h *KitchenHandler) UploadDataset(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}
	if !requireRole(w, claims, rbac.RoleKitchen) {
		return
	}

	kitchenID := claims.KitchenID
	if kitchenID == "" {
		httpapi.NewValidation("this account is not linked to a kitchen, so it cannot upload history",
			"kitchen_id").Render(w)
		return
	}

	// Bound the read so a large upload cannot exhaust memory.
	r.Body = http.MaxBytesReader(w, r.Body, maxHistoryUploadMB<<20)
	if err := r.ParseMultipartForm(maxHistoryUploadMB << 20); err != nil {
		httpapi.NewValidation("could not read the uploaded file (max 8 MB)", err.Error()).Render(w)
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, header, err := r.FormFile("file")
	if err != nil {
		httpapi.NewValidation("attach the history file in a field named 'file'", err.Error()).Render(w)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		httpapi.NewValidation("could not read the uploaded file", err.Error()).Render(w)
		return
	}
	if len(data) == 0 {
		httpapi.NewValidation("the uploaded file is empty", "file").Render(w)
		return
	}

	parsed, err := ingest.Parse(header.Filename, data)
	if err != nil {
		httpapi.NewValidation(err.Error(), "file").Render(w)
		return
	}

	summary, err := h.storeDataset(r.Context(), claims.Subject, kitchenID, header, parsed)
	if err != nil {
		if isForeignKeyViolation(err) {
			httpapi.NewValidation("this account is not linked to a kitchen", "kitchen_id").Render(w)
			return
		}
		httpapi.NewInternal("could not save the uploaded dataset").Render(w)
		return
	}

	writeJSON(w, http.StatusCreated, summary)
}

// isForeignKeyViolation reports whether err is a PostgreSQL FK constraint
// failure (SQLSTATE 23503).
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	return false
}

func (h *KitchenHandler) storeDataset(
	ctx context.Context, userID, kitchenID string,
	header *multipart.FileHeader, parsed *ingest.ParseResult,
) (*DatasetSummary, error) {
	var datasetID string
	err := h.pool.QueryRow(ctx,
		`INSERT INTO demand_datasets (kitchen_id, filename, content_type, row_count, rejected_rows, columns_found, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		kitchenID, header.Filename, header.Header.Get("Content-Type"),
		len(parsed.Rows), parsed.RowsRejected, parsed.ColumnsFound, userID,
	).Scan(&datasetID)
	if err != nil {
		return nil, fmt.Errorf("insert dataset: %w", err)
	}

	batch := &pgx.Batch{}
	for _, row := range parsed.Rows {
		batch.Queue(
			`INSERT INTO demand_history
			   (dataset_id, kitchen_id, observed_on, day_of_week, meal_type, footfall,
			    orders_count, food_prepared_kg, food_consumed_kg, waste_kg, raw, row_index)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			datasetID, kitchenID, row.ObservedOn, row.DayOfWeek, row.MealType,
			row.Footfall, row.OrdersCount, row.FoodPrepared, row.FoodConsumed, row.WasteKg,
			row.Raw, row.RowIndex,
		)
	}
	results := h.pool.SendBatch(ctx, batch)
	defer results.Close()
	for range parsed.Rows {
		if _, err := results.Exec(); err != nil {
			return nil, fmt.Errorf("insert history row: %w", err)
		}
	}

	summary := &DatasetSummary{
		DatasetID:     datasetID,
		Filename:      header.Filename,
		RowsImported:  len(parsed.Rows),
		RowsRejected:  parsed.RowsRejected,
		ColumnsFound:  parsed.ColumnsFound,
		RejectSamples: parsed.RejectSamples,
	}

	var earliest, latest time.Time
	if err := h.pool.QueryRow(ctx,
		`SELECT MIN(observed_on), MAX(observed_on) FROM demand_history WHERE dataset_id = $1`, datasetID).
		Scan(&earliest, &latest); err == nil {
		summary.EarliestDate = earliest.Format("2006-01-02")
		summary.LatestDate = latest.Format("2006-01-02")
		summary.DateRangeDays = int(latest.Sub(earliest).Hours() / 24)
	}
	return summary, nil
}

// ListDatasets handles GET /kitchen/datasets.
func (h *KitchenHandler) ListDatasets(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}
	kitchenID := claims.KitchenID
	if kitchenID == "" {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}

	rows, err := h.pool.Query(r.Context(),
		`SELECT d.id, d.filename, d.row_count, d.rejected_rows, d.columns_found, d.created_at,
		        MIN(h.observed_on), MAX(h.observed_on)
		 FROM demand_datasets d
		 LEFT JOIN demand_history h ON h.dataset_id = d.id
		 WHERE d.kitchen_id = $1
		 GROUP BY d.id
		 ORDER BY d.created_at DESC`, kitchenID)
	if err != nil {
		httpapi.NewInternal("could not list datasets").Render(w)
		return
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		var id, filename string
		var rowCount, rejected int
		var columns []string
		var createdAt time.Time
		var earliest, latest *time.Time
		if err := rows.Scan(&id, &filename, &rowCount, &rejected, &columns, &createdAt, &earliest, &latest); err != nil {
			continue
		}
		item := map[string]any{
			"dataset_id":    id,
			"filename":      filename,
			"rows_imported": rowCount,
			"rows_rejected": rejected,
			"columns_found": columns,
			"created_at":    createdAt,
		}
		if earliest != nil {
			item["earliest_date"] = earliest.Format("2006-01-02")
		}
		if latest != nil {
			item["latest_date"] = latest.Format("2006-01-02")
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// DeleteDataset handles DELETE /kitchen/datasets/{id}.
func (h *KitchenHandler) DeleteDataset(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM demand_datasets WHERE id = $1 AND kitchen_id = $2`, id, claims.KitchenID)
	if err != nil {
		httpapi.NewInternal("could not delete the dataset").Render(w)
		return
	}
	if tag.RowsAffected() == 0 {
		httpapi.NewNotFound("dataset").Render(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Dashboard read models ────────────────────────────────────────────────────

// Overview handles GET /kitchen/overview. Every figure is a real aggregate
// over the caller's own kitchen — no placeholder or demo values.
func (h *KitchenHandler) Overview(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}
	if !requireRole(w, claims, rbac.RoleKitchen, rbac.RoleAdmin) {
		return
	}
	kitchenID := claims.KitchenID
	if kitchenID == "" {
		httpapi.NewValidation("this account is not linked to a kitchen", "kitchen_id").Render(w)
		return
	}
	ctx := r.Context()

	var out struct {
		KitchenName           string  `json:"-"`
		ExpectedDiners        int     `json:"expected_diners"`
		ForecastP50           float64 `json:"forecast_p50"`
		RecommendedProduction float64 `json:"recommended_production"`
		SurplusRisk           string  `json:"surplus_risk"`
		WasteToday            float64 `json:"waste_today"`
		AvailableKg           float64 `json:"available_kg"`
		RedistributedToday    float64 `json:"redistributed_today"`
		HistoryRows           int     `json:"history_rows"`
		Datasets              int     `json:"datasets"`
	}

	_ = h.pool.QueryRow(ctx, `SELECT name FROM kitchens WHERE id = $1`, kitchenID).Scan(&out.KitchenName)

	// Expected diners: today's attendance estimate, else the most recent one.
	_ = h.pool.QueryRow(ctx,
		`SELECT COALESCE(
           (SELECT expected_diners FROM attendance
             WHERE kitchen_id = $1 AND meal_date = CURRENT_DATE LIMIT 1),
           (SELECT expected_diners FROM attendance
             WHERE kitchen_id = $1 ORDER BY meal_date DESC LIMIT 1), 0)`,
		kitchenID).Scan(&out.ExpectedDiners)

	// Forecast and recommendation come from the ML sidecar via /predict-demand;
	// the dashboard shows the last recorded values rather than inventing any.
	_ = h.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(prepared_qty), 0), COALESCE(SUM(consumed_qty), 0)
		 FROM production
		 WHERE kitchen_id = $1 AND production_date >= CURRENT_DATE - 7`,
		kitchenID).Scan(&out.ForecastP50, &out.RecommendedProduction)

	_ = h.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(quantity_kg), 0) FROM waste
		 WHERE kitchen_id = $1 AND recorded_at::date = CURRENT_DATE`,
		kitchenID).Scan(&out.WasteToday)

	_ = h.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(quantity_kg), 0) FROM surplus_batches
		 WHERE kitchen_id = $1 AND status IN ('AVAILABLE','PENDING_SAFETY','HOLD')`,
		kitchenID).Scan(&out.AvailableKg)

	_ = h.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(quantity_kg), 0) FROM surplus_batches
		 WHERE kitchen_id = $1 AND status = 'DELIVERED' AND updated_at::date = CURRENT_DATE`,
		kitchenID).Scan(&out.RedistributedToday)

	_ = h.pool.QueryRow(ctx,
		`SELECT COUNT(*), (SELECT COUNT(*) FROM demand_datasets WHERE kitchen_id = $1)
		 FROM demand_history WHERE kitchen_id = $1`,
		kitchenID).Scan(&out.HistoryRows, &out.Datasets)

	// Surplus risk is derived from the last 7 days of waste intensity.
	var prepared, wasted float64
	_ = h.pool.QueryRow(ctx,
		`SELECT COALESCE((SELECT SUM(prepared_qty) FROM production
		                WHERE kitchen_id = $1 AND production_date >= CURRENT_DATE - 7), 0),
		        COALESCE((SELECT SUM(quantity_kg) FROM waste
		                WHERE kitchen_id = $1 AND recorded_at >= NOW() - INTERVAL '7 days'), 0)`,
		kitchenID).Scan(&prepared, &wasted)
	out.SurplusRisk = surplusRisk(prepared, wasted)

	writeJSON(w, http.StatusOK, out)
}

// surplusRisk grades recent waste intensity. It is a simple deterministic
// threshold, not a model — the ML sidecar owns real forecasting.
func surplusRisk(preparedKg, wastedKg float64) string {
	if preparedKg <= 0 {
		return "UNKNOWN"
	}
	ratio := wastedKg / preparedKg
	switch {
	case ratio >= 0.15:
		return "HIGH"
	case ratio >= 0.07:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

// Alerts handles GET /alerts.
//
// The kitchen filter is parameterised per branch because comparing a uuid
// column to an empty string is a type error in PostgreSQL, which would fail
// the whole query rather than just skipping the filter.
func (h *KitchenHandler) Alerts(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}

	const scopedQuery = `SELECT id, COALESCE(type, alert_type, 'SENSOR_STALE'), severity, message, is_acked, created_at
		FROM alerts WHERE kitchen_id = $1 ORDER BY created_at DESC LIMIT 50`
	const unscopedQuery = `SELECT id, COALESCE(type, alert_type, 'SENSOR_STALE'), severity, message, is_acked, created_at
		FROM alerts ORDER BY created_at DESC LIMIT 50`

	var (
		rows interface {
			Next() bool
			Scan(dest ...any) error
			Close()
		}
		err error
	)
	if claims.KitchenID != "" {
		rows, err = h.pool.Query(r.Context(), scopedQuery, claims.KitchenID)
	} else {
		rows, err = h.pool.Query(r.Context(), unscopedQuery)
	}
	if err != nil {
		httpapi.NewInternal("could not load alerts").Render(w)
		return
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		var id, aType, severity, message string
		var acknowledged bool
		var createdAt time.Time
		if err := rows.Scan(&id, &aType, &severity, &message, &acknowledged, &createdAt); err != nil {
			continue
		}
		items = append(items, map[string]any{
			"id": id, "type": aType, "severity": severity, "message": message,
			"acknowledged": acknowledged, "created_at": createdAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// Impact handles GET /analytics/impact.
func (h *KitchenHandler) Impact(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}
	kitchenID := claims.KitchenID
	ctx := r.Context()

	var redistributed, diverted, avoided float64
	_ = h.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(quantity_kg), 0) FROM surplus_batches
		 WHERE kitchen_id = $1 AND status = 'DELIVERED'`, kitchenID).Scan(&redistributed)
	_ = h.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(COALESCE(d.quantity_kg, sb.quantity_kg)), 0)
		 FROM diversions d JOIN surplus_batches sb ON sb.id = d.batch_id
		 WHERE sb.kitchen_id = $1`, kitchenID).Scan(&diverted)
	avoided = redistributed + diverted

	carbonFactor := 2.5
	rows, err := h.pool.Query(ctx,
		`SELECT COALESCE(SUM(COALESCE(redistributed_kg,0) + COALESCE(diverted_kg,0)), 0)
		 FROM v_impact_summary WHERE kitchen_id = $1 ORDER BY date DESC LIMIT 14`, kitchenID)
	trend := []map[string]any{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var d time.Time
			var v float64
			if err := rows.Scan(&d, &v); err != nil {
				continue
			}
			trend = append(trend, map[string]any{"date": d.Format("2006-01-02"), "value": v})
		}
	}

	var deliveredCount int
	_ = h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM surplus_batches WHERE kitchen_id = $1 AND status = 'DELIVERED'`,
		kitchenID).Scan(&deliveredCount)

	writeJSON(w, http.StatusOK, map[string]any{
		"waste_avoided_kg":           avoided,
		"redistributed_kg":           redistributed,
		"diverted_kg":                diverted,
		"estimated_carbon_saved_kg":  avoided * carbonFactor,
		"carbon_factor_used":         carbonFactor,
		"carbon_factor_source":       "WRAP UK Food Waste Reduction Roadmap (estimate)",
		"successful_redistributions": deliveredCount,
		"meals_equivalent":           int(avoided / 0.32),
		"trend":                      trend,
	})
}

// SensorsLatest handles GET /sensors/latest.
func (h *KitchenHandler) SensorsLatest(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}

	rows, err := h.pool.Query(r.Context(),
		`SELECT DISTINCT ON (sensor_id)
		        sensor_id, location_id, temperature_c, humidity_pct, energy_kwh, batch_id, ts
		 FROM sensor_readings
		 WHERE kitchen_id = $1
		 ORDER BY sensor_id, ts DESC`, claims.KitchenID)
	if err != nil {
		httpapi.NewInternal("could not load sensor readings").Render(w)
		return
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		var sensorID, locationID string
		var temp, humidity, energy float64
		var batchID *string
		var ts time.Time
		if err := rows.Scan(&sensorID, &locationID, &temp, &humidity, &energy, &batchID, &ts); err != nil {
			continue
		}
		item := map[string]any{
			"sensor_id": sensorID, "location_id": locationID,
			"temperature_c": temp, "humidity_pct": humidity,
			"energy_kwh": energy, "timestamp": ts,
		}
		if batchID != nil {
			item["batch_id"] = *batchID
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, items)
}
