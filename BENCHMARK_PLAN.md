# Benchmark Plan

Do not pre-fill results. This file defines what to measure.

## Benchmark goals

1. Measure ingestion throughput.
2. Measure API p50/p95/p99 latency.
3. Measure end-to-end delivery latency.
4. Measure worker throughput.
5. Measure queue backlog behavior under burst traffic.
6. Measure recovery time after target/worker/Redis failures.
7. Measure the overhead introduced by resilience features.

## Scenarios

### Scenario A — steady traffic

Run sustained event creation at several increasing rates until the system reaches saturation.

### Scenario B — burst traffic

Send a short high-rate burst and observe queue depth, latency and recovery.

### Scenario C — slow target

Inject controlled latency and measure worker utilization and queue growth.

### Scenario D — failing target

Inject 5xx responses and observe retry behavior, circuit breaking and DLQ behavior.

### Scenario E — worker failure

Terminate a worker during active delivery and measure recovery.

### Scenario F — dependency interruption

Temporarily interrupt Redis or PostgreSQL and measure whether durable events are preserved and how quickly the system recovers.

### Scenario G — mixed-target interference

Compare healthy-only traffic with healthy, slow and repeatedly failing targets
sharing the same four-worker process. Keep healthy arrivals at 5/s, then add
10/s to a 500ms HTTP-200 receiver and 2/s to an HTTP-503 receiver for 30s.
The slow workload alone demands five worker-seconds per second, exceeding the
four-worker budget. This deliberately tests overload isolation, not normal capacity.
Repeat the healthy-only control after disabling the failed target and draining
the successful targets. Retain each target's k6 results, run-scoped SQL counts,
end-to-end success percentiles and actual workload overlap times.

Acceptance of the experiment requires no dropped arrivals/HTTP failures,
matching SQL acceptance counts, and all healthy/slow events completing within
the bounded drain. The failing target may remain pending or end in DLQ; label
that expected failure explicitly and never count it as verified delivery.
Reject unrelated verification errors. Record a final per-target SQL snapshot.
Use a unique disposable Compose project; never truncate shared tables.
No fairness guarantee or worker-policy change is implied by this experiment.

## Reported metrics

- requests/sec
- deliveries/sec
- p50 latency
- p95 latency
- p99 latency
- maximum/average queue depth
- worker utilization
- error rate
- retry count
- time to recover
- memory usage
- CPU usage

## Benchmark discipline

Record:

- hardware
- OS/runtime versions
- configuration
- worker count
- concurrency
- payload size
- target behavior
- test duration
- tool version

Never compare two results without documenting changed conditions.

Never write a resume metric until the raw result and test conditions are saved.
