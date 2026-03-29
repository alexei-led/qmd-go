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
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("B"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "c.md"), []byte("# C"), 0o644))

	files, err := store.ScanFiles(dir, "**/*.md", "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a.md", "sub/c.md"}, files)
}

func TestScanFiles_IgnorePatterns(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "vendor"), 0o755))
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

func TestExtractTitle_Markdown(t *testing.T) {
	assert.Equal(t, "My Title", store.ExtractTitle("# My Title\n\nBody", "file.md"))
	assert.Equal(t, "Sub", store.ExtractTitle("## Sub\n\nBody", "file.md"))
	assert.Equal(t, "Deep", store.ExtractTitle("### Deep\n\nBody", "file.md"))
}

func TestExtractTitle_OrgMode(t *testing.T) {
	assert.Equal(t, "Org Title", store.ExtractTitle("#+TITLE: Org Title\n\nBody", "file.org"))
	assert.Equal(t, "Lower", store.ExtractTitle("#+title: Lower\n\nBody", "file.org"))
}

func TestExtractTitle_Fallback(t *testing.T) {
	assert.Equal(t, "my notes", store.ExtractTitle("no heading here", "my-notes.md"))
	assert.Equal(t, "hello world", store.ExtractTitle("", "hello_world.txt"))
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
	hash, _ := store.InsertContent(d, "content")
	_, _ = store.InsertDocument(d, "coll", "file.md", "Old Title", hash)

	newHash, _ := store.InsertContent(d, "new content")
	_, _ = store.InsertDocument(d, "coll", "file.md", "New Title", newHash)

	var title, docHash string
	err := d.QueryRow(`SELECT title, hash FROM documents WHERE collection = ? AND path = ?`, "coll", "file.md").Scan(&title, &docHash)
	require.NoError(t, err)
	assert.Equal(t, "New Title", title)
	assert.Equal(t, newHash, docHash)
}

func TestDeactivateDocuments(t *testing.T) {
	d := openTestDB(t)
	hash, _ := store.InsertContent(d, "content")
	_, _ = store.InsertDocument(d, "coll", "keep.md", "Keep", hash)
	_, _ = store.InsertDocument(d, "coll", "remove.md", "Remove", hash)

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
	hash, _ := store.InsertContent(d, "content")
	_, _ = store.InsertDocument(d, "coll", "a.md", "A", hash)
	_, _ = store.InsertDocument(d, "coll", "b.md", "B", hash)

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
