package store

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
)

// ProgressFunc is called during reindex with status updates.
type ProgressFunc func(collection, status string, current, total int)

// ScanFiles returns file paths matching a collection's glob pattern,
// excluding any paths matching ignore patterns. Paths are relative to basePath.
func ScanFiles(basePath, pattern, ignorePatterns string) ([]string, error) {
	absBase, err := filepath.Abs(basePath)
	if err != nil {
		return nil, fmt.Errorf("resolve base path: %w", err)
	}

	info, err := os.Stat(absBase)
	if err != nil {
		return nil, fmt.Errorf("stat base path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("base path is not a directory: %s", absBase)
	}

	var ignoreList []string
	if ignorePatterns != "" {
		for _, p := range strings.Split(ignorePatterns, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				ignoreList = append(ignoreList, p)
			}
		}
	}

	var matches []string
	fsys := os.DirFS(absBase)
	err = doublestar.GlobWalk(fsys, pattern, func(path string, d fs.DirEntry) error {
		if d.IsDir() {
			return nil
		}
		for _, ig := range ignoreList {
			if ok, _ := doublestar.Match(ig, path); ok {
				return nil
			}
		}
		matches = append(matches, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("glob walk: %w", err)
	}

	return matches, nil
}

// HashContent returns the SHA-256 hex digest of the given content.
func HashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h)
}

// ExtractTitle extracts a title from document content.
// Priority: markdown heading, org-mode title, fallback to filename.
func ExtractTitle(content, filename string) string {
	if t := extractMarkdownTitle(content); t != "" {
		return t
	}
	if t := extractOrgTitle(content); t != "" {
		return t
	}
	return titleFromFilename(filename)
}

var markdownHeadingRe = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)

func extractMarkdownTitle(content string) string {
	m := markdownHeadingRe.FindStringSubmatch(content)
	if len(m) < 2 { //nolint:mnd
		return ""
	}
	return strings.TrimSpace(m[1])
}

var orgTitleRe = regexp.MustCompile(`(?m)^#\+(?i:title):\s+(.+)$`)

func extractOrgTitle(content string) string {
	m := orgTitleRe.FindStringSubmatch(content)
	if len(m) < 2 { //nolint:mnd
		return ""
	}
	return strings.TrimSpace(m[1])
}

func titleFromFilename(filename string) string {
	base := filepath.Base(filename)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.ReplaceAll(name, "_", " ")
	if name == "" {
		return base
	}
	return name
}

// Handelize converts a file path to a URL-safe "handle" form.
// Rules from TS store.ts:1577-1636:
//   - Triple underscore ___ -> folder separator /
//   - Emoji -> hex codepoints (e.g., U+1F389 -> 1f389)
//   - Non-letter/number -> dash separator
//   - Preserve file extension
//   - Must have at least one letter, number, or emoji
//
//nolint:gocyclo
func Handelize(path string) string {
	ext := filepath.Ext(path)
	name := strings.TrimSuffix(path, ext)

	// Triple underscore -> /
	name = strings.ReplaceAll(name, "___", "/")

	var result strings.Builder
	prevDash := false
	hasContent := false

	for i := 0; i < len(name); {
		r, size := utf8.DecodeRuneInString(name[i:])
		i += size

		if r == utf8.RuneError && size == 1 {
			continue
		}

		switch {
		case r == '/':
			if result.Len() > 0 && !prevDash {
				result.WriteRune('/')
			}
			prevDash = false
			hasContent = true
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if r > 0x7F && isEmoji(r) {
				// Emoji -> hex codepoints
				fmt.Fprintf(&result, "%x", r)
			} else {
				result.WriteRune(unicode.ToLower(r))
			}
			prevDash = false
			hasContent = true
		case isEmoji(r):
			fmt.Fprintf(&result, "%x", r)
			prevDash = false
			hasContent = true
		default:
			// Non-letter/number -> dash separator (collapse consecutive)
			if result.Len() > 0 && !prevDash {
				result.WriteByte('-')
				prevDash = true
			}
		}
	}

	if !hasContent {
		return ""
	}

	// Trim trailing dash
	s := result.String()
	s = strings.TrimRight(s, "-")

	// Restore extension
	if ext != "" {
		s += strings.ToLower(ext)
	}

	return s
}

// isEmoji returns true for common emoji Unicode ranges.
//
//nolint:gocyclo
func isEmoji(r rune) bool {
	return (r >= 0x1F600 && r <= 0x1F64F) || // emoticons
		(r >= 0x1F300 && r <= 0x1F5FF) || // misc symbols & pictographs
		(r >= 0x1F680 && r <= 0x1F6FF) || // transport & map
		(r >= 0x1F900 && r <= 0x1F9FF) || // supplemental symbols
		(r >= 0x1FA00 && r <= 0x1FA6F) || // chess symbols
		(r >= 0x1FA70 && r <= 0x1FAFF) || // symbols extended-A
		(r >= 0x2600 && r <= 0x26FF) || // misc symbols
		(r >= 0x2700 && r <= 0x27BF) || // dingbats
		(r >= 0xFE00 && r <= 0xFE0F) || // variation selectors
		(r >= 0x200D && r <= 0x200D) // ZWJ
}

// InsertContent inserts content into the content table (content-addressable).
// Returns the hash. No-op if hash already exists.
func InsertContent(d *sql.DB, content string) (string, error) {
	hash := HashContent(content)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.Exec(
		`INSERT OR IGNORE INTO content (hash, doc, created_at) VALUES (?, ?, ?)`,
		hash, content, now,
	)
	if err != nil {
		return "", fmt.Errorf("insert content: %w", err)
	}
	return hash, nil
}

