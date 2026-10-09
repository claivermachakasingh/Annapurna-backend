package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sih26234/food-waste/internal/config"
	"github.com/sih26234/food-waste/internal/rbac"
)

func ptr(s string) *string { return &s }

// The dedicated kitchen_id / recipient_id columns must win over the legacy
// org_id fallback, otherwise a KITCHEN user created through signup (whose
// org_id points at the organisation) would be scoped to the wrong rows.
func TestScopeForPrefersDedicatedColumns(t *testing.T) {
	org := ptr("org-1")
	kitchen := ptr("kitchen-9")
	recipient := ptr("recipient-7")

	cases := []struct {
		role             string
		org, kit, rec    *string
		wantKit, wantRec string
	}{
		{rbac.RoleKitchen, org, kitchen, nil, "kitchen-9", ""},
		{rbac.RoleNGO, org, nil, recipient, "", "recipient-7"},
		{rbac.RoleKitchen, org, nil, nil, "org-1", ""}, // legacy fallback
		{rbac.RoleNGO, org, nil, nil, "", "org-1"},     // legacy fallback
		{rbac.RoleLogistics, org, nil, nil, "", ""},    // not row-scoped
		{rbac.RoleAdmin, org, kitchen, nil, "", ""},    // not row-scoped
		{rbac.RoleKitchen, nil, nil, nil, "", ""},      // unassigned signup
	}

	for _, tc := range cases {
		gotKit, gotRec := scopeFor(tc.role, tc.org, tc.kit, tc.rec)
		if gotKit != tc.wantKit || gotRec != tc.wantRec {
			t.Errorf("scopeFor(%s) = (%q, %q), want (%q, %q)",
				tc.role, gotKit, gotRec, tc.wantKit, tc.wantRec)
		}
	}
}

// Role comparison must be case-insensitive: the DB CHECK uses upper(role), so
// a legacy lowercase row must still scope correctly.
func TestScopeForIsCaseInsensitive(t *testing.T) {
	kit, rec := scopeFor("kitchen", nil, ptr("k-1"), nil)
	if kit != "k-1" || rec != "" {
		t.Errorf("scopeFor(lowercase kitchen) = (%q, %q)", kit, rec)
	}
}

// Login must resolve the email exactly the way the users_email_lower_key unique
// index does, so two accounts can never differ only by letter case.
func TestLoginUsesLowerEmailLookup(t *testing.T) {
	if !strings.Contains(loginEmailPredicate, "lower(email) = $1") {
		t.Errorf("login query must filter on lower(email) so it matches users_email_lower_key, got %q", loginEmailPredicate)
	}
}

// expires_in must describe the real token lifetime, not a hardcoded 3600, or
// the client holds a token the server already rejected.
func TestExpiresInReflectsConfiguredTTL(t *testing.T) {
	h := &AuthHandler{cfg: &config.Config{JWTExpireMin: 15}}
	if got := h.expiresInSeconds(); got != 900 {
		t.Errorf("expiresInSeconds() = %d, want 900", got)
	}

	h = &AuthHandler{cfg: &config.Config{JWTExpireMin: 60}}
	if got := h.expiresInSeconds(); got != 3600 {
		t.Errorf("expiresInSeconds() = %d, want 3600", got)
	}

	h = &AuthHandler{}
	if got := h.expiresInSeconds(); got != 900 {
		t.Errorf("fallback expiresInSeconds() = %d, want 900", got)
	}
}

// The token response must always carry a user block, because the mobile client
// parses it unconditionally.
func TestTokenResponseAlwaysIncludesUser(t *testing.T) {
	h := &AuthHandler{cfg: &config.Config{JWTExpireMin: 15}}
	resp := h.tokenResponse("access", "refresh", identity{
		ID: "u1", Name: "", Email: "a@b.com", Role: rbac.RoleNGO,
	})

	user, ok := resp["user"].(identity)
	if !ok {
		t.Fatalf("user block missing or wrong type: %#v", resp["user"])
	}
	if user.Name != "" {
		t.Errorf("name = %q, want empty string (never null)", user.Name)
	}
	if resp["token_type"] != "bearer" {
		t.Errorf("token_type = %v, want bearer", resp["token_type"])
	}
}

func TestClientIPExtractsHost(t *testing.T) {
	withPort := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	withPort.RemoteAddr = "203.0.113.7:5555"
	if got := clientIP(withPort); got != "203.0.113.7" {
		t.Errorf("clientIP = %q, want 203.0.113.7", got)
	}

	noPort := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	noPort.RemoteAddr = "unix"
	if got := clientIP(noPort); got != "unix" {
		t.Errorf("clientIP without port = %q, want unix", got)
	}
}
