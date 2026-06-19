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
