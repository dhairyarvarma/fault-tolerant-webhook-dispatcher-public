package outbox

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/delivery"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/observability"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/retry"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/testutil"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/worker"
)

func TestRelay_RetrySurvivesSchedulerExitPublishFailureAndDuplicatePublication(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL required")
	}
	db := testutil.OpenDB(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	events, targets, outbox := store.NewEventStore(db), store.NewTargetStore(db), store.NewOutboxStore(db)
	var calls atomic.Int64
	const key = "retry-recovery-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get("Idempotency-Key"); got != key {
			t.Errorf("key = %q, want %q", got, key)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	target, err := targets.Create(ctx, "retry-recovery", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	event, _, err := events.CreateWithOutbox(ctx, target.ID, key, []byte(`{}`), "hash")
	if err != nil {
		t.Fatal(err)
	}
	// The initial delivery already happened and left a retry due now.
	if _, err := db.ExecContext(ctx, `UPDATE outbox_messages SET published_at=now()`); err != nil {
		t.Fatal(err)
	}
	if _, err := events.RecordAttemptAndTransition(ctx, event.ID, store.AttemptResult{
		Outcome: store.OutcomeRetryableFailure, StartedAt: time.Now(), FinishedAt: time.Now(),
	}, 5, func(int) time.Duration { return 0 }); err != nil {
		t.Fatal(err)
	}
	if n, err := events.ScheduleDueRetries(ctx, 100); err != nil || n != 1 {
		t.Fatalf("schedule count=%d err=%v", n, err)
	}
	// No scheduler action is needed after this commit, even with Redis down.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	metrics := observability.NewMetrics()
	broken := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { broken.Close() })
	NewRelay(store.NewOutboxStore(db), broken, "unused", logger, metrics).publishPending(ctx)
	if n, err := outbox.CountUnpublished(ctx); err != nil || n != 1 {
		t.Fatalf("pending after publish failure=%d err=%v", n, err)
	}
	got, err := events.GetByID(ctx, event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusQueued {
		t.Fatalf("status = %s, want QUEUED with durable outbox", got.Status)
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { rdb.Close() })
	stream, group := "test-retry-recovery-"+event.ID, "test-group"
	t.Cleanup(func() {
		if err := rdb.Del(context.Background(), stream).Err(); err != nil {
			t.Error(err)
		}
	})
	if err := rdb.XGroupCreateMkStream(ctx, stream, group, "0").Err(); err != nil {
		t.Fatal(err)
	}
	// Represent a relay that published then died before marking the row.
	if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"event_id": event.ID}}).Err(); err != nil {
		t.Fatal(err)
	}
	restarted := NewRelay(store.NewOutboxStore(db), rdb, stream, logger, metrics)
	restarted.publishPending(ctx)
	restarted.publishPending(ctx)
	if n, err := outbox.CountUnpublished(ctx); err != nil || n != 0 {
		t.Fatalf("pending after restart=%d err=%v", n, err)
	}
	if n, err := rdb.XLen(ctx, stream).Result(); err != nil || n != 2 {
		t.Fatalf("messages=%d want=2 err=%v", n, err)
	}

	pool := worker.NewPool(store.NewEventStore(db), targets, rdb,
		delivery.NewClient(time.Second, time.Second, 1024, true),
		retry.Policy{MaxAttempts: 5}, worker.CircuitConfig{FailureThreshold: 5, OpenCooldown: time.Second},
		stream, group, logger, metrics)
	workCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); pool.Run(workCtx, 1, 0) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("worker did not stop")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := events.GetByID(ctx, event.ID)
		if err != nil {
			t.Fatal(err)
		}
		pending, err := rdb.XPending(ctx, stream, group).Result()
		if err != nil {
			t.Fatal(err)
		}
		groups, err := rdb.XInfoGroups(ctx, stream).Result()
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == store.StatusSucceeded && pending.Count == 0 && len(groups) == 1 && groups[0].Lag == 0 {
			if got.AttemptCount != 2 {
				t.Fatalf("attempt count=%d, want initial failure + success", got.AttemptCount)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("retry did not finish: status=%s pending=%d", got.Status, pending.Count)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("HTTP calls=%d, want one despite duplicate publication", n)
	}
}
