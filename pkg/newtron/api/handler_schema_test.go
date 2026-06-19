package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// TestSchemaList verifies the index endpoint returns every registered kind
// in stable alphabetical order, with the registered label and description
// flowing through to the wire shape. The test does NOT pin the exact set
// of kinds — that would couple a routing test to the schema registry's
// composition — but it asserts a representative subset is present.
func TestSchemaList(t *testing.T) {
	s := NewServer(Config{})

	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/schema", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var env struct {
		Data SchemaList `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v; body: %s", err, w.Body.String())
	}
	got := indexByKind(env.Data.Kinds)
	for _, want := range []string{"ServiceSpec", "QoSPolicy", "FilterSpec", "IPVPNSpec", "DeviceProfile"} {
		entry, ok := got[want]
		if !ok {
			t.Errorf("kind %q missing from /schema response", want)
			continue
		}
		if entry.Label == "" {
			t.Errorf("kind %q has empty label", want)
		}
	}
	// Alphabetical order is part of the contract — UIs sort against the
	// returned slice rather than re-sorting by their own rules.
	for i := 1; i < len(env.Data.Kinds); i++ {
		if env.Data.Kinds[i-1].Kind > env.Data.Kinds[i].Kind {
			t.Errorf("kinds out of order: %q before %q",
				env.Data.Kinds[i-1].Kind, env.Data.Kinds[i].Kind)
		}
	}
}

func indexByKind(kinds []SchemaListEntry) map[string]SchemaListEntry {
	out := make(map[string]SchemaListEntry, len(kinds))
	for _, k := range kinds {
		out[k.Kind] = k
	}
	return out
}

// TestSchemaShow_ServiceSpec exercises the canonical authoring path — the
// metadata for ServiceSpec must surface every annotated field, the
// service_type enum's full vocabulary, and ref-typed fields with their
// target kind. This pins the contract that UI consumers rely on.
func TestSchemaShow_ServiceSpec(t *testing.T) {
	s := NewServer(Config{})

	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/schema/ServiceSpec", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var env struct {
		Data spec.SchemaMeta `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	if env.Data.Kind != "ServiceSpec" || env.Data.Label != "Service" {
		t.Errorf("kind/label: got %q/%q", env.Data.Kind, env.Data.Label)
	}
	byName := make(map[string]spec.FieldMeta, len(env.Data.Fields))
	for _, f := range env.Data.Fields {
		byName[f.Name] = f
	}
	// service_type — enum with the full type vocabulary
	st, ok := byName["service_type"]
	if !ok {
		t.Fatal("service_type field missing")
	}
	if st.Type != "enum" {
		t.Errorf("service_type.type = %q, want enum", st.Type)
	}
	wantEnum := map[string]bool{"evpn-irb": true, "evpn-bridged": true, "evpn-routed": true, "irb": true, "bridged": true, "routed": true}
	for _, v := range st.Enum {
		delete(wantEnum, v)
	}
	if len(wantEnum) > 0 {
		t.Errorf("service_type.enum missing values: %v (got %v)", wantEnum, st.Enum)
	}
	// ipvpn — ref to IPVPNSpec
	ipvpn, ok := byName["ipvpn"]
	if !ok {
		t.Fatal("ipvpn field missing")
	}
	if ipvpn.Type != "ref" || ipvpn.RefKind != "IPVPNSpec" {
		t.Errorf("ipvpn: type=%q ref_kind=%q, want ref/IPVPNSpec", ipvpn.Type, ipvpn.RefKind)
	}
	if ipvpn.Required {
		t.Error("ipvpn should be optional (omitempty)")
	}
	// description — required, basic string with label
	desc, ok := byName["description"]
	if !ok {
		t.Fatal("description field missing")
	}
	if !desc.Required {
		t.Error("description should be required (no omitempty)")
	}
	if desc.Label == "" {
		t.Error("description label empty")
	}
}

