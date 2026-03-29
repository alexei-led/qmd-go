package format

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/qmd-go/internal/store"
)

func testResults() []store.SearchResult {
	return []store.SearchResult{
		{
			Collection: "notes",
			Path:       "quantum.md",
			Title:      "Quantum Guide",
			Score:      0.9234,
			Snippet:    "@@ -2,3 @@ (1 before, 1 after)\ncontext\nquantum mechanics\nmore context",
			Hash:       "abc123",
			DocID:      1,
			LineStart:  2,
			LineEnd:    4,
		},
		{
			Collection: "notes",
			Path:       "physics.md",
			Title:      "Physics 101",
			Score:      0.8100,
			Snippet:    "intro to physics",
			Hash:       "def456",
			DocID:      2,
		},
	}
}

var formatCases = []struct {
	name   string
	format Format
	check  func(t *testing.T, out string)
}{
	{
		"JSON", JSON, func(t *testing.T, out string) {
			var parsed []store.SearchResult
			require.NoError(t, json.Unmarshal([]byte(out), &parsed))
			assert.Len(t, parsed, 2)
			assert.Equal(t, "Quantum Guide", parsed[0].Title)
			assert.InDelta(t, 0.9234, parsed[0].Score, 0.0001)
		},
	},
	{
		"CSV", CSV, func(t *testing.T, out string) {
			assert.Contains(t, out, "score,collection,path,title,snippet")
			assert.Contains(t, out, "0.9234")
			assert.Contains(t, out, "quantum.md")
		},
	},
	{
		"XML", XML, func(t *testing.T, out string) {
			assert.True(t, strings.HasPrefix(out, "<?xml"))
			type xmlResults struct {
				XMLName xml.Name `xml:"results"`
				Count   int      `xml:"count,attr"`
			}
			var parsed xmlResults
			require.NoError(t, xml.Unmarshal([]byte(out), &parsed))
			assert.Equal(t, 2, parsed.Count)
		},
	},
	{
		"Markdown", Markdown, func(t *testing.T, out string) {
			assert.Contains(t, out, "### [0.9234]")
			assert.Contains(t, out, "**Quantum Guide**")
			assert.Contains(t, out, "---")
		},
	},
	{
		"Files", Files, func(t *testing.T, out string) {
			lines := strings.Split(strings.TrimSpace(out), "\n")
			assert.Len(t, lines, 2)
			assert.Equal(t, "notes/quantum.md", lines[0])
			assert.Equal(t, "notes/physics.md", lines[1])
		},
	},
	{
		"Default", Default, func(t *testing.T, out string) {
			assert.Contains(t, out, "[0.9234]")
			assert.Contains(t, out, "notes/quantum.md")
			assert.Contains(t, out, "Quantum Guide")
		},
	},
}

func TestFormatResults(t *testing.T) {
	SetColor(false)
	defer SetColor(false)

	for _, tt := range formatCases {
		t.Run(tt.name, func(t *testing.T) {
			out := Results(testResults(), tt.format, Opts{})
			tt.check(t, out)
		})
	}
}

func TestFormatDefault_WithLineNumbers(t *testing.T) {
	SetColor(false)
	defer SetColor(false)

	out := Results(testResults(), Default, Opts{LineNumbers: true})
	assert.Contains(t, out, "2|")
}

func TestColorDisabledByEnv(t *testing.T) {
	tests := []struct {
		name string
		env  string
	}{
		{"NO_COLOR", "NO_COLOR"},
		{"QMD_NO_COLOR", "QMD_NO_COLOR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.env, "1")
			assert.False(t, detectColor())
		})
	}
}

func TestFormatResults_WithContext(t *testing.T) {
	SetColor(false)
	results := []store.SearchResult{{
		Collection: "notes", Path: "ctx.md", Title: "Context Test",
		Score: 0.95, Snippet: "snippet", Hash: "aaa111", DocID: 1,
		Context: "This is context info",
	}}

	t.Run("JSON includes context", func(t *testing.T) {
		out := Results(results, JSON, Opts{})
		assert.Contains(t, out, `"context"`)
		assert.Contains(t, out, "This is context info")
	})

	t.Run("JSON omits empty context", func(t *testing.T) {
		noCtx := []store.SearchResult{{
			Collection: "notes", Path: "noctx.md", Title: "No Ctx",
			Score: 0.85, Snippet: "text", Hash: "bbb222", DocID: 2,
		}}
		out := Results(noCtx, JSON, Opts{})
		assert.NotContains(t, out, `"context"`)
	})
}

func TestMultiGetResults_JSON(t *testing.T) {
	results := []store.MultiGetResult{
		{
			Doc: &store.RetrievedDoc{
				Filepath: "qmd://notes/found.md", DisplayPath: "notes/found.md",
				Title: "Found", Hash: "aaa111bbb222", DocID: "aaa111",
				CollectionName: "notes", Body: "found body",
			},
			Filepath: "qmd://notes/found.md",
		},
		{Filepath: "qmd://notes/big.md", Skipped: true, SkipReason: "too large"},
	}

	out := MultiGetResults(results, JSON)
	assert.Contains(t, out, "Found")
	assert.Contains(t, out, "too large")
}

func TestMultiGetResults_Default(t *testing.T) {
	SetColor(false)
	results := []store.MultiGetResult{
		{
			Doc: &store.RetrievedDoc{
				Filepath: "qmd://notes/found.md", DisplayPath: "notes/found.md",
				Title: "Found", Hash: "aaa111bbb222", DocID: "aaa111",
				CollectionName: "notes", Body: "found body content",
			},
			Filepath: "qmd://notes/found.md",
		},
		{Filepath: "qmd://notes/big.md", Skipped: true, SkipReason: "File too large"},
	}

	out := MultiGetResults(results, Default)
	assert.Contains(t, out, "found.md")
	assert.Contains(t, out, "found body content")
	assert.Contains(t, out, "skipped")
}

func TestLsResults_JSON(t *testing.T) {
	entries := []store.LsEntry{
		{Path: "qmd://notes/", FileCount: 5, IsCollection: true},
		{Path: "qmd://notes/readme.md", Size: 1024, ModifiedAt: "2024-01-01"},
	}
	out := LsResults(entries, JSON)
	assert.Contains(t, out, "qmd://notes/")
	assert.Contains(t, out, "readme.md")
}

func TestLsResults_Default(t *testing.T) {
	SetColor(false)
	entries := []store.LsEntry{
		{Path: "qmd://notes/", FileCount: 3, IsCollection: true},
		{Path: "qmd://notes/readme.md", Size: 512, ModifiedAt: "2024-06-15"},
	}
	out := LsResults(entries, Default)
	assert.Contains(t, out, "notes/")
	assert.Contains(t, out, "readme.md")
}
