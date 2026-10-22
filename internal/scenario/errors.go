package scenario

import "errors"

var (
	// ErrNotFound means no shipped scenario has the requested name.
	ErrNotFound = errors.New("not found")
	// ErrInvalid means a scenario file parsed but its content is not usable.
	ErrInvalid = errors.New("invalid scenario")
)
