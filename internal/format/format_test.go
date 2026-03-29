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

func TestFormatJSON(t *testing.T) {
	out := Results(testResults(), JSON, Opts{})
	var parsed []store.SearchResult
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	assert.Len(t, parsed, 2)
	assert.Equal(t, "Quantum Guide", parsed[0].Title)
	assert.InDelta(t, 0.9234, parsed[0].Score, 0.0001)
}

func TestFormatCSV(t *testing.T) {
	out := Results(testResults(), CSV, Opts{})
	assert.Contains(t, out, "score,collection,path,title,snippet")
	assert.Contains(t, out, "0.9234")
	assert.Contains(t, out, "quantum.md")
	assert.Contains(t, out, "0.8100")
	assert.Contains(t, out, "physics.md")
}

func TestFormatXML(t *testing.T) {
	out := Results(testResults(), XML, Opts{})
	assert.True(t, strings.HasPrefix(out, "<?xml"))

	type xmlResults struct {
		XMLName xml.Name `xml:"results"`
		Count   int      `xml:"count,attr"`
	}
	var parsed xmlResults
	require.NoError(t, xml.Unmarshal([]byte(out), &parsed))
	assert.Equal(t, 2, parsed.Count)
}

func TestFormatMarkdown(t *testing.T) {
	out := Results(testResults(), Markdown, Opts{})
	assert.Contains(t, out, "### [0.9234]")
	assert.Contains(t, out, "**Quantum Guide**")
	assert.Contains(t, out, "---")
}

func TestFormatFiles(t *testing.T) {
	out := Results(testResults(), Files, Opts{})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	assert.Len(t, lines, 2)
	assert.Equal(t, "notes/quantum.md", lines[0])
	assert.Equal(t, "notes/physics.md", lines[1])
}

func TestFormatDefault(t *testing.T) {
	SetColor(false)
	defer SetColor(false)

	out := Results(testResults(), Default, Opts{})
	assert.Contains(t, out, "[0.9234]")
	assert.Contains(t, out, "notes/quantum.md")
	assert.Contains(t, out, "Quantum Guide")
}

func TestFormatDefault_WithLineNumbers(t *testing.T) {
	SetColor(false)
	defer SetColor(false)

	out := Results(testResults(), Default, Opts{LineNumbers: true})
	assert.Contains(t, out, "2|")
}

func TestColorDisabledByEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	assert.False(t, detectColor())
}

func TestColorDisabledByQMDEnv(t *testing.T) {
	t.Setenv("QMD_NO_COLOR", "1")
	assert.False(t, detectColor())
}
