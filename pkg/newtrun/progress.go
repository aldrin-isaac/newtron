package newtrun

import (
	"fmt"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// ProgressReporter receives lifecycle callbacks during test execution.
//
// StepProgress is a per-device-operation event delivered between
// StepStart and StepEnd. Producers (currently: none in this repo; the
// newtron-action SSE consumer is the planned producer once newtron Phase
// 2b lands upstream) call StepProgress as each device operation
// completes. The payload type sonic.DeviceOp is reused directly from
// newtron — per ai-instructions §13 (Same Concept = Same Name) and
// DESIGN_PRINCIPLES_NEWTRON §46 (Wire Shape Mirrors Canonical Types),
// the same type that newtron's WriteResult.DeviceOps uses is the type
// the test framework forwards. No parallel device-op type.
//
// Sinks that cannot meaningfully render per-device-op events implement
// StepProgress as a no-op. Sinks that can (HTTPReporter for SSE;
// StateReporter for state.json) surface the events at the appropriate
// granularity.
type ProgressReporter interface {
	// SuiteStart fires once per run with the resolved suite metadata
	// (topology + platform are suite-level, not per-scenario) and the
	// dependency-ordered scenarios that will execute. Reporters use
	// the metadata in roster headers, SSE payloads, and persisted
	// state so consumers can show "running suite X on topology Y"
	// without a separate fetch.
	SuiteStart(suiteNetwork, suitePlatform string, scenarios []*Scenario)
	ScenarioStart(name string, index, total int)
	ScenarioEnd(result *ScenarioResult, index, total int)
	StepStart(scenario string, step *Step, index, total int)
	StepProgress(scenario string, step *Step, op *sonic.DeviceOp, index int)
	StepEnd(scenario string, result *StepResult, index, total int)
	SuiteEnd(results []*ScenarioResult, status SuiteStatus, duration time.Duration)
}

// formatDurationCompact formats a duration in a human-readable compact form.
func formatDurationCompact(d time.Duration) string {
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
//
// Save lets the caller pick which namespace the state file lives in.
// Defaults to SaveRunState (suite namespace under ~/.newtron/newtrun/<suite>/).
// The inline-runs handler injects SaveInlineRunState so the state lives under
// ~/.newtron/newtrun/_inline/<id>/ — keeping the suite directory undisturbed
// per the namespace-separation requirement in the inline-runs spec.
type StateReporter struct {
	Inner ProgressReporter
	State *RunState
	Save  func(*RunState) error

	scenarioIndex int // tracks current scenario index for StepStart

	// currentStepDeviceOps buffers DeviceOp events received via
	// StepProgress between StepStart and StepEnd. Flushed onto the
	// StepState's DeviceOps slice when StepEnd creates the persistent
	// record. Cleared at every StepStart.
	currentStepDeviceOps []sonic.DeviceOp
}

// save invokes the configured save function, falling back to SaveRunState
// when none is set. Centralizing this avoids a nil-check at every callback.
func (r *StateReporter) save() error {
	if r.Save != nil {
		return r.Save(r.State)
	}
	return SaveRunState(r.State)
}

func (r *StateReporter) SuiteStart(suiteNetwork, suitePlatform string, scenarios []*Scenario) {
	// Capture suite-level metadata on the run state. Topology was
	// previously read off scenarios[0].Network, which is now always
	// empty (LoadSuite rejects per-scenario topology).
	if r.State.Network == "" {
		r.State.Network = suiteNetwork
	}
	if r.State.Platform == "" {
		r.State.Platform = suitePlatform
	}
	// Initialize scenario states with metadata
	r.State.Scenarios = make([]ScenarioState, len(scenarios))
	for i, s := range scenarios {
		r.State.Scenarios[i] = ScenarioState{
			Name:        s.Name,
			Description: s.Description,
			TotalSteps:  len(s.Steps),
			Requires:    s.Requires,
		}
	}
	if err := r.save(); err != nil {
		util.Logger.Warnf("save run state: %v", err)
	}
	if r.Inner != nil {
		r.Inner.SuiteStart(suiteNetwork, suitePlatform, scenarios)
	}
}

func (r *StateReporter) ScenarioStart(name string, index, total int) {
	r.scenarioIndex = index
	if index < len(r.State.Scenarios) {
		r.State.Scenarios[index].Status = "running"
	}
	if err := r.save(); err != nil {
		util.Logger.Warnf("save run state: %v", err)
	}
	if r.Inner != nil {
		r.Inner.ScenarioStart(name, index, total)
	}
}

func (r *StateReporter) ScenarioEnd(result *ScenarioResult, index, total int) {
	if index < len(r.State.Scenarios) {
		r.State.Scenarios[index].Status = string(result.Status)
		r.State.Scenarios[index].Duration = result.Duration.Round(time.Second).String()
		r.State.Scenarios[index].CurrentStep = ""
		r.State.Scenarios[index].CurrentStepAction = ""
		r.State.Scenarios[index].CurrentStepIndex = 0
		r.State.Scenarios[index].SkipReason = result.SkipReason
	}
	if err := r.save(); err != nil {
		util.Logger.Warnf("save run state: %v", err)
	}
	if r.Inner != nil {
		r.Inner.ScenarioEnd(result, index, total)
	}
}

func (r *StateReporter) StepStart(scenario string, step *Step, index, total int) {
	if r.scenarioIndex < len(r.State.Scenarios) {
		r.State.Scenarios[r.scenarioIndex].CurrentStep = step.Name
		r.State.Scenarios[r.scenarioIndex].CurrentStepAction = string(step.Action)
		r.State.Scenarios[r.scenarioIndex].CurrentStepIndex = index
	}
	if err := r.save(); err != nil {
		util.Logger.Warnf("save run state: %v", err)
	}
	// Track current step's intended DeviceOps slice. We can't append here
	// directly because the StepEnd handler creates the StepState entry;
	// instead, we buffer events on the reporter and flush them at StepEnd.
	r.currentStepDeviceOps = nil
	if r.Inner != nil {
		r.Inner.StepStart(scenario, step, index, total)
	}
}

func (r *StateReporter) StepProgress(scenario string, step *Step, op *sonic.DeviceOp, index int) {
	if op != nil {
		r.currentStepDeviceOps = append(r.currentStepDeviceOps, *op)
	}
	if r.Inner != nil {
		r.Inner.StepProgress(scenario, step, op, index)
	}
}

func (r *StateReporter) StepEnd(scenario string, result *StepResult, index, total int) {
	// Incrementally persist each step result so `newtrun status --detail`
	// shows live progress while a scenario is still running.
	if r.scenarioIndex < len(r.State.Scenarios) {
		r.State.Scenarios[r.scenarioIndex].Steps = append(
			r.State.Scenarios[r.scenarioIndex].Steps,
			StepState{
				Name:      result.Name,
				Action:    string(result.Action),
				Status:    string(result.Status),
				Duration:  formatDurationCompact(result.Duration),
				Message:   result.Message,
				DeviceOps: r.currentStepDeviceOps,
			},
		)
		r.currentStepDeviceOps = nil
		if err := r.save(); err != nil {
			util.Logger.Warnf("save run state: %v", err)
		}
	}
	if r.Inner != nil {
		r.Inner.StepEnd(scenario, result, index, total)
	}
}

func (r *StateReporter) SuiteEnd(results []*ScenarioResult, status SuiteStatus, duration time.Duration) {
	if err := r.save(); err != nil {
		util.Logger.Warnf("save run state: %v", err)
	}
	if r.Inner != nil {
		r.Inner.SuiteEnd(results, status, duration)
	}
}
