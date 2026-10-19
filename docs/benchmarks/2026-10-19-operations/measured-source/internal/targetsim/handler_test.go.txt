package targetsim

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleStatus_ReturnsRequestedCode(t *testing.T) {
	srv := httptest.NewServer(NewHandler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/sim/status/503")
	if err != nil {
		t.Fatalf("GET error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestHandleLatency_SleepsBeforeResponding(t *testing.T) {
	srv := httptest.NewServer(NewHandler())
	defer srv.Close()

	start := time.Now()
	resp, err := http.Get(srv.URL + "/sim/latency/100/200")
	if err != nil {
		t.Fatalf("GET error = %v", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("elapsed = %v, want >= 100ms", elapsed)
	}
}

func TestHandleHang_ClientTimesOut(t *testing.T) {
	srv := httptest.NewServer(NewHandler())
	defer srv.Close()

	client := &http.Client{Timeout: 100 * time.Millisecond}
	_, err := client.Get(srv.URL + "/sim/hang")
	if err == nil {
		t.Fatal("expected a client timeout error, got nil")
	}
}

func TestHandleHang_PostBodyDoesNotStrandCanceledHandler(t *testing.T) {
	done := make(chan struct{})
	handler := NewHandler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		handler.ServeHTTP(w, r.WithContext(ctx))
		close(done)
	}))
	defer srv.Close()
	client := http.Client{Timeout: 50 * time.Millisecond}
	resp, err := client.Post(srv.URL+"/sim/hang", "application/json", strings.NewReader(`{"test":true}`))
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected timeout")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("POST handler did not observe client cancellation")
	}
}

func TestHandleReset_ClosesConnectionWithoutResponse(t *testing.T) {
	srv := httptest.NewServer(NewHandler())
	defer srv.Close()

	_, err := http.Get(srv.URL + "/sim/reset")
	if err == nil {
		t.Fatal("expected a connection error, got a successful response")
	}
}

func TestHandleFlaky_FailsNTimesThenSucceeds(t *testing.T) {
	srv := httptest.NewServer(NewHandler())
	defer srv.Close()

	url := srv.URL + "/sim/flaky/" + fmt.Sprintf("test-%d", time.Now().UnixNano()) + "?fail=2&failStatus=500&okStatus=200"

	for i, want := range []int{500, 500, 200, 200} {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("request %d: GET error = %v", i+1, err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("request %d: status = %d, want %d", i+1, resp.StatusCode, want)
		}
	}
}

func TestHandleFlaky_DifferentNamesAreIndependent(t *testing.T) {
	srv := httptest.NewServer(NewHandler())
	defer srv.Close()

	base := fmt.Sprintf("test-%d", time.Now().UnixNano())
	urlA := srv.URL + "/sim/flaky/" + base + "-a?fail=5&failStatus=500"
	urlB := srv.URL + "/sim/flaky/" + base + "-b?fail=0&okStatus=201"

	respA, _ := http.Get(urlA)
	respA.Body.Close()
	if respA.StatusCode != 500 {
		t.Errorf("name A status = %d, want 500 (independent counters)", respA.StatusCode)
	}

	respB, _ := http.Get(urlB)
	respB.Body.Close()
	if respB.StatusCode != 201 {
		t.Errorf("name B status = %d, want 201 (independent counters)", respB.StatusCode)
	}
}

func TestHandleIdempotent_FirstKeyCreatedThenRepeated(t *testing.T) {
	srv := httptest.NewServer(NewHandler())
	defer srv.Close()

	url := srv.URL + "/sim/idempotent/" + fmt.Sprintf("test-%d", time.Now().UnixNano())

	req1, _ := http.NewRequest(http.MethodPost, url, nil)
	req1.Header.Set("Idempotency-Key", "key-1")
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("first request error = %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusCreated {
		t.Errorf("first request status = %d, want 201", resp1.StatusCode)
	}

	req2, _ := http.NewRequest(http.MethodPost, url, nil)
	req2.Header.Set("Idempotency-Key", "key-1")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("second request error = %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("repeat request status = %d, want 200", resp2.StatusCode)
	}

	req3, _ := http.NewRequest(http.MethodPost, url, nil)
	req3.Header.Set("Idempotency-Key", "key-2")
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatalf("third request error = %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusCreated {
		t.Errorf("new key status = %d, want 201 (different key is independent)", resp3.StatusCode)
	}
}
