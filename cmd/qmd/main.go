package main

import (
	"fmt"
	"os"

	"github.com/urfave/cli/v2"
)

// Set via LDFLAGS at build time.
var version = "dev"

// CLI default values — must match TS constants.
const (
	defaultSearchResults  = 10
	defaultCandidateLimit = 40
	defaultMaxLines       = 100
	defaultMaxBytes       = 10240
	defaultDocsPerBatch   = 64
	defaultBatchMB        = 64
	defaultMCPPort        = 18790
)

func main() {
	app := &cli.App{
		Name:    "qmd",
		Usage:   "Query Markup Documents — semantic search over your notes",
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "index",
				Usage:   "index name (determines DB + config paths)",
				Value:   "default",
				EnvVars: []string{"QMD_INDEX"},
			},
		},
		Commands: []*cli.Command{
			searchCmd(),
			vsearchCmd(),
			queryCmd(),
			getCmd(),
			multiGetCmd(),
			lsCmd(),
			updateCmd(),
			embedCmd(),
			pullCmd(),
			statusCmd(),
			cleanupCmd(),
			collectionCmd(),
			contextCmd(),
			mcpCmd(),
		},
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// Stub commands — each will be implemented in later tasks.

func searchCmd() *cli.Command {
	return &cli.Command{
		Name:      "search",
		Usage:     "Full-text search",
		ArgsUsage: "<query>",
		Flags:     searchFlags(),
		Action: func(c *cli.Context) error {
			return fmt.Errorf("not yet implemented")
		},
	}
}

func vsearchCmd() *cli.Command {
	return &cli.Command{
		Name:      "vsearch",
		Usage:     "Vector similarity search",
		ArgsUsage: "<query>",
		Flags:     searchFlags(),
		Action: func(c *cli.Context) error {
			return fmt.Errorf("not yet implemented")
		},
	}
}

func queryCmd() *cli.Command {
	return &cli.Command{
		Name:      "query",
		Usage:     "Hybrid search (BM25 + vector + reranking)",
		ArgsUsage: "<query>",
		Flags: append(searchFlags(),
			&cli.BoolFlag{Name: "no-rerank", Usage: "skip reranking step"},
			&cli.BoolFlag{Name: "explain", Usage: "show scoring details"},
			&cli.StringFlag{Name: "intent", Usage: "intent context for search"},
			&cli.IntFlag{Name: "candidate-limit", Usage: "candidate pool size", Value: defaultCandidateLimit},
		),
		Action: func(c *cli.Context) error {
			return fmt.Errorf("not yet implemented")
		},
	}
}

func getCmd() *cli.Command {
	return &cli.Command{
		Name:      "get",
		Usage:     "Get document content",
		ArgsUsage: "<file[:line]>",
		Flags: []cli.Flag{
			&cli.IntFlag{Name: "from", Usage: "start line number"},
			&cli.IntFlag{Name: "l", Aliases: []string{"lines"}, Usage: "max lines to return", Value: defaultMaxLines},
			&cli.BoolFlag{Name: "line-numbers", Usage: "show line numbers"},
		},
		Action: func(c *cli.Context) error {
			return fmt.Errorf("not yet implemented")
		},
	}
}

func multiGetCmd() *cli.Command {
	return &cli.Command{
		Name:      "multi-get",
		Usage:     "Get multiple documents by glob pattern",
		ArgsUsage: "<pattern>",
		Flags: []cli.Flag{
			&cli.IntFlag{Name: "l", Aliases: []string{"lines"}, Usage: "max lines per file", Value: defaultMaxLines},
			&cli.IntFlag{Name: "max-bytes", Usage: "max total bytes", Value: defaultMaxBytes},
			&cli.BoolFlag{Name: "json", Usage: "JSON output"},
			&cli.BoolFlag{Name: "csv", Usage: "CSV output"},
			&cli.BoolFlag{Name: "md", Usage: "Markdown output"},
			&cli.BoolFlag{Name: "xml", Usage: "XML output"},
			&cli.BoolFlag{Name: "files", Usage: "file paths only"},
		},
		Action: func(c *cli.Context) error {
			return fmt.Errorf("not yet implemented")
		},
	}
}

func lsCmd() *cli.Command {
	return &cli.Command{
		Name:      "ls",
		Usage:     "List indexed documents",
		ArgsUsage: "[path]",
		Action: func(c *cli.Context) error {
			return fmt.Errorf("not yet implemented")
		},
	}
}

func updateCmd() *cli.Command {
	return &cli.Command{
		Name:  "update",
		Usage: "Scan and index documents",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "pull", Usage: "run collection update commands first"},
			&cli.IntFlag{Name: "max-docs-per-batch", Usage: "max docs per embedding batch", Value: defaultDocsPerBatch},
			&cli.IntFlag{Name: "max-batch-mb", Usage: "max batch size in MB", Value: defaultBatchMB},
		},
		Action: func(c *cli.Context) error {
			return fmt.Errorf("not yet implemented")
		},
	}
}

func embedCmd() *cli.Command {
	return &cli.Command{
		Name:  "embed",
		Usage: "Generate embeddings for indexed documents",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "clear", Usage: "clear all embeddings first"},
			&cli.BoolFlag{Name: "no-incremental", Usage: "re-embed all documents"},
			&cli.StringFlag{Name: "model", Usage: "embedding model override"},
		},
		Action: func(c *cli.Context) error {
			return fmt.Errorf("not yet implemented")
		},
	}
}

