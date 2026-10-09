package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sih26234/backend/internal/service"
)

// SensorsHandler holds dependencies for IoT sensor endpoints.
type SensorsHandler struct {
	sensorSvc service.SensorService
}

// NewSensorsHandler constructs a SensorsHandler.
func NewSensorsHandler(sensorSvc service.SensorService) *SensorsHandler {
	return &SensorsHandler{sensorSvc: sensorSvc}
}

// RegisterRoutes mounts sensor routes onto r.
func (h *SensorsHandler) RegisterRoutes(r chi.Router) {
	r.Post("/sensors/readings", h.PostReadings)
	r.Get("/sensors/latest", h.Latest)
	r.Get("/sensors/history", h.History)
}

// ─── Request / Response types ────────────────────────────────────────────────

// SensorReading represents one IoT reading payload.
type SensorReading struct {
	SensorID    string     `json:"sensor_id"`
	DeviceID    string     `json:"device_id"`
	KitchenID   string     `json:"kitchen_id"`
	TempC       *float64   `json:"temp_c,omitempty"`
	Humidity    *float64   `json:"humidity_pct,omitempty"`
	CO2PPM      *float64   `json:"co2_ppm,omitempty"`
	WeightKg    *float64   `json:"weight_kg,omitempty"`
	ReadingAt   time.Time  `json:"reading_at"`
}

// PostReadingsRequest is the body for POST /sensors/readings.
type PostReadingsRequest struct {
	Readings []SensorReading `json:"readings"`
}

// PostReadingsResponse is returned by POST /sensors/readings.
type PostReadingsResponse struct {
	Accepted int      `json:"accepted"`
	Rejected int      `json:"rejected"`
	Errors   []string `json:"errors,omitempty"`
}

// LatestReadingItem is one row in GET /sensors/latest.
type LatestReadingItem struct {
	SensorID  string     `json:"sensor_id"`
	KitchenID string     `json:"kitchen_id"`
	TempC     *float64   `json:"temp_c,omitempty"`
	Humidity  *float64   `json:"humidity_pct,omitempty"`
	CO2PPM    *float64   `json:"co2_ppm,omitempty"`
	WeightKg  *float64   `json:"weight_kg,omitempty"`
	ReadingAt time.Time  `json:"reading_at"`
	Status    string     `json:"status"` // ok | warning | critical
}

// HistoryPoint is a single time-series point.
type HistoryPoint struct {
	Timestamp time.Time `json:"ts"`
	Value     float64   `json:"value"`
}

// SensorHistoryResponse is returned by GET /sensors/history.
type SensorHistoryResponse struct {
	SensorID  string         `json:"sensor_id"`
	Metric    string         `json:"metric"`
	Points    []HistoryPoint `json:"points"`
	From      time.Time      `json:"from"`
	To        time.Time      `json:"to"`
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// PostReadings handles POST /sensors/readings.
//
//	@Summary      Ingest sensor readings
//	@Description  Accept a batch of IoT sensor readings for temperature, humidity, CO2, and weight.
//	@Tags         sensors
//	@Accept       json
//	@Produce      json
//	@Security     BearerAuth
//	@Param        body body PostReadingsRequest true "Sensor readings batch"
//	@Success      207 {object} PostReadingsResponse
//	@Failure      400 {object} ErrorResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /sensors/readings [post]
func (h *SensorsHandler) PostReadings(w http.ResponseWriter, r *http.Request) {
	var req PostReadingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if len(req.Readings) == 0 {
		writeError(w, http.StatusBadRequest, "at least one reading is required", nil)
		return
	}

	inputs := make([]service.SensorReading, len(req.Readings))
	for i, rd := range req.Readings {
		inputs[i] = service.SensorReading{
			SensorID:  rd.SensorID,
			DeviceID:  rd.DeviceID,
			KitchenID: rd.KitchenID,
			TempC:     rd.TempC,
			Humidity:  rd.Humidity,
			CO2PPM:    rd.CO2PPM,
			WeightKg:  rd.WeightKg,
			ReadingAt: rd.ReadingAt,
		}
	}

	result, err := h.sensorSvc.IngestReadings(r.Context(), inputs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to ingest readings", err)
		return
	}

	writeJSON(w, http.StatusMultiStatus, PostReadingsResponse{
		Accepted: result.Accepted,
		Rejected: result.Rejected,
		Errors:   result.Errors,
	})
}

// Latest handles GET /sensors/latest.
//
//	@Summary      Latest sensor readings
//	@Description  Return the most recent reading for every sensor, optionally filtered by kitchen.
//	@Tags         sensors
//	@Produce      json
//	@Security     BearerAuth
//	@Param        kitchen_id query string false "Filter by kitchen"
//	@Success      200 {array} LatestReadingItem
//	@Failure      500 {object} ErrorResponse
//	@Router       /sensors/latest [get]
func (h *SensorsHandler) Latest(w http.ResponseWriter, r *http.Request) {
	kitchenID := r.URL.Query().Get("kitchen_id")

	items, err := h.sensorSvc.LatestReadings(r.Context(), kitchenID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch latest readings", err)
		return
	}

	resp := make([]LatestReadingItem, len(items))
	for i, item := range items {
		resp[i] = LatestReadingItem{
			SensorID:  item.SensorID,
			KitchenID: item.KitchenID,
			TempC:     item.TempC,
			Humidity:  item.Humidity,
			CO2PPM:    item.CO2PPM,
			WeightKg:  item.WeightKg,
			ReadingAt: item.ReadingAt,
			Status:    item.Status,
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// History handles GET /sensors/history.
//
//	@Summary      Sensor reading history
//	@Description  Return a time-series of readings for a specific sensor and metric.
//	@Tags         sensors
//	@Produce      json
//	@Security     BearerAuth
//	@Param        sensor_id query string true  "Sensor ID"
//	@Param        metric    query string true  "Metric: temp_c|humidity_pct|co2_ppm|weight_kg"
//	@Param        from      query string false "Start RFC3339"
//	@Param        to        query string false "End RFC3339"
//	@Success      200 {object} SensorHistoryResponse
//	@Failure      400 {object} ErrorResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /sensors/history [get]
func (h *SensorsHandler) History(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sensorID := q.Get("sensor_id")
	metric := q.Get("metric")
	if sensorID == "" || metric == "" {
		writeError(w, http.StatusBadRequest, "sensor_id and metric are required", nil)
		return
	}

	result, err := h.sensorSvc.History(r.Context(), service.SensorHistoryInput{
		SensorID: sensorID,
		Metric:   metric,
		From:     q.Get("from"),
		To:       q.Get("to"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch history", err)
		return
	}

	points := make([]HistoryPoint, len(result.Points))
	for i, p := range result.Points {
		points[i] = HistoryPoint{Timestamp: p.Timestamp, Value: p.Value}
	}

	writeJSON(w, http.StatusOK, SensorHistoryResponse{
		SensorID: result.SensorID,
		Metric:   result.Metric,
		Points:   points,
		From:     result.From,
		To:       result.To,
	})
}
