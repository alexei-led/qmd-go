// Package openclaw provides OpenClaw sidecar integration for QMD.
package openclaw

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/user/qmd-go/internal/provider"
	"github.com/user/qmd-go/internal/store"
)

// Default configuration for OpenClaw memory search.
const (
	DefaultVectorWeight      = 0.7
	DefaultTextWeight        = 0.3
	DefaultMMRLambda         = 0.7
	DefaultTemporalHalfLife  = 30 * 24 * time.Hour // 30 days
	DefaultMaxResults        = 8
	DefaultCandidateMultiple = 4
	MaxSnippetChars          = 700
)

// MemorySearchConfig holds tunable parameters for memory_search.
type MemorySearchConfig struct {
	VectorWeight        float64       `json:"vectorWeight"`
	TextWeight          float64       `json:"textWeight"`
	MMRLambda           float64       `json:"mmrLambda"`
	TemporalHalfLife    time.Duration `json:"-"`
	MaxResults          int           `json:"maxResults"`
	CandidateMultiplier int           `json:"candidateMultiplier"`
}

// DefaultMemorySearchConfig returns the default configuration.
func DefaultMemorySearchConfig() MemorySearchConfig {
	return MemorySearchConfig{
		VectorWeight:        DefaultVectorWeight,
		TextWeight:          DefaultTextWeight,
		MMRLambda:           DefaultMMRLambda,
		TemporalHalfLife:    DefaultTemporalHalfLife,
		MaxResults:          DefaultMaxResults,
		CandidateMultiplier: DefaultCandidateMultiple,
	}
}

// MemorySearchResult is a single result from memory_search.
type MemorySearchResult struct {
	Path      string  `json:"path"`
	Title     string  `json:"title"`
	Snippet   string  `json:"snippet"`
	Score     float64 `json:"score"`
	LineStart int     `json:"lineStart,omitempty"`
	LineEnd   int     `json:"lineEnd,omitempty"`
}

