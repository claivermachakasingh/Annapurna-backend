package main

import (
	"bufio"
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/sih26234/food-waste/internal/asynqx"
	"github.com/sih26234/food-waste/internal/config"
	"github.com/sih26234/food-waste/internal/store"
)

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
}

func main() {
	config.LoadDotEnv(".env")
	loadDotEnv(".env")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := config.Load()

	ctx := context.Background()
	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Warn("worker: postgres connection failed", "error", err)
	} else {
		defer pool.Close()
	}

	redisOpt := asynq.RedisClientOpt{
		Addr: "localhost:6379",
		DB:   1,
	}

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: 10,
		Queues: map[string]int{
			"critical": 6,
			"default":  3,
			"low":      1,
		},
		ShutdownTimeout: 10 * time.Second,
	})

	mux := asynq.NewServeMux()
	mux.HandleFunc(asynqx.TaskExpirySweep, func(ctx context.Context, t *asynq.Task) error {
		logger.Info("executing expiry sweep")
		return nil
	})
	mux.HandleFunc(asynqx.TaskOfferExpiry, func(ctx context.Context, t *asynq.Task) error {
		logger.Info("executing offer expiry check")
		return nil
	})

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("Asynq worker running...")
		if err := srv.Run(mux); err != nil {
			logger.Error("worker error", "error", err)
		}
	}()

	<-quit
	logger.Info("shutting down worker...")
	srv.Shutdown()
	logger.Info("worker stopped")
}
