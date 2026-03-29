package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/qmd-go/internal/config"
)

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func decodeJSON(r *http.Request, v any) {
	_ = json.NewDecoder(r.Body).Decode(v)
}

// --- OpenAI Embedder ---

func TestOpenAIEmbedder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/embeddings", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

		var req openAIEmbedReq
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "test-model", req.Model)

		resp := openAIEmbedResp{}
		for i := range req.Input {
			resp.Data = append(resp.Data, struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				Embedding: []float32{0.1, 0.2, 0.3},
				Index:     i,
			})
		}
		writeJSON(w, resp)
	}))
	defer server.Close()

	e := NewRemoteEmbedder("openai", server.URL, "test-key", "test-model", nil)
	vecs, err := e.Embed(context.Background(), []string{"hello", "world"}, EmbedOpts{})
	require.NoError(t, err)
	assert.Len(t, vecs, 2)
	assert.Equal(t, []float32{0.1, 0.2, 0.3}, vecs[0])
	assert.Equal(t, 3, e.Dimensions())
}

func TestOpenAIEmbedder_EmptyInput(t *testing.T) {
	e := NewRemoteEmbedder("openai", "http://unused", "", "m", nil)
	vecs, err := e.Embed(context.Background(), nil, EmbedOpts{})
	require.NoError(t, err)
	assert.Nil(t, vecs)
}

// --- Cohere Embedder ---

func TestCohereEmbedder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v2/embed", r.URL.Path)

		var req cohereEmbedReq
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "search_query", req.InputType)
		assert.Equal(t, []string{"float"}, req.EmbeddingTypes)

		resp := cohereEmbedResp{}
		for range req.Texts {
			resp.Embeddings.Float = append(resp.Embeddings.Float, []float32{0.4, 0.5})
		}
		writeJSON(w, resp)
	}))
	defer server.Close()

	e := NewRemoteEmbedder("cohere", server.URL, "key", "embed-v4", nil)
	vecs, err := e.Embed(context.Background(), []string{"test"}, EmbedOpts{IsQuery: true})
	require.NoError(t, err)
	assert.Len(t, vecs, 1)
	assert.Equal(t, []float32{0.4, 0.5}, vecs[0])
}

// --- Gemini Embedder ---

func TestGeminiEmbedder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/v1beta/models/gemini-embed")
		assert.Equal(t, "test-key", r.Header.Get("X-Goog-Api-Key"))

		var req geminiBatchReq
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "RETRIEVAL_DOCUMENT", req.Requests[0].TaskType)

		resp := geminiBatchResp{}
		for range req.Requests {
			resp.Embeddings = append(resp.Embeddings, struct {
				Values []float32 `json:"values"`
			}{Values: []float32{0.7, 0.8, 0.9}})
		}
		writeJSON(w, resp)
	}))
	defer server.Close()

	e := NewRemoteEmbedder("gemini", server.URL, "test-key", "gemini-embed", nil)
	vecs, err := e.Embed(context.Background(), []string{"doc text"}, EmbedOpts{IsQuery: false})
	require.NoError(t, err)
	assert.Len(t, vecs, 1)
	assert.Equal(t, []float32{0.7, 0.8, 0.9}, vecs[0])
}

// --- Dimension Auto-Detection and Lock ---

func TestDimensionLock(t *testing.T) {
	dim := 3
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := openAIEmbedResp{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{{Embedding: make([]float32, dim), Index: 0}},
		}
		writeJSON(w, resp)
	}))
	defer server.Close()

	e := NewRemoteEmbedder("openai", server.URL, "", "m", nil)
	assert.Equal(t, 0, e.Dimensions())

	_, err := e.Embed(context.Background(), []string{"a"}, EmbedOpts{})
	require.NoError(t, err)
	assert.Equal(t, 3, e.Dimensions())

	dim = 5
	_, err = e.Embed(context.Background(), []string{"b"}, EmbedOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dimension mismatch")
}

// --- Qwen3 Prefix Normalization ---

