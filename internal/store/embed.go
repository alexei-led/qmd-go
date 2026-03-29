package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/user/qmd-go/internal/provider"
)

// Embedding batch constants — must match TS.
const (
	DefaultEmbedMaxDocsPerBatch = 64
	DefaultEmbedMaxBatchBytes   = 64 * 1024 * 1024 // 64MB
	embeddingSentinel           = "embedding..."
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
// Uses SQL-level batch claiming for concurrent coordination.
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
		chunks := ChunkDocument(item.content)
		texts := make([]string, len(chunks))
		for j, c := range chunks {
			texts[j] = c.Text
		}

		embeddings, err := embedder.Embed(ctx, texts, provider.EmbedOpts{})
		if err != nil {
			_ = unclaimBatch(d, []string{item.hash})
			return fmt.Errorf("embed hash %s: %w", item.hash, err)
		}

		if err := InsertVectors(d, item.hash, model, chunks, embeddings, now); err != nil {
			return fmt.Errorf("insert vectors for %s: %w", item.hash, err)
		}

		if progress != nil {
			progress(EmbedProgress{Phase: "embedding", Current: i + 1, Total: len(batch)})
		}
	}

	return nil
}

type embedItem struct {
	hash    string
	content string
}

// claimBatch atomically claims a batch of un-embedded content hashes.
func claimBatch(d *sql.DB, limit int, reembed bool) ([]embedItem, error) {
	var q string
	if reembed {
		q = `SELECT c.hash, c.doc FROM content c
			INNER JOIN documents d ON d.hash = c.hash AND d.active = 1
			GROUP BY c.hash LIMIT ?`
	} else {
		q = `SELECT c.hash, c.doc FROM content c
			INNER JOIN documents d ON d.hash = c.hash AND d.active = 1
			WHERE c.hash NOT IN (SELECT hash FROM content_vectors)
			GROUP BY c.hash LIMIT ?`
	}

	rows, err := d.Query(q, limit)
	if err != nil {
		return nil, fmt.Errorf("claim batch: %w", err)
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

func clearEmbeddings(d *sql.DB) error {
	if _, err := d.Exec(`DELETE FROM content_vectors`); err != nil {
		return fmt.Errorf("clear content_vectors: %w", err)
	}
	if _, err := d.Exec(`DELETE FROM vectors_vec`); err != nil {
		slog.Debug("clear vectors_vec (may not exist)", "error", err)
	}
	return nil
}

func unclaimBatch(d *sql.DB, hashes []string) error {
	for _, h := range hashes {
		if _, err := d.Exec(`DELETE FROM content_vectors WHERE hash = ? AND embedded_at = ?`, h, embeddingSentinel); err != nil {
			return fmt.Errorf("unclaim %s: %w", h, err)
		}
	}
	return nil
}
