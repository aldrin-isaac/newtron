package newtrun

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseScenario reads a YAML scenario file and returns a validated Scenario.
func ParseScenario(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading scenario %s: %w", path, err)
	}
	s, err := ParseScenarioBytes(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// ParseScenarioBytes parses a YAML scenario from a byte buffer and
// returns a validated Scenario. Used by the inline compose-and-run
// HTTP endpoint and the PUT scenario-authoring handler — both expect
// exactly one scenario per call.
//
// Uses yaml.NewDecoder rather than yaml.Unmarshal so a multi-document
// payload surfaces as an explicit error ("expected one scenario, got
// a stream of N") instead of silently truncating to the first
// document. Multi-document streams belong on the suite-directory
// write path (loadScenarioFiles); single-scenario endpoints reject
// them so an operator who pastes a split-per-identity file body
// into create-scenario sees the mistake immediately
// (ai-instructions §13 same concept = same name).
func ParseScenarioBytes(data []byte) (*Scenario, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var s Scenario
	if err := dec.Decode(&s); err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("parsing scenario: empty input")
		}
		return nil, fmt.Errorf("parsing scenario: %w", err)
	}
	// Detect a second document — same wire shape as the suite loader
	// reads, but explicitly rejected here.
	var extra Scenario
	if err := dec.Decode(&extra); err == nil {
		// More than one doc; count any further to make the error honest.
		n := 2
		for {
			if err := dec.Decode(&extra); err != nil {
				break
			}
			n++
		}
		return nil, fmt.Errorf("parsing scenario: expected one scenario, got a stream of %d", n)
	} else if err != io.EOF {
		return nil, fmt.Errorf("parsing scenario: %w", err)
	}
	applyDefaults(&s)
	for i, step := range s.Steps {
		if err := validateStepFields(s.Name, i, &step); err != nil {
			return nil, fmt.Errorf("validating scenario: %w", err)
		}
	}
	if err := validateCleanupSteps(&s); err != nil {
		return nil, fmt.Errorf("validating scenario: %w", err)
	}
	return &s, nil
}

// loadScenarioFiles is the underlying walk used by LoadSuite (run path). It
// returns each parsed scenario paired with its source file path so
// suite-level error messages (e.g. "scenario X sets topology — that
// belongs in suite.yaml") can name the offending file. Per §28
// (file-level feature cohesion), scenario loading lives here in
// parser.go; LoadSuite layers suite-level rejection on top.
//
// Each .yaml file may contain one or more YAML documents separated by
// --- lines. Multi-document files let a set of split-per-identity
// scenarios live in the same file while preserving alphabetical
// ordering on disk.
func loadScenarioFiles(dir string) ([]*Scenario, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("reading scenarios dir %s: %w", dir, err)
	}
	var (
		scenarios []*Scenario
		paths     []string
	)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") || e.Name() == "suite.yaml" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		ss, err := parseScenarioFile(path)
		if err != nil {
			return nil, nil, err
		}
		for range ss {
			paths = append(paths, path)
		}
		scenarios = append(scenarios, ss...)
	}
	return scenarios, paths, nil
}

