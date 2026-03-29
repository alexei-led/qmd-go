package format

import (
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/user/qmd-go/internal/store"
)

// Format selects the output format.
type Format string

const (
	Default  Format = ""
	JSON     Format = "json"
	CSV      Format = "csv"
	XML      Format = "xml"
	Markdown Format = "md"
	Files    Format = "files"
)

// Opts controls formatting behavior.
type Opts struct {
	LineNumbers bool
}

// Results formats search results in the requested format.
func Results(results []store.SearchResult, f Format, opts Opts) string {
	switch f {
	case JSON:
		return formatJSON(results)
	case CSV:
		return formatCSV(results)
	case XML:
		return formatXML(results)
	case Markdown:
		return formatMarkdown(results)
	case Files:
		return formatFiles(results)
	default:
		return formatDefault(results, opts)
	}
}

func formatJSON(results []store.SearchResult) string {
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}
	return string(data) + "\n"
}

func formatCSV(results []store.SearchResult) string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"score", "collection", "path", "title", "snippet"})
	for _, r := range results {
		_ = w.Write([]string{
			fmt.Sprintf("%.4f", r.Score),
			r.Collection,
			r.Path,
			r.Title,
			snippetOrBody(r),
		})
	}
	w.Flush()
	return b.String()
}

func formatXML(results []store.SearchResult) string {
	type xmlResult struct {
		XMLName    xml.Name `xml:"result"`
		Score      float64  `xml:"score,attr"`
		Collection string   `xml:"collection"`
		Path       string   `xml:"path"`
		Title      string   `xml:"title"`
		Snippet    string   `xml:"snippet,omitempty"`
		Body       string   `xml:"body,omitempty"`
	}
	type xmlResults struct {
		XMLName xml.Name    `xml:"results"`
		Count   int         `xml:"count,attr"`
		Results []xmlResult `xml:"result"`
	}

	xr := xmlResults{Count: len(results)}
	for _, r := range results {
		xr.Results = append(xr.Results, xmlResult{
			Score:      r.Score,
			Collection: r.Collection,
			Path:       r.Path,
			Title:      r.Title,
			Snippet:    r.Snippet,
			Body:       r.Body,
		})
	}

	data, err := xml.MarshalIndent(xr, "", "  ")
	if err != nil {
		return fmt.Sprintf("<error>%s</error>\n", err.Error())
	}
	return xml.Header + string(data) + "\n"
}

func formatMarkdown(results []store.SearchResult) string {
	var b strings.Builder
	for i, r := range results {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		fmt.Fprintf(&b, "### [%.4f] %s/%s\n\n", r.Score, r.Collection, r.Path)
		fmt.Fprintf(&b, "**%s**\n\n", r.Title)
		text := snippetOrBody(r)
		if text != "" {
			b.WriteString(text)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func formatFiles(results []store.SearchResult) string {
	var b strings.Builder
	for _, r := range results {
		fmt.Fprintf(&b, "%s/%s\n", r.Collection, r.Path)
	}
	return b.String()
}

func formatDefault(results []store.SearchResult, opts Opts) string {
	var b strings.Builder
	for i, r := range results {
		if i > 0 {
			b.WriteString("\n")
		}
		score := fmt.Sprintf("[%.4f]", r.Score)
		path := fmt.Sprintf("%s/%s", r.Collection, r.Path)
		fmt.Fprintf(&b, "%s %s %s %s\n",
			Green(score), Cyan(path), Dim("--"), Bold(r.Title))

		text := snippetOrBody(r)
		if text == "" {
			continue
		}

		lines := strings.Split(text, "\n")
		for j, line := range lines {
			if opts.LineNumbers && r.LineStart > 0 {
				fmt.Fprintf(&b, "  %s %s\n", Dim(fmt.Sprintf("%4d|", r.LineStart+j)), line)
			} else {
				fmt.Fprintf(&b, "  %s\n", line)
			}
		}
	}
	return b.String()
}

// MultiGetResults formats multi-get results in the requested format.
func MultiGetResults(results []store.MultiGetResult, f Format) string {
	switch f {
	case JSON:
		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return fmt.Sprintf(`{"error": %q}`, err.Error())
		}
		return string(data) + "\n"
	case Files:
		var b strings.Builder
		for _, r := range results {
			fmt.Fprintln(&b, r.Filepath)
		}
		return b.String()
	default:
		return multiGetDefault(results)
	}
}

func multiGetDefault(results []store.MultiGetResult) string {
	var b strings.Builder
	for i, r := range results {
		if i > 0 {
			b.WriteString("\n")
		}
		if r.Skipped {
			_, _ = fmt.Fprintf(&b, "--- %s (skipped: %s) ---\n", r.Filepath, r.SkipReason)
			continue
		}
		if r.Doc == nil {
			continue
		}
		_, _ = fmt.Fprintf(&b, "--- %s #%s ---\n", r.Doc.DisplayPath, r.Doc.DocID)
		if r.Doc.Body != "" {
			b.WriteString(r.Doc.Body)
			if !strings.HasSuffix(r.Doc.Body, "\n") {
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

// LsResults formats ls entries in the requested format.
func LsResults(entries []store.LsEntry, f Format) string {
	switch f {
	case JSON:
		data, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			return fmt.Sprintf(`{"error": %q}`, err.Error())
		}
		return string(data) + "\n"
	default:
		return lsDefault(entries)
	}
}

func lsDefault(entries []store.LsEntry) string {
	var b strings.Builder
	for _, e := range entries {
		if e.IsCollection {
			_, _ = fmt.Fprintf(&b, "%s  (%d files)\n", Cyan(e.Path), e.FileCount)
		} else {
			_, _ = fmt.Fprintf(&b, "%s  %s  %dB\n", e.Path, Dim(e.ModifiedAt), e.Size)
		}
	}
	return b.String()
}

func snippetOrBody(r store.SearchResult) string {
	if r.Body != "" {
		return r.Body
	}
	return r.Snippet
}
