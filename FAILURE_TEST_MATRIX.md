# Failure Test Matrix

This is the executable-coverage companion to `FAILURE_MODES.md`. A listed Go
test is deterministic and is exercised by `go test ./...` when its required
local dependencies are available. Procedures are deliberately not reported as
passed until their output is captured in `RESUME_EVIDENCE.md`.

| Failure / behavior | Evidence |
|---|---|
| 2xx, 4xx, 5xx delivery classification | `internal/delivery/client_test.go`; `internal/worker/pool_test.go` |
| Retryable 408, 429, 500, 503 and network errors | `internal/retry/policy_test.go`; `internal/delivery/client_test.go` |
| Timeout, connection reset, latency, temporary failures | `internal/targetsim/handler_test.go`; target-simulator routes |
| Durable create, outbox atomicity, duplicate idempotency | `internal/store/events_test.go` |
| Concurrent idempotency submission | `internal/store/events_test.go` |
| Duplicate queue work after success | `TestPool_Handle_DuplicateMessageAfterSuccessIsNoop` |
| Worker crash / abandoned pending message recovery | `TestPool_ReclaimStale_RecoversMessageAbandonedByDeadConsumer` |
| Database read or post-HTTP persistence failure preserves pending work until recovery | `TestPool_ProcessMessage_DatabaseFailureStaysPendingUntilRecovery` (isolated PostgreSQL schema and Redis stream; includes reclaim failure and stable outbound key) |
| Relay publication and no repeat after acknowledgement | `TestRelay_PublishPending_PublishesAndMarksPublished` |
| Retry scheduling rollback, concurrent schedulers, and batch/eligibility bounds | `internal/store/schedule_retries_test.go` |
| Scheduler exit before publication, Redis publish failure, and duplicate relay publication | `TestScheduler_RequeueDue_SchedulesOutboxWithoutRedis`; `TestRelay_RetrySurvivesSchedulerExitPublishFailureAndDuplicatePublication` (simulated crash boundaries, isolated real PostgreSQL/Redis, and real HTTP delivery) |
| Circuit open, half-open successful probe, failed probe | `internal/store/circuit_test.go`; `internal/worker/pool_test.go` |
| Retry budget exhaustion, DLQ and replay | `internal/worker/pool_test.go`; `internal/store/record_attempt_test.go` |
| Invalid URL, private address, redirect policy | `internal/outbound/policy_test.go`; `internal/delivery/client_test.go` |
| Payload bounds, API auth, event idempotency | `internal/api/api_test.go` |

## Current release regression coverage (2026-10-17)

The release-hardening record distinguishes executed deterministic boundaries
from historical process-kill/container-outage timing runs.

| Failure-mode rows covered | Executed evidence |
|---|---|
| 2xx, 400/401/403/404, 408/429, 500/503 | TestPool_HTTPFailureMatrixPersistsExpectedState: real simulator HTTP with durable status/attempt checks |
| DNS failure | TestClient_DNSFailureIsRetryableNetworkClass: deterministic resolver error |
| TCP connect timeout | TestClient_ConnectTimeoutIsNetworkFailure: injected dial boundary, not external packet-loss timing |
| Request timeout, reset, malformed response | Worker failure matrix; TestHandleHang_PostBodyDoesNotStrandCanceledHandler; TestClient_MalformedHTTPResponseIsBoundedNetworkFailure |
| Worker abandonment before HTTP; expired ownership | TestPool_ReclaimStale_RecoversMessageAbandonedByDeadConsumer; TestDeliveryLease_FencesExpiredOwners |
| HTTP success before database result | TestPool_ProcessMessage_DatabaseFailureStaysPendingUntilRecovery: real HTTP, failed persistence, unchanged outbound key |
| DB success before ack; relay publish before marker | TestRelay_RetrySurvivesSchedulerExitPublishFailureAndDuplicatePublication; terminal duplicate no-op tests |
| Redis unavailable or completely lost work | Failed relay publish test plus TestPool_RecoversLostTransportAndRecreatesGroup: deletes only test stream, recreates group in-process |
| PostgreSQL unavailable; no false acceptance | TestDatabaseOutageReturnsSafeErrorsAndKeepsLiveness; worker database-failure regression; transactional creation rollback tests |
| Circuit OPEN, successful/failed/abandoned probe, stale result | Store/worker circuit tests; TestCircuitProbeLease_ExpiredProbeRecoversAndLateOutcomeIsIgnored |
| Retry budget, DLQ, replay | RecordAttemptAndTransition tests and TestE2E_DLQReplayRetainsHistory |
| Concurrent duplicate key and queue work | TestE2E_ConcurrentIdempotencyCreatesOneLogicalEvent; TestPool_ConcurrentDuplicateMessagesShareDeliveryLease |
| Oversized/malformed input, invalid URL, private addresses, redirects | API request bounds/UUID tests, outbound policy tests, delivery redirect and mapped-address tests |
| Shutdown and reclaim scan bounds | TestWaitBackgroundHonorsCancellation; TestPool_ReclaimCursorAdvancesPastFreshMessages |
| Signing integrity and bad secrets | Signature tampering tests, TestClient_CorruptSecretFailsBeforeHTTP, and TestE2E_SignedDeliveryPassesReceiverVerification |

