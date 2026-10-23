// Package config loads and validates piplon.yaml.
//
// Load reads a file and Parse reads bytes; both return a fully resolved
// *Config: defaults applied, ${VAR} references expanded from the environment,
// per-target intervals inherited from the global one, and every field
// validated. All problems are reported at once, each naming the target and the
// field, for example:
//
//	target anthropic-direct: key: environment variable ANTHROPIC_API_KEY is not set
//
// Errors wrap ErrMissingEnv, ErrRequired or ErrInvalid. Unknown fields are
// rejected so a typo never silently disables a setting.
//
// Only ${VAR} is expanded (never $VAR), and only in string fields: target url,
// model, region and key, notify URLs and store.path. A variable that is unset
// or empty is an error. Keys and webhook URLs are held as Secret, which renders
// as [redacted] in fmt, slog and YAML; error messages never echo user values.
//
// Defaults: scenario standard-v1, interval 5m (10s..1h, overridable per
// target), timeout 30s (1s..5m), jitter 5s (below interval), concurrency 4
// (1..64), budget.daily_usd 2.00 (> 0), store.path ./piplon.db,
// store.retention 90d (>= 1d).
//
// Each target may set price (USD per million tokens: input, output,
// cache_read, cache_write, all >= 0). Without a price block a target gets
// DefaultPrice, deliberately high; inside a block, omitted fields are 0.
package config
