# Implementation Plan

## Execution rule

Implement one milestone at a time. Do not begin Milestone N+1 until Milestone N's acceptance criteria pass.

## Phase 0 — Repository and environment audit

### Goal

Confirm the development environment and align the repository with the specification.

### Tasks

- Read all source-of-truth documents.
- Run environment checks from `SETUP_PREREQUISITES.md`.
- Create Go module.
- Create base package layout.
- Create `.env.example`.
- Create Docker Compose skeleton.
- Create CI skeleton.

### Acceptance criteria

- Repository builds a minimal Go application.
- Docker Compose parses successfully.
- CI configuration is syntactically valid.
- No secrets exist in the repository.

---

## Phase 1 — Targets and durable event ingestion

### Goal

Create a durable API and PostgreSQL model without asynchronous delivery yet.

### Tasks

- HTTP server and configuration.
- Target CRUD needed by the API contract.
- Event creation transaction.
- Idempotency uniqueness constraint.
- Event status endpoint.
- Attempt history endpoint skeleton.
- Health/readiness endpoints.
- Request IDs and structured logging.

### Acceptance criteria

- Valid event is persisted.
- Duplicate idempotency requests do not create a second logical event.
- Transaction rollback creates no phantom event.
- API returns documented status/error shapes.
- Unit and repository tests pass.

---

## Phase 2 — Outbox + Redis Streams + worker pool

### Goal

Move delivery out of the API request path.

### Tasks

- Outbox table operations.
- Outbox relay.
- Redis Stream + consumer group.
- Worker pool.
- Durable event re-check before delivery.
- Successful delivery path.
- Delivery attempt persistence.

### Acceptance criteria

- API request does not synchronously wait for target delivery.
- Committed event eventually becomes worker work.
- Duplicate queue messages do not create duplicate logical events.
- Successful target response reaches `SUCCEEDED`.
- Attempt history is persisted.

---

## Phase 3 — Retry engine

### Goal

Handle transient failures safely.

### Tasks

- Error classification.
- Exponential backoff with jitter.
- Retry scheduling.
- Attempt limits.
- Retry timestamps.
- Target simulator with deterministic 5xx/timeout responses.

### Acceptance criteria

- Retryable failures retry according to policy.
- Non-retryable failures do not retry indefinitely.
- Retry count is accurate under concurrent workers.
- Exhausted attempts become `DLQ`.

---

## Phase 4 — Idempotency, circuit breaker, and resilience hardening

### Goal

Make partial failures and duplicates explicitly safe.

### Tasks

- Stable outbound idempotency headers.
- Circuit breaker per target.
- Half-open probe behavior.
- Worker crash recovery.
- Duplicate queue delivery handling.
- Manual retry/replay endpoint.
- Per-target concurrency limit only if benchmarks show it is useful.

### Acceptance criteria

- Repeated event delivery uses the same idempotency key.
- Circuit opens and later recovers according to tests.
- Worker termination does not permanently lose an event.
- Manual replay is controlled and auditable.

---

## Phase 5 — Security hardening

### Goal

Make arbitrary outbound HTTP delivery safer.

### Tasks

- SSRF policy.
- Redirect validation.
- Header allowlist.
- Payload limits.
- Response capture limits.
- Authentication middleware for non-local mode.
- Secret redaction.

### Acceptance criteria

- Private/loopback targets are blocked in public mode.
- Unsafe headers are rejected or stripped according to policy.
- Oversized payloads are rejected.
- Secrets never appear in normal logs or responses.

---

## Phase 6 — Observability and operational tooling

### Goal

Make the system explainable under load and failure.

### Tasks

- Prometheus metrics.
- Grafana dashboard.
- Queue depth metrics.
- circuit state metrics.
- structured logs.
- request/event correlation.

### Acceptance criteria

- Every important delivery can be correlated by event ID.
- Queue depth and delivery outcomes are visible.
- Dashboards work from a clean Docker Compose startup.

---

## Phase 7 — Testing, fault injection, and performance evaluation

### Goal

Generate evidence rather than claims.

### Tasks

- Complete integration suite.
- Complete failure matrix coverage.
- Race detector.
- k6 load tests.
- Benchmark scenarios from `BENCHMARK_PLAN.md`.
- Record hardware/configuration.
- Record measured results in `RESUME_EVIDENCE.md`.

### Acceptance criteria

- Critical failure scenarios are automated.
- Load test results are reproducible.
- No fabricated metrics exist anywhere in the repository.

---

## Phase 8 — CI/CD and optional cloud deployment

### Goal

Make the system reproducible outside the local machine.

### Tasks

- GitHub Actions: formatting/static checks/tests/build.
- Build versioned container image.
- Deploy to a single Linux VM using Docker Compose.
- Add TLS/reverse proxy if public access is needed.
- Add cloud secrets through the provider's secret mechanism, not Git.
- Document rollback and restart procedure.

### Acceptance criteria

- A clean machine/VM can deploy from the repository instructions.
- CI blocks regressions.
- Restarting the stack preserves durable event state.

## Phase 9 — Interview/resume polish

### Tasks

- Architecture diagram.
- Failure-mode demo script.
- Benchmark report.
- README quickstart.
- Final resume bullets using only measured results.
- Record key trade-offs in `DECISIONS.md`.

### Acceptance criteria

You can explain:

- why the outbox exists;
- why delivery is at-least-once;
- how duplicates happen;
- why Redis is not the source of truth;
- how circuit breaking works;
- how worker recovery works;
- what the benchmark actually measured.