func TestQwen3Normalization(t *testing.T) {
	var captured openAIEmbedReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodeJSON(r, &captured)
		resp := openAIEmbedResp{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{{Embedding: []float32{1}, Index: 0}},
		}
		writeJSON(w, resp)
	}))
	defer server.Close()

	e := NewRemoteEmbedder("openai", server.URL, "", "qwen3-embed", nil)

	_, err := e.Embed(context.Background(), []string{"hello"}, EmbedOpts{IsQuery: true})
	require.NoError(t, err)
	assert.Equal(t, "query: hello", captured.Input[0])

	_, err = e.Embed(context.Background(), []string{"doc text"}, EmbedOpts{IsQuery: false})
	require.NoError(t, err)
	assert.Equal(t, "passage: doc text", captured.Input[0])

	_, err = e.Embed(context.Background(), []string{"query: already prefixed"}, EmbedOpts{IsQuery: true})
	require.NoError(t, err)
	assert.Equal(t, "query: already prefixed", captured.Input[0])
}

func TestNonQwen3SkipsNormalization(t *testing.T) {
	var captured openAIEmbedReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodeJSON(r, &captured)
		resp := openAIEmbedResp{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{{Embedding: []float32{1}, Index: 0}},
		}
		writeJSON(w, resp)
	}))
	defer server.Close()

	e := NewRemoteEmbedder("openai", server.URL, "", "text-embedding-3-small", nil)
	_, err := e.Embed(context.Background(), []string{"hello"}, EmbedOpts{IsQuery: true})
	require.NoError(t, err)
	assert.Equal(t, "hello", captured.Input[0])
}

// --- Remote Generator ---

func TestRemoteGenerator(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer gen-key", r.Header.Get("Authorization"))

		var req chatCompletionReq
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "gpt-4", req.Model)
		assert.Len(t, req.Messages, 1)

		writeJSON(w, chatCompletionResp{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{{Message: struct {
				Content string `json:"content"`
			}{Content: "generated text"}}},
		})
	}))
	defer server.Close()

	g := NewRemoteGenerator(server.URL, "gen-key", "gpt-4", nil)
	result, err := g.Generate(context.Background(), []Message{{Role: "user", Content: "hello"}}, GenOpts{})
	require.NoError(t, err)
	assert.Equal(t, "generated text", result)
}

// --- Rerankers ---

func TestCohereReranker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v2/rerank", r.URL.Path)

		var req standardRerankReq
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "test query", req.Query)

		writeJSON(w, standardRerankResp{
			Results: []struct {
				Index          int     `json:"index"`
				RelevanceScore float64 `json:"relevance_score"`
			}{{Index: 1, RelevanceScore: 0.95}, {Index: 0, RelevanceScore: 0.80}},
		})
	}))
	defer server.Close()

	r := NewRemoteReranker("cohere", server.URL, "key", "rerank-v3.5", nil)
	results, err := r.Rerank(context.Background(), "test query", []RerankDoc{
		{Text: "doc a"}, {Text: "doc b"},
	}, RerankOpts{TopN: 2})
	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Equal(t, 1, results[0].Index)
	assert.InDelta(t, 0.95, results[0].Score, 0.001)
}

func TestJinaReranker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/rerank", r.URL.Path)
		writeJSON(w, standardRerankResp{
			Results: []struct {
				Index          int     `json:"index"`
				RelevanceScore float64 `json:"relevance_score"`
			}{{Index: 0, RelevanceScore: 0.9}},
		})
	}))
	defer server.Close()

	r := NewRemoteReranker("jina", server.URL, "key", "jina-reranker", nil)
	results, err := r.Rerank(context.Background(), "q", []RerankDoc{{Text: "d"}}, RerankOpts{})
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestVoyageReranker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/rerank", r.URL.Path)

		var req voyageRerankReq
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, 5, req.TopK)

		writeJSON(w, voyageRerankResp{
			Data: []struct {
				Index          int     `json:"index"`
				RelevanceScore float64 `json:"relevance_score"`
			}{{Index: 0, RelevanceScore: 0.88}},
		})
	}))
	defer server.Close()

	r := NewRemoteReranker("voyage", server.URL, "key", "rerank-2", nil)
	results, err := r.Rerank(context.Background(), "q", []RerankDoc{{Text: "d"}}, RerankOpts{TopN: 5})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.InDelta(t, 0.88, results[0].Score, 0.001)
}

