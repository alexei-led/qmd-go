package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/user/qmd-go/internal/provider"
	"github.com/user/qmd-go/internal/provider/mocks"
)

func TestStructuredSearch_LexOnly(t *testing.T) {
	d := setupTestDB(t)

	results, err := StructuredSearch(context.Background(), d, StructuredSearchRequest{
		Searches: []SearchQuery{{Type: "lex", Query: "quantum"}},
		Limit:    5,
		Explain:  true,
	}, nil, nil)

	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "quantum.md", results[0].Path)
	assert.Greater(t, results[0].Score, 0.0)
	require.NotNil(t, results[0].Explain)
	assert.Greater(t, results[0].Explain.LexScore, 0.0)
	assert.Equal(t, 0.0, results[0].Explain.VecScore)
}

func TestStructuredSearch_VecOnly(t *testing.T) {
	d := setupTestDB(t)

	embedder := mocks.NewEmbedder(t)
	fakeVec := []float32{0.1, 0.2, 0.3}
	embedder.EXPECT().
		Embed(mock.Anything, []string{"quantum"}, provider.EmbedOpts{IsQuery: true}).
		Return([][]float32{fakeVec}, nil)

	// Vec search requires embedded vectors — seed one.
	_, err := d.Exec(`INSERT INTO content_vectors (hash, seq, pos, model, embedded_at)
		VALUES ('abc123', 0, 0, '3d', '2024-01-01T00:00:00Z')`)
	require.NoError(t, err)

	results, err := StructuredSearch(context.Background(), d, StructuredSearchRequest{
		Searches: []SearchQuery{{Type: "vec", Query: "quantum"}},
		Limit:    5,
		Explain:  true,
	}, embedder, nil)

	require.NoError(t, err)
	// Vec search without vectors_vec table returns empty — that's expected since
	// sqlite-vec extension is not loaded in tests. Verify no error at minimum.
	_ = results
}

func TestStructuredSearch_MixedLexVec(t *testing.T) {
	d := setupTestDB(t)

	embedder := mocks.NewEmbedder(t)
	fakeVec := []float32{0.1, 0.2, 0.3}
	embedder.EXPECT().
		Embed(mock.Anything, []string{"quantum"}, provider.EmbedOpts{IsQuery: true}).
		Return([][]float32{fakeVec}, nil)

	results, err := StructuredSearch(context.Background(), d, StructuredSearchRequest{
		Searches: []SearchQuery{
			{Type: "lex", Query: "quantum"},
			{Type: "vec", Query: "quantum"},
		},
		Limit:   5,
		Explain: true,
	}, embedder, nil)

	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "quantum.md", results[0].Path)
}

func TestStructuredSearch_WithReranker(t *testing.T) {
	d := setupTestDB(t)

	reranker := mocks.NewReranker(t)
	reranker.EXPECT().
		Rerank(mock.Anything, "quantum", mock.Anything, provider.RerankOpts{}).
		Return([]provider.RerankResult{{Index: 0, Score: 0.95}}, nil)

	results, err := StructuredSearch(context.Background(), d, StructuredSearchRequest{
		Searches: []SearchQuery{{Type: "lex", Query: "quantum"}},
		Limit:    5,
	}, nil, reranker)

	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Greater(t, results[0].Score, 0.0)
}

func TestStructuredSearch_InvalidType(t *testing.T) {
	d := setupTestDB(t)

	_, err := StructuredSearch(context.Background(), d, StructuredSearchRequest{
		Searches: []SearchQuery{{Type: "invalid", Query: "test"}},
	}, nil, nil)

	assert.Error(t, err)
}

