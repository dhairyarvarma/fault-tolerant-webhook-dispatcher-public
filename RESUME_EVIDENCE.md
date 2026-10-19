# Resume Evidence Record

This file is intentionally a measurement sheet. Do not replace placeholders with invented values.

## Mixed-target isolation evidence — 2026-10-18

The [mixed-target report](docs/benchmarks/2026-10-18-mixed/README.md) retains
an overloaded shared-worker run and an opt-in process-local target cap.
Healthy delivery p95 was 9.965s uncapped and 0.265s capped in the paired local
mixed runs; slow-target p95 rose from 10.413s to 65.460s. All healthy/slow
events completed, while the failing target's pending/DLQ outcomes are reported
separately. This is an isolation tradeoff, not an overall throughput gain.
Source/image provenance, controls, shortened settings and lack of randomized
repeats are explicit in the report. Do not attach these numbers to an older
public snapshot, a production SLA or distributed fairness across replicas.

An optional resume bullet, only if you can defend the tradeoff:

> Measured noisy-neighbor effects in a Go webhook dispatcher and added opt-in
> per-process target concurrency isolation; observed healthy-delivery p95 change
> from 9.97s to 265ms in local mixed-target tests, with higher slow-target backlog.

## Current evidence — 2026-10-17

Use [the connection-pool experiment](docs/benchmarks/2026-10-17-pool/README.md),
not the historical figures below, for new claims. At 1a5be04, the optimized
two-minute run accepted/delivered 3,001 events at 25/s with 17.3ms API p95,
116ms delivery p95 and peak sampled pending work of 3. All three repeated
scenarios have zero HTTP failures/dropped arrivals and all accepted events
eventually succeeded. The slow receiver still creates a worker-bound backlog.
The subsequent ten-minute soak accepted/delivered 15,000 events at 25/s,
with zero failed/dropped requests, 15.7ms API p95, 113ms delivery p95 and
peak sampled pending work of 9. Raw results and memory-growth caveats are
retained in the same report.

The paired short experiment observed 905 versus 20 database sessions across
load/drain after replacing the two-idle/unbounded-open pool with 16-idle/
32-open per process. Delivery p95 changed from 12.615s to 0.107s. Raw inputs,
outputs, quotas and shared-machine limitations are retained; these are not
production SLAs or peak-capacity figures. The original large-backlog results
remain [unaltered](docs/benchmarks/2026-10-17/README.md).

A defensible measurement-focused resume bullet is:

> Diagnosed PostgreSQL connection churn in a Go webhook dispatcher; reduced
> observed session creation from 905 to 20 in paired local load runs, added
> backend-reuse regressions, and verified 3,001 deliveries at 25 arrivals/s
> with retained latency/backlog evidence, restore checks and Linux race tests.

Conditions and limits belong in the linked report. The short terminal demo,
case study and ownership worksheet are repository artifacts; an independent
student walkthrough and your personal rehearsal still need human completion.

## Historical measurements — 2026-10-16

The remaining sections describe the older commits/configuration explicitly
named below. Their destructive shared-database procedures are historical
records, not recommended reproduction steps; use the isolated rehearsal now.

## System version

- Git commit: `34e652538f1b441958246b4d5f542b54b03ac29d`
- Date: 2026-10-16
- Go version: go1.27.0 windows/amd64
- Docker version: 29.8.0 (build 88096ef)
- PostgreSQL version: 16.15 (postgres:16-alpine container)
- Redis version: 7.4.11 (redis:7-alpine container)
- OS: Microsoft Windows 11 Home Single Language 10.0.26200
- CPU: 13th Gen Intel(R) Core(TM) i9-13900H
- RAM: 15.6 GB visible to the OS

## Benchmark configuration

- Worker count: 4 (`WORKER_COUNT=4`)
- Payload size: ~70 bytes JSON (`{"type":"load.test","vu":N,"iteration":N}`)
- Target behavior: `target-simulator` container, `/sim/status/200` (always-200, no induced latency)
- Test duration: 30s
- k6 configuration: `constant-arrival-rate`, rate=25/s, timeUnit=1s, preAllocatedVUs=10, maxVUs=100, run via `docker run --rm grafana/k6 run load/k6/ingestion.js` against the host-run server (`host.docker.internal`)
- Other relevant settings: `OUTBOX_POLL_INTERVAL=200ms`; Postgres/Redis/target-simulator in Docker Compose containers, server binary run natively on the host; database truncated immediately before the run

