# Fault-Tolerant Webhook Delivery Engine

[CI](https://github.com/dhairyarvarma/fault-tolerant-webhook-dispatcher-public/actions)
| [Usage walkthrough](docs/walkthrough.md)
| [Public snapshot provenance](PUBLIC_RELEASE.md)

This is a separately published privacy-sanitized snapshot. Historical SHAs/CI
links refer to original private development history; benchmark derivatives
are labelled and retain their original measurement conditions.

```bash
git clone https://github.com/dhairyarvarma/fault-tolerant-webhook-dispatcher-public.git
cd fault-tolerant-webhook-dispatcher-public
```

A durable, asynchronous webhook delivery backend in Go. It decouples
receiving an event from delivering it, and makes delivery explicit,
observable, and resilient to partial failure: retries with backoff and
jitter, a per-target circuit breaker, dead-letter handling with manual
replay, and crash recovery - all backed by PostgreSQL as the single durable
source of truth, with Redis Streams as transport only.

**Delivery guarantee: at-least-once with idempotency protection.** This
system does not claim exactly-once end-to-end delivery - no honest system
built on an outbox + a network + an arbitrary external target can. A worker
crash can cause a message to be retried; every delivery carries a stable
`Idempotency-Key` so a target that honors it can deduplicate.

## Quickstart

For API examples, follow the [usage walkthrough](docs/walkthrough.md).
For configuration, backup/restore and deployment precautions, read the
[operations guide](docs/operations.md).

Prerequisites: Docker Desktop with Linux containers and Docker Compose v2.
Native tests/demo additionally need Go 1.27.2 or later. The shell examples
below use Bash; use the PowerShell 7 equivalent beneath them on Windows.

```bash
cp .env.example .env
# edit .env: set SECRET_ENCRYPTION_KEY (openssl rand -base64 32)

docker compose up -d --build
```

This starts six services: the API, PostgreSQL, Redis, a deterministic
target-service simulator (for testing without a real third-party
receiver), Prometheus, and Grafana.

```bash
# Create a target (ALLOW_PRIVATE_TARGETS is already set for the api
# container so it can reach the bundled simulator over the Docker network)
curl -s -X POST http://localhost:8080/api/v1/targets \
  -H "Content-Type: application/json" \
  -d '{"name":"demo","url":"http://target-simulator:9090/sim/status/200"}'
# -> {"target_id": "...", "circuit_state": "CLOSED", ...}

# Create an event against it
curl -s -X POST http://localhost:8080/api/v1/targets/<target_id>/events \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: demo-key-1" \
  -d '{"payload":{"type":"invoice.created","id":"inv_123"}}'
# -> {"event_id": "...", "status": "PENDING", ...}

# Poll it - delivery happens asynchronously
curl -s http://localhost:8080/api/v1/events/<event_id>
# -> {"status": "SUCCEEDED", "attempt_count": 1, ...}

curl -s http://localhost:8080/api/v1/events/<event_id>/attempts
```

Grafana is at `localhost:3000` (admin/admin), with a "Webhook Dispatcher"
dashboard provisioned automatically. Prometheus is at `localhost:9091`.
Full API contract: `API_SPEC.md` and `contracts/openapi.yaml`.

### Windows / PowerShell 7

Copy .env.example to .env. Generate the encryption key with the command below
and put its output in SECRET_ENCRYPTION_KEY in .env; do not commit or share it.
The .env database/Redis hostnames are for Compose, not native host processes.

```powershell
Copy-Item .env.example .env
[Convert]::ToBase64String([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
# Edit .env with the generated key, then:
docker compose up -d --build
Invoke-RestMethod http://localhost:8080/readyz
$target = Invoke-RestMethod http://localhost:8080/api/v1/targets -Method Post -ContentType application/json -Body '{"name":"demo","url":"http://target-simulator:9090/sim/status/200"}'
$event = Invoke-RestMethod "http://localhost:8080/api/v1/targets/$($target.target_id)/events" -Method Post -ContentType application/json -Headers @{'Idempotency-Key'='demo-1'} -Body '{"payload":{"type":"invoice.created"}}'
Invoke-RestMethod "http://localhost:8080/api/v1/events/$($event.event_id)"
```

Wait for /readyz to return 200 before creating targets. Poll the event until
SUCCEEDED; a PENDING/QUEUED response just means asynchronous work is unfinished.
If default host ports are occupied, set HTTP_PORT, POSTGRES_PORT, REDIS_PORT,
TARGETSIM_PORT, PROMETHEUS_PORT, or GRAFANA_PORT in .env. Use the corresponding
port in client commands. Compose's simulator URL remains container-internal.

### Isolated release rehearsal

From the repository root in PowerShell 7 on Windows, with Docker and Go available:

```powershell
./scripts/verify-release.ps1
# Also run native k6 ingestion, 2-minute sustained, and slow-receiver tests:
./scripts/verify-release.ps1 -Load
# Short connection-churn profile, or ten-minute sustained run:
./scripts/verify-release.ps1 -Profile
./scripts/verify-release.ps1 -Load -SustainedSeconds 600
```

The load option requires k6 on PATH. If it was installed into this checkout's
ignored tools directory, first run
`$env:PATH=(Join-Path $PWD '.tmp/tools')+';'+$env:PATH`.
The script uses its own unique Compose project, random loopback ports, and
synthetic credentials; it does not read .env or stop your normal application.
It checks container HTTP behavior, Prometheus/Grafana provisioning, database
restore fingerprints, post-restore delivery, and Redis/worker restart recovery.
Raw evidence is retained under reports/generated; only its test containers
and volumes are removed. Images/build cache remain available for reuse.
Load reports include PostgreSQL session-count deltas across each workload
and drain. These include benchmark/operator connections, not only the API.
This is local release evidence, not cloud deployment or an independent human
usability test.

For the design explanation, read the
[crash-boundary case study](docs/case-study-delivery-crash.md).

## Architecture

```text
Client -> API -> PostgreSQL (event + outbox, one transaction)
                       |
                 Outbox Relay --> Redis Stream --> Worker Pool
                                                       |
                                              circuit breaker check
                                                       |
                                               Delivery Client -> Target
                                                       |
                                          record attempt + transition (PostgreSQL)
```

See `diagrams/system-architecture.mmd` for the full component diagram
(including the retry scheduler, outbound/SSRF policy, and metrics path),
`diagrams/delivery-state-machine.mmd` for event states, and
`diagrams/failure-recovery.mmd` for how each failure mode is handled.

**Why PostgreSQL, not Redis, is the source of truth**: a worker re-checks
an event's durable status before ever calling the target, so a stale or
duplicate Redis message can't trigger a duplicate *logical* delivery
outcome - at worst it triggers a duplicate HTTP call, which the stable
idempotency key is there to let the target itself deduplicate. If Redis
loses stream state entirely, PostgreSQL retains the work. The scheduler
reconciles stale `PENDING`/`QUEUED` events after a 60s grace period, excluding
active delivery leases and unpublished outbox rows. Workers recreate missing
consumer groups. Expiring UUID claims serialize concurrent queue duplicates;
a crash after HTTP success can still repeat the HTTP call with the same key.
This recovery protocol is regression-tested and exercised by the current
isolated release rehearsal. See the [operations guide](docs/operations.md).

## What's implemented

- Durable event creation (event + outbox row in one transaction), idempotency
  via a `(target_id, idempotency_key)` uniqueness constraint
- Asynchronous delivery via an outbox relay and Redis Streams consumer group
- Retry classification (retryable vs. non-retryable per HTTP status/error
  class), exponential backoff with full jitter, configurable attempt budget;
  due retries are queued atomically with outbox rows for relay publication
- Per-target circuit breaker (CLOSED/OPEN/HALF_OPEN), persisted in
  PostgreSQL so it survives restarts and is shared across worker goroutines
- Dead-letter queue with manual replay (`POST /events/{id}/retry`), which
  resets the attempt budget while preserving event identity and full
  attempt history
- Optional process-local per-target concurrency cap
  (`WORKER_TARGET_CONCURRENCY`, disabled by default): durably defer saturated
  targets without consuming HTTP attempts rather than blocking workers.
  This is not distributed fairness; see Decision 40 and docs/operations.md.
- Opt-in bounded-parent history cleanup for old successful/canceled events,
  off by default. Deletion ends those events' audit/idempotency window;
  DLQ, pending, leased and unpublished work are preserved. See
  [operator precautions](docs/operations.md) before enabling it.
- Optional admission control
  (`EVENTS_MAX_PENDING_EVENTS`): rejects new event creation with `429` +
  `Retry-After` once the durable non-terminal row count hits the bound
  (`GET /api/v1/events?status=DLQ` exists for operator triage);
  disabled by default
- Worker crash recovery via Redis `XAUTOCLAIM` (a message claimed but never
  acked gets reassigned and reprocessed)
- SSRF/outbound policy: rejects loopback/private/link-local/metadata
  addresses in production mode, never follows redirects, no caller-controlled
  outbound headers
- `X-API-Key` authentication outside `development` mode; secrets encrypted
  at rest (AES-256-GCM), timestamped HMAC-SHA256 signatures for configured
  targets (`SECURITY_MODEL.md`, verifier example in `examples/receiver`)
- Prometheus metrics + a provisioned Grafana dashboard
- A deterministic target simulator (`internal/targetsim`) for fixed
  statuses, latency, hangs, connection resets, flaky-then-recovers behavior,
  and idempotency-aware responses - used throughout the test suite instead
  of a real third-party endpoint

## What's measured, not just claimed

The [operational rehearsal](docs/benchmarks/2026-10-19-operations/README.md)
adds admission recovery, two-process work sharing/owner-loss recovery, and a
ten-minute cap-enabled mixed soak: 4,801 events delivered, with zero failed
HTTP requests, dropped arrivals or final unfinished work. Healthy delivery
p95 was 116ms; the 500ms target's p95 was 3.835s. These are finite local
observations with explicit source/image provenance, not production promises.

The [mixed-target experiment](docs/benchmarks/2026-10-18-mixed/README.md)
demonstrates a noisy-neighbor tradeoff: healthy delivery p95 was 9.97s alongside
an overloaded slow receiver. With the optional two-slot process-local cap it
was 265ms, while slow-target p95 increased from 10.41s to 65.46s. All healthy/
slow events completed. These are sequential local observations, not a fairness
SLO or throughput gain; failing-target outcomes and raw evidence are retained.

The [pool experiment](docs/benchmarks/2026-10-17-pool/README.md) retains
before/after workloads, raw results, machine limits and a failed startup.
The [earlier report](docs/benchmarks/2026-10-17/README.md) preserves the
original backlog and fresh-checkout rehearsal rather than rewriting history.

- Diagnosed connection churn: a 25/s, 30s baseline opened 905 database sessions.
  A bounded warm pool reduced the observed count to 20; API p95 changed from
  385ms to 14.3ms and delivery p95 from 12.6s to 107ms. Shared-machine timings
  vary; session counts include probes and benchmark/operator connections.
- The optimized two-minute run accepted and delivered 3,001 events at 25/s,
  with zero HTTP failures or dropped arrivals, 17.3ms API p95, 116ms delivery
  p95 and peak sampled pending work of 3 under a one-CPU PostgreSQL quota.
- A 500ms receiver still saturates four workers and builds backlog at 20/s.
  Connection reuse does not remove that bound or establish peak capacity.
- A ten-minute soak accepted and delivered 15,000 events at 25/s, with zero
  failed/dropped requests, 15.7ms API p95 and 113ms delivery p95. Peak sampled
  pending work was 9. This finite local run is not a production SLA.
- Redis restart to durable success was 3.82s; worker SIGKILL/restart recovery
  after an observed claim was 61.67s. These are single-run observations that
  include command/polling overhead, not SLAs.
- Restore preserved all five durable table fingerprints and permitted new
  delivery using the restored encrypted secret.

These tests used explicit resource limits and shortened retry/circuit
settings. Compare results only with their recorded workload and source/image
conditions. The current recorded demo is linked in Development below.

Hosted formatting, vet, vulnerability scanning, build, repository tests,
Linux race tests and HTTP acceptance passed in the
[public release CI run](https://github.com/dhairyarvarma/fault-tolerant-webhook-dispatcher-public/actions/runs/39203502063).
The pool experiment retains a dated runtime-image scan and release rehearsal;
those are not claims about the latest vulnerability database. See
[provenance](PUBLIC_RELEASE.md) for evidence scope and the Actions tab for
current CI. No real cloud VM/TLS deployment or production SLA is claimed.

The [operations guide](docs/operations.md) covers the optional production
Compose overlay, isolated restore rehearsal and schema-aware rollback.

## Development

For a short narrated showcase using real PostgreSQL/Redis and a freshly built
isolated API, set TEST_DATABASE_URL and TEST_REDIS_URL as below, then run:

```bash
go run ./cmd/acceptance -demo
```

It asserts success, duplicate rejection with the same event ID, retry
exhaustion, open-circuit deferral without consuming attempts, successful
probe recovery, and replay with history retained. Demo settings are shortened
to three attempts and a five-second cooldown; timings are not benchmarks.
The command removes only its own temporary schema, stream, and server.

The recorded run is available as a
[readable transcript](docs/benchmarks/2026-10-17-pool/demo/showcase.txt) and
[timed terminal recording](docs/benchmarks/2026-10-17-pool/demo/showcase.cast).
It shows real HTTP behavior with shortened demo settings, not a production
recovery benchmark. To record another run in PowerShell 7, set the dependency
test URLs and run `./scripts/record-demo.ps1`. It records output only, not
keystrokes or environment values, and validates the recording structure and
demo exit status. The recording uses the
[asciicast v2 format](https://docs.asciinema.org/manual/asciicast/v2/); it is a
terminal recording, not an MP4 or a narrated screen-capture video.

```bash
go build ./...
go vet ./...
gofmt -l .
go test ./...                    # unit tests; DB/Redis-backed tests skip
                                  # gracefully if TEST_DATABASE_URL/
                                  # TEST_REDIS_URL aren't set
```

To run the repository/integration tests locally:

```bash
docker compose up -d postgres redis
export TEST_DATABASE_URL="postgres://webhook:webhook@localhost:5432/webhook?sslmode=disable"
export TEST_REDIS_URL="redis://localhost:6379/0"
go test ./...
```

Database fixtures use isolated schemas and test-owned Redis streams. To run
the HTTP acceptance suite against a newly built, isolated authenticated API
and local target simulator using the same TEST_DATABASE_URL/TEST_REDIS_URL:

```bash
go run ./cmd/acceptance
```

It creates temporary resources and stops its own server on exit. This does
not restart or replace your running application.

## Documentation

- [Architecture](ARCHITECTURE.md), [database design](DATABASE_DESIGN.md) and
  [delivery semantics](QUEUE_AND_DELIVERY_SEMANTICS.md).
- [API specification](API_SPEC.md) and [OpenAPI contract](contracts/openapi.yaml).
- [Engineering decisions](DECISIONS.md), [failure modes](FAILURE_MODES.md),
  [security model](SECURITY_MODEL.md) and [observability](OBSERVABILITY.md).
- [Usage, operations, case study and benchmark reports](docs/README.md).
- [Load-test instructions](load/README.md).
