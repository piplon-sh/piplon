package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/piplon-sh/piplon/internal/scenario"
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

// showScenario validates the embedded scenario, then prints the file parsed
// back as YAML so its comments stay visible.
func showScenario(cmd *cobra.Command, name string) error {
	if _, err := scenario.Load(name); err != nil {
		return err
	}
	raw, err := scenarios.FS.ReadFile(name + ".yaml")
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
