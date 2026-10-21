package embeddings

import (
	"os"
	"strings"

	"github.com/ramsterr/Wombat/internal/config"
)

// FromConfig builds the embedding client selected by embedding_provider and
// embedding_model, or by the WOMBAT_EMBED_* environment variables. It returns
// nil when no provider is configured, which makes semantic search use the
// offline local embedder.
func FromConfig(cfg *config.Config) *Client {
	if cfg == nil {
		return nil
	}
	name := strings.TrimSpace(cfg.EmbeddingProvider)
	if env := strings.TrimSpace(os.Getenv("WOMBAT_EMBED_PROVIDER")); env != "" {
		name = env
	}
	if name == "" {
		return nil
	}
	pc, ok := cfg.Providers[name]
	if !ok || pc == nil {
		return nil
	}
	base := pc.BaseURL
	if env := strings.TrimSpace(os.Getenv("WOMBAT_EMBED_BASE_URL")); env != "" {
		base = env
	}
	key := cfg.APIKey(name, pc)
	if env := os.Getenv("WOMBAT_EMBED_API_KEY"); env != "" {
		key = env
	}
	model := strings.TrimSpace(cfg.EmbeddingModel)
	if env := strings.TrimSpace(os.Getenv("WOMBAT_EMBED_MODEL")); env != "" {
		model = env
	}
	if model == "" {
		switch name {
		case "openai":
			model = "text-embedding-3-small"
		case "ollama":
			model = "nomic-embed-text"
		}
	}
	if model == "" {
		return nil
	}
	if key == "" && strings.Contains(base, "api.openai.com") {
		return nil
	}
	return New(Config{BaseURL: endpointBase(base, pc.Kind), APIKey: key, Model: model})
}

// endpointBase adds the OpenAI-compatible prefix some local servers omit.
func endpointBase(base, kind string) string {
	base = strings.TrimRight(base, "/")
	switch kind {
	case "ollama":
		if !strings.HasSuffix(base, "/v1") {
			base += "/v1"
		}
	case "opencode":
		if !strings.HasSuffix(base, "/v1") {
			base += "/openai/v1"
		}
	}
	return base
}
