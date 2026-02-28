package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtrun"
)

func newPauseCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "pause",
		Short: "Pause a running suite after the current scenario",
		Long: `Signals a running suite to pause after the current scenario completes.
The topology stays deployed. Resume with 'newtrun start'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			suite, err := resolveSuite(cmd, dir, func(s newtrun.SuiteStatus) bool {
				return s == newtrun.SuiteStatusRunning || s == newtrun.SuiteStatusPausing || s == newtrun.SuiteStatusPaused
			})
			if err != nil {
				return err
			}

			state, err := newtrun.LoadRunState(suite)
			if err != nil {
				return err
			}
			if state == nil {
				return fmt.Errorf("no state found for suite %s", suite)
			}

			if state.Status != newtrun.SuiteStatusRunning {
				return fmt.Errorf("suite %s is not running (status: %s)", suite, state.Status)
			}

			if state.PID == 0 || !newtrun.IsProcessAlive(state.PID) {
				return fmt.Errorf("suite %s runner process is not alive (pid %d)", suite, state.PID)
			}

			state.Status = newtrun.SuiteStatusPausing
			if err := newtrun.SaveRunState(state); err != nil {
				return err
			}

			fmt.Printf("pausing suite %s (pid %d); will stop after current scenario\n", suite, state.PID)
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "suite directory (auto-detected if omitted)")

	return cmd
}
