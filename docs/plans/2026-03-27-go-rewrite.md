# QMD Go Rewrite — Implementation Plan

## Overview

Rewrite QMD (Query Markup Documents) from TypeScript (~12,600 lines) to Go (~7,000 lines).
Full drop-in replacement for the TS QMD binary + OpenClaw plugin providing `memory_search`.

**Key constraints:**

- Same CLI commands, flags, and output formats (drop-in replacement)
- Same SQLite database schema (Go and TS binaries interchangeable)
- Same YAML config files (`~/.config/qmd/{index}.yml`)
- No CGO at compile time (kelindar/search uses purego for local embedding)
- Multi-agent safe: concurrent access from multiple AI agents via MCP
- OpenClaw integration: sidecar HTTP API for `memory_search` + thin TS plugin wrapper

**Why Go:**

1. Single binary — minimal runtime dependencies (shared lib for local embedding only)
2. Two-tier embedding — local GGUF via kelindar/search OR remote via OpenAI-compatible HTTP
3. Multi-agent concurrency — multiple Claude/AI agents searching simultaneously
4. OpenClaw plugin — provides `memory_search` with hybrid BM25+vector, MMR, temporal decay

---

## Source Architecture (TypeScript)

| Module                 | Lines | Purpose                                                    |
| ---------------------- | ----- | ---------------------------------------------------------- |
| `src/store.ts`         | 4,389 | Core: schema, search, indexing, chunking, RRF, reranking   |
| `src/cli/qmd.ts`       | 3,208 | CLI commands + arg parsing                                 |
| `src/llm.ts`           | 1,580 | Local LLM via node-llama-cpp (replaced by kelindar/search) |
| `src/mcp/server.ts`    | 807   | MCP protocol (stdio + HTTP) + REST endpoints               |
| `src/remote-llm.ts`    | 580   | Remote LLM via OpenAI-compatible REST                      |
| `src/collections.ts`   | 500   | YAML config management                                     |
| `src/cli/formatter.ts` | 430   | Output formatting (JSON, CSV, XML, MD, files)              |
| `src/hybrid-llm.ts`    | 80    | Local/remote routing (replaced by provider registry)       |
| `src/db.ts`            | 96    | SQLite abstraction                                         |

**Out of scope:** `src/index.ts` (SDK layer), `src/embedded-skills.ts` (skill files), skill CLI commands.

---

## Go Library Choices

| Go Package                                                                                  | Purpose                                        | Replaces TS                 |
| ------------------------------------------------------------------------------------------- | ---------------------------------------------- | --------------------------- |
| `ncruces/go-sqlite3`                                                                        | SQLite (WASM, no CGO, FTS5 bundled)            | better-sqlite3 / bun:sqlite |
| `ncruces/go-sqlite3/driver`                                                                 | database/sql driver for ncruces                | -                           |
| `asg017/sqlite-vec-go-bindings/ncruces`                                                     | Vector search (auto-register via blank import) | sqlite-vec npm              |
| `urfave/cli/v2`                                                                             | CLI framework                                  | manual parseArgs            |
| `gopkg.in/yaml.v3`                                                                          | YAML config                                    | yaml npm                    |
| `failsafe-go/failsafe-go`                                                                   | Circuit breaker + retry + timeout              | custom in remote-llm.ts     |
| `mark3labs/mcp-go`                                                                          | MCP protocol (spec 2025-11-25)                 | @modelcontextprotocol/sdk   |
| `bmatcuk/doublestar/v4`                                                                     | Glob matching                                  | fast-glob + picomatch       |
| `kelindar/search`                                                                           | Local GGUF embedding (purego+llama.cpp)        | node-llama-cpp              |
| `stretchr/testify`                                                                          | Testing (assert/require)                       | -                           |
| `jedib0t/go-pretty/v6`                                                                      | Table formatting for CLI output                | -                           |
| stdlib: `net/http`, `encoding/json`, `crypto/sha256`, `log/slog`, `sync`, `encoding/binary` | HTTP, JSON, hashing, logging, concurrency      | Node built-ins              |

### kelindar/search API Reference

```go
import "github.com/kelindar/search"

// Load GGUF model (gpuLayers=0 for CPU-only)
v, err := search.NewVectorizer("~/.cache/qmd/models/MiniLM-L6-v2.Q8_0.gguf", 0)
defer v.Close()

// Embed text → []float32
vec, err := v.EmbedText("some document text")

// Optional: reusable context for lower alloc overhead
ctx := v.Context(512)
defer ctx.Close()
vec, err = ctx.EmbedText("some text")

// Cosine similarity
score := search.Cosine(vecA, vecB)
```

Runtime requires `libllama_go.dylib` (macOS) or `libllama_go.so` (Linux) on PATH/DYLD_LIBRARY_PATH/LD_LIBRARY_PATH. No CGO at compile time.

Default model: `MiniLM-L6-v2.Q8_0.gguf` (384d, compatible with existing TS indexes using embeddinggemma 384d).

### Remote Embedding API (OpenAI-compatible)

Single HTTP client for all remote providers via `POST /v1/embeddings`:

```
POST /v1/embeddings
Auth: Bearer $API_KEY (or custom header per provider)
Body: {"model": "text-embedding-3-small", "input": ["text1", "text2"]}
Response: {"data": [{"embedding": [...], "index": 0}], "model": "..."}
```

