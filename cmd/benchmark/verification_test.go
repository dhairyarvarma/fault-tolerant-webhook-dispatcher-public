package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAcceptedRequests(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        int64
	}{
		{"accepted", `{"root_group":{"checks":{"event accepted":{"passes":12,"fails":2}}}}`, 12},
		{"missing check", `{"metrics":{"checks":{"passes":12}}}`, 0},
		{"empty", `{"root_group":{"checks":{"event accepted":{"passes":0}}}}`, 0},
		{"negative", `{"root_group":{"checks":{"event accepted":{"passes":-1}}}}`, 0},
		{"invalid", `{`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := acceptedRequests([]byte(tc.input))
			if got != tc.want || (err != nil) != (tc.want == 0) {
				t.Fatalf("got %d, %v; want %d", got, err, tc.want)
			}
		})
	}
}

func TestVerifyDelivery(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		report       deliveryReport
		queryErr     bool
		saveErr      bool
	}{
		{"success", "verified", deliveryReport{Accepted: 2, Succeeded: 2}, false, false},
		{"unfinished", "drain_timeout", deliveryReport{Accepted: 2, Succeeded: 1, Pending: 1}, false, false},
		{"dead letter", "terminal_failures", deliveryReport{Accepted: 2, Succeeded: 1, DLQ: 1}, false, false},
		{"canceled", "terminal_failures", deliveryReport{Accepted: 2, Succeeded: 1, Canceled: 1}, false, false},
		{"wrong schema", "count_mismatch", deliveryReport{}, false, false},
		{"partial counts", "count_mismatch", deliveryReport{Accepted: 1, Succeeded: 1}, false, false},
		{"query error", "query_failed", deliveryReport{}, true, false},
		{"save error", "artifact_failed", deliveryReport{Accepted: 2, Succeeded: 2}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := false
			status, err := verifyDelivery(t.Context(), 0, 2, func(context.Context) (deliveryReport, error) {
				if tc.queryErr {
					return deliveryReport{}, errors.New("sensitive database diagnostic")
				}
				return tc.report, nil
			}, func(report deliveryReport) error {
				saved = true
				if report != tc.report {
					t.Fatal("snapshot changed")
				}
				if tc.saveErr {
					return errors.New("write failed")
				}
				return nil
			})
			if status != tc.status || (err == nil) != (tc.status == "verified") || saved == tc.queryErr {
				t.Fatalf("status=%s err=%v saved=%t", status, err, saved)
			}
			if tc.queryErr && err.Error() != "delivery query failed" {
				t.Fatal("database diagnostics were exposed")
			}
		})
	}
}

func TestVerifyDeliveryHonorsCancellationAndQueryDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	status, err := verifyDelivery(ctx, time.Minute, 1, func(ctx context.Context) (deliveryReport, error) {
		return deliveryReport{}, ctx.Err()
	}, func(deliveryReport) error { t.Fatal("unexpected snapshot"); return nil })
	if status != "canceled" || !errors.Is(err, context.Canceled) {
		t.Fatalf("status=%s err=%v", status, err)
	}
	status, err = verifyDelivery(t.Context(), 10*time.Millisecond, 1, func(ctx context.Context) (deliveryReport, error) {
		<-ctx.Done()
		return deliveryReport{}, ctx.Err()
	}, func(deliveryReport) error { t.Fatal("unexpected snapshot"); return nil })
	if status != "drain_timeout" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("status=%s err=%v", status, err)
	}
}

func TestVerifyDeliveryWaitsForSuccess(t *testing.T) {
	calls, snapshots := 0, 0
	status, err := verifyDelivery(t.Context(), 3*time.Second, 1, func(context.Context) (deliveryReport, error) {
		calls++
		if calls == 1 {
			return deliveryReport{Accepted: 1, Pending: 1}, nil
		}
		return deliveryReport{Accepted: 1, Succeeded: 1}, nil
	}, func(deliveryReport) error { snapshots++; return nil })
	if status != "verified" || err != nil || calls != 2 || snapshots != 2 {
		t.Fatalf("status=%s err=%v calls=%d snapshots=%d", status, err, calls, snapshots)
	}
}

func TestVerifyDeliveryCancellationDuringDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	status, err := verifyDelivery(ctx, time.Minute, 1, func(context.Context) (deliveryReport, error) {
		return deliveryReport{Accepted: 1, Pending: 1}, nil
	}, func(deliveryReport) error { cancel(); return nil })
	if status != "canceled" || !errors.Is(err, context.Canceled) {
		t.Fatalf("status=%s err=%v", status, err)
	}
}
