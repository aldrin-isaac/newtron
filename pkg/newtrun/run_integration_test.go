package newtrun

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRun_ParameterizedSuite_EndToEnd is the integration test the
// audit identified as missing: iteration_test.go drives
// runScenarioSteps directly with pre-populated r.resolvedIterations,
// which skips the LoadSuite → EffectiveTargets → TargetIterations
// chain. This test exercises the real bridge by pointing Runner at a
// temp scenarios/ directory containing a real suite.yaml +
// parameterized scenario, then asserts the cross-product iteration
// fired and each StepResult carries its TargetBinding.
//
// The scenario uses `wait` so it runs without a newtron-server
// dependency. A faux GET /networks/<id>/info handler stands in for
// newtron-server so connectToServer succeeds.
func TestRun_ParameterizedSuite_EndToEnd(t *testing.T) {
	scenariosDir := t.TempDir()
	// suite.yaml declares targets — both Eth0 and Eth4 across two
	// devices; cross-product is 4 bindings × 1 step = 4 results.
	suiteYAML := `name: int-test
description: integration suite for Runner.Run + parameterized expansion
network: synthetic
targets:
  devices: [s1, s2]
  interfaces: [Ethernet0, Ethernet4]
parameters:
  admin_status:
    type: enum
    values: [up, down]
    default: up
`
	if err := os.WriteFile(filepath.Join(scenariosDir, "suite.yaml"), []byte(suiteYAML), 0o644); err != nil {
		t.Fatalf("write suite.yaml: %v", err)
	}
	scenarioYAML := `name: rollout
description: parameterized rollout
steps:
  - name: noop
    action: wait
    duration: 1ms
    params:
      _bind: "{{target.device}}-{{target.interface}}-{{param.admin_status}}"
`
	if err := os.WriteFile(filepath.Join(scenariosDir, "00-rollout.yaml"), []byte(scenarioYAML), 0o644); err != nil {
		t.Fatalf("write scenario: %v", err)
	}

	// Faux newtron-server: returns the network registry containing
	// "test-net" with topology "synthetic" matching the suite so the
	// runner's topology guard passes. Other endpoints (topology /
	// devices) return empty data — --no-deploy mode skips connection.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/newtron/v1/networks":
			_, _ = w.Write([]byte(`{"data":[{"id":"test-net","topology":"synthetic","has_topology":true,"spec_dir":""}]}`))
		case strings.HasSuffix(r.URL.Path, "/topology/devices"):
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			_, _ = w.Write([]byte(`{"data":null}`))
		}
	}))
	defer srv.Close()

	runner := NewRunner(scenariosDir)
	runner.ServerURL = srv.URL
	runner.NetworkID = "test-net"

	results, err := runner.Run(context.Background(), RunOptions{
		All:      true,
		NoDeploy: true,
		Keep:     true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1 scenario", len(results))
	}
	scenario := results[0]
	if scenario.Status != StepStatusPassed {
		t.Errorf("scenario status = %v, want PASS", scenario.Status)
	}
	if got := len(scenario.Steps); got != 4 {
		t.Fatalf("scenario.Steps = %d, want 4 (2 devices × 2 interfaces × 1 step)", got)
	}
	// Every step result carries the binding that produced it.
	for i, sr := range scenario.Steps {
		if len(sr.TargetBinding) != 2 {
			t.Errorf("Steps[%d].TargetBinding = %v, want 2 keys (device, interface)", i, sr.TargetBinding)
		}
		if _, ok := sr.TargetBinding["device"]; !ok {
			t.Errorf("Steps[%d] missing device binding", i)
		}
		if _, ok := sr.TargetBinding["interface"]; !ok {
			t.Errorf("Steps[%d] missing interface binding", i)
		}
	}
}
