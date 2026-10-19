package store

import (
	"context"
	"testing"
	"time"
)

func fixedDelay(d time.Duration) func(int) time.Duration {
	return func(int) time.Duration { return d }
}

func TestEventStore_RecordAttemptAndTransition_SuccessReachesSucceeded(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)
	es := NewEventStore(db)

	target := createTestTarget(t, ctx, ts)
	event, _, err := es.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("CreateWithOutbox() error = %v", err)
	}

	applied, err := es.RecordAttemptAndTransition(ctx, event.ID, AttemptResult{
		Outcome: OutcomeSuccess, StartedAt: time.Now(), FinishedAt: time.Now(),
	}, 5, fixedDelay(time.Second))
	if err != nil {
		t.Fatalf("RecordAttemptAndTransition() error = %v", err)
	}
	if !applied {
		t.Fatal("applied = false, want true")
	}

	got, err := es.GetByID(ctx, event.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != StatusSucceeded {
		t.Errorf("Status = %q, want %q", got.Status, StatusSucceeded)
	}
	if got.NextAttemptAt != nil {
		t.Errorf("NextAttemptAt = %v, want nil", got.NextAttemptAt)
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt = nil, want set")
	}
}

func TestEventStore_RecordAttemptAndTransition_RetryableFailureSchedulesBackoffWithinBounds(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)
	es := NewEventStore(db)

	target := createTestTarget(t, ctx, ts)
	event, _, err := es.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("CreateWithOutbox() error = %v", err)
	}

	before := time.Now()
	const backoff = 5 * time.Second
	applied, err := es.RecordAttemptAndTransition(ctx, event.ID, AttemptResult{
		Outcome: OutcomeRetryableFailure, StartedAt: time.Now(), FinishedAt: time.Now(),
	}, 5, fixedDelay(backoff))
	if err != nil {
		t.Fatalf("RecordAttemptAndTransition() error = %v", err)
	}
	if !applied {
		t.Fatal("applied = false, want true")
	}

	got, err := es.GetByID(ctx, event.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != StatusRetryWait {
		t.Fatalf("Status = %q, want %q", got.Status, StatusRetryWait)
	}
	if got.NextAttemptAt == nil {
		t.Fatal("NextAttemptAt = nil, want set")
	}
	// next_attempt_at must be ~before+backoff, bounded to tolerate test execution time.
	earliest := before.Add(backoff)
	latest := time.Now().Add(backoff)
	if got.NextAttemptAt.Before(earliest) || got.NextAttemptAt.After(latest) {
		t.Errorf("NextAttemptAt = %v, want within [%v, %v]", got.NextAttemptAt, earliest, latest)
	}
	if got.CompletedAt != nil {
		t.Error("CompletedAt should remain nil while retry is pending")
	}
}

func TestEventStore_RecordAttemptAndTransition_ExhaustedRetriesGoToDLQ(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)
	es := NewEventStore(db)

	target := createTestTarget(t, ctx, ts)
	event, _, err := es.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("CreateWithOutbox() error = %v", err)
	}

	const maxAttempts = 3
	for i := 1; i <= maxAttempts; i++ {
		applied, err := es.RecordAttemptAndTransition(ctx, event.ID, AttemptResult{
			Outcome: OutcomeRetryableFailure, StartedAt: time.Now(), FinishedAt: time.Now(),
		}, maxAttempts, fixedDelay(time.Millisecond))
		if err != nil {
			t.Fatalf("attempt %d: RecordAttemptAndTransition() error = %v", i, err)
		}
		if !applied {
			t.Fatalf("attempt %d: applied = false, want true", i)
		}
	}

	got, err := es.GetByID(ctx, event.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != StatusDLQ {
		t.Errorf("Status = %q, want %q after exhausting %d attempts", got.Status, StatusDLQ, maxAttempts)
	}
	if got.AttemptCount != maxAttempts {
		t.Errorf("AttemptCount = %d, want %d", got.AttemptCount, maxAttempts)
	}
	if got.NextAttemptAt != nil {
		t.Errorf("NextAttemptAt = %v, want nil once DLQ'd", got.NextAttemptAt)
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt = nil, want set once DLQ'd")
	}

	attempts, err := es.ListAttempts(ctx, event.ID, 10)
	if err != nil {
		t.Fatalf("ListAttempts() error = %v", err)
	}
	if len(attempts) != maxAttempts {
		t.Errorf("len(attempts) = %d, want %d", len(attempts), maxAttempts)
	}

	// A duplicate delivery attempt arriving after DLQ must not resurrect the event or add another attempt row.
	applied, err := es.RecordAttemptAndTransition(ctx, event.ID, AttemptResult{
		Outcome: OutcomeRetryableFailure, StartedAt: time.Now(), FinishedAt: time.Now(),
	}, maxAttempts, fixedDelay(time.Millisecond))
	if err != nil {
		t.Fatalf("post-DLQ RecordAttemptAndTransition() error = %v", err)
	}
	if applied {
		t.Error("applied = true for a duplicate attempt after DLQ, want false")
	}
	attemptsAfter, err := es.ListAttempts(ctx, event.ID, 10)
	if err != nil {
		t.Fatalf("ListAttempts() error = %v", err)
	}
	if len(attemptsAfter) != maxAttempts {
		t.Errorf("attempt count after duplicate post-DLQ delivery = %d, want still %d", len(attemptsAfter), maxAttempts)
	}
}

