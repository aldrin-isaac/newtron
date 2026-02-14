package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtest"
	"github.com/newtron-network/newtron/pkg/newtlab"
)

func newStopCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Destroy topology and clean up suite state",
		Long: `Tears down the deployed topology and removes suite state.
Refuses to stop a suite with a running process — use 'newtest pause' first.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			suite, err := resolveSuite(cmd, dir, nil)
			if err != nil {
				return err
			}

			state, err := newtest.LoadRunState(suite)
			if err != nil {
				return err
			}
			if state == nil {
				return fmt.Errorf("no state found for suite %s", suite)
			}

			// Refuse if runner is alive
			if state.PID != 0 && newtest.IsProcessAlive(state.PID) {
				return fmt.Errorf("suite %s is running (pid %d); use 'newtest pause' first", suite, state.PID)
			}

			// Destroy topology if we know the spec dir
			if state.SuiteDir != "" {
				topologiesDir := resolveTopologiesDir()
				topology := resolveTopologyFromState(state)
				if topology != "" {
					specDir := filepath.Join(topologiesDir, topology, "specs")
					lab, err := newtlab.NewLab(specDir)
					if err == nil {
						fmt.Printf("destroying topology %s...\n", topology)
						if err := lab.Destroy(cmd.Context()); err != nil {
							fmt.Printf("warning: destroy topology: %v\n", err)
						}
					}
				}
			}

			// Clean up state
			if err := newtest.RemoveRunState(suite); err != nil {
				return fmt.Errorf("remove state: %w", err)
			}

			fmt.Printf("suite %s stopped and cleaned up\n", suite)
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "suite directory (auto-detected if omitted)")

	return cmd
}
