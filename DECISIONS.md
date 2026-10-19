# Architecture and Scope Decisions

## Decision 1 — Go

Reason: strong fit for concurrent backend services, explicit concurrency primitives, low runtime overhead, and clear interview value.

## Decision 2 — PostgreSQL as durable source of truth

Reason: relational consistency, transactions, uniqueness constraints, and durable state transitions fit event/idempotency semantics.

## Decision 3 — Redis Streams as work transport

Reason: enough queue/consumer-group functionality for this scope without introducing another broker.

RabbitMQ remains a possible experiment only if a concrete requirement appears; it is not part of the baseline.

## Decision 4 — Modular monolith instead of microservices

Reason: the hard problem is delivery correctness, not service decomposition. Multiple services would increase operational complexity without automatically improving the project.

## Decision 5 — Docker Compose before cloud/Kubernetes

Reason: reproducibility and low setup friction. Kubernetes is intentionally out of scope unless a later experiment proves it necessary.

## Decision 6 — No AI/ML

Reason: this project is intentionally optimized for SWE/backend depth. AI would dilute rather than strengthen the core problem.

## Decision 7 — At-least-once + idempotency, not exactly-once

Reason: exact end-to-end once-only semantics across a worker, network and arbitrary external target cannot be honestly claimed by this architecture. The system should instead make duplicates safe/detectable.

## Decision 8 — Deterministic target simulator

Reason: real third-party endpoints are unreliable as a test oracle. A local simulator gives reproducible failures and measurable recovery behavior.

## Decision 9 — Single-VM cloud deployment

Reason: demonstrates cloud/container deployment without turning the student project into a large platform-engineering effort.

## Decision 10 — Idempotency-Key conflict returns 409 with the existing event_id

`API_SPEC.md` left the response shape for a duplicate `(target_id, Idempotency-Key)` unresolved beyond "reject ... or return the existing event." `contracts/openapi.yaml` already pins the status code to `409`. The implementation returns the standard error envelope plus a top-level `event_id` field referencing the existing event, so a client that retried its own request can still recover the event it created.

Reason: a 409 with no way to find the original event is operationally useless to a caller that lost track of the response to its first attempt.

## Decision 11 — Target secrets encrypted at rest with AES-256-GCM

`DATABASE_DESIGN.md` names the column `secret_ciphertext` without specifying the mechanism. The implementation (`internal/crypto`) encrypts with AES-256-GCM using a required `SECRET_ENCRYPTION_KEY` (base64, 32 bytes) read at startup; `config.Load()` fails fast if it is missing or the wrong length. There is no fallback/generated key, because a key that changes across restarts would silently make existing secrets undecryptable.

Reason: matches `SECURITY_MODEL.md`'s requirement to never store plaintext secrets, without introducing a KMS/external secrets dependency this phase does not need.

## Decision 12 — DELETE target is a soft-disable, not a row delete

`API_SPEC.md` says "according to the final persistence policy" and `DATABASE_DESIGN.md` says not to cascade-delete history. `DELETE /api/v1/targets/{id}` sets `enabled = FALSE` and returns `204`; the row and all referencing events/attempts remain queryable. A disabled target rejects new event creation with `403`.

Reason: preserves delivery history for audit/debugging while still stopping new work from flowing to a decommissioned target.

## Decision 13 — Non-retryable failures go straight to DLQ, not through attempt budget

`FAILURE_MODES.md` says a 400/401/403/404/422 is "non-retryable" but doesn't name the resulting event status. Since nothing will ever retry it, a non-retryable failure transitions the event to `DLQ` on the very first such attempt, regardless of `DELIVERY_MAX_ATTEMPTS` remaining. `RETRYABLE_FAILURE` only moves to `DLQ` once `attempt_count` reaches `DELIVERY_MAX_ATTEMPTS`.

Reason: `RETRY_WAIT` with no scheduled retry would be a silent dead end; `DLQ` is the documented terminal state for "stopped trying," and keeps the event visible/replayable via the DLQ mechanism Phase 4 adds.

## Decision 14 — Retry classification: only 408/429/5xx are retryable; other 4xx are not

`QUEUE_AND_DELIVERY_SEMANTICS.md` lists retryable examples (408, 429, 5xx, network/timeout) and "usually non-retryable" examples (400/401/403/404/422) "unless target policy explicitly overrides." No per-target override mechanism exists yet, so `internal/retry.Classify` treats every other 4xx the same as the named non-retryable codes — there's no policy today to differentiate them.

