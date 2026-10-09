package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sih26234/backend/internal/service"
)

// ReportsHandler holds dependencies for ESG report endpoints.
type ReportsHandler struct {
	reportSvc service.ReportService
}

// NewReportsHandler constructs a ReportsHandler.
func NewReportsHandler(reportSvc service.ReportService) *ReportsHandler {
	return &ReportsHandler{reportSvc: reportSvc}
}

// RegisterRoutes mounts report routes onto r.
func (h *ReportsHandler) RegisterRoutes(r chi.Router) {
	r.Get("/reports/esg", h.ESG)
	r.Get("/reports/jobs/{id}", h.JobStatus)
}

// ─── Response types ───────────────────────────────────────────────────────────

// ESGMetric is a single named ESG metric.
type ESGMetric struct {
	Name      string  `json:"name"`
	Value     float64 `json:"value"`
	Unit      string  `json:"unit"`
	Benchmark float64 `json:"benchmark,omitempty"`
}

// ESGReportResponse is returned by GET /reports/esg.
type ESGReportResponse struct {
	ReportID    string      `json:"report_id"`
	Period      string      `json:"period"`
	From        time.Time   `json:"from"`
	To          time.Time   `json:"to"`
	Environment []ESGMetric `json:"environment"`
	Social      []ESGMetric `json:"social"`
	Governance  []ESGMetric `json:"governance"`
	GeneratedAt time.Time   `json:"generated_at"`
	DownloadURL string      `json:"download_url,omitempty"`
}

// ReportJobResponse is returned by GET /reports/jobs/{id}.
type ReportJobResponse struct {
	JobID       string     `json:"job_id"`
	Status      string     `json:"status"` // queued | running | done | failed
	Progress    int        `json:"progress_pct"`
	DownloadURL string     `json:"download_url,omitempty"`
	Error       string     `json:"error,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// ESG handles GET /reports/esg.
//
//	@Summary      ESG report
//	@Description  Generate or retrieve a cached ESG compliance report for the given period.
//	@Tags         reports
//	@Produce      json
//	@Security     BearerAuth
//	@Param        kitchen_id query string false "Filter by kitchen"
//	@Param        from       query string false "Start date (YYYY-MM-DD)"
//	@Param        to         query string false "End date (YYYY-MM-DD)"
//	@Param        format     query string false "Output format: json|pdf (default json)"
//	@Success      200 {object} ESGReportResponse
//	@Success      202 {object} ReportJobResponse "Report is being generated"
//	@Failure      500 {object} ErrorResponse
//	@Router       /reports/esg [get]
func (h *ReportsHandler) ESG(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kitchenID := q.Get("kitchen_id")
	from := q.Get("from")
	to := q.Get("to")
	format := q.Get("format")
	if format == "" {
		format = "json"
	}

	result, err := h.reportSvc.ESG(r.Context(), service.ESGInput{
		KitchenID: kitchenID,
		From:      from,
		To:        to,
		Format:    format,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate ESG report", err)
		return
	}

	// If report is still being generated, return 202 with job info.
	if result.Async {
		writeJSON(w, http.StatusAccepted, ReportJobResponse{
			JobID:     result.JobID,
			Status:    "queued",
			Progress:  0,
			StartedAt: result.StartedAt,
		})
		return
	}

	env := make([]ESGMetric, len(result.Environment))
	for i, m := range result.Environment {
		env[i] = ESGMetric{Name: m.Name, Value: m.Value, Unit: m.Unit, Benchmark: m.Benchmark}
	}
	social := make([]ESGMetric, len(result.Social))
	for i, m := range result.Social {
		social[i] = ESGMetric{Name: m.Name, Value: m.Value, Unit: m.Unit, Benchmark: m.Benchmark}
	}
	gov := make([]ESGMetric, len(result.Governance))
	for i, m := range result.Governance {
		gov[i] = ESGMetric{Name: m.Name, Value: m.Value, Unit: m.Unit, Benchmark: m.Benchmark}
	}

	writeJSON(w, http.StatusOK, ESGReportResponse{
		ReportID:    result.ReportID,
		Period:      result.Period,
		From:        result.From,
		To:          result.To,
		Environment: env,
		Social:      social,
		Governance:  gov,
		GeneratedAt: result.GeneratedAt,
		DownloadURL: result.DownloadURL,
	})
}

// JobStatus handles GET /reports/jobs/{id}.
//
//	@Summary      Report job status
//	@Description  Poll the status of an async report generation job.
//	@Tags         reports
//	@Produce      json
//	@Security     BearerAuth
//	@Param        id path string true "Job ID"
//	@Success      200 {object} ReportJobResponse
//	@Failure      404 {object} ErrorResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /reports/jobs/{id} [get]
func (h *ReportsHandler) JobStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "job id is required", nil)
		return
	}

	result, err := h.reportSvc.JobStatus(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get job status", err)
		return
	}
	if result == nil {
		writeError(w, http.StatusNotFound, "job not found", nil)
		return
	}

	writeJSON(w, http.StatusOK, ReportJobResponse{
		JobID:       result.JobID,
		Status:      result.Status,
		Progress:    result.Progress,
		DownloadURL: result.DownloadURL,
		Error:       result.Error,
		StartedAt:   result.StartedAt,
		FinishedAt:  result.FinishedAt,
	})
}
