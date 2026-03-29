package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	gomcp "github.com/mark3labs/mcp-go/mcp"
	"github.com/user/qmd-go/internal/store"
)

var startTime = time.Now() //nolint:gochecknoglobals

func registerRESTRoutes(mux *http.ServeMux, d *deps) {
	mux.HandleFunc("POST /search", searchEndpoint(d))
	mux.HandleFunc("POST /query", queryEndpoint(d))
	mux.HandleFunc("GET /health", healthEndpoint(d))
}

const maxRequestBodyBytes = 1 << 20 // 1 MB

func searchEndpoint(d *deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		var req store.StructuredSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid request: %v", err)})
			return
		}

		results, err := store.StructuredSearch(r.Context(), d.db, req, d.embedder, d.reranker)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, results)
	}
}

// queryEndpoint is currently identical to searchEndpoint — both use StructuredSearch.
// TODO: wire generator into deps so /query can use HybridQuery with query expansion.
func queryEndpoint(d *deps) http.HandlerFunc {
	return searchEndpoint(d)
}

func healthEndpoint(d *deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		info, err := getStatusInfo(d.db, d.cfg)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok",
			"uptime": time.Since(startTime).String(),
			"index":  info,
		})
	}
}

func resourceHandler(d *deps) func(context.Context, gomcp.ReadResourceRequest) ([]gomcp.ResourceContents, error) {
	return func(_ context.Context, req gomcp.ReadResourceRequest) ([]gomcp.ResourceContents, error) {
		uri := req.Params.URI
		// Strip qmd:// prefix to get path
		path := strings.TrimPrefix(uri, "qmd://")

		doc, notFound, err := store.FindDocument(d.db, path, store.FindDocumentOpts{IncludeBody: true})
		if err != nil {
			return nil, fmt.Errorf("find document: %w", err)
		}
		if notFound != nil {
			return nil, fmt.Errorf("document not found: %s", path)
		}

		return []gomcp.ResourceContents{
			gomcp.TextResourceContents{
				URI:      uri,
				MIMEType: "text/plain",
				Text:     doc.Body,
			},
		}, nil
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