Reason: matches the spec's default policy exactly; a per-target override is listed as optional/future scope, not required for Phase 3.

## Decision 15 — Retry scheduler reuses `OUTBOX_POLL_INTERVAL`, no new config key

The retry scheduler (`internal/retry.Scheduler`) and outbox relay both poll
PostgreSQL for work. The scheduler reuses `cfg.OutboxPollInterval` rather
than adding a second interval setting. Since Decision 30 the scheduler
queues retries through the outbox and only the relay publishes to Redis.

Reason: avoids config sprawl for two loops with identical cadence needs; split them later only if production tuning actually calls for different intervals.

## Decision 16 — Full jitter backoff, capped at `DELIVERY_MAX_BACKOFF`

`next_attempt_at` is computed as a uniform random duration in `[0, min(BaseBackoff * 2^(attempt-1), MaxBackoff)]` ("full jitter"), not a fixed exponential curve.

Reason: avoids synchronized retry storms when many events fail at once (e.g. a target going down takes all its in-flight events down together); full jitter is the standard mitigation and needs no extra state.

## Decision 17 — Circuit-breaker state is stored with the target

`targets` retains the circuit state, consecutive failure count, and the time
the circuit last opened. The worker locks that row before allowing a delivery
or claiming the sole half-open probe, so the breaker works across worker
goroutines and process restarts.

Reason: an in-memory circuit would forget a failing target on restart and
would not coordinate multiple workers. PostgreSQL is already the durable
source of truth for target configuration and delivery state.

## Decision 18 — A manual replay restores the event's retry budget

Replaying a `DLQ` event preserves its event identity and full attempt history,
but resets `attempt_count` to zero, clears `completed_at`, and schedules it
immediately through the existing retry scheduler. A replay request for any
non-`DLQ` event returns `409`.

Reason: replay is an explicit operator action after the underlying failure has
been addressed; keeping an exhausted attempt count would send the event
straight back to the DLQ without a new delivery opportunity.

## Decision 19 — Outbound retries reuse the persisted Idempotency-Key

The delivery client sends the event's persisted `Idempotency-Key` unchanged on
every attempt. Since an event is unique per `(target_id, idempotency_key)`, the
target receives the same key on retries and duplicate queue deliveries.

Reason: this provides stable target-side duplicate protection while preserving
the system's honest at-least-once delivery semantics.

## Decision 20 — The outbound policy validates at registration and dial time

In public mode, only `http`/`https` targets resolving exclusively to public
addresses are accepted. Loopback, private, link-local, multicast,
unspecified, and carrier-grade NAT addresses are rejected. The delivery
transport resolves and checks the hostname again immediately before dialing.
`ALLOW_PRIVATE_TARGETS=true` is an explicit local-development exception for
the bundled simulator.

Reason: validating only on target creation leaves a DNS-rebinding window;
validating only on delivery allows unsafe target configuration into durable
state. The two checks serve different boundaries.

## Decision 21 — Webhook redirects are not followed

The delivery client returns the initial 3xx response as the attempt result and
does not issue a request to the `Location` target.

Reason: this is a strict, simple redirect policy that prevents redirect-based
SSRF and avoids silently changing a webhook `POST` into a `GET` on 301/302.
Following validated 307/308 redirects can be added later only if a concrete
integration requires it.

## Decision 22 — The initial API accepts no caller-controlled outbound headers

Events contain JSON payload only. The delivery client creates a new outbound
request and sets `Content-Type: application/json`, stable `Idempotency-Key`,
and generated event identity/signature headers (Decision 35); it does not
forward inbound `Authorization`, connection, or other arbitrary caller headers.

Reason: there is no current product requirement for custom headers, so an
empty allowlist is safer and simpler than storing and filtering arbitrary
client input.

## Decision 23 — API-key authentication uses `X-API-Key`

When `AUTH_ENABLED=true`, all API routes require an exact `X-API-Key` value.
`/healthz` and `/readyz` remain unauthenticated for infrastructure probes.
Configuration rejects `AUTH_ENABLED=false` outside the `development`
environment.

Reason: this meets the non-local authentication requirement without adding a
user or identity system outside the project scope.

## Decision 24 — Metrics use a small Prometheus text registry

