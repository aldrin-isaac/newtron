package main

import (
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
			base := resolveTopologiesDir()
			entries, err := os.ReadDir(base)
			if err != nil {
				return fmt.Errorf("read topologies dir %s: %w", base, err)
			}
			fmt.Println("newtrun topologies")
			fmt.Println()
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "  TOPOLOGY")
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				if _, err := os.Stat(filepath.Join(base, e.Name(), "specs", "topology.json")); err != nil {
					continue
				}
				fmt.Fprintf(w, "  %s\n", e.Name())
			}
			return w.Flush()
		},
	}
}
