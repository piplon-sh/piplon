package main

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/piplon-sh/piplon/scenarios"
)

const defaultScenario = "standard-v1"

func newScenarioCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scenario",
		Short: "Inspect the shipped request scenarios",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show [name]",
		Short: "Print a scenario as parsed YAML (default " + defaultScenario + ")",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := defaultScenario
			if len(args) == 1 {
				name = args[0]
			}
			return showScenario(cmd, name)
		},
	})
	return cmd
}

// showScenario parses the embedded scenario and prints it back as YAML, so a
// file that does not parse fails here rather than at probe time.
func showScenario(cmd *cobra.Command, name string) error {
	raw, err := scenarios.FS.ReadFile(name + ".yaml")
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("scenario %q not found; available: %s", name, availableScenarios())
	}
	if err != nil {
		return fmt.Errorf("scenario %s: %w", name, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("scenario %s: parse: %w", name, err)
	}
	enc := yaml.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return fmt.Errorf("scenario %s: print: %w", name, err)
	}
	return enc.Close()
}

func availableScenarios() string {
	files, _ := fs.Glob(scenarios.FS, "*.yaml")
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = strings.TrimSuffix(path.Base(f), ".yaml")
	}
	return strings.Join(names, ", ")
}
