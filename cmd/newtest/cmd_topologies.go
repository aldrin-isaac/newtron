package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newTopologiesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "topologies",
		Short: "List available topologies",
		RunE: func(cmd *cobra.Command, args []string) error {
			topologiesDir := resolveTopologiesDir()

			entries, err := os.ReadDir(topologiesDir)
			if err != nil {
				return err
			}

			fmt.Printf("newtest topologies\n\n")
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "  TOPOLOGY\tDEVICES\tLINKS\tDESCRIPTION")
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}

				topoPath := filepath.Join(topologiesDir, e.Name(), "specs", "topology.json")
				data, readErr := os.ReadFile(topoPath)
				if readErr != nil {
					fmt.Fprintf(w, "  %s\t-\t-\t(no topology.json)\n", e.Name())
					continue
				}

				var topo topoSummary
				if jsonErr := json.Unmarshal(data, &topo); jsonErr != nil {
					fmt.Fprintf(w, "  %s\t-\t-\t(parse error)\n", e.Name())
					continue
				}

				desc := topo.Description
				if len(desc) > 50 {
					desc = desc[:47] + "..."
				}

				fmt.Fprintf(w, "  %s\t%d\t%d\t%s\n",
					e.Name(), len(topo.Devices), len(topo.Links), desc)
			}
			w.Flush()
			return nil
		},
	}
}
