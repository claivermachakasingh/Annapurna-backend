package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/sih26234/food-waste/internal/files"
	"github.com/sih26234/food-waste/internal/httpapi"
	"github.com/sih26234/food-waste/internal/services"
)

type QualityHandler struct {
	quality     *services.QualityService
	maxUploadMB int64
	uploadsDir  string
}

func NewQualityHandler(q *services.QualityService, maxUploadMB int64, uploadsDir string) *QualityHandler {
	if maxUploadMB <= 0 {
		maxUploadMB = 8
	}
	if uploadsDir == "" {
		uploadsDir = "storage/uploads"
	}
	return &QualityHandler{quality: q, maxUploadMB: maxUploadMB, uploadsDir: uploadsDir}
}

// Check handles POST /quality/check (multipart/form-data).
//
// The uploaded image is validated (size, magic bytes, decodability) and
// re-encoded server-side before being stored; the authoritative safety
// decision is always produced by the quality service, never by the client.
// `device_preview` is accepted as telemetry only (D20).
func (h *QualityHandler) Check(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireClaims(w, r)
	if !ok {
		return
	}
	if !requireRole(w, claims, "KITCHEN") {
		return
	}

	if err := r.ParseMultipartForm((h.maxUploadMB << 20) + (1 << 20)); err != nil {
		httpapi.NewValidation("failed to parse multipart form", err.Error()).Render(w)
		return
	}

	batchID := r.FormValue("batch_id")
	if batchID == "" {
		httpapi.NewValidation("batch_id is required", nil).Render(w)
		return
	}

	// ── image upload validation & storage ────────────────────────────────────
	file, header, err := r.FormFile("image")
	if err != nil {
		httpapi.NewValidation("image file is required", err.Error()).Render(w)
		return
	}
	defer file.Close()

	if err := files.ValidateUpload(header, int(h.maxUploadMB)); err != nil {
		switch {
		case errors.Is(err, files.ErrFileTooLarge):
			httpapi.ErrImageTooLarge().Render(w)
		case errors.Is(err, files.ErrInvalidMagicBytes):
			httpapi.ErrImageUnsupported().Render(w)
		default:
			httpapi.ErrImageUnreadable().Render(w)
		}
		return
	}

	if _, err := file.Seek(0, 0); err != nil {
		httpapi.NewInternal("failed to rewind upload").Render(w)
		return
	}
	_, storedPath, err := files.SaveUpload(file, h.uploadsDir)
	if err != nil {
		httpapi.NewInternal("failed to store upload").Render(w)
		return
	}

	// ── optional fields ──────────────────────────────────────────────────────
	var tempC *float64
	if tempStr := r.FormValue("temperature_c"); tempStr != "" {
		if val, convErr := strconv.ParseFloat(tempStr, 64); convErr == nil {
			tempC = &val
		}
	}

	var preview *json.RawMessage
	if prevStr := r.FormValue("device_preview"); prevStr != "" && json.Valid([]byte(prevStr)) {
		raw := json.RawMessage(prevStr)
		preview = &raw
	}

	res, err := h.quality.CheckQuality(r.Context(), batchID, claims.Subject, storedPath, tempC, preview)
	if err != nil {
		renderServiceError(w, err)
		return
	}

	// Contract shape (08_API_CONTRACT) — the flat fields are kept for backward
	// compatibility with the earlier mobile client builds.
	resp := map[string]any{
		"batch_id": res.BatchID,
		"visual": map[string]any{
			"status":        res.VisualStatus,
			"risk_level":    res.RiskLevel,
			"confidence":    res.Confidence,
			"reason":        res.Reason,
			"detections":    []any{},
			"model_version": "cv-v1",
		},
		"safety_decision": map[string]any{
			"status":                  res.SafetyDecision,
			"reasons":                 []string{res.Reason},
			"danger_zone_minutes":     res.DangerZoneMinutes,
			"model_version":           "fusion-v1",
			"requires_human_approval": true,
		},
		"requires_human_approval": true,
		"quality_status":          res.VisualStatus,
		"risk_level":              res.RiskLevel,
		"confidence":              res.Confidence,
		"reason":                  res.Reason,
		"model_version":           "cv-v1",
		"image_path":              storedPath,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
