package store_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/qmd-go/internal/db"
	"github.com/user/qmd-go/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	require.NoError(t, store.InitializeDatabase(d))
	return d
}

func TestScanFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("B"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "c.md"), []byte("# C"), 0o644))

	files, err := store.ScanFiles(dir, "**/*.md", "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a.md", "sub/c.md"}, files)
}

func TestScanFiles_IgnorePatterns(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "vendor"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("A"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "vendor", "b.md"), []byte("B"), 0o644))

	files, err := store.ScanFiles(dir, "**/*.md", "vendor/**")
	require.NoError(t, err)
	assert.Equal(t, []string{"a.md"}, files)
}

func TestScanFiles_NonExistentDir(t *testing.T) {
	_, err := store.ScanFiles("/nonexistent/path", "**/*.md", "")
	assert.Error(t, err)
}

func TestHashContent(t *testing.T) {
	h := store.HashContent("hello")
	assert.Len(t, h, 64)
	assert.Equal(t, h, store.HashContent("hello"))
	assert.NotEqual(t, h, store.HashContent("world"))
}

func TestExtractTitle(t *testing.T) {
	tests := []struct {
		name, content, path, want string
	}{
		{"markdown h1", "# My Title\n\nBody", "file.md", "My Title"},
		{"markdown h2", "## Sub\n\nBody", "file.md", "Sub"},
		{"markdown h3", "### Deep\n\nBody", "file.md", "Deep"},
		{"org title", "#+TITLE: Org Title\n\nBody", "file.org", "Org Title"},
		{"org lowercase", "#+title: Lower\n\nBody", "file.org", "Lower"},
		{"fallback dashes", "no heading here", "my-notes.md", "my notes"},
		{"fallback underscores", "", "hello_world.txt", "hello world"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, store.ExtractTitle(tt.content, tt.path))
		})
	}
}

func TestHandelize(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"Hello World.md", "hello-world.md"},
		{"foo___bar/baz.md", "foo/bar/baz.md"},
		{"café.md", "café.md"},
		{"a--b.md", "a-b.md"},
		{".md", ""},
		{"---", ""},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.expected, store.Handelize(tc.input))
		})
	}
}

func TestHandelize_Emoji(t *testing.T) {
	result := store.Handelize("🎉party.md")
	assert.Contains(t, result, "1f389")
	assert.NotEmpty(t, result)
}

func TestInsertContent(t *testing.T) {
	d := openTestDB(t)
	hash1, err := store.InsertContent(d, "hello world")
	require.NoError(t, err)
	assert.Len(t, hash1, 64)

	hash2, err := store.InsertContent(d, "hello world")
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)
}

func TestInsertDocument(t *testing.T) {
	d := openTestDB(t)
	_, err := store.InsertContent(d, "doc content")
	require.NoError(t, err)
	hash := store.HashContent("doc content")

	id1, err := store.InsertDocument(d, "coll", "path.md", "Title", hash)
	require.NoError(t, err)
	assert.Greater(t, id1, int64(0))
}

func TestInsertDocument_UpsertUpdatesTitle(t *testing.T) {
	d := openTestDB(t)
	hash, err := store.InsertContent(d, "content")
	require.NoError(t, err)
	_, err = store.InsertDocument(d, "coll", "file.md", "Old Title", hash)
	require.NoError(t, err)

	newHash, err := store.InsertContent(d, "new content")
	require.NoError(t, err)
	_, err = store.InsertDocument(d, "coll", "file.md", "New Title", newHash)
	require.NoError(t, err)

	var title, docHash string
	err = d.QueryRow(`SELECT title, hash FROM documents WHERE collection = ? AND path = ?`, "coll", "file.md").Scan(&title, &docHash)
	require.NoError(t, err)
	assert.Equal(t, "New Title", title)
	assert.Equal(t, newHash, docHash)
}

func TestDeactivateDocuments(t *testing.T) {
	d := openTestDB(t)
	hash, err := store.InsertContent(d, "content")
	require.NoError(t, err)
	_, err = store.InsertDocument(d, "coll", "keep.md", "Keep", hash)
	require.NoError(t, err)
	_, err = store.InsertDocument(d, "coll", "remove.md", "Remove", hash)
	require.NoError(t, err)

	n, err := store.DeactivateDocuments(d, "coll", map[string]bool{"keep.md": true})
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	var active int
	err = d.QueryRow(`SELECT active FROM documents WHERE path = 'remove.md'`).Scan(&active)
	require.NoError(t, err)
	assert.Equal(t, 0, active)
}

