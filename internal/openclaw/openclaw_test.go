package openclaw

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	gomcp "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/qmd-go/internal/config"
	"github.com/user/qmd-go/internal/db"
	"github.com/user/qmd-go/internal/store"
)

func setupTestDB(t *testing.T) *testEnv {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(dbPath)
	require.NoError(t, err)
	require.NoError(t, store.InitializeDatabase(d))
	t.Cleanup(func() { _ = d.Close() })

	cfg := &config.Config{}
	return &testEnv{db: d, cfg: cfg}
}

type testEnv struct {
	db  *sql.DB
	cfg *config.Config
}

func seedMemoryDoc(t *testing.T, env *testEnv, path, hash, title, body, modifiedAt string) {
	t.Helper()
	_, err := env.db.Exec(`INSERT OR IGNORE INTO store_collections (name, path) VALUES (?, ?)`,
		MemoryCollectionName, "/home/user/.openclaw/workspace/memory")
	require.NoError(t, err)
	_, err = env.db.Exec(`INSERT INTO content (hash, doc, created_at) VALUES (?, ?, ?)`, hash, body, modifiedAt)
	require.NoError(t, err)
	_, err = env.db.Exec(`INSERT INTO documents (collection, path, title, hash, active, created_at, modified_at)
		VALUES (?, ?, ?, ?, 1, ?, ?)`, MemoryCollectionName, path, title, hash, modifiedAt, modifiedAt)
	require.NoError(t, err)
}

func callToolReq(args map[string]any) gomcp.CallToolRequest {
	return gomcp.CallToolRequest{
		Params: gomcp.CallToolParams{
			Arguments: args,
		},
	}
}

func TestMemoryGetHandler_Found(t *testing.T) {
	env := setupTestDB(t)
	seedMemoryDoc(t, env, "memory/test.md", "hash1", "Test Memory", "memory content here", "2024-01-15")

	handler := memoryGetHandler(env.db)
	result, err := handler(context.Background(), callToolReq(map[string]any{
		"path": "memory/test.md",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.NotEmpty(t, result.Content)
	text := result.Content[0].(gomcp.TextContent).Text
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &doc))
	assert.Equal(t, true, doc["found"])
	assert.Contains(t, doc["body"], "memory content")
}

func TestMemoryGetHandler_NotFound(t *testing.T) {
	env := setupTestDB(t)

	handler := memoryGetHandler(env.db)
	result, err := handler(context.Background(), callToolReq(map[string]any{
		"path": "nonexistent.md",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.NotEmpty(t, result.Content)
	text := result.Content[0].(gomcp.TextContent).Text
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &doc))
	assert.Equal(t, false, doc["found"])
	assert.Equal(t, "", doc["body"])
}

func TestMemoryGetHandler_MissingPath(t *testing.T) {
	env := setupTestDB(t)

	handler := memoryGetHandler(env.db)
	result, err := handler(context.Background(), callToolReq(map[string]any{}))
	require.NoError(t, err)
	require.True(t, result.IsError)
}

func TestTemporalDecayScore(t *testing.T) {
	halfLife := 30 * 24 * time.Hour

	tests := []struct {
		name    string
		age     time.Duration
		wantMin float64
		wantMax float64
	}{
		{"zero age", 0, 0.99, 1.01},
		{"half life age", halfLife, 0.49, 0.51},
		{"double half life", 2 * halfLife, 0.24, 0.26},
		{"negative age", -time.Hour, 0.99, 1.01},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := applyTemporalDecayScore(1.0, tt.age, halfLife)
			assert.Greater(t, result, tt.wantMin, "score too low")
			assert.Less(t, result, tt.wantMax, "score too high")
		})
	}
}

func TestApplyMMR_DiversitySelection(t *testing.T) {
	results := []scoredResult{
		{HybridResult: store.HybridResult{SearchResult: store.SearchResult{Path: "memory/a.md", Score: 0.9}}, adjustedScore: 0.9},
		{HybridResult: store.HybridResult{SearchResult: store.SearchResult{Path: "memory/a.md", Score: 0.85}}, adjustedScore: 0.85},
		{HybridResult: store.HybridResult{SearchResult: store.SearchResult{Path: "memory/b.md", Score: 0.8}}, adjustedScore: 0.8},
		{HybridResult: store.HybridResult{SearchResult: store.SearchResult{Path: "memory/c.md", Score: 0.7}}, adjustedScore: 0.7},
	}

	selected := applyMMR(results, DefaultMMRLambda, 3)
	assert.Len(t, selected, 3)

	// First result should be the highest scoring
	assert.Equal(t, "memory/a.md", selected[0].Path)

	// MMR should prefer diverse paths over duplicates
	paths := make(map[string]int)
	for _, s := range selected {
		paths[s.Path]++
	}
	assert.LessOrEqual(t, paths["memory/a.md"], 2, "MMR should promote diversity")
}

