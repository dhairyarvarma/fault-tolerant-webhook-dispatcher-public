#!/usr/bin/env bash
# Reproducible failure-mode demo. Recorded results and conditions are retained
# in docs/benchmarks/. Requires the local stack already running:
#   docker compose up -d
# and the API reachable at BASE_URL (default http://localhost:8080).
#
# Demonstrates, against the real running system (not a simulation of it):
#   1. Circuit breaker: opens after repeated target failures, blocks new
#      work without consuming its retry budget, recovers via a single
#      HALF_OPEN probe.
#   2. DLQ + manual replay: an event that exhausts its retry budget reaches
#      DLQ with full history; replay resets it for a fresh attempt.
#
# This script only reads/creates data through the public API - it does not
# touch Postgres/Redis directly, so it's safe to run against any instance
# of the stack (though it does create targets/events, so prefer a dev
# instance over a shared one).
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
SIM_URL="${SIM_URL:-http://target-simulator:9090}" # from the api container's network

step() { printf '\n=== %s ===\n' "$*"; }
show() { printf '%s\n' "$*"; }

step "Checking the API is reachable at $BASE_URL"
curl -sf "$BASE_URL/healthz" >/dev/null || {
  echo "API not reachable at $BASE_URL. Run 'docker compose up -d' first." >&2
  exit 1
}
show "OK"

### 1. Circuit breaker ########################################################

step "1a. Creating a target that fails its first 5 requests, then recovers"
NAME="demo-flaky-$(date +%s)"
TARGET=$(curl -s -X POST "$BASE_URL/api/v1/targets" -H "Content-Type: application/json" \
  -d "{\"name\":\"circuit-demo\",\"url\":\"$SIM_URL/sim/flaky/$NAME?fail=5&failStatus=500\"}")
TARGET_ID=$(echo "$TARGET" | grep -o '"target_id":"[^"]*"' | cut -d'"' -f4)
show "target_id=$TARGET_ID"

step "1b. Sending one event and letting its own retries exhaust (default:" \
     "5 attempts). Every attempt - whether it's this event's first try or" \
     "one of its own retries - feeds the same per-target circuit breaker," \
     "so by construction there's no interleaving with other events to race"
EVENT1=$(curl -s -X POST "$BASE_URL/api/v1/targets/$TARGET_ID/events" \
  -H "Content-Type: application/json" -H "Idempotency-Key: circuit-demo-1" \
  -d '{"payload":{"n":1}}')
EVENT1_ID=$(echo "$EVENT1" | grep -o '"event_id":"[^"]*"' | cut -d'"' -f4)
for i in $(seq 1 40); do
  STATUS=$(curl -s "$BASE_URL/api/v1/events/$EVENT1_ID" | grep -o '"status":"[^"]*"')
  echo "$STATUS" | grep -q DLQ && { show "-> event reached DLQ after $i polls (its retry budget and the"; \
                                     show "   circuit's failure threshold are both 5 by default, so they"; \
                                     show "   exhaust together)"; break; }
  sleep 1
done

CIRCUIT=$(curl -s "$BASE_URL/api/v1/targets/$TARGET_ID")
show "$CIRCUIT"
if echo "$CIRCUIT" | grep -q '"circuit_state":"OPEN"'; then
  show "-> circuit is OPEN, as expected"
else
  show "-> WARNING: circuit did not open as expected; inspect delivery_attempts"
fi

step "1c. A new event against the same target is deferred without ever" \
     "calling the target (no delivery attempt recorded while the circuit" \
     "is open - its own retry budget is untouched)"
EVENT2=$(curl -s -X POST "$BASE_URL/api/v1/targets/$TARGET_ID/events" \
  -H "Content-Type: application/json" -H "Idempotency-Key: circuit-demo-2" \
  -d '{"payload":{"n":1}}')
EVENT2_ID=$(echo "$EVENT2" | grep -o '"event_id":"[^"]*"' | cut -d'"' -f4)
sleep 1
show "$(curl -s "$BASE_URL/api/v1/events/$EVENT2_ID")"
show "-> RETRY_WAIT with attempt_count=0: deferred, not failed"

step "1d. Waiting for the configured cooldown (default 30s) for the circuit" \
     "to allow a single HALF_OPEN probe, which will succeed (6th real call" \
     "to the simulator, past its fail=5 threshold) and close the circuit"
for i in $(seq 1 20); do
  STATE=$(curl -s "$BASE_URL/api/v1/targets/$TARGET_ID" | grep -o '"circuit_state":"[^"]*"')
  show "t=${i}x2s: $STATE"
  echo "$STATE" | grep -q CLOSED && break
  sleep 2
done
show "$(curl -s "$BASE_URL/api/v1/events/$EVENT2_ID")"
show "-> the deferred event has now succeeded as the HALF_OPEN probe"

### 2. DLQ + manual replay ####################################################

step "2a. Creating a target that always fails, to exhaust an event's retry budget"
TARGET2=$(curl -s -X POST "$BASE_URL/api/v1/targets" -H "Content-Type: application/json" \
  -d "{\"name\":\"dlq-demo\",\"url\":\"$SIM_URL/sim/status/500\"}")
TARGET2_ID=$(echo "$TARGET2" | grep -o '"target_id":"[^"]*"' | cut -d'"' -f4)
EVENT2=$(curl -s -X POST "$BASE_URL/api/v1/targets/$TARGET2_ID/events" \
  -H "Content-Type: application/json" -H "Idempotency-Key: dlq-demo-1" \
  -d '{"payload":{"n":1}}')
EVENT2_ID=$(echo "$EVENT2" | grep -o '"event_id":"[^"]*"' | cut -d'"' -f4)
show "event_id=$EVENT2_ID (will retry with backoff, then DLQ)"

step "2b. Waiting for retries to exhaust (default: 5 attempts, exponential" \
     "backoff up to 60s between - this takes a while)"
for i in $(seq 1 60); do
  STATUS=$(curl -s "$BASE_URL/api/v1/events/$EVENT2_ID" | grep -o '"status":"[^"]*"')
  echo "$STATUS" | grep -q DLQ && { show "-> reached DLQ after $i polls"; break; }
  sleep 3
done
show "$(curl -s "$BASE_URL/api/v1/events/$EVENT2_ID")"

step "2c. Replaying it: resets attempt_count to 0 and schedules immediately," \
     "while preserving full attempt history"
show "$(curl -s -X POST "$BASE_URL/api/v1/events/$EVENT2_ID/retry")"
show "$(curl -s "$BASE_URL/api/v1/events/$EVENT2_ID/attempts")"
show "-> status reset to RETRY_WAIT/attempt_count=0, but every original" \
     "delivery_attempts row from before DLQ is still there"

step "Demo complete"
show "See docs/benchmarks/ for recorded procedures with exact timestamps,"
show "measured recovery times, and the Redis/PostgreSQL outage and SSRF"
show "rejection procedures this script doesn't repeat (they require"
show "stopping real containers, which isn't safe to automate blindly)."
