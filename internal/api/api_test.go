package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/api"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/crypto"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/observability"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/service"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/testutil"
)

const testPayloadMaxBytes = 4096

func testServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	return testServerWithOptions(t, false, "", true)
}

// testServerWithEventLimits builds a server whose event service has an
// admission-control bound, for the backpressure tests.
func testServerWithEventLimits(t *testing.T, maxPending int) *httptest.Server {
	t.Helper()
	ts, _ := testServerWithOptionsN(t, false, "", true, maxPending)
	return ts
}

func testServerWithOptions(t *testing.T, authEnabled bool, apiKey string, allowPrivateTargets bool) (*httptest.Server, *sql.DB) {
	t.Helper()
	return testServerWithOptionsN(t, authEnabled, apiKey, allowPrivateTargets, 0)
}

func testServerWithOptionsN(t *testing.T, authEnabled bool, apiKey string, allowPrivateTargets bool, maxPending int) (*httptest.Server, *sql.DB) {
	t.Helper()

	db := testutil.OpenDB(t)

	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	secretBox, err := crypto.NewSecretBox(bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatalf("NewSecretBox() error = %v", err)
	}

	targetStore := store.NewTargetStore(db)
	eventStore := store.NewEventStore(db)
	targetService := service.NewTargetService(targetStore, secretBox, allowPrivateTargets)
	eventService := service.NewEventService(eventStore, targetStore, maxPending)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(targetService, eventService, db, logger, testPayloadMaxBytes, authEnabled, apiKey, observability.NewMetrics())

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return ts, db
}

func createTarget(t *testing.T, ts *httptest.Server) map[string]any {
	t.Helper()
	body := `{"name":"example-service","url":"http://127.0.0.1:9090/webhook","secret":"shh"}`
	resp, err := http.Post(ts.URL+"/api/v1/targets", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /targets error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /targets status = %d, body = %s", resp.StatusCode, b)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode target response: %v", err)
	}
	return out
}

