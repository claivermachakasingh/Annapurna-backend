// Package rbac provides role constants, chi middleware for role-based access
// control, QR-event role mappings, and context helpers for row-scoped access.
package rbac

import (
	"context"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
)

// ─── Role constants ───────────────────────────────────────────────────────────

const (
	// RoleKitchen is assigned to food-donor kitchen accounts.
	RoleKitchen = "KITCHEN"

	// RoleNGO is assigned to recipient NGO / shelter accounts.
	RoleNGO = "NGO"

	// RoleLogistics is assigned to delivery / driver accounts.
	RoleLogistics = "LOGISTICS"

	// RoleAdmin is assigned to platform administrators.
	RoleAdmin = "ADMIN"

	// RoleSystem is used for internal service-to-service calls.
	RoleSystem = "SYSTEM"
)

// ─── Context keys ─────────────────────────────────────────────────────────────

// contextKey is an unexported type to prevent key collisions across packages.
type contextKey string

const (
	ctxKeyRole        contextKey = "role"
	ctxKeyUserID      contextKey = "user_id"
	ctxKeyKitchenID   contextKey = "kitchen_id"
	ctxKeyRecipientID contextKey = "recipient_id"
)

// ─── QR event role map ────────────────────────────────────────────────────────

// QREventRoleMap maps QR-code event type names to the roles permitted to
// scan / submit that event.  Maintained here so handler layers can look up
// authorised roles without hard-coding them per route.
var QREventRoleMap = map[string][]string{
	// Kitchen confirms food is ready for pickup.
	"pickup_ready": {RoleKitchen, RoleAdmin},

	// Logistics driver confirms pickup from the kitchen.
	"pickup_confirmed": {RoleLogistics, RoleAdmin},

	// Logistics driver confirms delivery to the NGO.
	"delivery_confirmed": {RoleLogistics, RoleAdmin},

	// NGO confirms receipt of the delivery.
	"receipt_confirmed": {RoleNGO, RoleAdmin},

	// Any authorised party can trigger a quality check scan.
	"quality_check": {RoleKitchen, RoleLogistics, RoleNGO, RoleAdmin},
}

// ─── Middleware ───────────────────────────────────────────────────────────────

// RequireRole returns a chi-compatible middleware that allows the request to
// proceed only if the role stored in the context matches one of the provided
// allowed roles.  It expects a prior authentication middleware to have called
// SetClaimsInContext (or equivalent) to populate the context values.
//
// If the role is missing or not in the allowed set it responds with 403.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, ok := r.Context().Value(ctxKeyRole).(string)
			if !ok || role == "" {
				http.Error(w, "forbidden: no role in context", http.StatusForbidden)
				return
			}

			if !slices.Contains(roles, role) {
				http.Error(w, "forbidden: insufficient role", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ─── Context setters ──────────────────────────────────────────────────────────

// SetClaimsInContext stores the auth claims fields into the request context.
// Call this from your JWT authentication middleware after verifying the token.
func SetClaimsInContext(ctx context.Context, userID, role, kitchenID, recipientID string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyUserID, userID)
	ctx = context.WithValue(ctx, ctxKeyRole, role)
	ctx = context.WithValue(ctx, ctxKeyKitchenID, kitchenID)
	ctx = context.WithValue(ctx, ctxKeyRecipientID, recipientID)
	return ctx
}

// ─── Context getters (row-scope helpers) ─────────────────────────────────────

// UserIDFromCtx extracts the authenticated user's ID from the context.
// Returns "" if not set.
func UserIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyUserID).(string)
	return v
}

// RoleFromCtx extracts the authenticated user's role from the context.
// Returns "" if not set.
func RoleFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyRole).(string)
	return v
}

// KitchenIDFromCtx extracts the kitchen_id claim from the context.
// Use this to enforce row-level scope for KITCHEN-role requests.
// Returns "" if not set (i.e. the caller is not a KITCHEN role).
func KitchenIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyKitchenID).(string)
	return v
}

// RecipientIDFromCtx extracts the recipient_id claim from the context.
// Use this to enforce row-level scope for NGO-role requests.
// Returns "" if not set (i.e. the caller is not an NGO role).
func RecipientIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyRecipientID).(string)
	return v
}

// ─── Route helper ─────────────────────────────────────────────────────────────

// URLParam is a thin wrapper around chi.URLParam for use inside handler
// functions that already import this package.
func URLParam(r *http.Request, key string) string {
	return chi.URLParam(r, key)
}
