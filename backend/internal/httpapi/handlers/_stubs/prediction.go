package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sih26234/backend/internal/domain"
	"github.com/sih26234/backend/internal/service"
)

// PredictionHandler holds dependencies for prediction endpoints.
type PredictionHandler struct {
	predSvc service.PredictionService
}

// NewPredictionHandler constructs a PredictionHandler.
func NewPredictionHandler(predSvc service.PredictionService) *PredictionHandler {
	return &PredictionHandler{predSvc: predSvc}
}

// RegisterRoutes mounts prediction routes onto r.
func (h *PredictionHandler) RegisterRoutes(r chi.Router) {
	r.Post("/predict-demand", h.PredictDemand)
	r.Post("/planning/what-if", h.WhatIf)
	r.Post("/feedback/outcome", h.RecordOutcome)
}

// ─── Request / Response types ────────────────────────────────────────────────

// PredictDemandRequest is the body for POST /predict-demand.
type PredictDemandRequest struct {
	MealType     domain.MealType `json:"meal_type"`
	Date         string          `json:"date"`
	KitchenID    string          `json:"kitchen_id"`
	GuestCount   *int            `json:"guest_count,omitempty"`
	SpecialEvent *string         `json:"special_event,omitempty"`
}

// PredictDemandResponse is returned by POST /predict-demand.
type PredictDemandResponse struct {
	PredictionID   string          `json:"prediction_id"`
	KitchenID      string          `json:"kitchen_id"`
	MealType       domain.MealType `json:"meal_type"`
	Date           string          `json:"date"`
	PredictedCount int             `json:"predicted_count"`
	ConfidenceLow  int             `json:"confidence_low"`
	ConfidenceHigh int             `json:"confidence_high"`
	ModelVersion   string          `json:"model_version"`
	GeneratedAt    time.Time       `json:"generated_at"`
}

// WhatIfRequest is the body for POST /planning/what-if.
type WhatIfRequest struct {
	KitchenID   string          `json:"kitchen_id"`
	MealType    domain.MealType `json:"meal_type"`
	Date        string          `json:"date"`
	Adjustments map[string]any  `json:"adjustments"`
}

// WhatIfResponse is returned by POST /planning/what-if.
type WhatIfResponse struct {
	ScenarioID      string   `json:"scenario_id"`
	BaselineCount   int      `json:"baseline_count"`
	AdjustedCount   int      `json:"adjusted_count"`
	Delta           int      `json:"delta"`
	WasteReduction  float64  `json:"waste_reduction_kg"`
	CostSaving      float64  `json:"cost_saving"`
	Recommendations []string `json:"recommendations"`
}

// FeedbackOutcomeRequest is the body for POST /feedback/outcome.
type FeedbackOutcomeRequest struct {
	PredictionID string  `json:"prediction_id"`
	ActualCount  int     `json:"actual_count"`
	WasteKg      float64 `json:"waste_kg"`
	Notes        string  `json:"notes,omitempty"`
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// PredictDemand handles POST /predict-demand.
func (h *PredictionHandler) PredictDemand(w http.ResponseWriter, r *http.Request) {
	var req PredictDemandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if req.KitchenID == "" || req.MealType == "" || req.Date == "" {
		writeError(w, http.StatusBadRequest, "kitchen_id, meal_type and date are required", nil)
		return
	}

	result, err := h.predSvc.PredictDemand(r.Context(), service.PredictDemandInput{
		KitchenID:    req.KitchenID,
		MealType:     req.MealType,
		Date:         req.Date,
		GuestCount:   req.GuestCount,
		SpecialEvent: req.SpecialEvent,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "prediction failed", err)
		return
	}

	writeJSON(w, http.StatusOK, PredictDemandResponse{
		PredictionID:   result.PredictionID,
		KitchenID:      result.KitchenID,
		MealType:       result.MealType,
		Date:           result.Date,
		PredictedCount: result.PredictedCount,
		ConfidenceLow:  result.ConfidenceLow,
		ConfidenceHigh: result.ConfidenceHigh,
		ModelVersion:   result.ModelVersion,
		GeneratedAt:    result.GeneratedAt,
	})
}

// WhatIf handles POST /planning/what-if.
func (h *PredictionHandler) WhatIf(w http.ResponseWriter, r *http.Request) {
	var req WhatIfRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if req.KitchenID == "" {
		writeError(w, http.StatusBadRequest, "kitchen_id is required", nil)
		return
	}

	result, err := h.predSvc.WhatIf(r.Context(), service.WhatIfInput{
		KitchenID:   req.KitchenID,
		MealType:    req.MealType,
		Date:        req.Date,
		Adjustments: req.Adjustments,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "what-if simulation failed", err)
		return
	}

	writeJSON(w, http.StatusOK, WhatIfResponse{
		ScenarioID:      result.ScenarioID,
		BaselineCount:   result.BaselineCount,
		AdjustedCount:   result.AdjustedCount,
		Delta:           result.Delta,
		WasteReduction:  result.WasteReductionKg,
		CostSaving:      result.CostSaving,
		Recommendations: result.Recommendations,
	})
}

// RecordOutcome handles POST /feedback/outcome.
func (h *PredictionHandler) RecordOutcome(w http.ResponseWriter, r *http.Request) {
	var req FeedbackOutcomeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if req.PredictionID == "" {
		writeError(w, http.StatusBadRequest, "prediction_id is required", nil)
		return
	}

	if err := h.predSvc.RecordOutcome(r.Context(), service.RecordOutcomeInput{
		PredictionID: req.PredictionID,
		ActualCount:  req.ActualCount,
		WasteKg:      req.WasteKg,
		Notes:        req.Notes,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record outcome", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
