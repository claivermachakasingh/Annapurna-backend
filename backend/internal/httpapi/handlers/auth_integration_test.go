package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sih26234/food-waste/internal/config"
	"github.com/sih26234/food-waste/internal/rbac"
)

// These tests exercise the real SQL, so they need a migrated database. They are
// skipped when DATABASE_URL is unset so `go test ./...` still works offline.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping database integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("users table is not migrated: %v", err)
	}
	return pool
}

func newTestAuthHandler(t *testing.T, pool *pgxpool.Pool) *AuthHandler {
	t.Helper()
	t.Setenv("JWT_SECRET", "integration-test-secret-long-enough")
	t.Setenv("JWT_EXPIRE_MIN", "15")
	t.Setenv("REFRESH_EXPIRE_DAYS", "30")
	// A nil redis client disables rate limiting for these tests.
	return NewAuthHandler(pool, nil, &config.Config{JWTExpireMin: 15})
}

func postJSON(t *testing.T, h http.HandlerFunc, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.RemoteAddr = "198.51.100.10:1234"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

type authResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	User         struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
		Role  string `json:"role"`
	} `json:"user"`
}

func decodeAuth(t *testing.T, rec *httptest.ResponseRecorder) authResponse {
	t.Helper()
	var out authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return out
}

func uniqueEmail(prefix string) string {
	return prefix + "-" + time.Now().Format("150405.000000000") + "@example.com"
}

// A signup must return a usable token pair whose user block carries the name
// the mobile client renders.
func TestRegisterReturnsTokensAndUser(t *testing.T) {
	pool := testPool(t)
	h := newTestAuthHandler(t, pool)

	email := uniqueEmail("register")
	rec := postJSON(t, h.Register, "/api/v1/auth/register", map[string]any{
		"name":     "Meera Kitchen",
		"email":    email,
		"password": "sup3rSecret",
		"role":     rbac.RoleKitchen,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (%s), want 201", rec.Code, rec.Body.String())
	}

	got := decodeAuth(t, rec)
	if got.AccessToken == "" || got.RefreshToken == "" {
		t.Error("register must return both tokens")
	}
	if got.User.Name != "Meera Kitchen" {
		t.Errorf("user.name = %q, want 'Meera Kitchen'", got.User.Name)
	}
	if got.User.Role != rbac.RoleKitchen {
		t.Errorf("user.role = %q, want KITCHEN", got.User.Role)
	}
	if got.User.Email != email {
		t.Errorf("user.email = %q, want %q", got.User.Email, email)
	}
	if got.ExpiresIn != 900 {
		t.Errorf("expires_in = %d, want 900 (JWT_EXPIRE_MIN=15)", got.ExpiresIn)
	}

	// The kitchen scope must exist, or every kitchen-scoped query returns nothing.
	var kitchenID, recipientID *string
	if err := pool.QueryRow(context.Background(),
		`SELECT kitchen_id, recipient_id FROM users WHERE lower(email) = $1`, email).
		Scan(&kitchenID, &recipientID); err != nil {
		t.Fatalf("read created user: %v", err)
	}
	if kitchenID == nil || *kitchenID == "" {
		t.Error("a KITCHEN signup must be linked to a kitchen")
	}
	if recipientID != nil {
		t.Errorf("kitchen signup should not set recipient_id, got %q", *recipientID)
	}
}

// The credentials returned by register must immediately work at login,
// including when the address is typed in a different case.
func TestRegisterThenLogin(t *testing.T) {
	pool := testPool(t)
	h := newTestAuthHandler(t, pool)

	email := uniqueEmail("roundtrip")
	rec := postJSON(t, h.Register, "/api/v1/auth/register", map[string]any{
		"name":     "Ravi NGO",
		"email":    email,
		"password": "sup3rSecret",
		"role":     rbac.RoleNGO,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d (%s)", rec.Code, rec.Body.String())
	}

	loginRec := postJSON(t, h.Login, "/api/v1/auth/login", map[string]any{
		"email":    upper(email),
		"password": "sup3rSecret",
	})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d (%s), want 200", loginRec.Code, loginRec.Body.String())
	}
	got := decodeAuth(t, loginRec)
	if got.User.Role != rbac.RoleNGO {
		t.Errorf("role = %q, want NGO", got.User.Role)
	}
	if got.User.Name != "Ravi NGO" {
		t.Errorf("name = %q, want 'Ravi NGO' — login must return the name too", got.User.Name)
	}
	if got.AccessToken == "" {
		t.Error("login must return an access token")
	}
}

// An NGO signup must be scoped to a recipient row, otherwise the NGO sees no
// offers.
func TestRegisterNgoProvisionsRecipient(t *testing.T) {
	pool := testPool(t)
	h := newTestAuthHandler(t, pool)

	email := uniqueEmail("ngo")
	rec := postJSON(t, h.Register, "/api/v1/auth/register", map[string]any{
		"name":     "Priya NGO",
		"email":    email,
		"password": "sup3rSecret",
		"role":     rbac.RoleNGO,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}

	var recipientID *string
	if err := pool.QueryRow(context.Background(),
		`SELECT recipient_id FROM users WHERE lower(email) = $1`, email).
		Scan(&recipientID); err != nil {
		t.Fatalf("read created user: %v", err)
	}
	if recipientID == nil || *recipientID == "" {
		t.Error("an NGO signup must be linked to a recipient")
	}
}

// The roles that carry no row scope must still be creatable.
func TestRegisterRolesWithoutScope(t *testing.T) {
	pool := testPool(t)
	h := newTestAuthHandler(t, pool)

	for _, role := range []string{rbac.RoleLogistics, rbac.RoleAdmin} {
		t.Run(role, func(t *testing.T) {
			rec := postJSON(t, h.Register, "/api/v1/auth/register", map[string]any{
				"name":     "Test " + role,
				"email":    uniqueEmail(role),
				"password": "sup3rSecret",
				"role":     role,
			})
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d (%s), want 201", rec.Code, rec.Body.String())
			}
			if got := decodeAuth(t, rec).User.Role; got != role {
				t.Errorf("role = %q, want %q", got, role)
			}
		})
	}
}

func TestRegisterRejectsDuplicateEmail(t *testing.T) {
	pool := testPool(t)
	h := newTestAuthHandler(t, pool)

	email := uniqueEmail("dupe")
	body := map[string]any{
		"name":     "First Account",
		"email":    email,
		"password": "sup3rSecret",
		"role":     rbac.RoleKitchen,
	}
	if rec := postJSON(t, h.Register, "/api/v1/auth/register", body); rec.Code != http.StatusCreated {
		t.Fatalf("first register = %d (%s)", rec.Code, rec.Body.String())
	}

	// Same address, different case: the unique index is on lower(email).
	body["email"] = upper(email)
	body["name"] = "Second Account"
	rec := postJSON(t, h.Register, "/api/v1/auth/register", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate register = %d (%s), want 409", rec.Code, rec.Body.String())
	}
	var env struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Code != "EMAIL_ALREADY_REGISTERED" {
		t.Errorf("code = %q, want EMAIL_ALREADY_REGISTERED", env.Code)
	}
}

func TestRegisterValidationErrors(t *testing.T) {
	pool := testPool(t)
	h := newTestAuthHandler(t, pool)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"bad email", map[string]any{"name": "A", "email": "nope", "password": "sup3rSecret", "role": "KITCHEN"}},
		{"short password", map[string]any{"name": "A", "email": uniqueEmail("short"), "password": "abc", "role": "KITCHEN"}},
		{"system role", map[string]any{"name": "A", "email": uniqueEmail("sys"), "password": "sup3rSecret", "role": "SYSTEM"}},
		{"missing name", map[string]any{"email": uniqueEmail("noname"), "password": "sup3rSecret", "role": "KITCHEN"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postJSON(t, h.Register, "/api/v1/auth/register", tc.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d (%s), want 422", rec.Code, rec.Body.String())
			}
		})
	}
}