Covers: OpenAI, Ollama (v0.5+ exposes /v1/embeddings), Voyage, Together, Fireworks, Groq, Mistral, Infinity, vLLM, llama-server, LiteLLM.

Provider-specific nuances handled via config:

- Auth header style (Bearer token, x-api-key, none)
- Model name mapping
- Response field paths (most follow OpenAI format)
- Request field extras (Cohere needs `input_type`, `embedding_types`)

For Cohere embed (`/v2/embed` with `texts` not `input`) and Gemini (`/v1beta/.../embedContent`): dedicated request/response adapters within the single remote client, selected by `provider_type` config.

---

## Project Structure

```
cmd/qmd/main.go                     # CLI entrypoint (urfave/cli v2)
internal/
  store/
    store.go                         # Store struct + factory
    schema.go                        # initializeDatabase() + all SQL
    search.go                        # FTS search (searchFTS, buildFTS5Query)
    vsearch.go                       # Vector search (searchVec)
    hybrid.go                        # hybridQuery pipeline
    structured.go                    # structuredSearch (MCP/HTTP callers)
    index.go                         # Document indexing + reindex
    chunk.go                         # Smart markdown chunking
    rrf.go                           # Reciprocal Rank Fusion
    snippet.go                       # Snippet extraction
    docid.go                         # Docid utilities
    fuzzy.go                         # Fuzzy matching + Levenshtein
    virtualpath.go                   # Virtual path utilities
    types.go                         # All shared types (SearchResult, etc.)
  db/
    db.go                            # database/sql wrapper + PRAGMA setup
  config/
    config.go                        # YAML config types + load/save
    collections.go                   # Collection CRUD operations
    context.go                       # Context management (add/rm/list/check)
  provider/
    provider.go                      # Embedder, Reranker, Generator interfaces
    registry.go                      # Provider factory from config/env
    local.go                         # Local embedding via kelindar/search (GGUF)
    remote.go                        # Remote OpenAI-compatible embed/gen via net/http
    reranker.go                      # Remote reranker (Cohere, Jina, Voyage, TEI)
    breaker.go                       # failsafe-go circuit breaker + retry wrapper
  format/
    format.go                        # Output formatters (JSON, CSV, XML, MD, files)
    color.go                         # ANSI color + NO_COLOR support
  mcp/
    server.go                        # MCP server (stdio + HTTP)
    session.go                       # Agent session management
    tools.go                         # Tool definitions + handlers
    rest.go                          # REST endpoints (POST /search, /query, GET /health)
  openclaw/
    sidecar.go                       # OpenClaw sidecar HTTP API
    memory.go                        # memory_search/memory_get with MMR + temporal decay
openclaw-plugin/                     # Thin TS wrapper (npm: @qmd/openclaw-plugin)
  package.json
  openclaw.plugin.json
  index.ts                           # registerTool proxying to qmd-go HTTP
```

---

## Database Schema (exact match with TS)

```sql
-- PRAGMAs (Go adds busy_timeout and wal_autocheckpoint that TS is missing)
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
PRAGMA wal_autocheckpoint = 1000;

-- Drop legacy tables
DROP TABLE IF EXISTS path_contexts;
DROP TABLE IF EXISTS collections;

-- Content-addressable storage
CREATE TABLE IF NOT EXISTS content (
  hash TEXT PRIMARY KEY,
  doc TEXT NOT NULL,
  created_at TEXT NOT NULL
);

-- Documents mapping
CREATE TABLE IF NOT EXISTS documents (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  collection TEXT NOT NULL,
  path TEXT NOT NULL,
  title TEXT NOT NULL,
  hash TEXT NOT NULL,
  created_at TEXT NOT NULL,
  modified_at TEXT NOT NULL,
  active INTEGER NOT NULL DEFAULT 1,
  FOREIGN KEY (hash) REFERENCES content(hash) ON DELETE CASCADE,
  UNIQUE(collection, path)
);
CREATE INDEX IF NOT EXISTS idx_documents_collection ON documents(collection, active);
CREATE INDEX IF NOT EXISTS idx_documents_hash ON documents(hash);
CREATE INDEX IF NOT EXISTS idx_documents_path ON documents(path, active);

-- LLM cache
CREATE TABLE IF NOT EXISTS llm_cache (
  hash TEXT PRIMARY KEY,
  result TEXT NOT NULL,
  created_at TEXT NOT NULL
);

-- Embeddings
CREATE TABLE IF NOT EXISTS content_vectors (
  hash TEXT NOT NULL,
  seq INTEGER NOT NULL DEFAULT 0,
  pos INTEGER NOT NULL DEFAULT 0,
  model TEXT NOT NULL,
  embedded_at TEXT NOT NULL,
  PRIMARY KEY (hash, seq)
);

-- Store collections (self-contained)
CREATE TABLE IF NOT EXISTS store_collections (
  name TEXT PRIMARY KEY,
  path TEXT NOT NULL,
  pattern TEXT NOT NULL DEFAULT '**/*.md',
  ignore_patterns TEXT,
  include_by_default INTEGER DEFAULT 1,
  update_command TEXT,
  context TEXT
);

-- Store config (key-value)
CREATE TABLE IF NOT EXISTS store_config (
  key TEXT PRIMARY KEY,
  value TEXT
);

-- FTS5
CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
  filepath, title, body,
  tokenize='porter unicode61'
);

-- Triggers
CREATE TRIGGER IF NOT EXISTS documents_ai AFTER INSERT ON documents
WHEN new.active = 1
BEGIN
  INSERT INTO documents_fts(rowid, filepath, title, body)
  SELECT new.id, new.collection || '/' || new.path, new.title,
    (SELECT doc FROM content WHERE hash = new.hash)
  WHERE new.active = 1;
END;

CREATE TRIGGER IF NOT EXISTS documents_ad AFTER DELETE ON documents BEGIN
  DELETE FROM documents_fts WHERE rowid = old.id;
END;

CREATE TRIGGER IF NOT EXISTS documents_au AFTER UPDATE ON documents
BEGIN
  DELETE FROM documents_fts WHERE rowid = old.id AND new.active = 0;
  INSERT OR REPLACE INTO documents_fts(rowid, filepath, title, body)
  SELECT new.id, new.collection || '/' || new.path, new.title,
    (SELECT doc FROM content WHERE hash = new.hash)
  WHERE new.active = 1;
END;
```

