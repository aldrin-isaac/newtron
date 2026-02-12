package main

import (
	"fmt"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtest"
)

func newPauseCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "pause",
		Short: "Pause a running suite after the current scenario",
		Long: `Signals a running suite to pause after the current scenario completes.
The topology stays deployed. Resume with 'newtest start'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			suite, err := resolveSuiteForControl(cmd, dir)
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

			if state.Status != newtest.StatusRunning {
				return fmt.Errorf("suite %s is not running (status: %s)", suite, state.Status)
			}

			if state.PID == 0 || !isProcAlive(state.PID) {
				return fmt.Errorf("suite %s runner process is not alive (pid %d)", suite, state.PID)
			}

			state.Status = newtest.StatusPausing
			if err := newtest.SaveRunState(state); err != nil {
				return err
			}

			fmt.Printf("pausing suite %s (pid %d); will stop after current scenario\n", suite, state.PID)
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "suite directory (auto-detected if omitted)")

	return cmd
}

// resolveSuiteForControl resolves the suite name for pause/stop/status commands.
// If --dir is provided, use it. Otherwise auto-detect from active suites.
func resolveSuiteForControl(cmd *cobra.Command, dir string) (string, error) {
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

	// Filter to active suites (running or paused)
	var active []string
	for _, s := range suites {
		state, err := newtest.LoadRunState(s)
		if err != nil || state == nil {
			continue
		}
		if state.Status == newtest.StatusRunning || state.Status == newtest.StatusPausing || state.Status == newtest.StatusPaused {
			active = append(active, s)
		}
	}

	if len(active) == 0 {
		return "", fmt.Errorf("no active suite found; use --dir to specify")
	}
	if len(active) > 1 {
		return "", fmt.Errorf("multiple active suites: %v; use --dir to specify", active)
	}
	return active[0], nil
}

// isProcAlive checks if a process is alive via kill(pid, 0).
func isProcAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
