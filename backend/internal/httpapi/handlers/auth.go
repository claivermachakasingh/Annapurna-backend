package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/sih26234/food-waste/internal/auth"
	"github.com/sih26234/food-waste/internal/config"
	"github.com/sih26234/food-waste/internal/httpapi"
	"github.com/sih26234/food-waste/internal/rbac"
	"github.com/sih26234/food-waste/internal/redisx"
	"github.com/sih26234/food-waste/internal/store"
	"github.com/sih26234/food-waste/internal/validation"
)

// authRateLimitWindow is the contract's login/register ceiling of 10 requests
// per minute per IP (08_API_CONTRACT row 1).
const authRateLimitWindow = time.Minute

// loginEmailPredicate matches the login lookup to the users_email_lower_key
// unique index, so a signup and a login agree on what "the same email" means.
const loginEmailPredicate = `lower(email) = $1`

type AuthHandler struct {
	pool    *pgxpool.Pool
	redis   *redis.Client
	cfg     *config.Config
	refresh *store.RefreshTokenStore
}

func NewAuthHandler(pool *pgxpool.Pool, rdb *redis.Client, cfg *config.Config) *AuthHandler {
	return &AuthHandler{pool: pool, redis: rdb, cfg: cfg, refresh: store.NewRefreshTokenStore(pool)}
}

// identity is the user projection returned to clients on register / refresh /
// me. `name` is required by the mobile client, which reads it as a non-null
// string, so it is always emitted (empty string rather than null).
type identity struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// issueTokens persists a refresh token (hash only) and mints a matching access
// token for the user. The raw refresh token is returned to the caller once.
func (h *AuthHandler) issueTokens(ctx context.Context, userID, role, kitchenID, recipientID string) (accessToken, refreshToken string, err error) {
	accessToken, err = auth.MintAccessToken(userID, role, kitchenID, recipientID)
	if err != nil {
		return "", "", err
	}
	refreshToken, err = auth.CreateRefreshToken(ctx, h.refresh, userID)
	if err != nil {
		return "", "", err
	}
	return accessToken, refreshToken, nil
}

// expiresInSeconds reports the real access-token lifetime (JWT_EXPIRE_MIN,
// default 15) so the client refreshes before the token actually dies, instead
// of the previously hardcoded 3600 which outlived the token.
func (h *AuthHandler) expiresInSeconds() int {
	if h.cfg != nil && h.cfg.JWTExpireMin > 0 {
		return h.cfg.JWTExpireMin * 60
	}
	return 900
}

// tokenResponse is the single response shape for every endpoint that hands out
// a token pair (the contract requires refresh to match login).
func (h *AuthHandler) tokenResponse(accessToken, refreshToken string, user identity) map[string]any {
	return map[string]any{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"token_type":    "bearer",
		"expires_in":    h.expiresInSeconds(),
		"user":          user,
	}
}

// loadIdentity reads the profile of an already-authenticated user and derives
// the access-token scope ids from the dedicated kitchen_id / recipient_id
// columns, falling back to org_id for rows seeded before those columns were
// populated.
func (h *AuthHandler) loadIdentity(ctx context.Context, userID string) (identity, string, string, error) {
	var name, email, role string
	var orgID, kitchenID, recipientID *string
	var isActive bool

	err := h.pool.QueryRow(ctx,
		`SELECT COALESCE(name, ''), email, role, org_id, kitchen_id, recipient_id, is_active
		 FROM users WHERE id = $1 LIMIT 1`,
		userID,
	).Scan(&name, &email, &role, &orgID, &kitchenID, &recipientID, &isActive)
	if err != nil {
		return identity{}, "", "", err
	}
	if !isActive {
		return identity{}, "", "", errors.New("account is disabled")
	}

	upperRole := strings.ToUpper(role)
	kitchen, recipient := scopeFor(upperRole, orgID, kitchenID, recipientID)
	return identity{
		ID:    userID,
		Name:  name,
		Email: email,
		Role:  upperRole,
	}, kitchen, recipient, nil
}

// scopeFor resolves the row-scope ids for a role. kitchen_id / recipient_id are
// authoritative; org_id is the legacy fallback.
func scopeFor(role string, orgID, kitchenID, recipientID *string) (kitchen, recipient string) {
	switch strings.ToUpper(role) {
	case rbac.RoleKitchen:
		return firstNonNil(kitchenID, orgID), ""
	case rbac.RoleNGO:
		return "", firstNonNil(recipientID, orgID)
	default:
		return "", ""
	}
}

