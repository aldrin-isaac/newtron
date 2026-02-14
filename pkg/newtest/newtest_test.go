package newtest

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/newtron-network/newtron/pkg/device"
	"github.com/newtron-network/newtron/pkg/network"
)

// ============================================================================
// DeviceSelector Tests
// ============================================================================

func TestDeviceSelector_Resolve_All(t *testing.T) {
	ds := DeviceSelector{All: true}
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
	ds := DeviceSelector{Devices: []string{"leaf1", "leaf2"}}
	got := ds.Resolve(nil)

	if len(got) != 2 || got[0] != "leaf1" || got[1] != "leaf2" {
		t.Errorf("Resolve(specific) = %v, want [leaf1 leaf2]", got)
	}
}

func TestDeviceSelector_UnmarshalYAML_All(t *testing.T) {
	// Simulate YAML unmarshaling by calling the method directly
	ds := &DeviceSelector{}
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
topology: 2node
platform: sonic-vs
steps:
  - name: provision-all
    action: provision
    devices: all
  - name: wait-convergence
    action: wait
    duration: 10s
  - name: verify-bgp
    action: verify-bgp
    devices: all
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
	if s.Topology != "2node" {
		t.Errorf("Topology = %q, want %q", s.Topology, "2node")
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

	// Check step 2: verify-bgp with defaults applied
	if s.Steps[2].Action != ActionVerifyBGP {
		t.Errorf("Steps[2].Action = %q, want %q", s.Steps[2].Action, ActionVerifyBGP)
	}
}

func TestParseAllScenarios(t *testing.T) {
	dir := t.TempDir()

	// Write two scenario files
	for _, name := range []string{"a.yaml", "b.yaml"} {
		content := `
name: ` + name + `
description: test
topology: 2node
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

func TestValidateScenario_MissingFields(t *testing.T) {
	tests := []struct {
		name    string
		s       Scenario
		wantErr bool
	}{
		{
			name:    "empty name",
			s:       Scenario{},
			wantErr: true,
		},
		{
			name:    "missing topology",
			s:       Scenario{Name: "test"},
			wantErr: true,
		},
		{
			name:    "missing platform",
			s:       Scenario{Name: "test", Topology: "2node"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateScenario(&tt.s, t.TempDir())
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateScenario() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateScenario_ValidWithTopologyDir(t *testing.T) {
	dir := t.TempDir()
	topoDir := filepath.Join(dir, "2node")
	if err := os.MkdirAll(topoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	s := Scenario{
		Name:     "test",
		Topology: "2node",
		Platform: "sonic-vs",
		Steps: []Step{
			{Name: "wait", Action: ActionWait, Duration: 5 * time.Second},
		},
	}

	if err := ValidateScenario(&s, dir); err != nil {
		t.Errorf("ValidateScenario() unexpected error: %v", err)
	}
}

func TestValidateStepFields_ProvisionRequiresDevices(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "2node"), 0o755); err != nil {
		t.Fatal(err)
	}

	s := Scenario{
		Name:     "test",
		Topology: "2node",
		Platform: "sonic-vs",
		Steps: []Step{
			{Name: "bad", Action: ActionProvision}, // no devices
		},
	}

	if err := ValidateScenario(&s, dir); err == nil {
		t.Error("expected error for provision step without devices")
	}
}

// ============================================================================
// Default Application Tests
// ============================================================================

func TestApplyDefaults_PingCount(t *testing.T) {
	s := Scenario{
		Steps: []Step{
			{Action: ActionVerifyPing},
		},
	}
	applyDefaults(&s)
	if s.Steps[0].Count != 5 {
		t.Errorf("ping count = %d, want 5", s.Steps[0].Count)
	}
}

func TestApplyDefaults_BGPTimeout(t *testing.T) {
	s := Scenario{
		Steps: []Step{
			{Action: ActionVerifyBGP, Expect: &ExpectBlock{}},
		},
	}
	applyDefaults(&s)
	if s.Steps[0].Expect.Timeout != 120*time.Second {
		t.Errorf("BGP timeout = %v, want 120s", s.Steps[0].Expect.Timeout)
	}
	if s.Steps[0].Expect.State != "Established" {
		t.Errorf("BGP state = %q, want %q", s.Steps[0].Expect.State, "Established")
	}
}

func TestApplyDefaults_RouteSource(t *testing.T) {
	s := Scenario{
		Steps: []Step{
			{Action: ActionVerifyRoute, Expect: &ExpectBlock{}},
		},
	}
	applyDefaults(&s)
	if s.Steps[0].Expect.Source != "app_db" {
		t.Errorf("route source = %q, want %q", s.Steps[0].Expect.Source, "app_db")
	}
}

func TestApplyDefaults_PingSuccessRate(t *testing.T) {
	s := Scenario{
		Steps: []Step{
			{Action: ActionVerifyPing, Expect: &ExpectBlock{}},
		},
	}
	applyDefaults(&s)
	if s.Steps[0].Expect.SuccessRate == nil {
		t.Fatal("expected SuccessRate to be set")
	}
	if *s.Steps[0].Expect.SuccessRate != 1.0 {
		t.Errorf("ping success rate = %f, want 1.0", *s.Steps[0].Expect.SuccessRate)
	}
}

// ============================================================================
// matchRoute Tests
// ============================================================================

func TestMatchRoute(t *testing.T) {
	entry := &device.RouteEntry{
		Prefix:   "10.0.0.0/24",
		Protocol: "bgp",
		NextHops: []device.NextHop{
			{IP: "10.0.0.1", Interface: "Ethernet0"},
			{IP: "10.0.0.2", Interface: "Ethernet4"},
		},
	}

	tests := []struct {
		name   string
		expect *ExpectBlock
		want   bool
	}{
		{
			name:   "empty expect matches all",
			expect: &ExpectBlock{},
			want:   true,
		},
		{
			name:   "matching protocol",
			expect: &ExpectBlock{Protocol: "bgp"},
			want:   true,
		},
		{
			name:   "wrong protocol",
			expect: &ExpectBlock{Protocol: "static"},
			want:   false,
		},
		{
			name:   "matching nexthop",
			expect: &ExpectBlock{NextHopIP: "10.0.0.1"},
			want:   true,
		},
		{
			name:   "wrong nexthop",
			expect: &ExpectBlock{NextHopIP: "10.0.0.99"},
			want:   false,
		},
		{
			name:   "both protocol and nexthop match",
			expect: &ExpectBlock{Protocol: "bgp", NextHopIP: "10.0.0.2"},
			want:   true,
		},
		{
			name:   "protocol matches but nexthop doesn't",
			expect: &ExpectBlock{Protocol: "bgp", NextHopIP: "10.0.0.99"},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchRoute(entry, tt.expect)
			if got != tt.want {
				t.Errorf("matchRoute() = %v, want %v", got, tt.want)
			}
		})
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
// matchFields Tests
// ============================================================================

func TestMatchFields(t *testing.T) {
	actual := map[string]string{
		"status":  "up",
		"speed":   "100G",
		"admin":   "up",
	}

	tests := []struct {
		name     string
		expected map[string]string
		want     bool
	}{
		{
			name:     "all match",
			expected: map[string]string{"status": "up", "speed": "100G"},
			want:     true,
		},
		{
			name:     "empty expected matches",
			expected: map[string]string{},
			want:     true,
		},
		{
			name:     "value mismatch",
			expected: map[string]string{"status": "down"},
			want:     false,
		},
		{
			name:     "missing key",
			expected: map[string]string{"nonexistent": "value"},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchFields(actual, tt.expected)
			if got != tt.want {
				t.Errorf("matchFields() = %v, want %v", got, tt.want)
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
// Report Tests
// ============================================================================

func TestStatusSymbol(t *testing.T) {
	tests := []struct {
		status StepStatus
		want   string
	}{
		{StepStatusPassed, "\u2713"},
		{StepStatusFailed, "\u2717"},
		{StepStatusSkipped, "\u2298"},
		{StepStatusError, "!"},
		{StepStatus("unknown"), "?"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			got := statusSymbol(tt.status)
			if got != tt.want {
				t.Errorf("statusSymbol(%s) = %q, want %q", tt.status, got, tt.want)
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

	sorted, err := TopologicalSort(scenarios)
	if err != nil {
		t.Fatalf("TopologicalSort error: %v", err)
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

	sorted, err := TopologicalSort(scenarios)
	if err != nil {
		t.Fatalf("TopologicalSort error: %v", err)
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

	sorted, err := TopologicalSort(scenarios)
	if err != nil {
		t.Fatalf("TopologicalSort error: %v", err)
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

	_, err := TopologicalSort(scenarios)
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

	_, err := TopologicalSort(scenarios)
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

	_, err := TopologicalSort(scenarios)
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
topology: 2node
platform: sonic-vpp
steps:
  - name: wait
    action: wait
    duration: 1s
`)
	writeScenario(t, dir, "02-b.yaml", `
name: b
description: second
topology: 2node
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
topology: 2node
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
topology: 2node
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
		{Name: "a", Topology: "2node"},
		{Name: "b", Topology: "2node"},
	}, ""); got != "2node" {
		t.Errorf("sharedTopology() = %q, want '2node'", got)
	}

	if got := sharedTopology([]*Scenario{
		{Name: "a", Topology: "2node"},
		{Name: "b", Topology: "4node"},
	}, ""); got != "" {
		t.Errorf("sharedTopology() = %q, want ''", got)
	}

	if got := sharedTopology([]*Scenario{
		{Name: "a", Topology: "2node"},
		{Name: "b", Topology: "4node"},
	}, "override"); got != "override" {
		t.Errorf("sharedTopology() = %q, want 'override'", got)
	}
}

// ============================================================================
// Report with SkipReason Tests
// ============================================================================

func TestPrintConsole_SkipReason(t *testing.T) {
	results := []*ScenarioResult{
		{
			Name:       "skipped-test",
			Topology:   "2node",
			Platform:   "sonic-vpp",
			Status:     StepStatusSkipped,
			SkipReason: "requires 'provision' which failed",
		},
	}

	gen := &ReportGenerator{Results: results}
	var buf bytes.Buffer
	gen.PrintConsole(&buf)

	output := buf.String()
	if !strings.Contains(output, "skipped") {
		t.Errorf("expected 'skipped' in output, got: %s", output)
	}
	if !strings.Contains(output, "requires 'provision' which failed") {
		t.Errorf("expected skip reason in output, got: %s", output)
	}
}

func TestWriteJUnit_SkipReason(t *testing.T) {
	results := []*ScenarioResult{
		{
			Name:       "skipped-test",
			Topology:   "2node",
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
topology: 2node
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
topology: 2node
platform: sonic-vpp
repeat: 20
steps:
  - name: apply-service
    action: ssh-command
    devices: [leaf1]
    command: "echo apply"
  - name: remove-service
    action: ssh-command
    devices: [leaf1]
    command: "echo remove"
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
topology: 2node
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

func TestPrintConsole_RepeatPass(t *testing.T) {
	results := []*ScenarioResult{
		{
			Name:     "service-churn",
			Topology: "2node",
			Platform: "sonic-vpp",
			Status:   StepStatusPassed,
			Duration: 5 * time.Minute,
			Repeat:   10,
			Steps: func() []StepResult {
				var steps []StepResult
				for i := 1; i <= 10; i++ {
					steps = append(steps,
						StepResult{Name: "apply", Action: ActionSSHCommand, Status: StepStatusPassed, Iteration: i},
						StepResult{Name: "remove", Action: ActionSSHCommand, Status: StepStatusPassed, Iteration: i},
					)
				}
				return steps
			}(),
		},
	}

	gen := &ReportGenerator{Results: results}
	var buf bytes.Buffer
	gen.PrintConsole(&buf)

	output := buf.String()
	if !strings.Contains(output, "10/10 iterations passed") {
		t.Errorf("expected '10/10 iterations passed' in output, got: %s", output)
	}
}

func TestPrintConsole_RepeatFail(t *testing.T) {
	results := []*ScenarioResult{
		{
			Name:            "service-churn",
			Topology:        "2node",
			Platform:        "sonic-vpp",
			Status:          StepStatusFailed,
			Duration:        2 * time.Minute,
			Repeat:          10,
			FailedIteration: 5,
			Steps: func() []StepResult {
				var steps []StepResult
				for i := 1; i <= 4; i++ {
					steps = append(steps,
						StepResult{Name: "apply", Action: ActionSSHCommand, Status: StepStatusPassed, Iteration: i},
						StepResult{Name: "remove", Action: ActionSSHCommand, Status: StepStatusPassed, Iteration: i},
					)
				}
				// Iteration 5 fails on apply
				steps = append(steps,
					StepResult{Name: "apply", Action: ActionSSHCommand, Status: StepStatusFailed, Iteration: 5, Message: "service binding not found"},
				)
				return steps
			}(),
		},
	}

	gen := &ReportGenerator{Results: results}
	var buf bytes.Buffer
	gen.PrintConsole(&buf)

	output := buf.String()
	if !strings.Contains(output, "failed on iteration 5/10") {
		t.Errorf("expected 'failed on iteration 5/10' in output, got: %s", output)
	}
	if !strings.Contains(output, "service binding not found") {
		t.Errorf("expected failure message in output, got: %s", output)
	}
}

func TestWriteJUnit_RepeatIterationInName(t *testing.T) {
	results := []*ScenarioResult{
		{
			Name:     "churn",
			Topology: "2node",
			Platform: "sonic-vpp",
			Status:   StepStatusPassed,
			Repeat:   3,
			Steps: []StepResult{
				{Name: "apply", Action: ActionSSHCommand, Status: StepStatusPassed, Iteration: 1},
				{Name: "remove", Action: ActionSSHCommand, Status: StepStatusPassed, Iteration: 1},
				{Name: "apply", Action: ActionSSHCommand, Status: StepStatusPassed, Iteration: 2},
				{Name: "remove", Action: ActionSSHCommand, Status: StepStatusPassed, Iteration: 2},
				{Name: "apply", Action: ActionSSHCommand, Status: StepStatusPassed, Iteration: 3},
				{Name: "remove", Action: ActionSSHCommand, Status: StepStatusPassed, Iteration: 3},
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
// Param Helper Tests
// ============================================================================

func TestStrParam(t *testing.T) {
	params := map[string]any{"name": "test", "count": 42, "flag": true}

	if got := strParam(params, "name"); got != "test" {
		t.Errorf("strParam(name) = %q, want %q", got, "test")
	}
	if got := strParam(params, "count"); got != "42" {
		t.Errorf("strParam(count) = %q, want %q", got, "42")
	}
	if got := strParam(params, "missing"); got != "" {
		t.Errorf("strParam(missing) = %q, want %q", got, "")
	}
	if got := strParam(nil, "any"); got != "" {
		t.Errorf("strParam(nil, any) = %q, want %q", got, "")
	}
}

func TestIntParam(t *testing.T) {
	params := map[string]any{
		"int_val":    42,
		"float_val":  float64(100),
		"string_val": "200",
		"bad_string": "abc",
		"bool_val":   true,
	}

	if got := intParam(params, "int_val"); got != 42 {
		t.Errorf("intParam(int_val) = %d, want 42", got)
	}
	if got := intParam(params, "float_val"); got != 100 {
		t.Errorf("intParam(float_val) = %d, want 100", got)
	}
	if got := intParam(params, "string_val"); got != 200 {
		t.Errorf("intParam(string_val) = %d, want 200", got)
	}
	if got := intParam(params, "bad_string"); got != 0 {
		t.Errorf("intParam(bad_string) = %d, want 0", got)
	}
	if got := intParam(params, "bool_val"); got != 0 {
		t.Errorf("intParam(bool_val) = %d, want 0", got)
	}
	if got := intParam(params, "missing"); got != 0 {
		t.Errorf("intParam(missing) = %d, want 0", got)
	}
	if got := intParam(nil, "any"); got != 0 {
		t.Errorf("intParam(nil, any) = %d, want 0", got)
	}
}

func TestBoolParam(t *testing.T) {
	params := map[string]any{
		"bool_true":   true,
		"bool_false":  false,
		"str_true":    "true",
		"str_false":   "false",
		"str_one":     "1",
		"int_val":     42,
	}

	if got := boolParam(params, "bool_true"); !got {
		t.Error("boolParam(bool_true) = false, want true")
	}
	if got := boolParam(params, "bool_false"); got {
		t.Error("boolParam(bool_false) = true, want false")
	}
	if got := boolParam(params, "str_true"); !got {
		t.Error("boolParam(str_true) = false, want true")
	}
	if got := boolParam(params, "str_false"); got {
		t.Error("boolParam(str_false) = true, want false")
	}
	if got := boolParam(params, "str_one"); !got {
		t.Error("boolParam(str_one) = false, want true")
	}
	if got := boolParam(params, "int_val"); got {
		t.Error("boolParam(int_val) = true, want false")
	}
	if got := boolParam(params, "missing"); got {
		t.Error("boolParam(missing) = true, want false")
	}
	if got := boolParam(nil, "any"); got {
		t.Error("boolParam(nil, any) = true, want false")
	}
}

// ============================================================================
// Validation Tests for New Actions
// ============================================================================

func TestValidateStepFields_NewActions(t *testing.T) {
	dir := t.TempDir()
	topoDir := filepath.Join(dir, "2node")
	if err := os.MkdirAll(topoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		step    Step
		wantErr bool
		errMsg  string
	}{
		// set-interface
		{
			name:    "set-interface valid",
			step:    Step{Name: "s", Action: ActionSetInterface, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Interface: "Ethernet0", Params: map[string]any{"property": "mtu", "value": "9000"}},
			wantErr: false,
		},
		{
			name:    "set-interface missing interface",
			step:    Step{Name: "s", Action: ActionSetInterface, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"property": "mtu"}},
			wantErr: true, errMsg: "interface is required",
		},
		{
			name:    "set-interface missing property",
			step:    Step{Name: "s", Action: ActionSetInterface, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Interface: "Ethernet0"},
			wantErr: true, errMsg: "params.property is required",
		},
		// create-vlan
		{
			name:    "create-vlan valid",
			step:    Step{Name: "s", Action: ActionCreateVLAN, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vlan_id": 100}},
			wantErr: false,
		},
		{
			name:    "create-vlan missing vlan_id",
			step:    Step{Name: "s", Action: ActionCreateVLAN, Devices: DeviceSelector{Devices: []string{"leaf1"}}},
			wantErr: true, errMsg: "params.vlan_id is required",
		},
		// add-vlan-member
		{
			name:    "add-vlan-member missing interface",
			step:    Step{Name: "s", Action: ActionAddVLANMember, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vlan_id": 100}},
			wantErr: true, errMsg: "params.interface is required",
		},
		// create-vrf
		{
			name:    "create-vrf valid",
			step:    Step{Name: "s", Action: ActionCreateVRF, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vrf": "Vrf_test"}},
			wantErr: false,
		},
		{
			name:    "create-vrf missing vrf",
			step:    Step{Name: "s", Action: ActionCreateVRF, Devices: DeviceSelector{Devices: []string{"leaf1"}}},
			wantErr: true, errMsg: "params.vrf is required",
		},
		// setup-evpn
		{
			name:    "setup-evpn valid",
			step:    Step{Name: "s", Action: ActionSetupEVPN, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"source_ip": "10.0.0.1"}},
			wantErr: false,
		},
		{
			name:    "setup-evpn missing source_ip",
			step:    Step{Name: "s", Action: ActionSetupEVPN, Devices: DeviceSelector{Devices: []string{"leaf1"}}},
			wantErr: true, errMsg: "params.source_ip is required",
		},
		{
			name:    "setup-evpn missing devices",
			step:    Step{Name: "s", Action: ActionSetupEVPN, Params: map[string]any{"source_ip": "10.0.0.1"}},
			wantErr: true, errMsg: "devices is required",
		},
		// add-vrf-interface
		{
			name:    "add-vrf-interface valid",
			step:    Step{Name: "s", Action: ActionAddVRFInterface, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vrf": "Vrf_test", "interface": "Ethernet0"}},
			wantErr: false,
		},
		{
			name:    "add-vrf-interface missing vrf",
			step:    Step{Name: "s", Action: ActionAddVRFInterface, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"interface": "Ethernet0"}},
			wantErr: true, errMsg: "params.vrf is required",
		},
		{
			name:    "add-vrf-interface missing interface",
			step:    Step{Name: "s", Action: ActionAddVRFInterface, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vrf": "Vrf_test"}},
			wantErr: true, errMsg: "params.interface is required",
		},
		// remove-vrf-interface
		{
			name:    "remove-vrf-interface valid",
			step:    Step{Name: "s", Action: ActionRemoveVRFInterface, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vrf": "Vrf_test", "interface": "Ethernet0"}},
			wantErr: false,
		},
		{
			name:    "remove-vrf-interface missing vrf",
			step:    Step{Name: "s", Action: ActionRemoveVRFInterface, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"interface": "Ethernet0"}},
			wantErr: true, errMsg: "params.vrf is required",
		},
		{
			name:    "remove-vrf-interface missing interface",
			step:    Step{Name: "s", Action: ActionRemoveVRFInterface, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vrf": "Vrf_test"}},
			wantErr: true, errMsg: "params.interface is required",
		},
		// bind-ipvpn
		{
			name:    "bind-ipvpn valid",
			step:    Step{Name: "s", Action: ActionBindIPVPN, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vrf": "Vrf_test", "ipvpn": "customer-a"}},
			wantErr: false,
		},
		{
			name:    "bind-ipvpn missing vrf",
			step:    Step{Name: "s", Action: ActionBindIPVPN, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"ipvpn": "customer-a"}},
			wantErr: true, errMsg: "params.vrf is required",
		},
		{
			name:    "bind-ipvpn missing ipvpn",
			step:    Step{Name: "s", Action: ActionBindIPVPN, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vrf": "Vrf_test"}},
			wantErr: true, errMsg: "params.ipvpn is required",
		},
		// unbind-ipvpn
		{
			name:    "unbind-ipvpn valid",
			step:    Step{Name: "s", Action: ActionUnbindIPVPN, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vrf": "Vrf_test"}},
			wantErr: false,
		},
		{
			name:    "unbind-ipvpn missing vrf",
			step:    Step{Name: "s", Action: ActionUnbindIPVPN, Devices: DeviceSelector{Devices: []string{"leaf1"}}},
			wantErr: true, errMsg: "params.vrf is required",
		},
		// bind-macvpn
		{
			name:    "bind-macvpn valid",
			step:    Step{Name: "s", Action: ActionBindMACVPN, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vlan_id": 100, "macvpn": "office-lan"}},
			wantErr: false,
		},
		{
			name:    "bind-macvpn missing vlan_id",
			step:    Step{Name: "s", Action: ActionBindMACVPN, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"macvpn": "office-lan"}},
			wantErr: true, errMsg: "params.vlan_id is required",
		},
		{
			name:    "bind-macvpn missing macvpn",
			step:    Step{Name: "s", Action: ActionBindMACVPN, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vlan_id": 100}},
			wantErr: true, errMsg: "params.macvpn is required",
		},
		// unbind-macvpn
		{
			name:    "unbind-macvpn valid",
			step:    Step{Name: "s", Action: ActionUnbindMACVPN, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vlan_id": 100}},
			wantErr: false,
		},
		{
			name:    "unbind-macvpn missing vlan_id",
			step:    Step{Name: "s", Action: ActionUnbindMACVPN, Devices: DeviceSelector{Devices: []string{"leaf1"}}},
			wantErr: true, errMsg: "params.vlan_id is required",
		},
		// add-static-route
		{
			name:    "add-static-route valid",
			step:    Step{Name: "s", Action: ActionAddStaticRoute, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vrf": "Vrf_test", "prefix": "10.0.0.0/24", "next_hop": "10.0.0.1"}},
			wantErr: false,
		},
		{
			name:    "add-static-route missing vrf",
			step:    Step{Name: "s", Action: ActionAddStaticRoute, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"prefix": "10.0.0.0/24", "next_hop": "10.0.0.1"}},
			wantErr: true, errMsg: "params.vrf is required",
		},
		{
			name:    "add-static-route missing prefix",
			step:    Step{Name: "s", Action: ActionAddStaticRoute, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vrf": "Vrf_test", "next_hop": "10.0.0.1"}},
			wantErr: true, errMsg: "params.prefix is required",
		},
		{
			name:    "add-static-route missing next_hop",
			step:    Step{Name: "s", Action: ActionAddStaticRoute, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vrf": "Vrf_test", "prefix": "10.0.0.0/24"}},
			wantErr: true, errMsg: "params.next_hop is required",
		},
		// remove-static-route
		{
			name:    "remove-static-route valid",
			step:    Step{Name: "s", Action: ActionRemoveStaticRoute, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vrf": "Vrf_test", "prefix": "10.0.0.0/24"}},
			wantErr: false,
		},
		{
			name:    "remove-static-route missing vrf",
			step:    Step{Name: "s", Action: ActionRemoveStaticRoute, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"prefix": "10.0.0.0/24"}},
			wantErr: true, errMsg: "params.vrf is required",
		},
		{
			name:    "remove-static-route missing prefix",
			step:    Step{Name: "s", Action: ActionRemoveStaticRoute, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vrf": "Vrf_test"}},
			wantErr: true, errMsg: "params.prefix is required",
		},
		// remove-vlan-member
		{
			name:    "remove-vlan-member valid",
			step:    Step{Name: "s", Action: ActionRemoveVLANMember, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vlan_id": 100, "interface": "Ethernet0"}},
			wantErr: false,
		},
		{
			name:    "remove-vlan-member missing vlan_id",
			step:    Step{Name: "s", Action: ActionRemoveVLANMember, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"interface": "Ethernet0"}},
			wantErr: true, errMsg: "params.vlan_id is required",
		},
		{
			name:    "remove-vlan-member missing interface",
			step:    Step{Name: "s", Action: ActionRemoveVLANMember, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vlan_id": 100}},
			wantErr: true, errMsg: "params.interface is required",
		},
		// apply-qos
		{
			name:    "apply-qos valid",
			step:    Step{Name: "s", Action: ActionApplyQoS, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"interface": "Ethernet0", "qos_policy": "8q-datacenter"}},
			wantErr: false,
		},
		{
			name:    "apply-qos missing interface",
			step:    Step{Name: "s", Action: ActionApplyQoS, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"qos_policy": "8q-datacenter"}},
			wantErr: true, errMsg: "params.interface is required",
		},
		{
			name:    "apply-qos missing qos_policy",
			step:    Step{Name: "s", Action: ActionApplyQoS, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"interface": "Ethernet0"}},
			wantErr: true, errMsg: "params.qos_policy is required",
		},
		// remove-qos
		{
			name:    "remove-qos valid",
			step:    Step{Name: "s", Action: ActionRemoveQoS, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"interface": "Ethernet0"}},
			wantErr: false,
		},
		{
			name:    "remove-qos missing interface",
			step:    Step{Name: "s", Action: ActionRemoveQoS, Devices: DeviceSelector{Devices: []string{"leaf1"}}},
			wantErr: true, errMsg: "params.interface is required",
		},
		// configure-svi
		{
			name:    "configure-svi valid",
			step:    Step{Name: "s", Action: ActionConfigureSVI, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"vlan_id": 100}},
			wantErr: false,
		},
		// bgp-add-neighbor
		{
			name:    "bgp-add-neighbor valid",
			step:    Step{Name: "s", Action: ActionBGPAddNeighbor, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"remote_asn": 65000, "neighbor_ip": "10.0.0.1"}},
			wantErr: false,
		},
		{
			name:    "bgp-add-neighbor missing remote_asn",
			step:    Step{Name: "s", Action: ActionBGPAddNeighbor, Devices: DeviceSelector{Devices: []string{"leaf1"}}},
			wantErr: true, errMsg: "params.remote_asn is required",
		},
		// bgp-remove-neighbor
		{
			name:    "bgp-remove-neighbor valid",
			step:    Step{Name: "s", Action: ActionBGPRemoveNeighbor, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Params: map[string]any{"neighbor_ip": "10.0.0.1"}},
			wantErr: false,
		},
		{
			name:    "bgp-remove-neighbor missing neighbor_ip",
			step:    Step{Name: "s", Action: ActionBGPRemoveNeighbor, Devices: DeviceSelector{Devices: []string{"leaf1"}}},
			wantErr: true, errMsg: "params.neighbor_ip is required",
		},
		// refresh-service
		{
			name:    "refresh-service valid",
			step:    Step{Name: "s", Action: ActionRefreshService, Devices: DeviceSelector{Devices: []string{"leaf1"}}, Interface: "Ethernet0"},
			wantErr: false,
		},
		{
			name:    "refresh-service missing interface",
			step:    Step{Name: "s", Action: ActionRefreshService, Devices: DeviceSelector{Devices: []string{"leaf1"}}},
			wantErr: true, errMsg: "interface is required",
		},
		// cleanup
		{
			name:    "cleanup valid",
			step:    Step{Name: "s", Action: ActionCleanup, Devices: DeviceSelector{Devices: []string{"leaf1"}}},
			wantErr: false,
		},
		{
			name:    "cleanup missing devices",
			step:    Step{Name: "s", Action: ActionCleanup},
			wantErr: true, errMsg: "devices is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Scenario{
				Name:     "test",
				Topology: "2node",
				Platform: "sonic-vpp",
				Steps:    []Step{tt.step},
			}
			err := ValidateScenario(&s, dir)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got: %s", tt.errMsg, err)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
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
	// All StepAction constants defined in scenario.go
	allActions := []StepAction{
		ActionProvision, ActionWait, ActionVerifyProvisioning,
		ActionVerifyConfigDB, ActionVerifyStateDB, ActionVerifyBGP,
		ActionVerifyHealth, ActionVerifyRoute, ActionVerifyPing,
		ActionApplyService, ActionRemoveService, ActionApplyBaseline,
		ActionSSHCommand, ActionRestartService, ActionApplyFRRDefaults,
		ActionSetInterface, ActionCreateVLAN, ActionDeleteVLAN,
		ActionAddVLANMember, ActionCreateVRF, ActionDeleteVRF,
		ActionSetupEVPN, ActionAddVRFInterface, ActionRemoveVRFInterface,
		ActionBindIPVPN, ActionUnbindIPVPN, ActionBindMACVPN, ActionUnbindMACVPN,
		ActionAddStaticRoute, ActionRemoveStaticRoute, ActionRemoveVLANMember,
		ActionApplyQoS, ActionRemoveQoS, ActionConfigureSVI,
		ActionBGPAddNeighbor, ActionBGPRemoveNeighbor,
		ActionRefreshService, ActionCleanup,
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
		ChangeSets: make(map[string]*network.ChangeSet),
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
		ChangeSets: make(map[string]*network.ChangeSet),
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
topology: 2node
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
topology: 2node
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
topology: 2node
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
topology: 2node
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
topology: 2node
platform: sonic-vpp
steps:
  - name: wait
    action: wait
    duration: 1s
`)
	writeScenario(t, dir, "06-vlan.yaml", `
name: vlan-numbered
description: numbered prefix
topology: 2node
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
// Executor Missing Params Panic Safety Tests (TE-01)
// ============================================================================

func TestExecutorsMissingParams_NoPanic(t *testing.T) {
	// Executors that operate via executeForDevices need r.Network and
	// resolveDevices, which requires a topology. With a nil Network, they
	// will either panic in resolveDevices or in the device lookup.
	// We test that the executor.Execute call itself doesn't panic with
	// nil Runner fields by wrapping in recover. Since these executors call
	// r.resolveDevices → r.allDeviceNames → r.Network.GetTopology, which
	// will panic on nil Network, we test the subset that doesn't call
	// resolveDevices first. For executors that do, we still verify no
	// unexpected panics by catching and reporting them.

	// Parameterized actions that extract params before doing device work.
	// Even with nil Network, the param extraction itself shouldn't panic.
	paramActions := []StepAction{
		ActionCreateVLAN, ActionDeleteVLAN, ActionAddVLANMember,
		ActionCreateVRF, ActionDeleteVRF, ActionSetupEVPN,
		ActionAddVRFInterface, ActionRemoveVRFInterface,
		ActionUnbindIPVPN, ActionUnbindMACVPN,
		ActionAddStaticRoute, ActionRemoveStaticRoute,
		ActionRemoveVLANMember, ActionRemoveQoS,
		ActionConfigureSVI, ActionBGPAddNeighbor, ActionBGPRemoveNeighbor,
		ActionSetInterface,
	}

	for _, action := range paramActions {
		t.Run(string(action), func(t *testing.T) {
			executor, ok := executors[action]
			if !ok {
				t.Skipf("no executor for %q", action)
				return
			}

			step := &Step{
				Action:  action,
				Name:    "test-" + string(action),
				Params:  map[string]any{},
				Devices: DeviceSelector{Devices: []string{"leaf1"}},
			}

			// The executor will panic at r.Network (nil dereference) when
			// it calls resolveDevices/executeForDevices. We verify that the
			// panic comes from the nil Network access, NOT from a missing
			// param causing an unexpected crash.
			func() {
				defer func() {
					if r := recover(); r != nil {
						// Expected: nil pointer on Network access.
						// Check that it's the Network nil deref, not something else.
						msg := fmt.Sprintf("%v", r)
						if !strings.Contains(msg, "nil pointer") && !strings.Contains(msg, "invalid memory address") {
							t.Errorf("executor %q panicked unexpectedly: %v", action, r)
						}
					}
				}()
				r := &Runner{
					ChangeSets: make(map[string]*network.ChangeSet),
				}
				executor.Execute(context.Background(), r, step)
			}()
		})
	}
}

func TestExecutorsMissingParams_BindIPVPN_NoNetwork(t *testing.T) {
	// bind-ipvpn extracts params and calls r.Network.GetIPVPN before
	// resolveDevices, so it should produce a clean panic or error on nil Network.
	executor := executors[ActionBindIPVPN]
	step := &Step{
		Action:  ActionBindIPVPN,
		Name:    "test-bind-ipvpn",
		Params:  map[string]any{"vrf": "Vrf_test", "ipvpn": "customer-a"},
		Devices: DeviceSelector{Devices: []string{"leaf1"}},
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				msg := fmt.Sprintf("%v", r)
				if !strings.Contains(msg, "nil pointer") && !strings.Contains(msg, "invalid memory address") {
					t.Errorf("executor panicked unexpectedly: %v", r)
				}
			}
		}()
		r := &Runner{
			ChangeSets: make(map[string]*network.ChangeSet),
		}
		executor.Execute(context.Background(), r, step)
	}()
}

func TestExecutorsMissingParams_BindMACVPN_NoNetwork(t *testing.T) {
	// bind-macvpn extracts params and calls r.Network.GetMACVPN before
	// resolveDevices, so it should produce a clean panic on nil Network.
	executor := executors[ActionBindMACVPN]
	step := &Step{
		Action:  ActionBindMACVPN,
		Name:    "test-bind-macvpn",
		Params:  map[string]any{"vlan_id": 100, "macvpn": "office-lan"},
		Devices: DeviceSelector{Devices: []string{"leaf1"}},
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				msg := fmt.Sprintf("%v", r)
				if !strings.Contains(msg, "nil pointer") && !strings.Contains(msg, "invalid memory address") {
					t.Errorf("executor panicked unexpectedly: %v", r)
				}
			}
		}()
		r := &Runner{
			ChangeSets: make(map[string]*network.ChangeSet),
		}
		executor.Execute(context.Background(), r, step)
	}()
}

func TestExecutorsMissingParams_ApplyQoS_NoNetwork(t *testing.T) {
	// apply-qos calls r.Network.GetQoSPolicy before resolveDevices
	executor := executors[ActionApplyQoS]
	step := &Step{
		Action:  ActionApplyQoS,
		Name:    "test-apply-qos",
		Params:  map[string]any{"interface": "Ethernet0", "qos_policy": "8q-datacenter"},
		Devices: DeviceSelector{Devices: []string{"leaf1"}},
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				msg := fmt.Sprintf("%v", r)
				if !strings.Contains(msg, "nil pointer") && !strings.Contains(msg, "invalid memory address") {
					t.Errorf("executor panicked unexpectedly: %v", r)
				}
			}
		}()
		r := &Runner{
			ChangeSets: make(map[string]*network.ChangeSet),
		}
		executor.Execute(context.Background(), r, step)
	}()
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
// DeviceSelector Additional Edge Cases (TE-01)
// ============================================================================

func TestDeviceSelector_Resolve_AllEmpty(t *testing.T) {
	ds := DeviceSelector{All: true}
	got := ds.Resolve(nil)
	if len(got) != 0 {
		t.Errorf("Resolve(all, nil) returned %d devices, want 0", len(got))
	}
}

func TestDeviceSelector_Resolve_AllPreservesOriginal(t *testing.T) {
	ds := DeviceSelector{All: true}
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
		{Name: "sc1", Topology: "2node", Platform: "sonic-vpp"},
		{Name: "sc2", Topology: "2node", Platform: "sonic-vpp"},
	}

	results, err := r.iterateScenarios(context.Background(), scenarios, RunOptions{}, func(_ context.Context, sc *Scenario, topology, platform string) (*ScenarioResult, error) {
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
	if results[0].Topology != "2node" || results[0].Platform != "sonic-vpp" {
		t.Errorf("result[0] topology=%q platform=%q, want 2node/sonic-vpp",
			results[0].Topology, results[0].Platform)
	}
}

func TestIterateScenarios_TopologyOverride(t *testing.T) {
	r := &Runner{}
	scenarios := []*Scenario{
		{Name: "sc1", Topology: "2node", Platform: "sonic-vpp"},
	}

	var gotTopology string
	_, err := r.iterateScenarios(context.Background(), scenarios, RunOptions{Topology: "4node"}, func(_ context.Context, sc *Scenario, topology, platform string) (*ScenarioResult, error) {
		gotTopology = topology
		return &ScenarioResult{Name: sc.Name, Topology: topology, Platform: platform, Status: StepStatusPassed}, nil
	})
	if err != nil {
		t.Fatalf("iterateScenarios error: %v", err)
	}
	if gotTopology != "4node" {
		t.Errorf("callback received topology=%q, want %q", gotTopology, "4node")
	}
}

func TestIterateScenarios_Resume(t *testing.T) {
	r := &Runner{}
	scenarios := []*Scenario{
		{Name: "sc1", Topology: "2node", Platform: "sonic-vpp"},
		{Name: "sc2", Topology: "2node", Platform: "sonic-vpp"},
	}

	callbackCalls := 0
	results, err := r.iterateScenarios(context.Background(), scenarios, RunOptions{
		Resume:    true,
		Completed: map[string]StepStatus{"sc1": StepStatusPassed},
	}, func(_ context.Context, sc *Scenario, topology, platform string) (*ScenarioResult, error) {
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
		{Name: "sc1", Topology: "2node", Platform: "sonic-vpp"},
		{Name: "sc2", Topology: "2node", Platform: "sonic-vpp", Requires: []string{"sc1"}},
	}

	results, err := r.iterateScenarios(context.Background(), scenarios, RunOptions{}, func(_ context.Context, sc *Scenario, topology, platform string) (*ScenarioResult, error) {
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
		{Name: "sc1", Topology: "2node", Platform: "sonic-vpp"},
		{Name: "sc2", Topology: "2node", Platform: "sonic-vpp"},
	}

	sentinel := fmt.Errorf("deploy failed")
	results, err := r.iterateScenarios(context.Background(), scenarios, RunOptions{}, func(_ context.Context, sc *Scenario, _, _ string) (*ScenarioResult, error) {
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
		ChangeSets: make(map[string]*network.ChangeSet),
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
		ChangeSets: make(map[string]*network.ChangeSet),
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
		ChangeSets: make(map[string]*network.ChangeSet),
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
		ChangeSets: make(map[string]*network.ChangeSet),
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

func TestRunScenarioSteps_InitChangeSets(t *testing.T) {
	r := &Runner{} // nil ChangeSets
	scenario := &Scenario{
		Name: "test",
		Steps: []Step{
			{Name: "quick-wait", Action: ActionWait, Duration: 0},
		},
	}
	result := &ScenarioResult{Name: "test"}
	r.runScenarioSteps(context.Background(), scenario, RunOptions{}, result)

	if r.ChangeSets == nil {
		t.Error("expected ChangeSets to be initialized, got nil")
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
