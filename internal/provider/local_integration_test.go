//go:build localembed

package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalEmbedder_Integration(t *testing.T) {
	modelPath := os.Getenv("QMD_TEST_MODEL_PATH")
	if modelPath == "" {
		modelPath = filepath.Join(os.Getenv("HOME"), ".cache", "qmd", "models", defaultModelFile)
	}
	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("skipping integration test: model not found at %s", modelPath)
	}

	e := NewLocalEmbedder(modelPath)
	defer func() { _ = e.Close() }()

	vecs, err := e.Embed(context.Background(), []string{"hello world", "test document"}, EmbedOpts{})
	require.NoError(t, err)
	assert.Len(t, vecs, 2)
	assert.Equal(t, len(vecs[0]), len(vecs[1]))
	assert.Greater(t, len(vecs[0]), 0)
	assert.Greater(t, e.Dimensions(), 0)
}

func TestLocalEmbedder_ConcurrentCalls(t *testing.T) {
	modelPath := os.Getenv("QMD_TEST_MODEL_PATH")
	if modelPath == "" {
		modelPath = filepath.Join(os.Getenv("HOME"), ".cache", "qmd", "models", defaultModelFile)
	}
	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("skipping integration test: model not found at %s", modelPath)
	}

	e := NewLocalEmbedder(modelPath)
	defer func() { _ = e.Close() }()

	errs := make(chan error, 4)
	for range 4 {
		go func() {
			_, err := e.Embed(context.Background(), []string{"concurrent test"}, EmbedOpts{})
			errs <- err
		}()
	}
	for range 4 {
		require.NoError(t, <-errs)
	}
}