### sqlite-vec Virtual Table

```sql
-- Created dynamically after dimension auto-detection
CREATE VIRTUAL TABLE IF NOT EXISTS vectors_vec USING vec0(
  hash TEXT,
  seq INTEGER,
  embedding float[384]  -- dimension from first embedding call
);
```

Graceful degradation: if sqlite-vec is not available, vector search is disabled but FTS still works.

### Schema Migration

Check for `seq` column in `content_vectors` — if missing, drop and recreate table (TS compatibility: `store.ts:700-705`).

---

## Constants (must match TS exactly)

```go
const (
    ChunkSizeTokens     = 900
    ChunkOverlapTokens   = 135   // 15% of chunk size
    ChunkSizeChars       = 3600  // ~4 chars/token
    ChunkOverlapChars    = 540
    ChunkWindowTokens    = 200
    ChunkWindowChars     = 800

    DefaultEmbedModel   = "embeddinggemma"
    DefaultRerankModel  = "ExpedientFalcon/qwen3-reranker:0.6b-q8_0"
    DefaultQueryModel   = "Qwen/Qwen3-1.7B"
    DefaultGlob         = "**/*.md"

    DefaultMultiGetMaxBytes       = 10 * 1024       // 10KB
    DefaultEmbedMaxDocsPerBatch   = 64
    DefaultEmbedMaxBatchBytes     = 64 * 1024 * 1024 // 64MB

    StrongSignalMinScore = 0.85
    RRFk                 = 60
    IntentWeightSnippet  = 0.3
    IntentWeightChunk    = 0.5
)
```

---

## CLI Commands (full parity)

All commands use `urfave/cli/v2`. Global flag `--index <name>` determines DB + config paths.

```
qmd search <query>         [-n NUM] [--min-score NUM] [--all] [-C NUM] [--full]
                           [--line-numbers] [--files|--json|--csv|--md|--xml]
                           [-c NAME]

qmd vsearch <query>        (same flags as search)

qmd query <query>          (same flags as search) + [--no-rerank] [--explain]
                           [--intent TEXT] [--candidate-limit NUM]

qmd get <file>[:line]      [--from LINE] [-l LINES] [--line-numbers]

qmd multi-get <pattern>    [-l LINES] [--max-bytes BYTES]
                           [--json|--csv|--md|--xml|--files]

qmd ls [path]

qmd update [--pull]        [--max-docs-per-batch N] [--max-batch-mb N]

qmd embed [--clear]        [--no-incremental] [--model MODEL]

qmd pull [--refresh]

qmd status

qmd cleanup                [--skip-inactive] [--skip-orphaned-content]
                           [--skip-orphaned-vectors] [--skip-llm-cache]

qmd collection add <path>  [--name NAME]
qmd collection list
qmd collection remove <name>
qmd collection rename <old> <new>
qmd collection set-update <name> 'COMMAND'
qmd collection include <name>
qmd collection exclude <name>
qmd collection show <name>

qmd context add <path>     [--context TEXT] [--global]
qmd context list
qmd context remove <path>
qmd context check

qmd mcp                    [--stdio] [--http [--port PORT]] [--daemon] [--openclaw]

qmd --version
qmd --help
```

---

## Provider Interfaces

```go
type EmbedOpts struct {
    IsQuery bool   // true for search queries, false for documents
    Model   string // override default model
}

type Embedder interface {
    Embed(ctx context.Context, texts []string, opts EmbedOpts) ([][]float32, error)
    Dimensions() int // 0 = auto-detect
    Close() error
}

type RerankDoc struct {
    Text string
    File string // for result mapping
}

type RerankResult struct {
    Index int
    Score float64
}

type RerankOpts struct {
    Model string
    TopN  int
}

type Reranker interface {
    Rerank(ctx context.Context, query string, docs []RerankDoc, opts RerankOpts) ([]RerankResult, error)
    Close() error
}

type Message struct {
    Role    string // "system", "user", "assistant"
    Content string
}

type GenOpts struct {
    Model       string
    MaxTokens   int
    Temperature float64
}

type Generator interface {
    Generate(ctx context.Context, messages []Message, opts GenOpts) (string, error)
    Close() error
}
```

