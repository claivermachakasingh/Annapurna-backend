package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sih26234/backend/internal/domain"
	"github.com/sih26234/backend/internal/service"
)

// QRHandler holds dependencies for QR tracking endpoints.
type QRHandler struct {
	qrSvc service.QRService
}

// NewQRHandler constructs a QRHandler.
func NewQRHandler(qrSvc service.QRService) *QRHandler {
	return &QRHandler{qrSvc: qrSvc}
}

// RegisterRoutes mounts QR routes onto r.
func (h *QRHandler) RegisterRoutes(r chi.Router) {
	r.Post("/qr/{batch_id}/event", h.PostEvent)
	r.Get("/qr/{batch_id}", h.GetBatch)
	r.Get("/qr/{batch_id}/verify", h.Verify)
	r.Get("/qr/{batch_id}/label", h.Label)
}

// ─── Request / Response types ────────────────────────────────────────────────

// QREventRequest is the body for POST /qr/{batch_id}/event.
type QREventRequest struct {
	Event     domain.QREvent `json:"event"`
	Location  string         `json:"location,omitempty"`
	ActorID   string         `json:"actor_id,omitempty"`
	TempC     *float64       `json:"temp_c,omitempty"`
	Notes     string         `json:"notes,omitempty"`
	Timestamp *time.Time     `json:"timestamp,omitempty"`
}

// QREventResponse is returned by POST /qr/{batch_id}/event.
type QREventResponse struct {
	EventID   string         `json:"event_id"`
	BatchID   string         `json:"batch_id"`
	Event     domain.QREvent `json:"event"`
	CreatedAt time.Time      `json:"created_at"`
}

// QRBatchResponse is returned by GET /qr/{batch_id}.
type QRBatchResponse struct {
	BatchID       string           `json:"batch_id"`
	ItemName      string           `json:"item_name"`
	QuantityKg    float64          `json:"quantity_kg"`
	KitchenID     string           `json:"kitchen_id"`
	Status        string           `json:"status"`
	VisualStatus  domain.VisualStatus `json:"visual_status"`
	SafetyDecision domain.SafetyDecision `json:"safety_decision"`
	Events        []QREventRecord  `json:"events"`
	CreatedAt     time.Time        `json:"created_at"`
	ExpiresAt     *time.Time       `json:"expires_at,omitempty"`
}

