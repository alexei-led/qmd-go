package store

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/user/qmd-go/internal/db"
)

// VectorSearchOpts configures a vector similarity search.
type VectorSearchOpts struct {
	Limit      int
	MinScore   float64
	Collection string
	SearchAll  bool
}

type vecHit struct {
	hash     string
	seq      int
	distance float64
}

// VectorSearch performs a two-step vector search:
// 1. sqlite-vec MATCH to find nearest vectors
// 2. Separate JOIN for document metadata
func VectorSearch(d *sql.DB, embedding []float32, opts VectorSearchOpts) ([]SearchResult, error) {
	if !db.VecAvailable(d) {
		return nil, fmt.Errorf("vector search unavailable: sqlite-vec not loaded")
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}

	hits, err := queryVectors(d, embedding, limit*3) //nolint:mnd // over-fetch for filtering
	if err != nil {
		return nil, err
	}

	return resolveVecHits(d, hits, opts, limit)
}

func queryVectors(d *sql.DB, embedding []float32, candidateLimit int) ([]vecHit, error) {
	vecBytes := Float32ToBytes(embedding)
	rows, err := d.Query(
		`SELECT hash, seq, distance FROM vectors_vec WHERE embedding MATCH ? ORDER BY distance LIMIT ?`,
		vecBytes, candidateLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hits []vecHit
	for rows.Next() {
		var h vecHit
		if err := rows.Scan(&h.hash, &h.seq, &h.distance); err != nil {
			return nil, fmt.Errorf("scan vec hit: %w", err)
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

func resolveVecHits(d *sql.DB, hits []vecHit, opts VectorSearchOpts, limit int) ([]SearchResult, error) {
	var results []SearchResult
	for _, hit := range hits {
		score := 1.0 - hit.distance
		if score < opts.MinScore {
			continue
		}

		r, ok, err := lookupDocByHash(d, hit.hash, opts.Collection, opts.SearchAll)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		r.Score = score
		results = append(results, r)

		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

func lookupDocByHash(d *sql.DB, hash, collection string, searchAll bool) (SearchResult, bool, error) {
	q := `SELECT d.id, d.collection, d.path, d.title, d.hash
		FROM documents d WHERE d.hash = ? AND d.active = 1`
	args := []any{hash}

	if !searchAll {
		if collection != "" {
			q += ` AND d.collection = ?`
			args = append(args, collection)
		} else {
			q += ` AND d.collection IN (SELECT name FROM store_collections WHERE include_by_default = 1)`
		}
	}

	var r SearchResult
	err := d.QueryRow(q, args...).Scan(&r.DocID, &r.Collection, &r.Path, &r.Title, &r.Hash)
	if err == sql.ErrNoRows {
		return r, false, nil
	}
	if err != nil {
		return r, false, fmt.Errorf("lookup doc for hash %s: %w", hash, err)
	}
	return r, true, nil
}

const float32Bytes = 4

// Float32ToBytes converts a float32 slice to little-endian bytes for sqlite-vec.
func Float32ToBytes(v []float32) []byte {
	buf := make([]byte, len(v)*float32Bytes)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*float32Bytes:], math.Float32bits(f))
	}
	return buf
}

// BytesToFloat32 converts little-endian bytes back to a float32 slice.
func BytesToFloat32(b []byte) []float32 {
	n := len(b) / float32Bytes
	v := make([]float32, n)
	for i := range n {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*float32Bytes:]))
	}
	return v
}

// InsertVectors inserts chunk embeddings into content_vectors and vectors_vec.
func InsertVectors(d *sql.DB, hash, model string, chunks []Chunk, embeddings [][]float32, timestamp string) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM content_vectors WHERE hash = ?`, hash); err != nil {
		return fmt.Errorf("delete stale content_vectors: %w", err)
	}

	metaStmt, err := tx.Prepare(`INSERT INTO content_vectors (hash, seq, pos, model, embedded_at) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare content_vectors: %w", err)
	}
	defer func() { _ = metaStmt.Close() }()

	vecAvail := db.VecAvailable(d)
	var vecStmt *sql.Stmt
	if vecAvail {
		if _, err := tx.Exec(`DELETE FROM vectors_vec WHERE hash = ?`, hash); err != nil {
			return fmt.Errorf("delete stale vectors_vec: %w", err)
		}
		vecStmt, err = tx.Prepare(`INSERT INTO vectors_vec (hash, seq, embedding) VALUES (?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare vectors_vec: %w", err)
		}
		defer func() { _ = vecStmt.Close() }()
	}

	for i, emb := range embeddings {
		pos := 0
		if i < len(chunks) {
			pos = chunks[i].Pos
		}
		if _, err := metaStmt.Exec(hash, i, pos, model, timestamp); err != nil {
			return fmt.Errorf("insert content_vectors seq %d: %w", i, err)
		}
		if vecStmt != nil {
			if _, err := vecStmt.Exec(hash, i, Float32ToBytes(emb)); err != nil {
				return fmt.Errorf("insert vectors_vec seq %d: %w", i, err)
			}
		}
	}

	return tx.Commit()
}
