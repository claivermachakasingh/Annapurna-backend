package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sih26234/backend/internal/service"
)

// RoutingHandler holds dependencies for route optimisation endpoints.
type RoutingHandler struct {
	routeSvc service.RoutingService
}

// NewRoutingHandler constructs a RoutingHandler.
func NewRoutingHandler(routeSvc service.RoutingService) *RoutingHandler {
	return &RoutingHandler{routeSvc: routeSvc}
}

// RegisterRoutes mounts routing routes onto r.
func (h *RoutingHandler) RegisterRoutes(r chi.Router) {
	r.Post("/route", h.Optimise)
}

// ─── Request / Response types ────────────────────────────────────────────────

// RouteWaypoint represents a location in the route request/response.
type RouteWaypoint struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	MatchID   string  `json:"match_id,omitempty"`
	WindowEnd string  `json:"window_end,omitempty"` // RFC3339
}

// RouteRequest is the body for POST /route.
type RouteRequest struct {
	VehicleID  string          `json:"vehicle_id"`
	Origin     RouteWaypoint   `json:"origin"`
	Waypoints  []RouteWaypoint `json:"waypoints"`
	MaxKm      *float64        `json:"max_km,omitempty"`
	OptimiseFor string         `json:"optimise_for,omitempty"` // time | distance | emissions
}

// RouteSegment is one leg of the optimised route.
type RouteSegment struct {
	From        RouteWaypoint `json:"from"`
	To          RouteWaypoint `json:"to"`
	DistanceKm  float64       `json:"distance_km"`
	DurationMin int           `json:"duration_min"`
	Polyline    string        `json:"polyline"` // encoded polyline
}

// RouteResponse is returned by POST /route.
type RouteResponse struct {
	RouteID        string         `json:"route_id"`
	VehicleID      string         `json:"vehicle_id"`
	TotalDistanceKm float64       `json:"total_distance_km"`
	TotalDurationMin int          `json:"total_duration_min"`
	CO2SavedKg     float64        `json:"co2_saved_kg"`
	Segments       []RouteSegment `json:"segments"`
	OptimisedAt    time.Time      `json:"optimised_at"`
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// Optimise handles POST /route.
//
//	@Summary      Optimise delivery route
//	@Description  Compute the optimal pickup/delivery sequence for a vehicle given a set of surplus matches.
//	@Tags         routing
//	@Accept       json
//	@Produce      json
//	@Security     BearerAuth
//	@Param        body body RouteRequest true "Route request"
//	@Success      200 {object} RouteResponse
//	@Failure      400 {object} ErrorResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /route [post]
func (h *RoutingHandler) Optimise(w http.ResponseWriter, r *http.Request) {
	var req RouteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if req.VehicleID == "" {
		writeError(w, http.StatusBadRequest, "vehicle_id is required", nil)
		return
	}
	if len(req.Waypoints) == 0 {
		writeError(w, http.StatusBadRequest, "at least one waypoint is required", nil)
		return
	}

	waypoints := make([]service.RouteWaypoint, len(req.Waypoints))
	for i, wp := range req.Waypoints {
		waypoints[i] = service.RouteWaypoint{
			ID:        wp.ID,
			Label:     wp.Label,
			Lat:       wp.Lat,
			Lng:       wp.Lng,
			MatchID:   wp.MatchID,
			WindowEnd: wp.WindowEnd,
		}
	}

	result, err := h.routeSvc.Optimise(r.Context(), service.RouteInput{
		VehicleID: req.VehicleID,
		Origin: service.RouteWaypoint{
			ID:    req.Origin.ID,
			Label: req.Origin.Label,
			Lat:   req.Origin.Lat,
			Lng:   req.Origin.Lng,
		},
		Waypoints:   waypoints,
		MaxKm:       req.MaxKm,
		OptimiseFor: req.OptimiseFor,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "route optimisation failed", err)
		return
	}

	segments := make([]RouteSegment, len(result.Segments))
	for i, seg := range result.Segments {
		segments[i] = RouteSegment{
			From:        RouteWaypoint{ID: seg.From.ID, Label: seg.From.Label, Lat: seg.From.Lat, Lng: seg.From.Lng},
			To:          RouteWaypoint{ID: seg.To.ID, Label: seg.To.Label, Lat: seg.To.Lat, Lng: seg.To.Lng},
			DistanceKm:  seg.DistanceKm,
			DurationMin: seg.DurationMin,
			Polyline:    seg.Polyline,
		}
	}

	writeJSON(w, http.StatusOK, RouteResponse{
		RouteID:          result.RouteID,
		VehicleID:        result.VehicleID,
		TotalDistanceKm:  result.TotalDistanceKm,
		TotalDurationMin: result.TotalDurationMin,
		CO2SavedKg:       result.CO2SavedKg,
		Segments:         segments,
		OptimisedAt:      result.OptimisedAt,
	})
}
