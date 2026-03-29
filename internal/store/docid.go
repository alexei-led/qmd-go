package store

import (
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// DocidMatch is the result of resolving a short hash (docid) to a document.
type DocidMatch struct {
	Filepath string // qmd://collection/path
	Hash     string
}

// RetrievedDoc is a fully resolved document for get/multi-get.
type RetrievedDoc struct {
	Filepath       string `json:"file"`
	DisplayPath    string `json:"displayPath"`
	Title          string `json:"title"`
	Context        string `json:"context,omitempty"`
	Hash           string `json:"hash"`
	DocID          string `json:"docid"`
	CollectionName string `json:"collection"`
	ModifiedAt     string `json:"modifiedAt"`
	BodyLength     int    `json:"bodyLength"`
	Body           string `json:"body,omitempty"`
}

// DocumentNotFound is returned when a document lookup fails.
type DocumentNotFound struct {
	Error        string   `json:"error"`
	Query        string   `json:"query"`
	SimilarFiles []string `json:"similarFiles,omitempty"`
}

// MultiGetResult is one entry from a multi-get operation.
type MultiGetResult struct {
	Doc        *RetrievedDoc `json:"doc,omitempty"`
	Filepath   string        `json:"file"`
	Skipped    bool          `json:"skipped,omitempty"`
	SkipReason string        `json:"reason,omitempty"`
}

// FindDocumentOpts controls FindDocument behavior.
type FindDocumentOpts struct {
	IncludeBody bool
}

// LsEntry represents one line in ls output.
type LsEntry struct {
	Path         string
	Size         int
	ModifiedAt   string
	FileCount    int
	IsCollection bool
}

const bytesPerKB = 1024

var hexRe = regexp.MustCompile(`(?i)^[a-f0-9]+$`)

// GetDocid returns the first 6 characters of a content hash.
func GetDocid(hash string) string {
	if len(hash) < 6 { //nolint:mnd
		return hash
	}
	return hash[:6]
}

// NormalizeDocid strips quotes and leading # from a docid string.
func NormalizeDocid(docid string) string {
	s := strings.Trim(docid, `"'`)
	s = strings.TrimPrefix(s, "#")
	return s
}

// IsDocid returns true if the input looks like a docid (hex string, 6+ chars).
func IsDocid(input string) bool {
	s := NormalizeDocid(input)
	return len(s) >= 6 && hexRe.MatchString(s) //nolint:mnd
}

// FindDocumentByDocid resolves a short hash prefix to a document.
func FindDocumentByDocid(d *sql.DB, docid string) (*DocidMatch, error) {
	shortHash := NormalizeDocid(docid)
	if len(shortHash) < 1 {
		return nil, nil
	}

	var match DocidMatch
	err := d.QueryRow(
		`SELECT 'qmd://' || d.collection || '/' || d.path, d.hash
		 FROM documents d WHERE d.hash LIKE ? AND d.active = 1 LIMIT 1`,
		shortHash+"%",
	).Scan(&match.Filepath, &match.Hash)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find by docid: %w", err)
	}
	return &match, nil
}

// ParseLineSpec splits "file:123" into the file path and optional line number.
func ParseLineSpec(input string) (path string, line int) {
	idx := strings.LastIndex(input, ":")
	if idx < 0 {
		return input, 0
	}
	suffix := input[idx+1:]
	n, err := strconv.Atoi(suffix)
	if err != nil || n < 1 {
		return input, 0
	}
	return input[:idx], n
}

// FindDocument resolves a file reference to a document.
// Resolution order: docid, qmd:// virtual path, suffix match, collection/path.
func FindDocument(d *sql.DB, filename string, opts FindDocumentOpts) (*RetrievedDoc, *DocumentNotFound, error) {
	fp, _ := ParseLineSpec(filename)
	fp, err := resolveDocidAndTilde(d, fp, filename)
	if err != nil {
		return nil, nil, err
	}
	if fp == "" {
		return nil, &DocumentNotFound{Error: "not_found", Query: filename}, nil
	}

	finder := docFinder{db: d, opts: opts}
	doc, err := finder.resolve(fp)
	if err != nil {
		return nil, nil, err
	}
	if doc != nil {
		doc.Context = getContextForDoc(d, doc.CollectionName)
		return doc, nil, nil
	}

	similar, _ := FindSimilarFiles(d, fp, 5, 5) //nolint:mnd
	return nil, &DocumentNotFound{Error: "not_found", Query: filename, SimilarFiles: similar}, nil
}

// resolveDocidAndTilde handles docid lookup and ~ expansion.
// Returns "" if docid lookup fails (caller should return not-found).
func resolveDocidAndTilde(d *sql.DB, fp, _ string) (string, error) {
	if IsDocid(fp) {
		match, err := FindDocumentByDocid(d, fp)
		if err != nil {
			return "", err
		}
		if match == nil {
			return "", nil
		}
		return match.Filepath, nil
	}
	if strings.HasPrefix(fp, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + fp[1:], nil
		}
	}
	return fp, nil
}

