package main

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/config"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/observability"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/testutil"
)

func TestRetention_DisabledAndCanceledDoNotTouchStore(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runRetention(context.Background(), nil, config.Config{}, observability.NewMetrics(), logger)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runRetention(ctx, nil, config.Config{HistoryRetentionAge: 24 * time.Hour, HistoryRetentionInterval: time.Hour, HistoryRetentionBatch: 1}, observability.NewMetrics(), logger)
}

func TestRetention_EnabledBatchAndShutdown(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	target, err := store.NewTargetStore(db).Create(ctx, "retention", "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	events := store.NewEventStore(db)
	e, _, err := events.CreateWithOutbox(ctx, target.ID, "old", []byte(`{}`), "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE events SET status='SUCCEEDED',created_at=now()-interval '48 hours',completed_at=now()-interval '48 hours' WHERE id=$1`, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE outbox_messages SET published_at=now() WHERE event_id=$1`, e.ID); err != nil {
		t.Fatal(err)
	}
	m := observability.NewMetrics()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	done := make(chan struct{})
	go func() {
		defer close(done)
		runRetention(ctx, events, config.Config{HistoryRetentionAge: 24 * time.Hour, HistoryRetentionInterval: 10 * time.Millisecond, HistoryRetentionBatch: 1}, m, logger)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		w := httptest.NewRecorder()
		m.Handler(w, httptest.NewRequest("GET", "/metrics", nil))
		if strings.Contains(w.Body.String(), "webhook_history_events_pruned_total 1\n") {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("enabled cleanup did not commit/count")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not stop")
	}
	if _, err := events.GetByID(context.Background(), e.ID); err != store.ErrNotFound {
		t.Fatalf("event not removed: %v", err)
	}
}
