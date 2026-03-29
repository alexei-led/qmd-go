# Behavioral Differences: Go vs TypeScript QMD

This document tracks known behavioral differences between the Go rewrite and the
original TypeScript implementation.

## Embedding Runtime

- Go uses `kelindar/search` (purego + llama.cpp shared lib) for local embedding.
  Requires `libllama_go.dylib` (macOS) or `libllama_go.so` (Linux) on the library
  path. The TS version uses `node-llama-cpp` which bundles its own native binary.
- Local embedding is optional in both versions; remote providers always work.

## SQLite Driver

- Go uses `ncruces/go-sqlite3` (WASM-based, no CGO). The TS version uses
  `better-sqlite3` or `bun:sqlite` (native bindings).
- Both use WAL mode and the same schema. Databases are interchangeable.

## CLI Framework

- Go uses `urfave/cli/v2`. Minor differences in help text formatting and flag
  parsing (e.g., `--flag=value` vs `--flag value` both work in Go).

## Error Messages

- Error message wording may differ but exit codes and semantics are preserved.

## Concurrency

- Go uses goroutines and `database/sql` connection pooling for concurrent access.
  The TS version uses single-threaded async with explicit locking.

## No Known Functional Differences

All CLI commands, flags, output formats, config files, and database operations
are functionally equivalent. If you discover a difference, please file an issue.
