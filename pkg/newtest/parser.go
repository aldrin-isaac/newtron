package newtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// ValidateScenario checks that a scenario is well-formed.
func ValidateScenario(s *Scenario, topologiesDir string) error {
	if s.Name == "" {
		return fmt.Errorf("scenario name is required")
	}
	if s.Topology == "" {
		return fmt.Errorf("scenario %s: topology is required", s.Name)
	}
	if s.Platform == "" {
		return fmt.Errorf("scenario %s: platform is required", s.Name)
	}

	// Check topology directory exists
	topoDir := filepath.Join(topologiesDir, s.Topology)
	if info, err := os.Stat(topoDir); err != nil || !info.IsDir() {
		return fmt.Errorf("scenario %s: topology directory %s not found", s.Name, topoDir)
	}

	// Validate each step
	for i, step := range s.Steps {
		if step.Name == "" {
			return fmt.Errorf("scenario %s step %d: name is required", s.Name, i+1)
		}
		if !validActions[step.Action] {
			return fmt.Errorf("scenario %s step %d (%s): unknown action %q", s.Name, i+1, step.Name, step.Action)
		}
		if err := validateStepFields(s.Name, i+1, &step); err != nil {
			return err
		}
	}

	return nil
}

// validateStepFields checks required fields per action type.
func validateStepFields(scenario string, index int, step *Step) error {
	prefix := fmt.Sprintf("scenario %s step %d (%s)", scenario, index, step.Name)

	switch step.Action {
	case ActionProvision:
		if !step.Devices.All && len(step.Devices.Devices) == 0 {
			return fmt.Errorf("%s: devices is required", prefix)
		}
	case ActionWait:
		if step.Duration == 0 {
			return fmt.Errorf("%s: duration is required", prefix)
		}
	case ActionVerifyProvisioning:
		if !step.Devices.All && len(step.Devices.Devices) == 0 {
			return fmt.Errorf("%s: devices is required", prefix)
		}
	case ActionVerifyConfigDB:
		if !step.Devices.All && len(step.Devices.Devices) == 0 {
			return fmt.Errorf("%s: devices is required", prefix)
		}
		if step.Table == "" {
			return fmt.Errorf("%s: table is required", prefix)
		}
		if step.Expect == nil {
			return fmt.Errorf("%s: expect is required", prefix)
		}
		if step.Expect.MinEntries == nil && step.Expect.Exists == nil && len(step.Expect.Fields) == 0 {
			return fmt.Errorf("%s: expect must have min_entries, exists, or fields", prefix)
		}
	case ActionVerifyStateDB:
		if !step.Devices.All && len(step.Devices.Devices) == 0 {
			return fmt.Errorf("%s: devices is required", prefix)
		}
		if step.Table == "" {
			return fmt.Errorf("%s: table is required", prefix)
		}
		if step.Key == "" {
			return fmt.Errorf("%s: key is required", prefix)
		}
		if step.Expect == nil || len(step.Expect.Fields) == 0 {
			return fmt.Errorf("%s: expect.fields is required", prefix)
		}
	case ActionVerifyBGP:
		if !step.Devices.All && len(step.Devices.Devices) == 0 {
			return fmt.Errorf("%s: devices is required", prefix)
		}
	case ActionVerifyHealth:
		if !step.Devices.All && len(step.Devices.Devices) == 0 {
			return fmt.Errorf("%s: devices is required", prefix)
		}
	case ActionVerifyRoute:
		devices := step.Devices.Resolve(nil)
		if !step.Devices.All && len(devices) != 1 {
			return fmt.Errorf("%s: verify-route requires exactly one device", prefix)
		}
		if step.Prefix == "" {
			return fmt.Errorf("%s: prefix is required", prefix)
		}
		if step.VRF == "" {
			return fmt.Errorf("%s: vrf is required", prefix)
		}
	case ActionVerifyPing:
		devices := step.Devices.Resolve(nil)
		if !step.Devices.All && len(devices) != 1 {
			return fmt.Errorf("%s: verify-ping requires exactly one device", prefix)
		}
		if step.Target == "" {
			return fmt.Errorf("%s: target is required", prefix)
		}
	case ActionApplyService:
		if !step.Devices.All && len(step.Devices.Devices) == 0 {
			return fmt.Errorf("%s: devices is required", prefix)
		}
		if step.Interface == "" {
			return fmt.Errorf("%s: interface is required", prefix)
		}
		if step.Service == "" {
			return fmt.Errorf("%s: service is required", prefix)
		}
	case ActionRemoveService:
		if !step.Devices.All && len(step.Devices.Devices) == 0 {
			return fmt.Errorf("%s: devices is required", prefix)
		}
		if step.Interface == "" {
			return fmt.Errorf("%s: interface is required", prefix)
		}
	case ActionApplyBaseline:
		if !step.Devices.All && len(step.Devices.Devices) == 0 {
			return fmt.Errorf("%s: devices is required", prefix)
		}
		if step.Configlet == "" {
			return fmt.Errorf("%s: configlet is required", prefix)
		}
	case ActionSSHCommand:
		if !step.Devices.All && len(step.Devices.Devices) == 0 {
			return fmt.Errorf("%s: devices is required", prefix)
		}
		if step.Command == "" {
			return fmt.Errorf("%s: command is required", prefix)
		}
	}

	return nil
}