// TestSchemaShow_TopLevelPaths pins the URL+identity metadata contract
// for top-level kinds — UIs depend on these to drive CRUD without
// hardcoded mappings (§27).
func TestSchemaShow_TopLevelPaths(t *testing.T) {
	s := NewServer(Config{})

	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/schema/IPVPNSpec", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var env struct {
		Data spec.SchemaMeta `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.Identifier != "name" {
		t.Errorf("identifier: got %q, want name", env.Data.Identifier)
	}
	if env.Data.ParentRef != "" {
		t.Errorf("parent_ref: got %q, want empty (top-level kind)", env.Data.ParentRef)
	}
	want := spec.SchemaPaths{
		List:   "/newtron/v1/networks/{netID}/ipvpns",
		Show:   "/newtron/v1/networks/{netID}/ipvpns/{name}",
		Create: "/newtron/v1/networks/{netID}/create-ipvpn",
		Update: "/newtron/v1/networks/{netID}/update-ipvpn",
		Delete: "/newtron/v1/networks/{netID}/delete-ipvpn",
	}
	if env.Data.Paths != want {
		t.Errorf("paths: got %+v, want %+v", env.Data.Paths, want)
	}
	// Top-level kinds get a synthetic `name` field prepended; it must
	// carry the immutable flag and pattern.
	if len(env.Data.Fields) == 0 || env.Data.Fields[0].Name != "name" {
		t.Fatalf("synthetic name field missing: %+v", env.Data.Fields)
	}
	if !env.Data.Fields[0].Immutable {
		t.Error("synthetic name field should be immutable")
	}
	if env.Data.Fields[0].Pattern == "" {
		t.Error("synthetic name field should carry a pattern")
	}
}

// TestSchemaShow_SubrulePaths pins the sub-rule contract: ParentRef
// declares the body field, paths use add/update/remove verbs, no
// List/Show (sub-rules aren't top-level addressable).
func TestSchemaShow_SubrulePaths(t *testing.T) {
	s := NewServer(Config{})

	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/schema/FilterRule", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var env struct {
		Data spec.SchemaMeta `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.Identifier != "seq" {
		t.Errorf("identifier: got %q, want seq", env.Data.Identifier)
	}
	if env.Data.ParentRef != "filter" {
		t.Errorf("parent_ref: got %q, want filter", env.Data.ParentRef)
	}
	if env.Data.Paths.List != "" || env.Data.Paths.Show != "" {
		t.Errorf("sub-rule should have no list/show paths: %+v", env.Data.Paths)
	}
	if env.Data.Paths.Create != "/newtron/v1/networks/{netID}/add-filter-rule" {
		t.Errorf("create path: %q", env.Data.Paths.Create)
	}
	if env.Data.Paths.Delete != "/newtron/v1/networks/{netID}/remove-filter-rule" {
		t.Errorf("delete path: %q", env.Data.Paths.Delete)
	}
}

// TestSchemaShow_QoSQueueSyntheticIdentifier verifies that QoSQueue's
// queue_id (which is the array index in QoSPolicy.Queues — implicit, not
// a struct field) is synthesized into the form field list with the
// correct min/max/immutable annotations.
func TestSchemaShow_QoSQueueSyntheticIdentifier(t *testing.T) {
	s := NewServer(Config{})

	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/schema/QoSQueue", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var env struct {
		Data spec.SchemaMeta `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.Identifier != "queue_id" {
		t.Errorf("identifier: got %q, want queue_id", env.Data.Identifier)
	}
	if env.Data.ParentRef != "policy" {
		t.Errorf("parent_ref: got %q, want policy", env.Data.ParentRef)
	}
	// queue_id is the first field (synthetic, prepended).
	if len(env.Data.Fields) == 0 || env.Data.Fields[0].Name != "queue_id" {
		t.Fatalf("synthetic queue_id field missing: %+v", env.Data.Fields)
	}
	qid := env.Data.Fields[0]
	if !qid.Immutable {
		t.Error("queue_id should be immutable")
	}
	if qid.Min == nil || *qid.Min != 0 {
		t.Errorf("queue_id.min: got %v, want 0", qid.Min)
	}
	if qid.Max == nil || *qid.Max != 7 {
		t.Errorf("queue_id.max: got %v, want 7", qid.Max)
	}
}

// TestSchemaShow_PlatformReadOnly verifies that PlatformSpec exposes
// only list+show paths (no create/update/delete).
func TestSchemaShow_PlatformReadOnly(t *testing.T) {
	s := NewServer(Config{})

	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/schema/PlatformSpec", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var env struct {
		Data spec.SchemaMeta `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.Paths.List == "" || env.Data.Paths.Show == "" {
		t.Errorf("platform list/show should be set: %+v", env.Data.Paths)
	}
	if env.Data.Paths.Create != "" || env.Data.Paths.Update != "" || env.Data.Paths.Delete != "" {
		t.Errorf("platform should be read-only — got create/update/delete: %+v", env.Data.Paths)
	}
}

func TestSchemaShow_NotFound(t *testing.T) {
	s := NewServer(Config{})

	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/schema/NoSuchKind", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", w.Code, w.Body.String())
	}
}

// TestSchemaShow_QoSQueue pins the enum + required-from-no-omitempty
// contract on a second kind, so a regression that breaks one kind without
// breaking ServiceSpec still trips a test.
func TestSchemaShow_QoSQueue(t *testing.T) {
	s := NewServer(Config{})

	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/schema/QoSQueue", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var env struct {
		Data spec.SchemaMeta `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byName := make(map[string]spec.FieldMeta, len(env.Data.Fields))
	for _, f := range env.Data.Fields {
		byName[f.Name] = f
	}
	if t1, ok := byName["type"]; !ok {
		t.Error("type field missing")
	} else {
		if t1.Type != "enum" {
			t.Errorf("type.type = %q, want enum", t1.Type)
		}
		if len(t1.Enum) != 2 || t1.Enum[0] != "strict" || t1.Enum[1] != "dwrr" {
			t.Errorf("type.enum = %v, want [strict dwrr]", t1.Enum)
		}
	}
}
