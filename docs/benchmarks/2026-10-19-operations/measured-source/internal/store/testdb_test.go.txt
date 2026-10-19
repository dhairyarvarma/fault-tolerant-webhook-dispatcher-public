package store

import (
	"context"
	"database/sql"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/testutil"
	"testing"
)

// testDB connects to a real PostgreSQL instance for repository tests. It
// skips the test if TEST_DATABASE_URL is not set, so `go test ./...` still
// passes without Docker running; the integration/CI workflow sets it.
func testDB(t *testing.T) *sql.DB {
	t.Helper()

	db := testutil.OpenDB(t)

	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	return db
}
