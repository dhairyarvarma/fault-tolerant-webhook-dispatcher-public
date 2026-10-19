# Repository Agent Instructions

## Mission

Build the Fault-Tolerant Webhook Delivery Engine specified by the repository documents.

## Read first

Before changing code, read:

- `PROJECT_SPEC.md`
- `ARCHITECTURE.md`
- `QUEUE_AND_DELIVERY_SEMANTICS.md`
- `API_SPEC.md`
- `DATABASE_DESIGN.md`
- `FAILURE_MODES.md`
- `IMPLEMENTATION_PLAN.md`
- `TEST_PLAN.md`
- `BENCHMARK_PLAN.md`
- `SECURITY_MODEL.md`
- `OBSERVABILITY.md`

## Non-negotiable engineering rules

1. Do not add AI/ML/LLM functionality. This is a backend/SWE project.
2. Do not add Kubernetes, service meshes, Kafka, RabbitMQ, Terraform, or additional microservices merely to increase resume buzzwords.
3. Treat PostgreSQL as the durable source of truth.
4. Treat Redis Streams as asynchronous work transport, not the only durable record of an event.
5. Delivery semantics are at-least-once with idempotency protection; never claim exactly-once end-to-end delivery.
6. Keep business logic independent from transport/HTTP handlers.
7. Prefer simple, explicit designs over speculative abstraction.
8. Do not silently change API, database, queue, or correctness semantics. Update the relevant documentation and `DECISIONS.md` first.
9. Never fabricate benchmark results, test coverage, throughput, latency, reliability, or failure-recovery numbers.
10. Never commit secrets, credentials, private keys, or real `.env` files.

## Development behavior

For each milestone:

1. Inspect the existing repository.
2. Read the milestone requirements and acceptance criteria.
3. Implement the smallest coherent change.
4. Add or update tests before declaring the milestone complete.
5. Run formatting, linting/static checks, unit tests, integration tests relevant to the change, and build checks.
6. Review the diff for accidental scope expansion.
7. Update documentation if behavior or architecture changed.
8. Update `TASKS.md` only with verified progress.

## Ambiguity rule

If ambiguity is blocking or could change correctness semantics, stop and report it. If it is non-blocking, choose the simplest option consistent with the existing documents and record the decision.

## Failure-first rule

When implementing a component that interacts with external systems, explicitly model failure before the happy path is considered complete.

## Security rule

Treat webhook targets as untrusted outbound destinations. Follow `SECURITY_MODEL.md`, especially SSRF controls, request size limits, header restrictions, secrets handling, and timeout requirements.

## Commit discipline

Use small, coherent commits. Do not rewrite unrelated code. Before any major milestone, ensure the working tree has no unexplained changes.

## Final completion rule

A milestone is not complete because the code compiles. It is complete only when its acceptance criteria and validation commands pass.
