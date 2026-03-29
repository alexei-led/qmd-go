package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/user/qmd-go/internal/provider"
)

// Embedding batch constants — must match TS.
const (
	DefaultEmbedMaxDocsPerBatch = 64
	DefaultEmbedMaxBatchBytes   = 64 * 1024 * 1024 // 64MB
)

// EmbedOpts configures an embedding run.
type EmbedOpts struct {
	Clear         bool
	NoIncremental bool
	MaxDocs       int
	MaxBytes      int
}

// EmbedProgress reports embedding progress.
type EmbedProgress struct {
	Phase   string
	Current int
	Total   int
}

// EmbedDocuments chunks and embeds all un-embedded content using the given embedder.
func EmbedDocuments(ctx context.Context, d *sql.DB, embedder provider.Embedder, opts EmbedOpts, progress func(EmbedProgress)) error {
	if opts.Clear {
		if err := clearEmbeddings(d); err != nil {
			return err
		}
	}

	maxDocs := opts.MaxDocs
	if maxDocs <= 0 {
		maxDocs = DefaultEmbedMaxDocsPerBatch
	}
	batch, err := claimBatch(d, maxDocs, opts.NoIncremental)
	if err != nil {
		return err
	}
	if len(batch) == 0 {
		slog.Info("no documents to embed")
		return nil
	}

	slog.Info("embedding batch", "docs", len(batch))
	if progress != nil {
		progress(EmbedProgress{Phase: "embedding", Total: len(batch)})
	}

	model := "default"
	if embedder.Dimensions() > 0 {
		model = fmt.Sprintf("%dd", embedder.Dimensions())
	}
	now := time.Now().UTC().Format(time.RFC3339)

	for i, item := range batch {
		if err := embedOne(ctx, d, embedder, item, model, now); err != nil {
			cleanupClaims(d, batch)
			return err
		}
		// Refresh claim timestamps for remaining unprocessed items so that
		// cleanupStaleClaims (30-min threshold) never reclaims hashes that
		// belong to a still-active batch with a slow embedder.
		if remaining := batch[i+1:]; len(remaining) > 0 {
			refreshClaimTimestamps(d, remaining)
		}
		if progress != nil {
			progress(EmbedProgress{Phase: "embedding", Current: i + 1, Total: len(batch)})
		}
	}

	return nil
}

// claimStalenessThreshold is how long a claiming sentinel must exist before
// it is considered stale (from a crashed/killed process). Set to 30 minutes
// so that large batches with slow remote embedders are never mistaken for stale.
const claimStalenessThreshold = 30 * time.Minute

// cleanupStaleClaims removes only old claiming sentinels left by crashed processes.
// Live claims from concurrent workers use a recent timestamp and are preserved.
func cleanupStaleClaims(d *sql.DB) {
	cutoff := time.Now().UTC().Add(-claimStalenessThreshold).Format(time.RFC3339)
	res, err := d.Exec(`DELETE FROM content_vectors WHERE model = 'claiming' AND embedded_at < ?`, cutoff)
	if err != nil {
		slog.Warn("failed to clean up stale claims", "error", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		slog.Info("cleaned up stale claiming sentinels", "count", n)
	}
}

// cleanupClaims removes claiming sentinels for the given batch only,
// leaving other workers' claims intact.
func cleanupClaims(d *sql.DB, batch []embedItem) {
	if len(batch) == 0 {
		return
	}
	placeholders := strings.Repeat("?,", len(batch))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(batch))
	for i, item := range batch {
		args[i] = item.hash
	}
	if _, err := d.Exec(`DELETE FROM content_vectors WHERE model = 'claiming' AND hash IN (`+placeholders+`)`, args...); err != nil {
		slog.Warn("failed to clean up claiming rows", "error", err)
	}
}

func embedOne(ctx context.Context, d *sql.DB, embedder provider.Embedder, item embedItem, model, timestamp string) error {
	chunks := ChunkDocument(item.content)
	texts := make([]string, len(chunks))
	for j, c := range chunks {
		texts[j] = c.Text
	}
	embeddings, err := embedder.Embed(ctx, texts, provider.EmbedOpts{})
	if err != nil {
		return fmt.Errorf("embed hash %s: %w", item.hash, err)
	}
	if err := InsertVectors(d, item.hash, model, chunks, embeddings, timestamp); err != nil {
		return fmt.Errorf("insert vectors for %s: %w", item.hash, err)
	}
	return nil
}

