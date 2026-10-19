package retry

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/testutil"
)

func TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis(t *testing.T) {
	for _, delay := range []time.Duration{0, time.Hour} {
		t.Run(delay.String(), func(t *testing.T) {
			db := testutil.OpenDB(t)
			ctx := context.Background()
			if err := store.Migrate(ctx, db); err != nil {
				t.Fatal(err)
			}
			events := store.NewEventStore(db)
			target, err := store.NewTargetStore(db).Create(ctx, "retry", "https://example.com/webhook", nil)
			if err != nil {
				t.Fatal(err)
			}
			event, _, err := events.CreateWithOutbox(ctx, target.ID, "retry-key", []byte(`{}`), "hash")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE outbox_messages SET published_at = now()`); err != nil {
				t.Fatal(err)
			}
			if _, err := events.RecordAttemptAndTransition(ctx, event.ID, store.AttemptResult{
				Outcome: store.OutcomeRetryableFailure, StartedAt: time.Now(), FinishedAt: time.Now(),
			}, 5, func(int) time.Duration { return delay }); err != nil {
				t.Fatal(err)
			}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			NewScheduler(events, logger).requeueDue(ctx)
			// Reconstruct the scheduler after commit, before the relay runs.
			NewScheduler(store.NewEventStore(db), logger).requeueDue(ctx)
			got, err := events.GetByID(ctx, event.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus, wantOutbox := store.StatusRetryWait, int64(0)
			if delay == 0 {
				wantStatus, wantOutbox = store.StatusQueued, 1
			}
			if got.Status != wantStatus {
				t.Errorf("status = %s, want %s", got.Status, wantStatus)
			}
			pending, err := store.NewOutboxStore(db).CountUnpublished(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if pending != wantOutbox {
				t.Errorf("pending outbox = %d, want %d", pending, wantOutbox)
			}
		})
	}
}

func TestScheduler_RequeueDue_ReportsDatabaseFailure(t *testing.T) {
	db, err := store.Open("postgres://unused:unused@localhost:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	NewScheduler(store.NewEventStore(db), slog.New(slog.NewTextHandler(&logs, nil))).requeueDue(context.Background())
	if !strings.Contains(logs.String(), "retry: schedule due retries failed") {
		t.Fatalf("missing failure log: %s", logs.String())
	}
}
