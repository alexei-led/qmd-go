# Modularity Review: QMD-Go

**Date:** 2026-03-29
**Scope:** All internal packages (`db`, `config`, `store`, `provider`, `format`, `mcp`, `openclaw`) + CLI entrypoint
**Model:** Balanced Coupling (Vlad Khononov)

---

## Domain Classification

| Package    | Subdomain      | Rationale                                                                              |
| ---------- | -------------- | -------------------------------------------------------------------------------------- |
| `store`    | **Core**       | Primary value: hybrid search, indexing, chunking, RRF — the reason QMD exists          |
| `openclaw` | **Core**       | Key differentiator: multi-agent memory search is the primary evolution direction       |
| `provider` | **Generic**    | Pluggable embedding/reranking backends; problem is well-defined, implementation varies |
| `mcp`      | **Supporting** | Integration layer exposing store capabilities to agents via MCP protocol               |
| `config`   | **Generic**    | Standard YAML config + XDG path resolution                                             |
| `db`       | **Generic**    | SQLite connection setup with PRAGMAs                                                   |
| `format`   | **Generic**    | Output rendering (JSON, CSV, XML, MD)                                                  |

---

## Integration Map

### Integration 1: openclaw → store (Explain trace internals)

**What's shared:** `memory.go` reads `HybridResult.Explain.Sources` string values to classify search origins. It pattern-matches on prefixes like `"vec:"` and `"lex:"` to split vector vs. lexical scores for weighted blending.

```
openclaw/memory.go  →  store.HybridResult.Explain.Sources []string
                       (parses "vec:query", "lex:query" prefixes)
```

- **Strength:** **Intrusive** — OpenClaw depends on the internal labeling scheme of the hybrid pipeline's source tracking. These string labels are implementation details of `store/hybrid.go`'s RRF fusion, not a documented contract.
- **Distance:** Low (same binary, same developer)
- **Volatility:** **High** — The hybrid scoring pipeline is core domain, actively evolving. Multi-agent memory search is the primary use case driving the Go rewrite. Any change to how sources are labeled, how explain traces are structured, or how RRF fusion categorizes results will silently break OpenClaw's weighted scoring.

**Balance:** `HIGH strength XOR LOW distance = UNBALANCED` **AND** `HIGH volatility` → **Critical issue.**

**Impact:** If you refine the hybrid pipeline (add a new search type like "hyde:", rename source labels, restructure ExplainTrace), OpenClaw's `applyWeightedScoring()` silently degrades — it won't crash, it'll just produce wrong scores. This is the most dangerous kind of coupling: silent semantic failure.

**Recommendation:** Define an explicit contract for search source classification. Two options:

1. **Typed source enum:** Replace `Sources []string` with `Sources []SearchSource` where `SearchSource` has a `Type` field (`Vector`, `Lexical`, `HyDE`) defined in `store`. OpenClaw switches on the type instead of parsing strings.
2. **Pre-computed scores in HybridResult:** Have the hybrid pipeline return separate `VectorScore` and `LexicalScore` fields directly, so consumers don't need to reverse-engineer source origins.

Option 2 is simpler and eliminates the coupling entirely — OpenClaw wouldn't need to know _how_ scores were computed, only _what_ the component scores are.

---

### Integration 2: openclaw → config (Config mutation)

**What's shared:** `sidecar.go` directly mutates `cfg.Collections` map to inject the `"openclaw-memory"` collection, then calls `config.SaveFile()` and `config.SyncToDB()`.

```
openclaw/sidecar.go  →  cfg.Collections["openclaw-memory"] = CollectionConfig{...}
                     →  config.SaveFile(cfgPath, cfg)
                     →  config.SyncToDB(db, cfg)
```

- **Strength:** **Intrusive** — OpenClaw reaches into Config's internal map and mutates it. It knows the exact structure of `CollectionConfig`, the semantics of `SaveFile`, and the sync-to-DB flow. It also owns knowledge about the default memory directory path (`~/.openclaw/workspace/memory`).
- **Distance:** Low (same binary, same developer)
- **Volatility:** **High** — Multi-agent support means multiple memory directories per agent. The current single-collection approach (`"openclaw-memory"`) will need to evolve to support per-agent collections, different file patterns, and dynamic registration.

