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

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(dbPath)
	require.NoError(t, err)
	require.NoError(t, store.InitializeDatabase(d))
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func seedDoc(t *testing.T, d *sql.DB, collection, path, hash, title, body string) {
	t.Helper()
	_, err := d.Exec(`INSERT OR IGNORE INTO store_collections (name, path) VALUES (?, ?)`,
		collection, "/home/user/"+collection)
	require.NoError(t, err)
	_, err = d.Exec(`INSERT INTO content (hash, doc, created_at) VALUES (?, ?, '2024-01-01')`, hash, body)
	require.NoError(t, err)
	_, err = d.Exec(`INSERT INTO documents (collection, path, title, hash, active, created_at, modified_at)
		VALUES (?, ?, ?, ?, 1, '2024-01-01', '2024-01-01')`, collection, path, title, hash)
	require.NoError(t, err)
}

func TestGetDocid(t *testing.T) {
	assert.Equal(t, "abc123", store.GetDocid("abc123def456"))
	assert.Equal(t, "abc", store.GetDocid("abc"))
}

func TestNormalizeDocid(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"#abc123", "abc123"},
		{`"abc123"`, "abc123"},
		{"'#abc123'", "abc123"},
		{"abc123", "abc123"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, store.NormalizeDocid(tt.input), "input: %s", tt.input)
	}
}

func TestIsDocid(t *testing.T) {
	assert.True(t, store.IsDocid("#abc123"))
	assert.True(t, store.IsDocid("abcdef"))
	assert.False(t, store.IsDocid("abc"))
	assert.False(t, store.IsDocid("not-hex"))
}

func TestParseLineSpec(t *testing.T) {
	tests := []struct {
		input    string
		wantPath string
		wantLine int
	}{
		{"file.md:10", "file.md", 10},
		{"file.md", "file.md", 0},
		{"file.md:0", "file.md:0", 0},
		{"file.md:abc", "file.md:abc", 0},
	}
	for _, tt := range tests {
		path, line := store.ParseLineSpec(tt.input)
		assert.Equal(t, tt.wantPath, path, "input: %s", tt.input)
		assert.Equal(t, tt.wantLine, line, "input: %s", tt.input)
	}
}

func TestFindDocumentByDocid(t *testing.T) {
	d := setupTestDB(t)
	seedDoc(t, d, "notes", "hello.md", "abcdef123456", "Hello", "Hello world")

	match, err := store.FindDocumentByDocid(d, "#abcdef")
	require.NoError(t, err)
	require.NotNil(t, match)
	assert.Equal(t, "qmd://notes/hello.md", match.Filepath)

	match, err = store.FindDocumentByDocid(d, "xxxxxx")
	require.NoError(t, err)
	assert.Nil(t, match)
}

func TestFindDocument_VirtualPath(t *testing.T) {
	d := setupTestDB(t)
	seedDoc(t, d, "notes", "hello.md", "abcdef123456", "Hello", "Hello world")

	doc, notFound, err := store.FindDocument(d, "qmd://notes/hello.md", store.FindDocumentOpts{IncludeBody: true})
	require.NoError(t, err)
	require.Nil(t, notFound)
	require.NotNil(t, doc)
	assert.Equal(t, "Hello world", doc.Body)
	assert.Equal(t, "abcdef", doc.DocID)
}

func TestFindDocument_Docid(t *testing.T) {
	d := setupTestDB(t)
	seedDoc(t, d, "notes", "hello.md", "abcdef123456", "Hello", "Hello world")

	doc, notFound, err := store.FindDocument(d, "#abcdef", store.FindDocumentOpts{})
	require.NoError(t, err)
	require.Nil(t, notFound)
	require.NotNil(t, doc)
	assert.Equal(t, "qmd://notes/hello.md", doc.Filepath)
}

func TestFindDocument_NotFound(t *testing.T) {
	d := setupTestDB(t)
	seedDoc(t, d, "notes", "hello.md", "abcdef123456", "Hello", "Hello world")

	_, notFound, err := store.FindDocument(d, "nonexistent.md", store.FindDocumentOpts{})
	require.NoError(t, err)
	require.NotNil(t, notFound)
	assert.Equal(t, "not_found", notFound.Error)
}

