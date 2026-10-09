package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func setSecrets(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-secret-that-is-long-enough-for-hs256")
	t.Setenv("JWT_EXPIRE_MIN", "60")
	t.Setenv("REFRESH_EXPIRE_DAYS", "30")
}

func TestMintAndVerifyAccessTokenRoundTrip(t *testing.T) {
	setSecrets(t)

	userID := uuid.NewString()
	kitchenID := uuid.NewString()

	token, err := MintAccessToken(userID, "KITCHEN", kitchenID, "")
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	claims, err := VerifyAccessToken(token)
	if err != nil {
		t.Fatalf("VerifyAccessToken: %v", err)
	}
	if claims.Subject != userID {
		t.Errorf("subject = %q, want %q", claims.Subject, userID)
	}
	if claims.Role != "KITCHEN" {
		t.Errorf("role = %q, want KITCHEN", claims.Role)
	}
	if claims.KitchenID != kitchenID {
		t.Errorf("kitchen_id = %q, want %q", claims.KitchenID, kitchenID)
	}
	if claims.RecipientID != "" {
		t.Errorf("recipient_id should be empty for kitchen tokens, got %q", claims.RecipientID)
	}
	if claims.ID == "" {
		t.Error("jti (token id) must be set so tokens can be denylisted")
	}
	if claims.ExpiresAt == nil || !claims.ExpiresAt.After(time.Now()) {
		t.Error("token must carry a future expiry")
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	setSecrets(t)
	token, err := MintAccessToken(uuid.NewString(), "ADMIN", "", "")
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	t.Setenv("JWT_SECRET", "a-completely-different-secret-value-32ch")
	if _, err := VerifyAccessToken(token); err == nil {
		t.Fatal("token signed with a different secret must not verify")
	}
}

func TestVerifyRejectsMalformedAndEmptyTokens(t *testing.T) {
	setSecrets(t)
	for _, tok := range []string{"", "not-a-jwt", "a.b.c", strings.Repeat("x", 40)} {
		if _, err := VerifyAccessToken(tok); err == nil {
			t.Errorf("token %q should not verify", tok)
		}
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	setSecrets(t)

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		},
		Role: "KITCHEN",
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).
		SignedString([]byte("test-secret-that-is-long-enough-for-hs256"))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := VerifyAccessToken(signed); err == nil {
		t.Fatal("expired token must be rejected")
	}
}

func TestPasswordHashingRoundTrip(t *testing.T) {
	hash, err := HashPassword("demo123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "demo123" {
		t.Fatal("password must never be stored in plain text")
	}
	if !strings.HasPrefix(hash, "$2a$") && !strings.HasPrefix(hash, "$2b$") {
		t.Errorf("expected a bcrypt hash, got %.4q", hash)
	}
	if err := CheckPassword(hash, "demo123"); err != nil {
		t.Errorf("correct password rejected: %v", err)
	}
	if err := CheckPassword(hash, "wrong-password"); err == nil {
		t.Error("incorrect password accepted")
	}
}

func TestPasswordHashesAreSalted(t *testing.T) {
	a, _ := HashPassword("demo123")
	b, _ := HashPassword("demo123")
	if a == b {
		t.Fatal("two hashes of the same password must differ (unique salt)")
	}
}

func TestMintRefreshTokenIsRandomAndHashed(t *testing.T) {
	setSecrets(t)

	raw1, hash1, err := MintRefreshToken()
	if err != nil {
		t.Fatalf("MintRefreshToken: %v", err)
	}
	raw2, hash2, err := MintRefreshToken()
	if err != nil {
		t.Fatalf("MintRefreshToken: %v", err)
	}
	if raw1 == raw2 || hash1 == hash2 {
		t.Fatal("refresh tokens must be unique")
	}
	if len(raw1) < 32 {
		t.Errorf("refresh token too short: %d chars", len(raw1))
	}
	if strings.Contains(hash1, raw1) || hash1 == raw1 {
		t.Fatal("stored value must be a hash, never the raw token")
	}
	if hash1 != hashSHA256Hex(raw1) {
		t.Error("stored hash must be the SHA-256 digest of the raw token")
	}
}

// ─── refresh rotation + reuse detection (D22) ─────────────────────────────────

type memStore struct {
	rows   map[string]RefreshTokenRow
	family map[string]string
}

func newMemStore() *memStore {
	return &memStore{rows: map[string]RefreshTokenRow{}, family: map[string]string{}}
}

func (m *memStore) InsertRefreshToken(_ context.Context, row RefreshTokenRow) error {
	m.rows[row.TokenHash] = row
	m.family[row.TokenHash] = row.FamilyID
	return nil
}

func (m *memStore) GetRefreshTokenByHash(_ context.Context, hash string) (RefreshTokenRow, error) {
	if r, ok := m.rows[hash]; ok {
		return r, nil
	}
	return RefreshTokenRow{}, ErrRefreshTokenNotFound
}

func (m *memStore) RevokeFamily(_ context.Context, familyID string) error {
	for hash, r := range m.rows {
		if r.FamilyID == familyID {
			r.Revoked = true
			m.rows[hash] = r
		}
	}
	return nil
}