func TestGetActiveDocumentPaths(t *testing.T) {
	d := openTestDB(t)
	hash, err := store.InsertContent(d, "content")
	require.NoError(t, err)
	_, err = store.InsertDocument(d, "coll", "a.md", "A", hash)
	require.NoError(t, err)
	_, err = store.InsertDocument(d, "coll", "b.md", "B", hash)
	require.NoError(t, err)

	paths, err := store.GetActiveDocumentPaths(d, "coll")
	require.NoError(t, err)
	assert.Len(t, paths, 2)
	assert.Equal(t, hash, paths["a.md"])
}

func TestReindexCollection(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("# Alpha\n\nContent A"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.md"), []byte("# Beta\n\nContent B"), 0o644))

	d := openTestDB(t)
	_, err := d.Exec(`INSERT INTO store_collections (name, path, pattern) VALUES (?, ?, ?)`, "test", dir, "**/*.md")
	require.NoError(t, err)

	var events []string
	progress := func(collection, status string, current, total int) {
		events = append(events, status)
	}

	err = store.ReindexCollection(d, "test", dir, "**/*.md", "", progress)
	require.NoError(t, err)

	paths, err := store.GetActiveDocumentPaths(d, "test")
	require.NoError(t, err)
	assert.Len(t, paths, 2)

	assert.Contains(t, events, "scanning")
	assert.Contains(t, events, "done")
}

func TestReindexCollection_SkipsUnchanged(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("# Alpha"), 0o644))

	d := openTestDB(t)
	require.NoError(t, store.ReindexCollection(d, "test", dir, "**/*.md", "", nil))

	var modifiedAt1 string
	err := d.QueryRow(`SELECT modified_at FROM documents WHERE path = 'a.md'`).Scan(&modifiedAt1)
	require.NoError(t, err)

	require.NoError(t, store.ReindexCollection(d, "test", dir, "**/*.md", "", nil))

	var modifiedAt2 string
	err = d.QueryRow(`SELECT modified_at FROM documents WHERE path = 'a.md'`).Scan(&modifiedAt2)
	require.NoError(t, err)
	assert.Equal(t, modifiedAt1, modifiedAt2)
}

func TestReindexCollection_DeactivatesRemoved(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.md"), []byte("# B"), 0o644))

	d := openTestDB(t)
	require.NoError(t, store.ReindexCollection(d, "test", dir, "**/*.md", "", nil))

	require.NoError(t, os.Remove(filepath.Join(dir, "b.md")))
	require.NoError(t, store.ReindexCollection(d, "test", dir, "**/*.md", "", nil))

	var active int
	err := d.QueryRow(`SELECT active FROM documents WHERE collection = 'test' AND path = 'b.md'`).Scan(&active)
	require.NoError(t, err)
	assert.Equal(t, 0, active)

	paths, err := store.GetActiveDocumentPaths(d, "test")
	require.NoError(t, err)
	assert.Len(t, paths, 1)
}

func TestReindexAll(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A"), 0o644))

	d := openTestDB(t)
	_, err := d.Exec(`INSERT INTO store_collections (name, path, pattern) VALUES (?, ?, ?)`, "all-test", dir, "**/*.md")
	require.NoError(t, err)

	require.NoError(t, store.ReindexAll(d, nil))

	paths, err := store.GetActiveDocumentPaths(d, "all-test")
	require.NoError(t, err)
	assert.Len(t, paths, 1)
}

func TestInsertContent_Dedup(t *testing.T) {
	d := openTestDB(t)

	hash1, err := store.InsertContent(d, "duplicate content")
	require.NoError(t, err)
	hash2, err := store.InsertContent(d, "duplicate content")
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)

	var count int
	err = d.QueryRow(`SELECT COUNT(*) FROM content WHERE hash = ?`, hash1).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestDeactivateDocuments_PreservesActive(t *testing.T) {
	d := openTestDB(t)
	hash, err := store.InsertContent(d, "shared content")
	require.NoError(t, err)

	_, err = store.InsertDocument(d, "coll", "a.md", "A", hash)
	require.NoError(t, err)
	_, err = store.InsertDocument(d, "coll", "b.md", "B", hash)
	require.NoError(t, err)
	_, err = store.InsertDocument(d, "coll", "c.md", "C", hash)
	require.NoError(t, err)

	n, err := store.DeactivateDocuments(d, "coll", map[string]bool{"a.md": true, "c.md": true})
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	var activeB int
	err = d.QueryRow(`SELECT active FROM documents WHERE path = 'b.md'`).Scan(&activeB)
	require.NoError(t, err)
	assert.Equal(t, 0, activeB)
}
