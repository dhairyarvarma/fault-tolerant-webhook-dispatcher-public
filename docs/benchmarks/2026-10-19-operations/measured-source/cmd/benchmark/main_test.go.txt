package main

import (
	"context"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/testutil"
	"math"
	"testing"
	"time"
)

func TestDeliverySummaryScopesRunAndIncludesFailedEvents(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	target, err := store.NewTargetStore(db).Create(ctx, "benchmark", "https://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	events := store.NewEventStore(db)
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	for i, key := range []string{"run-1", "run-2", "run-3", "other-1"} {
		event, _, err := events.CreateWithOutbox(ctx, target.ID, key, []byte(`{}`), "hash")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE events SET created_at=$2 WHERE id=$1`, event.ID, base); err != nil {
			t.Fatal(err)
		}
		outcome := store.OutcomeSuccess
		if i == 2 {
			outcome = store.OutcomeNonRetryableFailure
		}
		seconds := time.Second
		if i == 1 {
			seconds = 3 * time.Second
		}
		if _, err := events.RecordAttemptAndTransition(ctx, event.ID, store.AttemptResult{Outcome: outcome, StartedAt: base, FinishedAt: base.Add(seconds)}, 5, func(int) time.Duration { return 0 }); err != nil {
			t.Fatal(err)
		}
	}
	report, err := deliverySummary(ctx, db, target.ID, "run")
	if err != nil {
		t.Fatal(err)
	}
	if report.Accepted != 3 || report.Succeeded != 2 || report.DLQ != 1 || report.Pending != 0 || report.P50 == nil || math.Abs(*report.P50-2) > 0.0001 || report.P95 == nil || math.Abs(*report.P95-2.9) > 0.0001 {
		t.Fatalf("report=%+v", report)
	}
	canceled, _, err := events.CreateWithOutbox(ctx, target.ID, "run-canceled", []byte(`{}`), "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE events SET status='CANCELED' WHERE id=$1`, canceled.ID); err != nil {
		t.Fatal(err)
	}
	report, err = deliverySummary(ctx, db, target.ID, "run")
	if err != nil || report.Accepted != 4 || report.Canceled != 1 {
		t.Fatalf("canceled report=%+v err=%v", report, err)
	}
	empty, err := deliverySummary(ctx, db, target.ID, "missing-run")
	if err != nil || empty.Accepted != 0 || empty.P50 != nil {
		t.Fatalf("empty report=%+v err=%v", empty, err)
	}
	for _, tc := range []struct{ run, status string }{
		{"run", "terminal_failures"}, {"missing-run", "count_mismatch"}, {"other", "verified"},
	} {
		expected := int64(1)
		if tc.run == "run" {
			expected = 4
		}
		status, err := verifyDelivery(t.Context(), 0, expected, func(ctx context.Context) (deliveryReport, error) {
			return deliverySummary(ctx, db, target.ID, tc.run)
		}, func(deliveryReport) error { return nil })
		if status != tc.status || (err == nil) != (status == "verified") {
			t.Fatalf("run=%s status=%s err=%v", tc.run, status, err)
		}
	}
}
