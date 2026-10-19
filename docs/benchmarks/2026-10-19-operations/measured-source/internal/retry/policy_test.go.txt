package retry

import (
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/delivery"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

func statusResult(status int) delivery.Result {
	return delivery.Result{HTTPStatus: &status}
}

func errorResult(class string) delivery.Result {
	return delivery.Result{ErrorClass: &class}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		result delivery.Result
		want   string
	}{
		{"success", delivery.Result{Success: true}, store.OutcomeSuccess},
		{"408 request timeout", statusResult(http.StatusRequestTimeout), store.OutcomeRetryableFailure},
		{"429 too many requests", statusResult(http.StatusTooManyRequests), store.OutcomeRetryableFailure},
		{"500 internal server error", statusResult(http.StatusInternalServerError), store.OutcomeRetryableFailure},
		{"503 service unavailable", statusResult(http.StatusServiceUnavailable), store.OutcomeRetryableFailure},
		{"unknown status outside 5xx", statusResult(600), store.OutcomeNonRetryableFailure},
		{"400 bad request", statusResult(http.StatusBadRequest), store.OutcomeNonRetryableFailure},
		{"401 unauthorized", statusResult(http.StatusUnauthorized), store.OutcomeNonRetryableFailure},
		{"403 forbidden", statusResult(http.StatusForbidden), store.OutcomeNonRetryableFailure},
		{"404 not found", statusResult(http.StatusNotFound), store.OutcomeNonRetryableFailure},
		{"422 unprocessable entity", statusResult(http.StatusUnprocessableEntity), store.OutcomeNonRetryableFailure},
		{"network error", errorResult("network_error"), store.OutcomeRetryableFailure},
		{"invalid request (bad url/scheme)", errorResult("invalid_request"), store.OutcomeNonRetryableFailure},
		{"outbound policy", errorResult("outbound_policy"), store.OutcomeNonRetryableFailure},
		{"signing configuration", errorResult("signing_error"), store.OutcomeNonRetryableFailure},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.result); got != tc.want {
				t.Errorf("Classify(%+v) = %q, want %q", tc.result, got, tc.want)
			}
		})
	}
}

func TestPolicy_NextDelay_BoundedByCap(t *testing.T) {
	p := Policy{BaseBackoff: 100 * time.Millisecond, MaxBackoff: time.Second}

	for attempt := 1; attempt <= 10; attempt++ {
		for i := 0; i < 20; i++ { // sample jitter repeatedly
			d := p.NextDelay(attempt)
			if d < 0 || d > p.MaxBackoff {
				t.Fatalf("NextDelay(%d) = %v, want within [0, %v]", attempt, d, p.MaxBackoff)
			}
		}
	}
}

func TestPolicy_NextDelay_GrowsWithAttemptUntilCap(t *testing.T) {
	p := Policy{BaseBackoff: 10 * time.Millisecond, MaxBackoff: time.Second}

	maxOf := func(attempt int) time.Duration {
		var max time.Duration
		for i := 0; i < 50; i++ {
			if d := p.NextDelay(attempt); d > max {
				max = d
			}
		}
		return max
	}

	early := maxOf(1)
	later := maxOf(4)
	if later <= early {
		t.Errorf("observed max delay did not grow: attempt 1 max=%v, attempt 4 max=%v", early, later)
	}
}

func TestPolicy_NextDelay_HugeAttemptNumberDoesNotOverflowOrPanic(t *testing.T) {
	// BaseBackoff is deliberately large enough (>2.1s) that BaseBackoff<<shift
	// overflows int64 well before shift reaches the attempt numbers exercised
	// here - this is the case a too-small shift cap would have missed.
	p := Policy{BaseBackoff: 10 * time.Second, MaxBackoff: time.Minute}

	for _, attempt := range []int{1_000_000, math.MaxInt, math.MinInt, -1, 0} {
		d := p.NextDelay(attempt)
		if d < 0 || d > p.MaxBackoff {
			t.Errorf("NextDelay(%d) = %v, want within [0, %v]", attempt, d, p.MaxBackoff)
		}
	}
}

func TestPolicy_NextDelay_ZeroMaxBackoffIsZero(t *testing.T) {
	p := Policy{BaseBackoff: time.Second, MaxBackoff: 0}

	if d := p.NextDelay(1); d != 0 {
		t.Errorf("NextDelay(1) = %v, want 0 when MaxBackoff is 0", d)
	}
}

func TestPolicy_NextDelay_ZeroBaseBackoffStaysZero(t *testing.T) {
	// A deliberately configured zero base (e.g. "retry immediately, only
	// jitter growth matters later") must not be silently promoted to
	// MaxBackoff - zero is a legitimate computed value, not only an
	// overflow sentinel.
	p := Policy{BaseBackoff: 0, MaxBackoff: time.Minute}

	for attempt := 1; attempt <= 5; attempt++ {
		if d := p.NextDelay(attempt); d != 0 {
			t.Errorf("NextDelay(%d) = %v, want 0 when BaseBackoff is 0", attempt, d)
		}
	}
}

func TestPolicy_DelayCapDoesNotWrapOnHugeAttempts(t *testing.T) {
	p := Policy{BaseBackoff: time.Nanosecond, MaxBackoff: time.Duration(1<<63 - 1)}
	if got := p.delayCap(1000000); got != p.MaxBackoff {
		t.Fatalf("cap=%v want=%v", got, p.MaxBackoff)
	}
	for i := 0; i < 10; i++ {
		if got := p.NextDelay(1000000); got < 0 || got > p.MaxBackoff {
			t.Fatalf("invalid jitter %v", got)
		}
	}
}
