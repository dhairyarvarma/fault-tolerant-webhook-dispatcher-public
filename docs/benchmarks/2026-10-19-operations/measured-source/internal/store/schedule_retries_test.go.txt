package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/testutil"
)

func retryDB(t *testing.T) (*sql.DB, *store.EventStore, store.Target) {
	t.Helper()
	db := testutil.OpenDB(t)
	if err := store.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	target, err := store.NewTargetStore(db).Create(context.Background(), "retry", "https://example.com/webhook", nil)
	if err != nil {
		t.Fatal(err)
	}
	return db, store.NewEventStore(db), target
}

func seedRetry(t *testing.T, events *store.EventStore, targetID, key string) store.Event {
	t.Helper()
	ctx := context.Background()
	event, _, err := events.CreateWithOutbox(ctx, targetID, key, []byte(`{}`), "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := events.RecordAttemptAndTransition(ctx, event.ID, store.AttemptResult{
		Outcome: store.OutcomeRetryableFailure, StartedAt: time.Now(), FinishedAt: time.Now(),
	}, 5, func(int) time.Duration { return 0 }); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestScheduleDueRetries_OutboxFailureRollsBackStateAndBatch(t *testing.T) {
	db, events, target := retryDB(t)
	ctx := context.Background()
	first := seedRetry(t, events, target.ID, "first")
	second := seedRetry(t, events, target.ID, "second")
	// Existing rows survive; new retry-outbox inserts fail after the CTE update.
	if _, err := db.ExecContext(ctx, `ALTER TABLE outbox_messages ADD CONSTRAINT reject_insert CHECK (false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := events.ScheduleDueRetries(ctx, 100); err == nil {
		t.Fatal("expected outbox insert failure")
	}
	for _, id := range []string{first.ID, second.ID} {
		got, err := events.GetByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != store.StatusRetryWait {
			t.Errorf("status after rollback = %s, want RETRY_WAIT", got.Status)
		}
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM outbox_messages`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("outbox rows after rollback = %d, want 2 original rows", count)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE outbox_messages DROP CONSTRAINT reject_insert`); err != nil {
		t.Fatal(err)
	}
	if n, err := events.ScheduleDueRetries(ctx, 100); err != nil || n != 2 {
		t.Fatalf("after repair: count=%d err=%v", n, err)
	}
}

func TestScheduleDueRetries_ConcurrentSchedulersCreateOneRowPerRetry(t *testing.T) {
	db, events, target := retryDB(t)
	const eventCount = 12
	for i := 0; i < eventCount; i++ {
		seedRetry(t, events, target.ID, fmt.Sprint(i))
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	start := make(chan struct{})
	counts, failures := make(chan int64, 8), make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			n, err := events.ScheduleDueRetries(ctx, eventCount)
			counts <- n
			failures <- err
		}()
	}
	close(start)
	wg.Wait()
	close(counts)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var total int64
	for n := range counts {
		total += n
	}
	if total != eventCount {
		t.Fatalf("scheduled = %d, want %d", total, eventCount)
	}
	var bad int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM (
		SELECT e.id FROM events e LEFT JOIN outbox_messages o ON o.event_id = e.id
		GROUP BY e.id HAVING count(o.id) <> 2 OR min(e.status) <> 'QUEUED'
	) invalid`).Scan(&bad); err != nil {
		t.Fatal(err)
	}
	if bad != 0 {
		t.Errorf("%d events lack exactly one original and one retry outbox row", bad)
	}
	if n, err := events.ScheduleDueRetries(ctx, eventCount); err != nil || n != 0 {
		t.Fatalf("repeat poll: count=%d err=%v", n, err)
	}
}

func TestScheduleDueRetries_BatchLimitAndEligibility(t *testing.T) {
	db, events, target := retryDB(t)
	ctx := context.Background()
	for i, status := range []string{store.StatusPending, store.StatusQueued, store.StatusSucceeded, store.StatusDLQ, store.StatusCanceled} {
		ev, _, err := events.CreateWithOutbox(ctx, target.ID, fmt.Sprintf("other-%d", i), []byte(`{}`), "hash")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE events SET status=$2, next_attempt_at=now()-interval '1 hour' WHERE id=$1`, ev.ID, status); err != nil {
			t.Fatal(err)
		}
	}
	future := seedRetry(t, events, target.ID, "future")
	if _, err := db.ExecContext(ctx, `UPDATE events SET next_attempt_at=now()+interval '1 hour' WHERE id=$1`, future.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		seedRetry(t, events, target.ID, fmt.Sprintf("due-%d", i))
	}
	for _, want := range []int64{2, 1, 0} {
		if n, err := events.ScheduleDueRetries(ctx, 2); err != nil || n != want {
			t.Fatalf("schedule: count=%d want=%d err=%v", n, want, err)
		}
	}
	got, err := events.GetByID(ctx, future.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusRetryWait {
		t.Errorf("future status = %s, want RETRY_WAIT", got.Status)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM outbox_messages`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 12 {
		t.Errorf("outbox rows = %d, want 9 originals + 3 due retries", count)
	}
}
