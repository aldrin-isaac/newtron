// Package api implements the newtrun HTTP server.
//
// The server exposes newtrun's existing canonical types (RunState,
// ScenarioResult, StepResult, etc. from pkg/newtrun) over HTTP so
// consumers like the newtcon browser frontend can drive newtrun without
// filesystem access.
//
// Per DESIGN_PRINCIPLES_NEWTRON.md §46 (Wire Shape Mirrors Canonical
// Types), the HTTP responses serialize the canonical in-memory types
// directly. The wire types in this file are JSON-friendly mirrors of
// the existing report.go and scenario.go types — same fields, same
// meaning, JSON tags added (the existing types only have YAML tags
// because they were authored before the HTTP boundary existed).
package api

import (
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

// EventType identifies the SSE event kind. Mirrors the ProgressReporter
// callback names directly.
type EventType string

const (
	EventSuiteStart    EventType = "suite_start"
	EventScenarioStart EventType = "scenario_start"
	EventScenarioEnd   EventType = "scenario_end"
	EventStepStart     EventType = "step_start"
	EventStepProgress  EventType = "step_progress"
	EventStepEnd       EventType = "step_end"
	EventSuiteEnd      EventType = "suite_end"
)

// Event is one SSE event the server emits on GET /api/runs/{runKey}/events.
// Type discriminates the Payload's concrete shape. Satisfies
// httputil.Eventable so the generic SSE writer emits Type as the
// `event:` token and Payload as the JSON `data:` body.
type Event struct {
	Type    EventType `json:"type"`
	Payload any       `json:"payload"`
}

// Kind satisfies httputil.Eventable.
func (e Event) Kind() string { return string(e.Type) }

// Body satisfies httputil.Eventable.
func (e Event) Body() any { return e.Payload }

// SuiteStartPayload mirrors ProgressReporter.SuiteStart([]*Scenario).
// Scenarios are summarized to name + step count rather than serialized in
// full — the full Scenario objects include action-specific fields that
// browser consumers don't need at suite-start time. Per-step detail is
// surfaced incrementally via step_start / step_end events.
type SuiteStartPayload struct {
	Scenarios []ScenarioSummary `json:"scenarios"`
}

// ScenarioSummary is the per-scenario view returned by SuiteStart events
// and by GET /api/suites/{suite}/scenarios. The browser suite picker and
// `newtrun list <suite>` both render from this shape, so it carries the
// fields a chooser needs (name, topology, step count) plus dependency
// info (requires) so dependency-ordered lists can be rendered without a
// second call. Full scenario CRUD over HTTP is tracked in issue #33.
type ScenarioSummary struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Topology    string   `json:"topology"`
	Platform    string   `json:"platform,omitempty"`
	StepCount   int      `json:"step_count"`
	Requires    []string `json:"requires,omitempty"`
}

// ScenarioStartPayload mirrors ProgressReporter.ScenarioStart(name, index, total).
type ScenarioStartPayload struct {
	Name  string `json:"name"`
	Index int    `json:"index"`
	Total int    `json:"total"`
}

// ScenarioEndPayload mirrors ProgressReporter.ScenarioEnd(*ScenarioResult, ...).
type ScenarioEndPayload struct {
	Name        string                    `json:"name"`
	Topology    string                    `json:"topology,omitempty"`
	Platform    string                    `json:"platform,omitempty"`
	Status      newtrun.StepStatus        `json:"status"`
	Duration    string                    `json:"duration"`
	Steps       []StepResultPayload       `json:"steps,omitempty"`
	DeployError string                    `json:"deploy_error,omitempty"`
	SkipReason  string                    `json:"skip_reason,omitempty"`
	Index       int                       `json:"index"`
	Total       int                       `json:"total"`
}

// StepStartPayload mirrors ProgressReporter.StepStart(scenario, *Step, index, total).
type StepStartPayload struct {
	Scenario string             `json:"scenario"`
	Name     string             `json:"name"`
	Action   newtrun.StepAction `json:"action"`
	Index    int                `json:"index"`
	Total    int                `json:"total"`
}

// StepEndPayload mirrors ProgressReporter.StepEnd(scenario, *StepResult, index, total).
type StepEndPayload struct {
	Scenario string             `json:"scenario"`
	Result   StepResultPayload  `json:"result"`
	Index    int                `json:"index"`
	Total    int                `json:"total"`
}

