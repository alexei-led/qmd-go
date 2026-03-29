package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// RemoteEmbedder implements Embedder via HTTP APIs (OpenAI, Cohere, Gemini).
type RemoteEmbedder struct {
	client       *ResilientClient
	baseURL      string
	apiKey       string
	model        string
	providerType string // "openai", "cohere", "gemini"
	dims         int
	dimsMu       sync.RWMutex
}

// NewRemoteEmbedder creates a remote embedder for the given provider type.
func NewRemoteEmbedder(providerType, baseURL, apiKey, model string, client *ResilientClient) *RemoteEmbedder {
	if client == nil {
		client = NewResilientClient(nil)
	}
	return &RemoteEmbedder{
		client:       client,
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		model:        model,
		providerType: providerType,
	}
}

func (e *RemoteEmbedder) Embed(ctx context.Context, texts []string, opts EmbedOpts) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	model := e.model
	if opts.Model != "" {
		model = opts.Model
	}

	normalized := normalizeTexts(texts, model, opts.IsQuery)

	var (
		embeddings [][]float32
		err        error
	)

	switch e.providerType {
	case "cohere":
		embeddings, err = e.embedCohere(ctx, normalized, model, opts.IsQuery)
	case "gemini":
		embeddings, err = e.embedGemini(ctx, normalized, model, opts.IsQuery)
	default:
		embeddings, err = e.embedOpenAI(ctx, normalized, model)
	}
	if err != nil {
		return nil, err
	}

	if len(embeddings) > 0 {
		dim := len(embeddings[0])
		e.dimsMu.RLock()
		current := e.dims
		e.dimsMu.RUnlock()
		if current == 0 {
			e.dimsMu.Lock()
			if e.dims == 0 {
				e.dims = dim
			}
			current = e.dims
			e.dimsMu.Unlock()
		}
		if current != dim {
			return nil, fmt.Errorf("dimension mismatch: expected %d, got %d", current, dim)
		}
	}

	return embeddings, nil
}

func (e *RemoteEmbedder) Dimensions() int {
	e.dimsMu.Lock()
	defer e.dimsMu.Unlock()
	return e.dims
}

func (e *RemoteEmbedder) Close() error { return nil }

// --- OpenAI-compatible /v1/embeddings ---

func (e *RemoteEmbedder) embedOpenAI(ctx context.Context, texts []string, model string) ([][]float32, error) {
	reqBody, err := json.Marshal(openAIEmbedReq{Model: model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	respBody, err := e.client.Do(ctx, func() (*http.Request, error) {
		req, reqErr := http.NewRequest(http.MethodPost, e.baseURL+"/v1/embeddings", bytes.NewReader(reqBody))
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Content-Type", "application/json")
		if e.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+e.apiKey)
		}
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("openai embed: %w", err)
	}

	var result openAIEmbedResp
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse openai embed response: %w", err)
	}

	embeddings := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index >= 0 && d.Index < len(embeddings) {
			embeddings[d.Index] = d.Embedding
		}
	}
	for i, emb := range embeddings {
		if emb == nil {
			return nil, fmt.Errorf("openai embed: missing embedding for input %d", i)
		}
	}
	return embeddings, nil
}

type openAIEmbedReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIEmbedResp struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

// --- Cohere /v2/embed ---

func (e *RemoteEmbedder) embedCohere(ctx context.Context, texts []string, model string, isQuery bool) ([][]float32, error) {
	inputType := "search_document"
	if isQuery {
		inputType = "search_query"
	}

	reqBody, err := json.Marshal(cohereEmbedReq{
		Model:          model,
		Texts:          texts,
		InputType:      inputType,
		EmbeddingTypes: []string{"float"},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal cohere embed request: %w", err)
	}

	respBody, err := e.client.Do(ctx, func() (*http.Request, error) {
		req, reqErr := http.NewRequest(http.MethodPost, e.baseURL+"/v2/embed", bytes.NewReader(reqBody))
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Content-Type", "application/json")
		if e.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+e.apiKey)
		}
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("cohere embed: %w", err)
	}

	var result cohereEmbedResp
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse cohere embed response: %w", err)
	}
	return result.Embeddings.Float, nil
}

type cohereEmbedReq struct {
	Model          string   `json:"model"`
	Texts          []string `json:"texts"`
	InputType      string   `json:"input_type"`
	EmbeddingTypes []string `json:"embedding_types"`
}

type cohereEmbedResp struct {
	Embeddings struct {
		Float [][]float32 `json:"float"`
	} `json:"embeddings"`
}