// InsertDocument inserts or updates a document in the documents table.
// If a document with the same (collection, path) exists, it updates title/hash.
func InsertDocument(d *sql.DB, collection, path, title, hash string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	var existingID int64
	err := d.QueryRow(
		`SELECT id FROM documents WHERE collection = ? AND path = ?`,
		collection, path,
	).Scan(&existingID)

	if err == nil {
		_, err = d.Exec(
			`UPDATE documents SET title = ?, hash = ?, modified_at = ?, active = 1 WHERE id = ?`,
			title, hash, now, existingID,
		)
		if err != nil {
			return 0, fmt.Errorf("update document: %w", err)
		}
		return existingID, nil
	}

	result, err := d.Exec(
		`INSERT INTO documents (collection, path, title, hash, created_at, modified_at, active) VALUES (?, ?, ?, ?, ?, ?, 1)`,
		collection, path, title, hash, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("insert document: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get last insert id: %w", err)
	}
	return id, nil
}

// DeactivateDocuments marks documents as inactive for paths not in the given set.
// Returns the number of deactivated documents.
func DeactivateDocuments(d *sql.DB, collection string, activePaths map[string]bool) (int, error) {
	rows, err := d.Query(
		`SELECT id, path FROM documents WHERE collection = ? AND active = 1`,
		collection,
	)
	if err != nil {
		return 0, fmt.Errorf("query active docs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var toDeactivate []int64
	for rows.Next() {
		var id int64
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			return 0, fmt.Errorf("scan row: %w", err)
		}
		if !activePaths[path] {
			toDeactivate = append(toDeactivate, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate rows: %w", err)
	}

	for _, id := range toDeactivate {
		if _, err := d.Exec(`UPDATE documents SET active = 0 WHERE id = ?`, id); err != nil {
			return 0, fmt.Errorf("deactivate doc %d: %w", id, err)
		}
	}

	return len(toDeactivate), nil
}

// GetActiveDocumentPaths returns a map of path -> hash for all active documents in a collection.
func GetActiveDocumentPaths(d *sql.DB, collection string) (map[string]string, error) {
	rows, err := d.Query(
		`SELECT path, hash FROM documents WHERE collection = ? AND active = 1`,
		collection,
	)
	if err != nil {
		return nil, fmt.Errorf("query active paths: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]string)
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		result[path] = hash
	}
	return result, rows.Err()
}

// indexFile reads a file and inserts/updates its content and document record.
// Returns true if the file was new or updated, false if skipped.
func indexFile(d *sql.DB, collection, basePath, relPath string, existing map[string]string) (bool, error) {
	absPath := filepath.Join(basePath, relPath)
	content, err := os.ReadFile(absPath)
	if err != nil {
		slog.Warn("failed to read file", "path", absPath, "error", err)
		return false, nil //nolint:nilerr
	}

	contentStr := string(content)
	newHash := HashContent(contentStr)

	if existingHash, ok := existing[relPath]; ok && existingHash == newHash {
		return false, nil
	}

	hash, err := InsertContent(d, contentStr)
	if err != nil {
		return false, fmt.Errorf("insert content for %s: %w", relPath, err)
	}

	title := ExtractTitle(contentStr, relPath)
	if _, err := InsertDocument(d, collection, relPath, title, hash); err != nil {
		return false, fmt.Errorf("insert document %s: %w", relPath, err)
	}

	return true, nil
}

// ReindexCollection scans files and indexes them into the database.
// It inserts new/updated content, creates document records, and deactivates
// documents whose files are no longer present.
func ReindexCollection(d *sql.DB, name, basePath, pattern, ignorePatterns string, progress ProgressFunc) error {
	if progress != nil {
		progress(name, "scanning", 0, 0)
	}

	files, err := ScanFiles(basePath, pattern, ignorePatterns)
	if err != nil {
		return fmt.Errorf("scan %q: %w", name, err)
	}

	slog.Info("scanning collection", "name", name, "files", len(files))
	existing, err := GetActiveDocumentPaths(d, name)
	if err != nil {
		return fmt.Errorf("get existing paths for %q: %w", name, err)
	}

	activePaths := make(map[string]bool, len(files))
	indexed := 0

	for i, relPath := range files {
		activePaths[relPath] = true
		updated, err := indexFile(d, name, basePath, relPath, existing)
		if err != nil {
			return err
		}
		if updated {
			indexed++
		}
		if progress != nil {
			progress(name, "indexing", i+1, len(files))
		}
	}

	deactivated, err := DeactivateDocuments(d, name, activePaths)
	if err != nil {
		return fmt.Errorf("deactivate for %q: %w", name, err)
	}

	slog.Info("collection indexed", "name", name, "total", len(files),
		"new_or_updated", indexed, "deactivated", deactivated)
	if progress != nil {
		progress(name, "done", len(files), len(files))
	}
	return nil
}

// ReindexAll reindexes all collections from the database.
func ReindexAll(d *sql.DB, progress ProgressFunc) error {
	rows, err := d.Query(`SELECT name, path, pattern, ignore_patterns FROM store_collections`)
	if err != nil {
		return fmt.Errorf("list collections: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type col struct {
		name, path, pattern, ignore string
	}
	var cols []col
	for rows.Next() {
		var c col
		var ignore sql.NullString
		if err := rows.Scan(&c.name, &c.path, &c.pattern, &ignore); err != nil {
			return fmt.Errorf("scan collection: %w", err)
		}
		c.ignore = ignore.String
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, c := range cols {
		if err := ReindexCollection(d, c.name, c.path, c.pattern, c.ignore, progress); err != nil {
			return err
		}
	}
	return nil
}
