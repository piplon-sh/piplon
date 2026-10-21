// Package version holds build information injected at link time by goreleaser
// or the Makefile. Defaults are used for `go run` and `go install` builds.
package version

import (
	"fmt"
	"runtime"
)

// Set via -ldflags "-X github.com/piplon-sh/piplon/internal/version.Version=...".
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a single-line, human-readable version string.
func String() string {
	return fmt.Sprintf("piplon %s (%s) %s %s/%s", Version, Commit, Date, runtime.GOOS, runtime.GOARCH)
}
