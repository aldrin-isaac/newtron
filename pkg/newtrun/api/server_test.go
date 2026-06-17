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

// testTopology is the placeholder topology name every test fixture
// nests its suites under. The on-disk layout in tests mirrors the
// production layout exactly — <topologies-base>/<topology>/suites/<name>/
// — so the same ResolveSuiteDir glob handles both.
const testTopology = "test-topo"

// newTestServer builds a Server configured against a temporary directory.
// HOME is also overridden so newtrun.LoadRunState / SaveRunState route to
// the same temp directory. Tests address the suites root via
// suitesRoot(t, srv) so the per-topology layout stays an implementation
// detail of the resolver, not something every test has to assemble.
func newTestServer(t *testing.T) (*Server, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	topologiesBase := filepath.Join(tmpDir, "topologies")
	if err := os.MkdirAll(filepath.Join(topologiesBase, testTopology, "suites"), 0755); err != nil {
		t.Fatalf("mkdir suites root: %v", err)
	}
	t.Setenv("NEWTRUN_TOPOLOGIES_BASE", topologiesBase)

	srv := NewServer(Config{
		TopologiesBase: topologiesBase,
		Logger:         log.New(io.Discard, "", 0),
	})
	return srv, func() {}
}

// suitesRoot returns the per-topology suites directory the test fixture
// writes suites into. Equivalent to "where the production server would
// find suites under the configured topologies base, for the test
// topology." Helpers that build suites (writeMinimalSuite,
// newScenarioTestServer) join under this root.
func suitesRoot(srv *Server) string {
	return filepath.Join(srv.cfg.TopologiesBase, testTopology, "suites")
}

// seedSuite writes a state.json for one suite and creates the matching suite
// directory so ListSuiteStates returns it.
func seedSuite(t *testing.T, srv *Server, name string, status newtrun.SuiteStatus) {
	t.Helper()
	state := &newtrun.RunState{
		Suite:    name,
		Topology: testTopology,
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
	if err := os.MkdirAll(filepath.Join(suitesRoot(srv), name), 0755); err != nil {
		t.Fatalf("mkdir suite dir(%s): %v", name, err)
	}
}

func TestHealth(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/newtrun/v1/health")
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
	seedSuite(t, srv, "suite-a", newtrun.SuiteStatusComplete)
	seedSuite(t, srv, "suite-b", newtrun.SuiteStatusRunning)

	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/newtrun/v1/runs")
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
	seedSuite(t, srv, "suite-a", newtrun.SuiteStatusComplete)

	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/newtrun/v1/runs/suite-a")
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

	resp, err := http.Get(ts.URL + "/newtrun/v1/runs/nonexistent")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestSSEEndpointOpensConnectionAndSendsSubscribedComment(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/newtrun/v1/runs/suite-a/events", nil)
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
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/newtrun/v1/runs/suite-a/events", nil)
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
	srv.broker.Publish("suite-a", Event{
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
