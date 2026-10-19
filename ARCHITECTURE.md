# Architecture

## Architectural principle

Keep the project as a **modular monolith** with independent internal components rather than turning it into artificial microservices.

Suggested components inside one Go process:

- HTTP API
- service/domain layer
- PostgreSQL repository
- outbox relay
- Redis Streams producer/consumer
- worker pool
- delivery client
- retry scheduler/policy
- circuit breaker
- metrics/observability

## High-level flow

```text
                         ┌─────────────────────────┐
                         │       HTTP Client        │
                         └────────────┬────────────┘
                                      │
                                      ▼
                         ┌─────────────────────────┐
                         │        API Layer        │
                         └────────────┬────────────┘
                                      │ transaction
                                      ▼
                     ┌────────────────────────────────┐
                     │          PostgreSQL            │
                     │  events + attempts + outbox   │
                     └───────────────┬────────────────┘
                                     │
                             outbox relay
                                     │
                                     ▼
                         ┌─────────────────────────┐
                         │     Redis Stream        │
                         │     consumer group      │
                         └────────────┬────────────┘
                                      │
                                  workers
                                      │
                                      ▼
                         ┌─────────────────────────┐
                         │    Delivery Client      │
                         │ timeout/retry/security  │
                         └────────────┬────────────┘
                                      │ HTTPS
                                      ▼
                         ┌─────────────────────────┐
                         │     Target Service      │
                         │   real / test simulator │
                         └─────────────────────────┘
```

## Component responsibilities

### HTTP API

- Validate input.
- Authenticate non-local requests.
- Start PostgreSQL transactions.
- Create targets/events.
- Expose status and manual retry operations.
- Return stable error responses.

### PostgreSQL

Source of truth for:

- target configuration
- event state
- delivery attempt history
- outbox publication state

### Outbox relay

Periodically finds unpublished outbox entries, publishes event IDs/messages to Redis Streams, and records successful publication. It must tolerate duplicate publication because publication is itself at-least-once.

### Retry scheduler

Selects due `RETRY_WAIT` events using row locks and `SKIP LOCKED`, then changes
them to `QUEUED` and inserts their outbox rows in one atomic PostgreSQL
statement. The relay publishes retries through the same path as initial
events. The scheduler does not connect to Redis (Decision 30).

### Redis Streams

Transport for asynchronous work. Use a consumer group so multiple worker goroutines/processes can share delivery work.

### Worker pool

- claim work;
- load event from PostgreSQL;
- validate whether work is still eligible;
- claim an expiring PostgreSQL UUID lease; fence completion with that token;
- enforce worker/global concurrency limits;
- invoke delivery client;
- persist attempt outcome;
- schedule retry or final state transition;
- acknowledge/claim-recover queue work according to the documented protocol.

Consumers and reclamation share the configured HTTP concurrency budget.
An optional WORKER_TARGET_CONCURRENCY cap applies within one process, not
across replicas. Saturated targets are durably deferred without consuming
an attempt, rather than blocking a worker waiting for a target slot. See
Decision 40 for scope and additional scheduling/database costs.
The scheduler reconciles stale unleased PENDING/QUEUED work through the outbox
after a 60s grace period, recovering from lost Redis messages. Half-open
circuit probes expire and carry generations so stale outcomes are ignored.

### Delivery client

Responsible for:

- HTTP request construction
- HMAC signing
- timeouts
- allowed methods/scheme checks
- response classification
- SSRF policy enforcement
- bounded response-body capture

### Circuit breaker

Circuit state is maintained per target. It transitions between closed, open, and half-open according to configurable failure thresholds and cooldown.

### Observability

Use `log/slog` for structured logs and Prometheus for metrics. Grafana is the visualization layer in local Docker Compose.

## Consistency model

PostgreSQL is authoritative. Redis delivery messages are transport hints that cause workers to attempt work. A worker must re-check durable event state before performing a delivery, preventing stale queue messages from blindly triggering a completed event.

## Concurrency principles

- Database state transitions must be guarded against races.
- Idempotency must rely on database uniqueness constraints, not only in-memory maps.
- Worker coordination must tolerate duplicate queue messages.
- Shared mutable state in Go must use clear synchronization or be avoided.

## Technology boundaries

Do not introduce another queue or broker unless a benchmark or correctness requirement proves Redis Streams inadequate.

Do not split components into separate network services until there is a measured reason to do so.