// docFinder encapsulates the query-building logic for document lookup.
type docFinder struct {
	db   *sql.DB
	opts FindDocumentOpts
}

func (f *docFinder) resolve(fp string) (*RetrievedDoc, error) {
	if strings.HasPrefix(fp, "qmd://") {
		vp := fp[6:]
		if doc, err := f.queryOne(`d.collection || '/' || d.path = ?`, vp); doc != nil || err != nil {
			return doc, err
		}
	}

	if doc, err := f.queryOne(`'qmd://' || d.collection || '/' || d.path LIKE ?`, "%"+fp); doc != nil || err != nil {
		return doc, err
	}

	return f.resolveViaCollections(fp)
}

func (f *docFinder) resolveViaCollections(fp string) (*RetrievedDoc, error) {
	collections, err := listStoreCollections(f.db)
	if err != nil {
		return nil, err
	}
	for _, coll := range collections {
		var relPath string
		if strings.HasPrefix(fp, coll.path+"/") {
			relPath = fp[len(coll.path)+1:]
		} else if !strings.HasPrefix(fp, "/") {
			relPath = fp
		} else {
			continue
		}
		doc, err := f.queryOne(`d.collection = ? AND d.path = ?`, coll.name, relPath)
		if doc != nil || err != nil {
			return doc, err
		}
	}
	return nil, nil
}

func (f *docFinder) queryOne(where string, args ...any) (*RetrievedDoc, error) {
	cols := `'qmd://' || d.collection || '/' || d.path,
		d.collection || '/' || d.path,
		d.title, d.hash, d.collection, d.modified_at,
		LENGTH(content.doc)`
	if f.opts.IncludeBody {
		cols += `, content.doc`
	}
	q := fmt.Sprintf(`SELECT %s FROM documents d JOIN content ON content.hash = d.hash WHERE d.active = 1 AND %s LIMIT 1`, cols, where)

	doc := &RetrievedDoc{}
	var body sql.NullString
	var scanDst []any
	if f.opts.IncludeBody {
		scanDst = []any{&doc.Filepath, &doc.DisplayPath, &doc.Title, &doc.Hash, &doc.CollectionName, &doc.ModifiedAt, &doc.BodyLength, &body}
	} else {
		scanDst = []any{&doc.Filepath, &doc.DisplayPath, &doc.Title, &doc.Hash, &doc.CollectionName, &doc.ModifiedAt, &doc.BodyLength}
	}

	err := f.db.QueryRow(q, args...).Scan(scanDst...)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find document: %w", err)
	}
	doc.DocID = GetDocid(doc.Hash)
	if f.opts.IncludeBody && body.Valid {
		doc.Body = body.String
	}
	return doc, nil
}

// GetDocumentBody retrieves the body of a document, optionally slicing by line range.
func GetDocumentBody(d *sql.DB, filepath string, fromLine, maxLines int) (string, error) {
	var body string
	baseQ := `SELECT content.doc FROM documents d JOIN content ON content.hash = d.hash WHERE d.active = 1`

	if strings.HasPrefix(filepath, "qmd://") {
		vp := filepath[6:]
		err := d.QueryRow(baseQ+` AND d.collection || '/' || d.path = ?`, vp).Scan(&body)
		if err != nil {
			return "", fmt.Errorf("get body: %w", err)
		}
	} else {
		collections, err := listStoreCollections(d)
		if err != nil {
			return "", err
		}
		found := false
		for _, coll := range collections {
			if strings.HasPrefix(filepath, coll.path+"/") {
				relPath := filepath[len(coll.path)+1:]
				err = d.QueryRow(baseQ+` AND d.collection = ? AND d.path = ?`, coll.name, relPath).Scan(&body)
				if err == nil {
					found = true
					break
				}
			}
		}
		if !found {
			return "", fmt.Errorf("document not found: %s", filepath)
		}
	}

	return sliceLines(body, fromLine, maxLines), nil
}

func sliceLines(body string, fromLine, maxLines int) string {
	if fromLine <= 0 && maxLines <= 0 {
		return body
	}
	lines := strings.Split(body, "\n")
	start := 0
	if fromLine > 0 {
		start = fromLine - 1
	}
	if start > len(lines) {
		return ""
	}
	end := len(lines)
	if maxLines > 0 && start+maxLines < end {
		end = start + maxLines
	}
	return strings.Join(lines[start:end], "\n")
}

// FindDocuments resolves a multi-get pattern (glob or comma-separated) to documents.
func FindDocuments(d *sql.DB, pattern string, maxBytes int) ([]MultiGetResult, []string, error) {
	isComma := strings.Contains(pattern, ",") && !strings.Contains(pattern, "*") && !strings.Contains(pattern, "?")
	if isComma {
		return findDocumentsComma(d, pattern, maxBytes)
	}
	return findDocumentsGlob(d, pattern, maxBytes)
}

