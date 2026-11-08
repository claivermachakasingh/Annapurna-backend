// Package mlclient provides a typed HTTP client for the SIH26234 ML sidecar.
// Each endpoint has its own circuit breaker and per-call timeout.
// Mock fallbacks return deterministic contract-shaped fixtures.
package mlclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sony/gobreaker"
)

// Error sentinels.
var (
	ErrCVUnavailable = errors.New("CV_UNAVAILABLE")
	ErrMLUnavailable = errors.New("ML_UNAVAILABLE")
)

// Request / Response structs.

type PredictDemandRequest struct {
	KitchenID    int                    `json:"kitchen_id"`
	FoodCategory string                 `json:"food_category"`
	DateTime     time.Time              `json:"date_time"`
	Features     map[string]interface{} `json:"features,omitempty"`
}

// PredictionInterval contains the model's prediction quantiles.
type PredictionInterval struct {
	P10            float64 `json:"p10"`
	P50            float64 `json:"p50"`
	P90            float64 `json:"p90"`
	CoverageTarget float64 `json:"coverage_target"`
}

// PredictDemandResponse contains the complete forecasting model response.
type PredictDemandResponse struct {
	PredictedConsumption  int                `json:"predicted_consumption"`
	RecommendedProduction int                `json:"recommended_production"`
	ExpectedSurplus       int                `json:"expected_surplus"`
	SurplusRisk           string             `json:"surplus_risk"`
	PredictionInterval    PredictionInterval `json:"prediction_interval"`
	RecommendedQuantile   float64            `json:"recommended_quantile"`
	OperatingPointSetBy   string             `json:"operating_point_set_by"`
	TopDrivers            []map[string]any   `json:"top_drivers"`
	Confidence            float64            `json:"confidence"`
	ModelVersion          string             `json:"model_version"`
	DataSource            string             `json:"data_source"`
	ExpectedShortageKg    float64            `json:"expected_shortage_kg"`
	HistoryUsed           bool               `json:"history_used"`
	Caveat                string             `json:"caveat"`
}

type AssessSafetyRequest struct {
	BatchID         string    `json:"batch_id"`
	FoodCategory    string    `json:"food_category"`
	PreparedAt      time.Time `json:"prepared_at"`
	TemperatureC    float64   `json:"temperature_c"`
	DangerZoneMins  float64   `json:"danger_zone_mins"`
	StorageType     string    `json:"storage_type"`
	PackagingStatus string    `json:"packaging_status"`
}

type AssessSafetyResponse struct {
	Safe           bool    `json:"safe"`
	Status         string  `json:"status"`
	RiskScore      float64 `json:"risk_score"`
	Recommendation string  `json:"recommendation"`
	ModelVersion   string  `json:"model_version"`
}

