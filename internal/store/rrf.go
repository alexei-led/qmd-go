package store

import "sort"

// RRF constants — must match TS exactly.
const (
	RRFk            = 60
	topRankBonus0   = 0.05
	topRankBonus1_2 = 0.02
)

// rankedList is a list of document IDs with a weight multiplier.
type rankedList struct {
	docIDs []int64
	weight float64
}

// rrfResult holds a document's fused score and original rank.
type rrfResult struct {
	docID int64
	score float64
	rank  int // position after RRF sort (0-based)
}

// fuseRRF merges multiple ranked lists using Reciprocal Rank Fusion.
// Formula: score = Σ (weight / (k + rank)) with top-rank bonuses.
func fuseRRF(lists []rankedList, candidateLimit int) []rrfResult {
	scores := make(map[int64]float64)

	for _, list := range lists {
		for rank, docID := range list.docIDs {
			scores[docID] += list.weight / float64(RRFk+rank)
		}
	}

	// Apply top-rank bonus: +0.05 for rank 0, +0.02 for rank 1-2 in each list.
	for _, list := range lists {
		for rank, docID := range list.docIDs {
			switch {
			case rank == 0:
				scores[docID] += topRankBonus0
			case rank <= 2: //nolint:mnd
				scores[docID] += topRankBonus1_2
			}
		}
	}

	results := make([]rrfResult, 0, len(scores))
	for docID, score := range scores {
		results = append(results, rrfResult{docID: docID, score: score})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// Assign ranks after sort.
	for i := range results {
		results[i].rank = i
	}

	if candidateLimit > 0 && len(results) > candidateLimit {
		results = results[:candidateLimit]
	}

	return results
}

// blendScores performs position-aware score blending.
// RRF rank 1-3 (0-2): rrfWeight = 0.75
// RRF rank 4-10 (3-9): rrfWeight = 0.60
// RRF rank 11+ (10+): rrfWeight = 0.40
func blendScores(rrfScore, rerankScore float64, rrfRank int) float64 {
	var w float64
	switch {
	case rrfRank <= 2: //nolint:mnd
		w = 0.75
	case rrfRank <= 9: //nolint:mnd
		w = 0.60
	default:
		w = 0.40
	}
	return w*rrfScore + (1-w)*rerankScore
}
