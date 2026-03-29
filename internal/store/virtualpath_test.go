package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/user/qmd-go/internal/store"
)

func TestParseVirtualPath(t *testing.T) {
	vp := store.ParseVirtualPath("notes/ideas/foo.md")
	assert.Equal(t, "notes", vp.Collection)
	assert.Equal(t, "ideas/foo.md", vp.Path)
}

func TestParseVirtualPath_NoSlash(t *testing.T) {
	vp := store.ParseVirtualPath("file.md")
	assert.Equal(t, "", vp.Collection)
	assert.Equal(t, "file.md", vp.Path)
}

func TestBuildVirtualPath(t *testing.T) {
	assert.Equal(t, "coll/path.md", store.BuildVirtualPath("coll", "path.md"))
	assert.Equal(t, "path.md", store.BuildVirtualPath("", "path.md"))
}

func TestIsVirtualPath(t *testing.T) {
	assert.True(t, store.IsVirtualPath("coll/file.md"))
	assert.False(t, store.IsVirtualPath("/absolute/path.md"))
	assert.False(t, store.IsVirtualPath("./relative.md"))
	assert.False(t, store.IsVirtualPath("../parent.md"))
	assert.False(t, store.IsVirtualPath("~/home.md"))
	assert.False(t, store.IsVirtualPath("nopath"))
}

func TestToVirtualPath(t *testing.T) {
	collections := map[string]string{
		"notes":  "/home/user/notes",
		"work":   "/home/user/work",
		"nested": "/home/user/notes/deep",
	}

	assert.Equal(t, "notes/ideas/foo.md", store.ToVirtualPath("/home/user/notes/ideas/foo.md", collections))
	assert.Equal(t, "work/report.md", store.ToVirtualPath("/home/user/work/report.md", collections))
	assert.Equal(t, "nested/item.md", store.ToVirtualPath("/home/user/notes/deep/item.md", collections))
	assert.Equal(t, "/other/file.md", store.ToVirtualPath("/other/file.md", collections))
}

func TestResolveVirtualPath(t *testing.T) {
	collections := map[string]string{
		"notes": "/home/user/notes",
	}

	assert.Equal(t, "/home/user/notes/foo.md", store.ResolveVirtualPath("notes/foo.md", collections))
	assert.Equal(t, "unknown/bar.md", store.ResolveVirtualPath("unknown/bar.md", collections))
	assert.Equal(t, "nopath", store.ResolveVirtualPath("nopath", collections))
}
