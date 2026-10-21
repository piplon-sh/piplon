// Command piplon is an independent blackbox probe for LLM provider APIs.
//
// This is the entry point only. Subcommands are implemented in cmd/piplon/*.go
// and the real work lives under internal/.
package main

import (
	"fmt"
	"os"

	"github.com/piplon-sh/piplon/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "piplon:", err)
		os.Exit(1)
	}
}

// run is a placeholder dispatcher. It is replaced by cobra in roadmap week 1.
func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no command given; try: piplon version")
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Println(version.String())
		return nil
	default:
		return fmt.Errorf("unknown command %q (nothing is implemented yet; see docs/ROADMAP.md)", args[0])
	}
}
