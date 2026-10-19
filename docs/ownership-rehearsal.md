# Ownership rehearsal: explain, trace, change, test

This is a practice worksheet, not evidence that someone has completed it.
Aim to do the exercises without reading prepared answers aloud.

## Trace one event (10 minutes)

Starting at POST /api/v1/targets/{id}/events, follow:

1. [HTTP validation](../internal/api/events_handler.go) and the event service.
2. [CreateWithOutbox](../internal/store/events.go): where uniqueness and atomic
   creation live; why an early API response is not a delivered webhook.
3. [Relay](../internal/outbox/relay.go): publish/mark ordering and its duplicate window.
4. [Worker](../internal/worker/pool.go): state recheck, claim, circuit permission,
   HTTP, committed outcome, acknowledgement.
5. [Retry policy](../internal/retry/policy.go) and scheduler: why RETRY_WAIT
   messages cannot bypass next_attempt_at.

Explain what survives a process crash at each boundary, what may repeat, and
which system is authoritative. Trace both initial delivery and a due retry.

## Add a regression live (10 minutes)

In a disposable practice checkout, add a table case to TestClassify for HTTP
409 returning NON_RETRYABLE_FAILURE. The current policy already does that;
this exercise adds coverage rather than changing the product contract.

```bash
go test -count=1 -v ./internal/retry -run '^TestClassify$'
```

Then explain why 429 and 503 are retryable but 409 is not under this policy.
If asked to change 409 to retryable, first explain the compatibility impact,
update the documented policy, make the smallest classifier change, and change
the regression expectation. Do not silently change the main release's policy
as part of interview practice.

## Defend a failure (10 minutes)

Draw the HTTP-success/database-failure sequence from
[the case study](case-study-delivery-crash.md). Explain:

- Why two HTTP requests can correspond to one committed attempt.
- Why an expiring token is more useful than an in-memory mutex.
- Why idempotent ingestion does not deduplicate the receiver's side effects.
- What the 60-second recovery grace protects and what it costs.
- What measurement would justify increasing workers or splitting a service.

## Defend the performance change (5 minutes)

Read Decision 38 and internal/store/db.go. Explain the difference between
an open-connection cap and an idle-connection budget, and why adding workers
does not fix connection churn. Run the regression with TEST_DATABASE_URL set:

```bash
go test -count=1 -v ./internal/store -run '^TestOpen(BoundsConnectionBudget|ReusesConcurrentConnections)$'
```

Find the before/after session counts, workload, CPU limits, and delivery
percentiles in the retained evidence. Explain why a result from one shared
laptop is not a production capacity guarantee, and why four workers cannot
sustain 20 deliveries/s when each receiver call sleeps 500ms. Then describe
how 32 connections per replica can still exhaust a database across replicas.

## Completion checklist

- [ ] Explain the design without saying exactly-once delivery.
- [ ] Find the relevant code without searching the README for an answer.
- [ ] Run the narrated showcase and explain each assertion.
- [ ] Add and run the classifier regression without assistance.
- [ ] Ask another student to follow the quickstart and record their friction.

An agent-run fresh-checkout test is useful, but it does not replace that last
independent human check or demonstrate your personal understanding.
