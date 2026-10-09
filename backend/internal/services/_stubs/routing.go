package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"go.uber.org/zap"
)

// ErrRouteInfeasible is returned when the ML solver cannot find a valid route.
var ErrRouteInfeasible = errors.New("route is infeasible")

// RouteStop is a single delivery point in a route.
type RouteStop struct {
	RecipientID string  `json:"recipient_id"`
	Sequence    int     `json:"sequence"`
	Lat         float64 `json:"lat"`
	Lng         float64 `json:"lng"`
	ETA         *time.Time `json:"eta,omitempty"`
}

// RouteResult is the output of route planning.
type RouteResult struct {
	BatchID    string      `json:"batch_id"`
	Stops      []RouteStop `json:"stops"`
	TotalDistM float64     `json:"total_dist_m"`
	Solver     string      `json:"solver"`
	CreatedAt  time.Time   `json:"created_at"`
}

// RoutingService plans delivery routes using ML with haversine fallback.
type RoutingService struct {
	db  *sql.DB
	ml  *mlclient.Client
	log *zap.Logger
}

// NewRoutingService constructs a RoutingService.
func NewRoutingService(db *sql.DB, ml *mlclient.Client, log *zap.Logger) *RoutingService {
	return &RoutingService{db: db, ml: ml, log: log}
}

// BuildRoute plans a route for batchID visiting recipientIDs.
// It calls mlclient.BuildRoute with a 10s timeout; on ROUTE_INFEASIBLE returns 409.
// On other ML failures, it falls back to nearest-neighbour haversine.
// The result is persisted to the DB.
func (s *RoutingService) BuildRoute(ctx context.Context, batchID string, recipientIDs []string) (*RouteResult, error) {
	if batchID == "" {
		return nil, fmt.Errorf("batch_id is required")
	}
	if len(recipientIDs) == 0 {
		return nil, fmt.Errorf("at least one recipient_id is required")
	}

	// Load recipient coordinates.
	coords, err := s.loadRecipientCoords(ctx, recipientIDs)
	if err != nil {
		return nil, fmt.Errorf("load recipient coords: %w", err)
	}

	// Load origin (kitchen) coords from batch.
	origin, err := s.loadBatchOrigin(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("load batch origin: %w", err)
	}

	mlCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	mlResp, mlErr := s.ml.BuildRoute(mlCtx, mlclient.BuildRouteRequest{
		BatchID:      batchID,
		RecipientIDs: recipientIDs,
		Coords:       coords,
		Origin:       origin,
	})

	var result *RouteResult
	if mlErr != nil {
		if errors.Is(mlErr, mlclient.ErrRouteInfeasible) {
			return nil, ErrRouteInfeasible
		}
		s.log.Warn("mlclient.BuildRoute failed, using haversine fallback", zap.Error(mlErr))
		result = s.haversineFallback(batchID, origin, coords)
	} else {
		result = s.mapMLRoute(batchID, mlResp)
	}

	if err := s.persistRoute(ctx, result); err != nil {
		s.log.Error("failed to persist route", zap.String("batch_id", batchID), zap.Error(err))
	}

	return result, nil
}

// loadRecipientCoords fetches lat/lng for each recipient.
func (s *RoutingService) loadRecipientCoords(ctx context.Context, ids []string) (map[string][2]float64, error) {
	coords := make(map[string][2]float64, len(ids))
	for _, id := range ids {
		var lat, lng float64
		err := s.db.QueryRowContext(ctx,
			`SELECT lat, lng FROM recipients WHERE id = $1`, id,
		).Scan(&lat, &lng)
		if err != nil {
			return nil, fmt.Errorf("recipient %s: %w", id, err)
		}
		coords[id] = [2]float64{lat, lng}
	}
	return coords, nil
}

// loadBatchOrigin fetches the kitchen lat/lng for a batch.
func (s *RoutingService) loadBatchOrigin(ctx context.Context, batchID string) ([2]float64, error) {
	var lat, lng float64
	err := s.db.QueryRowContext(ctx, `
		SELECT k.lat, k.lng
		FROM surplus_batches sb
		JOIN kitchens k ON k.id = sb.kitchen_id
		WHERE sb.id = $1`, batchID,
	).Scan(&lat, &lng)
	if err != nil {
		return [2]float64{}, fmt.Errorf("batch %s origin: %w", batchID, err)
	}
	return [2]float64{lat, lng}, nil
}

