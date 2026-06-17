package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// scaffoldNetwork registers a fresh network with the given id at a
// scaffolded network dir. Returns the server. Used by the #152 update-X
// round-trip tests below — each test creates an entry, updates a
// field, and reads it back through the HTTP surface.
func scaffoldNetwork(t *testing.T, netID string) *Server {
	t.Helper()
	dir := t.TempDir()
	if err := spec.Scaffold(dir, "update-X round-trip test fixture"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	s := NewServer(Config{})
	if err := s.RegisterNetwork(netID, dir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	return s
}

// post is the test-local POST helper. No identity; auth not enforced
// in these tests (verified separately by the existing
// TestAuthorizationL4_* tables which include the new Update verbs via
// TestAPICompleteness's authorizedMethods entries).
func post(t *testing.T, s *Server, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

// TestUpdateService_RoundTrip exercises the full create → update →
// show path: POST /create-service stores a service; POST
// /update-service rewrites a field on it; GET /services/{name}
// returns the new value. The assertion targets a specific field
// (description) so a regression that silently drops Update's effect
// would be caught — §16 honest-tests requires the change be
// observable, not implied.
func TestUpdateService_RoundTrip(t *testing.T) {
	s := scaffoldNetwork(t, "default")

	if w := post(t, s, "/newtron/v1/networks/default/create-service", map[string]any{
		"name":        "transit",
		"type":        "routed",
		"description": "initial description",
	}); w.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", w.Code, w.Body.String())
	}

	if w := post(t, s, "/newtron/v1/networks/default/update-service", map[string]any{
		"name":        "transit",
		"type":        "routed",
		"description": "updated description",
	}); w.Code != http.StatusOK {
		t.Fatalf("update: status=%d body=%s", w.Code, w.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/networks/default/services/transit", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("show: status=%d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			Description string `json:"description"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	if env.Data.Description != "updated description" {
		t.Errorf("description: got %q, want %q — Update did not take effect", env.Data.Description, "updated description")
	}
}

// TestUpdateService_NotFound pins the 404 contract: POST
// /update-service against an unknown name returns NotFoundError →
// 404. Without the existence check in network.Network.UpdateService
// the call would silently store the entry like a Create — that
// would erode the Update/Create distinction and break operator
// intuition about which verb does what.
func TestUpdateService_NotFound(t *testing.T) {
	s := scaffoldNetwork(t, "default")

	w := post(t, s, "/newtron/v1/networks/default/update-service", map[string]any{
		"name":        "missing-service",
		"type":        "routed",
		"description": "no prior create — should 404",
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404; body=%s", w.Code, w.Body.String())
	}
}

// TestUpdateFilter_PreservesRules pins the sub-collection
// preservation semantic for the three kinds whose Create request
// doesn't transport sub-rules: Filter, RoutePolicy, QoSPolicy. An
// operator who Updates a filter's metadata (description, type)
// MUST NOT silently lose the rules they added separately via
// AddFilterRule. The wrapper reads the existing Filter's Rules and
// stitches them onto the replacement spec before persisting.
//
// Without this preservation, the full-replacement internal Update
// would store {Description, Type, Rules: nil}. The test would catch
// the regression by asserting Rules.length after update.
func TestUpdateFilter_PreservesRules(t *testing.T) {
	s := scaffoldNetwork(t, "default")

	if w := post(t, s, "/newtron/v1/networks/default/create-filter", map[string]any{
		"name": "blocklist",
		"type": "ipv4",
	}); w.Code != http.StatusCreated {
		t.Fatalf("create-filter: status=%d body=%s", w.Code, w.Body.String())
	}

	if w := post(t, s, "/newtron/v1/networks/default/add-filter-rule", map[string]any{
		"filter": "blocklist",
		"seq":    10,
		"action": "deny",
		"src_ip": "10.0.0.0/8",
	}); w.Code >= 400 {
		t.Fatalf("add-filter-rule: status=%d body=%s", w.Code, w.Body.String())
	}

	if w := post(t, s, "/newtron/v1/networks/default/update-filter", map[string]any{
		"name":        "blocklist",
		"type":        "ipv4",
		"description": "updated description preserves rules",
	}); w.Code != http.StatusOK {
		t.Fatalf("update-filter: status=%d body=%s", w.Code, w.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/networks/default/filters/BLOCKLIST", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("show: status=%d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			Description string                   `json:"description"`
			Rules       []map[string]interface{} `json:"rules"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	if env.Data.Description != "updated description preserves rules" {
		t.Errorf("description: got %q, want updated", env.Data.Description)
	}
	if len(env.Data.Rules) != 1 {
		t.Errorf("rules.length: got %d, want 1 — UpdateFilter dropped the rule", len(env.Data.Rules))
	}
}

// TestUpdateProfile_RoundTrip exercises the profile path
// specifically — Profile lives in nodes/<name>.json, distinct
// from the other spec kinds that live in network.json. The Loader
// path (UpdateProfile in spec/loader.go) is what changes, not the
// Network spec persist path. The test creates a profile, updates
// the mgmt_ip, and reads it back to confirm the new value landed
// on disk and in the loader cache.
func TestUpdateProfile_RoundTrip(t *testing.T) {
	s := scaffoldNetwork(t, "default")

	// Profile requires a zone to exist first.
	if w := post(t, s, "/newtron/v1/networks/default/create-zone", map[string]any{
		"name": "amer",
	}); w.Code != http.StatusCreated {
		t.Fatalf("create-zone: status=%d body=%s", w.Code, w.Body.String())
	}

	if w := post(t, s, "/newtron/v1/networks/default/create-profile", map[string]any{
		"name":    "switch1",
		"mgmt_ip": "10.0.0.1",
		"zone":    "amer",
	}); w.Code != http.StatusCreated {
		t.Fatalf("create-profile: status=%d body=%s", w.Code, w.Body.String())
	}

	if w := post(t, s, "/newtron/v1/networks/default/update-profile", map[string]any{
		"name":    "switch1",
		"mgmt_ip": "10.0.0.99",
		"zone":    "amer",
	}); w.Code != http.StatusOK {
		t.Fatalf("update-profile: status=%d body=%s", w.Code, w.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/networks/default/nodes/switch1", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("show: status=%d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			MgmtIP string `json:"mgmt_ip"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	if env.Data.MgmtIP != "10.0.0.99" {
		t.Errorf("mgmt_ip: got %q, want 10.0.0.99", env.Data.MgmtIP)
	}

	// Round-trip the on-disk file too: re-read the profile JSON and
	// confirm the new mgmt_ip landed atomically.
	specDir := s.networks["default"].specDir
	raw, err := os.ReadFile(filepath.Join(specDir, "nodes", "switch1.json"))
	if err != nil {
		t.Fatalf("read profile file: %v", err)
	}
	var onDisk map[string]any
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("unmarshal profile file: %v", err)
	}
	if mgmt, _ := onDisk["mgmt_ip"].(string); mgmt != "10.0.0.99" {
		t.Errorf("on-disk mgmt_ip: got %q, want 10.0.0.99", mgmt)
	}
}
