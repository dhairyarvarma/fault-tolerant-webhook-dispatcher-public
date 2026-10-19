package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

func TestDeliveryLease_FencesExpiredOwners(t *testing.T) {
	db, events, target := retryDB(t)
	ctx := context.Background()
	event, _, err := events.CreateWithOutbox(ctx, target.ID, "lease", []byte(`{}`), "hash")
	if err != nil {
		t.Fatal(err)
	}
	first, ok, err := events.ClaimDelivery(ctx, event.ID, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if _, ok, err := events.ClaimDelivery(ctx, event.ID, time.Minute); err != nil || ok {
		t.Fatalf("active claim stolen: %v %v", ok, err)
	}
	if _, err := db.Exec(`UPDATE events SET delivery_lease_until=now()-interval '1 second' WHERE id=$1`, event.ID); err != nil {
		t.Fatal(err)
	}
	second, ok, err := events.ClaimDelivery(ctx, event.ID, time.Minute)
	if err != nil || !ok || first == second {
		t.Fatalf("reclaim: %v %v", ok, err)
	}
	if err := events.ReleaseDelivery(ctx, event.ID, first); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := events.ClaimDelivery(ctx, event.ID, time.Minute); err != nil || ok {
		t.Fatalf("old release cleared new lease: %v %v", ok, err)
	}
	result := store.AttemptResult{Outcome: store.OutcomeSuccess, StartedAt: time.Now(), FinishedAt: time.Now()}
	if applied, err := events.RecordAttemptAndTransition(ctx, event.ID, result, 5, func(int) time.Duration { return 0 }, first); err != nil || applied {
		t.Fatalf("stale completion applied: %v %v", applied, err)
	}
	if applied, err := events.DeferRetry(ctx, event.ID, time.Now(), first); err != nil || applied {
		t.Fatalf("stale deferral applied: %v %v", applied, err)
	}
	if applied, err := events.RecordAttemptAndTransition(ctx, event.ID, result, 5, func(int) time.Duration { return 0 }, second); err != nil || !applied {
		t.Fatalf("current completion: %v %v", applied, err)
	}
}

func TestRecoverStaleWork_ExcludesActiveFutureTerminalAndPendingPublication(t *testing.T) {
	db, events, target := retryDB(t)
	ctx := context.Background()
	for _, key := range []string{"lost-pending", "lost-queued", "active", "future", "terminal", "unpublished", "recent"} {
		event, _, err := events.CreateWithOutbox(ctx, target.ID, key, []byte(`{}`), "hash")
		if err != nil {
			t.Fatal(err)
		}
		if key != "unpublished" {
			if _, err := db.Exec(`UPDATE outbox_messages SET published_at=now() WHERE event_id=$1`, event.ID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(`UPDATE events SET updated_at=now()-interval '2 minutes' WHERE id=$1`, event.ID); err != nil {
			t.Fatal(err)
		}
		switch key {
		case "lost-queued":
			_, err = db.Exec(`UPDATE events SET status='QUEUED' WHERE id=$1`, event.ID)
		case "active":
			_, err = db.Exec(`UPDATE events SET delivery_token=gen_random_uuid(), delivery_lease_until=now()+interval '1 hour' WHERE id=$1`, event.ID)
		case "future":
			_, err = db.Exec(`UPDATE events SET status='RETRY_WAIT',next_attempt_at=now()+interval '1 hour' WHERE id=$1`, event.ID)
		case "terminal":
			_, err = db.Exec(`UPDATE events SET status='SUCCEEDED' WHERE id=$1`, event.ID)
		case "recent":
			_, err = db.Exec(`UPDATE events SET updated_at=now() WHERE id=$1`, event.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []int64{1, 1, 0} {
		if n, err := events.RecoverStaleWork(ctx, 1, time.Minute); err != nil || n != want {
			t.Fatalf("recover=%d want=%d err=%v", n, want, err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM outbox_messages WHERE published_at IS NULL`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("unpublished=%d want original + two recoveries", count)
	}
}
