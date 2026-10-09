package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sih26234/backend/internal/domain"
	"github.com/sih26234/backend/internal/service"
)

// AdminHandler holds dependencies for admin, kitchen-ops and model management endpoints.
type AdminHandler struct {
	adminSvc service.AdminService
}

// NewAdminHandler constructs an AdminHandler.
func NewAdminHandler(adminSvc service.AdminService) *AdminHandler {
	return &AdminHandler{adminSvc: adminSvc}
}

// RegisterRoutes mounts admin and kitchen routes onto r.
func (h *AdminHandler) RegisterRoutes(r chi.Router) {
	// User management
	r.Get("/admin/users", h.ListUsers)
	r.Post("/admin/users", h.CreateUser)
	r.Patch("/admin/users/{id}", h.UpdateUser)

	// Model registry
	r.Get("/admin/models", h.ListModels)

	// Audit log
	r.Get("/admin/audit-log", h.AuditLog)

	// Data re-seed (dev/staging)
	r.Post("/admin/reseed", h.Reseed)

	// Kitchen ops
	r.Get("/kitchen/overview", h.KitchenOverview)
	r.Post("/kitchen/attendance", h.PostAttendance)
	r.Post("/kitchen/menu", h.PostMenu)
}

// ─── User types ───────────────────────────────────────────────────────────────

// AdminUserItem represents a user in the admin list.
type AdminUserItem struct {
	UserID    string      `json:"user_id"`
	Email     string      `json:"email"`
	Name      string      `json:"name"`
	Role      domain.Role `json:"role"`
	KitchenID string      `json:"kitchen_id,omitempty"`
	Active    bool        `json:"active"`
	CreatedAt time.Time   `json:"created_at"`
}

// ListUsersResponse is returned by GET /admin/users.
type ListUsersResponse struct {
	Users []AdminUserItem `json:"users"`
	Total int             `json:"total"`
	Page  int             `json:"page"`
	Limit int             `json:"limit"`
}

// CreateUserRequest is the body for POST /admin/users.
type CreateUserRequest struct {
	Email     string      `json:"email"`
	Name      string      `json:"name"`
	Role      domain.Role `json:"role"`
	KitchenID string      `json:"kitchen_id,omitempty"`
	Password  string      `json:"password"`
}

// UpdateUserRequest is the body for PATCH /admin/users/{id}.
type UpdateUserRequest struct {
	Name      *string      `json:"name,omitempty"`
	Role      *domain.Role `json:"role,omitempty"`
	Active    *bool        `json:"active,omitempty"`
	KitchenID *string      `json:"kitchen_id,omitempty"`
}

// ─── Model registry types ─────────────────────────────────────────────────────

