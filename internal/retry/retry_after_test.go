package retry

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/delivery"
)

func TestRetryAfterDelay(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, value string
		want        time.Duration
	}{
		{"seconds", "12", 12 * time.Second},
		{"whitespace", " 12\t", 12 * time.Second},
		{"zero", "0", 0},
		{"leading zeros", "0012", 12 * time.Second},
		{"capped seconds", "999", time.Minute},
		{"uint64 overflow", strings.Repeat("9", 100), time.Minute},
		{"duration overflow", "9223372036854775807", time.Minute},
		{"date", now.Add(20 * time.Second).Format(http.TimeFormat), 20 * time.Second},
		{"capped date", now.Add(time.Hour).Format(http.TimeFormat), time.Minute},
		{"past date", now.Add(-time.Hour).Format(http.TimeFormat), 0},
		{"current date", now.Format(http.TimeFormat), 0},
		{"empty", "", 0},
		{"negative", "-10", 0},
		{"signed", "+10", 0},
		{"fraction", "1.5", 0},
		{"combined values", "10, 20", 0},
		{"invalid", "tomorrow", 0},
		{"oversized", strings.Repeat("9", 129), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryAfterDelay(tc.value, now, time.Minute); got != tc.want {
				t.Fatalf("delay = %v, want %v", got, tc.want)
			}
		})
	}
	for _, cap := range []time.Duration{0, -time.Second, time.Millisecond, 1500 * time.Millisecond} {
		want := max(cap, 0)
		if got := retryAfterDelay("2", now, cap); got != want {
			t.Fatalf("cap %v: delay %v, want %v", cap, got, want)
		}
	}
}

func TestPolicyNextDelayFor(t *testing.T) {
	p := Policy{BaseBackoff: time.Second, MaxBackoff: time.Minute}
	for _, status := range []int{408, 429, 500, 503} {
		result := delivery.Result{HTTPStatus: &status, RetryAfter: "30", FinishedAt: time.Now()}
		if got := p.NextDelayFor(1, result); got != 30*time.Second {
			t.Fatalf("status %d: delay = %v", status, got)
		}
	}
	for _, status := range []int{200, 301, 400, 401, 404, 600} {
		result := delivery.Result{HTTPStatus: &status, RetryAfter: "30", FinishedAt: time.Now()}
		if got := p.NextDelayFor(1, result); got < 0 || got > time.Second {
			t.Fatalf("non-retryable status %d honored hint: %v", status, got)
		}
	}
	status := 429
	for i := 0; i < 100; i++ {
		// A zero hint must not override the jittered exponential delay.
		if got := p.NextDelayFor(1, delivery.Result{HTTPStatus: &status, RetryAfter: "0"}); got < 0 || got > time.Second {
			t.Fatalf("invalid jitter with zero hint: %v", got)
		}
	}
	p.MaxBackoff = 0
	if got := p.NextDelayFor(1, delivery.Result{HTTPStatus: &status, RetryAfter: "30"}); got != 0 {
		t.Fatalf("zero cap bypassed: %v", got)
	}
}
