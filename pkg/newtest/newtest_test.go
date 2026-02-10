package newtest

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/newtron-network/newtron/pkg/device"
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
		want  Status
	}{
		{
			name:  "all passed",
			steps: []StepResult{{Status: StatusPassed}, {Status: StatusPassed}},
			want:  StatusPassed,
		},
		{
			name:  "one failed",
			steps: []StepResult{{Status: StatusPassed}, {Status: StatusFailed}},
			want:  StatusFailed,
		},
		{
			name:  "one error",
			steps: []StepResult{{Status: StatusPassed}, {Status: StatusError}},
			want:  StatusError,
		},
		{
			name:  "error and failed prefers failed",
			steps: []StepResult{{Status: StatusError}, {Status: StatusFailed}},
			want:  StatusFailed,
		},
		{
			name:  "empty steps is passed",
			steps: []StepResult{},
			want:  StatusPassed,
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
		status Status
		want   string
	}{
		{StatusPassed, "\u2713"},
		{StatusFailed, "\u2717"},
		{StatusSkipped, "\u2298"},
		{StatusError, "!"},
		{Status("unknown"), "?"},
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

	if err := validateDependencyGraph(scenarios); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateDependencyGraph_UnknownRequires(t *testing.T) {
	scenarios := []*Scenario{
		{Name: "a"},
		{Name: "b", Requires: []string{"nonexistent"}},
	}

	err := validateDependencyGraph(scenarios)
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

	err := validateDependencyGraph(scenarios)
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

	err := validateDependencyGraph(scenarios)
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
	if err := validateDependencyGraph(scenarios); err != nil {
		t.Fatalf("validateDependencyGraph error: %v", err)
	}
	sorted, err := topologicalSort(scenarios)
	if err != nil {
		t.Fatalf("topologicalSort error: %v", err)
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
	if err := validateDependencyGraph(scenarios); err == nil {
		t.Fatal("expected cycle error")
	}
}

// ============================================================================
// Skip Propagation Tests
// ============================================================================

func TestStatusVerb(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{StatusFailed, "failed"},
		{StatusError, "errored"},
		{StatusSkipped, "was skipped"},
		{StatusPassed, "PASS"},
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
	status := map[string]Status{
		"a": StatusPassed,
		"b": StatusFailed,
		"c": StatusSkipped,
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
	if hasRequires([]*Scenario{{Name: "a"}, {Name: "b"}}) {
		t.Error("expected false for no requires")
	}
	if !hasRequires([]*Scenario{{Name: "a"}, {Name: "b", Requires: []string{"a"}}}) {
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
			Status:     StatusSkipped,
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
			Status:     StatusSkipped,
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
			Status:   StatusPassed,
			Duration: 5 * time.Minute,
			Repeat:   10,
			Steps: func() []StepResult {
				var steps []StepResult
				for i := 1; i <= 10; i++ {
					steps = append(steps,
						StepResult{Name: "apply", Action: ActionSSHCommand, Status: StatusPassed, Iteration: i},
						StepResult{Name: "remove", Action: ActionSSHCommand, Status: StatusPassed, Iteration: i},
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
			Status:          StatusFailed,
			Duration:        2 * time.Minute,
			Repeat:          10,
			FailedIteration: 5,
			Steps: func() []StepResult {
				var steps []StepResult
				for i := 1; i <= 4; i++ {
					steps = append(steps,
						StepResult{Name: "apply", Action: ActionSSHCommand, Status: StatusPassed, Iteration: i},
						StepResult{Name: "remove", Action: ActionSSHCommand, Status: StatusPassed, Iteration: i},
					)
				}
				// Iteration 5 fails on apply
				steps = append(steps,
					StepResult{Name: "apply", Action: ActionSSHCommand, Status: StatusFailed, Iteration: 5, Message: "service binding not found"},
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
			Status:   StatusPassed,
			Repeat:   3,
			Steps: []StepResult{
				{Name: "apply", Action: ActionSSHCommand, Status: StatusPassed, Iteration: 1},
				{Name: "remove", Action: ActionSSHCommand, Status: StatusPassed, Iteration: 1},
				{Name: "apply", Action: ActionSSHCommand, Status: StatusPassed, Iteration: 2},
				{Name: "remove", Action: ActionSSHCommand, Status: StatusPassed, Iteration: 2},
				{Name: "apply", Action: ActionSSHCommand, Status: StatusPassed, Iteration: 3},
				{Name: "remove", Action: ActionSSHCommand, Status: StatusPassed, Iteration: 3},
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
// Helpers
// ============================================================================

func writeScenario(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
