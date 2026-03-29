// Package db provides SQLite database connection management for QMD.
package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"runtime"
	"strings"

	_ "github.com/ncruces/go-sqlite3/driver"
)

// Open opens a SQLite database at the given path with QMD's standard PRAGMAs:
// WAL journal mode, foreign keys, busy timeout, and WAL autocheckpoint.
// It also configures the connection pool based on available CPUs.
func Open(dbPath string) (*sql.DB, error) {
	dsn := buildDSN(dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("db open: %w", err)
	}

	db.SetMaxOpenConns(runtime.NumCPU())

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("db ping: %w", err)
	}

	return db, nil
}

// buildDSN constructs a SQLite URI with QMD PRAGMAs.
func buildDSN(dbPath string) string {
	params := url.Values{}
	params.Add("_pragma", "journal_mode(WAL)")
	params.Add("_pragma", "foreign_keys(ON)")
	params.Add("_pragma", "busy_timeout(5000)")
	params.Add("_pragma", "wal_autocheckpoint(1000)")
	params.Add("_txlock", "immediate")
	escaped := strings.NewReplacer("?", "%3F", "#", "%23").Replace(dbPath)
	return fmt.Sprintf("file:%s?%s", escaped, params.Encode())
}

// VecAvailable reports whether the sqlite-vec extension is loaded.
func VecAvailable(db *sql.DB) bool {
	var version string
	err := db.QueryRow("SELECT vec_version()").Scan(&version)
	return err == nil && version != ""
}
