package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/newtlab"
	"github.com/aldrin-isaac/newtron/pkg/newtrun/client"
)

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <suite>",
		Short: "Cancel a running suite, destroy its topology, and remove state",
		Long: `Cancel the named suite's run (if active), tear down the deployed topology,
and remove the suite's state directory. The topology destroy step uses the
spec directory recorded in state, so it requires the state to be readable.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			suite := args[0]
			c := newClient()
			ctx := cmd.Context()
			if err := requireServer(ctx, c); err != nil {
				return err
			}

			// Fetch state before stopping so we still know the spec dir.
			state, err := c.GetRun(ctx, suite)
			if err != nil {
				return err
			}
			if state == nil {
				return fmt.Errorf("no state found for suite %s", suite)
			}

			// Cancel the active run. 404 is fine — no active run means
			// the state is already in a terminal state.
			if err := c.StopRun(ctx, suite); err != nil {
				var se *client.ServerError
				if !errors.As(err, &se) || se.StatusCode != 404 {
					return err
				}
			}

			// Destroy the topology if the spec dir is on disk.
			if state.SpecDir != "" {
				if lab, err := newtlab.NewLab(state.SpecDir); err == nil {
					fmt.Printf("destroying topology %s...\n", resolveTopologyFromState(state))
					if err := lab.Destroy(ctx); err != nil {
						fmt.Printf("warning: destroy topology: %v\n", err)
					}
				}
			}

			// Remove the state directory via the server.
			if err := c.DeleteRun(ctx, suite); err != nil {
				var se *client.ServerError
				if !errors.As(err, &se) || se.StatusCode != 404 {
					return err
				}
			}
			fmt.Printf("suite %s stopped and cleaned up\n", suite)
			return nil
		},
	}
}
