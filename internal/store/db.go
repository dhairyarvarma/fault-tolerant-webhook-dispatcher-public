package store

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Open opens a connection pool to PostgreSQL using the pgx stdlib driver.
func Open(databaseURL string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		// Malformed connection strings cannot be reliably redacted.
		return nil, errors.New("invalid DATABASE_URL")
	}
	db := stdlib.OpenDB(*cfg)
	// Keep bursts warm without allowing HTTP traffic to exhaust PostgreSQL slots.
	db.SetMaxOpenConns(32)
	db.SetMaxIdleConns(16)
	return db, nil
}
