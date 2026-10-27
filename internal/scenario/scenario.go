package scenario

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/piplon-sh/piplon/scenarios"
)

// Scenario is one versioned probe request.
type Scenario struct {
	Name        string   `yaml:"name"`
	Version     int      `yaml:"version"`
	Description string   `yaml:"description"`
	Limits      Limits   `yaml:"limits"`
	Cache       Cache    `yaml:"cache"`
	System      string   `yaml:"system"`
	Tools       []Tool   `yaml:"tools"`
	Padding     Padding  `yaml:"padding"`
	Turns       []Turn   `yaml:"turns"`
	Measures    []string `yaml:"measures"`

	padding string // generated once by Parse
}

// Limits bounds the request.
type Limits struct {
	MaxOutputTokens int           `yaml:"max_output_tokens" json:"max_output_tokens"`
	Temperature     float64       `yaml:"temperature" json:"temperature"`
	Stream          bool          `yaml:"stream" json:"stream"`
	Timeout         time.Duration `yaml:"timeout" json:"-"`
	// InputTokens is the measured input size of one turn, used only to
	// estimate cost against the daily budget.
	InputTokens int `yaml:"input_tokens" json:"input_tokens"`
}

// Cache says from which turn a prompt-cache read is expected; 0 means never.
type Cache struct {
	ExpectHitFromTurn int `yaml:"expect_hit_from_turn" json:"expect_hit_from_turn"`
}

// Tool is a provider-neutral tool definition; Parameters is a JSON Schema.
type Tool struct {
	Name        string         `yaml:"name" json:"name"`
	Description string         `yaml:"description" json:"description"`
	Parameters  map[string]any `yaml:"parameters" json:"parameters"`
}

// Padding sizes the deterministic filler appended to the system prompt.
type Padding struct {
	Rows int    `yaml:"rows"`
	Seed string `yaml:"seed"`
}

// Turn is one user message and what its answer must contain.
type Turn struct {
	Role    string `yaml:"role" json:"role"`
	Content string `yaml:"content" json:"content"`
	Expect  Expect `yaml:"expect" json:"expect"`
}

// Expect is the shape check applied to a turn's answer.
type Expect struct {
	Contains string `yaml:"contains" json:"contains,omitempty"`
}

// ID is the versioned name stored with every sample, for example "standard-v1".
func (s *Scenario) ID() string { return fmt.Sprintf("%s-v%d", s.Name, s.Version) }

// PaddingText returns the generated padding.
func (s *Scenario) PaddingText() string { return s.padding }

// SystemPrompt returns the system text, a blank line, then the padding.
func (s *Scenario) SystemPrompt() string {
	return strings.TrimRight(s.System, "\n") + "\n\n" + s.padding
}

// Names lists the shipped scenarios, sorted.
func Names() []string {
	files, _ := fs.Glob(scenarios.FS, "*.yaml")
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = strings.TrimSuffix(path.Base(f), ".yaml")
	}
	return names
}

// Load reads, validates and pads the shipped scenario called name.
func Load(name string) (*Scenario, error) {
	raw, err := scenarios.FS.ReadFile(name + ".yaml")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("scenario %q: %w; available: %s", name, ErrNotFound, strings.Join(Names(), ", "))
	}
	if err != nil {
		return nil, fmt.Errorf("scenario %s: %w", name, err)
	}
	s, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("scenario %s: %w", name, err)
	}
	if s.ID() != name {
		return nil, fmt.Errorf("scenario %s: name and version say %s: %w", name, s.ID(), ErrInvalid)
	}
	return s, nil
}

// Parse decodes and validates a scenario, then generates its padding.
// Unknown fields are rejected.
func Parse(data []byte) (*Scenario, error) {
	var s Scenario
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, err
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	s.padding = padding(s.Padding.Seed, s.Padding.Rows)
	return &s, nil
}

// rendered is the canonical, provider-neutral form of a scenario. Field order
// here is the byte order of the golden file.
type rendered struct {
	Scenario string `json:"scenario"`
	Limits   Limits `json:"limits"`
	Cache    Cache  `json:"cache"`
	System   string `json:"system"`
	Tools    []Tool `json:"tools"`
	Turns    []Turn `json:"turns"`
}

// Render returns the canonical JSON rendering: what adapters translate and
// what the golden test pins. Map keys in tool parameters are sorted by
// encoding/json, so the output is stable.
func (s *Scenario) Render() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(rendered{
		Scenario: s.ID(),
		Limits:   s.Limits,
		Cache:    s.Cache,
		System:   s.SystemPrompt(),
		Tools:    s.Tools,
		Turns:    s.Turns,
	})
	if err != nil {
		return nil, fmt.Errorf("scenario %s: render: %w", s.ID(), err)
	}
	return buf.Bytes(), nil
}
