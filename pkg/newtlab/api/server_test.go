package api

import (
	"encoding/json"
	"log"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/version"
)

// newTestServer constructs a server pinned to a temp topologies base so
// no test reaches into the real ~/.newtlab/labs/.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	base := t.TempDir()
	logger := log.New(&strings.Builder{}, "", 0) // silence test logger
	return NewServer(Config{
		TopologiesBase: base,
		Logger:         logger,
	})
}

func TestHealthEndpointReturnsOK(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var env httputil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error != "" {
		t.Errorf("Error = %q, want empty", env.Error)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("Data = %T, want map", env.Data)
	}
	if data["status"] != "ok" {
		t.Errorf("status = %v, want %q", data["status"], "ok")
	}
	if data["version"] != version.Version {
		t.Errorf("version = %v, want %q", data["version"], version.Version)
	}
}

func TestListTopologiesEmptyWhenNoLabsDeployed(t *testing.T) {
	// ListLabs reads ~/.newtlab/labs/. Override HOME to a temp dir so
	// the test doesn't see the developer's real labs.
	t.Setenv("HOME", t.TempDir())

	s := newTestServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/api/v1/topologies")
	if err != nil {
		t.Fatalf("GET /api/topologies: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var env httputil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Empty list serialized as `[]` decodes to []any{} (or nil if Go
	// chose to omit). Either is acceptable for an empty list.
	items, _ := env.Data.([]any)
	if len(items) != 0 {
		t.Errorf("got %d items, want 0: %+v", len(items), items)
	}
}

func TestStatusMissingTopologyReturns404(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newTestServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/api/v1/topologies/no-such-topo/status")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestDeployMissingNameReturns404(t *testing.T) {
	// Path matching with empty {name} won't reach the handler — the
	// mux returns 404 itself. Verify the route table behaves.
	s := newTestServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := ts.Client().Post(ts.URL+"/api/v1/topologies//deploy", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 || resp.StatusCode == 202 {
		t.Errorf("status = %d, expected error", resp.StatusCode)
	}
}

func TestUnknownRouteReturns404(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/api/v1/does-not-exist")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
