package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// RemoteReranker implements Reranker via HTTP APIs (Cohere, Jina, Voyage, TEI).
type RemoteReranker struct {
	client       *ResilientClient
	baseURL      string
	apiKey       string
	model        string
	providerType string // "cohere", "jina", "voyage", "tei"
}

// NewRemoteReranker creates a remote reranker for the given provider type.
func NewRemoteReranker(providerType, baseURL, apiKey, model string, client *ResilientClient) *RemoteReranker {
	if client == nil {
		client = NewResilientClient(nil)
	}
	return &RemoteReranker{
		client:       client,
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		model:        model,
		providerType: providerType,
	}
}

func (r *RemoteReranker) Rerank(ctx context.Context, query string, docs []RerankDoc, opts RerankOpts) ([]RerankResult, error) {
	if len(docs) == 0 {
		return nil, nil
	}

	model := r.model
	if opts.Model != "" {
		model = opts.Model
	}
	topN := opts.TopN
	if topN <= 0 {
		topN = len(docs)
	}

	texts := make([]string, len(docs))
	for i, d := range docs {
		texts[i] = d.Text
	}

	switch r.providerType {
	case "tei":
		return r.rerankTEI(ctx, query, texts, topN)
	case "voyage":
		return r.rerankVoyage(ctx, query, texts, model, topN)
	default:
		return r.rerankStandard(ctx, query, texts, model, topN)
	}
}

func (r *RemoteReranker) Close() error { return nil }

// --- Cohere /v2/rerank and Jina /v1/rerank (same request/response shape) ---

func (r *RemoteReranker) rerankStandard(ctx context.Context, query string, documents []string, model string, topN int) ([]RerankResult, error) {
	path := "/v1/rerank"
	if r.providerType == "cohere" {
		path = "/v2/rerank"
	}

	reqBody, err := json.Marshal(standardRerankReq{
		Model:     model,
		Query:     query,
		Documents: documents,
		TopN:      topN,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal rerank request: %w", err)
	}

	respBody, err := r.client.Do(ctx, func() (*http.Request, error) {
		req, reqErr := http.NewRequest(http.MethodPost, r.baseURL+path, bytes.NewReader(reqBody))
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Content-Type", "application/json")
		if r.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+r.apiKey)
		}
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s rerank: %w", r.providerType, err)
	}

	var result standardRerankResp
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse %s rerank response: %w", r.providerType, err)
	}

	results := make([]RerankResult, len(result.Results))
	for i, rr := range result.Results {
		results[i] = RerankResult{Index: rr.Index, Score: rr.RelevanceScore}
	}
	return results, nil
}

type standardRerankReq struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n"`
}

type standardRerankResp struct {
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
}

// --- Voyage /v1/rerank (uses top_k instead of top_n, data instead of results) ---

func (r *RemoteReranker) rerankVoyage(ctx context.Context, query string, documents []string, model string, topK int) ([]RerankResult, error) {
	reqBody, err := json.Marshal(voyageRerankReq{
		Model:     model,
		Query:     query,
		Documents: documents,
		TopK:      topK,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal voyage rerank request: %w", err)
	}

	respBody, err := r.client.Do(ctx, func() (*http.Request, error) {
		req, reqErr := http.NewRequest(http.MethodPost, r.baseURL+"/v1/rerank", bytes.NewReader(reqBody))
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Content-Type", "application/json")
		if r.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+r.apiKey)
		}
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("voyage rerank: %w", err)
	}

	var result voyageRerankResp
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse voyage rerank response: %w", err)
	}

	results := make([]RerankResult, len(result.Data))
	for i, rr := range result.Data {
		results[i] = RerankResult{Index: rr.Index, Score: rr.RelevanceScore}
	}
	return results, nil
}

type voyageRerankReq struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopK      int      `json:"top_k"`
}

type voyageRerankResp struct {
	Data []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"data"`
}

// --- TEI /rerank (no model field, uses texts, returns flat array with score) ---

func (r *RemoteReranker) rerankTEI(ctx context.Context, query string, texts []string, topN int) ([]RerankResult, error) {
	reqBody, err := json.Marshal(teiRerankReq{
		Query:    query,
		Texts:    texts,
		Truncate: true,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal tei rerank request: %w", err)
	}

	respBody, err := r.client.Do(ctx, func() (*http.Request, error) {
		req, reqErr := http.NewRequest(http.MethodPost, r.baseURL+"/rerank", bytes.NewReader(reqBody))
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Content-Type", "application/json")
		if r.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+r.apiKey)
		}
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("tei rerank: %w", err)
	}

	var raw []teiRerankItem
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, fmt.Errorf("parse tei rerank response: %w", err)
	}

	limit := topN
	if limit > len(raw) {
		limit = len(raw)
	}
	results := make([]RerankResult, limit)
	for i := range limit {
		results[i] = RerankResult{Index: raw[i].Index, Score: raw[i].Score}
	}
	return results, nil
}

type teiRerankReq struct {
	Query    string   `json:"query"`
	Texts    []string `json:"texts"`
	Truncate bool     `json:"truncate"`
}

type teiRerankItem struct {
	Index int     `json:"index"`
	Score float64 `json:"score"`
}