func TestGetDocumentBody_LineSlicing(t *testing.T) {
	d := setupTestDB(t)
	seedDoc(t, d, "notes", "lines.md", "aabbcc112233", "Lines", "line1\nline2\nline3\nline4\nline5")

	body, err := store.GetDocumentBody(d, "qmd://notes/lines.md", 2, 2)
	require.NoError(t, err)
	assert.Equal(t, "line2\nline3", body)

	body, err = store.GetDocumentBody(d, "qmd://notes/lines.md", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2\nline3\nline4\nline5", body)
}

func TestFindDocuments_Comma(t *testing.T) {
	d := setupTestDB(t)
	seedDoc(t, d, "notes", "a.md", "aaaaaa111111", "A", "doc a")
	seedDoc(t, d, "notes", "b.md", "bbbbbb222222", "B", "doc b")

	results, errs, err := store.FindDocuments(d, "qmd://notes/a.md,qmd://notes/b.md", 0)
	require.NoError(t, err)
	assert.Empty(t, errs)
	assert.Len(t, results, 2)
}

func TestFindDocuments_Glob(t *testing.T) {
	d := setupTestDB(t)
	seedDoc(t, d, "notes", "a.md", "aaaaaa111111", "A", "doc a")
	seedDoc(t, d, "notes", "b.md", "bbbbbb222222", "B", "doc b")

	results, errs, err := store.FindDocuments(d, "*.md", 0)
	require.NoError(t, err)
	assert.Empty(t, errs)
	assert.Len(t, results, 2)
}

func TestFindDocuments_MaxBytes(t *testing.T) {
	d := setupTestDB(t)
	seedDoc(t, d, "notes", "big.md", "cccccc333333", "Big", "x")

	results, _, err := store.FindDocuments(d, "qmd://notes/big.md", 0)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].Skipped)
}

func TestListDocuments_Collections(t *testing.T) {
	d := setupTestDB(t)
	seedDoc(t, d, "notes", "a.md", "aaaaaa111111", "A", "doc a")

	entries, err := store.ListDocuments(d, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.True(t, entries[0].IsCollection)
	assert.Equal(t, "qmd://notes/", entries[0].Path)
	assert.Equal(t, 1, entries[0].FileCount)
}

func TestListDocuments_InCollection(t *testing.T) {
	d := setupTestDB(t)
	seedDoc(t, d, "notes", "a.md", "aaaaaa111111", "A", "doc a")
	seedDoc(t, d, "notes", "b.md", "bbbbbb222222", "B", "doc b")

	entries, err := store.ListDocuments(d, "notes")
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestAddLineNumbers(t *testing.T) {
	result := store.AddLineNumbers("a\nb\nc", 5)
	assert.Equal(t, "5: a\n6: b\n7: c", result)
}

func TestLevenshtein(t *testing.T) {
	assert.Equal(t, 0, store.Levenshtein("abc", "abc"))
	assert.Equal(t, 1, store.Levenshtein("abc", "abd"))
	assert.Equal(t, 3, store.Levenshtein("", "abc"))
}

func TestFindSimilarFiles(t *testing.T) {
	d := setupTestDB(t)
	seedDoc(t, d, "notes", "hello.md", "aaaaaa111111", "Hello", "doc")
	seedDoc(t, d, "notes", "world.md", "bbbbbb222222", "World", "doc")

	similar, err := store.FindSimilarFiles(d, "hallo.md", 5, 5)
	require.NoError(t, err)
	require.NotEmpty(t, similar)
	assert.Equal(t, "hello.md", similar[0])
}

func TestMatchFilesByGlob(t *testing.T) {
	d := setupTestDB(t)
	seedDoc(t, d, "notes", "readme.md", "aaaaaa111111", "Readme", "doc")
	seedDoc(t, d, "notes", "notes.txt", "bbbbbb222222", "Notes", "doc")

	matches, err := store.MatchFilesByGlob(d, "*.md")
	require.NoError(t, err)
	assert.Len(t, matches, 1)
	assert.Contains(t, matches[0].Filepath, "readme.md")
}
