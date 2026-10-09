package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sih26234/food-waste/internal/httpapi"
	"github.com/sih26234/food-waste/internal/redisx"
)

const (
	HeaderIdempotencyKey = "Idempotency-Key"
	idempotencyTTL       = 24 * time.Hour
)

type idempotencyRecord struct {
	BodyHash   string            `json:"body_hash"`
	StatusCode int               `json:"status_code"`
	Body       []byte            `json:"body"`
	Headers    map[string]string `json:"headers"`
}

type responseCapture struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (rc *responseCapture) WriteHeader(code int) {
	rc.status = code
	rc.ResponseWriter.WriteHeader(code)
}
func (rc *responseCapture) Write(b []byte) (int, error) {
	rc.body.Write(b)
	return rc.ResponseWriter.Write(b)
}

// Idempotency stores/replays POST responses keyed by Idempotency-Key header.
// Mismatched payload hash → 422 IDEMPOTENCY_KEY_REUSED.
func Idempotency(store *redisx.KVStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				next.ServeHTTP(w, r)
				return
			}
			key := r.Header.Get(HeaderIdempotencyKey)
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				httpapi.NewInternal("failed to read request body").Render(w)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

			sum := sha256.Sum256(bodyBytes)
			bodyHash := hex.EncodeToString(sum[:])
			redisKey := fmt.Sprintf("idem:%s", key)
			ctx := r.Context()

			existing, found, _ := store.Get(ctx, redisKey)
			if found {
				var rec idempotencyRecord
				if json.Unmarshal([]byte(existing), &rec) == nil {
					if rec.BodyHash != bodyHash {
						httpapi.ErrIdempotencyKeyReused(key).Render(w)
						return
					}
					for k, v := range rec.Headers {
						w.Header().Set(k, v)
					}
					w.WriteHeader(rec.StatusCode)
					w.Write(rec.Body) //nolint:errcheck
					return
				}
			}

			rc := &responseCapture{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rc, r)

			if rc.status >= 200 && rc.status < 300 {
				rec := idempotencyRecord{
					BodyHash:   bodyHash,
					StatusCode: rc.status,
					Body:       rc.body.Bytes(),
					Headers:    map[string]string{"Content-Type": w.Header().Get("Content-Type")},
				}
				data, _ := json.Marshal(rec)
				_ = store.Set(context.Background(), redisKey, string(data), idempotencyTTL)
			}
		})
	}
}
