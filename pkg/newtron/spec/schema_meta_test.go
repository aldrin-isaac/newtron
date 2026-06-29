package spec

import (
	"reflect"
	"strings"
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

// nolint:unused — fields are referenced by the reflection extractor under test
type fixHidden struct {
	private string //nolint:unused // tests unexported filter
	Skip    string `json:"-"` // explicitly hidden
	Real    string `json:"real" label:"Real"`
}

type fixPointer struct {
	OptionalFlag *bool  `json:"flag,omitempty" label:"Flag"`
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

// TestExtractFields_PrefixListRefs pins that the filter-rule and
// route-policy-rule prefix-list fields are typed as refs to PrefixListSpec
// (newtron-gap-subrule-prefix-list-refs) — so a schema-driven UI renders a
// dropdown of existing prefix-lists rather than a free-text box. Same pattern
// as RoutingSpec.import/export_prefix_list (#264).
func TestExtractFields_PrefixListRefs(t *testing.T) {
	byName := func(v any) map[string]FieldMeta {
		m := map[string]FieldMeta{}
		for _, f := range extractFields(reflect.TypeOf(v)) {
			m[f.Name] = f
		}
		return m
	}
	assertRef := func(t *testing.T, fm FieldMeta, field string) {
		t.Helper()
		if fm.Type != "ref" || fm.RefKind != "PrefixListSpec" {
			t.Errorf("%s: type=%q ref_kind=%q, want ref/PrefixListSpec", field, fm.Type, fm.RefKind)
		}
	}

	fr := byName(FilterRule{})
	assertRef(t, fr["src_prefix_list"], "FilterRule.src_prefix_list")
	assertRef(t, fr["dst_prefix_list"], "FilterRule.dst_prefix_list")
	// The inline-match siblings stay plain strings — they are literal values,
	// not refs.
	if fr["src_ip"].Type != "string" {
		t.Errorf("FilterRule.src_ip: type=%q, want string", fr["src_ip"].Type)
	}

	rpr := byName(RoutePolicyRule{})
	assertRef(t, rpr["prefix_list"], "RoutePolicyRule.prefix_list")
	// community stays a literal string (no CommunityList spec kind).
	if rpr["community"].Type != "string" {
		t.Errorf("RoutePolicyRule.community: type=%q, want string", rpr["community"].Type)
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

// fixSchemaExcluded embeds a struct tagged `schema:"-"` — it must be suppressed
// from the authoring schema (unlike a plain embed, which flattens).
type fixSchemaExcluded struct {
	fixSimple `schema:"-"`
	Extra     string `json:"extra"`
}

func TestExtractFields_SchemaDashExcludesEmbed(t *testing.T) {
	got := extractFields(reflect.TypeOf(fixSchemaExcluded{}))
	if len(got) != 1 || got[0].Name != "extra" {
		t.Errorf("schema:\"-\" embed not suppressed: got %+v, want [extra] only", got)
	}
}

// TestSchema_OverrideMapsExcluded pins the flat-override model on the wire:
// ZoneSpec/NodeSpec embed OverridableSpecs as the override STORE, but overrides
// are authored via the flat create-<kind>?scope API — so the authoring schema
// must not advertise the seven override maps as fields (they'd render as dead
// "not authorable here" fields). The maps still serialize as JSON storage.
func TestSchema_OverrideMapsExcluded(t *testing.T) {
	overrideMaps := []string{"services", "filters", "ipvpns", "macvpns",
		"qos_policies", "route_policies", "prefix_lists"}
	has := func(m *SchemaMeta, name string) bool {
		for i := range m.Fields {
			if m.Fields[i].Name == name {
				return true
			}
		}
		return false
	}
	for _, kind := range []string{"ZoneSpec", "NodeSpec"} {
		m := LookupSchema(kind)
		if m == nil {
			t.Fatalf("%s: no schema registered", kind)
		}
		for _, om := range overrideMaps {
			if has(m, om) {
				t.Errorf("%s schema still advertises override map %q (should be authored via create-%s?scope)", kind, om, om)
			}
		}
	}
	// NodeSpec keeps its own fields, including evpn (a real nested config, not an override map).
	nm := LookupSchema("NodeSpec")
	for _, want := range []string{"mgmt_ip", "loopback_ip", "zone", "platform", "evpn"} {
		if !has(nm, want) {
			t.Errorf("NodeSpec schema dropped its own field %q", want)
		}
	}
	// ZoneSpec is a pure scope container — only the injected name field remains.
	zm := LookupSchema("ZoneSpec")
	if len(zm.Fields) != 1 || zm.Fields[0].Name != "name" {
		names := make([]string, len(zm.Fields))
		for i, f := range zm.Fields {
			names[i] = f.Name
		}
		t.Errorf("ZoneSpec fields = %v, want [name] only", names)
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

// ----------------------------------------------------------------------------
// RequiredWhen — extractor wiring, init-time validation, panic shapes
// ----------------------------------------------------------------------------

type fixSvc struct {
	Type        string `json:"service_type" enum:"evpn-irb,evpn-bridged,evpn-routed,irb,bridged,routed"`
	IPVPN       string `json:"ipvpn,omitempty" ref:"IPVPNSpec"`
	MACVPN      string `json:"macvpn,omitempty" ref:"MACVPNSpec"`
	ARPSuppress bool   `json:"arp_suppression,omitempty"`
}

func swapRegistry(t *testing.T) {
	t.Helper()
	saved := schemaRegistry
	schemaRegistry = map[string]schemaKind{}
	t.Cleanup(func() { schemaRegistry = saved })
}

// TestRequiredWhen_AttachedToTargetField verifies a registered
// RequiredWhen predicate is attached to the named field's FieldMeta in
// the returned SchemaMeta — UIs see it on the field they evaluate
// against.
func TestRequiredWhen_AttachedToTargetField(t *testing.T) {
	swapRegistry(t)
	RegisterSchemaKind(SchemaRegistration{
		Kind:   "FixSvc",
		Label:  "Service Fixture",
		Sample: fixSvc{},
		RequiredWhen: map[string]*RequiredWhen{
			"ipvpn":  {Field: "service_type", In: []any{"evpn-irb", "evpn-routed"}},
			"macvpn": {Field: "service_type", In: []any{"evpn-irb", "evpn-bridged"}},
		},
	})
	meta := LookupSchema("FixSvc")
	if meta == nil {
		t.Fatal("LookupSchema(FixSvc) returned nil")
	}
	byName := make(map[string]FieldMeta, len(meta.Fields))
	for _, f := range meta.Fields {
		byName[f.Name] = f
	}
	ipvpn := byName["ipvpn"]
	if ipvpn.RequiredWhen == nil {
		t.Fatal("ipvpn.required_when is nil")
	}
	if ipvpn.RequiredWhen.Field != "service_type" {
		t.Errorf("ipvpn.required_when.field = %q, want service_type", ipvpn.RequiredWhen.Field)
	}
	if len(ipvpn.RequiredWhen.In) != 2 {
		t.Errorf("ipvpn.required_when.in length = %d, want 2", len(ipvpn.RequiredWhen.In))
	}
	// Non-targeted fields stay clear.
	if byName["arp_suppression"].RequiredWhen != nil {
		t.Error("arp_suppression should not carry a required_when (none was registered)")
	}
}

// TestRequiredWhen_RefFieldThroughReference verifies a predicate that looks
// through a reference (Field is a `ref:` field, RefField names a property of the
// referenced kind) is accepted and emitted intact — the node-form case where
// loopback_ip is required when the platform's device_type isn't host.
func TestRequiredWhen_RefFieldThroughReference(t *testing.T) {
	swapRegistry(t)
	RegisterSchemaKind(SchemaRegistration{
		Kind:   "FixSvc",
		Sample: fixSvc{},
		// ipvpn is a `ref:"IPVPNSpec"` field, so it's a valid RefField anchor.
		RequiredWhen: map[string]*RequiredWhen{
			"macvpn": {Field: "ipvpn", RefField: "vrf_name", NotEquals: "host"},
		},
	})
	meta := LookupSchema("FixSvc")
	if meta == nil {
		t.Fatal("LookupSchema(FixSvc) returned nil")
	}
	for _, f := range meta.Fields {
		if f.Name != "macvpn" {
			continue
		}
		rw := f.RequiredWhen
		if rw == nil || rw.Field != "ipvpn" || rw.RefField != "vrf_name" || rw.NotEquals != "host" {
			t.Fatalf("macvpn.required_when = %+v, want {field:ipvpn, ref_field:vrf_name, not_equals:host}", rw)
		}
		return
	}
	t.Fatal("macvpn field not found in schema")
}

// TestRequiredWhen_PanicsOnRefFieldNonReference pins that ref_field may only
// anchor on a reference field — a property lookup needs a `ref:` to tell the
// client which kind to resolve.
func TestRequiredWhen_PanicsOnRefFieldNonReference(t *testing.T) {
	swapRegistry(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic: ref_field anchored on a non-reference field")
		}
	}()
	RegisterSchemaKind(SchemaRegistration{
		Kind:   "FixSvc",
		Sample: fixSvc{},
		RequiredWhen: map[string]*RequiredWhen{
			// service_type has no `ref:` tag — can't anchor a ref_field lookup.
			"macvpn": {Field: "service_type", RefField: "device_type", Equals: "switch"},
		},
	})
}

// TestRequiredWhen_PanicsOnUnknownTargetField confirms the init-time
// validator catches a typo'd map key — the canonical case the agreement
// with newtcon called out (`{"servce_type": ...}` instead of
// `{"service_type": ...}`).
func TestRequiredWhen_PanicsOnUnknownTargetField(t *testing.T) {
	swapRegistry(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on unknown target field; got none")
		} else {
			msg, _ := r.(string)
			if msg == "" {
				if e, ok := r.(error); ok {
					msg = e.Error()
				}
			}
			if msg == "" {
				t.Fatalf("panic value not stringable: %v (%T)", r, r)
			}
			if !strings.Contains(msg, "not_a_field") || !strings.Contains(msg, "FixSvc") {
				t.Errorf("panic message should name the bad field and the kind; got %q", msg)
			}
		}
	}()
	RegisterSchemaKind(SchemaRegistration{
		Kind:   "FixSvc",
		Sample: fixSvc{},
		RequiredWhen: map[string]*RequiredWhen{
			"not_a_field": {Field: "service_type", Equals: "evpn-irb"},
		},
	})
}

// TestRequiredWhen_PanicsOnUnknownConditionField catches a typo INSIDE
// the condition — the target key is real but it references a sibling
// that doesn't exist.
func TestRequiredWhen_PanicsOnUnknownConditionField(t *testing.T) {
	swapRegistry(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on unknown condition field; got none")
		}
	}()
	RegisterSchemaKind(SchemaRegistration{
		Kind:   "FixSvc",
		Sample: fixSvc{},
		RequiredWhen: map[string]*RequiredWhen{
			"ipvpn": {Field: "servce_type", Equals: "evpn-irb"}, // typo
		},
	})
}

// TestRequiredWhen_PanicsOnMixedShape pins the atomic-vs-combinator XOR
// invariant. A single node cannot set Field+operand AND AllOf/AnyOf.
func TestRequiredWhen_PanicsOnMixedShape(t *testing.T) {
	swapRegistry(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on mixed atomic+combinator shape; got none")
		}
	}()
	RegisterSchemaKind(SchemaRegistration{
		Kind:   "FixSvc",
		Sample: fixSvc{},
		RequiredWhen: map[string]*RequiredWhen{
			"ipvpn": {
				Field:  "service_type",
				Equals: "evpn-irb",
				AllOf:  []*RequiredWhen{{Field: "arp_suppression", Equals: false}},
			},
		},
	})
}

// TestRequiredWhen_PanicsOnMultipleOperands confirms an atomic node
// can carry at most one of Equals / NotEquals / In / NotIn — readers
// otherwise can't tell which operand wins.
func TestRequiredWhen_PanicsOnMultipleOperands(t *testing.T) {
	swapRegistry(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on multiple operands; got none")
		}
	}()
	RegisterSchemaKind(SchemaRegistration{
		Kind:   "FixSvc",
		Sample: fixSvc{},
		RequiredWhen: map[string]*RequiredWhen{
			"ipvpn": {Field: "service_type", Equals: "evpn-irb", In: []any{"evpn-routed"}},
		},
	})
}

// TestRequiredWhen_PanicsOnEmptyCondition catches a registration that
// supplies a non-nil condition with no actual content.
func TestRequiredWhen_PanicsOnEmptyCondition(t *testing.T) {
	swapRegistry(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on empty condition; got none")
		}
	}()
	RegisterSchemaKind(SchemaRegistration{
		Kind:   "FixSvc",
		Sample: fixSvc{},
		RequiredWhen: map[string]*RequiredWhen{
			"ipvpn": {}, // no field, no combinator
		},
	})
}