// --- Gemini /v1beta/.../batchEmbedContents ---

func (e *RemoteEmbedder) embedGemini(ctx context.Context, texts []string, model string, isQuery bool) ([][]float32, error) {
	taskType := "RETRIEVAL_DOCUMENT"
	if isQuery {
		taskType = "RETRIEVAL_QUERY"
	}

	requests := make([]geminiEmbedReq, len(texts))
	for i, text := range texts {
		requests[i] = geminiEmbedReq{
			Model:    "models/" + model,
			Content:  geminiContent{Parts: []geminiPart{{Text: text}}},
			TaskType: taskType,
		}
	}

	reqBody, err := json.Marshal(geminiBatchReq{Requests: requests})
	if err != nil {
		return nil, fmt.Errorf("marshal gemini embed request: %w", err)
	}

	url := e.baseURL + "/v1beta/models/" + model + ":batchEmbedContents"

	respBody, err := e.client.Do(ctx, func() (*http.Request, error) {
		req, reqErr := http.NewRequest(http.MethodPost, url, bytes.NewReader(reqBody))
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Content-Type", "application/json")
		if e.apiKey != "" {
			req.Header.Set("X-Goog-Api-Key", e.apiKey)
		}
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("gemini embed: %w", err)
	}

	var result geminiBatchResp
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse gemini embed response: %w", err)
	}

	embeddings := make([][]float32, len(result.Embeddings))
	for i, emb := range result.Embeddings {
		embeddings[i] = emb.Values
	}
	return embeddings, nil
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiEmbedReq struct {
	Model    string        `json:"model"`
	Content  geminiContent `json:"content"`
	TaskType string        `json:"taskType"`
}

type geminiBatchReq struct {
	Requests []geminiEmbedReq `json:"requests"`
}

type geminiBatchResp struct {
	Embeddings []struct {
		Values []float32 `json:"values"`
	} `json:"embeddings"`
}

// --- RemoteGenerator: OpenAI-compatible /v1/chat/completions ---

// RemoteGenerator implements Generator via OpenAI-compatible chat completions.
type RemoteGenerator struct {
	client  *ResilientClient
	baseURL string
	apiKey  string
	model   string
}

// NewRemoteGenerator creates a remote generator.
func NewRemoteGenerator(baseURL, apiKey, model string, client *ResilientClient) *RemoteGenerator {
	if client == nil {
		client = NewResilientClient(nil)
	}
	return &RemoteGenerator{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
	}
}

func (g *RemoteGenerator) Generate(ctx context.Context, messages []Message, opts GenOpts) (string, error) {
	model := g.model
	if opts.Model != "" {
		model = opts.Model
	}

	chatMsgs := make([]chatMsg, len(messages))
	for i, m := range messages {
		chatMsgs[i] = chatMsg(m)
	}

	body := chatCompletionReq{
		Model:    model,
		Messages: chatMsgs,
	}
	if opts.MaxTokens > 0 {
		body.MaxTokens = &opts.MaxTokens
	}
	if opts.Temperature > 0 {
		body.Temperature = &opts.Temperature
	}

	reqBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal chat request: %w", err)
	}

	respBody, err := g.client.Do(ctx, func() (*http.Request, error) {
		req, reqErr := http.NewRequest(http.MethodPost, g.baseURL+"/v1/chat/completions", bytes.NewReader(reqBody))
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Content-Type", "application/json")
		if g.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+g.apiKey)
		}
		return req, nil
	})
	if err != nil {
		return "", fmt.Errorf("chat completion: %w", err)
	}

	var result chatCompletionResp
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse chat response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("chat completion returned no choices")
	}
	return result.Choices[0].Message.Content, nil
}

func (g *RemoteGenerator) Close() error { return nil }

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionReq struct {
	Model       string    `json:"model"`
	Messages    []chatMsg `json:"messages"`
	MaxTokens   *int      `json:"max_tokens,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
}

type chatCompletionResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// --- Qwen3 prefix normalization ---

func normalizeTexts(texts []string, model string, isQuery bool) []string {
	if !isQwen3Model(model) {
		return texts
	}
	prefix := "passage: "
	if isQuery {
		prefix = "query: "
	}
	normalized := make([]string, len(texts))
	for i, t := range texts {
		if strings.HasPrefix(t, "query: ") || strings.HasPrefix(t, "passage: ") {
			normalized[i] = t
		} else {
			normalized[i] = prefix + t
		}
	}
	return normalized
}

func isQwen3Model(model string) bool {
	return strings.Contains(strings.ToLower(model), "qwen3")
}