// mapMLRoute converts the ML response to a RouteResult.
func (s *RoutingService) mapMLRoute(batchID string, resp *mlclient.BuildRouteResponse) *RouteResult {
	stops := make([]RouteStop, len(resp.Stops))
	for i, st := range resp.Stops {
		stops[i] = RouteStop{
			RecipientID: st.RecipientID,
			Sequence:    i + 1,
			Lat:         st.Lat,
			Lng:         st.Lng,
			ETA:         st.ETA,
		}
	}
	return &RouteResult{
		BatchID:    batchID,
		Stops:      stops,
		TotalDistM: resp.TotalDistM,
		Solver:     resp.Solver,
		CreatedAt:  time.Now().UTC(),
	}
}

// haversineFallback performs nearest-neighbour routing using the haversine formula.
func (s *RoutingService) haversineFallback(batchID string, origin [2]float64, coords map[string][2]float64) *RouteResult {
	type point struct {
		id  string
		lat float64
		lng float64
	}
	remaining := make([]point, 0, len(coords))
	for id, c := range coords {
		remaining = append(remaining, point{id: id, lat: c[0], lng: c[1]})
	}

	current := origin
	stops := make([]RouteStop, 0, len(remaining))
	totalDist := 0.0
	seq := 1

	for len(remaining) > 0 {
		sort.Slice(remaining, func(i, j int) bool {
			di := haversineM(current[0], current[1], remaining[i].lat, remaining[i].lng)
			dj := haversineM(current[0], current[1], remaining[j].lat, remaining[j].lng)
			return di < dj
		})
		nearest := remaining[0]
		remaining = remaining[1:]
		dist := haversineM(current[0], current[1], nearest.lat, nearest.lng)
		totalDist += dist
		stops = append(stops, RouteStop{
			RecipientID: nearest.id,
			Sequence:    seq,
			Lat:         nearest.lat,
			Lng:         nearest.lng,
		})
		current = [2]float64{nearest.lat, nearest.lng}
		seq++
	}

	return &RouteResult{
		BatchID:    batchID,
		Stops:      stops,
		TotalDistM: totalDist,
		Solver:     "haversine",
		CreatedAt:  time.Now().UTC(),
	}
}

// haversineM returns the great-circle distance in metres between two lat/lng points.
func haversineM(lat1, lng1, lat2, lng2 float64) float64 {
	const R = 6371000 // Earth radius in metres
	φ1 := lat1 * math.Pi / 180
	φ2 := lat2 * math.Pi / 180
	Δφ := (lat2 - lat1) * math.Pi / 180
	Δλ := (lng2 - lng1) * math.Pi / 180
	a := math.Sin(Δφ/2)*math.Sin(Δφ/2) +
		math.Cos(φ1)*math.Cos(φ2)*math.Sin(Δλ/2)*math.Sin(Δλ/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}

// persistRoute saves the route and its stops to the DB.
func (s *RoutingService) persistRoute(ctx context.Context, r *RouteResult) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO routes (batch_id, total_dist_m, solver, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (batch_id) DO UPDATE
		SET total_dist_m = EXCLUDED.total_dist_m,
		    solver       = EXCLUDED.solver,
		    created_at   = EXCLUDED.created_at`,
		r.BatchID, r.TotalDistM, r.Solver, r.CreatedAt,
	)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `DELETE FROM route_stops WHERE batch_id = $1`, r.BatchID)
	if err != nil {
		return err
	}

	for _, stop := range r.Stops {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO route_stops (batch_id, recipient_id, sequence, lat, lng, eta)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			r.BatchID, stop.RecipientID, stop.Sequence, stop.Lat, stop.Lng, stop.ETA,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetRoute retrieves a persisted route from the DB.
func (s *RoutingService) GetRoute(ctx context.Context, batchID string) (*RouteResult, error) {
	var result RouteResult
	err := s.db.QueryRowContext(ctx,
		`SELECT batch_id, total_dist_m, solver, created_at FROM routes WHERE batch_id = $1`,
		batchID,
	).Scan(&result.BatchID, &result.TotalDistM, &result.Solver, &result.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("route %s: %w", batchID, err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT recipient_id, sequence, lat, lng, eta FROM route_stops WHERE batch_id = $1 ORDER BY sequence`,
		batchID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var stop RouteStop
		if err := rows.Scan(&stop.RecipientID, &stop.Sequence, &stop.Lat, &stop.Lng, &stop.ETA); err != nil {
			return nil, err
		}
		result.Stops = append(result.Stops, stop)
	}
	return &result, rows.Err()
}
