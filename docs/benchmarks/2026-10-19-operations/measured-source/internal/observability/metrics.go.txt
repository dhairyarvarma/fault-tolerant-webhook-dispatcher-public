// Package observability provides Prometheus-compatible process metrics.
package observability

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var durationBuckets = [...]time.Duration{
	10 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond,
	250 * time.Millisecond, 500 * time.Millisecond, time.Second,
	2500 * time.Millisecond, 5 * time.Second, 10 * time.Second,
}

type histogram struct {
	mu      sync.Mutex
	count   uint64
	sumNano int64
	buckets [len(durationBuckets)]uint64
}

func (h *histogram) observe(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.count++
	h.sumNano += d.Nanoseconds()
	for i, limit := range durationBuckets {
		if d <= limit {
			h.buckets[i]++
		}
	}
}

// Metrics holds process-local observability state. Counters reset on process
// restart; durable delivery history remains in PostgreSQL.
type Metrics struct {
	eventsCreated  atomic.Uint64
	eventsRejected atomic.Uint64

	deliveryAttempts atomic.Uint64
	deliverySuccess  atomic.Uint64
	deliveryFailure  atomic.Uint64
	deliveryRetries  atomic.Uint64
	deliveryDLQ      atomic.Uint64
	targetDeferrals  atomic.Uint64
	historyPruned    atomic.Uint64
	historyErrors    atomic.Uint64

	queueDepth     atomic.Int64
	outboxPending  atomic.Int64
	workerActive   atomic.Int64
	workerBusy     atomic.Uint64
	circuitOpen    atomic.Uint64
	circuitMu      sync.RWMutex
	circuitCounts  [3]int64
	queueUnread    atomic.Int64
	durablePending atomic.Int64
	durableDLQ     atomic.Int64
	httpActive     atomic.Int64
	postgresErrors atomic.Uint64
	redisErrors    atomic.Uint64

	apiDuration      histogram
	deliveryDuration histogram
	recoveryDuration histogram
	postgresDuration histogram
	redisDuration    histogram
}

func NewMetrics() *Metrics { return &Metrics{} }

func (m *Metrics) TargetCapacityDeferred() { m.targetDeferrals.Add(1) }
func (m *Metrics) HistoryPruned(n uint64)  { m.historyPruned.Add(n) }
func (m *Metrics) HistoryCleanupFailed()   { m.historyErrors.Add(1) }

func (m *Metrics) EventCreated()                   { m.eventsCreated.Add(1) }
func (m *Metrics) EventRejected()                  { m.eventsRejected.Add(1) }
func (m *Metrics) ObserveAPI(d time.Duration)      { m.apiDuration.observe(d) }
func (m *Metrics) SetQueueDepth(n int64)           { m.queueDepth.Store(n) }
func (m *Metrics) SetOutboxPending(n int64)        { m.outboxPending.Store(n) }
func (m *Metrics) WorkerStarted()                  { m.workerActive.Add(1) }
func (m *Metrics) WorkerStopped()                  { m.workerActive.Add(-1) }
func (m *Metrics) CircuitOpen()                    { m.circuitOpen.Add(1) }
func (m *Metrics) ObserveRecovery(d time.Duration) { m.recoveryDuration.observe(d) }

func (m *Metrics) SetCircuitCounts(closed, open, halfOpen int64) {
	m.circuitMu.Lock()
	m.circuitCounts = [3]int64{closed, open, halfOpen}
	m.circuitMu.Unlock()
}
func (m *Metrics) SetQueueUnread(n int64) { m.queueUnread.Store(n) }
func (m *Metrics) SetDurableCounts(pending, dlq int64) {
	m.durablePending.Store(pending)
	m.durableDLQ.Store(dlq)
}
func (m *Metrics) DeliveryStarted()  { m.httpActive.Add(1); m.workerBusy.Add(1) }
func (m *Metrics) DeliveryFinished() { m.httpActive.Add(-1) }
func (m *Metrics) DependencyError(name string) {
	if name == "postgres" {
		m.postgresErrors.Add(1)
	} else if name == "redis" {
		m.redisErrors.Add(1)
	}
}

func (m *Metrics) ObserveDependencyCheck(name string, d time.Duration) {
	if name == "postgres" {
		m.postgresDuration.observe(d)
	} else if name == "redis" {
		m.redisDuration.observe(d)
	}
}

