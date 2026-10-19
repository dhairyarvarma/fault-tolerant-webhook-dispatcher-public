package worker

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/delivery"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/retry"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

func TestRetryAfterIsDurableAndCannotBeBypassedByQueueHint(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			targets, events := testStores(t)
			ctx := t.Context()
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.Header().Set("Retry-After", "30")
					w.WriteHeader(status)
				}
			}))
			defer srv.Close()
			target, err := targets.Create(ctx, "retry-after", srv.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			event, _, err := events.CreateWithOutbox(ctx, target.ID, "retry-after", []byte(`{}`), "hash")
			if err != nil {
				t.Fatal(err)
			}
			policy := retry.Policy{MaxAttempts: 2, BaseBackoff: time.Millisecond, MaxBackoff: time.Minute}
			pool := NewPool(events, targets, nil, delivery.NewClient(time.Second, time.Second, 1024, true), policy, testCircuitConfig(), "stream", "group", testLogger(), testMetrics())
			msg := redis.XMessage{Values: map[string]any{"event_id": event.ID}}
			before := time.Now()
			if err := pool.handle(ctx, msg); err != nil {
				t.Fatal(err)
			}
			after := time.Now()
			got, err := events.GetByID(ctx, event.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != store.StatusRetryWait || got.AttemptCount != 1 || got.NextAttemptAt == nil || got.NextAttemptAt.Before(before.Add(30*time.Second)) || got.NextAttemptAt.After(after.Add(30*time.Second)) {
				t.Fatalf("retry timing not durably persisted: %+v", got)
			}
			// A fresh pool represents a restarted process using the same durable state.
			restarted := NewPool(events, targets, nil, delivery.NewClient(time.Second, time.Second, 1024, true), policy, testCircuitConfig(), "stream", "group", testLogger(), testMetrics())
			if err := restarted.handle(ctx, msg); err != nil {
				t.Fatal(err)
			}
			if n, err := events.ScheduleDueRetries(ctx, 100); err != nil || n != 0 || calls.Load() != 1 {
				t.Fatalf("retry ran before due time: scheduled=%d calls=%d err=%v", n, calls.Load(), err)
			}
			// Advance the synthetic fixture instead of sleeping for the cooldown.
			if applied, err := events.DeferRetry(ctx, event.ID, time.Now().Add(-time.Second)); err != nil || !applied {
				t.Fatalf("advance fixture: applied=%v err=%v", applied, err)
			}
			if n, err := events.ScheduleDueRetries(ctx, 100); err != nil || n != 1 {
				t.Fatalf("schedule due retry: count=%d err=%v", n, err)
			}
			if err := restarted.handle(ctx, msg); err != nil {
				t.Fatal(err)
			}
			got, err = events.GetByID(ctx, event.ID)
			if err != nil || got.Status != store.StatusSucceeded || got.AttemptCount != 2 || calls.Load() != 2 {
				t.Fatalf("retry did not recover: event=%+v calls=%d err=%v", got, calls.Load(), err)
			}
		})
	}
}
