//go:build !windows

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/queue"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/testutil"
)

func TestShutdownServerProcess(t *testing.T) {
	if os.Getenv("WEBHOOK_SHUTDOWN_TEST_PROCESS") != "1" {
		t.Skip("subprocess entry point")
	}
	if err := run(slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
}

type shutdownServer struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
	logs bytes.Buffer
}

func (p *shutdownServer) stop() error {
	select {
	case <-p.done:
		return p.err
	default:
	}
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	select {
	case <-p.done:
		return p.err
	case <-time.After(12 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
		return fmt.Errorf("server exceeded bounded shutdown: %s", p.logs.String())
	}
}

func startShutdownServer(t *testing.T, databaseURL, redisURL, stream, address string) *shutdownServer {
	t.Helper()
	p := &shutdownServer{done: make(chan struct{})}
	p.cmd = exec.Command(os.Args[0], "-test.run=^TestShutdownServerProcess$", "-test.timeout=45s")
	p.cmd.Env = append(os.Environ(),
		"WEBHOOK_SHUTDOWN_TEST_PROCESS=1", "APP_ENV=development", "HTTP_ADDR="+address,
		"DATABASE_URL="+databaseURL, "REDIS_URL="+redisURL, "REDIS_STREAM="+stream,
		"REDIS_CONSUMER_GROUP=shutdown-test", "AUTH_ENABLED=true", "API_KEY=shutdown-only",
		"SECRET_ENCRYPTION_KEY=a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s=",
		"ALLOW_PRIVATE_TARGETS=true", "WORKER_COUNT=1", "WORKER_TARGET_CONCURRENCY=0",
		"OUTBOX_POLL_INTERVAL=20ms", "DELIVERY_CONNECT_TIMEOUT=1s", "DELIVERY_REQUEST_TIMEOUT=30s",
		"DELIVERY_MAX_ATTEMPTS=5", "DELIVERY_BASE_BACKOFF=1s", "DELIVERY_MAX_BACKOFF=60s",
		"CIRCUIT_FAILURE_THRESHOLD=1000", "CIRCUIT_OPEN_COOLDOWN=30s",
		"EVENTS_MAX_PENDING_EVENTS=0", "HISTORY_RETENTION_AGE=0s")
	p.cmd.Stdout, p.cmd.Stderr = &p.logs, &p.logs
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.err = p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		if err := p.stop(); err != nil {
			t.Errorf("server shutdown: %v; output: %s", err, p.logs.String())
		}
	})
	client := http.Client{Timeout: 200 * time.Millisecond}
	waitShutdownCondition(t, "server readiness", func() bool {
		select {
		case <-p.done:
			t.Fatalf("server exited before readiness: %v; %s", p.err, p.logs.String())
		default:
		}
		resp, err := client.Get("http://" + address + "/readyz")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	return p
}

func waitShutdownCondition(t *testing.T, name string, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", name)
		case <-ticker.C:
		}
	}
}

