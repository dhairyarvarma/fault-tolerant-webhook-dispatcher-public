package store

import (
	"context"
	"testing"
	"time"
)

func TestTargetStore_CircuitAllow_ClosedAllows(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)

	target := createTestTarget(t, ctx, ts)

	allow, _, _, err := ts.CircuitAllow(ctx, target.ID, time.Minute)
	if err != nil {
		t.Fatalf("CircuitAllow() error = %v", err)
	}
	if !allow {
		t.Error("allow = false for a freshly created (CLOSED) target, want true")
	}
}

func TestTargetStore_RecordCircuitOutcome_OpensAtThreshold(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)

	target := createTestTarget(t, ctx, ts)
	const threshold = 3

	for i := 1; i < threshold; i++ {
		if _, err := ts.RecordCircuitOutcome(ctx, target.ID, false, threshold); err != nil {
			t.Fatalf("failure %d: RecordCircuitOutcome() error = %v", i, err)
		}
		got, err := ts.Get(ctx, target.ID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if got.CircuitState != CircuitClosed {
			t.Fatalf("failure %d: CircuitState = %q, want still CLOSED (below threshold)", i, got.CircuitState)
		}
	}

	if _, err := ts.RecordCircuitOutcome(ctx, target.ID, false, threshold); err != nil {
		t.Fatalf("threshold failure: RecordCircuitOutcome() error = %v", err)
	}
	got, err := ts.Get(ctx, target.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.CircuitState != CircuitOpen {
		t.Errorf("CircuitState = %q, want OPEN after %d consecutive failures", got.CircuitState, threshold)
	}

	allow, retryAt, _, err := ts.CircuitAllow(ctx, target.ID, time.Hour)
	if err != nil {
		t.Fatalf("CircuitAllow() error = %v", err)
	}
	if allow {
		t.Error("allow = true for an OPEN circuit within its cooldown, want false")
	}
	if retryAt.Before(time.Now().Add(59*time.Minute)) || retryAt.After(time.Now().Add(time.Hour)) {
		t.Errorf("retryAt = %v, want approximately one hour after opening", retryAt)
	}
}

func TestTargetStore_CircuitAllow_ReturnsRemainingCooldown(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)
	target := createTestTarget(t, ctx, ts)

	openedAt := time.Now().Add(-30 * time.Minute)
	if _, err := db.ExecContext(ctx, `
		UPDATE targets SET circuit_state = $2, circuit_opened_at = $3 WHERE id = $1
	`, target.ID, CircuitOpen, openedAt); err != nil {
		t.Fatalf("set open circuit: %v", err)
	}

	allow, retryAt, _, err := ts.CircuitAllow(ctx, target.ID, time.Hour)
	if err != nil {
		t.Fatalf("CircuitAllow() error = %v", err)
	}
	if allow {
		t.Fatal("allow = true before cooldown has elapsed, want false")
	}
	if retryAt.Before(time.Now().Add(29*time.Minute)) || retryAt.After(time.Now().Add(31*time.Minute)) {
		t.Errorf("retryAt = %v, want approximately 30 minutes from now", retryAt)
	}
}

func TestTargetStore_CircuitAllow_ClaimsHalfOpenAfterCooldownThenDeniesSecondCaller(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)

	target := createTestTarget(t, ctx, ts)
	for i := 0; i < 5; i++ {
		if _, err := ts.RecordCircuitOutcome(ctx, target.ID, false, 5); err != nil {
			t.Fatalf("RecordCircuitOutcome() error = %v", err)
		}
	}
	got, err := ts.Get(ctx, target.ID)
	if err != nil || got.CircuitState != CircuitOpen {
		t.Fatalf("setup: CircuitState = %q, err = %v, want OPEN", got.CircuitState, err)
	}

	// A zero cooldown means it's immediately eligible for a probe.
	first, _, _, err := ts.CircuitAllow(ctx, target.ID, 0)
	if err != nil {
		t.Fatalf("first CircuitAllow() error = %v", err)
	}
	if !first {
		t.Fatal("first caller: allow = false, want true (should claim the probe slot)")
	}

	got, err = ts.Get(ctx, target.ID)
	if err != nil || got.CircuitState != CircuitHalfOpen {
		t.Fatalf("after claiming probe: CircuitState = %q, err = %v, want HALF_OPEN", got.CircuitState, err)
	}

	second, _, _, err := ts.CircuitAllow(ctx, target.ID, 0)
	if err != nil {
		t.Fatalf("second CircuitAllow() error = %v", err)
	}
	if second {
		t.Error("second caller: allow = true, want false (a probe is already in flight)")
	}
}

func TestTargetStore_RecordCircuitOutcome_SuccessfulProbeCloses(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)

	target := createTestTarget(t, ctx, ts)
	for i := 0; i < 5; i++ {
		if _, err := ts.RecordCircuitOutcome(ctx, target.ID, false, 5); err != nil {
			t.Fatalf("RecordCircuitOutcome() error = %v", err)
		}
	}
	if _, _, _, err := ts.CircuitAllow(ctx, target.ID, 0); err != nil { // claim the HALF_OPEN probe
		t.Fatalf("CircuitAllow() error = %v", err)
	}

	if _, err := ts.RecordCircuitOutcome(ctx, target.ID, true, 5); err != nil {
		t.Fatalf("RecordCircuitOutcome(success) error = %v", err)
	}

	got, err := ts.Get(ctx, target.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.CircuitState != CircuitClosed {
		t.Errorf("CircuitState = %q, want CLOSED after a successful probe", got.CircuitState)
	}

	allow, _, _, err := ts.CircuitAllow(ctx, target.ID, time.Hour)
	if err != nil {
		t.Fatalf("CircuitAllow() error = %v", err)
	}
	if !allow {
		t.Error("allow = false for a CLOSED circuit, want true")
	}
}

func TestTargetStore_RecordCircuitOutcome_FailedProbeReopens(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)

	target := createTestTarget(t, ctx, ts)
	for i := 0; i < 5; i++ {
		if _, err := ts.RecordCircuitOutcome(ctx, target.ID, false, 5); err != nil {
			t.Fatalf("RecordCircuitOutcome() error = %v", err)
		}
	}
	if _, _, _, err := ts.CircuitAllow(ctx, target.ID, 0); err != nil { // claim the HALF_OPEN probe
		t.Fatalf("CircuitAllow() error = %v", err)
	}

	if _, err := ts.RecordCircuitOutcome(ctx, target.ID, false, 5); err != nil {
		t.Fatalf("RecordCircuitOutcome(failure) error = %v", err)
	}

	got, err := ts.Get(ctx, target.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.CircuitState != CircuitOpen {
		t.Errorf("CircuitState = %q, want OPEN after a failed probe", got.CircuitState)
	}

	// Must still respect a fresh cooldown, not be immediately probeable again.
	allow, _, _, err := ts.CircuitAllow(ctx, target.ID, time.Hour)
	if err != nil {
		t.Fatalf("CircuitAllow() error = %v", err)
	}
	if allow {
		t.Error("allow = true immediately after reopening, want false (fresh cooldown)")
	}
}
