package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtest"
)

func newListCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "list [suite]",
		Short: "List available scenarios",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var positional string
			if len(args) > 0 {
				positional = args[0]
			}
			dir = resolveDir(cmd, dir, positional)

			scenarios, err := newtest.ParseAllScenarios(dir)
			if err != nil {
				return err
			}
			if len(scenarios) == 0 {
				fmt.Printf("No scenarios found in %s/\n", dir)
				return nil
			}

			// Sort by dependency order if applicable
			if newtest.HasRequiresExported(scenarios) {
				sorted, sortErr := newtest.TopologicalSortExported(scenarios)
				if sortErr == nil {
					scenarios = sorted
				}
			}

			// Derive suite name from dir
			suiteName := dir
			if idx := strings.LastIndex(dir, "/"); idx >= 0 {
				suiteName = dir[idx+1:]
			}

			fmt.Printf("Suite: %s (%d scenarios)\n\n", suiteName, len(scenarios))

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "  #\tSCENARIO\tSTEPS\tTOPOLOGY\tREQUIRES")
			for i, s := range scenarios {
				requires := "-"
				if len(s.Requires) > 0 {
					requires = strings.Join(s.Requires, ", ")
				}
				fmt.Fprintf(w, "  %d\t%s\t%d\t%s\t%s\n",
					i+1, s.Name, len(s.Steps), s.Topology, requires)
			}
			w.Flush()
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "directory containing scenario YAML files")

	return cmd
}
