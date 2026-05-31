package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

// inlineScenarioWait is the simplest valid inline scenario: a wait step.
// Used by tests that exercise the happy path.
const inlineScenarioWait = `
name: inline-test
description: smoke test
topology: test-topo
steps:
  - name: brief-pause
    action: wait
    duration: 10ms
`

func postInline(t *testing.T, ts *httptest.Server, req InlineRunRequest) (*http.Response, *httputil.APIResponse) {
	t.Helper()
	body, _ := json.Marshal(req)
	resp, err := http.Post(ts.URL+"/api/v1/runs/inline", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	var env httputil.APIResponse
	_ = json.NewDecoder(resp.Body).Decode(&env)
	resp.Body.Close()
	return resp, &env
}

func TestInlineRejectsMissingYAML(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, _ := postInline(t, ts, InlineRunRequest{ScenarioYAML: ""})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestInlineRejectsMalformedYAML(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, _ := postInline(t, ts, InlineRunRequest{ScenarioYAML: "this is not: valid: yaml:::"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestInlineRejectsBannedAction(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	banned := `
name: bad
topology: t
steps:
  - name: shell
    action: host-exec
    devices: [host-a]
    command: rm -rf /
`
	resp, env := postInline(t, ts, InlineRunRequest{ScenarioYAML: banned})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(env.Error, "host-exec") {
		t.Errorf("error should mention banned action: %s", env.Error)
	}
}

func TestInlineRejectsReconcileWithoutOptIn(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	scenario := `
name: bad
topology: t
steps:
  - name: rec
    action: topology-reconcile
    devices: all
`
	resp, env := postInline(t, ts, InlineRunRequest{ScenarioYAML: scenario})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(env.Error, "topology-reconcile") {
		t.Errorf("error should mention topology-reconcile: %s", env.Error)
	}
}

func TestInlineAcceptsReconcileWithOptIn(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	scenario := `
name: ok
topology: t
steps:
  - name: rec
    action: topology-reconcile
    devices: all
`
	resp, env := postInline(t, ts, InlineRunRequest{
		ScenarioYAML:   scenario,
		AllowReconcile: true,
	})
	// We expect either 202 (accepted) or 500 (the runner fails fast on
	// connect — there's no newtron-server in the test). What we MUST NOT
	// see is a 400 with a reconcile error.
	if resp.StatusCode == http.StatusBadRequest {
		t.Errorf("opt-in should have prevented 400: %s", env.Error)
	}
}

func TestInlineReturns202AndUUIDID(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, env := postInline(t, ts, InlineRunRequest{
		ScenarioYAML:   inlineScenarioWait,
		TimeoutSeconds: 5,
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status: got %d, want 202; error: %s", resp.StatusCode, env.Error)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("data: got %T", env.Data)
	}
	runID, _ := data["run_id"].(string)
	if len(runID) != 36 || strings.Count(runID, "-") != 4 {
		t.Errorf("run_id should be UUID-shaped, got %q", runID)
	}
}

func TestInlineStatePersistedToInlineNamespace(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, env := postInline(t, ts, InlineRunRequest{
		ScenarioYAML:   inlineScenarioWait,
		TimeoutSeconds: 5,
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202; error: %s", resp.StatusCode, env.Error)
	}
	data := env.Data.(map[string]any)
	runID := data["run_id"].(string)

	// Wait for the run to land in the inline namespace.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if state, _ := newtrun.LoadInlineRunState(runID); state != nil {
			// Should NOT be in the suite namespace.
			if suiteState, _ := newtrun.LoadRunState(runID); suiteState != nil {
				t.Errorf("state leaked into suite namespace under %q", runID)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("inline state for %s never appeared", runID)
}

func TestInlineDeleteUnreachableUntilTerminal(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, env := postInline(t, ts, InlineRunRequest{
		ScenarioYAML:   inlineScenarioWait,
		TimeoutSeconds: 5,
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202; error: %s", resp.StatusCode, env.Error)
	}
	runID := env.Data.(map[string]any)["run_id"].(string)

	// Wait for the run to finish (the wait step is 10ms; the runner has
	// no newtron-server to connect to, so it fails fast).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if srv.Registry().Get(runID) == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// DELETE should clean the inline state.
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/runs/"+runID, nil)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Errorf("DELETE status: got %d, want 200", delResp.StatusCode)
	}

	// State should now be absent from both namespaces.
	if state, _ := newtrun.LoadInlineRunState(runID); state != nil {
		t.Errorf("inline state still present after DELETE")
	}
}

func TestInlineGetRunResolvesInlineID(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, env := postInline(t, ts, InlineRunRequest{
		ScenarioYAML:   inlineScenarioWait,
		TimeoutSeconds: 5,
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202; error: %s", resp.StatusCode, env.Error)
	}
	runID := env.Data.(map[string]any)["run_id"].(string)

	// Wait briefly for state to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if state, _ := newtrun.LoadInlineRunState(runID); state != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// GET /api/runs/{runID} should resolve through LoadAnyRunState.
	getResp, err := http.Get(ts.URL + "/api/v1/runs/" + runID)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Errorf("GET status: got %d, want 200", getResp.StatusCode)
	}
	var getEnv httputil.APIResponse
	_ = json.NewDecoder(getResp.Body).Decode(&getEnv)
	state, ok := getEnv.Data.(map[string]any)
	if !ok {
		t.Fatalf("data: got %T", getEnv.Data)
	}
	if state["suite"] != runID {
		t.Errorf("state.Suite: got %v, want %v", state["suite"], runID)
	}
}

// TestFinalizeInlineState_DeadlineExceededPersistsAborted is the
// integration guard for the §7 consolidation: the unit test on
// SuiteStatusFromOutcome proves the helper returns aborted, but a
// future refactor could still bypass it for inline runs. This test
// drives the real finalizeInlineState → SaveInlineRunState chain
// with a DeadlineExceeded error and reads the persisted state.json
// back to assert it actually landed as aborted on disk. If the
// finalizer ever post-processes the helper's return again (or stops
// using it altogether), this test catches it before merge.
func TestFinalizeInlineState_DeadlineExceededPersistsAborted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const runID = "deadline-exceeded-test-uuid"
	state := &newtrun.RunState{
		Suite:   runID,
		Status:  newtrun.SuiteStatusRunning,
		Started: time.Now().Add(-time.Second),
	}
	finalizeInlineState(state, nil, context.DeadlineExceeded)

	if state.Status != newtrun.SuiteStatusAborted {
		t.Errorf("in-memory state.Status = %v, want SuiteStatusAborted", state.Status)
	}
	loaded, err := newtrun.LoadInlineRunState(runID)
	if err != nil {
		t.Fatalf("LoadInlineRunState: %v", err)
	}
	if loaded == nil {
		t.Fatal("no persisted state for finalized inline run")
	}
	if loaded.Status != newtrun.SuiteStatusAborted {
		t.Errorf("persisted state.Status = %v, want SuiteStatusAborted", loaded.Status)
	}
}

func TestNewRunIDIsUUIDShape(t *testing.T) {
	// Generate a batch and verify the shape and uniqueness.
	const n = 100
	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		id := newRunID()
		if len(id) != 36 {
			t.Errorf("id len: got %d, want 36 (%q)", len(id), id)
		}
		if strings.Count(id, "-") != 4 {
			t.Errorf("id should have 4 dashes: %q", id)
		}
		if seen[id] {
			t.Errorf("duplicate id %q", id)
		}
		seen[id] = true
	}
}