// TestRequiredWhen_CombinatorValidatesChildren verifies the validator
// recurses into AllOf / AnyOf children. The combinator wrapper looks
// well-formed but contains an invalid atomic child — registration must
// still panic.
func TestRequiredWhen_CombinatorValidatesChildren(t *testing.T) {
	swapRegistry(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on invalid child in combinator; got none")
		}
	}()
	RegisterSchemaKind(SchemaRegistration{
		Kind:   "FixSvc",
		Sample: fixSvc{},
		RequiredWhen: map[string]*RequiredWhen{
			"ipvpn": {AllOf: []*RequiredWhen{
				{Field: "service_type", Equals: "evpn-irb"},
				{Field: "nonexistent", Equals: true}, // bad child
			}},
		},
	})
}

// TestRequiredWhen_CombinatorAccepts confirms a well-formed combinator
// registers without panic — newtcon's `!arp_suppression` example.
func TestRequiredWhen_CombinatorAccepts(t *testing.T) {
	swapRegistry(t)
	RegisterSchemaKind(SchemaRegistration{
		Kind:   "FixSvc",
		Sample: fixSvc{},
		RequiredWhen: map[string]*RequiredWhen{
			"ipvpn": {AllOf: []*RequiredWhen{
				{Field: "service_type", In: []any{"evpn-irb", "evpn-routed"}},
				{Field: "arp_suppression", Equals: false},
			}},
		},
	})
	meta := LookupSchema("FixSvc")
	if meta == nil {
		t.Fatal("LookupSchema returned nil")
	}
	byName := make(map[string]FieldMeta, len(meta.Fields))
	for _, f := range meta.Fields {
		byName[f.Name] = f
	}
	cond := byName["ipvpn"].RequiredWhen
	if cond == nil || len(cond.AllOf) != 2 {
		t.Errorf("AllOf not propagated: %+v", cond)
	}
}

