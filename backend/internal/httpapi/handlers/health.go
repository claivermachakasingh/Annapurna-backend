package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type HealthHandler struct {
	pool  *pgxpool.Pool
	redis *redis.Client
}

func NewHealthHandler(pool *pgxpool.Pool, rdb *redis.Client) *HealthHandler {
	return &HealthHandler{pool: pool, redis: rdb}
}

func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	dbOk := true
	if h.pool != nil {
		if err := h.pool.Ping(r.Context()); err != nil {
			dbOk = false
		}
	}

	redisStatus := "ok"
	if h.redis != nil {
		if err := h.redis.Ping(r.Context()).Err(); err != nil {
			redisStatus = "degraded"
		}
	}

	status := "ready"
	code := http.StatusOK
	if !dbOk {
		status = "not_ready"
		code = http.StatusServiceUnavailable
	}

	w.WriteHeader(code)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":   status,
		"database": dbOk,
		"redis":    redisStatus,
	})
}
