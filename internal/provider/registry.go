package provider

import (
	"fmt"
	"os"

	"github.com/user/qmd-go/internal/config"
)

// Default provider URLs.
const (
	defaultOpenAIURL = "https://api.openai.com"
	defaultCohereURL = "https://api.cohere.ai"
	defaultGeminiURL = "https://generativelanguage.googleapis.com"
	defaultJinaURL   = "https://api.jina.ai"
	defaultVoyageURL = "https://api.voyageai.com"
)

// NewEmbedder creates an Embedder from config, falling back to env vars.
// Returns (nil, nil) if no embedding provider is configured.
func NewEmbedder(cfg *config.ProviderConfig) (Embedder, error) {
	cfg = resolveEmbedConfig(cfg)
	if cfg == nil {
		return nil, nil
	}

	apiKey := resolveAPIKey(cfg.APIKeyEnv)
	url := cfg.URL
	if url == "" {
		url = defaultEmbedURL(cfg.Type)
	}

	switch cfg.Type {
	case "local":
		return nil, fmt.Errorf("local embedder requires Task 6 (kelindar/search)")
	case "openai", "cohere", "gemini":
		return NewRemoteEmbedder(cfg.Type, url, apiKey, cfg.Model, nil), nil
	default:
		return nil, fmt.Errorf("unknown embed provider type: %q", cfg.Type)
	}
}

// NewReranker creates a Reranker from config.
// Returns (nil, nil) if no reranker is configured.
func NewReranker(cfg *config.ProviderConfig) (Reranker, error) {
	cfg = resolveRerankConfig(cfg)
	if cfg == nil {
		return nil, nil
	}

	apiKey := resolveAPIKey(cfg.APIKeyEnv)
	url := cfg.URL
	if url == "" {
		url = defaultRerankURL(cfg.Type)
	}

	switch cfg.Type {
	case "cohere", "jina", "voyage", "tei":
		return NewRemoteReranker(cfg.Type, url, apiKey, cfg.Model, nil), nil
	default:
		return nil, fmt.Errorf("unknown rerank provider type: %q", cfg.Type)
	}
}

// NewGenerator creates a Generator from config.
// Returns (nil, nil) if no generator is configured.
func NewGenerator(cfg *config.ProviderConfig) (Generator, error) {
	cfg = resolveGenConfig(cfg)
	if cfg == nil {
		return nil, nil
	}

	apiKey := resolveAPIKey(cfg.APIKeyEnv)
	url := cfg.URL
	if url == "" {
		url = defaultOpenAIURL
	}

	switch cfg.Type {
	case "openai":
		return NewRemoteGenerator(url, apiKey, cfg.Model, nil), nil
	default:
		return nil, fmt.Errorf("unknown generate provider type: %q", cfg.Type)
	}
}

// resolveEmbedConfig merges config with env var fallbacks.
func resolveEmbedConfig(cfg *config.ProviderConfig) *config.ProviderConfig {
	if envType := os.Getenv("QMD_EMBED_PROVIDER"); envType == "local" {
		return &config.ProviderConfig{Type: "local"}
	}
	if cfg != nil {
		return cfg
	}
	if envURL := os.Getenv("QMD_REMOTE_EMBED_URL"); envURL != "" {
		return &config.ProviderConfig{
			Type:      "openai",
			URL:       envURL,
			APIKeyEnv: "QMD_REMOTE_API_KEY",
		}
	}
	return nil
}

func resolveRerankConfig(cfg *config.ProviderConfig) *config.ProviderConfig {
	if cfg != nil {
		return cfg
	}
	if envURL := os.Getenv("QMD_REMOTE_RERANK_URL"); envURL != "" {
		return &config.ProviderConfig{
			Type:      "cohere",
			URL:       envURL,
			APIKeyEnv: "QMD_REMOTE_API_KEY",
		}
	}
	return nil
}

func resolveGenConfig(cfg *config.ProviderConfig) *config.ProviderConfig {
	if cfg != nil {
		return cfg
	}
	return nil
}

func resolveAPIKey(envName string) string {
	if envName == "" {
		return ""
	}
	return os.Getenv(envName)
}

func defaultEmbedURL(providerType string) string {
	switch providerType {
	case "cohere":
		return defaultCohereURL
	case "gemini":
		return defaultGeminiURL
	default:
		return defaultOpenAIURL
	}
}

func defaultRerankURL(providerType string) string {
	switch providerType {
	case "cohere":
		return defaultCohereURL
	case "jina":
		return defaultJinaURL
	case "voyage":
		return defaultVoyageURL
	default:
		return ""
	}
}
