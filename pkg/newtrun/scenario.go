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
	Network          string   `yaml:"network,omitempty"`          // suite-level field; LoadSuite rejects scenarios that set it
	Platform         string   `yaml:"platform,omitempty"`          // suite-level field; LoadSuite rejects scenarios that set it
	Requires         []string `yaml:"requires,omitempty"`
	After            []string `yaml:"after,omitempty"`             // Run after these scenarios (ordering only, no pass/fail gate)
	RequiresFeatures []string `yaml:"requires_features,omitempty"` // Platform features required (e.g., ["acl", "macvpn"])
	RequiresParams   []string `yaml:"requires_params,omitempty"`   // Suite-level parameters that must be set to a non-empty/non-zero value at run time; otherwise the scenario is skipped with a descriptive reason
	Repeat           int      `yaml:"repeat,omitempty"`

	// Cleanup steps run once per scenario, AFTER all iterations and repeats,
	// regardless of pass/fail — fabric-state teardown must not depend on the
	// scenario's outcome (a failed scenario that strands device state
	// cascades into unrelated downstream failures; the §48 evpn continuity
	// check stranding an interface IP into the portchannel scenario is the
	// motivating incident). Semantics:
	//   - best-effort: every cleanup step runs even if an earlier one fails
	//     (no fail-fast — partial teardown is worse than reported failures)
	//   - results are recorded like main steps; a cleanup failure fails an
	//     otherwise-passing scenario (a dirty fabric is a real failure)
	//   - no {{target.X}} references (validated at parse time) — cleanup is
	//     not iterated per binding
	Cleanup []Step `yaml:"cleanup,omitempty"`

	// As names the cached-session user whose Bearer the runner
	// attaches to every outbound newtron call this scenario makes
	// (auth-design.md §L2c "Identity forwarding through engines").
	// One scenario, one identity — authorization-testing scenarios
	// that need verified per-identity flows (mallory denied; alice
	// allowed) author one scenario per identity and connect them
	// via requires:. The alternative — per-step impersonation —
	// was rejected by design ("per scenario re-login not per step
	// 'as' masquerading"): a scenario that interleaves identities
	// reads as a single workflow under one operator, which is
	// architecturally false in an auth-enforced deployment where
	// each request carries its own verified caller.
	//
	// The named user must have a cached session at the time the
	// suite was started — the CLI populates UserSessions with every
	// cached session from ~/.newtron/sessions/ (over-supplying is
	// harmless), and a scenario that names a user absent from the
	// map fails fast at run start with "no session cached for user
	// X; run `newtron auth login --user X` first".
	//
	// Empty (the common case) lets the runner forward the operator's
	// own Bearer (extracted from the inbound /newtrun/v1/runs
	// request) on every outbound newtron call, or no credential at
	// all when the run was triggered without one.
	As string `yaml:"as,omitempty"`

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
	// requests. Under PR D the canonical "different identity" path
	// is splitting scenarios with `as:`; Headers is reserved for
	// non-identity per-step overrides (e.g., X-Newtron-Op-Tag for
	// audit-correlation, custom content negotiation). Headers
	// apply uniformly across a step including any batched
	// sub-calls — one step = one header set. Empty/nil preserves
	// pre-Headers behavior.
	Headers map[string]string `yaml:"headers,omitempty"`

	// Capture extracts values from the response body of a successful
	// newtron HTTP call and binds them to scenario-scoped variable
	// names. Each map value is a JQ expression run against the
	// response JSON; the result is stored under the map key and
	// referenced by later steps via {{captured.NAME}}. The captured
	// map is scenario-scoped (per parameterized iteration when applicable),
	// so cross-scenario carry is intentionally not supported — the
	// dependency graph in requires:/after: has parallel branches and
	// shared mutable state across them would be ambiguous. Capture
	// applies only to single-call newtron steps; batch and poll
	// modes reject it at parse time because their result shape isn't
	// a single response. Empty/nil preserves pre-Capture behavior.
	Capture map[string]string `yaml:"capture,omitempty"`

	// run-suite (composition: invoke another suite as a step)
	Suite      string              `yaml:"suite,omitempty"`      // suite name to invoke (resolved across the runner's NetworksBase)
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
