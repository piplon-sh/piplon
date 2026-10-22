package config

import "errors"

// Sentinel errors. Every validation error wraps exactly one of them.
var (
	// ErrMissingEnv means a ${VAR} reference points to an unset or empty variable.
	ErrMissingEnv = errors.New("environment variable missing")
	// ErrRequired means a mandatory field is absent.
	ErrRequired = errors.New("required field missing")
	// ErrInvalid means a field is present but its value is not acceptable.
	ErrInvalid = errors.New("invalid value")
)

// kindError carries a human message while still matching a sentinel with
// errors.Is, so messages read naturally without a trailing "invalid value".
type kindError struct {
	kind error
	msg  string
}

func (e *kindError) Error() string { return e.msg }
func (e *kindError) Unwrap() error { return e.kind }
