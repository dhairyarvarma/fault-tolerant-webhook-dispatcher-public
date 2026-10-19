package delivery

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

func TestDeliverCapturesOnlySingleBoundedRetryAfter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		want   string
	}{
		{"seconds", []string{"30"}, "30"},
		{"date", []string{"Mon, 19 Oct 2026 12:00:00 GMT"}, "Mon, 19 Oct 2026 12:00:00 GMT"},
		{"missing", nil, ""},
		{"duplicate", []string{"30", "60"}, ""},
		{"oversized", []string{strings.Repeat("9", 129)}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for _, value := range tc.values {
					w.Header().Add("Retry-After", value)
				}
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			defer srv.Close()
			client := NewClient(time.Second, time.Second, 1024, true)
			result := client.Deliver(t.Context(), store.Target{URL: srv.URL}, testEvent())
			if result.RetryAfter != tc.want || result.Success || result.HTTPStatus == nil || *result.HTTPStatus != 429 {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}
