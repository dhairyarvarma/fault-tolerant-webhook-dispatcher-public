# Task Board

Use this as the working checklist. Mark a task complete only after validation.

The phase checkboxes below record feature implementation history. Final
reliability and resume readiness are tracked by the acceptance gates in
[COMPLETION_PLAN.md](COMPLETION_PLAN.md), started 2026-10-17. In particular,
recovery, concurrency, and evidence gaps remain despite completed features.

## Phase 0

- [x] Verify local development tools
- [x] Initialize Go module
- [x] Create repository package layout
- [x] Create Docker Compose skeleton
- [x] Create CI skeleton
- [x] Baseline commit

## Phase 1

- [x] Configuration package
- [x] HTTP server
- [x] Request ID middleware
- [x] Target repository
- [x] Event repository
- [x] Idempotency constraint
- [x] Event transaction + outbox creation
- [x] Health/readiness
- [x] API error envelope

## Phase 2

- [x] Outbox relay
- [x] Redis Streams setup
- [x] Consumer group
- [x] Worker pool
- [x] Delivery client
- [x] Attempt persistence
- [x] Success state transition

## Phase 3

- [x] Retry classifier
- [x] Backoff + jitter
- [x] Retry scheduling
- [x] Attempt budget
- [x] Target simulator failure modes
- [x] DLQ transition

## Phase 4

- [x] Stable idempotency header
- [x] Circuit breaker
- [x] Duplicate queue handling
- [x] Worker crash recovery
- [x] Manual replay
- [ ] Optional per-target concurrency

## Phase 5

- [x] SSRF checks
- [x] Redirect policy
- [x] Header allowlist
- [x] Payload limits
- [x] Response capture limits
- [x] Auth for non-local mode
- [x] Secret redaction

## Phase 6

- [x] Prometheus metrics
- [x] Grafana dashboard
- [x] Structured logs
- [x] Correlation fields

## Phase 7

- [x] Unit tests complete
- [x] Integration tests complete
- [ ] Race tests (blocked: no 64-bit cgo toolchain on this Windows host; see RESUME_EVIDENCE.md)
- [x] Failure-boundary regression matrix (real dependency/HTTP tests and
      deterministic injections mapped in FAILURE_TEST_MATRIX.md; older
      process/container timing results remain historical)
- [x] k6 scenarios
- [x] Benchmark report
- [x] Resume evidence log

## Phase 8

- [x] GitHub Actions complete
- [x] Current container image rebuild and isolated startup (non-root,
      certificates, HTTP acceptance, restore and new delivery verified;
      docs/benchmarks/2026-10-17/README.md)
- [ ] Single-VM deployment (no cloud VM/account available in this environment; documented in DEPLOYMENT_PLAN.md, not executed)
- [x] Restart/recovery validation (verified locally against the full Docker Compose stack, both single-container and full teardown/recreate)
- [ ] Optional TLS/reverse proxy (Caddy template added to deploy/Caddyfile; not live-tested, needs a real public domain)

## Phase 9

- [x] README polished (rewritten as the real project README: quickstart,
      verified against the live stack, architecture, measured results)
