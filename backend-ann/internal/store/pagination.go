package store

import (
	"encoding/base64"
	"fmt"

	"github.com/google/uuid"
)

// CursorPage is a generic keyset-pagination result.
// T is typically a domain struct (e.g. SurplusBatch, Match).
//
// NextCursor is nil when there are no further pages.
type CursorPage[T any] struct {
	Items      []T
	NextCursor *string
}

// EncodeCursor base64url-encodes a UUID into an opaque cursor string.
// The encoding is URL-safe and padding-free so it is safe to embed in query
// parameters without additional escaping.
func EncodeCursor(id uuid.UUID) string {
	// Use the 16-byte binary form to keep the cursor compact.
	return base64.RawURLEncoding.EncodeToString(id[:])
}

// DecodeCursor decodes a cursor string produced by EncodeCursor back to a
// UUID.  Returns an error if the cursor is malformed.
func DecodeCursor(cursor string) (uuid.UUID, error) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("store.DecodeCursor: base64: %w", err)
	}
	if len(b) != 16 {
		return uuid.UUID{}, fmt.Errorf("store.DecodeCursor: expected 16 bytes, got %d", len(b))
	}
	var id uuid.UUID
	copy(id[:], b)
	return id, nil
}

// CursorFromString is a convenience wrapper that decodes an optional cursor
// pointer.  When ptr is nil it returns uuid.Nil and no error — callers
// interpret uuid.Nil as "start from the beginning".
func CursorFromString(ptr *string) (uuid.UUID, error) {
	if ptr == nil || *ptr == "" {
		return uuid.Nil, nil
	}
	return DecodeCursor(*ptr)
}

// StringPtr returns a pointer to s.  Useful for setting NextCursor.
func StringPtr(s string) *string { return &s }