// refreshClaimTimestamps updates the claim timestamp for unprocessed items
// so they are not mistaken for stale claims by concurrent processes.
func refreshClaimTimestamps(d *sql.DB, items []embedItem) {
	if len(items) == 0 {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	placeholders := strings.Repeat("?,", len(items))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(items)+1)
	args = append(args, now)
	for _, item := range items {
		args = append(args, item.hash)
	}
	if _, err := d.Exec(`UPDATE content_vectors SET embedded_at = ? WHERE model = 'claiming' AND hash IN (`+placeholders+`)`, args...); err != nil {
		slog.Warn("failed to refresh claim timestamps", "error", err)
	}
}

type embedItem struct {
	hash    string
	content string
}

// claimBatch atomically claims a batch of un-embedded content hashes.
// Uses INSERT with a sentinel embedded_at value to prevent concurrent
// processes from claiming the same hashes.
func claimBatch(d *sql.DB, limit int, reembed bool) ([]embedItem, error) {
	if reembed {
		return claimBatchReembed(d, limit)
	}

	// Clean up stale claiming sentinels left by crashed/killed processes.
	cleanupStaleClaims(d)

	tx, err := d.Begin()
	if err != nil {
		return nil, fmt.Errorf("claim batch begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Find unclaimed hashes and insert sentinel rows atomically.
	// embedded_at stores a real timestamp so concurrent workers' live claims
	// can be distinguished from stale ones left by crashed processes.
	claimTime := time.Now().UTC().Format(time.RFC3339)
	rows, err := tx.Query(`
		INSERT INTO content_vectors (hash, seq, pos, model, embedded_at)
		SELECT c.hash, 0, 0, 'claiming', ?
		FROM content c
		INNER JOIN documents d ON d.hash = c.hash AND d.active = 1
		WHERE c.hash NOT IN (SELECT hash FROM content_vectors)
		GROUP BY c.hash LIMIT ?
		RETURNING hash`, claimTime, limit)
	if err != nil {
		return nil, fmt.Errorf("claim batch: %w", err)
	}

	defer func() { _ = rows.Close() }()

	var hashes []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("scan claimed hash: %w", err)
		}
		hashes = append(hashes, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claimed rows: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("claim batch commit: %w", err)
	}

	// Fetch content for claimed hashes.
	return fetchContentForHashes(d, hashes)
}

// claimBatchReembed returns all active hashes for re-embedding (no sentinel needed).
func claimBatchReembed(d *sql.DB, limit int) ([]embedItem, error) {
	rows, err := d.Query(`SELECT c.hash, c.doc FROM content c
		INNER JOIN documents d ON d.hash = c.hash AND d.active = 1
		GROUP BY c.hash LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("claim batch reembed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var items []embedItem
	for rows.Next() {
		var it embedItem
		if err := rows.Scan(&it.hash, &it.content); err != nil {
			return nil, fmt.Errorf("scan batch item: %w", err)
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// fetchContentForHashes loads content for a set of claimed hashes.
func fetchContentForHashes(d *sql.DB, hashes []string) ([]embedItem, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(hashes))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(hashes))
	for i, h := range hashes {
		args[i] = h
	}
	rows, err := d.Query(fmt.Sprintf(`SELECT hash, doc FROM content WHERE hash IN (%s)`, placeholders), args...)
	if err != nil {
		return nil, fmt.Errorf("fetch content: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var items []embedItem
	for rows.Next() {
		var it embedItem
		if err := rows.Scan(&it.hash, &it.content); err != nil {
			return nil, fmt.Errorf("scan content: %w", err)
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

func clearEmbeddings(d *sql.DB) error {
	if _, err := d.Exec(`DELETE FROM content_vectors`); err != nil {
		return fmt.Errorf("clear content_vectors: %w", err)
	}
	if _, err := d.Exec(`DELETE FROM vectors_vec`); err != nil {
		slog.Debug("clear vectors_vec (may not exist)", "error", err)
	}
	return nil
}
