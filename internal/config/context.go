package config

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

// AddContext adds a context annotation for a path.
// If global is true, the context applies to all collections.
func AddContext(d *sql.DB, cfg *Config, cfgPath, path, context string, global bool) error {
	for _, e := range cfg.Contexts {
		if e.Path == path {
			return fmt.Errorf("context for path %q already exists", path)
		}
	}

	cfg.Contexts = append(cfg.Contexts, ContextEntry{
		Path:    path,
		Context: context,
		Global:  global,
	})

	if err := SaveFile(cfgPath, cfg); err != nil {
		return err
	}

	return syncContextsToDB(d, cfg)
}

// RemoveContext removes a context annotation for a path.
func RemoveContext(d *sql.DB, cfg *Config, cfgPath, path string) error {
	found := false
	filtered := make([]ContextEntry, 0, len(cfg.Contexts))
	for _, e := range cfg.Contexts {
		if e.Path == path {
			found = true
			continue
		}
		filtered = append(filtered, e)
	}
	if !found {
		return fmt.Errorf("context for path %q not found", path)
	}
	cfg.Contexts = filtered

	if err := SaveFile(cfgPath, cfg); err != nil {
		return err
	}

	return syncContextsToDB(d, cfg)
}

// ListContexts returns all context entries from the config.
func ListContexts(cfg *Config) []ContextEntry {
	return cfg.Contexts
}

// FindContextForPath resolves context for a document path using hierarchical matching.
// It checks (in order): exact path match, parent directory matches (most specific first),
// global contexts. Returns the first matching context string, or the collection-level
// context from the database as fallback.
//
//nolint:gocyclo
func FindContextForPath(d *sql.DB, cfg *Config, docPath, collection string) string {
	absPath, err := filepath.Abs(docPath)
	if err != nil {
		absPath = docPath
	}

	// Exact match first.
	for _, e := range cfg.Contexts {
		entryAbs, err := filepath.Abs(e.Path)
		if err != nil {
			entryAbs = e.Path
		}
		if entryAbs == absPath {
			return e.Context
		}
	}

	// Parent directory matches — walk up path components, most specific first.
	dir := filepath.Dir(absPath)
	for dir != "/" && dir != "." {
		for _, e := range cfg.Contexts {
			if e.Global {
				continue
			}
			entryAbs, err := filepath.Abs(e.Path)
			if err != nil {
				entryAbs = e.Path
			}
			if entryAbs == dir || strings.HasPrefix(dir, entryAbs+string(filepath.Separator)) {
				return e.Context
			}
		}
		dir = filepath.Dir(dir)
	}

	// Global contexts.
	for _, e := range cfg.Contexts {
		if e.Global {
			return e.Context
		}
	}

	// Fallback: collection-level context from DB.
	if collection != "" {
		var ctx sql.NullString
		_ = d.QueryRow(`SELECT context FROM store_collections WHERE name = ?`, collection).Scan(&ctx)
		if ctx.Valid && ctx.String != "" {
			return ctx.String
		}
	}

	return ""
}

// CheckContexts validates that all context paths exist on the filesystem.
// Returns a list of paths that do not exist.
func CheckContexts(cfg *Config) []string {
	var missing []string
	for _, e := range cfg.Contexts {
		if e.Global {
			continue
		}
		absPath, err := filepath.Abs(e.Path)
		if err != nil {
			missing = append(missing, e.Path)
			continue
		}
		if !pathExists(absPath) {
			missing = append(missing, e.Path)
		}
	}
	return missing
}

// syncContextsToDB writes context entries to the store_collections context field.
// Global contexts are stored as a special "__global" key in store_config.
func syncContextsToDB(d *sql.DB, cfg *Config) error {
	var globals []string
	for _, e := range cfg.Contexts {
		if e.Global {
			globals = append(globals, e.Context)
		}
	}

	globalCtx := strings.Join(globals, "\n")
	_, err := d.Exec(`INSERT INTO store_config (key, value) VALUES ('global_context', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, globalCtx)
	return err
}

func pathExists(path string) bool {
	_, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	_, err = filepath.EvalSymlinks(path)
	return err == nil
}
