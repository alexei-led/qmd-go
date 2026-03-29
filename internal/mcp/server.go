package mcp

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/user/qmd-go/internal/config"
	"github.com/user/qmd-go/internal/openclaw"
	"github.com/user/qmd-go/internal/provider"
)

const (
	serverReadHeaderTimeout = 10 * time.Second
	serverReadTimeout       = 30 * time.Second
	serverWriteTimeout      = 60 * time.Second
)

// ServerOpts configures MCP server startup.
type ServerOpts struct {
	DB        *sql.DB
	Config    *config.Config
	IndexName string
	CfgPath   string
	Embedder  provider.Embedder
	Reranker  provider.Reranker
	OpenClaw  bool
}

// newMCPServer creates a configured MCP server with all tools and resources.
func newMCPServer(opts ServerOpts) *server.MCPServer {
	d := &deps{
		db:       opts.DB,
		cfg:      opts.Config,
		embedder: opts.Embedder,
		reranker: opts.Reranker,
	}

	instructions := buildInstructions(opts.DB, opts.Config, opts.IndexName)

	s := server.NewMCPServer("qmd", "1.0.0",
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(true, false),
		server.WithInstructions(instructions),
		server.WithRecovery(),
	)

	s.AddTool(queryTool(), queryHandler(d))
	s.AddTool(getTool(), getHandler(d))
	s.AddTool(multiGetTool(), multiGetHandler(d))
	s.AddTool(statusTool(), statusHandler(d))

	s.AddResourceTemplate(
		mcp.NewResourceTemplate("qmd://{+path}", "QMD Document",
			mcp.WithTemplateDescription("Retrieve a QMD-indexed document by path"),
			mcp.WithTemplateMIMEType("text/plain"),
		),
		resourceHandler(d),
	)

	if opts.OpenClaw {
		if err := openclaw.Setup(s, openclaw.SetupOpts{
			DB:       opts.DB,
			Config:   opts.Config,
			CfgPath:  opts.CfgPath,
			Embedder: opts.Embedder,
			Reranker: opts.Reranker,
		}); err != nil {
			slog.Warn("openclaw setup failed", "error", err)
		}
	}

	return s
}

// ServeStdio runs the MCP server over stdin/stdout.
func ServeStdio(opts ServerOpts) error {
	s := newMCPServer(opts)
	slog.Info("starting MCP server", "transport", "stdio", "index", opts.IndexName)
	return server.ServeStdio(s)
}

// ServeHTTP runs the MCP server over HTTP with SSE transport plus REST endpoints.
func ServeHTTP(opts ServerOpts, port int) error {
	s := newMCPServer(opts)

	d := &deps{
		db:       opts.DB,
		cfg:      opts.Config,
		embedder: opts.Embedder,
		reranker: opts.Reranker,
	}

	sseServer := server.NewSSEServer(s,
		server.WithSSEEndpoint("/sse"),
		server.WithMessageEndpoint("/message"),
		server.WithKeepAlive(true),
	)

	mux := http.NewServeMux()
	mux.Handle("/sse", sseServer.SSEHandler())
	mux.Handle("/message", sseServer.MessageHandler())

	registerRESTRoutes(mux, d)

	addr := ":" + strconv.Itoa(port)
	slog.Info("starting MCP server", "transport", "http", "addr", addr, "index", opts.IndexName)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("http server: %w", err)
		}
	case <-ctx.Done():
		slog.Info("shutting down MCP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
	}

	return nil
}

// ServeDaemon starts the HTTP server as a background daemon.
func ServeDaemon(opts ServerOpts, port int) error {
	pidFile := daemonPIDFile(opts.IndexName)
	if isRunning(pidFile) {
		return fmt.Errorf("daemon already running (pidfile: %s)", pidFile)
	}

	if err := writePID(pidFile); err != nil {
		return fmt.Errorf("write pidfile: %w", err)
	}
	defer func() { _ = os.Remove(pidFile) }()

	return ServeHTTP(opts, port)
}

// StopDaemon stops a running daemon by sending SIGTERM.
func StopDaemon(indexName string) error {
	pidFile := daemonPIDFile(indexName)
	return stopDaemon(pidFile)
}