**Balance:** `HIGH strength XOR LOW distance = UNBALANCED` **AND** `HIGH volatility` → **Critical issue.**

**Impact:** When you add multi-agent support (multiple memory folders), you'll need to change how OpenClaw registers collections. The current tight mutation pattern means every change to collection registration touches both `openclaw/sidecar.go` AND `config/collections.go` internals.

**Recommendation:** Introduce a `config.RegisterCollection(db, cfg, cfgPath, name, opts)` function that encapsulates the add-save-sync flow. OpenClaw calls this instead of mutating internals. This also provides a natural extension point for multi-agent: `RegisterCollection` can handle deduplication, validation, and conflict resolution in one place.

---

### Integration 3: config → database (Raw SQL for store tables)

**What's shared:** The `config` package executes raw SQL against `store_collections` and `store_config` tables — tables whose schema is defined in `store/schema.go`.

```
config/collections.go  →  INSERT INTO store_collections ...
                       →  SELECT/DELETE/UPDATE store_collections
config/context.go      →  INSERT INTO store_config ...
                       →  SELECT FROM store_collections
```

- **Strength:** **Intrusive** — `config` knows exact column names, types, and UPSERT patterns for tables it doesn't own.
- **Distance:** Low (same binary, same developer)
- **Volatility:** **Low** — Schema is frozen for TypeScript compatibility. Column names and table structure won't change without breaking the TS↔Go interchangeability guarantee.

**Balance:** `HIGH strength XOR LOW distance = UNBALANCED` **BUT** `LOW volatility` → **Tolerable technical debt.**

**Impact:** Low urgency. The schema compatibility constraint with TypeScript means these tables are effectively a stable contract. If you ever drop TS compatibility, this becomes a real problem — schema changes in `store/schema.go` would require synchronized changes in `config/`.

**Note for future:** If TS compatibility is dropped, move collection/context DB operations into `store` and have `config` call store functions instead of raw SQL. Until then, this is fine.

---

### Integration 4: mcp/tools.go → database (Raw SQL bypass)

**What's shared:** The `statusHandler` in `mcp/tools.go` runs direct SQL queries (`SELECT COUNT(*) FROM documents`, `SELECT COUNT(*) FROM content_vectors`) instead of calling `store.GetStatus()`.

- **Strength:** **Intrusive** — knows table names and column semantics
- **Distance:** Low
- **Volatility:** **Low** — simple count queries on stable tables

**Balance:** `UNBALANCED` but `LOW volatility` → **Minor debt.**

**Recommendation:** Replace with `store.GetStatus()` call when convenient. Not urgent.

---

### Integration 5: CLI ↔ MCP orchestration duplication

**What's shared:** This isn't a coupling issue in the Balanced Coupling sense — it's a **cohesion** issue. The CLI commands (`getAction`, `queryAction`, `multiGetAction`) and MCP tool handlers (`getHandler`, `queryHandler`, `multiGetHandler`) duplicate identical parameter construction and store call sequences.

```
cmd/qmd/main.go:queryAction()     →  store.HybridQuery(...)
internal/mcp/tools.go:queryHandler()  →  store.StructuredSearch(...)

cmd/qmd/main.go:getAction()       →  store.FindDocument() + GetDocumentBody()
internal/mcp/tools.go:getHandler()    →  store.FindDocument() + GetDocumentBody()
```

- **Observation:** The CLI `query` command calls `store.HybridQuery()` (full 8-step pipeline with generator), while the MCP `query` tool calls `store.StructuredSearch()` (no query expansion). This is an intentional behavioral split, not accidental duplication.
- **The `get` and `multi-get` commands** are genuine duplication — identical logic in both paths.

**Impact:** Bug fixes to parameter handling must be applied in two places. As the codebase evolves, these paths can drift silently.

**Recommendation:** Not urgent for a single-developer project. If it starts causing drift bugs, extract shared action functions that both CLI and MCP call. The format/output layer is already cleanly separated — the issue is only in parameter construction and error handling.

---

## Integrations That Are Well-Balanced

These deserve acknowledgment — they demonstrate good design:

