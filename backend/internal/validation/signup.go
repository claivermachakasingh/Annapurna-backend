// Package validation holds the input rules shared by the auth handlers. It is
// deliberately free of HTTP and database concerns so the rules can be unit
// tested directly.
package validation

import (
	"fmt"
	"net/mail"
	"strings"
	"unicode"

	"github.com/sih26234/food-waste/internal/rbac"
)

// FieldError describes a single rejected input field.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

// SignupInput is the validated form of POST /auth/register.
type SignupInput struct {
	Name             string
	Email            string
	Password         string
	Role             string
	OrganisationName string
	KitchenName      string
	RecipientName    string
	Latitude         float64
	Longitude        float64
}

// SignupRoles are the roles a person may choose for themselves. SYSTEM is
// deliberately excluded: it is reserved for service-to-service callers.
var SignupRoles = []string{rbac.RoleKitchen, rbac.RoleNGO, rbac.RoleLogistics, rbac.RoleAdmin}

// Signup coordinate bounds. Latitudes are ±90 and longitudes ±180; signup
// accepts anything inside those ranges because a kitchen or NGO may be
// anywhere, and defaults are applied when the caller sends nothing.
const (
	minLatitude  = -90.0
	maxLatitude  = 90.0
	minLongitude = -180.0
	maxLongitude = 180.0
)

// minPasswordLength is the shortest password accepted at signup. It is a floor
// on top of bcrypt's own 72-byte limit.
const minPasswordLength = 8

// Validate normalises and checks the signup input, returning the first problem
// found. Callers should render the error as a 422 with the field name.
func (s *SignupInput) Validate() *FieldError {
	s.Name = strings.TrimSpace(s.Name)
	s.Email = strings.ToLower(strings.TrimSpace(s.Email))
	s.Role = strings.ToUpper(strings.TrimSpace(s.Role))
	s.OrganisationName = strings.TrimSpace(s.OrganisationName)
	s.KitchenName = strings.TrimSpace(s.KitchenName)
	s.RecipientName = strings.TrimSpace(s.RecipientName)

	if s.Name == "" {
		return &FieldError{"name", "name is required"}
	}
	if len([]rune(s.Name)) > 120 {
		return &FieldError{"name", "name must be 120 characters or fewer"}
	}
	if fe := validateEmail(s.Email); fe != nil {
		return fe
	}
	if fe := validatePassword(s.Password); fe != nil {
		return fe
	}
	if !isSignupRole(s.Role) {
		return &FieldError{"role", fmt.Sprintf("role must be one of %s", strings.Join(SignupRoles, ", "))}
	}
	if s.Latitude < minLatitude || s.Latitude > maxLatitude {
		return &FieldError{"latitude", "latitude must be between -90 and 90"}
	}
	if s.Longitude < minLongitude || s.Longitude > maxLongitude {
		return &FieldError{"longitude", "longitude must be between -180 and 180"}
	}
	return nil
}

func isSignupRole(role string) bool {
	for _, allowed := range SignupRoles {
		if role == allowed {
			return true
		}
	}
	return false
}

// validateEmail mirrors the users_email_check constraint (an "@" past the first
// character) but rejects anything net/mail cannot parse, so a typo fails at
// signup rather than at first login.
func validateEmail(email string) *FieldError {
	if email == "" {
		return &FieldError{"email", "email is required"}
	}
	if len(email) > 254 {
		return &FieldError{"email", "email must be 254 characters or fewer"}
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return &FieldError{"email", "a valid email address is required"}
	}
	if !strings.Contains(email[strings.Index(email, "@"):], ".") {
		return &FieldError{"email", "email must include a domain, for example example.com"}
	}
	return nil
}

// validatePassword enforces the minimum length and requires some variety, so a
// trivially guessable signup password is rejected at the door.
func validatePassword(password string) *FieldError {
	if password == "" {
		return &FieldError{"password", "password is required"}
	}
	if len([]rune(password)) < minPasswordLength {
		return &FieldError{"password", fmt.Sprintf("password must be at least %d characters", minPasswordLength)}
	}
	if len(password) > 72 {
		// bcrypt silently truncates beyond 72 bytes, which would make two
		// different passwords interchangeable. Reject instead.
		return &FieldError{"password", "password must be 72 bytes or fewer"}
	}

	var hasLetter, hasOther bool
	for _, r := range password {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		default:
			hasOther = true
		}
	}
	if !hasLetter || !hasOther {
		return &FieldError{"password", "password must mix letters with digits or symbols"}
	}
	return nil
}
