package observability

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMetricsHandler_ExposesPrometheusCountersGaugesAndHistograms(t *testing.T) {
	m := NewMetrics()
	m.EventCreated()
	m.TargetCapacityDeferred()
	m.HistoryPruned(3)
	m.HistoryCleanupFailed()
	m.EventRejected()
	m.SetQueueDepth(3)
	m.SetOutboxPending(2)
	m.WorkerStarted()
	m.DeliveryAttempt(false, true, false, 25*time.Millisecond)
	m.CircuitOpen()
	m.SetCircuitCounts(0, 1, 0)
	m.SetQueueUnread(4)
	m.SetDurableCounts(6, 2)
	m.DeliveryStarted()
	m.DeliveryFinished()
	m.ObserveAPI(5 * time.Millisecond)

	recorder := httptest.NewRecorder()
	m.Handler(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := recorder.Body.String()

	for _, want := range []string{
		"webhook_events_created_total 1",
		"webhook_target_capacity_deferrals_total 1",
		"webhook_history_events_pruned_total 3",
		"webhook_history_cleanup_errors_total 1",
		"webhook_events_rejected_total 1",
		"webhook_queue_depth 3",
		"webhook_queue_unread 4",
		"webhook_events_pending 6",
		"webhook_events_dlq 2",
		"webhook_delivery_in_flight 0",
		"webhook_worker_busy_total 1",
		"webhook_outbox_pending 2",
		"webhook_worker_active 1",
		"webhook_delivery_retries_total 1",
		"webhook_circuit_open_total 1",
		"webhook_circuit_state{state=\"OPEN\"} 1",
		"webhook_api_request_duration_seconds_bucket",
		"webhook_delivery_duration_seconds_bucket",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q:\n%s", want, body)
		}
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "text/plain") {
		t.Errorf("Content-Type = %q, want Prometheus text", contentType)
	}
}

func TestHistogramConcurrentScrapesHaveConsistentCounts(t *testing.T) {
	m := NewMetrics()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			m.ObserveAPI(time.Millisecond)
		}
	}()
	defer wg.Wait()
	for i := 0; i < 100; i++ {
		w := httptest.NewRecorder()
		m.Handler(w, httptest.NewRequest("GET", "/metrics", nil))
		var infinity, count uint64
		for _, line := range strings.Split(w.Body.String(), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			if fields[0] == `webhook_api_request_duration_seconds_bucket{le="+Inf"}` {
				infinity, _ = strconv.ParseUint(fields[1], 10, 64)
			}
			if fields[0] == "webhook_api_request_duration_seconds_count" {
				count, _ = strconv.ParseUint(fields[1], 10, 64)
			}
		}
		if infinity != count {
			t.Fatalf("inconsistent histogram: infinity=%d count=%d", infinity, count)
		}
	}
}
