package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const defaultModelFile = "MiniLM-L6-v2.Q8_0.gguf"

// vectorizer is the internal interface satisfied by search.Vectorizer.
// This indirection lets us gate the kelindar/search import behind a build tag.
type vectorizer interface {
	EmbedText(text string) ([]float32, error)
	Close() error
}

// LocalEmbedder implements Embedder using kelindar/search for local GGUF inference.
// The model is loaded lazily on the first Embed call.
type LocalEmbedder struct {
	modelPath string

	mu   sync.Mutex
	vec  vectorizer
	dims int
}

// NewLocalEmbedder creates a local embedder. The model is not loaded until the
// first call to Embed. If modelPath is empty, the default model location is used.
func NewLocalEmbedder(modelPath string) *LocalEmbedder {
	if modelPath == "" {
		modelPath = defaultModelPath()
	}
	return &LocalEmbedder{modelPath: modelPath}
}

func (e *LocalEmbedder) Embed(ctx context.Context, texts []string, _ EmbedOpts) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	if err := e.init(); err != nil {
		return nil, err
	}

	embeddings := make([][]float32, len(texts))
	for i, text := range texts {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		vec, err := e.vec.EmbedText(text)
		if err != nil {
			return nil, fmt.Errorf("embed text %d: %w", i, err)
		}
		embeddings[i] = vec
	}

	if e.dims == 0 && len(embeddings) > 0 && len(embeddings[0]) > 0 {
		e.mu.Lock()
		e.dims = len(embeddings[0])
		e.mu.Unlock()
	}

	return embeddings, nil
}

func (e *LocalEmbedder) Dimensions() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dims
}

func (e *LocalEmbedder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.vec != nil {
		err := e.vec.Close()
		e.vec = nil
		return err
	}
	return nil
}

// init lazily loads the GGUF model on first use.
func (e *LocalEmbedder) init() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.vec != nil {
		return nil
	}

	if _, err := os.Stat(e.modelPath); err != nil {
		return fmt.Errorf("model not found at %s: %w (run 'qmd pull' to download)", e.modelPath, err)
	}

	v, err := openVectorizer(e.modelPath)
	if err != nil {
		return fmt.Errorf("load model %s: %w", e.modelPath, err)
	}
	e.vec = v
	return nil
}

// defaultModelPath returns ~/.cache/qmd/models/MiniLM-L6-v2.Q8_0.gguf,
// respecting XDG_CACHE_HOME if set.
func defaultModelPath() string {
	cacheDir := os.Getenv("XDG_CACHE_HOME")
	if cacheDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return defaultModelFile
		}
		cacheDir = filepath.Join(home, ".cache")
	}
	return filepath.Join(cacheDir, "qmd", "models", defaultModelFile)
}

// ResolveModelPath returns the model file path from config or defaults.
func ResolveModelPath(configModel string) string {
	if configModel == "" {
		return defaultModelPath()
	}
	if filepath.IsAbs(configModel) {
		return configModel
	}
	cacheDir := os.Getenv("XDG_CACHE_HOME")
	if cacheDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return configModel
		}
		cacheDir = filepath.Join(home, ".cache")
	}
	return filepath.Join(cacheDir, "qmd", "models", configModel)
}
