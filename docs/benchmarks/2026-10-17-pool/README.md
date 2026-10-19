Public derivative: personal identifiers are redacted and hashes regenerated. Historical commit IDs refer to private development history. See [provenance](../../../PUBLIC_RELEASE.md).

# PostgreSQL connection-pool experiment

## Question and change

Can a bounded warm pool reduce the demonstrated database pressure without
changing circuit, transaction, lease, or acknowledgement semantics?

The old database/sql defaults kept two idle connections and allowed unlimited
open connections. A regression holding 12 simultaneous connections in two
bursts observed 20 idle closures. The fix in 1a5be04 keeps up to 16 idle and
caps open connections at 32 per process; both regression tests pass.
See Decision 38 for the replica-budget tradeoff. No dependency or service added.

## Control and provenance

The instrumented before run uses the unchanged application at 0df04f7, with
uncommitted profiling/health-check tooling. It opened 905 PostgreSQL sessions
across load and drain while accepting/delivering 751 events in a 25/s, 30s
workload. API p95 was 385.42ms, delivery p95 12.615s, sampled peak durable
pending 309. Mean PostgreSQL CPU across ten samples was 88.59% on Docker's
per-core scale. The quota is one CPU. This independently confirms session
churn during delivery, not just in the synthetic regression.

Sessions are database-wide deltas from pg_stat_database, including SQL
inspection and the benchmark collector; they are not API-only counts.
The collector was built after the API image and uses the new pool, but only
opens its SQL connection after HTTP load. The API image itself is unchanged.

Conditions match the prior report: shared i9-13900H Windows host, Docker
Desktop Linux VM (20 reported CPUs, 8,133,451,776 bytes memory), native k6
1.8.2, Go 1.27.2, 50 preallocated/100 maximum VUs, four workers. API has
2 CPU/512MiB; PostgreSQL 1 CPU/512MiB; Redis 0.5 CPU/128MiB; simulator
1 CPU/128MiB. One target per workload; shortened rehearsal retry/circuit
settings are captured in deploy/compose.verification.yml.

This is a sequential shared-machine experiment, not randomized repeated
trials or a production capacity guarantee. The earlier unchanged-code run
had delivery p95 22.82s at the same short workload: variation matters.
Compare session counts as well as latency; do not attribute every timing
difference solely to the pool change.

## Completed before/after results

| Workload | Accepted / succeeded | API p95 (ms) | Delivery p95 (s) | Peak sampled pending | New DB sessions |
|---|---|---|---|---|---|
| Before, 25/s for 30s | 751 / 751 | 385.42 | 12.615 | 309 | 905 |
| After, 25/s for 30s | 750 / 750 | 14.31 | 0.107 | 3 | 20 |
| After, 25/s for 120s | 3,001 / 3,001 | 17.31 | 0.116 | 3 | 60 |
| After, 20/s for 20s, 500ms receiver | 400 / 400 | 14.50 | 31.700 | 248 | 28 |

Every completed row has zero HTTP failures, dropped arrivals, final pending,
DLQ and canceled events. Arrival scheduling can produce an extra boundary
iteration, hence 751/3,001; counts are observed, not rounded to rate*duration.
Delivery percentiles include queueing and cover successful events only;
all outcome counts are retained in each delivery.json.

The short comparison reduces observed session creation by 97.8%. The TCP
health probe itself opens a database session every two seconds; those probe
sessions, the collector and SQL commands are included in both deltas.
This is not a claim of 20 API connections or zero new API connections.

During load plus drain, mean PostgreSQL CPU was 32.40% across seven samples
in the short after run, versus 88.59% across ten before samples. During the
two-minute after run it was 33.07% across 28 samples, with peak container
memory 12.21MiB (API) and 76.43MiB (PostgreSQL). Docker memory is not RSS.
Maxima are sampled, not exact instantaneous peaks. Fast HTTP calls can fall
entirely between in-flight metric samples.

The slow target still saturates all four workers: an idealized maximum of
8 deliveries/s cannot sustain 20 arrivals/s. It drained about 33.5s after
load. The two fast runs' final SQL checks completed about 0.05s/0.11s after
HTTP load, not evidence of zero queueing. The pool fix addresses connection
churn, not receiver latency or a general capacity ceiling.

The earlier unchanged implementation's two-minute result (3,000 delivered,
95.7s delivery p95, peak pending 2,394) remains in the immutable prior
bundle. It is supporting context, not a paired repeated trial.

## Ten-minute soak

The 25/s, 600s soak at the same pool implementation accepted and delivered
15,000 events, with zero HTTP failures, dropped iterations, DLQ, canceled or
final pending events. API p50/p95/p99 was 8.78/15.69/28.49ms; successful
end-to-end delivery p50/p95/p99 was 0.070/0.113/0.120s. Across 601 metric
samples, peak durable pending was 9. Final SQL verification finished about
0.174s after HTTP load ended, including query overhead.

Across 141 Docker resource samples, mean API/PostgreSQL CPU was 30.40%/
30.47% on the per-core scale. API container memory ranged from an initial
11.08MiB to a final 14.09MiB, peaking at 14.41MiB. PostgreSQL went from
68.91MiB to 110.9MiB, peaking at 112.7MiB. This finite run did not exhaust
the configured limits, but it does not establish absence of leaks or bounded
database growth: history is retained and database caching grows. Redis and
simulator peaks were 10.84MiB and 10.17MiB. Memory readings are not RSS.
See soak-analysis.json and the unedited samples for the derivation.
The soak's database-session delta was 303, including roughly 300 periodic
TCP health probes plus collector/operator sessions over ten minutes.

