package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// httpPostJSON drives a POST request with a JSON body against the server's
// handler. Mirrors httpDo (api_test.go) but for write paths.
func httpPostJSON(t *testing.T, s *Server, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.HTTPServer().Handler.ServeHTTP(w, req)
	return w
}

// loadTopologyJSON reads topology.json from a network dir and parses the steps
// for one device. Returns the count of steps that match the given URL — the
// hook test asserts on this rather than on a full equality check so an
// unrelated step ordering change in 1node-vs doesn't break the test.
func loadTopologyJSON(t *testing.T, specDir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(specDir, "topology.json"))
	if err != nil {
		t.Fatalf("read topology.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse topology.json: %v", err)
	}
	return m
}

func deviceSteps(t *testing.T, topo map[string]any, device string) []any {
	t.Helper()
	devs, ok := topo["nodes"].(map[string]any)
	if !ok {
		t.Fatal("topology.nodes missing")
	}
	dev, ok := devs[device].(map[string]any)
	if !ok {
		t.Fatalf("topology.nodes.%s missing", device)
	}
	steps, _ := dev["steps"].([]any)
	return steps
}

func stepURLCount(steps []any, url string) int {
	n := 0
	for _, s := range steps {
		if m, ok := s.(map[string]any); ok {
			if u, _ := m["url"].(string); u == url {
				n++
			}
		}
	}
	return n
}

// newPersistTestServer mirrors newTestServer but registers the network from
// a writable copy of the network dir so persist=topology can actually rewrite
// topology.json without polluting the lab spec.
func newPersistTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	specDir := copyTestSpecDir(t)
	s := NewServer(Config{})
	if err := s.RegisterNetwork("default", specDir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(t.Context()) })
	return s, specDir
}

// TestPersistHook_TopologyJSONReflectsWriteWhenRequested is the happy-path
// test for issue #75C: a write under ?persist=topology lands in topology.json
// before the response returns.
func TestPersistHook_TopologyJSONReflectsWriteWhenRequested(t *testing.T) {
	s, specDir := newPersistTestServer(t)

	before := stepURLCount(deviceSteps(t, loadTopologyJSON(t, specDir), "switch1"), "/create-vlan")

	w := httpPostJSON(t, s,
		"/newtron/v1/networks/default/nodes/switch1/create-vlan?mode=topology&persist=topology",
		map[string]any{"id": 991, "description": "persist hook test"})
	if w.Code != http.StatusCreated {
		t.Fatalf("POST: got %d, want 201; body: %s", w.Code, w.Body.String())
	}

	after := stepURLCount(deviceSteps(t, loadTopologyJSON(t, specDir), "switch1"), "/create-vlan")
	if after != before+1 {
		t.Errorf("create-vlan step count: got %d, want %d (before=%d)", after, before+1, before)
	}
}

// TestPersistHook_TopologyJSONUntouchedWhenAbsent is the negative-path test:
// the same write without ?persist=topology must NOT rewrite topology.json.
// Guards against the hook accidentally firing for every write. The primary
// assertion is the step count (file-content invariant); mtime is a
// secondary sanity check since coarse-resolution filesystems can give
// false negatives there.
func TestPersistHook_TopologyJSONUntouchedWhenAbsent(t *testing.T) {
	s, specDir := newPersistTestServer(t)

	beforeSteps := stepURLCount(deviceSteps(t, loadTopologyJSON(t, specDir), "switch1"), "/create-vlan")
	infoBefore, err := os.Stat(filepath.Join(specDir, "topology.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	w := httpPostJSON(t, s,
		"/newtron/v1/networks/default/nodes/switch1/create-vlan?mode=topology",
		map[string]any{"id": 992})
	if w.Code != http.StatusCreated {
		t.Fatalf("POST: got %d, want 201; body: %s", w.Code, w.Body.String())
	}

	// Primary assertion: the file content (step count) is unchanged. A
	// /create-vlan step appearing without persist=topology would prove
	// the hook leaked.
	afterSteps := stepURLCount(deviceSteps(t, loadTopologyJSON(t, specDir), "switch1"), "/create-vlan")
	if afterSteps != beforeSteps {
		t.Errorf("create-vlan step count changed without persist=topology: got %d, want %d",
			afterSteps, beforeSteps)
	}

	// Secondary check: mtime is unchanged. On coarse-resolution
	// filesystems this can pass even when a write happened, so step
	// count is authoritative — this just catches the cheap case where
	// SaveDeviceIntents reran and rewrote with sub-second resolution.
	infoAfter, err := os.Stat(filepath.Join(specDir, "topology.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !infoAfter.ModTime().Equal(infoBefore.ModTime()) {
		t.Logf("note: topology.json mtime changed (before=%v after=%v) — verify step count above is the real signal",
			infoBefore.ModTime(), infoAfter.ModTime())
	}
}

// TestPersistHook_ReadOnlyHandlerIsNoOp ensures ?persist=topology on a
// read-only path (here: /intent/projection) does not mutate topology.json.
// The hook is gated on HasUnsavedIntents, which read handlers never set —
// proves the data-driven design works.
func TestPersistHook_ReadOnlyHandlerIsNoOp(t *testing.T) {
	s, specDir := newPersistTestServer(t)
	infoBefore, err := os.Stat(filepath.Join(specDir, "topology.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	w := httpDo(t, s, http.MethodGet,
		"/newtron/v1/networks/default/nodes/switch1/intent/projection?mode=topology&persist=topology",
	)
	if w.Code != http.StatusOK {
		t.Fatalf("GET: got %d, want 200; body: %s", w.Code, w.Body.String())
	}

	infoAfter, err := os.Stat(filepath.Join(specDir, "topology.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !infoAfter.ModTime().Equal(infoBefore.ModTime()) {
		t.Errorf("topology.json mtime changed for a read-only handler")
	}
}

// TestPersistHook_FromCtxDefaultsToNone is a unit test for the parsing
// edge: an unrecognized persist value falls through to PersistNone — the
// safe default. Future addition of a new persist mode shouldn't accidentally
// pick the wrong default.
func TestPersistHook_FromCtxDefaultsToNone(t *testing.T) {
	cases := []struct {
		q    string
		want PersistMode
	}{
		{"", PersistNone},
		{"topology", PersistTopology},
		{"bogus", PersistNone},
		{"TOPOLOGY", PersistNone}, // case-sensitive
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/x?persist="+tc.q, nil)
		var got PersistMode
		withPersist(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = persistFromCtx(r.Context())
		})).ServeHTTP(httptest.NewRecorder(), req)
		if got != tc.want {
			t.Errorf("persist=%q: got %q, want %q", tc.q, got, tc.want)
		}
	}
}
