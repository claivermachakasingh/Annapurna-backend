package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/sih26234/food-waste/internal/auth"
	"github.com/sih26234/food-waste/internal/httpapi"
)

type claimsKey struct{}

// publicPaths bypass JWT verification (matched after stripping /api/v1).
var publicPaths = map[string]bool{
	"/health":        true,
	"/ready":         true,
	"/metrics":       true,
	"/app/config":    true,
	"/auth/register": true,
	"/auth/login":    true,
	"/auth/refresh":  true,
}

// Auth validates Bearer JWTs and stores Claims in context.
// Skips verification for public paths.
func Auth(verifier auth.TokenVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stripped := strings.TrimPrefix(r.URL.Path, "/api/v1")
			if publicPaths[stripped] {
				next.ServeHTTP(w, r)
				return
			}
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				httpapi.NewUnauthorized("missing or malformed Authorization header").Render(w)
				return
			}
			token := strings.TrimPrefix(header, "Bearer ")
			claims, err := verifier.VerifyAccessToken(r.Context(), token)
			if err != nil {
				httpapi.NewUnauthorized("invalid or expired token").Render(w)
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClaimsFromCtx retrieves JWT claims stored by Auth middleware.
func ClaimsFromCtx(ctx context.Context) *auth.Claims {
	c, _ := ctx.Value(claimsKey{}).(*auth.Claims)
	return c
}
