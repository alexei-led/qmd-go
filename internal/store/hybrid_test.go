package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFuseRRF_SingleList(t *testing.T) {
	lists := []rankedList{
		{docIDs: []int64{1, 2, 3}, weight: 1.0},
	}
	results := fuseRRF(lists, 10)
	require.Len(t, results, 3)
	assert.Equal(t, int64(1), results[0].docID)
	assert.Greater(t, results[0].score, results[1].score)
}

func TestFuseRRF_MultipleListsWeighted(t *testing.T) {
	lists := []rankedList{
		{docIDs: []int64{1, 2, 3}, weight: 2.0},
		{docIDs: []int64{3, 4, 1}, weight: 1.0},
	}
	results := fuseRRF(lists, 10)
	require.True(t, len(results) >= 3)

	scoreMap := make(map[int64]float64)
	for _, r := range results {
		scoreMap[r.docID] = r.score
	}
	assert.Greater(t, scoreMap[1], scoreMap[4])
	assert.Greater(t, scoreMap[3], scoreMap[4])
}

func TestFuseRRF_CandidateLimit(t *testing.T) {
	lists := []rankedList{
		{docIDs: []int64{1, 2, 3, 4, 5}, weight: 1.0},
	}
	results := fuseRRF(lists, 2)
	assert.Len(t, results, 2)
}

func TestFuseRRF_TopRankBonus(t *testing.T) {
	lists := []rankedList{
		{docIDs: []int64{10, 20, 30, 40}, weight: 1.0},
	}
	results := fuseRRF(lists, 10)
	require.Len(t, results, 4)
	assert.Equal(t, int64(10), results[0].docID)
	gap01 := results[0].score - results[1].score
	gap12 := results[1].score - results[2].score
	gap23 := results[2].score - results[3].score
	assert.Greater(t, gap01, gap23)
	_ = gap12
}

func TestBlendScores(t *testing.T) {
	tests := []struct {
		name    string
		rrfRank int
		want    float64
	}{
		{"rank 0 uses 0.75", 0, 0.75*0.8 + 0.25*0.6},
		{"rank 5 uses 0.60", 5, 0.60*0.8 + 0.40*0.6},
		{"rank 15 uses 0.40", 15, 0.40*0.8 + 0.60*0.6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blendScores(0.8, 0.6, tt.rrfRank)
			assert.InDelta(t, tt.want, got, 0.001)
		})
	}
}

func TestParseExpansionResponse(t *testing.T) {
	resp := "LEX: golang, concurrency, goroutines\nVEC: go programming language; parallel execution\nHYDE: Go is a systems language with built-in concurrency"
	lex, vec, hyde := parseExpansionResponse(resp)
	assert.Equal(t, []string{"golang", "concurrency", "goroutines"}, lex)
	assert.Equal(t, []string{"go programming language", "parallel execution"}, vec)
	assert.Equal(t, []string{"Go is a systems language with built-in concurrency"}, hyde)
}

func TestParseExpansionResponse_Empty(t *testing.T) {
	lex, vec, hyde := parseExpansionResponse("")
	assert.Nil(t, lex)
	assert.Nil(t, vec)
	assert.Nil(t, hyde)
}

func TestFormatAndParseCachedExpansion(t *testing.T) {
	lex := []string{"a", "b"}
	vec := []string{"x y"}
	hyde := []string{"doc snippet"}
	cached := formatCachedExpansion(lex, vec, hyde)
	gotLex, gotVec, gotHyde := parseExpansionResponse(cached)
	assert.Equal(t, lex, gotLex)
	assert.Equal(t, vec, gotVec)
	assert.Equal(t, hyde, gotHyde)
}

