package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

// newTestServer builds a Server configured against a temporary directory.
// HOME is also overridden so newtrun.LoadRunState / SaveRunState route to
// the same temp directory.
func newTestServer(t *testing.T) (*Server, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	suitesBase := filepath.Join(tmpDir, "suites")
	topologiesBase := filepath.Join(tmpDir, "topologies")
	if err := os.MkdirAll(suitesBase, 0755); err != nil {
		t.Fatalf("mkdir suitesBase: %v", err)
	}
	if err := os.MkdirAll(topologiesBase, 0755); err != nil {
		t.Fatalf("mkdir topologiesBase: %v", err)
	}
	t.Setenv("NEWTRUN_SUITES_BASE", suitesBase)

	srv := NewServer(Config{
		SuitesBase:     suitesBase,
		TopologiesBase: topologiesBase,
		Logger:         log.New(io.Discard, "", 0),
	})
	return srv, func() {}
}

// seedSuite writes a state.json for one suite and creates the matching suite
// directory so ListSuiteStates returns it.
func seedSuite(t *testing.T, name string, status newtrun.SuiteStatus) {
	t.Helper()
	state := &newtrun.RunState{
		Suite:    name,
		Topology: "test-topo",
		Status:   status,
		Started:  time.Now().Add(-time.Hour),
		Updated:  time.Now().Add(-30 * time.Minute),
	}
	if status == newtrun.SuiteStatusComplete || status == newtrun.SuiteStatusFailed {
		state.Finished = time.Now().Add(-15 * time.Minute)
	}
	if err := newtrun.SaveRunState(state); err != nil {
		t.Fatalf("SaveRunState(%s): %v", name, err)
	}
	// Create the corresponding suite directory under the suites base.
	suitesBase := os.Getenv("NEWTRUN_SUITES_BASE")
	if err := os.MkdirAll(filepath.Join(suitesBase, name), 0755); err != nil {
		t.Fatalf("mkdir suite dir(%s): %v", name, err)
	}
}

func TestHealth(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}

	var body httputil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, ok := body.Data.(map[string]any)
	if !ok || data["status"] != "ok" {
		t.Errorf("data: got %+v, want status=ok", body.Data)
	}
}

func TestListRunsReturnsSeededSuites(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedSuite(t, "suite-a", newtrun.SuiteStatusComplete)
	seedSuite(t, "suite-b", newtrun.SuiteStatusRunning)

	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/runs")
	if err != nil {
		t.Fatalf("GET /api/runs: %v", err)
	}
	defer resp.Body.Close()

	var body httputil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	infos, ok := body.Data.([]any)
	if !ok {
		t.Fatalf("data: got %T, want []any", body.Data)
	}
	if len(infos) != 2 {
		t.Errorf("infos: got %d, want 2 (data=%+v)", len(infos), infos)
	}
}

func TestGetRunReturnsFullState(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedSuite(t, "suite-a", newtrun.SuiteStatusComplete)

	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/runs/suite-a")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}

	var body httputil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	state, ok := body.Data.(map[string]any)
	if !ok {
		t.Fatalf("data: got %T, want map", body.Data)
	}
	if state["suite"] != "suite-a" {
		t.Errorf("suite: got %v, want suite-a", state["suite"])
	}
	if state["status"] != "complete" {
		t.Errorf("status: got %v, want complete", state["status"])
	}
}

func TestGetRunNotFound(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/runs/nonexistent")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestListTopologiesEmptyBaseReturnsEmptyList(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/topologies")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var body httputil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, ok := body.Data.(map[string]any)
	if !ok {
		t.Fatalf("data: got %T", body.Data)
	}
	tops, ok := data["topologies"].([]any)
	if !ok {
		t.Fatalf("topologies: got %T", data["topologies"])
	}
	if len(tops) != 0 {
		t.Errorf("topologies: got %d, want 0", len(tops))
	}
}

func TestListTopologiesReturnsSubdirs(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	for _, name := range []string{"topo-a", "topo-b"} {
		if err := os.MkdirAll(filepath.Join(srv.cfg.TopologiesBase, name), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/topologies")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var body httputil.APIResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	data := body.Data.(map[string]any)
	tops := data["topologies"].([]any)
	if len(tops) != 2 {
		t.Errorf("got %d topologies, want 2: %+v", len(tops), tops)
	}
}

func TestSSEEndpointOpensConnectionAndSendsSubscribedComment(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/runs/suite-a/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type: got %q, want text/event-stream", ct)
	}

	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if !strings.HasPrefix(line, ": subscribed to suite-a") {
		t.Errorf("first line: got %q, want subscription comment", line)
	}
}

func TestSSEEndpointStreamsBrokerEvents(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/runs/suite-a/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	// Skip the initial ": subscribed to suite-a\n\n" comment.
	_, _ = reader.ReadString('\n')
	_, _ = reader.ReadString('\n')

	// Give the handler a moment to register its subscription, then publish.
	time.Sleep(100 * time.Millisecond)
	srv.Broker().Publish("suite-a", Event{
		Type:    EventScenarioStart,
		Payload: ScenarioStartPayload{Name: "s1", Index: 0, Total: 1},
	})

	// Read the next event line.
	deadline := time.Now().Add(2 * time.Second)
	var sawEvent, sawData bool
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		if strings.HasPrefix(line, "event: scenario_start") {
			sawEvent = true
		}
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, "\"name\":\"s1\"") {
			sawData = true
		}
		if sawEvent && sawData {
			return
		}
	}
	if !sawEvent {
		t.Errorf("did not see 'event: scenario_start' line")
	}
	if !sawData {
		t.Errorf("did not see 'data:' line with payload")
	}
}
