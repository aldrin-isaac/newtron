package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "list [suite]",
		Short: "List suites or scenarios",
		Long: `Without arguments, lists all available test suites known to newtrun-server.
With a suite name, lists the scenarios in that suite.

  newtrun list                          # show all suites
  newtrun list 2node-ngdp-incremental   # show scenarios in suite`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			ctx := cmd.Context()
			if err := requireServer(ctx, c); err != nil {
				return err
			}

			if len(args) == 0 && dir == "" {
				return listSuites(ctx)
			}

			suite := dir
			if len(args) > 0 && args[0] != "" {
				suite = args[0]
			}
			return listScenarios(ctx, suite)
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "suite name (alternative to positional arg)")

	return cmd
}

func listSuites(ctx context.Context) error {
	c := newClient()
	names, err := c.ListSuites(ctx)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Println("no suites found")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  SUITE\tSCENARIOS\tTOPOLOGY")
	for _, name := range names {
		summary, err := c.ListSuiteScenarios(ctx, name)
		if err != nil || summary == nil || len(summary.Scenarios) == 0 {
			continue
		}
		fmt.Fprintf(w, "  %s\t%d\t%s\n",
			name, len(summary.Scenarios), summary.Topology)
	}
	return w.Flush()
}

func listScenarios(ctx context.Context, suite string) error {
	c := newClient()
	resp, err := c.ListSuiteScenarios(ctx, suite)
	if err != nil {
		return err
	}
	if resp == nil || len(resp.Scenarios) == 0 {
		fmt.Printf("No scenarios found in suite %q\n", suite)
		return nil
	}
	// Topology + Platform live on the response envelope (suite-
	// level), not on each ScenarioSummary. The header carries the
	// suite metadata; the per-scenario rows carry the rest.
	header := fmt.Sprintf("Suite: %s  topology=%s", resp.Suite, resp.Topology)
	if resp.Platform != "" {
		header += "  platform=" + resp.Platform
	}
	fmt.Printf("%s  (%d scenarios)\n\n", header, len(resp.Scenarios))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  #\tSCENARIO\tSTEPS\tREQUIRES")
	for i, s := range resp.Scenarios {
		requires := "-"
		if len(s.Requires) > 0 {
			requires = strings.Join(s.Requires, ", ")
		}
		fmt.Fprintf(w, "  %d\t%s\t%d\t%s\n",
			i+1, s.Name, s.StepCount, requires)
	}
	return w.Flush()
}
