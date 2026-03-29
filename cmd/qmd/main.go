package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/urfave/cli/v2"

	"github.com/user/qmd-go/internal/config"
	dbpkg "github.com/user/qmd-go/internal/db"
	"github.com/user/qmd-go/internal/format"
	mcppkg "github.com/user/qmd-go/internal/mcp"
	"github.com/user/qmd-go/internal/provider"
	"github.com/user/qmd-go/internal/store"
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
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"f"},
				Usage:   "output format: json, csv, xml, md, files",
				EnvVars: []string{"QMD_FORMAT"},
			},
			&cli.BoolFlag{
				Name:    "no-color",
				Usage:   "disable colored output",
				EnvVars: []string{"NO_COLOR", "QMD_NO_COLOR"},
			},
		},
		Before: func(c *cli.Context) error {
			if c.Bool("no-color") {
				format.SetColor(false)
			}
			return nil
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
		fmt.Fprintf(os.Stderr, "%s %v\n", format.Yellow("error:"), err)
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
		Action:    searchAction,
	}
}

func searchAction(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("usage: qmd search <query>")
	}
	query := c.Args().First()

	_, _, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	results, err := store.SearchFTS(database, query, store.SearchOpts{
		Limit:        c.Int("n"),
		MinScore:     c.Float64("min-score"),
		Collection:   c.String("c"),
		SearchAll:    c.Bool("all"),
		ContextLines: c.Int("C"),
		ShowFull:     c.Bool("full"),
	})
	if err != nil {
		return err
	}

	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	f := resolveFormat(c)
	out := format.Results(results, f, format.Opts{LineNumbers: c.Bool("line-numbers")})
	fmt.Print(out)
	return nil
}

func resolveFormat(c *cli.Context) format.Format {
	// Check global --format flag first.
	if f := c.String("format"); f != "" {
		switch strings.ToLower(f) {
		case "json":
			return format.JSON
		case "csv":
			return format.CSV
		case "xml":
			return format.XML
		case "md", "markdown":
			return format.Markdown
		case "files":
			return format.Files
		}
	}

	// Fall back to per-command boolean flags.
	switch {
	case c.Bool("json"):
		return format.JSON
	case c.Bool("csv"):
		return format.CSV
	case c.Bool("xml"):
		return format.XML
	case c.Bool("md"):
		return format.Markdown
	case c.Bool("files"):
		return format.Files
	default:
		return format.Default
	}
}

func vsearchCmd() *cli.Command {
	return &cli.Command{
		Name:      "vsearch",
		Usage:     "Vector similarity search",
		ArgsUsage: "<query>",
		Flags:     searchFlags(),
		Action:    vsearchAction,
	}
}

func vsearchAction(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("usage: qmd vsearch <query>")
	}
	query := c.Args().First()

	cfg, _, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	embedder, err := provider.NewEmbedder(cfg.Providers.Embed)
	if err != nil {
		return fmt.Errorf("create embedder: %w", err)
	}
	if embedder == nil {
		return fmt.Errorf("no embedding provider configured")
	}
	defer func() { _ = embedder.Close() }()

	vecs, err := embedder.Embed(c.Context, []string{query}, provider.EmbedOpts{IsQuery: true})
	if err != nil {
		return fmt.Errorf("embed query: %w", err)
	}

	results, err := store.VectorSearch(database, vecs[0], store.VectorSearchOpts{
		Limit:      c.Int("n"),
		MinScore:   c.Float64("min-score"),
		Collection: c.String("c"),
		SearchAll:  c.Bool("all"),
	})
	if err != nil {
		return err
	}

	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	f := resolveFormat(c)
	out := format.Results(results, f, format.Opts{LineNumbers: c.Bool("line-numbers")})
	fmt.Print(out)
	return nil
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
		Action: queryAction,
	}
}

