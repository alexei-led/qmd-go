package store

import (
	"fmt"
	"strings"
)

// Snippet holds extracted text with line range information.
type Snippet struct {
	Text      string
	LineStart int
	LineEnd   int
}

// ExtractSnippet finds the most relevant lines in body for the given query/intent.
// contextLines controls how many lines before/after the best match to include.
// If contextLines <= 0, defaults to 1 before and 2 after.
func ExtractSnippet(body, query, intent string, contextLines int) Snippet {
	if body == "" {
		return Snippet{}
	}
	lines := strings.Split(body, "\n")

	queryTerms := extractTerms(query)
	intentTerms := extractIntentTerms(intent)

	bestIdx := 0
	bestScore := -1.0
	for i, line := range lines {
		score := scoreLine(line, queryTerms, intentTerms)
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	linesBefore := 1
	linesAfter := 2
	if contextLines > 0 {
		linesBefore = contextLines
		linesAfter = contextLines
	}

	start := max(bestIdx-linesBefore, 0)
	end := min(bestIdx+linesAfter+1, len(lines))

	actualBefore := bestIdx - start
	actualAfter := end - bestIdx - 1
	count := end - start

	header := fmt.Sprintf("@@ -%d,%d @@ (%d before, %d after)", start+1, count, actualBefore, actualAfter)
	snippetLines := make([]string, 0, count+1)
	snippetLines = append(snippetLines, header)
	snippetLines = append(snippetLines, lines[start:end]...)

	return Snippet{
		Text:      strings.Join(snippetLines, "\n"),
		LineStart: start + 1,
		LineEnd:   end,
	}
}

// extractTerms pulls individual terms from a query string.
func extractTerms(query string) []string {
	words := strings.Fields(strings.ToLower(query))
	var terms []string
	for _, w := range words {
		w = strings.Trim(w, `"`)
		w = strings.TrimPrefix(w, "-")
		cleaned := sanitizeForMatch(w)
		if cleaned != "" {
			terms = append(terms, cleaned)
		}
	}
	return terms
}

// extractIntentTerms pulls terms from intent text, filtering out stop words.
func extractIntentTerms(intent string) []string {
	if intent == "" {
		return nil
	}
	words := strings.Fields(strings.ToLower(intent))
	var terms []string
	for _, w := range words {
		cleaned := sanitizeForMatch(w)
		if cleaned != "" && !intentStopWords[cleaned] {
			terms = append(terms, cleaned)
		}
	}
	return terms
}

// sanitizeForMatch strips punctuation from a term for matching.
func sanitizeForMatch(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-")
}

// scoreLine scores a line by query and intent term matches.
func scoreLine(line string, queryTerms, intentTerms []string) float64 {
	lower := strings.ToLower(line)
	score := 0.0
	for _, term := range queryTerms {
		if strings.Contains(lower, term) {
			score += 1.0
		}
	}
	for _, term := range intentTerms {
		if strings.Contains(lower, term) {
			score += 0.3
		}
	}
	return score
}

// intentStopWords are common English words (2-6 chars) filtered from intent terms.
var intentStopWords = map[string]bool{
	"a": true, "i": true,
	// 2-char
	"am": true, "an": true, "as": true, "at": true, "be": true, "by": true,
	"do": true, "go": true, "he": true, "if": true, "in": true, "is": true,
	"it": true, "me": true, "my": true, "no": true, "of": true, "on": true,
	"or": true, "so": true, "to": true, "up": true, "us": true, "we": true,
	// 3-char
	"all": true, "and": true, "any": true, "are": true, "but": true,
	"can": true, "did": true, "for": true, "get": true, "got": true,
	"had": true, "has": true, "her": true, "him": true, "his": true,
	"how": true, "its": true, "let": true, "may": true, "new": true,
	"nor": true, "not": true, "now": true, "old": true, "one": true,
	"our": true, "out": true, "own": true, "say": true, "see": true,
	"she": true, "the": true, "too": true, "use": true, "was": true,
	"way": true, "who": true, "why": true, "yet": true, "you": true,
	// 4-char
	"also": true, "back": true, "been": true, "call": true, "come": true,
	"does": true, "done": true, "each": true, "even": true, "from": true,
	"gave": true, "goes": true, "gone": true, "have": true, "here": true,
	"into": true, "just": true, "know": true, "like": true, "long": true,
	"look": true, "made": true, "make": true, "many": true, "more": true,
	"most": true, "much": true, "must": true, "need": true, "only": true,
	"over": true, "said": true, "same": true, "some": true, "such": true,
	"take": true, "tell": true, "than": true, "that": true, "them": true,
	"then": true, "they": true, "this": true, "very": true, "want": true,
	"well": true, "were": true, "what": true, "when": true, "will": true,
	"with": true, "your": true,
	// 5-char
	"about": true, "after": true, "again": true, "being": true, "below": true,
	"could": true, "every": true, "first": true, "given": true, "going": true,
	"might": true, "never": true, "other": true, "quite": true, "shall": true,
	"since": true, "still": true, "their": true, "there": true, "these": true,
	"thing": true, "those": true, "under": true, "until": true, "using": true,
	"where": true, "which": true, "while": true, "whole": true, "would": true,
	// 6-char
	"above": true, "almost": true, "always": true, "became": true,
	"before": true, "behind": true, "either": true, "enough": true,
	"having": true, "indeed": true, "itself": true, "little": true,
	"making": true, "mostly": true, "rather": true, "really": true,
	"should": true, "simply": true, "though": true, "within": true,
}
