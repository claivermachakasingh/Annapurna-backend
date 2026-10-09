package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type SyncItem struct {
	ClientEventID string          `json:"client_event_id"`
	Kind          string          `json:"kind"`
	Payload       json.RawMessage `json:"payload"`
	ClientTS      *time.Time      `json:"client_ts,omitempty"`
}

type SyncItemResult struct {
	ClientEventID string `json:"client_event_id"`
	Status        string `json:"status"`
	Code          string `json:"code,omitempty"`
	Body          any    `json:"body,omitempty"`
}

type SyncResult struct {
	Results    []SyncItemResult `json:"results"`
	Applied    int              `json:"applied"`
	Duplicates int              `json:"duplicates"`
}

type SyncService struct {
	pool    *pgxpool.Pool
	redis   *redis.Client
	surplus *SurplusService
	quality *QualityService
	qrSvc   *QRService
}

func NewSyncService(pool *pgxpool.Pool, rdb *redis.Client, surplus *SurplusService, quality *QualityService, qr *QRService) *SyncService {
	return &SyncService{pool: pool, redis: rdb, surplus: surplus, quality: quality, qrSvc: qr}
}

func (s *SyncService) ProcessBatch(ctx context.Context, userID, role, kitchenID string, items []SyncItem) (*SyncResult, error) {
	result := &SyncResult{
		Results: make([]SyncItemResult, 0, len(items)),
	}

	for _, item := range items {
		if item.ClientEventID == "" {
			result.Results = append(result.Results, SyncItemResult{
				ClientEventID: item.ClientEventID,
				Status:        "REJECTED",
				Code:          "MISSING_CLIENT_EVENT_ID",
			})
			continue
		}

		if s.redis != nil {
			dedupKey := fmt.Sprintf("sync:dedupe:%s:%s", userID, item.ClientEventID)
			ok, _ := s.redis.SetNX(ctx, dedupKey, "seen", 24*time.Hour).Result()
			if !ok {
				result.Duplicates++
				result.Results = append(result.Results, SyncItemResult{
					ClientEventID: item.ClientEventID,
					Status:        "DUPLICATE",
				})
				continue
			}
		}

		ir := s.processItem(ctx, userID, role, kitchenID, item)
		if ir.Status == "ACCEPTED" {
			result.Applied++
		} else if ir.Status == "DUPLICATE" {
			result.Duplicates++
		}
		result.Results = append(result.Results, ir)
	}

	return result, nil
}

func (s *SyncService) processItem(ctx context.Context, userID, role, kitchenID string, item SyncItem) SyncItemResult {
	base := SyncItemResult{ClientEventID: item.ClientEventID}

	switch item.Kind {
	case "surplus":
		var req CreateSurplusRequest
		if err := json.Unmarshal(item.Payload, &req); err != nil {
			base.Status = "REJECTED"
			base.Code = "MALFORMED_PAYLOAD"
			return base
		}
		req.ClientEventID = &item.ClientEventID
		b, err := s.surplus.CreateSurplus(ctx, kitchenID, userID, req)
		if err != nil {
			base.Status = "REJECTED"
			base.Code = "SURPLUS_CREATE_FAILED"
			return base
		}
		base.Status = "ACCEPTED"
		base.Body = b
		return base

	case "qr_event":
		type qrPayload struct {
			BatchID      string   `json:"batch_id"`
			EventType    string   `json:"event_type"`
			Lat          *float64 `json:"lat,omitempty"`
			Lng          *float64 `json:"lng,omitempty"`
			EvidenceHash *string  `json:"evidence_hash,omitempty"`
		}
		var p qrPayload
		if err := json.Unmarshal(item.Payload, &p); err != nil {
			base.Status = "REJECTED"
			base.Code = "MALFORMED_PAYLOAD"
			return base
		}
		cid := item.ClientEventID
		res, err := s.qrSvc.RecordEvent(ctx, p.BatchID, userID, role, p.EventType, p.Lat, p.Lng, p.EvidenceHash, &cid, item.ClientTS)
		if err != nil {
			base.Status = "REJECTED"
			base.Code = "QR_EVENT_FAILED"
			return base
		}
		base.Status = "ACCEPTED"
		base.Body = res
		return base

	default:
		base.Status = "ACCEPTED"
		return base
	}
}
