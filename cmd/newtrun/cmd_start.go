package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtron"
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
		serverURL string
		networkID string
		monitor   bool
	)

	cmd := &cobra.Command{
		Use:   "start [suite]",
		Short: "Start or resume a test suite",
		Long: `Deploy topology (if needed), run scenarios, and leave topology up.

The suite can be a name (resolved under newtrun/suites/) or a path.
All scenarios run by default. Use --scenario to run a single one.

  newtrun start 2node-incremental                     # run all scenarios
  newtrun start 2node-incremental --scenario boot-ssh
  newtrun start 2node-incremental --monitor           # live status dashboard

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

			// Set up progress reporter with state tracking.
			// In monitor mode, suppress console output (Inner=nil) — the
			// monitor dashboard reads from the persisted state file instead.
			var inner newtrun.ProgressReporter
			if !monitor {
				inner = newtrun.NewConsoleProgress(verboseFlag)
			}
			reporter := &newtrun.StateReporter{
				Inner: inner,
				State: state,
			}

			runner := newtrun.NewRunner(absDir, topologiesDir)
			runner.Progress = reporter

			// Resolve server URL: flag > env > settings > default
			if serverURL == "" {
				serverURL = os.Getenv("NEWTRON_SERVER")
			}
			if serverURL == "" {
				if s, err := newtron.LoadSettings(); err == nil {
					serverURL = s.GetServerURL()
				}
			}
			if serverURL == "" {
				serverURL = newtron.DefaultServerURL
			}
			runner.ServerURL = serverURL

			// Resolve network ID: flag > env > settings > default
			if networkID == "" {
				networkID = os.Getenv("NEWTRON_NETWORK_ID")
			}
			if networkID == "" {
				if s, err := newtron.LoadSettings(); err == nil {
					networkID = s.GetNetworkID()
				}
			}
			if networkID == "" {
				networkID = newtron.DefaultNetworkID
			}
			runner.NetworkID = networkID

			// In monitor mode, run the suite in a goroutine and show the
			// live status dashboard (equivalent to newtrun status --detail --monitor).
			type runResult struct {
				results []*newtrun.ScenarioResult
				err     error
			}
			var resultCh chan runResult
			if monitor {
				resultCh = make(chan runResult, 1)
				go func() {
					r, e := runner.Run(opts)
					// Update state status BEFORE sending result so the
					// monitor sees the terminal status and exits.
					finalizeRunState(state, r, e)
					resultCh <- runResult{r, e}
				}()
				time.Sleep(2 * time.Second)
				_ = monitorSuite(suite, true)
			}

			var results []*newtrun.ScenarioResult
			var runErr error
			if monitor {
				res := <-resultCh
				results, runErr = res.results, res.err
			} else {
				results, runErr = runner.Run(opts)
			}

			// Handle pause
			var pauseErr *newtrun.PauseError
			if errors.As(runErr, &pauseErr) {
				if !monitor {
					state.Status = newtrun.SuiteStatusPaused
					if err := newtrun.SaveRunState(state); err != nil {
						util.Logger.Warnf("failed to save run state: %v", err)
					}
				}
				suiteName := filepath.Base(dir)
				fmt.Fprintf(os.Stderr, "\n%s; resume with: newtrun start %s\n", pauseErr, suiteName)
				return nil
			}

			if runErr != nil {
				if !monitor {
					state.Status = newtrun.SuiteStatusFailed
					state.Finished = time.Now()
					if err := newtrun.SaveRunState(state); err != nil {
						util.Logger.Warnf("failed to save run state: %v", err)
					}
				}
				return runErr
			}

			// Finalize state (already done in monitor mode goroutine).
			if !monitor {
				finalizeRunState(state, results, runErr)
			}

			hasFailure, hasError := false, false
			for _, r := range results {
				if r.Status == newtrun.StepStatusFailed {
					hasFailure = true
				}
				if r.Status == newtrun.StepStatusError || r.DeployError != nil {
					hasError = true
				}
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
	cmd.Flags().StringVar(&serverURL, "server", "", "newtron-server URL (env: NEWTRON_SERVER)")
	cmd.Flags().StringVar(&networkID, "network-id", "", "Network identifier (env: NEWTRON_NETWORK_ID)")
	cmd.Flags().BoolVarP(&monitor, "monitor", "m", false, "show live status dashboard during run")

	return cmd
}

// finalizeRunState sets the terminal status on the run state and persists it.
// Called from the runner goroutine in monitor mode (so the monitor sees the
// status change and exits), or from the main goroutine in normal mode.
func finalizeRunState(state *newtrun.RunState, results []*newtrun.ScenarioResult, runErr error) {
	if runErr != nil {
		state.Status = newtrun.SuiteStatusFailed
		state.Finished = time.Now()
		if err := newtrun.SaveRunState(state); err != nil {
			util.Logger.Warnf("failed to save run state: %v", err)
		}
		return
	}

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
}
