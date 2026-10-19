# Observability

## Logs

Use structured logs with `log/slog`.

Every important log should include safe correlation fields where available:

- request_id
- event_id
- target_id
- worker_id
- attempt_number
- outcome

Never log secrets or full sensitive payloads.

## Metrics

At minimum expose:

### Ingestion

- `webhook_events_created_total`
- `webhook_events_rejected_total`
- `webhook_api_request_duration_seconds`

### Delivery

- `webhook_delivery_attempts_total`
- `webhook_delivery_success_total`
- `webhook_delivery_failure_total`
- `webhook_delivery_duration_seconds`
- `webhook_delivery_retries_total`
- `webhook_delivery_dlq_total`

### Queue

- `webhook_queue_depth`
- `webhook_outbox_pending`
- `webhook_worker_active`
- `webhook_worker_busy_total`

### Resilience

- `webhook_circuit_open_total`
- `webhook_circuit_state`
- `webhook_recovery_duration_seconds`

### Database/dependencies

- DB operation latency
- Redis operation latency
- dependency error counters

Use histograms for latency where appropriate.

`webhook_queue_depth` is the Redis consumer-group pending count, while
`webhook_outbox_pending` is the count of PostgreSQL outbox rows awaiting
publication. `webhook_queue_unread` is consumer-group lag (not yet claimed).
`webhook_events_pending` counts durable nonterminal events, while
`webhook_events_dlq` counts current DLQ rows rather than cumulative outcomes.
`webhook_delivery_in_flight` counts delivery-client invocations in progress,
including destination validation/DNS and HTTP, while
`webhook_worker_busy_total` counts started invocations, including local
configuration failures and uncommitted attempts. These are not wire-request
counters: validation can prevent a request from being sent.
Outcome counters count committed attempts. An observer refreshes gauges every
2 seconds even when consumers are idle; unavailable dependencies leave the
last value and increment fixed-label `webhook_dependency_errors_total`.
`webhook_postgres_check_duration_seconds` and
`webhook_redis_check_duration_seconds` measure observer query batches, not
all application database/Redis operations or end-to-end event latency.

`webhook_circuit_state` counts enabled targets by durable PostgreSQL state,
including HALF_OPEN; it has no per-target ID labels or retained target map.
`webhook_circuit_open_total` retains its historical meaning of blocked
delivery decisions, not distinct circuit transitions. The recovery duration
histogram measures successful reclaimed-message handling through queue ack,
not crash-to-recovery time. Process counters reset on restart. Histogram
scrapes use a consistent count/bucket snapshot under concurrent delivery.
The API duration histogram covers all service HTTP routes, including probes
and scrapes; use run-scoped client measurements for ingestion benchmarks.

## Dashboards

Grafana should eventually show:

1. Throughput and request latency.
2. Delivery success/failure/retry rate.
3. Queue depth over time.
4. Active workers.
5. Circuit state and target failures.
6. DLQ growth.

## What not to claim

Metrics displayed by Grafana are observations from the running system. They are not automatically benchmark results. Benchmark results should be produced by the dedicated benchmark procedure and recorded in `RESUME_EVIDENCE.md`.

## Capacity and retention counters

webhook_target_capacity_deferrals_total counts committed capacity deferrals,
not HTTP calls/retries or distinct events. Failed/stale-token deferrals do not
increment it. It has no target/event labels and resets on process restart.
Compare its rate with durable backlog, HTTP in flight and attempt retry rate.
webhook_history_events_pruned_total counts committed parent events removed;
webhook_history_cleanup_errors_total counts failed cleanup batches. Both are
process-local. The provisioned dashboard includes congestion/retention panels.
Deletion counters are operational observations, not a durable audit archive.
