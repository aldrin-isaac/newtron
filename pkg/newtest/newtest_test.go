package newtest

import (
	"os"
	"path/filepath"
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
