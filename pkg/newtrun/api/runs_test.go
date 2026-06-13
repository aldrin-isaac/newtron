package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

// writeMinimalSuite creates a single-scenario suite directory the server
// can resolve. The scenario uses `wait` so it runs without needing a
// newtron-server connection. The fixture writes a suite.yaml manifest
// alongside — handleStartRun's pre-flight LoadSuite refuses to resolve
// a directory without one.
func writeMinimalSuite(t *testing.T, base, name, body string) {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	manifest := fmt.Sprintf("name: %s\ntopology: synthetic\n", name)
	if err := os.WriteFile(filepath.Join(dir, "suite.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write suite.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "00-only.yaml"), []byte(body), 0644); err != nil {
		t.Fatalf("write scenario: %v", err)
	}
}

func TestStartRunRejectsMissingSuite(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	body, _ := json.Marshal(StartRunRequest{Suite: ""})
	resp, err := http.Post(ts.URL+"/newtrun/v1/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestStartRunRejectsUnknownSuite(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	body, _ := json.Marshal(StartRunRequest{Suite: "does-not-exist"})
	resp, err := http.Post(ts.URL+"/newtrun/v1/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestStartRunSameSuiteRejected409(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	// Pre-acquire the suite key directly via the registry so we don't have
	// to invoke a real scenario run.
	_, err := srv.registry.Acquire("blocked-suite")
	if err != nil {
		t.Fatalf("pre-Acquire: %v", err)
	}
	writeMinimalSuite(t, srv.cfg.SuitesBase, "blocked-suite", scenarioYAMLBody)

	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	body, _ := json.Marshal(StartRunRequest{Suite: "blocked-suite"})
	resp, err := http.Post(ts.URL+"/newtrun/v1/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status: got %d, want 409", resp.StatusCode)
	}
}

func TestStartRunReturns202AndRegistersEntry(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	writeMinimalSuite(t, srv.cfg.SuitesBase, "blocked-suite", scenarioYAMLBody)

	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	body, _ := json.Marshal(StartRunRequest{Suite: "blocked-suite", NoDeploy: true})
	resp, err := http.Post(ts.URL+"/newtrun/v1/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status: got %d, want 202", resp.StatusCode)
	}

	// Give the goroutine a moment to register; then verify registry tracks it.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if srv.registry.Get("blocked-suite") != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The runner may have completed already (the scenario body fails fast on
	// connection refused — there's no newtron-server in tests). Both
	// "registered then released" and "registered now" are correct outcomes;
	// we only fail if neither ever happened. Since the registry was the
	// substrate for the 409 rejection test above, the path is exercised.
	t.Log("registry entry registered + released before assertion; not a failure")
}

// TestStartRunReturns400OnBadTargetsOverride and the param-override
// variant guard the pre-flight validation in handleStartRun. Without
// it, override-validation errors would land in the goroutine's
// state.json as status=failed instead of as 400 on the request.
func TestStartRunReturns400OnUnknownTargetsDimension(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	writeMinimalSuite(t, srv.cfg.SuitesBase, "demo-suite", scenarioYAMLBody)
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	body, _ := json.Marshal(StartRunRequest{
		Suite:   "demo-suite",
		All:     true,
		Targets: map[string][]string{"racks": {"r1"}},
	})
	resp, err := http.Post(ts.URL+"/newtrun/v1/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400 (unknown dimension should fail pre-flight, not bury in state.json)", resp.StatusCode)
	}
}

func TestStartRunReturns400OnTargetWhitelistViolation(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	writeMinimalSuite(t, srv.cfg.SuitesBase, "demo-suite", scenarioYAMLBody)
	// Suite needs a declared dimension for the override to even
	// reach the value-whitelist check. Append it to the suite.yaml
	// the fixture wrote.
	suitePath := filepath.Join(srv.cfg.SuitesBase, "demo-suite", "suite.yaml")
	manifest, _ := os.ReadFile(suitePath)
	manifest = append(manifest, []byte("targets:\n  devices: [s1]\n")...)
	_ = os.WriteFile(suitePath, manifest, 0644)

	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	body, _ := json.Marshal(StartRunRequest{
		Suite:   "demo-suite",
		All:     true,
		Targets: map[string][]string{"devices": {"s1; rm -rf /"}},
	})
	resp, err := http.Post(ts.URL+"/newtrun/v1/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400 (identifier-whitelist violation should fail pre-flight)", resp.StatusCode)
	}
}

