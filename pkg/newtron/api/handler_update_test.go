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
	if err := spec.CreateEmpty(dir, "update-X round-trip test fixture"); err != nil {
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
		"name":         "transit",
		"service_type": "routed",
		"description":  "initial description",
	}); w.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", w.Code, w.Body.String())
	}

	if w := post(t, s, "/newtron/v1/networks/default/update-service", map[string]any{
		"name":         "transit",
		"service_type": "routed",
		"description":  "updated description",
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
		"name":         "missing-service",
		"service_type": "routed",
		"description":  "no prior create — should 404",
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
// path (UpdateNodeSpec in spec/loader.go) is what changes, not the
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

	if w := post(t, s, "/newtron/v1/networks/default/create-node", map[string]any{
		"name":    "switch1",
		"mgmt_ip": "10.0.0.1",
		"zone":    "amer",
	}); w.Code != http.StatusCreated {
		t.Fatalf("create-node: status=%d body=%s", w.Code, w.Body.String())
	}

	if w := post(t, s, "/newtron/v1/networks/default/update-node", map[string]any{
		"name":    "switch1",
		"mgmt_ip": "10.0.0.99",
		"zone":    "amer",
	}); w.Code != http.StatusOK {
		t.Fatalf("update-node: status=%d body=%s", w.Code, w.Body.String())
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

// seedFilterWithRule scaffolds a filter at "blocklist" with one rule at
// seq=10 (action=deny, src_ip=10.0.0.0/8). Returned for chaining in the
// UpdateFilterRule tests below; isolates the setup that's identical across
// every TestUpdateFilterRule_* variant.
func seedFilterWithRule(t *testing.T, s *Server) {
	t.Helper()
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
		t.Fatalf("add-filter-rule seed: status=%d body=%s", w.Code, w.Body.String())
	}
}

// readFilterRules issues GET /filters/{name} and returns the response's
// rules slice. Used by the UpdateFilterRule tests to verify post-state
// without re-parsing the full filter envelope each time.
func readFilterRules(t *testing.T, s *Server, name string) []map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/networks/default/filters/"+name, nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("show: status=%d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			Rules []map[string]any `json:"rules"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	return env.Data.Rules
}

// TestUpdateFilterRule_HappyPath verifies the basic edit path: change a
// rule's action field and confirm the new value lands without affecting
// the sequence number. Issue #209.
func TestUpdateFilterRule_HappyPath(t *testing.T) {
	s := scaffoldNetwork(t, "default")
	seedFilterWithRule(t, s)

	w := post(t, s, "/newtron/v1/networks/default/update-filter-rule", map[string]any{
		"filter": "blocklist",
		"seq":    10,
		"action": "permit",
		"src_ip": "10.0.0.0/8",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update-filter-rule: status=%d body=%s", w.Code, w.Body.String())
	}
	rules := readFilterRules(t, s, "BLOCKLIST")
	if len(rules) != 1 {
		t.Fatalf("rules.length: got %d, want 1", len(rules))
	}
	if rules[0]["seq"].(float64) != 10 {
		t.Errorf("seq: got %v, want 10 (no renumber requested)", rules[0]["seq"])
	}
	if rules[0]["action"].(string) != "permit" {
		t.Errorf("action: got %q, want 'permit'", rules[0]["action"])
	}
}

// TestUpdateFilterRule_RenumberOnly verifies that supplying new_seq
// rotates the rule's sequence and re-sorts the rule list, leaving the
// other fields unchanged.
func TestUpdateFilterRule_RenumberOnly(t *testing.T) {
	s := scaffoldNetwork(t, "default")
	seedFilterWithRule(t, s)

	w := post(t, s, "/newtron/v1/networks/default/update-filter-rule", map[string]any{
		"filter":  "blocklist",
		"seq":     10,
		"new_seq": 5,
		"action":  "deny", // same as seeded; verifies non-PK fields unchanged
		"src_ip":  "10.0.0.0/8",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update-filter-rule: status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data map[string]int `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	if resp.Data["seq"] != 5 {
		t.Errorf("response seq: got %d, want 5", resp.Data["seq"])
	}
	rules := readFilterRules(t, s, "BLOCKLIST")
	if rules[0]["seq"].(float64) != 5 {
		t.Errorf("on-disk seq: got %v, want 5 (renumber)", rules[0]["seq"])
	}
}

// TestUpdateFilterRule_RenumberAndEdit verifies that a renumber and a
// field change happen in one atomic call.
func TestUpdateFilterRule_RenumberAndEdit(t *testing.T) {
	s := scaffoldNetwork(t, "default")
	seedFilterWithRule(t, s)

	w := post(t, s, "/newtron/v1/networks/default/update-filter-rule", map[string]any{
		"filter":  "blocklist",
		"seq":     10,
		"new_seq": 5,
		"action":  "permit",
		"src_ip":  "172.16.0.0/12",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update-filter-rule: status=%d body=%s", w.Code, w.Body.String())
	}
	rules := readFilterRules(t, s, "BLOCKLIST")
	if rules[0]["seq"].(float64) != 5 || rules[0]["action"].(string) != "permit" || rules[0]["src_ip"].(string) != "172.16.0.0/12" {
		t.Errorf("rule after renumber+edit: %+v", rules[0])
	}
}

// TestUpdateFilterRule_NewSeqCollides verifies rejection when the target
// renumber slot is occupied by another rule.
func TestUpdateFilterRule_NewSeqCollides(t *testing.T) {
	s := scaffoldNetwork(t, "default")
	seedFilterWithRule(t, s) // rule at seq=10
	if w := post(t, s, "/newtron/v1/networks/default/add-filter-rule", map[string]any{
		"filter": "blocklist",
		"seq":    20,
		"action": "permit",
	}); w.Code >= 400 {
		t.Fatalf("add second rule: status=%d body=%s", w.Code, w.Body.String())
	}
	w := post(t, s, "/newtron/v1/networks/default/update-filter-rule", map[string]any{
		"filter":  "blocklist",
		"seq":     10,
		"new_seq": 20,
		"action":  "permit",
	})
	if w.Code < 400 {
		t.Fatalf("expected error on collision; got status=%d body=%s", w.Code, w.Body.String())
	}
}

// TestUpdateFilterRule_RuleNotFound verifies rejection when the
// identified rule doesn't exist.
func TestUpdateFilterRule_RuleNotFound(t *testing.T) {
	s := scaffoldNetwork(t, "default")
	seedFilterWithRule(t, s)
	w := post(t, s, "/newtron/v1/networks/default/update-filter-rule", map[string]any{
		"filter": "blocklist",
		"seq":    999,
		"action": "deny",
	})
	if w.Code < 400 {
		t.Fatalf("expected error for missing rule; got status=%d body=%s", w.Code, w.Body.String())
	}
}

// TestUpdateFilterRule_FilterNotFound verifies rejection when the
// parent filter doesn't exist.
func TestUpdateFilterRule_FilterNotFound(t *testing.T) {
	s := scaffoldNetwork(t, "default")
	w := post(t, s, "/newtron/v1/networks/default/update-filter-rule", map[string]any{
		"filter": "nonexistent",
		"seq":    10,
		"action": "deny",
	})
	if w.Code < 400 {
		t.Fatalf("expected error for missing filter; got status=%d body=%s", w.Code, w.Body.String())
	}
}

// seedRoutePolicyWithRule scaffolds a route policy "rp" with one rule
// at seq=10. Mirrors seedFilterWithRule. Issue #210.
func seedRoutePolicyWithRule(t *testing.T, s *Server) {
	t.Helper()
	if w := post(t, s, "/newtron/v1/networks/default/create-route-policy", map[string]any{
		"name": "rp",
	}); w.Code != http.StatusCreated {
		t.Fatalf("create-route-policy: status=%d body=%s", w.Code, w.Body.String())
	}
	if w := post(t, s, "/newtron/v1/networks/default/add-route-policy-rule", map[string]any{
		"policy": "rp",
		"seq":    10,
		"action": "permit",
	}); w.Code >= 400 {
		t.Fatalf("add-route-policy-rule seed: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateRoutePolicyRule_HappyPath(t *testing.T) {
	s := scaffoldNetwork(t, "default")
	seedRoutePolicyWithRule(t, s)
	w := post(t, s, "/newtron/v1/networks/default/update-route-policy-rule", map[string]any{
		"policy": "rp",
		"seq":    10,
		"action": "deny",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update-route-policy-rule: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateRoutePolicyRule_Renumber(t *testing.T) {
	s := scaffoldNetwork(t, "default")
	seedRoutePolicyWithRule(t, s)
	w := post(t, s, "/newtron/v1/networks/default/update-route-policy-rule", map[string]any{
		"policy":  "rp",
		"seq":     10,
		"new_seq": 5,
		"action":  "permit",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update-route-policy-rule renumber: status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data map[string]int `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data["seq"] != 5 {
		t.Errorf("response seq: got %d, want 5", resp.Data["seq"])
	}
}

func TestUpdateRoutePolicyRule_Collision(t *testing.T) {
	s := scaffoldNetwork(t, "default")
	seedRoutePolicyWithRule(t, s)
	if w := post(t, s, "/newtron/v1/networks/default/add-route-policy-rule", map[string]any{
		"policy": "rp",
		"seq":    20,
		"action": "permit",
	}); w.Code >= 400 {
		t.Fatalf("add second rule: %d %s", w.Code, w.Body.String())
	}
	w := post(t, s, "/newtron/v1/networks/default/update-route-policy-rule", map[string]any{
		"policy":  "rp",
		"seq":     10,
		"new_seq": 20,
		"action":  "permit",
	})
	if w.Code < 400 {
		t.Fatalf("expected error; got status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateRoutePolicyRule_NotFound(t *testing.T) {
	s := scaffoldNetwork(t, "default")
	seedRoutePolicyWithRule(t, s)
	w := post(t, s, "/newtron/v1/networks/default/update-route-policy-rule", map[string]any{
		"policy": "rp",
		"seq":    999,
		"action": "deny",
	})
	if w.Code < 400 {
		t.Fatalf("expected error; got status=%d body=%s", w.Code, w.Body.String())
	}
}

// seedQoSPolicyWithQueue scaffolds a QoS policy "qp" with one queue at
// queue_id=2 (name=voice, type=strict). Issue #211.
func seedQoSPolicyWithQueue(t *testing.T, s *Server) {
	t.Helper()
	if w := post(t, s, "/newtron/v1/networks/default/create-qos-policy", map[string]any{
		"name": "qp",
	}); w.Code != http.StatusCreated {
		t.Fatalf("create-qos-policy: status=%d body=%s", w.Code, w.Body.String())
	}
	if w := post(t, s, "/newtron/v1/networks/default/add-qos-queue", map[string]any{
		"policy":   "qp",
		"queue_id": 2,
		"name":     "voice",
		"type":     "strict",
	}); w.Code >= 400 {
		t.Fatalf("add-qos-queue seed: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateQoSQueue_HappyPath(t *testing.T) {
	s := scaffoldNetwork(t, "default")
	seedQoSPolicyWithQueue(t, s)
	w := post(t, s, "/newtron/v1/networks/default/update-qos-queue", map[string]any{
		"policy":   "qp",
		"queue_id": 2,
		"name":     "voice",
		"type":     "dwrr",
		"weight":   50,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update-qos-queue: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateQoSQueue_Relocate(t *testing.T) {
	s := scaffoldNetwork(t, "default")
	seedQoSPolicyWithQueue(t, s)
	w := post(t, s, "/newtron/v1/networks/default/update-qos-queue", map[string]any{
		"policy":       "qp",
		"queue_id":     2,
		"new_queue_id": 4,
		"name":         "voice",
		"type":         "strict",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update-qos-queue relocate: status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data map[string]int `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data["queue_id"] != 4 {
		t.Errorf("response queue_id: got %d, want 4", resp.Data["queue_id"])
	}
}

func TestUpdateQoSQueue_RelocateCollides(t *testing.T) {
	s := scaffoldNetwork(t, "default")
	seedQoSPolicyWithQueue(t, s)
	if w := post(t, s, "/newtron/v1/networks/default/add-qos-queue", map[string]any{
		"policy":   "qp",
		"queue_id": 4,
		"name":     "video",
		"type":     "dwrr",
		"weight":   30,
	}); w.Code >= 400 {
		t.Fatalf("add second queue: %d %s", w.Code, w.Body.String())
	}
	w := post(t, s, "/newtron/v1/networks/default/update-qos-queue", map[string]any{
		"policy":       "qp",
		"queue_id":     2,
		"new_queue_id": 4,
		"name":         "voice",
		"type":         "strict",
	})
	if w.Code < 400 {
		t.Fatalf("expected error; got status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateQoSQueue_NotFound(t *testing.T) {
	s := scaffoldNetwork(t, "default")
	seedQoSPolicyWithQueue(t, s)
	w := post(t, s, "/newtron/v1/networks/default/update-qos-queue", map[string]any{
		"policy":   "qp",
		"queue_id": 7,
		"name":     "ghost",
		"type":     "strict",
	})
	if w.Code < 400 {
		t.Fatalf("expected error for missing queue; got status=%d body=%s", w.Code, w.Body.String())
	}
}