### store → provider (Interface contracts) ✓

Store calls `Embedder.Embed()`, `Reranker.Rerank()`, `Generator.Generate()` through clean interfaces. **Contract coupling + Low distance + High volatility = BALANCED.** The provider abstraction is the right level — it allows swapping local/remote backends without touching search logic.

### format → store (Read-only value types) ✓

Format receives `store.SearchResult` structs and renders them. **Functional coupling + Low distance + Low volatility = BALANCED.** Pure function with no side effects.

### main.go orchestration ✓

The CLI entrypoint follows a clean pipeline: `config.Load() → db.Open() → store.InitializeDatabase() → provider.New*() → store/mcp calls`. Each step receives outputs from the previous. **Functional coupling at integration seam — appropriate for an entrypoint.**

### db package isolation ✓

`db.Open()` returns `*sql.DB` and nothing else. Zero knowledge of business domain. **Minimal contract coupling.**

---

## Summary

| #   | Integration                    | Strength     | Distance | Volatility | Verdict      |
| --- | ------------------------------ | ------------ | -------- | ---------- | ------------ |
| 1   | openclaw → store Explain trace | Intrusive    | Low      | **High**   | **Critical** |
| 2   | openclaw → config mutation     | Intrusive    | Low      | **High**   | **Critical** |
| 3   | config → DB raw SQL            | Intrusive    | Low      | Low        | Tolerable    |
| 4   | mcp → DB raw SQL               | Intrusive    | Low      | Low        | Minor        |
| 5   | CLI ↔ MCP duplication          | — (cohesion) | Low      | Moderate   | Monitor      |
| —   | store → provider interfaces    | Contract     | Low      | High       | ✓ Balanced   |
| —   | format → store types           | Functional   | Low      | Low        | ✓ Balanced   |
| —   | main.go pipeline               | Functional   | Low      | Low        | ✓ Balanced   |

### Priority Actions

1. **Issue #1 — Typed source scores.** Add `VectorScore`/`LexicalScore` fields to `HybridResult` so OpenClaw doesn't parse source label strings. Estimated scope: ~20 lines in `store/hybrid.go`, ~15 lines in `openclaw/memory.go`.

2. **Issue #2 — Collection registration API.** Add `config.RegisterCollection()` that encapsulates mutate-save-sync. This also prepares for multi-agent support (multiple memory directories). Estimated scope: ~30 lines in `config/collections.go`, ~10 lines in `openclaw/sidecar.go`.

3. **Issues #3–5** — No action needed now. Revisit #3 if TS compatibility is dropped. Address #5 if drift bugs appear.

---

## Architecture Diagram

```
┌─────────────────────────────────────────────────────┐
│                   cmd/qmd/main.go                    │
│              (CLI orchestration + wiring)             │
└──┬──────┬──────┬──────┬──────┬──────┬───────────────┘
   │      │      │      │      │      │
   ▼      ▼      ▼      ▼      ▼      ▼
┌──────┐┌──────┐┌──────┐┌──────────┐┌────────┐┌──────┐
│config││  db  ││format││ provider ││  store  ││ mcp  │
│      ││      ││      ││          ││         ││      │
│ YAML ││SQLite││ JSON ││Embedder  ││ Search  ││ MCP  │
│ paths││ WAL  ││ CSV  ││Reranker  ││ Index   ││ REST │
│ sync ││      ││ XML  ││Generator ││ Hybrid  ││      │
└──┬───┘└──────┘└──────┘└──────────┘└────┬────┘└──┬───┘
   │                                      │        │
   │  ⚠ Intrusive: raw SQL               │        │
   │  on store_collections table          │        │
   ├──────────────────────────────────────┘        │
   │                                               │
   │         ┌──────────┐                          │
   │         │ openclaw │◄─────────────────────────┘
   │         │          │   (conditional sidecar)
   │  ⚠ ────│ memory   │
   │ mutates │ search   │──⚠── parses Explain.Sources
   │ Config  │          │      string labels
   │         └──────────┘
   │
   ▼
  [DB: store_collections, store_config]
```

`⚠` = unbalanced coupling in volatile area (Issues #1, #2)