func TestEventStore_RecordAttemptAndTransition_NonRetryableFailureGoesStraightToDLQ(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)
	es := NewEventStore(db)

	target := createTestTarget(t, ctx, ts)
	event, _, err := es.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("CreateWithOutbox() error = %v", err)
	}

	applied, err := es.RecordAttemptAndTransition(ctx, event.ID, AttemptResult{
		Outcome: OutcomeNonRetryableFailure, StartedAt: time.Now(), FinishedAt: time.Now(),
	}, 5, fixedDelay(time.Second)) // plenty of attempts remain, but non-retryable must not consume them
	if err != nil {
		t.Fatalf("RecordAttemptAndTransition() error = %v", err)
	}
	if !applied {
		t.Fatal("applied = false, want true")
	}

	got, err := es.GetByID(ctx, event.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != StatusDLQ {
		t.Errorf("Status = %q, want %q on the very first non-retryable failure", got.Status, StatusDLQ)
	}
	if got.AttemptCount != 1 {
		t.Errorf("AttemptCount = %d, want 1", got.AttemptCount)
	}
}

// TestEventStore_RecordAttemptAndTransition_MaxAttemptsOneNeverEntersRetryWait
// covers the boundary the 3-iteration exhaustion test above doesn't reach:
// with only one attempt allowed, the first (and only) retryable failure must
// go straight to DLQ without ever passing through RETRY_WAIT.
func TestEventStore_RecordAttemptAndTransition_MaxAttemptsOneNeverEntersRetryWait(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)
	es := NewEventStore(db)

	target := createTestTarget(t, ctx, ts)
	event, _, err := es.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("CreateWithOutbox() error = %v", err)
	}

	applied, err := es.RecordAttemptAndTransition(ctx, event.ID, AttemptResult{
		Outcome: OutcomeRetryableFailure, StartedAt: time.Now(), FinishedAt: time.Now(),
	}, 1, fixedDelay(time.Hour)) // a huge delay would be obviously wrong if RETRY_WAIT were ever entered
	if err != nil {
		t.Fatalf("RecordAttemptAndTransition() error = %v", err)
	}
	if !applied {
		t.Fatal("applied = false, want true")
	}

	got, err := es.GetByID(ctx, event.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != StatusDLQ {
		t.Errorf("Status = %q, want %q when MaxAttempts=1 is reached on attempt 1", got.Status, StatusDLQ)
	}
	if got.NextAttemptAt != nil {
		t.Errorf("NextAttemptAt = %v, want nil (must never schedule a retry past MaxAttempts)", got.NextAttemptAt)
	}
}

func TestEventStore_ReplayFromDLQ_ResetsForFreshAttempt(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)
	es := NewEventStore(db)

	target := createTestTarget(t, ctx, ts)
	event, _, err := es.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("CreateWithOutbox() error = %v", err)
	}

	// Drive it to DLQ via a non-retryable failure.
	if _, err := es.RecordAttemptAndTransition(ctx, event.ID, AttemptResult{
		Outcome: OutcomeNonRetryableFailure, StartedAt: time.Now(), FinishedAt: time.Now(),
	}, 5, fixedDelay(time.Second)); err != nil {
		t.Fatalf("RecordAttemptAndTransition() error = %v", err)
	}
	before, err := es.GetByID(ctx, event.ID)
	if err != nil || before.Status != StatusDLQ {
		t.Fatalf("setup: Status = %q, err = %v, want DLQ", before.Status, err)
	}

	applied, err := es.ReplayFromDLQ(ctx, event.ID)
	if err != nil {
		t.Fatalf("ReplayFromDLQ() error = %v", err)
	}
	if !applied {
		t.Fatal("applied = false, want true for a DLQ event")
	}

	after, err := es.GetByID(ctx, event.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if after.Status != StatusRetryWait {
		t.Errorf("Status = %q, want RETRY_WAIT", after.Status)
	}
	if after.AttemptCount != 0 {
		t.Errorf("AttemptCount = %d, want 0 (replay gives a fresh budget)", after.AttemptCount)
	}
	if after.NextAttemptAt == nil || after.NextAttemptAt.After(time.Now()) {
		t.Errorf("NextAttemptAt = %v, want due immediately", after.NextAttemptAt)
	}
	if after.CompletedAt != nil {
		t.Error("CompletedAt should be cleared on replay")
	}

	// History must survive the reset.
	attempts, err := es.ListAttempts(ctx, event.ID, 10)
	if err != nil {
		t.Fatalf("ListAttempts() error = %v", err)
	}
	if len(attempts) != 1 {
		t.Errorf("len(attempts) = %d, want 1 (the original DLQ'ing attempt must remain)", len(attempts))
	}
}

func TestEventStore_ReplayFromDLQ_NotEligibleWhenNotDLQ(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)
	es := NewEventStore(db)

	target := createTestTarget(t, ctx, ts)
	event, _, err := es.CreateWithOutbox(ctx, target.ID, "key-1", []byte(`{}`), "hash")
	if err != nil {
		t.Fatalf("CreateWithOutbox() error = %v", err)
	}
	// event is PENDING, not DLQ.

	applied, err := es.ReplayFromDLQ(ctx, event.ID)
	if err != nil {
		t.Fatalf("ReplayFromDLQ() error = %v", err)
	}
	if applied {
		t.Error("applied = true for a non-DLQ event, want false")
	}

	got, err := es.GetByID(ctx, event.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != StatusPending {
		t.Errorf("Status = %q, want unchanged PENDING", got.Status)
	}
}
