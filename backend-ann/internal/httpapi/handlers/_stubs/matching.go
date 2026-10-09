package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sih26234/backend/internal/domain"
	"github.com/sih26234/backend/internal/service"
)

// MatchingHandler holds dependencies for matching endpoints.
type MatchingHandler struct {
	matchSvc service.MatchingService
}

// NewMatchingHandler constructs a MatchingHandler.
func NewMatchingHandler(matchSvc service.MatchingService) *MatchingHandler {
	return &MatchingHandler{matchSvc: matchSvc}
}

// RegisterRoutes mounts matching routes onto r.
func (h *MatchingHandler) RegisterRoutes(r chi.Router) {
	r.Post("/match", h.CreateMatch)
	r.Post("/matches/{id}/respond", h.Respond)
}

// ─── Request / Response types ────────────────────────────────────────────────

// CreateMatchRequest is the body for POST /match.
type CreateMatchRequest struct {
	SurplusID   string  `json:"surplus_id"`
	RecipientID string  `json:"recipient_id,omitempty"`
	QuantityKg  float64 `json:"quantity_kg"`
	PickupBy    string  `json:"pickup_by"`
	Notes       string  `json:"notes,omitempty"`
}

// CreateMatchResponse is returned by POST /match.
type CreateMatchResponse struct {
	MatchID     string               `json:"match_id"`
	SurplusID   string               `json:"surplus_id"`
	RecipientID string               `json:"recipient_id"`
	QuantityKg  float64              `json:"quantity_kg"`
	Status      domain.SurplusStatus `json:"status"`
	PickupBy    time.Time            `json:"pickup_by"`
	CreatedAt   time.Time            `json:"created_at"`
}

// RespondRequest is the body for POST /matches/{id}/respond.
type RespondRequest struct {
	Accept bool   `json:"accept"`
	Reason string `json:"reason,omitempty"`
}

// RespondResponse is returned by POST /matches/{id}/respond.
type RespondResponse struct {
	MatchID   string               `json:"match_id"`
	Status    domain.SurplusStatus `json:"status"`
	UpdatedAt time.Time            `json:"updated_at"`
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// CreateMatch handles POST /match.
func (h *MatchingHandler) CreateMatch(w http.ResponseWriter, r *http.Request) {
	var req CreateMatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if req.SurplusID == "" || req.QuantityKg <= 0 || req.PickupBy == "" {
		writeError(w, http.StatusBadRequest, "surplus_id, quantity_kg > 0 and pickup_by are required", nil)
		return
	}

	result, err := h.matchSvc.CreateMatch(r.Context(), service.CreateMatchInput{
		SurplusID:   req.SurplusID,
		RecipientID: req.RecipientID,
		QuantityKg:  req.QuantityKg,
		PickupBy:    req.PickupBy,
		Notes:       req.Notes,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create match", err)
		return
	}

	writeJSON(w, http.StatusCreated, CreateMatchResponse{
		MatchID:     result.MatchID,
		SurplusID:   result.SurplusID,
		RecipientID: result.RecipientID,
		QuantityKg:  result.QuantityKg,
		Status:      result.Status,
		PickupBy:    result.PickupBy,
		CreatedAt:   result.CreatedAt,
	})
}

// Respond handles POST /matches/{id}/respond.
func (h *MatchingHandler) Respond(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "match id is required", nil)
		return
	}

	var req RespondRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	result, err := h.matchSvc.Respond(r.Context(), service.MatchRespondInput{
		MatchID: id,
		Accept:  req.Accept,
		Reason:  req.Reason,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to process response", err)
		return
	}

	writeJSON(w, http.StatusOK, RespondResponse{
		MatchID:   result.MatchID,
		Status:    result.Status,
		UpdatedAt: result.UpdatedAt,
	})
}
