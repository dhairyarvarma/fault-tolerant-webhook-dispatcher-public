package store

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

func TestOpenBoundsConnectionBudget(t *testing.T) {
	db, err := Open("postgres://localhost/webhook?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := db.Stats().MaxOpenConnections; got != 32 {
		t.Fatalf("max open connections = %d, want 32", got)
	}
}

func TestOpenReusesConcurrentConnections(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	db, err := Open(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	// Hold more than the stdlib's two default idle connections concurrently.
	batch := func() map[int]bool {
		connections := make([]*sql.Conn, 0, 12)
		pids := make(map[int]bool)
		for range 12 {
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Close() })
			connections = append(connections, conn)
			var pid int
			if err := conn.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			pids[pid] = true
		}
		for _, conn := range connections {
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
		}
		return pids
	}
	first, second := batch(), batch()
	if got := db.Stats().MaxIdleClosed; got != 0 {
		t.Fatalf("closed %d reusable connections across two bursts, want 0", got)
	}
	for pid := range first {
		if !second[pid] {
			t.Fatal("second burst created a replacement PostgreSQL backend")
		}
	}
}
