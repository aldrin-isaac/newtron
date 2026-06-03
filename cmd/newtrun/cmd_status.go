package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/cli"
	newtlabclient "github.com/aldrin-isaac/newtron/pkg/newtlab/client"
	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

func newStatusCmd() *cobra.Command {
	var (
		dir         string
		jsonOutput  bool
		suiteFilter string
		detail      bool
		monitor     bool
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show suite run status",
		Long: `Show the status of a running, paused, or completed test suite.
Without --dir or --suite, shows all suites with state.

  newtrun status                       # all suites
  newtrun status --suite 2node-ngdp    # suites whose name contains "2node-ngdp"
  newtrun status --detail              # show per-step timing and status
  newtrun status --monitor             # auto-refresh every 2s (implies --detail)
  newtrun status --dir /path/to/suite  # specific suite by directory
  newtrun status --json                # machine-readable output`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if monitor {
				detail = true
			}

			// Strict Option A: every read goes through newtrun-server,
			// so probe the server first. If it's down, the error
			// message tells the operator how to bring it back up rather
			// than printing stale state from disk.
			if err := requireServer(cmd.Context(), newClient()); err != nil {
				return err
			}

			// Specific suite by directory path.
			if cmd.Flags().Changed("dir") {
				suite := newtrun.SuiteName(dir)
				if monitor {
					return monitorSuite(suite, detail)
				}
				return printSuiteStatus(suite, jsonOutput, detail)
			}

			// All suites (optionally filtered).
			suites, err := listSuiteNamesViaClient()
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
				var states []*newtrun.RunState
				for _, suite := range suites {
					state, err := fetchRunStateViaClient(suite)
					if err != nil || state == nil {
						continue
					}
					states = append(states, state)
				}
				return json.NewEncoder(os.Stdout).Encode(states)
			}

			// In monitor mode, pick the first running suite.
			if monitor {
				suite := findRunningSuite(suites)
				if suite == "" {
					suite = suites[0]
				}
				return monitorSuite(suite, detail)
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
	cmd.Flags().BoolVarP(&monitor, "monitor", "m", false, "auto-refresh every 2s (implies --detail)")

	return cmd
}

func printSuiteStatus(suite string, jsonMode, detail bool) error {
	state, err := fetchRunStateViaClient(suite)
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
	fmt.Printf("newtrun: %s\n", suite)

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

	// Target
	if state.Target != "" {
		fmt.Printf("  target:    %s\n", state.Target)
	}

	// Runner status. The server-mode runner is a goroutine under the
	// registry, not a separate OS process, so the legacy PID display
	// was retired with the AcquireLock/ReleaseLock helpers.
	fmt.Printf("  status:    %s\n", colorRunStatus(state.Status, string(state.Status)))

	// Timing
	if !state.Started.IsZero() {
		ago := time.Since(state.Started).Round(time.Second)
		fmt.Printf("  started:   %s (%s ago)\n", state.Started.Format(newtrun.DateTimeFormat), ago)
	}
	if !state.Finished.IsZero() {
		ago := time.Since(state.Finished).Round(time.Second)
		duration := state.Finished.Sub(state.Started).Round(time.Second)
		fmt.Printf("  finished:  %s (%s ago, took %s)\n", state.Finished.Format(newtrun.DateTimeFormat), ago, duration)
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
			status := colorScenarioStatus(newtrun.StepStatus(sc.Status))
			steps := fmt.Sprintf("%d", sc.TotalSteps)

			// Show step progress for running scenarios
			if sc.Status == "running" && sc.CurrentStep != "" {
				duration = fmt.Sprintf("step %d/%d: %s", sc.CurrentStepIndex+1, sc.TotalSteps, sc.CurrentStep)
			}

			// Show skip reason for skipped scenarios
			if newtrun.StepStatus(sc.Status) == newtrun.StepStatusSkipped && sc.SkipReason != "" {
				duration = sc.SkipReason
			}

			t.Row(fmt.Sprintf("%d", i+1), sc.Name, steps, status, requires, duration)

			switch newtrun.StepStatus(sc.Status) {
			case newtrun.StepStatusPassed:
				passed++
			case newtrun.StepStatusFailed:
				failed++
			case newtrun.StepStatusError:
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
func printDetailView(state *newtrun.RunState) {
	for _, sc := range state.Scenarios {
		if len(sc.Steps) == 0 && sc.Status != "running" {
			continue
		}

		fmt.Printf("\n  %s\n", sc.Name)
		if sc.Description != "" {
			for _, line := range strings.Split(sc.Description, "\n") {
				fmt.Printf("    %s\n", cli.Dim(line))
			}
		}
		// The on-disk scenario path is server-internal and intentionally
		// absent from RunState — operators inspect scenario contents via
		// `newtrun scenario get <suite> <name>` rather than chasing the
		// filename here.
		fmt.Println()

		t := cli.NewTable("#", "STEP", "ACTION", "STATUS", "DURATION", "MESSAGE").WithPrefix("    ")

		for i, step := range sc.Steps {
			status := colorScenarioStatus(newtrun.StepStatus(step.Status))
			msg := step.Message
			if len(msg) > 60 {
				msg = msg[:57] + "..."
			}
			t.Row(fmt.Sprintf("%d", i+1), step.Name, step.Action, status, step.Duration, msg)
		}

		// Show currently running step (started but not yet completed).
		if sc.Status == "running" && sc.CurrentStep != "" && sc.CurrentStepIndex >= len(sc.Steps) {
			action := sc.CurrentStepAction
			t.Row(
				fmt.Sprintf("%d", sc.CurrentStepIndex+1),
				sc.CurrentStep,
				action,
				cli.Yellow("running"),
				"...",
				"",
			)
		}
		t.Flush()
	}
}

// monitorSuite auto-refreshes status every 2 seconds until the suite finishes.
func monitorSuite(suite string, detail bool) error {
	for {
		fmt.Print("\033[2J\033[H") // clear screen, cursor to top
		if err := printSuiteStatus(suite, false, detail); err != nil {
			fmt.Printf("  error: %v\n", err)
		}

		// Exit if suite is no longer active.
		state, _ := fetchRunStateViaClient(suite)
		if state == nil || (state.Status != newtrun.SuiteStatusRunning && state.Status != newtrun.SuiteStatusPausing) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	return nil
}

// findRunningSuite returns the first running suite from the list, or "" if none.
func findRunningSuite(suites []string) string {
	for _, s := range suites {
		state, err := fetchRunStateViaClient(s)
		if err != nil || state == nil {
			continue
		}
		if state.Status == newtrun.SuiteStatusRunning {
			return s
		}
	}
	return ""
}


func checkTopologyStatus(topology string) string {
	// Topology name is the lab name (per the same convention newtron
	// uses). Routes through newtlab-server's HTTP client — newtlab
	// owns LabState (§27), so cmd/newtrun consults it via the API
	// rather than reading state.json from disk.
	lc := newtlabclient.New(newtlabURL())
	state, err := lc.LabStatus(context.Background(), topology)
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

func colorRunStatus(status newtrun.SuiteStatus, text string) string {
	switch status {
	case newtrun.SuiteStatusRunning:
		return cli.Green(text)
	case newtrun.SuiteStatusPausing, newtrun.SuiteStatusPaused:
		return cli.Yellow(text)
	case newtrun.SuiteStatusComplete:
		return cli.Green(text)
	case newtrun.SuiteStatusFailed, newtrun.SuiteStatusAborted:
		return cli.Red(text)
	default:
		return text
	}
}

func colorScenarioStatus(status newtrun.StepStatus) string {
	switch status {
	case newtrun.StepStatusPassed:
		return cli.Green(string(newtrun.StepStatusPassed))
	case newtrun.StepStatusFailed:
		return cli.Red(string(newtrun.StepStatusFailed))
	case newtrun.StepStatusError:
		return cli.Red(string(newtrun.StepStatusError))
	case newtrun.StepStatusSkipped:
		return cli.Yellow(string(newtrun.StepStatusSkipped))
	case "":
		return "\u2014" // —
	default:
		return string(status)
	}
}
