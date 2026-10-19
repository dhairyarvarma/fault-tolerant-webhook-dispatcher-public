package worker

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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
)

// testPolicy allows several retries with a near-zero backoff so tests stay fast.
func testPolicy() retry.Policy {
	return retry.Policy{MaxAttempts: 5, BaseBackoff: time.Millisecond, MaxBackoff: 10 * time.Millisecond}
}

// testCircuitConfig has a high failure threshold so the breaker never
// interferes with tests that aren't specifically exercising it.
func testCircuitConfig() CircuitConfig {
	return CircuitConfig{FailureThreshold: 1000, OpenCooldown: time.Millisecond}
}

func testStores(t *testing.T) (*store.TargetStore, *store.EventStore) {
	t.Helper()

	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping worker test")
	}

	db := testutil.OpenDB(t)

	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	return store.NewTargetStore(db), store.NewEventStore(db)
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testMetrics() *observability.Metrics { return observability.NewMetrics() }

func TestValidEventID(t *testing.T) {
	for _, tc := range []struct {
		id    string
		valid bool
	}{
		{"00000000-0000-0000-0000-000000000000", true},
		{"ABCDEF01-2345-6789-abcd-0123456789ab", true},
		{"", false},
		{"not-a-uuid", false},
		{"00000000_0000-0000-0000-000000000000", false},
		{"00000000-0000-0000-0000-00000000000z", false},
	} {
		if got := validEventID(tc.id); got != tc.valid {
			t.Errorf("validEventID(%q) = %v, want %v", tc.id, got, tc.valid)
		}
	}
}

func TestPool_Handle_DatabaseReadFailureIsReported(t *testing.T) {
	db, err := store.Open("postgres://unused:unused@localhost:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	p := &Pool{events: store.NewEventStore(db), logger: testLogger()}
	err = p.handle(context.Background(), redis.XMessage{Values: map[string]any{
		"event_id": "00000000-0000-0000-0000-000000000001",
	}})
	if err == nil {
		t.Fatal("closed database must report an error so the message stays pending")
	}
}

func TestPool_Handle_MalformedMessageIsDiscardable(t *testing.T) {
	p := &Pool{logger: testLogger()}
	for _, values := range []map[string]any{nil, {"event_id": "invalid"}, {"event_id": 123}} {
		if err := p.handle(context.Background(), redis.XMessage{Values: values}); err != nil {
			t.Fatalf("malformed message must be discardable: %v", err)
		}
	}
}

func TestPool_ProcessMessage_DatabaseFailureStaysPendingUntilRecovery(t *testing.T) {
	dbURL, redisURL := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_URL")
	if dbURL == "" || redisURL == "" {
		t.Skip("TEST_DATABASE_URL and TEST_REDIS_URL required")
	}
	for _, failureAt := range []string{"event_read", "attempt_commit"} {
		t.Run(failureAt, func(t *testing.T) {
			ctx := context.Background()
			// Isolate from a running app's relay, which can consume rows in public.
			admin, err := store.Open(dbURL)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { admin.Close() })
			// Keep extension ownership out of the disposable test schema.
			if _, err := admin.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			schema := fmt.Sprintf("worker_ack_%d", time.Now().UnixNano())
			if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
					t.Error(err)
				}
			})
			isolatedURL, err := url.Parse(dbURL)
			if err != nil {
				t.Fatal(err)
			}
			query := isolatedURL.Query()
			query.Set("search_path", schema)
			isolatedURL.RawQuery = query.Encode()
			openDB := func() *sql.DB {
				db, err := store.Open(isolatedURL.String())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { db.Close() })
				if err := store.Migrate(ctx, db); err != nil {
					t.Fatal(err)
				}
				return db
			}
			db := openDB()
			targets, events := store.NewTargetStore(db), store.NewEventStore(db)
			var calls atomic.Int64
			const key = "ack-recovery-key"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Idempotency-Key"); got != key {
					t.Errorf("Idempotency-Key = %q, want %q", got, key)
				}
				if calls.Add(1) == 1 && failureAt == "attempt_commit" {
					// Fail after target success, before the worker can commit it.
					if err := db.Close(); err != nil {
						t.Error(err)
					}
				}
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(srv.Close)
			target, err := targets.Create(ctx, "ack-recovery", srv.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			event, _, err := events.CreateWithOutbox(ctx, target.ID, key, []byte(`{}`), "hash")
			if err != nil {
				t.Fatal(err)
			}
			opts, err := redis.ParseURL(redisURL)
			if err != nil {
				t.Fatal(err)
			}
			rdb := redis.NewClient(opts)
			t.Cleanup(func() { rdb.Close() })
			stream := "test-ack-recovery-" + event.ID
			const group = "test-group"
			t.Cleanup(func() { rdb.Del(context.Background(), stream) })
			if err := rdb.XGroupCreateMkStream(ctx, stream, group, "0").Err(); err != nil {
				t.Fatal(err)
			}
			if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"event_id": event.ID}}).Err(); err != nil {
				t.Fatal(err)
			}
			streams, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
				Group: group, Consumer: "test-worker", Streams: []string{stream, ">"}, Count: 1,
			}).Result()
			if err != nil {
				t.Fatal(err)
			}
			msg := streams[0].Messages[0]
			targetDB := openDB()
			newPool := func(db *sql.DB) *Pool {
				return NewPool(store.NewEventStore(db), store.NewTargetStore(targetDB), rdb,
					delivery.NewClient(time.Second, time.Second, 1024, true), testPolicy(),
					testCircuitConfig(), stream, group, testLogger(), testMetrics())
			}
			pool := newPool(db)
			if failureAt == "event_read" {
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			pool.processMessage(ctx, msg, "test-worker")
			pool.reclaimStale(ctx, 0) // The reclaimer must preserve failures too.
			pending, err := rdb.XPending(ctx, stream, group).Result()
			if err != nil {
				t.Fatal(err)
			}
			if pending.Count != 1 {
				t.Fatalf("pending after database failure = %d, want 1", pending.Count)
			}
			recoveredDB := openDB()
			// Model the crashed owner's lease expiring without a wall-clock wait.
			if _, err := recoveredDB.Exec(`UPDATE events SET delivery_lease_until=now()-interval '1 second' WHERE id=$1`, event.ID); err != nil {
				t.Fatal(err)
			}
			newPool(recoveredDB).reclaimStale(ctx, 0)
			got, err := store.NewEventStore(recoveredDB).GetByID(ctx, event.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != store.StatusSucceeded || got.AttemptCount != 1 {
				t.Fatalf("recovered event = %+v, want SUCCEEDED with one committed attempt", got)
			}
			pending, err = rdb.XPending(ctx, stream, group).Result()
			if err != nil {
				t.Fatal(err)
			}
			if pending.Count != 0 {
				t.Fatalf("pending after recovery = %d, want 0", pending.Count)
			}
			wantCalls := int64(1)
			if failureAt == "attempt_commit" {
				wantCalls = 2
			}
			if got := calls.Load(); got != wantCalls {
				t.Errorf("HTTP calls = %d, want %d", got, wantCalls)
			}
		})
	}
}

