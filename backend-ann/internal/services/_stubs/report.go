package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	reportJobPrefix  = "job:"
	reportJobTTL     = 24 * time.Hour
	fastReportLimit  = 2 * time.Second
)

// JobStatus represents the state of an async report job.
type JobStatus struct {
	JobID     string      `json:"job_id"`
	Status    string      `json:"status"` // PENDING, RUNNING, DONE, FAILED
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	ResultURL string      `json:"result_url,omitempty"`
	Error     string      `json:"error,omitempty"`
}

// ESGRequest parameters for ESG report generation.
type ESGRequest struct {
	KitchenID string
	From      time.Time
	To        time.Time
	Format    string // "pdf" or "json"
}

// ReportService handles ESG report generation, both inline and async.
type ReportService struct {
	db              *sql.DB
	rdb             *redis.Client
	asynqClient     *asynq.Client
	analyticsService *AnalyticsService
	log             *zap.Logger
}

// NewReportService constructs a ReportService.
func NewReportService(
	db *sql.DB,
	rdb *redis.Client,
	asynqClient *asynq.Client,
	analytics *AnalyticsService,
	log *zap.Logger,
) *ReportService {
	return &ReportService{
		db:               db,
		rdb:              rdb,
		asynqClient:      asynqClient,
		analyticsService: analytics,
		log:              log,
	}
}

// GenerateESG generates an ESG report. If estimated time > 2s, it enqueues an
// Asynq job and returns (jobID, 202). Otherwise it returns (jobID, 200) with
// the result stored synchronously.
func (s *ReportService) GenerateESG(ctx context.Context, req ESGRequest) (jobID string, statusCode int, err error) {
	if req.KitchenID == "" {
		return "", 0, fmt.Errorf("kitchen_id is required")
	}
	if req.Format == "" {
		req.Format = "pdf"
	}
	req.Format = strings.ToLower(req.Format)

	jobID = uuid.NewString()
	now := time.Now().UTC()

	// Estimate: longer date ranges are slower.
	estimated := s.estimateDuration(req)

	js := &JobStatus{
		JobID:     jobID,
		Status:    "PENDING",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.saveJobStatus(ctx, js); err != nil {
		return "", 0, fmt.Errorf("save job status: %w", err)
	}

	if estimated > fastReportLimit {
		payload := asynqx.ReportPayload{
			JobID:     jobID,
			KitchenID: req.KitchenID,
			From:      req.From,
			To:        req.To,
			Format:    req.Format,
		}
		task, err := asynqx.NewReportTask(payload)
		if err != nil {
			return "", 0, err
		}
		_, err = s.asynqClient.EnqueueContext(ctx, task,
			asynq.MaxRetry(2),
			asynq.Timeout(5*time.Minute),
		)
		if err != nil {
			return "", 0, fmt.Errorf("enqueue report task: %w", err)
		}
		return jobID, 202, nil
	}

	// Fast path: generate inline.
	go func() {
		bgCtx := context.Background()
		pdfBytes, genErr := s.generatePDF(bgCtx, req)
		js.UpdatedAt = time.Now().UTC()
		if genErr != nil {
			js.Status = "FAILED"
			js.Error = genErr.Error()
		} else {
			redisKey := fmt.Sprintf("report:result:%s", jobID)
			_ = s.rdb.Set(bgCtx, redisKey, pdfBytes, reportJobTTL).Err()
			js.Status = "DONE"
			js.ResultURL = fmt.Sprintf("/reports/%s/download", jobID)
		}
		_ = s.saveJobStatus(bgCtx, js)
	}()

	return jobID, 200, nil
}

// GetJobStatus retrieves the status of a report generation job.
func (s *ReportService) GetJobStatus(ctx context.Context, jobID string) (*JobStatus, error) {
	raw, err := s.rdb.Get(ctx, reportJobPrefix+jobID).Bytes()
	if err != nil {
		return nil, fmt.Errorf("job %s not found", jobID)
	}
	var js JobStatus
	if err := json.Unmarshal(raw, &js); err != nil {
		return nil, err
	}
	return &js, nil
}

// GetResult fetches the raw PDF bytes for a completed report.
func (s *ReportService) GetResult(ctx context.Context, jobID string) ([]byte, error) {
	data, err := s.rdb.Get(ctx, fmt.Sprintf("report:result:%s", jobID)).Bytes()
	if err != nil {
		return nil, fmt.Errorf("result for job %s not found", jobID)
	}
	return data, nil
}

// saveJobStatus persists a JobStatus to Redis.
func (s *ReportService) saveJobStatus(ctx context.Context, js *JobStatus) error {
	raw, err := json.Marshal(js)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, reportJobPrefix+js.JobID, raw, reportJobTTL).Err()
}

// estimateDuration estimates generation time based on the date range.
func (s *ReportService) estimateDuration(req ESGRequest) time.Duration {
	days := req.To.Sub(req.From).Hours() / 24
	if days > 90 {
		return 5 * time.Second
	}
	if days > 30 {
		return 3 * time.Second
	}
	return 1 * time.Second
}

// generatePDF builds the ESG PDF using maroto v2.
func (s *ReportService) generatePDF(ctx context.Context, req ESGRequest) ([]byte, error) {
	impact, err := s.analyticsService.GetImpact(ctx, req.KitchenID)
	if err != nil {
		return nil, fmt.Errorf("get impact: %w", err)
	}
	return buildESGPDF(req, impact)
}
