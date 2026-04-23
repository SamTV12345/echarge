package data

import (
	"database/sql"
	"errors"
)

// ErrNotFound is returned by GetStation when no row matches the given ID.
var ErrNotFound = errors.New("station not found")

// Store holds the in-memory SQLite database populated from the embedded registry.
// The zero value is NOT usable. Use Load to construct one.
type Store struct {
	db *sql.DB
}

// DB exposes the underlying sql.DB (used by tests).
func (s *Store) DB() *sql.DB { return s.db }

// Close releases the underlying database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}
