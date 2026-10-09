// Package config loads all application configuration from environment variables.
// Call Load() once at startup; it terminates the process (log.Fatal) if any
// required variable is missing or malformed.
package config

import (
	"log"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime configuration for the food-waste platform.
type Config struct {
	// ── HTTP ──────────────────────────────────────────────────────────────────
	Port int

	// ── Persistence ───────────────────────────────────────────────────────────
	DatabaseURL string
	RedisURL    string

	// ── JWT access token ──────────────────────────────────────────────────────
	JWTSecret    string
	JWTExpireMin int

	// ── JWT refresh token ─────────────────────────────────────────────────────
	RefreshSecret     string
	RefreshExpireDays int

	// ── CORS ──────────────────────────────────────────────────────────────────
	AllowedOrigins []string

	// ── ML serving ────────────────────────────────────────────────────────────
	MLServingURL   string
	MLServingToken string

	// ── Model bundle ──────────────────────────────────────────────────────────
	ModelBundleDir string

	// ── File upload ───────────────────────────────────────────────────────────
	MaxUploadMB int64
	UploadsDir  string

	// ── Carbon accounting ─────────────────────────────────────────────────────
	CarbonFactorKgCO2ePerKg float64

	// ── Food safety danger zone (°C) ──────────────────────────────────────────
	DangerZoneMinC float64
	DangerZoneMaxC float64

	// ── Feature flags ─────────────────────────────────────────────────────────
	FeatureMockML      bool
	FeatureMockCV      bool
	FeatureMockRouting bool

	// ── Mobile minimum version ────────────────────────────────────────────────
	AppMinVersion string

	// ── Firebase ──────────────────────────────────────────────────────────────
	GoogleApplicationCredentials string
}

// Load reads environment variables and returns a validated Config.
// It calls log.Fatal for any required variable that is absent or unparseable.
func Load() *Config {
	cfg := &Config{}

	// ── PORT ──────────────────────────────────────────────────────────────────
	cfg.Port = requireInt("PORT")

	// ── DATABASE_URL ──────────────────────────────────────────────────────────
	cfg.DatabaseURL = requireStr("DATABASE_URL")

	// ── REDIS_URL ─────────────────────────────────────────────────────────────
	cfg.RedisURL = requireStr("REDIS_URL")

	// ── JWT ───────────────────────────────────────────────────────────────────
	cfg.JWTSecret = requireStr("JWT_SECRET")
	cfg.JWTExpireMin = requireInt("JWT_EXPIRE_MIN")

	// ── REFRESH JWT ───────────────────────────────────────────────────────────
	cfg.RefreshSecret = requireStr("REFRESH_SECRET")
	cfg.RefreshExpireDays = requireInt("REFRESH_EXPIRE_DAYS")

	// ── CORS ──────────────────────────────────────────────────────────────────
	rawOrigins := requireStr("ALLOWED_ORIGINS")
	for _, o := range strings.Split(rawOrigins, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			cfg.AllowedOrigins = append(cfg.AllowedOrigins, o)
		}
	}

	// ── ML serving ────────────────────────────────────────────────────────────
	cfg.MLServingURL = requireStr("MLSERVING_URL")
	cfg.MLServingToken = optionalStr("MLSERVING_TOKEN", "")

	// ── Model bundle ──────────────────────────────────────────────────────────
	cfg.ModelBundleDir = optionalStr("MODEL_BUNDLE_DIR", "./models")

	// ── File uploads ──────────────────────────────────────────────────────────
	cfg.MaxUploadMB = int64(requireInt("MAX_UPLOAD_MB"))
	cfg.UploadsDir = optionalStr("UPLOADS_DIR", "storage/uploads")

	// ── Carbon factor ─────────────────────────────────────────────────────────
	cfg.CarbonFactorKgCO2ePerKg = requireFloat("CARBON_FACTOR_KG_CO2E_PER_KG")

	// ── Danger zone ───────────────────────────────────────────────────────────
	cfg.DangerZoneMinC = requireFloat("DANGER_ZONE_MIN_C")
	cfg.DangerZoneMaxC = requireFloat("DANGER_ZONE_MAX_C")

	// ── Feature flags ─────────────────────────────────────────────────────────
	cfg.FeatureMockML = optionalBool("FEATURE_MOCK_ML", false)
	cfg.FeatureMockCV = optionalBool("FEATURE_MOCK_CV", false)
	cfg.FeatureMockRouting = optionalBool("FEATURE_MOCK_ROUTING", false)

	// ── App min version ───────────────────────────────────────────────────────
	cfg.AppMinVersion = optionalStr("APP_MIN_VERSION", "1.0.0")

	// ── Firebase ──────────────────────────────────────────────────────────────
	cfg.GoogleApplicationCredentials = optionalStr("GOOGLE_APPLICATION_CREDENTIALS", "")

	return cfg
}

// ── helpers ───────────────────────────────────────────────────────────────────

func requireStr(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("config: required environment variable %q is not set", key)
	}
	return v
}

func requireInt(key string) int {
	raw := requireStr(key)
	v, err := strconv.Atoi(raw)
	if err != nil {
		log.Fatalf("config: environment variable %q must be an integer, got %q: %v", key, raw, err)
	}
	return v
}

func requireFloat(key string) float64 {
	raw := requireStr(key)
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		log.Fatalf("config: environment variable %q must be a float, got %q: %v", key, raw, err)
	}
	return v
}

func optionalStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func optionalBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		log.Fatalf("config: environment variable %q must be a boolean (true/false), got %q: %v", key, v, err)
	}
	return b
}
