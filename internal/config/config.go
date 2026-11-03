package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is a fully resolved piplon.yaml.
type Config struct {
	Scenario    string        `yaml:"scenario"`
	Interval    time.Duration `yaml:"interval"`
	Timeout     time.Duration `yaml:"timeout"`
	Jitter      time.Duration `yaml:"jitter"`
	Concurrency int           `yaml:"concurrency"`
	Budget      Budget        `yaml:"budget"`
	Targets     []Target      `yaml:"targets"`
	Notify      Notify        `yaml:"notify"`
	Store       Store         `yaml:"store"`
	Serve       Serve         `yaml:"serve"`
}

// Budget caps what probing may spend per day.
type Budget struct {
	DailyUSD float64 `yaml:"daily_usd"`
}

// Target is one provider/model/region/path to probe.
type Target struct {
	Name        string        `yaml:"name"`
	Format      string        `yaml:"format"`
	URL         string        `yaml:"url"`
	Model       string        `yaml:"model"`
	Key         Secret        `yaml:"key"`
	Region      string        `yaml:"region"`
	Interval    time.Duration `yaml:"interval"` // inherited from Config.Interval when unset
	CompareWith string        `yaml:"compare_with"`
	Price       *Price        `yaml:"price"` // never nil after Parse
}

// Price is what a target charges, in USD per million tokens. InputTokens are
// the uncached ones; cache reads and writes are priced separately.
type Price struct {
	Input      float64 `yaml:"input"`
	Output     float64 `yaml:"output"`
	CacheRead  float64 `yaml:"cache_read"`
	CacheWrite float64 `yaml:"cache_write"`
}

// OllamaURL is where an ollama target without a url is probed: a local Ollama
// serves its OpenAI-compatible API there.
const OllamaURL = "http://localhost:11434/v1"

// DefaultPrice applies to targets without a price block. It is deliberately
// high so a missing price overestimates cost instead of overspending.
var DefaultPrice = Price{Input: 3.00, Output: 15.00, CacheRead: 0.30, CacheWrite: 3.75}

// Notify holds the notification channels. Webhook URLs embed tokens, so they
// are Secrets.
type Notify struct {
	Slack   Secret `yaml:"slack"`
	Discord Secret `yaml:"discord"`
	Webhook Secret `yaml:"webhook"`
}

// Store configures the local SQLite database.
type Store struct {
	Path      string   `yaml:"path"`
	Retention Duration `yaml:"retention"`
}

// Serve configures the HTTP endpoints. Listen is empty unless set: piplon
// run serves only when it is, and piplon serve falls back to DefaultListen.
type Serve struct {
	Listen string `yaml:"listen"`
}

// DefaultListen is where piplon serve listens without serve.listen or
// --listen: loopback only, so nothing is exposed to the network by default.
const DefaultListen = "127.0.0.1:9464"

func defaults() *Config {
	return &Config{
		Scenario:    "standard-v1",
		Interval:    5 * time.Minute,
		Timeout:     30 * time.Second,
		Jitter:      5 * time.Second,
		Concurrency: 4,
		Budget:      Budget{DailyUSD: 5.00},
		Store:       Store{Path: "./piplon.db", Retention: Duration(90 * 24 * time.Hour)},
	}
}

// Load reads and resolves the config file at path, expanding ${VAR} from the
// process environment.
func Load(path string) (*Config, error) { return LoadTargets(path, nil) }

// LoadTargets is Load keeping only the named targets, in config order; nil
// keeps them all. See ParseTargets.
func LoadTargets(path string, names []string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path is chosen by the operator
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	cfg, err := ParseTargets(data, os.LookupEnv, names)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// Parse resolves a config from YAML bytes. lookup resolves ${VAR} references;
// pass os.LookupEnv in production.
func Parse(data []byte, lookup func(string) (string, bool)) (*Config, error) {
	return ParseTargets(data, lookup, nil)
}

// ParseTargets is Parse keeping only the named targets, in config order; nil
// keeps them all. The others are dropped before their ${VAR} references are
// expanded, so their variables need not be set, and only their names are
// checked: unique, and valid for compare_with. A name not in the config is
// an error.
func ParseTargets(data []byte, lookup func(string) (string, bool), names []string) (*Config, error) {
	cfg := defaults()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	var dropped []Target
	if names != nil {
		var kept []Target
		for _, t := range cfg.Targets {
			if slices.Contains(names, t.Name) {
				kept = append(kept, t)
			} else {
				dropped = append(dropped, t)
			}
		}
		for _, n := range names {
			if !slices.ContainsFunc(kept, func(t Target) bool { return t.Name == n }) {
				return nil, fieldErr(targetScope(0, n), "name", invalid("is not in the config"))
			}
		}
		cfg.Targets = kept
	}
	errs := expandAll(cfg, lookup)
	for i := range cfg.Targets {
		if cfg.Targets[i].Interval == 0 {
			cfg.Targets[i].Interval = cfg.Interval
		}
		if cfg.Targets[i].Format == "ollama" && cfg.Targets[i].URL == "" {
			cfg.Targets[i].URL = OllamaURL
		}
		if cfg.Targets[i].Price == nil {
			p := DefaultPrice
			if cfg.Targets[i].Format == "ollama" {
				p = Price{} // local models are free; the default would exhaust the budget
			}
			cfg.Targets[i].Price = &p
		}
	}
	errs = append(errs, validate(cfg, dropped)...)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return cfg, nil
}
