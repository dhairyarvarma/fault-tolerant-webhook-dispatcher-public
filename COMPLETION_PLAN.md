# Completion plan: reliability, evidence, and interview readiness

Baseline reviewed: `49808b2`, 2026-10-17. This plan supersedes broad
"feature-complete" wording in older planning notes. Existing features and
historical measurements remain useful; completion requires the gates below.

## Working rules

- Keep the Go modular monolith, PostgreSQL, Redis Streams, and Compose.
- Preserve at-least-once semantics and stable target-side idempotency keys.
- Fix a demonstrated risk, add a regression test, validate, then commit a
  reviewable change. Update decisions/contracts before semantic changes.
- Use isolated test resources. Never truncate the user's database or use a
  shared Redis flush as a fault injection shortcut.
- Record tested commit, environment, commands, raw outputs, and limitations.
  A configured CI job is not evidence that a CI run passed.
- No cloud spending, publishing, or destructive operations are implied by
  this plan. Prepare deployment locally before any external deployment.

## 1. Close recovery holes (first priority)

1. Make worker processing report success/failure to both the normal consumer
   and reclaimer. Acknowledge only terminal/no-op work or a committed result
   or deferral. Keep transient failures pending. Explicitly handle malformed
   messages and deleted/nonexistent events so poison messages cannot loop.
2. Route scheduled retries through a durable publication transaction instead
   of committing QUEUED and then relying on an unprotected Redis publish.
   Verify crashes before/after publication and duplicate publications.
3. Design recovery for accepted unfinished work whose Redis message is lost.
   Use durable ownership/expiry or bounded reconciliation; prevent active
   work and future retries from being blindly republished. Recreate missing
   stream groups on dependency recovery, not only process startup.
4. Add an expiring half-open probe claim. A crashed probe must release its
   slot eventually, and an old probe must not overwrite a newer claim.
5. Bound worker shutdown, wait for background tasks, and prevent queued batch
   messages from being reclaimed while still legitimately active. Validate
   reclaimer pagination and timeout/configuration relationships.

Acceptance: deterministic regression tests show no stranded accepted work
after database failures, scheduler crashes, lost transport state, or abandoned
probes; terminal results never regress and duplicates keep their key.

## 2. Prove concurrency and operator semantics

1. Make admission counting/reservation atomic with insertion when enabled;
   duplicate keys must retain their documented behavior under overload.
2. Test concurrent duplicate messages, concurrent idempotency requests,
   replay versus in-flight results, and circuit outcomes across workers.
3. Define soft-disable behavior for already accepted events and enforce it.
4. Verify replay history ordering and identifiers after a reset attempt
   budget; validate malformed IDs, pagination bounds, and all status filters.

Acceptance: parallel tests prove the configured bound and legal transitions;
OpenAPI, API_SPEC, decisions, and tests agree on every edge case.

## 3. Complete security and runtime hardening

1. Implement the architecture's HMAC signing requirement with a documented
   timestamp/signature format, exact signed bytes, verifier example, and
   tests. Reuse encrypted target secrets and never expose plaintext.
2. Review SSRF address normalization, DNS changes, redirects, TLS, body/error
   bounds, authentication boundaries, and secret redaction.
3. Validate all durations, counts, and booleans; reject unsafe configuration.
   Add inbound HTTP timeouts and safe deployment defaults.
4. Verify container certificates, runtime user, build reproducibility, and
   dependency vulnerability checks. Explain any unresolved result.

Acceptance: signature verification and negative security tests pass;
production configuration fails closed and operational instructions work.

## 4. Make observability accurately describe the system

1. Distinguish unread queue backlog, claimed messages, durable pending work,
   and active HTTP deliveries. Refresh gauges even when traffic is idle.
2. Report breaker transitions/probe state correctly; bound retained metric
   state. Define recovery duration and attempt counts precisely.
3. Check concurrent histogram snapshots, units, label cardinality, error
   visibility, and request/event/attempt/worker correlation.
4. Exercise Prometheus scraping and every Grafana query from a clean Compose
   setup. Capture screenshots or API responses as evidence.

Acceptance: dashboards agree with known database/Redis fixtures and explain
outage/backlog behavior without implying benchmark guarantees.

## 5. Automate release acceptance

1. Add deterministic fault tests for Gate 1 windows; avoid timing-dependent
   process kills when a controlled failure can exercise the same boundary.
2. Run the HTTP-only acceptance suite in CI against the built application,
   including delivery, duplicate acceptance, replay, auth, and disable.
3. Run unit/integration/race tests on supported Linux with dependencies;
   record a successful run URL and commit when available.
