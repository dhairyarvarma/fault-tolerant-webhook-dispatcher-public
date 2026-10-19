Public derivative: personal identifiers are redacted and hashes regenerated. Historical commit IDs refer to private development history. See [provenance](../../../PUBLIC_RELEASE.md).

# Operational validation and capped soak — 2026-10-19

## Executed checks

The disposable rehearsal operations-20261019045037-f7b3af1e passed:

- With Redis stopped and admission bound 3, exactly three new events/outbox
  rows committed. An excess request returned 429 with Retry-After: 5 and no
  phantom rows. A duplicate at capacity returned 409 with the original ID.
  Restarting Redis delivered all three; the formerly rejected key could then
  be accepted and delivered. This tests durable admission/recovery, not an
  ingestion-capacity ceiling or a timed outage SLA.
- Two API containers shared PostgreSQL, stream/group and work. Sixteen
  events to a 1s receiver completed. Each process's sampled HTTP-in-flight
  peak was 2 and the paired-scrape sum reached 4. The cap is process-local,
  not a cluster-wide limit of 2. Scrapes are sequential, not simultaneous
  receiver-side concurrency measurements. Processes started against an
  already-migrated database; concurrent fresh migrations were not tested.
- With only the first process running, a specific event's delivery claim
  was observed. The second process started, the same claim was confirmed,
  and the first was killed. The survivor completed that event in 64.091s
  from the kill command, including polling and an 8s receiver. This proves
  owner-loss recovery, not a precisely timed HTTP-success/commit crash window.
- One API then ran a ten-minute capped soak, with concurrent healthy 5/s
  and 500ms slow-target 3/s workloads. All 4,801 accepted events completed.

## Soak results

| Target | Accepted / succeeded | API p95 ms | Delivery p50 / p95 / p99 s |
|---|---|---|---|
| Healthy | 3,001 / 3,001 | 15.215 | 0.077 / 0.116 / 0.121 |
| 500ms receiver | 1,800 / 1,800 | 15.096 | 0.578 / 3.835 / 7.196 |

Each workload has zero failed HTTP requests, dropped arrivals, final pending,
DLQ or canceled events. SQL acceptance counts match k6's named acceptance
checks; both benchmark processes exited zero with completed/verified status.
Boundary scheduling accounts for 3,001 rather than a rounded 3,000. Delivery
latency includes queueing and uses committed successful-attempt timestamps;
it is not API latency or just the receiver sleep duration.

There were 440 committed capacity deferrals during the soak. These consume
no delivery attempts, can repeat for one event and are not distinct failures.
The durable-pending gauge peaked at a sampled 4; sampled HTTP-in-flight peaked
at 3. The slow target's long tail shows that jittered deferral is not FIFO
fairness, even at offered load below its idealized two-slot service rate.

Across 142 Docker resource samples, API container memory was 7.395MiB at
the start, 11.21MiB at the end and 12.35MiB at its sampled peak. PostgreSQL
was 44.34MiB initially, 61.05MiB finally and 64.77MiB at peak. Mean API/PG
CPU was 12.42%/14.14% on Docker's per-core scale. These are not RSS or exact
instantaneous maxima. History/cache growth is expected; this finite run does
not prove absence of leaks or bounded storage. See analysis.json, analyze.ps1
and resources.jsonl for derivation and original samples.

## Conditions and source provenance

Shared Windows i9-13900H host; Docker Desktop VM reports 20 CPUs and
8,133,451,776 bytes memory. Go 1.27.2 and native k6 1.8.2. Four workers,
WORKER_TARGET_CONCURRENCY=2. Quotas: API 2 CPU/512MiB; PostgreSQL 1 CPU/512MiB;
Redis 0.5 CPU/128MiB; simulator 1 CPU/128MiB. Twenty preallocated and fifty
maximum VUs per workload. Retry/circuit timings are shortened verification
settings, not production defaults. Retention stayed disabled throughout.
New capacity/retention counters were successfully queried through Prometheus;
dashboard JSON has congestion/retention panels. Browser layout was not reviewed.

The measured image was built from private HEAD 06887da plus uncommitted
metrics/retention/isolation changes. Image identities are in operations.json;
the exact measured cmd/internal Go files are retained as .go.txt files under
measured-source, checked against measured-source-sha256.json before retaining
this bundle.
Go module, Dockerfile and dependencies were unchanged from that HEAD. Later
hardening uses bulk cleanup, allows a shorter configurable cleanup interval,
and retries a caller key racing with purge. Those changes were not exercised
by this retention-disabled soak; final-source correctness is tested separately.
Do not label these figures as measurements of a later public commit/image.

Resource sampling and metric collection ran during load. Heavy race/build
verification ran separately. Small documentation/source-record work continued
on the shared host, so these are not isolated laboratory or randomized trials.
There is no claim of peak throughput, a production SLO or arbitrary-replica HA.

## Reproduce

From the repository root in PowerShell 7, with Go, Docker and native k6:

```powershell
./scripts/verify-operations.ps1 -SelfTest
./scripts/verify-operations.ps1
```

The default soak is 600s; SoakSeconds accepts 120–1800. Only synthetic data,
unique test projects and random loopback ports are used. The second process
uses the verification overlay's restored-api service with its database/stream
explicitly pointed to the first process's state; this is not a restore test.
Both APIs, test dependencies and the project network were removed at the end.
The normal application and its data were untouched; images/cache remain.

The bundle retains the executed harness/configuration, observations, workload
copies, k6 summaries/output, SQL outcome snapshots, metric samples and resource
samples. No generated executable or real .env is included. Personal home paths
were replaced with PUBLIC_USER; originals remain in local reports/generated.
These are redacted derivatives. sha256.json covers retained derivative bytes,
not the unredacted originals. Retention fixture tests run only in temporary
schemas; no cleanup job was enabled on existing user or production data.

Final frozen-source Linux race/DB/Redis, vet/build and signed HTTP acceptance
passed; see linux-final-verification.txt and the dated validation record.
linux-initial-verification.txt preserves an earlier vet import-resolution
failure while source was still being edited; it is not reported as a pass.
