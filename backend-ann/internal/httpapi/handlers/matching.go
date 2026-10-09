package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/sih26234/food-waste/internal/httpapi"
	"github.com/sih26234/food-waste/internal/services"
)

type MatchingHandler struct {
	matchSvc *services.MatchingService
	routeSvc *services.RoutingService
}

func NewMatchingHandler(matchSvc *services.MatchingService, routeSvc *services.RoutingService) *MatchingHandler {
	return &MatchingHandler{matchSvc: matchSvc, routeSvc: routeSvc}
}

// Match triggers finding NGOs for a surplus batch
func (h *MatchingHandler) Match(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}
	if !requireRole(w, claims, "KITCHEN") {
		return
	}

	batchID := chi.URLParam(r, "batchID")
	if batchID == "" {
		httpapi.NewValidation("batchID is required", "").Render(w)
		return
	}
	
	maxResults := 5
	if maxStr := r.URL.Query().Get("max"); maxStr != "" {
		if m, err := strconv.Atoi(maxStr); err == nil && m > 0 {
			maxResults = m
		}
	}

	matches, err := h.matchSvc.Match(r.Context(), batchID, maxResults, claims.KitchenID)
	if err != nil {
		renderServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"matches": matches,
		"items":   matches,
	})
}

// List handles NGOs listing matches offered to them
func (h *MatchingHandler) List(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}
	if !requireRole(w, claims, "NGO") {
		return
	}
	
	if claims.RecipientID == "" {
		httpapi.NewValidation("NGO recipient ID is missing from token", "").Render(w)
		return
	}
	
	status := r.URL.Query().Get("status")
	
	matches, err := h.matchSvc.ListMatches(r.Context(), claims.RecipientID, status)
	if err != nil {
		renderServiceError(w, err)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"matches": matches,
		"items":   matches,
	})
}

// Respond allows NGO to ACCEPT or REJECT a match
func (h *MatchingHandler) Respond(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}
	if !requireRole(w, claims, "NGO") {
		return
	}
	
	matchID := chi.URLParam(r, "matchID")
	if matchID == "" {
		matchID = chi.URLParam(r, "id")
	}
	if matchID == "" {
		httpapi.NewValidation("matchID is required", "").Render(w)
		return
	}
	
	var req struct {
		Decision string `json:"decision"`
		Action   string `json:"action"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	
	decision := strings.ToUpper(strings.TrimSpace(req.Decision))
	if decision == "" {
		decision = strings.ToUpper(strings.TrimSpace(req.Action))
	}
	if decision == "ACCEPT" {
		decision = "ACCEPTED"
	} else if decision == "DECLINE" || decision == "REJECT" {
		decision = "DECLINED"
	}
	
	if decision != "ACCEPTED" && decision != "DECLINED" && decision != "REJECTED" {
		httpapi.NewValidation("decision must be ACCEPTED or DECLINED", "").Render(w)
		return
	}
	
	batchID, err := h.matchSvc.Respond(r.Context(), matchID, decision)
	if err != nil {
		renderServiceError(w, err)
		return
	}
	
	if decision == "ACCEPTED" && h.routeSvc != nil {
		err = h.routeSvc.CreateRoute(r.Context(), batchID, claims.RecipientID)
		if err != nil {
			// Log this error but still return success for matching since it succeeded.
			fmt.Printf("Warning: failed to create route: %v\n", err)
		}
	}
	
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"match_id": matchID,
		"status":   decision,
	})
}
