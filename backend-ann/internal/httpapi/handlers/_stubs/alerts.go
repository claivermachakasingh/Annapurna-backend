package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sih26234/backend/internal/domain"
	"github.com/sih26234/backend/internal/service"
)

// AlertsHandler holds dependencies for alert endpoints.
type AlertsHandler struct {
	alertSvc service.AlertService
}

// NewAlertsHandler constructs an AlertsHandler.
func NewAlertsHandler(alertSvc service.AlertService) *AlertsHandler {
	return &AlertsHandler{alertSvc: alertSvc}
}

// RegisterRoutes mounts alert routes onto r.
func (h *AlertsHandler) RegisterRoutes(r chi.Router) {
	r.Get("/alerts", h.ListAlerts)
	r.Post("/alerts/{id}/ack", h.Acknowledge)
}

// ─── Request / Response types ────────────────────────────────────────────────

// AlertItem is one alert in the list response.
type AlertItem struct {
	AlertID     string            `json:"alert_id"`
	Type        domain.AlertType  `json:"type"`
	Severity    domain.Severity   `json:"severity"`
	Title       string            `json:"title"`
	Message     string            `json:"message"`
	KitchenID   string            `json:"kitchen_id,omitempty"`
	ResourceID  string            `json:"resource_id,omitempty"`
	Acknowledged bool             `json:"acknowledged"`
	AckedBy     string            `json:"acked_by,omitempty"`
	AckedAt     *time.Time        `json:"acked_at,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
}

// ListAlertsResponse is returned by GET /alerts.
type ListAlertsResponse struct {
	Alerts []AlertItem `json:"alerts"`
	Total  int         `json:"total"`
	Page   int         `json:"page"`
	Limit  int         `json:"limit"`
}

// AckRequest is the body for POST /alerts/{id}/ack.
type AckRequest struct {
	Note string `json:"note,omitempty"`
}

// AckResponse is returned by POST /alerts/{id}/ack.
type AckResponse struct {
	AlertID string    `json:"alert_id"`
	AckedAt time.Time `json:"acked_at"`
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// ListAlerts handles GET /alerts.
//
//	@Summary      List alerts
//	@Description  Return active (or all) alerts, filterable by severity and kitchen.
//	@Tags         alerts
//	@Produce      json
//	@Security     BearerAuth
//	@Param        kitchen_id    query  string  false  "Filter by kitchen"
//	@Param        severity      query  string  false  "Filter by severity: low|medium|high|critical"
//	@Param        unacked_only  query  bool    false  "Return only unacknowledged alerts"
//	@Param        page          query  int     false  "Page number (default 1)"
//	@Param        limit         query  int     false  "Page size (default 50)"
//	@Success      200 {object} ListAlertsResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /alerts [get]
func (h *AlertsHandler) ListAlerts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kitchenID := q.Get("kitchen_id")
	severity := q.Get("severity")
	unackedOnly, _ := strconv.ParseBool(q.Get("unacked_only"))
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}

	result, err := h.alertSvc.List(r.Context(), service.ListAlertsInput{
		KitchenID:   kitchenID,
		Severity:    severity,
		UnackedOnly: unackedOnly,
		Page:        page,
		Limit:       limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list alerts", err)
		return
	}

	alerts := make([]AlertItem, len(result.Alerts))
	for i, a := range result.Alerts {
		alerts[i] = AlertItem{
			AlertID:      a.AlertID,
			Type:         a.Type,
			Severity:     a.Severity,
			Title:        a.Title,
			Message:      a.Message,
			KitchenID:    a.KitchenID,
			ResourceID:   a.ResourceID,
			Acknowledged: a.Acknowledged,
			AckedBy:      a.AckedBy,
			AckedAt:      a.AckedAt,
			CreatedAt:    a.CreatedAt,
		}
	}

	writeJSON(w, http.StatusOK, ListAlertsResponse{
		Alerts: alerts,
		Total:  result.Total,
		Page:   page,
		Limit:  limit,
	})
}

// Acknowledge handles POST /alerts/{id}/ack.
//
//	@Summary      Acknowledge an alert
//	@Description  Mark an alert as acknowledged by the calling user.
//	@Tags         alerts
//	@Accept       json
//	@Produce      json
//	@Security     BearerAuth
//	@Param        id   path  string     true  "Alert ID"
//	@Param        body body  AckRequest false "Optional note"
//	@Success      200 {object} AckResponse
//	@Failure      404 {object} ErrorResponse
//	@Failure      409 {object} ErrorResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /alerts/{id}/ack [post]
func (h *AlertsHandler) Acknowledge(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "alert id is required", nil)
		return
	}

	var req AckRequest
	// Body is optional; ignore parse errors silently for empty body.
	_ = json.NewDecoder(r.Body).Decode(&req)

	userID := userIDFromContext(r.Context())

	result, err := h.alertSvc.Acknowledge(r.Context(), service.AckAlertInput{
		AlertID: id,
		UserID:  userID,
		Note:    req.Note,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to acknowledge alert", err)
		return
	}

	writeJSON(w, http.StatusOK, AckResponse{
		AlertID: result.AlertID,
		AckedAt: result.AckedAt,
	})
}
