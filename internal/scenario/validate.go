package scenario

import (
	"errors"
	"fmt"
)

func (s *Scenario) validate() error {
	var errs []error
	bad := func(field, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: %s: %w", field, fmt.Sprintf(format, args...), ErrInvalid))
	}

	if s.Name == "" {
		bad("name", "is required")
	}
	if s.Version < 1 {
		bad("version", "must be at least 1")
	}
	if s.System == "" {
		bad("system", "is required")
	}
	if s.Limits.MaxOutputTokens < 1 {
		bad("limits.max_output_tokens", "must be at least 1")
	}
	if s.Limits.Timeout <= 0 {
		bad("limits.timeout", "must be greater than 0")
	}
	if s.Padding.Seed == "" {
		bad("padding.seed", "is required")
	}
	if c := s.Cache.ExpectHitFromTurn; c < 0 || c > len(s.Turns) {
		bad("cache.expect_hit_from_turn", "%d is outside 0..%d", c, len(s.Turns))
	}
	seen := make(map[string]bool, len(s.Tools))
	for i, t := range s.Tools {
		switch {
		case t.Name == "":
			bad(fmt.Sprintf("tools[%d].name", i), "is required")
		case seen[t.Name]:
			bad(fmt.Sprintf("tools[%d].name", i), "duplicate %q", t.Name)
		}
		seen[t.Name] = true
	}
	if len(s.Turns) == 0 {
		bad("turns", "at least one turn is required")
	}
	for i, t := range s.Turns {
		if t.Role != "user" {
			bad(fmt.Sprintf("turns[%d].role", i), "%q must be user", t.Role)
		}
		if t.Content == "" {
			bad(fmt.Sprintf("turns[%d].content", i), "is required")
		}
	}
	// Sizing needs turn 1, so it is checked only once the rest is sound.
	if len(errs) == 0 {
		if base := estimateTokens(s.baseText()); s.Padding.TargetInputTokens <= base {
			bad("padding.target_input_tokens", "%d is below the estimated %d tokens of system, tools and turn 1",
				s.Padding.TargetInputTokens, base)
		}
	}
	return errors.Join(errs...)
}
