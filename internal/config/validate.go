package config

import (
	"fmt"
	"io/fs"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/piplon-sh/piplon/scenarios"
)

const (
	minInterval    = 10 * time.Second
	maxInterval    = time.Hour
	minTimeout     = time.Second
	maxTimeout     = 5 * time.Minute
	maxConcurrency = 64
	minRetention   = 24 * time.Hour
)

// namePattern keeps target names safe as metric labels and file names.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// formatRule says which fields a wire format needs.
type formatRule struct {
	needsURL, needsKey, needsRegion, noKey bool
}

var formatNames = []string{"anthropic", "openai", "bedrock", "google", "ollama"}

var formats = map[string]formatRule{
	"anthropic": {needsURL: true, needsKey: true},
	"openai":    {needsURL: true},
	"bedrock":   {needsRegion: true, noKey: true},
	"google":    {needsURL: true, needsKey: true},
	"ollama":    {needsURL: true},
}

func required(format string, args ...any) error {
	return &kindError{ErrRequired, fmt.Sprintf(format, args...)}
}

func invalid(format string, args ...any) error {
	return &kindError{ErrInvalid, fmt.Sprintf(format, args...)}
}

// fieldErr prefixes err with where it happened: "target x: key: ...".
func fieldErr(scope, field string, err error) error {
	if scope == "" {
		return fmt.Errorf("%s: %w", field, err)
	}
	return fmt.Errorf("%s: %s: %w", scope, field, err)
}

// targetScope names a target in errors, falling back to its index when the
// name is missing.
func targetScope(i int, name string) string {
	switch {
	case name == "":
		return fmt.Sprintf("targets[%d]", i)
	case namePattern.MatchString(name):
		return "target " + name
	default:
		return fmt.Sprintf("target %q", name)
	}
}

func validate(cfg *Config) []error {
	var errs []error
	add := func(scope, field string, err error) { errs = append(errs, fieldErr(scope, field, err)) }

	if _, err := fs.Stat(scenarios.FS, cfg.Scenario+".yaml"); err != nil {
		add("", "scenario", invalid("%q is not a shipped scenario", cfg.Scenario))
	}
	if err := checkInterval(cfg.Interval); err != nil {
		add("", "interval", err)
	}
	if cfg.Timeout < minTimeout || cfg.Timeout > maxTimeout {
		add("", "timeout", invalid("%v is outside 1s..5m", cfg.Timeout))
	}
	if cfg.Jitter < 0 || cfg.Jitter >= cfg.Interval {
		add("", "jitter", invalid("%v must be at least 0 and below interval %v", cfg.Jitter, cfg.Interval))
	}
	if cfg.Concurrency < 1 || cfg.Concurrency > maxConcurrency {
		add("", "concurrency", invalid("%d is outside 1..%d", cfg.Concurrency, maxConcurrency))
	}
	if cfg.Budget.DailyUSD <= 0 {
		add("", "budget.daily_usd", invalid("must be greater than 0"))
	}
	if cfg.Store.Path == "" {
		add("", "store.path", required("is required"))
	}
	if time.Duration(cfg.Store.Retention) < minRetention {
		add("", "store.retention", invalid("must be at least 1d"))
	}
	for _, n := range []struct {
		field string
		value Secret
	}{{"slack", cfg.Notify.Slack}, {"discord", cfg.Notify.Discord}, {"webhook", cfg.Notify.Webhook}} {
		if n.value != "" && !isHTTPURL(n.value.Reveal()) {
			add("notify", n.field, invalid("must be an http or https URL with a host"))
		}
	}
	if len(cfg.Targets) == 0 {
		add("", "targets", required("at least one target is required"))
	}
	return append(errs, validateTargets(cfg)...)
}

func validateTargets(cfg *Config) []error {
	var errs []error
	seen := make(map[string]int, len(cfg.Targets))
	for i, t := range cfg.Targets {
		if j, dup := seen[t.Name]; dup && t.Name != "" {
			errs = append(errs, fieldErr(targetScope(i, t.Name), "name", invalid("duplicate of targets[%d]", j)))
			continue
		}
		seen[t.Name] = i
	}
	for i, t := range cfg.Targets {
		scope := targetScope(i, t.Name)
		for _, e := range checkTarget(t, cfg, seen) {
			errs = append(errs, fieldErr(scope, e.field, e.err))
		}
	}
	return errs
}

type problem struct {
	field string
	err   error
}

func checkTarget(t Target, cfg *Config, names map[string]int) []problem {
	var ps []problem
	add := func(field string, err error) { ps = append(ps, problem{field, err}) }

	switch {
	case t.Name == "":
		add("name", required("is required"))
	case !namePattern.MatchString(t.Name):
		add("name", invalid("must match %s", namePattern))
	}
	if t.Model == "" {
		add("model", required("is required"))
	}
	if err := checkInterval(t.Interval); err != nil {
		add("interval", err)
	} else if t.Interval <= cfg.Jitter {
		add("interval", invalid("%v must be greater than jitter %v", t.Interval, cfg.Jitter))
	}
	for _, f := range []struct {
		field string
		usd   float64
	}{
		{"price.input", t.Price.Input}, {"price.output", t.Price.Output},
		{"price.cache_read", t.Price.CacheRead}, {"price.cache_write", t.Price.CacheWrite},
	} {
		if f.usd < 0 {
			add(f.field, invalid("must be at least 0"))
		}
	}
	switch _, ok := names[t.CompareWith]; {
	case t.CompareWith == "":
	case t.CompareWith == t.Name:
		add("compare_with", invalid("cannot compare a target with itself"))
	case !ok:
		add("compare_with", invalid("no target named %q", t.CompareWith))
	}

	if t.Format == "" {
		return append(ps, problem{"format", required("is required")})
	}
	rule, ok := formats[t.Format]
	if !ok {
		return append(ps, problem{"format", invalid("%q is not one of %s", t.Format, strings.Join(formatNames, ", "))})
	}
	switch {
	case rule.needsURL && t.URL == "":
		add("url", required("is required for format %s", t.Format))
	case t.URL != "" && !isHTTPURL(t.URL):
		add("url", invalid("must be an http or https URL with a host"))
	}
	switch {
	case rule.needsKey && t.Key == "":
		add("key", required("is required for format %s", t.Format))
	case rule.noKey && t.Key != "":
		add("key", invalid("is not used by format %s", t.Format))
	}
	if rule.needsRegion && t.Region == "" {
		add("region", required("is required for format %s", t.Format))
	}
	return ps
}

func checkInterval(d time.Duration) error {
	if d < minInterval || d > maxInterval {
		return invalid("%v is outside 10s..1h", d)
	}
	return nil
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
