// Package auth provides JWT minting/verification and bcrypt password helpers
// for the food-waste redistribution platform.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// ─── Errors ───────────────────────────────────────────────────────────────────

var (
	ErrTokenExpired   = errors.New("auth: token expired")
	ErrTokenInvalid   = errors.New("auth: token invalid")
	ErrTokenMalformed = errors.New("auth: token malformed")
)

// ─── Claims ───────────────────────────────────────────────────────────────────

// Claims is the JWT payload used for access tokens.
// Field names match the JSON/wire representation used by the API.
type Claims struct {
	jwt.RegisteredClaims

	// Role is one of: KITCHEN | NGO | LOGISTICS | ADMIN | SYSTEM
	Role string `json:"role"`

	// KitchenID is populated when Role == KITCHEN; empty otherwise.
	KitchenID string `json:"kitchen_id,omitempty"`

	// RecipientID is populated when Role == NGO; empty otherwise.
	RecipientID string `json:"recipient_id,omitempty"`
}

// ─── Token minting ────────────────────────────────────────────────────────────

// MintAccessToken creates a signed HS256 JWT access token.
// userID  → sub claim (must be a valid UUID string)
// role    → role claim
// kitchenID, recipientID → optional scope claims (pass "" when not applicable)
func MintAccessToken(userID, role, kitchenID, recipientID string) (string, error) {
	secret := accessSecret()
	expireMin := accessExpireMin()

	now := time.Now().UTC()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ID:        uuid.NewString(), // jti – unique per token
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(expireMin) * time.Minute)),
			Issuer:    "food-waste-platform",
		},
		Role:        role,
		KitchenID:   kitchenID,
		RecipientID: recipientID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("auth: MintAccessToken sign: %w", err)
	}
	return signed, nil
}

// MintRefreshToken generates a cryptographically random refresh token.
// Returns (rawToken, sha256Hex, error).
//   - rawToken  – 32-byte random, hex-encoded; send to the client as a cookie.
//   - sha256Hex – store this in the database, never the raw token.
func MintRefreshToken() (raw string, sha256hex string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", fmt.Errorf("auth: MintRefreshToken rand: %w", err)
	}
	raw = hex.EncodeToString(b)
	sha256hex = hashSHA256Hex(raw)
	return raw, sha256hex, nil
}

// ─── Token verification ───────────────────────────────────────────────────────

// VerifyAccessToken parses and validates a signed access token string.
// Returns typed errors (ErrTokenExpired, ErrTokenInvalid, ErrTokenMalformed)
// so callers can map them to appropriate HTTP responses.
func VerifyAccessToken(tokenStr string) (*Claims, error) {
	secret := accessSecret()

	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		switch {
		case errors.Is(err, jwt.ErrTokenExpired):
			return nil, ErrTokenExpired
		case errors.Is(err, jwt.ErrTokenMalformed):
			return nil, ErrTokenMalformed
		default:
			return nil, ErrTokenInvalid
		}
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrTokenInvalid
	}
	return claims, nil
}

// ─── Password helpers ─────────────────────────────────────────────────────────

const bcryptCost = 12

// HashPassword returns a bcrypt hash (cost 12) of the plaintext password.
func HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("auth: HashPassword: %w", err)
	}
	return string(h), nil
}

// CheckPassword returns nil if plain matches the stored bcrypt hash,
// or bcrypt.ErrMismatchedHashAndPassword if it does not.
func CheckPassword(hash, plain string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

func accessSecret() string {
	s := os.Getenv("JWT_SECRET")
	if s == "" {
		panic("auth: JWT_SECRET is not set")
	}
	return s
}

func accessExpireMin() int {
	raw := os.Getenv("JWT_EXPIRE_MIN")
	if raw == "" {
		return 15 // sensible default
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return 15
	}
	return v
}
