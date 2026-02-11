package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtest"
)

// suiteInfo holds summary data for a discovered suite.
type suiteInfo struct {
	Name      string
	Scenarios int
	Topology  string
}

func newSuitesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "suites",
		Short: "List available test suites",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Scan parent dir of suites (default: newtest/suites/)
			topologiesDir := resolveTopologiesDir()
			suitesBase := filepath.Dir(resolveDir(cmd, ""))

			// If we resolved to a specific suite, go up one level
			// The base is typically newtest/suites/
			entries, err := os.ReadDir(suitesBase)
			if err != nil {
				// Try the default parent
				suitesBase = "newtest/suites"
				entries, err = os.ReadDir(suitesBase)
				if err != nil {
					return fmt.Errorf("cannot find suites directory: %w", err)
				}
			}

			var suites []suiteInfo
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				dir := filepath.Join(suitesBase, e.Name())
				scenarios, parseErr := newtest.ParseAllScenarios(dir)
				if parseErr != nil || len(scenarios) == 0 {
					continue
				}

				topology := scenarios[0].Topology
				suites = append(suites, suiteInfo{
					Name:      e.Name(),
					Scenarios: len(scenarios),
					Topology:  topology,
				})
			}

			if len(suites) == 0 {
				fmt.Printf("No suites found in %s/\n", suitesBase)
				return nil
			}

			fmt.Printf("newtest suites\n\n")
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "  SUITE\tSCENARIOS\tTOPOLOGY\tDEVICES\tLINKS")
			for _, s := range suites {
				devices, links := topoCounts(topologiesDir, s.Topology)
				fmt.Fprintf(w, "  %s\t%d\t%s\t%d\t%d\n",
					s.Name, s.Scenarios, s.Topology, devices, links)
			}
			w.Flush()
			return nil
		},
	}
}

// topoCounts returns device and link counts from a topology.json.
func topoCounts(topologiesDir, topology string) (int, int) {
	path := filepath.Join(topologiesDir, topology, "specs", "topology.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0
	}
	var topo struct {
		Devices map[string]json.RawMessage `json:"devices"`
		Links   []json.RawMessage          `json:"links"`
	}
	if err := json.Unmarshal(data, &topo); err != nil {
		return 0, 0
	}
	return len(topo.Devices), len(topo.Links)
}
