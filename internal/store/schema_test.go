package store_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/qmd-go/internal/db"
	"github.com/user/qmd-go/internal/store"
)

func TestInitializeDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(dbPath)
	require.NoError(t, err)
	defer func() { _ = d.Close() }()

	err = store.InitializeDatabase(d)
	require.NoError(t, err)

	tables := []string{
		"content", "documents", "llm_cache",
		"content_vectors", "store_collections", "store_config",
		"documents_fts",
	}
	for _, table := range tables {
		var name string
		err := d.QueryRow(
			"SELECT name FROM sqlite_master WHERE type IN ('table','view') AND name = ?",
			table,
		).Scan(&name)
		assert.NoError(t, err, "table %s should exist", table)
		assert.Equal(t, table, name)
	}
}

func TestInitializeDatabase_Idempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(dbPath)
	require.NoError(t, err)
	defer func() { _ = d.Close() }()

	require.NoError(t, store.InitializeDatabase(d))
	require.NoError(t, store.InitializeDatabase(d))
}

func TestInitializeDatabase_FTSTriggers(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(dbPath)
	require.NoError(t, err)
	defer func() { _ = d.Close() }()

	require.NoError(t, store.InitializeDatabase(d))

	// Insert content and document — triggers should populate FTS.
	_, err = d.Exec(`INSERT INTO content (hash, doc, created_at) VALUES ('abc123', 'Hello world document', '2024-01-01')`)
	require.NoError(t, err)

	_, err = d.Exec(`INSERT INTO documents (collection, path, title, hash, created_at, modified_at, active)
		VALUES ('notes', 'test.md', 'Test Doc', 'abc123', '2024-01-01', '2024-01-01', 1)`)
	require.NoError(t, err)

	var count int
	err = d.QueryRow("SELECT count(*) FROM documents_fts WHERE documents_fts MATCH 'hello'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Deactivate document — trigger should remove from FTS.
	_, err = d.Exec("UPDATE documents SET active = 0 WHERE path = 'test.md'")
	require.NoError(t, err)

	err = d.QueryRow("SELECT count(*) FROM documents_fts WHERE documents_fts MATCH 'hello'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestInitializeDatabase_Indexes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(dbPath)
	require.NoError(t, err)
	defer func() { _ = d.Close() }()

	require.NoError(t, store.InitializeDatabase(d))

	indexes := []string{
		"idx_documents_collection",
		"idx_documents_hash",
		"idx_documents_path",
	}
	for _, idx := range indexes {
		var name string
		err := d.QueryRow(
			"SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?",
			idx,
		).Scan(&name)
		assert.NoError(t, err, "index %s should exist", idx)
	}
}

func TestMigrateContentVectors_MissingSeq(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(dbPath)
	require.NoError(t, err)
	defer func() { _ = d.Close() }()

	// Create old-schema content_vectors without seq column.
	_, err = d.Exec(`CREATE TABLE content_vectors (
		hash TEXT PRIMARY KEY,
		pos INTEGER NOT NULL DEFAULT 0,
		model TEXT NOT NULL,
		embedded_at TEXT NOT NULL
	)`)
	require.NoError(t, err)

	// InitializeDatabase should detect missing seq and recreate.
	err = store.InitializeDatabase(d)
	require.NoError(t, err)

	// Verify seq column now exists by inserting a row with seq.
	_, err = d.Exec(`INSERT INTO content_vectors (hash, seq, pos, model, embedded_at)
		VALUES ('abc', 0, 0, 'test', '2024-01-01')`)
	assert.NoError(t, err)
}

func TestLegacyTablesDropped(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(dbPath)
	require.NoError(t, err)
	defer func() { _ = d.Close() }()

	// Create legacy tables that should be dropped.
	_, err = d.Exec("CREATE TABLE path_contexts (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)
	_, err = d.Exec("CREATE TABLE collections (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)

	require.NoError(t, store.InitializeDatabase(d))

	for _, table := range []string{"path_contexts", "collections"} {
		var name string
		err := d.QueryRow(
			"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?",
			table,
		).Scan(&name)
		assert.ErrorIs(t, err, sql.ErrNoRows, "legacy table %s should be dropped", table)
	}
}
