package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

// newReportCmd implements the `newtrun report` subcommand (issue #29 R2).
// Replaces the dropped `--junit` flag on `newtrun start` — the new
// entry point fetches a completed run's state from newtrun-server and
// renders JUnit XML or markdown locally, reusing the existing
// ReportGenerator. Operators run it after `newtrun start` finishes (or
// in CI, against a server that already holds the results).
//
// The state lives server-side in `~/.newtron/newtrun/<suite>/state.json`;
// the client reads it through GET /newtrun/v1/runs/<suite> rather than
// touching the file directly so it works against a remote server.
func newReportCmd() *cobra.Command {
	var (
		format string
		out    string
	)
	cmd := &cobra.Command{
		Use:   "report <suite>",
		Short: "Render a JUnit XML or markdown report from a finished run",
		Long: `Fetch the most recent run state for <suite> from newtrun-server
and render a report file locally. Useful for CI integrations that
consume JUnit XML, or for sharing markdown summaries.

  newtrun report 2node-vs-primitive --format junit --out report.xml
  newtrun report 2node-vs-primitive --format markdown --out report.md

If --out is omitted, the report is written to stdout.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			suite := args[0]
			if format != "junit" && format != "markdown" {
				return fmt.Errorf("--format must be junit or markdown, got %q", format)
			}
			c := newClient()
			ctx := cmd.Context()
			if err := requireServer(ctx, c); err != nil {
				return err
			}
			state, err := c.GetRun(ctx, suite)
			if err != nil {
				return fmt.Errorf("fetch run state for %q: %w", suite, err)
			}
			if state == nil {
				return fmt.Errorf("no run state found for suite %q", suite)
			}
			gen := &newtrun.ReportGenerator{Results: newtrun.ResultsFromRunState(state)}
			path := out
			if path == "" {
				// Default: write to stdout via a temp marker. ReportGenerator
				// writes to files, so we keep it filesystem-bound; a "-"
				// sentinel for stdout would require touching the generator's
				// internals. Leave that for a future refactor when there's
				// a stdout consumer that justifies the change.
				return fmt.Errorf("--out is required (stdout streaming not yet supported)")
			}
			switch format {
			case "junit":
				if err := gen.WriteJUnit(path); err != nil {
					return fmt.Errorf("write JUnit report: %w", err)
				}
			case "markdown":
				if err := gen.WriteMarkdown(path); err != nil {
					return fmt.Errorf("write markdown report: %w", err)
				}
			}
			fmt.Fprintf(cmd.OutOrStderr(), "wrote %s report to %s\n", format, path)
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "junit", "report format: junit or markdown")
	cmd.Flags().StringVarP(&out, "out", "o", "", "output path (required)")
	return cmd
}
