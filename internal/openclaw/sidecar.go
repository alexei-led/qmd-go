package openclaw

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/mark3labs/mcp-go/server"

	"github.com/user/qmd-go/internal/config"
	"github.com/user/qmd-go/internal/provider"
)

// MemoryCollectionName is the collection name used for OpenClaw memory files.
const MemoryCollectionName = "openclaw-memory"

// DefaultMemoryDir returns the default OpenClaw workspace memory directory.
func DefaultMemoryDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".openclaw", "workspace", "memory")
}

// SetupOpts configures OpenClaw sidecar initialization.
type SetupOpts struct {
	DB       *sql.DB
	Config   *config.Config
	CfgPath  string
	Embedder provider.Embedder
	Reranker provider.Reranker
}

// Setup initializes the OpenClaw sidecar by auto-discovering and registering
// the workspace memory collection. It adds memory_search and memory_get tools
// to the MCP server.
func Setup(s *server.MCPServer, opts SetupOpts) error {
	memDir := DefaultMemoryDir()

	if err := config.RegisterCollection(opts.DB, opts.Config, opts.CfgPath, MemoryCollectionName, memDir,
		config.WithPattern("**/*.md"),
		config.WithContext("OpenClaw workspace memory files"),
	); err != nil {
		return fmt.Errorf("openclaw setup: %w", err)
	}

	cfg := DefaultMemorySearchConfig()

	s.AddTool(memorySearchTool(), memorySearchHandler(opts.DB, opts.Embedder, opts.Reranker, cfg))
	s.AddTool(memoryGetTool(), memoryGetHandler(opts.DB))

	slog.Info("openclaw sidecar enabled", "memoryDir", memDir, "collection", MemoryCollectionName)
	return nil
}
