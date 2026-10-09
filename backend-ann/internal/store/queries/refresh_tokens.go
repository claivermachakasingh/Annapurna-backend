package queries

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ────────────────────────────────────────────────────────────────────────────
// Domain types
// ────────────────────────────────────────────────────────────────────────────

// RefreshToken mirrors the refresh_tokens table.
type RefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash string // SHA-256 hex of the raw token sent to the client
	FamilyID  uuid.UUID
	IsRevoked bool
	ExpiresAt time.Time
	CreatedAt time.Time
}

// CreateRefreshTokenParams holds insert fields.
type CreateRefreshTokenParams struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	FamilyID  uuid.UUID
	ExpiresAt time.Time
}

// ────────────────────────────────────────────────────────────────────────────
// Queries
// ────────────────────────────────────────────────────────────────────────────

const createRefreshTokenSQL = `
INSERT INTO refresh_tokens
    (id, user_id, token_hash, family_id, is_revoked, expires_at, created_at)
VALUES ($1, $2, $3, $4, FALSE, $5, NOW())
RETURNING id, user_id, token_hash, family_id, is_revoked, expires_at, created_at`

// CreateRefreshToken inserts a new refresh token record.
func CreateRefreshToken(ctx context.Context, db DBTX, p CreateRefreshTokenParams) (RefreshToken, error) {
	row := db.QueryRow(ctx, createRefreshTokenSQL,
		p.ID, p.UserID, p.TokenHash, p.FamilyID, p.ExpiresAt,
	)
	return scanRefreshToken(row)
}

const getRefreshTokenByHashSQL = `
SELECT id, user_id, token_hash, family_id, is_revoked, expires_at, created_at
FROM refresh_tokens
WHERE token_hash = $1
LIMIT 1`

// GetRefreshTokenByHash looks up a refresh token by its SHA-256 hash.
// Returns pgx.ErrNoRows when not found.
func GetRefreshTokenByHash(ctx context.Context, db DBTX, tokenHash string) (RefreshToken, error) {
	row := db.QueryRow(ctx, getRefreshTokenByHashSQL, tokenHash)
	return scanRefreshToken(row)
}

const markRefreshTokenRevokedSQL = `
UPDATE refresh_tokens
SET is_revoked = TRUE
WHERE id = $1`

// MarkRefreshTokenRevoked marks a single token as revoked (soft delete).
func MarkRefreshTokenRevoked(ctx context.Context, db DBTX, id uuid.UUID) error {
	_, err := db.Exec(ctx, markRefreshTokenRevokedSQL, id)
	if err != nil {
		return fmt.Errorf("queries.MarkRefreshTokenRevoked: %w", err)
	}
	return nil
}

const revokeTokenFamilySQL = `
UPDATE refresh_tokens
SET is_revoked = TRUE
WHERE family_id = $1`

// RevokeTokenFamily revokes every token that belongs to the given family.
// Called on token-reuse detection (theft response).
func RevokeTokenFamily(ctx context.Context, db DBTX, familyID uuid.UUID) error {
	_, err := db.Exec(ctx, revokeTokenFamilySQL, familyID)
	if err != nil {
		return fmt.Errorf("queries.RevokeTokenFamily: %w", err)
	}
	return nil
}

const deleteExpiredTokensSQL = `
DELETE FROM refresh_tokens
WHERE expires_at < NOW()`

// DeleteExpiredTokens hard-deletes rows that have passed their expiry time.
// Intended for periodic housekeeping.
func DeleteExpiredTokens(ctx context.Context, db DBTX) (int64, error) {
	tag, err := db.Exec(ctx, deleteExpiredTokensSQL)
	if err != nil {
		return 0, fmt.Errorf("queries.DeleteExpiredTokens: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func scanRefreshToken(row pgx.Row) (RefreshToken, error) {
	var t RefreshToken
	err := row.Scan(
		&t.ID, &t.UserID, &t.TokenHash,
		&t.FamilyID, &t.IsRevoked,
		&t.ExpiresAt, &t.CreatedAt,
	)
	if err != nil {
		return RefreshToken{}, fmt.Errorf("queries.scanRefreshToken: %w", err)
	}
	return t, nil
}
