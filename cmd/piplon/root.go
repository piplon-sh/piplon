package main

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/piplon-sh/piplon/internal/version"
)

// errNotImplemented is returned by subcommands that exist only as stubs.
var errNotImplemented = errors.New("not implemented yet (see docs/ROADMAP.md)")

// newRootCmd builds the full command tree. A fresh tree per call keeps tests
// independent of each other.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "piplon",
		Short:         "Independent blackbox probe for LLM provider APIs",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.AddCommand(newVersionCmd(), newRunCmd(), newScenarioCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version and build information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Println(version.String())
		},
	}
}

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Probe every configured target on a schedule",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return errNotImplemented
		},
	}
}
