// Package config manages QMD YAML configuration files and path resolution.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProviderConfig describes an embedding, reranking, or generation provider.
type ProviderConfig struct {
	Type      string `yaml:"type"                json:"type"`
	URL       string `yaml:"url,omitempty"        json:"url,omitempty"`
	Model     string `yaml:"model,omitempty"      json:"model,omitempty"`
	APIKeyEnv string `yaml:"api_key_env,omitempty" json:"apiKeyEnv,omitempty"`
}

// ProvidersConfig groups all provider configurations.
type ProvidersConfig struct {
	Embed    *ProviderConfig `yaml:"embed,omitempty"    json:"embed,omitempty"`
	Rerank   *ProviderConfig `yaml:"rerank,omitempty"   json:"rerank,omitempty"`
	Generate *ProviderConfig `yaml:"generate,omitempty" json:"generate,omitempty"`
}

// CollectionConfig represents a single collection entry in the YAML config.
type CollectionConfig struct {
	Path             string `yaml:"path"                        json:"path"`
	Pattern          string `yaml:"pattern,omitempty"           json:"pattern,omitempty"`
	IgnorePatterns   string `yaml:"ignore_patterns,omitempty"   json:"ignorePatterns,omitempty"`
	IncludeByDefault *bool  `yaml:"include_by_default,omitempty" json:"includeByDefault,omitempty"`
	UpdateCommand    string `yaml:"update_command,omitempty"    json:"updateCommand,omitempty"`
	Context          string `yaml:"context,omitempty"           json:"context,omitempty"`
}

// ContextEntry represents a context annotation in the YAML config.
type ContextEntry struct {
	Path    string `yaml:"path"              json:"path"`
	Context string `yaml:"context"           json:"context"`
	Global  bool   `yaml:"global,omitempty"  json:"global,omitempty"`
}

// Config is the top-level QMD YAML configuration.
type Config struct {
	Providers   *ProvidersConfig            `yaml:"providers,omitempty"   json:"providers,omitempty"`
	Collections map[string]CollectionConfig `yaml:"collections,omitempty" json:"collections,omitempty"`
	Contexts    []ContextEntry              `yaml:"contexts,omitempty"    json:"contexts,omitempty"`
}

// DefaultPattern is the default glob used when no pattern is specified.
const DefaultPattern = "**/*.md"

const (
	DirPerm  = 0o755
	filePerm = 0o644
)

// Paths holds resolved file-system paths for a given index.
type Paths struct {
	ConfigFile string
	DBFile     string
	ConfigDir  string
	DataDir    string
}

// ResolvePaths returns the config and database paths for the given index name.
// Respects QMD_CONFIG_DIR, XDG_CONFIG_HOME, and XDG_DATA_HOME.
func ResolvePaths(index string) (Paths, error) {
	configDir, err := resolveConfigDir()
	if err != nil {
		return Paths{}, err
	}
	dataDir, err := resolveDataDir()
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		ConfigFile: filepath.Join(configDir, index+".yml"),
		DBFile:     filepath.Join(dataDir, index+".db"),
		ConfigDir:  configDir,
		DataDir:    dataDir,
	}, nil
}

func resolveConfigDir() (string, error) {
	if dir := os.Getenv("QMD_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "qmd"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve config dir: %w", err)
	}
	return filepath.Join(home, ".config", "qmd"), nil
}

func resolveDataDir() (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "qmd"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve data dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "qmd"), nil
}

// Load reads and parses the YAML config for the given index.
// Returns an empty Config (not an error) if the file does not exist.
func Load(index string) (*Config, Paths, error) {
	paths, err := ResolvePaths(index)
	if err != nil {
		return nil, Paths{}, err
	}
	return LoadFile(paths.ConfigFile, paths)
}

// LoadFile reads a config from a specific file path.
func LoadFile(path string, paths Paths) (*Config, Paths, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, paths, nil
		}
		return nil, paths, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, paths, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &cfg, paths, nil
}

// Save writes the config to the YAML file for the given index.
// Creates parent directories as needed.
func Save(index string, cfg *Config) error {
	paths, err := ResolvePaths(index)
	if err != nil {
		return err
	}
	return SaveFile(paths.ConfigFile, cfg)
}

// SaveFile writes a config to a specific file path.
func SaveFile(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), DirPerm); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	return os.WriteFile(path, data, filePerm)
}

// CollectionName derives a collection name from a filesystem path.
// Uses the last path component, lowercased, with spaces replaced by dashes.
func CollectionName(dirPath string) string {
	name := filepath.Base(dirPath)
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	return name
}
