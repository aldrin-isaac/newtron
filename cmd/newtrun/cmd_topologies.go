package main

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newTopologiesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "topologies",
		Short: "List available topologies known to newtrun-server",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			ctx := cmd.Context()
			if err := requireServer(ctx, c); err != nil {
				return err
			}
			names, err := c.ListTopologies(ctx)
			if err != nil {
				return err
			}
			fmt.Println("newtrun topologies")
			fmt.Println()
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "  TOPOLOGY")
			for _, name := range names {
				fmt.Fprintf(w, "  %s\n", name)
			}
			return w.Flush()
		},
	}
}
