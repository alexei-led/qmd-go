package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/user/qmd-go/internal/config"
	"github.com/user/qmd-go/internal/provider"
	"github.com/user/qmd-go/internal/store"
)

const (
	defaultMaxLines = 100
	defaultMaxBytes = 10240
)

// deps holds shared dependencies for tool handlers.
type deps struct {
	db       *sql.DB
	cfg      *config.Config
	embedder provider.Embedder
	reranker provider.Reranker
}

func queryTool() mcp.Tool {
	return mcp.NewToolWithRawSchema("query",
		"Hybrid search across indexed documents using lexical (BM25), vector, or HyDE queries",
		json.RawMessage(`{
			"type": "object",
			"properties": {
				"searches": {
					"type": "array",
					"items": {
						"type": "object",
						"properties": {
							"type": { "type": "string", "enum": ["lex", "vec", "hyde"] },
							"query": { "type": "string" }
						},
						"required": ["type", "query"]
					},
					"description": "Array of search queries with type (lex/vec/hyde) and query text"
				},
				"limit": { "type": "number", "default": 10, "description": "Maximum results to return" },
				"minScore": { "type": "number", "default": 0, "description": "Minimum score threshold" },
				"candidateLimit": { "type": "number", "default": 40, "description": "Candidate pool size for reranking" },
				"collections": {
					"type": "array",
					"items": { "type": "string" },
					"description": "Collections to search (empty = default collections)"
				},
				"intent": { "type": "string", "description": "Context about what you're looking for, improves snippet relevance" }
			},
			"required": ["searches"]
		}`),
	)
}

func queryHandler(d *deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args store.StructuredSearchRequest
		if err := req.BindArguments(&args); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		if len(args.Searches) == 0 {
			return mcp.NewToolResultError("searches array is required and must not be empty"), nil
		}

		results, err := store.StructuredSearch(ctx, d.db, args, d.embedder, d.reranker)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
		}

		out := make([]searchResultJSON, 0, len(results))
		for _, r := range results {
			out = append(out, searchResultJSON{
				Collection: r.Collection,
				Path:       r.Path,
				Title:      r.Title,
				Score:      r.Score,
				Snippet:    r.Snippet,
				DocID:      r.DocID,
				LineStart:  r.LineStart,
				LineEnd:    r.LineEnd,
			})
		}

		data, _ := json.Marshal(out)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// searchResultJSON is a subset of store.SearchResult for MCP tool output (omits Body/Hash).
type searchResultJSON struct {
	Collection string  `json:"collection"`
	Path       string  `json:"path"`
	Title      string  `json:"title"`
	Score      float64 `json:"score"`
	Snippet    string  `json:"snippet,omitempty"`
	DocID      int64   `json:"docId"`
	LineStart  int     `json:"lineStart,omitempty"`
	LineEnd    int     `json:"lineEnd,omitempty"`
}

func getTool() mcp.Tool {
	return mcp.NewTool("get",
		mcp.WithDescription("Get document content by file path, #docid, or path:line"),
		mcp.WithString("file", mcp.Required(), mcp.Description("File path, #docid, or path:line")),
		mcp.WithNumber("fromLine", mcp.Description("Start line (1-based)"), mcp.DefaultNumber(1)),
		mcp.WithNumber("maxLines", mcp.Description("Maximum lines to return"), mcp.DefaultNumber(defaultMaxLines)),
		mcp.WithBoolean("lineNumbers", mcp.Description("Include line numbers"), mcp.DefaultBool(false)),
	)
}

