package newtest

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/newtron-network/newtron/pkg/cli"
)

// ProgressReporter receives lifecycle callbacks during test execution.
type ProgressReporter interface {
	SuiteStart(scenarios []*Scenario)
	ScenarioStart(name string, index, total int)
	ScenarioEnd(result *ScenarioResult, index, total int)
	StepStart(scenario string, step *Step, index, total int)
	StepEnd(scenario string, result *StepResult, index, total int)
	SuiteEnd(results []*ScenarioResult, duration time.Duration)
}

// ConsoleProgress is an append-only terminal progress reporter.
// It never uses ANSI cursor rewriting, so output is safe for pipes, CI,
// and scrollback buffers.
type ConsoleProgress struct {
	W       io.Writer
	Verbose bool

	suiteName string
	dotWidth  int
}

// NewConsoleProgress creates a ConsoleProgress writing to stdout.
func NewConsoleProgress(verbose bool) *ConsoleProgress {
	return &ConsoleProgress{
		W:       os.Stdout,
		Verbose: verbose,
	}
}

func (p *ConsoleProgress) SuiteStart(scenarios []*Scenario) {
	if len(scenarios) == 0 {
		return
	}

	// Derive suite name from common topology + detect shared properties
	topology := scenarios[0].Topology
	platform := scenarios[0].Platform
	p.suiteName = topology

	// Compute max scenario name length for dot padding
	maxName := 0
	for _, s := range scenarios {
		if len(s.Name) > maxName {
			maxName = len(s.Name)
		}
	}
	p.dotWidth = maxName + 6 // padding for dots

	fmt.Fprintf(p.W, "\nnewtest: %d scenarios, topology: %s, platform: %s\n\n",
		len(scenarios), topology, platform)

	// Print scenario roster
	fmt.Fprintf(p.W, "  %-4s  %-*s  %s\n", "#", p.dotWidth-6, "SCENARIO", "STEPS")
	for i, s := range scenarios {
		fmt.Fprintf(p.W, "  %-4d  %-*s  %d\n", i+1, p.dotWidth-6, s.Name, len(s.Steps))
	}
	fmt.Fprintln(p.W)
}

func (p *ConsoleProgress) ScenarioStart(name string, index, total int) {
	if p.Verbose {
		fmt.Fprintf(p.W, "  [%d/%d]  %s\n", index+1, total, name)
	}
}

func (p *ConsoleProgress) ScenarioEnd(result *ScenarioResult, index, total int) {
	tag := fmt.Sprintf("[%d/%d]", index+1, total)

	if p.Verbose {
		// Surface deploy/connect errors that otherwise have no visible output
		if result.DeployError != nil {
			fmt.Fprintf(p.W, "          %s\n", cli.Dim(result.DeployError.Error()))
		}
		fmt.Fprintf(p.W, "          %s  (%s)\n\n", p.colorStatus(result.Status), p.formatDuration(result.Duration))
		return
	}

	padded := cli.DotPad(result.Name, p.dotWidth)

	switch result.Status {
	case StatusSkipped:
		fmt.Fprintf(p.W, "  %-7s %s %s\n", tag, padded, cli.Yellow("SKIP"))
	case StatusPassed:
		fmt.Fprintf(p.W, "  %-7s %s %s  (%s)\n", tag, padded, cli.Green("PASS"), p.formatDuration(result.Duration))
	case StatusFailed:
		fmt.Fprintf(p.W, "  %-7s %s %s  (%s)\n", tag, padded, cli.Red("FAIL"), p.formatDuration(result.Duration))
	case StatusError:
		fmt.Fprintf(p.W, "  %-7s %s %s  (%s)\n", tag, padded, cli.Red("ERROR"), p.formatDuration(result.Duration))
	}
}

func (p *ConsoleProgress) StepStart(scenario string, step *Step, index, total int) {
	// Only show in verbose mode
}

func (p *ConsoleProgress) StepEnd(scenario string, result *StepResult, index, total int) {
	if !p.Verbose {
		return
	}

	stepDot := cli.DotPad(result.Name, p.dotWidth-10)
	tag := fmt.Sprintf("[%d/%d]", index+1, total)
	fmt.Fprintf(p.W, "          %s %s %s  (%s)\n", tag, stepDot, p.colorStatus(result.Status), p.formatDuration(result.Duration))

	// Print failure details
	if result.Status == StatusFailed || result.Status == StatusError {
		if result.Message != "" {
			fmt.Fprintf(p.W, "               %s\n", cli.Dim(result.Message))
		}
		for _, d := range result.Details {
			if d.Status != StatusPassed {
				fmt.Fprintf(p.W, "               %s: %s\n", d.Device, cli.Dim(d.Message))
			}
		}
	}
}