// StepProgressPayload mirrors ProgressReporter.StepProgress(scenario,
// *Step, *sonic.DeviceOp, index). Op is the canonical DeviceOp shape —
// no wrapper type — per §46 (Wire Shape Mirrors Canonical Types) and
// ai-instructions §13 (Same Concept = Same Name).
//
// One event per device operation. The browser frontend's "watch device
// writes land in real time" UX renders one of these per row in the
// per-device-op timeline.
type StepProgressPayload struct {
	Scenario string                 `json:"scenario"`
	Step     string                 `json:"step"`
	Action   newtrun.StepAction     `json:"action"`
	Index    int                    `json:"index"`
	Op       sonic.DeviceOp   `json:"op"`
}

// SuiteEndPayload mirrors ProgressReporter.SuiteEnd. The Status field
// carries the terminal SuiteStatus (complete, failed, aborted, paused)
// so wire consumers can distinguish "the suite ran and N scenarios
// failed" from "the run was aborted mid-stream" — the two look
// identical if you only count results.
type SuiteEndPayload struct {
	Results  []ScenarioEndPayload `json:"results"`
	Status   newtrun.SuiteStatus  `json:"status"`
	Duration string               `json:"duration"`
}

// StepResultPayload mirrors newtrun.StepResult with JSON tags and a string
// duration (Go's time.Duration serializes as nanoseconds by default, which
// is awkward for browser consumers).
type StepResultPayload struct {
	Name      string                 `json:"name"`
	Action    newtrun.StepAction     `json:"action"`
	Status    newtrun.StepStatus     `json:"status"`
	Duration  string                 `json:"duration"`
	Message   string                 `json:"message,omitempty"`
	Details   []DeviceResultPayload  `json:"details,omitempty"`
	Iteration int                    `json:"iteration,omitempty"`
}

// DeviceResultPayload mirrors newtrun.DeviceResult.
type DeviceResultPayload struct {
	Device  string             `json:"device"`
	Status  newtrun.StepStatus `json:"status"`
	Message string             `json:"message,omitempty"`
}

// scenarioSummaryFrom converts a *newtrun.Scenario to its summary form.
func scenarioSummaryFrom(s *newtrun.Scenario) ScenarioSummary {
	return ScenarioSummary{
		Name:        s.Name,
		Description: s.Description,
		Topology:    s.Topology,
		Platform:    s.Platform,
		StepCount:   len(s.Steps),
	}
}

// scenarioEndFrom converts a *newtrun.ScenarioResult plus index/total into a
// JSON-friendly payload.
func scenarioEndFrom(r *newtrun.ScenarioResult, index, total int) ScenarioEndPayload {
	steps := make([]StepResultPayload, 0, len(r.Steps))
	for i := range r.Steps {
		steps = append(steps, stepResultFrom(&r.Steps[i]))
	}
	deployErr := ""
	if r.DeployError != nil {
		deployErr = r.DeployError.Error()
	}
	return ScenarioEndPayload{
		Name:        r.Name,
		Topology:    r.Topology,
		Platform:    r.Platform,
		Status:      r.Status,
		Duration:    durationString(r.Duration),
		Steps:       steps,
		DeployError: deployErr,
		SkipReason:  r.SkipReason,
		Index:       index,
		Total:       total,
	}
}

// stepResultFrom converts a *newtrun.StepResult into a JSON-friendly payload.
func stepResultFrom(r *newtrun.StepResult) StepResultPayload {
	details := make([]DeviceResultPayload, 0, len(r.Details))
	for _, d := range r.Details {
		details = append(details, DeviceResultPayload{
			Device:  d.Device,
			Status:  d.Status,
			Message: d.Message,
		})
	}
	return StepResultPayload{
		Name:      r.Name,
		Action:    r.Action,
		Status:    r.Status,
		Duration:  durationString(r.Duration),
		Message:   r.Message,
		Details:   details,
		Iteration: r.Iteration,
	}
}

// durationString renders a time.Duration in the same compact form newtrun's
// console reporter uses ("<1s", "5s", "2m30s").
func durationString(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	d = d.Round(time.Second)
	if d < time.Minute {
		return d.String()
	}
	return d.String()
}

// RunInfo is the response shape for GET /api/runs (list).
type RunInfo struct {
	Suite    string             `json:"suite"`
	Topology string             `json:"topology,omitempty"`
	Status   newtrun.SuiteStatus `json:"status"`
	Started  time.Time          `json:"started,omitempty"`
	Updated  time.Time          `json:"updated,omitempty"`
	Finished time.Time          `json:"finished,omitempty"`
}

// runInfoFrom summarizes a *newtrun.RunState to its list-view form.
func runInfoFrom(s *newtrun.RunState) RunInfo {
	return RunInfo{
		Suite:    s.Suite,
		Topology: s.Topology,
		Status:   s.Status,
		Started:  s.Started,
		Updated:  s.Updated,
		Finished: s.Finished,
	}
}

