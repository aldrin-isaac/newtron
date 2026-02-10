package newtest

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Status represents the outcome of a step or scenario.
type Status string

const (
	StatusPassed  Status = "PASS"
	StatusFailed  Status = "FAIL"
	StatusSkipped Status = "SKIP"
	StatusError   Status = "ERROR"
)

// ScenarioResult holds the result of a single scenario execution.
type ScenarioResult struct {
	Name        string
	Topology    string
	Platform    string
	Status      Status
	Duration    time.Duration
	Steps       []StepResult
	DeployError error
	SkipReason  string // set when Status==StatusSkipped (e.g. "requires 'bgp-converge' which failed")

	Repeat          int // total iterations requested (from scenario.repeat, 0 = no repeat)
	FailedIteration int // which iteration failed (0 = none; only set when Repeat > 1)
}

// StepResult holds the result of a single step execution.
type StepResult struct {
	Name      string
	Action    StepAction
	Status    Status
	Duration  time.Duration
	Message   string
	Device    string
	Details   []DeviceResult
	Iteration int // 1-based iteration number (0 = no repeat)
}

// DeviceResult holds the result for a single device within a multi-device step.
type DeviceResult struct {
	Device  string
	Status  Status
	Message string
}

// ReportGenerator produces test reports from scenario results.
type ReportGenerator struct {
	Results []*ScenarioResult
}

// statusSymbol returns the console symbol for a status.
func statusSymbol(s Status) string {
	switch s {
	case StatusPassed:
		return "\u2713" // ✓
	case StatusFailed:
		return "\u2717" // ✗
	case StatusSkipped:
		return "\u2298" // ⊘
	case StatusError:
		return "!"
	default:
		return "?"
	}
}

// PrintConsole writes human-readable output to w.
func (g *ReportGenerator) PrintConsole(w io.Writer) {
	for _, r := range g.Results {
		fmt.Fprintf(w, "\nnewtest: %s (%s topology, %s)\n\n", r.Name, r.Topology, r.Platform)

		if r.Status == StatusSkipped && r.SkipReason != "" {
			fmt.Fprintf(w, "  %s skipped: %s\n\n", statusSymbol(StatusSkipped), r.SkipReason)
			continue
		}

		if r.DeployError != nil {
			fmt.Fprintf(w, "  ! Deploy failed: %s\n\n", r.DeployError)
			continue
		}

		if r.Repeat > 1 {
			g.printRepeatConsole(w, r)
		} else {
			g.printStepsConsole(w, r)
		}
	}
}

// printStepsConsole prints step details for a single-run scenario.
func (g *ReportGenerator) printStepsConsole(w io.Writer, r *ScenarioResult) {
	fmt.Fprintf(w, "Running steps...\n")
	for i, step := range r.Steps {
		fmt.Fprintf(w, "  [%d/%d] %s\n", i+1, len(r.Steps), step.Name)
		if len(step.Details) > 0 {
			for _, d := range step.Details {
				fmt.Fprintf(w, "    %s %s: %s\n", statusSymbol(d.Status), d.Device, d.Message)
			}
		} else if step.Message != "" {
			fmt.Fprintf(w, "    %s %s\n", statusSymbol(step.Status), step.Message)
		}
		fmt.Fprintln(w)
	}

	passed := 0
	for _, s := range r.Steps {
		if s.Status == StatusPassed {
			passed++
		}
	}
	fmt.Fprintf(w, "%s: %s (%d/%d steps passed, %s)\n\n",
		r.Status, r.Name, passed, len(r.Steps), r.Duration.Round(time.Second))
}

// printRepeatConsole prints a concise summary for repeated scenarios.
// Only shows step details for the failed iteration (if any).
func (g *ReportGenerator) printRepeatConsole(w io.Writer, r *ScenarioResult) {
	if r.FailedIteration > 0 {
		// Show which iteration failed and its step details
		fmt.Fprintf(w, "Running %d iterations...\n", r.Repeat)
		fmt.Fprintf(w, "  %s iterations 1-%d passed\n", statusSymbol(StatusPassed), r.FailedIteration-1)
		fmt.Fprintf(w, "  %s iteration %d:\n", statusSymbol(StatusFailed), r.FailedIteration)

		for _, step := range r.Steps {
			if step.Iteration != r.FailedIteration {
				continue
			}
			fmt.Fprintf(w, "    [%s] %s", statusSymbol(step.Status), step.Name)
			if step.Message != "" && step.Status != StatusPassed {
				fmt.Fprintf(w, ": %s", step.Message)
			}
			fmt.Fprintln(w)
			for _, d := range step.Details {
				if d.Status != StatusPassed {
					fmt.Fprintf(w, "      %s %s: %s\n", statusSymbol(d.Status), d.Device, d.Message)
				}
			}
		}
		fmt.Fprintf(w, "\n%s: %s (failed on iteration %d/%d, %s)\n\n",
			r.Status, r.Name, r.FailedIteration, r.Repeat, r.Duration.Round(time.Second))
	} else {
		fmt.Fprintf(w, "%s: %s (%d/%d iterations passed, %s)\n\n",
			r.Status, r.Name, r.Repeat, r.Repeat, r.Duration.Round(time.Second))
	}
}

