package spec

import (
	"reflect"
	"testing"
)

// fixtures — distinct test types so the extractor can be exercised in
// isolation from the real spec registry.

type fixSimple struct {
	Name        string `json:"name" label:"Name" tooltip:"The thing's name"`
	Description string `json:"description,omitempty" label:"Description"`
	Count       int    `json:"count"`
}

type fixEnum struct {
	Action string `json:"action" enum:"permit,deny" label:"Action"`
	Type   string `json:"type" enum:"strict, dwrr , wrr" label:"Type"`
}

type fixRef struct {
	IPVPNName string `json:"ipvpn,omitempty" ref:"IPVPNSpec" label:"IP-VPN"`
}

type fixNested struct {
	Inner fixSimple `json:"inner" label:"Inner"`
}

type fixArray struct {
	Rules  []fixSimple `json:"rules" label:"Rules" item_kind:"FilterRule"`
	Names  []string    `json:"names,omitempty"`
	Queues []*fixEnum  `json:"queues"`
}

type fixMap struct {
	Services map[string]*fixSimple `json:"services"`
	Strings  map[string]string     `json:"strings,omitempty"`
}

type fixHidden struct {
	private string `json:"private"`         //nolint:unused
	Skip    string `json:"-"`               // explicitly hidden
	Real    string `json:"real" label:"Real"`
}

type fixPointer struct {
	OptionalFlag *bool `json:"flag,omitempty" label:"Flag"`
	Required     string `json:"required"`
}

type fixEmbedded struct {
	fixSimple
	Extra string `json:"extra"`
}

