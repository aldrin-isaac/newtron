package newtrun

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// deviceSelector Tests
// ============================================================================

func TestDeviceSelector_Resolve_All(t *testing.T) {
	ds := deviceSelector{All: true}
	all := []string{"spine1", "leaf1", "leaf2"}
	got := ds.Resolve(all)

	// Should return sorted copy
	if len(got) != 3 {
		t.Fatalf("Resolve(all) returned %d devices, want 3", len(got))
	}
	if got[0] != "leaf1" || got[1] != "leaf2" || got[2] != "spine1" {
		t.Errorf("Resolve(all) = %v, want [leaf1 leaf2 spine1]", got)
	}
}

func TestDeviceSelector_Resolve_Specific(t *testing.T) {
	ds := deviceSelector{Devices: []string{"leaf1", "leaf2"}}
	got := ds.Resolve(nil)

	if len(got) != 2 || got[0] != "leaf1" || got[1] != "leaf2" {
		t.Errorf("Resolve(specific) = %v, want [leaf1 leaf2]", got)
	}
}

func TestDeviceSelector_UnmarshalYAML_All(t *testing.T) {
	// Simulate YAML unmarshaling by calling the method directly
	ds := &deviceSelector{}
	err := ds.UnmarshalYAML(func(v any) error {
		if sp, ok := v.(*string); ok {
			*sp = "all"
			return nil
		}
		return nil
	})
	if err != nil {
		t.Fatalf("UnmarshalYAML error: %v", err)
	}
	if !ds.All {
		t.Error("expected All=true after unmarshaling \"all\"")
	}
}

// ============================================================================
// Parser Tests
// ============================================================================

func TestParseScenario(t *testing.T) {
	dir := t.TempDir()
	yamlContent := `
name: test-basic
description: Basic test scenario
topology: 2node-ngdp
platform: sonic-vs
steps:
  - name: provision-all
    action: provision
    devices: all
  - name: wait-convergence
    action: wait
    duration: 10s
  - name: check-bgp
    action: newtron
    devices: all
    url: /node/{{device}}/bgp/check
    expect:
      jq: 'length > 0 and all(.[]; .status == "pass")'
`
	path := filepath.Join(dir, "test-basic.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := ParseScenario(path)
	if err != nil {
		t.Fatalf("ParseScenario error: %v", err)
	}

	if s.Name != "test-basic" {
		t.Errorf("Name = %q, want %q", s.Name, "test-basic")
	}
	if s.Topology != "2node-ngdp" {
		t.Errorf("Topology = %q, want %q", s.Topology, "2node-ngdp")
	}
	if s.Platform != "sonic-vs" {
		t.Errorf("Platform = %q, want %q", s.Platform, "sonic-vs")
	}
	if len(s.Steps) != 3 {
		t.Fatalf("len(Steps) = %d, want 3", len(s.Steps))
	}

	// Check step 0: provision
	if s.Steps[0].Action != ActionProvision {
		t.Errorf("Steps[0].Action = %q, want %q", s.Steps[0].Action, ActionProvision)
	}
	if !s.Steps[0].Devices.All {
		t.Error("Steps[0].Devices.All should be true")
	}

	// Check step 1: wait with duration
	if s.Steps[1].Action != ActionWait {
		t.Errorf("Steps[1].Action = %q, want %q", s.Steps[1].Action, ActionWait)
	}
	if s.Steps[1].Duration != 10*time.Second {
		t.Errorf("Steps[1].Duration = %v, want 10s", s.Steps[1].Duration)
	}

	// Check step 2: newtron
	if s.Steps[2].Action != ActionNewtron {
		t.Errorf("Steps[2].Action = %q, want %q", s.Steps[2].Action, ActionNewtron)
	}
}

func TestParseAllScenarios(t *testing.T) {
	dir := t.TempDir()

	// Write two scenario files
	for _, name := range []string{"a.yaml", "b.yaml"} {
		content := `
name: ` + name + `
description: test
topology: 2node-ngdp
platform: sonic-vs
steps:
  - name: wait
    action: wait
    duration: 1s
`
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Non-yaml file should be ignored
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("ignore me"), 0o644); err != nil {
		t.Fatal(err)
	}

	scenarios, err := ParseAllScenarios(dir)
	if err != nil {
		t.Fatalf("ParseAllScenarios error: %v", err)
	}
	if len(scenarios) != 2 {
		t.Errorf("len(scenarios) = %d, want 2", len(scenarios))
	}
}

// ============================================================================
// Validator Tests
// ============================================================================

func TestValidateStepFields_ProvisionRequiresDevices(t *testing.T) {
	step := Step{Name: "bad", Action: ActionProvision} // no devices
	if err := validateStepFields("test", 1, &step); err == nil {
		t.Error("expected error for provision step without devices")
	}
}

// ============================================================================
// parsePingSuccessRate Tests
// ============================================================================

func TestParsePingSuccessRate(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   float64
	}{
		{
			name:   "no loss",
			output: "5 packets transmitted, 5 received, 0% packet loss",
			want:   1.0,
		},
		{
			name:   "100% loss",
			output: "5 packets transmitted, 0 received, 100% packet loss",
			want:   0.0,
		},
		{
			name:   "20% loss",
			output: "5 packets transmitted, 4 received, 20% packet loss",
			want:   0.8,
		},
		{
			name:   "no match returns 0",
			output: "some random output",
			want:   0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePingSuccessRate(tt.output)
			if got != tt.want {
				t.Errorf("parsePingSuccessRate() = %f, want %f", got, tt.want)
			}
		})
	}
}

