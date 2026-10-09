package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type RoutingService struct {
	pool *pgxpool.Pool
}

func NewRoutingService(pool *pgxpool.Pool) *RoutingService {
	return &RoutingService{pool: pool}
}

type AvailableDelivery struct {
	RouteID       string    `json:"route_id"`
	BatchID       string    `json:"batch_id"`
	FoodName      string    `json:"food_name"`
	QuantityKg    float64   `json:"quantity_kg"`
	QuantityUnit  string    `json:"quantity_unit"`
	KitchenName   string    `json:"kitchen_name"`
	KitchenLat    float64   `json:"kitchen_lat"`
	KitchenLng    float64   `json:"kitchen_lng"`
	RecipientName string    `json:"recipient_name"`
	RecipientLat  float64   `json:"recipient_lat"`
	RecipientLng  float64   `json:"recipient_lng"`
	DistanceKm    float64   `json:"distance_km"`
	CreatedAt     time.Time `json:"created_at"`
}

func (s *RoutingService) CreateRoute(ctx context.Context, batchID string, recipientID string) error {
	// This creates a planned route when a match is accepted.
	// In a real system, a solver like OSRM or VRP engine would optimize multiple stops.
	// Here, we create a simple Point A (Kitchen) to Point B (NGO) route.

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Get coords to calculate rough distance
	var kLat, kLng, rLat, rLng float64
	err = tx.QueryRow(ctx, `
		SELECT k.latitude, k.longitude, r.latitude, r.longitude
		FROM surplus_batches sb
		JOIN kitchens k ON sb.kitchen_id = k.id
		CROSS JOIN recipients r
		WHERE sb.id = $1 AND r.id = $2
	`, batchID, recipientID).Scan(&kLat, &kLng, &rLat, &rLng)
	if err != nil {
		return fmt.Errorf("fetch coords: %w", err)
	}

	// For demonstration, use a rough euclidean distance approximation if OSRM isn't available immediately.
	// We'll call OSRM for actual driving distance
	distKm, etaMins := s.getRouteFromOSRM(kLat, kLng, rLat, rLng)

	var routeID string
	err = tx.QueryRow(ctx, `
		INSERT INTO routes (batch_id, distance_km, eta_minutes, solver, status)
		VALUES ($1, $2, $3, 'OSRM', 'planned')
		RETURNING id
	`, batchID, distKm, etaMins).Scan(&routeID)
	if err != nil {
		return fmt.Errorf("insert route: %w", err)
	}

	// Insert stops
	_, err = tx.Exec(ctx, `
		INSERT INTO route_stops (route_id, batch_id, sequence, address)
		VALUES ($1, $2, 1, 'Kitchen')
	`, routeID, batchID)
	if err != nil {
		return fmt.Errorf("insert kitchen stop: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO route_stops (route_id, recipient_id, sequence, address)
		VALUES ($1, $2, 2, 'NGO')
	`, routeID, recipientID)
	if err != nil {
		return fmt.Errorf("insert recipient stop: %w", err)
	}

	return tx.Commit(ctx)
}

func (s *RoutingService) getRouteFromOSRM(lat1, lng1, lat2, lng2 float64) (float64, int) {
	url := fmt.Sprintf("http://router.project-osrm.org/route/v1/driving/%f,%f;%f,%f?overview=false", lng1, lat1, lng2, lat2)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 5.0, 15 // Fallback values
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		var result struct {
			Routes []struct {
				Distance float64 `json:"distance"` // in meters
				Duration float64 `json:"duration"` // in seconds
			} `json:"routes"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && len(result.Routes) > 0 {
			distKm := result.Routes[0].Distance / 1000.0
			etaMins := int(result.Routes[0].Duration / 60.0)
			return distKm, etaMins
		}
	}
	return 5.0, 15
}

func (s *RoutingService) ListAvailableDeliveries(ctx context.Context) ([]AvailableDelivery, error) {
	query := `
		SELECT r.id, r.batch_id, sb.food_name, sb.quantity_kg, COALESCE(sb.quantity_unit, 'kg'),
		       k.name as kitchen_name, COALESCE(k.latitude, 28.6139) as kitchen_lat, COALESCE(k.longitude, 77.2090) as kitchen_lng,
		       rec.name as recipient_name, COALESCE(rec.latitude, 28.6139) as recipient_lat, COALESCE(rec.longitude, 77.2090) as recipient_lng,
		       r.distance_km, r.created_at
		FROM routes r
		JOIN surplus_batches sb ON r.batch_id = sb.id
		JOIN kitchens k ON sb.kitchen_id = k.id
		JOIN route_stops rs ON rs.route_id = r.id AND rs.sequence = 2
		JOIN recipients rec ON rs.recipient_id = rec.id
		WHERE r.status = 'planned' AND r.driver_id IS NULL
		ORDER BY r.created_at DESC`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query available deliveries: %w", err)
	}
	defer rows.Close()

	var items []AvailableDelivery
	for rows.Next() {
		var item AvailableDelivery
		if err := rows.Scan(
			&item.RouteID, &item.BatchID, &item.FoodName, &item.QuantityKg, &item.QuantityUnit,
			&item.KitchenName, &item.KitchenLat, &item.KitchenLng,
			&item.RecipientName, &item.RecipientLat, &item.RecipientLng,
			&item.DistanceKm, &item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan delivery: %w", err)
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *RoutingService) AssignDriver(ctx context.Context, routeID string, driverID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	
	res, err := tx.Exec(ctx, `
		UPDATE routes SET driver_id = $1, status = 'assigned', updated_at = NOW()
		WHERE id = $2 AND status = 'planned' AND driver_id IS NULL
	`, driverID, routeID)
	if err != nil {
		return fmt.Errorf("assign driver: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("route not available or already assigned")
	}
	
	// Also transition surplus batch status
	var batchID string
	err = tx.QueryRow(ctx, `SELECT batch_id FROM routes WHERE id = $1`, routeID).Scan(&batchID)
	if err == nil {
		_, _ = tx.Exec(ctx, `UPDATE surplus_batches SET status = 'IN_TRANSIT', updated_at = NOW() WHERE id = $1`, batchID)
	}
	
	return tx.Commit(ctx)
}

func (s *RoutingService) ListAssignedDeliveries(ctx context.Context, driverID string) ([]AvailableDelivery, error) {
	query := `
		SELECT r.id, r.batch_id, sb.food_name, sb.quantity_kg, COALESCE(sb.quantity_unit, 'kg'),
		       k.name as kitchen_name, COALESCE(k.latitude, 28.6139) as kitchen_lat, COALESCE(k.longitude, 77.2090) as kitchen_lng,
		       rec.name as recipient_name, COALESCE(rec.latitude, 28.6139) as recipient_lat, COALESCE(rec.longitude, 77.2090) as recipient_lng,
		       r.distance_km, r.created_at
		FROM routes r
		JOIN surplus_batches sb ON r.batch_id = sb.id
		JOIN kitchens k ON sb.kitchen_id = k.id
		JOIN route_stops rs ON rs.route_id = r.id AND rs.sequence = 2
		JOIN recipients rec ON rs.recipient_id = rec.id
		WHERE r.status = 'assigned' AND r.driver_id = $1
		ORDER BY r.created_at DESC`

	rows, err := s.pool.Query(ctx, query, driverID)
	if err != nil {
		return nil, fmt.Errorf("query assigned deliveries: %w", err)
	}
	defer rows.Close()

	var items []AvailableDelivery
	for rows.Next() {
		var item AvailableDelivery
		if err := rows.Scan(
			&item.RouteID, &item.BatchID, &item.FoodName, &item.QuantityKg, &item.QuantityUnit,
			&item.KitchenName, &item.KitchenLat, &item.KitchenLng,
			&item.RecipientName, &item.RecipientLat, &item.RecipientLng,
			&item.DistanceKm, &item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan delivery: %w", err)
		}
		items = append(items, item)
	}
	return items, nil
}
