package asynqx

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// Task type name constants.
const (
	TaskReports          = "reports:generate_esg"
	TaskPush             = "push:send"
	TaskExpirySweep      = "cron:expiry_sweep"
	TaskSensorBulk       = "sensor:bulk_ingest"
	TaskOfferExpiry      = "cron:offer_expiry"
	TaskDriftCheck       = "cron:drift_check"
	TaskStaleSensorCheck = "cron:stale_sensor_check"
)

// ReportPayload is the payload for TaskReports.
type ReportPayload struct {
	JobID     string    `json:"job_id"`
	KitchenID string    `json:"kitchen_id"`
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Format    string    `json:"format"` // "pdf" | "json"
}

// PushPayload is the payload for TaskPush.
type PushPayload struct {
	UserIDs   []string          `json:"user_ids"`
	KitchenID string            `json:"kitchen_id,omitempty"`
	EventType string            `json:"event_type"`
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Data      map[string]string `json:"data,omitempty"`
}

// SensorBulkPayload is the payload for TaskSensorBulk.
type SensorBulkPayload struct {
	KitchenID string            `json:"kitchen_id"`
	Readings  []json.RawMessage `json:"readings"`
}

// NewReportTask creates an Asynq task for ESG report generation.
func NewReportTask(p ReportPayload) (*asynq.Task, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal report payload: %w", err)
	}
	return asynq.NewTask(TaskReports, raw), nil
}

// NewPushTask creates an Asynq task for push notification delivery.
func NewPushTask(p PushPayload) (*asynq.Task, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal push payload: %w", err)
	}
	return asynq.NewTask(TaskPush, raw), nil
}

// NewSensorBulkTask creates an Asynq task for bulk sensor ingestion.
func NewSensorBulkTask(p SensorBulkPayload) (*asynq.Task, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal sensor bulk payload: %w", err)
	}
	return asynq.NewTask(TaskSensorBulk, raw), nil
}

// ParseReportPayload deserialises a ReportPayload from task bytes.
func ParseReportPayload(b []byte) (ReportPayload, error) {
	var p ReportPayload
	return p, json.Unmarshal(b, &p)
}

// ParsePushPayload deserialises a PushPayload from task bytes.
func ParsePushPayload(b []byte) (PushPayload, error) {
	var p PushPayload
	return p, json.Unmarshal(b, &p)
}

// ParseSensorBulkPayload deserialises a SensorBulkPayload from task bytes.
func ParseSensorBulkPayload(b []byte) (SensorBulkPayload, error) {
	var p SensorBulkPayload
	return p, json.Unmarshal(b, &p)
}
