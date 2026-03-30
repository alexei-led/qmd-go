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

## Tooling Notes

### golangci-lint v2 Config Format

This project uses golangci-lint v2 (`.golangci.yaml` with `version: "2"`). Key differences from v1:

- Linter settings go under `linters.settings`, not top-level `linters-settings`
- Formatters (`goimports`, `gofmt`) go under `formatters.enable`, not `linters.enable`
- `typecheck`, `gosimple`, `stylecheck` are removed — subsumed by `staticcheck`
- `perfsprint` was removed from linters in v2

### CI: golangci-lint v2 Requires Manual Install

`golangci/golangci-lint-action@v6` installs v1.x which cannot parse Go 1.26+ code. The CI workflow uses `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest` instead.

### Release: Matrix Cross-Compilation

The release workflow uses GitHub Actions matrix strategy for parallel cross-compilation (4 jobs: darwin-arm64/amd64, linux-amd64/arm64). Each job uploads its binary via `actions/upload-artifact@v4`. The release job downloads all with `actions/download-artifact@v4` and `merge-multiple: true`.

### Mock Generation

Uses mockery v2.53.6+ with `--with-expecter` for type-safe testify-compatible mocks. Generated mocks in `internal/provider/mocks/`. Regenerate with `make mock`.
