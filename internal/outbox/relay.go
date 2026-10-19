// Package outbox implements the outbox relay: it finds committed,
// unpublished outbox rows and publishes them to Redis Streams, tolerating
// at-least-once publication per QUEUE_AND_DELIVERY_SEMANTICS.md.
package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/observability"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

const fetchBatchSize = 100

type Relay struct {
	outbox  *store.OutboxStore
	redis   *redis.Client
	stream  string
	logger  *slog.Logger
	metrics *observability.Metrics
}

func NewRelay(outboxStore *store.OutboxStore, redisClient *redis.Client, stream string, logger *slog.Logger, metrics *observability.Metrics) *Relay {
	return &Relay{outbox: outboxStore, redis: redisClient, stream: stream, logger: logger, metrics: metrics}
}

// Run polls for unpublished outbox rows every interval until ctx is
// canceled. If the process crashes between XAdd and MarkPublished, the row
// is republished on the next poll; the worker pool's durable re-check
// before delivery makes that safe.
func (r *Relay) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		r.publishPending(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Relay) publishPending(ctx context.Context) {
	if count, err := r.outbox.CountUnpublished(ctx); err != nil {
		r.logger.Error("outbox: count unpublished failed", "error", err)
	} else {
		r.metrics.SetOutboxPending(count)
	}

	messages, err := r.outbox.FetchUnpublished(ctx, fetchBatchSize)
	if err != nil {
		r.logger.Error("outbox: fetch unpublished failed", "error", err)
		return
	}

	for _, m := range messages {
		if err := r.redis.XAdd(ctx, &redis.XAddArgs{
			Stream: r.stream,
			Values: map[string]any{"event_id": m.EventID},
		}).Err(); err != nil {
			r.logger.Error("outbox: publish failed", "outbox_id", m.ID, "event_id", m.EventID, "error", err)
			continue
		}
		if err := r.outbox.MarkPublished(ctx, m.ID); err != nil {
			r.logger.Error("outbox: mark published failed", "outbox_id", m.ID, "error", err)
		}
	}
}
