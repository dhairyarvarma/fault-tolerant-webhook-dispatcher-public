package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/api"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/config"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/crypto"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/delivery"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/observability"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/outbox"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/queue"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/retry"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/service"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(logger); err != nil {
		logger.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	db, err := store.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	startupCtx, startupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer startupCancel()
	if err := db.PingContext(startupCtx); err != nil {
		return err
	}
	if err := store.Migrate(startupCtx, db); err != nil {
		return err
	}

	redisClient, err := queue.Open(cfg.RedisURL)
	if err != nil {
		return err
	}
	defer redisClient.Close()
	if err := queue.EnsureConsumerGroup(startupCtx, redisClient, cfg.RedisStream, cfg.RedisConsumerGroup); err != nil {
		logger.Warn("Redis group unavailable at startup; workers will retry", "error", err)
	}

	secretBox, err := crypto.NewSecretBox(cfg.SecretEncryptionKey)
	if err != nil {
		return err
	}

	targetStore := store.NewTargetStore(db)
	eventStore := store.NewEventStore(db)
	outboxStore := store.NewOutboxStore(db)
	metrics := observability.NewMetrics()

	targetService := service.NewTargetService(targetStore, secretBox, cfg.AllowPrivateTargets)
	eventService := service.NewEventService(eventStore, targetStore, cfg.EventsMaxPending)

	server := api.NewServer(targetService, eventService, db, logger, cfg.PayloadMaxBytes, cfg.AuthEnabled, cfg.APIKey, metrics)

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// appCtx governs the outbox relay and worker pool, which run for the
	// life of the process independently of any single HTTP request.
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()
	var background sync.WaitGroup
	start := func(run func()) {
		background.Add(1)
		go func() { defer background.Done(); run() }()
	}

	relay := outbox.NewRelay(outboxStore, redisClient, cfg.RedisStream, logger, metrics)
	start(func() { relay.Run(appCtx, cfg.OutboxPollInterval) })

	retryPolicy := retry.Policy{
		MaxAttempts: cfg.DeliveryMaxAttempts,
		BaseBackoff: cfg.DeliveryBaseBackoff,
		MaxBackoff:  cfg.DeliveryMaxBackoff,
	}
	scheduler := retry.NewScheduler(eventStore, logger)
	start(func() { scheduler.Run(appCtx, cfg.OutboxPollInterval) })
	start(func() { runRetention(appCtx, eventStore, cfg, metrics, logger) })

	circuitCfg := worker.CircuitConfig{
		FailureThreshold: cfg.CircuitFailureThreshold,
		OpenCooldown:     cfg.CircuitOpenCooldown,
	}
	deliveryClient := delivery.NewClient(cfg.DeliveryConnectTimeout, cfg.DeliveryRequestTimeout, cfg.ResponseCaptureMaxBytes, cfg.AllowPrivateTargets, secretBox)
	pool := worker.NewPool(eventStore, targetStore, redisClient, deliveryClient, retryPolicy, circuitCfg, cfg.RedisStream, cfg.RedisConsumerGroup, logger, metrics)
	start(func() { pool.Run(appCtx, cfg.WorkerCount, cfg.WorkerTargetConcurrency) })

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("webhook-dispatcher listening", "http_addr", cfg.HTTPAddr, "env", cfg.AppEnv)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stop)
	var serveErr error

	select {
	case err := <-serverErr:
		serveErr = err
	case <-stop:
		logger.Info("shutdown signal received")
	}

	appCancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	shutdownErr := httpServer.Shutdown(shutdownCtx)
	return errors.Join(serveErr, shutdownErr, waitBackground(shutdownCtx, &background))
}

func waitBackground(ctx context.Context, wg *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