func (p *ConsoleProgress) SuiteEnd(results []*ScenarioResult, duration time.Duration) {
	passed, failed, skipped, errored := 0, 0, 0, 0
	for _, r := range results {
		switch r.Status {
		case StatusPassed:
			passed++
		case StatusFailed:
			failed++
		case StatusSkipped:
			skipped++
		case StatusError:
			errored++
		}
	}

	fmt.Fprintf(p.W, "\n---\n")
	fmt.Fprintf(p.W, "newtest: %d scenarios", len(results))

	parts := []string{}
	if passed > 0 {
		parts = append(parts, cli.Green(fmt.Sprintf("%d passed", passed)))
	}
	if failed > 0 {
		parts = append(parts, cli.Red(fmt.Sprintf("%d failed", failed)))
	}
	if errored > 0 {
		parts = append(parts, cli.Red(fmt.Sprintf("%d errored", errored)))
	}
	if skipped > 0 {
		parts = append(parts, cli.Yellow(fmt.Sprintf("%d skipped", skipped)))
	}
	if len(parts) > 0 {
		fmt.Fprintf(p.W, ": %s", strings.Join(parts, ", "))
	}
	fmt.Fprintf(p.W, "  (%s)\n", p.formatDuration(duration))

	// Print failure details
	if failed+errored > 0 {
		fmt.Fprintf(p.W, "\n  FAILED:\n")
		for i, r := range results {
			if r.Status != StatusFailed && r.Status != StatusError {
				continue
			}
			fmt.Fprintf(p.W, "    [%d]  %s\n", i+1, r.Name)
			if r.DeployError != nil {
				fmt.Fprintf(p.W, "         deploy: %s\n", r.DeployError)
				continue
			}
			for _, step := range r.Steps {
				if step.Status == StatusFailed || step.Status == StatusError {
					msg := step.Message
					if msg == "" {
						var msgs []string
						for _, d := range step.Details {
							if d.Status != StatusPassed && d.Message != "" {
								msgs = append(msgs, d.Device+": "+d.Message)
							}
						}
						if len(msgs) > 0 {
							msg = strings.Join(msgs, "; ")
						} else {
							msg = string(step.Status)
						}
					}
					fmt.Fprintf(p.W, "         step %q (%s): %s\n", step.Name, step.Action, msg)
				}
			}
		}
	}

	// Print skip details
	if skipped > 0 {
		fmt.Fprintf(p.W, "\n  SKIPPED:\n")
		for i, r := range results {
			if r.Status != StatusSkipped {
				continue
			}
			reason := r.SkipReason
			if reason == "" {
				reason = "skipped"
			}
			padded := cli.DotPad(r.Name, p.dotWidth)
			fmt.Fprintf(p.W, "    [%d]  %s %s\n", i+1, padded, reason)
		}
	}

	fmt.Fprintln(p.W)
}

func (p *ConsoleProgress) colorStatus(s Status) string {
	switch s {
	case StatusPassed:
		return cli.Green(string(s))
	case StatusFailed:
		return cli.Red(string(s))
	case StatusSkipped:
		return cli.Yellow(string(s))
	case StatusError:
		return cli.Red(string(s))
	default:
		return string(s)
	}
}

func (p *ConsoleProgress) formatDuration(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if s == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dm%02ds", m, s)
}

// StateReporter wraps a ProgressReporter and persists run state after each
// scenario completes. This enables the status command and resume on pause.
type StateReporter struct {
	Inner ProgressReporter
	State *RunState
}

func (r *StateReporter) SuiteStart(scenarios []*Scenario) {
	// Initialize scenario states
	r.State.Scenarios = make([]ScenarioState, len(scenarios))
	for i, s := range scenarios {
		r.State.Scenarios[i] = ScenarioState{Name: s.Name}
	}
	_ = SaveRunState(r.State)
	r.Inner.SuiteStart(scenarios)
}

func (r *StateReporter) ScenarioStart(name string, index, total int) {
	r.Inner.ScenarioStart(name, index, total)
}

func (r *StateReporter) ScenarioEnd(result *ScenarioResult, index, total int) {
	if index < len(r.State.Scenarios) {
		r.State.Scenarios[index].Status = string(result.Status)
		r.State.Scenarios[index].Duration = result.Duration.Round(time.Second).String()
	}
	_ = SaveRunState(r.State)
	r.Inner.ScenarioEnd(result, index, total)
}

func (r *StateReporter) StepStart(scenario string, step *Step, index, total int) {
	r.Inner.StepStart(scenario, step, index, total)
}

func (r *StateReporter) StepEnd(scenario string, result *StepResult, index, total int) {
	r.Inner.StepEnd(scenario, result, index, total)
}

func (r *StateReporter) SuiteEnd(results []*ScenarioResult, duration time.Duration) {
	_ = SaveRunState(r.State)
	r.Inner.SuiteEnd(results, duration)
}
