package httpapi

import (
	"encoding/json"
	"net/http"
)

const (
	CodeInternal               = "INTERNAL"
	CodeUnauthorized           = "UNAUTHORIZED"
	CodeForbidden              = "FORBIDDEN_ROLE"
	CodeNotFound               = "NOT_FOUND"
	CodeValidation             = "VALIDATION_ERROR"
	CodeConflict               = "CONFLICT"
	CodeRateLimited            = "RATE_LIMITED"
	CodeIdempotencyKeyReused   = "IDEMPOTENCY_KEY_REUSED"
	CodeAppVersionUnsupported  = "APP_VERSION_UNSUPPORTED"
	CodeSurplusExpired         = "SURPLUS_EXPIRED"
	CodeInvalidStateTransition = "INVALID_STATE_TRANSITION"
	CodeSafetyRejected         = "SAFETY_REJECTED_CANNOT_APPROVE"
	CodeImageTooLarge          = "IMAGE_TOO_LARGE"
	CodeImageUnsupported       = "IMAGE_UNSUPPORTED"
	CodeImageUnreadable        = "IMAGE_UNREADABLE"
	CodeCVUnavailable          = "CV_UNAVAILABLE"
	CodeMLUnavailable          = "ML_UNAVAILABLE"
	CodeNoEligibleRecipient    = "NO_ELIGIBLE_RECIPIENT"
	CodeMatchNotFound          = "MATCH_NOT_FOUND"
	CodeQRChainBroken          = "QR_CHAIN_BROKEN"
	CodeRefreshTokenReused     = "REFRESH_TOKEN_REUSED"
	CodeSyncItemRejected       = "SYNC_ITEM_REJECTED"
	CodeSyncOutOfOrder         = "SYNC_OUT_OF_ORDER"
	CodeRouteInfeasible        = "ROUTE_INFEASIBLE"
)

// AppError is the canonical API error type.
type AppError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
	Details any    `json:"details,omitempty"`
}

func (e *AppError) Error() string { return e.Code + ": " + e.Message }

type errorEnvelope struct {
	Detail  string `json:"detail"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func (e *AppError) Render(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(e.Status)
	env := errorEnvelope{Detail: e.Message, Code: e.Code, Message: e.Message, Details: e.Details}
	if err := json.NewEncoder(w).Encode(env); err != nil {
		http.Error(w, `{"detail":"encoding failed","code":"INTERNAL","message":"encoding failed"}`, 500)
	}
}

func RenderError(w http.ResponseWriter, e *AppError) { e.Render(w) }

func NewInternal(msg string) *AppError {
	return &AppError{Code: CodeInternal, Message: msg, Status: http.StatusInternalServerError}
}
func NewUnauthorized(msg string) *AppError {
	return &AppError{Code: CodeUnauthorized, Message: msg, Status: http.StatusUnauthorized}
}
func NewForbidden(msg string) *AppError {
	return &AppError{Code: CodeForbidden, Message: msg, Status: http.StatusForbidden}
}
func NewNotFound(resource string) *AppError {
	return &AppError{Code: CodeNotFound, Message: resource + " not found", Status: http.StatusNotFound}
}
func NewValidation(msg string, details any) *AppError {
	return &AppError{Code: CodeValidation, Message: msg, Status: http.StatusUnprocessableEntity, Details: details}
}

// NewAppError builds an error with an explicit status, used for contract codes
// that are not covered by the helpers below (e.g. REFRESH_TOKEN_REUSED).
func NewAppError(code, msg string, status int) *AppError {
	return &AppError{Code: code, Message: msg, Status: status}
}

func NewConflict(code, msg string) *AppError {
	return &AppError{Code: code, Message: msg, Status: http.StatusConflict}
}
func NewRateLimited() *AppError {
	return &AppError{Code: CodeRateLimited, Message: "too many requests", Status: http.StatusTooManyRequests}
}

// Typed constructors matching the spec error codes.

func ErrSurplusExpired() *AppError {
	return &AppError{Code: CodeSurplusExpired, Message: "surplus batch has expired", Status: http.StatusConflict}
}
func ErrInvalidStateTransition(from, to string) *AppError {
	return &AppError{Code: CodeInvalidStateTransition,
		Message: "cannot transition from " + from + " to " + to, Status: http.StatusConflict,
		Details: map[string]string{"from": from, "to": to}}
}
func ErrSafetyRejected() *AppError {
	return &AppError{Code: CodeSafetyRejected,
		Message: "safety check rejected; ADMIN override_reason required", Status: http.StatusConflict}
}
func ErrImageTooLarge() *AppError {
	return &AppError{Code: CodeImageTooLarge, Message: "image exceeds maximum upload size", Status: http.StatusRequestEntityTooLarge}
}
func ErrImageUnsupported() *AppError {
	return &AppError{Code: CodeImageUnsupported, Message: "image format not supported (JPG/PNG only)", Status: http.StatusUnsupportedMediaType}
}
func ErrImageUnreadable() *AppError {
	return &AppError{Code: CodeImageUnreadable, Message: "image could not be decoded", Status: http.StatusUnprocessableEntity}
}
func ErrCVUnavailable() *AppError {
	return &AppError{Code: CodeCVUnavailable, Message: "CV service unavailable", Status: http.StatusServiceUnavailable}
}
func ErrMLUnavailable() *AppError {
	return &AppError{Code: CodeMLUnavailable, Message: "ML service unavailable", Status: http.StatusServiceUnavailable}
}
func ErrNoEligibleRecipient() *AppError {
	return &AppError{Code: CodeNoEligibleRecipient, Message: "no eligible recipient found", Status: http.StatusConflict}
}
func ErrQRChainBroken(at *int) *AppError {
	details := any(nil)
	if at != nil {
		details = map[string]int{"broken_at": *at}
	}
	return &AppError{Code: CodeQRChainBroken, Message: "QR chain integrity check failed",
		Status: http.StatusUnprocessableEntity, Details: details}
}
func ErrIdempotencyKeyReused(key string) *AppError {
	return &AppError{Code: CodeIdempotencyKeyReused,
		Message: "idempotency key reused with different payload", Status: http.StatusUnprocessableEntity,
		Details: map[string]string{"idempotency_key": key}}
}
func ErrAppVersionUnsupported(current, minimum, storeLink string) *AppError {
	return &AppError{Code: CodeAppVersionUnsupported,
		Message: "app version " + current + " is below minimum " + minimum,
		Status:  http.StatusUpgradeRequired,
		Details: map[string]string{"current": current, "minimum": minimum, "store_link": storeLink}}
}
func ErrRefreshTokenReused() *AppError {
	return &AppError{Code: CodeRefreshTokenReused, Message: "refresh token already used; family revoked", Status: http.StatusUnauthorized}
}
func ErrSyncOutOfOrder() *AppError {
	return &AppError{Code: CodeSyncOutOfOrder, Message: "QR sync items out of sequence", Status: http.StatusConflict}
}
func ErrRouteInfeasible() *AppError {
	return &AppError{Code: CodeRouteInfeasible, Message: "no feasible route within constraints", Status: http.StatusConflict}
}
