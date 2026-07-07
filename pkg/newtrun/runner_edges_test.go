package newtrun

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Integration tests for the runner-edge features added after the §48
// continuity-check campaign exposed them (post-mortem "Bad 6"):
//   - cleanup: blocks (failed scenarios stranding fabric state cascaded
//     into unrelated downstream failures)
//   - device-scoped capture (uptime-witness scenarios had to hardcode
//     /nodes/switch1/... to reach the network-scoped capture path)
//   - host-exec poll (asynchronous dataplane readiness had no host-side
//     polling; scenarios embedded fixed waits)
//
// All three follow capture_integration_test.go's faux-server pattern.

// edgeHarness stands up a suite dir + faux newtron-server and returns the
// runner plus a recorder of which URL paths were hit.
func edgeHarness(t *testing.T, scenarioYAML string, handler func(path string, w http.ResponseWriter, r *http.Request) bool) (*Runner, *[]string, func()) {
	t.Helper()
	scenariosDir := t.TempDir()
	suiteYAML := "name: edges-int\ndescription: runner edges integration\nnetwork: synthetic\n"
	if err := os.WriteFile(filepath.Join(scenariosDir, "suite.yaml"), []byte(suiteYAML), 0o644); err != nil {
		t.Fatalf("write suite.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scenariosDir, "00-edge.yaml"), []byte(scenarioYAML), 0o644); err != nil {
		t.Fatalf("write scenario: %v", err)
	}

	var mu sync.Mutex
	hits := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.URL.Path)
		mu.Unlock()
		switch {
		case r.URL.Path == "/newtron/v1/networks":
			_, _ = w.Write([]byte(`{"data":[{"id":"test-net","topology":"synthetic","has_topology":true,"dir":""}]}`))
		case strings.HasSuffix(r.URL.Path, "/topology/devices"):
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			if handler != nil && handler(r.URL.Path, w, r) {
				return
			}
			_, _ = w.Write([]byte(`{"data":null}`))
		}
	}))

	runner := NewRunner(scenariosDir)
	runner.ServerURL = srv.URL
	runner.NetworkID = "test-net"
	return runner, &hits, srv.Close
}

func runEdgeSuite(t *testing.T, runner *Runner) *ScenarioResult {
	t.Helper()
	results, err := runner.Run(context.Background(), RunOptions{All: true, NoDeploy: true, Keep: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1 scenario", len(results))
	}
	return results[0]
}

// TestRun_CleanupRunsOnFailure pins the motivating property: a scenario
// whose main steps fail still executes EVERY cleanup step (best-effort,
// no fail-fast between cleanup steps), and the cleanup results are
// recorded with the cleanup/ prefix.
func TestRun_CleanupRunsOnFailure(t *testing.T) {
	scenarioYAML := `name: cleanup-on-failure
description: cleanup must run when main steps fail
steps:
  - name: failing-step
    action: newtron
    url: /check
    expect:
      jq: '.ready == true'
cleanup:
  - name: teardown-a
    action: newtron
    method: POST
    url: /teardown-a
  - name: teardown-b
    action: newtron
    method: POST
    url: /teardown-b
`
	runner, hits, closeSrv := edgeHarness(t, scenarioYAML, func(path string, w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(path, "/check") {
			_, _ = w.Write([]byte(`{"data":{"ready":false}}`))
			return true
		}
		return false
	})
	defer closeSrv()

	result := runEdgeSuite(t, runner)
	if result.Status != StepStatusFailed {
		t.Fatalf("scenario status = %v, want FAIL (main step failed)", result.Status)
	}

	hitA, hitB := false, false
	for _, h := range *hits {
		if strings.HasSuffix(h, "/teardown-a") {
			hitA = true
		}
		if strings.HasSuffix(h, "/teardown-b") {
			hitB = true
		}
	}
	if !hitA || !hitB {
		t.Errorf("cleanup endpoints hit: a=%v b=%v, want both (best-effort cleanup after failure)", hitA, hitB)
	}

	var names []string
	for _, s := range result.Steps {
		names = append(names, s.Name)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "cleanup/teardown-a") || !strings.Contains(joined, "cleanup/teardown-b") {
		t.Errorf("cleanup step results missing or unprefixed: %v", names)
	}
}

// TestRun_CleanupFailureFailsScenario pins the dirty-fabric rule: main
// steps pass, a cleanup step fails, the scenario fails — a fabric left
// dirty is a real failure, not a footnote.
func TestRun_CleanupFailureFailsScenario(t *testing.T) {
	scenarioYAML := `name: cleanup-failure
description: a failed cleanup fails the scenario
steps:
  - name: main-passes
    action: newtron
    url: /ok
cleanup:
  - name: teardown-fails
    action: newtron
    url: /check
    expect:
      jq: '.gone == true'
`
	runner, _, closeSrv := edgeHarness(t, scenarioYAML, func(path string, w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(path, "/check") {
			_, _ = w.Write([]byte(`{"data":{"gone":false}}`))
			return true
		}
		return false
	})
	defer closeSrv()

	result := runEdgeSuite(t, runner)
	if result.Status != StepStatusFailed {
		t.Fatalf("scenario status = %v, want FAIL (cleanup failed on an otherwise-passing scenario)", result.Status)
	}
}

// TestRun_DeviceScopedCapture pins the single-device capture path: a
// {{device}}-templated step with exactly one device captures from its one
// response, and a later step reads the value — no more hardcoding
// /nodes/<name>/... to reach the network-scoped path.
func TestRun_DeviceScopedCapture(t *testing.T) {
	scenarioYAML := `name: device-capture
description: capture on a single-device templated step
steps:
  - name: capture-from-device
    action: newtron
    devices: [switch1]
    url: /nodes/{{device}}/status
    capture:
      up_before: .uptime
  - name: use-captured
    action: newtron
    method: POST
    url: /assert
    params:
      seen: "{{captured.up_before}}"
`
	var mu sync.Mutex
	var assertBody string
	runner, _, closeSrv := edgeHarness(t, scenarioYAML, func(path string, w http.ResponseWriter, r *http.Request) bool {
		switch {
		case strings.HasSuffix(path, "/nodes/switch1/status"):
			_, _ = w.Write([]byte(`{"data":{"uptime":"12345"}}`))
			return true
		case strings.HasSuffix(path, "/assert"):
			buf := make([]byte, 256)
			n, _ := r.Body.Read(buf)
			mu.Lock()
			assertBody = string(buf[:n])
			mu.Unlock()
			_, _ = w.Write([]byte(`{"data":null}`))
			return true
		}
		return false
	})
	defer closeSrv()

	result := runEdgeSuite(t, runner)
	if result.Status != StepStatusPassed {
		t.Fatalf("scenario status = %v, steps = %+v", result.Status, result.Steps)
	}
	mu.Lock()
	body := assertBody
	mu.Unlock()
	if !strings.Contains(body, "12345") {
		t.Errorf("captured value did not reach the later step: body = %q", body)
	}
}
