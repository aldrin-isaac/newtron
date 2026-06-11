// Package newtrun implements an E2E test orchestrator for newtron and SONiC.
// It parses YAML scenario files, deploys VM topologies via newtlab, provisions
// devices via newtron, and runs multi-step verification sequences.
package newtrun

import (
	"fmt"
	"sort"
	"time"
)

// Scenario is a parsed test scenario YAML file. Targets and
// parameters live on the parent Suite, not here — a scenario is the
// step list and its dependency metadata; the suite owns iteration and
// run-time bindings (see suite.go).
//
// Scenarios come in two shapes, distinguished per-scenario by whether
// any step uses {{target.X}} or {{param.X}} tokens. Both shapes
// coexist within one suite:
//
//   - Embedded-target scenarios use step-level devices: selectors and
//     {{device}} substitution; the runner dispatches per device.
//     Typical use: testing — the scenario covers a known matrix.
//
//   - Parameterized scenarios reference the suite-level targets:/
//     parameters: catalog via {{target.X}} / {{param.X}}; the runner
//     iterates the cross-product of declared targets. Step-level
//     devices: and {{device}} are forbidden in these scenarios.
//     Typical use: production rollout.
//
// ScenarioIsParameterized (suite.go) makes the per-scenario decision.
type Scenario struct {
	Name             string   `yaml:"name"`
	Description      string   `yaml:"description"`
	Topology         string   `yaml:"topology,omitempty"`        // suite-level field; LoadSuite rejects scenarios that set it
	Platform         string   `yaml:"platform,omitempty"`        // suite-level field; LoadSuite rejects scenarios that set it
	Requires         []string `yaml:"requires,omitempty"`
	After            []string `yaml:"after,omitempty"`              // Run after these scenarios (ordering only, no pass/fail gate)
	RequiresFeatures []string `yaml:"requires_features,omitempty"` // Platform features required (e.g., ["acl", "macvpn"])
	Repeat           int      `yaml:"repeat,omitempty"`

	Steps []Step `yaml:"steps"`
}

// Step is a single action within a scenario.
// Fields are action-specific — the parser validates that only relevant
// fields are set for each action type.
type Step struct {
	Name    string         `yaml:"name"`
	Action  StepAction     `yaml:"action"`
	Devices deviceSelector `yaml:"devices,omitempty"`

	// wait
	Duration time.Duration `yaml:"duration,omitempty"`

	// host-exec, newtron (shared)
	Command string         `yaml:"command,omitempty"`
	Params  map[string]any `yaml:"params,omitempty"`

	// newtron (generic server action)
	Method string      `yaml:"method,omitempty"` // HTTP method: GET, POST, DELETE
	URL    string      `yaml:"url,omitempty"`    // URL template (e.g., /node/{{device}}/vlan)
	Poll   *PollBlock  `yaml:"poll,omitempty"`   // polling configuration
	Batch  []BatchCall `yaml:"batch,omitempty"`  // sequential batch of calls
	// Headers attaches per-step HTTP headers to outbound newtron
	// requests — typically the caller identity for an auth-enforced
	// newtron-server (X-Newtron-Caller: alice) so a scenario can
	// verify enforcement under different identities. Headers apply
	// uniformly across a step including any batched sub-calls — one
	// step = one identity. Empty/nil preserves pre-Headers behavior.
	Headers map[string]string `yaml:"headers,omitempty"`

	// run-suite (composition: invoke another suite as a step)
	Suite      string              `yaml:"suite,omitempty"`      // suite name to invoke (resolved under SuitesBase)
	Parameters map[string]any      `yaml:"parameters,omitempty"` // parameter overrides for the called suite
	Targets    map[string][]string `yaml:"targets,omitempty"`    // target-dimension overrides for the called suite

	// All actions
	Expect        *ExpectBlock `yaml:"expect,omitempty"`
	ExpectFailure bool         `yaml:"expect_failure,omitempty"`
}

// StepAction identifies the type of step to execute.
type StepAction string

const (
	ActionProvision          StepAction = "topology-reconcile"
	ActionWait               StepAction = "wait"
	ActionVerifyProvisioning StepAction = "verify-topology"
	ActionHostExec           StepAction = "host-exec"
	ActionNewtron            StepAction = "newtron"
	ActionNewtronCLI         StepAction = "newtron-cli"
	ActionRunSuite           StepAction = "run-suite"
)

// validActions is the set of all recognized step actions, derived from the
// executors map in steps.go at init time. This avoids manual synchronization
// between the two maps.
var validActions map[StepAction]bool

func init() {
	validActions = make(map[StepAction]bool, len(executors))
	for action := range executors {
		validActions[action] = true
	}
}

// deviceSelector handles the two YAML forms for the "devices" field:
//
//	devices: all           → All: true
//	devices: [leaf1, leaf2] → Devices: ["leaf1", "leaf2"]
type deviceSelector struct {
	All     bool
	Devices []string
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (ds *deviceSelector) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err == nil {
		if s == "all" {
			ds.All = true
			return nil
		}
		return fmt.Errorf("invalid device selector string: %q (expected \"all\")", s)
	}
	return unmarshal(&ds.Devices)
}

// Resolve returns the list of device names to target.
// If All is true, returns allDevices sorted for deterministic ordering.
func (ds *deviceSelector) Resolve(allDevices []string) []string {
	if ds.All {
		sorted := make([]string, len(allDevices))
		copy(sorted, allDevices)
		sort.Strings(sorted)
		return sorted
	}
	return ds.Devices
}

// PollBlock configures polling for the generic newtron action.
type PollBlock struct {
	Timeout  time.Duration `yaml:"timeout"`
	Interval time.Duration `yaml:"interval"`
}

// BatchCall is a single HTTP call within a batch sequence.
type BatchCall struct {
	Method string         `yaml:"method"`
	URL    string         `yaml:"url"`
	Params map[string]any `yaml:"params,omitempty"`
}

// ExpectBlock is a union of all action-specific expectation fields.
type ExpectBlock struct {
	// Polling (used internally by newtronExecutor.executePoll to bridge poll: config to pollForDevices)
	Timeout      time.Duration `yaml:"timeout,omitempty"`
	PollInterval time.Duration `yaml:"poll_interval,omitempty"`

	// host-exec
	SuccessRate *float64 `yaml:"success_rate,omitempty"`
	Contains    string   `yaml:"contains,omitempty"`

	// newtron (generic server action) — jq expression evaluated against response body
	JQ string `yaml:"jq,omitempty"`
}
