package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sih26234/backend/internal/service"
)

// RecipientsHandler holds dependencies for recipient listing endpoints.
type RecipientsHandler struct {
	recipientSvc service.RecipientService
}

// NewRecipientsHandler constructs a RecipientsHandler.
func NewRecipientsHandler(recipientSvc service.RecipientService) *RecipientsHandler {
	return &RecipientsHandler{recipientSvc: recipientSvc}
}

// RegisterRoutes mounts recipient routes onto r.
func (h *RecipientsHandler) RegisterRoutes(r chi.Router) {
	r.Get("/recipients", h.ListRecipients)
}

// ─── Response types ───────────────────────────────────────────────────────────

// RecipientItem is one recipient in the list.
type RecipientItem struct {
	RecipientID  string    `json:"recipient_id"`
	Name         string    `json:"name"`
	Type         string    `json:"type"` // ngo | shelter | food_bank | community_kitchen
	ContactName  string    `json:"contact_name,omitempty"`
	ContactPhone string    `json:"contact_phone,omitempty"`
	Address      string    `json:"address,omitempty"`
	Lat          float64   `json:"lat,omitempty"`
	Lng          float64   `json:"lng,omitempty"`
	CapacityKg   float64   `json:"capacity_kg"`
	AcceptsTypes []string  `json:"accepts_types"` // breakfast | lunch | dinner | snacks
	Active       bool      `json:"active"`
	RegisteredAt time.Time `json:"registered_at"`
}

// ListRecipientsResponse is returned by GET /recipients.
type ListRecipientsResponse struct {
	Recipients []RecipientItem `json:"recipients"`
	Total      int             `json:"total"`
	Page       int             `json:"page"`
	Limit      int             `json:"limit"`
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// ListRecipients handles GET /recipients.
//
//	@Summary      List recipients
//	@Description  Return registered food redistribution recipients (NGOs, shelters, food banks).
//	@Tags         recipients
//	@Produce      json
//	@Security     BearerAuth
//	@Param        type       query  string  false  "Filter by type: ngo|shelter|food_bank|community_kitchen"
//	@Param        active     query  bool    false  "Filter by active status"
//	@Param        lat        query  number  false  "Origin latitude for proximity sort"
//	@Param        lng        query  number  false  "Origin longitude for proximity sort"
//	@Param        radius_km  query  number  false  "Radius filter (km)"
//	@Param        page       query  int     false  "Page (default 1)"
//	@Param        limit      query  int     false  "Limit (default 50)"
//	@Success      200 {object} ListRecipientsResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /recipients [get]
func (h *RecipientsHandler) ListRecipients(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rtype := q.Get("type")
	activeOnly, _ := strconv.ParseBool(q.Get("active"))
	lat, _ := strconv.ParseFloat(q.Get("lat"), 64)
	lng, _ := strconv.ParseFloat(q.Get("lng"), 64)
	radiusKm, _ := strconv.ParseFloat(q.Get("radius_km"), 64)
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}

	result, err := h.recipientSvc.ListRecipients(r.Context(), service.ListRecipientsInput{
		Type:       rtype,
		ActiveOnly: activeOnly,
		Lat:        lat,
		Lng:        lng,
		RadiusKm:   radiusKm,
		Page:       page,
		Limit:      limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list recipients", err)
		return
	}

	items := make([]RecipientItem, len(result.Recipients))
	for i, rc := range result.Recipients {
		items[i] = RecipientItem{
			RecipientID:  rc.RecipientID,
			Name:         rc.Name,
			Type:         rc.Type,
			ContactName:  rc.ContactName,
			ContactPhone: rc.ContactPhone,
			Address:      rc.Address,
			Lat:          rc.Lat,
			Lng:          rc.Lng,
			CapacityKg:   rc.CapacityKg,
			AcceptsTypes: rc.AcceptsTypes,
			Active:       rc.Active,
			RegisteredAt: rc.RegisteredAt,
		}
	}

	writeJSON(w, http.StatusOK, ListRecipientsResponse{
		Recipients: items,
		Total:      result.Total,
		Page:       page,
		Limit:      limit,
	})
}
