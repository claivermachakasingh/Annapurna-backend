package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/sih26234/food-waste/internal/httpapi"
	"github.com/sih26234/food-waste/internal/services"
)

// maxSyncItems is the contract bound for POST /sync/batch (D24).
const maxSyncItems = 50

type SyncHandler struct {
	sync *services.SyncService
}

func NewSyncHandler(s *services.SyncService) *SyncHandler {
	return &SyncHandler{sync: s}
}

// Batch replays an offline outbox. Items are processed in order and each gets
// its own ACCEPTED / DUPLICATE / REJECTED result; the actor (and therefore the
// row scoping: kitchen_id, recipient_id) comes from the access token, never
// from the payload.
func (h *SyncHandler) Batch(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}

	var req struct {
		Items []services.SyncItem `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.NewValidation("invalid request", err.Error()).Render(w)
		return
	}
	if len(req.Items) == 0 {
		httpapi.NewValidation("items must not be empty", nil).Render(w)
		return
	}
	if len(req.Items) > maxSyncItems {
		httpapi.NewValidation("sync batch exceeds 50 items", len(req.Items)).Render(w)
		return
	}

	result, err := h.sync.ProcessBatch(r.Context(), claims.Subject, claims.Role, claims.KitchenID, req.Items)
	if err != nil {
		httpapi.NewInternal(err.Error()).Render(w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
