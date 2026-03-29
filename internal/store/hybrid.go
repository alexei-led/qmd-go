package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/user/qmd-go/internal/provider"
)

// Hybrid search constants — must match TS exactly.
const (
	StrongSignalMinScore = 0.85
	IntentWeightChunk    = 0.5
	bm25ProbeLimit       = 20
	strongSignalGap      = 0.15
)

// HybridOpts configures a hybrid query.
type HybridOpts struct {
	Limit          int
	MinScore       float64
	CandidateLimit int
	Collection     string
	SearchAll      bool
	Intent         string
	NoRerank       bool
	Explain        bool
	ContextLines   int
	ShowFull       bool
}

// HybridResult extends SearchResult with explain trace.
type HybridResult struct {
	SearchResult
	Explain *ExplainTrace `json:"explain,omitempty"`
}

// ExplainTrace holds RRF scoring details.
type ExplainTrace struct {
	RRFScore    float64  `json:"rrfScore"`
	RRFRank     int      `json:"rrfRank"`
	RerankScore float64  `json:"rerankScore,omitempty"`
	BlendWeight float64  `json:"blendWeight"`
	FinalScore  float64  `json:"finalScore"`
	Sources     []string `json:"sources"`
}

// chunkInfo holds a selected chunk for a document.
type chunkInfo struct {
	text string
	pos  int
}

// scoredDoc holds a document with its blended score.
type scoredDoc struct {
	docID int64
	score float64
	trace *ExplainTrace
}

// HybridQuery runs the full 8-step hybrid search pipeline.
func HybridQuery(
	ctx context.Context,
	d *sql.DB,
	query string,
	embedder provider.Embedder,
	reranker provider.Reranker,
	generator provider.Generator,
	opts HybridOpts,
) ([]HybridResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	if opts.CandidateLimit <= 0 {
		opts.CandidateLimit = 40 //nolint:mnd
	}

	// Steps 1-3: Probe, expand, build ranked lists.
	lists, sourceLabels, err := buildRankedLists(ctx, d, query, embedder, generator, opts)
	if err != nil {
		return nil, err
	}
	if len(lists) == 0 {
		return nil, nil
	}

	// Step 4: RRF Fusion.
	fused := fuseRRF(lists, opts.CandidateLimit)
	if len(fused) == 0 {
		return nil, nil
	}

	// Steps 5-7: Load content, chunk, rerank, blend.
	scored, contentMap, docResults, err := scoreDocuments(ctx, d, query, reranker, fused, lists, sourceLabels, opts)
	if err != nil {
		return nil, err
	}

	// Step 8: Dedup + filter.
	return collectHybridResults(scored, contentMap, docResults, query, opts)
}