### Provider Configuration

```yaml
# ~/.config/qmd/index.yml
providers:
  embed:
    type: local           # kelindar/search GGUF
    model: MiniLM-L6-v2.Q8_0.gguf
  # OR
  embed:
    type: openai          # OpenAI-compatible /v1/embeddings
    url: http://localhost:11434/v1  # Ollama with OpenAI compat
    model: nomic-embed-text
  # OR
  embed:
    type: openai
    api_key_env: OPENAI_API_KEY
    model: text-embedding-3-small
  rerank:
    type: cohere
    api_key_env: COHERE_API_KEY
    model: rerank-v3.5
  generate:
    type: openai
    url: http://localhost:11434/v1
    model: qwen3:1.7b
```

Env var fallbacks (backwards-compatible with TS):

- `QMD_REMOTE_EMBED_URL`, `QMD_REMOTE_RERANK_URL`, `QMD_REMOTE_API_KEY`
- `QMD_EMBED_PROVIDER=local` (use kelindar/search)

---

## Core Algorithms

### Smart Chunking (store.ts:100-226 → internal/store/chunk.go)

Break point patterns with scores:

````go
var breakPatterns = []struct {
    Pattern *regexp.Regexp
    Score   int
    Name    string
}{
    {regexp.MustCompile(`\n#{1}(?!#)`), 100, "h1"},
    {regexp.MustCompile(`\n#{2}(?!#)`), 90, "h2"},
    {regexp.MustCompile(`\n#{3}(?!#)`), 80, "h3"},
    {regexp.MustCompile(`\n#{4}(?!#)`), 70, "h4"},
    {regexp.MustCompile(`\n#{5}(?!#)`), 60, "h5"},
    {regexp.MustCompile(`\n#{6}(?!#)`), 50, "h6"},
    {regexp.MustCompile("\\n```"), 80, "codeblock"},
    {regexp.MustCompile(`\n(?:---|\*\*\*|___)\s*\n`), 60, "hr"},
    {regexp.MustCompile(`\n\n+`), 20, "blank"},
    {regexp.MustCompile(`\n[-*]\s`), 5, "list"},
    {regexp.MustCompile(`\n\d+\.\s`), 5, "numlist"},
    {regexp.MustCompile(`\n`), 1, "newline"},
}
````

Key rules:

- **Never split inside code fences** — detect ``` regions, mark as off-limits
- **Unclosed code fences** extend to document end
- **Best cutoff** uses squared distance decay: `1.0 - (normalizedDist² × decayFactor)`
- **15% overlap** between chunks for context continuity

### FTS5 Query Building (store.ts:2676-2742 → internal/store/search.go)

```
Input: 'machine "deep learning" -neural'
Output: '"machine"* AND "deep learning" NOT "neural"*'
```

Rules:

- Plain terms → `"term"*` (prefix match, quoted for safety)
- Quoted phrases → `"exact phrase"` (no prefix match)
- `-term` → NOT clause
- All positive terms joined with AND
- All-negation query → return nil (can't search with only negation)
- Unicode sanitization: strip non-alphanumeric except internal hyphens

### Hybrid Query Pipeline (store.ts:3718-4112 → internal/store/hybrid.go)

**Step 1: BM25 Probe** — FTS with 20-result limit. Check strong signal: `topScore >= 0.85 AND (top - second) >= gap`. When intent provided, DISABLE strong signal bypass.

**Step 2: Query Expansion** — Call expandQuery() for lex/vec/hyde variants. Skip if strong signal (no intent).

**Step 3: Route by Type** — Run FTS for all lex expansions. Batch embed ALL vec/hyde queries in single Embedder.Embed() call. Run sqlite-vec lookups with pre-computed embeddings.

**Step 4: RRF Fusion** — First 2 lists get 2x weight. RRF formula: `score = Σ (weight / (k + rank))` with k=60. Top-rank bonus: +0.05 for rank 0, +0.02 for rank 1-2. Slice to candidateLimit.

**Step 5: Chunk Selection** — Pick best chunk per document. Score by keyword overlap: query terms at 1.0 + intent terms at 0.5.

**Step 6: Rerank Chunks** — Rerank the CHUNKS not full documents. Call store.rerank() on best chunk per candidate.

**Step 7: Blend Scores** — Position-aware weighting:

- RRF rank 1-3: `rrfWeight = 0.75`
- RRF rank 4-10: `rrfWeight = 0.60`
- RRF rank 11+: `rrfWeight = 0.40`
- `blendedScore = rrfWeight * rrfScore + (1 - rrfWeight) * rerankScore`

**Step 8: Dedup + Filter** — Remove duplicates, filter by minScore, slice to limit.

### Structured Search (store.ts:4114-4389 → internal/store/structured.go)

Differs from hybridQuery:

- Takes pre-expanded queries (lex/vec/hyde) as input — no query expansion
- Validates queries (newlines, lex/semantic validation)
- Collection filtering across multiple collections
- Only first list gets 2x weight (caller ordered by importance)
- Primary query: first lex query for keyword matching, fallback to first vec
- Used by MCP/HTTP callers that generate their own expansions

### Snippet Extraction (store.ts:3514-3630 → internal/store/snippet.go)

Intent stop words (2-char through 6-char common words). Score each line by query term matches (1.0) + intent term matches (0.3). Extract: 1 line before best, best line, 2 lines after. Format with diff-style header: `@@ -LINE,COUNT @@ (N before, M after)`.

### Handelize (store.ts:1577-1636 → internal/store/index.go)

- Triple underscore `___` → folder separator `/`
- Emoji → hex codepoints (e.g., 🎉 → `1f389`)
- Non-letter/number → dash separator
- Preserve file extension
- Validate: must have at least one letter, number, or emoji

---

## Concurrency Design

### SQLite Connection Strategy

```go
// Use database/sql with ncruces driver
import (
    "database/sql"
    _ "github.com/ncruces/go-sqlite3/driver"
    _ "github.com/ncruces/go-sqlite3/embed"
    _ "github.com/asg017/sqlite-vec-go-bindings/ncruces"
)

