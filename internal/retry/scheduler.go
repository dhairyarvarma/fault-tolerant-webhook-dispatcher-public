package retry

import (
	"context"
	"log/slog"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

const claimBatchSize = 100

// Scheduler queues due retries through PostgreSQL's transactional outbox.
// The relay publishes them, even if this scheduler exits before publication.
type Scheduler struct {
	events *store.EventStore
	logger *slog.Logger
}

func NewScheduler(events *store.EventStore, logger *slog.Logger) *Scheduler {
	return &Scheduler{events: events, logger: logger}
}

func (s *Scheduler) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		s.requeueDue(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Scheduler) requeueDue(ctx context.Context) {
	if _, err := s.events.ScheduleDueRetries(ctx, claimBatchSize); err != nil {
		s.logger.Error("retry: schedule due retries failed", "error", err)
	}
	if _, err := s.events.RecoverStaleWork(ctx, claimBatchSize, time.Minute); err != nil {
		s.logger.Error("retry: recover stale work failed", "error", err)
	}
}
