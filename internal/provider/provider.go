// Package provider defines embedding, reranking, and generation interfaces
// and provides remote HTTP implementations with circuit breaker + retry.
package provider

//go:generate mockery --name=Embedder --output=mocks --outpkg=mocks --with-expecter
//go:generate mockery --name=Reranker --output=mocks --outpkg=mocks --with-expecter
//go:generate mockery --name=Generator --output=mocks --outpkg=mocks --with-expecter

import "context"

// EmbedOpts configures an embedding request.
type EmbedOpts struct {
	IsQuery bool   // true for search queries, false for documents
	Model   string // override default model
}

// Embedder produces vector embeddings from text.
type Embedder interface {
	Embed(ctx context.Context, texts []string, opts EmbedOpts) ([][]float32, error)
	Dimensions() int // 0 = not yet detected
	Close() error
}

// RerankDoc is a document to be reranked.
type RerankDoc struct {
	Text string
	File string // for result mapping
}

// RerankResult is a reranked document with its score.
type RerankResult struct {
	Index int
	Score float64
}

// RerankOpts configures a rerank request.
type RerankOpts struct {
	Model string
	TopN  int
}

// Reranker reranks documents by relevance to a query.
type Reranker interface {
	Rerank(ctx context.Context, query string, docs []RerankDoc, opts RerankOpts) ([]RerankResult, error)
	Close() error
}

// Message is a chat message for text generation.
type Message struct {
	Role    string // "system", "user", "assistant"
	Content string
}

// GenOpts configures a generation request.
type GenOpts struct {
	Model       string
	MaxTokens   int
	Temperature float64
}

// Generator produces text from chat messages.
type Generator interface {
	Generate(ctx context.Context, messages []Message, opts GenOpts) (string, error)
	Close() error
}