The additional short and slow runs in this rehearsal accepted/delivered
750 and 400 events respectively, also without failed/dropped requests or
final unfinished work. Their raw results remain under soak/load.

The five-panel dashboard's 15 Prometheus expressions all returned live data
during the soak. Ingestion/success rates were about 25/s, workers 4, failure
and retry rates zero. Raw dashboard-queries.json retains each expression,
timestamp and response. These query checks do not verify browser layout.

## Startup failure retained

The first profiling attempt failed before load: pg_isready used a Unix
socket and passed against PostgreSQL's temporary initialization server.
The API then encountered connection refused during the final server switch.
TCP health checks now wait for the actual server, and local Compose waits
for dependency health. The successful before run exercises that fix.
The failed attempt is retained under startup-failure, not discarded.

## Reproduce

From PowerShell 7 with Go, Docker Desktop and native k6 available:

```powershell
./scripts/verify-release.ps1 -Profile
./scripts/verify-release.ps1 -Load
./scripts/verify-release.ps1 -Load -SustainedSeconds 600
```

Unique disposable projects use synthetic credentials and loopback ports;
the normal application and its data are untouched. Original artifacts are
under reports/generated. Only raw text/JSON/script evidence is retained here,
not the generated benchmark executable.

## Correctness checks

On the pool change: native go test -count=1 -timeout 2m ./..., go vet ./...,
and go build -buildvcs=false ./... pass. Linux go test -race -count=1
-timeout 3m ./... passes against real PostgreSQL/Redis with isolated fixtures.
The race command skips HTTP e2e without TEST_BASE_URL; separate native HTTP
acceptance passes all eight cases, including signed receiver verification.
govulncheck 1.9.0 reports no vulnerabilities found at execution time.
Raw race, acceptance and scanner output are included in this bundle.

## Local image rollback

After the soak drained, the API was stopped and switched from the pool image
back to the preceding application image, without downgrading the schema.
All five durable-table fingerprints matched before/after rollback. Existing
success remained readable, a new event delivered on the old image, and both
old history and new delivery worked after rolling forward to the pool image.
soak/rollback.json records image IDs, fingerprints and event IDs; the exact
one-off verification script and transcript are retained alongside it.
This proves local same-schema compatibility only, not a cloud rollout,
schema downgrade, high-availability deployment or zero-downtime upgrade.
Both disposable rehearsal stacks were removed after retaining evidence.

## Runtime support and package scanning

The measured before/after/soak images use Alpine 3.20. Alpine's official
[release table](https://www.alpinelinux.org/releases/) lists that branch as
past normal support, so 6415c58 changes the runtime to supported Alpine 3.24.
Its first fresh scan found one medium-severity zlib issue, CVE-2026-85091,
installed 1.3.2-r0 with fixed version 1.3.2-r1. That report is retained under
runtime/initial-scan rather than replaced by the clean rescan.

a905733 refreshes installed base packages during a fresh image build. The
result contains Alpine 3.24.2, zlib 1.3.2-r1, libssl3/libcrypto3 3.5.9-r0.
Trivy 0.76.0 reports zero findings for 17 OS packages and the Go application
binary. Scanner image digest is
sha256:2416d1f74045b57274df1a340eee6b510cacd73c66a6ccb8da092b117eb7d222.
The database metadata reports UpdatedAt 2026-10-16T01:13:54Z and download on
2026-10-17T10:21:01Z; the rescan deliberately reuses that same database for
comparison. This is a dated scan result, not proof of absence of security bugs.
Only the application runtime image was scanned, not every dependency service.

The scanner reads a docker-save archive mounted read-only, without Docker
socket access or application image publication. See the official
[Trivy image-input reference](https://trivy.dev/docs/dev/references/configuration/cli/trivy_image/).
Raw JSON includes image identity, packages and findings. Normal cached builds
can retain stale package layers: use --pull --no-cache for a release security
refresh, then rescan and retain the tested image ID.

The application binary SHA256 is identical in the measured soak, supported
base, and patched-base images:
810cffb45146b8ca5cd7a3b84c71623209a5cc50c6c5b1bd007742ec4db933f6.
The ten-minute load was not rerun on the changed OS image; it belongs to the
recorded measured image. Separate container acceptance/recovery rehearsals
cover the refreshed runtime. Do not silently relabel old image measurements.
The final a905733 rehearsal passes startup/readiness/non-root/CA checks,
container HTTP acceptance/showcase, Prometheus/Grafana provisioning, all-five-
table restore fingerprints and encrypted-secret delivery. Its Redis restart
observation is 4.247s and observed-claim SIGKILL recovery is 61.625s, including
Docker commands and polling. These are individual observations, not SLAs.
The final native suite, vet, build and module verification also pass; raw
native output is retained under runtime/pool-final-native.txt.

## Evidence map and remaining limits

- before: instrumented unchanged-code short run and full release checks.
- after: the committed pool change's three repeated scenarios.
- soak: ten-minute follow-up, all dashboard query responses, and rollback.
- runtime: initial supported-base verification and the retained zlib finding.
- runtime-patched: fixed image scan, package metadata and final release check.
- demo: refreshed readable transcript and line-timed terminal recording.
- sha256.json: byte hashes for the bundle, excluding the manifest itself;
  Git attributes disable line-ending conversion.

Independent human walkthrough, personal interview rehearsal, hosted CI and
cloud/TLS deployment are not demonstrated by these local checks. There is no
Git remote or authorized cloud host/domain in this environment. The benchmark
runner's exit status checks HTTP thresholds, not complete delivery; inspect
delivery.json explicitly, as done for every reported run here. Broader load
envelopes, multi-tenant authorization and automatic history retention remain
unimplemented or unproven; adding another broker does not resolve those gaps.
