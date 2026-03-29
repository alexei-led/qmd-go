package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/qmd-go/internal/db"
)

func TestBuildFTS5Query(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  *string
	}{
		{"plain terms", "machine learning", ptr(`"machine"* AND "learning"*`)},
		{"quoted phrase", `machine "deep learning"`, ptr(`"machine"* AND "deep learning"`)},
		{"negation", "machine -neural", ptr(`"machine"* NOT "neural"*`)},
		{"negated phrase", `machine -"neural network"`, ptr(`"machine"* NOT "neural network"`)},
		{"all negation returns nil", "-neural -network", nil},
		{"empty returns nil", "", nil},
		{"whitespace only returns nil", "   ", nil},
		{"unicode cleanup", "héllo wörld!", ptr(`"héllo"* AND "wörld"*`)},
		{"strips special chars", "test@#$%value", ptr(`"testvalue"*`)},
		{"preserves internal hyphens", "well-known", ptr(`"well-known"*`)},
		{"strips trailing hyphens", "test-", ptr(`"test"*`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildFTS5Query(tt.input)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Equal(t, *tt.want, *got)
			}
		})
	}
}

func TestSanitizeFTSTerm(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"hello", "hello"},
		{"hello-world", "hello-world"},
		{"hello-", "hello"},
		{"@hello!", "hello"},
		{"test123", "test123"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, sanitizeFTSTerm(tt.input))
		})
	}
}

func TestSearchFTS_Integration(t *testing.T) {
	d := setupTestDB(t)

	results, err := SearchFTS(d, "quantum mechanics", SearchOpts{Limit: 5})
	require.NoError(t, err)
	require.Len(t, results, 1)

	r := results[0]
	assert.Equal(t, "test-col", r.Collection)
	assert.Equal(t, "quantum.md", r.Path)
	assert.Equal(t, "Quantum Guide", r.Title)
	assert.Greater(t, r.Score, 0.0)
	assert.Less(t, r.Score, 1.0)
	assert.NotEmpty(t, r.Snippet)
}

func TestSearchFTS_MinScore(t *testing.T) {
	d := setupTestDB(t)

	results, err := SearchFTS(d, "quantum", SearchOpts{Limit: 10, MinScore: 0.999})
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestSearchFTS_CollectionFilter(t *testing.T) {
	d := setupTestDB(t)

	results, err := SearchFTS(d, "quantum", SearchOpts{Limit: 10, Collection: "nonexistent"})
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestSearchFTS_ShowFull(t *testing.T) {
	d := setupTestDB(t)

	results, err := SearchFTS(d, "quantum", SearchOpts{Limit: 5, ShowFull: true})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.NotEmpty(t, results[0].Body)
	assert.Empty(t, results[0].Snippet)
}

func TestSearchFTS_NilQuery(t *testing.T) {
	d := setupTestDB(t)

	results, err := SearchFTS(d, "-only -negation", SearchOpts{Limit: 5})
	require.NoError(t, err)
	assert.Nil(t, results)
}

func TestSearchFTS_ScoreNormalization(t *testing.T) {
	d := setupTestDB(t)

	results, err := SearchFTS(d, "quantum", SearchOpts{Limit: 10, SearchAll: true})
	require.NoError(t, err)

	for _, r := range results {
		assert.Greater(t, r.Score, 0.0, "score should be positive")
		assert.Less(t, r.Score, 1.0, "score should be less than 1")
	}
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	require.NoError(t, InitializeDatabase(d))

	_, err = d.Exec(`INSERT INTO store_collections (name, path, pattern, include_by_default)
		VALUES ('test-col', '/tmp/test', '**/*.md', 1)`)
	require.NoError(t, err)

	_, err = d.Exec(`INSERT INTO content (hash, doc, created_at)
		VALUES ('abc123', '# Quantum Guide
Quantum mechanics is a fundamental theory in physics.
It describes nature at the smallest scales of energy.
The theory was developed in the early 20th century.
Wave-particle duality is a key concept.', '2024-01-01T00:00:00Z')`)
	require.NoError(t, err)

	_, err = d.Exec(`INSERT INTO documents (collection, path, title, hash, created_at, modified_at, active)
		VALUES ('test-col', 'quantum.md', 'Quantum Guide', 'abc123', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', 1)`)
	require.NoError(t, err)

	_, err = d.Exec(`INSERT INTO content (hash, doc, created_at)
		VALUES ('def456', '# Cooking Recipes
How to make pasta from scratch.
Italian cuisine basics and techniques.', '2024-01-01T00:00:00Z')`)
	require.NoError(t, err)

	_, err = d.Exec(`INSERT INTO documents (collection, path, title, hash, created_at, modified_at, active)
		VALUES ('test-col', 'cooking.md', 'Cooking Recipes', 'def456', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', 1)`)
	require.NoError(t, err)

	return d
}

func ptr(s string) *string { return &s }

func setupEmptyTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	require.NoError(t, InitializeDatabase(d))
	return d
}
