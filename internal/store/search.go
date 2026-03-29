package store

import (
	"database/sql"
	"math"
	"strings"
	"unicode"
)

// SearchOpts configures an FTS search.
type SearchOpts struct {
	Limit        int
	MinScore     float64
	Collection   string
	SearchAll    bool
	Intent       string
	ContextLines int
	ShowFull     bool
}

// SearchFTS runs a full-text search using FTS5 with BM25 scoring.
func SearchFTS(d *sql.DB, query string, opts SearchOpts) ([]SearchResult, error) {
	ftsQuery := BuildFTS5Query(query)
	if ftsQuery == nil {
		return nil, nil
	}

	q, args := buildSearchSQL(*ftsQuery, opts)

	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return collectSearchResults(rows, query, opts)
}

func buildSearchSQL(ftsQuery string, opts SearchOpts) (string, []any) {
	limit := max(opts.Limit, 1)

	q := `
		SELECT d.id, d.collection, d.path, d.title, d.hash,
			bm25(documents_fts, 1.0, 2.0, 5.0) AS score,
			c.doc,
			COALESCE(sc.context, '') AS ctx
		FROM documents_fts f
		JOIN documents d ON d.id = f.rowid
		JOIN content c ON c.hash = d.hash
		LEFT JOIN store_collections sc ON sc.name = d.collection
		WHERE documents_fts MATCH ?
			AND d.active = 1`

	args := []any{ftsQuery}

	if opts.Collection != "" {
		q += ` AND d.collection = ?`
		args = append(args, opts.Collection)
	} else if !opts.SearchAll {
		q += ` AND d.collection IN (SELECT name FROM store_collections WHERE include_by_default = 1)`
	}

	q += ` ORDER BY score LIMIT ?`
	args = append(args, limit)

	return q, args
}

func collectSearchResults(rows *sql.Rows, query string, opts SearchOpts) ([]SearchResult, error) {
	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var rawScore float64
		var body, ctx string
		if err := rows.Scan(&r.DocID, &r.Collection, &r.Path, &r.Title, &r.Hash, &rawScore, &body, &ctx); err != nil {
			return nil, err
		}

		abs := math.Abs(rawScore)
		r.Score = abs / (1 + abs)
		r.Context = ctx

		if opts.MinScore > 0 && r.Score < opts.MinScore {
			continue
		}

		if opts.ShowFull {
			r.Body = body
		} else {
			snip := ExtractSnippet(body, query, opts.Intent, opts.ContextLines)
			r.Snippet = snip.Text
			r.LineStart = snip.LineStart
			r.LineEnd = snip.LineEnd
		}

		results = append(results, r)
	}

	return results, rows.Err()
}

// searchToken represents a parsed token from a search query.
type searchToken struct {
	text    string
	negated bool
	phrase  bool
}

// BuildFTS5Query converts a user query into an FTS5 MATCH expression.
// Returns nil for empty or all-negation queries.
func BuildFTS5Query(input string) *string {
	tokens := parseSearchTokens(input)
	if len(tokens) == 0 {
		return nil
	}

	var positive, negative []string
	for _, t := range tokens {
		if t.negated {
			if t.phrase {
				negative = append(negative, `"`+t.text+`"`)
			} else {
				negative = append(negative, `"`+t.text+`"*`)
			}
		} else {
			if t.phrase {
				positive = append(positive, `"`+t.text+`"`)
			} else {
				positive = append(positive, `"`+t.text+`"*`)
			}
		}
	}

	if len(positive) == 0 {
		return nil
	}

	q := strings.Join(positive, " AND ")
	for _, neg := range negative {
		q += " NOT " + neg
	}

	return &q
}

// parseSearchTokens splits a search string into tokens, handling quoted phrases and negation.
func parseSearchTokens(input string) []searchToken {
	var tokens []searchToken
	runes := []rune(input)
	i := 0

	for i < len(runes) {
		for i < len(runes) && unicode.IsSpace(runes[i]) {
			i++
		}
		if i >= len(runes) {
			break
		}

		negated := false
		if runes[i] == '-' {
			negated = true
			i++
			if i >= len(runes) {
				break
			}
		}

		if runes[i] == '"' {
			i++
			end := i
			for end < len(runes) && runes[end] != '"' {
				end++
			}
			text := strings.TrimSpace(string(runes[i:end]))
			if end < len(runes) {
				end++ // skip closing quote
			}
			i = end
			if text != "" {
				tokens = append(tokens, searchToken{text: text, negated: negated, phrase: true})
			}
		} else {
			end := i
			for end < len(runes) && !unicode.IsSpace(runes[end]) {
				end++
			}
			text := sanitizeFTSTerm(string(runes[i:end]))
			i = end
			if text != "" {
				tokens = append(tokens, searchToken{text: text, negated: negated, phrase: false})
			}
		}
	}

	return tokens
}

// sanitizeFTSTerm strips non-alphanumeric characters except internal hyphens.
func sanitizeFTSTerm(s string) string {
	var result []rune
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			result = append(result, r)
		} else if r == '-' && len(result) > 0 {
			result = append(result, r)
		}
	}
	for len(result) > 0 && result[len(result)-1] == '-' {
		result = result[:len(result)-1]
	}
	return string(result)
}
