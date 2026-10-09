package middleware

import (
	"net/http"
	"strings"

	"github.com/sih26234/food-waste/internal/config"
	"github.com/sih26234/food-waste/internal/httpapi"
	"golang.org/x/mod/semver"
)

const HeaderAppVersion = "X-App-Version"

// AppVersion enforces minimum app version per D26.
// Skips /app/config and all /auth/* paths.
// Returns 426 APP_VERSION_UNSUPPORTED with a store-link payload if below minimum.
func AppVersion(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stripped := strings.TrimPrefix(r.URL.Path, "/api/v1")
			if stripped == "/app/config" || strings.HasPrefix(stripped, "/auth/") {
				next.ServeHTTP(w, r)
				return
			}
			clientVersion := r.Header.Get(HeaderAppVersion)
			if clientVersion == "" {
				next.ServeHTTP(w, r)
				return
			}
			cv := clientVersion
			if !strings.HasPrefix(cv, "v") {
				cv = "v" + cv
			}
			minV := cfg.AppMinVersion
			if !strings.HasPrefix(minV, "v") {
				minV = "v" + minV
			}
			if semver.IsValid(cv) && semver.IsValid(minV) && semver.Compare(cv, minV) < 0 {
				httpapi.ErrAppVersionUnsupported(clientVersion, cfg.AppMinVersion, cfg.AppStoreLink).Render(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