// buildRankedLists performs steps 1-3: BM25 probe, query expansion, FTS+vec search.
func buildRankedLists(
	ctx context.Context,
	d *sql.DB,
	query string,
	embedder provider.Embedder,
	generator provider.Generator,
	opts HybridOpts,
) ([]rankedList, []string, error) {
	// Step 1: BM25 Probe.
	probeResults, err := SearchFTS(d, query, SearchOpts{
		Limit: bm25ProbeLimit, Collection: opts.Collection, SearchAll: opts.SearchAll,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("bm25 probe: %w", err)
	}

	strongSignal := isStrongSignal(probeResults, opts.Intent)
	slog.Debug("hybrid step 1", "probeResults", len(probeResults), "strongSignal", strongSignal)

	// Step 2: Query Expansion.
	var lexQueries, vecQueries, hydeQueries []string
	if !strongSignal {
		lexQueries, vecQueries, hydeQueries = expandQuery(ctx, d, query, generator)
	}

	allLex := append([]string{query}, lexQueries...)
	allVec := append([]string{query}, vecQueries...)
	allVec = append(allVec, hydeQueries...)

	// Step 3: Route by type.
	var lists []rankedList
	var sourceLabels []string

	for i, q := range allLex {
		results, ftsErr := SearchFTS(d, q, SearchOpts{
			Limit: opts.CandidateLimit, Collection: opts.Collection, SearchAll: opts.SearchAll,
		})
		if ftsErr != nil {
			slog.Warn("fts expansion failed", "query", q, "error", ftsErr)
			continue
		}
		weight := 1.0
		if i < 2 { //nolint:mnd
			weight = 2.0
		}
		lists = append(lists, rankedList{docIDs: resultDocIDs(results), weight: weight})
		sourceLabels = append(sourceLabels, fmt.Sprintf("fts:%s", q))
	}

	vecLists, vecLabels := buildVecLists(ctx, embedder, d, allVec, opts)
	lists = append(lists, vecLists...)
	sourceLabels = append(sourceLabels, vecLabels...)

	return lists, sourceLabels, nil
}

func buildVecLists(ctx context.Context, embedder provider.Embedder, d *sql.DB, allVec []string, opts HybridOpts) ([]rankedList, []string) {
	if embedder == nil || len(allVec) == 0 {
		return nil, nil
	}
	embeddings, err := embedder.Embed(ctx, allVec, provider.EmbedOpts{IsQuery: true})
	if err != nil {
		slog.Warn("embed queries failed", "error", err)
		return nil, nil
	}

	var lists []rankedList
	var labels []string
	for i, emb := range embeddings {
		results, vecErr := VectorSearch(d, emb, VectorSearchOpts{
			Limit: opts.CandidateLimit, Collection: opts.Collection, SearchAll: opts.SearchAll,
		})
		if vecErr != nil {
			slog.Warn("vec search failed", "error", vecErr)
			continue
		}
		weight := 1.0
		if i < 2 { //nolint:mnd
			weight = 2.0
		}
		lists = append(lists, rankedList{docIDs: resultDocIDs(results), weight: weight})
		labels = append(labels, fmt.Sprintf("vec:%s", allVec[i]))
	}
	return lists, labels
}

// scoreDocuments performs steps 5-7: chunk selection, reranking, score blending.
func scoreDocuments(
	ctx context.Context,
	d *sql.DB,
	query string,
	reranker provider.Reranker,
	fused []rrfResult,
	lists []rankedList,
	sourceLabels []string,
	opts HybridOpts,
) ([]scoredDoc, map[int64]string, map[int64]SearchResult, error) {
	contentMap := make(map[int64]string, len(fused))
	docResults := make(map[int64]SearchResult, len(fused))
	for _, r := range fused {
		sr, body, loadErr := loadDocContent(d, r.docID)
		if loadErr != nil {
			slog.Warn("load doc content", "docID", r.docID, "error", loadErr)
			continue
		}
		contentMap[r.docID] = body
		docResults[r.docID] = sr
	}

	// Step 5: Best chunk selection.
	queryTerms := extractTerms(query)
	intentTerms := extractIntentTerms(opts.Intent)
	bestChunks := make(map[int64]chunkInfo, len(fused))
	for docID, body := range contentMap {
		chunks := ChunkDocument(body)
		best := selectBestChunk(chunks, queryTerms, intentTerms)
		bestChunks[docID] = chunkInfo{text: best.Text, pos: best.Pos}
	}

	// Step 6: Rerank.
	rerankScores := make(map[int64]float64, len(fused))
	if !opts.NoRerank && reranker != nil && len(bestChunks) > 0 {
		var err error
		rerankScores, err = rerankChunks(ctx, d, reranker, query, fused, bestChunks)
		if err != nil {
			slog.Warn("rerank failed, skipping", "error", err)
		}
	}

	// Step 7: Blend.
	scored := make([]scoredDoc, 0, len(fused))
	for _, r := range fused {
		if _, ok := contentMap[r.docID]; !ok {
			continue
		}
		rs := rerankScores[r.docID]
		final := blendScores(r.score, rs, r.rank)
		var trace *ExplainTrace
		if opts.Explain {
			trace = buildTrace(r, rs, final, lists, sourceLabels)
		}
		scored = append(scored, scoredDoc{docID: r.docID, score: final, trace: trace})
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	return scored, contentMap, docResults, nil
}

func buildTrace(r rrfResult, rerankScore, finalScore float64, lists []rankedList, labels []string) *ExplainTrace {
	w := 0.40
	switch {
	case r.rank <= 2: //nolint:mnd
		w = 0.75
	case r.rank <= 9: //nolint:mnd
		w = 0.60
	}
	return &ExplainTrace{
		RRFScore: r.score, RRFRank: r.rank, RerankScore: rerankScore,
		BlendWeight: w, FinalScore: finalScore, Sources: docSources(r.docID, lists, labels),
	}
}

// collectHybridResults performs step 8: dedup, filter, format.
func collectHybridResults(
	scored []scoredDoc,
	contentMap map[int64]string,
	docResults map[int64]SearchResult,
	query string,
	opts HybridOpts,
) ([]HybridResult, error) {
	seen := make(map[int64]bool)
	var results []HybridResult
	for _, sd := range scored {
		if seen[sd.docID] {
			continue
		}
		if opts.MinScore > 0 && sd.score < opts.MinScore {
			continue
		}
		seen[sd.docID] = true

		sr := docResults[sd.docID]
		sr.Score = sd.score

		if opts.ShowFull {
			sr.Body = contentMap[sd.docID]
		} else {
			snip := ExtractSnippet(contentMap[sd.docID], query, opts.Intent, opts.ContextLines)
			sr.Snippet = snip.Text
			sr.LineStart = snip.LineStart
			sr.LineEnd = snip.LineEnd
		}

		results = append(results, HybridResult{SearchResult: sr, Explain: sd.trace})
		if len(results) >= opts.Limit {
			break
		}
	}
	return results, nil
}

func isStrongSignal(results []SearchResult, intent string) bool {
	if intent != "" {
		return false
	}
	if len(results) < 2 { //nolint:mnd
		return false
	}
	top := results[0].Score
	second := results[1].Score
	return top >= StrongSignalMinScore && (top-second) >= strongSignalGap
}

func resultDocIDs(results []SearchResult) []int64 {
	ids := make([]int64, len(results))
	for i, r := range results {
		ids[i] = r.DocID
	}
	return ids
}

func loadDocContent(d *sql.DB, docID int64) (SearchResult, string, error) {
	var sr SearchResult
	var body string
	err := d.QueryRow(`
		SELECT d.id, d.collection, d.path, d.title, d.hash, c.doc
		FROM documents d
		JOIN content c ON c.hash = d.hash
		WHERE d.id = ? AND d.active = 1`, docID,
	).Scan(&sr.DocID, &sr.Collection, &sr.Path, &sr.Title, &sr.Hash, &body)
	if err != nil {
		return sr, "", err
	}
	return sr, body, nil
}

// selectBestChunk picks the chunk with highest keyword overlap.
func selectBestChunk(chunks []Chunk, queryTerms, intentTerms []string) Chunk {
	if len(chunks) == 0 {
		return Chunk{}
	}
	best := 0
	bestScore := -1.0
	for i, c := range chunks {
		lower := strings.ToLower(c.Text)
		score := 0.0
		for _, t := range queryTerms {
			if strings.Contains(lower, t) {
				score += 1.0
			}
		}
		for _, t := range intentTerms {
			if strings.Contains(lower, t) {
				score += IntentWeightChunk
			}
		}
		if score > bestScore {
			bestScore = score
			best = i
		}
	}
	return chunks[best]
}

// expandQuery calls the generator to produce lex/vec/hyde variants.
func expandQuery(ctx context.Context, d *sql.DB, query string, gen provider.Generator) (lex, vec, hyde []string) {
	if gen == nil {
		return nil, nil, nil
	}

	cacheKey := queryExpansionCacheKey(query)
	if cached, ok := lookupLLMCache(d, cacheKey); ok {
		return parseExpansionResponse(cached)
	}

	prompt := fmt.Sprintf(
		`Given the search query: "%s"
Generate search expansions in this exact format:
LEX: term1, term2, term3
VEC: semantic variation 1; semantic variation 2
HYDE: hypothetical document snippet

Keep it brief.`, query)

	result, err := gen.Generate(ctx, []provider.Message{
		{Role: "system", Content: "You are a search query expansion assistant. Respond only in the specified format."},
		{Role: "user", Content: prompt},
	}, provider.GenOpts{MaxTokens: 200, Temperature: 0.3}) //nolint:mnd
	if err != nil {
		slog.Warn("query expansion failed", "error", err)
		return nil, nil, nil
	}

	lex, vec, hyde = parseExpansionResponse(result)
	storeLLMCache(d, cacheKey, formatCachedExpansion(lex, vec, hyde))
	return lex, vec, hyde
}

func queryExpansionCacheKey(query string) string {
	h := sha256.Sum256([]byte("expand:" + query))
	return hex.EncodeToString(h[:])
}

func lookupLLMCache(d *sql.DB, hash string) (string, bool) {
	var result string
	err := d.QueryRow(`SELECT result FROM llm_cache WHERE hash = ?`, hash).Scan(&result)
	if err != nil {
		return "", false
	}
	return result, true
}

func storeLLMCache(d *sql.DB, hash, result string) {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.Exec(`INSERT OR REPLACE INTO llm_cache (hash, result, created_at) VALUES (?, ?, ?)`, hash, result, now)
	if err != nil {
		slog.Warn("store llm cache", "error", err)
	}
}

func parseExpansionResponse(resp string) (lex, vec, hyde []string) {
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "LEX:"):
			for _, t := range strings.Split(strings.TrimPrefix(line, "LEX:"), ",") {
				if t = strings.TrimSpace(t); t != "" {
					lex = append(lex, t)
				}
			}
		case strings.HasPrefix(line, "VEC:"):
			for _, t := range strings.Split(strings.TrimPrefix(line, "VEC:"), ";") {
				if t = strings.TrimSpace(t); t != "" {
					vec = append(vec, t)
				}
			}
		case strings.HasPrefix(line, "HYDE:"):
			if t := strings.TrimSpace(strings.TrimPrefix(line, "HYDE:")); t != "" {
				hyde = append(hyde, t)
			}
		}
	}
	return lex, vec, hyde
}

