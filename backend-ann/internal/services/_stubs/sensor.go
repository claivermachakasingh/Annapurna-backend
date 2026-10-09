package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	sensorStreamKey  = "stream:sensor"
	sensorLatestKey  = "sensor:latest"
	sensorPubChannel = "chan:kitchen"
	alertDedupeTTL   = 10 * time.Minute
	staleSensorAge   = 24 * time.Hour
)

// SensorReading is a single measurement from a kitchen sensor.
type SensorReading struct {
	SensorID  string            `json:"sensor_id"`
	KitchenID string            `json:"kitchen_id"`
	Timestamp time.Time         `json:"timestamp"`
	Temp      *float64          `json:"temperature_c,omitempty"`
	Humidity  *float64          `json:"humidity_pct,omitempty"`
	EnergyKWh *float64          `json:"energy_kwh,omitempty"`
	Extra     map[string]string `json:"extra,omitempty"`
}

// SensorService manages sensor data ingestion and retrieval.
type SensorService struct {
	db           *sql.DB
	rdb          *redis.Client
	alertService *AlertService
	log          *zap.Logger
}

// NewSensorService constructs a SensorService.
func NewSensorService(db *sql.DB, rdb *redis.Client, alerts *AlertService, log *zap.Logger) *SensorService {
	return &SensorService{
		db:           db,
		rdb:          rdb,
		alertService: alerts,
		log:          log,
	}
}

// Ingest validates and persists a batch of sensor readings.
// Stale readings (>24h) are rejected. Valid ones are streamed to Redis,
// their latest snapshot is stored in a hash, and kitchen channel is published.
func (s *SensorService) Ingest(ctx context.Context, readings []SensorReading) error {
	var firstErr error
	validCount := 0
	for _, r := range readings {
		if err := s.validateReading(r); err != nil {
			s.log.Warn("invalid sensor reading, skipping",
				zap.String("sensor_id", r.SensorID), zap.Error(err))
			if firstErr == nil {
				firstErr = fmt.Errorf("sensor %s: %w", r.SensorID, err)
			}
			continue
		}
		validCount++

		if err := s.xaddStream(ctx, r); err != nil {
			s.log.Error("XADD failed", zap.String("sensor_id", r.SensorID), zap.Error(err))
		}
		if err := s.hsetLatest(ctx, r); err != nil {
			s.log.Error("HSET latest failed", zap.String("sensor_id", r.SensorID), zap.Error(err))
		}
		s.publishKitchen(ctx, r)
		s.evaluateThresholds(ctx, r)
	}

	if validCount == 0 && len(readings) > 0 {
		return firstErr
	}
	return nil
}

// validateReading checks value ranges and staleness.
func (s *SensorService) validateReading(r SensorReading) error {
	if r.SensorID == "" {
		return fmt.Errorf("sensor_id is required")
	}
	if r.KitchenID == "" {
		return fmt.Errorf("kitchen_id is required")
	}
	if time.Since(r.Timestamp) > staleSensorAge {
		return fmt.Errorf("stale reading: timestamp %s is older than 24h", r.Timestamp)
	}
	if r.Temp != nil && (*r.Temp < -30 || *r.Temp > 120) {
		return fmt.Errorf("temperature out of range [-30, 120]: %.2f", *r.Temp)
	}
	if r.Humidity != nil && (*r.Humidity < 0 || *r.Humidity > 100) {
		return fmt.Errorf("humidity out of range [0, 100]: %.2f", *r.Humidity)
	}
	if r.EnergyKWh != nil && *r.EnergyKWh < 0 {
		return fmt.Errorf("energy_kwh must be >= 0: %.4f", *r.EnergyKWh)
	}
	return nil
}

// xaddStream appends the reading to stream:sensor.
func (s *SensorService) xaddStream(ctx context.Context, r SensorReading) error {
	fields := map[string]interface{}{
		"sensor_id":  r.SensorID,
		"kitchen_id": r.KitchenID,
		"timestamp":  r.Timestamp.UnixMilli(),
	}
	if r.Temp != nil {
		fields["temperature_c"] = strconv.FormatFloat(*r.Temp, 'f', 4, 64)
	}
	if r.Humidity != nil {
		fields["humidity_pct"] = strconv.FormatFloat(*r.Humidity, 'f', 4, 64)
	}
	if r.EnergyKWh != nil {
		fields["energy_kwh"] = strconv.FormatFloat(*r.EnergyKWh, 'f', 6, 64)
	}
	return s.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: sensorStreamKey,
		Values: fields,
	}).Err()
}

// hsetLatest updates sensor:latest hash with the most recent JSON reading.
func (s *SensorService) hsetLatest(ctx context.Context, r SensorReading) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.rdb.HSet(ctx, sensorLatestKey, r.SensorID, raw).Err()
}

// publishKitchen broadcasts the reading to chan:kitchen:<kitchenID>.
func (s *SensorService) publishKitchen(ctx context.Context, r SensorReading) {
	raw, err := json.Marshal(r)
	if err != nil {
		s.log.Warn("failed to marshal reading for publish", zap.Error(err))
		return
	}
	channel := fmt.Sprintf("%s:%s", sensorPubChannel, r.KitchenID)
	if err := s.rdb.Publish(ctx, channel, raw).Err(); err != nil {
		s.log.Warn("failed to publish sensor reading", zap.Error(err))
	}
}

