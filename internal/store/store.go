// Package store owns land.db: opening it, migrating it, and every SQL
// statement the rest of the program issues. No other package may import
// database/sql directly — callers get structs and typed repository methods.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: keeps cross-compilation to Windows a single go build
)

// Store is an open land.db. It is safe for concurrent use: database/sql pools
// the underlying connections.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens (creating if needed) the database at path and migrates it to the
// current schema version.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// A single writer keeps the write path predictable on NTFS; readers still
	// get their own connections from the pool.
	db.SetMaxOpenConns(8)
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping %s: %w", path, err)
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Path reports the file backing this store (used by logs and the admin UI).
func (s *Store) Path() string { return s.path }

// ErrNotFound is returned by lookups that matched no row.
var ErrNotFound = errors.New("store: not found")

// ErrConflict is returned when a write collides with an existing row (for
// example a duplicate virtual path). HTTP handlers map it to 409.
var ErrConflict = errors.New("store: conflict")

// isNoRows keeps database/sql's sentinel from leaking into callers that only
// care whether something exists.
func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// DBForTests exposes the underlying handle to tests in dependent packages that
// need to age rows (upload cleanup, session expiry). Production code must not
// use it: the store owns every statement.
func (s *Store) DBForTests() *sql.DB { return s.db }