func TestPool_Handle_SuccessReachesSucceeded(t *testing.T) {
	targetStore, eventStore := testStores(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	target, err := targetStore.Create(ctx, "example", srv.URL, nil)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	event, _, err := eventStore.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	pool := NewPool(eventStore, targetStore, nil,
		delivery.NewClient(2*time.Second, 2*time.Second, 1024, true), testPolicy(), testCircuitConfig(), "stream", "group", testLogger(), testMetrics())
	pool.handle(ctx, redis.XMessage{ID: "1-1", Values: map[string]any{"event_id": event.ID}})

	got, err := eventStore.GetByID(ctx, event.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != store.StatusSucceeded {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusSucceeded)
	}
	if got.AttemptCount != 1 {
		t.Errorf("AttemptCount = %d, want 1", got.AttemptCount)
	}

	attempts, err := eventStore.ListAttempts(ctx, event.ID, 10)
	if err != nil {
		t.Fatalf("ListAttempts() error = %v", err)
	}
	if len(attempts) != 1 || attempts[0].Outcome != store.OutcomeSuccess {
		t.Errorf("attempts = %+v, want one SUCCESS attempt", attempts)
	}
}

func TestPool_Handle_FailureReachesRetryWait(t *testing.T) {
	targetStore, eventStore := testStores(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	target, err := targetStore.Create(ctx, "example", srv.URL, nil)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	event, _, err := eventStore.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	pool := NewPool(eventStore, targetStore, nil,
		delivery.NewClient(2*time.Second, 2*time.Second, 1024, true), testPolicy(), testCircuitConfig(), "stream", "group", testLogger(), testMetrics())
	pool.handle(ctx, redis.XMessage{ID: "1-1", Values: map[string]any{"event_id": event.ID}})

	got, err := eventStore.GetByID(ctx, event.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != store.StatusRetryWait {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusRetryWait)
	}
}

func TestPool_Handle_NonRetryableFailureGoesStraightToDLQ(t *testing.T) {
	targetStore, eventStore := testStores(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound) // 404 is non-retryable per QUEUE_AND_DELIVERY_SEMANTICS.md
	}))
	defer srv.Close()

	target, err := targetStore.Create(ctx, "example", srv.URL, nil)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	event, _, err := eventStore.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	// MaxAttempts is generous; a non-retryable failure must still go straight to DLQ.
	pool := NewPool(eventStore, targetStore, nil,
		delivery.NewClient(2*time.Second, 2*time.Second, 1024, true), testPolicy(), testCircuitConfig(), "stream", "group", testLogger(), testMetrics())
	pool.handle(ctx, redis.XMessage{ID: "1-1", Values: map[string]any{"event_id": event.ID}})

	got, err := eventStore.GetByID(ctx, event.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != store.StatusDLQ {
		t.Errorf("Status = %q, want %q on first 404", got.Status, store.StatusDLQ)
	}
	if got.AttemptCount != 1 {
		t.Errorf("AttemptCount = %d, want 1", got.AttemptCount)
	}
}

