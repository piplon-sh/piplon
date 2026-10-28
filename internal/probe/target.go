package probe

import (
	"fmt"

	"github.com/piplon-sh/piplon/internal/config"
)

// ForTarget returns the adapter for t's format; ollama uses the OpenAI
// adapter. Formats the config accepts but no adapter implements yet wrap
// ErrUnsupported.
func ForTarget(t config.Target) (Prober, error) {
	switch t.Format {
	case "anthropic":
		return &Anthropic{URL: t.URL, Model: t.Model, Key: t.Key}, nil
	case "openai", "ollama": // Ollama serves the OpenAI-compatible API
		return &OpenAI{URL: t.URL, Model: t.Model, Key: t.Key}, nil
	default:
		return nil, fmt.Errorf("target %s: %s: %w", t.Name, t.Format, ErrUnsupported)
	}
}
