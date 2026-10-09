package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/sih26234/food-waste/internal/auth"
	"github.com/sih26234/food-waste/internal/httpapi"
	"github.com/sih26234/food-waste/internal/services"
)

// requireClaims verifies the bearer token and returns its claims. On failure it
// writes a 401 and returns ok=false so the caller can return immediately.
func requireClaims(w http.ResponseWriter, r *http.Request) (*auth.Claims, bool) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		httpapi.NewUnauthorized("missing bearer token").Render(w)
		return nil, false
	}
	claims, err := auth.VerifyAccessToken(strings.TrimPrefix(header, "Bearer "))
	if err != nil {
		httpapi.NewUnauthorized("invalid or expired token").Render(w)
		return nil, false
	}
	return claims, true
}

// requireRole enforces RBAC after requireClaims. SYSTEM is never allowed on
// business endpoints; ADMIN is allowed everywhere.
func requireRole(w http.ResponseWriter, claims *auth.Claims, roles ...string) bool {
	if strings.EqualFold(claims.Role, "ADMIN") {
		return true
	}
	for _, role := range roles {
		if strings.EqualFold(claims.Role, role) {
			return true
		}
	}
	httpapi.NewForbidden("role " + claims.Role + " is not allowed to perform this action").Render(w)
	return false
}

// writeJSON sets the content type, then writes the status and encoded body in
// that order so the header is never lost.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// renderServiceError maps a domain error onto the API error envelope, keeping
// the contract's codes (08_API_CONTRACT) instead of leaking a 500.
func renderServiceError(w http.ResponseWriter, err error) {
	var notFound *services.NotFoundError
	if errors.As(err, &notFound) {
		httpapi.NewNotFound(notFound.Error()).Render(w)
		return
	}

	var invalid *services.ValidationError
	if errors.As(err, &invalid) {
		httpapi.NewValidation(invalid.Message, invalid.Field).Render(w)
		return
	}

	var conflict *services.ConflictError
	if errors.As(err, &conflict) {
		httpapi.NewConflict(conflict.Code, conflict.Message).Render(w)
		return
	}

	var forbidden *services.ForbiddenError
	if errors.As(err, &forbidden) {
		httpapi.NewForbidden(forbidden.Message).Render(w)
		return
	}

	httpapi.NewInternal(err.Error()).Render(w)
}
