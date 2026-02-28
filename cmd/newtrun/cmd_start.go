package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtrun"
	"github.com/newtron-network/newtron/pkg/util"
)

func newStartCmd() *cobra.Command {
	var (
		dir       string
		scenario  string
		topology  string
		platform  string
		junitPath string
	)

	cmd := &cobra.Command{
		Use:   "start [suite]",
		Short: "Start or resume a test suite",
		Long: `Deploy topology (if needed), run scenarios, and leave topology up.

The suite can be a name (resolved under newtrun/suites/) or a path.
All scenarios run by default. Use --scenario to run a single one.

  newtrun start 2node-incremental                    # run all scenarios
  newtrun start 2node-incremental --scenario boot-ssh

If a previous run was paused, start resumes from where it left off.
Use 'newtrun pause' to gracefully interrupt, 'newtrun stop' to tear down.`,
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

			topologiesDir := resolveTopologiesDir()
			suite := newtrun.SuiteName(absDir)

			fmt.Fprintf(os.Stderr, "newtrun: suite %s (%s)\n", suite, absDir)

			// Check for paused state → resume
			opts := newtrun.RunOptions{
				Scenario:  scenario,
				All:       scenario == "",
				Topology:  topology,
				Platform:  platform,
				Verbose:   verboseFlag,
				JUnitPath: junitPath,
				Suite:     suite,
				Keep:      true,    // lifecycle mode: always keep
				NoDeploy:  false,   // EnsureTopology handles reuse
			}

			existing, err := newtrun.LoadRunState(suite)
			if err != nil {
				return err
			}
			if existing != nil && existing.Status == newtrun.SuiteStatusPaused {
				fmt.Fprintf(os.Stderr, "resuming paused suite %s\n", suite)
				opts.Resume = true
				completedMap := make(map[string]newtrun.StepStatus)
				for _, sc := range existing.Scenarios {
					if sc.Status != "" {
						completedMap[sc.Name] = newtrun.StepStatus(sc.Status)
					}
				}
				opts.Completed = completedMap
			}

			// Build run state
			state := &newtrun.RunState{
				Suite:    suite,
				SuiteDir: absDir,
				Topology: topology,
				Platform: platform,
				Status:   newtrun.SuiteStatusRunning,
				Started:  time.Now(),
			}

			if err := newtrun.AcquireLock(state); err != nil {
				return err
			}
			defer func() { _ = newtrun.ReleaseLock(state) }()

			// Set up progress reporter with state tracking
			console := newtrun.NewConsoleProgress(verboseFlag)
			reporter := &newtrun.StateReporter{
				Inner: console,
				State: state,
			}

			runner := newtrun.NewRunner(absDir, topologiesDir)
			runner.Progress = reporter

			results, runErr := runner.Run(opts)

			// Handle pause
			var pauseErr *newtrun.PauseError
			if errors.As(runErr, &pauseErr) {
				state.Status = newtrun.SuiteStatusPaused
				if err := newtrun.SaveRunState(state); err != nil {
					util.Logger.Warnf("failed to save run state: %v", err)
				}
				suiteName := filepath.Base(dir)
				fmt.Fprintf(os.Stderr, "\n%s; resume with: newtrun start %s\n", pauseErr, suiteName)
				return nil
			}

			if runErr != nil {
				state.Status = newtrun.SuiteStatusFailed
				state.Finished = time.Now()
				if err := newtrun.SaveRunState(state); err != nil {
					util.Logger.Warnf("failed to save run state: %v", err)
				}
				return runErr
			}

			// Determine final status
			hasFailure, hasError := false, false
			for _, r := range results {
				if r.Status == newtrun.StepStatusFailed {
					hasFailure = true
				}
				if r.Status == newtrun.StepStatusError || r.DeployError != nil {
					hasError = true
				}
			}

			if hasFailure || hasError {
				state.Status = newtrun.SuiteStatusFailed
			} else {
				state.Status = newtrun.SuiteStatusComplete
			}
			state.Finished = time.Now()
			if err := newtrun.SaveRunState(state); err != nil {
				util.Logger.Warnf("failed to save run state: %v", err)
			}

			// Write reports
			gen := &newtrun.ReportGenerator{Results: results}
			if err := gen.WriteMarkdown("newtrun/.generated/report.md"); err != nil {
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
	cmd.Flags().StringVar(&scenario, "scenario", "", "run specific scenario (default: all)")
	cmd.Flags().StringVar(&topology, "topology", "", "override topology")
	cmd.Flags().StringVar(&platform, "platform", "", "override platform")
	cmd.Flags().StringVar(&junitPath, "junit", "", "JUnit XML output path")

	return cmd
}