// TopologiesResponse is the response shape for GET /api/topologies. Returns
// the topology names discoverable under the configured topologies base
// directory.
type TopologiesResponse struct {
	Topologies []string `json:"topologies"`
}

// SuitesResponse is the response shape for GET /api/suites. Returns the suite
// names discoverable under the configured suites base directory.
type SuitesResponse struct {
	Suites []string `json:"suites"`
}

// HealthResponse is the response shape for GET /api/health.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// SuiteScenariosResponse is the response shape for GET
// /api/suites/{suite}/scenarios.
type SuiteScenariosResponse struct {
	Suite     string            `json:"suite"`
	Topology  string            `json:"topology,omitempty"`
	Scenarios []ScenarioSummary `json:"scenarios"`
}

// StartRunRequest is the body for POST /api/runs. Names the suite to run
// and optional run-shaping options that mirror the existing CLI flags.
//
// Suite (named) and SuiteDir (path) are alternatives — exactly one must
// be set. Suite resolves under the server's SuitesBase; SuiteDir is an
// absolute filesystem path the server reads directly. The path mode
// matches the original CLI's --dir flag and honors the server's
// filesystem permissions; security posture is bounded by deployment-
// time trust controls (loopback default, reverse proxy for non-loopback).
type StartRunRequest struct {
	// Suite is the file-backed suite name under the server's SuitesBase.
	// Mutually exclusive with SuiteDir.
	Suite string `json:"suite,omitempty"`

	// SuiteDir is an absolute path to a suite directory the server reads
	// directly. Mutually exclusive with Suite.
	SuiteDir string `json:"suite_dir,omitempty"`

	// Scenario, if set, runs a single scenario from the suite. Mutually
	// exclusive with Target and All.
	Scenario string `json:"scenario,omitempty"`

	// Target, if set, runs the minimal dependency chain to reach the named
	// scenario. Mutually exclusive with Scenario and All.
	Target string `json:"target,omitempty"`

	// All requests every scenario in the suite. Default when Scenario and
	// Target are both unset.
	All bool `json:"all,omitempty"`

	// Platform overrides the per-scenario platform.
	Platform string `json:"platform,omitempty"`

	// NoDeploy skips topology deployment. Used by tests that already have
	// the topology up.
	NoDeploy bool `json:"no_deploy,omitempty"`

	// Verbose controls per-step output in the console reporter chained
	// after the server-side reporters. The browser frontend uses the
	// HTTPReporter stream regardless of this flag.
	Verbose bool `json:"verbose,omitempty"`

	// NewtronServer is the newtron-server URL the runner should connect to
	// for topology discovery. If empty, the server uses its configured
	// default (Config.NewtronServer).
	NewtronServer string `json:"newtron_server,omitempty"`

	// NetworkID overrides the newtron network identifier for this run. If
	// empty, the server uses its configured default (Config.NetworkID).
	NetworkID string `json:"network_id,omitempty"`

	// JUnitPath, when non-empty, instructs the run to remember a JUnit
	// XML output path. The server does not write the file — it's a hint
	// the CLI uses to coordinate report generation client-side. Present
	// here for CLI compatibility with the original --junit flag.
	JUnitPath string `json:"junit_path,omitempty"`
}

// StartRunResponse is the body returned by POST /api/runs.
type StartRunResponse struct {
	Suite   string    `json:"suite"`
	Started time.Time `json:"started"`
}

// InlineRunRequest is the body for POST /api/runs/inline. Per §46 the
// request shape mirrors the substrate: ScenarioYAML carries the same
// YAML grammar the file-based parser consumes, NOT a derived shape.
// The browser frontend's compose layer renders operator clicks into
// the same Scenario YAML a test engineer would write into a suite
// directory.
type InlineRunRequest struct {
	// ScenarioYAML is the inline scenario body. Parsed via the same
	// ParseScenarioBytes the rest of the framework uses.
	ScenarioYAML string `json:"scenario_yaml"`

	// NewtronServer overrides the server's default newtron-server URL
	// for this run only. Empty = use the server's configured default.
	NewtronServer string `json:"newtron_server,omitempty"`

	// TimeoutSeconds overrides the safety policy's wall-time budget for
	// this run only. 0 = use the policy's default (60 seconds).
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`

	// AllowReconcile opts into permitting the topology-reconcile
	// action for this scenario. Default false (the high-impact gate).
	AllowReconcile bool `json:"allow_reconcile,omitempty"`
}

// InlineRunResponse is the body returned by POST /api/runs/inline.
// RunID is the UUID the server allocated for this run; subsequent
// GET / POST / DELETE calls use it as the path parameter.
type InlineRunResponse struct {
	RunID   string    `json:"run_id"`
	Started time.Time `json:"started"`
}