func TestSelectBestChunk(t *testing.T) {
	chunks := []Chunk{
		{Text: "Introduction to databases", Pos: 0, Seq: 0},
		{Text: "Go concurrency patterns with goroutines", Pos: 100, Seq: 1},
		{Text: "Summary and conclusion", Pos: 200, Seq: 2},
	}
	best := selectBestChunk(chunks, []string{"concurrency", "goroutines"}, nil)
	assert.Equal(t, chunks[1], best)
}

func TestSelectBestChunk_IntentWeighting(t *testing.T) {
	chunks := []Chunk{
		{Text: "Go concurrency basics", Pos: 0, Seq: 0},
		{Text: "Concurrency patterns for performance", Pos: 100, Seq: 1},
	}
	best := selectBestChunk(chunks, []string{"concurrency"}, []string{"performance"})
	assert.Equal(t, chunks[1], best)
}

func TestSelectBestChunk_Empty(t *testing.T) {
	best := selectBestChunk(nil, []string{"test"}, nil)
	assert.Equal(t, Chunk{}, best)
}

func TestIsStrongSignal(t *testing.T) {
	high := []SearchResult{{Score: 0.90}, {Score: 0.50}}
	assert.True(t, isStrongSignal(high, ""))

	withIntent := []SearchResult{{Score: 0.90}, {Score: 0.50}}
	assert.False(t, isStrongSignal(withIntent, "some intent"))

	close := []SearchResult{{Score: 0.90}, {Score: 0.85}}
	assert.False(t, isStrongSignal(close, ""))

	low := []SearchResult{{Score: 0.50}, {Score: 0.20}}
	assert.False(t, isStrongSignal(low, ""))

	single := []SearchResult{{Score: 0.95}}
	assert.False(t, isStrongSignal(single, ""))
}

func TestLLMCache(t *testing.T) {
	d := setupTestDB(t)
	defer func() { _ = d.Close() }()

	_, ok := lookupLLMCache(d, "testkey")
	assert.False(t, ok)

	storeLLMCache(d, "testkey", "testvalue")
	val, ok := lookupLLMCache(d, "testkey")
	assert.True(t, ok)
	assert.Equal(t, "testvalue", val)

	storeLLMCache(d, "testkey", "updated")
	val, ok = lookupLLMCache(d, "testkey")
	assert.True(t, ok)
	assert.Equal(t, "updated", val)
}

func TestRerankCacheKey(t *testing.T) {
	k1 := rerankCacheKey("query1", "chunk1")
	k2 := rerankCacheKey("query1", "chunk2")
	k3 := rerankCacheKey("query2", "chunk1")
	assert.NotEqual(t, k1, k2)
	assert.NotEqual(t, k1, k3)
	assert.Len(t, k1, 64)
}

func TestQueryExpansionCacheKey(t *testing.T) {
	k1 := queryExpansionCacheKey("test query")
	k2 := queryExpansionCacheKey("other query")
	assert.NotEqual(t, k1, k2)
	assert.Len(t, k1, 64)
}

func TestDocSources(t *testing.T) {
	lists := []rankedList{
		{docIDs: []int64{1, 2, 3}, weight: 1.0},
		{docIDs: []int64{2, 4}, weight: 1.0},
	}
	labels := []string{"fts:query", "vec:query"}

	sources := docSources(2, lists, labels)
	assert.Equal(t, []string{"fts:query", "vec:query"}, sources)

	sources = docSources(4, lists, labels)
	assert.Equal(t, []string{"vec:query"}, sources)

	sources = docSources(99, lists, labels)
	assert.Nil(t, sources)
}

func TestValidateSearchQueries(t *testing.T) {
	assert.NoError(t, validateSearchQueries([]SearchQuery{
		{Type: "lex", Query: "test"},
		{Type: "vec", Query: "test"},
		{Type: "hyde", Query: "test"},
	}))

	assert.Error(t, validateSearchQueries([]SearchQuery{
		{Type: "bad", Query: "test"},
	}))

	assert.Error(t, validateSearchQueries([]SearchQuery{
		{Type: "lex", Query: "test\ninjection"},
	}))
}