func queryAction(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("usage: qmd query <query>")
	}
	query := c.Args().First()

	cfg, _, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	embedder, reranker, generator := initQueryProviders(c, cfg)
	if embedder != nil {
		defer func() { _ = embedder.Close() }()
	}
	if reranker != nil {
		defer func() { _ = reranker.Close() }()
	}
	if generator != nil {
		defer func() { _ = generator.Close() }()
	}

	results, err := store.HybridQuery(c.Context, database, query, embedder, reranker, generator, store.HybridOpts{
		Limit:          c.Int("n"),
		MinScore:       c.Float64("min-score"),
		CandidateLimit: c.Int("candidate-limit"),
		Collection:     c.String("c"),
		SearchAll:      c.Bool("all"),
		Intent:         c.String("intent"),
		NoRerank:       c.Bool("no-rerank"),
		Explain:        c.Bool("explain"),
		ContextLines:   c.Int("C"),
		ShowFull:       c.Bool("full"),
	})
	if err != nil {
		return err
	}

	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	return outputQueryResults(c, results)
}

func initQueryProviders(c *cli.Context, cfg *config.Config) (provider.Embedder, provider.Reranker, provider.Generator) {
	embedder, err := provider.NewEmbedder(cfg.Providers.Embed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: embedder unavailable: %v\n", err)
	}

	var reranker provider.Reranker
	if !c.Bool("no-rerank") {
		reranker, err = provider.NewReranker(cfg.Providers.Rerank)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: reranker unavailable: %v\n", err)
		}
	}

	var generator provider.Generator
	generator, err = provider.NewGenerator(cfg.Providers.Generate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: generator unavailable: %v\n", err)
	}

	return embedder, reranker, generator
}

func outputQueryResults(c *cli.Context, results []store.HybridResult) error {
	if c.Bool("explain") {
		return printExplainResults(results, resolveFormat(c))
	}

	searchResults := make([]store.SearchResult, len(results))
	for i, r := range results {
		searchResults[i] = r.SearchResult
	}
	f := resolveFormat(c)
	out := format.Results(searchResults, f, format.Opts{LineNumbers: c.Bool("line-numbers")})
	fmt.Print(out)
	return nil
}

func printExplainResults(results []store.HybridResult, f format.Format) error {
	if f == format.JSON {
		out, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}

	for i, r := range results {
		fmt.Printf("#%d %s (score: %.4f)\n", i+1, r.Path, r.Score)
		if r.Explain != nil {
			fmt.Printf("    RRF: %.4f (rank %d)  Rerank: %.4f  Blend: %.2f  Final: %.4f\n",
				r.Explain.RRFScore, r.Explain.RRFRank, r.Explain.RerankScore,
				r.Explain.BlendWeight, r.Explain.FinalScore)
			if len(r.Explain.Sources) > 0 {
				fmt.Printf("    Sources: %s\n", strings.Join(r.Explain.Sources, ", "))
			}
		}
		if r.Snippet != "" {
			fmt.Printf("    %s\n", r.Snippet)
		}
	}
	return nil
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
		Action: getAction,
	}
}

func getAction(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("usage: qmd get <file[:line]>")
	}
	filename := c.Args().First()

	_, _, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	doc, notFound, err := store.FindDocument(database, filename, store.FindDocumentOpts{IncludeBody: true})
	if err != nil {
		return err
	}
	if notFound != nil {
		data, _ := json.MarshalIndent(notFound, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	_, lineSpec := store.ParseLineSpec(filename)
	fromLine := c.Int("from")
	if fromLine == 0 && lineSpec > 0 {
		fromLine = lineSpec
	}
	maxLines := c.Int("l")

	body := doc.Body
	if fromLine > 0 || maxLines > 0 {
		body, err = store.GetDocumentBody(database, doc.Filepath, fromLine, maxLines)
		if err != nil {
			return err
		}
	}

	if c.Bool("line-numbers") {
		startLine := 1
		if fromLine > 0 {
			startLine = fromLine
		}
		body = store.AddLineNumbers(body, startLine)
	}

	_, _ = fmt.Fprintf(os.Stdout, "--- %s #%s ---\n", doc.DisplayPath, doc.DocID)
	fmt.Print(body)
	if body != "" && !strings.HasSuffix(body, "\n") {
		fmt.Println()
	}
	return nil
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
		Action: multiGetAction,
	}
}

