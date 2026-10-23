package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// env returns a lookup function backed by a fixed map, so tests never depend
// on the real environment.
func env(vars map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

const minimal = `
targets:
  - name: anthropic-direct
    format: anthropic
    url: https://api.anthropic.com
    model: claude-sonnet-4-5
    key: ${ANTHROPIC_API_KEY}
`

func TestParseMinimalAppliesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(minimal), env(map[string]string{"ANTHROPIC_API_KEY": "sk-test"}))
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name      string
		got, want any
	}{
		{"scenario", cfg.Scenario, "standard-v1"},
		{"interval", cfg.Interval, 5 * time.Minute},
		{"timeout", cfg.Timeout, 30 * time.Second},
		{"jitter", cfg.Jitter, 5 * time.Second},
		{"concurrency", cfg.Concurrency, 4},
		{"budget", cfg.Budget.DailyUSD, 2.00},
		{"store.path", cfg.Store.Path, "./piplon.db"},
		{"store.retention", cfg.Store.Retention, Duration(90 * 24 * time.Hour)},
		{"target interval", cfg.Targets[0].Interval, 5 * time.Minute},
		{"target key", cfg.Targets[0].Key.Reveal(), "sk-test"},
		{"target price", *cfg.Targets[0].Price, DefaultPrice},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestParseFullExample(t *testing.T) {
	const full = `
scenario: standard-v1
interval: 60s
budget:
  daily_usd: 2.50
targets:
  - name: anthropic-direct
    format: anthropic
    url: https://api.anthropic.com
    model: claude-sonnet-4-5
    key: ${ANTHROPIC_API_KEY}
  - name: anthropic-via-gateway
    format: openai
    url: https://litellm.internal/v1
    model: claude-sonnet-4-5
    key: ${LITELLM_KEY}
    compare_with: anthropic-direct
  - name: bedrock-eu-west-1
    format: bedrock
    region: eu-west-1
    model: anthropic.claude-sonnet-4-5
    interval: 5m
notify:
  slack: ${SLACK_WEBHOOK}
store:
  path: ./piplon.db
  retention: 30d
`
	cfg, err := Parse([]byte(full), env(map[string]string{
		"ANTHROPIC_API_KEY": "sk-a",
		"LITELLM_KEY":       "sk-l",
		"SLACK_WEBHOOK":     "https://hooks.slack.com/services/T/B/X",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Targets) != 3 {
		t.Fatalf("got %d targets, want 3", len(cfg.Targets))
	}
	if got := cfg.Targets[2].Interval; got != 5*time.Minute {
		t.Errorf("bedrock interval: got %v, want 5m", got)
	}
	if got := cfg.Targets[1].CompareWith; got != "anthropic-direct" {
		t.Errorf("compare_with: got %q", got)
	}
	if got := cfg.Budget.DailyUSD; got != 2.50 {
		t.Errorf("budget: got %v, want 2.50", got)
	}
	if got := cfg.Store.Retention; got != Duration(30*24*time.Hour) {
		t.Errorf("retention: got %v, want 30d", got)
	}
	if got := cfg.Notify.Slack.Reveal(); got != "https://hooks.slack.com/services/T/B/X" {
		t.Errorf("slack: got %q", got)
	}
}

// target renders a single-target config with the given extra or replaced lines.
func target(lines string) string {
	return "targets:\n  - name: t1\n" + lines
}

const okTarget = "    format: anthropic\n    url: https://api.anthropic.com\n    model: m\n    key: k\n"

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		env     map[string]string
		wantErr error
		wantMsg string
	}{
		{
			name:    "missing env var names target, field and variable",
			yaml:    minimal,
			wantErr: ErrMissingEnv,
			wantMsg: "target anthropic-direct: key: environment variable ANTHROPIC_API_KEY is not set",
		},
		{
			name:    "empty env var is not a silent empty key",
			yaml:    minimal,
			env:     map[string]string{"ANTHROPIC_API_KEY": ""},
			wantErr: ErrMissingEnv,
			wantMsg: "target anthropic-direct: key: environment variable ANTHROPIC_API_KEY is empty",
		},
		{
			name:    "unknown field is rejected",
			yaml:    target(okTarget + "    model_name: m\n"),
			wantMsg: "field model_name not found",
		},
		{
			name:    "no targets",
			yaml:    "interval: 60s\n",
			wantErr: ErrRequired,
			wantMsg: "targets: at least one target is required",
		},
		{
			name:    "missing name",
			yaml:    "targets:\n  - format: anthropic\n    url: https://a\n    model: m\n    key: k\n",
			wantErr: ErrRequired,
			wantMsg: "targets[0]: name: is required",
		},
		{
			name:    "invalid name",
			yaml:    "targets:\n  - name: Bad Name\n" + okTarget,
			wantErr: ErrInvalid,
			wantMsg: `target "Bad Name": name: must match`,
		},
		{
			name:    "duplicate name",
			yaml:    target(okTarget) + "  - name: t1\n" + okTarget,
			wantErr: ErrInvalid,
			wantMsg: "target t1: name: duplicate of targets[0]",
		},
		{
			name:    "unknown format",
			yaml:    target("    format: grpc\n    url: https://a\n    model: m\n"),
			wantErr: ErrInvalid,
			wantMsg: `target t1: format: "grpc" is not one of anthropic, openai, bedrock, google, ollama`,
		},
		{
			name:    "missing format",
			yaml:    target("    url: https://a\n    model: m\n"),
			wantErr: ErrRequired,
			wantMsg: "target t1: format: is required",
		},
		{
			name:    "missing model",
			yaml:    target("    format: openai\n    url: https://a\n"),
			wantErr: ErrRequired,
			wantMsg: "target t1: model: is required",
		},
		{
			name:    "missing url",
			yaml:    target("    format: openai\n    model: m\n"),
			wantErr: ErrRequired,
			wantMsg: "target t1: url: is required for format openai",
		},
		{
			name:    "url without http scheme",
			yaml:    target("    format: openai\n    url: ftp://a\n    model: m\n"),
			wantErr: ErrInvalid,
			wantMsg: "target t1: url: must be an http or https URL with a host",
		},
		{
			name:    "anthropic needs a key",
			yaml:    target("    format: anthropic\n    url: https://a\n    model: m\n"),
			wantErr: ErrRequired,
			wantMsg: "target t1: key: is required for format anthropic",
		},
		{
			name:    "bedrock needs a region",
			yaml:    target("    format: bedrock\n    model: m\n"),
			wantErr: ErrRequired,
			wantMsg: "target t1: region: is required for format bedrock",
		},
		{
			name:    "bedrock rejects a key",
			yaml:    target("    format: bedrock\n    region: eu-west-1\n    model: m\n    key: k\n"),
			wantErr: ErrInvalid,
			wantMsg: "target t1: key: is not used by format bedrock",
		},
		{
			name:    "compare_with unknown target",
			yaml:    target(okTarget + "    compare_with: nope\n"),
			wantErr: ErrInvalid,
			wantMsg: `target t1: compare_with: no target named "nope"`,
		},
		{
			name:    "compare_with itself",
			yaml:    target(okTarget + "    compare_with: t1\n"),
			wantErr: ErrInvalid,
			wantMsg: "target t1: compare_with: cannot compare a target with itself",
		},
		{
			name:    "global interval too short",
			yaml:    "interval: 5s\n" + target(okTarget),
			wantErr: ErrInvalid,
			wantMsg: "interval: 5s is outside 10s..1h",
		},
		{
			name:    "target interval too long",
			yaml:    target(okTarget + "    interval: 2h\n"),
			wantErr: ErrInvalid,
			wantMsg: "target t1: interval: 2h0m0s is outside 10s..1h",
		},
		{
			name:    "jitter not below interval",
			yaml:    "interval: 60s\njitter: 60s\n" + target(okTarget),
			wantErr: ErrInvalid,
			wantMsg: "jitter: 1m0s must be at least 0 and below interval 1m0s",
		},
		{
			name:    "timeout out of range",
			yaml:    "timeout: 10m\n" + target(okTarget),
			wantErr: ErrInvalid,
			wantMsg: "timeout: 10m0s is outside 1s..5m",
		},
		{
			name:    "concurrency out of range",
			yaml:    "concurrency: 0\n" + target(okTarget),
			wantErr: ErrInvalid,
			wantMsg: "concurrency: 0 is outside 1..64",
		},
		{
			name:    "budget zero",
			yaml:    "budget:\n  daily_usd: 0\n" + target(okTarget),
			wantErr: ErrInvalid,
			wantMsg: "budget.daily_usd: must be greater than 0",
		},
		{
			name:    "retention too short",
			yaml:    "store:\n  retention: 1h\n" + target(okTarget),
			wantErr: ErrInvalid,
			wantMsg: "store.retention: must be at least 1d",
		},
		{
			name:    "retention unparseable",
			yaml:    "store:\n  retention: forever\n" + target(okTarget),
			wantMsg: `invalid duration "forever"`,
		},
		{
			name:    "unknown scenario",
			yaml:    "scenario: standard-v9\n" + target(okTarget),
			wantErr: ErrInvalid,
			wantMsg: `scenario: "standard-v9" is not a shipped scenario`,
		},
		{
			name:    "negative price",
			yaml:    target(okTarget + "    price: {input: -1}\n"),
			wantErr: ErrInvalid,
			wantMsg: "target t1: price.input: must be at least 0",
		},
		{
			name:    "notify env var missing",
			yaml:    target(okTarget) + "notify:\n  slack: ${SLACK_WEBHOOK}\n",
			wantErr: ErrMissingEnv,
			wantMsg: "notify: slack: environment variable SLACK_WEBHOOK is not set",
		},
		{
			name:    "notify url invalid",
			yaml:    target(okTarget) + "notify:\n  discord: not-a-url\n",
			wantErr: ErrInvalid,
			wantMsg: "notify: discord: must be an http or https URL with a host",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml), env(tt.env))
			if err == nil {
				t.Fatal("expected an error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want it to wrap %v", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error %q\ndoes not contain %q", err, tt.wantMsg)
			}
		})
	}
}