// Login must not reveal whether the address exists.
func TestLoginUnknownEmailIsUnauthorized(t *testing.T) {
	pool := testPool(t)
	h := newTestAuthHandler(t, pool)

	rec := postJSON(t, h.Login, "/api/v1/auth/login", map[string]any{
		"email":    uniqueEmail("nobody"),
		"password": "sup3rSecret",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d (%s), want 401", rec.Code, rec.Body.String())
	}
}

func TestLoginWrongPasswordIsUnauthorized(t *testing.T) {
	pool := testPool(t)
	h := newTestAuthHandler(t, pool)

	email := uniqueEmail("wrongpw")
	if rec := postJSON(t, h.Register, "/api/v1/auth/register", map[string]any{
		"name": "Wrong Password", "email": email, "password": "sup3rSecret", "role": rbac.RoleKitchen,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("register = %d (%s)", rec.Code, rec.Body.String())
	}

	rec := postJSON(t, h.Login, "/api/v1/auth/login", map[string]any{
		"email": email, "password": "not-the-password",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d (%s), want 401", rec.Code, rec.Body.String())
	}
}

// /auth/me must work with the token register just issued.
func TestMeAfterRegister(t *testing.T) {
	pool := testPool(t)
	h := newTestAuthHandler(t, pool)

	email := uniqueEmail("me")
	rec := postJSON(t, h.Register, "/api/v1/auth/register", map[string]any{
		"name": "Me Tester", "email": email, "password": "sup3rSecret", "role": rbac.RoleKitchen,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register = %d (%s)", rec.Code, rec.Body.String())
	}
	tokens := decodeAuth(t, rec)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	meRec := httptest.NewRecorder()
	h.Me(meRec, req)

	if meRec.Code != http.StatusOK {
		t.Fatalf("me status = %d (%s)", meRec.Code, meRec.Body.String())
	}
	var me struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.Unmarshal(meRec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.Name != "Me Tester" || me.Email != email || me.Role != rbac.RoleKitchen {
		t.Errorf("me = %+v, want the registered profile", me)
	}
}

// The token pair from register must survive a refresh round trip.
func TestRefreshAfterRegister(t *testing.T) {
	pool := testPool(t)
	h := newTestAuthHandler(t, pool)

	rec := postJSON(t, h.Register, "/api/v1/auth/register", map[string]any{
		"name": "Refresh Tester", "email": uniqueEmail("refresh"), "password": "sup3rSecret", "role": rbac.RoleAdmin,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register = %d (%s)", rec.Code, rec.Body.String())
	}
	first := decodeAuth(t, rec)

	refreshRec := postJSON(t, h.Refresh, "/api/v1/auth/refresh", map[string]any{
		"refresh_token": first.RefreshToken,
	})
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh = %d (%s)", refreshRec.Code, refreshRec.Body.String())
	}
	second := decodeAuth(t, refreshRec)
	if second.AccessToken == "" {
		t.Error("refresh must return a new access token")
	}
	if second.User.Role != rbac.RoleAdmin {
		t.Errorf("refresh user.role = %q, want ADMIN", second.User.Role)
	}
	if second.User.Name != "Refresh Tester" {
		t.Errorf("refresh user.name = %q — refresh must include the user block", second.User.Name)
	}
}

func upper(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'a' && r <= 'z' {
			out[i] = r - 32
		}
	}
	return string(out)
}
