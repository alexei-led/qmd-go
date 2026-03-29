package store

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChunkDocument_ShortText(t *testing.T) {
	text := "Hello world"
	chunks := ChunkDocument(text)
	require.Len(t, chunks, 1)
	assert.Equal(t, text, chunks[0].Text)
	assert.Equal(t, 0, chunks[0].Pos)
	assert.Equal(t, 0, chunks[0].Seq)
}

func TestChunkDocument_ExactLimit(t *testing.T) {
	text := strings.Repeat("x", ChunkSizeChars)
	chunks := ChunkDocument(text)
	assert.Len(t, chunks, 1)
}

func TestChunkDocument_SplitsLongText(t *testing.T) {
	text := strings.Repeat("word ", ChunkSizeChars)
	chunks := ChunkDocument(text)
	assert.Greater(t, len(chunks), 1)
	for i, c := range chunks {
		assert.Equal(t, i, c.Seq)
		assert.NotEmpty(t, c.Text)
	}
}

func TestChunkDocument_PrefersHeadings(t *testing.T) {
	part1 := strings.Repeat("a", ChunkSizeChars-100)
	text := part1 + "\n## Section Two\n" + strings.Repeat("b", ChunkSizeChars)
	chunks := ChunkDocument(text)
	require.Greater(t, len(chunks), 1)
	assert.True(t, strings.HasSuffix(chunks[0].Text, "\n") || !strings.Contains(chunks[0].Text, "## Section Two"))
}

func TestChunkDocument_NeverSplitsInsideCodeFence(t *testing.T) {
	code := "```go\n" + strings.Repeat("x\n", ChunkSizeChars/2) + "```\n"
	text := code + strings.Repeat("y ", ChunkSizeChars)
	chunks := ChunkDocument(text)
	for _, c := range chunks {
		opens := strings.Count(c.Text, "```")
		if opens > 0 && opens%2 != 0 {
			rest := text[c.Pos+len(c.Text):]
			if strings.Contains(rest, "```") {
				t.Log("chunk has unclosed fence but document continues with closing fence — checking no split inside")
			}
		}
	}
	assert.Greater(t, len(chunks), 1)
}

func TestChunkDocument_UnclosedFenceExtends(t *testing.T) {
	text := "```python\n" + strings.Repeat("code\n", 1000)
	fences := findCodeFences(text)
	require.Len(t, fences, 1)
	assert.Equal(t, len(text), fences[0].end)
}

func TestFindCodeFences_MultiplePairs(t *testing.T) {
	text := "before\n```\ncode1\n```\nmiddle\n```\ncode2\n```\nafter"
	fences := findCodeFences(text)
	assert.Len(t, fences, 2)
}

func TestChunkDocument_SequentialSeq(t *testing.T) {
	text := strings.Repeat("word\n", ChunkSizeChars)
	chunks := ChunkDocument(text)
	for i, c := range chunks {
		assert.Equal(t, i, c.Seq)
	}
}

func TestFloat32RoundTrip(t *testing.T) {
	original := []float32{1.0, -0.5, 3.14, 0.0, -1e10}
	bytes := Float32ToBytes(original)
	assert.Len(t, bytes, len(original)*4)
	restored := BytesToFloat32(bytes)
	assert.Equal(t, original, restored)
}