4. Map every FAILURE_MODES row to a named automated test or an exact manual
   procedure, with executed/unexecuted status. Correct blanket completion
   checkboxes and distinguish skipped tests from executed tests.

Acceptance: formatting, vet, build, unit, integration, race, and acceptance
gates pass for the same commit; remaining manual gaps are explicit.

## 6. Produce reproducible performance evidence

1. Add a runner that records versions, machine/container limits, configuration,
   payloads, target behavior, run ID, timestamps, commit, and raw k6 summaries.
2. Repeat steady, burst, slow/failing-target, ceiling, and soak runs. Capture
   ingestion and delivery rates separately, dropped arrivals, errors, API
   and end-to-end percentiles, durable backlog, CPU, and memory.
3. Repeat crash/dependency recovery with start/end definitions. Preserve
   raw samples and SQL queries; distinguish outage from permanent state loss.
4. State the tested envelope and bottleneck. A 200/s arrival stage is not
   proof of a maximum throughput or a zero-loss guarantee.

Acceptance: another developer can rerun the commands; report numbers trace
to artifacts from the final implementation. Historical runs stay labelled
with their original commit and conditions.

## 7. Finish deployment and operations

1. Verify clean-machine Compose startup, health checks, migration behavior,
   persistent storage, backup/restore, graceful restart, and image rollback.
2. Document production overrides: auth, private-target policy, TLS, exposed
   ports, secret injection/rotation, logging, and resource limits.
3. Prepare a single-VM runbook and optional TLS configuration. Execute on a
   real VM only when a host/account and deployment authorization are available.
   Local readiness and an executed cloud deployment are separate claims.

Acceptance: a fresh local installation passes the demo and acceptance suite;
backup restore and rollback retain event history. External gaps stay explicit.

## 8. Package a defensible resume and interview demo

1. Rewrite README, walkthrough, diagrams, task board, improvement notes, and
   contracts around actual behavior, including limitations and tradeoffs.
2. Prepare a 5-minute demo: accept event, deliver, duplicate conflict, failure
   and retry, circuit recovery, DLQ/replay, metrics, and one recovery case.
3. Prepare interview explanations tied to code: transaction boundaries,
   duplicate windows, acknowledgement, leases, retry policy, SSRF/HMAC,
   admission tradeoffs, bottlenecks, and why this remains one service.
4. Write two or three concise resume bullets using only retained evidence;
   include conditions for performance/recovery numbers. Remove unsupported
   "fully reproduced", production-ready, and reliability claims.

Acceptance: every public claim has a code/test/evidence reference; the demo
works from documented commands and limitations can be explained candidly.

## Progress

- [x] Repository review and completion plan.
- [x] Gate 1, step 1: acknowledgement fix, malformed-message handling, and
  isolated real PostgreSQL/Redis regression tests for read/write failures.
  Both normal processing and reclaim preserve failed work, then recover it
  with the original outbound key (verified 2026-10-17).
- [x] Gate 1, step 2: due retries and outbox rows commit atomically; the
  scheduler no longer publishes to Redis. Isolated integration tests cover
  batch rollback, concurrent scheduling, delayed publication/restart, Redis
  publish failure, and duplicate publication through a real HTTP worker.
  Already-published transport-state loss is still covered by step 3.
- [x] Gate 1: recovery correctness (isolated dependency-backed regression
  suite passes; current Redis/worker timing observations are retained).
- [x] Gate 1, steps 3–4: expiring delivery claims, token-fenced outcomes,
  bounded stale-work reconciliation, runtime consumer-group recreation,
  expiring circuit probes, and generation-fenced circuit outcomes. Isolated
  regression tests execute against PostgreSQL/Redis (2026-10-17).
- [x] Gate 1, step 5 implementation: single-message claims avoid locally
  queued batches, reclamation retains its scan cursor, and shutdown waits
  for background work with a deadline. Shutdown and scan tests accompany it.
- [x] Gate 2: concurrency and operator semantics (parallel DB regressions and
  authenticated HTTP acceptance run locally; see release-hardening record).
- [x] Gate 2 implementation/regressions: atomic bounded admission, duplicate
  acceptance at capacity, delivery ownership, replay fencing/history order,
  disabled-target cancellation, and malformed-ID validation.
- [ ] Gate 3: security and runtime hardening.
- [x] Gate 3 code: HMAC signing/verifier, strict configuration, request bounds,
  mapped-address SSRF checks, and retry overflow fixes. Patched image build,
  non-root/certificates and the Go vulnerability rescan now pass locally.
  The pool follow-up also replaces out-of-support Alpine 3.20 with 3.24.2,
  fixes the scanner's zlib finding via base-package refresh, and rescans
  the runtime with Trivy 0.76.0: zero OS/Go-binary findings against the
  retained 2026-10-16 database. Initial finding and clean results are both
  retained; only the application image was scanned. The patched image's
  complete container/restore/recovery rehearsal passes at a905733.