// ============================================================================
// computeOverallStatus Tests
// ============================================================================

func TestComputeOverallStatus(t *testing.T) {
	tests := []struct {
		name  string
		steps []StepResult
		want  StepStatus
	}{
		{
			name:  "all passed",
			steps: []StepResult{{Status: StepStatusPassed}, {Status: StepStatusPassed}},
			want:  StepStatusPassed,
		},
		{
			name:  "one failed",
			steps: []StepResult{{Status: StepStatusPassed}, {Status: StepStatusFailed}},
			want:  StepStatusFailed,
		},
		{
			name:  "one error",
			steps: []StepResult{{Status: StepStatusPassed}, {Status: StepStatusError}},
			want:  StepStatusError,
		},
		{
			name:  "error and failed prefers failed",
			steps: []StepResult{{Status: StepStatusError}, {Status: StepStatusFailed}},
			want:  StepStatusFailed,
		},
		{
			name:  "empty steps is passed",
			steps: []StepResult{},
			want:  StepStatusPassed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeOverallStatus(tt.steps)
			if got != tt.want {
				t.Errorf("computeOverallStatus() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ============================================================================
// topologicalSort Tests
// ============================================================================

func TestTopologicalSort_Linear(t *testing.T) {
	scenarios := []*Scenario{
		{Name: "c", Requires: []string{"b"}},
		{Name: "b", Requires: []string{"a"}},
		{Name: "a"},
	}

	sorted, err := topologicalSort(scenarios)
	if err != nil {
		t.Fatalf("topologicalSort error: %v", err)
	}

	if len(sorted) != 3 {
		t.Fatalf("expected 3 scenarios, got %d", len(sorted))
	}

	indexOf := make(map[string]int)
	for i, s := range sorted {
		indexOf[s.Name] = i
	}
	if indexOf["a"] > indexOf["b"] {
		t.Errorf("a (index %d) should come before b (index %d)", indexOf["a"], indexOf["b"])
	}
	if indexOf["b"] > indexOf["c"] {
		t.Errorf("b (index %d) should come before c (index %d)", indexOf["b"], indexOf["c"])
	}
}

func TestTopologicalSort_Diamond(t *testing.T) {
	scenarios := []*Scenario{
		{Name: "d", Requires: []string{"b", "c"}},
		{Name: "b", Requires: []string{"a"}},
		{Name: "c", Requires: []string{"a"}},
		{Name: "a"},
	}

	sorted, err := topologicalSort(scenarios)
	if err != nil {
		t.Fatalf("topologicalSort error: %v", err)
	}

	indexOf := make(map[string]int)
	for i, s := range sorted {
		indexOf[s.Name] = i
	}

	if indexOf["a"] > indexOf["b"] || indexOf["a"] > indexOf["c"] {
		t.Error("a should come before b and c")
	}
	if indexOf["b"] > indexOf["d"] || indexOf["c"] > indexOf["d"] {
		t.Error("b and c should come before d")
	}
}

func TestTopologicalSort_NoDeps(t *testing.T) {
	scenarios := []*Scenario{
		{Name: "x"},
		{Name: "y"},
		{Name: "z"},
	}

	sorted, err := topologicalSort(scenarios)
	if err != nil {
		t.Fatalf("topologicalSort error: %v", err)
	}
	if len(sorted) != 3 {
		t.Fatalf("expected 3 scenarios, got %d", len(sorted))
	}
}

// ============================================================================
// Cycle Detection Tests
// ============================================================================

func TestTopologicalSort_Cycle(t *testing.T) {
	scenarios := []*Scenario{
		{Name: "a", Requires: []string{"b"}},
		{Name: "b", Requires: []string{"a"}},
	}

	_, err := topologicalSort(scenarios)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "dependency cycle") {
		t.Errorf("expected 'dependency cycle' in error, got: %s", err)
	}
}

func TestTopologicalSort_SelfCycle(t *testing.T) {
	scenarios := []*Scenario{
		{Name: "a", Requires: []string{"a"}},
	}

	_, err := topologicalSort(scenarios)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
}

func TestTopologicalSort_ThreeNodeCycle(t *testing.T) {
	scenarios := []*Scenario{
		{Name: "a", Requires: []string{"c"}},
		{Name: "b", Requires: []string{"a"}},
		{Name: "c", Requires: []string{"b"}},
	}

	_, err := topologicalSort(scenarios)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
}

// ============================================================================
// validateDependencyGraph Tests
// ============================================================================

func TestValidateDependencyGraph_Valid(t *testing.T) {
	scenarios := []*Scenario{
		{Name: "a"},
		{Name: "b", Requires: []string{"a"}},
		{Name: "c", Requires: []string{"a", "b"}},
	}

	if _, err := ValidateDependencyGraph(scenarios); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateDependencyGraph_UnknownRequires(t *testing.T) {
	scenarios := []*Scenario{
		{Name: "a"},
		{Name: "b", Requires: []string{"nonexistent"}},
	}

	_, err := ValidateDependencyGraph(scenarios)
	if err == nil {
		t.Fatal("expected error for unknown requires")
	}
	if !strings.Contains(err.Error(), "unknown scenario") {
		t.Errorf("expected 'unknown scenario' in error, got: %s", err)
	}
}

func TestValidateDependencyGraph_DuplicateName(t *testing.T) {
	scenarios := []*Scenario{
		{Name: "a"},
		{Name: "a"},
	}

	_, err := ValidateDependencyGraph(scenarios)
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected 'duplicate' in error, got: %s", err)
	}
}

func TestValidateDependencyGraph_SelfRequires(t *testing.T) {
	scenarios := []*Scenario{
		{Name: "a", Requires: []string{"a"}},
	}

	_, err := ValidateDependencyGraph(scenarios)
	if err == nil {
		t.Fatal("expected error for self-requires")
	}
	if !strings.Contains(err.Error(), "requires itself") {
		t.Errorf("expected 'requires itself' in error, got: %s", err)
	}
}

// ============================================================================
// Dependency Graph from YAML Tests
// ============================================================================

func TestParseAndSortScenariosWithRequires(t *testing.T) {
	dir := t.TempDir()

	writeScenario(t, dir, "01-a.yaml", `
name: a
description: first
topology: 2node-ngdp
platform: sonic-vpp
steps:
  - name: wait
    action: wait
    duration: 1s
`)
	writeScenario(t, dir, "02-b.yaml", `
name: b
description: second
topology: 2node-ngdp
platform: sonic-vpp
requires: [a]
steps:
  - name: wait
    action: wait
    duration: 1s
`)

	scenarios, err := ParseAllScenarios(dir)
	if err != nil {
		t.Fatalf("ParseAllScenarios error: %v", err)
	}
	sorted, err := ValidateDependencyGraph(scenarios)
	if err != nil {
		t.Fatalf("ValidateDependencyGraph error: %v", err)
	}
	if len(sorted) != 2 {
		t.Fatalf("expected 2 scenarios, got %d", len(sorted))
	}
	if sorted[0].Name != "a" {
		t.Errorf("first scenario should be 'a', got %q", sorted[0].Name)
	}
	if sorted[1].Name != "b" {
		t.Errorf("second scenario should be 'b', got %q", sorted[1].Name)
	}
}

func TestParseScenariosWithCycleError(t *testing.T) {
	dir := t.TempDir()

	writeScenario(t, dir, "a.yaml", `
name: a
description: first
topology: 2node-ngdp
platform: sonic-vpp
requires: [b]
steps:
  - name: wait
    action: wait
    duration: 1s
`)
	writeScenario(t, dir, "b.yaml", `
name: b
description: second
topology: 2node-ngdp
platform: sonic-vpp
requires: [a]
steps:
  - name: wait
    action: wait
    duration: 1s
`)

	scenarios, err := ParseAllScenarios(dir)
	if err != nil {
		t.Fatalf("ParseAllScenarios error: %v", err)
	}
	if _, err := ValidateDependencyGraph(scenarios); err == nil {
		t.Fatal("expected cycle error")
	}
}

// ============================================================================
// Target Chain Tests
// ============================================================================

func TestComputeTargetChain_Linear(t *testing.T) {
	scenarios := []*Scenario{
		{Name: "a"},
		{Name: "b", Requires: []string{"a"}},
		{Name: "c", Requires: []string{"b"}},
		{Name: "d", Requires: []string{"c"}},
	}
	chain, err := ComputeTargetChain(scenarios, "c")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	names := scenarioNames(chain)
	if len(names) != 3 {
		t.Fatalf("expected 3 scenarios, got %d: %v", len(names), names)
	}
	// d should NOT be included
	for _, n := range names {
		if n == "d" {
			t.Error("d should not be in the chain")
		}
	}
}

func TestComputeTargetChain_Diamond(t *testing.T) {
	// a → b, a → c, b+c → d
	scenarios := []*Scenario{
		{Name: "a"},
		{Name: "b", Requires: []string{"a"}},
		{Name: "c", Requires: []string{"a"}},
		{Name: "d", Requires: []string{"b", "c"}},
	}
	chain, err := ComputeTargetChain(scenarios, "d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chain) != 4 {
		t.Fatalf("expected 4 scenarios, got %d", len(chain))
	}
}

func TestComputeTargetChain_RootTarget(t *testing.T) {
	scenarios := []*Scenario{
		{Name: "a"},
		{Name: "b", Requires: []string{"a"}},
	}
	chain, err := ComputeTargetChain(scenarios, "a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chain) != 1 || chain[0].Name != "a" {
		t.Fatalf("expected just [a], got %v", scenarioNames(chain))
	}
}

func TestComputeTargetChain_UnknownTarget(t *testing.T) {
	scenarios := []*Scenario{{Name: "a"}}
	_, err := ComputeTargetChain(scenarios, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown target")
	}
}

func TestComputeTargetChain_IgnoresAfter(t *testing.T) {
	// "b" has After: ["a"] (soft dep) — should NOT be included when targeting "b"
	scenarios := []*Scenario{
		{Name: "a"},
		{Name: "b", After: []string{"a"}},
	}
	chain, err := ComputeTargetChain(scenarios, "b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chain) != 1 || chain[0].Name != "b" {
		t.Fatalf("expected just [b], got %v", scenarioNames(chain))
	}
}

func scenarioNames(scenarios []*Scenario) []string {
	names := make([]string, len(scenarios))
	for i, s := range scenarios {
		names[i] = s.Name
	}
	return names
}

// ============================================================================
// Skip Propagation Tests
// ============================================================================

func TestStatusVerb(t *testing.T) {
	tests := []struct {
		status StepStatus
		want   string
	}{
		{StepStatusFailed, "failed"},
		{StepStatusError, "errored"},
		{StepStatusSkipped, "was skipped"},
		{StepStatusPassed, "PASS"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			got := statusVerb(tt.status)
			if got != tt.want {
				t.Errorf("statusVerb(%s) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestCheckRequires(t *testing.T) {
	status := map[string]StepStatus{
		"a": StepStatusPassed,
		"b": StepStatusFailed,
		"c": StepStatusSkipped,
	}

	tests := []struct {
		name     string
		scenario *Scenario
		wantSkip bool
	}{
		{"no requires", &Scenario{Name: "x"}, false},
		{"all passed", &Scenario{Name: "x", Requires: []string{"a"}}, false},
		{"one failed", &Scenario{Name: "x", Requires: []string{"a", "b"}}, true},
		{"one skipped", &Scenario{Name: "x", Requires: []string{"c"}}, true},
		{"not yet run", &Scenario{Name: "x", Requires: []string{"unknown"}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := checkRequires(tt.scenario, status)
			if (reason != "") != tt.wantSkip {
				t.Errorf("checkRequires() = %q, wantSkip=%v", reason, tt.wantSkip)
			}
		})
	}
}

func TestHasRequires(t *testing.T) {
	if HasRequires([]*Scenario{{Name: "a"}, {Name: "b"}}) {
		t.Error("expected false for no requires")
	}
	if !HasRequires([]*Scenario{{Name: "a"}, {Name: "b", Requires: []string{"a"}}}) {
		t.Error("expected true when one has requires")
	}
}

func TestSharedTopology(t *testing.T) {
	if got := sharedTopology([]*Scenario{
		{Name: "a", Topology: "2node-ngdp"},
		{Name: "b", Topology: "2node-ngdp"},
	}, ""); got != "2node-ngdp" {
		t.Errorf("sharedTopology() = %q, want '2node-ngdp'", got)
	}

	if got := sharedTopology([]*Scenario{
		{Name: "a", Topology: "2node-ngdp"},
		{Name: "b", Topology: "4node-ngdp"},
	}, ""); got != "" {
		t.Errorf("sharedTopology() = %q, want ''", got)
	}

	if got := sharedTopology([]*Scenario{
		{Name: "a", Topology: "2node-ngdp"},
		{Name: "b", Topology: "4node-ngdp"},
	}, "override"); got != "override" {
		t.Errorf("sharedTopology() = %q, want 'override'", got)
	}
}

// ============================================================================
// Report with SkipReason Tests
// ============================================================================

func TestWriteJUnit_SkipReason(t *testing.T) {
	results := []*ScenarioResult{
		{
			Name:       "skipped-test",
			Topology:   "2node-ngdp",
			Platform:   "sonic-vpp",
			Status:     StepStatusSkipped,
			SkipReason: "requires 'boot-ssh' which failed",
		},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "results.xml")
	gen := &ReportGenerator{Results: results}
	if err := gen.WriteJUnit(path); err != nil {
		t.Fatalf("WriteJUnit error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading JUnit: %v", err)
	}
	xmlStr := string(data)
	if !strings.Contains(xmlStr, "skipped") {
		t.Errorf("expected <skipped> element in JUnit XML, got: %s", xmlStr)
	}
	if !strings.Contains(xmlStr, "requires") {
		t.Errorf("expected skip reason in JUnit XML, got: %s", xmlStr)
	}
}

// ============================================================================
// Scenario Requires Parsing Test
// ============================================================================

func TestParseScenario_Requires(t *testing.T) {
	dir := t.TempDir()
	writeScenario(t, dir, "test.yaml", `
name: test-requires
description: test
topology: 2node-ngdp
platform: sonic-vpp
requires: [boot-ssh, provision]
steps:
  - name: wait
    action: wait
    duration: 1s
`)

	s, err := ParseScenario(filepath.Join(dir, "test.yaml"))
	if err != nil {
		t.Fatalf("ParseScenario error: %v", err)
	}
	if len(s.Requires) != 2 {
		t.Fatalf("expected 2 requires, got %d", len(s.Requires))
	}
	if s.Requires[0] != "boot-ssh" {
		t.Errorf("Requires[0] = %q, want %q", s.Requires[0], "boot-ssh")
	}
	if s.Requires[1] != "provision" {
		t.Errorf("Requires[1] = %q, want %q", s.Requires[1], "provision")
	}
}

// ============================================================================
// Repeat Tests
// ============================================================================

func TestParseScenario_Repeat(t *testing.T) {
	dir := t.TempDir()
	writeScenario(t, dir, "churn.yaml", `
name: service-churn
description: stress test
topology: 2node-ngdp
platform: sonic-vpp
repeat: 20
steps:
  - name: apply-service
    action: newtron
    devices: [leaf1]
    url: /node/{{device}}/interface/Ethernet0/service
    method: POST
    params:
      service: transit
  - name: remove-service
    action: newtron
    devices: [leaf1]
    url: /node/{{device}}/interface/Ethernet0/service
    method: DELETE
`)

	s, err := ParseScenario(filepath.Join(dir, "churn.yaml"))
	if err != nil {
		t.Fatalf("ParseScenario error: %v", err)
	}
	if s.Repeat != 20 {
		t.Errorf("Repeat = %d, want 20", s.Repeat)
	}
}

func TestParseScenario_RepeatDefault(t *testing.T) {
	dir := t.TempDir()
	writeScenario(t, dir, "basic.yaml", `
name: basic
description: no repeat
topology: 2node-ngdp
platform: sonic-vpp
steps:
  - name: wait
    action: wait
    duration: 1s
`)

	s, err := ParseScenario(filepath.Join(dir, "basic.yaml"))
	if err != nil {
		t.Fatalf("ParseScenario error: %v", err)
	}
	if s.Repeat != 0 {
		t.Errorf("Repeat = %d, want 0 (default)", s.Repeat)
	}
}

func TestWriteJUnit_RepeatIterationInName(t *testing.T) {
	results := []*ScenarioResult{
		{
			Name:     "churn",
			Topology: "2node-ngdp",
			Platform: "sonic-vpp",
			Status:   StepStatusPassed,
			Repeat:   3,
			Steps: []StepResult{
				{Name: "apply", Action: ActionNewtron, Status: StepStatusPassed, Iteration: 1},
				{Name: "remove", Action: ActionNewtron, Status: StepStatusPassed, Iteration: 1},
				{Name: "apply", Action: ActionNewtron, Status: StepStatusPassed, Iteration: 2},
				{Name: "remove", Action: ActionNewtron, Status: StepStatusPassed, Iteration: 2},
				{Name: "apply", Action: ActionNewtron, Status: StepStatusPassed, Iteration: 3},
				{Name: "remove", Action: ActionNewtron, Status: StepStatusPassed, Iteration: 3},
			},
		},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "results.xml")
	gen := &ReportGenerator{Results: results}
	if err := gen.WriteJUnit(path); err != nil {
		t.Fatalf("WriteJUnit error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading JUnit: %v", err)
	}
	xmlStr := string(data)
	if !strings.Contains(xmlStr, "[iter 1] apply") {
		t.Errorf("expected '[iter 1] apply' in JUnit XML, got: %s", xmlStr)
	}
	if !strings.Contains(xmlStr, "[iter 3] remove") {
		t.Errorf("expected '[iter 3] remove' in JUnit XML, got: %s", xmlStr)
	}
}

// ============================================================================
// requireParam Tests
// ============================================================================

func TestRequireParam(t *testing.T) {
	if err := requireParam("test", map[string]any{"key": "val"}, "key"); err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
	if err := requireParam("test", map[string]any{"key": "val"}, "missing"); err == nil {
		t.Error("expected error for missing key")
	}
	if err := requireParam("test", nil, "key"); err == nil {
		t.Error("expected error for nil params")
	}
}

// ============================================================================
// Executor Registration Completeness Test (TE-01)
// ============================================================================

func TestAllActionsHaveExecutors(t *testing.T) {
	// Every StepAction in validActions should have an executor
	for action := range validActions {
		if _, ok := executors[action]; !ok {
			t.Errorf("action %q is in validActions but has no executor", action)
		}
	}
	// Every executor should have a corresponding validAction entry
	for action := range executors {
		if _, ok := validActions[action]; !ok {
			t.Errorf("executor %q is registered but not in validActions", action)
		}
	}
}

func TestExecutorCountMatchesActionConstants(t *testing.T) {
	allActions := []StepAction{
		ActionProvision, ActionWait, ActionVerifyProvisioning,
		ActionHostExec, ActionNewtron,
	}

	if len(executors) != len(allActions) {
		t.Errorf("executors map has %d entries, but there are %d StepAction constants",
			len(executors), len(allActions))
	}

	for _, action := range allActions {
		if _, ok := executors[action]; !ok {
			t.Errorf("StepAction constant %q has no executor", action)
		}
	}
}

// ============================================================================
// executeStep Dispatch Tests (TE-01)
// ============================================================================

func TestExecuteStep_UnknownAction(t *testing.T) {
	r := &Runner{
		Composites: make(map[string]string),
	}
	step := &Step{Action: "nonexistent-action", Name: "test-unknown"}
	output := r.executeStep(context.Background(), step, 0, 1, RunOptions{})

	if output.Result.Status != StepStatusError {
		t.Errorf("expected StepStatusError for unknown action, got %v", output.Result.Status)
	}
	if !strings.Contains(output.Result.Message, "unknown action") {
		t.Errorf("expected 'unknown action' in message, got %q", output.Result.Message)
	}
	if output.Result.Name != "test-unknown" {
		t.Errorf("expected step name 'test-unknown', got %q", output.Result.Name)
	}
	if output.Result.Action != "nonexistent-action" {
		t.Errorf("expected action 'nonexistent-action', got %q", output.Result.Action)
	}
}

func TestExecuteStep_SetsNameAndAction(t *testing.T) {
	// The wait executor is the simplest — it just sleeps.
	// With 0 duration it returns immediately.
	r := &Runner{
		Composites: make(map[string]string),
	}
	step := &Step{
		Action:   ActionWait,
		Name:     "quick-wait",
		Duration: 0,
	}
	output := r.executeStep(context.Background(), step, 0, 1, RunOptions{})

	if output.Result.Name != "quick-wait" {
		t.Errorf("Name = %q, want %q", output.Result.Name, "quick-wait")
	}
	if output.Result.Action != ActionWait {
		t.Errorf("Action = %q, want %q", output.Result.Action, ActionWait)
	}
	if output.Result.Status != StepStatusPassed {
		t.Errorf("Status = %v, want %v", output.Result.Status, StepStatusPassed)
	}
	if output.Result.Duration == 0 {
		t.Error("expected Duration to be set (non-zero)")
	}
}

// ============================================================================
// resolveScenarioPath Tests (TE-01)
// ============================================================================

func TestResolveScenarioPath_ExactMatch(t *testing.T) {
	dir := t.TempDir()
	writeScenario(t, dir, "boot-ssh.yaml", `
name: boot-ssh
description: test
topology: 2node-ngdp
platform: sonic-vpp
steps:
  - name: wait
    action: wait
    duration: 1s
`)
	got, err := resolveScenarioPath(dir, "boot-ssh")
	if err != nil {
		t.Fatalf("resolveScenarioPath error: %v", err)
	}
	if filepath.Base(got) != "boot-ssh.yaml" {
		t.Errorf("expected boot-ssh.yaml, got %s", filepath.Base(got))
	}
}

func TestResolveScenarioPath_NumberedPrefix(t *testing.T) {
	dir := t.TempDir()
	writeScenario(t, dir, "03-bgp-converge.yaml", `
name: bgp-converge
description: test
topology: 2node-ngdp
platform: sonic-vpp
steps:
  - name: wait
    action: wait
    duration: 1s
`)
	got, err := resolveScenarioPath(dir, "bgp-converge")
	if err != nil {
		t.Fatalf("resolveScenarioPath error: %v", err)
	}
	if filepath.Base(got) != "03-bgp-converge.yaml" {
		t.Errorf("expected 03-bgp-converge.yaml, got %s", filepath.Base(got))
	}
}

func TestResolveScenarioPath_NameFieldScan(t *testing.T) {
	dir := t.TempDir()
	// File name doesn't match the scenario name at all
	writeScenario(t, dir, "scenario-alpha.yaml", `
name: my-custom-name
description: test
topology: 2node-ngdp
platform: sonic-vpp
steps:
  - name: wait
    action: wait
    duration: 1s
`)
	got, err := resolveScenarioPath(dir, "my-custom-name")
	if err != nil {
		t.Fatalf("resolveScenarioPath error: %v", err)
	}
	if filepath.Base(got) != "scenario-alpha.yaml" {
		t.Errorf("expected scenario-alpha.yaml, got %s", filepath.Base(got))
	}
}

func TestResolveScenarioPath_NotFound(t *testing.T) {
	dir := t.TempDir()
	writeScenario(t, dir, "other.yaml", `
name: other
description: test
topology: 2node-ngdp
platform: sonic-vpp
steps:
  - name: wait
    action: wait
    duration: 1s
`)
	_, err := resolveScenarioPath(dir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent scenario")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %s", err)
	}
}

func TestResolveScenarioPath_PrefersExactOverNumbered(t *testing.T) {
	dir := t.TempDir()
	// Both exact match and numbered prefix exist
	writeScenario(t, dir, "vlan.yaml", `
name: vlan
description: exact match
topology: 2node-ngdp
platform: sonic-vpp
steps:
  - name: wait
    action: wait
    duration: 1s
`)
	writeScenario(t, dir, "06-vlan.yaml", `
name: vlan-numbered
description: numbered prefix
topology: 2node-ngdp
platform: sonic-vpp
steps:
  - name: wait
    action: wait
    duration: 1s
`)
	got, err := resolveScenarioPath(dir, "vlan")
	if err != nil {
		t.Fatalf("resolveScenarioPath error: %v", err)
	}
	// Exact match should win
	if filepath.Base(got) != "vlan.yaml" {
		t.Errorf("expected vlan.yaml (exact), got %s", filepath.Base(got))
	}
}

// ============================================================================
// pollUntil Tests (TE-01)
// ============================================================================

func TestPollUntil_ImmediateSuccess(t *testing.T) {
	calls := 0
	err := pollUntil(context.Background(), 5*time.Second, 10*time.Millisecond, func() (bool, error) {
		calls++
		return true, nil
	})
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestPollUntil_Timeout(t *testing.T) {
	err := pollUntil(context.Background(), 50*time.Millisecond, 10*time.Millisecond, func() (bool, error) {
		return false, nil
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected 'timeout' in error, got: %s", err)
	}
}

func TestPollUntil_ErrorPropagation(t *testing.T) {
	sentinel := fmt.Errorf("redis connection refused")
	err := pollUntil(context.Background(), 5*time.Second, 10*time.Millisecond, func() (bool, error) {
		return false, sentinel
	})
	if err != sentinel {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

func TestPollUntil_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := pollUntil(ctx, 5*time.Second, 10*time.Millisecond, func() (bool, error) {
		return false, nil
	})
	if err == nil {
		t.Fatal("expected error after context cancellation, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected 'timeout' in error, got: %s", err)
	}
}

func TestPollUntil_SuccessAfterRetries(t *testing.T) {
	calls := 0
	err := pollUntil(context.Background(), 5*time.Second, 10*time.Millisecond, func() (bool, error) {
		calls++
		return calls >= 3, nil
	})
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

// ============================================================================
// Wait Executor Tests (TE-01)
// ============================================================================

func TestWaitExecutor_ZeroDuration(t *testing.T) {
	executor := executors[ActionWait]
	step := &Step{Action: ActionWait, Name: "zero-wait", Duration: 0}
	output := executor.Execute(context.Background(), &Runner{}, step)

	if output.Result.Status != StepStatusPassed {
		t.Errorf("expected StepStatusPassed, got %v", output.Result.Status)
	}
}

func TestWaitExecutor_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	executor := executors[ActionWait]
	step := &Step{Action: ActionWait, Name: "cancelled-wait", Duration: 10 * time.Second}
	output := executor.Execute(ctx, &Runner{}, step)

	if output.Result.Status != StepStatusError {
		t.Errorf("expected StepStatusError for cancelled context, got %v", output.Result.Status)
	}
	if !strings.Contains(output.Result.Message, "interrupted") {
		t.Errorf("expected 'interrupted' in message, got %q", output.Result.Message)
	}
}

// ============================================================================
// deviceSelector Additional Edge Cases (TE-01)
// ============================================================================

func TestDeviceSelector_Resolve_AllEmpty(t *testing.T) {
	ds := deviceSelector{All: true}
	got := ds.Resolve(nil)
	if len(got) != 0 {
		t.Errorf("Resolve(all, nil) returned %d devices, want 0", len(got))
	}
}

func TestDeviceSelector_Resolve_AllPreservesOriginal(t *testing.T) {
	ds := deviceSelector{All: true}
	original := []string{"spine1", "leaf1", "leaf2"}
	_ = ds.Resolve(original)
	// Original should not be modified (Resolve copies before sort)
	if original[0] != "spine1" || original[1] != "leaf1" || original[2] != "leaf2" {
		t.Error("Resolve(all) modified the original slice")
	}
}

// ============================================================================
// iterateScenarios Tests (TE-02)
// ============================================================================

func TestIterateScenarios_Normal(t *testing.T) {
	r := &Runner{}
	scenarios := []*Scenario{
		{Name: "sc1", Topology: "2node-ngdp", Platform: "sonic-vpp"},
		{Name: "sc2", Topology: "2node-ngdp", Platform: "sonic-vpp"},
	}

	results, err := r.iterateScenarios(context.Background(), scenarios, RunOptions{}, "", func(_ context.Context, sc *Scenario, topology, platform string) (*ScenarioResult, error) {
		return &ScenarioResult{
			Name:     sc.Name,
			Topology: topology,
			Platform: platform,
			Status:   StepStatusPassed,
		}, nil
	})
	if err != nil {
		t.Fatalf("iterateScenarios error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for i, res := range results {
		if res.Status != StepStatusPassed {
			t.Errorf("result[%d].Status = %v, want %v", i, res.Status, StepStatusPassed)
		}
	}
	if results[0].Name != "sc1" || results[1].Name != "sc2" {
		t.Errorf("names = [%q, %q], want [sc1, sc2]", results[0].Name, results[1].Name)
	}
	if results[0].Topology != "2node-ngdp" || results[0].Platform != "sonic-vpp" {
		t.Errorf("result[0] topology=%q platform=%q, want 2node-ngdp/sonic-vpp",
			results[0].Topology, results[0].Platform)
	}
}

func TestIterateScenarios_TopologyOverride(t *testing.T) {
	r := &Runner{}
	scenarios := []*Scenario{
		{Name: "sc1", Topology: "2node-ngdp", Platform: "sonic-vpp"},
	}

	var gotTopology string
	_, err := r.iterateScenarios(context.Background(), scenarios, RunOptions{Topology: "4node-ngdp"}, "", func(_ context.Context, sc *Scenario, topology, platform string) (*ScenarioResult, error) {
		gotTopology = topology
		return &ScenarioResult{Name: sc.Name, Topology: topology, Platform: platform, Status: StepStatusPassed}, nil
	})
	if err != nil {
		t.Fatalf("iterateScenarios error: %v", err)
	}
	if gotTopology != "4node-ngdp" {
		t.Errorf("callback received topology=%q, want %q", gotTopology, "4node-ngdp")
	}
}

func TestIterateScenarios_Resume(t *testing.T) {
	r := &Runner{}
	scenarios := []*Scenario{
		{Name: "sc1", Topology: "2node-ngdp", Platform: "sonic-vpp"},
		{Name: "sc2", Topology: "2node-ngdp", Platform: "sonic-vpp"},
	}

	callbackCalls := 0
	results, err := r.iterateScenarios(context.Background(), scenarios, RunOptions{
		Resume:    true,
		Completed: map[string]StepStatus{"sc1": StepStatusPassed},
	}, "", func(_ context.Context, sc *Scenario, topology, platform string) (*ScenarioResult, error) {
		callbackCalls++
		return &ScenarioResult{Name: sc.Name, Topology: topology, Platform: platform, Status: StepStatusPassed}, nil
	})
	if err != nil {
		t.Fatalf("iterateScenarios error: %v", err)
	}
	if callbackCalls != 1 {
		t.Errorf("callback called %d times, want 1 (sc1 should be skipped)", callbackCalls)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Status != StepStatusSkipped {
		t.Errorf("results[0].Status = %v, want StepStatusSkipped", results[0].Status)
	}
	if !strings.Contains(results[0].SkipReason, "already passed (resumed)") {
		t.Errorf("results[0].SkipReason = %q, want 'already passed (resumed)'",
			results[0].SkipReason)
	}
	if results[1].Status != StepStatusPassed {
		t.Errorf("results[1].Status = %v, want StepStatusPassed", results[1].Status)
	}
}

func TestIterateScenarios_RequiresSkip(t *testing.T) {
	r := &Runner{}
	scenarios := []*Scenario{
		{Name: "sc1", Topology: "2node-ngdp", Platform: "sonic-vpp"},
		{Name: "sc2", Topology: "2node-ngdp", Platform: "sonic-vpp", Requires: []string{"sc1"}},
	}

	results, err := r.iterateScenarios(context.Background(), scenarios, RunOptions{}, "", func(_ context.Context, sc *Scenario, topology, platform string) (*ScenarioResult, error) {
		return &ScenarioResult{Name: sc.Name, Topology: topology, Platform: platform, Status: StepStatusFailed}, nil
	})
	if err != nil {
		t.Fatalf("iterateScenarios error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[1].Status != StepStatusSkipped {
		t.Errorf("results[1].Status = %v, want StepStatusSkipped", results[1].Status)
	}
	if !strings.Contains(results[1].SkipReason, "requires") {
		t.Errorf("results[1].SkipReason = %q, want to contain 'requires'",
			results[1].SkipReason)
	}
}

func TestIterateScenarios_CallbackError(t *testing.T) {
	r := &Runner{}
	scenarios := []*Scenario{
		{Name: "sc1", Topology: "2node-ngdp", Platform: "sonic-vpp"},
		{Name: "sc2", Topology: "2node-ngdp", Platform: "sonic-vpp"},
	}

	sentinel := fmt.Errorf("deploy failed")
	results, err := r.iterateScenarios(context.Background(), scenarios, RunOptions{}, "", func(_ context.Context, sc *Scenario, _, _ string) (*ScenarioResult, error) {
		if sc.Name == "sc1" {
			return nil, sentinel
		}
		return &ScenarioResult{Name: sc.Name, Status: StepStatusPassed}, nil
	})
	if err != sentinel {
		t.Errorf("expected sentinel error, got %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results (error on first scenario), got %d", len(results))
	}
	_ = results // ensure partial results are returned
}

// ============================================================================
// runScenarioSteps Tests (TE-02)
// ============================================================================

func TestRunScenarioSteps_SingleStep(t *testing.T) {
	r := &Runner{
		Composites: make(map[string]string),
	}
	scenario := &Scenario{
		Name: "test",
		Steps: []Step{
			{Name: "quick-wait", Action: ActionWait, Duration: 0},
		},
	}
	result := &ScenarioResult{Name: "test"}
	r.runScenarioSteps(context.Background(), scenario, RunOptions{}, result)

	if len(result.Steps) != 1 {
		t.Fatalf("expected 1 step result, got %d", len(result.Steps))
	}
	if result.Status != StepStatusPassed {
		t.Errorf("Status = %v, want StepStatusPassed", result.Status)
	}
	if result.Steps[0].Name != "quick-wait" {
		t.Errorf("Steps[0].Name = %q, want %q", result.Steps[0].Name, "quick-wait")
	}
}

func TestRunScenarioSteps_FailFast(t *testing.T) {
	r := &Runner{
		Composites: make(map[string]string),
	}
	scenario := &Scenario{
		Name: "test",
		Steps: []Step{
			{Name: "bad-step", Action: "nonexistent-action"},
			{Name: "should-not-run", Action: ActionWait, Duration: 0},
		},
	}
	result := &ScenarioResult{Name: "test"}
	r.runScenarioSteps(context.Background(), scenario, RunOptions{}, result)

	if len(result.Steps) != 1 {
		t.Fatalf("expected 1 step result (fail-fast), got %d", len(result.Steps))
	}
	if result.Status != StepStatusError {
		t.Errorf("Status = %v, want StepStatusError", result.Status)
	}
}

func TestRunScenarioSteps_Repeat(t *testing.T) {
	r := &Runner{
		Composites: make(map[string]string),
	}
	scenario := &Scenario{
		Name:   "test",
		Repeat: 3,
		Steps: []Step{
			{Name: "quick-wait", Action: ActionWait, Duration: 0},
		},
	}
	result := &ScenarioResult{Name: "test"}
	r.runScenarioSteps(context.Background(), scenario, RunOptions{}, result)

	if len(result.Steps) != 3 {
		t.Fatalf("expected 3 step results (3 iterations), got %d", len(result.Steps))
	}
	if result.Status != StepStatusPassed {
		t.Errorf("Status = %v, want StepStatusPassed", result.Status)
	}
	for i, sr := range result.Steps {
		if sr.Iteration != i+1 {
			t.Errorf("Steps[%d].Iteration = %d, want %d", i, sr.Iteration, i+1)
		}
	}
	if result.Repeat != 3 {
		t.Errorf("Repeat = %d, want 3", result.Repeat)
	}
}

func TestRunScenarioSteps_RepeatFailsOnIteration(t *testing.T) {
	r := &Runner{
		Composites: make(map[string]string),
	}
	scenario := &Scenario{
		Name:   "test",
		Repeat: 3,
		Steps: []Step{
			{Name: "bad-step", Action: "nonexistent-action"},
		},
	}
	result := &ScenarioResult{Name: "test"}
	r.runScenarioSteps(context.Background(), scenario, RunOptions{}, result)

	if len(result.Steps) != 1 {
		t.Fatalf("expected 1 step result (fail on iter 1), got %d", len(result.Steps))
	}
	if result.FailedIteration != 1 {
		t.Errorf("FailedIteration = %d, want 1", result.FailedIteration)
	}
	if result.Status != StepStatusError {
		t.Errorf("Status = %v, want StepStatusError", result.Status)
	}
}

func TestRunScenarioSteps_InitComposites(t *testing.T) {
	r := &Runner{} // nil Composites
	scenario := &Scenario{
		Name: "test",
		Steps: []Step{
			{Name: "quick-wait", Action: ActionWait, Duration: 0},
		},
	}
	result := &ScenarioResult{Name: "test"}
	r.runScenarioSteps(context.Background(), scenario, RunOptions{}, result)

	if r.Composites == nil {
		t.Error("expected Composites to be initialized, got nil")
	}
}

// ============================================================================
// Run Validation Tests (TE-02)
// ============================================================================

func TestRun_NoFlags(t *testing.T) {
	r := NewRunner(t.TempDir(), t.TempDir())
	_, err := r.Run(RunOptions{})
	if err == nil {
		t.Fatal("expected error for no flags, got nil")
	}
	if !strings.Contains(err.Error(), "--scenario") || !strings.Contains(err.Error(), "--all") {
		t.Errorf("expected error about --scenario/--all, got: %s", err)
	}
}

func TestRun_TopologyNotFound(t *testing.T) {
	r := NewRunner(t.TempDir(), t.TempDir())
	_, err := r.Run(RunOptions{Scenario: "x", Topology: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent topology, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %s", err)
	}
}

// ============================================================================
// Helpers
// ============================================================================

func writeScenario(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