// WriteMarkdown writes a markdown report to the given path.
func (g *ReportGenerator) WriteMarkdown(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "# newtest Report — %s\n\n", time.Now().Format("2006-01-02 15:04:05"))

	// Summary table
	fmt.Fprintln(f, "| Scenario | Topology | Platform | Result | Duration | Note |")
	fmt.Fprintln(f, "|----------|----------|----------|--------|----------|------|")
	for _, r := range g.Results {
		note := ""
		if r.SkipReason != "" {
			note = r.SkipReason
		}
		if r.Repeat > 1 && r.FailedIteration > 0 {
			note = fmt.Sprintf("failed on iteration %d/%d", r.FailedIteration, r.Repeat)
		} else if r.Repeat > 1 {
			note = fmt.Sprintf("%d iterations", r.Repeat)
		}
		fmt.Fprintf(f, "| %s | %s | %s | %s | %s | %s |\n",
			r.Name, r.Topology, r.Platform, r.Status,
			r.Duration.Round(time.Second), note)
	}

	// Failures section
	hasFailures := false
	for _, r := range g.Results {
		for _, s := range r.Steps {
			if s.Status == StatusFailed {
				if !hasFailures {
					fmt.Fprintf(f, "\n## Failures\n\n")
					hasFailures = true
				}
				fmt.Fprintf(f, "### %s\n", r.Name)
				fmt.Fprintf(f, "Step %s (%s): %s\n\n", s.Name, s.Action, s.Message)
				for _, d := range s.Details {
					if d.Status == StatusFailed {
						fmt.Fprintf(f, "  %s: %s\n", d.Device, d.Message)
					}
				}
			}
		}
	}

	return nil
}

// WriteJUnit writes a JUnit XML report for CI integration.
func (g *ReportGenerator) WriteJUnit(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	suites := junitTestSuites{}

	for _, r := range g.Results {
		suite := junitTestSuite{
			Name: r.Name,
			Time: r.Duration.Seconds(),
		}

		// Scenario-level skip: emit a single skipped test case
		if r.Status == StatusSkipped && r.SkipReason != "" {
			suite.Tests = 1
			suite.Skipped = 1
			suite.Cases = append(suite.Cases, junitTestCase{
				Name:      r.Name,
				ClassName: r.Name,
				Time:      0,
				Skipped:   &junitSkipped{Message: r.SkipReason},
			})
			suites.Suites = append(suites.Suites, suite)
			continue
		}

		for _, s := range r.Steps {
			suite.Tests++
			stepName := s.Name
			if s.Iteration > 0 {
				stepName = fmt.Sprintf("[iter %d] %s", s.Iteration, s.Name)
			}
			tc := junitTestCase{
				Name:      stepName,
				ClassName: r.Name,
				Time:      s.Duration.Seconds(),
			}

			switch s.Status {
			case StatusFailed:
				suite.Failures++
				tc.Failure = &junitFailure{
					Message: s.Message,
					Type:    string(s.Action),
				}
			case StatusSkipped:
				suite.Skipped++
				tc.Skipped = &junitSkipped{
					Message: s.Message,
				}
			case StatusError:
				suite.Errors++
				tc.Error = &junitError{
					Message: s.Message,
					Type:    string(s.Action),
				}
			}

			suite.Cases = append(suite.Cases, tc)
		}

		suites.Suites = append(suites.Suites, suite)
	}

	data, err := xml.MarshalIndent(suites, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, append([]byte(xml.Header), data...), 0o644)
}

// statusVerb returns a past-tense verb for a status, used in skip reasons.
func statusVerb(s Status) string {
	switch s {
	case StatusFailed:
		return "failed"
	case StatusError:
		return "errored"
	case StatusSkipped:
		return "was skipped"
	default:
		return string(s)
	}
}

// JUnit XML types

type junitTestSuites struct {
	XMLName xml.Name         `xml:"testsuites"`
	Suites  []junitTestSuite `xml:"testsuite"`
}

type junitTestSuite struct {
	Name     string          `xml:"name,attr"`
	Tests    int             `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Errors   int             `xml:"errors,attr"`
	Skipped  int             `xml:"skipped,attr"`
	Time     float64         `xml:"time,attr"`
	Cases    []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Time      float64       `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
	Error     *junitError   `xml:"error,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
}

type junitSkipped struct {
	Message string `xml:"message,attr"`
}

type junitError struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
}
