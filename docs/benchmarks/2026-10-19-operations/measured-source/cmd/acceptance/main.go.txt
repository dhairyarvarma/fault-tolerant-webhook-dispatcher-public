// Command acceptance runs HTTP acceptance tests against an isolated built server.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/queue"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/signature"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/targetsim"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (runErr error) {
	demo := flag.Bool("demo", false, "run the narrated showcase with shortened retry/circuit settings")
	flag.Parse()
	dbURL, redisURL := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_URL")
	if dbURL == "" || redisURL == "" {
		return fmt.Errorf("TEST_DATABASE_URL and TEST_REDIS_URL are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	admin, err := store.Open(dbURL)
	if err != nil {
		return err
	}
	defer admin.Close()
	if _, err := admin.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public"); err != nil {
		return err
	}
	schema := fmt.Sprintf("acceptance_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		return err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			fmt.Fprintln(os.Stderr, "schema cleanup:", err)
			if runErr == nil {
				runErr = err
			}
		}
	}()
	u, err := url.Parse(dbURL)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	rdb, err := queue.Open(redisURL)
	if err != nil {
		return err
	}
	defer rdb.Close()
	stream := "acceptance-" + schema
	defer rdb.Del(context.Background(), stream)
	temp, err := os.MkdirTemp("", "webhook-acceptance-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	binary := filepath.Join(temp, "server.exe")
	build := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", binary, "./cmd/server")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	address := listener.Addr().String()
	listener.Close()
	simMux := http.NewServeMux()
	simMux.Handle("/", targetsim.NewHandler())
	simMux.HandleFunc("POST /signed", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			w.WriteHeader(400)
			return
		}
		if !signature.Verify("acceptance-signing-secret", r.Header.Get("X-Webhook-Timestamp"), r.Header.Get("X-Webhook-Id"), body, r.Header.Get("X-Webhook-Signature"), time.Now(), time.Minute) {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(204)
	})
	simulator := httptest.NewServer(simMux)
	defer simulator.Close()
	logs, err := os.Create(filepath.Join(temp, "server.log"))
	if err != nil {
		return err
	}
	defer logs.Close()
	server := exec.CommandContext(ctx, binary)
	server.Env = append(os.Environ(), "APP_ENV=development", "HTTP_ADDR="+address, "DATABASE_URL="+u.String(), "REDIS_URL="+redisURL,
		"REDIS_STREAM="+stream, "REDIS_CONSUMER_GROUP=acceptance", "ALLOW_PRIVATE_TARGETS=true", "AUTH_ENABLED=true", "API_KEY=acceptance-only",
		"SECRET_ENCRYPTION_KEY=a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s=", "OUTBOX_POLL_INTERVAL=20ms", "EVENTS_MAX_PENDING_EVENTS=0")
	if *demo {
		server.Env = append(server.Env, "DELIVERY_MAX_ATTEMPTS=3", "DELIVERY_BASE_BACKOFF=200ms",
			"DELIVERY_MAX_BACKOFF=400ms", "CIRCUIT_FAILURE_THRESHOLD=3", "CIRCUIT_OPEN_COOLDOWN=5s")
		fmt.Println("Isolated HTTP showcase: 3 attempts, 200–400ms backoff cap, 5s circuit cooldown.")
		fmt.Println("These shortened demo settings are not production defaults or benchmark results.")
	}
	server.Stdout = logs
	server.Stderr = logs
	if err := server.Start(); err != nil {
		return err
	}
	defer func() { server.Process.Kill(); server.Wait() }()
	base := "http://" + address
	ready := false
	client := http.Client{Timeout: time.Second}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		resp, err := client.Get(base + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		return fmt.Errorf("isolated server did not become ready")
	}
	args := []string{"test", "-count=1", "-v", "./test/e2e"}
	if *demo {
		args = append(args, "-run", "^TestE2E_Showcase$")
	}
	tests := exec.CommandContext(ctx, "go", args...)
	tests.Env = append(os.Environ(), "TEST_BASE_URL="+base, "TEST_TARGET_URL="+simulator.URL, "TEST_API_KEY=acceptance-only")
	tests.Env = append(tests.Env, "TEST_SIGNING_TARGET_URL="+simulator.URL+"/signed")
	if *demo {
		tests.Env = append(tests.Env, "TEST_SHOWCASE=1")
	}
	tests.Stdout = os.Stdout
	tests.Stderr = os.Stderr
	return tests.Run()
}