func (m *memStore) DeleteExpiredTokensForUser(_ context.Context, userID string) error {
	for hash, r := range m.rows {
		if r.UserID == userID && time.Now().UTC().After(r.ExpiresAt) {
			delete(m.rows, hash)
		}
	}
	return nil
}

func TestCreateRefreshTokenPersistsHashedRow(t *testing.T) {
	setSecrets(t)
	ctx := context.Background()
	store := newMemStore()
	userID := uuid.NewString()

	raw, err := CreateRefreshToken(ctx, store, userID)
	if err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}
	if len(store.rows) != 1 {
		t.Fatalf("expected exactly 1 persisted row, got %d", len(store.rows))
	}
	for hash, row := range store.rows {
		if strings.Contains(hash, raw) {
			t.Fatal("raw token must never be persisted")
		}
		if row.UserID != userID {
			t.Errorf("row owner = %q, want %q", row.UserID, userID)
		}
		if row.FamilyID == "" || row.ID == "" {
			t.Error("row must have id and family_id")
		}
		if row.Revoked {
			t.Error("a freshly issued token must not be revoked")
		}
		if !row.ExpiresAt.After(time.Now().Add(29 * 24 * time.Hour)) {
			t.Errorf("TTL shorter than configured 30 days: %s", row.ExpiresAt)
		}
	}
}

func TestRotateRefreshTokenIssuesNewToken(t *testing.T) {
	setSecrets(t)
	ctx := context.Background()
	store := newMemStore()
	userID := uuid.NewString()

	raw1, err := CreateRefreshToken(ctx, store, userID)
	if err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}

	raw2, gotUser, err := RotateRefreshToken(ctx, store, raw1)
	if err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}
	if gotUser != userID {
		t.Errorf("rotated user = %q, want %q", gotUser, userID)
	}
	if raw2 == raw1 {
		t.Fatal("rotation must issue a fresh token")
	}
	if len(store.rows) != 2 {
		t.Fatalf("expected 2 persisted rows after rotation, got %d", len(store.rows))
	}
}

func TestRotateRefreshTokenRejectsUnknownToken(t *testing.T) {
	setSecrets(t)
	_, _, err := RotateRefreshToken(context.Background(), newMemStore(), "never-issued")
	if !errors.Is(err, ErrRefreshTokenNotFound) {
		t.Fatalf("err = %v, want ErrRefreshTokenNotFound", err)
	}
}

func TestRotateRefreshTokenDetectsReuse(t *testing.T) {
	setSecrets(t)
	ctx := context.Background()
	store := newMemStore()
	userID := uuid.NewString()

	raw1, err := CreateRefreshToken(ctx, store, userID)
	if err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}
	raw2, _, err := RotateRefreshToken(ctx, store, raw1)
	if err != nil {
		t.Fatalf("first rotation: %v", err)
	}

	// Replaying the already-rotated token is a reuse attempt and must fail.
	if _, _, err := RotateRefreshToken(ctx, store, raw1); !errors.Is(err, ErrRefreshTokenRevoked) {
		t.Fatalf("reuse err = %v, want ErrRefreshTokenRevoked", err)
	}
	// The family containing raw1 is revoked, and raw2 keeps working because
	// rotation resets the chain into a new family.
	if _, _, err := RotateRefreshToken(ctx, store, raw2); err != nil {
		t.Errorf("token issued by rotation must still work: %v", err)
	}
}

func TestRotateRefreshTokenRejectsExpired(t *testing.T) {
	setSecrets(t)
	ctx := context.Background()
	store := newMemStore()

	raw := "expired-raw-token-value"
	row := RefreshTokenRow{
		ID:        uuid.NewString(),
		UserID:    uuid.NewString(),
		FamilyID:  uuid.NewString(),
		TokenHash: hashSHA256Hex(raw),
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}
	if err := store.InsertRefreshToken(ctx, row); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if _, _, err := RotateRefreshToken(ctx, store, raw); !errors.Is(err, ErrRefreshTokenExpired) {
		t.Fatalf("err = %v, want ErrRefreshTokenExpired", err)
	}
	if !store.rows[row.TokenHash].Revoked {
		t.Error("expired token's family should be revoked as cleanup")
	}
}

func TestRevokeFamilyIsIdempotentForUnknownTokens(t *testing.T) {
	setSecrets(t)
	ctx := context.Background()
	store := newMemStore()

	raw, err := CreateRefreshToken(ctx, store, uuid.NewString())
	if err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}
	if err := RevokeFamily(ctx, store, raw); err != nil {
		t.Fatalf("RevokeFamily: %v", err)
	}
	if _, _, err := RotateRefreshToken(ctx, store, raw); !errors.Is(err, ErrRefreshTokenRevoked) {
		t.Fatalf("revoked token err = %v, want ErrRefreshTokenRevoked", err)
	}
	// Revoking something that was never issued is treated as success (logout
	// must be idempotent).
	if err := RevokeFamily(ctx, store, "never-issued"); err != nil {
		t.Errorf("RevokeFamily(unknown) = %v, want nil", err)
	}
}
