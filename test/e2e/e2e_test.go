// Package e2e is a black-box acceptance suite: it only talks HTTP to a
// running webhook-dispatcher stack, exactly as a real client would. It
// imports nothing from internal/, so it can never accidentally lean on
// implementation details. Set TEST_BASE_URL (e.g.
// http://localhost:8080) to run it; it skips otherwise. The API must be
// running in a mode that accepts private targets (the Compose default)
// when pointing events at the bundled simulator.
package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func baseURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("TEST_BASE_URL")
	if u == "" {
		t.Skip("TEST_BASE_URL not set; skipping black-box e2e test")
	}
	return strings.TrimRight(u, "/")
}

func doJSON(t *testing.T, method, url string, headers map[string]string, body string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build %s %s: %v", method, url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key := os.Getenv("TEST_API_KEY"); key != "" {
		req.Header.Set("X-API-Key", key)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

func createTarget(t *testing.T, base, url string) string {
	t.Helper()
	body := fmt.Sprintf(`{"name":"e2e-%d","url":%q}`, time.Now().UnixNano(), url)
	code, _, b := doJSON(t, "POST", base+"/api/v1/targets", nil, body)
	if code != http.StatusCreated {
		t.Fatalf("create target: status %d, body %s", code, b)
	}
	var out struct {
		TargetID string `json:"target_id"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode target: %v", err)
	}
	return out.TargetID
}

func createEvent(t *testing.T, base, targetID, key string) (int, []byte) {
	t.Helper()
	// doJSON returns (status, header, body); callers only need status+body.
	status, _, b := doJSON(t, "POST", base+"/api/v1/targets/"+targetID+"/events",
		map[string]string{"Idempotency-Key": key}, `{"payload":{"type":"e2e.test"}}`)
	return status, b
}

func targetURL(path string) string {
	base := os.Getenv("TEST_TARGET_URL")
	if base == "" {
		base = "http://target-simulator:9090"
	}
	return strings.TrimRight(base, "/") + path
}

func waitStatus(t *testing.T, base, eventID string, want string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("event %s did not reach %s within %v", eventID, want, timeout)
		}
		code, _, b := doJSON(t, "GET", base+"/api/v1/events/"+eventID, nil, "")
		if code != http.StatusOK {
			t.Fatalf("GET event: %d, %s", code, b)
		}
		var ev map[string]any
		_ = json.Unmarshal(b, &ev)
		if ev["status"] == want {
			return ev
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestE2E_DeliveryLifecycle drives the core happy path end to end:
// target -> event -> asynchronous delivery -> SUCCEEDED, with attempt
// history recorded, using only the public API.
func TestE2E_DeliveryLifecycle(t *testing.T) {
	base := baseURL(t)
	targetID := createTarget(t, base, targetURL("/sim/status/200"))

	code, b := createEvent(t, base, targetID, "e2e-key")
	if code != http.StatusCreated {
		t.Fatalf("create event status = %d, body %s", code, b)
	}
	var ev struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(b, &ev); err != nil {
		t.Fatalf("decode event: %v", err)
	}

	final := waitStatus(t, base, ev.EventID, "SUCCEEDED", 30*time.Second)
	if n, ok := final["attempt_count"].(float64); !ok || n < 1 {
		t.Errorf("attempt_count = %v, want >= 1", final["attempt_count"])
	}

	code, _, b = doJSON(t, "GET", base+"/api/v1/events/"+ev.EventID+"/attempts", nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET attempts: %d, %s", code, b)
	}
	var envelope struct {
		Attempts []map[string]any `json:"attempts"`
	}
	_ = json.Unmarshal(b, &envelope)
	var attempts []map[string]any
	_ = json.Unmarshal(b, &attempts)
	if len(envelope.Attempts) < 1 && len(attempts) < 1 {
		t.Errorf("attempt history empty after success: %s", b)
	}
}

// TestE2E_IdempotencyConflict verifies the documented 409 behavior:
// replaying the same (target, Idempotency-Key) returns the existing
// event_id in the error envelope's sibling field.
func TestE2E_IdempotencyConflict(t *testing.T) {
	base := baseURL(t)
	targetID := createTarget(t, base, targetURL("/sim/status/200"))

	if code, _ := createEvent(t, base, targetID, "e2e-dup-key"); code != http.StatusCreated {
		t.Fatalf("first create status = %d", code)
	}
	code, b := createEvent(t, base, targetID, "e2e-dup-key")
	if code != http.StatusConflict {
		t.Fatalf("duplicate create status = %d, want 409, body %s", code, b)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if id, ok := out["event_id"].(string); !ok || id == "" {
		t.Errorf("409 body missing top-level event_id: %s", b)
	}
}

// TestE2E_ReplayNonDLQIsConflict verifies that replaying an event that
// has never reached DLQ returns 409, not a silent state change.
func TestE2E_ReplayNonDLQIsConflict(t *testing.T) {
	base := baseURL(t)
	targetID := createTarget(t, base, targetURL("/sim/status/200"))

	_, b := createEvent(t, base, targetID, "e2e-replay-key")
	var ev struct {
		EventID string `json:"event_id"`
	}
	_ = json.Unmarshal(b, &ev)

	waitStatus(t, base, ev.EventID, "SUCCEEDED", 30*time.Second)

	code, _, _ := doJSON(t, "POST", base+"/api/v1/events/"+ev.EventID+"/retry", nil, "")
	if code != http.StatusConflict {
		t.Errorf("replay of succeeded event status = %d, want 409", code)
	}
}

func TestE2E_DLQReplayRetainsHistory(t *testing.T) {
	base := baseURL(t)
	target := createTarget(t, base, targetURL(fmt.Sprintf("/sim/flaky/replay-%d?fail=1&failStatus=400&okStatus=200", time.Now().UnixNano())))
	code, body := createEvent(t, base, target, "dlq-replay")
	if code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	var event struct {
		ID string `json:"event_id"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, base, event.ID, "DLQ", 30*time.Second)
	code, _, body = doJSON(t, "POST", base+"/api/v1/events/"+event.ID+"/retry", nil, "")
	if code != 200 {
		t.Fatalf("replay: %d %s", code, body)
	}
	waitStatus(t, base, event.ID, "SUCCEEDED", 30*time.Second)
	code, _, body = doJSON(t, "GET", base+"/api/v1/events/"+event.ID+"/attempts", nil, "")
	var history struct {
		Attempts []map[string]any `json:"attempts"`
	}
	if err := json.Unmarshal(body, &history); err != nil {
		t.Fatal(err)
	}
	if code != 200 || len(history.Attempts) != 2 {
		t.Fatalf("replay history: %d %s", code, body)
	}
}

func TestE2E_DisabledTargetRejectsNewEvents(t *testing.T) {
	base := baseURL(t)
	target := createTarget(t, base, targetURL("/sim/status/200"))
	code, _, body := doJSON(t, "DELETE", base+"/api/v1/targets/"+target, nil, "")
	if code != 204 {
		t.Fatalf("disable: %d %s", code, body)
	}
	code, body = createEvent(t, base, target, "disabled")
	if code != 403 {
		t.Fatalf("disabled creation: %d %s", code, body)
	}
}

func TestE2E_AuthenticationAndHealth(t *testing.T) {
	base := baseURL(t)
	if os.Getenv("TEST_API_KEY") == "" {
		t.Skip("TEST_API_KEY required for auth checks")
	}
	for _, tc := range []struct {
		path string
		want int
	}{{"/api/v1/events?status=DLQ", 401}, {"/healthz", 200}, {"/readyz", 200}} {
		resp, err := (&http.Client{Timeout: 3 * time.Second}).Get(base + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s: %d want %d", tc.path, resp.StatusCode, tc.want)
		}
	}
}

func TestE2E_ConcurrentIdempotencyCreatesOneLogicalEvent(t *testing.T) {
	base := baseURL(t)
	target := createTarget(t, base, targetURL("/sim/status/200"))
	var wg sync.WaitGroup
	var created, conflicts atomic.Int64
	ids := make(chan string, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, body := createEvent(t, base, target, "concurrent-key")
			switch code {
			case 201:
				created.Add(1)
			case 409:
				conflicts.Add(1)
			default:
				t.Errorf("create: %d %s", code, body)
			}
			var event struct {
				ID string `json:"event_id"`
			}
			if err := json.Unmarshal(body, &event); err != nil {
				t.Error(err)
			}
			ids <- event.ID
		}()
	}
	wg.Wait()
	close(ids)
	if created.Load() != 1 || conflicts.Load() != 9 {
		t.Fatalf("created=%d conflicts=%d", created.Load(), conflicts.Load())
	}
	first := ""
	for id := range ids {
		if id == "" {
			t.Fatal("missing event identity")
		}
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("concurrent requests returned different logical events")
		}
	}
}

func TestE2E_SignedDeliveryPassesReceiverVerification(t *testing.T) {
	base := baseURL(t)
	destination := os.Getenv("TEST_SIGNING_TARGET_URL")
	if destination == "" {
		t.Skip("TEST_SIGNING_TARGET_URL required")
	}
	body := fmt.Sprintf(`{"name":"signed","url":%q,"secret":"acceptance-signing-secret"}`, destination)
	code, _, bodyBytes := doJSON(t, "POST", base+"/api/v1/targets", nil, body)
	if code != 201 {
		t.Fatalf("target: %d %s", code, bodyBytes)
	}
	var target struct {
		ID string `json:"target_id"`
	}
	if err := json.Unmarshal(bodyBytes, &target); err != nil {
		t.Fatal(err)
	}
	code, bodyBytes = createEvent(t, base, target.ID, "signed-key")
	if code != 201 {
		t.Fatalf("event: %d %s", code, bodyBytes)
	}
	var event struct {
		ID string `json:"event_id"`
	}
	if err := json.Unmarshal(bodyBytes, &event); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, base, event.ID, "SUCCEEDED", 30*time.Second)
}
