# Case study: a receiver accepted the webhook, but the worker could not commit

## Problem

The dangerous failure is not a receiver returning 500. It is a receiver doing
the requested work and returning 200, followed by the dispatcher losing its
database connection or crashing before it records that success.

The database still says delivery is unfinished. Retrying may repeat the
receiver's business effect; discarding the work may lose an event whose
receiver never actually completed it. A timeout cannot tell those cases apart.

## Decision and alternatives

This dispatcher chooses at-least-once delivery attempts. It preserves
unfinished durable work and retries using the same identity. The receiver must
atomically deduplicate that identity with its business operation.

Acknowledging Redis before the database transaction would make recovery
dependent on later reconciliation and enlarge the period in which work looks
finished to the transport but is not durably complete. Holding a database
transaction open across HTTP would tie up locks/connections and would still
not make the remote side effect part of the local commit. An in-memory mutex
cannot coordinate multiple processes or survive a crash. None of these
alternatives makes an arbitrary external HTTP operation exactly-once.

## Implementation

The event and outbox row commit together. Relay publication may repeat.
Workers reload PostgreSQL state rather than trusting queue messages.
Before delivery, a worker acquires an expiring UUID ownership token; concurrent
queue duplicates cannot both own a live claim. Completion checks the token and
deadline, and commits attempt history plus the resulting event state together.

If processing fails, processMessage does not acknowledge the Redis message.
Redis reclamation, or PostgreSQL stale-work reconciliation after transport
loss, can make the unfinished event eligible again. A stale worker cannot
clear a newer token or write a completion with its old token. Tokens fence
database writes; they cannot undo HTTP already received by another system.

The outbound Idempotency-Key stays stable across retries. X-Webhook-Id is the
stable event UUID; receivers shared across several target registrations should
deduplicate that UUID or correctly scope caller keys, which are only unique
per target.

## Evidence: model the exact boundary, not a lucky process-kill time

[The worker regression](../internal/worker/pool_test.go), in
TestPool_ProcessMessage_DatabaseFailureStaysPendingUntilRecovery/attempt_commit,
uses real PostgreSQL and Redis and a local HTTP receiver. On its first request,
the receiver closes the worker's database pool and returns 200. The test
verifies that the message remains pending, opens a new pool, explicitly expires
the abandoned lease, and reclaims the work.

The result is intentionally **two HTTP requests but one committed successful
attempt**, with the same Idempotency-Key on both requests and no pending Redis
message after recovery. Attempt history records committed outcomes; it is not
an authoritative count of all requests that ever reached a receiver.

[Lease regressions](../internal/store/delivery_lease_test.go) also prove that
old release, completion, and deferral operations cannot overwrite the next
owner's claim. [Transport recovery tests](../internal/worker/transport_recovery_test.go)
exercise duplicate messages and loss/recreation of Redis transport state.

Run the boundary regression with TEST_DATABASE_URL and TEST_REDIS_URL set to
dedicated local dependencies:

```bash
go test -count=1 -v ./internal/worker -run '^TestPool_ProcessMessage_DatabaseFailureStaysPendingUntilRecovery$'
go test -count=1 -v ./internal/store -run '^TestDeliveryLease_FencesExpiredOwners$'
```

The test advances lease expiry explicitly, so it proves correctness at that
boundary rather than measuring real-time crash recovery. The release rehearsal
separately kills a process after observing a database claim and records the
time to durable success. That experiment does not claim to hit the exact
HTTP-success-before-commit instruction.

## Tradeoff and remaining limit

Recovery waits for lease/grace/reclaim timing instead of immediately stealing
work. This costs recovery latency but reduces concurrent duplicate execution.
Long-lived pauses or uncertain network outcomes can still produce repeated
HTTP calls. A receiver that ignores idempotency can repeat a payment, email,
or other side effect; the dispatcher alone cannot prevent it.

The design remains a modular monolith because these are transaction,
ownership, and failure-boundary problems, not evidence that another broker or
several new services are necessary.