func formatCachedExpansion(lex, vec, hyde []string) string {
	return fmt.Sprintf("LEX:%s\nVEC:%s\nHYDE:%s",
		strings.Join(lex, ","),
		strings.Join(vec, ";"),
		strings.Join(hyde, ";"))
}

// rerankChunks calls the reranker on best chunks and caches results.
func rerankChunks(
	ctx context.Context,
	d *sql.DB,
	rnk provider.Reranker,
	query string,
	fused []rrfResult,
	bestChunks map[int64]chunkInfo,
) (map[int64]float64, error) {
	scores := make(map[int64]float64, len(fused))

	var docs []provider.RerankDoc
	var docIDs []int64
	for _, r := range fused {
		bc, ok := bestChunks[r.docID]
		if !ok || bc.text == "" {
			continue
		}

		cacheKey := rerankCacheKey(query, bc.text)
		if cached, ok := lookupLLMCache(d, cacheKey); ok {
			var score float64
			if _, err := fmt.Sscanf(cached, "%f", &score); err == nil {
				scores[r.docID] = score
				continue
			}
		}

		docs = append(docs, provider.RerankDoc{Text: bc.text})
		docIDs = append(docIDs, r.docID)
	}

	if len(docs) == 0 {
		return scores, nil
	}

	results, err := rnk.Rerank(ctx, query, docs, provider.RerankOpts{})
	if err != nil {
		return scores, err
	}

	for _, rr := range results {
		if rr.Index < len(docIDs) {
			docID := docIDs[rr.Index]
			scores[docID] = rr.Score
			bc := bestChunks[docID]
			cacheKey := rerankCacheKey(query, bc.text)
			storeLLMCache(d, cacheKey, fmt.Sprintf("%f", rr.Score))
		}
	}

	return scores, nil
}

func rerankCacheKey(query, chunkText string) string {
	h := sha256.Sum256([]byte("rerank:" + query + ":" + chunkText))
	return hex.EncodeToString(h[:])
}

// docSources finds which source lists a docID appeared in.
func docSources(docID int64, lists []rankedList, labels []string) []string {
	var sources []string
	for i, list := range lists {
		for _, id := range list.docIDs {
			if id == docID {
				if i < len(labels) {
					sources = append(sources, labels[i])
				}
				break
			}
		}
	}
	return sources
}