func findDocumentsComma(d *sql.DB, pattern string, maxBytes int) ([]MultiGetResult, []string, error) {
	names := strings.Split(pattern, ",")
	var results []MultiGetResult
	var errs []string

	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		doc, notFound, err := FindDocument(d, name, FindDocumentOpts{IncludeBody: true})
		if err != nil {
			return nil, nil, err
		}
		if notFound != nil {
			msg := fmt.Sprintf("File not found: %s", name)
			if len(notFound.SimilarFiles) > 0 {
				msg += fmt.Sprintf(" (did you mean: %s?)", strings.Join(notFound.SimilarFiles, ", "))
			}
			errs = append(errs, msg)
			continue
		}

		results = append(results, toMultiGetResult(doc, maxBytes))
	}

	return results, errs, nil
}

func findDocumentsGlob(d *sql.DB, pattern string, maxBytes int) ([]MultiGetResult, []string, error) {
	matches, err := MatchFilesByGlob(d, pattern)
	if err != nil {
		return nil, nil, err
	}
	if len(matches) == 0 {
		return nil, []string{fmt.Sprintf("No files matched pattern: %s", pattern)}, nil
	}

	var results []MultiGetResult
	for _, m := range matches {
		doc, _, err := FindDocument(d, m.Filepath, FindDocumentOpts{IncludeBody: true})
		if err != nil {
			return nil, nil, err
		}
		if doc == nil {
			continue
		}
		results = append(results, toMultiGetResult(doc, maxBytes))
	}

	return results, nil, nil
}

func toMultiGetResult(doc *RetrievedDoc, maxBytes int) MultiGetResult {
	if maxBytes > 0 && doc.BodyLength > maxBytes {
		return MultiGetResult{
			Filepath:   doc.Filepath,
			Skipped:    true,
			SkipReason: fmt.Sprintf("File too large (%dKB > %dKB)", doc.BodyLength/bytesPerKB, maxBytes/bytesPerKB),
		}
	}
	return MultiGetResult{Doc: doc, Filepath: doc.Filepath}
}

// ListDocuments returns documents for the ls command.
func ListDocuments(d *sql.DB, path string) ([]LsEntry, error) {
	if path == "" {
		return listCollections(d)
	}

	path = strings.TrimPrefix(path, "qmd://")
	path = strings.TrimSuffix(path, "/")

	vp := ParseVirtualPath(path)
	collection := vp.Collection
	subPath := vp.Path
	if collection == "" {
		collection = path
		subPath = ""
	}

	q := `SELECT 'qmd://' || d.collection || '/' || d.path,
		LENGTH(content.doc), d.modified_at
		FROM documents d JOIN content ON content.hash = d.hash
		WHERE d.active = 1 AND d.collection = ?`
	args := []any{collection}

	if subPath != "" {
		q += ` AND d.path LIKE ?`
		args = append(args, subPath+"%")
	}
	q += ` ORDER BY d.path`

	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []LsEntry
	for rows.Next() {
		var e LsEntry
		if err := rows.Scan(&e.Path, &e.Size, &e.ModifiedAt); err != nil {
			return nil, fmt.Errorf("scan ls entry: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func listCollections(d *sql.DB) ([]LsEntry, error) {
	rows, err := d.Query(`SELECT sc.name, COUNT(d.id)
		FROM store_collections sc
		LEFT JOIN documents d ON d.collection = sc.name AND d.active = 1
		GROUP BY sc.name ORDER BY sc.name`)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []LsEntry
	for rows.Next() {
		var e LsEntry
		var count int
		if err := rows.Scan(&e.Path, &count); err != nil {
			return nil, fmt.Errorf("scan collection: %w", err)
		}
		e.Path = fmt.Sprintf("qmd://%s/", e.Path)
		e.FileCount = count
		e.IsCollection = true
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// AddLineNumbers prepends "N: " to each line.
func AddLineNumbers(text string, startLine int) string {
	lines := strings.Split(text, "\n")
	var b strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&b, "%d: %s", startLine+i, line)
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

type storeCollection struct {
	name, path string
}

func listStoreCollections(d *sql.DB) ([]storeCollection, error) {
	rows, err := d.Query(`SELECT name, path FROM store_collections`)
	if err != nil {
		return nil, fmt.Errorf("list store collections: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var cols []storeCollection
	for rows.Next() {
		var c storeCollection
		if err := rows.Scan(&c.name, &c.path); err != nil {
			return nil, fmt.Errorf("scan store collection: %w", err)
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// getContextForDoc retrieves collection-level context from the database.
func getContextForDoc(d *sql.DB, collection string) string {
	var ctx sql.NullString
	_ = d.QueryRow(`SELECT context FROM store_collections WHERE name = ?`, collection).Scan(&ctx)
	if ctx.Valid && ctx.String != "" {
		return ctx.String
	}
	return ""
}
