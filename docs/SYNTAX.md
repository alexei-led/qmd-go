# QMD Query Syntax

QMD queries are structured documents with typed sub-queries. Each line specifies a search type and query text.

## Grammar

```ebnf
query          = expand_query | query_document ;
expand_query   = text | explicit_expand ;
explicit_expand= "expand:" text ;
query_document = [ intent_line ] { typed_line } ;
intent_line    = "intent:" text newline ;
typed_line     = type ":" text newline ;
type           = "lex" | "vec" | "hyde" ;
text           = quoted_phrase | plain_text ;
quoted_phrase  = '"' { character } '"' ;
plain_text     = { character } ;
newline        = "\n" ;
```

## Query Types

| Type   | Method | Description                        |
| ------ | ------ | ---------------------------------- |
| `lex`  | BM25   | Keyword search with exact matching |
| `vec`  | Vector | Semantic similarity search         |
| `hyde` | Vector | Hypothetical document embedding    |

## Default Behavior

A single-line query with no prefix is treated as an expand query and passed to the expansion model, which emits lex, vec, and hyde variants automatically.

```
# These are equivalent:
how does authentication work
expand: how does authentication work
```

## Lex Query Syntax

```ebnf
lex_query   = { lex_term } ;
lex_term    = negation | phrase | word ;
negation    = "-" ( phrase | word ) ;
phrase      = '"' { character } '"' ;
word        = { letter | digit | "'" } ;
```

| Syntax      | Meaning        | Example                      |
| ----------- | -------------- | ---------------------------- |
| `word`      | Prefix match   | `perf` matches "performance" |
| `"phrase"`  | Exact phrase   | `"rate limiter"`             |
| `-word`     | Exclude term   | `-sports`                    |
| `-"phrase"` | Exclude phrase | `-"test data"`               |

## Vec and Hyde Queries

Vec queries are natural language questions — no special syntax needed.

Hyde queries are hypothetical answer passages (50-100 words). Write what you expect the answer to look like.

## Multi-Line Queries

Combine multiple query types. First query gets 2x weight in fusion.

```
lex: rate limiter algorithm
vec: how does rate limiting work in the API
hyde: The API implements rate limiting using a token bucket algorithm...
```

## Intent

An optional `intent:` line disambiguates vague queries. It steers expansion, reranking, and snippet extraction but does not search on its own.

```
intent: web page load times and Core Web Vitals
lex: performance
vec: how to improve performance
```

## MCP/HTTP API

```json
{
  "searches": [
    { "type": "lex", "query": "CAP theorem" },
    { "type": "vec", "query": "consistency vs availability" }
  ],
  "intent": "distributed systems tradeoffs",
  "limit": 10
}
```

## CLI

```bash
qmd query "how does auth work"
qmd query $'lex: auth token\nvec: how does authentication work'
qmd query --intent "web performance" "performance"
```
