# Delivery Walkthrough

Self-serve tour of one event's life through the system, using the bundled
simulator. Every state below is real: watch the API, PostgreSQL, and the
metrics endpoint change as you go. Run the stack first
(`docker compose up -d`), then follow with `curl`.

## 0. Assumptions

```bash
export API=http://localhost:8080
export SIM=target-simulator:9090   # address reachable by the Compose API container
```

## 1. Register a target (validation boundary)

```bash
curl -s -X POST $API/api/v1/targets -H "Content-Type: application/json" \
  -d '{"name":"walkthrough","url":"http://'$SIM'/sim/status/200"}'
```

This is where the outbound policy runs: in production mode
(`ALLOW_PRIVATE_TARGETS=false`) a loopback/private URL here is a `422`
before anything is durably stored (SECURITY_MODEL.md, DECISIONS.md #20).
The Compose setup sets the deviation flag so the bundled simulator is
reachable. Save `target_id` from the response as `$TID`.

## 2. Create an event (durable, atomically)

```bash
curl -s -X POST $API/api/v1/targets/$TID/events \
  -H "Content-Type: application/json" -H "Idempotency-Key: walk-1" \
  -d '{"payload":{"type":"walkthrough"}}'
```

Inside one PostgreSQL transaction this writes two rows: the event
(`status=PENDING`) and an outbox message. Nothing reaches Redis yet.
If you set `EVENTS_MAX_PENDING_EVENTS` and the durable pending count is
at/above it, this call returns `429 OVERLOADED` with `Retry-After`
(DECISIONS.md #28) instead.

Repeating the same call with the same Idempotency-Key returns `409` plus
a top-level `event_id` for the original - a second logical event is
never created.

## 3. Outbox relay -> Redis Stream (initial status stays PENDING)

```bash
psql -c "SELECT status FROM events WHERE id = '$EID'"   # PENDING until an attempt/deferral is recorded
```

The relay polls the outbox table (default 500ms), publishes to the
`webhook:deliveries` stream, and marks the outbox row published. Initial
publication does not change the event status. `QUEUED` is used when the
retry scheduler claims due retries. A crash before the outbox publication
marker commits can cause duplicate publication. After complete Redis state
loss, the scheduler recreates hints for stale, unleased unfinished work
after a 60s grace period; future retries and unpublished outbox rows are
excluded. Regression tests cover that path; see `COMPLETION_PLAN.md`.

## 4. Worker delivery (in-flight, not persisted)

A worker reads the message from the consumer group, re-checks the
event's durable status, claims an expiring PostgreSQL UUID lease, checks the
target's circuit permit, then makes the HTTP POST with the stable key.
Configured target secrets produce timestamped HMAC signatures. There is no
DELIVERING status: ownership lives in the lease, and actual HTTP in-flight
count is `webhook_delivery_in_flight`. Redis PEL depth is claimed-but-unacked
transport work, which also includes failures awaiting recovery.

## 5. Record and transition

The attempt is written to `delivery_attempts`, then the event's status
is transitioned under the row's lock (`RecordAttemptAndTransition`).
Watch it land:

```bash
curl -s $API/api/v1/events/$EID
curl -s $API/api/v1/events/$EID/attempts
```

With `/sim/status/200` the status becomes `SUCCEEDED`, `attempt_count=1`.

To watch retries instead: use `/sim/flaky/<name>?fail=5` or
`/sim/status/503`. The worker records a `RETRYABLE_FAILURE` attempt and
moves the event to `RETRY_WAIT` with a full-jitter `next_attempt_at`
(backoff base/max from DELIVERY_* config, DECISIONS.md #16). The retry
scheduler atomically changes due retries to `QUEUED` and inserts outbox
rows. The relay then publishes those rows to Redis. If the process exits
before publication, the retry's outbox row remains available on restart
(DECISIONS.md #30).

To watch the circuit breaker: point 5 separate events (fixed threshold)
at an always-failing target (`/sim/status/500`). On the 5th failure the
per-target breaker stored in PostgreSQL goes `OPEN`
(`GET /api/v1/targets/$TID` shows `circuit_state`); new events against
that target are deferred with zero HTTP attempts until the 30s cooldown
expires and a HALF_OPEN probe succeeds.

## 6. Dead-letter and replay

Against `/sim/status/500` and `DELIVERY_MAX_ATTEMPTS=5`, the event ends
in `DLQ` with a full attempt history. Replay it explicitly:

```bash
curl -s -X POST $API/api/v1/events/$EID/retry
```

`200` with `status=RETRY_WAIT, attempt_count=0` - budget restored, every
original attempt row kept, the same Idempotency-Key is reused on the new
attempts (DECISIONS.md #18-19). Replaying a non-DLQ event returns `409`.

## 7. Operator triage without SQL

```bash
curl -s "$API/api/v1/events?status=DLQ&limit=100"
```

Lists newest events in a given status (DECISIONS-improved list endpoint;
see CONTRACTS in contracts/openapi.yaml for the exact contract).
