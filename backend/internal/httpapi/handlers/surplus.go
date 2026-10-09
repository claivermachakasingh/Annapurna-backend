package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/sih26234/food-waste/internal/httpapi"
	"github.com/sih26234/food-waste/internal/services"
)

type SurplusHandler struct {
	surplus  *services.SurplusService
	approve  *services.ApproveService
	matchSvc *services.MatchingService
}

func NewSurplusHandler(s *services.SurplusService, a *services.ApproveService, m *services.MatchingService) *SurplusHandler {
	return &SurplusHandler{surplus: s, approve: a, matchSvc: m}
}

// Create handles POST /surplus → 201 with the new batch in PENDING_SAFETY.
func (h *SurplusHandler) Create(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}
	if !requireRole(w, claims, "KITCHEN") {
		return
	}

	var req services.CreateSurplusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.NewValidation("invalid request", err.Error()).Render(w)
		return
	}

	batch, err := h.surplus.CreateSurplus(r.Context(), claims.KitchenID, claims.Subject, req)
	if err != nil {
		renderServiceError(w, err)
		return
	}

	if h.matchSvc != nil {
		_, _ = h.matchSvc.Match(r.Context(), batch.ID, 5, claims.KitchenID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(batch)
}

// List handles GET /surplus. KITCHEN users only ever see their own kitchen's
// batches (row scoping); NGO/LOGISTICS/ADMIN see every batch they may act on.
func (h *SurplusHandler) List(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}

	params := services.ListSurplusParams{Limit: 20}
	if claims.Role == "KITCHEN" {
		params.KitchenID = claims.KitchenID
	}
	if limit := r.URL.Query().Get("limit"); limit != "" {
		if n, err := strconv.Atoi(limit); err == nil {
			params.Limit = n
		}
	}
	if status := r.URL.Query().Get("status"); status != "" {
		s := services.SurplusStatus(status)
		params.Status = &s
	}
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		params.Cursor = &cursor
	}

	result, err := h.surplus.ListSurplus(r.Context(), params)
	if err != nil {
		renderServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// Get handles GET /surplus/{id}.
func (h *SurplusHandler) Get(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireClaims(w, r); !ok {
		return
	}
	id := chi.URLParam(r, "id")
	batch, err := h.surplus.GetSurplus(r.Context(), id)
	if err != nil {
		renderServiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(batch)
}

// Approve handles POST /surplus/{id}/approve — always a human decision.
func (h *SurplusHandler) Approve(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}
	if !requireRole(w, claims, "KITCHEN") {
		return
	}

	id := chi.URLParam(r, "id")
	var req services.ApproveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.NewValidation("invalid request", err.Error()).Render(w)
		return
	}
	if req.Decision == "" {
		httpapi.NewValidation("decision is required", "APPROVE|HOLD|REJECT").Render(w)
		return
	}

	if err := h.approve.Approve(r.Context(), id, claims.Subject, claims.Role, req); err != nil {
		renderServiceError(w, err)
		return
	}

	// Return the persisted post-decision state rather than an assumption.
	batch, err := h.surplus.GetSurplus(r.Context(), id)
	if err != nil {
		renderServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"batch_id":      id,
		"status":        batch.Status,
		"safety_status": batch.SafetyStatus,
		"approved_by":   batch.ApprovedBy,
		"approved_at":   batch.ApprovedAt,
	})
}

// Divert handles POST /surplus/{id}/divert (recovery hierarchy: feed/compost/
// biogas). The stream is recorded in diversions when provided.
func (h *SurplusHandler) Divert(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}
	if !requireRole(w, claims, "KITCHEN") {
		return
	}

	id := chi.URLParam(r, "id")
	var req struct {
		Stream     string  `json:"stream"`
		QuantityKg float64 `json:"quantity_kg"`
		Reason     string  `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // body is optional

	if err := h.surplus.Divert(r.Context(), id, claims.Subject, req.Stream, req.QuantityKg, req.Reason); err != nil {
		renderServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"batch_id": id,
		"status":   string(services.StatusDiverted),
		"stream":   req.Stream,
	})
}
