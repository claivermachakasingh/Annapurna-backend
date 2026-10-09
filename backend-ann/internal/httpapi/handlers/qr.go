package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sih26234/food-waste/internal/httpapi"
	"github.com/sih26234/food-waste/internal/services"
)

// QRHandler exposes the append-only QR hash chain (API contract rows 22–24).
type QRHandler struct {
	qr *services.QRService
}

func NewQRHandler(qr *services.QRService) *QRHandler {
	return &QRHandler{qr: qr}
}

// eventTypes is the closed vocabulary enforced by the qr_events CHECK
// constraint; validating here turns a 500 into a 422.
var eventTypes = map[string]bool{
	"CREATED":    true,
	"APPROVED":   true,
	"MATCHED":    true,
	"PICKED_UP":  true,
	"HANDED_OFF": true,
	"RECEIVED":   true,
}

// systemEventTypes are produced by the platform flows (create/approve/match),
// so a human caller may not fabricate them through this endpoint.
var systemEventTypes = map[string]bool{"CREATED": true, "APPROVED": true, "MATCHED": true}

type recordEventRequest struct {
	EventType     string   `json:"event_type"`
	Lat           *float64 `json:"lat"`
	Lng           *float64 `json:"lng"`
	EvidenceHash  *string  `json:"evidence_hash"`
	ClientEventID *string  `json:"client_event_id"`
	ClientTS      *string  `json:"client_ts"`
}

// RecordEvent handles POST /qr/{batch_id}/event.
//
// client_event_id/client_ts are provenance only and are never hashed (D24);
// replaying the same client_event_id returns the already-recorded event.
func (h *QRHandler) RecordEvent(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}

	batchID := strings.TrimSpace(chi.URLParam(r, "batchID"))
	if batchID == "" {
		httpapi.NewValidation("batch_id is required", "").Render(w)
		return
	}

	var req recordEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.NewValidation("invalid request", err.Error()).Render(w)
		return
	}
	req.EventType = strings.ToUpper(strings.TrimSpace(req.EventType))
	if !eventTypes[req.EventType] {
		httpapi.NewValidation("unknown event_type",
			"event_type must be one of CREATED, APPROVED, MATCHED, PICKED_UP, HANDED_OFF, RECEIVED").Render(w)
		return
	}
	if systemEventTypes[req.EventType] && !requireRole(w, claims, "KITCHEN", "ADMIN") {
		return
	}
	if (req.Lat == nil) != (req.Lng == nil) {
		httpapi.NewValidation("invalid coordinates", "lat and lng must be supplied together").Render(w)
		return
	}
	if req.Lat != nil && (*req.Lat < -90 || *req.Lat > 90) {
		httpapi.NewValidation("invalid coordinates", "lat must be between -90 and 90").Render(w)
		return
	}
	if req.Lng != nil && (*req.Lng < -180 || *req.Lng > 180) {
		httpapi.NewValidation("invalid coordinates", "lng must be between -180 and 180").Render(w)
		return
	}

	var clientTS *time.Time
	if req.ClientTS != nil && *req.ClientTS != "" {
		raw := *req.ClientTS
		ts, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			ts, err = time.Parse(time.RFC3339, raw)
		}
		if err != nil {
			ts, err = time.Parse("2006-01-02T15:04:05.999999999", raw)
		}
		if err != nil {
			ts, err = time.Parse("2006-01-02T15:04:05", raw)
		}
		if err != nil {
			now := time.Now().UTC()
			clientTS = &now
		} else {
			ts = ts.UTC()
			clientTS = &ts
		}
	}

	if _, err := h.qr.GetTimeline(r.Context(), batchID); err != nil {
		renderServiceError(w, err)
		return
	}

	res, err := h.qr.RecordEvent(r.Context(), batchID, claims.Subject, claims.Role, req.EventType,
		req.Lat, req.Lng, req.EvidenceHash, req.ClientEventID, clientTS)
	if err != nil {
		renderServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(res)
}

type timelineEvent struct {
	ID           string     `json:"id"`
	EventType    string     `json:"event_type"`
	ActorID      string     `json:"actor_id,omitempty"`
	Lat          *float64   `json:"lat,omitempty"`
	Lng          *float64   `json:"lng,omitempty"`
	EvidenceHash *string    `json:"evidence_hash,omitempty"`
	PrevHash     string     `json:"prev_hash"`
	Hash         string     `json:"hash"`
	ClientTS     *time.Time `json:"captured_at,omitempty"`
	ServerTS     time.Time  `json:"server_ts"`
	CreatedAt    time.Time  `json:"created_at"`
}

type timelineResponse struct {
	BatchID    string          `json:"batch_id"`
	EventCount int             `json:"event_count"`
	ChainValid bool            `json:"chain_valid"`
	BrokenAt   *int            `json:"broken_at,omitempty"`
	Events     []timelineEvent `json:"events"`
}

// Get handles GET /qr/{batch_id} → the ordered timeline. client_ts is surfaced
// as "captured_at" exactly as the contract requires.
func (h *QRHandler) Get(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireClaims(w, r); !ok {
		return
	}

	batchID := strings.TrimSpace(chi.URLParam(r, "batchID"))
	events, err := h.qr.GetTimeline(r.Context(), batchID)
	if err != nil {
		renderServiceError(w, err)
		return
	}

	valid, brokenAt, err := h.qr.VerifyChain(r.Context(), batchID)
	if err != nil {
		renderServiceError(w, err)
		return
	}

	out := timelineResponse{
		BatchID:    batchID,
		EventCount: len(events),
		ChainValid: valid,
		BrokenAt:   brokenAt,
		Events:     make([]timelineEvent, 0, len(events)),
	}
	for _, e := range events {
		hash := e.Hash
		if hash == "" {
			hash = e.EventHash
		}
		serverTS := e.CreatedAt
		if e.ServerTSIso != "" {
			if ts, perr := time.Parse(time.RFC3339, e.ServerTSIso); perr == nil {
				serverTS = ts
			}
		}
		out.Events = append(out.Events, timelineEvent{
			ID:           e.ID,
			EventType:    e.EventType,
			ActorID:      e.ActorID,
			Lat:          e.Lat,
			Lng:          e.Lng,
			EvidenceHash: e.EvidenceHash,
			PrevHash:     e.PrevHash,
			Hash:         hash,
			ClientTS:     e.ClientTS,
			ServerTS:     serverTS,
			CreatedAt:    e.CreatedAt,
		})
	}

	writeJSON(w, http.StatusOK, out)
}

type verifyResponse struct {
	BatchID    string    `json:"batch_id"`
	Valid      bool      `json:"valid"`
	BrokenAt   *int      `json:"broken_at,omitempty"`
	EventCount int       `json:"event_count"`
	VerifiedAt time.Time `json:"verified_at"`
}

// Verify handles GET /qr/{batch_id}/verify → integrity of the whole chain.
func (h *QRHandler) Verify(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireClaims(w, r); !ok {
		return
	}

	batchID := strings.TrimSpace(chi.URLParam(r, "batchID"))
	events, err := h.qr.GetTimeline(r.Context(), batchID)
	if err != nil {
		renderServiceError(w, err)
		return
	}
	valid, brokenAt, err := h.qr.VerifyChain(r.Context(), batchID)
	if err != nil {
		renderServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, verifyResponse{
		BatchID:    batchID,
		Valid:      valid,
		BrokenAt:   brokenAt,
		EventCount: len(events),
		VerifiedAt: time.Now().UTC(),
	})
}
