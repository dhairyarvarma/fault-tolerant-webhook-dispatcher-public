// Run with WEBHOOK_SECRET set to the secret configured on the webhook target.
package main

import (
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/signature"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	secret := os.Getenv("WEBHOOK_SECRET")
	if secret == "" {
		log.Fatal("WEBHOOK_SECRET is required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if !signature.Verify(secret, r.Header.Get("X-Webhook-Timestamp"), r.Header.Get("X-Webhook-Id"), body, r.Header.Get("X-Webhook-Signature"), time.Now(), 5*time.Minute) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
		// Persist X-Webhook-Id (or a target-scoped key) atomically with business effects.
		w.WriteHeader(http.StatusNoContent)
	})
	server := http.Server{Addr: "127.0.0.1:9092", Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	log.Fatal(server.ListenAndServe())
}