// TestRequiredWhen_SyntheticIdentifierAllowed confirms a RequiredWhen
// can reference the synthetic IdentifierField — for top-level kinds the
// "name" field is virtual but participates in the form, so it's a valid
// sibling reference.
func TestRequiredWhen_SyntheticIdentifierAllowed(t *testing.T) {
	swapRegistry(t)
	RegisterSchemaKind(SchemaRegistration{
		Kind:   "FixSvc",
		Sample: fixSvc{},
		IdentifierField: &FieldMeta{
			Name:     "name",
			Label:    "Name",
			Type:     "string",
			Required: true,
		},
		RequiredWhen: map[string]*RequiredWhen{
			"ipvpn": {Field: "name", Equals: "primary"},
		},
	})
	meta := LookupSchema("FixSvc")
	if meta == nil {
		t.Fatal("LookupSchema returned nil")
	}
}

func TestRegisterAndLookup(t *testing.T) {
	// Snapshot + restore so this test doesn't pollute the real registry.
	saved := schemaRegistry
	schemaRegistry = map[string]schemaKind{}
	defer func() { schemaRegistry = saved }()

	RegisterSchemaKind(SchemaRegistration{
		Kind:        "FixSimple",
		Label:       "Simple Fixture",
		Description: "A test fixture",
		Sample:      fixSimple{},
		Identifier:  "name",
		Paths: SchemaPaths{
			List:   "/x/list",
			Create: "/x/create",
		},
	})
	RegisterSchemaKind(SchemaRegistration{
		Kind:   "FixEnum",
		Label:  "Enum Fixture",
		Sample: fixEnum{},
	})

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
	if meta.Identifier != "name" {
		t.Errorf("identifier: got %q, want name", meta.Identifier)
	}
	if meta.Paths.List != "/x/list" || meta.Paths.Create != "/x/create" {
		t.Errorf("paths not surfaced: %+v", meta.Paths)
	}
	if meta.Paths.Update != "" || meta.Paths.Delete != "" {
		t.Errorf("unset paths should remain empty: %+v", meta.Paths)
	}
	if len(meta.Fields) != 3 {
		t.Errorf("want 3 fields, got %d", len(meta.Fields))
	}

	if LookupSchema("nonexistent") != nil {
		t.Error("LookupSchema(nonexistent) should return nil")
	}
}