db, err := sql.Open("sqlite3", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)&_pragma=wal_autocheckpoint(1000)")
db.SetMaxOpenConns(runtime.NumCPU())
```

Using database/sql driver gives automatic connection pooling. WAL mode enables concurrent reads. busy_timeout=5000 prevents SQLITE_BUSY errors under concurrent writes.

### Embedding Coordination

Multiple agents calling `qmd embed` simultaneously:

```sql
-- Claim batch (atomic, prevents double-embedding)
UPDATE content_vectors
SET embedded_at = 'embedding...'
WHERE hash IN (
    SELECT hash FROM content
    WHERE hash NOT IN (SELECT hash FROM content_vectors)
    LIMIT 64
)
RETURNING hash;
```

Each `qmd embed` claims a batch, preventing double-embedding. Sentinel cleaned up on next run if process crashes.

### Float32 to sqlite-vec

```go
func float32ToBytes(v []float32) []byte {
    buf := make([]byte, len(v)*4)
    for i, f := range v {
        binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
    }
    return buf
}
```

---

## MCP Tools (exact JSON schemas from TS)

### query

```json
{
  "name": "query",
  "inputSchema": {
    "searches": [{ "type": "lex|vec|hyde", "query": "text" }],
    "limit": 10,
    "minScore": 0,
    "candidateLimit": 40,
    "collections": ["name"],
    "intent": "context text"
  }
}
```

### get

```json
{
  "name": "get",
  "inputSchema": {
    "file": "path or #docid or path:line",
    "fromLine": 1,
    "maxLines": 100,
    "lineNumbers": false
  }
}
```

### multi_get

```json
{
  "name": "multi_get",
  "inputSchema": {
    "pattern": "glob or comma-separated",
    "maxLines": 100,
    "maxBytes": 10240,
    "lineNumbers": false
  }
}
```

### status

```json
{ "name": "status", "inputSchema": {} }
```

### REST Endpoints (for OpenClaw sidecar)

- `POST /search` — structuredSearch without MCP protocol
- `POST /query` — query with request body
- `GET /health` — health check + uptime

---

## OpenClaw Integration

OpenClaw already uses QMD as an optional sidecar backend for `memory_search`. qmd-go replaces the TS binary.

### Sidecar Mode

`qmd mcp --http --port 18790 --openclaw` starts the HTTP server with OpenClaw-specific behavior:

- Auto-discovers OpenClaw workspace memory files (`~/.openclaw/workspace/memory/`)
- Indexes `MEMORY.md` + `memory/**/*.md` as a collection
- Supports OpenClaw memory_search parameters:
  - Hybrid search: `vectorWeight: 0.7`, `textWeight: 0.3`
  - MMR diversity: `lambda: 0.7`
  - Temporal decay: 30-day half-life
  - Max results: 8, candidateMultiplier: 4
- Returns snippet text (capped ~700 chars), file path, line range, score

### Thin TS Plugin Wrapper

```typescript
// openclaw-plugin/index.ts
export default (api) => {
  api.registerTool("memory_search", {
    description: "Semantic search over memory files via QMD",
    inputSchema: { query: { type: "string" }, limit: { type: "number" } },
    handler: async ({ query, limit }) => {
      const resp = await fetch(`http://localhost:${port}/search`, {
        method: "POST",
        body: JSON.stringify({ searches: [{ type: "vec", query }], limit }),
      });
      return resp.json();
    },
  });
};
```

### Installation Flow

```bash
# Install qmd binary
brew install qmd   # OR: download from GitHub releases

# Start as OpenClaw sidecar
qmd mcp --http --port 18790 --openclaw

# Install OpenClaw plugin (optional)
openclaw plugins install @qmd/openclaw-plugin
```

OpenClaw config:

```json5
agents: {
  defaults: {
    memorySearch: {
      provider: "qmd",
      remote: { url: "http://localhost:18790" }
    }
  }
}
```

---

## Circuit Breaker + Retry (failsafe-go)

```go
import "github.com/failsafe-go/failsafe-go"
import "github.com/failsafe-go/failsafe-go/circuitbreaker"
import "github.com/failsafe-go/failsafe-go/retrypolicy"

cb := circuitbreaker.Builder[*http.Response]().
    WithFailureThreshold(2).                    // 2 failures → open
    WithDelay(10 * time.Minute).                // 10min cooldown
    Build()

retry := retrypolicy.Builder[*http.Response]().
    WithMaxRetries(3).
    WithBackoff(time.Second, 30*time.Second).
    Build()

