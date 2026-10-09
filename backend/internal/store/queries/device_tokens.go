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

// DeviceToken mirrors the device_tokens table used for push notifications.
type DeviceToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Token     string // FCM / APNs device token
	Platform  string // ios | android | web
	IsValid   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UpsertDeviceTokenParams holds fields for an upsert.
type UpsertDeviceTokenParams struct {
	ID       uuid.UUID
	UserID   uuid.UUID
	Token    string
	Platform string
}

// ────────────────────────────────────────────────────────────────────────────
// Queries
// ────────────────────────────────────────────────────────────────────────────

const upsertDeviceTokenSQL = `
INSERT INTO device_tokens (id, user_id, token, platform, is_valid, created_at, updated_at)
VALUES ($1, $2, $3, $4, TRUE, NOW(), NOW())
ON CONFLICT (token) DO UPDATE
    SET user_id    = EXCLUDED.user_id,
        platform   = EXCLUDED.platform,
        is_valid   = TRUE,
        updated_at = NOW()
RETURNING id, user_id, token, platform, is_valid, created_at, updated_at`

// UpsertDeviceToken inserts or updates a device token.  If the token already
// exists it is re-associated to the given user and marked valid.
func UpsertDeviceToken(ctx context.Context, db DBTX, p UpsertDeviceTokenParams) (DeviceToken, error) {
	row := db.QueryRow(ctx, upsertDeviceTokenSQL,
		p.ID, p.UserID, p.Token, p.Platform,
	)
	return scanDeviceToken(row)
}

const getDeviceTokensByUserIDsSQL = `
SELECT id, user_id, token, platform, is_valid, created_at, updated_at
FROM device_tokens
WHERE user_id = ANY($1)
  AND is_valid = TRUE`

// GetDeviceTokensByUserIDs returns all valid device tokens for the given user IDs.
// Useful for fan-out push notifications.
func GetDeviceTokensByUserIDs(ctx context.Context, db DBTX, userIDs []uuid.UUID) ([]DeviceToken, error) {
	rows, err := db.Query(ctx, getDeviceTokensByUserIDsSQL, userIDs)
	if err != nil {
		return nil, fmt.Errorf("queries.GetDeviceTokensByUserIDs: %w", err)
	}
	defer rows.Close()
	return collectDeviceTokens(rows)
}

const pruneInvalidTokensSQL = `
UPDATE device_tokens
SET is_valid = FALSE, updated_at = NOW()
WHERE token = ANY($1)`

// PruneInvalidTokens marks all listed tokens as invalid.  Called after the
// push provider reports them as unregistered / expired.
func PruneInvalidTokens(ctx context.Context, db DBTX, tokens []string) (int64, error) {
	tag, err := db.Exec(ctx, pruneInvalidTokensSQL, tokens)
	if err != nil {
		return 0, fmt.Errorf("queries.PruneInvalidTokens: %w", err)
	}
	return tag.RowsAffected(), nil
}

const deleteDeviceTokenSQL = `
DELETE FROM device_tokens
WHERE token = $1`

// DeleteDeviceToken removes a single device token (e.g. on user logout).
func DeleteDeviceToken(ctx context.Context, db DBTX, token string) error {
	_, err := db.Exec(ctx, deleteDeviceTokenSQL, token)
	if err != nil {
		return fmt.Errorf("queries.DeleteDeviceToken: %w", err)
	}
	return nil
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func scanDeviceToken(row pgx.Row) (DeviceToken, error) {
	var d DeviceToken
	err := row.Scan(
		&d.ID, &d.UserID, &d.Token,
		&d.Platform, &d.IsValid,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return DeviceToken{}, fmt.Errorf("queries.scanDeviceToken: %w", err)
	}
	return d, nil
}

func collectDeviceTokens(rows pgx.Rows) ([]DeviceToken, error) {
	var tokens []DeviceToken
	for rows.Next() {
		var d DeviceToken
		if err := rows.Scan(
			&d.ID, &d.UserID, &d.Token,
			&d.Platform, &d.IsValid,
			&d.CreatedAt, &d.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("queries.collectDeviceTokens: %w", err)
		}
		tokens = append(tokens, d)
	}
	return tokens, rows.Err()
}
