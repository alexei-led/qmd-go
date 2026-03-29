package config_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/qmd-go/internal/config"
	"github.com/user/qmd-go/internal/db"
	"github.com/user/qmd-go/internal/store"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(dbPath)
	require.NoError(t, err)
	require.NoError(t, store.InitializeDatabase(d))
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func boolPtr(v bool) *bool { return &v }

func TestYAMLRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.yml")

	original := &config.Config{
		Providers: &config.ProvidersConfig{
			Embed: &config.ProviderConfig{
				Type:  "openai",
				URL:   "http://localhost:11434/v1",
				Model: "nomic-embed-text",
			},
			Rerank: &config.ProviderConfig{
				Type:      "cohere",
				APIKeyEnv: "COHERE_API_KEY",
				Model:     "rerank-v3.5",
			},
		},
		Collections: map[string]config.CollectionConfig{
			"notes": {
				Path:    "/home/user/notes",
				Pattern: "**/*.md",
			},
			"docs": {
				Path:             "/home/user/docs",
				Pattern:          "**/*.txt",
				IncludeByDefault: boolPtr(false),
				UpdateCommand:    "git pull",
			},
		},
		Contexts: []config.ContextEntry{
			{Path: "/home/user/notes", Context: "personal notes"},
			{Path: "*", Context: "all documents", Global: true},
		},
	}

	require.NoError(t, config.SaveFile(cfgPath, original))

	loaded, _, err := config.LoadFile(cfgPath, config.Paths{ConfigFile: cfgPath})
	require.NoError(t, err)

	assert.Equal(t, original.Providers.Embed.Type, loaded.Providers.Embed.Type)
	assert.Equal(t, original.Providers.Embed.URL, loaded.Providers.Embed.URL)
	assert.Equal(t, original.Providers.Embed.Model, loaded.Providers.Embed.Model)
	assert.Equal(t, original.Providers.Rerank.APIKeyEnv, loaded.Providers.Rerank.APIKeyEnv)

	assert.Len(t, loaded.Collections, 2)
	assert.Equal(t, "/home/user/notes", loaded.Collections["notes"].Path)
	assert.Equal(t, "**/*.txt", loaded.Collections["docs"].Pattern)
	assert.Equal(t, false, *loaded.Collections["docs"].IncludeByDefault)
	assert.Equal(t, "git pull", loaded.Collections["docs"].UpdateCommand)

	assert.Len(t, loaded.Contexts, 2)
	assert.Equal(t, "personal notes", loaded.Contexts[0].Context)
	assert.True(t, loaded.Contexts[1].Global)
}

func TestLoadFile_NotExist(t *testing.T) {
	cfg, _, err := config.LoadFile("/nonexistent/path.yml", config.Paths{})
	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.Nil(t, cfg.Providers)
	assert.Empty(t, cfg.Collections)
}

func TestResolvePaths(t *testing.T) {
	t.Setenv("QMD_CONFIG_DIR", "/tmp/qmd-test-config")
	t.Setenv("XDG_DATA_HOME", "/tmp/qmd-test-data")

	paths, err := config.ResolvePaths("myindex")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/qmd-test-config/myindex.yml", paths.ConfigFile)
	assert.Equal(t, "/tmp/qmd-test-data/qmd/myindex.db", paths.DBFile)
}

func TestResolvePaths_XDGConfig(t *testing.T) {
	t.Setenv("QMD_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-config")
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")

	paths, err := config.ResolvePaths("default")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/xdg-config/qmd/default.yml", paths.ConfigFile)
	assert.Equal(t, "/tmp/xdg-data/qmd/default.db", paths.DBFile)
}

func TestSyncToDB(t *testing.T) {
	d := setupTestDB(t)

	cfg := &config.Config{
		Collections: map[string]config.CollectionConfig{
			"notes": {Path: "/tmp/notes", Pattern: "**/*.md"},
			"docs":  {Path: "/tmp/docs", Pattern: "*.txt", IncludeByDefault: boolPtr(false)},
		},
	}

	require.NoError(t, config.SyncToDB(d, cfg))

	cols, err := config.ListCollections(d)
	require.NoError(t, err)
	assert.Len(t, cols, 2)

	// Second sync with same config should be a no-op (hash matches).
	require.NoError(t, config.SyncToDB(d, cfg))
}

func TestSyncToDB_HashSkip(t *testing.T) {
	d := setupTestDB(t)

	cfg := &config.Config{
		Collections: map[string]config.CollectionConfig{
			"notes": {Path: "/tmp/notes"},
		},
	}
	require.NoError(t, config.SyncToDB(d, cfg))

	// Modify DB directly — sync should not overwrite since hash matches.
	_, err := d.Exec(`UPDATE store_collections SET pattern = 'MODIFIED' WHERE name = 'notes'`)
	require.NoError(t, err)

	require.NoError(t, config.SyncToDB(d, cfg))

	var pattern string
	err = d.QueryRow(`SELECT pattern FROM store_collections WHERE name = 'notes'`).Scan(&pattern)
	require.NoError(t, err)
	assert.Equal(t, "MODIFIED", pattern)
}

