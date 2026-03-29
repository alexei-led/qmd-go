package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gomcp "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/qmd-go/internal/config"
	"github.com/user/qmd-go/internal/db"
	"github.com/user/qmd-go/internal/store"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(dbPath)
	require.NoError(t, err)
	require.NoError(t, store.InitializeDatabase(d))
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func seedDoc(t *testing.T, d *sql.DB, collection, path, hash, title, body string) {
	t.Helper()
	_, err := d.Exec(`INSERT OR IGNORE INTO store_collections (name, path) VALUES (?, ?)`,
		collection, "/home/user/"+collection)
	require.NoError(t, err)
	_, err = d.Exec(`INSERT INTO content (hash, doc, created_at) VALUES (?, ?, '2024-01-01')`, hash, body)
	require.NoError(t, err)
	_, err = d.Exec(`INSERT INTO documents (collection, path, title, hash, active, created_at, modified_at)
		VALUES (?, ?, ?, ?, 1, '2024-01-01', '2024-01-01')`, collection, path, title, hash)
	require.NoError(t, err)
}

func testDeps(t *testing.T) *deps {
	t.Helper()
	d := setupTestDB(t)
	return &deps{db: d, cfg: &config.Config{}}
}

func callToolReq(args map[string]any) gomcp.CallToolRequest {
	return gomcp.CallToolRequest{
		Params: gomcp.CallToolParams{
			Arguments: args,
		},
	}
}

// --- Tool handler tests ---

func TestStatusHandler(t *testing.T) {
	d := testDeps(t)
	seedDoc(t, d.db, "notes", "test.md", "abc123def456", "Test Doc", "hello world")

	handler := statusHandler(d)
	result, err := handler(context.Background(), gomcp.CallToolRequest{})
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.NotEmpty(t, result.Content)
	text := result.Content[0].(gomcp.TextContent).Text
	var info store.StatusInfo
	require.NoError(t, json.Unmarshal([]byte(text), &info))

	assert.Equal(t, 1, info.TotalDocuments)
	assert.Equal(t, 1, info.ActiveDocuments)
	assert.Len(t, info.Collections, 1)
	assert.Equal(t, "notes", info.Collections[0].Name)
}