- [x] Architecture diagram polished (also fixed two inaccuracies found
      while polishing: a DELIVERING state the code never wrote, and a
      DLQ->QUEUED replay transition that's actually DLQ->RETRY_WAIT; see
      DECISIONS.md #27)
- [x] Failure demo documented (scripts/failure-demo.sh, run end-to-end
      against the live stack - also exposed and fixed a race in the first
      draft's own test design, not a product bug)
- [x] Interview questions reviewed (every question in INTERVIEW_DEPTH.md
      cross-referenced to where the real answer lives in code/decisions/
      measured evidence)
- [x] Resume bullets use only measured values (RESUME_EVIDENCE.md, three
      variants, all reusing only already-measured Phase 7/8 numbers)

## Current release/showcase follow-through — 2026-10-17

- [x] Linux race run and Go vulnerability rescan after security updates.
- [x] Fresh local clone: isolated container acceptance, live scrape and
      dashboard provisioning, matching restore fingerprints and new delivery.
- [x] Current ingestion, 2-minute sustained and slow-receiver runs; all raw
      evidence retained, including the earlier dropped-arrival failure.
- [x] Redis/worker restart observations with explicit timing definitions.
- [x] Output-only timed terminal demo, crash case study and ownership worksheet.
- [x] Controlled connection-pool optimization and ten-minute, 15,000-event
      soak; raw results in docs/benchmarks/2026-10-17-pool/README.md.
- [ ] Wider burst/capacity/failing-target envelopes remain unproven.
- [ ] Independent student walkthrough and personal interview rehearsal.

## Benchmark verification follow-through — 2026-10-18

- [x] Configured SQL verification fails closed on query/setup errors,
      mismatched accepted counts, unfinished draining and terminal failures.
- [x] Ingestion-only runs are explicitly labelled; final metadata and last
      available SQL snapshots are retained on verification failure.
- [x] Deterministic verifier regressions, isolated PostgreSQL verification,
      and native-k6 command-level checks; see the dated validation record.
- [x] Publish the approved private repository on main; hosted test/race/HTTP
      acceptance and image publishing pass at ced4cbe in run 39094548569.
      See docs/validation/2026-10-18-github-publication.md for exact evidence.

## Resume presentation follow-through — 2026-10-18

- [x] README-linked five-minute showcase, evidence-backed resume bullets and
      independent walkthrough feedback checklist.
- [x] Repeat isolated release rehearsal in a clean local clone: acceptance,
      restore, Redis restart and observed-claim SIGKILL recovery pass.
      See docs/validation/2026-10-18-resume-showcase.md for skips and provenance.
- [x] Document public-release privacy disclosures and proposed safe route;
      keep the original private history and benchmark evidence unchanged.
- [x] Owner explicitly approved publishing the separate sanitized public repository.
- [ ] New narrated screen video uploaded and linked (terminal recording exists).
- [ ] Independent human walkthrough and personal live coding/design rehearsal.

## Mixed-target isolation — 2026-10-18

- [x] Reproducible healthy/mixed/healthy load harness using isolated resources,
      per-target acceptance checks, bounded SQL verification and raw artifacts.
- [x] Measure healthy delivery delay behind overloaded slow successful HTTP;
      retain both the uncapped result and expected failing-target observations.
- [x] Opt-in WORKER_TARGET_CONCURRENCY with durable, token-fenced no-attempt
      deferrals, process-local bounded active-target accounting and default off.
- [x] Repeat identical offered workloads with cap 2; retain improved healthy
      latency alongside slower hot-target draining and larger durable backlog.
- [x] Concurrency/deferral/recovery/config regressions; complete Linux race
      suite with real PostgreSQL/Redis, vet/build and signed HTTP acceptance.
      See docs/validation/2026-10-18-target-isolation.md and the benchmark report.
- [x] Two-process sharing/owner-loss recovery and a ten-minute capped soak;
      see docs/benchmarks/2026-10-19-operations/README.md.
- [ ] Repeated randomized trials and simultaneous fresh-database migrations;
      these remain outside the observed two-process steady-state rehearsal.

## Operational follow-through — 2026-10-19

- [x] Committed-capacity-deferral counter and congestion/retention dashboard panels.
- [x] Opt-in atomic terminal-history cleanup, bounded-parent batches/deadline,
      explicit idempotency window and eligibility/rollback/concurrency regressions.
- [x] Redis interruption plus admission rejection/no phantom work, duplicate
      identity at capacity, successful drain and resumed admission.
- [x] Final frozen-source Linux race/real-dependency tests, vet/build and signed
      HTTP acceptance. Earlier failed verification log retained explicitly.
- [x] Evidence/source snapshots, machine/resource samples, provenance, privacy
      redaction and operator safety instructions retained.
