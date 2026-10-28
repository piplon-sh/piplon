package probe

import "errors"

// ErrSetup means the Scheduler is missing something it needs to run.
var ErrSetup = errors.New("scheduler setup")

// ErrUnsupported means no adapter exists yet for a target's format.
var ErrUnsupported = errors.New("format not supported yet")