func TestCollectionAddAndShow(t *testing.T) {
	d := setupTestDB(t)
	cfgPath := filepath.Join(t.TempDir(), "test.yml")
	cfg := &config.Config{}

	require.NoError(t, config.AddCollection(d, cfg, cfgPath, "notes", "/tmp/notes"))
	assert.Contains(t, cfg.Collections, "notes")

	cols, err := config.ListCollections(d)
	require.NoError(t, err)
	assert.Len(t, cols, 1)
	assert.Equal(t, "notes", cols[0].Name)

	err = config.AddCollection(d, cfg, cfgPath, "notes", "/tmp/other")
	assert.Error(t, err)

	col, err := config.ShowCollection(d, "notes")
	require.NoError(t, err)
	assert.Equal(t, "**/*.md", col.Pattern)
}

func TestCollectionUpdateAndInclude(t *testing.T) {
	d := setupTestDB(t)
	cfgPath := filepath.Join(t.TempDir(), "test.yml")
	cfg := &config.Config{}
	require.NoError(t, config.AddCollection(d, cfg, cfgPath, "notes", "/tmp/notes"))

	require.NoError(t, config.SetUpdateCommand(d, cfg, cfgPath, "notes", "git pull"))
	col, err := config.ShowCollection(d, "notes")
	require.NoError(t, err)
	assert.Equal(t, "git pull", col.UpdateCommand)

	require.NoError(t, config.SetIncludeByDefault(d, cfg, cfgPath, "notes", false))
	col, err = config.ShowCollection(d, "notes")
	require.NoError(t, err)
	assert.False(t, *col.IncludeByDefault)

	require.NoError(t, config.SetIncludeByDefault(d, cfg, cfgPath, "notes", true))
	col, err = config.ShowCollection(d, "notes")
	require.NoError(t, err)
	assert.True(t, *col.IncludeByDefault)
}

func TestCollectionRenameAndRemove(t *testing.T) {
	d := setupTestDB(t)
	cfgPath := filepath.Join(t.TempDir(), "test.yml")
	cfg := &config.Config{}
	require.NoError(t, config.AddCollection(d, cfg, cfgPath, "notes", "/tmp/notes"))
	require.NoError(t, config.SetUpdateCommand(d, cfg, cfgPath, "notes", "git pull"))

	require.NoError(t, config.RenameCollection(d, cfg, cfgPath, "notes", "my-notes"))
	assert.NotContains(t, cfg.Collections, "notes")
	assert.Contains(t, cfg.Collections, "my-notes")

	_, err := config.ShowCollection(d, "notes")
	assert.Error(t, err)
	col, err := config.ShowCollection(d, "my-notes")
	require.NoError(t, err)
	assert.Equal(t, "git pull", col.UpdateCommand)

	require.NoError(t, config.AddCollection(d, cfg, cfgPath, "docs", "/tmp/docs"))
	err = config.RenameCollection(d, cfg, cfgPath, "my-notes", "docs")
	assert.Error(t, err)

	require.NoError(t, config.RemoveCollection(d, cfg, cfgPath, "my-notes"))
	assert.NotContains(t, cfg.Collections, "my-notes")
	_, err = config.ShowCollection(d, "my-notes")
	assert.Error(t, err)

	err = config.RemoveCollection(d, cfg, cfgPath, "nonexistent")
	assert.Error(t, err)
}

func TestCollectionName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/home/user/My Notes", "my-notes"},
		{"/tmp/docs", "docs"},
		{"./relative/path", "path"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, config.CollectionName(tt.input))
	}
}

func TestContextManagement(t *testing.T) {
	d := setupTestDB(t)
	cfgPath := filepath.Join(t.TempDir(), "test.yml")
	cfg := &config.Config{}

	// Add context
	require.NoError(t, config.AddContext(d, cfg, cfgPath, "/tmp/notes", "personal notes", false))
	assert.Len(t, cfg.Contexts, 1)

	// Add duplicate
	err := config.AddContext(d, cfg, cfgPath, "/tmp/notes", "other", false)
	assert.Error(t, err)

	// Add global context
	require.NoError(t, config.AddContext(d, cfg, cfgPath, "*", "all docs", true))
	assert.Len(t, cfg.Contexts, 2)

	// List
	entries := config.ListContexts(cfg)
	assert.Len(t, entries, 2)

	// Remove
	require.NoError(t, config.RemoveContext(d, cfg, cfgPath, "/tmp/notes"))
	assert.Len(t, cfg.Contexts, 1)

	// Remove nonexistent
	err = config.RemoveContext(d, cfg, cfgPath, "/nonexistent")
	assert.Error(t, err)
}

