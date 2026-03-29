package store

import (
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/user/qmd-go/internal/db"
)

// GetStatus gathers index metadata: collections, document counts, embedding info.
func GetStatus(d *sql.DB, indexName, dbPath string) (StatusInfo, error) {
	info := StatusInfo{
		IndexName:    indexName,
		DBPath:       dbPath,
		VecAvailable: db.VecAvailable(d),
	}

	cols, err := queryCollections(d)
	if err != nil {
		return info, fmt.Errorf("query collections: %w", err)
	}
	info.Collections = cols

	if err := d.QueryRow(`SELECT COUNT(*) FROM documents`).Scan(&info.TotalDocuments); err != nil {
		return info, fmt.Errorf("count documents: %w", err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM documents WHERE active = 1`).Scan(&info.ActiveDocuments); err != nil {
		return info, fmt.Errorf("count active documents: %w", err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM content_vectors`).Scan(&info.EmbeddedChunks); err != nil {
		return info, fmt.Errorf("count vectors: %w", err)
	}

	var model sql.NullString
	_ = d.QueryRow(`SELECT model FROM content_vectors LIMIT 1`).Scan(&model)
	if model.Valid {
		info.EmbedModel = model.String
	}

	dim := detectEmbedDimension(d)
	if dim > 0 {
		info.EmbedDimension = dim
	}

	return info, nil
}

func queryCollections(d *sql.DB) ([]CollectionInfo, error) {
	rows, err := d.Query(`SELECT name, path, pattern, COALESCE(ignore_patterns, ''),
		COALESCE(include_by_default, 1), COALESCE(update_command, ''), COALESCE(context, '')
		FROM store_collections ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var cols []CollectionInfo
	for rows.Next() {
		var c CollectionInfo
		if err := rows.Scan(&c.Name, &c.Path, &c.Pattern, &c.IgnorePatterns,
			&c.IncludeByDefault, &c.UpdateCommand, &c.Context); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// CleanupOpts controls which cleanup operations to perform.
type CleanupOpts struct {
	SkipInactive        bool
	SkipOrphanedContent bool
	SkipOrphanedVectors bool
	SkipLLMCache        bool
}

// CleanupResult reports what was cleaned up.
type CleanupResult struct {
	InactiveRemoved int
	ContentRemoved  int
	VectorsRemoved  int
	CacheCleared    int
	Vacuumed        bool
}

// Cleanup removes orphaned data and vacuums the database.
func Cleanup(d *sql.DB, opts CleanupOpts) (CleanupResult, error) {
	var result CleanupResult

	if !opts.SkipInactive {
		res, err := d.Exec(`DELETE FROM documents WHERE active = 0`)
		if err != nil {
			return result, fmt.Errorf("delete inactive documents: %w", err)
		}
		n, _ := res.RowsAffected()
		result.InactiveRemoved = int(n)
	}

	if !opts.SkipOrphanedContent {
		res, err := d.Exec(`DELETE FROM content WHERE hash NOT IN (SELECT DISTINCT hash FROM documents)`)
		if err != nil {
			return result, fmt.Errorf("delete orphaned content: %w", err)
		}
		n, _ := res.RowsAffected()
		result.ContentRemoved = int(n)
	}

	if !opts.SkipOrphanedVectors {
		res, err := d.Exec(`DELETE FROM content_vectors WHERE hash NOT IN (SELECT DISTINCT hash FROM documents WHERE active = 1)`)
		if err != nil {
			return result, fmt.Errorf("delete orphaned vectors: %w", err)
		}
		n, _ := res.RowsAffected()
		result.VectorsRemoved = int(n)

		// Also clean up the virtual table to keep it in sync.
		if _, err := d.Exec(`DELETE FROM vectors_vec WHERE hash NOT IN (SELECT DISTINCT hash FROM documents WHERE active = 1)`); err != nil {
			slog.Debug("cleanup vectors_vec (may not exist)", "error", err)
		}
	}

	if !opts.SkipLLMCache {
		res, err := d.Exec(`DELETE FROM llm_cache`)
		if err != nil {
			return result, fmt.Errorf("clear llm cache: %w", err)
		}
		n, _ := res.RowsAffected()
		result.CacheCleared = int(n)
	}

	if _, err := d.Exec(`VACUUM`); err != nil {
		return result, fmt.Errorf("vacuum: %w", err)
	}
	result.Vacuumed = true

	return result, nil
}
