// Package metrics registers all Prometheus metrics for the SIH26234 backend.
// Call Register() once during application startup (before serving traffic).
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// HTTP layer.
var (
	// HTTPRequestDuration tracks latency for every HTTP request by method,
	// route pattern, and status code.
	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "sih26234",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration in seconds.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"method", "route", "status_code"},
	)
)

// Sidecar (ML container) layer.
var (
	// SidecarRequestDuration tracks round-trip time to the ML sidecar per endpoint.
	SidecarRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "sih26234",
			Subsystem: "sidecar",
			Name:      "request_duration_seconds",
			Help:      "ML sidecar request duration in seconds.",
			Buckets:   []float64{.01, .05, .1, .25, .5, 1, 2, 5, 10},
		},
		[]string{"endpoint"},
	)
)

// CV inference.
var (
	// CVInferenceSeconds tracks time spent waiting for computer-vision batch
	// inference responses.
	CVInferenceSeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "sih26234",
			Subsystem: "cv",
			Name:      "inference_seconds",
			Help:      "Computer-vision inference latency in seconds.",
			Buckets:   []float64{.1, .5, 1, 2, 4, 8, 15},
		},
	)
)

// ML predictions.
var (
	// MLPredictSeconds tracks demand-prediction call latency.
	MLPredictSeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "sih26234",
			Subsystem: "ml",
			Name:      "predict_seconds",
			Help:      "ML demand-prediction latency in seconds.",
			Buckets:   prometheus.DefBuckets,
		},
	)
)

// Safety decisions.
var (
	// SafetyDecisionsTotal counts food-safety assessment outcomes by status
	// (e.g. "safe", "unsafe", "hold", "error").
	SafetyDecisionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "sih26234",
			Subsystem: "safety",
			Name:      "decisions_total",
			Help:      "Total food-safety decisions by status.",
		},
		[]string{"status"},
	)
)

// QR / chain verification.
var (
	// QRChainVerifyFailures counts QR cold-chain verification failures.
	QRChainVerifyFailures = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "sih26234",
			Subsystem: "qr",
			Name:      "chain_verify_failures_total",
			Help:      "Total QR cold-chain verification failures.",
		},
	)
)

// Offline sync.
var (
	// SyncBatchSize is a histogram of offline-sync payload sizes (record count).
	SyncBatchSize = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "sih26234",
			Subsystem: "sync",
			Name:      "batch_size",
			Help:      "Number of records in an offline-sync batch.",
			Buckets:   []float64{1, 5, 10, 25, 50, 100, 250, 500},
		},
	)

	// SyncRejectedTotal counts sync batches rejected (e.g. stale, duplicate,
	// schema error).
	SyncRejectedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "sih26234",
			Subsystem: "sync",
			Name:      "rejected_total",
			Help:      "Total offline-sync batches rejected.",
		},
	)
)

// Push notifications.
var (
	// PushSentTotal counts successfully sent push notifications.
	PushSentTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "sih26234",
			Subsystem: "push",
			Name:      "sent_total",
			Help:      "Total push notifications sent successfully.",
		},
	)

	// PushFailuresTotal counts push-notification delivery failures.
	PushFailuresTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "sih26234",
			Subsystem: "push",
			Name:      "failures_total",
			Help:      "Total push-notification delivery failures.",
		},
	)
)

// Redis health.
var (
	// RedisDegraded is a gauge that is set to 1 when the backend is operating
	// in graceful-degrade mode (Redis unreachable) and 0 when healthy.
	RedisDegraded = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "sih26234",
			Subsystem: "redis",
			Name:      "degraded",
			Help:      "1 when Redis is unreachable and the backend is in graceful-degrade mode.",
		},
	)
)

// Register is a no-op when promauto is used (metrics self-register at package
// init via promauto). It exists so callers have an explicit registration call
// site and can confirm all metrics are initialised before the first scrape.
func Register() {
	// All metrics above are registered by promauto at package init.
	// This function intentionally left with no-op body.
}

// PrometheusHandler returns the standard Prometheus /metrics HTTP handler.
func PrometheusHandler() http.Handler {
	return promhttp.Handler()
}
