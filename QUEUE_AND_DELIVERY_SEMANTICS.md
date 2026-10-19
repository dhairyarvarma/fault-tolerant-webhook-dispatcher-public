# Queue and Delivery Semantics

## Required semantics

### Event creation

Creating a webhook event and its outbox entry must happen in **one PostgreSQL transaction**.

If the transaction commits:

- the event exists durably;
- the outbox entry exists durably.

If the transaction rolls back, neither should exist.

## Outbox publishing

The relay publishes committed outbox entries to Redis Streams.

The relay may publish the same event more than once due to a crash between publishing and recording the publication result. Workers must therefore be safe when they receive duplicate queue messages.

## Worker delivery

Before sending HTTP, workers atomically claim PENDING/QUEUED events with an
expiring UUID lease. Completion and circuit deferral verify the token. A
RETRY_WAIT queue hint never bypasses the scheduler/backoff. Processing has a
deadline, and the lease outlasts it; stale claims cannot clear newer claims.
The scheduler reconciles stale unleased work through the outbox after 60s,
in bounded batches, so loss of a published Redis message is recoverable.

With WORKER_TARGET_CONCURRENCY enabled, a full process-local target slot
budget causes a token-fenced RETRY_WAIT deferral of 1–2 seconds with jitter,
without making HTTP or increasing the attempt count. The existing scheduler
and outbox requeue it. A deferral database failure stays pending for reclaim.
This bounds one target per process only; it is not distributed FIFO fairness.

Consumers acknowledge only a committed attempt/deferral, an already-terminal
event, or a malformed message/nonexistent event that cannot be processed.
Transient processing/database errors stay pending for reclaim (Decision 29).

A queue message is not proof that delivery is still needed. The worker loads the durable event and evaluates its current state.

### Example

```text
Queue message arrives
        ↓
Load event from PostgreSQL
        ↓
Already SUCCEEDED?
   ├── yes → no HTTP call; acknowledge/finish queue work
   └── no  → continue
```

## Delivery state machine

Recommended states:

- `PENDING`
- `QUEUED`
- `DELIVERING`
- `RETRY_WAIT`
- `SUCCEEDED`
- `DLQ`
- `CANCELED`

State transitions must be explicit and tested.

## Retry policy

When a retry becomes due, the scheduler atomically changes `RETRY_WAIT` to
`QUEUED` and inserts an outbox row. The existing relay publishes that row;
the scheduler never publishes directly to Redis. A failed outbox insert
rolls back the state change. Restarting before publication leaves durable
work for the relay (Decision 30).

Initial configurable defaults:

- maximum attempts: 5
- exponential backoff
- jitter
- maximum delay cap
- request timeout
- connect timeout

These are defaults to benchmark and tune, not guaranteed production values.

### Retryable examples

- connection refused
- DNS/network failure
- timeout
- HTTP 408
- HTTP 429
- HTTP 5xx

### Usually non-retryable examples

- malformed target URL
- unsupported scheme
- HTTP 400/401/403/404/422 unless target policy explicitly overrides

The implementation must make this policy explicit and test each class.

For retryable HTTP responses, a single valid Retry-After value (delay-seconds
or HTTP date) is a minimum delay, bounded by DELIVERY_MAX_BACKOFF. The actual
delay is max(jittered backoff, bounded hint). Invalid, duplicate, expired or
over-128-byte hints fall back to backoff. An exhausted attempt still reaches
DLQ. The chosen next_attempt_at is committed with the attempt and recovered
through the scheduler/outbox; no worker sleeps for the delay. This is not a
target-wide rate limit. See Decision 43 for clock and cap tradeoffs.

## Idempotency

Every delivery must carry a stable idempotency key derived from the event/target identity.

The same event retry must reuse the same key.

The system must reject duplicate event creation when the same target and idempotency key already refer to an existing event, or return the existing event according to the final API contract.

## Exactly-once warning

Do not write `exactly once`, `exactly-once delivery`, or equivalent claims in documentation.

The correct claim is:

> at-least-once delivery attempts with idempotency protection.

## Circuit breaker

Half-open probes have a durable expiry and generation. Expired probes can
be replaced; a late result cannot mutate a later circuit generation.

Recommended conceptual behavior:

```text
CLOSED
  │ repeated failures over threshold
  ▼
OPEN
  │ cooldown elapsed
  ▼
HALF-OPEN
  │ success ───────────────► CLOSED
  │ failure ───────────────► OPEN
```

The exact thresholds are configurable and should be validated through failure tests.

## Dead-letter behavior

When retry attempts are exhausted, persist `DLQ` state and preserve the full attempt history.

Manual replay must create a new delivery execution while preserving the event identity/history.

## Crash scenarios that must work

1. Worker crashes before making the HTTP request.
2. Worker crashes during the HTTP request.
3. Worker crashes immediately after target success but before recording success.
4. Relay crashes after Redis publish but before marking the outbox row published.
5. Redis temporarily disappears.
6. PostgreSQL temporarily disappears.
7. Target service returns 500 repeatedly.
8. Target service becomes healthy after circuit opening.
