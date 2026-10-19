package store

import (
	"context"
	"testing"
)

func TestTargetStore_CreateAndGet(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)

	created, err := ts.Create(ctx, "example-service", "https://example.com/webhook", []byte("ciphertext"))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected generated ID")
	}
	if created.CircuitState != CircuitClosed {
		t.Errorf("CircuitState = %q, want %q", created.CircuitState, CircuitClosed)
	}
	if !created.Enabled {
		t.Error("expected new target to be enabled")
	}

	got, err := ts.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "example-service" || got.URL != "https://example.com/webhook" {
		t.Errorf("Get() = %+v, unexpected fields", got)
	}
}

func TestTargetStore_GetNotFound(t *testing.T) {
	db := testDB(t)
	ts := NewTargetStore(db)

	_, err := ts.Get(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err != ErrNotFound {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestTargetStore_Disable(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ts := NewTargetStore(db)

	created, err := ts.Create(ctx, "example-service", "https://example.com/webhook", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := ts.Disable(ctx, created.ID); err != nil {
		t.Fatalf("Disable() error = %v", err)
	}

	got, err := ts.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Enabled {
		t.Error("expected target to be disabled")
	}
}

func TestTargetStore_DisableNotFound(t *testing.T) {
	db := testDB(t)
	ts := NewTargetStore(db)

	err := ts.Disable(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err != ErrNotFound {
		t.Fatalf("Disable() error = %v, want ErrNotFound", err)
	}
}
