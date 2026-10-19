// Package worker implements the delivery worker pool: claiming queue work,
// re-checking durable event state, invoking the delivery client, persisting
// attempt outcomes, and scheduling retries or final state transitions.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/delivery"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/observability"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/queue"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/retry"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

const (
	readCount = 1
	readBlock = 2 * time.Second

	// Worker crash recovery: a message left pending (claimed but never
	// acked) for longer than reclaimMinIdle is assumed to belong to a dead
	// or stuck consumer and is reassigned to the reclaimer.
	reclaimInterval  = 30 * time.Second
	reclaimMinIdle   = 60 * time.Second
	reclaimBatchSize = 1
	reclaimConsumer  = "reclaimer"

	// readErrorBackoff throttles the consumer loop after a read error (e.g.
	// Redis unreachable or the consumer group missing), so an outage
	// produces one log line per backoff interval instead of a tight,
	// log-flooding busy loop.
	readErrorBackoff = 2 * time.Second
)

type Pool struct {
	events        *store.EventStore
	targets       *store.TargetStore
	redis         *redis.Client
	delivery      *delivery.Client
	retryPolicy   retry.Policy
	circuitCfg    CircuitConfig
	stream        string
	group         string
	logger        *slog.Logger
	metrics       *observability.Metrics
	reclaimCursor string
	slots         chan struct{}
	targetLimit   int
	targetMu      sync.Mutex
	targetActive  map[string]int
}

// CircuitConfig configures the per-target circuit breaker the pool checks
// before every delivery attempt.
type CircuitConfig struct {
	FailureThreshold int
	OpenCooldown     time.Duration
}

func NewPool(
	events *store.EventStore, targets *store.TargetStore, redisClient *redis.Client,
	deliveryClient *delivery.Client, retryPolicy retry.Policy, circuitCfg CircuitConfig,
	stream, group string, logger *slog.Logger, metrics *observability.Metrics,
) *Pool {
	return &Pool{
		events: events, targets: targets, redis: redisClient, delivery: deliveryClient,
		retryPolicy: retryPolicy, circuitCfg: circuitCfg, stream: stream, group: group, logger: logger, metrics: metrics,
	}
}

// Run starts workerCount goroutines consuming the stream's consumer group
// plus one reclaimer goroutine that recovers messages left pending by a
// crashed worker, and blocks until ctx is canceled and all have returned.
func (p *Pool) Run(ctx context.Context, workerCount, targetLimit int) {
	p.targetLimit = targetLimit
	p.slots = make(chan struct{}, workerCount)
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		consumer := fmt.Sprintf("worker-%d", i)
		go func() {
			defer wg.Done()
			p.metrics.WorkerStarted()
			defer p.metrics.WorkerStopped()
			p.runConsumer(ctx, consumer)
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		p.runReclaimer(ctx)
	}()
	wg.Add(1)
	go func() { defer wg.Done(); p.observe(ctx) }()

	wg.Wait()
}

// runReclaimer periodically reassigns messages that have sat unacked for
// longer than reclaimMinIdle - the signature of a worker that crashed (or
// hung) after claiming a message but before processing/acking it - to
// itself, then processes them like any other message.
func (p *Pool) runReclaimer(ctx context.Context) {
	ticker := time.NewTicker(reclaimInterval)
	defer ticker.Stop()

	for {
		for i := 0; i < 100; i++ {
			if !p.reclaimStale(ctx, reclaimMinIdle) || p.reclaimCursor == "0-0" || ctx.Err() != nil {
				break
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *Pool) reclaimStale(ctx context.Context, minIdle time.Duration) bool {
	start := p.reclaimCursor
	if start == "" {
		start = "0-0"
	}
	messages, next, err := p.redis.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   p.stream,
		Group:    p.group,
		MinIdle:  minIdle,
		Start:    start,
		Consumer: reclaimConsumer,
		Count:    reclaimBatchSize,
	}).Result()
	if err != nil {
		p.logger.Error("worker: reclaim stale messages failed", "error", err)
		return false
	}
	p.reclaimCursor = next

	for _, msg := range messages {
		p.logger.Info("worker: reclaimed stale message", "message_id", msg.ID)
		started := time.Now()
		if p.processMessage(ctx, msg, reclaimConsumer) {
			p.metrics.ObserveRecovery(time.Since(started))
		}
	}
	return true
}

func (p *Pool) runConsumer(ctx context.Context, consumer string) {
	for {
		if ctx.Err() != nil {
			return
		}

		streams, err := p.redis.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    p.group,
			Consumer: consumer,
			Streams:  []string{p.stream, ">"},
			Count:    readCount,
			Block:    readBlock,
		}).Result()
		if err != nil {
			if strings.HasPrefix(err.Error(), "NOGROUP") {
				if ensureErr := queue.EnsureConsumerGroup(ctx, p.redis, p.stream, p.group); ensureErr == nil {
					continue
				}
			}
			if errors.Is(err, redis.Nil) || errors.Is(err, context.Canceled) {
				continue
			}
			p.logger.Error("worker: read failed", "consumer", consumer, "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(readErrorBackoff):
			}
			continue
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				p.processMessage(ctx, msg, consumer)
			}
		}
	}
}