// ModelRegistryItem describes one registered ML model.
type ModelRegistryItem struct {
	ModelID     string    `json:"model_id"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Type        string    `json:"type"` // demand_forecast | visual_inspection | routing
	Status      string    `json:"status"` // active | shadow | retired
	Accuracy    float64   `json:"accuracy,omitempty"`
	DeployedAt  time.Time `json:"deployed_at"`
}

// ─── Audit log types ──────────────────────────────────────────────────────────

// AuditLogEntry is one row in the audit log.
type AuditLogEntry struct {
	EntryID    string    `json:"entry_id"`
	ActorID    string    `json:"actor_id"`
	ActorEmail string    `json:"actor_email"`
	Action     string    `json:"action"`
	Resource   string    `json:"resource"`
	ResourceID string    `json:"resource_id,omitempty"`
	IPAddress  string    `json:"ip_address,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// AuditLogResponse is returned by GET /admin/audit-log.
type AuditLogResponse struct {
	Entries []AuditLogEntry `json:"entries"`
	Total   int             `json:"total"`
	Page    int             `json:"page"`
	Limit   int             `json:"limit"`
}

// ─── Kitchen types ────────────────────────────────────────────────────────────

// KitchenOverviewResponse is returned by GET /kitchen/overview.
type KitchenOverviewResponse struct {
	KitchenID         string    `json:"kitchen_id"`
	Name              string    `json:"name"`
	TodayHeadcount    int       `json:"today_headcount"`
	PredictedCount    int       `json:"predicted_count"`
	TodayWasteKg      float64   `json:"today_waste_kg"`
	TodayRedirectedKg float64   `json:"today_redirected_kg"`
	ActiveAlerts      int       `json:"active_alerts"`
	ActiveSensors     int       `json:"active_sensors"`
	LastUpdated       time.Time `json:"last_updated"`
}

// AttendanceRequest is the body for POST /kitchen/attendance.
type AttendanceRequest struct {
	KitchenID  string          `json:"kitchen_id"`
	MealType   domain.MealType `json:"meal_type"`
	Date       string          `json:"date"` // YYYY-MM-DD
	HeadCount  int             `json:"headcount"`
	SpecialNote string         `json:"special_note,omitempty"`
}

// AttendanceResponse is returned by POST /kitchen/attendance.
type AttendanceResponse struct {
	AttendanceID string    `json:"attendance_id"`
	KitchenID    string    `json:"kitchen_id"`
	HeadCount    int       `json:"headcount"`
	RecordedAt   time.Time `json:"recorded_at"`
}

// MenuItemEntry is one menu entry.
type MenuItemEntry struct {
	ItemName  string  `json:"item_name"`
	PortionKg float64 `json:"portion_kg"`
	Category  string  `json:"category,omitempty"`
}

// PostMenuRequest is the body for POST /kitchen/menu.
type PostMenuRequest struct {
	KitchenID string          `json:"kitchen_id"`
	MealType  domain.MealType `json:"meal_type"`
	Date      string          `json:"date"` // YYYY-MM-DD
	Items     []MenuItemEntry `json:"items"`
}

// PostMenuResponse is returned by POST /kitchen/menu.
type PostMenuResponse struct {
	MenuID    string    `json:"menu_id"`
	KitchenID string    `json:"kitchen_id"`
	ItemCount int       `json:"item_count"`
	CreatedAt time.Time `json:"created_at"`
}

// ReseedResponse is returned by POST /admin/reseed.
type ReseedResponse struct {
	OK        bool      `json:"ok"`
	Message   string    `json:"message"`
	StartedAt time.Time `json:"started_at"`
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// ListUsers handles GET /admin/users.
func (h *AdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	role := q.Get("role")
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}

	result, err := h.adminSvc.ListUsers(r.Context(), service.ListUsersInput{Role: role, Page: page, Limit: limit})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users", err)
		return
	}

	users := make([]AdminUserItem, len(result.Users))
	for i, u := range result.Users {
		users[i] = AdminUserItem{
			UserID:    u.UserID,
			Email:     u.Email,
			Name:      u.Name,
			Role:      u.Role,
			KitchenID: u.KitchenID,
			Active:    u.Active,
			CreatedAt: u.CreatedAt,
		}
	}

	writeJSON(w, http.StatusOK, ListUsersResponse{Users: users, Total: result.Total, Page: page, Limit: limit})
}

// CreateUser handles POST /admin/users.
func (h *AdminHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if req.Email == "" || req.Role == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email, role and password are required", nil)
		return
	}

	result, err := h.adminSvc.CreateUser(r.Context(), service.CreateUserInput{
		Email:     req.Email,
		Name:      req.Name,
		Role:      req.Role,
		KitchenID: req.KitchenID,
		Password:  req.Password,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create user", err)
		return
	}

	writeJSON(w, http.StatusCreated, AdminUserItem{
		UserID:    result.UserID,
		Email:     result.Email,
		Name:      result.Name,
		Role:      result.Role,
		KitchenID: result.KitchenID,
		Active:    result.Active,
		CreatedAt: result.CreatedAt,
	})
}

// UpdateUser handles PATCH /admin/users/{id}.
func (h *AdminHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "user id is required", nil)
		return
	}

	var req UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	result, err := h.adminSvc.UpdateUser(r.Context(), service.UpdateUserInput{
		UserID:    id,
		Name:      req.Name,
		Role:      req.Role,
		Active:    req.Active,
		KitchenID: req.KitchenID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update user", err)
		return
	}

	writeJSON(w, http.StatusOK, AdminUserItem{
		UserID:    result.UserID,
		Email:     result.Email,
		Name:      result.Name,
		Role:      result.Role,
		KitchenID: result.KitchenID,
		Active:    result.Active,
		CreatedAt: result.CreatedAt,
	})
}

// ListModels handles GET /admin/models.
func (h *AdminHandler) ListModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.adminSvc.ListModels(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list models", err)
		return
	}

	items := make([]ModelRegistryItem, len(models))
	for i, m := range models {
		items[i] = ModelRegistryItem{
			ModelID:    m.ModelID,
			Name:       m.Name,
			Version:    m.Version,
			Type:       m.Type,
			Status:     m.Status,
			Accuracy:   m.Accuracy,
			DeployedAt: m.DeployedAt,
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"models": items, "total": len(items)})
}

