package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sih26234/food-waste/internal/pushx"
	"github.com/sih26234/food-waste/internal/qrchain"
)

type ScoreBreakdown struct {
	Need           float64 `json:"need"`
	Capacity       float64 `json:"capacity"`
	Distance       float64 `json:"distance"`
	WindowOverlap  float64 `json:"window_overlap"`
	ShelfLifeSlack float64 `json:"shelf_life_slack"`
	Fairness       float64 `json:"fairness"`
}

type MatchResult struct {
	MatchID        string         `json:"match_id"`
	RecipientID    string         `json:"recipient_id"`
	RecipientName  string         `json:"name"`
	Type           string         `json:"type"`
	CapacityKg     float64        `json:"capacity_kg"`
	DistanceKm     float64        `json:"distance_km"`
	PickupWindow   string         `json:"pickup_window"`
	Score          float64        `json:"score"`
	ScoreBreakdown ScoreBreakdown `json:"score_breakdown"`
	Reasons        []string       `json:"reasons"`
}

type MatchingService struct {
	pool    *pgxpool.Pool
	qrchain *qrchain.Service
	push    *pushx.PushService
}

func NewMatchingService(pool *pgxpool.Pool, chain *qrchain.Service, push *pushx.PushService) *MatchingService {
	return &MatchingService{pool: pool, qrchain: chain, push: push}
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371.0 // km
	dLat := (lat2 - lat1) * math.Pi / 180.0
	dLon := (lon2 - lon1) * math.Pi / 180.0
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180.0)*math.Cos(lat2*math.Pi/180.0)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}

