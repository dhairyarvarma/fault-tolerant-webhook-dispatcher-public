package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func createEventFor(t *testing.T, baseURL, targetID, key string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest("POST", baseURL+"/api/v1/targets/"+targetID+"/events", strings.NewReader(`{"payload":{"k":"v"}}`))
	if err != nil {
		t.Fatalf("build create-event request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func TestListEvents_FiltersByStatus(t *testing.T) {
	ts, _ := testServer(t)
	target := createTarget(t, ts)
	if target["target_id"] == nil {
		t.Fatalf("createTarget response has no target_id: %v", target)
	}
	targetID := target["target_id"].(string)

	for i := range 2 {
		if code, body := createEventFor(t, ts.URL, targetID, fmt.Sprintf("list-key-%d", i)); code != http.StatusCreated {
			t.Fatalf("create event %d status = %d, body = %s", i, code, body)
		}
	}

	resp, err := http.Get(ts.URL + "/api/v1/events?status=PENDING")
	if err != nil {
		t.Fatalf("GET /events error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /events?status=PENDING status = %d, body = %s", resp.StatusCode, b)
	}
	var out struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Events) < 2 {
		t.Errorf("got %d PENDING events, want >= 2", len(out.Events))
	}
	for _, ev := range out.Events {
		if ev["status"] != "PENDING" {
			t.Errorf("status = %v, want PENDING", ev["status"])
		}
	}
}

func TestListEvents_InvalidStatus(t *testing.T) {
	ts, _ := testServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/events?status=BOGUS")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.StatusCode)
	}
}

func TestListEvents_NonIntegerLimit(t *testing.T) {
	ts, _ := testServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/events?status=PENDING&limit=abc")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAdmissionBackpressure_RejectsWith429(t *testing.T) {
	// The admission bound is global (durable pending rows across all
	// targets, DECISIONS.md #28), and this shared test database retains
	// PENDING rows from other tests (no delivery workers run here), so the
	// limit is derived from the live count rather than hardcoded.
	dbTS, db := testServer(t)
	var pending int
	row := db.QueryRow(`SELECT COUNT(*) FROM events WHERE status IN ('PENDING','QUEUED','RETRY_WAIT')`)
	if err := row.Scan(&pending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	_ = dbTS

	limit := pending + 2
	ts := testServerWithEventLimits(t, limit)
	target := createTarget(t, ts)
	targetID := target["target_id"].(string)

	for i := 0; i < 2; i++ {
		key := fmt.Sprintf("bp-key-%d", i)
		if code, body := createEventFor(t, ts.URL, targetID, key); code != http.StatusCreated {
			t.Fatalf("create %d (limit=%d) status = %d, body = %s", i, limit, code, body)
		}
	}

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/targets/"+targetID+"/events", strings.NewReader(`{"payload":{"k":"v"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "bp-key-2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("over-limit create status = %d, body = %s", resp.StatusCode, b)
	}
	if ra := resp.Header.Get("Retry-After"); ra == "" {
		t.Error("Retry-After header missing on 429")
	}
	var env map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&env)
	errObj, _ := env["error"].(map[string]any)
	if errObj["code"] != "OVERLOADED" {
		t.Errorf("code = %v, want OVERLOADED", errObj["code"])
	}
}

func TestAdmissionBackpressure_DisabledByDefault(t *testing.T) {
	ts, _ := testServer(t)
	target := createTarget(t, ts)
	targetID := target["target_id"].(string)
	for i := range 5 {
		if code, body := createEventFor(t, ts.URL, targetID, fmt.Sprintf("nolimit-%d", i)); code != http.StatusCreated {
			t.Fatalf("create %d status = %d, body = %s", i, code, body)
		}
	}
}