// AuditLog handles GET /admin/audit-log.
func (h *AdminHandler) AuditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	actorID := q.Get("actor_id")
	resource := q.Get("resource")
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}

	result, err := h.adminSvc.AuditLog(r.Context(), service.AuditLogInput{
		ActorID:  actorID,
		Resource: resource,
		Page:     page,
		Limit:    limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch audit log", err)
		return
	}

	entries := make([]AuditLogEntry, len(result.Entries))
	for i, e := range result.Entries {
		entries[i] = AuditLogEntry{
			EntryID:    e.EntryID,
			ActorID:    e.ActorID,
			ActorEmail: e.ActorEmail,
			Action:     e.Action,
			Resource:   e.Resource,
			ResourceID: e.ResourceID,
			IPAddress:  e.IPAddress,
			UserAgent:  e.UserAgent,
			CreatedAt:  e.CreatedAt,
		}
	}

	writeJSON(w, http.StatusOK, AuditLogResponse{Entries: entries, Total: result.Total, Page: page, Limit: limit})
}

// Reseed handles POST /admin/reseed.
func (h *AdminHandler) Reseed(w http.ResponseWriter, r *http.Request) {
	if err := h.adminSvc.Reseed(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "reseed failed", err)
		return
	}
	writeJSON(w, http.StatusOK, ReseedResponse{OK: true, Message: "reseed initiated", StartedAt: time.Now().UTC()})
}

// KitchenOverview handles GET /kitchen/overview.
func (h *AdminHandler) KitchenOverview(w http.ResponseWriter, r *http.Request) {
	kitchenID := r.URL.Query().Get("kitchen_id")
	if kitchenID == "" {
		kitchenID = kitchenIDFromContext(r.Context())
	}

	result, err := h.adminSvc.KitchenOverview(r.Context(), kitchenID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch kitchen overview", err)
		return
	}

	writeJSON(w, http.StatusOK, KitchenOverviewResponse{
		KitchenID:         result.KitchenID,
		Name:              result.Name,
		TodayHeadcount:    result.TodayHeadcount,
		PredictedCount:    result.PredictedCount,
		TodayWasteKg:      result.TodayWasteKg,
		TodayRedirectedKg: result.TodayRedirectedKg,
		ActiveAlerts:      result.ActiveAlerts,
		ActiveSensors:     result.ActiveSensors,
		LastUpdated:       result.LastUpdated,
	})
}

// PostAttendance handles POST /kitchen/attendance.
func (h *AdminHandler) PostAttendance(w http.ResponseWriter, r *http.Request) {
	var req AttendanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if req.KitchenID == "" || req.Date == "" || req.HeadCount < 0 {
		writeError(w, http.StatusBadRequest, "kitchen_id, date and headcount >= 0 are required", nil)
		return
	}

	result, err := h.adminSvc.PostAttendance(r.Context(), service.PostAttendanceInput{
		KitchenID:   req.KitchenID,
		MealType:    req.MealType,
		Date:        req.Date,
		HeadCount:   req.HeadCount,
		SpecialNote: req.SpecialNote,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record attendance", err)
		return
	}

	writeJSON(w, http.StatusCreated, AttendanceResponse{
		AttendanceID: result.AttendanceID,
		KitchenID:    result.KitchenID,
		HeadCount:    result.HeadCount,
		RecordedAt:   result.RecordedAt,
	})
}

// PostMenu handles POST /kitchen/menu.
func (h *AdminHandler) PostMenu(w http.ResponseWriter, r *http.Request) {
	var req PostMenuRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if req.KitchenID == "" || req.Date == "" || len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "kitchen_id, date and items are required", nil)
		return
	}

	items := make([]service.MenuItemEntry, len(req.Items))
	for i, it := range req.Items {
		items[i] = service.MenuItemEntry{
			ItemName:  it.ItemName,
			PortionKg: it.PortionKg,
			Category:  it.Category,
		}
	}

	result, err := h.adminSvc.PostMenu(r.Context(), service.PostMenuInput{
		KitchenID: req.KitchenID,
		MealType:  req.MealType,
		Date:      req.Date,
		Items:     items,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to post menu", err)
		return
	}

	writeJSON(w, http.StatusCreated, PostMenuResponse{
		MenuID:    result.MenuID,
		KitchenID: result.KitchenID,
		ItemCount: result.ItemCount,
		CreatedAt: result.CreatedAt,
	})
}
