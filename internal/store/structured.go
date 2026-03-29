package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"github.com/user/qmd-go/internal/provider"
)

// StructuredSearch runs a search with pre-expanded queries, used by MCP/HTTP callers.
// Differs from HybridQuery: no query expansion, only first list gets 2x weight,
// collection filtering across multiple collections.
func StructuredSearch(
	ctx context.Context,
	d *sql.DB,
	req StructuredSearchRequest,
	embedder provider.Embedder,
	reranker provider.Reranker,
) ([]HybridResult, error) {
	if err := validateSearchQueries(req.Searches); err != nil {
		return nil, err
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	candidateLimit := req.CandidateLimit
	if candidateLimit <= 0 {
		candidateLimit = 40
	}

	lists, sourceLabels, primaryQuery := buildStructuredLists(ctx, d, req, embedder, candidateLimit)
	if len(lists) == 0 {
		return nil, nil
	}

	fused := fuseRRF(lists, candidateLimit)
	if len(fused) == 0 {
		return nil, nil
	}

	opts := HybridOpts{Limit: limit, MinScore: req.MinScore, Intent: req.Intent, Explain: req.Explain}
	scored, contentMap, docResults, err := scoreDocuments(ctx, d, primaryQuery, reranker, fused, lists, sourceLabels, opts)
	if err != nil {
		return nil, err
	}

	return collectHybridResults(scored, contentMap, docResults, primaryQuery, opts)
}

func validateSearchQueries(searches []SearchQuery) error {
	for _, sq := range searches {
		if strings.ContainsAny(sq.Query, "\n\r") {
			return fmt.Errorf("query contains newlines: %q", sq.Query)
		}
		switch sq.Type {
		case "lex", "vec", "hyde":
		default:
			return fmt.Errorf("invalid query type: %q", sq.Type)
		}
	}
	return nil
}

func buildStructuredLists(
	ctx context.Context,
	d *sql.DB,
	req StructuredSearchRequest,
	embedder provider.Embedder,
	candidateLimit int,
) ([]rankedList, []string, string) {
	var lists []rankedList
	var sourceLabels []string
	var primaryQuery string

	for i, sq := range req.Searches {
		weight := 1.0
		if i == 0 {
			weight = 2.0
		}

		switch sq.Type {
		case "lex":
			if primaryQuery == "" {
				primaryQuery = sq.Query
			}
			results := searchCollections(d, sq.Query, candidateLimit, req.Collections)
			lists = append(lists, rankedList{docIDs: resultDocIDs(results), weight: weight})
			sourceLabels = append(sourceLabels, fmt.Sprintf("lex:%s", sq.Query))

		case "vec", "hyde":
			if primaryQuery == "" {
				primaryQuery = sq.Query
			}
			if embedder == nil {
				continue
			}
			embeddings, err := embedder.Embed(ctx, []string{sq.Query}, provider.EmbedOpts{IsQuery: true})
			if err != nil {
				slog.Warn("structured embed failed", "query", sq.Query, "error", err)
				continue
			}
			results := vecSearchCollections(d, embeddings[0], candidateLimit, req.Collections)
			lists = append(lists, rankedList{docIDs: resultDocIDs(results), weight: weight})
			sourceLabels = append(sourceLabels, fmt.Sprintf("%s:%s", sq.Type, sq.Query))
		}
	}

	return lists, sourceLabels, primaryQuery
}

// searchCollections runs FTS across multiple collections, merging results.
func searchCollections(d *sql.DB, query string, limit int, collections []string) []SearchResult {
	if len(collections) == 0 {
		results, err := SearchFTS(d, query, SearchOpts{Limit: limit})
		if err != nil {
			slog.Warn("structured fts failed", "error", err)
			return nil
		}
		return results
	}

	var all []SearchResult
	for _, col := range collections {
		results, err := SearchFTS(d, query, SearchOpts{Limit: limit, Collection: col})
		if err != nil {
			slog.Warn("structured fts failed", "collection", col, "error", err)
			continue
		}
		all = append(all, results...)
	}
	return all
}

// vecSearchCollections runs vector search across multiple collections.
func vecSearchCollections(d *sql.DB, embedding []float32, limit int, collections []string) []SearchResult {
	if len(collections) == 0 {
		results, err := VectorSearch(d, embedding, VectorSearchOpts{Limit: limit})
		if err != nil {
			slog.Warn("structured vec failed", "error", err)
			return nil
		}
		return results
	}

	var all []SearchResult
	for _, col := range collections {
		results, err := VectorSearch(d, embedding, VectorSearchOpts{Limit: limit, Collection: col})
		if err != nil {
			slog.Warn("structured vec failed", "collection", col, "error", err)
			continue
		}
		all = append(all, results...)
	}
	return all
}
