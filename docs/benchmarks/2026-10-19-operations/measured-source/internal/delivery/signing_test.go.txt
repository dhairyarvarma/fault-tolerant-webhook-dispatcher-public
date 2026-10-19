package delivery

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/crypto"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClient_SignsExactBodyWithEncryptedTargetSecret(t *testing.T) {
	box, err := crypto.NewSecretBox(bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := box.Seal("receiver-secret")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		mac := hmac.New(sha256.New, []byte("receiver-secret"))
		mac.Write([]byte(r.Header.Get("X-Webhook-Timestamp") + "." + r.Header.Get("X-Webhook-Id") + "."))
		mac.Write(body)
		if got, want := r.Header.Get("X-Webhook-Signature"), "v1="+hex.EncodeToString(mac.Sum(nil)); got != want {
			t.Errorf("signature mismatch")
		}
		if r.Header.Get("X-Webhook-Id") != testEvent().ID || r.Header.Get("Idempotency-Key") != testEvent().IdempotencyKey {
			t.Error("identity changed")
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	client := NewClient(time.Second, time.Second, 1024, true, box)
	for i := 0; i < 2; i++ {
		if result := client.Deliver(t.Context(), store.Target{URL: srv.URL, SecretCiphertext: encrypted}, testEvent()); !result.Success {
			t.Fatalf("signed delivery failed: %+v", result)
		}
	}
}

func TestClient_CorruptSecretFailsBeforeHTTP(t *testing.T) {
	box, err := crypto.NewSecretBox(bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer srv.Close()
	result := NewClient(time.Second, time.Second, 1024, true, box).Deliver(t.Context(), store.Target{URL: srv.URL, SecretCiphertext: []byte("corrupt")}, testEvent())
	if result.Success || result.ErrorClass == nil || *result.ErrorClass != "signing_error" || calls.Load() != 0 {
		t.Fatalf("corrupt secret result=%+v calls=%d", result, calls.Load())
	}
}