// parseScenarioFile reads a scenario file that may contain one or more YAML
// documents (separated by ---). Each document is parsed and validated as an
// independent Scenario. Returns one scenario per document in file order.
func parseScenarioFile(path string) ([]*Scenario, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading scenario %s: %w", path, err)
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	var out []*Scenario
	for {
		var s Scenario
		if err := dec.Decode(&s); err != nil {
			// io.EOF means no more documents.
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		applyDefaults(&s)
		for i, step := range s.Steps {
			if err := validateStepFields(s.Name, i, &step); err != nil {
				return nil, fmt.Errorf("%s: validating scenario: %w", path, err)
			}
		}
		if err := validateCleanupSteps(&s); err != nil {
			return nil, fmt.Errorf("%s: validating scenario: %w", path, err)
		}
		out = append(out, &s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: file contains no scenario documents", path)
	}
	return out, nil
}

// requireParam checks that a required key exists in step.Params.
func requireParam(prefix string, params map[string]any, key string) error {
	if params == nil {
		return fmt.Errorf("%s: params.%s is required", prefix, key)
	}
	if _, ok := params[key]; !ok {
		return fmt.Errorf("%s: params.%s is required", prefix, key)
	}
	return nil
}

// requireDevices checks that the step has a device selector.
func requireDevices(prefix string, step *Step) error {
	if !step.Devices.All && len(step.Devices.Devices) == 0 {
		return fmt.Errorf("%s: devices is required", prefix)
	}
	return nil
}

// stepValidation declares what fields/params each action requires.
type stepValidation struct {
	needsDevices bool     // must have a device selector
	singleDevice bool     // exactly one device required (implies needsDevices)
	fields       []string // required step-level fields: "command", etc.
	params       []string // required params map keys
	custom       func(prefix string, step *Step) error
}

// requireSnapshotName validates the snapshot / verify-snapshot name.
func requireSnapshotName(prefix string, step *Step) error {
	if step.Snapshot == "" {
		return fmt.Errorf("%s: snapshot name is required (snapshot: <name>)", prefix)
	}
	return nil
}

// stepValidations is the declarative validation table for all step actions.
// Actions not listed here have no field requirements.
var stepValidations = map[StepAction]stepValidation{
	ActionWait: {custom: func(prefix string, step *Step) error {
		if step.Duration == 0 {
			return fmt.Errorf("%s: duration is required", prefix)
		}
		return nil
	}},
	ActionProvision:          {needsDevices: true},
	ActionVerifyProvisioning: {needsDevices: true},
	ActionHostExec:           {singleDevice: true, fields: []string{"command"}},
	ActionSnapshot:           {needsDevices: true, custom: requireSnapshotName},
	ActionVerifySnapshot:     {needsDevices: true, custom: requireSnapshotName},
	ActionNewtron: {custom: func(prefix string, step *Step) error {
		if step.URL == "" && len(step.Batch) == 0 {
			return fmt.Errorf("%s: newtron requires url or batch", prefix)
		}
		// Capture extracts values from a single response body. Batch
		// emits multiple responses with no canonical "the response";
		// poll loops until an assertion holds and reports only the
		// passing iteration's status, not the final body. In both
		// cases there is no single response to extract from — reject
		// at parse time so the suite author gets a clear message
		// instead of a "captured nothing" surprise at runtime.
		if len(step.Capture) > 0 {
			if len(step.Batch) > 0 {
				return fmt.Errorf("%s: capture is not supported on batch steps (no single response body)", prefix)
			}
			if step.Poll != nil {
				return fmt.Errorf("%s: capture is not supported on poll steps (no single response body)", prefix)
			}
			// A {{device}}-templated step fans out per device — N responses,
			// no canonical body. With exactly one explicit device there IS a
			// single response, so capture composes (the step runs on the
			// single-call path, not the fan-out path).
			if strings.Contains(step.URL, "{{device}}") && (step.Devices.All || len(step.Devices.Devices) != 1) {
				return fmt.Errorf("%s: capture on a {{device}}-templated step requires exactly one device in devices: — multiple devices produce multiple responses with no single body to extract from", prefix)
			}
			for name, expr := range step.Capture {
				if name == "" {
					return fmt.Errorf("%s: capture has an entry with empty variable name (each entry must have a non-empty name to reference as {{captured.NAME}})", prefix)
				}
				if expr == "" {
					return fmt.Errorf("%s: capture %q has empty JQ expression (each entry must have a non-empty JQ expression, e.g. \".key\")", prefix, name)
				}
			}
		}
		return nil
	}},
	ActionRunSuite: {custom: func(prefix string, step *Step) error {
		if step.Suite == "" {
			return fmt.Errorf("%s: run-suite requires suite", prefix)
		}
		// Reject names that could escape the topologies tree via path traversal.
		// Mirrors pkg/newtrun/api.nameRE — duplicated here so the parser
		// stays self-contained (and doesn't import its api subpackage).
		if strings.ContainsAny(step.Suite, "/\\") || step.Suite == "." || step.Suite == ".." {
			return fmt.Errorf("%s: invalid suite name %q (no path separators)", prefix, step.Suite)
		}
		return nil
	}},
}

// stepFieldGetter maps step-level field names to their accessor functions.
var stepFieldGetter = map[string]func(*Step) string{
	"command": func(s *Step) string { return s.Command },
}

// validateStepFields checks required fields per action type using the
// stepValidations table.
func validateStepFields(scenario string, index int, step *Step) error {
	prefix := fmt.Sprintf("scenario %s step %d (%s)", scenario, index, step.Name)

	// Cross-field guard: suite/parameters/targets are run-suite-only.
	// Catching a typo (e.g. "wait" with targets) here is cheaper than
	// silently dropping the fields and surfacing as a "step ran but
	// nothing happened" mystery at runtime.
	if step.Action != ActionRunSuite {
		if step.Suite != "" {
			return fmt.Errorf("%s: 'suite' is only valid for action run-suite (got action %q)", prefix, step.Action)
		}
		if len(step.Parameters) > 0 {
			return fmt.Errorf("%s: 'parameters' is only valid for action run-suite (got action %q)", prefix, step.Action)
		}
		if len(step.Targets) > 0 {
			return fmt.Errorf("%s: 'targets' is only valid for action run-suite (got action %q)", prefix, step.Action)
		}
	}
	// Response-capture is wired only for the single-call newtron
	// path; other actions don't produce a JSON body the JQ
	// extractor can read.
	if step.Action != ActionNewtron && len(step.Capture) > 0 {
		return fmt.Errorf("%s: 'capture' is only valid for action newtron (got action %q)", prefix, step.Action)
	}

	// Poll needs both knobs — pollUntil has no defaults, and a zero
	// timeout silently degenerates to a single attempt.
	if step.Poll != nil && (step.Poll.Timeout <= 0 || step.Poll.Interval <= 0) {
		return fmt.Errorf("%s: poll requires timeout and interval (both > 0)", prefix)
	}

	v, ok := stepValidations[step.Action]
	if !ok {
		return nil // no validation rules for this action
	}

	// Check device requirements
	if v.singleDevice {
		devices := step.Devices.Resolve(nil)
		if !step.Devices.All && len(devices) != 1 {
			return fmt.Errorf("%s: %s requires exactly one device", prefix, step.Action)
		}
	} else if v.needsDevices {
		if err := requireDevices(prefix, step); err != nil {
			return err
		}
	}

	// Check required step-level fields
	for _, field := range v.fields {
		getter, exists := stepFieldGetter[field]
		if !exists {
			return fmt.Errorf("%s: unknown validation field %q (bug)", prefix, field)
		}
		if getter(step) == "" {
			return fmt.Errorf("%s: %s is required", prefix, field)
		}
	}

	// Check required params
	for _, key := range v.params {
		if err := requireParam(prefix, step.Params, key); err != nil {
			return err
		}
	}

	// Run custom validation if present
	if v.custom != nil {
		if err := v.custom(prefix, step); err != nil {
			return err
		}
	}

	return nil
}

// ValidateDependencyGraph checks that all Requires references exist and there
// are no cycles. On success it returns scenarios in dependency order.
func ValidateDependencyGraph(scenarios []*Scenario) ([]*Scenario, error) {
	names := make(map[string]bool, len(scenarios))
	for _, s := range scenarios {
		if names[s.Name] {
			return nil, fmt.Errorf("duplicate scenario name: %s", s.Name)
		}
		names[s.Name] = true
	}

	for _, s := range scenarios {
		for _, req := range s.Requires {
			if !names[req] {
				return nil, fmt.Errorf("scenario %s requires unknown scenario %q", s.Name, req)
			}
			if req == s.Name {
				return nil, fmt.Errorf("scenario %s requires itself", s.Name)
			}
		}
		for _, after := range s.After {
			if !names[after] {
				return nil, fmt.Errorf("scenario %s has after reference to unknown scenario %q", s.Name, after)
			}
			if after == s.Name {
				return nil, fmt.Errorf("scenario %s has after reference to itself", s.Name)
			}
		}
	}

	return topologicalSort(scenarios)
}

// topologicalSort returns scenarios in dependency order using Kahn's algorithm.
// Only dependencies present in the input set are counted — references to
// scenarios outside the set are ignored (allows subset sorting).
func topologicalSort(scenarios []*Scenario) ([]*Scenario, error) {
	byName := make(map[string]*Scenario, len(scenarios))
	for _, s := range scenarios {
		byName[s.Name] = s
	}

	inDegree := make(map[string]int, len(scenarios))
	dependents := make(map[string][]string) // name -> scenarios that depend on it

	for _, s := range scenarios {
		deg := 0
		for _, req := range s.Requires {
			if _, ok := byName[req]; ok {
				deg++
				dependents[req] = append(dependents[req], s.Name)
			}
		}
		for _, after := range s.After {
			if _, ok := byName[after]; ok {
				deg++
				dependents[after] = append(dependents[after], s.Name)
			}
		}
		inDegree[s.Name] = deg
	}

	// Seed queue with scenarios that have no dependencies
	var queue []string
	for _, s := range scenarios {
		if inDegree[s.Name] == 0 {
			queue = append(queue, s.Name)
		}
	}

	var sorted []*Scenario
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		sorted = append(sorted, byName[name])

		for _, dep := range dependents[name] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if len(sorted) != len(scenarios) {
		var inCycle []string
		for name, deg := range inDegree {
			if deg > 0 {
				inCycle = append(inCycle, name)
			}
		}
		return nil, fmt.Errorf("dependency cycle involving: %s", strings.Join(inCycle, ", "))
	}

	return sorted, nil
}

// ComputeTargetChain returns the minimal set of scenarios needed to reach the
// target, including all transitive requires dependencies, in dependency order.
// Only hard dependencies (Requires) are traversed — soft dependencies (After)
// are not included unless they are also in the requires chain.
func ComputeTargetChain(scenarios []*Scenario, target string) ([]*Scenario, error) {
	byName := make(map[string]*Scenario, len(scenarios))
	for _, s := range scenarios {
		byName[s.Name] = s
	}

	if _, ok := byName[target]; !ok {
		return nil, fmt.Errorf("target scenario %q not found", target)
	}

	// BFS backwards through requires to collect the full chain
	needed := make(map[string]bool)
	queue := []string{target}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if needed[name] {
			continue
		}
		needed[name] = true
		s := byName[name]
		for _, req := range s.Requires {
			if !needed[req] {
				queue = append(queue, req)
			}
		}
	}

	// Filter to only needed scenarios and topologically sort
	var chain []*Scenario
	for _, s := range scenarios {
		if needed[s.Name] {
			chain = append(chain, s)
		}
	}

	return topologicalSort(chain)
}

// applyDefaults sets default values for steps.
func applyDefaults(s *Scenario) {
	// No defaults needed for the remaining 5 actions.
}

// validateCleanupSteps validates a scenario's cleanup: block. Cleanup steps
// use the same per-action validation as main steps, plus one restriction:
// no {{target.X}} references — cleanup runs once per scenario (after all
// target iterations), so there is no binding to expand against.
func validateCleanupSteps(s *Scenario) error {
	for i, step := range s.Cleanup {
		if err := validateStepFields(s.Name+" cleanup", i, &step); err != nil {
			return err
		}
		if stepText, _ := yaml.Marshal(step); strings.Contains(string(stepText), "{{target.") {
			return fmt.Errorf("scenario %q cleanup step %d (%s): cleanup steps cannot reference {{target.X}} — cleanup runs once per scenario, after all target iterations", s.Name, i, step.Name)
		}
	}
	return nil
}