func TestPool_Handle_ExhaustedRetriesGoToDLQ(t *testing.T) {
	targetStore, eventStore := testStores(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	target, err := targetStore.Create(ctx, "example", srv.URL, nil)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	event, _, err := eventStore.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	policy := retry.Policy{MaxAttempts: 2, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
	pool := NewPool(eventStore, targetStore, nil,
		delivery.NewClient(2*time.Second, 2*time.Second, 1024, true), policy, testCircuitConfig(), "stream", "group", testLogger(), testMetrics())

	msg := redis.XMessage{ID: "1-1", Values: map[string]any{"event_id": event.ID}}
	pool.handle(ctx, msg) // attempt 1/2: still retryable

	got, err := eventStore.GetByID(ctx, event.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != store.StatusRetryWait {
		t.Fatalf("Status after attempt 1 = %q, want %q", got.Status, store.StatusRetryWait)
	}

	if _, err := eventStore.ScheduleDueRetries(ctx, 100); err != nil {
		t.Fatal(err)
	}
	pool.handle(ctx, msg) // attempt 2/2: attempts exhausted

	got, err = eventStore.GetByID(ctx, event.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != store.StatusDLQ {
		t.Errorf("Status after exhausting attempts = %q, want %q", got.Status, store.StatusDLQ)
	}
	if got.AttemptCount != 2 {
		t.Errorf("AttemptCount = %d, want 2", got.AttemptCount)
	}
}

func TestPool_Handle_DuplicateMessageAfterSuccessIsNoop(t *testing.T) {
	targetStore, eventStore := testStores(t)
	ctx := context.Background()

	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	target, err := targetStore.Create(ctx, "example", srv.URL, nil)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	event, _, err := eventStore.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	pool := NewPool(eventStore, targetStore, nil,
		delivery.NewClient(2*time.Second, 2*time.Second, 1024, true), testPolicy(), testCircuitConfig(), "stream", "group", testLogger(), testMetrics())

	msg := redis.XMessage{ID: "1-1", Values: map[string]any{"event_id": event.ID}}
	pool.handle(ctx, msg)
	pool.handle(ctx, msg) // duplicate queue message for the same (now SUCCEEDED) event

	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Errorf("target received %d requests, want exactly 1 (duplicate message must not re-deliver)", got)
	}

	attempts, err := eventStore.ListAttempts(ctx, event.ID, 10)
	if err != nil {
		t.Fatalf("ListAttempts() error = %v", err)
	}
	if len(attempts) != 1 {
		t.Errorf("attempt count = %d, want 1 (duplicate message must not record a second attempt)", len(attempts))
	}
}

func TestPool_Handle_CircuitOpensAndBlocksFurtherDeliveryWithoutConsumingAttempt(t *testing.T) {
	targetStore, eventStore := testStores(t)
	ctx := context.Background()

	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	target, err := targetStore.Create(ctx, "example", srv.URL, nil)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	circuitCfg := CircuitConfig{FailureThreshold: 2, OpenCooldown: time.Hour}
	pool := NewPool(eventStore, targetStore, nil,
		delivery.NewClient(2*time.Second, 2*time.Second, 1024, true), testPolicy(), circuitCfg, "stream", "group", testLogger(), testMetrics())

	event1, _, err := eventStore.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("create event1: %v", err)
	}
	event2, _, err := eventStore.CreateWithOutbox(ctx, target.ID, "key-2", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("create event2: %v", err)
	}

	pool.handle(ctx, redis.XMessage{ID: "1-1", Values: map[string]any{"event_id": event1.ID}})
	pool.handle(ctx, redis.XMessage{ID: "1-2", Values: map[string]any{"event_id": event2.ID}})

	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Fatalf("calls = %d, want 2 (both failures should reach the target before the circuit opens)", got)
	}

	target2, err := targetStore.Get(ctx, target.ID)
	if err != nil || target2.CircuitState != store.CircuitOpen {
		t.Fatalf("CircuitState = %q, err = %v, want OPEN after 2 consecutive failures", target2.CircuitState, err)
	}

	event3, _, err := eventStore.CreateWithOutbox(ctx, target.ID, "key-3", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("create event3: %v", err)
	}
	pool.handle(ctx, redis.XMessage{ID: "1-3", Values: map[string]any{"event_id": event3.ID}})

	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Errorf("calls = %d, want still 2 (an open circuit must block delivery)", got)
	}

	got3, err := eventStore.GetByID(ctx, event3.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got3.Status != store.StatusRetryWait {
		t.Errorf("event3 Status = %q, want RETRY_WAIT (deferred, not failed)", got3.Status)
	}
	if got3.AttemptCount != 0 {
		t.Errorf("event3 AttemptCount = %d, want 0 (a circuit-blocked delivery must not consume the retry budget)", got3.AttemptCount)
	}
}

func TestPool_Handle_HalfOpenProbeSuccessClosesCircuit(t *testing.T) {
	targetStore, eventStore := testStores(t)
	ctx := context.Background()

	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	target, err := targetStore.Create(ctx, "example", srv.URL, nil)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	// Threshold=1 opens on the first failure; a zero cooldown makes the next
	// delivery attempt immediately eligible to claim the HALF_OPEN probe.
	circuitCfg := CircuitConfig{FailureThreshold: 1, OpenCooldown: 0}
	pool := NewPool(eventStore, targetStore, nil,
		delivery.NewClient(2*time.Second, 2*time.Second, 1024, true), testPolicy(), circuitCfg, "stream", "group", testLogger(), testMetrics())

	event1, _, err := eventStore.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("create event1: %v", err)
	}
	pool.handle(ctx, redis.XMessage{ID: "1-1", Values: map[string]any{"event_id": event1.ID}})

	got, err := targetStore.Get(ctx, target.ID)
	if err != nil || got.CircuitState != store.CircuitOpen {
		t.Fatalf("CircuitState = %q, err = %v, want OPEN after the first failure", got.CircuitState, err)
	}

	event2, _, err := eventStore.CreateWithOutbox(ctx, target.ID, "key-2", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("create event2: %v", err)
	}
	pool.handle(ctx, redis.XMessage{ID: "1-2", Values: map[string]any{"event_id": event2.ID}}) // the probe

	got, err = targetStore.Get(ctx, target.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.CircuitState != store.CircuitClosed {
		t.Errorf("CircuitState = %q, want CLOSED after a successful probe", got.CircuitState)
	}

	got2, err := eventStore.GetByID(ctx, event2.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got2.Status != store.StatusSucceeded {
		t.Errorf("event2 Status = %q, want SUCCEEDED (the probe delivery itself succeeded)", got2.Status)
	}
}

func TestPool_ReclaimStale_RecoversMessageAbandonedByDeadConsumer(t *testing.T) {
	targetStore2, eventStore := testStores(t)
	ctx := context.Background()

	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL not set; skipping crash-recovery test")
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("redis.ParseURL() error = %v", err)
	}
	redisClient := redis.NewClient(opts)
	defer redisClient.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	target, err := targetStore2.Create(ctx, "example", srv.URL, nil)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	event, _, err := eventStore.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	stream := fmt.Sprintf("test-reclaim-stream-%d", time.Now().UnixNano())
	const group = "test-group"
	if err := redisClient.XGroupCreateMkStream(ctx, stream, group, "0").Err(); err != nil {
		t.Fatalf("XGroupCreateMkStream() error = %v", err)
	}
	if err := redisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: stream, Values: map[string]any{"event_id": event.ID},
	}).Err(); err != nil {
		t.Fatalf("XAdd() error = %v", err)
	}

	// A "dead" consumer claims the message (via XReadGroup) but crashes
	// before acking it - exactly the scenario reclaimStale must recover.
	if _, err := redisClient.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: group, Consumer: "dead-worker", Streams: []string{stream, ">"}, Count: 1,
	}).Result(); err != nil {
		t.Fatalf("XReadGroup() error = %v", err)
	}

	pool := NewPool(eventStore, targetStore2, redisClient,
		delivery.NewClient(2*time.Second, 2*time.Second, 1024, true), testPolicy(), testCircuitConfig(), stream, group, testLogger(), testMetrics())
	pool.reclaimStale(ctx, 0) // minIdle=0: eligible immediately, no need to wait out a real idle window

	got, err := eventStore.GetByID(ctx, event.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != store.StatusSucceeded {
		t.Errorf("Status = %q, want SUCCEEDED (reclaimed message must be delivered)", got.Status)
	}

	pending, err := redisClient.XPending(ctx, stream, group).Result()
	if err != nil {
		t.Fatalf("XPending() error = %v", err)
	}
	if pending.Count != 0 {
		t.Errorf("pending count = %d, want 0 (reclaimed message must be acked)", pending.Count)
	}
}