func firstNonNil(primary, fallback *string) string {
	if primary != nil {
		return *primary
	}
	if fallback != nil {
		return *fallback
	}
	return ""
}

// rateLimitAuth applies the contract's 10 requests/minute/IP ceiling to the
// unauthenticated credential endpoints. It fails open when Redis is down.
func (h *AuthHandler) rateLimitAuth(w http.ResponseWriter, r *http.Request) bool {
	if h.redis == nil {
		return true
	}
	key := "rl:auth:" + clientIP(r)
	allowed, retryAfter, err := redisx.SlidingWindowLimit(r.Context(), h.redis, key, 10, authRateLimitWindow)
	if err != nil || allowed {
		return true
	}
	seconds := int(retryAfter.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	httpapi.NewRateLimited().Render(w)
	return false
}

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// Login exchanges email + password for a token pair.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	if !h.rateLimitAuth(w, r) {
		return
	}

	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.NewValidation("invalid request body", err.Error()).Render(w)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" || req.Password == "" {
		httpapi.NewValidation("email and password are required", "").Render(w)
		return
	}

	var userID, name, passwordHash, role string
	var orgID, kitchenID, recipientID *string
	var isActive bool

	err := h.pool.QueryRow(r.Context(),
		`SELECT id, COALESCE(name, ''), password_hash, role,
		        org_id, kitchen_id, recipient_id, is_active
		 FROM users WHERE `+loginEmailPredicate+` LIMIT 1`,
		email,
	).Scan(&userID, &name, &passwordHash, &role, &orgID, &kitchenID, &recipientID, &isActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpapi.NewUnauthorized("invalid credentials").Render(w)
			return
		}
		httpapi.NewInternal("database error").Render(w)
		return
	}

	if !isActive {
		httpapi.NewUnauthorized("account is disabled").Render(w)
		return
	}

	if err := auth.CheckPassword(passwordHash, req.Password); err != nil {
		httpapi.NewUnauthorized("invalid credentials").Render(w)
		return
	}

	upperRole := strings.ToUpper(role)
	kitchen, recipient := scopeFor(upperRole, orgID, kitchenID, recipientID)

	accessToken, rawRefresh, err := h.issueTokens(r.Context(), userID, upperRole, kitchen, recipient)
	if err != nil {
		httpapi.NewInternal("failed to issue tokens").Render(w)
		return
	}

	writeJSON(w, http.StatusOK, h.tokenResponse(accessToken, rawRefresh, identity{
		ID:    userID,
		Name:  name,
		Email: email,
		Role:  upperRole,
	}))
}

