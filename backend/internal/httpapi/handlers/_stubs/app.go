package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sih26234/backend/internal/service"
)

// AppHandler holds dependencies for mobile app config endpoints.
type AppHandler struct {
	appSvc service.AppService
}

// NewAppHandler constructs an AppHandler.
func NewAppHandler(appSvc service.AppService) *AppHandler {
	return &AppHandler{appSvc: appSvc}
}

// RegisterRoutes mounts app routes onto r.
func (h *AppHandler) RegisterRoutes(r chi.Router) {
	r.Get("/app/config", h.Config)
	r.Get("/app/model-bundle", h.ModelBundle)
}

// ─── Response types ───────────────────────────────────────────────────────────

// FeatureFlag is one runtime feature toggle.
type FeatureFlag struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// AppConfigResponse is returned by GET /app/config.
type AppConfigResponse struct {
	MinAppVersion  string        `json:"min_app_version"`
	ForceUpdate    bool          `json:"force_update"`
	FeatureFlags   []FeatureFlag `json:"feature_flags"`
	SyncIntervalS  int           `json:"sync_interval_s"`
	OfflineEnabled bool          `json:"offline_enabled"`
	FetchedAt      time.Time     `json:"fetched_at"`
}

// ModelBundleResponse is returned by GET /app/model-bundle.
type ModelBundleResponse struct {
	Version     string    `json:"version"`
	BundleURL   string    `json:"bundle_url"`
	Checksum    string    `json:"checksum"`
	SizeBytes   int64     `json:"size_bytes"`
	PublishedAt time.Time `json:"published_at"`
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// Config handles GET /app/config.
//
//	@Summary      App runtime config
//	@Description  Return feature flags, minimum version, and sync settings for mobile clients.
//	@Tags         app
//	@Produce      json
//	@Security     BearerAuth
//	@Param        platform query string false "Client platform: android|ios"
//	@Param        version  query string false "Current app version"
//	@Success      200 {object} AppConfigResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /app/config [get]
func (h *AppHandler) Config(w http.ResponseWriter, r *http.Request) {
	platform := r.URL.Query().Get("platform")
	version := r.URL.Query().Get("version")

	result, err := h.appSvc.Config(r.Context(), service.AppConfigInput{
		Platform: platform,
		Version:  version,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch app config", err)
		return
	}

	flags := make([]FeatureFlag, len(result.FeatureFlags))
	for i, f := range result.FeatureFlags {
		flags[i] = FeatureFlag{Name: f.Name, Enabled: f.Enabled}
	}

	writeJSON(w, http.StatusOK, AppConfigResponse{
		MinAppVersion:  result.MinAppVersion,
		ForceUpdate:    result.ForceUpdate,
		FeatureFlags:   flags,
		SyncIntervalS:  result.SyncIntervalS,
		OfflineEnabled: result.OfflineEnabled,
		FetchedAt:      time.Now().UTC(),
	})
}

// ModelBundle handles GET /app/model-bundle.
//
//	@Summary      On-device ML model bundle
//	@Description  Return download URL and metadata for the latest on-device ML model bundle.
//	@Tags         app
//	@Produce      json
//	@Security     BearerAuth
//	@Param        platform query string false "Client platform: android|ios"
//	@Success      200 {object} ModelBundleResponse
//	@Failure      404 {object} ErrorResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /app/model-bundle [get]
func (h *AppHandler) ModelBundle(w http.ResponseWriter, r *http.Request) {
	platform := r.URL.Query().Get("platform")

	result, err := h.appSvc.ModelBundle(r.Context(), platform)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch model bundle", err)
		return
	}
	if result == nil {
		writeError(w, http.StatusNotFound, "no model bundle available", nil)
		return
	}

	writeJSON(w, http.StatusOK, ModelBundleResponse{
		Version:     result.Version,
		BundleURL:   result.BundleURL,
		Checksum:    result.Checksum,
		SizeBytes:   result.SizeBytes,
		PublishedAt: result.PublishedAt,
	})
}
