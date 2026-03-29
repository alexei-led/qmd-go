package provider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/qmd-go/internal/config"
)

func TestLocalEmbedder_EmptyInput(t *testing.T) {
	e := NewLocalEmbedder("/nonexistent/model.gguf")
	vecs, err := e.Embed(context.Background(), nil, EmbedOpts{})
	require.NoError(t, err)
	assert.Nil(t, vecs)
}

func TestLocalEmbedder_EmptySlice(t *testing.T) {
	e := NewLocalEmbedder("/nonexistent/model.gguf")
	vecs, err := e.Embed(context.Background(), []string{}, EmbedOpts{})
	require.NoError(t, err)
	assert.Nil(t, vecs)
}

func TestLocalEmbedder_ModelNotFound(t *testing.T) {
	e := NewLocalEmbedder("/nonexistent/model.gguf")
	_, err := e.Embed(context.Background(), []string{"hello"}, EmbedOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model not found")
	assert.Contains(t, err.Error(), "qmd pull")
}

func TestLocalEmbedder_DimensionsZeroBeforeEmbed(t *testing.T) {
	e := NewLocalEmbedder("/nonexistent/model.gguf")
	assert.Equal(t, 0, e.Dimensions())
}

func TestLocalEmbedder_CloseWithoutInit(t *testing.T) {
	e := NewLocalEmbedder("/nonexistent/model.gguf")
	require.NoError(t, e.Close())
}

func TestLocalEmbedder_ContextCancellation(t *testing.T) {
	e := NewLocalEmbedder("/nonexistent/model.gguf")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := e.Embed(ctx, []string{"hello"}, EmbedOpts{})
	require.Error(t, err)
}

func TestDefaultModelPath(t *testing.T) {
	path := defaultModelPath()
	assert.Contains(t, path, ".cache")
	assert.Contains(t, path, "qmd")
	assert.Contains(t, path, defaultModelFile)
}

func TestDefaultModelPath_XDGOverride(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/custom/cache")
	path := defaultModelPath()
	assert.Equal(t, "/custom/cache/qmd/models/"+defaultModelFile, path)
}

func TestResolveModelPath_Empty(t *testing.T) {
	path := ResolveModelPath("")
	assert.Contains(t, path, defaultModelFile)
}

func TestResolveModelPath_Absolute(t *testing.T) {
	path := ResolveModelPath("/my/model.gguf")
	assert.Equal(t, "/my/model.gguf", path)
}

func TestResolveModelPath_Relative(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/custom/cache")
	path := ResolveModelPath("my-model.gguf")
	assert.Equal(t, "/custom/cache/qmd/models/my-model.gguf", path)
}

func TestNewEmbedder_Local(t *testing.T) {
	e, err := NewEmbedder(&config.ProviderConfig{
		Type:  "local",
		Model: "/some/model.gguf",
	})
	require.NoError(t, err)
	require.NotNil(t, e)

	le, ok := e.(*LocalEmbedder)
	require.True(t, ok)
	assert.Equal(t, "/some/model.gguf", le.modelPath)
}

func TestNewEmbedder_LocalEnvVar(t *testing.T) {
	t.Setenv("QMD_EMBED_PROVIDER", "local")
	e, err := NewEmbedder(nil)
	require.NoError(t, err)
	require.NotNil(t, e)

	_, ok := e.(*LocalEmbedder)
	require.True(t, ok)
}
