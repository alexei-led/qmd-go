package store

import (
	"path/filepath"
	"strings"
)

// VirtualPath represents a parsed collection/path reference.
type VirtualPath struct {
	Collection string
	Path       string
}

// ParseVirtualPath splits "collection/path/to/file.md" into collection and path.
// If there is no slash, the entire string is treated as the path with empty collection.
func ParseVirtualPath(vpath string) VirtualPath {
	idx := strings.IndexByte(vpath, '/')
	if idx < 0 {
		return VirtualPath{Path: vpath}
	}
	return VirtualPath{
		Collection: vpath[:idx],
		Path:       vpath[idx+1:],
	}
}

// BuildVirtualPath constructs a virtual path from collection and relative path.
func BuildVirtualPath(collection, path string) string {
	if collection == "" {
		return path
	}
	return collection + "/" + path
}

// IsVirtualPath returns true if the path looks like a virtual path (collection/path)
// rather than an absolute or relative filesystem path.
func IsVirtualPath(path string) bool {
	if filepath.IsAbs(path) {
		return false
	}
	if strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") {
		return false
	}
	if strings.HasPrefix(path, "~") {
		return false
	}
	return strings.Contains(path, "/")
}

// ToVirtualPath converts an absolute filesystem path to a virtual path
// by finding which collection's base path is a prefix.
func ToVirtualPath(absPath string, collections map[string]string) string {
	absPath = filepath.Clean(absPath)
	var bestCollection, bestRelPath string
	bestLen := 0

	for name, basePath := range collections {
		base := filepath.Clean(basePath)
		if strings.HasPrefix(absPath, base+"/") {
			if len(base) > bestLen {
				bestLen = len(base)
				bestCollection = name
				bestRelPath = absPath[len(base)+1:]
			}
		}
	}

	if bestCollection != "" {
		return BuildVirtualPath(bestCollection, bestRelPath)
	}
	return absPath
}

// ResolveVirtualPath converts a virtual path back to an absolute filesystem path
// using the collection's base path from the provided map.
func ResolveVirtualPath(vpath string, collections map[string]string) string {
	vp := ParseVirtualPath(vpath)
	if vp.Collection == "" {
		return vpath
	}
	basePath, ok := collections[vp.Collection]
	if !ok {
		return vpath
	}
	return filepath.Join(basePath, vp.Path)
}
