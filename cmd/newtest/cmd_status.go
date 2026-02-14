package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtest"
	"github.com/newtron-network/newtron/pkg/newtlab"
)

func newStatusCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show suite run status",
		Long: `Show the status of a running, paused, or completed test suite.
Without --dir, shows all suites with state.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Specific suite
			if cmd.Flags().Changed("dir") {
				suite := newtest.SuiteName(dir)
				return printSuiteStatus(suite)
			}

			// All suites
			suites, err := newtest.ListSuiteStates()
			if err != nil {
				return err
			}
			if len(suites) == 0 {
				fmt.Println("no active suites")
				return nil
			}

			for i, suite := range suites {
				if i > 0 {
					fmt.Println()
				}
				if err := printSuiteStatus(suite); err != nil {
					fmt.Printf("  error: %v\n", err)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "suite directory")

	return cmd
}

func printSuiteStatus(suite string) error {
	state, err := newtest.LoadRunState(suite)
	if err != nil {
		return err
	}
	if state == nil {
		return fmt.Errorf("no state found for suite %s", suite)
	}

	// Header
	fmt.Printf("newtest: %s\n", suite)

	// Topology info
	topology := resolveTopologyFromState(state)
	topoStatus := "unknown"
	if topology != "" {
		topoStatus = checkTopologyStatus(topology)
	}
	fmt.Printf("  topology:  %s (%s)\n", topology, topoStatus)

	// Platform
	platform := state.Platform
	if platform == "" {
		platform = "default"
	}
	fmt.Printf("  platform:  %s\n", platform)

	// Runner status
	statusStr := string(state.Status)
	if state.PID != 0 && newtest.IsProcessAlive(state.PID) {
		statusStr = fmt.Sprintf("%s (pid %d)", statusStr, state.PID)
	} else if state.PID != 0 {
		// PID recorded but not alive
		if state.Status == newtest.SuiteStatusRunning {
			statusStr = cli.Yellow("aborted") + fmt.Sprintf(" (pid %d exited)", state.PID)
		}
	}
	fmt.Printf("  status:    %s\n", colorRunStatus(state.Status, statusStr))

	// Timing
	if !state.Started.IsZero() {
		ago := time.Since(state.Started).Round(time.Second)
		fmt.Printf("  started:   %s (%s ago)\n", state.Started.Format("2006-01-02 15:04:05"), ago)
	}

	// Scenario table
	if len(state.Scenarios) > 0 {
		fmt.Println()

		// Compute column widths
		maxName := 8 // "SCENARIO"
		for _, sc := range state.Scenarios {
			if len(sc.Name) > maxName {
				maxName = len(sc.Name)
			}
		}

		fmt.Printf("  %-4s  %-*s  %-8s  %s\n", "#", maxName, "SCENARIO", "STATUS", "DURATION")

		passed, failed, errored, running := 0, 0, 0, 0
		for i, sc := range state.Scenarios {
			fmt.Printf("  %-4d  %-*s  %-8s  %s\n", i+1, maxName, sc.Name, colorScenarioStatus(sc.Status), sc.Duration)

			switch newtest.StepStatus(sc.Status) {
			case newtest.StepStatusPassed:
				passed++
			case newtest.StepStatusFailed:
				failed++
			case newtest.StepStatusError:
				errored++
			case "":
				// pending
			default:
				running++
			}
		}

		// Summary line
		fmt.Printf("\n  progress: %d/%d passed", passed, len(state.Scenarios))
		if failed > 0 {
			fmt.Printf(", %d failed", failed)
		}
		if errored > 0 {
			fmt.Printf(", %d errored", errored)
		}
		pending := len(state.Scenarios) - passed - failed - errored - running
		if pending > 0 {
			fmt.Printf(", %d pending", pending)
		}
		fmt.Println()
	}

	return nil
}

func checkTopologyStatus(topology string) string {
	topologiesDir := resolveTopologiesDir()
	specDir := filepath.Join(topologiesDir, topology, "specs")

	lab, err := newtlab.NewLab(specDir)
	if err != nil {
		return "not found"
	}

	state, err := lab.Status()
	if err != nil {
		return "not deployed"
	}

	running, total := 0, 0
	for _, node := range state.Nodes {
		total++
		if node.Status == "running" {
			running++
		}
	}

	if running == total && total > 0 {
		return fmt.Sprintf("deployed, %d nodes running", total)
	}
	if running > 0 {
		return fmt.Sprintf("degraded, %d/%d nodes running", running, total)
	}
	return fmt.Sprintf("stopped, %d nodes", total)
}

func colorRunStatus(status newtest.SuiteStatus, text string) string {
	switch status {
	case newtest.SuiteStatusRunning:
		return cli.Green(text)
	case newtest.SuiteStatusPausing, newtest.SuiteStatusPaused:
		return cli.Yellow(text)
	case newtest.SuiteStatusComplete:
		return cli.Green(text)
	case newtest.SuiteStatusFailed, newtest.SuiteStatusAborted:
		return cli.Red(text)
	default:
		return text
	}
}

func colorScenarioStatus(status string) string {
	switch newtest.StepStatus(status) {
	case newtest.StepStatusPassed:
		return cli.Green(string(newtest.StepStatusPassed))
	case newtest.StepStatusFailed:
		return cli.Red(string(newtest.StepStatusFailed))
	case newtest.StepStatusError:
		return cli.Red(string(newtest.StepStatusError))
	case newtest.StepStatusSkipped:
		return cli.Yellow(string(newtest.StepStatusSkipped))
	case "":
		return "\u2014" // —
	default:
		return status
	}
}