func multiGetAction(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("usage: qmd multi-get <pattern>")
	}
	pattern := c.Args().First()

	_, _, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	results, errs, err := store.FindDocuments(database, pattern, c.Int("max-bytes"))
	if err != nil {
		return err
	}

	for _, e := range errs {
		_, _ = fmt.Fprintf(os.Stderr, "warning: %s\n", e)
	}

	f := resolveFormat(c)
	out := format.MultiGetResults(results, f)
	fmt.Print(out)
	return nil
}

func lsCmd() *cli.Command {
	return &cli.Command{
		Name:      "ls",
		Usage:     "List indexed documents",
		ArgsUsage: "[path]",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "JSON output"},
		},
		Action: lsAction,
	}
}

func lsAction(c *cli.Context) error {
	path := ""
	if c.NArg() > 0 {
		path = c.Args().First()
	}

	_, _, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	entries, err := store.ListDocuments(database, path)
	if err != nil {
		return err
	}

	f := format.Default
	if c.Bool("json") {
		f = format.JSON
	}
	out := format.LsResults(entries, f)
	fmt.Print(out)
	return nil
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
		Action: updateAction,
	}
}

func updateAction(c *cli.Context) error {
	_, _, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	if c.Bool("pull") {
		if err := runUpdateCommands(database); err != nil {
			return err
		}
	}

	progress := func(collection, status string, current, total int) {
		switch status {
		case "scanning":
			fmt.Printf("Scanning %s...\n", collection)
		case "done":
			fmt.Printf("  %s: %d files indexed\n", collection, total)
		}
	}

	return store.ReindexAll(database, progress)
}

func runUpdateCommands(d *sql.DB) error {
	rows, err := d.Query(`SELECT name, update_command FROM store_collections WHERE update_command IS NOT NULL AND update_command != ''`)
	if err != nil {
		return fmt.Errorf("query update commands: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var name, cmd string
		if err := rows.Scan(&name, &cmd); err != nil {
			return fmt.Errorf("scan update command: %w", err)
		}
		fmt.Printf("Running update command for %s: %s\n", name, cmd)
		if err := execShellCommand(cmd); err != nil {
			fmt.Fprintf(os.Stderr, "warning: update command for %s failed: %v\n", name, err)
		}
	}
	return rows.Err()
}

func execShellCommand(cmd string) error {
	c := exec.Command("sh", "-c", cmd)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
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
		Action: embedAction,
	}
}

func embedAction(c *cli.Context) error {
	cfg, _, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	embedder, err := provider.NewEmbedder(cfg.Providers.Embed)
	if err != nil {
		return fmt.Errorf("create embedder: %w", err)
	}
	if embedder == nil {
		return fmt.Errorf("no embedding provider configured")
	}
	defer func() { _ = embedder.Close() }()

	return store.EmbedDocuments(c.Context, database, embedder, store.EmbedOpts{
		Clear:         c.Bool("clear"),
		NoIncremental: c.Bool("no-incremental"),
		MaxDocs:       defaultDocsPerBatch,
	}, func(p store.EmbedProgress) {
		if p.Current > 0 {
			fmt.Fprintf(os.Stderr, "\rEmbedding: %d/%d", p.Current, p.Total)
		}
	})
}

func pullCmd() *cli.Command {
	return &cli.Command{
		Name:  "pull",
		Usage: "Download or check embedding models",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "refresh", Usage: "re-download even if present"},
		},
		Action: pullAction,
	}
}

func pullAction(c *cli.Context) error {
	cfg, _, _, err := openIndex(c)
	if err != nil {
		return err
	}

	var configModel, providerType string
	if cfg.Providers != nil && cfg.Providers.Embed != nil {
		configModel = cfg.Providers.Embed.Model
		providerType = cfg.Providers.Embed.Type
	}

	status, err := provider.PullModel(configModel, providerType)
	if err != nil {
		return err
	}

	fmt.Printf("Model: %s\n", status.Path)
	const megabyte = 1024 * 1024
	fmt.Printf("Size:  %.1f MB\n", float64(status.Size)/megabyte)
	fmt.Println("Model is available.")
	return nil
}