The service exposes its required process counters, gauges, and latency
histograms from a standard-library registry at `/metrics`, without adding a
Prometheus client dependency. Grafana and Prometheus are provisioned by the
local Compose stack.

Reason: the required metric set is small and fixed. Keeping the exporter local
avoids a dependency solely for instrumentation while preserving Prometheus'
standard scrape format. Durable delivery truth remains in PostgreSQL; process
metrics reset when the API process restarts.

## Decision 25 — Prometheus's default host port moved to 9091

`target-simulator` and `prometheus` both defaulted to host port 9090. This
was never caught earlier because every prior session started services
individually rather than bringing up the full `docker compose up -d` stack
at once; the first time that was actually tried (2026-10-17), Docker
silently dropped Prometheus's port publish instead of erroring, so
Prometheus was running but unreachable from the host. `PROMETHEUS_PORT`
now defaults to 9091; Grafana's datasource is unaffected since it reaches
Prometheus over the Docker network at `prometheus:9090`, not the host port.

Reason: target-simulator's host port is already referenced throughout
`RESUME_EVIDENCE.md`/`FAILURE_TEST_MATRIX.md` as `localhost:9090`, so it
was simpler and less disruptive to move Prometheus's port instead of the
simulator's.

## Decision 26 — CI builds and pushes a SHA-tagged image to GHCR

`.github/workflows/ci.yml` gained a `build-image` job (depends on
`build-and-test` passing) that builds the API image and pushes
`ghcr.io/<repo>:<sha>` and `:latest`, authenticated with the automatic
`GITHUB_TOKEN` - gated to pushes on `main` only, not pull requests.

Reason: GHCR needs no registry account or secret to set up, matching
`SETUP_PREREQUISITES.md`'s "don't create cloud credentials before they're
needed." Tagging by commit SHA (not only `latest`) is what makes the
rollback procedure in `DEPLOYMENT_PLAN.md` possible without rebuilding.

## Decision 27 — No persisted DELIVERING state

