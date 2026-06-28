package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	for _, want := range []string{"ServiceSpec", "QoSPolicy", "FilterSpec", "IPVPNSpec", "NodeSpec"} {
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

// TestSchemaShow_ServiceSpecRequiredWhen pins the contract for newtcon
// PR2: ServiceSpec's ipvpn and macvpn ref fields surface required_when
// predicates on the wire. The shape must match what newtcon's
// evaluator walks — atomic node with `field` + `in`, no DSL string.
func TestSchemaShow_ServiceSpecRequiredWhen(t *testing.T) {
	s := NewServer(Config{})
	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/schema/ServiceSpec", nil)
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
	ipvpn, ok := byName["ipvpn"]
	if !ok {
		t.Fatal("ipvpn field missing")
	}
	if ipvpn.RequiredWhen == nil {
		t.Fatal("ipvpn.required_when nil — PR2 expected the predicate on this field")
	}
	if ipvpn.RequiredWhen.Field != "service_type" {
		t.Errorf("ipvpn.required_when.field = %q, want service_type", ipvpn.RequiredWhen.Field)
	}
	gotIn := stringSet(ipvpn.RequiredWhen.In)
	wantIn := map[string]bool{"evpn-irb": true, "evpn-routed": true}
	if !setsEqual(gotIn, wantIn) {
		t.Errorf("ipvpn.required_when.in = %v, want %v", gotIn, wantIn)
	}
	macvpn, ok := byName["macvpn"]
	if !ok {
		t.Fatal("macvpn field missing")
	}
	if macvpn.RequiredWhen == nil {
		t.Fatal("macvpn.required_when nil")
	}
	gotMac := stringSet(macvpn.RequiredWhen.In)
	wantMac := map[string]bool{"evpn-irb": true, "evpn-bridged": true}
	if !setsEqual(gotMac, wantMac) {
		t.Errorf("macvpn.required_when.in = %v, want %v", gotMac, wantMac)
	}
	// Non-conditional fields must not carry the predicate.
	desc := byName["description"]
	if desc.RequiredWhen != nil {
		t.Errorf("description.required_when should be nil; got %+v", desc.RequiredWhen)
	}
}

func stringSet(values []any) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		if s, ok := v.(string); ok {
			out[s] = true
		}
	}
	return out
}

func setsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
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

