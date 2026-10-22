package config

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const redacted = "[redacted]"

// Secret holds a credential or a URL that embeds one. It never renders its
// value: use Reveal only at the point where the value is sent to a provider.
type Secret string

// Reveal returns the raw value.
func (s Secret) Reveal() string { return string(s) }

// String implements fmt.Stringer and hides the value.
func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return redacted
}

// GoString hides the value from %#v.
func (s Secret) GoString() string { return s.String() }

// LogValue hides the value from slog.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

// MarshalYAML hides the value when a config is printed back.
func (s Secret) MarshalYAML() (any, error) { return s.String(), nil }

// Duration is a time.Duration that also accepts whole days ("90d"), for
// settings such as retention where hours are an awkward unit.
type Duration time.Duration

// UnmarshalYAML accepts Go units ("36h") or whole days ("90d").
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	s := node.Value
	invalid := fmt.Errorf("line %d: invalid duration %q: use Go units (36h) or whole days (90d)", node.Line, s)
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 {
			return invalid
		}
		*d = Duration(time.Duration(n) * 24 * time.Hour)
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil || v < 0 {
		return invalid
	}
	*d = Duration(v)
	return nil
}

// String renders whole days as "Nd" and anything else in Go units.
func (d Duration) String() string {
	const day = 24 * time.Hour
	if v := time.Duration(d); v > 0 && v%day == 0 {
		return fmt.Sprintf("%dd", v/day)
	}
	return time.Duration(d).String()
}