func statusCmd() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "Show index status",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "JSON output"},
		},
		Action: statusAction,
	}
}

func statusAction(c *cli.Context) error {
	cfg, paths, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	info, err := store.GetStatus(database, c.String("index"), paths.DBFile)
	if err != nil {
		return err
	}

	if c.Bool("json") {
		data, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	fmt.Printf("Index:       %s\n", info.IndexName)
	fmt.Printf("Database:    %s\n", info.DBPath)
	fmt.Printf("Documents:   %d active / %d total\n", info.ActiveDocuments, info.TotalDocuments)
	fmt.Printf("Vectors:     %d chunks\n", info.EmbeddedChunks)
	fmt.Printf("Vec engine:  %s\n", boolStatus(info.VecAvailable))
	if info.EmbedModel != "" {
		fmt.Printf("Model:       %s\n", info.EmbedModel)
	}
	if info.EmbedDimension > 0 {
		fmt.Printf("Dimensions:  %d\n", info.EmbedDimension)
	}

	providerType := "none"
	if cfg.Providers != nil && cfg.Providers.Embed != nil {
		providerType = cfg.Providers.Embed.Type
	}
	fmt.Printf("Provider:    %s\n", providerType)

	if len(info.Collections) > 0 {
		fmt.Printf("\nCollections (%d):\n", len(info.Collections))
		for _, col := range info.Collections {
			status := "included"
			if !col.IncludeByDefault {
				status = "excluded"
			}
			fmt.Printf("  %-20s %s (%s)\n", col.Name, col.Path, status)
		}
	}
	return nil
}

func boolStatus(b bool) string {
	if b {
		return "available"
	}
	return "unavailable"
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
		Action: cleanupAction,
	}
}

func cleanupAction(c *cli.Context) error {
	_, _, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	result, err := store.Cleanup(database, store.CleanupOpts{
		SkipInactive:        c.Bool("skip-inactive"),
		SkipOrphanedContent: c.Bool("skip-orphaned-content"),
		SkipOrphanedVectors: c.Bool("skip-orphaned-vectors"),
		SkipLLMCache:        c.Bool("skip-llm-cache"),
	})
	if err != nil {
		return err
	}

	fmt.Printf("Inactive documents removed: %d\n", result.InactiveRemoved)
	fmt.Printf("Orphaned content removed:   %d\n", result.ContentRemoved)
	fmt.Printf("Orphaned vectors removed:   %d\n", result.VectorsRemoved)
	fmt.Printf("LLM cache entries cleared:  %d\n", result.CacheCleared)
	if result.Vacuumed {
		fmt.Println("Database vacuumed.")
	}
	return nil
}

// openIndex loads the config, opens the database, and initializes the schema for the given index.
func openIndex(c *cli.Context) (*config.Config, config.Paths, *sql.DB, error) {
	index := c.String("index")
	cfg, paths, err := config.Load(index)
	if err != nil {
		return nil, config.Paths{}, nil, err
	}

	if err := os.MkdirAll(paths.DataDir, config.DirPerm); err != nil {
		return nil, paths, nil, fmt.Errorf("create data dir: %w", err)
	}

	database, err := dbpkg.Open(paths.DBFile)
	if err != nil {
		return nil, paths, nil, err
	}

	if err := store.InitializeDatabase(database); err != nil {
		_ = database.Close()
		return nil, paths, nil, err
	}

	if err := config.SyncToDB(database, cfg); err != nil {
		_ = database.Close()
		return nil, paths, nil, err
	}

	return cfg, paths, database, nil
}

func collectionCmd() *cli.Command {
	return &cli.Command{
		Name:  "collection",
		Usage: "Manage collections",
		Subcommands: []*cli.Command{
			{
				Name: "add", Usage: "Add a collection", ArgsUsage: "<path>",
				Flags:  []cli.Flag{&cli.StringFlag{Name: "name", Usage: "collection name"}},
				Action: collectionAddAction,
			},
			{Name: "list", Usage: "List collections", Action: collectionListAction},
			{Name: "remove", Usage: "Remove a collection", ArgsUsage: "<name>", Action: collectionRemoveAction},
			{Name: "rename", Usage: "Rename a collection", ArgsUsage: "<old> <new>", Action: collectionRenameAction},
			{Name: "set-update", Usage: "Set update command", ArgsUsage: "<name> <command>", Action: collectionSetUpdateAction},
			{Name: "include", Usage: "Include collection in default search", ArgsUsage: "<name>", Action: collectionIncludeAction},
			{Name: "exclude", Usage: "Exclude collection from default search", ArgsUsage: "<name>", Action: collectionExcludeAction},
			{Name: "show", Usage: "Show collection details", ArgsUsage: "<name>", Action: collectionShowAction},
		},
	}
}

