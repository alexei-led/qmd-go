package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Levenshtein computes the edit distance between two strings.
func Levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// FindSimilarFiles returns active document paths within the given edit distance.
func FindSimilarFiles(d *sql.DB, query string, maxDistance, limit int) ([]string, error) {
	rows, err := d.Query(`SELECT path FROM documents WHERE active = 1`)
	if err != nil {
		return nil, fmt.Errorf("query paths: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type scored struct {
		path string
		dist int
	}
	var matches []scored
	queryLower := strings.ToLower(query)

	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		dist := Levenshtein(strings.ToLower(path), queryLower)
		if dist <= maxDistance {
			matches = append(matches, scored{path, dist})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i].dist < matches[j].dist })
	if len(matches) > limit {
		matches = matches[:limit]
	}

	result := make([]string, len(matches))
	for i, m := range matches {
		result[i] = m.path
	}
	return result, nil
}

// GlobMatch represents a document matched by a glob pattern.
type GlobMatch struct {
	Filepath    string // qmd://collection/path
	DisplayPath string // collection/path
	BodyLength  int
}

// MatchFilesByGlob returns all active documents matching the given glob pattern.
func MatchFilesByGlob(d *sql.DB, pattern string) ([]GlobMatch, error) {
	rows, err := d.Query(`SELECT 'qmd://' || d.collection || '/' || d.path,
		d.collection || '/' || d.path,
		d.path, LENGTH(content.doc)
		FROM documents d JOIN content ON content.hash = d.hash
		WHERE d.active = 1`)
	if err != nil {
		return nil, fmt.Errorf("query documents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var matches []GlobMatch
	for rows.Next() {
		var virtualPath, displayPath, relPath string
		var bodyLen int
		if err := rows.Scan(&virtualPath, &displayPath, &relPath, &bodyLen); err != nil {
			return nil, err
		}
		matchVP, _ := doublestar.Match(pattern, virtualPath)
		matchRel, _ := doublestar.Match(pattern, relPath)
		matchDisp, _ := doublestar.Match(pattern, displayPath)
		if matchVP || matchRel || matchDisp {
			matches = append(matches, GlobMatch{
				Filepath:    virtualPath,
				DisplayPath: displayPath,
				BodyLength:  bodyLen,
			})
		}
	}
	return matches, rows.Err()
}
