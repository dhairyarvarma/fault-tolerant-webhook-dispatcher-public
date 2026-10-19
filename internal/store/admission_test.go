package store_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCreateWithOutbox_AtomicAdmissionAndDuplicateAtCapacity(t *testing.T) {
	db, events, target := retryDB(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	var accepted, rejected atomic.Int64
	keys := make(chan string, 20)
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			key := fmt.Sprint(i)
			_, created, err := events.CreateWithOutbox(ctx, target.ID, key, []byte(`{}`), "hash", 3)
			if errors.Is(err, store.ErrOverloaded) {
				rejected.Add(1)
				return
			}
			if err != nil || !created {
				t.Errorf("create: %v %v", created, err)
				return
			}
			accepted.Add(1)
			keys <- key
		}(i)
	}
	close(start)
	wg.Wait()
	close(keys)
	if accepted.Load() != 3 || rejected.Load() != 17 {
		t.Fatalf("accepted=%d rejected=%d", accepted.Load(), rejected.Load())
	}
	for key := range keys {
		event, created, err := events.CreateWithOutbox(ctx, target.ID, key, []byte(`{}`), "hash", 3)
		if err != nil || created || event.ID == "" {
			t.Fatalf("duplicate at capacity: created=%v err=%v", created, err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM outbox_messages`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("outbox count=%d want 3", count)
	}
}

func TestReplay_FencesOldDeliveryAndOrdersHistoryByTime(t *testing.T) {
	_, events, target := retryDB(t)
	ctx := context.Background()
	event, _, err := events.CreateWithOutbox(ctx, target.ID, "replay", []byte(`{}`), "hash")
	if err != nil {
		t.Fatal(err)
	}
	token, ok, err := events.ClaimDelivery(ctx, event.ID, time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	for i, outcome := range []string{store.OutcomeRetryableFailure, store.OutcomeNonRetryableFailure} {
		at := base.Add(time.Duration(i) * time.Second)
		if _, err := events.RecordAttemptAndTransition(ctx, event.ID, store.AttemptResult{Outcome: outcome, StartedAt: at, FinishedAt: at}, 5, func(int) time.Duration { return 0 }); err != nil {
			t.Fatal(err)
		}
	}
	if applied, err := events.ReplayFromDLQ(ctx, event.ID); err != nil || !applied {
		t.Fatalf("replay: %v %v", applied, err)
	}
	at := base.Add(2 * time.Second)
	attempt := store.AttemptResult{Outcome: store.OutcomeSuccess, StartedAt: at, FinishedAt: at}
	if applied, err := events.RecordAttemptAndTransition(ctx, event.ID, attempt, 5, func(int) time.Duration { return 0 }, token); err != nil || applied {
		t.Fatalf("pre-replay owner applied: %v %v", applied, err)
	}
	if _, err := events.RecordAttemptAndTransition(ctx, event.ID, attempt, 5, func(int) time.Duration { return 0 }); err != nil {
		t.Fatal(err)
	}
	history, err := events.ListAttempts(ctx, event.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 || history[0].Outcome != store.OutcomeSuccess || history[0].AttemptNumber != 1 || history[1].AttemptNumber != 2 {
		t.Fatalf("history not chronological across replay: %+v", history)
	}
}
