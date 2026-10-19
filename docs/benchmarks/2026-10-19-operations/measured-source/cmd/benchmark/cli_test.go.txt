package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/testutil"
)

// This fixture tests the benchmark command, not real worker delivery or capacity.
func TestBenchmarkCLI(t *testing.T) {
	k6, err := exec.LookPath("k6")
	if err != nil {
		t.Skip("native k6 required for command-level verification tests")
	}
	db := testutil.OpenDB(t)
	if err := store.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	var schema string
	if err := db.QueryRow("SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	dbURL, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	query := dbURL.Query()
	query.Set("search_path", schema)
	dbURL.RawQuery = query.Encode()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "benchmark.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-buildvcs=false", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	for _, tc := range []struct{ mode, status, verification string }{
		{"success", "completed", "verified"},
		{"pending", "verification_failed", "drain_timeout"},
		{"dlq", "verification_failed", "terminal_failures"},
		{"missing", "verification_failed", "count_mismatch"},
		{"query_error", "verification_failed", "query_failed"},
		{"invalid_database", "verification_failed", "connection_setup_failed"},
		{"ingestion_only", "ingestion_only", "not_configured"},
		{"http_error", "load_failed", "summary_failed"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			target, err := store.NewTargetStore(db).Create(t.Context(), "cli-fixture", "https://example.com", nil)
			if err != nil {
				t.Fatal(err)
			}
			events := store.NewEventStore(db)
			mux := http.NewServeMux()
			mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
			mux.HandleFunc("POST /api/v1/targets/{target}/events", func(w http.ResponseWriter, r *http.Request) {
				if tc.mode == "http_error" {
					w.WriteHeader(503)
					return
				}
				if tc.mode != "missing" {
					event, _, err := events.CreateWithOutbox(r.Context(), target.ID, r.Header.Get("Idempotency-Key"), []byte(`{}`), "fixture")
					if err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					if tc.mode != "pending" {
						outcome := store.OutcomeSuccess
						if tc.mode == "dlq" {
							outcome = store.OutcomeNonRetryableFailure
						}
						_, err = events.RecordAttemptAndTransition(r.Context(), event.ID, store.AttemptResult{Outcome: outcome, StartedAt: time.Now(), FinishedAt: time.Now()}, 1, func(int) time.Duration { return 0 })
						if err != nil {
							t.Error(err)
							w.WriteHeader(500)
							return
						}
					}
				}
				w.WriteHeader(201)
			})
			server := httptest.NewServer(mux)
			defer server.Close()
			destination := dbURL.String()
			if tc.mode == "query_error" {
				u := *dbURL
				q := u.Query()
				q.Set("search_path", schema+"_missing")
				u.RawQuery = q.Encode()
				destination = u.String()
			} else if tc.mode == "invalid_database" {
				destination = "postgres://user:benchmark-test-secret@%invalid/database"
			} else if tc.mode == "ingestion_only" {
				destination = ""
			}
			outputDir := t.TempDir()
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "-target-id", target.ID, "-base-url", server.URL, "-rate", "2", "-duration", "1s", "-drain-wait", "0s", "-api-commit", "test-fixture", "-machine", "test-fixture", "-target-behavior", "test-fixture-not-worker", "-output", outputDir)
			command.Dir = root
			command.Env = append(os.Environ(), "TEST_DATABASE_URL="+destination, "K6_NO_USAGE_REPORT=true", "PATH="+filepath.Dir(k6)+string(os.PathListSeparator)+os.Getenv("PATH"))
			output, runErr := command.CombinedOutput()
			wantSuccess := tc.status == "completed" || tc.status == "ingestion_only"
			if (runErr == nil) != wantSuccess {
				t.Fatalf("exit: %v\n%s", runErr, output)
			}
			paths, err := filepath.Glob(filepath.Join(outputDir, "*", "metadata.json"))
			if err != nil || len(paths) != 1 {
				t.Fatalf("metadata paths=%v err=%v", paths, err)
			}
			data, err := os.ReadFile(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			var metadata map[string]any
			if err := json.Unmarshal(data, &metadata); err != nil {
				t.Fatal(err)
			}
			if metadata["status"] != tc.status || metadata["delivery_verification"] != tc.verification {
				t.Fatalf("status=%v verification=%v\n%s", metadata["status"], metadata["delivery_verification"], output)
			}
			if strings.Contains(string(output)+string(data), "benchmark-test-secret") {
				t.Fatal("database credentials exposed")
			}
			snapshots, err := filepath.Glob(filepath.Join(outputDir, "*", "delivery.json"))
			wantSnapshot := tc.verification == "verified" || tc.verification == "drain_timeout" || tc.verification == "terminal_failures" || tc.verification == "count_mismatch"
			if err != nil || (len(snapshots) == 1) != wantSnapshot {
				t.Fatalf("delivery snapshots=%v err=%v", snapshots, err)
			}
		})
	}
}