// TestRegister_SyntheticIdentifierField verifies that an IdentifierField
// on the registration is prepended to the field list. Used by sub-rule
// kinds (QoSQueue) whose identifier is implicit in the parent's
// representation.
func TestRegister_SyntheticIdentifierField(t *testing.T) {
	saved := schemaRegistry
	schemaRegistry = map[string]schemaKind{}
	defer func() { schemaRegistry = saved }()

	min := 0
	max := 7
	RegisterSchemaKind(SchemaRegistration{
		Kind:       "FixSubrule",
		Label:      "Sub-rule Fixture",
		Sample:     fixSimple{},
		Identifier: "slot",
		ParentRef:  "parent",
		IdentifierField: &FieldMeta{
			Name:      "slot",
			Label:     "Slot",
			Type:      "int",
			Required:  true,
			Min:       &min,
			Max:       &max,
			Immutable: true,
		},
	})

	meta := LookupSchema("FixSubrule")
	if meta == nil {
		t.Fatal("LookupSchema(FixSubrule) returned nil")
	}
	if meta.ParentRef != "parent" {
		t.Errorf("parent_ref: got %q, want parent", meta.ParentRef)
	}
	// Synthetic field is first.
	if len(meta.Fields) < 1 || meta.Fields[0].Name != "slot" {
		t.Fatalf("synthetic identifier field not prepended: %+v", meta.Fields)
	}
	if !meta.Fields[0].Immutable {
		t.Error("synthetic identifier should be immutable")
	}
	if meta.Fields[0].Min == nil || *meta.Fields[0].Min != 0 {
		t.Errorf("min: %v, want 0", meta.Fields[0].Min)
	}
	if meta.Fields[0].Max == nil || *meta.Fields[0].Max != 7 {
		t.Errorf("max: %v, want 7", meta.Fields[0].Max)
	}
	// Original struct fields follow.
	if len(meta.Fields) != 4 {
		t.Errorf("want 4 fields (1 synthetic + 3 from fixSimple), got %d", len(meta.Fields))
	}
}

