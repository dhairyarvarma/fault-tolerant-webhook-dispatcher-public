// Command benchmark retains k6 inputs, output, metrics samples, and delivery checks.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

type deliveryReport struct {
	Accepted  int64    `json:"accepted"`
	Succeeded int64    `json:"succeeded"`
	DLQ       int64    `json:"dlq"`
	Pending   int64    `json:"pending"`
	Canceled  int64    `json:"canceled"`
	P50       *float64 `json:"success_latency_p50_seconds"`
	P95       *float64 `json:"success_latency_p95_seconds"`
	P99       *float64 `json:"success_latency_p99_seconds"`
}

func deliverySummary(ctx context.Context, db *sql.DB, target, runID string) (deliveryReport, error) {
	var report deliveryReport
	err := db.QueryRowContext(ctx, `WITH selected AS (
	 SELECT * FROM events WHERE target_id=$1 AND idempotency_key LIKE $2
	), successes AS (
	 SELECT extract(epoch FROM max(a.finished_at)-e.created_at)::double precision AS latency
	 FROM selected e JOIN delivery_attempts a ON a.event_id=e.id
	 WHERE e.status='SUCCEEDED' AND a.outcome='SUCCESS' GROUP BY e.id,e.created_at
	)
	SELECT (SELECT count(*) FROM selected),
	 (SELECT count(*) FROM selected WHERE status='SUCCEEDED'),
	 (SELECT count(*) FROM selected WHERE status='DLQ'),
	 (SELECT count(*) FROM selected WHERE status IN ('PENDING','QUEUED','RETRY_WAIT')),
	 (SELECT count(*) FROM selected WHERE status='CANCELED'),
	 percentile_cont(0.5) WITHIN GROUP(ORDER BY latency),
	 percentile_cont(0.95) WITHIN GROUP(ORDER BY latency),
	 percentile_cont(0.99) WITHIN GROUP(ORDER BY latency) FROM successes`, target, runID+"-%").Scan(
		&report.Accepted, &report.Succeeded, &report.DLQ, &report.Pending, &report.Canceled, &report.P50, &report.P95, &report.P99)
	return report, err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	scenario := flag.String("scenario", "ingestion", "ingestion, ceiling, or soak")
	target := flag.String("target-id", "", "dedicated benchmark target UUID")
	base := flag.String("base-url", "http://localhost:8080", "API address")
	rate := flag.Int("rate", 25, "ingestion/soak requests per second")
	duration := flag.String("duration", "30s", "ingestion/soak duration; set 10m for a soak")
	workers := flag.Int("workers", 4, "operator-reported API worker count")
	machine := flag.String("machine", "", "operator-reported API machine and resource limits")
	apiCommit := flag.String("api-commit", "", "operator-reported commit deployed to the API")
	behavior := flag.String("target-behavior", "", "operator-reported receiver status, latency, and failure configuration")
	output := flag.String("output", "docs/benchmarks", "artifact parent directory")
	drain := flag.Duration("drain-wait", 2*time.Minute, "maximum wait for delivery completion")
	validate := flag.Bool("validate-only", false, "validate inputs without running load or writing results")
	flag.Parse()
	provided := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { provided[f.Name] = true })
	if *scenario == "soak" {
		if !provided["rate"] {
			*rate = 10
		}
		if !provided["duration"] {
			*duration = "10m"
		}
	}
	if *scenario != "ingestion" && *scenario != "ceiling" && *scenario != "soak" {
		return fmt.Errorf("unknown scenario")
	}
	if *target == "" || *rate <= 0 || *workers <= 0 || *drain < 0 {
		return fmt.Errorf("target-id and positive rate/workers required")
	}
	if d, err := time.ParseDuration(*duration); err != nil || d <= 0 {
		return fmt.Errorf("invalid duration")
	}
	u, err := url.Parse(*base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("invalid API URL")
	}
	pre, maxVUs := os.Getenv("PRE_ALLOCATED_VUS"), os.Getenv("MAX_VUS")
	if pre == "" {
		pre = "10"
		if *scenario == "ceiling" {
			pre = "20"
		}
	}
	if maxVUs == "" {
		maxVUs = "100"
		if *scenario == "ceiling" {
			maxVUs = "300"
		}
	}
	p, e1 := strconv.Atoi(pre)
	m, e2 := strconv.Atoi(maxVUs)
	if e1 != nil || e2 != nil || p <= 0 || m < p {
		return fmt.Errorf("invalid VU configuration")
	}
	script := filepath.Join("load", "k6", *scenario+".js")
	source, err := os.ReadFile(script)
	if err != nil {
		return err
	}
	runID := time.Now().UTC().Format("20060102T150405.000000000Z")
	metadata := map[string]any{"run_id": runID, "scenario": *scenario, "target_id": *target, "base_url": u.Redacted(),
		"rate": *rate, "duration": *duration, "reported_workers": *workers, "reported_api_machine": *machine, "reported_api_commit": *apiCommit,
		"reported_target_behavior": *behavior, "workload_source": script,
		"load_generator_os": runtime.GOOS, "load_generator_arch": runtime.GOARCH, "load_generator_cpus": runtime.NumCPU(),
		"go_version": runtime.Version(), "script_sha256": fmt.Sprintf("%x", sha256.Sum256(source)),
		"pre_allocated_vus": pre, "max_vus": maxVUs, "ceiling_stages": os.Getenv("CEILING_STAGES")}
	metadata["drain_wait_seconds"] = drain.Seconds()
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	git := exec.Command("git", "-c", "safe.directory="+wd, "rev-parse", "HEAD")
	if commit, err := git.Output(); err == nil {
		metadata["runner_checkout_commit"] = strings.TrimSpace(string(commit))
	}
	if *validate {
		metadata["status"] = "validation_only_no_load"
		return json.NewEncoder(os.Stdout).Encode(metadata)
	}
	if *apiCommit == "" || *machine == "" || *behavior == "" {
		return fmt.Errorf("api-commit, machine, and target-behavior are required for executed load")
	}
	k6, err := exec.LookPath("k6")
	if err != nil {
		return fmt.Errorf("native k6 is required; no load executed (validate-only is available)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if version, err := exec.CommandContext(ctx, k6, "version").Output(); err == nil {
		metadata["k6_version"] = strings.TrimSpace(string(version))
	}
	dir := filepath.Join(*output, runID)
	if err := os.MkdirAll(*output, 0755); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0755); err != nil {
		return err
	}
	archivedScript := filepath.Join(dir, "script.js")
	if err := os.WriteFile(archivedScript, source, 0644); err != nil {
		return err
	}
	writeJSON := func(name string, value any) error {
		body, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, name), body, 0644)
	}
	metadata["status"] = "running"
	if err := writeJSON("metadata.json", metadata); err != nil {
		return err
	}
	log, err := os.Create(filepath.Join(dir, "k6-output.txt"))
	if err != nil {
		return err
	}
	defer log.Close()
	samples, err := os.Create(filepath.Join(dir, "metrics.jsonl"))
	if err != nil {
		return err
	}
	defer samples.Close()
	sampleCtx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		client := http.Client{Timeout: 2 * time.Second}
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			req, _ := http.NewRequestWithContext(sampleCtx, "GET", strings.TrimRight(*base, "/")+"/metrics", nil)
			resp, err := client.Do(req)
			if err == nil {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
				resp.Body.Close()
				json.NewEncoder(samples).Encode(map[string]any{"at": time.Now().UTC(), "status": resp.StatusCode, "metrics": string(body)})
			}
			select {
			case <-sampleCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() { stop(); wg.Wait() }()
	command := exec.CommandContext(ctx, k6, "run", "--summary-export", filepath.Join(dir, "k6-summary.json"), "--summary-trend-stats=avg,min,med,max,p(50),p(95),p(99)", archivedScript)
	command.Env = append(os.Environ(), "BASE_URL="+*base, "TARGET_ID="+*target, "RATE="+fmt.Sprint(*rate), "DURATION="+*duration, "RUN_ID="+runID, "PRE_ALLOCATED_VUS="+pre, "MAX_VUS="+maxVUs)
	command.Stdout = io.MultiWriter(os.Stdout, log)
	command.Stderr = io.MultiWriter(os.Stderr, log)
	metadata["load_started_at"] = time.Now().UTC()
	loadErr := command.Run()
	metadata["load_finished_at"] = time.Now().UTC()
	metadata["load_succeeded"] = loadErr == nil
	var verificationErr error
	if dbURL := os.Getenv("TEST_DATABASE_URL"); dbURL != "" {
		summary, err := os.ReadFile(filepath.Join(dir, "k6-summary.json"))
		var expected int64
		if err == nil {
			expected, err = acceptedRequests(summary)
		}
		if err != nil {
			metadata["delivery_verification"] = "summary_failed"
			verificationErr = fmt.Errorf("cannot verify k6 accepted-event count")
		} else {
			metadata["delivery_expected"] = expected
			db, err := store.Open(dbURL)
			if err != nil {
				metadata["delivery_verification"] = "connection_setup_failed"
				verificationErr = fmt.Errorf("delivery database setup failed")
			} else {
				defer db.Close()
				metadata["delivery_verification"], verificationErr = verifyDelivery(ctx, *drain, expected,
					func(ctx context.Context) (deliveryReport, error) { return deliverySummary(ctx, db, *target, runID) },
					func(report deliveryReport) error { return writeJSON("delivery.json", report) })
			}
		}
	} else {
		metadata["delivery_verification"] = "not_configured"
	}
	metadata["finished_at"] = time.Now().UTC()
	metadata["status"] = "completed"
	if os.Getenv("TEST_DATABASE_URL") == "" {
		metadata["status"] = "ingestion_only"
	}
	if verificationErr != nil {
		metadata["status"] = "verification_failed"
	}
	if loadErr != nil {
		metadata["status"] = "load_failed"
	}
	if err := writeJSON("metadata.json", metadata); err != nil {
		return errors.Join(loadErr, verificationErr, err)
	}
	return errors.Join(loadErr, verificationErr)
}