type CvPredictResponse struct {
	BatchID        string            `json:"batch_id"`
	DetectedItems  []DetectedItem    `json:"detected_items"`
	FreshnessScore float64           `json:"freshness_score"`
	EstimatedQty   float64           `json:"estimated_qty_kg"`
	Confidence     float64           `json:"confidence"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type DetectedItem struct {
	Label      string  `json:"label"`
	Confidence float64 `json:"confidence"`
	BBoxX      float64 `json:"bbox_x"`
	BBoxY      float64 `json:"bbox_y"`
	BBoxW      float64 `json:"bbox_w"`
	BBoxH      float64 `json:"bbox_h"`
}

type BuildRouteRequest struct {
	DonorLat    float64   `json:"donor_lat"`
	DonorLon    float64   `json:"donor_lon"`
	Recipients  []LatLon  `json:"recipients"`
	MaxStops    int       `json:"max_stops,omitempty"`
	Deadline    time.Time `json:"deadline,omitempty"`
	VehicleType string    `json:"vehicle_type,omitempty"`
}

type LatLon struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
	ID  string  `json:"id,omitempty"`
}

type BuildRouteResponse struct {
	OrderedStops     []LatLon `json:"ordered_stops"`
	TotalDistanceKm  float64  `json:"total_distance_km"`
	EstimatedMinutes int      `json:"estimated_minutes"`
	ModelVersion     string   `json:"model_version"`
}

type DetectAnomaliesRequest struct {
	KitchenID   int                    `json:"kitchen_id"`
	MetricName  string                 `json:"metric_name"`
	WindowHours int                    `json:"window_hours"`
	DataPoints  []TimeSeriesPoint      `json:"data_points"`
	Features    map[string]interface{} `json:"features,omitempty"`
}

type TimeSeriesPoint struct {
	TS    time.Time `json:"ts"`
	Value float64   `json:"value"`
}

type DetectAnomaliesResponse struct {
	Anomalies    []AnomalyPoint `json:"anomalies"`
	AnomalyScore float64        `json:"anomaly_score"`
	ModelVersion string         `json:"model_version"`
}

type AnomalyPoint struct {
	TS       time.Time `json:"ts"`
	Value    float64   `json:"value"`
	Severity string    `json:"severity"`
}

// MLClient is a typed HTTP client for the ML sidecar.
type MLClient struct {
	baseURL     string
	token       string
	http        *http.Client
	mockML      bool
	mockCV      bool
	mockRouting bool

	cbDemand  *gobreaker.CircuitBreaker
	cbSafety  *gobreaker.CircuitBreaker
	cbCV      *gobreaker.CircuitBreaker
	cbRouting *gobreaker.CircuitBreaker
	cbAnomaly *gobreaker.CircuitBreaker
}

// NewMLClient constructs an MLClient.
func NewMLClient(baseURL, token string, mockML, mockCV, mockRouting bool) *MLClient {
	if os.Getenv("FEATURE_MOCK_ML") == "true" {
		mockML = true
	}
	if os.Getenv("FEATURE_MOCK_CV") == "true" {
		mockCV = true
	}
	if os.Getenv("FEATURE_MOCK_ROUTING") == "true" {
		mockRouting = true
	}

	newCB := func(name string) *gobreaker.CircuitBreaker {
		return gobreaker.NewCircuitBreaker(gobreaker.Settings{
			Name:        name,
			MaxRequests: 3,
			Interval:    30 * time.Second,
			Timeout:     60 * time.Second,
			ReadyToTrip: func(counts gobreaker.Counts) bool {
				return counts.ConsecutiveFailures >= 5
			},
		})
	}

	return &MLClient{
		baseURL:     strings.TrimRight(baseURL, "/"),
		token:       token,
		http:        &http.Client{},
		mockML:      mockML,
		mockCV:      mockCV,
		mockRouting: mockRouting,
		cbDemand:    newCB("ml.demand"),
		cbSafety:    newCB("ml.safety"),
		cbCV:        newCB("ml.cv"),
		cbRouting:   newCB("ml.routing"),
		cbAnomaly:   newCB("ml.anomaly"),
	}
}

// PredictDemand calls the demand forecasting model.
func (c *MLClient) PredictDemand(ctx context.Context, req PredictDemandRequest) (*PredictDemandResponse, error) {
	if c.mockML {
		return &PredictDemandResponse{
			PredictedConsumption:  50,
			RecommendedProduction: 55,
			ExpectedSurplus:       5,
			SurplusRisk:           "LOW",
			PredictionInterval: PredictionInterval{
				P10:            40,
				P50:            50,
				P90:            60,
				CoverageTarget: 0.8,
			},
			RecommendedQuantile: 0.85,
			TopDrivers:          []map[string]any{},
			Confidence:          0.82,
			ModelVersion:        "mock-v1",
			DataSource:          "MOCK",
			HistoryUsed:         false,
		}, nil
	}

	res, err := executeBreaker[PredictDemandResponse](
		ctx,
		c.cbDemand,
		func() (*PredictDemandResponse, error) {
			// Allow time for a Render free instance to wake up.
			tctx, cancel := context.WithTimeout(ctx, 120*time.Second)
			defer cancel()

			return post[PredictDemandResponse](tctx, c, "/predict/demand", req)
		},
	)
	if err != nil {
		return nil, wrapML(err)
	}
	return res, nil
}

// AssessSafety calls the safety assessment endpoint.
func (c *MLClient) AssessSafety(ctx context.Context, req AssessSafetyRequest) (*AssessSafetyResponse, error) {
	if c.mockML {
		return &AssessSafetyResponse{
			Safe:           true,
			Status:         "safe",
			RiskScore:      0.1,
			Recommendation: "No action required.",
			ModelVersion:   "mock-v1",
		}, nil
	}

	res, err := executeBreaker[AssessSafetyResponse](ctx, c.cbSafety, func() (*AssessSafetyResponse, error) {
		tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		return post[AssessSafetyResponse](tctx, c, "/assess/safety", req)
	})
	if err != nil {
		return nil, wrapML(err)
	}
	return res, nil
}

// CvPredict sends an image to the CV endpoint.
func (c *MLClient) CvPredict(ctx context.Context, imagePath, batchID string) (*CvPredictResponse, error) {
	if c.mockCV {
		return &CvPredictResponse{
			BatchID:        batchID,
			FreshnessScore: 0.87,
			EstimatedQty:   4.5,
			Confidence:     0.91,
			DetectedItems: []DetectedItem{
				{Label: "bread", Confidence: 0.94, BBoxX: 10, BBoxY: 10, BBoxW: 80, BBoxH: 60},
			},
		}, nil
	}

	res, err := executeBreaker[CvPredictResponse](ctx, c.cbCV, func() (*CvPredictResponse, error) {
		tctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		return cvPost(tctx, c, imagePath, batchID)
	})
	if err != nil {
		return nil, wrapCV(err)
	}
	return res, nil
}

// BuildRoute calls the route optimisation endpoint.
func (c *MLClient) BuildRoute(ctx context.Context, req BuildRouteRequest) (*BuildRouteResponse, error) {
	if c.mockRouting {
		stops := make([]LatLon, len(req.Recipients))
		copy(stops, req.Recipients)

		return &BuildRouteResponse{
			OrderedStops:     stops,
			TotalDistanceKm:  12.4,
			EstimatedMinutes: 28,
			ModelVersion:     "mock-v1",
		}, nil
	}

	res, err := executeBreaker[BuildRouteResponse](ctx, c.cbRouting, func() (*BuildRouteResponse, error) {
		tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return post[BuildRouteResponse](tctx, c, "/routing/build", req)
	})
	if err != nil {
		return nil, wrapML(err)
	}
	return res, nil
}

// DetectAnomalies calls the anomaly detection endpoint.
func (c *MLClient) DetectAnomalies(ctx context.Context, req DetectAnomaliesRequest) (*DetectAnomaliesResponse, error) {
	if c.mockML {
		return &DetectAnomaliesResponse{
			Anomalies:    []AnomalyPoint{},
			AnomalyScore: 0.05,
			ModelVersion: "mock-v1",
		}, nil
	}

	res, err := executeBreaker[DetectAnomaliesResponse](ctx, c.cbAnomaly, func() (*DetectAnomaliesResponse, error) {
		tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return post[DetectAnomaliesResponse](tctx, c, "/anomaly/detect", req)
	})
	if err != nil {
		return nil, wrapML(err)
	}
	return res, nil
}

// post is a generic JSON POST helper.
func post[T any](ctx context.Context, c *MLClient, path string, body interface{}) (*T, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("mlclient: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("mlclient: build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mlclient: do request %q: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("mlclient: %q returned %d: %s", path, resp.StatusCode, body)
	}

	var result T
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("mlclient: decode response %q: %w", path, err)
	}

	return &result, nil
}

// cvPost builds a multipart/form-data request for the CV endpoint.
func cvPost(ctx context.Context, c *MLClient, imagePath, batchID string) (*CvPredictResponse, error) {
	f, err := os.Open(imagePath)
	if err != nil {
		return nil, fmt.Errorf("mlclient: open image %q: %w", imagePath, err)
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	_ = mw.WriteField("batch_id", batchID)

	part, err := mw.CreateFormFile("image", filepath.Base(imagePath))
	if err != nil {
		return nil, fmt.Errorf("mlclient: create form file: %w", err)
	}

	if _, err := io.Copy(part, f); err != nil {
		return nil, fmt.Errorf("mlclient: copy image: %w", err)
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("mlclient: close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/cv/predict", &buf)
	if err != nil {
		return nil, fmt.Errorf("mlclient: build cv request: %w", err)
	}

	req.Header.Set("Content-Type", mw.FormDataContentType())
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mlclient: do cv request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("mlclient: cv returned %d: %s", resp.StatusCode, body)
	}

	var result CvPredictResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("mlclient: decode cv response: %w", err)
	}

	return &result, nil
}

// executeBreaker wraps fn inside a circuit breaker.
func executeBreaker[T any](ctx context.Context, cb *gobreaker.CircuitBreaker, fn func() (*T, error)) (*T, error) {
	result, err := cb.Execute(func() (interface{}, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		return fn()
	})

	if err != nil {
		return nil, err
	}

	return result.(*T), nil
}

// wrapML converts sidecar errors to ML_UNAVAILABLE.
func wrapML(err error) error {
	return fmt.Errorf("%w: %v", ErrMLUnavailable, err)
}

// wrapCV converts sidecar errors to CV_UNAVAILABLE.
func wrapCV(err error) error {
	return fmt.Errorf("%w: %v", ErrCVUnavailable, err)
}
