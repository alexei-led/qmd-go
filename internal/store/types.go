// Package store provides the core QMD data store.
package store

// SearchResult represents a single search result from FTS or vector search.
type SearchResult struct {
	Collection string  `json:"collection"`
	Path       string  `json:"path"`
	Title      string  `json:"title"`
	Score      float64 `json:"score"`
	Snippet    string  `json:"snippet,omitempty"`
	Body       string  `json:"body,omitempty"`
	Hash       string  `json:"hash"`
	DocID      int64   `json:"docId"`
	LineStart  int     `json:"lineStart,omitempty"`
	LineEnd    int     `json:"lineEnd,omitempty"`
	Context    string  `json:"context,omitempty"`
}

// CollectionInfo holds metadata about a stored collection.
type CollectionInfo struct {
	Name             string `json:"name"`
	Path             string `json:"path"`
	Pattern          string `json:"pattern"`
	IgnorePatterns   string `json:"ignorePatterns,omitempty"`
	IncludeByDefault bool   `json:"includeByDefault"`
	UpdateCommand    string `json:"updateCommand,omitempty"`
	Context          string `json:"context,omitempty"`
}

// StatusInfo holds the result of the status command.
type StatusInfo struct {
	IndexName       string           `json:"indexName"`
	DBPath          string           `json:"dbPath"`
	Collections     []CollectionInfo `json:"collections"`
	TotalDocuments  int              `json:"totalDocuments"`
	ActiveDocuments int              `json:"activeDocuments"`
	EmbeddedChunks  int              `json:"embeddedChunks"`
	VecAvailable    bool             `json:"vecAvailable"`
	EmbedDimension  int              `json:"embedDimension,omitempty"`
	EmbedModel      string           `json:"embedModel,omitempty"`
}

// SearchQuery represents a single search query in a structured search request.
type SearchQuery struct {
	Type  string `json:"type"` // "lex", "vec", or "hyde"
	Query string `json:"query"`
}

// StructuredSearchRequest represents a structured search request from MCP/HTTP.
type StructuredSearchRequest struct {
	Searches       []SearchQuery `json:"searches"`
	Limit          int           `json:"limit,omitempty"`
	MinScore       float64       `json:"minScore,omitempty"`
	CandidateLimit int           `json:"candidateLimit,omitempty"`
	Collections    []string      `json:"collections,omitempty"`
	Intent         string        `json:"intent,omitempty"`
	Explain        bool          `json:"explain,omitempty"`
}
