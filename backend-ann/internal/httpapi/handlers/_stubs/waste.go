package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sih26234/backend/internal/domain"
	"github.com/sih26234/backend/internal/service"
)

// WasteHandler holds dependencies for waste endpoints.
type WasteHandler struct {
	wasteSvc service.WasteService
}

// NewWasteHandler constructs a WasteHandler.
func NewWasteHandler(wasteSvc service.WasteService) *WasteHandler {
	return &WasteHandler{wasteSvc: wasteSvc}
}

// RegisterRoutes mounts waste routes onto r.
func (h *WasteHandler) RegisterRoutes(r chi.Router) {
	r.Post("/waste", h.LogWaste)
	r.Get("/waste/analytics", h.Analytics)
}

// ─── Request / Response types ────────────────────────────────────────────────

// LogWasteRequest is the body for POST /waste.
type LogWasteRequest struct {
	KitchenID       string                 `json:"kitchen_id"`
	MealType        domain.MealType        `json:"meal_type"`
	ItemName        string                 `json:"item_name"`
	QuantityKg      float64                `json:"quantity_kg"`
	Cause           domain.WasteCause      `json:"cause"`
	DiversionStream domain.DiversionStream `json:"diversion_stream"`
	BatchID         string                 `json:"batch_id,omitempty"`
	Notes           string                 `json:"notes,omitempty"`
	RecordedAt      *time.Time             `json:"recorded_at,omitempty"`
}

// LogWasteResponse is returned by POST /waste.
type LogWasteResponse struct {
	WasteID    string    `json:"waste_id"`
	KitchenID  string    `json:"kitchen_id"`
	QuantityKg float64   `json:"quantity_kg"`
	CreatedAt  time.Time `json:"created_at"`
}

// WasteAnalyticsResponse is returned by GET /waste/analytics.
type WasteAnalyticsResponse struct {
	Period          string             `json:"period"`
	TotalKg         float64            `json:"total_kg"`
	ByMealType      map[string]float64 `json:"by_meal_type"`
	ByCause         map[string]float64 `json:"by_cause"`
	ByDiversion     map[string]float64 `json:"by_diversion_stream"`
	DailyTrend      []DailyWastePoint  `json:"daily_trend"`
	TopItems        []WasteItemSummary `json:"top_items"`
	ReductionVsLast float64            `json:"reduction_vs_last_period_pct"`
}

// DailyWastePoint is a single point in the daily trend series.
type DailyWastePoint struct {
	Date   string  `json:"date"`
	Kg     float64 `json:"kg"`
	Events int     `json:"events"`
}

// WasteItemSummary summarises waste for a single food item.
type WasteItemSummary struct {
	ItemName   string  `json:"item_name"`
	TotalKg    float64 `json:"total_kg"`
	EventCount int     `json:"event_count"`
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// LogWaste handles POST /waste.
func (h *WasteHandler) LogWaste(w http.ResponseWriter, r *http.Request) {
	var req LogWasteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if req.KitchenID == "" || req.ItemName == "" || req.QuantityKg <= 0 {
		writeError(w, http.StatusBadRequest, "kitchen_id, item_name and quantity_kg > 0 are required", nil)
		return
	}

	result, err := h.wasteSvc.LogWaste(r.Context(), service.LogWasteInput{
		KitchenID:       req.KitchenID,
		MealType:        req.MealType,
		ItemName:        req.ItemName,
		QuantityKg:      req.QuantityKg,
		Cause:           req.Cause,
		DiversionStream: req.DiversionStream,
		BatchID:         req.BatchID,
		Notes:           req.Notes,
		RecordedAt:      req.RecordedAt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to log waste", err)
		return
	}

	writeJSON(w, http.StatusCreated, LogWasteResponse{
		WasteID:    result.WasteID,
		KitchenID:  result.KitchenID,
		QuantityKg: result.QuantityKg,
		CreatedAt:  result.CreatedAt,
	})
}

// Analytics handles GET /waste/analytics.
func (h *WasteHandler) Analytics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kitchenID := q.Get("kitchen_id")
	from := q.Get("from")
	to := q.Get("to")
	period := q.Get("period")
	if period == "" {
		period = "day"
	}

	result, err := h.wasteSvc.Analytics(r.Context(), service.WasteAnalyticsInput{
		KitchenID: kitchenID,
		From:      from,
		To:        to,
		Period:    period,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch analytics", err)
		return
	}

	resp := WasteAnalyticsResponse{
		Period:          result.Period,
		TotalKg:         result.TotalKg,
		ByMealType:      result.ByMealType,
		ByCause:         result.ByCause,
		ByDiversion:     result.ByDiversion,
		ReductionVsLast: result.ReductionVsLast,
	}
	for _, p := range result.DailyTrend {
		resp.DailyTrend = append(resp.DailyTrend, DailyWastePoint{Date: p.Date, Kg: p.Kg, Events: p.Events})
	}
	for _, it := range result.TopItems {
		resp.TopItems = append(resp.TopItems, WasteItemSummary{ItemName: it.ItemName, TotalKg: it.TotalKg, EventCount: it.EventCount})
	}

	writeJSON(w, http.StatusOK, resp)
}
