# Interview guide and five-minute demo

## One-minute project explanation

This is a Go modular monolith for asynchronous outbound webhooks. PostgreSQL
owns event state, attempts, circuit state, delivery leases, and publication
intent. Redis Streams distributes work. The core reliability work is making
every crash window explicit: transactional outbox acceptance/retries,
acknowledgement only after safe processing, expiring token-fenced ownership,
and reconciliation after transport loss. Stable idempotency keys cover
the HTTP-success-before-database-commit duplicate window at the receiver.

## Five-minute demo

Start with `go run ./cmd/acceptance -demo` after setting the two dependency
test URLs. This narrated, assertion-backed demo runs on a disposable API and
uses shortened retry/circuit timings. Pause the recording to explain each
step; do not present these demo timings as production recovery measurements.

1. Show the architecture diagram and the event/outbox transaction.
2. Create a target/event, poll SUCCEEDED, and show recorded attempt history.
3. Resubmit the same key and show 409 with the same event ID. Explain that
   this prevents duplicate logical acceptance, not arbitrary network effects.
4. Use the simulator's flaky endpoint or a 503 to show RETRY_WAIT and jitter;
   use a nonretryable failure to show DLQ and replay with history retained.
5. Show queue pending versus unread, durable pending, and HTTP in-flight.
6. Run go run ./cmd/acceptance to demonstrate public behavior, concurrent
   idempotency, auth/health, DLQ replay, disable, and signature verification.
7. Explain the isolated recovery tests rather than claiming that a fragile
   hand-timed process kill proved every crash window.

Use the failure-demo script for a longer simulator walk-through. Rehearse
on a dedicated stack and check all commands before presenting. Avoid
showing .env, API keys, target secrets, or real payloads while screen-sharing.

## Questions to be able to answer from code

**Why an outbox?** Event acceptance and durable publication intent commit
together. Due retries use the same mechanism. Redis publication is outside
the transaction and may repeat; it cannot silently remove durable intent.
Start at internal/store/events.go and internal/outbox/relay.go.

**What happens after a successful HTTP call and a crash?** The result might
not be committed. The expired lease/pending message is recovered and HTTP
may repeat with the same key. The receiver must persist the key with its
business effect. HMAC authenticates bytes; it does not deduplicate effects.

**Why a delivery token?** A database UUID claim serializes concurrent queue
duplicates. Completion/release verifies the token, preventing a late owner
from changing a newer attempt. The processing deadline expires before the
lease. Transport hints for RETRY_WAIT cannot bypass the due-time scheduler.

**Why reconcile PostgreSQL if Redis already reclaims messages?** Redis
reclamation needs its pending list to exist. Reconciliation recreates lost
transport hints for stale unleased work, excluding terminal/future work and
unpublished outbox rows. A grace period limits repeated hints under backlog.

**What if a half-open probe dies?** Its deadline expires. A new probe claims
a higher generation under the target row lock. Old outcomes are ignored,
including late closed-state requests after a new open transition.

**How is admission bounded?** With the limit enabled, a schema-scoped
transaction advisory lock serializes duplicate lookup, pending count, and
event/outbox insertion. The tradeoff is admission throughput. Instances must
share the configured limit; explicit operator replay is outside this limit.

**What prevents SSRF?** Registration and actual connection validate allowed
addresses, with IPv4-mapped normalization. Redirects are not followed. DNS
dependency errors stay retryable; rejected destinations are configuration
failures. Local private-target mode must be configured explicitly.

**How are signatures verified?** HMAC-SHA256 over timestamp, event ID, and
exact body bytes using the configured secret. The receiver checks the clock
window and uses constant-time comparison before parsing JSON. See
SECURITY_MODEL.md and examples/receiver.

**What do the graphs actually measure?** Redis claimed/unread messages,
durable unfinished/DLQ events, and real HTTP in-flight calls are distinct.
Latency histograms are process observations; API/HTTP latency is not the
same as acceptance-to-completion latency. Historical benchmark results
belong to their recorded commit/configuration.

**Why one service and these tools?** The project needs durable transactions,
asynchronous work sharing, and explicit failure handling. Separate services,
another broker, or a cluster would add coordination without a measured need.

**What did measurement change?** The default SQL pool retained only two idle
connections and had no open cap. A short baseline created 905 PostgreSQL
sessions across load/drain. Keeping 16 idle and capping opens at 32 reduced
that observed count to 20; delivery p95 changed from 12.615s to 0.107s in
the paired local 25/s, 30s runs. A regression checks backend reuse across
two concurrent bursts. The full conditions and shared-machine limitations
are in [the pool experiment](benchmarks/2026-10-17-pool/README.md).
Explain why connection reuse is different from removing durable writes or
circuit locks, and budget the cap across replicas rather than per replica alone.

## Resume wording supported by the current tests

- Built a Go webhook dispatcher with PostgreSQL transactional outbox,
  Redis Streams workers, jittered retries, DLQ/replay, and token-fenced
  delivery ownership; regression-tested transport loss, duplicate work,
  database failures, and abandoned circuit probes.
- Hardened outbound delivery with registration/dial-time SSRF checks,
  encrypted target secrets, timestamped HMAC-SHA256 signatures, bounded
  requests, and authenticated HTTP acceptance tests.
- Added Prometheus/Grafana instrumentation distinguishing durable backlog,
  queue lag, and active HTTP work, plus a reproducible load-evidence runner
  that retains workload inputs, raw k6 output, and run-scoped delivery checks.

Use performance numbers only with the matching retained workload and
implementation. Current raw results are linked in the pool experiment;
historical figures are not a capacity promise for a different configuration.

## Candid limits to mention

External HTTP effects are at-least-once. Admission has a serialization cost.
Reconciliation can add harmless duplicate transport hints under long backlog.
Metrics are sampled/process-local. Optional terminal-history retention ends
the deleted events' audit/idempotency window and does not bound DLQ or Redis
history. Multi-tenant authorization is not implemented. A configured CI/TLS/cloud path is not an observed
successful deployment. See COMPLETION_PLAN.md for remaining release gates.