// TestSchemaList_HasPrefixListSpec verifies PrefixListSpec (added as
// part of newtcon's universal-engine follow-up) appears in /schema with
// the registered label, closing the asymmetry where every other
// top-level kind had a schema entry.
func TestSchemaList_HasPrefixListSpec(t *testing.T) {
	s := NewServer(Config{})
	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/schema", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	var env struct {
		Data SchemaList `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	by := indexByKind(env.Data.Kinds)
	pl, ok := by["PrefixListSpec"]
	if !ok {
		t.Fatal("PrefixListSpec missing from /schema response")
	}
	if pl.Label != "Prefix List" {
		t.Errorf("label: got %q, want %q", pl.Label, "Prefix List")
	}
}

// TestSchemaShow_PrefixListSpec verifies the PrefixListSpec kind exposes
// the same top-level shape every other top-level kind does: synthetic
// `name` identifier field with pattern+immutable, prefixes field with
// type=array of strings, full CRUD paths.
func TestSchemaShow_PrefixListSpec(t *testing.T) {
	s := NewServer(Config{})
	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/schema/PrefixListSpec", nil)
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
	want := spec.SchemaPaths{
		List:   "/newtron/v1/networks/{netID}/prefix-lists",
		Show:   "/newtron/v1/networks/{netID}/prefix-lists/{name}",
		Create: "/newtron/v1/networks/{netID}/create-prefix-list",
		Update: "/newtron/v1/networks/{netID}/update-prefix-list",
		Delete: "/newtron/v1/networks/{netID}/delete-prefix-list",
	}
	if env.Data.Paths != want {
		t.Errorf("paths: got %+v, want %+v", env.Data.Paths, want)
	}
	if len(env.Data.Fields) == 0 || env.Data.Fields[0].Name != "name" {
		t.Fatalf("synthetic name field missing: %+v", env.Data.Fields)
	}
	prefixes := findField(env.Data.Fields, "prefixes")
	if prefixes == nil {
		t.Fatal("prefixes field missing")
	}
	if prefixes.Type != "array" || prefixes.ItemType != "string" {
		t.Errorf("prefixes: type=%s item_type=%s, want array/string", prefixes.Type, prefixes.ItemType)
	}
}

func findField(fields []spec.FieldMeta, name string) *spec.FieldMeta {
	for i := range fields {
		if fields[i].Name == name {
			return &fields[i]
		}
	}
	return nil
}

// TestSchemaAll verifies GET /schema/all returns every registered kind's
// full SchemaMeta in one response — the universal-engine cold-start
// collapse from N+1 requests to one.
func TestSchemaAll(t *testing.T) {
	s := NewServer(Config{})
	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/schema/all", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var env struct {
		Data SchemaAllResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Count must match /schema's list.
	listReq := httptest.NewRequest(http.MethodGet, "/newtron/v1/schema", nil)
	listW := httptest.NewRecorder()
	s.Handler().ServeHTTP(listW, listReq)
	var listEnv struct {
		Data SchemaList `json:"data"`
	}
	if err := json.Unmarshal(listW.Body.Bytes(), &listEnv); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(env.Data.Schemas) != len(listEnv.Data.Kinds) {
		t.Errorf("schema/all returned %d kinds; /schema returned %d", len(env.Data.Schemas), len(listEnv.Data.Kinds))
	}
	// Spot-check: ServiceSpec entry includes its synthetic name field and
	// the full enum vocabulary — proves the full SchemaMeta is included,
	// not the summary shape.
	var serviceMeta *spec.SchemaMeta
	for i := range env.Data.Schemas {
		if env.Data.Schemas[i].Kind == "ServiceSpec" {
			serviceMeta = &env.Data.Schemas[i]
			break
		}
	}
	if serviceMeta == nil {
		t.Fatal("ServiceSpec missing from /schema/all")
	}
	if len(serviceMeta.Fields) < 2 || serviceMeta.Fields[0].Name != "name" {
		t.Errorf("ServiceSpec fields look summary-shaped, not full: %+v", serviceMeta.Fields[:min(len(serviceMeta.Fields), 3)])
	}
	st := findField(serviceMeta.Fields, "service_type")
	if st == nil || len(st.Enum) != 6 {
		t.Errorf("ServiceSpec.service_type enum: got %+v, want 6 values", st)
	}
}

// TestSchemaCacheHeaders_LastModifiedPresent verifies every schema
// endpoint sets a Last-Modified header. Without these headers UIs cannot
// implement conditional fetches and re-pull the full schema on every
// visibility-change.
func TestSchemaCacheHeaders_LastModifiedPresent(t *testing.T) {
	s := NewServer(Config{})
	cases := []string{
		"/newtron/v1/schema",
		"/newtron/v1/schema/all",
		"/newtron/v1/schema/ServiceSpec",
	}
	for _, path := range cases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, w.Code)
			continue
		}
		if got := w.Header().Get("Last-Modified"); got == "" {
			t.Errorf("%s: Last-Modified header missing", path)
		}
		if got := w.Header().Get("Cache-Control"); got == "" {
			t.Errorf("%s: Cache-Control header missing", path)
		}
	}
}

// TestSchemaCacheHeaders_IfModifiedSinceNotModified verifies that
// when the client's If-Modified-Since equals the server's
// Last-Modified, the server returns 304 with no body. This is the
// hot-path newtcon's visibility-change re-fetch hits.
func TestSchemaCacheHeaders_IfModifiedSinceNotModified(t *testing.T) {
	s := NewServer(Config{})
	// First, fetch the canonical Last-Modified value.
	primingReq := httptest.NewRequest(http.MethodGet, "/newtron/v1/schema", nil)
	primingW := httptest.NewRecorder()
	s.Handler().ServeHTTP(primingW, primingReq)
	lm := primingW.Header().Get("Last-Modified")
	if lm == "" {
		t.Fatal("priming Last-Modified empty")
	}
	// Re-fetch with that exact value as If-Modified-Since.
	for _, path := range []string{
		"/newtron/v1/schema",
		"/newtron/v1/schema/all",
		"/newtron/v1/schema/ServiceSpec",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("If-Modified-Since", lm)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusNotModified {
			t.Errorf("%s: status = %d, want 304", path, w.Code)
		}
		// 304 response MUST NOT carry a message body.
		if w.Body.Len() != 0 {
			t.Errorf("%s: 304 response should have empty body; got %d bytes", path, w.Body.Len())
		}
	}
}

// TestSchemaCacheHeaders_IfModifiedSinceStale verifies that when the
// client's If-Modified-Since is older than the server's Last-Modified,
// the server returns 200 with the full body. Without this branch the
// conditional fetch would erroneously withhold updated schemas after
// a deploy.
func TestSchemaCacheHeaders_IfModifiedSinceStale(t *testing.T) {
	s := NewServer(Config{})
	// One hour in the past — guaranteed older than any boot-time
	// timestamp the server might be carrying.
	old := time.Now().UTC().Add(-time.Hour).Format(http.TimeFormat)
	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/schema", nil)
	req.Header.Set("If-Modified-Since", old)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (stale cache should re-fetch)", w.Code)
	}
	if w.Body.Len() == 0 {
		t.Error("200 response should carry a body")
	}
}