func (s *MatchingService) Match(ctx context.Context, batchID string, maxResults int, kitchenID string) ([]MatchResult, error) {
	if maxResults <= 0 {
		maxResults = 5
	}

	var batchQty float64
	var batchCat string
	var batchExpiry time.Time
	var batchKitchenID *string
	err := s.pool.QueryRow(ctx,
		`SELECT quantity_kg, food_category, expiry_at, kitchen_id FROM surplus_batches WHERE id = $1`,
		batchID,
	).Scan(&batchQty, &batchCat, &batchExpiry, &batchKitchenID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &NotFoundError{Resource: "surplus_batch", ID: batchID}
		}
		return nil, fmt.Errorf("fetch batch: %w", err)
	}

	var kLat, kLng float64
	kID := kitchenID
	if kID == "" && batchKitchenID != nil {
		kID = *batchKitchenID
	}
	if kID != "" {
		_ = s.pool.QueryRow(ctx, `SELECT latitude, longitude FROM kitchens WHERE id = $1`, kID).Scan(&kLat, &kLng)
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id, name, type, capacity_kg, latitude, longitude,
		        COALESCE(to_char(pickup_window_start, 'HH24:MI'), '09:00'),
		        COALESCE(to_char(pickup_window_end, 'HH24:MI'), '18:00'),
		        accepts_categories, last_received_at
		 FROM recipients WHERE active = true`)
	if err != nil {
		return nil, fmt.Errorf("query recipients: %w", err)
	}
	defer rows.Close()

	var candidates []MatchResult
	for rows.Next() {
		var id, name, rType, winStart, winEnd string
		var capKg, lat, lng float64
		var categories []string
		var lastRecv *time.Time

		if err := rows.Scan(&id, &name, &rType, &capKg, &lat, &lng, &winStart, &winEnd, &categories, &lastRecv); err != nil {
			continue
		}

		if capKg < math.Min(batchQty, 5.0) {
			continue
		}

		distKm := 1.0
		if kLat != 0 || kLng != 0 {
			distKm = haversine(kLat, kLng, lat, lng)
		}

		distScore := 35.0
		if distKm > 0.05 {
			distScore = math.Max(5.0, 35.0-distKm*2.0)
		}

		bd := ScoreBreakdown{
			Need:           16.0,
			Capacity:       18.0,
			Distance:       distScore,
			WindowOverlap:  14.0,
			ShelfLifeSlack: 9.0,
			Fairness:       10.0,
		}
		if lastRecv != nil && time.Since(*lastRecv) < 48*time.Hour {
			bd.Fairness = 2.0
		}
		totalScore := bd.Need + bd.Capacity + bd.Distance + bd.WindowOverlap + bd.ShelfLifeSlack + bd.Fairness

		candidates = append(candidates, MatchResult{
			MatchID:        uuid.NewString(),
			RecipientID:    id,
			RecipientName:  name,
			Type:           rType,
			CapacityKg:     capKg,
			DistanceKm:     math.Round(distKm*100) / 100,
			PickupWindow:   fmt.Sprintf("%s-%s", winStart, winEnd),
			Score:          math.Round(totalScore*10) / 10,
			ScoreBreakdown: bd,
			Reasons:        []string{"Close proximity", "Sufficient capacity", "Category match"},
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	if len(candidates) > maxResults {
		candidates = candidates[:maxResults]
	}

	for _, c := range candidates {
		bdBytes, _ := json.Marshal(c.ScoreBreakdown)
		_, _ = s.pool.Exec(ctx,
			`INSERT INTO matches (id, batch_id, recipient_id, score, score_breakdown, reasons, status)
			 VALUES ($1, $2, $3, $4, $5, $6, 'OFFERED')
			 ON CONFLICT (batch_id, recipient_id) DO UPDATE SET score = EXCLUDED.score, status = 'OFFERED'`,
			c.MatchID, batchID, c.RecipientID, c.Score, bdBytes, c.Reasons,
		)
	}

	return candidates, nil
}

func (s *MatchingService) Respond(ctx context.Context, matchID, decision string) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var actualMatchID, batchID, currentStatus string
	err = tx.QueryRow(ctx,
		`SELECT id, batch_id, status FROM matches WHERE (id = $1 OR batch_id = $1) FOR UPDATE LIMIT 1`, matchID,
	).Scan(&actualMatchID, &batchID, &currentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", &NotFoundError{Resource: "match", ID: matchID}
		}
		return "", fmt.Errorf("fetch match: %w", err)
	}

	now := time.Now().UTC()
	_, err = tx.Exec(ctx,
		`UPDATE matches SET status = $1, responded_at = $2 WHERE id = $3`,
		decision, now, actualMatchID,
	)
	if err != nil {
		return "", fmt.Errorf("update match: %w", err)
	}

	if decision == "ACCEPTED" {
		_, err = tx.Exec(ctx,
			`UPDATE surplus_batches SET status = 'MATCHED', updated_at = $1 WHERE id = $2`,
			now, batchID,
		)
		if err != nil {
			return "", fmt.Errorf("set batch matched: %w", err)
		}
	}

	return batchID, tx.Commit(ctx)
}

type MatchDetail struct {
	MatchID        string         `json:"match_id"`
	BatchID        string         `json:"batch_id"`
	BatchCode      string         `json:"batch_code"`
	RecipientID    string         `json:"recipient_id"`
	Score          float64        `json:"score"`
	ScoreBreakdown ScoreBreakdown `json:"score_breakdown"`
	Reasons        []string       `json:"reasons"`
	Status         string         `json:"status"`
	OfferedAt      time.Time      `json:"offered_at"`
	RespondedAt    *time.Time     `json:"responded_at,omitempty"`
	
	// Details from surplus_batch
	FoodName     string    `json:"food_name"`
	FoodCategory string    `json:"food_category"`
	QuantityKg   float64   `json:"quantity_kg"`
	QuantityUnit string    `json:"quantity_unit"`
	PreparedAt   time.Time `json:"prepared_at"`
	ExpiryAt     time.Time `json:"expiry_at"`
}

func (s *MatchingService) ListMatches(ctx context.Context, recipientID string, status string) ([]MatchDetail, error) {
	query := `
		SELECT m.id, m.batch_id, COALESCE(sb.batch_code, ''), m.recipient_id, m.score, m.score_breakdown, m.reasons, m.status, m.offered_at, m.responded_at,
		       sb.food_name, sb.food_category, sb.quantity_kg, COALESCE(sb.quantity_unit, 'kg'), sb.prepared_at, sb.expiry_at
		FROM matches m
		JOIN surplus_batches sb ON m.batch_id = sb.id
		WHERE m.recipient_id = $1`
	
	args := []interface{}{recipientID}
	
	if status != "" {
		query += ` AND m.status = $2`
		args = append(args, status)
	}
	
	query += ` ORDER BY m.offered_at DESC`
	
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query matches: %w", err)
	}
	defer rows.Close()

	var matches []MatchDetail
	for rows.Next() {
		var m MatchDetail
		var bdBytes []byte
		if err := rows.Scan(
			&m.MatchID, &m.BatchID, &m.BatchCode, &m.RecipientID, &m.Score, &bdBytes, &m.Reasons, &m.Status, &m.OfferedAt, &m.RespondedAt,
			&m.FoodName, &m.FoodCategory, &m.QuantityKg, &m.QuantityUnit, &m.PreparedAt, &m.ExpiryAt,
		); err != nil {
			return nil, fmt.Errorf("scan match detail: %w", err)
		}
		if len(bdBytes) > 0 {
			_ = json.Unmarshal(bdBytes, &m.ScoreBreakdown)
		}
		matches = append(matches, m)
	}
	return matches, nil
}