func collectionAddAction(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("usage: qmd collection add <path>")
	}
	dirPath := c.Args().First()
	name := c.String("name")
	if name == "" {
		name = config.CollectionName(dirPath)
	}
	cfg, paths, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	if err := config.AddCollection(database, cfg, paths.ConfigFile, name, dirPath); err != nil {
		return err
	}
	fmt.Printf("Added collection %q → %s\n", name, dirPath)
	return nil
}

func collectionListAction(c *cli.Context) error {
	_, _, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	cols, err := config.ListCollections(database)
	if err != nil {
		return err
	}
	if len(cols) == 0 {
		fmt.Println("No collections configured.")
		return nil
	}
	for _, col := range cols {
		status := "included"
		if !col.IncludeByDefault {
			status = "excluded"
		}
		fmt.Printf("%-20s %s (%s)\n", col.Name, col.Path, status)
	}
	return nil
}

func collectionRemoveAction(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("usage: qmd collection remove <name>")
	}
	name := c.Args().First()
	cfg, paths, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	if err := config.RemoveCollection(database, cfg, paths.ConfigFile, name); err != nil {
		return err
	}
	fmt.Printf("Removed collection %q\n", name)
	return nil
}

func collectionRenameAction(c *cli.Context) error {
	if c.NArg() < 2 { //nolint:mnd
		return fmt.Errorf("usage: qmd collection rename <old> <new>")
	}
	oldName := c.Args().Get(0)
	newName := c.Args().Get(1)
	cfg, paths, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	if err := config.RenameCollection(database, cfg, paths.ConfigFile, oldName, newName); err != nil {
		return err
	}
	fmt.Printf("Renamed collection %q → %q\n", oldName, newName)
	return nil
}

func collectionSetUpdateAction(c *cli.Context) error {
	if c.NArg() < 2 { //nolint:mnd
		return fmt.Errorf("usage: qmd collection set-update <name> <command>")
	}
	name := c.Args().Get(0)
	command := c.Args().Get(1)
	cfg, paths, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	if err := config.SetUpdateCommand(database, cfg, paths.ConfigFile, name, command); err != nil {
		return err
	}
	fmt.Printf("Set update command for %q: %s\n", name, command)
	return nil
}

func collectionIncludeAction(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("usage: qmd collection include <name>")
	}
	name := c.Args().First()
	cfg, paths, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	if err := config.SetIncludeByDefault(database, cfg, paths.ConfigFile, name, true); err != nil {
		return err
	}
	fmt.Printf("Collection %q included in default search\n", name)
	return nil
}

func collectionExcludeAction(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("usage: qmd collection exclude <name>")
	}
	name := c.Args().First()
	cfg, paths, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	if err := config.SetIncludeByDefault(database, cfg, paths.ConfigFile, name, false); err != nil {
		return err
	}
	fmt.Printf("Collection %q excluded from default search\n", name)
	return nil
}

func collectionShowAction(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("usage: qmd collection show <name>")
	}
	name := c.Args().First()
	_, _, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	col, err := config.ShowCollection(database, name)
	if err != nil {
		return err
	}
	fmt.Printf("Name:              %s\n", name)
	fmt.Printf("Path:              %s\n", col.Path)
	fmt.Printf("Pattern:           %s\n", col.Pattern)
	if col.IgnorePatterns != "" {
		fmt.Printf("Ignore patterns:   %s\n", col.IgnorePatterns)
	}
	if col.IncludeByDefault != nil {
		fmt.Printf("Include by default: %v\n", *col.IncludeByDefault)
	}
	if col.UpdateCommand != "" {
		fmt.Printf("Update command:    %s\n", col.UpdateCommand)
	}
	if col.Context != "" {
		fmt.Printf("Context:           %s\n", col.Context)
	}
	return nil
}

