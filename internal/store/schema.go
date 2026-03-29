package store

import (
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/user/qmd-go/internal/db"
)

// Schema DDL statements — must match the TypeScript QMD schema exactly.
const schemaSQL = `
-- Drop legacy tables
DROP TABLE IF EXISTS path_contexts;
DROP TABLE IF EXISTS collections;

-- Content-addressable storage
CREATE TABLE IF NOT EXISTS content (
  hash TEXT PRIMARY KEY,
  doc TEXT NOT NULL,
  created_at TEXT NOT NULL
);

-- Documents mapping
CREATE TABLE IF NOT EXISTS documents (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  collection TEXT NOT NULL,
  path TEXT NOT NULL,
  title TEXT NOT NULL,
  hash TEXT NOT NULL,
  created_at TEXT NOT NULL,
  modified_at TEXT NOT NULL,
  active INTEGER NOT NULL DEFAULT 1,
  FOREIGN KEY (hash) REFERENCES content(hash) ON DELETE CASCADE,
  UNIQUE(collection, path)
);
CREATE INDEX IF NOT EXISTS idx_documents_collection ON documents(collection, active);
CREATE INDEX IF NOT EXISTS idx_documents_hash ON documents(hash);
CREATE INDEX IF NOT EXISTS idx_documents_path ON documents(path, active);

-- LLM cache
CREATE TABLE IF NOT EXISTS llm_cache (
  hash TEXT PRIMARY KEY,
  result TEXT NOT NULL,
  created_at TEXT NOT NULL
);

-- Embeddings
CREATE TABLE IF NOT EXISTS content_vectors (
  hash TEXT NOT NULL,
  seq INTEGER NOT NULL DEFAULT 0,
  pos INTEGER NOT NULL DEFAULT 0,
  model TEXT NOT NULL,
  embedded_at TEXT NOT NULL,
  PRIMARY KEY (hash, seq)
);

-- Store collections (self-contained)
CREATE TABLE IF NOT EXISTS store_collections (
  name TEXT PRIMARY KEY,
  path TEXT NOT NULL,
  pattern TEXT NOT NULL DEFAULT '**/*.md',
  ignore_patterns TEXT,
  include_by_default INTEGER DEFAULT 1,
  update_command TEXT,
  context TEXT
);

-- Store config (key-value)
CREATE TABLE IF NOT EXISTS store_config (
  key TEXT PRIMARY KEY,
  value TEXT
);
`

// FTS5 and triggers — executed separately because they use different syntax.
const ftsSQL = `
CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
  filepath, title, body,
  tokenize='porter unicode61'
);
`

const triggersSQL = `
CREATE TRIGGER IF NOT EXISTS documents_ai AFTER INSERT ON documents
WHEN new.active = 1
BEGIN
  INSERT INTO documents_fts(rowid, filepath, title, body)
  SELECT new.id, new.collection || '/' || new.path, new.title,
    (SELECT doc FROM content WHERE hash = new.hash)
  WHERE new.active = 1;
END;

CREATE TRIGGER IF NOT EXISTS documents_ad AFTER DELETE ON documents BEGIN
  DELETE FROM documents_fts WHERE rowid = old.id;
END;

CREATE TRIGGER IF NOT EXISTS documents_au AFTER UPDATE ON documents
BEGIN
  DELETE FROM documents_fts WHERE rowid = old.id AND new.active = 0;
  INSERT OR REPLACE INTO documents_fts(rowid, filepath, title, body)
  SELECT new.id, new.collection || '/' || new.path, new.title,
    (SELECT doc FROM content WHERE hash = new.hash)
  WHERE new.active = 1;
END;
`

// InitializeDatabase creates all tables, indexes, FTS5, and triggers.
// It also attempts to create the sqlite-vec virtual table if available.
func InitializeDatabase(d *sql.DB) error {
	if _, err := d.Exec(schemaSQL); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	if _, err := d.Exec(ftsSQL); err != nil {
		return fmt.Errorf("fts5: %w", err)
	}
	if _, err := d.Exec(triggersSQL); err != nil {
		return fmt.Errorf("triggers: %w", err)
	}

	if err := migrateContentVectors(d); err != nil {
		return fmt.Errorf("migrate content_vectors: %w", err)
	}

	initVecTable(d)

	return nil
}

// initVecTable creates the vectors_vec virtual table if sqlite-vec is available.
// Fails silently — vector search is disabled when sqlite-vec is absent.
func initVecTable(d *sql.DB) {
	if !db.VecAvailable(d) {
		slog.Info("sqlite-vec not available, vector search disabled")
		return
	}

	dim := detectEmbedDimension(d)
	if dim == 0 {
		dim = 384 // default dimension
	}

	stmt := fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS vectors_vec USING vec0(
		hash TEXT,
		seq INTEGER,
		embedding float[%d]
	)`, dim)

	if _, err := d.Exec(stmt); err != nil {
		slog.Warn("failed to create vectors_vec table", "error", err)
	}
}

// detectEmbedDimension checks existing vectors for their dimension.
func detectEmbedDimension(d *sql.DB) int {
	var dim int
	row := d.QueryRow(`SELECT length(embedding) / 4 FROM vectors_vec LIMIT 1`)
	if row.Scan(&dim) == nil && dim > 0 {
		return dim
	}
	return 0
}

// migrateContentVectors checks for the seq column in content_vectors.
// If missing, drops and recreates the table (TS compatibility).
func migrateContentVectors(d *sql.DB) error {
	var hasSeq bool
	rows, err := d.Query("PRAGMA table_info(content_vectors)")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == "seq" {
			hasSeq = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if !hasSeq {
		slog.Info("migrating content_vectors: adding seq column")
		if _, err := d.Exec("DROP TABLE IF EXISTS content_vectors"); err != nil {
			return err
		}
		migrateSQL := `CREATE TABLE IF NOT EXISTS content_vectors (
			hash TEXT NOT NULL,
			seq INTEGER NOT NULL DEFAULT 0,
			pos INTEGER NOT NULL DEFAULT 0,
			model TEXT NOT NULL,
			embedded_at TEXT NOT NULL,
			PRIMARY KEY (hash, seq)
		)`
		if _, err := d.Exec(migrateSQL); err != nil {
			return err
		}
	}

	return nil
}