func (p *Pool) refreshQueueDepth(ctx context.Context) {
	started := time.Now()
	defer func() { p.metrics.ObserveDependencyCheck("redis", time.Since(started)) }()
	pending, err := p.redis.XPending(ctx, p.stream, p.group).Result()
	if err == nil {
		p.metrics.SetQueueDepth(pending.Count)
	} else {
		p.metrics.DependencyError("redis")
	}
	groups, err := p.redis.XInfoGroups(ctx, p.stream).Result()
	if err != nil {
		p.metrics.DependencyError("redis")
		return
	}
	for _, group := range groups {
		if group.Name == p.group && group.Lag >= 0 {
			p.metrics.SetQueueUnread(group.Lag)
		}
	}
}

func (p *Pool) observe(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		observation, cancel := context.WithTimeout(ctx, 2*time.Second)
		p.refreshQueueDepth(observation)
		started := time.Now()
		if pending, dlq, err := p.events.WorkCounts(observation); err == nil {
			p.metrics.SetDurableCounts(pending, dlq)
		} else {
			p.metrics.DependencyError("postgres")
		}
		if closed, open, half, err := p.targets.CircuitCounts(observation); err == nil {
			p.metrics.SetCircuitCounts(closed, open, half)
		} else {
			p.metrics.DependencyError("postgres")
		}
		p.metrics.ObserveDependencyCheck("postgres", time.Since(started))
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// processMessage leaves transient failures pending so reclaim can retry them.
func (p *Pool) processMessage(ctx context.Context, msg redis.XMessage, consumer string) bool {
	if err := p.handle(ctx, msg, consumer); err != nil {
		p.logger.Error("worker: processing failed; message remains pending",
			"worker_id", consumer, "message_id", msg.ID, "event_id", msg.Values["event_id"], "error", err)
		return false
	}
	if err := p.redis.XAck(ctx, p.stream, p.group, msg.ID).Err(); err != nil {
		p.logger.Error("worker: ack failed", "worker_id", consumer, "message_id", msg.ID, "error", err)
		return false
	}
	return true
}

func (p *Pool) handle(ctx context.Context, msg redis.XMessage, consumers ...string) error {
	consumer := "unknown"
	if len(consumers) > 0 {
		consumer = consumers[0]
	}
	eventID, _ := msg.Values["event_id"].(string)
	if eventID == "" {
		p.logger.Error("worker: message missing event_id", "worker_id", consumer, "message_id", msg.ID)
		return nil
	}
	if !validEventID(eventID) {
		p.logger.Error("worker: message has invalid event_id", "worker_id", consumer, "message_id", msg.ID)
		return nil
	}
	processingTimeout := 15 * time.Second
	if p.delivery != nil {
		processingTimeout = p.delivery.RequestTimeout() + 5*time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, processingTimeout)
	defer cancel()
	if p.slots != nil {
		select {
		case p.slots <- struct{}{}:
			defer func() { <-p.slots }()
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	event, err := p.events.GetByID(ctx, eventID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			p.logger.Warn("worker: event no longer exists", "worker_id", consumer, "event_id", eventID)
			return nil
		}
		return fmt.Errorf("load event: %w", err)
	}
	if store.IsTerminalStatus(event.Status) || event.Status == store.StatusRetryWait {
		return nil
	}
	token, acquired, err := p.events.ClaimDelivery(ctx, eventID, processingTimeout+5*time.Second)
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer releaseCancel()
		if err := p.events.ReleaseDelivery(releaseCtx, eventID, token); err != nil {
			p.logger.Error("worker: release delivery lease failed", "event_id", eventID, "error", err)
		}
	}()
	// Refresh attempt count after acquiring ownership rather than using a stale snapshot.
	event, err = p.events.GetByID(ctx, eventID)
	if err != nil {
		return err
	}

	target, err := p.targets.Get(ctx, event.TargetID)
	if err != nil {
		return fmt.Errorf("load target: %w", err)
	}
	if !target.Enabled {
		return p.events.CancelDelivery(ctx, eventID, token)
	}
	if !p.acquireTarget(target.ID) {
		applied, err := p.events.DeferRetry(ctx, eventID, time.Now().Add(time.Second+time.Duration(rand.Int64N(int64(time.Second)))), token)
		if err == nil && applied {
			p.metrics.TargetCapacityDeferred()
		}
		return err
	}
	defer p.releaseTarget(target.ID)

	allow, retryAt, generation, err := p.targets.CircuitAllow(ctx, target.ID, p.circuitCfg.OpenCooldown, processingTimeout+5*time.Second)
	if err != nil {
		return fmt.Errorf("check circuit: %w", err)
	}
	if !allow {
		p.metrics.CircuitOpen()
		p.logger.Info("worker: circuit open", "worker_id", consumer, "event_id", eventID, "target_id", target.ID)
		if _, err := p.events.DeferRetry(ctx, eventID, retryAt, token); err != nil {
			return fmt.Errorf("defer circuit-blocked event: %w", err)
		}
		return nil
	}

	p.metrics.DeliveryStarted()
	result := p.delivery.Deliver(ctx, target, event)
	p.metrics.DeliveryFinished()

	if _, err := p.targets.RecordCircuitOutcome(ctx, target.ID, result.Success, p.circuitCfg.FailureThreshold, generation); err != nil {
		return fmt.Errorf("record circuit outcome: %w", err)
	}

	attempt := store.AttemptResult{
		Outcome:      retry.Classify(result),
		StartedAt:    result.StartedAt,
		FinishedAt:   result.FinishedAt,
		HTTPStatus:   result.HTTPStatus,
		DurationMS:   int(result.FinishedAt.Sub(result.StartedAt).Milliseconds()),
		ErrorClass:   result.ErrorClass,
		ErrorMessage: result.ErrorMessage,
	}

	applied, err := p.events.RecordAttemptAndTransition(ctx, eventID, attempt, p.retryPolicy.MaxAttempts, func(number int) time.Duration {
		return p.retryPolicy.NextDelayFor(number, result)
	}, token)
	if err != nil {
		return fmt.Errorf("record attempt: %w", err)
	}
	if applied {
		attemptNumber := event.AttemptCount + 1
		retryScheduled := attempt.Outcome == store.OutcomeRetryableFailure && attemptNumber < p.retryPolicy.MaxAttempts
		dlq := attempt.Outcome == store.OutcomeNonRetryableFailure || (attempt.Outcome == store.OutcomeRetryableFailure && !retryScheduled)
		p.metrics.DeliveryAttempt(result.Success, retryScheduled, dlq, time.Duration(attempt.DurationMS)*time.Millisecond)
		p.logger.Info("worker: delivery attempt completed",
			"worker_id", consumer,
			"event_id", eventID,
			"target_id", target.ID,
			"attempt_number", attemptNumber,
			"outcome", attempt.Outcome,
		)
	}
	return nil
}

func (p *Pool) acquireTarget(id string) bool {
	if p.targetLimit == 0 {
		return true
	}
	p.targetMu.Lock()
	defer p.targetMu.Unlock()
	if p.targetActive[id] >= p.targetLimit {
		return false
	}
	if p.targetActive == nil {
		p.targetActive = make(map[string]int)
	}
	p.targetActive[id]++
	return true
}

func (p *Pool) releaseTarget(id string) {
	if p.targetLimit == 0 {
		return
	}
	p.targetMu.Lock()
	defer p.targetMu.Unlock()
	if p.targetActive[id] <= 1 {
		delete(p.targetActive, id)
	} else {
		p.targetActive[id]--
	}
}

func validEventID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