resp, err := failsafe.Get(func() (*http.Response, error) {
    return http.DefaultClient.Do(req)
}, retry, cb)
```

Per-endpoint circuit breakers. States: closed → open (2 failures) → half-open (cooldown elapsed) → closed (success).

---

## Implementation Tasks

### Task 0: Project Bootstrap

**Files:** `go.mod`, `cmd/qmd/main.go`, `Makefile`, `.golangci.yaml`, `CLAUDE.md`

- [x] 0a: `go mod init github.com/user/qmd-go` with all dependencies
- [x] 0b: Makefile with build, test, lint, fmt, release targets + LDFLAGS version embedding
- [x] 0c: `.golangci.yaml` (gofmt, gocyclo limit 15, funlen limit 105, mnd)
- [x] 0d: `cmd/qmd/main.go` — urfave/cli v2 app skeleton with `--index` global flag, version
- [x] 0e: CLAUDE.md with build commands, architecture overview, conventions

**Accept:** `make build` produces binary, `qmd --version` works, `make lint` passes.

### Task 1: SQLite + Schema

**Files:** `internal/db/db.go`, `internal/store/schema.go`, `internal/store/types.go`

Port: `src/db.ts` (96 lines), `src/store.ts:612-783`

- [x] 1a: `internal/db/db.go` — `sql.Open` with ncruces driver, PRAGMA setup via DSN params, ping check
- [x] 1b: `internal/store/schema.go` — `initializeDatabase()` executing ALL SQL from schema section above (tables, indexes, FTS5, triggers, legacy drops)
- [x] 1c: sqlite-vec loading via blank import, `vectors_vec` virtual table with graceful degradation
- [x] 1d: `internal/store/types.go` — all shared types (SearchResult, DocumentResult, etc.)
- [x] 1e: Schema migration — check for `seq` column in `content_vectors`, drop/recreate if missing
- [x] 1f: Tests: create new DB, open existing TS DB, vec_version() check, concurrent reads

**Accept:** Go-created DB opens without error in TS qmd. `make test` passes.

### Task 2: Config + Collections

**Files:** `internal/config/config.go`, `internal/config/collections.go`, `internal/config/context.go`

Port: `src/collections.ts` (500 lines), `src/store.ts:789-957`

- [x] 2a: Config types matching TS YAML schema + `providers` section
- [x] 2b: Config paths: `~/.config/qmd/{index}.yml`, XDG_CONFIG_HOME, QMD_CONFIG_DIR
- [x] 2c: loadConfig/saveConfig with exact YAML formatting
- [x] 2d: Config-to-DB sync (SHA-256 hash check, upsert store_collections)
- [x] 2e: Collection CRUD: add, remove, rename, show, set-update, include, exclude
- [x] 2f: Context management: add, remove, list, check, findContextForPath (hierarchical)
- [x] 2g: CLI commands: `qmd collection *`, `qmd context *`
- [x] 2h: Tests: YAML round-trip, config sync, collection CRUD

**Accept:** Add collection in Go, TS sees it. YAML files identical format.

### Task 3: Document Indexing

**Files:** `internal/store/index.go`, `internal/store/virtualpath.go`

Port: `src/store.ts:1058-1180, 1577-1636, 1891-2024`

- [x] 3a: File scanning with doublestar glob + ignore patterns
- [x] 3b: Content hashing (crypto/sha256)
- [x] 3c: Title extraction (markdown headings, org-mode, fallback to filename)
- [x] 3d: Handelize function (triple underscore, emoji→hex, Unicode cleanup)
- [x] 3e: Document CRUD: insertContent, insertDocument, deactivate, getActiveDocumentPaths
- [x] 3f: Virtual path utilities: parse, build, resolve, isVirtualPath, toVirtualPath
- [x] 3g: Reindex orchestration with progress callback
- [x] 3h: CLI command: `qmd update [--pull]`
- [x] 3i: Tests: indexing, title extraction, handelize edge cases, virtual paths

**Accept:** Go-indexed content searchable by TS `qmd search`.

### Task 4: FTS Search + Formatters

**Files:** `internal/store/search.go`, `internal/store/snippet.go`, `internal/format/format.go`, `internal/format/color.go`

Port: `src/store.ts:2653-2818, 3514-3630`, `src/cli/formatter.ts` (430 lines)

- [x] 4a: `buildFTS5Query()` — quoted phrases, negation, prefix matching, Unicode sanitization
- [x] 4b: `searchFTS()` with BM25 score normalization (`|x| / (1 + |x|)`)
- [x] 4c: Snippet extraction with intent weighting and diff-style headers
- [x] 4d: Context resolution (hierarchical inheritance from store_collections)
- [x] 4e: Output formatters: JSON, CSV, XML, Markdown, files list
- [x] 4f: ANSI color output with NO_COLOR/QMD_NO_COLOR support
- [x] 4g: CLI command: `qmd search <text>` with all flags
- [x] 4h: Tests: FTS query building, score normalization, all formatters, snippets

**Accept:** Same query returns same results in Go and TS.

### Task 5: Provider System + Remote Adapters

**Files:** `internal/provider/provider.go`, `internal/provider/registry.go`, `internal/provider/remote.go`, `internal/provider/reranker.go`, `internal/provider/breaker.go`

Port: `src/remote-llm.ts` (580 lines)

- [x] 5a: Embedder, Reranker, Generator interfaces in `provider.go`
- [x] 5b: Remote embedder — OpenAI-compatible `/v1/embeddings` client with provider nuances
- [x] 5c: Remote embedder — Cohere adapter (`/v2/embed`, `texts` field, `input_type`)
- [x] 5d: Remote embedder — Gemini adapter (`/v1beta/.../embedContent`)
- [x] 5e: Remote generator — OpenAI-compatible `/v1/chat/completions`
- [x] 5f: Remote rerankers — Cohere (`/v2/rerank`), Jina (`/v1/rerank`), Voyage (`/v1/rerank` with `top_k`), TEI (`/rerank`)
- [x] 5g: failsafe-go circuit breaker + retry wrapper
- [x] 5h: Provider registry — factory from config/env vars
- [x] 5i: Embedding dimension auto-detection and lock
- [x] 5j: Embedding input normalization (Qwen3 prefix handling)
- [x] 5k: Tests: each adapter with httptest.Server, circuit breaker state transitions

**Accept:** Mock HTTP tests pass for all provider formats.

### Task 6: Local Embedding (kelindar/search)

**Files:** `internal/provider/local.go`

New — replaces hugot/GoMLX from original plan

- [x] 6a: `LocalEmbedder` wrapping `search.NewVectorizer`
- [x] 6b: GGUF model path resolution: `~/.cache/qmd/models/` or config override
- [x] 6c: Lazy initialization (first Embed call loads model)
- [x] 6d: Batch embedding: loop `EmbedText()` per text, collect `[][]float32`
- [x] 6e: `QMD_EMBED_PROVIDER=local` env var or `providers.embed.type: local` config
- [x] 6f: Tests: embed with real model (integration, skipped without model), concurrent calls

**Accept:** `qmd embed --provider local` produces 384d vectors matching TS index format.

### Task 7: Embeddings + Vector Search

**Files:** `internal/store/chunk.go`, `internal/store/vsearch.go`

Port: `src/store.ts:72-226, 1196-1452, 2820-2968`

- [x] 7a: Smart chunking with break point scoring (all patterns from algorithm section)
- [x] 7b: Code fence detection + never-split-inside-fences rule
- [x] 7c: chunkDocument() with char-based token estimation (~4 chars/token)
- [x] 7d: Embedding generation with batch processing (64 docs, 64MB limits)
- [x] 7e: Concurrent embedding coordination (SQL-level batch claiming)
- [x] 7f: Vector insertion — float32→bytes into content_vectors + vectors_vec
- [x] 7g: Vector search — two-step: vec MATCH first, then separate JOIN for metadata
- [x] 7h: CLI commands: `qmd embed [-f] [--provider ...]`, `qmd vsearch <text>`
- [x] 7i: Tests: chunking algorithm, code fence edge cases, vector round-trip
- [x] 7j: Benchmark: 10K-vector cosine search latency

**Accept:** Embed in Go, search in TS (and vice versa). Benchmark logged.

### Task 8: Hybrid Search Pipeline

**Files:** `internal/store/hybrid.go`, `internal/store/structured.go`, `internal/store/rrf.go`

Port: `src/store.ts:2970-4389`

- [x] 8a: Query expansion with LLM cache (SHA-256 key → llm_cache table)
- [x] 8b: RRF implementation (k=60, 2x weight for first 2 lists, top-rank bonus)
- [x] 8c: Best-chunk selection (keyword overlap + intent weighting)
- [x] 8d: Reranking with per-chunk cache (key: query + model + chunk text)
- [x] 8e: Position-aware score blending (0.75/0.60/0.40 by rank)
- [x] 8f: hybridQuery orchestration (all 8 steps from algorithm section)
- [x] 8g: structuredSearch (pre-expanded queries, collection filtering)
- [x] 8h: Explain mode (RRF trace output)
- [x] 8i: CLI command: `qmd query <text>` with --no-rerank, --explain, --intent
- [x] 8j: Tests: RRF fusion, score blending, explain format, cache keys

**Accept:** Same query returns same top-5 in Go and TS.

### Task 9: Document Retrieval

**Files:** `internal/store/docid.go`, `internal/store/fuzzy.go`

Port: `src/store.ts:3173-3461, 2143-2258`

- [ ] 9a: Document lookup: virtual path, docid (#abc123), absolute path
- [ ] 9b: Document body retrieval with line range slicing (1-indexed)
- [ ] 9c: Multi-get: glob patterns, comma-separated, max-bytes threshold
- [ ] 9d: File listing (ls command)
- [ ] 9e: Fuzzy matching: Levenshtein, findSimilarFiles, matchFilesByGlob
- [ ] 9f: Docid utilities: normalize, validate, lookup
- [ ] 9g: CLI commands: `qmd get`, `qmd multi-get`, `qmd ls`
- [ ] 9h: Tests: docid resolution, fuzzy matching, multi-get patterns

**Accept:** All retrieval commands produce identical output to TS.

### Task 10: MCP Server + Multi-Agent

**Files:** `internal/mcp/server.go`, `internal/mcp/session.go`, `internal/mcp/tools.go`, `internal/mcp/rest.go`

Port: `src/mcp/server.ts` (807 lines)

- [ ] 10a: MCP server core with mark3labs/mcp-go (server.NewMCPServer, s.AddTool)
- [ ] 10b: Tools: query, get, multi_get, status (exact JSON schemas from MCP section)
- [ ] 10c: Resource template: `qmd://{+path}`
- [ ] 10d: Dynamic instructions builder
- [ ] 10e: Stdio transport: `qmd mcp [--stdio]`
- [ ] 10f: HTTP transport: `qmd mcp --http [--port N]`
- [ ] 10g: REST endpoints: POST /search, POST /query, GET /health
- [ ] 10h: Multi-agent sessions: per-session McpServer+Transport, shared Store
- [ ] 10i: Daemon mode: `qmd mcp --daemon`, `qmd mcp stop`
- [ ] 10j: Tests: tool handlers, concurrent sessions, REST endpoints

