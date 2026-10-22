// Package scenarios embeds the shipped scenario files into the binary, so
// `piplon` never depends on the repository layout at run time.
package scenarios

import "embed"

// FS holds every shipped scenario, one file per version (for example
// standard-v1.yaml). Files are never edited in place: a change is a new version.
//
//go:embed *.yaml
var FS embed.FS
