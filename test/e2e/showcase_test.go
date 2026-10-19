package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Only run on a dedicated instance with the documented shortened demo settings.
func TestE2E_Showcase(t *testing.T) {
	if os.Getenv("TEST_SHOWCASE") != "1" {
		t.Skip("run go run ./cmd/acceptance -demo for the isolated showcase")
	}
	base := baseURL(t)
	eventID := func(target, key string) string {
		t.Helper()
		code, body := createEvent(t, base, target, key)
		var event struct {
			ID string `json:"event_id"`
		}
		if code != 201 || json.Unmarshal(body, &event) != nil || event.ID == "" {
			t.Fatalf("event creation failed: %d %s", code, body)
		}
		return event.ID
	}
	circuit := func(target, want string) {
		t.Helper()
		code, _, body := doJSON(t, "GET", base+"/api/v1/targets/"+target, nil, "")
		var state struct {
			Circuit string `json:"circuit_state"`
		}
		if code != 200 || json.Unmarshal(body, &state) != nil || state.Circuit != want {
			t.Fatalf("want circuit %s: %d %s", want, code, body)
		}
	}

	t.Log("1. Accept an event asynchronously and inspect its durable outcome.")
	target := createTarget(t, base, targetURL("/sim/status/200"))
	first := eventID(target, "showcase-success")
	waitStatus(t, base, first, "SUCCEEDED", 15*time.Second)
	t.Logf("   event=%s -> SUCCEEDED", first)
	code, body := createEvent(t, base, target, "showcase-success")
	var duplicate struct {
		ID string `json:"event_id"`
	}
	if code != 409 || json.Unmarshal(body, &duplicate) != nil || duplicate.ID != first {
		t.Fatalf("duplicate did not preserve identity: %d %s", code, body)
	}
	t.Log("2. Reusing the key returns 409 and the SAME event ID; no second logical event.")

	flaky := createTarget(t, base, targetURL(fmt.Sprintf("/sim/flaky/showcase-%d?fail=3&failStatus=500&okStatus=200", time.Now().UnixNano())))
	failed := eventID(flaky, "showcase-failure")
	state := waitStatus(t, base, failed, "DLQ", 15*time.Second)
	if state["attempt_count"] != float64(3) {
		t.Fatalf("want exactly 3 failed attempts: %v", state)
	}
	circuit(flaky, "OPEN")
	t.Logf("3. event=%s -> DLQ after 3 HTTP 500 attempts; circuit=OPEN.", failed)
	deferred := eventID(flaky, "showcase-deferred")
	state = waitStatus(t, base, deferred, "RETRY_WAIT", 3*time.Second)
	if state["attempt_count"] != float64(0) {
		t.Fatalf("open circuit consumed retry budget: %v", state)
	}
	t.Log("4. New event is deferred with ZERO attempts while the circuit is open.")
	waitStatus(t, base, deferred, "SUCCEEDED", 15*time.Second)
	circuit(flaky, "CLOSED")
	t.Log("5. After cooldown, a successful probe closes the circuit; deferred event succeeds.")

	code, _, body = doJSON(t, "POST", base+"/api/v1/events/"+failed+"/retry", nil, "")
	if code != 200 {
		t.Fatalf("replay: %d %s", code, body)
	}
	waitStatus(t, base, failed, "SUCCEEDED", 15*time.Second)
	code, _, body = doJSON(t, "GET", base+"/api/v1/events/"+failed+"/attempts", nil, "")
	var history struct {
		Attempts []struct {
			Outcome string `json:"outcome"`
		} `json:"attempts"`
	}
	if code != 200 || json.Unmarshal(body, &history) != nil || len(history.Attempts) != 4 || history.Attempts[0].Outcome != "SUCCESS" {
		t.Fatalf("replay lost attempt history: %d %s", code, body)
	}
	t.Log("6. Replay succeeds with the SAME event ID and all 4 historical attempts retained.")
	code, _, body = doJSON(t, "GET", base+"/metrics", nil, "")
	if code != 200 || !strings.Contains(string(body), "webhook_delivery_success_total") {
		t.Fatalf("metrics unavailable: %d", code)
	}
	t.Log("7. Metrics are exposed. At-least-once delivery still permits duplicate HTTP after a crash.")
}