// TestExtractFields_Validation covers the validation tag parsing —
// pattern, min, max, format, immutable.
type fixValidation struct {
	Name     string `json:"name" label:"Name" pattern:"^[A-Z]+$" immutable:"true"`
	VlanID   int    `json:"vlan_id" label:"VLAN ID" min:"1" max:"4094"`
	LoopIP   string `json:"loop_ip" label:"Loopback" format:"cidr"`
	Negative int    `json:"negative" min:"-100" max:"-1"`
}

func TestExtractFields_Validation(t *testing.T) {
	got := extractFields(reflect.TypeOf(fixValidation{}))
	if len(got) != 4 {
		t.Fatalf("want 4 fields, got %d", len(got))
	}
	byName := make(map[string]FieldMeta, len(got))
	for _, f := range got {
		byName[f.Name] = f
	}
	if name := byName["name"]; name.Pattern != "^[A-Z]+$" || !name.Immutable {
		t.Errorf("name: pattern=%q immutable=%v", name.Pattern, name.Immutable)
	}
	if vlan := byName["vlan_id"]; vlan.Min == nil || *vlan.Min != 1 || vlan.Max == nil || *vlan.Max != 4094 {
		t.Errorf("vlan_id: min=%v max=%v", vlan.Min, vlan.Max)
	}
	if ip := byName["loop_ip"]; ip.Format != "cidr" {
		t.Errorf("loop_ip format=%q, want cidr", ip.Format)
	}
	if neg := byName["negative"]; neg.Min == nil || *neg.Min != -100 || neg.Max == nil || *neg.Max != -1 {
		t.Errorf("negative: min=%v max=%v", neg.Min, neg.Max)
	}
}

