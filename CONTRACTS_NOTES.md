# Contract Notes

These notes explain choices behind the API contract.

## Why a target resource exists

A target owns the destination URL and secret. Events reference a target rather than duplicating destination configuration on every event.

## Why idempotency is caller-supplied

Callers need a stable operation key when they may retry the event-creation request themselves. The database uniqueness constraint turns that key into a durable guard.

## Why event payloads are JSON-only initially

JSON keeps the first implementation focused on delivery infrastructure. Binary/multipart webhook payloads are outside baseline scope.

## Why manual retry exists

A DLQ without a recovery path is operationally incomplete. Manual replay also creates a concrete interview discussion around safe state transitions.
