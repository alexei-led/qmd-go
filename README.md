# QMD (Go)

[![CI](https://github.com/alexei-led/qmd-go/actions/workflows/ci.yml/badge.svg)](https://github.com/alexei-led/qmd-go/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/alexei-led/qmd-go)](https://goreportcard.com/report/github.com/alexei-led/qmd-go)
[![Release](https://img.shields.io/github/v/release/alexei-led/qmd-go)](https://github.com/alexei-led/qmd-go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

On-device semantic search for markdown notes. Combines BM25 full-text search, vector similarity, and LLM re-ranking in a single binary.

Go rewrite of [tobi/qmd](https://github.com/tobi/qmd) — same CLI, same database, same config files. Drop-in replacement.

## Why Go

The TypeScript QMD works well but has operational pain points that Go solves:

- **Single binary** — no Node.js runtime, no native module compilation, no `node_modules`. Copy one file and run.
- **Concurrent access** — multiple AI agents searching simultaneously via goroutines and connection pooling. The TS version is single-threaded and crashes under concurrent load.
- **Remote embeddings** — supports any OpenAI-compatible embedding API (OpenAI, Ollama, Voyage, Together, etc.) alongside local GGUF models. The TS version requires a local model.
- **OpenClaw integration** — built-in sidecar mode for [OpenClaw](https://docs.openclaw.ai/reference/memory-config.md) multi-agent memory search with MMR diversity and temporal decay.
- **Fast startup** — no JIT warmup. Cold start under 50ms.

The database schema, config files, and CLI interface are identical. You can switch between the Go and TypeScript binaries on the same index without migration.

## Install

```bash
# Homebrew
brew install alexei-led/tap/qmd

# From source
make build

# Docker
docker run --rm -v ~/.config/qmd:/root/.config/qmd -v ~/.local/share/qmd:/root/.local/share/qmd qmd status
```

Pre-built binaries: darwin-arm64, linux-amd64, linux-arm64.

## Quick Start

```bash
# Add a folder of markdown notes
qmd collection add notes ~/notes

# Index files
qmd update

# Search
qmd search "quantum mechanics"

# Semantic search (requires embeddings)
qmd embed
qmd query "how does entanglement work"

# Get a document
qmd get notes/readme.md
qmd get notes/readme.md:42     # from line 42
```

## Commands

| Command                                        | Description                                           |
| ---------------------------------------------- | ----------------------------------------------------- |
| `search <query>`                               | BM25 full-text search                                 |
| `vsearch <query>`                              | Vector similarity search                              |
| `query <query>`                                | Hybrid search (BM25 + vector + reranking + expansion) |
| `get <path>`                                   | Retrieve document by path, `qmd://` URI, or `#docid`  |
| `multi-get <pattern>`                          | Batch retrieve by glob or comma-separated paths       |
| `ls [collection]`                              | List collections or documents                         |
| `update`                                       | Reindex all collections                               |
| `embed`                                        | Generate vector embeddings                            |
| `pull`                                         | Download embedding models                             |
| `status`                                       | Show index stats                                      |
| `cleanup`                                      | Remove orphaned data                                  |
| `collection <add\|remove\|rename\|list\|show>` | Manage collections                                    |
| `context <add\|remove\|list\|check>`           | Manage context annotations                            |
| `mcp [--port N] [--openclaw]`                  | Start MCP server (stdio or HTTP)                      |

**Global flags:** `--index <name>` (env: `QMD_INDEX`, default: `"default"`), `--format <json|csv|xml|md|files>`, `--no-color`

## Query Syntax

QMD supports structured queries with typed sub-queries. See [docs/SYNTAX.md](docs/SYNTAX.md) for the full grammar.

```bash
# Simple (auto-expanded by LLM into lex/vec/hyde variants)
qmd query "how does authentication work"

# Structured (manual control)
qmd query $'lex: auth token\nvec: how does authentication work'

# With intent (disambiguates vague queries)
qmd query --intent "web performance" "performance"
```

| Type    | Method            | When to use                                   |
| ------- | ----------------- | --------------------------------------------- |
| `lex:`  | BM25 keyword      | Exact terms, code symbols, error messages     |
| `vec:`  | Vector similarity | Concepts, questions, natural language         |
| `hyde:` | Hypothetical doc  | Write what you expect the answer to look like |

## MCP Server

QMD exposes an [MCP](https://modelcontextprotocol.io) server for AI agent integration.

```bash
# Stdio transport (Claude Desktop, Claude Code)
qmd mcp

# HTTP transport (shared server, multiple agents)
qmd mcp --port 18790

# With OpenClaw sidecar
qmd mcp --port 18790 --openclaw
```

**Claude Code** (`~/.claude.json`):

```json
{
  "mcpServers": {
    "qmd": { "command": "qmd", "args": ["mcp"] }
  }
}
```

**Tools:** `query` (hybrid search), `get` (retrieve document), `multi-get` (batch retrieve), `status` (index info)

**Resources:** `qmd://{collection}/{path}` — read any indexed document

## OpenClaw Integration

QMD serves as the memory search backend for [OpenClaw](https://docs.openclaw.ai) agents.

```bash
# Start sidecar (auto-discovers ~/.openclaw/workspace/memory)
qmd mcp --port 18790 --openclaw
```

This registers two additional MCP tools:

- `memory_search` — hybrid search with MMR diversity and temporal decay
- `memory_get` — retrieve memory files

The [OpenClaw plugin](openclaw-plugin/) is a thin TypeScript wrapper that proxies `memory_search` to the QMD HTTP sidecar. See [OpenClaw memory config docs](https://docs.openclaw.ai/reference/memory-config.md) for agent configuration.

## Output Formats

All search commands support `--format` / `-f`:

```bash
qmd search "test" -f json    # JSON array
qmd search "test" -f csv     # CSV with headers
qmd search "test" -f xml     # XML document
qmd search "test" -f md      # Markdown
qmd search "test" -f files   # File paths only (for piping)
```

## Configuration

Config: `~/.config/qmd/{index}.yml` (override: `QMD_CONFIG_DIR` or `XDG_CONFIG_HOME`)
Database: `~/.local/share/qmd/{index}.db` (override: `XDG_DATA_HOME`)

```yaml
providers:
  embed:
    type: openai # or: local, ollama, voyage, together
    url: https://api.openai.com/v1
    model: text-embedding-3-small
    api_key_env: OPENAI_API_KEY
  rerank:
    type: cohere
    api_key_env: COHERE_API_KEY
  generate:
    type: openai
    api_key_env: OPENAI_API_KEY

collections:
  notes:
    path: ~/notes
    pattern: "**/*.md"
    context: "Personal knowledge base"
  docs:
    path: ~/projects/docs
    pattern: "**/*.md"
    ignore_patterns: "**/node_modules/**"
```

## Architecture

```
cmd/qmd/main.go          # CLI entrypoint (urfave/cli v2)
internal/
  db/                     # SQLite via ncruces/go-sqlite3 (WASM, no CGO)
  store/                  # Search, indexing, chunking, RRF fusion, embedding
  config/                 # YAML config, collection CRUD, context management
  provider/               # Embedder/Reranker/Generator interfaces + adapters
  format/                 # Output formatters (JSON, CSV, XML, MD)
  mcp/                    # MCP server (stdio + HTTP) + REST endpoints
  openclaw/               # OpenClaw sidecar (memory_search, temporal decay, MMR)
```

**Search pipeline** (8 steps):

1. BM25 probe → strong signal detection
2. Query expansion via LLM (lex/vec/hyde variants)
3. Parallel FTS + vector search
4. RRF fusion (reciprocal rank fusion)
5. Content loading + smart chunking
6. Reranking via cross-encoder
7. Position-aware score blending
8. Dedup + snippet extraction

**Key design choices:**

- No CGO at compile time — SQLite via WASM ([ncruces/go-sqlite3](https://github.com/nicruces/go-sqlite3))
- Provider interfaces decouple search from embedding backends
- WAL mode + connection pooling for concurrent multi-agent access
- Content-addressable storage (SHA-256) deduplicates across collections

## Differences from TypeScript QMD

See [docs/behavioral-differences.md](docs/behavioral-differences.md). All CLI commands, flags, output formats, config files, and database operations are functionally equivalent.

## Development

```bash
make build        # Build binary
make test         # Run tests (298 tests, race detector)
make lint         # golangci-lint
make fmt          # gofumpt + goimports
make mock         # Regenerate provider mocks (mockery)
make release      # Cross-compile
```

## License

[MIT](LICENSE)
