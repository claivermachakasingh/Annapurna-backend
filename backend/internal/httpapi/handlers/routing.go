package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/sih26234/food-waste/internal/httpapi"
	"github.com/sih26234/food-waste/internal/services"
)

type RoutingHandler struct {
	routeSvc *services.RoutingService
}

func NewRoutingHandler(routeSvc *services.RoutingService) *RoutingHandler {
	return &RoutingHandler{routeSvc: routeSvc}
}

// ListAvailable handles drivers fetching available deliveries
func (h *RoutingHandler) ListAvailable(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}
	if !requireRole(w, claims, "DRIVER", "LOGISTICS") {
		return
	}

	deliveries, err := h.routeSvc.ListAvailableDeliveries(r.Context())
	if err != nil {
		renderServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"deliveries": deliveries,
	})
}

// Assign handles a driver claiming a delivery
func (h *RoutingHandler) Assign(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}
	if !requireRole(w, claims, "DRIVER", "LOGISTICS") {
		return
	}

	routeID := chi.URLParam(r, "routeID")
	if routeID == "" {
		httpapi.NewValidation("routeID is required", "").Render(w)
		return
	}

	err := h.routeSvc.AssignDriver(r.Context(), routeID, claims.Subject)
	if err != nil {
		renderServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"route_id": routeID,
		"status":   "assigned",
	})
}

// ListAssigned handles drivers fetching their assigned deliveries
func (h *RoutingHandler) ListAssigned(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}
	if !requireRole(w, claims, "DRIVER", "LOGISTICS") {
		return
	}

	deliveries, err := h.routeSvc.ListAssignedDeliveries(r.Context(), claims.Subject)
	if err != nil {
		renderServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"deliveries": deliveries,
	})
}