func getHandler(d *deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		file, err := req.RequireString("file")
		if err != nil {
			return mcp.NewToolResultError("file parameter is required"), nil
		}
		fromLine := req.GetInt("fromLine", 1)
		maxLines := req.GetInt("maxLines", defaultMaxLines)
		lineNumbers := req.GetBool("lineNumbers", false)

		doc, notFound, err := store.FindDocument(d.db, file, store.FindDocumentOpts{IncludeBody: false})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("error: %v", err)), nil
		}
		if notFound != nil {
			data, _ := json.Marshal(notFound)
			return mcp.NewToolResultText(string(data)), nil
		}

		body, err := store.GetDocumentBody(d.db, doc.Filepath, fromLine, maxLines)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("error reading body: %v", err)), nil
		}

		if lineNumbers {
			body = store.AddLineNumbers(body, fromLine)
		}

		result := map[string]any{
			"file":       doc.Filepath,
			"title":      doc.Title,
			"collection": doc.CollectionName,
			"docid":      doc.DocID,
			"body":       body,
		}
		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func multiGetTool() mcp.Tool {
	return mcp.NewTool("multi_get",
		mcp.WithDescription("Get multiple documents by glob pattern or comma-separated paths"),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Glob pattern or comma-separated file paths")),
		mcp.WithNumber("maxLines", mcp.Description("Max lines per document"), mcp.DefaultNumber(defaultMaxLines)),
		mcp.WithNumber("maxBytes", mcp.Description("Max total bytes"), mcp.DefaultNumber(defaultMaxBytes)),
		mcp.WithBoolean("lineNumbers", mcp.Description("Include line numbers"), mcp.DefaultBool(false)),
	)
}

func multiGetHandler(d *deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		pattern, err := req.RequireString("pattern")
		if err != nil {
			return mcp.NewToolResultError("pattern parameter is required"), nil
		}
		maxBytes := req.GetInt("maxBytes", defaultMaxBytes)

		results, errs, err := store.FindDocuments(d.db, pattern, maxBytes)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("error: %v", err)), nil
		}

		out := map[string]any{
			"results": results,
		}
		if len(errs) > 0 {
			out["errors"] = errs
		}
		data, _ := json.Marshal(out)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func statusTool() mcp.Tool {
	return mcp.NewTool("status",
		mcp.WithDescription("Get index status: collections, document counts, embedding coverage"),
	)
}

func statusHandler(d *deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		info, err := getStatusInfo(d.db, d.cfg)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("error: %v", err)), nil
		}
		data, _ := json.Marshal(info)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func getStatusInfo(db *sql.DB, cfg *config.Config) (*store.StatusInfo, error) {
	info := &store.StatusInfo{}

	cols, err := config.ListCollections(db)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	for i := range cols {
		c := &cols[i]
		info.Collections = append(info.Collections, store.CollectionInfo{
			Name:             c.Name,
			Path:             c.Path,
			Pattern:          c.Pattern,
			IgnorePatterns:   c.IgnorePatterns,
			IncludeByDefault: c.IncludeByDefault,
			UpdateCommand:    c.UpdateCommand,
			Context:          c.Context,
		})
	}

	row := db.QueryRow("SELECT COUNT(*) FROM documents")
	_ = row.Scan(&info.TotalDocuments)
	row = db.QueryRow("SELECT COUNT(*) FROM documents WHERE active = 1")
	_ = row.Scan(&info.ActiveDocuments)

	row = db.QueryRow("SELECT COUNT(*) FROM content_vectors WHERE embedded_at != 'embedding...'")
	_ = row.Scan(&info.EmbeddedChunks)

	if cfg.Providers != nil && cfg.Providers.Embed != nil {
		info.EmbedModel = cfg.Providers.Embed.Model
	}

	return info, nil
}

func buildInstructions(db *sql.DB, cfg *config.Config, indexName string) string {
	var b strings.Builder
	b.WriteString("QMD (Query Markup Documents) — semantic search over indexed documents.\n\n")
	fmt.Fprintf(&b, "Index: %s\n", indexName)

	cols, err := config.ListCollections(db)
	if err == nil && len(cols) > 0 {
		b.WriteString("\nCollections:\n")
		for _, c := range cols {
			fmt.Fprintf(&b, "- %s: %s (%s)", c.Name, c.Path, c.Pattern)
			if !c.IncludeByDefault {
				b.WriteString(" [excluded by default]")
			}
			b.WriteString("\n")
		}
	}

	if cfg != nil && len(cfg.Contexts) > 0 {
		b.WriteString("\nContext annotations:\n")
		for _, ctx := range cfg.Contexts {
			fmt.Fprintf(&b, "- %s: %s", ctx.Path, ctx.Context)
			if ctx.Global {
				b.WriteString(" [global]")
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("\nUse the 'query' tool for hybrid search (combines BM25 + vector similarity).\n")
	b.WriteString("Use the 'get' tool to retrieve full document content by path or #docid.\n")
	b.WriteString("Use 'multi_get' for batch retrieval by glob pattern.\n")
	b.WriteString("Use 'status' to check index health and available collections.\n")

	return b.String()
}
