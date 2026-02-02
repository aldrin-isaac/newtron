package health

import (
	"context"
	"testing"
	"time"

	"github.com/newtron-network/newtron/pkg/network"
)

func TestStatusConstants(t *testing.T) {
	tests := []struct {
		status   Status
		expected string
	}{
		{StatusOK, "ok"},
		{StatusWarning, "warning"},
		{StatusCritical, "critical"},
		{StatusUnknown, "unknown"},
	}

	for _, tt := range tests {
		if string(tt.status) != tt.expected {
			t.Errorf("Status %v = %q, want %q", tt.status, string(tt.status), tt.expected)
		}
	}
}

func TestResult_Structure(t *testing.T) {
	now := time.Now()
	result := Result{
		Check:     "interfaces",
		Status:    StatusOK,
		Message:   "All interfaces healthy",
		Details:   map[string]int{"total": 48, "down": 0},
		Duration:  100 * time.Millisecond,
		Timestamp: now,
	}

	if result.Check != "interfaces" {
		t.Errorf("Check = %q", result.Check)
	}
	if result.Status != StatusOK {
		t.Errorf("Status = %q", result.Status)
	}
	if result.Message != "All interfaces healthy" {
		t.Errorf("Message = %q", result.Message)
	}
	if result.Duration != 100*time.Millisecond {
		t.Errorf("Duration = %v", result.Duration)
	}
	if result.Timestamp != now {
		t.Errorf("Timestamp = %v", result.Timestamp)
	}

	details, ok := result.Details.(map[string]int)
	if !ok {
		t.Fatalf("Details is not map[string]int")
	}
	if details["total"] != 48 {
		t.Errorf("Details[total] = %d", details["total"])
	}
}

func TestReport_Structure(t *testing.T) {
	now := time.Now()
	report := Report{
		Device:    "leaf1-ny",
		Timestamp: now,
		Overall:   StatusOK,
		Results: []Result{
			{Check: "interfaces", Status: StatusOK},
			{Check: "bgp", Status: StatusOK},
		},
		Duration: 500 * time.Millisecond,
	}

	if report.Device != "leaf1-ny" {
		t.Errorf("Device = %q", report.Device)
	}
	if report.Overall != StatusOK {
		t.Errorf("Overall = %q", report.Overall)
	}
	if len(report.Results) != 2 {
		t.Errorf("Results count = %d", len(report.Results))
	}
	if report.Duration != 500*time.Millisecond {
		t.Errorf("Duration = %v", report.Duration)
	}
}

func TestNewChecker(t *testing.T) {
	checker := NewChecker()

	// Should have default checks
	checks := checker.ListChecks()
	if len(checks) != 5 {
		t.Errorf("ListChecks() count = %d, want %d", len(checks), 5)
	}

	// Verify expected checks are present
	expectedChecks := map[string]bool{
		"interfaces": false,
		"lag":        false,
		"bgp":        false,
		"vxlan":      false,
		"evpn":       false,
	}

	for _, name := range checks {
		if _, ok := expectedChecks[name]; ok {
			expectedChecks[name] = true
		}
	}

	for name, found := range expectedChecks {
		if !found {
			t.Errorf("Expected check '%s' not found", name)
		}
	}
}

func TestChecker_AddCheck(t *testing.T) {
	checker := NewChecker()
	initialCount := len(checker.ListChecks())

	// Add custom check
	checker.AddCheck(&customCheck{name: "custom"})

	checks := checker.ListChecks()
	if len(checks) != initialCount+1 {
		t.Errorf("ListChecks() count = %d, want %d", len(checks), initialCount+1)
	}

	// Verify custom check is present
	found := false
	for _, name := range checks {
		if name == "custom" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Custom check not found in list")
	}
}

func TestChecker_ListChecks(t *testing.T) {
	checker := NewChecker()
	checks := checker.ListChecks()

	if len(checks) == 0 {
		t.Error("ListChecks() should return at least one check")
	}

	// Check that names are not empty
	for _, name := range checks {
		if name == "" {
			t.Error("Check name should not be empty")
		}
	}
}

func TestInterfaceCheck_Name(t *testing.T) {
	check := &InterfaceCheck{}
	if check.Name() != "interfaces" {
		t.Errorf("Name() = %q, want %q", check.Name(), "interfaces")
	}
}

func TestLAGCheck_Name(t *testing.T) {
	check := &LAGCheck{}
	if check.Name() != "lag" {
		t.Errorf("Name() = %q, want %q", check.Name(), "lag")
	}
}

func TestBGPCheck_Name(t *testing.T) {
	check := &BGPCheck{}
	if check.Name() != "bgp" {
		t.Errorf("Name() = %q, want %q", check.Name(), "bgp")
	}
}

func TestVXLANCheck_Name(t *testing.T) {
	check := &VXLANCheck{}
	if check.Name() != "vxlan" {
		t.Errorf("Name() = %q, want %q", check.Name(), "vxlan")
	}
}

func TestEVPNCheck_Name(t *testing.T) {
	check := &EVPNCheck{}
	if check.Name() != "evpn" {
		t.Errorf("Name() = %q, want %q", check.Name(), "evpn")
	}
}

func TestStatus_Comparison(t *testing.T) {
	// Test that we can compare statuses
	tests := []struct {
		a, b     Status
		expected bool
	}{
		{StatusOK, StatusOK, true},
		{StatusOK, StatusWarning, false},
		{StatusWarning, StatusWarning, true},
		{StatusCritical, StatusCritical, true},
		{StatusUnknown, StatusUnknown, true},
	}

	for _, tt := range tests {
		if (tt.a == tt.b) != tt.expected {
			t.Errorf("(%q == %q) = %v, want %v", tt.a, tt.b, tt.a == tt.b, tt.expected)
		}
	}
}

// customCheck is a test implementation of Check interface
type customCheck struct {
	name string
}

func (c *customCheck) Name() string {
	return c.name
}

func (c *customCheck) Run(ctx context.Context, d *network.Device) Result {
	return Result{
		Check:   c.name,
		Status:  StatusOK,
		Message: "Custom check passed",
	}
}
