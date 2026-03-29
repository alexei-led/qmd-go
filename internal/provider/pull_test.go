package provider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckModel_NotFound(t *testing.T) {
	status := CheckModel("/nonexistent/model.gguf")
	assert.False(t, status.Exists)
	assert.Equal(t, "/nonexistent/model.gguf", status.Path)
}

func TestCheckModel_Found(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "model.gguf")
	require.NoError(t, os.WriteFile(tmp, []byte("fake model"), 0o644))

	status := CheckModel(tmp)
	assert.True(t, status.Exists)
	assert.Equal(t, int64(10), status.Size)
}

func TestPullModel_RemoteProvider(t *testing.T) {
	_, err := PullModel("", "openai")
	assert.ErrorContains(t, err, "does not require a local model")
}

func TestPullModel_Missing(t *testing.T) {
	_, err := PullModel("/nonexistent/model.gguf", "local")
	assert.ErrorContains(t, err, "model not found")
}

func TestPullModel_Exists(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "model.gguf")
	require.NoError(t, os.WriteFile(tmp, []byte("fake"), 0o644))

	status, err := PullModel(tmp, "local")
	require.NoError(t, err)
	assert.True(t, status.Exists)
}