func contextCmd() *cli.Command {
	return &cli.Command{
		Name:  "context",
		Usage: "Manage context annotations",
		Subcommands: []*cli.Command{
			{
				Name: "add", Usage: "Add context to a path", ArgsUsage: "<path>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "context", Usage: "context text"},
					&cli.BoolFlag{Name: "global", Usage: "apply globally"},
				},
				Action: contextAddAction,
			},
			{Name: "list", Usage: "List contexts", Action: contextListAction},
			{Name: "remove", Usage: "Remove context", ArgsUsage: "<path>", Action: contextRemoveAction},
			{Name: "check", Usage: "Check context resolution", Action: contextCheckAction},
		},
	}
}

func contextAddAction(c *cli.Context) error {
	if c.NArg() < 1 && !c.Bool("global") {
		return fmt.Errorf("usage: qmd context add <path> --context <text>")
	}
	path := c.Args().First()
	ctxText := c.String("context")
	global := c.Bool("global")
	if ctxText == "" {
		return fmt.Errorf("--context flag is required")
	}
	if global && path == "" {
		path = "*"
	}

	cfg, paths, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	if err := config.AddContext(database, cfg, paths.ConfigFile, path, ctxText, global); err != nil {
		return err
	}
	if global {
		fmt.Printf("Added global context: %s\n", ctxText)
	} else {
		fmt.Printf("Added context for %q: %s\n", path, ctxText)
	}
	return nil
}

func contextListAction(c *cli.Context) error {
	cfg, _, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	entries := config.ListContexts(cfg)
	if len(entries) == 0 {
		fmt.Println("No context annotations configured.")
		return nil
	}
	for _, e := range entries {
		scope := e.Path
		if e.Global {
			scope = "(global)"
		}
		fmt.Printf("%-30s %s\n", scope, e.Context)
	}
	return nil
}

func contextRemoveAction(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("usage: qmd context remove <path>")
	}
	path := c.Args().First()
	cfg, paths, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	if err := config.RemoveContext(database, cfg, paths.ConfigFile, path); err != nil {
		return err
	}
	fmt.Printf("Removed context for %q\n", path)
	return nil
}

func contextCheckAction(c *cli.Context) error {
	cfg, _, database, err := openIndex(c)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	missing := config.CheckContexts(cfg)
	if len(missing) == 0 {
		fmt.Println("All context paths exist.")
		return nil
	}
	fmt.Println("Missing paths:")
	for _, p := range missing {
		fmt.Printf("  %s\n", p)
	}
	return nil
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
		Subcommands: []*cli.Command{
			{
				Name:  "stop",
				Usage: "Stop a running MCP daemon",
				Action: func(c *cli.Context) error {
					return mcppkg.StopDaemon(c.String("index"))
				},
			},
		},
		Action: func(c *cli.Context) error {
			cfg, _, database, err := openIndex(c)
			if err != nil {
				return err
			}
			defer func() { _ = database.Close() }()

			opts := mcpServerOpts(c, cfg, database)
			port := c.Int("port")

			switch {
			case c.Bool("daemon"):
				return mcppkg.ServeDaemon(opts, port)
			case c.Bool("http"):
				return mcppkg.ServeHTTP(opts, port)
			default:
				return mcppkg.ServeStdio(opts)
			}
		},
	}
}

func mcpServerOpts(c *cli.Context, cfg *config.Config, database *sql.DB) mcppkg.ServerOpts {
	embedder, reranker, _ := initQueryProviders(c, cfg)
	cfgPath, _ := config.ResolvePaths(c.String("index"))
	return mcppkg.ServerOpts{
		DB:        database,
		Config:    cfg,
		IndexName: c.String("index"),
		CfgPath:   cfgPath.ConfigFile,
		Embedder:  embedder,
		Reranker:  reranker,
		OpenClaw:  c.Bool("openclaw"),
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