Exact commands run:

```bash
docker compose exec -T postgres psql -U webhook -d webhook -c \
  "TRUNCATE outbox_messages, delivery_attempts, events, targets RESTART IDENTITY CASCADE;"

curl -s -X POST http://localhost:8099/api/v1/targets -H "Content-Type: application/json" \
  -d '{"name":"loadtest-target","url":"http://localhost:9090/sim/status/200"}'

docker run --rm --add-host=host.docker.internal:host-gateway \
  -e BASE_URL=http://host.docker.internal:8099 -e TARGET_ID=<target_id> \
  -e RATE=25 -e DURATION=30s -v "<repo>/load/k6:/scripts" \
  grafana/k6 run --summary-trend-stats="avg,min,med,max,p(50),p(95),p(99)" /scripts/ingestion.js
```

Run window: 2026-10-16T14:32:25Z - 2026-10-16T14:32:57Z (UTC).

## Results

### Throughput

- Ingestion requests/sec: 25.00/s (750 iterations / 30.0s; k6-reported rate 25.001328/s; 0 failed checks)
- Delivery attempts/sec: 24.99/s (750 `delivery_attempts` rows; span from first `started_at` to last `finished_at` = 30.01s; all 750 events reached `SUCCEEDED`, 0 retries, 0 DLQ)

### Latency

- API p50: 15.38ms
- API p95: 56.41ms
- API p99: 67.97ms
- End-to-end delivery p50: 157.6ms
- End-to-end delivery p95: 202.2ms
- End-to-end delivery p99: 222.4ms (max observed: 240.6ms)

  Measured as `delivery_attempts.finished_at - events.created_at` for all 750
  events (`SELECT percentile_cont(...) FROM events e JOIN delivery_attempts da
  ON da.event_id = e.id`), i.e. wall-clock time from API acceptance to the
  worker's successful HTTP response. This is bounded below by
  `OUTBOX_POLL_INTERVAL=200ms` (the relay must notice the new outbox row
  before anything can happen), which is consistent with the p50 of 157.6ms.

### Resilience

All measured live against the running binary (not just the automated test
suite), using the real Docker Compose Postgres/Redis/target-simulator stack.
Exact procedure for each is below the table.

- Worker crash recovery time: **66s** (crash to `SUCCEEDED`). Bounded by the
  hardcoded `reclaimMinIdle=60s` + up to one `reclaimInterval=30s` tick
  before the stale message becomes eligible for `XAUTOCLAIM`; 66s is
  consistent with "idle long enough, caught on the next tick."
- Redis interruption recovery time: **~2.5s** (`docker compose start redis`
  to the pending event reaching `SUCCEEDED`). Dominated by container
  startup; the app's own reconnect is near-instant once Redis answers.
- PostgreSQL interruption recovery time: **~2.2s** (`docker compose start
  postgres` to `/readyz` returning 200 again).
- Target recovery after circuit opening: **27-31s**, consistent with the
  configured `CIRCUIT_OPEN_COOLDOWN=30s` (polled every 2s; OPEN at t=27s,
  CLOSED at t=31s via a successful HALF_OPEN probe).
- Maximum observed queue depth: **29** (`webhook_queue_depth`, i.e. Redis
  consumer-group `XPENDING` count), under a 50 req/s / 15s k6 burst against
  a target with 500ms simulated latency and `WORKER_COUNT=4`.

Procedures:

1. **Worker crash**: created an event against a target with 5s simulated
   latency (`/sim/latency/5000/200`) so the worker was guaranteed mid-request;
   confirmed the event was still `PENDING` in Postgres; `taskkill /F
   server.exe` while the HTTP call was in flight; immediately restarted the
   server (new process, same consumer names); confirmed via `XPENDING` logic
   that the message stayed claimed-but-unacked under the dead consumer, then
   polled Postgres every 2s until `SUCCEEDED`.
