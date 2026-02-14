package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtest"
	"github.com/newtron-network/newtron/pkg/util"
)

func newStartCmd() *cobra.Command {
	var (
		dir       string
		scenario  string
		all       bool
		topology  string
		platform  string
		junitPath string
	)

	cmd := &cobra.Command{
		Use:   "start [suite]",
		Short: "Start or resume a test suite",
		Long: `Deploy topology (if needed), run scenarios, and leave topology up.

The suite can be a name (resolved under newtest/suites/) or a path.
All scenarios run by default unless --scenario selects one.

  newtest start 2node-incremental
  newtest start --scenario boot-ssh

If a previous run was paused, start resumes from where it left off.
Use 'newtest pause' to gracefully interrupt, 'newtest stop' to tear down.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if verboseFlag {
				util.SetLogLevel("debug")
			} else {
				util.SetLogLevel("warn")
			}

			// Positional arg overrides --dir
			var positional string
			if len(args) > 0 {
				positional = args[0]
			}
			dir = resolveDir(cmd, dir, positional)
			absDir, err := filepath.Abs(dir)
			if err != nil {
				return fmt.Errorf("resolve dir: %w", err)
			}

			// Default to --all unless --scenario is specified
			if scenario == "" && !cmd.Flags().Changed("all") {
				all = true
			}

			topologiesDir := resolveTopologiesDir()
			suite := newtest.SuiteName(absDir)

			// Check for paused state → resume
			opts := newtest.RunOptions{
				Scenario:  scenario,
				All:       all,
				Topology:  topology,
				Platform:  platform,
				Verbose:   verboseFlag,
				JUnitPath: junitPath,
				Suite:     suite,
				Keep:      true,    // lifecycle mode: always keep
				NoDeploy:  false,   // EnsureTopology handles reuse
			}

			existing, err := newtest.LoadRunState(suite)
			if err != nil {
				return err
			}
			if existing != nil && existing.Status == newtest.SuiteStatusPaused {
				fmt.Fprintf(os.Stderr, "resuming paused suite %s\n", suite)
				opts.Resume = true
				completedMap := make(map[string]newtest.StepStatus)
				for _, sc := range existing.Scenarios {
					if sc.Status != "" {
						completedMap[sc.Name] = newtest.StepStatus(sc.Status)
					}
				}
				opts.Completed = completedMap
			}

			// Build run state
			state := &newtest.RunState{
				Suite:    suite,
				SuiteDir: absDir,
				Topology: topology,
				Platform: platform,
				Status:   newtest.SuiteStatusRunning,
				Started:  time.Now(),
			}

			if err := newtest.AcquireLock(state); err != nil {
				return err
			}
			defer func() { _ = newtest.ReleaseLock(state) }()

			// Set up progress reporter with state tracking
			console := newtest.NewConsoleProgress(verboseFlag)
			reporter := &newtest.StateReporter{
				Inner: console,
				State: state,
			}

			runner := newtest.NewRunner(absDir, topologiesDir)
			runner.Progress = reporter

			results, runErr := runner.Run(opts)

			// Handle pause
			var pauseErr *newtest.PauseError
			if errors.As(runErr, &pauseErr) {
				state.Status = newtest.SuiteStatusPaused
				if err := newtest.SaveRunState(state); err != nil {
					util.Logger.Warnf("failed to save run state: %v", err)
				}
				suiteName := filepath.Base(dir)
				fmt.Fprintf(os.Stderr, "\n%s; resume with: newtest start %s\n", pauseErr, suiteName)
				return nil
			}

			if runErr != nil {
				state.Status = newtest.SuiteStatusFailed
				if err := newtest.SaveRunState(state); err != nil {
					util.Logger.Warnf("failed to save run state: %v", err)
				}
				return runErr
			}

			// Determine final status
			hasFailure, hasError := false, false
			for _, r := range results {
				if r.Status == newtest.StepStatusFailed {
					hasFailure = true
				}
				if r.Status == newtest.StepStatusError || r.DeployError != nil {
					hasError = true
				}
			}

			if hasFailure || hasError {
				state.Status = newtest.SuiteStatusFailed
			} else {
				state.Status = newtest.SuiteStatusComplete
			}
			if err := newtest.SaveRunState(state); err != nil {
				util.Logger.Warnf("failed to save run state: %v", err)
			}

			// Write reports
			gen := &newtest.ReportGenerator{Results: results}
			if err := gen.WriteMarkdown("newtest/.generated/report.md"); err != nil {
				util.Logger.Warnf("failed to write markdown report: %v", err)
			}
			if junitPath != "" {
				if err := gen.WriteJUnit(junitPath); err != nil {
					util.Logger.Warnf("failed to write JUnit report: %v", err)
				}
			}

			// Exit code via sentinel errors (deferred cleanup runs first)
			if hasError {
				return errInfraError
			}
			if hasFailure {
				return errTestFailure
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "directory containing scenario YAML files")
	cmd.Flags().StringVar(&scenario, "scenario", "", "run specific scenario")
	cmd.Flags().BoolVar(&all, "all", false, "run all scenarios in dir")
	cmd.Flags().StringVar(&topology, "topology", "", "override topology")
	cmd.Flags().StringVar(&platform, "platform", "", "override platform")
	cmd.Flags().StringVar(&junitPath, "junit", "", "JUnit XML output path")

	return cmd
}
