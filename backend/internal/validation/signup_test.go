package validation

import (
	"strings"
	"testing"
)

func validInput() SignupInput {
	return SignupInput{
		Name:     "Meera Kitchen",
		Email:    "Meera@Example.com",
		Password: "sup3rSecret",
		Role:     "kitchen",
	}
}

func TestValidateAcceptsAndNormalises(t *testing.T) {
	in := validInput()
	if err := in.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	if in.Email != "meera@example.com" {
		t.Errorf("email = %q, want lowercased meera@example.com", in.Email)
	}
	if in.Role != "KITCHEN" {
		t.Errorf("role = %q, want KITCHEN", in.Role)
	}
	if in.Name != "Meera Kitchen" {
		t.Errorf("name = %q, want trimmed", in.Name)
	}
}

func TestValidateRejectsBadFields(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*SignupInput)
		wantField string
	}{
		{"empty name", func(s *SignupInput) { s.Name = "   " }, "name"},
		{"long name", func(s *SignupInput) { s.Name = strings.Repeat("x", 121) }, "name"},
		{"empty email", func(s *SignupInput) { s.Email = "" }, "email"},
		{"malformed email", func(s *SignupInput) { s.Email = "not-an-email" }, "email"},
		{"email without domain dot", func(s *SignupInput) { s.Email = "user@localhost" }, "email"},
		{"email with display name", func(s *SignupInput) { s.Email = "Meera <meera@example.com>" }, "email"},
		{"empty password", func(s *SignupInput) { s.Password = "" }, "password"},
		{"short password", func(s *SignupInput) { s.Password = "a1b2" }, "password"},
		{"letters only password", func(s *SignupInput) { s.Password = "abcdefghij" }, "password"},
		{"password over bcrypt limit", func(s *SignupInput) { s.Password = strings.Repeat("a1", 40) }, "password"},
		{"unknown role", func(s *SignupInput) { s.Role = "ROOT" }, "role"},
		{"system role not self-servable", func(s *SignupInput) { s.Role = "SYSTEM" }, "role"},
		{"latitude out of range", func(s *SignupInput) { s.Latitude = 91 }, "latitude"},
		{"longitude out of range", func(s *SignupInput) { s.Longitude = -181 }, "longitude"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			tc.mutate(&in)
			err := in.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want error on %s", tc.wantField)
			}
			if err.Field != tc.wantField {
				t.Errorf("Field = %q, want %q (message: %s)", err.Field, tc.wantField, err.Message)
			}
		})
	}
}

// All four human-facing roles must be selectable at signup.
func TestValidateAllowsEverySelfServiceRole(t *testing.T) {
	for _, role := range SignupRoles {
		in := validInput()
		in.Role = role
		if err := in.Validate(); err != nil {
			t.Errorf("role %s rejected: %v", role, err)
		}
	}
}

// An account that can own data must be able to be created without coordinates;
// the handler falls back to a default location.
func TestValidateAllowsZeroCoordinates(t *testing.T) {
	in := validInput()
	in.Latitude, in.Longitude = 0, 0
	if err := in.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil for zero coordinates", err)
	}
}
