# Improvement Plan

Follow-on work identified after implementation across all 9 phases,
filtered through this repository's own rules (AGENTS.md): no buzzword
stacking, and every extension gets "a documented reason and acceptance
criteria before implementation" (PROJECT_SPEC.md).

## Current state (2026-10-17)

- Features implemented across IMPLEMENTATION_PLAN.md. Final completion is
  governed by COMPLETION_PLAN.md after the 2026-10-17 correctness review.
  Build/vet/fmt clean; all
  unit tests pass; DB-backed store tests pass against the live stack.
- Measured evidence (RESUME_EVIDENCE.md): 25 req/s sustained, 0 failed
  requests, API p99 68ms; 66s worker-crash recovery; ~2.5s/~2.2s
  Redis/PostgreSQL outage recovery; live circuit-breaker and DLQ replay
  runs; live SSRF rejections.
- Current release status is tracked by COMPLETION_PLAN.md. Dependency-backed
  recovery/concurrency tests and the isolated HTTP runner pass locally. CI
  is configured for race tests, but there is no Git remote/hosted run yet.
  Current image, load, scan, dashboard, restore, and cloud/TLS verification
  remain explicitly open. Historical manual outage timings are not final-code
  measurements.

## Planned improvements (and acceptance criteria)

1. **Admission backpressure on durable pending work** (extension rule:
   documented reason + criteria first). Reason: under outage/overload the
   outbox and retry scheduler keep accepting new events into PostgreSQL;
   unbounded queue growth is the documented weakest spot under burst
   traffic. Acceptance: new `EVENTS_MAX_PENDING_EVENTS` config (0 =
   disabled, the default, preserving current behavior); event creation
   with a non-terminal, non-DLQ count at or above the limit is rejected
   with HTTP 429 and a `Retry-After` header; unit + DB-backed tests.
2. **Event list endpoint** `GET /api/v1/events?status=` for operators:
   acceptance: validate status against the known enum; bounded limit
   (default 50, max 100); OpenAPI updated; DLQ triage becomes possible
   without SQL access.
3. **Black-box e2e acceptance suite** (`test/e2e`, HTTP-only, skipping
   unless `TEST_BASE_URL` is set): create target -> deliver -> SUCCEEDED;
   idempotency duplicate -> 409 with event_id; replay of non-DLQ -> 409.
   Reason: validates the system the way a client sees it.
4. **CI race coverage note**: record in RESUME_EVIDENCE.md that the CI
   `go test -race` job already covers the DB/Redis-backed integration
   tests (TEST_* env vars are set job-wide), so the local Windows gap does
   not mean "no race validation anywhere".
5. **Load ceiling + soak**: new k6 scenarios; only claimable after
   actually running them (RESUME_EVIDENCE.md rule).
6. **docs/walkthrough.md**: self-serve interview prep following one event
   through every state against the bundled simulator.

## Deliberately not done (and why)

- Per-target concurrency caps: correct behavior would require a second
  coordination surface; deferred until a benchmark shows worker
  monopolization actually happens (`webhook_queue_depth` data first).
- Single-VM deployment: no VM/account available in this environment; the
  documented plan exists (DEPLOYMENT_PLAN.md).
- Exactly-once machinery, Kubernetes, multi-service split: against
  project scope rules.