2. **Redis interruption**: created a target + event, `docker compose stop
   redis`, confirmed via direct SQL that the event + unpublished outbox row
   persisted and `/readyz` stayed 200 (ingestion doesn't depend on Redis),
   `docker compose start redis`, polled Postgres every 0.2s until
   `SUCCEEDED`.
3. **PostgreSQL interruption**: `docker compose stop postgres`, attempted
   `POST /api/v1/targets` -> got `500 INTERNAL_ERROR` (generic message, no
   internals leaked) and `/readyz` -> `503 {"reason":"database
   unreachable"}`; `docker compose start postgres`; polled `/readyz` every
   0.3s until 200; confirmed zero rows for the attempted target name (no
   phantom creation) and that a fresh target creation succeeded immediately
   after.
4. **Circuit recovery**: created a target at `/sim/flaky/{name}?fail=5`
   (fails the first 5 calls, succeeds after); created 5 separate events
   against it; confirmed `circuit_state=OPEN, consecutive_failures=5` in
   Postgres; created a 6th event and confirmed it was deferred
   (`RETRY_WAIT`, `attempt_count=0`, zero `delivery_attempts` rows - no HTTP
   call made); polled `circuit_state` every 2s until `CLOSED`; confirmed the
   6th event eventually reached `SUCCEEDED`.
5. **Queue depth**: ran the k6 scenario (`RATE=50`, `DURATION=15s`,
   `PRE_ALLOCATED_VUS=20`) against a 500ms-latency target while polling
   `/metrics` for `webhook_queue_depth` once per second.

### Resource use

- CPU: not captured as a percentage; `Get-Process server` reported
  13.73s cumulative CPU time partway through a 50 req/s / 15s burst (20
  threads). No idle-vs-loaded percentage comparison was taken - flagged as
  a gap, not fabricated.
- Memory: ~26 MB working set (`server.exe`, `Get-Process`/`tasklist`),
  stable between idle and the load-test bursts run during this session.

## Failure-test evidence

- [x] target 500 - live (circuit-recovery and DLQ procedures above)
- [ ] target 429 - automated only (`internal/retry/policy_test.go`), not
      separately exercised against the live stack
- [ ] target timeout - automated only (`internal/targetsim/handler_test.go`
      `/sim/hang`), not separately exercised against the live stack
- [x] worker crash - live, see Resilience procedure 1
- [x] Redis interruption - live, see Resilience procedure 2
- [x] PostgreSQL interruption - live, see Resilience procedure 3
- [ ] duplicate queue message - automated only
      (`TestPool_Handle_DuplicateMessageAfterSuccessIsNoop`), not separately
      exercised against the live stack
- [ ] concurrent idempotency submission - automated only
      (`internal/store/events_test.go`), not separately exercised against
      the live stack
- [x] circuit recovery - live, see Resilience procedure 4
- [x] DLQ + replay - live: drove an event to `DLQ` (5 attempts against an
      always-failing target), called `POST /api/v1/events/{id}/retry`, got
      `200` with `status=RETRY_WAIT, attempt_count=0`, and confirmed all 5
      original `delivery_attempts` rows were preserved.
- [x] SSRF rejection - live: with `ALLOW_PRIVATE_TARGETS=false` (production
      default), `POST /api/v1/targets` with a cloud metadata address
      (`169.254.169.254`), a loopback address, and an RFC1918 private
      address all returned `422`; a legitimate public URL
      (`https://example.com/webhook`) was accepted (`201`).

## Validation limitations

- Race detector: still not runnable on this Windows environment as of
  2026-10-16. `go test -race` fails while compiling `runtime/cgo` because
  the installed `gcc` (MinGW 6.3.0) reports 64-bit mode is unimplemented.
  Re-checked this session; unchanged. Run it on a host with a supported
  64-bit C toolchain (or WSL2/Linux) before claiming race validation.
  Note: the repository's GitHub Actions CI runs `go test -race ./...` on
  Ubuntu with TEST_DATABASE_URL/TEST_REDIS_URL set job-wide, so both the
  unit paths and the DB/Redis integration paths get race coverage on
  every push - the Windows-host gap is a local-execution limitation, not
  a codebase-wide absence of race validation.
- k6: **now run** (see Benchmark configuration/Results above), via
  `docker run grafana/k6` rather than a native install (none was available).
  Only the one ingestion scenario in `load/k6/ingestion.js` was exercised;
  no sustained/soak test or higher-rate ceiling search was attempted.
- End-to-end delivery latency was computed from timestamps already in
  Postgres (not a k6-measured metric); it reflects this specific run's
  conditions (`/sim/status/200`, no induced latency, 200ms outbox poll) and
  should not be read as a general latency floor under different
  configurations.
- Resource use (CPU/memory) is a single rough sample during one burst, not
  a profiled measurement across multiple load levels.

## Resume bullet candidates

Only write a bullet after a result above has been measured and reproduced.

Template:

> Engineered a fault-tolerant asynchronous webhook delivery engine in Go using PostgreSQL, Redis Streams and concurrent workers, implementing <verified mechanisms> and achieving <verified throughput/latency/recovery metric> under <verified test conditions>.

Candidate grounded in the measurements above (single run, 2026-10-16, i9-13900H,
4 workers, k6 via Docker - reproduce before using in a real resume):

> Built a webhook delivery engine (Go, PostgreSQL, Redis Streams) with transactional outbox, jittered retries, circuit breaking, and worker reclaim; in an earlier recorded configuration, observed 25 req/s ingestion with API p99 about 68ms, one worker-crash recovery in 66s, and a pending event completing about 2.5s after Redis restarted. Reproduce on the final implementation before using these numbers.

Shorter variant (same facts, interview-context length):

> Built and fault-tested an asynchronous webhook engine with PostgreSQL outbox, Redis Streams, retry backoff, persistent circuit breaking, and receiver idempotency keys. Historical load/outage observations are recorded for their original conditions; final-version performance remains to be measured.

Security-hardening-focused variant:

> Hardened an outbound webhook delivery path against SSRF: destination validation at both registration and dial time, no redirect-following, zero caller-controlled outbound headers, and AES-256-GCM secret-at-rest encryption - verified live by confirming cloud-metadata, loopback, and RFC1918 addresses are all rejected in production mode while legitimate public URLs are accepted.

## Rule

For current-code, nonnumeric resume bullets and code-backed interview answers,
use docs/interview-guide.md. The new benchmark runner retains raw artifacts;
its validation-only run is not a load result.

Current recovery/concurrency/signing and HTTP acceptance verification is
recorded in [docs/validation/2026-10-17-release-hardening.md](docs/validation/2026-10-17-release-hardening.md).
Historical throughput/recovery results below and above belong to their
recorded commits; the lease/reconciliation protocol changes require new
performance measurements before reusing those numbers for the final code.

Functional regression verification for the retry-outbox crash-window fix
is recorded in [docs/validation/2026-10-17-retry-outbox.md](docs/validation/2026-10-17-retry-outbox.md),
including captured real PostgreSQL/Redis test output. These checks do not
remeasure the historical performance numbers above.

No benchmark number in the final resume may exist only because an AI assistant suggested it.

## Ceiling search and soak (2026-10-17, follow-up session)

Configuration for both runs: same stack and machine as the baseline above;
full Docker Compose stack (API runs as the `api` container, not the host);
target `http://target-simulator:9090/sim/status/200` (always-200, no latency);
k6 via `docker run --network webhook-dispatcher-handoff_default grafana/k6`.
Work produced by these runs is not "supporting evidence" numbers elsewhere;
they were rerun deliberately and every claim below comes from these runs.

### Ceiling search (load/k6/ceiling.js)

Stages (each `constant-arrival-rate`, chained by startTime, 30s each):
25/s → 50/s → 100/s → 200/s.

Two runs:

- Run 1: 11,245 iterations + 8 dropped arrivals, **0 failed requests**,
  p(50)/p(95)/p(99) = 20.7/66.5/137.6ms aggregate.
- Run 2: 11,250 iterations + 3 dropped arrivals, **0 failed requests**,
  p(50)/p(95) = 29.8/75.0ms aggregate.

Interpretation, stated precisely: within the staged range the API never
rejected an event and returned 100% 201s; the only drops were 3-8 k6
executor-arrival drops at the 200/s stage (0.02-0.07% of stage arrivals).
The API's ingestion ceiling under this configuration was not reached at
200/s; finding the true ceiling would need heavier stages (documented as
future work, not claimed). 

### Soak (load/k6/soak.js, 5 minutes)

- RATE=10/s for 5m, exactly 3,000/3,000 iterations succeeded
  (checks 100%, 0 failed requests), p(95) = 50.9ms.
- Post-run verification: all 3,000 soak events at `SUCCEEDED` in
  PostgreSQL - zero backlog at the end of the run (delivery kept pace
  with 10/s while 4 workers also drained, `OUTBOX_POLL_INTERVAL=200ms`).
- This run is 5 minutes, not the planned 10; the scenario file accepts
  DURATION so a longer run is one parameter change. Copy before claiming.