func TestHealthz(t *testing.T) {
	ts, _ := testServer(t)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestMalformedResourceIDsReturnValidationError(t *testing.T) {
	ts, _ := testServer(t)
	for _, tc := range []struct{ method, path string }{{"GET", "/api/v1/targets/invalid"}, {"DELETE", "/api/v1/targets/invalid"}, {"GET", "/api/v1/events/invalid"}, {"GET", "/api/v1/events/invalid/attempts"}, {"POST", "/api/v1/events/invalid/retry"}} {
		req, _ := http.NewRequest(tc.method, ts.URL+tc.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 422 {
			t.Errorf("%s %s: %d", tc.method, tc.path, resp.StatusCode)
		}
	}
}

func TestCreateTargetRejectsOversizedAndTrailingJSON(t *testing.T) {
	ts, _ := testServer(t)
	for _, tc := range []struct {
		body string
		want int
	}{{`{"name":"` + strings.Repeat("x", testPayloadMaxBytes) + `"}`, 413}, {`{"name":"one"} {"name":"two"}`, 400}, {`{"name":"` + strings.Repeat("x", 201) + `","url":"https://example.com/webhook"}`, 422}} {
		resp, err := http.Post(ts.URL+"/api/v1/targets", "application/json", strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("status=%d want=%d", resp.StatusCode, tc.want)
		}
	}
}

func TestDatabaseOutageReturnsSafeErrorsAndKeepsLiveness(t *testing.T) {
	ts, db := testServer(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(ts.URL+"/api/v1/targets", "application/json", strings.NewReader(`{"name":"no-phantom","url":"http://127.0.0.1:9090/webhook"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 500 || !bytes.Contains(body, []byte(`"INTERNAL_ERROR"`)) || bytes.Contains(body, []byte("database is closed")) {
		t.Fatalf("unsafe outage error: %d %s", resp.StatusCode, body)
	}
	for _, tc := range []struct {
		path string
		want int
	}{{"/healthz", 200}, {"/readyz", 503}} {
		resp, err := http.Get(ts.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s: %d", tc.path, resp.StatusCode)
		}
	}
}

func TestReadyz(t *testing.T) {
	ts, _ := testServer(t)
	resp, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestMetrics(t *testing.T) {
	ts, _ := testServer(t)
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read metrics response: %v", err)
	}
	if !strings.Contains(string(body), "webhook_events_created_total") {
		t.Errorf("metrics response missing event counter: %s", body)
	}
}

func TestCreateTarget_DoesNotEchoSecret(t *testing.T) {
	ts, _ := testServer(t)
	out := createTarget(t, ts)
	if _, ok := out["secret"]; ok {
		t.Error("response must not echo the secret")
	}
	if out["target_id"] == "" || out["target_id"] == nil {
		t.Error("expected a target_id")
	}
	if out["circuit_state"] != "CLOSED" {
		t.Errorf("circuit_state = %v, want CLOSED", out["circuit_state"])
	}
}

func TestCreateTarget_InvalidURLRejected(t *testing.T) {
	ts, _ := testServer(t)
	body := `{"name":"bad","url":"not-a-url"}`
	resp, err := http.Post(ts.URL+"/api/v1/targets", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /targets error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.StatusCode)
	}
}

func TestCreateTarget_PublicModeRejectsLoopbackDestination(t *testing.T) {
	ts, _ := testServerWithOptions(t, false, "", false)
	body := `{"name":"private","url":"http://127.0.0.1:9090/webhook"}`
	resp, err := http.Post(ts.URL+"/api/v1/targets", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /targets error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.StatusCode)
	}
}

func TestAPIKeyAuthenticationProtectsAPIRoutesButNotHealth(t *testing.T) {
	ts, _ := testServerWithOptions(t, true, "test-api-key", true)

	unauthenticated, err := http.Get(ts.URL + "/api/v1/targets/00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("unauthenticated API request: %v", err)
	}
	defer unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated status = %d, want 401", unauthenticated.StatusCode)
	}

	authenticated, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/targets/00000000-0000-0000-0000-000000000000", nil)
	if err != nil {
		t.Fatalf("new authenticated request: %v", err)
	}
	authenticated.Header.Set("X-API-Key", "test-api-key")
	resp, err := http.DefaultClient.Do(authenticated)
	if err != nil {
		t.Fatalf("authenticated API request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("authenticated status = %d, want 404", resp.StatusCode)
	}

	health, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("health request: %v", err)
	}
	defer health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Errorf("health status = %d, want 200", health.StatusCode)
	}
}

func TestGetTarget_NotFound(t *testing.T) {
	ts, _ := testServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/targets/00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("GET /targets/{id} error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestCreateEvent_PersistsAndIsRetrievable(t *testing.T) {
	ts, _ := testServer(t)
	target := createTarget(t, ts)
	targetID := target["target_id"].(string)

	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+fmt.Sprintf("/api/v1/targets/%s/events", targetID),
		strings.NewReader(`{"payload":{"type":"invoice.created","id":"inv_123"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key-1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /events error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, b)
	}
	var created map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created["status"] != "PENDING" {
		t.Errorf("status = %v, want PENDING", created["status"])
	}

	eventID := created["event_id"].(string)
	getResp, err := http.Get(ts.URL + "/api/v1/events/" + eventID)
	if err != nil {
		t.Fatalf("GET /events/{id} error = %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Errorf("GET status = %d, want 200", getResp.StatusCode)
	}

	attemptsResp, err := http.Get(ts.URL + "/api/v1/events/" + eventID + "/attempts")
	if err != nil {
		t.Fatalf("GET /events/{id}/attempts error = %v", err)
	}
	defer attemptsResp.Body.Close()
	if attemptsResp.StatusCode != http.StatusOK {
		t.Errorf("attempts status = %d, want 200", attemptsResp.StatusCode)
	}
}

func TestCreateEvent_DuplicateIdempotencyKeyReturns409WithExistingEventID(t *testing.T) {
	ts, _ := testServer(t)
	target := createTarget(t, ts)
	targetID := target["target_id"].(string)
	url := ts.URL + fmt.Sprintf("/api/v1/targets/%s/events", targetID)

	postEvent := func(payload string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "dup-key")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /events error = %v", err)
		}
		return resp
	}

	first := postEvent(`{"payload":{"n":1}}`)
	defer first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first status = %d, want 201", first.StatusCode)
	}
	var firstBody map[string]any
	json.NewDecoder(first.Body).Decode(&firstBody)

	second := postEvent(`{"payload":{"n":2}}`)
	defer second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second status = %d, want 409", second.StatusCode)
	}
	var secondBody map[string]any
	if err := json.NewDecoder(second.Body).Decode(&secondBody); err != nil {
		t.Fatalf("decode conflict body: %v", err)
	}
	if secondBody["event_id"] != firstBody["event_id"] {
		t.Errorf("conflict event_id = %v, want %v", secondBody["event_id"], firstBody["event_id"])
	}
}

func TestCreateEvent_MissingIdempotencyKeyRejected(t *testing.T) {
	ts, _ := testServer(t)
	target := createTarget(t, ts)
	targetID := target["target_id"].(string)

	resp, err := http.Post(ts.URL+fmt.Sprintf("/api/v1/targets/%s/events", targetID),
		"application/json", strings.NewReader(`{"payload":{"n":1}}`))
	if err != nil {
		t.Fatalf("POST /events error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateEvent_RejectsNonJSONContentType(t *testing.T) {
	ts, _ := testServer(t)
	target := createTarget(t, ts)
	targetID := target["target_id"].(string)

	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+fmt.Sprintf("/api/v1/targets/%s/events", targetID), strings.NewReader(`{"payload":{"n":1}}`))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Idempotency-Key", "wrong-content-type")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /events error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateEvent_OversizedPayloadRejected(t *testing.T) {
	ts, _ := testServer(t)
	target := createTarget(t, ts)
	targetID := target["target_id"].(string)

	oversized := `{"payload":{"data":"` + strings.Repeat("x", testPayloadMaxBytes*2) + `"}}`
	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+fmt.Sprintf("/api/v1/targets/%s/events", targetID), strings.NewReader(oversized))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "big-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /events error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

func TestDeleteTarget_DisablesWithoutDeletingHistory(t *testing.T) {
	ts, db := testServer(t)
	target := createTarget(t, ts)
	targetID := target["target_id"].(string)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/targets/"+targetID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /targets/{id} error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}

	var enabled bool
	if err := db.QueryRow(`SELECT enabled FROM targets WHERE id = $1`, targetID).Scan(&enabled); err != nil {
		t.Fatalf("query target: %v", err)
	}
	if enabled {
		t.Error("expected target to be disabled, row still present (good) but enabled=true")
	}

	// A disabled target must still reject new events.
	req2, _ := http.NewRequest(http.MethodPost,
		ts.URL+fmt.Sprintf("/api/v1/targets/%s/events", targetID), strings.NewReader(`{"payload":{"n":1}}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", "after-disable")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("POST /events error = %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for disabled target", resp2.StatusCode)
	}
}

func TestReplayEvent_DLQEventIsResetForRetry(t *testing.T) {
	ts, db := testServer(t)
	target := createTarget(t, ts)
	targetID := target["target_id"].(string)

	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+fmt.Sprintf("/api/v1/targets/%s/events", targetID), strings.NewReader(`{"payload":{"n":1}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "replay-key-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /events error = %v", err)
	}
	defer resp.Body.Close()
	var created map[string]any
	json.NewDecoder(resp.Body).Decode(&created)
	eventID := created["event_id"].(string)

	// Drive it to DLQ directly, simulating exhausted retries.
	if _, err := db.Exec(`UPDATE events SET status = 'DLQ', attempt_count = 5 WHERE id = $1`, eventID); err != nil {
		t.Fatalf("force DLQ: %v", err)
	}

	replayResp, err := http.Post(ts.URL+"/api/v1/events/"+eventID+"/retry", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /events/{id}/retry error = %v", err)
	}
	defer replayResp.Body.Close()
	if replayResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(replayResp.Body)
		t.Fatalf("status = %d, body = %s", replayResp.StatusCode, b)
	}
	var replayed map[string]any
	if err := json.NewDecoder(replayResp.Body).Decode(&replayed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if replayed["status"] != "RETRY_WAIT" {
		t.Errorf("status = %v, want RETRY_WAIT", replayed["status"])
	}
	if replayed["attempt_count"] != float64(0) {
		t.Errorf("attempt_count = %v, want 0", replayed["attempt_count"])
	}
}

func TestReplayEvent_NotEligibleWhenNotDLQ(t *testing.T) {
	ts, _ := testServer(t)
	target := createTarget(t, ts)
	targetID := target["target_id"].(string)

	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+fmt.Sprintf("/api/v1/targets/%s/events", targetID), strings.NewReader(`{"payload":{"n":1}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "replay-key-2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /events error = %v", err)
	}
	defer resp.Body.Close()
	var created map[string]any
	json.NewDecoder(resp.Body).Decode(&created)
	eventID := created["event_id"].(string)

	// Still PENDING - never reached DLQ.
	replayResp, err := http.Post(ts.URL+"/api/v1/events/"+eventID+"/retry", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /events/{id}/retry error = %v", err)
	}
	defer replayResp.Body.Close()
	if replayResp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", replayResp.StatusCode)
	}
}

func TestReplayEvent_NotFound(t *testing.T) {
	ts, _ := testServer(t)
	resp, err := http.Post(ts.URL+"/api/v1/events/00000000-0000-0000-0000-000000000000/retry", "application/json", nil)
	if err != nil {
		t.Fatalf("POST error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
