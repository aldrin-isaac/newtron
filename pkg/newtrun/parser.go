package newtrun

import (
	"fmt"
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

	var s Scenario
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing scenario %s: %w", path, err)
	}

	applyDefaults(&s)

	for i, step := range s.Steps {
		if err := validateStepFields(s.Name, i, &step); err != nil {
			return nil, fmt.Errorf("validating %s: %w", path, err)
		}
	}

	return &s, nil
}

// ParseAllScenarios reads all .yaml files in dir and returns parsed scenarios.
func ParseAllScenarios(dir string) ([]*Scenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading scenarios dir %s: %w", dir, err)
	}

	var scenarios []*Scenario
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		s, err := ParseScenario(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		scenarios = append(scenarios, s)
	}
	return scenarios, nil
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
	ActionNewtron: {custom: func(prefix string, step *Step) error {
		if step.URL == "" && len(step.Batch) == 0 {
			return fmt.Errorf("%s: newtron requires url or batch", prefix)
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
func topologicalSort(scenarios []*Scenario) ([]*Scenario, error) {
	byName := make(map[string]*Scenario, len(scenarios))
	inDegree := make(map[string]int, len(scenarios))
	dependents := make(map[string][]string) // name -> scenarios that depend on it

	for _, s := range scenarios {
		byName[s.Name] = s
		inDegree[s.Name] = len(s.Requires) + len(s.After)
		for _, req := range s.Requires {
			dependents[req] = append(dependents[req], s.Name)
		}
		for _, after := range s.After {
			dependents[after] = append(dependents[after], s.Name)
		}
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

// applyDefaults sets default values for steps.
func applyDefaults(s *Scenario) {
	// No defaults needed for the remaining 5 actions.
}