func TestTEIReranker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/rerank", r.URL.Path)

		var req teiRerankReq
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.True(t, req.Truncate)

		writeJSON(w, []teiRerankItem{
			{Index: 1, Score: 0.92},
			{Index: 0, Score: 0.75},
		})
	}))
	defer server.Close()

	r := NewRemoteReranker("tei", server.URL, "", "", nil)
	results, err := r.Rerank(context.Background(), "q", []RerankDoc{{Text: "a"}, {Text: "b"}}, RerankOpts{TopN: 1})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, 1, results[0].Index)
}

// --- Circuit Breaker ---

func TestCircuitBreakerOpensAfterFailures(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("error"))
	}))
	defer server.Close()

	client := NewResilientClient(&http.Client{Timeout: time.Second})
	assert.False(t, client.IsOpen())

	e := NewRemoteEmbedder("openai", server.URL, "", "m", client)

	_, err := e.Embed(context.Background(), []string{"a"}, EmbedOpts{})
	require.Error(t, err)

	_, err = e.Embed(context.Background(), []string{"b"}, EmbedOpts{})
	require.Error(t, err)

	assert.True(t, client.IsOpen())
}

func TestCircuitBreakerIgnoresClientErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer server.Close()

	client := NewResilientClient(&http.Client{Timeout: time.Second})
	e := NewRemoteEmbedder("openai", server.URL, "", "m", client)

	for range 5 {
		_, _ = e.Embed(context.Background(), []string{"x"}, EmbedOpts{})
	}
	assert.False(t, client.IsOpen())
}

func TestRetryDoesNotRetryClientErrors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("unauthorized"))
	}))
	defer server.Close()

	client := NewResilientClient(&http.Client{Timeout: time.Second})
	e := NewRemoteEmbedder("openai", server.URL, "", "m", client)

	_, err := e.Embed(context.Background(), []string{"x"}, EmbedOpts{})
	require.Error(t, err)
	assert.Equal(t, int32(1), calls.Load())
}

// --- Registry ---

func TestNewEmbedder_OpenAI(t *testing.T) {
	e, err := NewEmbedder(&config.ProviderConfig{
		Type:  "openai",
		URL:   "http://localhost:11434/v1",
		Model: "nomic-embed-text",
	})
	require.NoError(t, err)
	require.NotNil(t, e)

	re, ok := e.(*RemoteEmbedder)
	require.True(t, ok)
	assert.Equal(t, "openai", re.providerType)
	assert.Equal(t, "http://localhost:11434/v1", re.baseURL)
}

func TestNewEmbedder_EnvFallback(t *testing.T) {
	t.Setenv("QMD_REMOTE_EMBED_URL", "http://env-embed:8080")
	t.Setenv("QMD_REMOTE_API_KEY", "env-key")

	e, err := NewEmbedder(nil)
	require.NoError(t, err)
	require.NotNil(t, e)

	re := e.(*RemoteEmbedder)
	assert.Equal(t, "http://env-embed:8080", re.baseURL)
	assert.Equal(t, "env-key", re.apiKey)
}

func TestNewEmbedder_LocalViaEnv(t *testing.T) {
	t.Setenv("QMD_EMBED_PROVIDER", "local")
	e, err := NewEmbedder(nil)
	require.NoError(t, err)
	require.NotNil(t, e)
	_, ok := e.(*LocalEmbedder)
	require.True(t, ok)
}

func TestNewEmbedder_NilReturnsNil(t *testing.T) {
	e, err := NewEmbedder(nil)
	require.NoError(t, err)
	assert.Nil(t, e)
}

func TestNewReranker_Cohere(t *testing.T) {
	r, err := NewReranker(&config.ProviderConfig{
		Type:  "cohere",
		Model: "rerank-v3.5",
	})
	require.NoError(t, err)
	require.NotNil(t, r)

	rr := r.(*RemoteReranker)
	assert.Equal(t, "cohere", rr.providerType)
	assert.Equal(t, defaultCohereURL, rr.baseURL)
}

func TestNewGenerator_OpenAI(t *testing.T) {
	g, err := NewGenerator(&config.ProviderConfig{
		Type:  "openai",
		Model: "gpt-4",
	})
	require.NoError(t, err)
	require.NotNil(t, g)

	rg := g.(*RemoteGenerator)
	assert.Equal(t, "gpt-4", rg.model)
}
