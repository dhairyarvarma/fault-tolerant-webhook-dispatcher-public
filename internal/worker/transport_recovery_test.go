package worker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/delivery"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/outbox"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/queue"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/targetsim"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/testutil"
	"github.com/redis/go-redis/v9"
)

func TestPool_ConcurrentDuplicateMessagesShareDeliveryLease(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	events, targets := store.NewEventStore(db), store.NewTargetStore(db)
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseDelivery := func() { releaseOnce.Do(func() { close(release) }) }
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	defer releaseDelivery()
	target, err := targets.Create(ctx, "duplicates", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	event, _, err := events.CreateWithOutbox(ctx, target.ID, "dup-key", []byte(`{}`), "hash")
	if err != nil {
		t.Fatal(err)
	}
	p := NewPool(events, targets, nil, delivery.NewClient(time.Second, 3*time.Second, 1024, true), testPolicy(), testCircuitConfig(), "", "", testLogger(), testMetrics())
	msg := redis.XMessage{Values: map[string]any{"event_id": event.ID}}
	done := make(chan error, 1)
	go func() { done <- p.handle(ctx, msg) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("delivery never started")
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.handle(ctx, msg); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("HTTP calls=%d want 1", calls.Load())
	}
	releaseDelivery()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPool_RecoversLostTransportAndRecreatesGroup(t *testing.T) {
	if os.Getenv("TEST_REDIS_URL") == "" {
		t.Skip("TEST_REDIS_URL required")
	}
	db := testutil.OpenDB(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	events, targets := store.NewEventStore(db), store.NewTargetStore(db)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	target, err := targets.Create(ctx, "lost-transport", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	event, _, err := events.CreateWithOutbox(ctx, target.ID, "lost-key", []byte(`{}`), "hash")
	if err != nil {
		t.Fatal(err)
	}
	opts, err := redis.ParseURL(os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()
	stream, group := "test-lost-"+event.ID, "workers"
	defer rdb.Del(ctx, stream)
	if err := queue.EnsureConsumerGroup(ctx, rdb, stream, group); err != nil {
		t.Fatal(err)
	}
	if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"event_id": event.ID}}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE outbox_messages SET published_at=now()`); err != nil {
		t.Fatal(err)
	}
	// Destroy only this test stream, after its outbox row was marked published.
	if err := rdb.Del(ctx, stream).Err(); err != nil {
		t.Fatal(err)
	}
	if n, err := events.RecoverStaleWork(ctx, 100, 0); err != nil || n != 1 {
		t.Fatalf("recover=%d err=%v", n, err)
	}
	p := NewPool(events, targets, rdb, delivery.NewClient(time.Second, time.Second, 1024, true), testPolicy(), testCircuitConfig(), stream, group, testLogger(), testMetrics())
	workCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	wg.Add(2)
	go func() { defer wg.Done(); p.Run(workCtx, 1, 0) }()
	go func() {
		defer wg.Done()
		outbox.NewRelay(store.NewOutboxStore(db), rdb, stream, testLogger(), testMetrics()).Run(workCtx, 10*time.Millisecond)
	}()
	deadline := time.Now().Add(6 * time.Second)
	for {
		got, err := events.GetByID(ctx, event.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == store.StatusSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lost event stuck in %s", got.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Group deletion while the same worker process is running also heals.
	if err := rdb.XGroupDestroy(ctx, stream, group).Err(); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(6 * time.Second)
	for {
		groups, err := rdb.XInfoGroups(ctx, stream).Result()
		if err == nil && len(groups) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("group not recreated")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPool_ReclaimCursorAdvancesPastFreshMessages(t *testing.T) {
	if os.Getenv("TEST_REDIS_URL") == "" {
		t.Skip("TEST_REDIS_URL required")
	}
	opts, err := redis.ParseURL(os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()
	ctx := context.Background()
	stream, group := fmt.Sprintf("test-reclaim-cursor-%d", time.Now().UnixNano()), "workers"
	defer rdb.Del(ctx, stream)
	if err := queue.EnsureConsumerGroup(ctx, rdb, stream, group); err != nil {
		t.Fatal(err)
	}
	var last string
	for i := 0; i < 12; i++ {
		last, err = rdb.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"event_id": "malformed"}}).Result()
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: group, Consumer: "dead", Streams: []string{stream, ">"}, Count: 12}).Result(); err != nil {
		t.Fatal(err)
	}
	if err := rdb.Do(ctx, "XCLAIM", stream, group, "dead", 0, last, "IDLE", 120000).Err(); err != nil {
		t.Fatal(err)
	}
	p := &Pool{redis: rdb, stream: stream, group: group, logger: testLogger(), metrics: testMetrics()}
	p.reclaimStale(ctx, time.Minute)
	if p.reclaimCursor == "0-0" {
		t.Fatal("scan cursor did not move beyond fresh messages")
	}
	p.reclaimStale(ctx, time.Minute)
	if pending, err := rdb.XPending(ctx, stream, group).Result(); err != nil || pending.Count != 11 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
}

func TestPool_DisabledTargetsAndFutureRetriesDoNotSendHTTP(t *testing.T) {
	for _, disabled := range []bool{true, false} {
		t.Run(fmt.Sprint(disabled), func(t *testing.T) {
			db := testutil.OpenDB(t)
			ctx := context.Background()
			if err := store.Migrate(ctx, db); err != nil {
				t.Fatal(err)
			}
			events, targets := store.NewEventStore(db), store.NewTargetStore(db)
			var calls atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
			defer srv.Close()
			target, err := targets.Create(ctx, "no-http", srv.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			event, _, err := events.CreateWithOutbox(ctx, target.ID, "no-http-key", []byte(`{}`), "hash")
			if err != nil {
				t.Fatal(err)
			}
			want := store.StatusCanceled
			if disabled {
				if err := targets.Disable(ctx, target.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				want = store.StatusRetryWait
				if _, err := events.RecordAttemptAndTransition(ctx, event.ID, store.AttemptResult{Outcome: store.OutcomeRetryableFailure, StartedAt: time.Now(), FinishedAt: time.Now()}, 5, func(int) time.Duration { return time.Hour }); err != nil {
					t.Fatal(err)
				}
			}
			p := NewPool(events, targets, nil, delivery.NewClient(time.Second, time.Second, 1024, true), testPolicy(), testCircuitConfig(), "", "", testLogger(), testMetrics())
			if err := p.handle(ctx, redis.XMessage{Values: map[string]any{"event_id": event.ID}}); err != nil {
				t.Fatal(err)
			}
			got, err := events.GetByID(ctx, event.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != want || calls.Load() != 0 {
				t.Fatalf("status=%s want=%s calls=%d", got.Status, want, calls.Load())
			}
		})
	}
}

func TestPool_EnforcesSharedConcurrencyBudget(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	events, targets := store.NewEventStore(db), store.NewTargetStore(db)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	defer finish()
	target, err := targets.Create(ctx, "slots", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := NewPool(events, targets, nil, delivery.NewClient(time.Second, 3*time.Second, 1024, true), testPolicy(), testCircuitConfig(), "", "", testLogger(), testMetrics())
	p.slots = make(chan struct{}, 1)
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		ev, _, err := events.CreateWithOutbox(ctx, target.ID, fmt.Sprint(i), []byte(`{}`), "hash")
		if err != nil {
			t.Fatal(err)
		}
		go func() { done <- p.handle(ctx, redis.XMessage{Values: map[string]any{"event_id": ev.ID}}) }()
		if i == 0 {
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("first call did not start")
			}
		}
	}
	// The shared slot must stay occupied until the first HTTP call returns.
	if len(p.slots) != 1 {
		t.Fatal("slot not held by active delivery")
	}
	finish()
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d want 2", calls.Load())
	}
}

func TestPool_HTTPFailureMatrixPersistsExpectedState(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	events, targets := store.NewEventStore(db), store.NewTargetStore(db)
	srv := httptest.NewServer(targetsim.NewHandler())
	defer srv.Close()
	for _, tc := range []struct{ path, want string }{
		{"/sim/status/200", store.StatusSucceeded}, {"/sim/status/400", store.StatusDLQ},
		{"/sim/status/401", store.StatusDLQ}, {"/sim/status/403", store.StatusDLQ}, {"/sim/status/404", store.StatusDLQ},
		{"/sim/status/408", store.StatusRetryWait}, {"/sim/status/429", store.StatusRetryWait},
		{"/sim/status/500", store.StatusRetryWait}, {"/sim/status/503", store.StatusRetryWait},
		{"/sim/hang", store.StatusRetryWait}, {"/sim/reset", store.StatusRetryWait},
	} {
		t.Run(tc.path, func(t *testing.T) {
			target, err := targets.Create(ctx, "matrix", srv.URL+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			event, _, err := events.CreateWithOutbox(ctx, target.ID, "matrix", []byte(`{}`), "hash")
			if err != nil {
				t.Fatal(err)
			}
			p := NewPool(events, targets, nil, delivery.NewClient(50*time.Millisecond, 100*time.Millisecond, 1024, true), testPolicy(), testCircuitConfig(), "", "", testLogger(), testMetrics())
			if err := p.handle(ctx, redis.XMessage{Values: map[string]any{"event_id": event.ID}}); err != nil {
				t.Fatal(err)
			}
			got, err := events.GetByID(ctx, event.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.want || got.AttemptCount != 1 {
				t.Fatalf("status=%s want=%s count=%d", got.Status, tc.want, got.AttemptCount)
			}
		})
	}
}
