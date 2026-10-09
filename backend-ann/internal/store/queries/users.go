// Package queries contains hand-crafted pgx query functions following sqlc
// patterns.  Each function accepts a DBTX interface so callers can pass either
// a *pgxpool.Pool or a pgx.Tx (enabling use inside RunTx).
package queries

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBTX is satisfied by both *pgxpool.Pool and pgx.Tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ────────────────────────────────────────────────────────────────────────────
// Domain types
// ────────────────────────────────────────────────────────────────────────────

// User mirrors the users table.
type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	Role         string // kitchen | ngo | admin | processor | driver
	OrgID        *uuid.UUID
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CreateUserParams holds the fields required when inserting a new user.
type CreateUserParams struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	Role         string
	OrgID        *uuid.UUID
}

// ────────────────────────────────────────────────────────────────────────────
// Queries
// ────────────────────────────────────────────────────────────────────────────

const getUserByEmailSQL = `
SELECT id, email, password_hash, role, org_id, is_active, created_at, updated_at
FROM users
WHERE email = $1
LIMIT 1`

// GetUserByEmail returns the user with the given e-mail address.
// Returns pgx.ErrNoRows when not found.
func GetUserByEmail(ctx context.Context, db DBTX, email string) (User, error) {
	row := db.QueryRow(ctx, getUserByEmailSQL, email)
	return scanUser(row)
}

const getUserByIDSQL = `
SELECT id, email, password_hash, role, org_id, is_active, created_at, updated_at
FROM users
WHERE id = $1
LIMIT 1`

// GetUserByID returns the user with the given UUID.
// Returns pgx.ErrNoRows when not found.
func GetUserByID(ctx context.Context, db DBTX, id uuid.UUID) (User, error) {
	row := db.QueryRow(ctx, getUserByIDSQL, id)
	return scanUser(row)
}

const createUserSQL = `
INSERT INTO users (id, email, password_hash, role, org_id, is_active, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, TRUE, NOW(), NOW())
RETURNING id, email, password_hash, role, org_id, is_active, created_at, updated_at`

// CreateUser inserts a new user row and returns the persisted record.
func CreateUser(ctx context.Context, db DBTX, p CreateUserParams) (User, error) {
	row := db.QueryRow(ctx, createUserSQL,
		p.ID, p.Email, p.PasswordHash, p.Role, p.OrgID,
	)
	return scanUser(row)
}

const updateUserActiveSQL = `
UPDATE users
SET is_active = $2, updated_at = NOW()
WHERE id = $1
RETURNING id, email, password_hash, role, org_id, is_active, created_at, updated_at`

// UpdateUserActive enables or disables a user account.
func UpdateUserActive(ctx context.Context, db DBTX, id uuid.UUID, active bool) (User, error) {
	row := db.QueryRow(ctx, updateUserActiveSQL, id, active)
	return scanUser(row)
}

const listUsersSQL = `
SELECT id, email, password_hash, role, org_id, is_active, created_at, updated_at
FROM users
WHERE ($1::uuid IS NULL OR id > $1)
ORDER BY id ASC
LIMIT $2`

// ListUsers returns up to limit users with cursor-based pagination.
// Pass uuid.Nil as afterID to start from the beginning.
func ListUsers(ctx context.Context, db DBTX, afterID uuid.UUID, limit int) ([]User, error) {
	var afterArg interface{} = afterID
	if afterID == uuid.Nil {
		afterArg = nil
	}
	rows, err := db.Query(ctx, listUsersSQL, afterArg, limit)
	if err != nil {
		return nil, fmt.Errorf("queries.ListUsers: %w", err)
	}
	defer rows.Close()
	return collectUsers(rows)
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.Role,
		&u.OrgID, &u.IsActive, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return User{}, fmt.Errorf("queries.scanUser: %w", err)
	}
	return u, nil
}

func collectUsers(rows pgx.Rows) ([]User, error) {
	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(
			&u.ID, &u.Email, &u.PasswordHash, &u.Role,
			&u.OrgID, &u.IsActive, &u.CreatedAt, &u.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("queries.collectUsers: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}
