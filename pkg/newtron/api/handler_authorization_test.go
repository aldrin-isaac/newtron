package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// authzServeGet registers a network from specDir under id "default"
// on a fresh NewServer(Config{}) (the unauth surface — operator
// owns the perimeter), then performs the GET. The inspector
// endpoint is itself a spec.* read, ungated like every other
// /networks GET, so no --enforce-authorization is needed.
func authzServeGet(t *testing.T, specDir, path string) *httptest.ResponseRecorder {
	t.Helper()
	srv := NewServer(Config{})
	if err := srv.RegisterNetwork("default", specDir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

// TestGetAuthorization_ReturnsTable: the happy-path read.
//
// Setup: scaffoldWithPermissions (shared with authorization_test.go)
// writes a network.json whose authorization table has one user_group
// ("spec-team": ["alice"]), five permission entries (all shorthand:
// ["spec-team"]), and one super_user ("root"). GET /authorization
// MUST return that exact table.
//
// §16: the docstring claim is "the endpoint exposes the live
// authorization table." The setup writes the table to disk; the
// assertion reads it back through the endpoint. The transport
// path is real (httptest + mux). The wire shape is real
// (PermissionGrants.MarshalJSON). Nothing is faked.
func TestGetAuthorization_ReturnsTable(t *testing.T) {
	specDir := scaffoldWithPermissions(t)
	w := authzServeGet(t, specDir, "/newtron/v1/networks/default/authorization")

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var envelope struct {
		Data struct {
			UserGroups  map[string][]string        `json:"user_groups"`
			Permissions map[string]json.RawMessage `json:"permissions"`
			SuperUsers  []string                   `json:"super_users"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	got := envelope.Data

	if want := []string{"alice"}; !equalStringSlice(got.UserGroups["spec-team"], want) {
		t.Errorf("user_groups[spec-team]: got %v, want %v", got.UserGroups["spec-team"], want)
	}
	if want := []string{"root"}; !equalStringSlice(got.SuperUsers, want) {
		t.Errorf("super_users: got %v, want %v", got.SuperUsers, want)
	}
	for _, key := range []string{"spec.author", "qos.create", "qos.delete", "filter.create", "filter.delete"} {
		if _, ok := got.Permissions[key]; !ok {
			t.Errorf("permissions[%q]: missing", key)
		}
	}
}

// TestGetAuthorization_EmptyNetwork: a scaffolded but otherwise
// untouched network has no permissions, no groups, no super_users.
// The endpoint must succeed and return the zero-valued table —
// not 500, not "not configured" — because the absence of grants
// IS the authorization state (and is exactly what L3 enforcement
// sees on this network: everything denied except super_users,
// which is also empty).
func TestGetAuthorization_EmptyNetwork(t *testing.T) {
	dir := t.TempDir()
	if err := spec.CreateEmpty(dir, "empty-network test fixture"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	w := authzServeGet(t, dir, "/newtron/v1/networks/default/authorization")

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var envelope struct {
		Data struct {
			UserGroups  map[string][]string        `json:"user_groups"`
			Permissions map[string]json.RawMessage `json:"permissions"`
			SuperUsers  []string                   `json:"super_users"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	got := envelope.Data
	if len(got.UserGroups) != 0 {
		t.Errorf("user_groups: got %v, want empty", got.UserGroups)
	}
	if len(got.Permissions) != 0 {
		t.Errorf("permissions: got %v, want empty", got.Permissions)
	}
	if len(got.SuperUsers) != 0 {
		t.Errorf("super_users: got %v, want empty", got.SuperUsers)
	}
}

// TestGetAuthorization_UnknownNetwork_404: requireNetwork answers
// 404 when the netID isn't registered, like every other /networks
// read endpoint. Records the existing behavior so a future change
// to requireNetwork can't silently regress the inspector to 500.
func TestGetAuthorization_UnknownNetwork_404(t *testing.T) {
	dir := t.TempDir()
	if err := spec.CreateEmpty(dir, "unknown-network test fixture"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	w := authzServeGet(t, dir, "/newtron/v1/networks/missing/authorization")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

// TestGetAuthorization_WireForm_ShorthandVsTyped pins the wire
// shape PermissionGrants.MarshalJSON produces:
//   - a grant with no scope (empty Where) emits as a bare string
//     in a JSON array: ["spec-team"]
//   - a grant with a scope (non-empty Where) emits as an object:
//     [{"groups":[...],"where":{...}}]
//
// The endpoint is the operator-facing inspector for the auth
// table; the wire shape it emits must equal the form an operator
// would author by hand. This test catches a regression where a
// future change to PermissionGrants.MarshalJSON (or a wrapper
// here that ignores it) would distort what an inspector displays.
func TestGetAuthorization_WireForm_ShorthandVsTyped(t *testing.T) {
	dir := t.TempDir()
	if err := spec.CreateEmpty(dir, "wire-form test fixture"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	// Rewrite network.json with one shorthand grant and one typed
	// grant. The shorthand entry has no Where — the simplest form
	// an operator writes. The typed entry constrains to a specific
	// device — the L5 form.
	netJSON := `{
  "version": "1.0",
  "super_users": [],
  "user_groups": {"net-admins": ["alice"], "edge-admins": ["bob"]},
  "permissions": {
    "spec.author": ["net-admins"],
    "node.vlan.create": [
      {"groups": ["edge-admins"], "where": {"device": "switch1"}}
    ]
  },
  "services": {}
}`
	if err := os.WriteFile(filepath.Join(dir, "network.json"), []byte(netJSON), 0o644); err != nil {
		t.Fatalf("write network.json: %v", err)
	}
	writeZoneFile(t, dir, "amer")

	w := authzServeGet(t, dir, "/newtron/v1/networks/default/authorization")

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var envelope struct {
		Data struct {
			Permissions map[string]json.RawMessage `json:"permissions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	got := envelope.Data

	shorthand := string(got.Permissions["spec.author"])
	if shorthand != `["net-admins"]` {
		t.Errorf("spec.author wire form: got %s, want [\"net-admins\"] (shorthand for empty-Where grant)", shorthand)
	}
	typed := string(got.Permissions["node.vlan.create"])
	var typedEntries []map[string]any
	if err := json.Unmarshal(got.Permissions["node.vlan.create"], &typedEntries); err != nil {
		t.Fatalf("typed entry decode: %v (raw: %s)", err, typed)
	}
	if len(typedEntries) != 1 {
		t.Fatalf("typed entries: got %d, want 1; raw: %s", len(typedEntries), typed)
	}
	if _, ok := typedEntries[0]["where"]; !ok {
		t.Errorf("typed entry missing 'where' (this is the whole point of the typed form); raw: %s", typed)
	}
}

// TestGetAuthorization_EngageWhenConfigured_Fallback pins the
// auth.read engage-when-configured contract:
//
//	1) --enforce-authorization=true ON
//	2) network.json has NO auth.read entry
//	3) Caller is mallory (no group, would be denied by any actual
//	   gate)
//
// The endpoint MUST still return 200 — the gate is in fallback mode
// because no operator has opted in. This preserves the
// zero-ceremony quickstart and existing deployments that took GET
// /authorization for granted as readable.
func TestGetAuthorization_EngageWhenConfigured_Fallback(t *testing.T) {
	specDir := scaffoldWithPermissions(t)
	s := NewServer(Config{
		AuditCallerHeader:    "X-Newtron-Caller",
		EnforceAuthorization: true,
	})
	if err := s.RegisterNetwork("default", specDir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/networks/default/authorization", nil)
	req.Header.Set("X-Newtron-Caller", "mallory")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (no auth.read entry → fallback ungated); body: %s", w.Code, w.Body.String())
	}
}

// TestGetAuthorization_EngageWhenConfigured_GateEngagesAndDenies
// pins the engaged-by-opt-in half of the contract. Same scaffold as
// above but the network.json carries an auth.read entry granting
// only "iam-team". mallory (no group) MUST 403; iam-ian (in
// iam-team) MUST 200; root (super_user) MUST 200 — super-users
// bypass auth.read like every other permission.
func TestGetAuthorization_EngageWhenConfigured_GateEngagesAndDenies(t *testing.T) {
	dir := t.TempDir()
	if err := spec.CreateEmpty(dir, "auth.read gate fires"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	// Rewrite network.json with an auth.read entry.
	netJSON := `{
  "version": "1.0",
  "super_users": ["root"],
  "user_groups": {"iam-team": ["iam-ian"]},
  "permissions": {
    "auth.read": ["iam-team"]
  },
  "services": {}
}`
	if err := os.WriteFile(filepath.Join(dir, "network.json"), []byte(netJSON), 0o644); err != nil {
		t.Fatalf("write network.json: %v", err)
	}
	writeZoneFile(t, dir, "amer")
	s := NewServer(Config{
		AuditCallerHeader:    "X-Newtron-Caller",
		EnforceAuthorization: true,
	})
	if err := s.RegisterNetwork("default", dir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	cases := []struct {
		caller   string
		wantCode int
		why      string
	}{
		{"mallory", http.StatusForbidden, "no group → no auth.read grant matches"},
		{"iam-ian", http.StatusOK, "iam-team granted auth.read"},
		{"root", http.StatusOK, "super_user bypass"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/newtron/v1/networks/default/authorization", nil)
		req.Header.Set("X-Newtron-Caller", tc.caller)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != tc.wantCode {
			t.Errorf("[%s] %s: got %d, want %d; body: %s", tc.caller, tc.why, w.Code, tc.wantCode, w.Body.String())
		}
	}
}

// equalStringSlice is the tiny helper the assertions above need.
// Empty and nil are equivalent — the endpoint may emit either for
// "no groups", and the assertion shouldn't care which.
func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