// validateDependencyGraph checks that all Requires references exist and there
// are no cycles.
func validateDependencyGraph(scenarios []*Scenario) error {
	names := make(map[string]bool, len(scenarios))
	for _, s := range scenarios {
		if names[s.Name] {
			return fmt.Errorf("duplicate scenario name: %s", s.Name)
		}
		names[s.Name] = true
	}

	for _, s := range scenarios {
		for _, req := range s.Requires {
			if !names[req] {
				return fmt.Errorf("scenario %s requires unknown scenario %q", s.Name, req)
			}
			if req == s.Name {
				return fmt.Errorf("scenario %s requires itself", s.Name)
			}
		}
	}

	// Cycle detection via topological sort
	_, err := topologicalSort(scenarios)
	return err
}

// topologicalSort returns scenarios in dependency order using Kahn's algorithm.
func topologicalSort(scenarios []*Scenario) ([]*Scenario, error) {
	byName := make(map[string]*Scenario, len(scenarios))
	inDegree := make(map[string]int, len(scenarios))
	dependents := make(map[string][]string) // name -> scenarios that depend on it

	for _, s := range scenarios {
		byName[s.Name] = s
		inDegree[s.Name] = len(s.Requires)
		for _, req := range s.Requires {
			dependents[req] = append(dependents[req], s.Name)
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
	for i := range s.Steps {
		step := &s.Steps[i]

		// Default ping count
		if step.Action == ActionVerifyPing && step.Count == 0 {
			step.Count = 5
		}

		if step.Expect == nil {
			continue
		}

		// Default timeouts per action
		switch step.Action {
		case ActionVerifyStateDB:
			if step.Expect.Timeout == 0 {
				step.Expect.Timeout = 120 * time.Second
			}
			if step.Expect.PollInterval == 0 {
				step.Expect.PollInterval = 5 * time.Second
			}
		case ActionVerifyBGP:
			if step.Expect.Timeout == 0 {
				step.Expect.Timeout = 120 * time.Second
			}
			if step.Expect.PollInterval == 0 {
				step.Expect.PollInterval = 5 * time.Second
			}
			if step.Expect.State == "" {
				step.Expect.State = "Established"
			}
		case ActionVerifyRoute:
			if step.Expect.Timeout == 0 {
				step.Expect.Timeout = 60 * time.Second
			}
			if step.Expect.PollInterval == 0 {
				step.Expect.PollInterval = 5 * time.Second
			}
			if step.Expect.Source == "" {
				step.Expect.Source = "app_db"
			}
		case ActionVerifyPing:
			if step.Expect.Timeout == 0 {
				step.Expect.Timeout = 30 * time.Second
			}
			if step.Expect.SuccessRate == nil {
				rate := 1.0
				step.Expect.SuccessRate = &rate
			}
		}
	}
}