**Accept:** MCP works with Claude Desktop and Claude Code. REST endpoints return correct JSON.

### Task 11: OpenClaw Integration

**Files:** `internal/openclaw/sidecar.go`, `internal/openclaw/memory.go`, `openclaw-plugin/*`

New — OpenClaw sidecar + plugin

- [ ] 11a: `--openclaw` flag on `qmd mcp --http` — auto-discovers workspace memory files
- [ ] 11b: Memory collection auto-setup: index `~/.openclaw/workspace/memory/**/*.md` + `MEMORY.md`
- [ ] 11c: memory_search handler with MMR diversity (lambda 0.7) and temporal decay (30-day half-life)
- [ ] 11d: memory_get handler (return empty text for missing files, not error)
- [ ] 11e: Configurable hybrid weights (vectorWeight/textWeight), maxResults, candidateMultiplier
- [ ] 11f: Thin TS plugin wrapper: package.json, openclaw.plugin.json, index.ts
- [ ] 11g: Tests: sidecar endpoints, MMR diversity, temporal decay scoring

**Accept:** OpenClaw memory_search works with qmd-go sidecar. Plugin installable via `openclaw plugins install`.

### Task 12: CLI Polish + Remaining Commands

**Files:** updates across cmd/qmd/main.go and internal/