func TestPartialPriceLeavesOmittedFieldsAtZero(t *testing.T) {
	cfg, err := Parse([]byte(target(okTarget+"    price: {input: 0.5, output: 2}\n")), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := Price{Input: 0.5, Output: 2}
	if got := *cfg.Targets[0].Price; got != want {
		t.Errorf("price: got %+v, want %+v", got, want)
	}
}

func TestParseReportsEveryError(t *testing.T) {
	yaml := "targets:\n" +
		"  - name: a\n    format: openai\n    url: https://a\n" +
		"  - name: b\n    format: openai\n    model: m\n"
	_, err := Parse([]byte(yaml), env(nil))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"target a: model", "target b: url"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestErrorsNeverEchoSecrets(t *testing.T) {
	const secret = "sk-very-secret"
	yaml := target("    format: openai\n    url: ftp://h/?key="+secret+"\n    key: ${K}\n") +
		"notify:\n  webhook: " + secret + "\n"
	_, err := Parse([]byte(yaml), env(map[string]string{"K": secret}))
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks a secret: %q", err)
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "piplon.yaml")
	if err := os.WriteFile(path, []byte(minimal), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-env")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Targets[0].Key.Reveal(); got != "sk-env" {
		t.Errorf("key: got %q, want sk-env", got)
	}
}

func TestLoadMissingFileNamesPath(t *testing.T) {
	_, err := Load("/nonexistent/piplon.yaml")
	if err == nil || !strings.Contains(err.Error(), "/nonexistent/piplon.yaml") {
		t.Fatalf("got %v, want an error naming the path", err)
	}
}
