package config

import (
	"fmt"
	"regexp"
)

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expand replaces every ${VAR} in s. Anything else, including $VAR, is kept
// literally so keys that contain a dollar sign survive.
func expand(s string, lookup func(string) (string, bool)) (string, error) {
	var firstErr error
	out := envRef.ReplaceAllStringFunc(s, func(ref string) string {
		name := envRef.FindStringSubmatch(ref)[1]
		v, ok := lookup(name)
		switch {
		case firstErr != nil:
		case !ok:
			firstErr = &kindError{ErrMissingEnv, fmt.Sprintf("environment variable %s is not set", name)}
		case v == "":
			firstErr = &kindError{ErrMissingEnv, fmt.Sprintf("environment variable %s is empty", name)}
		}
		return v
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

// expandAll expands every string field that may hold a ${VAR} reference.
func expandAll(cfg *Config, lookup func(string) (string, bool)) []error {
	var errs []error
	field := func(scope, name string, p *string) {
		v, err := expand(*p, lookup)
		if err != nil {
			errs = append(errs, fieldErr(scope, name, err))
			return
		}
		*p = v
	}
	for i := range cfg.Targets {
		t := &cfg.Targets[i]
		scope := targetScope(i, t.Name)
		field(scope, "url", &t.URL)
		field(scope, "model", &t.Model)
		field(scope, "region", &t.Region)
		// (*string)(&x) views a Secret as its underlying string, in place.
		field(scope, "key", (*string)(&t.Key))
	}
	field("notify", "slack", (*string)(&cfg.Notify.Slack))
	field("notify", "discord", (*string)(&cfg.Notify.Discord))
	field("notify", "webhook", (*string)(&cfg.Notify.Webhook))
	field("", "store.path", &cfg.Store.Path)
	return errs
}