func TestApplyMMR_FewResults(t *testing.T) {
	results := []scoredResult{
		{HybridResult: store.HybridResult{SearchResult: store.SearchResult{Path: "a.md"}}, adjustedScore: 0.9},
		{HybridResult: store.HybridResult{SearchResult: store.SearchResult{Path: "b.md"}}, adjustedScore: 0.8},
	}
	selected := applyMMR(results, DefaultMMRLambda, 5)
	assert.Len(t, selected, 2)
}

func TestPathSimilarity(t *testing.T) {
	assert.Equal(t, 1.0, pathSimilarity("memory/a.md", "memory/a.md"))
	assert.Greater(t, pathSimilarity("memory/a.md", "memory/b.md"), 0.0)
	assert.Less(t, pathSimilarity("foo/bar.md", "baz/qux.md"), pathSimilarity("memory/a.md", "memory/b.md"))
}

func TestApplyWeightedScoring(t *testing.T) {
	vecWeight := DefaultVectorWeight // 0.7
	txtWeight := DefaultTextWeight   // 0.3

	tests := []struct {
		name      string
		result    store.HybridResult
		wantDelta float64
	}{
		{
			"nil explain → text weight",
			store.HybridResult{SearchResult: store.SearchResult{Score: 1.0}},
			1.0 * (scoreBaseline + txtWeight),
		},
		{
			"vec only → vec weight",
			store.HybridResult{
				SearchResult: store.SearchResult{Score: 1.0},
				Explain:      &store.ExplainTrace{VecScore: 0.5},
			},
			1.0 * (scoreBaseline + vecWeight),
		},
		{
			"lex only → text weight",
			store.HybridResult{
				SearchResult: store.SearchResult{Score: 1.0},
				Explain:      &store.ExplainTrace{LexScore: 0.3},
			},
			1.0 * (scoreBaseline + txtWeight),
		},
		{
			"both → blended weight",
			store.HybridResult{
				SearchResult: store.SearchResult{Score: 1.0},
				Explain:      &store.ExplainTrace{VecScore: 0.4, LexScore: 0.2},
			},
			1.0 * (scoreBaseline + vecWeight*similarityBlend + txtWeight*(1-similarityBlend)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scored := applyWeightedScoring([]store.HybridResult{tt.result}, vecWeight, txtWeight)
			assert.InDelta(t, tt.wantDelta, scored[0].adjustedScore, 0.001)
		})
	}
}

func TestSetup_RegistersTools(t *testing.T) {
	env := setupTestDB(t)

	s := server.NewMCPServer("test", "1.0.0",
		server.WithToolCapabilities(false),
	)

	err := Setup(s, SetupOpts{
		DB:     env.db,
		Config: env.cfg,
	})
	require.NoError(t, err)

	// Verify collection was created
	var count int
	err = env.db.QueryRow(`SELECT COUNT(*) FROM store_collections WHERE name = ?`, MemoryCollectionName).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestEnsureMemoryCollection_Idempotent(t *testing.T) {
	env := setupTestDB(t)
	memDir := t.TempDir()

	require.NoError(t, config.RegisterCollection(env.db, env.cfg, "", MemoryCollectionName, memDir,
		config.WithPattern("**/*.md"),
		config.WithContext("OpenClaw workspace memory files"),
	))
	require.NoError(t, config.RegisterCollection(env.db, env.cfg, "", MemoryCollectionName, memDir,
		config.WithPattern("**/*.md"),
		config.WithContext("OpenClaw workspace memory files"),
	))

	var count int
	err := env.db.QueryRow(`SELECT COUNT(*) FROM store_collections WHERE name = ?`, MemoryCollectionName).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestDefaultMemorySearchConfig(t *testing.T) {
	cfg := DefaultMemorySearchConfig()
	assert.Equal(t, DefaultVectorWeight, cfg.VectorWeight)
	assert.Equal(t, DefaultTextWeight, cfg.TextWeight)
	assert.Equal(t, DefaultMMRLambda, cfg.MMRLambda)
	assert.Equal(t, DefaultMaxResults, cfg.MaxResults)
	assert.Equal(t, DefaultCandidateMultiple, cfg.CandidateMultiplier)
}
