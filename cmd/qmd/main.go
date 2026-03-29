package main

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"

	"github.com/urfave/cli/v2"

	"github.com/user/qmd-go/internal/config"
	dbpkg "github.com/user/qmd-go/internal/db"
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
