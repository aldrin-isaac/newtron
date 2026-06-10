package spec

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestPermissionGrants_LegacyShorthand pins the wire-shape compat:
// a flat array of group strings still decodes correctly, into a
// single PermissionGrant with no Where. This is the only compat
// shim in the auth subsystem; it must hold (auth-design.md §5 L5).
func TestPermissionGrants_LegacyShorthand(t *testing.T) {
	data := []byte(`["neteng", "netops"]`)
	var got PermissionGrants
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := PermissionGrants{
		{Groups: []string{"neteng", "netops"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestPermissionGrants_TypedForm pins the L5 syntax: an array of
// {groups, where} objects produces one PermissionGrant per object.
func TestPermissionGrants_TypedForm(t *testing.T) {
	data := []byte(`[
		{"groups": ["edge-ops"],  "where": {"device": "edge-*"}},
		{"groups": ["spine-ops"], "where": {"device": "spine-*"}}
	]`)
	var got PermissionGrants
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := PermissionGrants{
		{Groups: []string{"edge-ops"}, Where: map[string]string{"device": "edge-*"}},
		{Groups: []string{"spine-ops"}, Where: map[string]string{"device": "spine-*"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestPermissionGrants_EmptyArray pins that [] decodes to nil grants
// — equivalent to "no grants", which downstream code reads as deny.
func TestPermissionGrants_EmptyArray(t *testing.T) {
	data := []byte(`[]`)
	var got PermissionGrants
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

// TestPermissionGrants_RejectMalformed pins that the unmarshaller
// distinguishes legitimate decode errors from the type discrimination
// path. A first element that is neither a string nor an object is a
// hard error — not a silent fallback to one or the other shape.
func TestPermissionGrants_RejectMalformed(t *testing.T) {
	cases := map[string]string{
		"not an array":     `{"groups": ["a"]}`,
		"first is number":  `[42]`,
		"first is null":    `[null]`,
		"first is boolean": `[true]`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			var got PermissionGrants
			if err := json.Unmarshal([]byte(raw), &got); err == nil {
				t.Errorf("expected error for %s, got nil; result=%+v", name, got)
			}
		})
	}
}

// TestPermissionGrants_MarshalShorthand pins that grants with no
// Where round-trip back to the legacy flat-list form on emit. An
// operator who hand-edits a permission with no scoping continues to
// see ["group1", "group2"] in the file after a server-side rewrite.
func TestPermissionGrants_MarshalShorthand(t *testing.T) {
	g := PermissionGrants{{Groups: []string{"a", "b"}}}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(data) != `["a","b"]` {
		t.Errorf("got %s, want [\"a\",\"b\"]", string(data))
	}
}

// TestPermissionGrants_MarshalTyped pins that grants with a non-empty
// Where emit as the typed form. Round-trip is identity-preserving for
// the L5 syntax.
func TestPermissionGrants_MarshalTyped(t *testing.T) {
	g := PermissionGrants{
		{Groups: []string{"edge-ops"}, Where: map[string]string{"device": "edge-*"}},
	}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Round-trip should give back the same grants.
	var roundtrip PermissionGrants
	if err := json.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("Unmarshal back: %v", err)
	}
	if !reflect.DeepEqual(roundtrip, g) {
		t.Errorf("round-trip mismatch: got %+v, want %+v", roundtrip, g)
	}
}
