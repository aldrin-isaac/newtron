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
}

// StepResult holds the result of a single step execution.
type StepResult struct {
	Name     string
	Action   StepAction
	Status   Status
	Duration time.Duration
	Message  string
	Device   string
	Details  []DeviceResult
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

		if r.DeployError != nil {
			fmt.Fprintf(w, "  ! Deploy failed: %s\n\n", r.DeployError)
			continue
		}

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
	fmt.Fprintln(f, "| Scenario | Topology | Platform | Result | Duration |")
	fmt.Fprintln(f, "|----------|----------|----------|--------|----------|")
	for _, r := range g.Results {
		fmt.Fprintf(f, "| %s | %s | %s | %s | %s |\n",
			r.Name, r.Topology, r.Platform, r.Status,
			r.Duration.Round(time.Second))
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

		for _, s := range r.Steps {
			suite.Tests++
			tc := junitTestCase{
				Name:      s.Name,
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
