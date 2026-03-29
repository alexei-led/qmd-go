package store

import (
	"regexp"
	"strings"
)

// Chunking constants — must match TS exactly.
const (
	ChunkSizeTokens    = 900
	ChunkOverlapTokens = 135 // 15% of chunk size
	ChunkSizeChars     = 3600
	ChunkOverlapChars  = 540
	ChunkWindowChars   = 800

	distanceDecayFactor = 2.0
)

// breakPattern defines a markdown break point for chunking.
type breakPattern struct {
	Pattern *regexp.Regexp
	Score   int
	Name    string
}

// headingScore returns the score for a heading match, or 0 if not a heading
// at the given level (e.g. "## foo" is h2=90, but "### foo" is NOT h2).
// This replaces negative lookahead which Go regexp doesn't support.
func headingScore(text string, pos int) int {
	if pos >= len(text) || text[pos] != '\n' {
		return 0
	}
	rest := text[pos+1:]
	level := 0
	for level < len(rest) && rest[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0
	}
	// Must be followed by a space (actual heading, not just hashes)
	if level < len(rest) && rest[level] == '#' {
		return 0 // more hashes follow — not this heading level
	}
	scores := [7]int{0, 100, 90, 80, 70, 60, 50}
	return scores[level]
}

var nonHeadingPatterns = []breakPattern{
	{regexp.MustCompile("\\n```"), 80, "codeblock"},
	{regexp.MustCompile(`\n(?:---|\*{3}|___)\s*\n`), 60, "hr"},
	{regexp.MustCompile(`\n\n+`), 20, "blank"},
	{regexp.MustCompile(`\n[-*]\s`), 5, "list"},
	{regexp.MustCompile(`\n\d+\.\s`), 5, "numlist"},
	{regexp.MustCompile(`\n`), 1, "newline"},
}

// Chunk represents a piece of a document.
type Chunk struct {
	Text string
	Pos  int // character position in original document
	Seq  int // sequence number (0-based)
}

// ChunkDocument splits a document into overlapping chunks using smart break points.
// Token estimation uses ~4 chars/token.
func ChunkDocument(text string) []Chunk {
	if len(text) <= ChunkSizeChars {
		return []Chunk{{Text: text, Pos: 0, Seq: 0}}
	}

	fences := findCodeFences(text)
	var chunks []Chunk
	pos := 0
	seq := 0

	for pos < len(text) {
		end := pos + ChunkSizeChars
		if end >= len(text) {
			chunks = append(chunks, Chunk{Text: text[pos:], Pos: pos, Seq: seq})
			break
		}

		cutoff := findBestCutoff(text, pos, end, fences)
		chunks = append(chunks, Chunk{Text: text[pos:cutoff], Pos: pos, Seq: seq})
		seq++

		next := cutoff - ChunkOverlapChars
		if next <= pos {
			next = cutoff
		}
		pos = next
	}

	return chunks
}

// codeFence represents a region inside code fences that should not be split.
type codeFence struct {
	start, end int
}

// findCodeFences locates all ``` regions in the text.
// Unclosed fences extend to document end.
func findCodeFences(text string) []codeFence {
	var fences []codeFence
	re := regexp.MustCompile("(?m)^```")
	matches := re.FindAllStringIndex(text, -1)
	for i := 0; i < len(matches); i += 2 {
		start := matches[i][0]
		var end int
		if i+1 < len(matches) {
			end = matches[i+1][1]
		} else {
			end = len(text)
		}
		fences = append(fences, codeFence{start, end})
	}
	return fences
}

// insideFence returns true if pos falls inside any code fence.
func insideFence(pos int, fences []codeFence) bool {
	for _, f := range fences {
		if pos >= f.start && pos < f.end {
			return true
		}
	}
	return false
}

// findBestCutoff finds the best break point in the window around the target end.
// Uses squared distance decay to prefer breaks closer to the target.
func findBestCutoff(text string, chunkStart, targetEnd int, fences []codeFence) int {
	windowStart := max(targetEnd-ChunkWindowChars, chunkStart)
	windowEnd := min(targetEnd, len(text))

	window := text[windowStart:windowEnd]
	bestPos := targetEnd
	bestScore := float64(-1)

	// Check headings (replaces negative lookahead patterns)
	for i := range window {
		absPos := windowStart + i
		if absPos <= chunkStart || insideFence(absPos, fences) {
			continue
		}
		if score := headingScore(text, absPos); score > 0 {
			s := applyDecay(score, absPos, targetEnd)
			if s > bestScore {
				bestScore = s
				bestPos = absPos + 1 // cut after the newline
			}
		}
	}

	// Check non-heading patterns
	for _, bp := range nonHeadingPatterns {
		for _, loc := range bp.Pattern.FindAllStringIndex(window, -1) {
			absPos := windowStart + loc[0]
			if absPos <= chunkStart || insideFence(absPos, fences) {
				continue
			}

			s := applyDecay(bp.Score, absPos, targetEnd)
			if s > bestScore {
				bestScore = s
				bestPos = absPos + len(strings.TrimRight(window[loc[0]:loc[1]], " \t"))
			}
		}
	}

	return bestPos
}

func applyDecay(score, absPos, targetEnd int) float64 {
	dist := float64(targetEnd-absPos) / float64(ChunkWindowChars)
	decay := 1.0 - dist*dist*distanceDecayFactor
	if decay < 0 {
		decay = 0
	}
	return float64(score) * decay
}