- [ ] 12a: `qmd status` — index info, collections, embedding coverage, provider status
- [ ] 12b: `qmd cleanup` — orphan cleanup + vacuum (all skip flags)
- [ ] 12c: `qmd pull` — download/check models via provider
- [ ] 12d: Global flags on root command (all output and filtering flags)
- [ ] 12e: Error handling: consistent messages, colored warnings, exit codes
- [ ] 12f: Tests: status output, cleanup operations, flag parsing

**Accept:** Every CLI command matches TS output format.

### Task 13: Verification + Distribution

- [ ] 13a: Full test suite passes (`make test`)
- [ ] 13b: Linter clean (`make lint`)
- [ ] 13c: Cross-validate every command between Go and TS binaries
- [ ] 13d: Benchmark: search latency, embedding throughput, vector query perf
- [ ] 13e: Goreleaser config for GitHub releases (darwin-arm64, linux-amd64, linux-arm64)
- [ ] 13f: Homebrew formula
- [ ] 13g: Docker image (minimal)
- [ ] 13h: Document behavioral differences (if any)

**Accept:** All benchmarks logged. Release artifacts built. brew install works.

---

## Risk Areas

### HIGH: sqlite-vec WASM performance

ncruces WASM sqlite-vec binding is newer. Benchmark 10K-vector cosine search in Task 7j. Fallback: `mattn/go-sqlite3` with CGO + sqlite-vec CGO bindings (only SQLite layer needs CGO).

### MEDIUM: kelindar/search shared library

Requires `libllama_go.dylib`/`.so` at runtime. macOS/arm64 dylib must be built from source (repo only ships linux-x64 and win-x64 prebuilts). Ship alongside binary in releases. Mitigation: local embedding is optional; remote providers always work.

### MEDIUM: Concurrent embedding coordination

SQL-level claiming (UPDATE...RETURNING) needs testing under concurrent load with multiple processes.

### LOW: Embedding dimension compatibility

Existing TS databases use 384d vectors. Go auto-detects from existing vectors. kelindar/search with MiniLM-L6-v2.Q8_0.gguf also produces 384d — compatible.

---

## Critical Path

```
Task 0 (Bootstrap)
  └→ Task 1 (SQLite + Schema)
       ├→ Task 2 (Config) → Task 5 (Providers) → Task 6 (Local Embed)
       │                                              ↓
       ├→ Task 3 (Indexing) → Task 4 (FTS Search)   Task 7 (Embed + Vector)
       │                                              ↓
       ├→ Task 9 (Retrieval)                    Task 8 (Hybrid Search)
       │                                              ↓
       └→ Task 10 (MCP) ←─────────────────── depends on 8, 9
            └→ Task 11 (OpenClaw) ←── depends on 10
Task 12 (Polish) ←── depends on all
Task 13 (Verify) ←── final
```

## Post-Completion Testing Matrix

- OpenAI API (remote embed + rerank + gen)
- Ollama (local/remote embed + gen via /v1/embeddings)
- kelindar/search (local GGUF embed)
- Cohere (rerank)
- Gemini (embed + gen)
- OpenClaw sidecar (memory_search end-to-end)
- Concurrent: 3 Claude agents searching simultaneously
- Cross-compat: Go binary ↔ TS binary on same database