- [ ] Gate 4: observability accuracy.
- [x] Gate 4 code: durable pending/DLQ counts, unread versus claimed Redis
  work, delivery-invocation in-flight/started counters, durable circuit-state counts,
  idle refresh, dependency errors, and consistent histogram snapshots.
  Live Prometheus scraping and dashboard provisioning now pass; every
  dashboard panel has not been visually reviewed.
  All 15 expressions across five panels also return live Prometheus API
  data during the pool-change soak (including roughly 25/s ingestion and
  success, four workers, zero failure/retry rates). Raw responses are retained
  in docs/benchmarks/2026-10-17-pool; this is not a visual layout review.
- [x] Gate 5: hosted formatting/vet/scan/build, dependency-backed tests,
  Linux race tests and HTTP acceptance pass at ced4cbe (2026-10-18).
- [x] Gate 5 HTTP runner: eight acceptance cases executed locally against a
  newly built isolated server; CI now runs that command as well. A successful
  hosted CI run now passes formatting/vet/scan/build, real dependency-backed
  tests, Linux race tests and HTTP acceptance at ced4cbe (2026-10-18).
  See docs/validation/2026-10-18-github-publication.md for the run link and
  image job result. Linux Docker race testing also passes locally.
- [ ] Gate 6: measured evidence.
- [x] Gate 6 tooling: native k6 evidence runner retains exact workload, tool/
  configuration metadata, raw output, sampled metrics, and run-scoped delivery
  counts/percentiles. Three current load scenarios execute with raw artifacts,
  resource samples and a retained failing baseline. A two-minute sustained
  run revealed a database-side backlog. A controlled pool fix and repeat
  tests remove that observed backlog at 25/s; the ten-minute follow-up
  delivers all 15,000 accepted events (113ms delivery p95, sampled peak
  pending 9). Broader ceiling/burst/failing-target envelopes remain unproven.
- [ ] Gate 7: deployment readiness.
- [x] Gate 7 preparation: loopback-only service ports, dependency health checks,
  SHA-image production overlay, TLS template, and backup/restore/rollback
  runbook. Current isolated startup/restore/new-delivery checks pass from a
  fresh local clone. Same-schema local image rollback/roll-forward now preserves
  all five durable fingerprints and supports new delivery on both images.
  Any authorized cloud/TLS rehearsal and hosted release publication remain.
- [ ] Gate 8: resume and interview package.
- [x] Gate 8 preparation: code-backed explanations, five-minute demo sequence,
  and evidence-backed resume bullets. A live output-only terminal demo,
  crash-boundary case study and ownership worksheet are retained. Independent
  student walkthrough and personal interview rehearsal need human participation.

## Current verification status and external blockers

- Reviewed escalation now permits Docker engine access. The patched runtime
  image builds and the isolated stack/restore rehearsal passes locally.
- Native k6 v1.8.2 and govulncheck v1.9.0 are installed in ignored .tmp/tools.
  The Go 1.27.2/x/text v0.41.0 rescan reports no vulnerabilities found.
  Current-version load and resource evidence is retained, including high
  delivery latency and database pressure under the stated resource limits.
- Native Windows cgo remains unavailable, but Linux Docker can run race tests.
  See docs/validation/2026-10-17-access-and-security.md for executed checks.
- The approved private repository is now origin on main:
  https://github.com/dhairyarvarma/fault-tolerant-webhook-dispatcher.
  Hosted checks and image publishing at ced4cbe pass; publication evidence
  replaces the earlier missing-remote blocker. Dated records preserve older state.
- No authorized VM/domain is available for executed cloud/TLS deployment.

These do not block native Go correctness/integration/HTTP acceptance checks,
which have been run. They do block declaring the full release complete.

Current experiment: docs/benchmarks/2026-10-17-pool/README.md. Connection-churn
profiling, a bounded warm pool, regressions and the original three load
scenarios are complete; the original report remains unchanged. Broader
capacity/failure envelopes are separate from the tested workload, not a reason
to add infrastructure merely to increase the feature list.

2026-10-18 follow-up: configured benchmark delivery verification now returns
nonzero on database/summary/artifact errors, mismatched accepted counts,
unfinished work and terminal failures. Native-k6 CLI regressions verify exit
codes, metadata and retained SQL snapshots. Ingestion-only results are labelled
explicitly. The frozen older bundles describe the prior runner; see
docs/validation/2026-10-18-benchmark-verification.md for the new behavior.