func TestFindContextForPath(t *testing.T) {
	d := setupTestDB(t)
	dir := t.TempDir()

	notesDir := filepath.Join(dir, "notes")
	subDir := filepath.Join(notesDir, "sub")
	require.NoError(t, os.MkdirAll(subDir, config.DirPerm))

	cfg := &config.Config{
		Contexts: []config.ContextEntry{
			{Path: notesDir, Context: "notes context"},
			{Path: "*", Context: "global context", Global: true},
		},
	}

	// Exact match
	ctx := config.FindContextForPath(d, cfg, notesDir, "")
	assert.Equal(t, "notes context", ctx)

	// Child path inherits parent context
	ctx = config.FindContextForPath(d, cfg, filepath.Join(notesDir, "file.md"), "")
	assert.Equal(t, "notes context", ctx)

	// Deeply nested inherits
	ctx = config.FindContextForPath(d, cfg, filepath.Join(subDir, "deep.md"), "")
	assert.Equal(t, "notes context", ctx)

	// Unrelated path falls to global
	ctx = config.FindContextForPath(d, cfg, "/some/other/path.md", "")
	assert.Equal(t, "global context", ctx)

	// Collection-level fallback
	cfg2 := &config.Config{}
	_, err := d.Exec(`INSERT INTO store_collections (name, path, pattern, context) VALUES ('test', '/tmp', '**/*.md', 'collection ctx')`)
	require.NoError(t, err)
	ctx = config.FindContextForPath(d, cfg2, "/some/file.md", "test")
	assert.Equal(t, "collection ctx", ctx)
}

func TestRegisterCollection(t *testing.T) {
	d := setupTestDB(t)
	cfg := &config.Config{}

	err := config.RegisterCollection(d, cfg, "", "notes", "/tmp/notes",
		config.WithPattern("**/*.txt"),
		config.WithContext("my notes"),
		config.WithIgnorePatterns("*.draft"),
	)
	require.NoError(t, err)
	require.Contains(t, cfg.Collections, "notes")
	assert.Equal(t, "**/*.txt", cfg.Collections["notes"].Pattern)
	assert.Equal(t, "my notes", cfg.Collections["notes"].Context)
	assert.Equal(t, "*.draft", cfg.Collections["notes"].IgnorePatterns)

	col, err := config.ShowCollection(d, "notes")
	require.NoError(t, err)
	assert.Equal(t, "**/*.txt", col.Pattern)
	assert.Equal(t, "my notes", col.Context)
}

func TestRegisterCollection_Idempotent(t *testing.T) {
	d := setupTestDB(t)
	cfg := &config.Config{}

	require.NoError(t, config.RegisterCollection(d, cfg, "", "notes", "/tmp/notes",
		config.WithPattern("**/*.txt"),
	))
	require.NoError(t, config.RegisterCollection(d, cfg, "", "notes", "/other/path",
		config.WithPattern("**/*.go"),
	))
	assert.Equal(t, "**/*.txt", cfg.Collections["notes"].Pattern, "second call should be no-op")
}

func TestRegisterCollection_DefaultPattern(t *testing.T) {
	d := setupTestDB(t)
	cfg := &config.Config{}

	require.NoError(t, config.RegisterCollection(d, cfg, "", "docs", "/tmp/docs"))
	assert.Equal(t, "**/*.md", cfg.Collections["docs"].Pattern)
}

func TestRegisterCollection_WritesYAML(t *testing.T) {
	d := setupTestDB(t)
	cfg := &config.Config{}
	cfgPath := filepath.Join(t.TempDir(), "out.yml")

	require.NoError(t, config.RegisterCollection(d, cfg, cfgPath, "docs", "/tmp/docs"))
	_, err := os.Stat(cfgPath)
	require.NoError(t, err, "config file should be written")
}

func TestCheckContexts(t *testing.T) {
	dir := t.TempDir()
	existingPath := filepath.Join(dir, "exists")
	require.NoError(t, os.MkdirAll(existingPath, config.DirPerm))

	cfg := &config.Config{
		Contexts: []config.ContextEntry{
			{Path: existingPath, Context: "exists"},
			{Path: "/nonexistent/path", Context: "missing"},
			{Path: "*", Context: "global", Global: true},
		},
	}

	missing := config.CheckContexts(cfg)
	assert.Len(t, missing, 1)
	assert.Equal(t, "/nonexistent/path", missing[0])
}
