package store

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/testutil"
)

func TestMigrateConcurrentFreshSchema(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	var schema string
	if err := db.QueryRowContext(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	const instances = 8
	start := make(chan struct{})
	results := make(chan error, instances)
	for i := 0; i < instances; i++ {
		// Independent pools model different server processes sharing one schema.
		peer, err := sql.Open("pgx", u.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { peer.Close() })
		go func() {
			<-start
			results <- Migrate(ctx, peer)
		}()
	}
	close(start)
	for i := 0; i < instances; i++ {
		if err := <-results; err != nil {
			t.Errorf("concurrent migration: %v", err)
		}
	}
	if t.Failed() {
		return
	}
	files, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != len(files) {
		t.Fatalf("recorded migrations=%d want=%d err=%v", count, len(files), err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("idempotent startup: %v", err)
	}
	if _, err := NewTargetStore(db).Create(ctx, "after-migration", "https://example.com/webhook", nil); err != nil {
		t.Fatalf("migrated schema is unusable: %v", err)
	}
}

func TestMigrateLockWaitCancellation(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := t.Context()
	owner, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Rollback()
	if _, err := owner.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext(current_schema()),70132)`); err != nil {
		t.Fatal(err)
	}
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	err = Migrate(wait, db)
	if err == nil || !strings.Contains(err.Error(), "lock migrations") || wait.Err() != context.DeadlineExceeded {
		t.Fatalf("waiting migration did not honor cancellation: %v", err)
	}
	if err := owner.Rollback(); err != nil {
		t.Fatal(err)
	}
	retry, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	if err := Migrate(retry, db); err != nil {
		t.Fatalf("lock leaked after canceled waiter: %v", err)
	}
}

func TestMigrateFailureRollsBackBatchAndReleasesLock(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := t.Context()
	// This fixture collides with the index created by the third migration.
	if _, err := db.ExecContext(ctx, "CREATE TABLE idx_events_recovery (id int)"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db); err == nil {
		t.Fatal("expected migration failure")
	}
	var remaining int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables
		WHERE table_schema=current_schema() AND table_name IN ('schema_migrations','targets','events')`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("failed migration left partial batch: tables=%d err=%v", remaining, err)
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE idx_events_recovery"); err != nil {
		t.Fatal(err)
	}
	retry, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := Migrate(retry, db); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
}
