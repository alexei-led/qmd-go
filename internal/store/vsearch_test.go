package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInsertVectors_ContentVectorsOnly(t *testing.T) {
	d := setupTestDB(t)

	hash := "abc123"
	_, err := d.Exec(`INSERT OR IGNORE INTO content (hash, doc, created_at) VALUES (?, ?, ?)`,
		hash, "test doc", time.Now().UTC().Format(time.RFC3339))
	require.NoError(t, err)

	chunks := []Chunk{{Text: "chunk one", Pos: 0, Seq: 0}, {Text: "chunk two", Pos: 100, Seq: 1}}
	embeddings := [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}}

	err = InsertVectors(d, hash, "test-model", chunks, embeddings, time.Now().UTC().Format(time.RFC3339))
	require.NoError(t, err)

	var count int
	require.NoError(t, d.QueryRow(`SELECT COUNT(*) FROM content_vectors WHERE hash = ?`, hash).Scan(&count))
	assert.Equal(t, 2, count)

	var pos int
	require.NoError(t, d.QueryRow(`SELECT pos FROM content_vectors WHERE hash = ? AND seq = 1`, hash).Scan(&pos))
	assert.Equal(t, 100, pos)
}

func TestFloat32ToBytes_Empty(t *testing.T) {
	b := Float32ToBytes(nil)
	assert.Empty(t, b)
	v := BytesToFloat32(nil)
	assert.Empty(t, v)
}

func BenchmarkFloat32ToBytes(b *testing.B) {
	vec := make([]float32, 384)
	for i := range vec {
		vec[i] = float32(i) * 0.001
	}
	b.ResetTimer()
	for b.Loop() {
		Float32ToBytes(vec)
	}
}
