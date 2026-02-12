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
		parallel  int
		junitPath string
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start or resume a test suite",
		Long: `Deploy topology (if needed), run scenarios, and leave topology up.

If a previous run was paused, start resumes from where it left off.
Use 'newtest pause' to gracefully interrupt, 'newtest stop' to tear down.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if verboseFlag {
				util.SetLogLevel("debug")
			} else {
				util.SetLogLevel("warn")
			}

			dir = resolveDir(cmd, dir)
			absDir, err := filepath.Abs(dir)
			if err != nil {
				return fmt.Errorf("resolve dir: %w", err)
			}

			topologiesDir := resolveTopologiesDir()
			suite := newtest.SuiteName(absDir)

			// Check for paused state → resume
			opts := newtest.RunOptions{
				Scenario:  scenario,
				All:       all,
				Topology:  topology,
				Platform:  platform,
				Parallel:  parallel,
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
			if existing != nil && existing.Status == newtest.StatusPaused {
				fmt.Fprintf(os.Stderr, "resuming paused suite %s\n", suite)
				opts.Resume = true
				completedMap := make(map[string]newtest.Status)
				for _, sc := range existing.Scenarios {
					if sc.Status != "" {
						completedMap[sc.Name] = newtest.Status(sc.Status)
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
				Status:   newtest.StatusRunning,
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
				state.Status = newtest.StatusPaused
				_ = newtest.SaveRunState(state)
				fmt.Fprintf(os.Stderr, "\n%s; resume with: newtest start --dir %s --all\n", pauseErr, dir)
				return nil
			}

			if runErr != nil {
				state.Status = newtest.StatusRunFailed
				_ = newtest.SaveRunState(state)
				return runErr
			}

			// Determine final status
			hasFailure, hasError := false, false
			for _, r := range results {
				if r.Status == newtest.StatusFailed {
					hasFailure = true
				}
				if r.Status == newtest.StatusError || r.DeployError != nil {
					hasError = true
				}
			}

			if hasFailure || hasError {
				state.Status = newtest.StatusRunFailed
			} else {
				state.Status = newtest.StatusComplete
			}
			_ = newtest.SaveRunState(state)

			// Write reports
			gen := &newtest.ReportGenerator{Results: results}
			_ = gen.WriteMarkdown("newtest/.generated/report.md")
			if junitPath != "" {
				_ = gen.WriteJUnit(junitPath)
			}

			// Exit code
			if hasError {
				os.Exit(2)
			}
			if hasFailure {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "directory containing scenario YAML files")
	cmd.Flags().StringVar(&scenario, "scenario", "", "run specific scenario")
	cmd.Flags().BoolVar(&all, "all", false, "run all scenarios in dir")
	cmd.Flags().StringVar(&topology, "topology", "", "override topology")
	cmd.Flags().StringVar(&platform, "platform", "", "override platform")
	cmd.Flags().IntVar(&parallel, "parallel", 1, "parallel provisioning count")
	cmd.Flags().StringVar(&junitPath, "junit", "", "JUnit XML output path")

	return cmd
}
