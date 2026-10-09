package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// ─── Errors ───────────────────────────────────────────────────────────────────

var (
	// ErrRefreshTokenNotFound is returned when the provided token hash does not
	// exist in the database (already rotated, never issued, or expired).
	ErrRefreshTokenNotFound = errors.New("auth: refresh token not found")

	// ErrRefreshTokenRevoked is returned when the token family has been revoked,
	// which indicates a possible reuse / theft attempt.
	ErrRefreshTokenRevoked = errors.New("auth: refresh token family revoked (reuse detected)")

	// ErrRefreshTokenExpired is returned when the token TTL has passed.
	ErrRefreshTokenExpired = errors.New("auth: refresh token expired")
)

// ─── Domain types ─────────────────────────────────────────────────────────────

// RefreshTokenRow mirrors the database row for a refresh token.
type RefreshTokenRow struct {
	// ID is the primary key (UUID).
	ID string

	// UserID is the owner of this token.
	UserID string

	// FamilyID groups a chain of rotated tokens together.
	// All tokens in a family share the same FamilyID.
	FamilyID string

	// TokenHash is the SHA-256 hex digest of the raw token string.
	TokenHash string

	// Revoked is true when this family has been invalidated (reuse detected).
	Revoked bool

	// ExpiresAt is the UTC timestamp after which this token is invalid.
	ExpiresAt time.Time

	// CreatedAt is the UTC creation timestamp.
	CreatedAt time.Time
}

// ─── Store interface ──────────────────────────────────────────────────────────

// RefreshTokenStore is the narrow persistence interface required by the
// refresh-token service layer. Callers inject a concrete implementation
// (e.g. a sqlc-generated queries object wrapped in an adapter).
type RefreshTokenStore interface {
	// InsertRefreshToken persists a new refresh token row.
	InsertRefreshToken(ctx context.Context, row RefreshTokenRow) error

	// GetRefreshTokenByHash retrieves a token row by its SHA-256 hash.
	// Returns ErrRefreshTokenNotFound if no row exists.
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (RefreshTokenRow, error)

	// RevokeFamily marks every token in a family as revoked.
	RevokeFamily(ctx context.Context, familyID string) error

	// DeleteExpiredTokensForUser removes all expired tokens for a user.
	// Called opportunistically; errors are non-fatal to callers.
	DeleteExpiredTokensForUser(ctx context.Context, userID string) error
}

// ─── Service functions ────────────────────────────────────────────────────────

// CreateRefreshToken mints a new refresh token, persists it, and returns the
// raw token string to be delivered to the client (e.g. as an HttpOnly cookie).
//
// A new FamilyID is generated, so this always starts a fresh token family.
// Use RotateRefreshToken for subsequent refreshes within the same session.
func CreateRefreshToken(ctx context.Context, store RefreshTokenStore, userID string) (raw string, err error) {
	raw, hash, err := MintRefreshToken()
	if err != nil {
		return "", err
	}

	row := RefreshTokenRow{
		ID:        uuid.NewString(),
		UserID:    userID,
		FamilyID:  uuid.NewString(), // new family for a new login
		TokenHash: hash,
		Revoked:   false,
		ExpiresAt: time.Now().UTC().Add(refreshTTL()),
		CreatedAt: time.Now().UTC(),
	}

	if err = store.InsertRefreshToken(ctx, row); err != nil {
		return "", fmt.Errorf("auth: CreateRefreshToken insert: %w", err)
	}

	// Best-effort cleanup of stale tokens; ignore error.
	_ = store.DeleteExpiredTokensForUser(ctx, userID)

	return raw, nil
}

// RotateRefreshToken implements refresh-token rotation with reuse detection.
//
//  1. Look up the provided raw token by its hash.
//  2. If the family is already revoked → someone is reusing an old token;
//     the entire family has already been revoked.  Return an error.
//  3. If the token itself is expired → return ErrRefreshTokenExpired.
//  4. Revoke the *current* token's family (so it cannot be reused again).
//  5. Mint and persist a fresh token in a NEW family (rotation resets the chain).
//  6. Return the new raw token.
//
// The caller must invalidate the old cookie and set a new one.
func RotateRefreshToken(ctx context.Context, store RefreshTokenStore, rawToken string) (newRaw string, userID string, err error) {
	hash := hashSHA256Hex(rawToken)

	existing, err := store.GetRefreshTokenByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrRefreshTokenNotFound) {
			return "", "", ErrRefreshTokenNotFound
		}
		return "", "", fmt.Errorf("auth: RotateRefreshToken lookup: %w", err)
	}

	// Reuse detection: if the family is already revoked, an old token from this
	// family is being presented – possible theft.  Family is already revoked, so
	// just return the error.
	if existing.Revoked {
		return "", "", ErrRefreshTokenRevoked
	}

	// Expiry check.
	if time.Now().UTC().After(existing.ExpiresAt) {
		// Revoke the family on expiry as a cleanup measure.
		_ = store.RevokeFamily(ctx, existing.FamilyID)
		return "", "", ErrRefreshTokenExpired
	}

	// Invalidate the current family so replay of the just-used token fails.
	if err = store.RevokeFamily(ctx, existing.FamilyID); err != nil {
		return "", "", fmt.Errorf("auth: RotateRefreshToken revoke family: %w", err)
	}

	// Issue a fresh token in a brand-new family.
	newRaw, err = CreateRefreshToken(ctx, store, existing.UserID)
	if err != nil {
		return "", "", err
	}

	return newRaw, existing.UserID, nil
}

// RevokeFamily invalidates all tokens in the family that contains rawToken.
// Use this on explicit logout or when a security event is detected.
func RevokeFamily(ctx context.Context, store RefreshTokenStore, rawToken string) error {
	hash := hashSHA256Hex(rawToken)

	existing, err := store.GetRefreshTokenByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrRefreshTokenNotFound) {
			// Already gone – treat as success.
			return nil
		}
		return fmt.Errorf("auth: RevokeFamily lookup: %w", err)
	}

	if err = store.RevokeFamily(ctx, existing.FamilyID); err != nil {
		return fmt.Errorf("auth: RevokeFamily revoke: %w", err)
	}
	return nil
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

// hashSHA256Hex returns the hex-encoded SHA-256 digest of s.
// Shared by auth.go (MintRefreshToken callers) and this file.
func hashSHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func refreshTTL() time.Duration {
	raw := os.Getenv("REFRESH_EXPIRE_DAYS")
	if raw == "" {
		return 30 * 24 * time.Hour
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days <= 0 {
		return 30 * 24 * time.Hour
	}
	return time.Duration(days) * 24 * time.Hour
}
