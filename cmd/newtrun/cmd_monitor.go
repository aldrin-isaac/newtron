package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
	"github.com/aldrin-isaac/newtron/pkg/newtrun/api"
)

// newMonitorCmd implements `newtrun monitor <suite>` (issue #29 R4).
// Subscribes to the suite's SSE event stream and refreshes the scenario-
// oriented dashboard on each event. Distinct from `newtrun status
// --monitor` (which polls every 2 seconds) — the SSE path wakes the
// renderer the instant a scenario starts, a step lands, or the suite
// ends, so an operator watching a long run sees changes without the
// 2-second lag.
//
// The renderer reuses printSuiteStatus over a freshly-fetched RunState:
// the SSE event signals "something changed," the state fetch supplies
// the authoritative view. Re-implementing the server-side StateReporter
// on the client would be a duplicate state machine — instead the client
// uses events as wake-ups and state as truth.
func newMonitorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "monitor <suite>",
		Short: "Live SSE-driven dashboard for a running suite",
		Long: `Subscribes to the suite's SSE event stream and refreshes the
scenario-oriented dashboard on each event.

  newtrun monitor 2node-vs-primitive

Compared to 'newtrun status --monitor' (2-second polling), the SSE path
wakes the renderer immediately on every event — scenario starts, step
ends, suite ends — so an operator watching a long run sees changes
without the polling lag. Press Ctrl-C to detach; the run continues
server-side.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			suite := args[0]
			c := newClient()
			ctx := cmd.Context()
			if err := requireServer(ctx, c); err != nil {
				return err
			}
			return monitorSuiteSSE(ctx, suite)
		},
	}
	return cmd
}

// monitorSuiteSSE renders the dashboard once, then subscribes to the
// SSE event stream and re-renders on every event until the suite ends
// (or the operator cancels). Errors from the event stream are surfaced
// — a dropped connection is more useful as a failure than a silent
// stall on a stale view.
func monitorSuiteSSE(ctx context.Context, suite string) error {
	c := newClient()

	// Initial render. If the run hasn't started yet, this prints the
	// state-not-found error and exits — the operator can re-run after
	// 'newtrun start <suite>'.
	if err := printSuiteStatus(suite, false, true); err != nil {
		return err
	}

	terminated := false
	streamErr := c.StreamEvents(ctx, suite, func(ev api.Event) {
		if terminated {
			return
		}
		// Re-render on every event. Cheap (one GET to the server's
		// state.json equivalent); always-current; reuses every
		// formatting branch in printSuiteStatus.
		fmt.Print("\033[2J\033[H") // clear screen, cursor to top
		if err := printSuiteStatus(suite, false, true); err != nil {
			fmt.Printf("  error: %v\n", err)
		}
		if ev.Type == api.EventSuiteEnd {
			terminated = true
		}
	})

	// Re-fetch terminal state and render once more after the stream
	// closes — the last event might have arrived during the render and
	// the post-end refresh ensures the final view is consistent.
	if state, _ := fetchRunStateViaClient(suite); state != nil &&
		state.Status != newtrun.SuiteStatusRunning &&
		state.Status != newtrun.SuiteStatusPausing {
		fmt.Print("\033[2J\033[H")
		_ = printSuiteStatus(suite, false, true)
	}
	return streamErr
}
