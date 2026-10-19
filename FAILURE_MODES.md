# Failure Mode Matrix

Every scenario below should eventually have an automated test or a reproducible test procedure.

| Failure | Expected behavior |
|---|---|
| Target returns 2xx | Mark delivery successful; acknowledge queue work |
| Target returns 400 | Mark attempt non-retryable according to policy |
| Target returns 401/403 | Non-retryable by default; record outcome safely |
| Target returns 404 | Non-retryable by default |
| Target returns 408 | Retry with backoff |
| Target returns 429 | Retry with backoff; preserve relevant retry timing if safely available |
| Target returns 500 | Retry with backoff |
| Target returns 503 | Retry with backoff |
| DNS failure | Retryable network failure |
| TCP connection timeout | Retryable network failure |
| HTTP request timeout | Retryable failure |
| Target closes connection | Retryable network failure |
| Worker crashes before HTTP call | Queue/work item must become recoverable |
| Worker crashes after HTTP success before DB update | Duplicate possible; idempotency protection must remain intact |
| Worker crashes after DB success before queue acknowledgement | Duplicate queue message must be harmless |
| Outbox relay crashes after Redis publish | Duplicate queue publication must be harmless |
| Redis unavailable | Do not lose durable events; relay/worker recovers later |
| PostgreSQL unavailable | API should fail safely; no phantom successful creation |
| Circuit breaker open | Avoid sending normal traffic to repeatedly failing target |
| Circuit half-open probe succeeds | Close circuit and resume normal delivery |
| Circuit half-open probe fails | Re-open circuit |
| Retry budget exhausted | Move event to `DLQ` with history intact |
| Manual replay requested | Re-enter delivery processing under documented replay semantics |
| Duplicate Idempotency-Key | Return/reuse existing logical event; never silently create another |
| Oversized payload | Reject before durable creation |
| Invalid URL | Reject before queueing |
| Private/loopback target in public mode | Reject according to SSRF policy |
| Malicious redirect to private IP | Follow redirect only under safe outbound policy; otherwise reject |
| Malformed target response | Record bounded failure safely |

## Failure injection requirements

The repository should contain a deterministic target simulator capable of:

- fixed status codes;
- configurable latency;
- timeout/hang behavior;
- connection reset simulation if practical;
- temporary failure for N requests followed by success;
- idempotency-aware success.

The goal is to make reliability tests repeatable, not dependent on a real third-party service.