func TestParseIntTag(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		want int
	}{
		{"0", true, 0},
		{"7", true, 7},
		{"4094", true, 4094},
		{"-100", true, -100},
		{"-1", true, -1},
		{"", false, 0},
		{"-", false, 0},
		{"abc", false, 0},
		{"12x", false, 0},
		{"--3", false, 0},
	}
	for _, c := range cases {
		got, ok := parseIntTag(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseIntTag(%q) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// ============================================================================
// AppliesWhen — field-applicability key (mirrors RequiredWhen; shares the
// validateConditionMap validator). Confirms the AppliesWhen branch is wired
// into both the field-attach pass and the init-time validation.
// ============================================================================

// TestAppliesWhen_AttachedToTargetField verifies a registered AppliesWhen
// predicate is attached to the named field's FieldMeta, and that a field
// with no predicate stays clear.
func TestAppliesWhen_AttachedToTargetField(t *testing.T) {
	swapRegistry(t)
	RegisterSchemaKind(SchemaRegistration{
		Kind:   "FixSvc",
		Label:  "Service Fixture",
		Sample: fixSvc{},
		AppliesWhen: map[string]*RequiredWhen{
			"ipvpn": {Field: "service_type", In: []any{"routed", "irb", "evpn-routed", "evpn-irb"}},
		},
	})
	meta := LookupSchema("FixSvc")
	if meta == nil {
		t.Fatal("LookupSchema(FixSvc) returned nil")
	}
	byName := make(map[string]FieldMeta, len(meta.Fields))
	for _, f := range meta.Fields {
		byName[f.Name] = f
	}
	ipvpn := byName["ipvpn"]
	if ipvpn.AppliesWhen == nil {
		t.Fatal("ipvpn.applies_when is nil")
	}
	if ipvpn.AppliesWhen.Field != "service_type" {
		t.Errorf("ipvpn.applies_when.field = %q, want service_type", ipvpn.AppliesWhen.Field)
	}
	if len(ipvpn.AppliesWhen.In) != 4 {
		t.Errorf("ipvpn.applies_when.in length = %d, want 4", len(ipvpn.AppliesWhen.In))
	}
	// Non-targeted fields stay clear.
	if byName["macvpn"].AppliesWhen != nil {
		t.Error("macvpn should not carry an applies_when (none was registered)")
	}
}

// TestAppliesWhen_CoexistsWithRequiredWhen confirms both predicates can
// target the same kind on different fields and both reach their FieldMeta —
// the two axes are independent (applicability vs requiredness).
func TestAppliesWhen_CoexistsWithRequiredWhen(t *testing.T) {
	swapRegistry(t)
	RegisterSchemaKind(SchemaRegistration{
		Kind:   "FixSvc",
		Sample: fixSvc{},
		RequiredWhen: map[string]*RequiredWhen{
			"macvpn": {Field: "service_type", In: []any{"evpn-irb", "evpn-bridged"}},
		},
		AppliesWhen: map[string]*RequiredWhen{
			"ipvpn": {Field: "service_type", In: []any{"routed", "irb", "evpn-routed", "evpn-irb"}},
		},
	})
	meta := LookupSchema("FixSvc")
	byName := make(map[string]FieldMeta, len(meta.Fields))
	for _, f := range meta.Fields {
		byName[f.Name] = f
	}
	if byName["ipvpn"].AppliesWhen == nil {
		t.Error("ipvpn.applies_when should be set")
	}
	if byName["macvpn"].RequiredWhen == nil {
		t.Error("macvpn.required_when should be set")
	}
}

// TestAppliesWhen_PanicsOnUnknownTargetField confirms the shared
// init-time validator runs for AppliesWhen too — a typo'd map key fails
// at server start, naming the bad field, the kind, and the AppliesWhen
// label (so the operator knows which map to fix).
func TestAppliesWhen_PanicsOnUnknownTargetField(t *testing.T) {
	swapRegistry(t)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on unknown applies_when target field; got none")
		}
		msg, _ := r.(string)
		if msg == "" {
			if e, ok := r.(error); ok {
				msg = e.Error()
			}
		}
		if !strings.Contains(msg, "not_a_field") || !strings.Contains(msg, "FixSvc") || !strings.Contains(msg, "AppliesWhen") {
			t.Errorf("panic message should name the bad field, the kind, and AppliesWhen; got %q", msg)
		}
	}()
	RegisterSchemaKind(SchemaRegistration{
		Kind:   "FixSvc",
		Sample: fixSvc{},
		AppliesWhen: map[string]*RequiredWhen{
			"not_a_field": {Field: "service_type", Equals: "routed"},
		},
	})
}

