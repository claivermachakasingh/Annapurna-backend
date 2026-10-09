package handlers

import (
	"net/http"
	"strings"

	"github.com/sih26234/food-waste/internal/auth"
	"github.com/sih26234/food-waste/internal/httpapi"
	"github.com/sih26234/food-waste/internal/sse"
)

type EventsHandler struct {
	hub *sse.Hub
}

func NewEventsHandler(hub *sse.Hub) *EventsHandler {
	return &EventsHandler{hub: hub}
}

// claimsForStream authenticates an SSE request. EventSource cannot set
// headers, so the access token is accepted either in the Authorization header
// (curl, mobile) or in the ?token= query parameter (browser EventSource).
func claimsForStream(r *http.Request) (*auth.Claims, bool) {
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if raw == "" {
		raw = r.URL.Query().Get("token")
	}
	if raw == "" {
		return nil, false
	}
	claims, err := auth.VerifyAccessToken(raw)
	if err != nil {
		return nil, false
	}
	return claims, true
}

// Stream handles GET /events/stream.
//
// The stream is scoped to the caller's own kitchen: a KITCHEN user always
// follows their own kitchen_id from the token (the query parameter is ignored,
// so it cannot be spoofed), while ADMIN may name any kitchen.
func (h *EventsHandler) Stream(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsForStream(r)
	if !ok {
		httpapi.NewUnauthorized("invalid or expired token").Render(w)
		return
	}

	kitchenID := claims.KitchenID
	if strings.EqualFold(claims.Role, "ADMIN") {
		if requested := strings.TrimSpace(r.URL.Query().Get("kitchen_id")); requested != "" {
			kitchenID = requested
		}
	}
	if kitchenID == "" {
		httpapi.NewValidation("no kitchen scope for this account", "kitchen_id").Render(w)
		return
	}

	h.hub.StreamKitchen(w, r, kitchenID)
}
