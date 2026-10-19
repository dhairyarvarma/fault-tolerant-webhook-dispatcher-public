package delivery

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

func testEvent() store.Event {
	return store.Event{
		ID:             "event-1",
		IdempotencyKey: "key-1",
		PayloadJSON:    []byte(`{"type":"invoice.created"}`),
	}
}

func TestClient_Deliver_Success(t *testing.T) {
	var gotIdempotencyKey string
	var gotAuthorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIdempotencyKey = r.Header.Get("Idempotency-Key")
		gotAuthorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(2*time.Second, 2*time.Second, 1024, true)
	result := client.Deliver(t.Context(), store.Target{URL: srv.URL}, testEvent())

	if !result.Success {
		t.Fatalf("Success = false, want true; error = %v", result.ErrorMessage)
	}
	if *result.HTTPStatus != http.StatusOK {
		t.Errorf("HTTPStatus = %d, want 200", *result.HTTPStatus)
	}
	if gotIdempotencyKey != "key-1" {
		t.Errorf("Idempotency-Key header = %q, want key-1", gotIdempotencyKey)
	}
	if gotAuthorization != "" {
		t.Errorf("Authorization header = %q, want no caller-controlled credentials forwarded", gotAuthorization)
	}
}

func TestClient_Deliver_HTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewClient(2*time.Second, 2*time.Second, 1024, true)
	result := client.Deliver(t.Context(), store.Target{URL: srv.URL}, testEvent())

	if result.Success {
		t.Fatal("Success = true, want false for 500 response")
	}
	if *result.HTTPStatus != http.StatusInternalServerError {
		t.Errorf("HTTPStatus = %d, want 500", *result.HTTPStatus)
	}
}

func TestClient_Deliver_TimeoutIsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(2*time.Second, 50*time.Millisecond, 1024, true)
	result := client.Deliver(t.Context(), store.Target{URL: srv.URL}, testEvent())

	if result.Success {
		t.Fatal("Success = true, want false for a request that exceeds the timeout")
	}
	if result.HTTPStatus != nil {
		t.Errorf("HTTPStatus = %v, want nil for a timeout", result.HTTPStatus)
	}
}

func TestClient_Deliver_ResponseBodyBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 1<<20)) // 1 MiB, far larger than the capture limit
	}))
	defer srv.Close()

	client := NewClient(2*time.Second, 2*time.Second, 16, true)
	result := client.Deliver(t.Context(), store.Target{URL: srv.URL}, testEvent())

	if !result.Success {
		t.Fatalf("expected success reading a bounded prefix of an oversized body, got error = %v", result.ErrorMessage)
	}
}

func TestClient_Deliver_PublicModeBlocksLoopbackAndRedactsTarget(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(2*time.Second, 2*time.Second, 1024, false)
	result := client.Deliver(t.Context(), store.Target{URL: srv.URL}, testEvent())

	if result.Success || result.ErrorClass == nil || *result.ErrorClass != "outbound_policy" {
		t.Fatalf("result = %+v, want outbound policy failure", result)
	}
	if got := atomic.LoadInt64(&calls); got != 0 {
		t.Errorf("target calls = %d, want 0", got)
	}
	if result.ErrorMessage == nil || *result.ErrorMessage != "destination rejected by outbound policy" {
		t.Errorf("ErrorMessage = %v, want redacted policy message", result.ErrorMessage)
	}
}

func TestClient_Deliver_DoesNotFollowRedirects(t *testing.T) {
	var redirectedCalls int64
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&redirectedCalls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	client := NewClient(2*time.Second, 2*time.Second, 1024, true)
	result := client.Deliver(t.Context(), store.Target{URL: source.URL}, testEvent())

	if result.Success || result.HTTPStatus == nil || *result.HTTPStatus != http.StatusTemporaryRedirect {
		t.Fatalf("result = %+v, want an unfollowed 307 response", result)
	}
	if got := atomic.LoadInt64(&redirectedCalls); got != 0 {
		t.Errorf("redirect destination calls = %d, want 0", got)
	}
}