// evaluateThresholds checks values against threshold rules and creates deduplicated alerts.
func (s *SensorService) evaluateThresholds(ctx context.Context, r SensorReading) {
	type rule struct {
		field  string
		value  float64
		warnLo *float64
		warnHi *float64
		critLo *float64
		critHi *float64
	}

	fp := func(v float64) *float64 { return &v }

	var rules []rule
	if r.Temp != nil {
		rules = append(rules, rule{
			field:  "temperature_c",
			value:  *r.Temp,
			warnLo: fp(0),
			warnHi: fp(90),
			critLo: fp(-10),
			critHi: fp(110),
		})
	}
	if r.Humidity != nil {
		rules = append(rules, rule{
			field:  "humidity_pct",
			value:  *r.Humidity,
			warnHi: fp(85),
			critHi: fp(95),
		})
	}
	if r.EnergyKWh != nil {
		rules = append(rules, rule{
			field:  "energy_kwh",
			value:  *r.EnergyKWh,
			warnHi: fp(500),
			critHi: fp(1000),
		})
	}

	for _, rl := range rules {
		severity := ""
		switch {
		case rl.critLo != nil && rl.value < *rl.critLo:
			severity = "CRITICAL"
		case rl.critHi != nil && rl.value > *rl.critHi:
			severity = "CRITICAL"
		case rl.warnLo != nil && rl.value < *rl.warnLo:
			severity = "WARN"
		case rl.warnHi != nil && rl.value > *rl.warnHi:
			severity = "WARN"
		}
		if severity == "" {
			continue
		}

		// Deduplicate: only one alert per sensor+field per 10 minutes.
		dedupeKey := fmt.Sprintf("alert:dedupe:%s:%s:%s", r.KitchenID, r.SensorID, rl.field)
		exists, _ := s.rdb.Exists(ctx, dedupeKey).Result()
		if exists > 0 {
			continue
		}
		_ = s.rdb.Set(ctx, dedupeKey, "1", alertDedupeTTL).Err()

		msg := fmt.Sprintf("Sensor %s: %s=%.2f exceeded %s threshold", r.SensorID, rl.field, rl.value, severity)
		_, err := s.alertService.Create(ctx, models.CreateAlertRequest{
			KitchenID: r.KitchenID,
			Source:    "sensor",
			Severity:  severity,
			Message:   msg,
		})
		if err != nil {
			s.log.Error("failed to create sensor alert", zap.Error(err))
		}
	}
}

// GetLatest returns the most recent reading for each sensor belonging to a kitchen.
func (s *SensorService) GetLatest(ctx context.Context, kitchenID string) ([]SensorReading, error) {
	all, err := s.rdb.HGetAll(ctx, sensorLatestKey).Result()
	if err != nil {
		return nil, err
	}
	var readings []SensorReading
	for _, raw := range all {
		var r SensorReading
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			continue
		}
		if r.KitchenID == kitchenID {
			readings = append(readings, r)
		}
	}
	return readings, nil
}

// GetHistory retrieves sensor readings from the stream for the given kitchen in the time window.
func (s *SensorService) GetHistory(ctx context.Context, kitchenID string, from, to time.Time) ([]SensorReading, error) {
	entries, err := s.rdb.XRange(ctx, sensorStreamKey,
		strconv.FormatInt(from.UnixMilli(), 10)+"-0",
		strconv.FormatInt(to.UnixMilli(), 10)+"-0",
	).Result()
	if err != nil {
		return nil, err
	}

	var readings []SensorReading
	for _, e := range entries {
		r := SensorReading{Extra: map[string]string{}}
		if v, ok := e.Values["sensor_id"]; ok {
			r.SensorID = fmt.Sprintf("%v", v)
		}
		if v, ok := e.Values["kitchen_id"]; ok {
			r.KitchenID = fmt.Sprintf("%v", v)
		}
		if r.KitchenID != kitchenID {
			continue
		}
		if v, ok := e.Values["timestamp"]; ok {
			ms, _ := strconv.ParseInt(fmt.Sprintf("%v", v), 10, 64)
			r.Timestamp = time.UnixMilli(ms).UTC()
		}
		if v, ok := e.Values["temperature_c"]; ok {
			if f, err := strconv.ParseFloat(fmt.Sprintf("%v", v), 64); err == nil {
				r.Temp = &f
			}
		}
		if v, ok := e.Values["humidity_pct"]; ok {
			if f, err := strconv.ParseFloat(fmt.Sprintf("%v", v), 64); err == nil {
				r.Humidity = &f
			}
		}
		if v, ok := e.Values["energy_kwh"]; ok {
			if f, err := strconv.ParseFloat(fmt.Sprintf("%v", v), 64); err == nil {
				r.EnergyKWh = &f
			}
		}
		readings = append(readings, r)
	}
	return readings, nil
}

// CheckStaleSensors is called by cron to flag sensors that have gone silent.
func (s *SensorService) CheckStaleSensors(ctx context.Context) error {
	all, err := s.rdb.HGetAll(ctx, sensorLatestKey).Result()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for sensorID, raw := range all {
		var r SensorReading
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			continue
		}
		if now.Sub(r.Timestamp) > staleSensorAge {
			msg := fmt.Sprintf("Sensor %s has not reported for over 24h (last: %s)", sensorID, r.Timestamp.Format(time.RFC3339))
			_, err := s.alertService.Create(ctx, models.CreateAlertRequest{
				KitchenID: r.KitchenID,
				Source:    "sensor-stale",
				Severity:  "WARN",
				Message:   msg,
			})
			if err != nil {
				s.log.Error("failed to create stale sensor alert",
					zap.String("sensor_id", sensorID), zap.Error(err))
			}
		}
	}
	return nil
}