// memorySearchTool returns the MCP tool definition for memory_search.
func memorySearchTool() mcp.Tool {
	return mcp.NewToolWithRawSchema("memory_search",
		"Semantic search over workspace memory files with MMR diversity and temporal decay",
		json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": { "type": "string", "description": "Search query text" },
				"limit": { "type": "number", "description": "Maximum results (default 8)" },
				"vectorWeight": { "type": "number", "description": "Weight for vector similarity (default 0.7)" },
				"textWeight": { "type": "number", "description": "Weight for BM25 text search (default 0.3)" }
			},
			"required": ["query"]
		}`),
	)
}

// memorySearchHandler returns the handler for memory_search.
func memorySearchHandler(
	db *sql.DB,
	embedder provider.Embedder,
	reranker provider.Reranker,
	cfg MemorySearchConfig,
) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, err := req.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError("query parameter is required"), nil
		}

		limit := req.GetInt("limit", cfg.MaxResults)
		if limit <= 0 {
			limit = cfg.MaxResults
		}

		vecWeight := req.GetFloat("vectorWeight", cfg.VectorWeight)
		txtWeight := req.GetFloat("textWeight", cfg.TextWeight)

		candidateLimit := limit * cfg.CandidateMultiplier

		var searches []store.SearchQuery
		searches = append(searches, store.SearchQuery{Type: "vec", Query: query})
		searches = append(searches, store.SearchQuery{Type: "lex", Query: query})

		ssReq := store.StructuredSearchRequest{
			Searches:       searches,
			Limit:          candidateLimit,
			CandidateLimit: candidateLimit,
			Collections:    []string{MemoryCollectionName},
		}

		results, err := store.StructuredSearch(ctx, db, ssReq, embedder, reranker)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
		}

		scored := applyWeightedScoring(results, vecWeight, txtWeight)
		scored = applyTemporalDecay(db, scored, cfg.TemporalHalfLife)
		selected := applyMMR(scored, cfg.MMRLambda, limit)

		out := make([]MemorySearchResult, 0, len(selected))
		for _, s := range selected {
			snippet := s.Snippet
			if len(snippet) > MaxSnippetChars {
				snippet = snippet[:MaxSnippetChars]
			}
			out = append(out, MemorySearchResult{
				Path:      s.Path,
				Title:     s.Title,
				Snippet:   snippet,
				Score:     s.Score,
				LineStart: s.LineStart,
				LineEnd:   s.LineEnd,
			})
		}

		data, _ := json.Marshal(out)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// memoryGetTool returns the MCP tool definition for memory_get.
func memoryGetTool() mcp.Tool {
	return mcp.NewTool("memory_get",
		mcp.WithDescription("Get memory file content by path. Returns empty text for missing files."),
		mcp.WithString("path", mcp.Required(), mcp.Description("File path to retrieve")),
	)
}

// memoryGetHandler returns the handler for memory_get.
func memoryGetHandler(db *sql.DB) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path, err := req.RequireString("path")
		if err != nil {
			return mcp.NewToolResultError("path parameter is required"), nil
		}

		doc, notFound, err := store.FindDocument(db, path, store.FindDocumentOpts{IncludeBody: false})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("error: %v", err)), nil
		}
		if notFound != nil {
			// Return empty text for missing files, not error.
			result := map[string]any{"path": path, "body": "", "found": false}
			data, _ := json.Marshal(result)
			return mcp.NewToolResultText(string(data)), nil
		}

		body, err := store.GetDocumentBody(db, doc.Filepath, 1, 0)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("error reading body: %v", err)), nil
		}

		result := map[string]any{
			"path":  doc.Filepath,
			"title": doc.Title,
			"body":  body,
			"found": true,
		}
		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// scoredResult wraps a HybridResult with additional scoring metadata.
type scoredResult struct {
	store.HybridResult
	adjustedScore float64
}

const (
	decayBase       = 2.0
	similarityBlend = 0.5
	scoreBaseline   = 0.5
)

// applyWeightedScoring blends vector and text scores using the configured weights.
// Since StructuredSearch already fuses scores via RRF, we scale the combined score
// by the dominant weight preference.
func applyWeightedScoring(results []store.HybridResult, vecWeight, txtWeight float64) []scoredResult {
	total := vecWeight + txtWeight
	if total == 0 {
		total = 1
	}
	normVec := vecWeight / total
	normTxt := txtWeight / total

	blended := make([]scoredResult, len(results))
	for i, r := range results {
		var weight float64
		if r.Explain != nil && r.Explain.RerankScore > 0 {
			weight = normVec
		} else {
			weight = normTxt + (normVec-normTxt)*similarityBlend
		}
		blended[i] = scoredResult{
			HybridResult:  r,
			adjustedScore: r.Score * (scoreBaseline + weight),
		}
	}
	return blended
}

// applyTemporalDecay applies exponential decay based on document modification time.
// Score *= 2^(-age/halfLife)
func applyTemporalDecay(db *sql.DB, results []scoredResult, halfLife time.Duration) []scoredResult {
	now := time.Now()
	for i := range results {
		modTime := getDocModifiedAt(db, results[i].Path)
		if modTime.IsZero() {
			continue
		}
		age := max(now.Sub(modTime), 0)
		decay := math.Pow(decayBase, -float64(age)/float64(halfLife))
		results[i].adjustedScore *= decay
	}
	return results
}

// ApplyTemporalDecayScore applies temporal decay to a single score (exported for testing).
func ApplyTemporalDecayScore(score float64, age, halfLife time.Duration) float64 {
	age = max(age, 0)
	return score * math.Pow(decayBase, -float64(age)/float64(halfLife))
}

// applyMMR applies Maximal Marginal Relevance for diversity.
// MMR = lambda * sim(doc, query) - (1-lambda) * max(sim(doc, selected_docs))
// Since we don't have raw embeddings here, we approximate inter-document similarity
// using path-based overlap and score proximity as a diversity proxy.
func applyMMR(results []scoredResult, lambda float64, limit int) []scoredResult {
	if len(results) <= limit {
		return results
	}

	selected := make([]scoredResult, 0, limit)
	remaining := make([]scoredResult, len(results))
	copy(remaining, results)

	for len(selected) < limit && len(remaining) > 0 {
		bestIdx := -1
		bestMMR := math.Inf(-1)

		for i, cand := range remaining {
			relevance := cand.adjustedScore
			maxSim := 0.0
			for _, sel := range selected {
				sim := pathSimilarity(cand.Path, sel.Path)
				scoreSim := 1.0 - math.Abs(cand.adjustedScore-sel.adjustedScore)
				combined := similarityBlend*sim + similarityBlend*scoreSim
				if combined > maxSim {
					maxSim = combined
				}
			}
			mmr := lambda*relevance - (1-lambda)*maxSim
			if mmr > bestMMR {
				bestMMR = mmr
				bestIdx = i
			}
		}

		if bestIdx < 0 {
			break
		}
		selected = append(selected, remaining[bestIdx])
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}

	return selected
}

// pathSimilarity returns a simple similarity between two paths based on shared components.
func pathSimilarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	partsA := splitPath(a)
	partsB := splitPath(b)

	shared := 0
	total := max(len(partsA), len(partsB))
	if total == 0 {
		return 0
	}

	setB := make(map[string]bool, len(partsB))
	for _, p := range partsB {
		setB[p] = true
	}
	for _, p := range partsA {
		if setB[p] {
			shared++
		}
	}
	return float64(shared) / float64(total)
}

func splitPath(p string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(p); i++ {
		if i == len(p) || p[i] == '/' {
			if i > start {
				parts = append(parts, p[start:i])
			}
			start = i + 1
		}
	}
	return parts
}

func getDocModifiedAt(db *sql.DB, path string) time.Time {
	var modAt string
	err := db.QueryRow(`SELECT modified_at FROM documents WHERE path = ? AND active = 1`, path).Scan(&modAt)
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, modAt)
	if err != nil {
		t, err = time.Parse("2006-01-02", modAt)
		if err != nil {
			return time.Time{}
		}
	}
	return t
}
