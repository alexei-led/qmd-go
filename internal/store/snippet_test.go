package store

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractSnippet_BasicMatch(t *testing.T) {
	body := "line one\nline two\nthe quantum result\nline four\nline five"
	snip := ExtractSnippet(body, "quantum", "", 0)

	assert.Equal(t, 2, snip.LineStart)
	assert.Equal(t, 5, snip.LineEnd)
	assert.Contains(t, snip.Text, "the quantum result")
	assert.Contains(t, snip.Text, "@@ -2,4 @@")
}

func TestExtractSnippet_WithIntent(t *testing.T) {
	body := "line one\napple banana\ncherry date\nquantum physics\nline five"
	snip := ExtractSnippet(body, "missing", "quantum physics experiment", 0)

	assert.Contains(t, snip.Text, "quantum physics")
}

func TestExtractSnippet_FirstLine(t *testing.T) {
	body := "quantum match here\nline two\nline three"
	snip := ExtractSnippet(body, "quantum", "", 0)

	assert.Equal(t, 1, snip.LineStart)
	assert.Contains(t, snip.Text, "quantum match here")
	assert.Contains(t, snip.Text, "(0 before,")
}

func TestExtractSnippet_LastLine(t *testing.T) {
	body := "line one\nline two\nquantum match here"
	snip := ExtractSnippet(body, "quantum", "", 0)

	assert.Equal(t, 3, snip.LineEnd)
	assert.Contains(t, snip.Text, "quantum match here")
	assert.Contains(t, snip.Text, "0 after)")
}

func TestExtractSnippet_CustomContextLines(t *testing.T) {
	body := "a\nb\nc\nd\nquantum\nf\ng\nh\ni"
	snip := ExtractSnippet(body, "quantum", "", 3)

	assert.Contains(t, snip.Text, "b\nc\nd\nquantum\nf\ng\nh")
}

func TestExtractSnippet_DiffHeader(t *testing.T) {
	body := "before\ntarget keyword\nafter1\nafter2"
	snip := ExtractSnippet(body, "keyword", "", 0)

	lines := strings.Split(snip.Text, "\n")
	assert.True(t, strings.HasPrefix(lines[0], "@@ -"))
	assert.Contains(t, lines[0], "before,")
	assert.Contains(t, lines[0], "after)")
}

func TestExtractSnippet_EmptyBody(t *testing.T) {
	snip := ExtractSnippet("", "test", "", 0)
	assert.Equal(t, 0, snip.LineStart)
}

func TestExtractTerms(t *testing.T) {
	terms := extractTerms(`machine "deep learning" -neural`)
	assert.Contains(t, terms, "machine")
	assert.Contains(t, terms, "deep")
	assert.Contains(t, terms, "learning")
	assert.Contains(t, terms, "neural")
}

func TestExtractIntentTerms_FiltersStopWords(t *testing.T) {
	terms := extractIntentTerms("the quantum theory of everything about physics")
	assert.Contains(t, terms, "quantum")
	assert.Contains(t, terms, "theory")
	assert.Contains(t, terms, "physics")
	assert.NotContains(t, terms, "the")
	assert.NotContains(t, terms, "of")
	assert.NotContains(t, terms, "about")
}

func TestExtractIntentTerms_Empty(t *testing.T) {
	assert.Nil(t, extractIntentTerms(""))
}

func TestScoreLine(t *testing.T) {
	score := scoreLine("quantum mechanics is fascinating", []string{"quantum", "mechanics"}, []string{"fascinating"})
	assert.InDelta(t, 2.3, score, 0.01)
}

func TestExtractSnippet_IntentDisambiguates(t *testing.T) {
	body := "Introduction\nWeb performance optimization is critical for user experience.\nMeasuring load times and render speed matters.\nFiller line one.\nFiller line two.\nFiller line three.\nHealth metrics track patient outcomes over time.\nPerformance of health systems is measured annually.\nConclusion"

	snipWeb := ExtractSnippet(body, "performance", "web", 0)
	assert.Contains(t, snipWeb.Text, "Web performance")

	snipHealth := ExtractSnippet(body, "performance", "health", 0)
	assert.Contains(t, snipHealth.Text, "Health metrics")
}

func TestExtractSnippet_DiffHeaderFormat(t *testing.T) {
	body := "a\nb\nc\ntarget keyword\ne\nf"
	snip := ExtractSnippet(body, "keyword", "", 0)
	lines := strings.Split(snip.Text, "\n")
	assert.Regexp(t, `^@@ -\d+,\d+ @@ \(\d+ before, \d+ after\)$`, lines[0])
}

func TestExtractIntentTerms_DomainTerms(t *testing.T) {
	terms := extractIntentTerms("kubernetes API deployment")
	assert.Contains(t, terms, "kubernetes")
	assert.Contains(t, terms, "api")
	assert.Contains(t, terms, "deployment")
}

func TestExtractIntentTerms_ShortTerms(t *testing.T) {
	terms := extractIntentTerms("CI CD DB API SQL")
	assert.Contains(t, terms, "ci")
	assert.Contains(t, terms, "cd")
	assert.Contains(t, terms, "db")
	assert.Contains(t, terms, "api")
	assert.Contains(t, terms, "sql")
}

func TestExtractIntentTerms_Lowercases(t *testing.T) {
	terms := extractIntentTerms("Kubernetes DOCKER Terraform")
	for _, term := range terms {
		assert.Equal(t, strings.ToLower(term), term)
	}
}
