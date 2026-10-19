package worker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/delivery"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
	"github.com/redis/go-redis/v9"
)

func TestTargetConcurrency_BoundedAndReleased(t *testing.T) {
	p := &Pool{targetLimit: 2}
	var active, peak atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !p.acquireTarget("same") {
				return
			}
			n := active.Add(1)
			for old := peak.Load(); n > old; old = peak.Load() {
				if peak.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
			p.releaseTarget("same")
		}()
	}
	wg.Wait()
	if peak.Load() > 2 || len(p.targetActive) != 0 {
		t.Fatalf("peak=%d retained=%v", peak.Load(), p.targetActive)
	}
	for i := 0; i < 100; i++ {
		id := fmt.Sprint(i)
		if !p.acquireTarget(id) {
			t.Fatal("new target rejected")
		}
		p.releaseTarget(id)
	}
	if len(p.targetActive) != 0 {
		t.Fatal("idle target identities retained")
	}
	p.targetLimit = 0
	for i := 0; i < 10; i++ {
		if !p.acquireTarget("same") {
			t.Fatal("disabled cap rejected work")
		}
	}
	if len(p.targetActive) != 0 {
		t.Fatal("disabled cap allocated target entries")
	}
}

func TestPool_TargetConcurrencyDefersWithoutAttemptAndRecovers(t *testing.T) {
	targets, events := testStores(t)
	ctx := context.Background()
	started, release := make(chan struct{}), make(chan struct{})
	var slowCalls atomic.Int64
	var once sync.Once
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slowCalls.Add(1)
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(200)
	}))
	defer slow.Close()
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer fast.Close()
	slowTarget, err := targets.Create(ctx, "slow", slow.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	fastTarget, err := targets.Create(ctx, "fast", fast.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	create := func(target, key string) store.Event {
		t.Helper()
		e, _, err := events.CreateWithOutbox(ctx, target, key, []byte(`{}`), "hash")
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	first, second, healthy := create(slowTarget.ID, "first"), create(slowTarget.ID, "second"), create(fastTarget.ID, "healthy")
	p := NewPool(events, targets, nil, delivery.NewClient(time.Second, 5*time.Second, 1024, true), testPolicy(), testCircuitConfig(), "", "", testLogger(), testMetrics())
	p.targetLimit = 1
	p.slots = make(chan struct{}, 4)
	handle := func(id string) error { return p.handle(ctx, redis.XMessage{Values: map[string]any{"event_id": id}}) }
	done := make(chan error, 1)
	go func() { done <- handle(first.ID) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("slow request did not start")
	}
	if err := handle(second.ID); err != nil {
		t.Fatal(err)
	}
	deferred, err := events.GetByID(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deferred.Status != store.StatusRetryWait || deferred.AttemptCount != 0 || deferred.NextAttemptAt == nil || !deferred.NextAttemptAt.After(time.Now()) {
		t.Fatalf("not durably deferred: %+v", deferred)
	}
	attempts, err := events.ListAttempts(ctx, second.ID, 10)
	if err != nil || len(attempts) != 0 {
		t.Fatalf("deferral consumed attempt: %+v %v", attempts, err)
	}
	if err := handle(second.ID); err != nil {
		t.Fatal(err)
	}
	if slowCalls.Load() != 1 {
		t.Fatal("deferral or duplicate queue hint made an HTTP call")
	}
	w := httptest.NewRecorder()
	p.metrics.Handler(w, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(w.Body.String(), "webhook_target_capacity_deferrals_total 1\n") {
		t.Fatal("committed capacity deferral not counted exactly once")
	}
	if err := handle(healthy.ID); err != nil {
		t.Fatal(err)
	}
	got, err := events.GetByID(ctx, healthy.ID)
	if err != nil || got.Status != store.StatusSucceeded {
		t.Fatalf("healthy target blocked: %+v %v", got, err)
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(*deferred.NextAttemptAt) + 20*time.Millisecond)
	if n, err := events.ScheduleDueRetries(ctx, 10); err != nil || n != 1 {
		t.Fatalf("scheduled=%d error=%v", n, err)
	}
	if err := handle(second.ID); err != nil {
		t.Fatal(err)
	}
	got, err = events.GetByID(ctx, second.ID)
	if err != nil || got.Status != store.StatusSucceeded || got.AttemptCount != 1 {
		t.Fatalf("deferred work failed to recover: %+v %v", got, err)
	}
	if len(p.targetActive) != 0 {
		t.Fatal("target slot leaked")
	}
	if slowCalls.Load() != 2 {
		t.Fatalf("slow HTTP requests=%d want 2", slowCalls.Load())
	}
}
