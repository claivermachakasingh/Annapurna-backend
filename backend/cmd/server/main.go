package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/sih26234/food-waste/internal/config"
	"github.com/sih26234/food-waste/internal/httpapi/handlers"
	mw "github.com/sih26234/food-waste/internal/httpapi/middleware"
	_ "github.com/sih26234/food-waste/internal/metrics"
	"github.com/sih26234/food-waste/internal/mlclient"
	"github.com/sih26234/food-waste/internal/pushx"
	"github.com/sih26234/food-waste/internal/qrchain"
	"github.com/sih26234/food-waste/internal/redisx"
	"github.com/sih26234/food-waste/internal/services"
	"github.com/sih26234/food-waste/internal/sse"
	"github.com/sih26234/food-waste/internal/store"
)

func main() {
	config.LoadDotEnv(".env") // developer convenience; real env vars win

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := config.Load()

	// Uploads are written with UUID filenames (never client-supplied paths).
	if err := os.MkdirAll(cfg.UploadsDir, 0o755); err != nil {
		logger.Error("could not create uploads directory", "dir", cfg.UploadsDir, "error", err)
	}

	ctx := context.Background()

	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Warn("could not connect to postgres (running in offline/mock mode)", "error", err)
	} else {
		defer pool.Close()
		logger.Info("connected to postgres")
	}

	rdb, err := redisx.NewClient(cfg.RedisURL)
	if err != nil {
		logger.Warn("could not connect to redis (degraded mode active)", "error", err)
	} else {
		defer rdb.Close()
		logger.Info("connected to redis")
	}

	mlClient := mlclient.NewMLClient(
		cfg.MLServingURL,
		cfg.MLServingToken,
		cfg.FeatureMockML,
		cfg.FeatureMockCV,
		cfg.FeatureMockRouting,
	)

	pushSvc, _ := pushx.NewPushService(ctx, "", nil, nil, true, zap.NewNop())
	qrChainSvc := qrchain.NewService(pool)

	surplusSvc := services.NewSurplusService(pool, qrChainSvc)
	qualitySvc := services.NewQualityService(pool, rdb, mlClient)
	approveSvc := services.NewApproveService(pool, qrChainSvc)
	matchSvc := services.NewMatchingService(pool, qrChainSvc, pushSvc)
	qrSvc := services.NewQRService(pool, qrChainSvc)
	syncSvc := services.NewSyncService(pool, rdb, surplusSvc, qualitySvc, qrSvc)
	routeSvc := services.NewRoutingService(pool)

	sseHub := sse.NewHub(ctx, rdb)

	hAuth := handlers.NewAuthHandler(pool, rdb, cfg)
	hKitchen := handlers.NewKitchenHandler(pool)
	hPrediction := handlers.NewPredictionHandler(pool, mlClient, cfg.CarbonFactorKgCO2ePerKg)
	hSurplus := handlers.NewSurplusHandler(surplusSvc, approveSvc, matchSvc)
	hMatch := handlers.NewMatchingHandler(matchSvc, routeSvc)
	hRouting := handlers.NewRoutingHandler(routeSvc)
	hQuality := handlers.NewQualityHandler(qualitySvc, cfg.MaxUploadMB, cfg.UploadsDir)
	hSync := handlers.NewSyncHandler(syncSvc)
	hQR := handlers.NewQRHandler(qrSvc)
	hHealth := handlers.NewHealthHandler(pool, rdb)
	hEvents := handlers.NewEventsHandler(sseHub)

	r := chi.NewRouter()

	r.Use(mw.RequestID)
	r.Use(mw.Recoverer(logger))
	r.Use(chiMiddleware.RealIP)

	r.Get("/health", hHealth.Health)
	r.Get("/ready", hHealth.Ready)
	r.Handle("/metrics", promhttp.Handler())

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/register", hAuth.Register)
		r.Post("/auth/login", hAuth.Login)
		r.Post("/auth/refresh", hAuth.Refresh)
		r.Post("/auth/logout", hAuth.Logout)
		r.Post("/auth/logout-all", hAuth.LogoutAll)
		r.Get("/auth/me", hAuth.Me)

		r.Post("/surplus", hSurplus.Create)
		r.Get("/surplus", hSurplus.List)
		r.Get("/surplus/{id}", hSurplus.Get)
		r.Post("/surplus/{id}/approve", hSurplus.Approve)
		r.Post("/surplus/{id}/divert", hSurplus.Divert)

		// Matching
		r.Post("/surplus/{batchID}/match", hMatch.Match)
		r.Post("/matches/{batchID}", hMatch.Match)
		r.Get("/matches", hMatch.List)
		r.Post("/matches/{matchID}/respond", hMatch.Respond)

		// Routing / Logistics
		r.Get("/routes/available", hRouting.ListAvailable)
		r.Get("/routes/assigned", hRouting.ListAssigned)
		r.Post("/routes/{routeID}/assign", hRouting.Assign)

		r.Post("/quality/check", hQuality.Check)

		// Kitchen dashboard read models. Every figure is a real aggregate over
		// the caller's own kitchen.
		r.Get("/kitchen/overview", hKitchen.Overview)
		r.Get("/alerts", hKitchen.Alerts)
		r.Get("/analytics/impact", hKitchen.Impact)
		r.Get("/sensors/latest", hKitchen.SensorsLatest)

		// Historical footfall / orders upload feeding demand prediction.
		r.Post("/kitchen/datasets", hKitchen.UploadDataset)
		r.Get("/kitchen/datasets", hKitchen.ListDatasets)
		r.Delete("/kitchen/datasets/{id}", hKitchen.DeleteDataset)

		// Demand prediction. Assembles the feature vector and forwards it to the
		// ML sidecar; the model itself lives behind /predict/demand.
		r.Post("/predict-demand", hPrediction.PredictDemand)

		r.Post("/sync/batch", hSync.Batch)

		// Append-only QR hash chain (contract rows 22-24).
		r.Get("/qr/{batchID}", hQR.Get)
		r.Post("/qr/{batchID}/event", hQR.RecordEvent)
		r.Get("/qr/{batchID}/verify", hQR.Verify)

		r.Get("/events/stream", hEvents.Stream)
	})

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("Go backend server starting", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-quit
	logger.Info("shutdown signal received, draining...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
	logger.Info("server stopped gracefully")
}