// QREventRecord is one entry in a batch's event history.
type QREventRecord struct {
	EventID   string         `json:"event_id"`
	Event     domain.QREvent `json:"event"`
	Location  string         `json:"location,omitempty"`
	ActorID   string         `json:"actor_id,omitempty"`
	TempC     *float64       `json:"temp_c,omitempty"`
	Notes     string         `json:"notes,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

// QRVerifyResponse is returned by GET /qr/{batch_id}/verify.
type QRVerifyResponse struct {
	BatchID  string `json:"batch_id"`
	Valid    bool   `json:"valid"`
	Status   string `json:"status"`
	Message  string `json:"message"`
}

// QRLabelResponse is returned by GET /qr/{batch_id}/label.
type QRLabelResponse struct {
	BatchID   string `json:"batch_id"`
	LabelURL  string `json:"label_url"`
	QRCodeURL string `json:"qr_code_url"`
	ItemName  string `json:"item_name"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// PostEvent handles POST /qr/{batch_id}/event.
//
//	@Summary      Record a QR scan event
//	@Description  Append a lifecycle event (packed, dispatched, received, etc.) to a food batch.
//	@Tags         qr
//	@Accept       json
//	@Produce      json
//	@Security     BearerAuth
//	@Param        batch_id path  string        true "Batch ID"
//	@Param        body     body  QREventRequest true "QR event"
//	@Success      201 {object} QREventResponse
//	@Failure      400 {object} ErrorResponse
//	@Failure      404 {object} ErrorResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /qr/{batch_id}/event [post]
func (h *QRHandler) PostEvent(w http.ResponseWriter, r *http.Request) {
	batchID := chi.URLParam(r, "batch_id")
	if batchID == "" {
		writeError(w, http.StatusBadRequest, "batch_id is required", nil)
		return
	}

	var req QREventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if req.Event == "" {
		writeError(w, http.StatusBadRequest, "event is required", nil)
		return
	}

	result, err := h.qrSvc.PostEvent(r.Context(), service.QREventInput{
		BatchID:   batchID,
		Event:     req.Event,
		Location:  req.Location,
		ActorID:   req.ActorID,
		TempC:     req.TempC,
		Notes:     req.Notes,
		Timestamp: req.Timestamp,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record event", err)
		return
	}

	writeJSON(w, http.StatusCreated, QREventResponse{
		EventID:   result.EventID,
		BatchID:   result.BatchID,
		Event:     result.Event,
		CreatedAt: result.CreatedAt,
	})
}

// GetBatch handles GET /qr/{batch_id}.
//
//	@Summary      Get batch detail
//	@Description  Retrieve full batch info and event history for a QR-tracked food batch.
//	@Tags         qr
//	@Produce      json
//	@Security     BearerAuth
//	@Param        batch_id path string true "Batch ID"
//	@Success      200 {object} QRBatchResponse
//	@Failure      404 {object} ErrorResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /qr/{batch_id} [get]
func (h *QRHandler) GetBatch(w http.ResponseWriter, r *http.Request) {
	batchID := chi.URLParam(r, "batch_id")
	if batchID == "" {
		writeError(w, http.StatusBadRequest, "batch_id is required", nil)
		return
	}

	result, err := h.qrSvc.GetBatch(r.Context(), batchID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retrieve batch", err)
		return
	}
	if result == nil {
		writeError(w, http.StatusNotFound, "batch not found", nil)
		return
	}

	events := make([]QREventRecord, len(result.Events))
	for i, ev := range result.Events {
		events[i] = QREventRecord{
			EventID:   ev.EventID,
			Event:     ev.Event,
			Location:  ev.Location,
			ActorID:   ev.ActorID,
			TempC:     ev.TempC,
			Notes:     ev.Notes,
			Timestamp: ev.Timestamp,
		}
	}

	writeJSON(w, http.StatusOK, QRBatchResponse{
		BatchID:        result.BatchID,
		ItemName:       result.ItemName,
		QuantityKg:     result.QuantityKg,
		KitchenID:      result.KitchenID,
		Status:         result.Status,
		VisualStatus:   result.VisualStatus,
		SafetyDecision: result.SafetyDecision,
		Events:         events,
		CreatedAt:      result.CreatedAt,
		ExpiresAt:      result.ExpiresAt,
	})
}

// Verify handles GET /qr/{batch_id}/verify.
//
//	@Summary      Verify batch QR code
//	@Description  Perform a safety and authenticity check on the QR-tagged batch.
//	@Tags         qr
//	@Produce      json
//	@Security     BearerAuth
//	@Param        batch_id path string true "Batch ID"
//	@Success      200 {object} QRVerifyResponse
//	@Failure      404 {object} ErrorResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /qr/{batch_id}/verify [get]
func (h *QRHandler) Verify(w http.ResponseWriter, r *http.Request) {
	batchID := chi.URLParam(r, "batch_id")
	if batchID == "" {
		writeError(w, http.StatusBadRequest, "batch_id is required", nil)
		return
	}

	result, err := h.qrSvc.Verify(r.Context(), batchID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "verification failed", err)
		return
	}

	writeJSON(w, http.StatusOK, QRVerifyResponse{
		BatchID: batchID,
		Valid:   result.Valid,
		Status:  result.Status,
		Message: result.Message,
	})
}

// Label handles GET /qr/{batch_id}/label.
//
//	@Summary      Get printable QR label
//	@Description  Return URLs for a printable QR code label for the food batch.
//	@Tags         qr
//	@Produce      json
//	@Security     BearerAuth
//	@Param        batch_id path string true "Batch ID"
//	@Success      200 {object} QRLabelResponse
//	@Failure      404 {object} ErrorResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /qr/{batch_id}/label [get]
func (h *QRHandler) Label(w http.ResponseWriter, r *http.Request) {
	batchID := chi.URLParam(r, "batch_id")
	if batchID == "" {
		writeError(w, http.StatusBadRequest, "batch_id is required", nil)
		return
	}

	result, err := h.qrSvc.GetLabel(r.Context(), batchID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get label", err)
		return
	}
	if result == nil {
		writeError(w, http.StatusNotFound, "batch not found", nil)
		return
	}

	writeJSON(w, http.StatusOK, QRLabelResponse{
		BatchID:   result.BatchID,
		LabelURL:  result.LabelURL,
		QRCodeURL: result.QRCodeURL,
		ItemName:  result.ItemName,
		ExpiresAt: result.ExpiresAt,
	})
}
