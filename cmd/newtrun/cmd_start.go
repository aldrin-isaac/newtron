package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
	"github.com/aldrin-isaac/newtron/pkg/newtrun/api"
)

func newStartCmd() *cobra.Command {
	var (
		scenario  string
		target    string
		platform  string
		serverURL string // newtron-server URL (original --server semantics)
		networkID string
		junitPath string
		monitor   bool
		noDeploy  bool
	)

	cmd := &cobra.Command{
		Use:   "start <suite>",
		Short: "Start or resume a suite run",
		Long: `Submit a run of the named file-backed suite to newtrun-server, then
stream scenario and step events back to the terminal as they arrive.

  newtrun start 2node-ngdp-primitive                        # run all scenarios
  newtrun start 2node-ngdp-primitive --scenario boot-ssh    # run one
  newtrun start 2node-ngdp-primitive --target cross-switch  # run dependency chain
  newtrun start 2node-ngdp-primitive --monitor              # live dashboard
  newtrun start 2node-ngdp-primitive --junit out.xml        # JUnit XML report

If the suite is paused (previous run completed pause cleanly), newtrun-server
resumes from where it stopped — scenarios already passed are skipped.

The topology and per-Node atomicity model are determined by newtron-server.
Pause with 'newtrun pause <suite>'; tear down with 'newtrun stop <suite>'.

Exit code: 0 on success; 1 on test failure; 2 on infrastructure error.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			suiteName := args[0]
			if suiteName == "" {
				return fmt.Errorf("provide a suite name")
			}

			c := newClient()
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			if err := requireServer(ctx, c); err != nil {
				return err
			}

			// Resolve newtron-server URL: --server flag wins, else env,
			// else built-in default. Matches the original --server semantics.
			if serverURL == "" {
				serverURL = os.Getenv("NEWTRON_SERVER")
			}
			// Resolve network ID similarly.
			if networkID == "" {
				networkID = os.Getenv("NEWTRON_NETWORK_ID")
			}

			req := api.StartRunRequest{
				Suite:         suiteName,
				Scenario:      scenario,
				Target:        target,
				Platform:      platform,
				NoDeploy:      noDeploy,
				Verbose:       verboseFlag,
				NewtronServer: serverURL,
				NetworkID:     networkID,
				JUnitPath:     junitPath,
			}

			started, err := c.StartRun(ctx, req)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "newtrun: started suite %s at %s\n",
				started.Suite, started.Started.Format(time.RFC3339))

			// Subscribe to events; render them; cancel the stream when
			// SuiteEnd arrives. Cancellation makes the stream return
			// context.Canceled which we treat as the normal terminal
			// condition.
			//
			// In monitor mode, the renderer skips per-event terminal
			// output and the monitor loop is what the operator watches
			// (auto-refresh dashboard reading state.json directly). The
			// terminal exit code still derives from the same atomic
			// flags so behavior matches the non-monitor case.
			var hasFailure, hasError, suiteEndSeen, suiteAborted atomic.Bool
			scenarioResults := make([]*newtrun.ScenarioResult, 0)
			var resultsMu sync.Mutex

			// markSuiteEnd is called from the SSE handler whenever a
			// SuiteEnd arrives. The status field on the wire payload
			// distinguishes "the run completed normally with N failures"
			// (the suite was actually buggy) from "aborted" (the server
			// shut down mid-run) — exit-code mapping below uses that
			// distinction to avoid blaming tests for an infrastructure
			// outage.
			markSuiteEnd := func(ev api.Event) {
				if ev.Type != api.EventSuiteEnd {
					return
				}
				suiteEndSeen.Store(true)
				if payload, err := json.Marshal(ev.Payload); err == nil {
					var p api.SuiteEndPayload
					if err := json.Unmarshal(payload, &p); err == nil {
						if p.Status == newtrun.SuiteStatusAborted {
							suiteAborted.Store(true)
						}
					}
				}
			}

			var streamDone chan struct{}
			if monitor {
				streamDone = make(chan struct{})
				go func() {
					defer close(streamDone)
					_ = c.StreamEvents(ctx, started.Suite, func(ev api.Event) {
						trackStatus(ev, &hasFailure, &hasError)
						collectResult(ev, &scenarioResults, &resultsMu)
						markSuiteEnd(ev)
						if ev.Type == api.EventSuiteEnd {
							cancel()
						}
					})
				}()
				// Brief delay so the run's initial state file lands before
				// the monitor loop tries to read it.
				time.Sleep(500 * time.Millisecond)
				_ = monitorSuite(started.Suite, true)
				<-streamDone
			} else {
				streamErr := c.StreamEvents(ctx, started.Suite, func(ev api.Event) {
					renderEvent(ev, &hasFailure, &hasError)
					collectResult(ev, &scenarioResults, &resultsMu)
					markSuiteEnd(ev)
					if ev.Type == api.EventSuiteEnd {
						cancel()
					}
				})
				if streamErr != nil && ctx.Err() == nil {
					// The SSE stream broke before we asked it to. This
					// is almost always the server going away mid-run
					// (SIGKILL with no drain; network blip). Report it
					// as infrastructure error with a clearer message
					// than the raw transport error, so an operator
					// doesn't mistake it for a tool bug.
					return fmt.Errorf("%w: newtrun-server connection lost mid-run; check state.json for the last persisted status",
						errInfraError)
				}
			}

			// If the stream ended cleanly but we never saw SuiteEnd, the
			// server-side run was reaped (server shut down between events)
			// — surface it the same way as the broken-stream case.
			if !suiteEndSeen.Load() {
				return fmt.Errorf("%w: stream ended without SuiteEnd; the server may have been shut down mid-run",
					errInfraError)
			}
			// Aborted suites are infrastructure errors, not test failures —
			// don't blame the suite for the server's shutdown.
			if suiteAborted.Load() {
				return fmt.Errorf("%w: run was aborted (server shut down)", errInfraError)
			}

			// Write reports (markdown always; JUnit if --junit set). Mirrors
			// the original cmd_start.go behavior.
			resultsMu.Lock()
			results := append([]*newtrun.ScenarioResult(nil), scenarioResults...)
			resultsMu.Unlock()
			if len(results) > 0 {
				gen := &newtrun.ReportGenerator{Results: results}
				if err := gen.WriteMarkdown("newtrun/.generated/report.md"); err != nil {
					fmt.Fprintf(os.Stderr, "warning: failed to write markdown report: %v\n", err)
				}
				if junitPath != "" {
					if err := gen.WriteJUnit(junitPath); err != nil {
						fmt.Fprintf(os.Stderr, "warning: failed to write JUnit report: %v\n", err)
					}
				}
			}

			if hasError.Load() {
				return errInfraError
			}
			if hasFailure.Load() {
				return errTestFailure
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&scenario, "scenario", "", "run specific scenario (default: all)")
	cmd.Flags().StringVar(&target, "target", "", "run minimal dependency chain to reach scenario")
	cmd.Flags().StringVar(&platform, "platform", "", "override platform")
	cmd.Flags().StringVar(&junitPath, "junit", "", "JUnit XML output path")
	cmd.Flags().StringVar(&serverURL, "server", "", "newtron-server URL (default: http://127.0.0.1:18080, env: NEWTRON_SERVER)")
	cmd.Flags().StringVar(&networkID, "network-id", "", "newtron network identifier (env: NEWTRON_NETWORK_ID)")
	cmd.Flags().BoolVarP(&monitor, "monitor", "m", false, "show live status dashboard during run")
	cmd.Flags().BoolVar(&noDeploy, "no-deploy", false, "skip topology deployment (for loopback/offline mode)")
	return cmd
}

// collectResult builds ScenarioResults from SSE events so the CLI can
// hand them to ReportGenerator at run end. Mirrors the original in-process
// pipeline where the Runner returned ScenarioResults directly. We
// reconstruct from event payloads since the server side already discards
// the in-memory slice.
// trackStatus updates the FAIL/ERROR flags used for the process exit code.
// Called from both monitor and non-monitor paths so the exit code is
// consistent regardless of how events were rendered.
func trackStatus(ev api.Event, hasFailure, hasError *atomic.Bool) {
	if ev.Type != api.EventScenarioEnd {
		return
	}
	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		return
	}
	var p api.ScenarioEndPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return
	}
	switch string(p.Status) {
	case "FAIL":
		hasFailure.Store(true)
	case "ERROR":
		hasError.Store(true)
	}
}

func collectResult(ev api.Event, results *[]*newtrun.ScenarioResult, mu *sync.Mutex) {
	if ev.Type != api.EventScenarioEnd {
		return
	}
	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		return
	}
	var p api.ScenarioEndPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return
	}
	// Translate the wire payload back into a ScenarioResult — the same
	// shape the original in-process Runner produced for the report
	// generator. Fields not present on the payload (like DeployError as a
	// Go error type) get reasonable defaults; the report renders fine
	// without them.
	r := &newtrun.ScenarioResult{
		Name:       p.Name,
		Topology:   p.Topology,
		Platform:   p.Platform,
		Status:     p.Status,
		Duration:   parseDuration(p.Duration),
		SkipReason: p.SkipReason,
	}
	for _, s := range p.Steps {
		r.Steps = append(r.Steps, newtrun.StepResult{
			Name:      s.Name,
			Action:    s.Action,
			Status:    s.Status,
			Duration:  parseDuration(s.Duration),
			Message:   s.Message,
			Iteration: s.Iteration,
		})
	}
	mu.Lock()
	*results = append(*results, r)
	mu.Unlock()
}

// parseDuration accepts the durationString output from pkg/newtrun/api/types.go
// ("<1s", "5s", "2m30s") and returns a time.Duration. Conservatively rounds
// down on parse failure rather than returning zero, so report timings remain
// human-meaningful.
func parseDuration(s string) time.Duration {
	if s == "" || s == "<1s" {
		return 500 * time.Millisecond // best-effort placeholder
	}
	// Try standard Go duration first ("5s", "2m30s").
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	// Try seconds-only ("5s") that escaped above. Defensive.
	if strings.HasSuffix(s, "s") {
		if d, err := time.ParseDuration(s); err == nil {
			return d
		}
	}
	return 0
}

// renderEvent prints a one-line summary of each event in a form similar
// to the existing consoleProgress (--verbose) output. Status tracking
// for the exit code is done via atomic flags so concurrent renders are
// safe.
func renderEvent(ev api.Event, hasFailure, hasError *atomic.Bool) {
	// The event payload was decoded as map[string]any by the client.
	// Re-marshal to inspect typed fields.
	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		return
	}
	switch ev.Type {
	case api.EventSuiteStart:
		var p api.SuiteStartPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return
		}
		fmt.Fprintf(os.Stderr, "newtrun: %d scenarios\n\n", len(p.Scenarios))
		for i, s := range p.Scenarios {
			fmt.Fprintf(os.Stderr, "  %d  %s  (%d steps)\n", i+1, s.Name, s.StepCount)
		}
		fmt.Fprintln(os.Stderr)

	case api.EventScenarioStart:
		var p api.ScenarioStartPayload
		_ = json.Unmarshal(payload, &p)
		fmt.Fprintf(os.Stderr, "  [%d/%d] %s\n", p.Index+1, p.Total, p.Name)

	case api.EventStepEnd:
		var p api.StepEndPayload
		_ = json.Unmarshal(payload, &p)
		if verboseFlag {
			fmt.Fprintf(os.Stderr, "          [%d/%d] %s %s (%s)\n",
				p.Index+1, p.Total, p.Result.Name, p.Result.Status, p.Result.Duration)
		}

	case api.EventScenarioEnd:
		var p api.ScenarioEndPayload
		_ = json.Unmarshal(payload, &p)
		fmt.Fprintf(os.Stderr, "          %s (%s)\n\n", p.Status, p.Duration)
		switch string(p.Status) {
		case "FAIL":
			hasFailure.Store(true)
		case "ERROR":
			hasError.Store(true)
		}

	case api.EventSuiteEnd:
		var p api.SuiteEndPayload
		_ = json.Unmarshal(payload, &p)
		passed, failed, skipped, errored := 0, 0, 0, 0
		for _, r := range p.Results {
			switch string(r.Status) {
			case "PASS":
				passed++
			case "FAIL":
				failed++
			case "SKIP":
				skipped++
			case "ERROR":
				errored++
			}
		}
		fmt.Fprintf(os.Stderr, "---\n")
		fmt.Fprintf(os.Stderr, "newtrun: %d scenarios — %d passed, %d failed, %d errored, %d skipped (%s)\n",
			len(p.Results), passed, failed, errored, skipped, p.Duration)
	}
}