// TestAppliesWhen_PanicsOnUnknownConditionField catches a typo INSIDE the
// applies_when condition — target key is real but it references a sibling
// that doesn't exist.
func TestAppliesWhen_PanicsOnUnknownConditionField(t *testing.T) {
	swapRegistry(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on unknown applies_when condition field; got none")
		}
	}()
	RegisterSchemaKind(SchemaRegistration{
		Kind:   "FixSvc",
		Sample: fixSvc{},
		AppliesWhen: map[string]*RequiredWhen{
			"ipvpn": {Field: "servce_type", Equals: "routed"}, // typo
		},
	})
}

// TestScopeFieldsInjected pins the scoped-writes schema surface (P2): every
// kind whose writes accept scope/scope_instance (the overridable spec kinds and
// their sub-rule kinds) declares both fields in its schema, so a schema-driven
// UI renders the override form with no special-casing; non-overridable kinds do
// not. scope is an enum; scope_instance is gated by AppliesWhen/RequiredWhen
// scope != network.
func TestScopeFieldsInjected(t *testing.T) {
	find := func(m *SchemaMeta, name string) *FieldMeta {
		for i := range m.Fields {
			if m.Fields[i].Name == name {
				return &m.Fields[i]
			}
		}
		return nil
	}

	scoped := []string{
		"ServiceSpec", "IPVPNSpec", "MACVPNSpec", "QoSPolicy", "FilterSpec",
		"RoutePolicy", "PrefixListSpec", // top-level overridable
		"FilterRule", "QoSQueue", "RoutePolicyRule", "PrefixListEntry", // sub-rules
	}
	for _, kind := range scoped {
		m := LookupSchema(kind)
		if m == nil {
			t.Fatalf("%s: no schema registered", kind)
		}
		scope := find(m, "scope")
		if scope == nil {
			t.Errorf("%s: missing injected 'scope' field", kind)
			continue
		}
		if scope.Type != "enum" || !reflect.DeepEqual(scope.Enum, []string{ScopeNetwork, ScopeZone, ScopeNode}) {
			t.Errorf("%s: scope field shape wrong: %+v", kind, scope)
		}
		if scope.Default != ScopeNetwork {
			t.Errorf("%s: scope default = %v, want %q", kind, scope.Default, ScopeNetwork)
		}
		si := find(m, "scope_instance")
		if si == nil {
			t.Errorf("%s: missing injected 'scope_instance' field", kind)
			continue
		}
		if si.Type != "ref" {
			t.Errorf("%s: scope_instance type = %q, want ref", kind, si.Type)
		}
		if si.AppliesWhen == nil || si.AppliesWhen.Field != "scope" || si.AppliesWhen.NotEquals != ScopeNetwork {
			t.Errorf("%s: scope_instance AppliesWhen wrong: %+v", kind, si.AppliesWhen)
		}
		if si.RequiredWhen == nil || si.RequiredWhen.NotEquals != ScopeNetwork {
			t.Errorf("%s: scope_instance RequiredWhen wrong: %+v", kind, si.RequiredWhen)
		}
		// Sibling-conditional ref: ZoneSpec when scope=zone, NodeSpec when scope=node.
		wantRef := map[any]string{ScopeZone: "ZoneSpec", ScopeNode: "NodeSpec"}
		if len(si.RefWhen) != 2 {
			t.Errorf("%s: scope_instance RefWhen = %+v, want 2 branches", kind, si.RefWhen)
		}
		for _, rc := range si.RefWhen {
			if rc.When == nil || rc.When.Field != "scope" {
				t.Errorf("%s: RefWhen branch has bad predicate: %+v", kind, rc)
				continue
			}
			if want := wantRef[rc.When.Equals]; want != rc.RefKind {
				t.Errorf("%s: RefWhen scope=%v → %q, want %q", kind, rc.When.Equals, rc.RefKind, want)
			}
		}
	}

	// Non-overridable kinds must NOT carry the scope surface.
	for _, kind := range []string{"PlatformSpec", "ZoneSpec", "NodeSpec"} {
		m := LookupSchema(kind)
		if m == nil {
			t.Fatalf("%s: no schema registered", kind)
		}
		if find(m, "scope") != nil || find(m, "scope_instance") != nil {
			t.Errorf("%s: must not carry scope fields (not an overridable kind)", kind)
		}
	}
}
