package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtest"
	"github.com/newtron-network/newtron/pkg/newtlab"
)

func newStatusCmd() *cobra.Command {
	var (
		dir         string
		jsonOutput  bool
		suiteFilter string
		detail      bool
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show suite run status",
		Long: `Show the status of a running, paused, or completed test suite.
Without --dir or --suite, shows all suites with state.

  newtest status                       # all suites
  newtest status --suite 2node         # suites whose name contains "2node"
  newtest status --detail              # show per-step timing and status
  newtest status --dir /path/to/suite  # specific suite by directory
  newtest status --json                # machine-readable output`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Specific suite by directory path.
			if cmd.Flags().Changed("dir") {
				suite := newtest.SuiteName(dir)
				return printSuiteStatus(suite, jsonOutput, detail)
			}

			// All suites (optionally filtered).
			suites, err := newtest.ListSuiteStates()
			if err != nil {
				return err
			}

			// Apply --suite filter (substring match, case-insensitive).
			if suiteFilter != "" {
				lower := strings.ToLower(suiteFilter)
				var matched []string
				for _, s := range suites {
					if strings.Contains(strings.ToLower(s), lower) {
						matched = append(matched, s)
					}
				}
				if len(matched) == 0 {
					return fmt.Errorf("no suite matching %q", suiteFilter)
				}
				suites = matched
			}

			if len(suites) == 0 {
				if jsonOutput {
					fmt.Println("[]")
					return nil
				}
				fmt.Println("no active suites")
				return nil
			}

			if jsonOutput {
				var states []*newtest.RunState
				for _, suite := range suites {
					state, err := newtest.LoadRunState(suite)
					if err != nil || state == nil {
						continue
					}
					states = append(states, state)
				}
				return json.NewEncoder(os.Stdout).Encode(states)
			}

			for i, suite := range suites {
				if i > 0 {
					fmt.Println()
				}
				if err := printSuiteStatus(suite, false, detail); err != nil {
					fmt.Printf("  error: %v\n", err)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "suite directory")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "JSON output")
	cmd.Flags().StringVarP(&suiteFilter, "suite", "s", "", "show only suites whose name contains this string")
	cmd.Flags().BoolVarP(&detail, "detail", "d", false, "show per-step timing and status")

	return cmd
}

func printSuiteStatus(suite string, jsonMode, detail bool) error {
	state, err := newtest.LoadRunState(suite)
	if err != nil {
		return err
	}
	if state == nil {
		return fmt.Errorf("no state found for suite %s", suite)
	}

	if jsonMode {
		return json.NewEncoder(os.Stdout).Encode(state)
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
		fmt.Printf("  started:   %s (%s ago)\n", state.Started.Format(newtest.DateTimeFormat), ago)
	}
	if !state.Finished.IsZero() {
		ago := time.Since(state.Finished).Round(time.Second)
		duration := state.Finished.Sub(state.Started).Round(time.Second)
		fmt.Printf("  finished:  %s (%s ago, took %s)\n", state.Finished.Format(newtest.DateTimeFormat), ago, duration)
	}

	// Scenario summary
	if len(state.Scenarios) > 0 {
		totalSteps := 0
		for _, sc := range state.Scenarios {
			totalSteps += sc.TotalSteps
		}
		fmt.Printf("  scenarios: %d (%d steps total)\n", len(state.Scenarios), totalSteps)
	}

	// Scenario table
	if len(state.Scenarios) > 0 {
		fmt.Println()

		t := cli.NewTable("#", "SCENARIO", "STEPS", "STATUS", "REQUIRES", "DURATION").WithPrefix("  ")

		passed, failed, errored, running := 0, 0, 0, 0
		for i, sc := range state.Scenarios {
			requires := "\u2014" // —
			if len(sc.Requires) > 0 {
				requires = strings.Join(sc.Requires, ", ")
			}

			duration := sc.Duration
			status := colorScenarioStatus(newtest.StepStatus(sc.Status))
			steps := fmt.Sprintf("%d", sc.TotalSteps)

			// Show step progress for running scenarios
			if sc.Status == "running" && sc.CurrentStep != "" {
				duration = fmt.Sprintf("step %d/%d: %s", sc.CurrentStepIndex+1, sc.TotalSteps, sc.CurrentStep)
			}

			// Show skip reason for skipped scenarios
			if newtest.StepStatus(sc.Status) == newtest.StepStatusSkipped && sc.SkipReason != "" {
				duration = sc.SkipReason
			}

			t.Row(fmt.Sprintf("%d", i+1), sc.Name, steps, status, requires, duration)

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
		t.Flush()

		// Detail view: expand each scenario to show per-step results
		if detail {
			printDetailView(state)
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

// printDetailView prints per-step results for each scenario that has them.
func printDetailView(state *newtest.RunState) {
	for _, sc := range state.Scenarios {
		if len(sc.Steps) == 0 {
			continue
		}

		fmt.Printf("\n  %s:\n", sc.Name)
		if sc.Description != "" {
			fmt.Printf("    %s\n", cli.Dim(sc.Description))
		}
		t := cli.NewTable("#", "STEP", "ACTION", "STATUS", "DURATION", "MESSAGE").WithPrefix("    ")

		for i, step := range sc.Steps {
			status := colorScenarioStatus(newtest.StepStatus(step.Status))
			msg := step.Message
			if len(msg) > 60 {
				msg = msg[:57] + "..."
			}
			t.Row(fmt.Sprintf("%d", i+1), step.Name, step.Action, status, step.Duration, msg)
		}
		t.Flush()
	}
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

func colorScenarioStatus(status newtest.StepStatus) string {
	switch status {
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
		return string(status)
	}
}
