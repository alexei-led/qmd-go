package config

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// CollectionOption configures a collection during registration.
type CollectionOption func(*CollectionConfig)

// WithPattern sets the glob pattern for file discovery.
func WithPattern(pattern string) CollectionOption {
	return func(c *CollectionConfig) { c.Pattern = pattern }
}

// WithContext sets the collection context annotation.
func WithContext(ctx string) CollectionOption {
	return func(c *CollectionConfig) { c.Context = ctx }
}

// WithIgnorePatterns sets the ignore patterns.
func WithIgnorePatterns(patterns string) CollectionOption {
	return func(c *CollectionConfig) { c.IgnorePatterns = patterns }
}

// RegisterCollection adds a collection if it doesn't already exist, persists to
// YAML (if cfgPath is non-empty), and syncs to the database. It is idempotent:
// if the collection already exists, it returns nil without changes.
func RegisterCollection(d *sql.DB, cfg *Config, cfgPath, name, dirPath string, opts ...CollectionOption) error {
	if cfg.Collections == nil {
		cfg.Collections = make(map[string]CollectionConfig)
	}
	if _, exists := cfg.Collections[name]; exists {
		return nil
	}

	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		return fmt.Errorf("abs path: %w", err)
	}

	col := CollectionConfig{
		Path:    absPath,
		Pattern: DefaultPattern,
	}
	for _, opt := range opts {
		opt(&col)
	}
	cfg.Collections[name] = col

	if cfgPath != "" {
		if saveErr := SaveFile(cfgPath, cfg); saveErr != nil {
			slog.Warn("persist config file", "path", cfgPath, "error", saveErr)
		}
	}
	return SyncToDB(d, cfg)
}