// Register creates a self-service account for any of the four human-facing
// roles and immediately returns a token pair, so the client lands on the
// correct role dashboard instead of a demo page.
//
// KITCHEN and NGO accounts need a scope id (kitchen_id / recipient_id) or every
// kitchen- and NGO-scoped query would return nothing, so signup provisions the
// organisation plus the kitchen or recipient row and links them to the user.
// ADMIN and LOGISTICS are not row-scoped, so they only need the user row.
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	if !h.rateLimitAuth(w, r) {
		return
	}

	var req struct {
		Name             string  `json:"name"`
		Email            string  `json:"email"`
		Password         string  `json:"password"`
		Role             string  `json:"role"`
		OrganisationName string  `json:"organisation_name"`
		KitchenName      string  `json:"kitchen_name"`
		RecipientName    string  `json:"recipient_name"`
		Latitude         float64 `json:"latitude"`
		Longitude        float64 `json:"longitude"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.NewValidation("invalid request body", err.Error()).Render(w)
		return
	}

	input := validation.SignupInput{
		Name:             strings.TrimSpace(req.Name),
		Email:            strings.TrimSpace(req.Email),
		Password:         req.Password,
		Role:             strings.TrimSpace(req.Role),
		OrganisationName: strings.TrimSpace(req.OrganisationName),
		KitchenName:      strings.TrimSpace(req.KitchenName),
		RecipientName:    strings.TrimSpace(req.RecipientName),
		Latitude:         req.Latitude,
		Longitude:        req.Longitude,
	}
	if verr := input.Validate(); verr != nil {
		httpapi.NewValidation(verr.Message, verr.Field).Render(w)
		return
	}
	input.Email = strings.ToLower(input.Email)

	hash, err := auth.HashPassword(input.Password)
	if err != nil {
		httpapi.NewInternal("failed to hash password").Render(w)
		return
	}

	created, err := h.createUser(r.Context(), input, hash)
	if err != nil {
		if errors.Is(err, ErrEmailTaken) {
			httpapi.NewConflict("EMAIL_ALREADY_REGISTERED",
				"an account with this email already exists").Render(w)
			return
		}
		httpapi.NewInternal("failed to create account").Render(w)
		return
	}

	accessToken, rawRefresh, err := h.issueTokens(r.Context(), created.ID, created.Role, created.KitchenID, created.RecipientID)
	if err != nil {
		httpapi.NewInternal("failed to issue tokens").Render(w)
		return
	}

	writeJSON(w, http.StatusCreated, h.tokenResponse(accessToken, rawRefresh, identity{
		ID:    created.ID,
		Name:  created.Name,
		Email: created.Email,
		Role:  created.Role,
	}))
}

// refreshTokenFromRequest accepts the token in the JSON body (mobile) or in
// the refresh_token cookie (web).
func refreshTokenFromRequest(r *http.Request) string {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.RefreshToken != "" {
		return body.RefreshToken
	}
	if c, err := r.Cookie("refresh_token"); err == nil {
		return c.Value
	}
	return ""
}

// Refresh rotates the refresh token (single use) and mints a new access token.
// Presenting an already-rotated token is treated as theft: the whole family is
// revoked and the caller gets 401 REFRESH_TOKEN_REUSED.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	presented := refreshTokenFromRequest(r)
	if presented == "" {
		httpapi.NewUnauthorized("refresh_token is required").Render(w)
		return
	}

	newRaw, userID, err := auth.RotateRefreshToken(r.Context(), h.refresh, presented)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrRefreshTokenRevoked):
			httpapi.NewAppError(httpapi.CodeRefreshTokenReused,
				"refresh token reuse detected; session revoked", http.StatusUnauthorized).Render(w)
		case errors.Is(err, auth.ErrRefreshTokenExpired), errors.Is(err, auth.ErrRefreshTokenNotFound):
			httpapi.NewUnauthorized("invalid or expired refresh token").Render(w)
		default:
			httpapi.NewInternal("could not rotate refresh token").Render(w)
		}
		return
	}

	user, kitchenID, recipientID, err := h.loadIdentity(r.Context(), userID)
	if err != nil {
		httpapi.NewUnauthorized("user not found or disabled").Render(w)
		return
	}

	accessToken, err := auth.MintAccessToken(userID, user.Role, kitchenID, recipientID)
	if err != nil {
		httpapi.NewInternal("failed to mint access token").Render(w)
		return
	}
	h.refresh.TouchLastUsed(r.Context(), presented)

	writeJSON(w, http.StatusOK, h.tokenResponse(accessToken, newRaw, user))
}

// Logout revokes the presented refresh token's family (idempotent).
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if presented := refreshTokenFromRequest(r); presented != "" {
		if err := auth.RevokeFamily(r.Context(), h.refresh, presented); err != nil {
			httpapi.NewInternal("could not revoke refresh token").Render(w)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// LogoutAll revokes every refresh family owned by the authenticated user.
func (h *AuthHandler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}
	if err := h.refresh.RevokeAllForUser(r.Context(), claims.Subject); err != nil {
		httpapi.NewInternal("could not revoke sessions").Render(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Me returns the authenticated user's profile.
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}

	user, _, _, err := h.loadIdentity(r.Context(), claims.Subject)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpapi.NewUnauthorized("user not found").Render(w)
			return
		}
		httpapi.NewUnauthorized("user not found or disabled").Render(w)
		return
	}

	writeJSON(w, http.StatusOK, user)
}

// ─── user creation ────────────────────────────────────────────────────────────

// ErrEmailTaken is returned when the users_email_lower_key unique index rejects
// a duplicate registration.
var ErrEmailTaken = errors.New("handlers: email already registered")

// RegisteredUser is the persisted result of a successful signup.
type RegisteredUser struct {
	ID          string
	Name        string
	Email       string
	Role        string
	KitchenID   string
	RecipientID string
}

// createUser provisions the organisation, the kitchen or recipient row the role
// needs, and finally the user — all in one serializable transaction so a failed
// insert never leaves an orphaned organisation behind.
func (h *AuthHandler) createUser(ctx context.Context, in validation.SignupInput, passwordHash string) (RegisteredUser, error) {
	var out RegisteredUser

	err := store.RunTx(ctx, h.pool, func(ctx context.Context, tx pgx.Tx) error {
		out = RegisteredUser{Name: in.Name, Email: in.Email, Role: in.Role}

		// Reject a duplicate email inside the transaction so two concurrent
		// signups cannot both pass the check and race on the unique index.
		var existing string
		err := tx.QueryRow(ctx, `SELECT id FROM users WHERE lower(email) = $1 LIMIT 1`, in.Email).Scan(&existing)
		switch {
		case err == nil:
			return ErrEmailTaken
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}

		orgName := in.OrganisationName
		if orgName == "" {
			orgName = in.Name + "'s Organisation"
		}
		// Capture the id from the insert rather than looking the row up by
		// name: two people may legitimately pick the same organisation name,
		// and a name lookup would attach the account to someone else's org.
		orgID, err := insertOrganisation(ctx, tx, orgName, organisationTypeFor(in.Role))
		if err != nil {
			return err
		}

		switch in.Role {
		case rbac.RoleKitchen:
			kitchenID, err := insertKitchen(ctx, tx, orgID, orgName, in.KitchenName, in.Latitude, in.Longitude)
			if err != nil {
				return err
			}
			out.KitchenID = kitchenID
		case rbac.RoleNGO:
			recipientID, err := insertRecipient(ctx, tx, orgID, orgName, in.RecipientName, in.Latitude, in.Longitude)
			if err != nil {
				return err
			}
			out.RecipientID = recipientID
		}

		if err := tx.QueryRow(ctx,
			`INSERT INTO users (org_id, kitchen_id, recipient_id, name, email, password_hash, role, is_active)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)
			 RETURNING id`,
			orgID, nullable(out.KitchenID), nullable(out.RecipientID),
			in.Name, in.Email, passwordHash, in.Role,
		).Scan(&out.ID); err != nil {
			if isUniqueViolation(err) {
				return ErrEmailTaken
			}
			return fmt.Errorf("insert user: %w", err)
		}
		return nil
	})

	return out, err
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}

// organisationTypeFor maps a role onto the organisations.type vocabulary the
// seed data uses, so admin listings group new accounts sensibly.
func organisationTypeFor(role string) string {
	switch role {
	case rbac.RoleKitchen:
		return "HOSTEL"
	case rbac.RoleNGO:
		return "NGO"
	default:
		return "OTHER"
	}
}

func insertOrganisation(ctx context.Context, tx pgx.Tx, name, typ string) (string, error) {
	var id string
	err := tx.QueryRow(ctx,
		`INSERT INTO organisations (name, type) VALUES ($1, $2) RETURNING id`,
		name, typ).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert organisation: %w", err)
	}
	return id, nil
}

func insertKitchen(ctx context.Context, tx pgx.Tx, orgID, orgName, kitchenName string, lat, lng float64) (string, error) {
	if kitchenName == "" {
		kitchenName = orgName
	}
	var id string
	err := tx.QueryRow(ctx,
		`INSERT INTO kitchens (org_id, name, type, latitude, longitude)
		 VALUES ($1, $2, 'HOSTEL', $3, $4) RETURNING id`,
		orgID, kitchenName, lat, lng).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert kitchen: %w", err)
	}
	return id, nil
}

func insertRecipient(ctx context.Context, tx pgx.Tx, orgID, orgName, recipientName string, lat, lng float64) (string, error) {
	if recipientName == "" {
		recipientName = orgName
	}
	var id string
	err := tx.QueryRow(ctx,
		`INSERT INTO recipients (org_id, name, type, capacity_kg, latitude, longitude)
		 VALUES ($1, $2, 'NGO', 100, $3, $4) RETURNING id`,
		orgID, recipientName, lat, lng).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert recipient: %w", err)
	}
	return id, nil
}
