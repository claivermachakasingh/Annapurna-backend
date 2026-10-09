package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/sih26234/food-waste/internal/httpapi"
)

// Recoverer catches panics, logs them with slog (including stack trace),
// and returns 500 INTERNAL to the client without leaking stack traces.
func Recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rv := recover(); rv != nil {
					requestID := GetRequestID(r.Context())
					logger.ErrorContext(r.Context(), "panic recovered",
						slog.String("request_id", requestID),
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
						slog.String("panic", fmt.Sprintf("%v", rv)),
						slog.String("stack", string(debug.Stack())),
					)
					httpapi.NewInternal("an unexpected error occurred").Render(w)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
