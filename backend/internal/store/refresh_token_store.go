package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sih26234/food-waste/internal/auth"
)

// RefreshTokenStore is the PostgreSQL-backed implementation of
// auth.RefreshTokenStore. Tokens are persisted as SHA-256 hashes only, so a
// database leak cannot be replayed against the API.
type RefreshTokenStore struct {
	pool *pgxpool.Pool
}

var _ auth.RefreshTokenStore = (*RefreshTokenStore)(nil)

func NewRefreshTokenStore(pool *pgxpool.Pool) *RefreshTokenStore {
	return &RefreshTokenStore{pool: pool}
}

func (s *RefreshTokenStore) InsertRefreshToken(ctx context.Context, row auth.RefreshTokenRow) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, is_revoked, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		row.ID, row.UserID, row.FamilyID, row.TokenHash, row.Revoked, row.ExpiresAt, row.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

func (s *RefreshTokenStore) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (auth.RefreshTokenRow, error) {
	var row auth.RefreshTokenRow
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, family_id, token_hash, is_revoked, expires_at, created_at
		 FROM refresh_tokens WHERE token_hash = $1 LIMIT 1`,
		tokenHash,
	).Scan(&row.ID, &row.UserID, &row.FamilyID, &row.TokenHash, &row.Revoked, &row.ExpiresAt, &row.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.RefreshTokenRow{}, auth.ErrRefreshTokenNotFound
		}
		return auth.RefreshTokenRow{}, fmt.Errorf("get refresh token: %w", err)
	}
	return row, nil
}

// RevokeFamily revokes every token in a family (reuse detection / logout).
func (s *RefreshTokenStore) RevokeFamily(ctx context.Context, familyID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET is_revoked = true WHERE family_id = $1`, familyID)
	if err != nil {
		return fmt.Errorf("revoke refresh family: %w", err)
	}
	return nil
}

// RevokeAllForUser revokes every family a user owns (POST /auth/logout-all).
func (s *RefreshTokenStore) RevokeAllForUser(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET is_revoked = true WHERE user_id = $1 AND is_revoked = false`, userID)
	if err != nil {
		return fmt.Errorf("revoke all refresh tokens: %w", err)
	}
	return nil
}

// DeleteExpiredTokensForUser opportunistically clears stale rows on login.
func (s *RefreshTokenStore) DeleteExpiredTokensForUser(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM refresh_tokens WHERE user_id = $1 AND expires_at < now()`, userID)
	if err != nil {
		return fmt.Errorf("delete expired refresh tokens: %w", err)
	}
	return nil
}

// TouchLastUsed records rotation time; failures are non-fatal for callers.
func (s *RefreshTokenStore) TouchLastUsed(ctx context.Context, tokenHash string) {
	_, _ = s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET last_used_at = now() WHERE token_hash = $1`, tokenHash)
}
