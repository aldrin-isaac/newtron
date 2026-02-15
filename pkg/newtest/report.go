package newtest

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StepStatus represents the outcome of a step or scenario.
type StepStatus string

const (
	StepStatusPassed  StepStatus = "PASS"
	StepStatusFailed  StepStatus = "FAIL"
	StepStatusSkipped StepStatus = "SKIP"
	StepStatusError   StepStatus = "ERROR"
)

// ScenarioResult holds the result of a single scenario execution.
type ScenarioResult struct {
	Name        string
	Topology    string
	Platform    string
	Status      StepStatus
	Duration    time.Duration
	Steps       []StepResult
	DeployError error
	SkipReason  string // set when Status==StepStatusSkipped (e.g. "requires 'bgp-converge' which failed")

	Repeat          int // total iterations requested (from scenario.repeat, 0 = no repeat)
	FailedIteration int // which iteration failed (0 = none; only set when Repeat > 1)
}

// StepResult holds the result of a single step execution.
type StepResult struct {
	Name      string
	Action    StepAction
	Status    StepStatus
	Duration  time.Duration
	Message   string
	Device    string
	Details   []DeviceResult
	Iteration int // 1-based iteration number (0 = no repeat)
}

// DeviceResult holds the result for a single device within a multi-device step.
type DeviceResult struct {
	Device  string
	Status  StepStatus
	Message string
}

// ReportGenerator produces test reports from scenario results.
type ReportGenerator struct {
	Results []*ScenarioResult
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

	fmt.Fprintf(f, "# newtest Report — %s\n\n", time.Now().Format(DateTimeFormat))

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
			if s.Status == StepStatusFailed {
				if !hasFailures {
					fmt.Fprintf(f, "\n## Failures\n\n")
					hasFailures = true
				}
				fmt.Fprintf(f, "### %s\n", r.Name)
				fmt.Fprintf(f, "Step %s (%s): %s\n\n", s.Name, s.Action, s.Message)
				for _, d := range s.Details {
					if d.Status == StepStatusFailed {
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
		if r.Status == StepStatusSkipped && r.SkipReason != "" {
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
			case StepStatusFailed:
				suite.Failures++
				tc.Failure = &junitFailure{
					Message: s.Message,
					Type:    string(s.Action),
				}
			case StepStatusSkipped:
				suite.Skipped++
				tc.Skipped = &junitSkipped{
					Message: s.Message,
				}
			case StepStatusError:
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
func statusVerb(s StepStatus) string {
	switch s {
	case StepStatusFailed:
		return "failed"
	case StepStatusError:
		return "errored"
	case StepStatusSkipped:
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
