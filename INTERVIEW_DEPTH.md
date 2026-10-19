# Interview Depth Map

For current code-backed answers, the five-minute demo, tested mechanisms,
and resume wording, see docs/interview-guide.md. Historical performance
numbers belong to their recorded commit and require a final-version rerun.

The project should be built so you can answer these questions without relying on the coding assistant.

Every question below is answerable from this repository as it stands - not
hypothetically. Each section ends with where the real answer (code,
decision record, or measured evidence) actually lives.

## Backend

- Why is webhook delivery asynchronous?
- Why isn't Redis the system of record?
- Why is PostgreSQL + outbox used?
- How do you handle concurrent status updates?

> `ARCHITECTURE.md` (consistency model), `DECISIONS.md` #2-3, and
> `internal/store/events.go`'s `RecordAttemptAndTransition` (row-locked via
> `SELECT ... FOR UPDATE` before any status write - that's the concurrency
> answer, not just a design claim).

## Distributed systems

- What happens when a worker crashes?
- What happens if the worker crashes after the target receives the request?
- Why can duplicate delivery happen?
- What does at-least-once mean here?
- What failure occurs between DB commit and Redis publish?

> `FAILURE_MODES.md`, `QUEUE_AND_DELIVERY_SEMANTICS.md`'s crash-scenario
> list, `internal/worker/pool.go`'s `reclaimStale`/`XAUTOCLAIM`, and the
> real measured 66s crash-recovery run in `RESUME_EVIDENCE.md` (not just a
> theoretical answer - it was actually crashed and timed).

## Queues

- Why use consumer groups?
- What happens to a message whose consumer disappears?
- How do you prevent one bad target from starving every worker?

> `internal/worker/pool.go` (`XReadGroup`/`XAUTOCLAIM`), `DECISIONS.md` #17
> (circuit breaker), and the live circuit-breaker demo in
> `scripts/failure-demo.sh` - run it to see a bad target get isolated in
> real time, not just hear the claim.

## Resilience

- How does exponential backoff work?
- Why add jitter?
- When should an HTTP 429 retry?
- What is the circuit-breaker state machine?

> `internal/retry/policy.go` (`NextDelay`, full jitter), `internal/retry/policy_test.go`
> (classification table including 429), `internal/breaker/breaker.go` (the
> pure state machine, unit-tested independent of any database), and
> `DECISIONS.md` #13-16.

## Database

- Why is the idempotency constraint in PostgreSQL rather than only in Redis?
- What transaction contains the event and outbox record?
- How do you prevent duplicate event creation?

> `internal/store/events.go`'s `CreateWithOutbox` (`ON CONFLICT DO NOTHING`
> inside one transaction) and its test
> `TestEventStore_CreateWithOutbox_ConcurrentSameKeyCreatesOneEvent`, which
> races 10 concurrent goroutines at the same idempotency key and asserts
> exactly one event exists afterward.

## Security

- Why is arbitrary webhook delivery an SSRF risk?
- How do redirects complicate SSRF protection?
- What headers can a caller safely control?
- How are webhook secrets protected?

> `SECURITY_MODEL.md`, `internal/outbound/policy.go`, `DECISIONS.md` #20-22,
> and the live SSRF-rejection procedure in `RESUME_EVIDENCE.md` (cloud
> metadata address, loopback, and an RFC1918 address all actually rejected
> against a running public-mode server; a real public URL actually
> accepted).

## Performance

- Where is the bottleneck under burst traffic?
- What does p95 latency represent?
- How would worker count affect throughput?
- When could Redis or PostgreSQL become the limiting factor?

> `RESUME_EVIDENCE.md`'s Results section has real numbers to reason from
> (25 req/s sustained, `webhook_queue_depth` peaking at 29 under a 50 req/s
> burst with `WORKER_COUNT=4` and a 500ms-latency target) - answer from
> what was actually observed, not from first principles alone.

## Cloud

- How would you deploy this to a single VM?
- What survives a process restart?
- What happens if the VM restarts?
- Which state is ephemeral vs durable?

> `DEPLOYMENT_PLAN.md` Stage 3 (deployment steps, documented but not
> executed - no VM was available) and its Recovery Test section (which
> *was* executed, twice, against the full local Compose stack: PostgreSQL's
> named volume persists, Redis has none by design and simply resets its
> consumer group on startup).