`QUEUE_AND_DELIVERY_SEMANTICS.md`'s recommended state list includes
`DELIVERING`. The implementation defined the constant (Phase 3) but never
actually wrote it anywhere - found while polishing the architecture
diagrams for Phase 9, when the diagram's claimed `QUEUED -> DELIVERING ->
SUCCEEDED` transition didn't match any code path. Removed the dead
constant rather than retrofitting real behavior to match the stale
diagram.

Reason: a worker's "in flight" status is already observable without a
durable column - it's exactly the set of messages claimed but not yet
acked in the Redis consumer group's PEL (the same state `XAUTOCLAIM`-based
crash recovery already keys off). Adding a `DELIVERING` write on every
delivery attempt would mean one more UPDATE per attempt and one more state
for `RecordAttemptAndTransition`'s row lock to reason about, for
information already available via `XPENDING` (and already exposed as the
`webhook_queue_depth` metric). The diagrams and this decision record the
deviation rather than silently drifting from the source-of-truth spec, per
`AGENTS.md` rule 8.

Later update: Decision 31 adds a durable UUID delivery lease, while retaining
the same public status enum. Ownership is now represented by that lease;
Redis pending count alone is not proof that an HTTP call is in flight.

## Decision 28 — Admission control on durable pending work

New config `EVENTS_MAX_PENDING_EVENTS` (default `0`, which disables the
limit and preserves existing behavior entirely). When set, creating an
event while the count of non-terminal, non-DLQ rows (PENDING, QUEUED,
RETRY_WAIT) is at or above the bound is rejected with `429` and a
`Retry-After: 5` header (`OVERLOADED` error code). The check happens in
the service layer before any write, so a rejected request leaves no
state.

Reason: under a sustained outage of delivery (e.g. targets slow plus
retry backoff), event creation continues unbounded until the retry
scheduler catches up; `webhook_queue_depth` only measures the Redis PEL,
not durable advance commitments. This is the first of the optional
extensions, added with a documented reason and criteria per
IMPROVEMENTS.md: bounded durable queue growth, cheap (one indexed COUNT
per create with the limit enabled), and off by default so no behavior
changes unless an operator explicitly opts in. The bound is global, not
per-target: the runaway case is total accepted work, not one target's
load.

## Decision 29 — Acknowledge queue work only after a safe processing result

Normal consumers and the reclaimer acknowledge a message only after the
worker has persisted its attempt/deferral, observed an already-terminal
event, or classified a malformed message/nonexistent event as unprocessable.
Database and processing failures leave the message pending for reclaim.

Reason: acknowledging after a failed database read/write removes the only
active transport hint while leaving durable work unfinished. Stable outbound
idempotency keys still protect the expected duplicate HTTP window when a
target succeeds before the result can be committed. See COMPLETION_PLAN.md
for the remaining recovery gates; this decision alone does not close them.

## Decision 30 — Schedule retries through the transactional outbox

The retry scheduler atomically changes due `RETRY_WAIT` events to `QUEUED`
and inserts a new outbox row for each event in one PostgreSQL statement.
`FOR UPDATE SKIP LOCKED` coordinates concurrent schedulers. The scheduler
has no Redis dependency; the existing relay publishes committed retries.

Reason: the previous sequence committed `QUEUED` before publishing to Redis,
so a crash between those operations stranded the event. Now either both the
state change and outbox row commit, or neither does. A relay crash after
publication can still cause duplicate messages, using the same event and
idempotency key. Recovery from loss of already-published Redis state remains
a separate open gate in COMPLETION_PLAN.md.

## Decision 31 — Durable delivery leases and bounded transport reconciliation

Workers claim an eligible PENDING/QUEUED event in PostgreSQL before HTTP.
A UUID token fences completion/release; the lease lasts longer than the
bounded processing context. Active duplicate queue messages do not send
another HTTP request. RETRY_WAIT messages cannot bypass their scheduler.

The scheduler also reconciles up to 100 PENDING/QUEUED events stale for 60s,
with no active lease or unpublished outbox row. Reconciliation atomically
adds an outbox row and refreshes updated_at, limiting repeat publication to
once per grace period. Redis groups are recreated on NOGROUP while running.
This recovers durable work after loss of transport state, without scanning
terminal events or accelerating future retries. Backlogged unclaimed work
may be republished after the grace period; leases and terminal checks protect
processing, while stable keys cover the unavoidable HTTP crash window.

## Decision 32 — Half-open probes expire and circuit outcomes are fenced

Circuit decisions carry a durable generation. Claiming a half-open probe
increments it and sets a deadline longer than the processing context.
Expired/legacy probes can be replaced under the target row lock. Outcomes
with an older generation are ignored, preventing abandoned probes and
in-flight traffic from changing a newer circuit state.

## Decision 33 — Admission checks share the event creation transaction

With a nonzero pending limit, creation takes a schema-scoped PostgreSQL
advisory transaction lock, checks for the existing idempotency key first,
then counts pending work and inserts the event/outbox in the same transaction.
Instances must share the configured limit. This serializes bounded admission;
disabled admission keeps its existing fast path. Explicit DLQ replay is an
operator action and is outside the new-event admission limit.

## Decision 34 — Disabling a target cancels work before its next HTTP attempt

A worker that observes a disabled target cancels its claimed event, retains
history, and records no delivery attempt. Already in-flight HTTP calls cannot
be undone. Disabled targets reject new creation and manual replay. Attempt
history is ordered by start time and ID, because attempt numbers reset on
replay and are not global history sequence numbers.

## Decision 35 — Sign configured targets with timestamped HMAC-SHA256

When a target has an encrypted secret, the delivery client decrypts it in
memory and signs `timestamp + "." + event_id + "." + raw_body_bytes` with
HMAC-SHA256. Headers are X-Webhook-Timestamp (Unix seconds), X-Webhook-Id
(stable event UUID), and X-Webhook-Signature (`v1=` plus lowercase hex).
Retries retain the event/idempotency identity and use a fresh timestamp.
Targets without a configured secret remain unsigned. Decryption/signing
failures stop delivery safely without exposing secrets. The receiver helper
uses constant-time comparison and a configurable clock-tolerance window;
receivers still need durable idempotency to prevent repeated business effects.

## Decision 36 — Metrics separate durable backlog, unread work, and active HTTP

A periodic observer refreshes queue/durable/circuit gauges even when idle.
Circuit-state counts come from PostgreSQL rather than an unbounded map of
target IDs. Queue pending and unread group lag are separate gauges. HTTP
in-flight and started-work counters track actual calls; outcome counters
track committed attempts. Histogram scrapes take consistent snapshots.
The recovery histogram measures successful reclaimed-message processing,
not time since a crash. Dependency errors are fixed-label counters.

## Decision 37 — Public delivery excludes special-purpose and transition ranges

Public mode normalizes IPv4-mapped addresses and conservatively rejects
special-purpose IPv4 blocks, IPv6 protocol/documentation/6to4 ranges, and
IPv6 outside 2000::/3. This deliberately excludes some specialized reachable
addresses, including NAT64 paths, rather than treating IsGlobalUnicast as a
proof of public routability. Local private-target mode remains explicit.
The checked-in policy is reviewed against IANA registries; future allocation
changes require an intentional review rather than runtime network lookups.

## Decision 38 — Bound and reuse PostgreSQL connections

Every pool opened through store.Open allows at most 32 open connections and
retains up to 16 idle connections. The previous database/sql defaults allowed
unlimited open connections but retained only two idle: the regression holding
12 connections in each of two bursts discarded 20 reusable connections.
Retaining warm backends avoids repeated connection/authentication setup; the
open limit makes overload wait inside the existing request context instead of
consuming every database slot. No transaction, lock, or delivery rule changes.

These are per-process budgets, not a cluster-wide reservation. Account for
replicas, benchmark/acceptance clients, migrations and operator connections
against PostgreSQL max_connections. Revisit the constants with measurements
before scaling replicas or substantially changing concurrency. This removes
connection churn; it does not promise to remove WAL or hot-target contention.

## Decision 39 — Benchmark success requires verified delivery when configured

With TEST_DATABASE_URL configured, the runner must reconcile SQL event counts
with k6's named event-accepted check and wait for every accepted event to
succeed. Missing/invalid summaries, database failures, count mismatches,
terminal failures, artifact-write failures and expired drain budgets return
nonzero, retaining final metadata and the last available delivery snapshot.
Errors must not expose database credentials. A zero drain budget performs one
bounded snapshot, not an unbounded wait. Deliberately failing-target runs can
therefore return nonzero even when the observed retry/DLQ behavior is correct.

Without TEST_DATABASE_URL, success is explicitly ingestion_only, not verified
delivery. HTTP/load errors and delivery-verification results remain separate
metadata fields. Historical evidence bundles retain their original semantics.

## Decision 40 — Optional per-process target concurrency isolation

A 2026-10-18 mixed-target run observed healthy delivery p95 increase from
94ms alone to 9.97s alongside an overloaded 500ms successful receiver and
an HTTP-503 receiver. Circuit breaking does not isolate slow successes.
Add WORKER_TARGET_CONCURRENCY (default 0, disabled), bounded by WORKER_COUNT.
An enabled limit caps active processing for one target within one pool.
If full, do not wait holding a worker: token-fenced DeferRetry persists a
1–2 second jittered deferral without an HTTP call or consumed attempt, then
acknowledgement follows the existing durable-processing protocol. The retry
scheduler/outbox make it eligible again. No new queue, dependency or schema.

This is not a distributed target limit, strict FIFO fairness, or a guaranteed
latency SLO. Across N replicas the aggregate limit can reach N times the
configured value. Active-target map entries are removed at zero, bounded by
active workers. The tradeoffs are more deferred work/SQL/publications and
slower draining for a hot target. Keep the default unchanged; compare the
same mixed workload with the opt-in limit before recommending it.

## Decision 41 — Observe committed capacity deferrals without target labels

webhook_target_capacity_deferrals_total counts only committed token-fenced
capacity deferrals, not HTTP attempts, failures or circuit decisions. Repeated
deferrals count again. It resets per process and has no event/target labels.

## Decision 42 — Opt-in history cleanup defines a deduplication window

HISTORY_RETENTION_AGE=0s disables cleanup; enabled age must be >=24h. Delete
only SUCCEEDED/CANCELED events created AND completed before the cutoff,
with their attempts and published outbox rows, atomically. Skip DLQ, pending
work, ownership tokens/leases, locked events and unpublished outbox rows.
Retain targets. Bound parent events per batch and query time; rollback child
deletes on any failure. Child rows per parent are not bounded.

The age is the minimum audit/ingestion-idempotency window. After deletion,
GETs return 404 and a reused key can create another event. This cannot undo
receiver effects. Operators must choose a window longer than replay/audit
obligations and back up before enabling it. DLQ, targets, Redis history and
physical disk reclamation are not automatically bounded. No cleanup will
be run against the user's existing data as part of implementation/testing.