// SyncToDB synchronizes the YAML collections config into the store_collections table.
// It uses a SHA-256 hash of the serialized collections to skip no-op syncs.
func SyncToDB(d *sql.DB, cfg *Config) error {
	currentHash, err := collectionsHash(cfg.Collections)
	if err != nil {
		return fmt.Errorf("hash collections: %w", err)
	}

	var storedHash sql.NullString
	_ = d.QueryRow(`SELECT value FROM store_config WHERE key = 'collections_hash'`).Scan(&storedHash)
	if storedHash.Valid && storedHash.String == currentHash {
		return nil
	}

	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin sync tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for name, col := range cfg.Collections {
		pattern := col.Pattern
		if pattern == "" {
			pattern = DefaultPattern
		}
		includeBD := 1
		if col.IncludeByDefault != nil && !*col.IncludeByDefault {
			includeBD = 0
		}

		absPath, err := filepath.Abs(col.Path)
		if err != nil {
			return fmt.Errorf("abs path for %s: %w", name, err)
		}

		_, err = tx.Exec(`INSERT INTO store_collections (name, path, pattern, ignore_patterns, include_by_default, update_command, context)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(name) DO UPDATE SET
				path = excluded.path,
				pattern = excluded.pattern,
				ignore_patterns = excluded.ignore_patterns,
				include_by_default = excluded.include_by_default,
				update_command = excluded.update_command,
				context = excluded.context`,
			name, absPath, pattern, col.IgnorePatterns, includeBD, col.UpdateCommand, col.Context)
		if err != nil {
			return fmt.Errorf("upsert collection %s: %w", name, err)
		}
	}

	_, err = tx.Exec(`INSERT INTO store_config (key, value) VALUES ('collections_hash', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, currentHash)
	if err != nil {
		return fmt.Errorf("update collections hash: %w", err)
	}

	return tx.Commit()
}

// AddCollection adds a new collection to both the config file and the database.
// Unlike RegisterCollection, it returns an error if the collection already exists
// or if the config file cannot be saved.
func AddCollection(d *sql.DB, cfg *Config, cfgPath, name, dirPath string) error {
	if cfg.Collections != nil {
		if _, exists := cfg.Collections[name]; exists {
			return fmt.Errorf("collection %q already exists", name)
		}
	}
	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		return fmt.Errorf("abs path: %w", err)
	}
	if cfg.Collections == nil {
		cfg.Collections = make(map[string]CollectionConfig)
	}
	cfg.Collections[name] = CollectionConfig{
		Path:    absPath,
		Pattern: DefaultPattern,
	}
	if err := SaveFile(cfgPath, cfg); err != nil {
		return err
	}
	return SyncToDB(d, cfg)
}

// RemoveCollection removes a collection from config and database.
func RemoveCollection(d *sql.DB, cfg *Config, cfgPath, name string) error {
	if _, exists := cfg.Collections[name]; !exists {
		return fmt.Errorf("collection %q not found", name)
	}
	delete(cfg.Collections, name)

	if _, err := d.Exec(`DELETE FROM store_collections WHERE name = ?`, name); err != nil {
		return fmt.Errorf("delete from DB: %w", err)
	}

	if err := SaveFile(cfgPath, cfg); err != nil {
		return err
	}
	return invalidateCollectionsHash(d)
}

// RenameCollection renames a collection in both config and database.
func RenameCollection(d *sql.DB, cfg *Config, cfgPath, oldName, newName string) error {
	col, exists := cfg.Collections[oldName]
	if !exists {
		return fmt.Errorf("collection %q not found", oldName)
	}
	if _, exists := cfg.Collections[newName]; exists {
		return fmt.Errorf("collection %q already exists", newName)
	}

	delete(cfg.Collections, oldName)
	cfg.Collections[newName] = col

	if _, err := d.Exec(`UPDATE store_collections SET name = ? WHERE name = ?`, newName, oldName); err != nil {
		return fmt.Errorf("rename in DB: %w", err)
	}
	if _, err := d.Exec(`UPDATE documents SET collection = ? WHERE collection = ?`, newName, oldName); err != nil {
		return fmt.Errorf("rename documents: %w", err)
	}

	if err := SaveFile(cfgPath, cfg); err != nil {
		return err
	}
	return invalidateCollectionsHash(d)
}

// SetUpdateCommand sets the update command for a collection.
func SetUpdateCommand(d *sql.DB, cfg *Config, cfgPath, name, command string) error {
	col, exists := cfg.Collections[name]
	if !exists {
		return fmt.Errorf("collection %q not found", name)
	}
	col.UpdateCommand = command
	cfg.Collections[name] = col

	if err := SaveFile(cfgPath, cfg); err != nil {
		return err
	}
	return SyncToDB(d, cfg)
}

// SetIncludeByDefault sets whether a collection is included in default searches.
func SetIncludeByDefault(d *sql.DB, cfg *Config, cfgPath, name string, include bool) error {
	col, exists := cfg.Collections[name]
	if !exists {
		return fmt.Errorf("collection %q not found", name)
	}
	col.IncludeByDefault = &include
	cfg.Collections[name] = col

	if err := SaveFile(cfgPath, cfg); err != nil {
		return err
	}
	return SyncToDB(d, cfg)
}

// ShowCollection returns the collection info from the database.
func ShowCollection(d *sql.DB, name string) (*CollectionConfig, error) {
	var col CollectionConfig
	var ignorePatterns, updateCommand, context sql.NullString
	var includeBD int

	err := d.QueryRow(`SELECT path, pattern, ignore_patterns, include_by_default, update_command, context
		FROM store_collections WHERE name = ?`, name).
		Scan(&col.Path, &col.Pattern, &ignorePatterns, &includeBD, &updateCommand, &context)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("collection %q not found", name)
		}
		return nil, fmt.Errorf("query collection: %w", err)
	}

	col.IgnorePatterns = ignorePatterns.String
	col.UpdateCommand = updateCommand.String
	col.Context = context.String
	include := includeBD == 1
	col.IncludeByDefault = &include
	return &col, nil
}

// ListCollections returns all collections from the database.
func ListCollections(d *sql.DB) ([]CollectionInfo, error) {
	rows, err := d.Query(`SELECT name, path, pattern, ignore_patterns, include_by_default, update_command, context
		FROM store_collections ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []CollectionInfo
	for rows.Next() {
		var ci CollectionInfo
		var ignorePatterns, updateCommand, context sql.NullString
		var includeBD int

		if err := rows.Scan(&ci.Name, &ci.Path, &ci.Pattern, &ignorePatterns, &includeBD, &updateCommand, &context); err != nil {
			return nil, fmt.Errorf("scan collection: %w", err)
		}
		ci.IgnorePatterns = ignorePatterns.String
		ci.IncludeByDefault = includeBD == 1
		ci.UpdateCommand = updateCommand.String
		ci.Context = context.String
		result = append(result, ci)
	}
	return result, rows.Err()
}

// CollectionInfo is re-exported from store/types for use within config.
// This avoids a circular dependency — the canonical type lives in store.
type CollectionInfo struct {
	Name             string `json:"name"`
	Path             string `json:"path"`
	Pattern          string `json:"pattern"`
	IgnorePatterns   string `json:"ignorePatterns,omitempty"`
	IncludeByDefault bool   `json:"includeByDefault"`
	UpdateCommand    string `json:"updateCommand,omitempty"`
	Context          string `json:"context,omitempty"`
}

func collectionsHash(collections map[string]CollectionConfig) (string, error) {
	data, err := yaml.Marshal(collections)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func invalidateCollectionsHash(d *sql.DB) error {
	_, err := d.Exec(`DELETE FROM store_config WHERE key = 'collections_hash'`)
	return err
}
