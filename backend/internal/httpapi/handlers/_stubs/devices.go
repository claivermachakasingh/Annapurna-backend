package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sih26234/backend/internal/domain"
	"github.com/sih26234/backend/internal/service"
)

// DevicesHandler holds dependencies for device registration endpoints.
type DevicesHandler struct {
	deviceSvc service.DeviceService
}

// NewDevicesHandler constructs a DevicesHandler.
func NewDevicesHandler(deviceSvc service.DeviceService) *DevicesHandler {
	return &DevicesHandler{deviceSvc: deviceSvc}
}

// RegisterRoutes mounts device routes onto r.
func (h *DevicesHandler) RegisterRoutes(r chi.Router) {
	r.Post("/devices", h.Register)
	r.Delete("/devices/{id}", h.Deregister)
}

// ─── Request / Response types ────────────────────────────────────────────────

// RegisterDeviceRequest is the body for POST /devices.
type RegisterDeviceRequest struct {
	DeviceToken string                `json:"device_token"`
	Platform    domain.DevicePlatform `json:"platform"`
	AppVersion  string                `json:"app_version,omitempty"`
	KitchenID   string                `json:"kitchen_id,omitempty"`
	UserID      string                `json:"user_id,omitempty"`
}

// RegisterDeviceResponse is returned by POST /devices.
type RegisterDeviceResponse struct {
	DeviceID    string                `json:"device_id"`
	DeviceToken string                `json:"device_token"`
	Platform    domain.DevicePlatform `json:"platform"`
	RegisteredAt time.Time            `json:"registered_at"`
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// Register handles POST /devices.
//
//	@Summary      Register a device
//	@Description  Register a mobile device for push notifications.
//	@Tags         devices
//	@Accept       json
//	@Produce      json
//	@Security     BearerAuth
//	@Param        body body RegisterDeviceRequest true "Device registration"
//	@Success      201 {object} RegisterDeviceResponse
//	@Failure      400 {object} ErrorResponse
//	@Failure      409 {object} ErrorResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /devices [post]
func (h *DevicesHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if req.DeviceToken == "" || req.Platform == "" {
		writeError(w, http.StatusBadRequest, "device_token and platform are required", nil)
		return
	}

	result, err := h.deviceSvc.Register(r.Context(), service.RegisterDeviceInput{
		DeviceToken: req.DeviceToken,
		Platform:    req.Platform,
		AppVersion:  req.AppVersion,
		KitchenID:   req.KitchenID,
		UserID:      req.UserID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to register device", err)
		return
	}

	writeJSON(w, http.StatusCreated, RegisterDeviceResponse{
		DeviceID:     result.DeviceID,
		DeviceToken:  result.DeviceToken,
		Platform:     result.Platform,
		RegisteredAt: result.RegisteredAt,
	})
}

// Deregister handles DELETE /devices/{id}.
//
//	@Summary      Deregister a device
//	@Description  Remove a mobile device from push notification registry.
//	@Tags         devices
//	@Produce      json
//	@Security     BearerAuth
//	@Param        id path string true "Device ID"
//	@Success      204 "No content"
//	@Failure      404 {object} ErrorResponse
//	@Failure      500 {object} ErrorResponse
//	@Router       /devices/{id} [delete]
func (h *DevicesHandler) Deregister(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "device id is required", nil)
		return
	}

	if err := h.deviceSvc.Deregister(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to deregister device", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
