package delivery

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

type resolverFailure struct{}

func (resolverFailure) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return nil, &net.DNSError{Err: "temporary resolution failure", Name: "receiver.invalid", IsTemporary: true}
}

func TestClient_DNSFailureIsRetryableNetworkClass(t *testing.T) {
	client := NewClient(time.Second, time.Second, 1024, false)
	client.policy.Resolver = resolverFailure{}
	result := client.Deliver(t.Context(), store.Target{URL: "https://receiver.invalid/webhook"}, testEvent())
	if result.Success || result.ErrorClass == nil || *result.ErrorClass != "network_error" || result.HTTPStatus != nil {
		t.Fatalf("DNS result=%+v", result)
	}
}

func TestClient_ConnectTimeoutIsNetworkFailure(t *testing.T) {
	// Local target validates normally; inject the dial boundary failure itself.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected HTTP request") }))
	defer srv.Close()
	client := NewClient(time.Second, time.Second, 1024, true)
	client.httpClient.Transport.(*http.Transport).DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}
	}
	result := client.Deliver(t.Context(), store.Target{URL: srv.URL}, testEvent())
	if result.Success || result.ErrorClass == nil || *result.ErrorClass != "network_error" {
		t.Fatalf("dial result=%+v", result)
	}
}

func TestClient_MalformedHTTPResponseIsBoundedNetworkFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		buffer.WriteString("not-an-http-response\r\n\r\n")
		buffer.Flush()
	}))
	defer srv.Close()
	result := NewClient(time.Second, time.Second, 32, true).Deliver(t.Context(), store.Target{URL: srv.URL}, testEvent())
	if result.Success || result.ErrorClass == nil || *result.ErrorClass != "network_error" || result.ErrorMessage == nil || *result.ErrorMessage != "outbound request failed" {
		t.Fatalf("malformed response result=%+v", result)
	}
}
