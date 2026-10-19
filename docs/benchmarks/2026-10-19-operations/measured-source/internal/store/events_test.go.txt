package store

import (
	"context"
	"sync"
	"testing"
)

func createTestTarget(t *testing.T, ctx context.Context, ts *TargetStore) Target {
	t.Helper()
	target, err := ts.Create(ctx, "example-service", "https://example.com/webhook", nil)
	if err != nil {
		t.Fatalf("create test target: %v", err)
	}
	return target
}

func TestEventStore_CreateWithOutbox_PersistsEventAndOutboxRow(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)
	es := NewEventStore(db)

	target := createTestTarget(t, ctx, ts)

	event, created, err := es.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{"type":"invoice.created"}`), "hash-1")
	if err != nil {
		t.Fatalf("CreateWithOutbox() error = %v", err)
	}
	if !created {
		t.Fatal("expected created = true for new event")
	}
	if event.Status != StatusPending {
		t.Errorf("Status = %q, want %q", event.Status, StatusPending)
	}

	var outboxCount int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM outbox_messages WHERE event_id = $1`, event.ID,
	).Scan(&outboxCount); err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	if outboxCount != 1 {
		t.Errorf("outbox row count = %d, want 1", outboxCount)
	}
}

func TestEventStore_CreateWithOutbox_DuplicateIdempotencyKeyReturnsExisting(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)
	es := NewEventStore(db)

	target := createTestTarget(t, ctx, ts)

	first, created, err := es.CreateWithOutbox(ctx, target.ID, "shared-key", []byte(`{"n":1}`), "hash-a")
	if err != nil || !created {
		t.Fatalf("first CreateWithOutbox() = (%+v, %v, %v)", first, created, err)
	}

	second, created, err := es.CreateWithOutbox(ctx, target.ID, "shared-key", []byte(`{"n":2}`), "hash-b")
	if err != nil {
		t.Fatalf("second CreateWithOutbox() error = %v", err)
	}
	if created {
		t.Fatal("expected created = false for duplicate idempotency key")
	}
	if second.ID != first.ID {
		t.Errorf("second.ID = %q, want %q (the original event)", second.ID, first.ID)
	}
	if second.PayloadHash != "hash-a" {
		t.Errorf("payload was overwritten by duplicate request: PayloadHash = %q, want hash-a", second.PayloadHash)
	}

	var eventCount int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM events WHERE target_id = $1 AND idempotency_key = $2`, target.ID, "shared-key",
	).Scan(&eventCount); err != nil {
		t.Fatalf("query events: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("event count = %d, want exactly 1 logical event", eventCount)
	}

	var outboxCount int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM outbox_messages WHERE event_id = $1`, first.ID,
	).Scan(&outboxCount); err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	if outboxCount != 1 {
		t.Errorf("outbox row count = %d, want 1 (no second publish on duplicate)", outboxCount)
	}
}

func TestEventStore_CreateWithOutbox_ConcurrentSameKeyCreatesOneEvent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)
	es := NewEventStore(db)

	target := createTestTarget(t, ctx, ts)

	const n = 10
	var wg sync.WaitGroup
	ids := make([]string, n)
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ev, _, err := es.CreateWithOutbox(ctx, target.ID, "race-key", []byte(`{}`), "hash")
			ids[i] = ev.ID
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: CreateWithOutbox() error = %v", i, err)
		}
	}
	for i := 1; i < n; i++ {
		if ids[i] != ids[0] {
			t.Errorf("goroutine %d got event ID %q, want %q (all concurrent calls must agree on one event)", i, ids[i], ids[0])
		}
	}

	var eventCount int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM events WHERE target_id = $1 AND idempotency_key = $2`, target.ID, "race-key",
	).Scan(&eventCount); err != nil {
		t.Fatalf("query events: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("event count = %d, want exactly 1 under concurrent creation", eventCount)
	}
}

func TestEventStore_CreateWithOutbox_FailedInsertCreatesNoPhantomEvent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	es := NewEventStore(db)

	nonexistentTargetID := "00000000-0000-0000-0000-000000000000"
	const idempotencyKey = "phantom-event-rollback-test-key"
	_, _, err := es.CreateWithOutbox(ctx, nonexistentTargetID, idempotencyKey, []byte(`{}`), "hash")
	if err == nil {
		t.Fatal("expected a foreign key violation error for a nonexistent target")
	}

	var eventCount int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM events WHERE idempotency_key = $1`, idempotencyKey,
	).Scan(&eventCount); err != nil {
		t.Fatalf("query events: %v", err)
	}
	if eventCount != 0 {
		t.Errorf("event count = %d, want 0 (rolled-back transaction must leave no phantom event)", eventCount)
	}

	var outboxCount int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM outbox_messages om JOIN events e ON e.id = om.event_id WHERE e.idempotency_key = $1`,
		idempotencyKey,
	).Scan(&outboxCount); err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	if outboxCount != 0 {
		t.Errorf("outbox count = %d, want 0", outboxCount)
	}
}

func TestEventStore_GetByID_NotFound(t *testing.T) {
	db := testDB(t)
	es := NewEventStore(db)

	_, err := es.GetByID(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err != ErrNotFound {
		t.Fatalf("GetByID() error = %v, want ErrNotFound", err)
	}
}

func TestEventStore_ListAttempts_EmptyForNewEvent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)
	es := NewEventStore(db)

	target := createTestTarget(t, ctx, ts)
	event, _, err := es.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("CreateWithOutbox() error = %v", err)
	}

	attempts, err := es.ListAttempts(ctx, event.ID, 50)
	if err != nil {
		t.Fatalf("ListAttempts() error = %v", err)
	}
	if len(attempts) != 0 {
		t.Errorf("len(attempts) = %d, want 0", len(attempts))
	}
}