// TestExtractFields_BasicTypes covers primitive types, omitempty inference,
// and the humanize default-label fallback.
func TestExtractFields_BasicTypes(t *testing.T) {
	got := extractFields(reflect.TypeOf(fixSimple{}))
	want := []FieldMeta{
		{Name: "name", Label: "Name", Description: "The thing's name", Type: "string", Required: true},
		{Name: "description", Label: "Description", Type: "string", Required: false},
		{Name: "count", Label: "Count", Type: "int", Required: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractFields(fixSimple) = %+v, want %+v", got, want)
	}
}

func TestExtractFields_EnumValues(t *testing.T) {
	got := extractFields(reflect.TypeOf(fixEnum{}))
	if len(got) != 2 {
		t.Fatalf("want 2 fields, got %d", len(got))
	}
	if got[0].Type != "enum" || !reflect.DeepEqual(got[0].Enum, []string{"permit", "deny"}) {
		t.Errorf("action: got type=%s enum=%v", got[0].Type, got[0].Enum)
	}
	// Whitespace in the enum tag should be trimmed.
	if !reflect.DeepEqual(got[1].Enum, []string{"strict", "dwrr", "wrr"}) {
		t.Errorf("type enum whitespace not trimmed: %v", got[1].Enum)
	}
}

func TestExtractFields_RefKind(t *testing.T) {
	got := extractFields(reflect.TypeOf(fixRef{}))
	if len(got) != 1 {
		t.Fatalf("want 1 field, got %d", len(got))
	}
	if got[0].Type != "ref" || got[0].RefKind != "IPVPNSpec" {
		t.Errorf("ipvpn: got type=%s ref_kind=%s", got[0].Type, got[0].RefKind)
	}
	// Pointer-or-omitempty optional inference applies to ref-typed fields
	// too — required is false here because of omitempty.
	if got[0].Required {
		t.Error("ipvpn should be optional (omitempty)")
	}
}

func TestExtractFields_NestedObject(t *testing.T) {
	got := extractFields(reflect.TypeOf(fixNested{}))
	if len(got) != 1 {
		t.Fatalf("want 1 field, got %d", len(got))
	}
	if got[0].Type != "object" || got[0].ItemKind != "fixSimple" {
		t.Errorf("inner: type=%s item_kind=%s, want object/fixSimple", got[0].Type, got[0].ItemKind)
	}
}

func TestExtractFields_Arrays(t *testing.T) {
	got := extractFields(reflect.TypeOf(fixArray{}))
	if len(got) != 3 {
		t.Fatalf("want 3 fields, got %d", len(got))
	}
	// rules: []fixSimple with item_kind tag override → uses tag value
	if got[0].Type != "array" || got[0].ItemKind != "FilterRule" {
		t.Errorf("rules: type=%s item_kind=%s, want array/FilterRule (from tag)", got[0].Type, got[0].ItemKind)
	}
	// names: []string → primitive item_type
	if got[1].Type != "array" || got[1].ItemType != "string" {
		t.Errorf("names: type=%s item_type=%s, want array/string", got[1].Type, got[1].ItemType)
	}
	// queues: []*fixEnum → pointer is unwrapped, item_kind inferred from type name
	if got[2].Type != "array" || got[2].ItemKind != "fixEnum" {
		t.Errorf("queues: type=%s item_kind=%s, want array/fixEnum", got[2].Type, got[2].ItemKind)
	}
}

func TestExtractFields_Maps(t *testing.T) {
	got := extractFields(reflect.TypeOf(fixMap{}))
	if len(got) != 2 {
		t.Fatalf("want 2 fields, got %d", len(got))
	}
	// services: map[string]*fixSimple → element is struct, ItemKind set
	if got[0].Type != "map" || got[0].ItemKind != "fixSimple" {
		t.Errorf("services: type=%s item_kind=%s, want map/fixSimple", got[0].Type, got[0].ItemKind)
	}
	// strings: map[string]string → element is primitive, ItemType set
	if got[1].Type != "map" || got[1].ItemType != "string" {
		t.Errorf("strings: type=%s item_type=%s, want map/string", got[1].Type, got[1].ItemType)
	}
}

func TestExtractFields_HiddenFields(t *testing.T) {
	got := extractFields(reflect.TypeOf(fixHidden{}))
	// Only `Real` survives — `private` is unexported, `Skip` is json:"-".
	if len(got) != 1 || got[0].Name != "real" {
		t.Errorf("hidden fields not filtered: %+v", got)
	}
}

func TestExtractFields_PointerOptional(t *testing.T) {
	got := extractFields(reflect.TypeOf(fixPointer{}))
	if len(got) != 2 {
		t.Fatalf("want 2 fields, got %d", len(got))
	}
	// Pointer is always optional regardless of tag.
	if got[0].Required {
		t.Error("OptionalFlag (*bool) should be optional")
	}
	if !got[1].Required {
		t.Error("Required (string without omitempty) should be required")
	}
	// Pointer-to-bool unwraps to bool type.
	if got[0].Type != "bool" {
		t.Errorf("OptionalFlag type=%s, want bool", got[0].Type)
	}
}

func TestExtractFields_EmbeddedFlattened(t *testing.T) {
	got := extractFields(reflect.TypeOf(fixEmbedded{}))
	// fixSimple contributes name, description, count; then extra → 4 fields.
	if len(got) != 4 {
		t.Fatalf("want 4 fields (embedded flattened), got %d: %+v", len(got), got)
	}
	names := []string{got[0].Name, got[1].Name, got[2].Name, got[3].Name}
	want := []string{"name", "description", "count", "extra"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("embedded field order: %v, want %v", names, want)
	}
}

func TestHumanizeName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"name", "Name"},
		{"vrf_name", "VRF Name"},
		{"src_ip", "Src IP"},
		{"ipvpn", "Ipvpn"}, // not in acronym table; default casing
		{"qos_policy", "QoS Policy"},
		{"bgp_peer_as", "BGP Peer AS"},
		{"l2vni", "L2VNI"},
		{"mac_address", "MAC Address"},
	}
	for _, c := range cases {
		if got := humanizeName(c.in); got != c.want {
			t.Errorf("humanizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRegisterAndLookup(t *testing.T) {
	// Snapshot + restore so this test doesn't pollute the real registry.
	saved := schemaRegistry
	schemaRegistry = map[string]schemaKind{}
	defer func() { schemaRegistry = saved }()

	RegisterSchemaKind("FixSimple", "Simple Fixture", "A test fixture", fixSimple{})
	RegisterSchemaKind("FixEnum", "Enum Fixture", "", fixEnum{})

	kinds := ListSchemaKinds()
	want := []string{"FixEnum", "FixSimple"}
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("ListSchemaKinds = %v, want %v", kinds, want)
	}

	meta := LookupSchema("FixSimple")
	if meta == nil {
		t.Fatal("LookupSchema(FixSimple) returned nil")
	}
	if meta.Kind != "fixSimple" || meta.Label != "Simple Fixture" || meta.Description != "A test fixture" {
		t.Errorf("unexpected meta: %+v", meta)
	}
	if len(meta.Fields) != 3 {
		t.Errorf("want 3 fields, got %d", len(meta.Fields))
	}

	if LookupSchema("nonexistent") != nil {
		t.Error("LookupSchema(nonexistent) should return nil")
	}
}