func TestGetHandler_Found(t *testing.T) {
	d := testDeps(t)
	seedDoc(t, d.db, "notes", "test.md", "abc123def456", "Test Doc", "line one\nline two\nline three")

	handler := getHandler(d)
	result, err := handler(context.Background(), callToolReq(map[string]any{
		"file": "qmd://notes/test.md",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.NotEmpty(t, result.Content)
	text := result.Content[0].(gomcp.TextContent).Text
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &doc))
	assert.Equal(t, "qmd://notes/test.md", doc["file"])
	assert.Equal(t, "Test Doc", doc["title"])
	assert.Contains(t, doc["body"], "line one")
}

func TestGetHandler_NotFound(t *testing.T) {
	d := testDeps(t)

	handler := getHandler(d)
	result, err := handler(context.Background(), callToolReq(map[string]any{
		"file": "nonexistent.md",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.NotEmpty(t, result.Content)
	text := result.Content[0].(gomcp.TextContent).Text
	var notFound store.DocumentNotFound
	require.NoError(t, json.Unmarshal([]byte(text), &notFound))
	assert.Equal(t, "not_found", notFound.Error)
}

func TestGetHandler_MissingFileParam(t *testing.T) {
	d := testDeps(t)

	handler := getHandler(d)
	result, err := handler(context.Background(), callToolReq(map[string]any{}))
	require.NoError(t, err)
	require.True(t, result.IsError)
}

func TestQueryHandler_EmptySearches(t *testing.T) {
	d := testDeps(t)

	handler := queryHandler(d)
	result, err := handler(context.Background(), callToolReq(map[string]any{
		"searches": []any{},
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.NotEmpty(t, result.Content)
	assert.Contains(t, result.Content[0].(gomcp.TextContent).Text, "must not be empty")
}

func TestMultiGetHandler_MissingPattern(t *testing.T) {
	d := testDeps(t)

	handler := multiGetHandler(d)
	result, err := handler(context.Background(), callToolReq(map[string]any{}))
	require.NoError(t, err)
	require.True(t, result.IsError)
}

func TestMultiGetHandler_NoMatches(t *testing.T) {
	d := testDeps(t)

	handler := multiGetHandler(d)
	result, err := handler(context.Background(), callToolReq(map[string]any{
		"pattern": "*.nonexistent",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
}

func TestQueryHandler_LexSearch(t *testing.T) {
	d := testDeps(t)
	seedDoc(t, d.db, "notes", "test.md", "abc123def456", "Test Doc", "hello world content here")

	handler := queryHandler(d)
	result, err := handler(context.Background(), callToolReq(map[string]any{
		"searches": []any{map[string]any{"type": "lex", "query": "hello"}},
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.NotEmpty(t, result.Content)
	text := result.Content[0].(gomcp.TextContent).Text
	var results []searchResultJSON
	require.NoError(t, json.Unmarshal([]byte(text), &results))
	require.NotEmpty(t, results)
	assert.Equal(t, "notes", results[0].Collection)
	assert.Greater(t, results[0].Score, 0.0)
}

func TestQueryHandler_WithLimit(t *testing.T) {
	d := testDeps(t)
	seedDoc(t, d.db, "notes", "a.md", "hash_a_123456", "Doc A", "hello world alpha")
	seedDoc(t, d.db, "notes", "b.md", "hash_b_123456", "Doc B", "hello world beta")

	handler := queryHandler(d)
	result, err := handler(context.Background(), callToolReq(map[string]any{
		"searches": []any{map[string]any{"type": "lex", "query": "hello"}},
		"limit":    1,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.NotEmpty(t, result.Content)
	text := result.Content[0].(gomcp.TextContent).Text
	var results []searchResultJSON
	require.NoError(t, json.Unmarshal([]byte(text), &results))
	assert.Len(t, results, 1)
}

func TestQueryHandler_CollectionFilter(t *testing.T) {
	d := testDeps(t)
	seedDoc(t, d.db, "notes", "test.md", "abc123def456", "Test Doc", "hello world content")

	handler := queryHandler(d)
	result, err := handler(context.Background(), callToolReq(map[string]any{
		"searches":    []any{map[string]any{"type": "lex", "query": "hello"}},
		"collections": []any{"nonexistent"},
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.NotEmpty(t, result.Content)
	text := result.Content[0].(gomcp.TextContent).Text
	var results []searchResultJSON
	require.NoError(t, json.Unmarshal([]byte(text), &results))
	assert.Empty(t, results)
}

func TestQueryHandler_InvalidType(t *testing.T) {
	d := testDeps(t)

	handler := queryHandler(d)
	result, err := handler(context.Background(), callToolReq(map[string]any{
		"searches": []any{map[string]any{"type": "invalid", "query": "hello"}},
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
}

func TestGetHandler_WithFromLine(t *testing.T) {
	d := testDeps(t)
	seedDoc(t, d.db, "notes", "test.md", "abc123def456", "Test Doc", "line one\nline two\nline three")

	handler := getHandler(d)
	result, err := handler(context.Background(), callToolReq(map[string]any{
		"file": "qmd://notes/test.md", "fromLine": 2,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.NotEmpty(t, result.Content)
	text := result.Content[0].(gomcp.TextContent).Text
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &doc))
	assert.NotContains(t, doc["body"], "line one")
	assert.Contains(t, doc["body"], "line two")
}

func TestGetHandler_WithMaxLines(t *testing.T) {
	d := testDeps(t)
	seedDoc(t, d.db, "notes", "test.md", "abc123def456", "Test Doc", "line one\nline two\nline three")

	handler := getHandler(d)
	result, err := handler(context.Background(), callToolReq(map[string]any{
		"file": "qmd://notes/test.md", "maxLines": 1,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.NotEmpty(t, result.Content)
	text := result.Content[0].(gomcp.TextContent).Text
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &doc))
	body := doc["body"].(string)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	assert.Len(t, lines, 1)
}

func TestMultiGetHandler_GlobMatch(t *testing.T) {
	d := testDeps(t)
	seedDoc(t, d.db, "notes", "a.md", "hash_a_123456", "Doc A", "content a")
	seedDoc(t, d.db, "notes", "b.md", "hash_b_123456", "Doc B", "content b")

	handler := multiGetHandler(d)
	result, err := handler(context.Background(), callToolReq(map[string]any{
		"pattern": "*.md",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.NotEmpty(t, result.Content)
	text := result.Content[0].(gomcp.TextContent).Text
	assert.Contains(t, text, "a.md")
	assert.Contains(t, text, "b.md")
}

func TestMultiGetHandler_MaxLines(t *testing.T) {
	d := testDeps(t)
	seedDoc(t, d.db, "notes", "test.md", "abc123def456", "Test Doc", "line one\nline two\nline three")

	handler := multiGetHandler(d)
	result, err := handler(context.Background(), callToolReq(map[string]any{
		"pattern": "*.md", "maxLines": 1,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.NotEmpty(t, result.Content)
	text := result.Content[0].(gomcp.TextContent).Text
	assert.NotContains(t, text, "line three")
}

// --- REST endpoint tests ---

func TestHealthEndpoint(t *testing.T) {
	d := testDeps(t)
	seedDoc(t, d.db, "notes", "test.md", "abc123def456", "Test Doc", "hello")

	handler := healthEndpoint(d)
	req := httptest.NewRequest(http.MethodGet, "/health", http.NoBody)
	rec := httptest.NewRecorder()
	handler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
	assert.NotEmpty(t, body["uptime"])
	assert.NotNil(t, body["index"])
}

func TestSearchEndpoint_InvalidJSON(t *testing.T) {
	d := testDeps(t)

	handler := searchEndpoint(d)
	req := httptest.NewRequest(http.MethodPost, "/search", strings.NewReader("{invalid"))
	rec := httptest.NewRecorder()
	handler(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "invalid request")
}

func TestQueryEndpoint_InvalidJSON(t *testing.T) {
	d := testDeps(t)

	handler := queryEndpoint(d)
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	handler(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSearchEndpoint_LexSearch(t *testing.T) {
	d := testDeps(t)
	seedDoc(t, d.db, "notes", "test.md", "abc123def456", "Test Doc", "hello world content here")

	handler := searchEndpoint(d)
	body := `{"searches":[{"type":"lex","query":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

// --- Resource handler tests ---

func TestResourceHandler_Found(t *testing.T) {
	d := testDeps(t)
	seedDoc(t, d.db, "notes", "test.md", "abc123def456", "Test Doc", "the body content")

	handler := resourceHandler(d)
	contents, err := handler(context.Background(), gomcp.ReadResourceRequest{
		Params: gomcp.ReadResourceParams{
			URI: "qmd://notes/test.md",
		},
	})
	require.NoError(t, err)
	require.Len(t, contents, 1)

	tc, ok := contents[0].(gomcp.TextResourceContents)
	require.True(t, ok)
	assert.Equal(t, "qmd://notes/test.md", tc.URI)
	assert.Equal(t, "text/plain", tc.MIMEType)
	assert.Equal(t, "the body content", tc.Text)
}

func TestResourceHandler_NotFound(t *testing.T) {
	d := testDeps(t)

	handler := resourceHandler(d)
	_, err := handler(context.Background(), gomcp.ReadResourceRequest{
		Params: gomcp.ReadResourceParams{
			URI: "qmd://nonexistent/file.md",
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// --- buildInstructions tests ---

func TestBuildInstructions(t *testing.T) {
	d := setupTestDB(t)
	_, err := d.Exec(`INSERT INTO store_collections (name, path, pattern) VALUES ('docs', '/tmp/docs', '**/*.md')`)
	require.NoError(t, err)

	cfg := &config.Config{
		Contexts: []config.ContextEntry{
			{Path: "/tmp/docs", Context: "Documentation files", Global: true},
		},
	}

	result := buildInstructions(d, cfg, "test-index")
	assert.Contains(t, result, "test-index")
	assert.Contains(t, result, "docs")
	assert.Contains(t, result, "Documentation files")
	assert.Contains(t, result, "[global]")
	assert.Contains(t, result, "query")
}

func TestBuildInstructions_Empty(t *testing.T) {
	d := setupTestDB(t)
	result := buildInstructions(d, &config.Config{}, "empty")
	assert.Contains(t, result, "empty")
	assert.Contains(t, result, "query")
}

// --- PID file / daemon tests ---

func TestDaemonPIDFile(t *testing.T) {
	path := daemonPIDFile("myindex")
	assert.Contains(t, path, "qmd-myindex.pid")
}

func TestWritePIDAndIsRunning(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "test.pid")
	require.NoError(t, writePID(pidFile))

	data, err := os.ReadFile(pidFile)
	require.NoError(t, err)
	assert.NotEmpty(t, data)
	assert.True(t, isRunning(pidFile))
}

func TestIsRunning_NoPIDFile(t *testing.T) {
	assert.False(t, isRunning(filepath.Join(t.TempDir(), "nonexistent.pid")))
}

func TestIsRunning_InvalidPID(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "bad.pid")
	require.NoError(t, os.WriteFile(pidFile, []byte("not-a-number"), 0o600))
	assert.False(t, isRunning(pidFile))
}

func TestIsRunning_DeadProcess(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "dead.pid")
	require.NoError(t, os.WriteFile(pidFile, []byte("999999999"), 0o600))
	assert.False(t, isRunning(pidFile))
}

func TestStopDaemon_NoPIDFile(t *testing.T) {
	err := stopDaemon(filepath.Join(t.TempDir(), "nonexistent.pid"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no daemon running")
}

func TestStopDaemon_InvalidPID(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "bad.pid")
	require.NoError(t, os.WriteFile(pidFile, []byte("garbage"), 0o600))
	err := stopDaemon(pidFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid pidfile")
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusCreated, map[string]string{"key": "value"})

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "value", body["key"])
}
