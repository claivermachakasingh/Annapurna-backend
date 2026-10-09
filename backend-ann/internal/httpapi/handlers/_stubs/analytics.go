package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sih26234/backend/internal/service"
)

// AnalyticsHandler holds dependencies for impact analytics endpoints.
type AnalyticsHandler struct {
	analyticsSvc service.AnalyticsService
}

// NewAnalyticsHandler constructs an AnalyticsHandler.
func NewAnalyticsHandler(analyticsSvc service.AnalyticsService) *AnalyticsHandler {
	return &AnalyticsHandler{analyticsSvc: analyticsSvc}
}

// RegisterRoutes mounts analytics routes onto r.
func (h *AnalyticsHandler) RegisterRoutes(r chi.Router) {
	r.Get("/analytics/impact", h.Impact)
}

// ─── Response types ───────────────────────────────────────────────────────────

// SDGContribution maps a UN SDG number to the impact score.
type SDGContribution struct {
	Goal        int     `json:"goal"`
	Label       string  `json:"label"`
	Score       float64 `json:"score"`
	Description string  `json:"description"`
}

// ImpactResponse is returned by GET /analytics/impact.
type ImpactResponse struct {
	Period              string            `json:"period"`
	From                time.Time         `json:"from"`
	To                  time.Time         `json:"to"`
	TotalWasteKg        float64           `json:"total_waste_kg"`
	RedirectedKg        float64           `json:"redirected_kg"`
	CompostedKg         float64           `json:"composted_kg"`
	CO2SavedKg          float64           `json:"co2_saved_kg"`
	WaterSavedLitres    float64           `json:"water_saved_litres"`
	MealsProvided       int               `json:"meals_provided"`
	CostSaved           float64           `json:"cost_saved"`
	DiversionRate       float64           `json:"diversion_rate_pct"`
	SDGContributions    []SDGContribution `json:"sdg_contributions"`
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// Impact handles GET /analytics/impact.
//
//	@Summary      Impact analytics
//	@Description  Return aggregate environmental and social impact metrics including SDG alignment.
//	@Tags         analytics
//	@Produce      json
//	@Security     BearerAuth
//	@Param        kitchen_id query string false "Filter by kitchen"
//	@Param        from       query string false "Start date (YYYY-MM-DD)"
//	@Param        to         query string false "End date (YYYY-MM-DD)"
//	@Param        period     query string false "Aggregation period: day|week|month|year"
//	@Success      200 {object} ImpactResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /analytics/impact [get]
func (h *AnalyticsHandler) Impact(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kitchenID := q.Get("kitchen_id")
	from := q.Get("from")
	to := q.Get("to")
	period := q.Get("period")
	if period == "" {
		period = "month"
	}

	result, err := h.analyticsSvc.Impact(r.Context(), service.ImpactInput{
		KitchenID: kitchenID,
		From:      from,
		To:        to,
		Period:    period,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute impact metrics", err)
		return
	}

	sdg := make([]SDGContribution, len(result.SDGContributions))
	for i, s := range result.SDGContributions {
		sdg[i] = SDGContribution{
			Goal:        s.Goal,
			Label:       s.Label,
			Score:       s.Score,
			Description: s.Description,
		}
	}

	writeJSON(w, http.StatusOK, ImpactResponse{
		Period:           result.Period,
		From:             result.From,
		To:               result.To,
		TotalWasteKg:     result.TotalWasteKg,
		RedirectedKg:     result.RedirectedKg,
		CompostedKg:      result.CompostedKg,
		CO2SavedKg:       result.CO2SavedKg,
		WaterSavedLitres: result.WaterSavedLitres,
		MealsProvided:    result.MealsProvided,
		CostSaved:        result.CostSaved,
		DiversionRate:    result.DiversionRate,
		SDGContributions: sdg,
	})
}
