package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/newtrun/api"
)

func newStartCmd() *cobra.Command {
	var (
		scenario string
		target   string
		platform string
		noDeploy bool
		newtronServer string
	)

	cmd := &cobra.Command{
		Use:   "start <suite>",
		Short: "Start a suite run on newtrun-server and stream results",
		Long: `Submit a run of the named file-backed suite to newtrun-server, then
stream scenario and step events back to the terminal as they arrive.

  newtrun start 2node-ngdp-primitive                        # run all scenarios
  newtrun start 2node-ngdp-primitive --scenario boot-ssh    # run one
  newtrun start 2node-ngdp-primitive --target cross-switch  # run dependency chain

The topology and per-Node atomicity model are determined by newtron-server.
Pause with 'newtrun pause <suite>'; tear down with 'newtrun stop <suite>'.

Exit code: 0 on success; 1 on test failure; 2 on infrastructure error.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			suite := args[0]
			c := newClient()
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			if err := requireServer(ctx, c); err != nil {
				return err
			}

			req := api.StartRunRequest{
				Suite:         suite,
				Scenario:      scenario,
				Target:        target,
				Platform:      platform,
				NoDeploy:      noDeploy,
				Verbose:       verboseFlag,
				NewtronServer: newtronServer,
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
			var hasFailure, hasError atomic.Bool

			streamErr := c.StreamEvents(ctx, suite, func(ev api.Event) {
				renderEvent(ev, &hasFailure, &hasError)
				if ev.Type == api.EventSuiteEnd {
					cancel()
				}
			})
			if streamErr != nil && ctx.Err() == nil {
				return streamErr
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

	cmd.Flags().StringVar(&scenario, "scenario", "", "run specific scenario")
	cmd.Flags().StringVar(&target, "target", "", "run minimal dependency chain to reach scenario")
	cmd.Flags().StringVar(&platform, "platform", "", "override platform")
	cmd.Flags().StringVar(&newtronServer, "newtron-server", "", "newtron-server URL (server-side runner uses this for topology discovery)")
	cmd.Flags().BoolVar(&noDeploy, "no-deploy", false, "skip topology deployment (for loopback/offline mode)")
	return cmd
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
