package outbox

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/observability"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/testutil"
)

func testDeps(t *testing.T) (*store.OutboxStore, *store.TargetStore, *store.EventStore, *redis.Client) {
	t.Helper()

	dbURL := os.Getenv("TEST_DATABASE_URL")
	redisURL := os.Getenv("TEST_REDIS_URL")
	if dbURL == "" || redisURL == "" {
		t.Skip("TEST_DATABASE_URL and TEST_REDIS_URL must both be set; skipping outbox relay test")
	}

	db := testutil.OpenDB(t)

	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("redis.ParseURL() error = %v", err)
	}
	redisClient := redis.NewClient(opts)
	t.Cleanup(func() { redisClient.Close() })

	return store.NewOutboxStore(db), store.NewTargetStore(db), store.NewEventStore(db), redisClient
}

func TestRelay_PublishPending_PublishesAndMarksPublished(t *testing.T) {
	outboxStore, targetStore, eventStore, redisClient := testDeps(t)
	ctx := context.Background()
	stream := fmt.Sprintf("test-stream-%d", time.Now().UnixNano())

	target, err := targetStore.Create(ctx, "example", "https://example.com/webhook", nil)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	event, created, err := eventStore.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{}`), "hash")
	if err != nil || !created {
		t.Fatalf("create event: created=%v err=%v", created, err)
	}

	relay := NewRelay(outboxStore, redisClient, stream, slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewMetrics())
	relay.publishPending(ctx)

	// The shared dev database may carry unpublished outbox rows left behind
	// by unrelated tests (store/api/worker), which this poll legitimately
	// publishes too. Scope the assertion to this test's own event instead of
	// assuming the stream is otherwise empty.
	countForEvent := func() int {
		msgs, err := redisClient.XRange(ctx, stream, "-", "+").Result()
		if err != nil {
			t.Fatalf("XRange() error = %v", err)
		}
		n := 0
		for _, m := range msgs {
			if m.Values["event_id"] == event.ID {
				n++
			}
		}
		return n
	}

	if n := countForEvent(); n != 1 {
		t.Fatalf("stream has %d messages for this event, want 1", n)
	}

	pending, err := outboxStore.FetchUnpublished(ctx, 1000)
	if err != nil {
		t.Fatalf("FetchUnpublished() error = %v", err)
	}
	for _, m := range pending {
		if m.EventID == event.ID {
			t.Error("outbox row for published event still shows as unpublished")
		}
	}

	// A second poll must not re-publish an already-published row.
	relay.publishPending(ctx)
	if n := countForEvent(); n != 1 {
		t.Errorf("stream has %d messages for this event after second poll, want still 1", n)
	}
}
