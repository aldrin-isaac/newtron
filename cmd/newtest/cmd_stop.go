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
			suite, err := resolveSuiteForStop(cmd, dir)
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
			if state.PID != 0 && isProcAlive(state.PID) {
				return fmt.Errorf("suite %s is running (pid %d); use 'newtest pause' first", suite, state.PID)
			}

			// Destroy topology if we know the spec dir
			if state.SuiteDir != "" {
				topologiesDir := resolveTopologiesDir()
				topology := state.Topology
				if topology == "" {
					// Try to infer from scenarios
					scenarios, _ := newtest.ParseAllScenarios(state.SuiteDir)
					if len(scenarios) > 0 {
						topology = scenarios[0].Topology
					}
				}
				if topology != "" {
					specDir := filepath.Join(topologiesDir, topology, "specs")
					lab, err := newtlab.NewLab(specDir)
					if err == nil {
						fmt.Printf("destroying topology %s...\n", topology)
						if err := lab.Destroy(); err != nil {
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

// resolveSuiteForStop resolves the suite name for the stop command.
// Unlike resolveSuiteForControl, accepts any suite with state (not just active).
func resolveSuiteForStop(cmd *cobra.Command, dir string) (string, error) {
	if cmd.Flags().Changed("dir") {
		return newtest.SuiteName(dir), nil
	}

	suites, err := newtest.ListSuiteStates()
	if err != nil {
		return "", err
	}

	if len(suites) == 0 {
		return "", fmt.Errorf("no active suite found; use --dir to specify")
	}
	if len(suites) > 1 {
		return "", fmt.Errorf("multiple active suites: %v; use --dir to specify", suites)
	}
	return suites[0], nil
}
