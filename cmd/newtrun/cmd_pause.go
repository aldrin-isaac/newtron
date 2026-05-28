package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause <suite>",
		Short: "Pause a running suite after the current scenario",
		Long: `Signal newtrun-server to pause the named suite's running scenario.
The topology stays deployed. Resume with 'newtrun start <suite>'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			suite := args[0]
			c := newClient()
			ctx := cmd.Context()
			if err := requireServer(ctx, c); err != nil {
				return err
			}
			if err := c.PauseRun(ctx, suite); err != nil {
				return err
			}
			fmt.Printf("pausing suite %s; will stop after current scenario\n", suite)
			return nil
		},
	}
}
