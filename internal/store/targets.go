package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type TargetStore struct {
	db *sql.DB
}

func NewTargetStore(db *sql.DB) *TargetStore {
	return &TargetStore{db: db}
}

// Create inserts a new target and returns it with server-generated fields
// populated.
func (s *TargetStore) Create(ctx context.Context, name, url string, secretCiphertext []byte) (Target, error) {
	var t Target
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO targets (name, url, secret_ciphertext)
		VALUES ($1, $2, $3)
		RETURNING id, name, url, secret_ciphertext, enabled, circuit_state, created_at, updated_at
	`, name, url, secretCiphertext).Scan(
		&t.ID, &t.Name, &t.URL, &t.SecretCiphertext, &t.Enabled, &t.CircuitState, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return Target{}, fmt.Errorf("insert target: %w", err)
	}
	return t, nil
}

// Get returns a target by ID.
func (s *TargetStore) Get(ctx context.Context, id string) (Target, error) {
	var t Target
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, url, secret_ciphertext, enabled, circuit_state, created_at, updated_at
		FROM targets
		WHERE id = $1
	`, id).Scan(
		&t.ID, &t.Name, &t.URL, &t.SecretCiphertext, &t.Enabled, &t.CircuitState, &t.CreatedAt, &t.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Target{}, ErrNotFound
	}
	if err != nil {
		return Target{}, fmt.Errorf("select target: %w", err)
	}
	return t, nil
}

// Disable marks a target as disabled without deleting its historical
// delivery data, per DATABASE_DESIGN.md.
func (s *TargetStore) Disable(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE targets SET enabled = FALSE, updated_at = now() WHERE id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("disable target: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("disable target rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}