func pullCmd() *cli.Command {
	return &cli.Command{
		Name:  "pull",
		Usage: "Download or check embedding models",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "refresh", Usage: "re-download even if present"},
		},
		Action: func(c *cli.Context) error {
			return fmt.Errorf("not yet implemented")
		},
	}
}

func statusCmd() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "Show index status",
		Action: func(c *cli.Context) error {
			return fmt.Errorf("not yet implemented")
		},
	}
}

func cleanupCmd() *cli.Command {
	return &cli.Command{
		Name:  "cleanup",
		Usage: "Clean up orphaned data and vacuum",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "skip-inactive", Usage: "skip inactive document cleanup"},
			&cli.BoolFlag{Name: "skip-orphaned-content", Usage: "skip orphaned content cleanup"},
			&cli.BoolFlag{Name: "skip-orphaned-vectors", Usage: "skip orphaned vector cleanup"},
			&cli.BoolFlag{Name: "skip-llm-cache", Usage: "skip LLM cache cleanup"},
		},
		Action: func(c *cli.Context) error {
			return fmt.Errorf("not yet implemented")
		},
	}
}

func collectionCmd() *cli.Command {
	return &cli.Command{
		Name:  "collection",
		Usage: "Manage collections",
		Subcommands: []*cli.Command{
			{Name: "add", Usage: "Add a collection", ArgsUsage: "<path>",
				Flags:  []cli.Flag{&cli.StringFlag{Name: "name", Usage: "collection name"}},
				Action: func(c *cli.Context) error { return fmt.Errorf("not yet implemented") }},
			{Name: "list", Usage: "List collections",
				Action: func(c *cli.Context) error { return fmt.Errorf("not yet implemented") }},
			{Name: "remove", Usage: "Remove a collection", ArgsUsage: "<name>",
				Action: func(c *cli.Context) error { return fmt.Errorf("not yet implemented") }},
			{Name: "rename", Usage: "Rename a collection", ArgsUsage: "<old> <new>",
				Action: func(c *cli.Context) error { return fmt.Errorf("not yet implemented") }},
			{Name: "set-update", Usage: "Set update command", ArgsUsage: "<name> <command>",
				Action: func(c *cli.Context) error { return fmt.Errorf("not yet implemented") }},
			{Name: "include", Usage: "Include collection in default search", ArgsUsage: "<name>",
				Action: func(c *cli.Context) error { return fmt.Errorf("not yet implemented") }},
			{Name: "exclude", Usage: "Exclude collection from default search", ArgsUsage: "<name>",
				Action: func(c *cli.Context) error { return fmt.Errorf("not yet implemented") }},
			{Name: "show", Usage: "Show collection details", ArgsUsage: "<name>",
				Action: func(c *cli.Context) error { return fmt.Errorf("not yet implemented") }},
		},
	}
}

func contextCmd() *cli.Command {
	return &cli.Command{
		Name:  "context",
		Usage: "Manage context annotations",
		Subcommands: []*cli.Command{
			{Name: "add", Usage: "Add context to a path", ArgsUsage: "<path>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "context", Usage: "context text"},
					&cli.BoolFlag{Name: "global", Usage: "apply globally"},
				},
				Action: func(c *cli.Context) error { return fmt.Errorf("not yet implemented") }},
			{Name: "list", Usage: "List contexts",
				Action: func(c *cli.Context) error { return fmt.Errorf("not yet implemented") }},
			{Name: "remove", Usage: "Remove context", ArgsUsage: "<path>",
				Action: func(c *cli.Context) error { return fmt.Errorf("not yet implemented") }},
			{Name: "check", Usage: "Check context resolution",
				Action: func(c *cli.Context) error { return fmt.Errorf("not yet implemented") }},
		},
	}
}

func mcpCmd() *cli.Command {
	return &cli.Command{
		Name:  "mcp",
		Usage: "Start MCP server",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "stdio", Usage: "use stdio transport"},
			&cli.BoolFlag{Name: "http", Usage: "use HTTP transport"},
			&cli.IntFlag{Name: "port", Usage: "HTTP port", Value: defaultMCPPort},
			&cli.BoolFlag{Name: "daemon", Usage: "run as background daemon"},
			&cli.BoolFlag{Name: "openclaw", Usage: "enable OpenClaw sidecar mode"},
		},
		Action: func(c *cli.Context) error {
			return fmt.Errorf("not yet implemented")
		},
	}
}

// searchFlags returns the common flags for search/vsearch/query commands.
func searchFlags() []cli.Flag {
	return []cli.Flag{
		&cli.IntFlag{Name: "n", Usage: "max results", Value: defaultSearchResults},
		&cli.Float64Flag{Name: "min-score", Usage: "minimum score threshold"},
		&cli.BoolFlag{Name: "all", Usage: "search all collections"},
		&cli.IntFlag{Name: "C", Aliases: []string{"context"}, Usage: "context lines"},
		&cli.BoolFlag{Name: "full", Usage: "show full document content"},
		&cli.BoolFlag{Name: "line-numbers", Usage: "show line numbers"},
		&cli.BoolFlag{Name: "files", Usage: "file paths only"},
		&cli.BoolFlag{Name: "json", Usage: "JSON output"},
		&cli.BoolFlag{Name: "csv", Usage: "CSV output"},
		&cli.BoolFlag{Name: "md", Usage: "Markdown output"},
		&cli.BoolFlag{Name: "xml", Usage: "XML output"},
		&cli.StringFlag{Name: "c", Aliases: []string{"collection"}, Usage: "collection name filter"},
	}
}
