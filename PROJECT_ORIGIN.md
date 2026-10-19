# Project Origin and Refinement Notes

This project originated from the backend-focused webhook infrastructure concept reviewed during project selection.

The original concept emphasized:

- asynchronous event delivery;
- worker pools;
- PostgreSQL;
- Redis/RabbitMQ;
- retries and exponential backoff;
- dead-letter handling;
- circuit breaking;
- idempotency;
- Prometheus/Grafana;
- load testing;
- failure scenarios.

This handoff intentionally refines it in the following ways:

1. **Redis Streams is the baseline queue** to reduce setup and dependency count.
2. **PostgreSQL + outbox is the durable boundary**, avoiding an unsafe DB/queue dual write.
3. **At-least-once + idempotency** is explicit rather than an unrealistic exactly-once claim.
4. **A deterministic target simulator** is part of the test strategy so failures are reproducible.
5. **The architecture is a modular monolith**, not a collection of artificial microservices.
6. **Cloud deployment is a single-VM extension**, not Kubernetes by default.
7. **AI/ML is intentionally excluded** because it does not strengthen this specific backend problem.
8. **Benchmarking and failure injection are treated as first-class deliverables**, not optional demo polish.
