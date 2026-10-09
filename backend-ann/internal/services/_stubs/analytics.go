package services

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	carbonFactor      = 2.5   // kg CO2 per kg food waste avoided
	mealsPerKg        = 0.4   // kg food per meal equivalent
	impactCacheTTL    = 30 * time.Second
)

// ImpactMetrics holds computed environmental and social impact figures.
type ImpactMetrics struct {
	KitchenID         string    `json:"kitchen_id"`
	ComputedAt        time.Time `json:"computed_at"`
	WasteAvoidedKg    float64   `json:"waste_avoided_kg"`
	CarbonSavedKgCO2  float64   `json:"carbon_saved_kg_co2"`
	MealsEquivalent   float64   `json:"meals_equivalent"`
	DeliveredKg       float64   `json:"delivered_kg"`
	DivertedKg        float64   `json:"diverted_kg"`
	Cached            bool      `json:"cached"`
}

// AnalyticsService computes environmental and social impact from SQL views.
type AnalyticsService struct {
	db  *sql.DB
	rdb *redis.Client
	log *zap.Logger
}

// NewAnalyticsService constructs an AnalyticsService.
func NewAnalyticsService(db *sql.DB, rdb *redis.Client, log *zap.Logger) *AnalyticsService {
	return &AnalyticsService{db: db, rdb: rdb, log: log}
}

// GetImpact computes impact metrics for a kitchen, caching results for 30s.
// waste_avoided_kg = Σdelivered_kg + Σdiverted_kg
// carbon            = waste_avoided_kg × 2.5
// meals_equivalent  = waste_avoided_kg / 0.4
func (s *AnalyticsService) GetImpact(ctx context.Context, kitchenID string) (*ImpactMetrics, error) {
	if kitchenID == "" {
		return nil, fmt.Errorf("kitchen_id is required")
	}

	cacheKey := fmt.Sprintf("impact:%s", kitchenID)
	if cached := s.fromImpactCache(ctx, cacheKey); cached != nil {
		cached.Cached = true
		return cached, nil
	}

	delivered, diverted, err := s.fetchImpactTotals(ctx, kitchenID)
	if err != nil {
		return nil, fmt.Errorf("fetch impact totals: %w", err)
	}

	wasteAvoided := delivered + diverted
	impact := &ImpactMetrics{
		KitchenID:        kitchenID,
		ComputedAt:       time.Now().UTC(),
		WasteAvoidedKg:   wasteAvoided,
		CarbonSavedKgCO2: wasteAvoided * carbonFactor,
		MealsEquivalent:  wasteAvoided / mealsPerKg,
		DeliveredKg:      delivered,
		DivertedKg:       diverted,
	}

	s.toImpactCache(ctx, cacheKey, impact)
	return impact, nil
}

// fetchImpactTotals queries the SQL views for delivered and diverted quantities.
func (s *AnalyticsService) fetchImpactTotals(ctx context.Context, kitchenID string) (delivered, diverted float64, err error) {
	// Delivered: surplus batches with status='DELIVERED'
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(quantity_kg), 0)
		FROM surplus_batches
		WHERE kitchen_id = $1 AND status = 'DELIVERED'`,
		kitchenID,
	).Scan(&delivered)
	if err != nil {
		return 0, 0, fmt.Errorf("query delivered: %w", err)
	}

	// Diverted: surplus batches with status='DIVERTED'
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(quantity_kg), 0)
		FROM surplus_batches
		WHERE kitchen_id = $1 AND status = 'DIVERTED'`,
		kitchenID,
	).Scan(&diverted)
	if err != nil {
		return 0, 0, fmt.Errorf("query diverted: %w", err)
	}

	return delivered, diverted, nil
}

// fromImpactCache retrieves a cached ImpactMetrics from Redis.
func (s *AnalyticsService) fromImpactCache(ctx context.Context, key string) *ImpactMetrics {
	raw, err := s.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil
	}
	var m ImpactMetrics
	if err := jsonUnmarshal(raw, &m); err != nil {
		return nil
	}
	return &m
}

// toImpactCache stores ImpactMetrics in Redis with a 30s TTL.
func (s *AnalyticsService) toImpactCache(ctx context.Context, key string, m *ImpactMetrics) {
	raw, err := jsonMarshal(m)
	if err != nil {
		s.log.Warn("failed to marshal impact for cache", zap.Error(err))
		return
	}
	if err := s.rdb.Set(ctx, key, raw, impactCacheTTL).Err(); err != nil {
		s.log.Warn("failed to cache impact", zap.Error(err))
	}
}
