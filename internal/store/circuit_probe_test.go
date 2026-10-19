package store_test

import (
	"context"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
	"testing"
	"time"
)

func TestCircuitProbeLease_ExpiredProbeRecoversAndLateOutcomeIsIgnored(t *testing.T) {
	db, _, target := retryDB(t)
	targets := store.NewTargetStore(db)
	ctx := context.Background()
	if _, err := targets.RecordCircuitOutcome(ctx, target.ID, false, 1); err != nil {
		t.Fatal(err)
	}
	allow, _, first, err := targets.CircuitAllow(ctx, target.ID, 0, time.Minute)
	if err != nil || !allow {
		t.Fatalf("probe: allow=%v err=%v", allow, err)
	}
	allow, retryAt, _, err := targets.CircuitAllow(ctx, target.ID, 0, time.Minute)
	if err != nil || allow || !retryAt.After(time.Now()) {
		t.Fatalf("second probe: allow=%v retry=%v err=%v", allow, retryAt, err)
	}
	if _, err := db.Exec(`UPDATE targets SET circuit_probe_until=now()-interval '1 second' WHERE id=$1`, target.ID); err != nil {
		t.Fatal(err)
	}
	if state, err := targets.RecordCircuitOutcome(ctx, target.ID, true, 1, first); err != nil || state != store.CircuitHalfOpen {
		t.Fatalf("expired outcome changed state: %s %v", state, err)
	}
	allow, _, second, err := targets.CircuitAllow(ctx, target.ID, 0, time.Minute)
	if err != nil || !allow || second == first {
		t.Fatalf("replacement probe: %v %v", allow, err)
	}
	if state, err := targets.RecordCircuitOutcome(ctx, target.ID, true, 1, first); err != nil || state != store.CircuitHalfOpen {
		t.Fatalf("old probe changed state: %s %v", state, err)
	}
	if state, err := targets.RecordCircuitOutcome(ctx, target.ID, true, 1, second); err != nil || state != store.CircuitClosed {
		t.Fatalf("current probe: %s %v", state, err)
	}
	if state, err := targets.RecordCircuitOutcome(ctx, target.ID, false, 1, first); err != nil || state != store.CircuitClosed {
		t.Fatalf("late failure changed state: %s %v", state, err)
	}
}

func TestCircuitProbeLease_ClosedTrafficCannotUndoNewOpenState(t *testing.T) {
	db, _, target := retryDB(t)
	targets := store.NewTargetStore(db)
	ctx := context.Background()
	_, _, generation, err := targets.CircuitAllow(ctx, target.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := targets.RecordCircuitOutcome(ctx, target.ID, false, 1, generation); err != nil || state != store.CircuitOpen {
		t.Fatalf("open: %s %v", state, err)
	}
	if state, err := targets.RecordCircuitOutcome(ctx, target.ID, true, 1, generation); err != nil || state != store.CircuitOpen {
		t.Fatalf("late closed outcome: %s %v", state, err)
	}
}

func TestMetricCountsUseDurableStateAndExcludeDisabledTargets(t *testing.T) {
	db, events, target := retryDB(t)
	ctx := context.Background()
	for _, status := range []string{store.StatusPending, store.StatusDLQ, store.StatusSucceeded} {
		event, _, err := events.CreateWithOutbox(ctx, target.ID, status, []byte(`{}`), "hash")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE events SET status=$2 WHERE id=$1`, event.ID, status); err != nil {
			t.Fatal(err)
		}
	}
	if pending, dlq, err := events.WorkCounts(ctx); err != nil || pending != 1 || dlq != 1 {
		t.Fatalf("counts pending=%d dlq=%d err=%v", pending, dlq, err)
	}
	if _, err := db.Exec(`UPDATE targets SET circuit_state='HALF_OPEN' WHERE id=$1`, target.ID); err != nil {
		t.Fatal(err)
	}
	targets := store.NewTargetStore(db)
	if closed, open, half, err := targets.CircuitCounts(ctx); err != nil || closed != 0 || open != 0 || half != 1 {
		t.Fatalf("circuits=%d/%d/%d err=%v", closed, open, half, err)
	}
	if err := targets.Disable(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, half, err := targets.CircuitCounts(ctx); err != nil || half != 0 {
		t.Fatalf("disabled circuit counted: %d %v", half, err)
	}
}
