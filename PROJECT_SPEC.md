# Project Specification

## Working title

Fault-Tolerant Webhook Delivery Engine

## One-line description

A durable, asynchronous webhook delivery backend that handles unreliable downstream services through retries, idempotency, circuit breaking, dead-letter handling, observability, and reproducible failure testing.

## Problem

A webhook sender cannot safely perform synchronous HTTP delivery inside the original API request because downstream services can be slow, unavailable, rate-limited, or intermittently failing. Reliable delivery therefore requires durable state, asynchronous workers, retry scheduling, duplicate protection, and explicit failure handling.

## Target user

A developer or SaaS backend that needs to enqueue outbound HTTP webhook events and later inspect their delivery state.

## Core value proposition

The system decouples event creation from delivery while making delivery behavior explicit, observable, testable, and resilient to partial failure.

## Core product flow

```text
Client
  ↓
Create Event API
  ↓
PostgreSQL transaction
  ├── event record
  └── outbox record
  ↓
Outbox Relay
  ↓
Redis Stream
  ↓
Worker Pool
  ↓
Target Webhook
  ↓
Success / Retry / DLQ
```

## Functional requirements

1. Register and manage webhook targets.
2. Create a webhook event for a target.
3. Persist event state durably.
4. Publish pending work asynchronously.
5. Deliver HTTP requests with configurable timeouts.
6. Record every delivery attempt.
7. Retry retryable failures using exponential backoff and jitter.
8. Respect non-retryable HTTP classes according to the documented policy.
9. Prevent accidental duplicate acceptance when the same idempotency key is reused for a target.
10. Detect repeated target failures with a circuit breaker.
11. Move exhausted deliveries to a dead-letter state.
12. Support explicit retry/replay of eligible dead-letter events.
13. Expose event and attempt status through an API.
14. Expose health/readiness and metrics endpoints.
15. Provide a deterministic target-service simulator for integration and failure tests.

## Non-functional requirements

- Durable event state.
- Asynchronous processing.
- Bounded concurrency.
- Explicit timeouts.
- Structured logging.
- Prometheus-compatible metrics.
- Deterministic local development with Docker Compose.
- Reproducible unit/integration/load tests.
- Clear failure semantics.
- No secret leakage into logs or persistence.

## Delivery semantics

The system guarantees **at-least-once processing/delivery attempts with idempotency protection**. It does not guarantee exactly-once end-to-end delivery.

If the worker sends a request and crashes before recording the success, the event may be retried. The target receives an idempotency key so a target that honors the key can deduplicate the operation.

## Scope boundaries

### In scope

- One backend service process containing API, outbox relay, and worker components as separate internal modules/goroutines.
- PostgreSQL.
- Redis Streams.
- A local target-service simulator.
- Metrics/logging.
- Docker Compose.
- GitHub Actions CI.
- Optional single-VM cloud deployment.

### Explicitly out of scope

- Kubernetes.
- Multi-region deployment.
- Exactly-once distributed transactions.
- Global distributed consensus.
- Full webhook provider marketplace.
- Complex frontend application.
- AI/ML/LLM functionality.
- Multi-service production architecture.
- Billing/subscriptions.
- Enterprise SSO.

## Optional advanced extensions

Only add these after the core is stable and benchmarked:

- per-target concurrency limits;
- delivery priority classes;
- adaptive retry configuration from observed failure rates;
- richer delivery analytics;
- cloud deployment;
- a minimal read-only admin UI.

Each extension must have a documented reason and acceptance criteria before implementation.