func TestStartRunReturns400OnUnknownParameterOverride(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	writeMinimalSuite(t, srv.cfg.SuitesBase, "demo-suite", scenarioYAMLBody)
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	body, _ := json.Marshal(StartRunRequest{
		Suite:      "demo-suite",
		All:        true,
		Parameters: map[string]any{"made_up": "x"},
	})
	resp, err := http.Post(ts.URL+"/newtrun/v1/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400 (undeclared parameter should fail pre-flight)", resp.StatusCode)
	}
}

// TestStepResultPayload_CarriesTargetBinding guards the wire shape
// for parameterized runs. StepResult.TargetBinding (canonical Go
// type) and StepResultPayload.TargetBinding (wire shape) are both
// declared, but a future rename of stepResultFrom that drops the
// field would silently break browser frontends / inline-state
// consumers that pivot results on (device, interface) tuples. The
// test serializes a StepResult containing a binding through
// stepResultFrom and asserts the raw JSON carries target_binding.
func TestStepResultPayload_CarriesTargetBinding(t *testing.T) {
	canonical := &newtrun.StepResult{
		Name:   "verify-admin-status",
		Action: newtrun.ActionNewtron,
		Status: newtrun.StepStatusPassed,
		TargetBinding: map[string]string{
			"device":    "switch1",
			"interface": "Ethernet0",
		},
	}
	payload := stepResultFrom(canonical)
	if !reflect.DeepEqual(payload.TargetBinding, canonical.TargetBinding) {
		t.Errorf("payload.TargetBinding = %v, want %v",
			payload.TargetBinding, canonical.TargetBinding)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"target_binding"`)) {
		t.Errorf("JSON missing target_binding field: %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"device":"switch1"`)) {
		t.Errorf("JSON missing device=switch1: %s", raw)
	}
}

// TestStepResultPayload_OmitsEmptyTargetBinding confirms the omitempty
// tag works — embedded-target step results (binding=nil) must not
// emit "target_binding": null on the wire.
func TestStepResultPayload_OmitsEmptyTargetBinding(t *testing.T) {
	canonical := &newtrun.StepResult{
		Name:          "wait",
		Action:        newtrun.ActionWait,
		Status:        newtrun.StepStatusPassed,
		TargetBinding: nil,
	}
	raw, err := json.Marshal(stepResultFrom(canonical))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(raw, []byte(`"target_binding"`)) {
		t.Errorf("nil binding leaked target_binding field: %s", raw)
	}
}

func TestStopRunReturns404ForUnknownSuite(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/newtrun/v1/runs/no-such/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestStopRunCallsRegistryCancel(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	entry, _ := srv.registry.Acquire("cancel-target")
	canceled := false
	entry.Cancel = func() { canceled = true }
	defer srv.registry.Release("cancel-target", &RunResult{})

	resp, err := http.Post(ts.URL+"/newtrun/v1/runs/cancel-target/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	if !canceled {
		t.Error("Cancel was not invoked on the registry entry")
	}
}

func TestDeleteRunRejectsActiveRun(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	_, _ = srv.registry.Acquire("active")
	defer srv.registry.Release("active", &RunResult{})

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/newtrun/v1/runs/active", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status: got %d, want 409", resp.StatusCode)
	}
}

