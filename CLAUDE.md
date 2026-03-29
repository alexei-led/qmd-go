# QMD Go

Go rewrite of QMD (Query Markup Documents) — semantic search over markdown notes.

## Build Commands

```bash
make build        # Build binary with version embedding
make test         # Run tests with race detector
make lint         # Run golangci-lint
make fmt          # Format code (gofumpt + goimports)
make release      # Cross-compile for darwin-arm64, linux-amd64, linux-arm64
make clean        # Remove build artifacts
```

## Architecture

```
cmd/qmd/main.go          # CLI entrypoint (urfave/cli v2)
internal/
  db/db.go               # SQLite via ncruces/go-sqlite3 (WASM, no CGO)
  store/                  # Core: schema, search, indexing, chunking, RRF
  config/                 # YAML config + collection CRUD
  provider/              # Embedding/reranking/generation (local + remote)
  format/                # Output formatters (JSON, CSV, XML, MD)
  mcp/                   # MCP server (stdio + HTTP) + REST endpoints
  openclaw/              # OpenClaw sidecar integration
```

## Key Dependencies

- `ncruces/go-sqlite3` — SQLite via WASM (no CGO)
- `asg017/sqlite-vec-go-bindings` — Vector search extension
- `urfave/cli/v2` — CLI framework
- `kelindar/search` — Local GGUF embedding (purego)
- `failsafe-go/failsafe-go` — Circuit breaker + retry
- `mark3labs/mcp-go` — MCP protocol
- `stretchr/testify` — Test assertions

## Conventions

- No CGO at compile time
- SQLite WAL mode for concurrent access
- All SQL constants must match the TypeScript schema exactly
- Use `database/sql` with ncruces driver for connection pooling
- Provider interfaces: Embedder, Reranker, Generator
- Config paths: `~/.config/qmd/{index}.yml`
- DB path: `~/.local/share/qmd/{index}.db`

## Global CLI Flag

`--index <name>` (env: QMD_INDEX, default: "default") selects the config + database pair.
