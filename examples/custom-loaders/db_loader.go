package main

import (
	"database/sql"
	"errors"
	"fmt"
)

// DBLoader reads scripts from a SQL table via the standard database/sql package.
// It imports no specific driver, so the backend (Postgres, MySQL, SQLite, …) is
// the caller's choice — open the *sql.DB with your driver and pass it in.
//
// The expected table looks like:
//
//	CREATE TABLE scripts (
//	    key     TEXT PRIMARY KEY,
//	    src     TEXT NOT NULL,
//	    version TEXT NOT NULL, -- engine version: a content hash the writer sets
//	    display TEXT           -- human label, e.g. "1.0.0" (may be empty)
//	);
//
// NOTE: this loader is compile-verified by the example build/vet, but it is not
// exercised at runtime by the tests — doing so would require importing a concrete
// SQL driver, which would add a dependency to the module. In your own service you
// open a real *sql.DB and pass it to NewDBLoader.
//
// Since: 2026-06-08
type DBLoader struct {
	db    *sql.DB
	query string
}

// NewDBLoader returns a loader backed by db. The query must select (src, version,
// display) for a single key parameter; adjust the placeholder ($1, ?, …) to match
// your driver.
func NewDBLoader(db *sql.DB) *DBLoader {
	return &DBLoader{
		db:    db,
		query: `SELECT src, version, display FROM scripts WHERE key = ?`,
	}
}

// Load fetches one row by key. An unknown key (sql.ErrNoRows) is mapped to a
// not-found error. *sql.DB is safe for concurrent use, so no extra locking is
// needed. display may be NULL in the table, so it is scanned into a sql.NullString.
func (l *DBLoader) Load(key string) (src, version, displayVersion string, err error) {
	var display sql.NullString
	err = l.db.QueryRow(l.query, key).Scan(&src, &version, &display)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", fmt.Errorf("luart: script %q not found", key)
	}
	if err != nil {
		return "", "", "", fmt.Errorf("luart: load %q: %w", key, err)
	}
	return src, version, display.String, nil
}