// DeliveryAttempt records a persisted attempt outcome. retry and dlq indicate
// the event's resulting state after that attempt.
func (m *Metrics) DeliveryAttempt(success, retry, dlq bool, duration time.Duration) {
	m.deliveryAttempts.Add(1)
	m.deliveryDuration.observe(duration)
	if success {
		m.deliverySuccess.Add(1)
	} else {
		m.deliveryFailure.Add(1)
	}
	if retry {
		m.deliveryRetries.Add(1)
	}
	if dlq {
		m.deliveryDLQ.Add(1)
	}
}

// Handler exposes the registry in Prometheus' text exposition format.
func (m *Metrics) Handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	writeCounter(w, "webhook_events_created_total", m.eventsCreated.Load())
	writeCounter(w, "webhook_events_rejected_total", m.eventsRejected.Load())
	writeHistogram(w, "webhook_api_request_duration_seconds", &m.apiDuration)
	writeCounter(w, "webhook_delivery_attempts_total", m.deliveryAttempts.Load())
	writeCounter(w, "webhook_delivery_success_total", m.deliverySuccess.Load())
	writeCounter(w, "webhook_delivery_failure_total", m.deliveryFailure.Load())
	writeHistogram(w, "webhook_delivery_duration_seconds", &m.deliveryDuration)
	writeCounter(w, "webhook_delivery_retries_total", m.deliveryRetries.Load())
	writeCounter(w, "webhook_delivery_dlq_total", m.deliveryDLQ.Load())
	writeCounter(w, "webhook_target_capacity_deferrals_total", m.targetDeferrals.Load())
	writeCounter(w, "webhook_history_events_pruned_total", m.historyPruned.Load())
	writeCounter(w, "webhook_history_cleanup_errors_total", m.historyErrors.Load())
	writeGauge(w, "webhook_queue_depth", m.queueDepth.Load())
	writeGauge(w, "webhook_queue_unread", m.queueUnread.Load())
	writeGauge(w, "webhook_events_pending", m.durablePending.Load())
	writeGauge(w, "webhook_events_dlq", m.durableDLQ.Load())
	writeGauge(w, "webhook_delivery_in_flight", m.httpActive.Load())
	fmt.Fprintf(w, "# TYPE webhook_dependency_errors_total counter\nwebhook_dependency_errors_total{dependency=\"postgres\"} %d\nwebhook_dependency_errors_total{dependency=\"redis\"} %d\n", m.postgresErrors.Load(), m.redisErrors.Load())
	writeGauge(w, "webhook_outbox_pending", m.outboxPending.Load())
	writeGauge(w, "webhook_worker_active", m.workerActive.Load())
	writeCounter(w, "webhook_worker_busy_total", m.workerBusy.Load())
	writeCounter(w, "webhook_circuit_open_total", m.circuitOpen.Load())
	m.writeCircuitStates(w)
	writeHistogram(w, "webhook_recovery_duration_seconds", &m.recoveryDuration)
	writeHistogram(w, "webhook_postgres_check_duration_seconds", &m.postgresDuration)
	writeHistogram(w, "webhook_redis_check_duration_seconds", &m.redisDuration)
}

func (m *Metrics) writeCircuitStates(w http.ResponseWriter) {
	m.circuitMu.RLock()
	counts := m.circuitCounts
	m.circuitMu.RUnlock()
	fmt.Fprintln(w, "# TYPE webhook_circuit_state gauge")
	for i, state := range []string{"CLOSED", "OPEN", "HALF_OPEN"} {
		fmt.Fprintf(w, "webhook_circuit_state{state=%q} %d\n", state, counts[i])
	}
}

func writeCounter(w http.ResponseWriter, name string, value uint64) {
	fmt.Fprintf(w, "# TYPE %s counter\n%s %d\n", name, name, value)
}

func writeGauge(w http.ResponseWriter, name string, value int64) {
	fmt.Fprintf(w, "# TYPE %s gauge\n%s %d\n", name, name, value)
}

func writeHistogram(w http.ResponseWriter, name string, h *histogram) {
	h.mu.Lock()
	buckets, count, sum := h.buckets, h.count, h.sumNano
	h.mu.Unlock()
	fmt.Fprintf(w, "# TYPE %s histogram\n", name)
	for i, limit := range durationBuckets {
		fmt.Fprintf(w, "%s_bucket{le=%q} %d\n", name, strconv.FormatFloat(limit.Seconds(), 'f', -1, 64), buckets[i])
	}
	fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n", name, count)
	fmt.Fprintf(w, "%s_sum %s\n%s_count %d\n", name, strconv.FormatFloat(float64(sum)/float64(time.Second), 'f', -1, 64), name, count)
}
