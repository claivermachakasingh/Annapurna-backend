package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sih26234/food-waste/internal/httpapi"
	"github.com/sih26234/food-waste/internal/redisx"
)

type rateLimitRule struct {
	prefix string
	limit  int
	window time.Duration
}

// Per-path rules from the spec; evaluated in order (first match wins).
var rateLimitRules = []rateLimitRule{
	{prefix: "/auth/login", limit: 10, window: time.Minute},
	{prefix: "/quality/check", limit: 20, window: time.Minute},
	{prefix: "/sync/batch", limit: 120, window: time.Minute},
}

const defaultRateLimit = 300

// RateLimit applies sliding-window rate limits per spec §05.
// Fails open on Redis errors (never blocks traffic due to Redis being down).
func RateLimit(limiter *redisx.SlidingWindowLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stripped := strings.TrimPrefix(r.URL.Path, "/api/v1")

			limit := defaultRateLimit
			window := time.Minute
			for _, rule := range rateLimitRules {
				if strings.HasPrefix(stripped, rule.prefix) {
					limit = rule.limit
					window = rule.window
					break
				}
			}

			ip := r.RemoteAddr
			if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
				ip = strings.Split(fwd, ",")[0]
			}
			key := fmt.Sprintf("rl:api:%s", ip)

			allowed, retryAfter, err := limiter.Allow(r.Context(), key, limit, window)
			if err != nil {
				// Fail open on Redis error.
				next.ServeHTTP(w, r)
				return
			}
			if !allowed {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds())))
				httpapi.NewRateLimited().Render(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