These tests execute against isolated real dependencies where needed. They
do not remeasure outage durations or prove a timed process kill happened in
a microsecond window. Run the operational rehearsal separately before
claiming final deployment/recovery timing.

## Historical manual procedures (older implementation)

Run against the live Docker Compose stack (Postgres + Redis + target-simulator)
plus the server binary, not just `go test`. Commands, timestamps, and output
for every "done" item below are recorded in `RESUME_EVIDENCE.md`.

- [x] **Redis interruption** - create an event, stop Redis, verify the durable
  event/outbox record remains and the API stays healthy, restart Redis, and
  verify relay/worker progress resumes. Done 2026-10-16: ~2.5s recovery.
- [x] **PostgreSQL interruption** - stop PostgreSQL, submit an event, verify
  the API returns a safe failure without a success response, restart
  PostgreSQL, and verify no phantom event exists. Done 2026-10-16: ~2.2s
  recovery, zero phantom rows.
- [x] **Worker crash mid-delivery** - target a slow endpoint
  (`/sim/latency/5000/200`) so a worker is guaranteed in-flight, kill the
  server process, restart it, and verify a stale consumer-group message is
  reclaimed and completed. Done 2026-10-16: 66s recovery (bounded by the
  60s/30s reclaim thresholds).
- [x] **Circuit open -> half-open -> closed, live** - trip a target's circuit
  with 5 failures, confirm a new event is deferred without an attempt while
  OPEN, then confirm automatic recovery via a successful probe. Done
  2026-10-16: circuit opened immediately at threshold, closed 27-31s later
  (matching the 30s cooldown).
- [x] **DLQ + manual replay, live** - exhaust an event's retries against an
  always-failing target, call `POST /events/{id}/retry`, confirm the reset
  (`RETRY_WAIT`, `attempt_count=0`) and that attempt history survives. Done
  2026-10-16.
- [x] **SSRF rejection, live** - with `ALLOW_PRIVATE_TARGETS=false` (the
  production default), confirm a cloud metadata address, a loopback
  address, and an RFC1918 address are all rejected (`422`) while a public
  URL is accepted. Done 2026-10-16.
- [ ] **Crash after target success before durable transition** - send to
  `/sim/idempotent/{name}`, terminate a worker in the (sub-millisecond)
  window between receiving a successful HTTP response and committing the
  durable state transition, restart it, and verify repeated delivery
  preserves the same idempotency key. **Not done**: this window is too
  narrow to hit reliably via external process-kill timing; the
  duplicate-delivery-is-safe property this would exercise is instead
  covered at the unit level (idempotency key stability, row-locked
  terminal-status guard in `RecordAttemptAndTransition`), not via an actual
  timed process kill against the live stack.
- [ ] **Duplicate queue message, live** - covered only by
  `TestPool_Handle_DuplicateMessageAfterSuccessIsNoop`; not separately
  re-run against the live stack this session.
- [ ] **Concurrent idempotency submission, live** - covered only by
  `internal/store/events_test.go`'s concurrent-goroutine test; not
  separately re-run against the live stack this session.
