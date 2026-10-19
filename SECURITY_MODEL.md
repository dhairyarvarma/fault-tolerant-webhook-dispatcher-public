# Security Model

Webhook delivery creates an important outbound-request attack surface because users can influence target destinations.

## Threats

- SSRF against localhost or private network services.
- Header injection.
- Oversized payloads.
- Credential leakage in logs.
- Secret leakage through APIs.
- Unbounded retry amplification.
- Malicious redirects.
- Resource exhaustion through too many concurrent deliveries.
- Unsafe response-body logging.

## Required controls

### SSRF protection

- Only allow `http`/`https` schemes.
- Reject invalid/ambiguous URLs.
- Reject private, loopback, link-local, multicast, and other non-public destination ranges in public mode.
- Resolve hostnames and validate destination addresses as part of the outbound connection policy.
- Treat redirects as a new destination that must also pass the outbound policy.
- Local development may explicitly enable private targets for the bundled test-service, but the mode must be visibly configured and never be the production default.

Public mode conservatively excludes special-purpose IPv4 ranges and IPv6
protocol/documentation/transition ranges; IPv6 support is limited to
2000::/3. This is a deliberate policy, including rejection of some specialized
reachable addresses. IsGlobalUnicast alone does not establish that a
destination is appropriate for public webhooks. References reviewed for the
checked-in ranges: [IANA IPv4 special-purpose registry](https://www.iana.org/assignments/iana-ipv4-special-registry)
and [IANA IPv6 special-purpose registry](https://www.iana.org/assignments/iana-ipv6-special-registry).
Review the policy when adding specialized networking support.

### Header controls

Do not permit arbitrary hop-by-hop or connection-management headers. Use a documented allowlist for client-controlled outbound headers.

### Payload limits

Set a configurable maximum request payload size and bounded response capture size.

### Timeouts

Configure connect and overall request timeouts. No outbound request should be allowed to hang indefinitely.

### Secrets

Webhook secrets are sensitive. Do not log them, return them in API responses, or commit them to source control.

### Delivery signatures

Configured target secrets are decrypted in memory and used as literal UTF-8
HMAC keys (no automatic base64/hex decoding). The signed bytes are:

```text
<Unix seconds>.<event UUID>.<exact HTTP body bytes>
```

`X-Webhook-Id` contains the event UUID, `X-Webhook-Timestamp` contains the
Unix timestamp, and `X-Webhook-Signature` is `v1=<HMAC-SHA256 lowercase hex>`.
Verify raw bytes before JSON parsing using constant-time comparison and an
appropriate clock tolerance (the example uses +/-5 minutes). A timestamp
does not replace durable target-side idempotency. The Idempotency-Key stays
unchanged across retries; timestamps/signatures may change. Targets without
a configured secret are unsigned. Signing/decryption failures make no HTTP
call and are classified as non-retryable configuration failures.

Caller idempotency keys are unique per target, not globally across target
registrations. Receivers can deduplicate by the stable X-Webhook-Id, or by
an appropriately scoped idempotency key, atomically with the business effect.

`go run ./examples/receiver` starts a bounded verification-only receiver on
127.0.0.1:9092 with WEBHOOK_SECRET supplied through the environment. Use a
native local API with ALLOW_PRIVATE_TARGETS=true to exercise it. Production
receivers need TLS and durable business-operation deduplication.

### Runtime bounds

Configuration rejects invalid booleans, nonpositive worker/poll/circuit
settings, invalid retry bounds, and request timeouts exceeding one minute.
Private-target mode is rejected outside the development environment.
Connect timeout cannot exceed request timeout. Inbound header/body/write/idle
timeouts are explicit. The image runs as a non-root numeric user and installs
CA certificates; local secrets/caches are excluded by .dockerignore. Image
build verification must be recorded separately when Docker access is available.

### Authentication

Local-only development can be unauthenticated. Non-local deployment must use an API credential mechanism.

### Rate limiting

If API rate limiting is implemented, keep it focused on protecting the ingestion endpoint and expensive operational endpoints. Do not add a complex adaptive gateway unless a measured requirement appears.

### Logging

Never log:

- API keys
- webhook secrets
- Authorization headers
- full request payloads by default
- arbitrary downstream response bodies

Use request IDs for correlation.

Malformed PostgreSQL and Redis connection strings produce static configuration
errors; parser diagnostics are not logged because they may contain credentials.