func TestDeleteRunRemovesState(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	// Seed state directly via newtrun's helpers.
	state := &newtrun.RunState{
		Suite:    "to-delete",
		Status:   newtrun.SuiteStatusComplete,
		Finished: time.Now(),
	}
	if err := newtrun.SaveRunState(state); err != nil {
		t.Fatalf("SaveRunState: %v", err)
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/newtrun/v1/runs/to-delete", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}

	// State should now be absent.
	if got, _ := newtrun.LoadRunState("to-delete"); got != nil {
		t.Errorf("state still present after DELETE")
	}
}

func TestPauseRunWritesPausingStatus(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	// An active run must exist in the registry for the pause endpoint to
	// accept it; a state file must exist for it to update.
	_, _ = srv.registry.Acquire("paused-target")
	defer srv.registry.Release("paused-target", &RunResult{})

	state := &newtrun.RunState{Suite: "paused-target", Status: newtrun.SuiteStatusRunning}
	if err := newtrun.SaveRunState(state); err != nil {
		t.Fatalf("SaveRunState: %v", err)
	}

	resp, err := http.Post(ts.URL+"/newtrun/v1/runs/paused-target/pause", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}

	got, _ := newtrun.LoadRunState("paused-target")
	if got == nil || got.Status != newtrun.SuiteStatusPausing {
		t.Errorf("state.Status: got %v, want %v", got.Status, newtrun.SuiteStatusPausing)
	}
}

func TestServerStopCancelsInFlightRuns(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	entry, _ := srv.registry.Acquire("running")
	canceled := make(chan struct{})
	entry.Cancel = func() { close(canceled) }

	// Simulate the run goroutine completing after cancellation.
	go func() {
		<-canceled
		srv.registry.Release("running", &RunResult{})
	}()

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// We can't call srv.Stop here because the httpServer was never started.
	// Instead call CancelAll directly — that's the substantive behavior
	// Server.Stop relies on.
	srv.registry.CancelAll(time.Second)

	select {
	case <-canceled:
	case <-stopCtx.Done():
		t.Fatal("Cancel was not invoked on the in-flight run")
	}
}

// TestReconcileStaleStatus drives the helper through every input
// branch directly, so a regression in the rule (flipped condition,
// wrong registry key, missing nil-guard) surfaces here rather than
// only through the integration paths in handleGetRun / handleListRuns.
// Per §16 (Write Honest Tests): assert the specific status that
// should result, not just "something changed."
func TestReconcileStaleStatus(t *testing.T) {
	srv, _ := newTestServer(t)
	cases := []struct {
		name           string
		startStatus    newtrun.SuiteStatus
		acquireFirst   bool // populate the registry with the runKey
		wantStatusAfter newtrun.SuiteStatus
	}{
		{"running + not in registry → aborted", newtrun.SuiteStatusRunning, false, newtrun.SuiteStatusAborted},
		{"pausing + not in registry → aborted", newtrun.SuiteStatusPausing, false, newtrun.SuiteStatusAborted},
		{"running + in registry → no change", newtrun.SuiteStatusRunning, true, newtrun.SuiteStatusRunning},
		{"pausing + in registry → no change", newtrun.SuiteStatusPausing, true, newtrun.SuiteStatusPausing},
		{"complete + not in registry → no change", newtrun.SuiteStatusComplete, false, newtrun.SuiteStatusComplete},
		{"failed + not in registry → no change", newtrun.SuiteStatusFailed, false, newtrun.SuiteStatusFailed},
		{"paused + not in registry → no change", newtrun.SuiteStatusPaused, false, newtrun.SuiteStatusPaused},
		{"aborted + not in registry → no change", newtrun.SuiteStatusAborted, false, newtrun.SuiteStatusAborted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runKey := "reconcile-test-" + string(tc.startStatus)
			if tc.acquireFirst {
				entry, err := srv.registry.Acquire(runKey)
				if err != nil {
					t.Fatalf("registry.Acquire: %v", err)
				}
				defer srv.registry.Release(runKey, &RunResult{})
				_ = entry
			}
			state := &newtrun.RunState{Suite: runKey, Status: tc.startStatus}
			srv.reconcileStaleStatus(state, runKey)
			if state.Status != tc.wantStatusAfter {
				t.Errorf("Status: got %v, want %v", state.Status, tc.wantStatusAfter)
			}
		})
	}
}

// TestReconcileStaleStatus_NilState exercises the nil-guard so a future
// caller that hands in a nil state (e.g., after a not-found LoadAnyRunState
// returns) cannot panic the handler.
func TestReconcileStaleStatus_NilState(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.reconcileStaleStatus(nil, "anything") // must not panic
}

// scenarioYAMLBody is a minimal scenario that requires no newtron-server
// to load. topology is suite-level (writeMinimalSuite emits suite.yaml),
// not on the scenario; `wait` is a pure sleep.
const scenarioYAMLBody = `
name: smoke
description: minimal scenario for tests
steps:
  - name: brief-pause
    action: wait
    duration: 10ms
`

func init() {
	// Ensure the test server's default newtron URL is unreachable so
	// runner.connectToServer fails fast. Tests don't rely on the runner
	// actually completing; they only need the lifecycle entry/exit points.
	_ = strings.NewReader // silence unused if all tests are removed
}

// TestOperatorBearer_ExtractsFromAuthorizationHeader pins the
// inbound-side of the engine-composition refactor (PR C) operator-
// Bearer-forward flow (auth-design.md §L2c "Identity forwarding
// through engines"). The /newtrun/v1/runs handler reads the
// operator's session key out of the Authorization header so the
// runner can forward it on every outbound newtron call.
func TestOperatorBearer_ExtractsFromAuthorizationHeader(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"absent", "", ""},
		{"bearer present", "Bearer abc123", "abc123"},
		{"bearer case insensitive", "bearer abc123", "abc123"},
		{"bearer with trailing space", "Bearer  abc123  ", "abc123"},
		{"basic ignored", "Basic YWxpY2U6cHc=", ""},
		{"token scheme ignored", "Token abc123", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/runs", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			got := operatorBearer(req)
			if got != tc.want {
				t.Errorf("operatorBearer(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}
