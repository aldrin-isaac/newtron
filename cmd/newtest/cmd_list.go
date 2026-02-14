package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtest"
)

// topoSummary is a lightweight view of topology.json for counting
// devices, links, and showing a description.
type topoSummary struct {
	Description string                     `json:"description"`
	Devices     map[string]json.RawMessage `json:"devices"`
	Links       []json.RawMessage          `json:"links"`
}

// topoCounts returns device and link counts from a topology.json.
func topoCounts(topologiesDir, topology string) (int, int) {
	path := filepath.Join(topologiesDir, topology, "specs", "topology.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0
	}
	var topo topoSummary
	if err := json.Unmarshal(data, &topo); err != nil {
		return 0, 0
	}
	return len(topo.Devices), len(topo.Links)
}

func newListCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "list [suite]",
		Short: "List suites or scenarios",
		Long: `Without arguments, lists all available test suites.
With a suite name, lists the scenarios in that suite.

  newtest list                     # show all suites
  newtest list 2node-incremental   # show scenarios in suite`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// If no positional arg and no --dir, show all suites
			if len(args) == 0 && !cmd.Flags().Changed("dir") {
				return listSuites()
			}

			// Otherwise, list scenarios in the specified suite
			var positional string
			if len(args) > 0 {
				positional = args[0]
			}
			dir = resolveDir(cmd, dir, positional)
			return listScenarios(dir)
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "directory containing scenario YAML files")

	return cmd
}

func listSuites() error {
	topologiesDir := resolveTopologiesDir()
	base := suitesBaseDir()

	entries, err := os.ReadDir(base)
	if err != nil {
		return fmt.Errorf("cannot find suites directory: %w", err)
	}

	type info struct {
		Name      string
		Scenarios int
		Topology  string
	}
	var suites []info
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, e.Name())
		scenarios, parseErr := newtest.ParseAllScenarios(dir)
		if parseErr != nil || len(scenarios) == 0 {
			continue
		}
		suites = append(suites, info{
			Name:      e.Name(),
			Scenarios: len(scenarios),
			Topology:  scenarios[0].Topology,
		})
	}

	if len(suites) == 0 {
		fmt.Printf("No suites found in %s/\n", base)
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  SUITE\tSCENARIOS\tTOPOLOGY\tDEVICES\tLINKS")
	for _, s := range suites {
		devices, links := topoCounts(topologiesDir, s.Topology)
		fmt.Fprintf(w, "  %s\t%d\t%s\t%d\t%d\n",
			s.Name, s.Scenarios, s.Topology, devices, links)
	}
	w.Flush()
	return nil
}

func listScenarios(dir string) error {
	scenarios, err := newtest.ParseAllScenarios(dir)
	if err != nil {
		return err
	}
	if len(scenarios) == 0 {
		fmt.Printf("No scenarios found in %s/\n", dir)
		return nil
	}

	// Sort by dependency order if applicable
	if newtest.HasRequires(scenarios) {
		sorted, sortErr := newtest.ValidateDependencyGraph(scenarios)
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
}
