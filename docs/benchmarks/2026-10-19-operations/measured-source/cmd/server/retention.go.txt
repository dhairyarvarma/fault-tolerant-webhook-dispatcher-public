package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/config"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/observability"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

func runRetention(ctx context.Context, events *store.EventStore, cfg config.Config, metrics *observability.Metrics, logger *slog.Logger) {
	if cfg.HistoryRetentionAge == 0 {
		return
	}
	ticker := time.NewTicker(cfg.HistoryRetentionInterval)
	defer ticker.Stop()
	// Wait one interval so opting in never deletes history during startup.
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			batch, cancel := context.WithTimeout(ctx, 5*time.Second)
			n, err := events.PruneCompleted(batch, cfg.HistoryRetentionAge, cfg.HistoryRetentionBatch)
			cancel()
			if err != nil {
				metrics.HistoryCleanupFailed()
				logger.Error("history cleanup failed; transaction rolled back")
			} else {
				metrics.HistoryPruned(uint64(n))
				logger.Info("history cleanup complete", "events_pruned", n)
			}
		}
	}
}
