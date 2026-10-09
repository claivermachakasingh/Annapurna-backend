package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const sseAlertChannel = "sse:alerts"

// AlertService manages the lifecycle of system alerts.
type AlertService struct {
	db          *sql.DB
	rdb         *redis.Client
	asynqClient *asynq.Client
	log         *zap.Logger
}

// NewAlertService constructs an AlertService.
func NewAlertService(db *sql.DB, rdb *redis.Client, asynqClient *asynq.Client, log *zap.Logger) *AlertService {
	return &AlertService{
		db:          db,
		rdb:         rdb,
		asynqClient: asynqClient,
		log:         log,
	}
}

// Create persists a new alert, publishes to SSE, and enqueues a push task for WARN/CRITICAL.
func (s *AlertService) Create(ctx context.Context, req models.CreateAlertRequest) (*models.Alert, error) {
	now := time.Now().UTC()
	alert := &models.Alert{
		ID:        uuid.NewString(),
		KitchenID: req.KitchenID,
		Source:    req.Source,
		Severity:  req.Severity,
		Message:   req.Message,
		Status:    "OPEN",
		CreatedAt: now,
		UpdatedAt: now,
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO alerts (id, kitchen_id, source, severity, message, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		alert.ID, alert.KitchenID, alert.Source, alert.Severity,
		alert.Message, alert.Status, alert.CreatedAt, alert.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert alert: %w", err)
	}

	s.publishSSE(ctx, alert)

	if req.Severity == "WARN" || req.Severity == "CRITICAL" {
		if err := s.enqueuePush(ctx, alert); err != nil {
			s.log.Warn("failed to enqueue push task", zap.String("alert_id", alert.ID), zap.Error(err))
		}
	}

	return alert, nil
}

// List returns alerts scoped to a kitchen, with optional status filtering.
func (s *AlertService) List(ctx context.Context, kitchenID, status string, limit, offset int) ([]models.Alert, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `SELECT id, kitchen_id, source, severity, message, status, created_at, updated_at, acked_at, acked_by
	          FROM alerts WHERE kitchen_id = $1`
	args := []interface{}{kitchenID}

	if status != "" {
		args = append(args, status)
		query += fmt.Sprintf(" AND status = $%d", len(args))
	}
	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []models.Alert
	for rows.Next() {
		var a models.Alert
		if err := rows.Scan(
			&a.ID, &a.KitchenID, &a.Source, &a.Severity,
			&a.Message, &a.Status, &a.CreatedAt, &a.UpdatedAt,
			&a.AckedAt, &a.AckedBy,
		); err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

// Ack transitions an OPEN alert to ACKED and records the acknowledging user.
func (s *AlertService) Ack(ctx context.Context, alertID, userID string) (*models.Alert, error) {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
		UPDATE alerts
		SET status = 'ACKED', acked_at = $1, acked_by = $2, updated_at = $3
		WHERE id = $4 AND status = 'OPEN'`,
		now, userID, now, alertID,
	)
	if err != nil {
		return nil, fmt.Errorf("ack alert: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, fmt.Errorf("alert %s not found or already acked", alertID)
	}

	var a models.Alert
	err = s.db.QueryRowContext(ctx, `
		SELECT id, kitchen_id, source, severity, message, status, created_at, updated_at, acked_at, acked_by
		FROM alerts WHERE id = $1`, alertID,
	).Scan(
		&a.ID, &a.KitchenID, &a.Source, &a.Severity,
		&a.Message, &a.Status, &a.CreatedAt, &a.UpdatedAt,
		&a.AckedAt, &a.AckedBy,
	)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// publishSSE emits the alert to the kitchen-scoped SSE Redis pub/sub channel.
func (s *AlertService) publishSSE(ctx context.Context, alert *models.Alert) {
	raw, err := json.Marshal(alert)
	if err != nil {
		s.log.Warn("failed to marshal alert for SSE", zap.Error(err))
		return
	}
	channel := fmt.Sprintf("%s:%s", sseAlertChannel, alert.KitchenID)
	if err := s.rdb.Publish(ctx, channel, raw).Err(); err != nil {
		s.log.Warn("failed to publish SSE alert", zap.Error(err))
	}
}

// enqueuePush submits a push notification task via Asynq.
func (s *AlertService) enqueuePush(ctx context.Context, alert *models.Alert) error {
	payload := asynqx.PushPayload{
		UserIDs:   []string{},
		KitchenID: alert.KitchenID,
		EventType: "alert",
		Title:     fmt.Sprintf("[%s] %s", alert.Severity, alert.Source),
		Body:      alert.Message,
		Data: map[string]string{
			"alert_id": alert.ID,
			"severity": alert.Severity,
		},
	}
	task, err := asynqx.NewPushTask(payload)
	if err != nil {
		return err
	}
	_, err = s.asynqClient.EnqueueContext(ctx, task,
		asynq.MaxRetry(3),
		asynq.Timeout(30*time.Second),
	)
	return err
}
