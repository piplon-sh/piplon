package probe

import "errors"

// ErrSetup means the Scheduler is missing something it needs to run.
var ErrSetup = errors.New("scheduler setup")
