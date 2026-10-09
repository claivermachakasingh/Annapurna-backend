package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sih26234/backend/internal/service"
)

// ProcessingHandler holds dependencies for processing metrics endpoints.
type ProcessingHandler struct {
	procSvc service.ProcessingService
}

// NewProcessingHandler constructs a ProcessingHandler.
func NewProcessingHandler(procSvc service.ProcessingService) *ProcessingHandler {
	return &ProcessingHandler{procSvc: procSvc}
}

// RegisterRoutes mounts processing routes onto r.
func (h *ProcessingHandler) RegisterRoutes(r chi.Router) {
	r.Post("/processing/metrics", h.PostMetrics)
	r.Get("/processing/analytics", h.Analytics)
}

// ─── Request / Response types ────────────────────────────────────────────────

// ProcessingMetricsRequest is the body for POST /processing/metrics.
type ProcessingMetricsRequest struct {
	KitchenID    string     `json:"kitchen_id"`
	BatchID      string     `json:"batch_id,omitempty"`
	ItemName     string     `json:"item_name"`
	InputKg      float64    `json:"input_kg"`
	OutputKg     float64    `json:"output_kg"`
	WasteKg      float64    `json:"waste_kg"`
	ProcessType  string     `json:"process_type"` // cooking | chilling | packaging | composting
	StartedAt    time.Time  `json:"started_at"`
	EndedAt      time.Time  `json:"ended_at"`
	Notes        string     `json:"notes,omitempty"`
}

// ProcessingMetricsResponse is returned by POST /processing/metrics.
type ProcessingMetricsResponse struct {
	MetricID    string    `json:"metric_id"`
	KitchenID   string    `json:"kitchen_id"`
	YieldPct    float64   `json:"yield_pct"`
	CreatedAt   time.Time `json:"created_at"`
}

// ProcessingAnalyticsItem is one aggregated row.
type ProcessingAnalyticsItem struct {
	ProcessType string  `json:"process_type"`
	TotalInputKg float64 `json:"total_input_kg"`
	TotalOutputKg float64 `json:"total_output_kg"`
	TotalWasteKg float64 `json:"total_waste_kg"`
	AvgYieldPct  float64 `json:"avg_yield_pct"`
	BatchCount   int     `json:"batch_count"`
}

// ProcessingAnalyticsResponse is returned by GET /processing/analytics.
type ProcessingAnalyticsResponse struct {
	KitchenID string                    `json:"kitchen_id,omitempty"`
	From      string                    `json:"from"`
	To        string                    `json:"to"`
	Summary   []ProcessingAnalyticsItem `json:"summary"`
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// PostMetrics handles POST /processing/metrics.
//
//	@Summary      Submit processing metrics
//	@Description  Record input/output/waste metrics for a food processing batch.
//	@Tags         processing
//	@Accept       json
//	@Produce      json
//	@Security     BearerAuth
//	@Param        body body ProcessingMetricsRequest true "Processing metrics"
//	@Success      201 {object} ProcessingMetricsResponse
//	@Failure      400 {object} ErrorResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /processing/metrics [post]
func (h *ProcessingHandler) PostMetrics(w http.ResponseWriter, r *http.Request) {
	var req ProcessingMetricsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if req.KitchenID == "" || req.ItemName == "" || req.InputKg <= 0 {
		writeError(w, http.StatusBadRequest, "kitchen_id, item_name and input_kg > 0 are required", nil)
		return
	}

	result, err := h.procSvc.PostMetrics(r.Context(), service.ProcessingMetricsInput{
		KitchenID:   req.KitchenID,
		BatchID:     req.BatchID,
		ItemName:    req.ItemName,
		InputKg:     req.InputKg,
		OutputKg:    req.OutputKg,
		WasteKg:     req.WasteKg,
		ProcessType: req.ProcessType,
		StartedAt:   req.StartedAt,
		EndedAt:     req.EndedAt,
		Notes:       req.Notes,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record metrics", err)
		return
	}

	writeJSON(w, http.StatusCreated, ProcessingMetricsResponse{
		MetricID:  result.MetricID,
		KitchenID: result.KitchenID,
		YieldPct:  result.YieldPct,
		CreatedAt: result.CreatedAt,
	})
}

// Analytics handles GET /processing/analytics.
//
//	@Summary      Processing analytics
//	@Description  Aggregate processing yield and waste across process types.
//	@Tags         processing
//	@Produce      json
//	@Security     BearerAuth
//	@Param        kitchen_id query string false "Filter by kitchen"
//	@Param        from       query string false "Start date (YYYY-MM-DD)"
//	@Param        to         query string false "End date (YYYY-MM-DD)"
//	@Success      200 {object} ProcessingAnalyticsResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /processing/analytics [get]
func (h *ProcessingHandler) Analytics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kitchenID := q.Get("kitchen_id")
	from := q.Get("from")
	to := q.Get("to")

	result, err := h.procSvc.Analytics(r.Context(), service.ProcessingAnalyticsInput{
		KitchenID: kitchenID,
		From:      from,
		To:        to,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch processing analytics", err)
		return
	}

	summary := make([]ProcessingAnalyticsItem, len(result.Summary))
	for i, s := range result.Summary {
		summary[i] = ProcessingAnalyticsItem{
			ProcessType:   s.ProcessType,
			TotalInputKg:  s.TotalInputKg,
			TotalOutputKg: s.TotalOutputKg,
			TotalWasteKg:  s.TotalWasteKg,
			AvgYieldPct:   s.AvgYieldPct,
			BatchCount:    s.BatchCount,
		}
	}

	writeJSON(w, http.StatusOK, ProcessingAnalyticsResponse{
		KitchenID: result.KitchenID,
		From:      result.From,
		To:        result.To,
		Summary:   summary,
	})
}
