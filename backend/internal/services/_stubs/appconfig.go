package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const appConfigCacheTTL = 60 * time.Second
const appConfigCacheKey = "appconfig:v1"

// ModelBundle describes the downloaded ML model bundle.
type ModelBundle struct {
	Name              string            `json:"name"`
	Version           string            `json:"version"`
	DemandModelSHA256 string            `json:"demand_model_sha256"`
	QualityModelSHA256 string           `json:"quality_model_sha256"`
	PreprocessConsts  map[string]float64 `json:"preprocess_consts"`
	ManifestPath      string            `json:"manifest_path,omitempty"`
}

// DisplayCopy holds user-facing labels and messaging.
type DisplayCopy struct {
	AppName      string `json:"app_name"`
	TagLine      string `json:"tag_line"`
	SupportEmail string `json:"support_email"`
}

// AppConfig is the full application configuration.
type AppConfig struct {
	Env         string      `json:"env"`
	APIBase     string      `json:"api_base"`
	ModelBundle ModelBundle `json:"model_bundle"`
	Display     DisplayCopy `json:"display"`
}

// AppConfigService provides application configuration with Redis caching.
type AppConfigService struct {
	rdb            *redis.Client
	log            *zap.Logger
	modelBundleDir string
}

// NewAppConfigService constructs an AppConfigService.
func NewAppConfigService(rdb *redis.Client, log *zap.Logger) *AppConfigService {
	return &AppConfigService{
		rdb:            rdb,
		log:            log,
		modelBundleDir: os.Getenv("MODEL_BUNDLE_DIR"),
	}
}

// GetConfig returns the full AppConfig, loading from env + manifest + cache.
// Cache TTL is 60s.
func (s *AppConfigService) GetConfig(ctx context.Context) (*AppConfig, error) {
	if cached := s.fromConfigCache(ctx); cached != nil {
		return cached, nil
	}

	cfg := &AppConfig{
		Env:     envOrDefault("APP_ENV", "production"),
		APIBase: envOrDefault("API_BASE_URL", "https://api.sih26234.local"),
		Display: DisplayCopy{
			AppName:      envOrDefault("DISPLAY_APP_NAME", "FoodBridge"),
			TagLine:      envOrDefault("DISPLAY_TAGLINE", "Redistributing surplus, nourishing communities"),
			SupportEmail: envOrDefault("SUPPORT_EMAIL", "support@sih26234.local"),
		},
	}

	bundle, err := s.loadModelBundle()
	if err != nil {
		s.log.Warn("failed to load model bundle manifest, using defaults", zap.Error(err))
		bundle = ModelBundle{
			Name:    "unknown",
			Version: "unknown",
		}
	}
	cfg.ModelBundle = bundle

	s.toConfigCache(ctx, cfg)
	return cfg, nil
}

// loadModelBundle reads MODEL_BUNDLE_DIR/manifest.json and parses it.
func (s *AppConfigService) loadModelBundle() (ModelBundle, error) {
	if s.modelBundleDir == "" {
		return ModelBundle{}, fmt.Errorf("MODEL_BUNDLE_DIR not set")
	}

	manifestPath := filepath.Join(s.modelBundleDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return ModelBundle{}, fmt.Errorf("read manifest: %w", err)
	}

	var raw struct {
		Name              string            `json:"name"`
		Version           string            `json:"version"`
		DemandModelSHA256  string           `json:"demand_model_sha256"`
		QualityModelSHA256 string           `json:"quality_model_sha256"`
		PreprocessConsts  map[string]float64 `json:"preprocess_consts"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return ModelBundle{}, fmt.Errorf("parse manifest: %w", err)
	}

	return ModelBundle{
		Name:               raw.Name,
		Version:            raw.Version,
		DemandModelSHA256:  raw.DemandModelSHA256,
		QualityModelSHA256: raw.QualityModelSHA256,
		PreprocessConsts:   raw.PreprocessConsts,
		ManifestPath:       manifestPath,
	}, nil
}

// fromConfigCache retrieves a cached AppConfig.
func (s *AppConfigService) fromConfigCache(ctx context.Context) *AppConfig {
	raw, err := s.rdb.Get(ctx, appConfigCacheKey).Bytes()
	if err != nil {
		return nil
	}
	var cfg AppConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil
	}
	return &cfg
}

// toConfigCache stores the AppConfig in Redis with a 60s TTL.
func (s *AppConfigService) toConfigCache(ctx context.Context, cfg *AppConfig) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		s.log.Warn("failed to marshal appconfig for cache", zap.Error(err))
		return
	}
	if err := s.rdb.Set(ctx, appConfigCacheKey, raw, appConfigCacheTTL).Err(); err != nil {
		s.log.Warn("failed to cache appconfig", zap.Error(err))
	}
}

// envOrDefault returns the env var or a fallback.
func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