func TestSIGTERMDuringDeliveryPreservesWorkForRestart(t *testing.T) {
	if os.Getenv("TEST_REDIS_URL") == "" {
		t.Skip("TEST_REDIS_URL required")
	}
	db := testutil.OpenDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	var schema string
	if err := db.QueryRowContext(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	rdb, err := queue.Open(os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	stream := "shutdown-" + schema
	t.Cleanup(func() { rdb.Del(context.Background(), stream); rdb.Close() })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var heldCalls, otherCalls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/hold" {
			if heldCalls.Add(1) == 1 {
				close(started)
				select {
				case <-r.Context().Done():
					close(canceled)
				case <-release:
				}
				return
			}
		} else {
			otherCalls.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(receiver.Close)
	t.Cleanup(func() { close(release) })
	first := startShutdownServer(t, u.String(), os.Getenv("TEST_REDIS_URL"), stream, address)
	targets, events := store.NewTargetStore(db), store.NewEventStore(db)
	create := func(path, key string) store.Event {
		t.Helper()
		target, err := targets.Create(ctx, key, receiver.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		event, _, err := events.CreateWithOutbox(ctx, target.ID, key, []byte(`{}`), "hash")
		if err != nil {
			t.Fatal(err)
		}
		return event
	}
	inFlight := create("/hold", "in-flight")
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("delivery never reached receiver")
	}
	pending := create("/ok", "pending")
	future := create("/ok", "future")
	if applied, err := events.DeferRetry(ctx, future.ID, time.Now().Add(time.Hour)); err != nil || !applied {
		t.Fatalf("seed future retry: %v %v", applied, err)
	}
	futureBefore, err := events.GetByID(ctx, future.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitShutdownCondition(t, "queued work behind active delivery", func() bool {
		messages, err := rdb.XRange(ctx, stream, "-", "+").Result()
		if err != nil {
			return false
		}
		for _, message := range messages {
			if message.Values["event_id"] == pending.ID {
				return true
			}
		}
		return false
	})
	if err := first.stop(); err != nil {
		t.Fatalf("SIGTERM during delivery: %v; %s", err, first.logs.String())
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("outbound request was not canceled")
	}
	left, err := rdb.XPending(ctx, stream, "shutdown-test").Result()
	if err != nil || left.Count != 1 {
		t.Fatalf("uncommitted delivery was acknowledged: pending=%+v err=%v", left, err)
	}
	for _, event := range []store.Event{inFlight, pending} {
		got, err := events.GetByID(ctx, event.ID)
		if err != nil || got.AttemptCount != 0 || store.IsTerminalStatus(got.Status) {
			t.Fatalf("shutdown lost or finalized unfinished work: %+v err=%v", got, err)
		}
	}
	if otherCalls.Load() != 0 {
		t.Fatal("queued/future delivery started after shutdown")
	}
	var released bool
	if err := db.QueryRowContext(ctx, `SELECT delivery_token IS NULL AND delivery_lease_until IS NULL FROM events WHERE id=$1`, inFlight.ID).Scan(&released); err != nil || !released {
		t.Fatalf("shutdown did not release delivery lease: %v %v", released, err)
	}
	// Advance only the fixture's grace timestamp, not the future retry deadline.
	if _, err := db.ExecContext(ctx, `UPDATE events SET updated_at=now()-interval '61 seconds' WHERE id=$1`, inFlight.ID); err != nil {
		t.Fatal(err)
	}
	second := startShutdownServer(t, u.String(), os.Getenv("TEST_REDIS_URL"), stream, address)
	for _, event := range []store.Event{inFlight, pending} {
		waitShutdownCondition(t, "unfinished delivery recovery", func() bool {
			got, err := events.GetByID(ctx, event.ID)
			return err == nil && got.Status == store.StatusSucceeded && got.AttemptCount == 1
		})
	}
	futureAfter, err := events.GetByID(ctx, future.ID)
	if err != nil || futureAfter.Status != store.StatusRetryWait || futureAfter.AttemptCount != 0 || futureAfter.NextAttemptAt == nil || futureBefore.NextAttemptAt == nil || !futureAfter.NextAttemptAt.Equal(*futureBefore.NextAttemptAt) {
		t.Fatalf("restart bypassed retry deadline: %+v err=%v", futureAfter, err)
	}
	if heldCalls.Load() != 2 || otherCalls.Load() != 1 {
		t.Fatalf("unexpected receiver calls: held=%d other=%d", heldCalls.Load(), otherCalls.Load())
	}
	if applied, err := events.DeferRetry(ctx, future.ID, time.Now().Add(-time.Second)); err != nil || !applied {
		t.Fatalf("advance retry fixture: %v %v", applied, err)
	}
	waitShutdownCondition(t, "due retry recovery", func() bool {
		got, err := events.GetByID(ctx, future.ID)
		return err == nil && got.Status == store.StatusSucceeded && got.AttemptCount == 1
	})
	if err := second.stop(); err != nil {
		t.Fatalf("idle SIGTERM: %v; %s", err, second.logs.String())
	}
}
