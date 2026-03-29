package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/qmd-go/internal/store"
)

func TestGetStatus_EmptyDB(t *testing.T) {
	d := openTestDB(t)

	info, err := store.GetStatus(d, "test", "/tmp/test.db")
	require.NoError(t, err)

	assert.Equal(t, "test", info.IndexName)
	assert.Equal(t, "/tmp/test.db", info.DBPath)
	assert.Equal(t, 0, info.TotalDocuments)
	assert.Equal(t, 0, info.ActiveDocuments)
	assert.Equal(t, 0, info.EmbeddedChunks)
	assert.Empty(t, info.Collections)
}

func TestGetStatus_WithData(t *testing.T) {
	d := openTestDB(t)

	_, err := d.Exec(`INSERT INTO store_collections (name, path, pattern) VALUES ('notes', '/notes', '**/*.md')`)
	require.NoError(t, err)
	_, err = d.Exec(`INSERT INTO content (hash, doc, created_at) VALUES ('abc123', 'hello', '2024-01-01')`)
	require.NoError(t, err)
	_, err = d.Exec(`INSERT INTO documents (collection, path, title, hash, created_at, modified_at, active) VALUES ('notes', 'a.md', 'A', 'abc123', '2024-01-01', '2024-01-01', 1)`)
	require.NoError(t, err)

	info, err := store.GetStatus(d, "default", "/data/default.db")
	require.NoError(t, err)

	assert.Equal(t, 1, info.TotalDocuments)
	assert.Equal(t, 1, info.ActiveDocuments)
	assert.Len(t, info.Collections, 1)
	assert.Equal(t, "notes", info.Collections[0].Name)
}

func TestCleanup_RemovesInactive(t *testing.T) {
	d := openTestDB(t)

	_, err := d.Exec(`INSERT INTO content (hash, doc, created_at) VALUES ('h1', 'doc1', '2024-01-01')`)
	require.NoError(t, err)
	_, err = d.Exec(`INSERT INTO documents (collection, path, title, hash, created_at, modified_at, active) VALUES ('c', 'a.md', 'A', 'h1', '2024-01-01', '2024-01-01', 0)`)
	require.NoError(t, err)

	result, err := store.Cleanup(d, store.CleanupOpts{})
	require.NoError(t, err)

	assert.Equal(t, 1, result.InactiveRemoved)
	assert.True(t, result.Vacuumed)
}

func TestCleanup_SkipFlags(t *testing.T) {
	d := openTestDB(t)

	_, err := d.Exec(`INSERT INTO content (hash, doc, created_at) VALUES ('h1', 'doc1', '2024-01-01')`)
	require.NoError(t, err)
	_, err = d.Exec(`INSERT INTO documents (collection, path, title, hash, created_at, modified_at, active) VALUES ('c', 'a.md', 'A', 'h1', '2024-01-01', '2024-01-01', 0)`)
	require.NoError(t, err)

	result, err := store.Cleanup(d, store.CleanupOpts{
		SkipInactive:        true,
		SkipOrphanedContent: true,
		SkipOrphanedVectors: true,
		SkipLLMCache:        true,
	})
	require.NoError(t, err)

	assert.Equal(t, 0, result.InactiveRemoved)
	assert.Equal(t, 0, result.ContentRemoved)
	assert.Equal(t, 0, result.VectorsRemoved)
	assert.Equal(t, 0, result.CacheCleared)
	assert.True(t, result.Vacuumed)
}

func TestCleanup_OrphanedContent(t *testing.T) {
	d := openTestDB(t)

	_, err := d.Exec(`INSERT INTO content (hash, doc, created_at) VALUES ('orphan', 'orphaned', '2024-01-01')`)
	require.NoError(t, err)

	result, err := store.Cleanup(d, store.CleanupOpts{SkipInactive: true, SkipLLMCache: true, SkipOrphanedVectors: true})
	require.NoError(t, err)

	assert.Equal(t, 1, result.ContentRemoved)
}