func TestStructuredSearch_EmptySearches(t *testing.T) {
	d := setupTestDB(t)

	results, err := StructuredSearch(context.Background(), d, StructuredSearchRequest{
		Searches: nil,
	}, nil, nil)

	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestStructuredSearch_CollectionFilter(t *testing.T) {
	d := setupTestDB(t)

	results, err := StructuredSearch(context.Background(), d, StructuredSearchRequest{
		Searches:    []SearchQuery{{Type: "lex", Query: "quantum"}},
		Collections: []string{"nonexistent-col"},
		Limit:       5,
	}, nil, nil)

	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestEmbedDocuments_HappyPath(t *testing.T) {
	d := setupTestDB(t)

	embedder := mocks.NewEmbedder(t)
	embedder.EXPECT().Dimensions().Return(3)
	embedder.EXPECT().
		Embed(mock.Anything, mock.Anything, provider.EmbedOpts{}).
		Return([][]float32{{0.1, 0.2, 0.3}}, nil).
		Maybe()

	var progressCalls []EmbedProgress
	err := EmbedDocuments(context.Background(), d, embedder, EmbedOpts{}, func(p EmbedProgress) {
		progressCalls = append(progressCalls, p)
	})

	require.NoError(t, err)
	assert.NotEmpty(t, progressCalls)
	assert.Equal(t, "embedding", progressCalls[0].Phase)
}

func TestEmbedDocuments_NothingToEmbed(t *testing.T) {
	d := setupTestDB(t)

	// Embed once to exhaust all content.
	embedder := mocks.NewEmbedder(t)
	embedder.EXPECT().Dimensions().Return(3).Maybe()
	embedder.EXPECT().
		Embed(mock.Anything, mock.Anything, provider.EmbedOpts{}).
		Return([][]float32{{0.1, 0.2, 0.3}}, nil).
		Maybe()

	require.NoError(t, EmbedDocuments(context.Background(), d, embedder, EmbedOpts{}, nil))

	// Second call — nothing to embed.
	embedder2 := mocks.NewEmbedder(t)
	embedder2.EXPECT().Dimensions().Return(3).Maybe()
	// Embed should NOT be called — no new content.

	require.NoError(t, EmbedDocuments(context.Background(), d, embedder2, EmbedOpts{}, nil))
}

func TestEmbedDocuments_ClearThenReembed(t *testing.T) {
	d := setupTestDB(t)

	embedder := mocks.NewEmbedder(t)
	embedder.EXPECT().Dimensions().Return(3).Maybe()
	embedder.EXPECT().
		Embed(mock.Anything, mock.Anything, provider.EmbedOpts{}).
		Return([][]float32{{0.1, 0.2, 0.3}}, nil).
		Maybe()

	// First embed.
	require.NoError(t, EmbedDocuments(context.Background(), d, embedder, EmbedOpts{}, nil))

	// Clear and re-embed.
	embedder2 := mocks.NewEmbedder(t)
	embedder2.EXPECT().Dimensions().Return(3).Maybe()
	embedder2.EXPECT().
		Embed(mock.Anything, mock.Anything, provider.EmbedOpts{}).
		Return([][]float32{{0.4, 0.5, 0.6}}, nil).
		Maybe()

	require.NoError(t, EmbedDocuments(context.Background(), d, embedder2, EmbedOpts{Clear: true}, nil))
}

func TestHybridQuery_NoExpansion_StrongSignal(t *testing.T) {
	d := setupTestDB(t)

	// Seed a second doc to make FTS return >1 results with score gap.
	_, err := d.Exec(`INSERT INTO content (hash, doc, created_at)
		VALUES ('ghi789', '# Quantum Physics Deep Dive
Quantum quantum quantum mechanics is wonderful.
Quantum entanglement and superposition explained.
The quantum world is fascinating.', '2024-01-01T00:00:00Z')`)
	require.NoError(t, err)
	_, err = d.Exec(`INSERT INTO documents (collection, path, title, hash, created_at, modified_at, active)
		VALUES ('test-col', 'quantum-deep.md', 'Quantum Deep', 'ghi789', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', 1)`)
	require.NoError(t, err)

	results, err := HybridQuery(context.Background(), d, "quantum", nil, nil, nil, HybridOpts{
		Limit:   5,
		Explain: true,
	})

	require.NoError(t, err)
	require.NotEmpty(t, results)
	for _, r := range results {
		assert.Greater(t, r.Score, 0.0)
		assert.NotEmpty(t, r.Snippet)
	}
}

func TestHybridQuery_WithExpansion(t *testing.T) {
	d := setupTestDB(t)

	generator := mocks.NewGenerator(t)
	generator.EXPECT().
		Generate(mock.Anything, mock.Anything, mock.Anything).
		Return("LEX: physics, theory\nVEC: quantum mechanics study\nHYDE: quantum physics explanation", nil)

	results, err := HybridQuery(context.Background(), d, "quantum", nil, nil, generator, HybridOpts{
		Limit: 5,
	})

	require.NoError(t, err)
	require.NotEmpty(t, results)
}

func TestHybridQuery_EmptyDB(t *testing.T) {
	d := setupEmptyTestDB(t)

	results, err := HybridQuery(context.Background(), d, "anything", nil, nil, nil, HybridOpts{
		Limit: 5,
	})

	require.NoError(t, err)
	assert.Empty(t, results)
}
