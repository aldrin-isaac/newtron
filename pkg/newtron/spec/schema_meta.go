// Package spec — schema metadata extraction.
//
// SchemaMeta is the canonical description of a spec type that UIs consume to
// render forms: per-field human label, tooltip, type hint, required-ness,
// enum values, and references to other spec kinds for dropdown rendering.
//
// The metadata is derived once at boot from Go struct tags on the spec types
// themselves — the field definition is the only source of truth, so the
// label and tooltip cannot drift from the schema they describe. Every UI
// that consumes /newtron/v1/schema renders the same vocabulary by default;
// per-locale i18n overrides stay at the UI layer.
//
// Tag conventions on each spec field:
//
//	json:"wire_name,omitempty"  // required-ness inferred from omitempty
//	label:"Human Label"          // form-field label
//	tooltip:"Extended help text"  // hover/help text
//	enum:"value1,value2,value3"  // for fixed-vocabulary string fields
//	ref:"KindName"               // names another spec kind (UI renders dropdown)
//	item_kind:"KindName"         // for arrays/maps of objects: element kind
//
// Required-ness rule: a field is required iff its json tag is missing the
// ",omitempty" option AND it is not embedded. Pointer fields are always
// optional regardless of tag (nil is meaningful). This matches the existing
// convention in pkg/newtron/spec/types.go.
package spec

import (
	"reflect"
	"strings"
)

// FieldMeta describes one field of a spec type.
type FieldMeta struct {
	Name        string   `json:"name"`                  // wire name (from json tag)
	Label       string   `json:"label"`                 // human-readable label
	Description string   `json:"description,omitempty"` // tooltip / extended help
	Type        string   `json:"type"`                  // string|int|bool|enum|array|map|object|ref
	Required    bool     `json:"required"`
	Enum        []string `json:"enum,omitempty"`      // for type=enum
	RefKind     string   `json:"ref_kind,omitempty"`  // for type=ref — UI renders dropdown of this kind's names
	ItemType    string   `json:"item_type,omitempty"` // for type=array|map — primitive element type
	ItemKind    string   `json:"item_kind,omitempty"` // for type=array|map of objects — element kind name

	// Validation hints (UI client-side validation; server still validates
	// server-side on POST). All optional.
	Pattern   string `json:"pattern,omitempty"`   // regex the value must match
	Min       *int   `json:"min,omitempty"`       // inclusive lower bound for type=int
	Max       *int   `json:"max,omitempty"`       // inclusive upper bound for type=int
	Format    string `json:"format,omitempty"`    // semantic hint — "cidr", "ipv4", "ipv6", "mac", "asn"
	Immutable bool   `json:"immutable,omitempty"` // value is fixed at create time — UI suppresses edit affordance in update-mode forms
}

// SchemaPaths declares the HTTP path templates a UI uses to drive a kind
// end-to-end. Path components in braces (`{netID}`, `{name}`) are
// substituted by the UI at request time. Omitted paths mean the verb
// doesn't exist for this kind:
//
//   - PlatformSpec is read-only: List + Show populated, Create/Update/Delete
//     omitted.
//   - Sub-rule kinds (FilterRule, QoSQueue, …) aren't top-level
//     addressable: List + Show omitted, Create/Update/Delete carry the
//     add-X / update-X / remove-X verbs that take the parent's name in
//     the request body (see ParentIdentifierField on SchemaMeta).
//   - PrefixListEntry has no Update verb (per §47 there are no other
//     mutable fields).
type SchemaPaths struct {
	List   string `json:"list,omitempty"`   // GET — enumerate names
	Show   string `json:"show,omitempty"`   // GET — fetch one named instance
	Create string `json:"create,omitempty"` // POST — create
	Update string `json:"update,omitempty"` // POST — replace fields in place
	Delete string `json:"delete,omitempty"` // POST — remove
}

// SchemaMeta describes one spec type as a whole — the kind, its fields,
// and the URL+identity metadata a UI needs to drive create/update/delete
// without hardcoded mappings.
type SchemaMeta struct {
	Kind        string      `json:"kind"`                  // Go type name (e.g. "ServiceSpec")
	Label       string      `json:"label"`                 // human label for the kind
	Description string      `json:"description,omitempty"` // tooltip for the kind
	Fields      []FieldMeta `json:"fields"`

	// Identifier names the field that addresses one row of this kind. For
	// most top-level kinds it's "name"; sub-rules use "seq" / "queue_id" /
	// "prefix". UIs use this to suppress the identifier field in edit-mode
	// forms (the URL carries it) and to detect rename / renumber against
	// the source row.
	Identifier string `json:"identifier,omitempty"`

	// ParentRef names the wire field a sub-rule's request body uses to
	// identify its parent (e.g. FilterRule's add/update/remove bodies
	// carry `filter: "<parent_name>"`). Empty for top-level kinds.
	ParentRef string `json:"parent_ref,omitempty"`

	// Paths carries the HTTP path templates for this kind's CRUD verbs.
	// `omitzero` (Go 1.24+) drops the whole object when every path is
	// empty — embedded-only kinds (RoutingSpec, RoutePolicySet, EVPNConfig)
	// have no paths and shouldn't surface a noisy empty object on the wire.
	Paths SchemaPaths `json:"paths,omitzero"`
}

// SchemaRegistration carries everything required to register a spec kind
// with the schema metadata endpoint. Passed to RegisterSchemaKind at
// init() time.
//
// Sample is a zero value of the kind's Go type; reflection on its tags
// drives the per-field metadata. Identifier / ParentRef / Paths are
// static metadata the kind's struct tags cannot express.
//
// IdentifierField is optional and used only when the identifier isn't
// already a field on the Sample struct — sub-rule kinds whose identifier
// is implicit in the parent's representation (e.g. QoSQueue's queue_id
// is the array index in QoSPolicy.Queues). When non-nil, this FieldMeta
// is prepended to the extracted field list so universal UIs see a
// complete form shape.
type SchemaRegistration struct {
	Kind            string
	Label           string
	Description     string
	Sample          any
	Identifier      string
	IdentifierField *FieldMeta
	ParentRef       string
	Paths           SchemaPaths
}

// schemaKind carries a registered kind's reflect.Type plus the static
// metadata that doesn't come from struct tags. The registry hands these
// to buildSchemaMeta to produce the final SchemaMeta document.
type schemaKind struct {
	t               reflect.Type
	label           string
	description     string
	identifier      string
	identifierField *FieldMeta
	parentRef       string
	paths           SchemaPaths
}

// schemaRegistry holds every spec kind that participates in the schema
// metadata endpoint. Populated by RegisterSchemaKind at init() time from
// the spec package; SchemaMeta docs are built on demand so registration
// order doesn't matter.
var schemaRegistry = map[string]schemaKind{}

// RegisterSchemaKind makes a spec type available to the schema metadata
// endpoint. Call at init() time:
//
//	func init() {
//	    RegisterSchemaKind(SchemaRegistration{
//	        Kind:        "IPVPNSpec",
//	        Label:       "IP-VPN",
//	        Description: "A Layer-3 VPN — VRF + L3VNI + route targets.",
//	        Sample:      IPVPNSpec{},
//	        Identifier:  "name",
//	        Paths: SchemaPaths{
//	            List:   "/newtron/v1/networks/{netID}/ipvpns",
//	            Show:   "/newtron/v1/networks/{netID}/ipvpns/{name}",
//	            Create: "/newtron/v1/networks/{netID}/create-ipvpn",
//	            Update: "/newtron/v1/networks/{netID}/update-ipvpn",
//	            Delete: "/newtron/v1/networks/{netID}/delete-ipvpn",
//	        },
//	    })
//	}
func RegisterSchemaKind(reg SchemaRegistration) {
	t := reflect.TypeOf(reg.Sample)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	schemaRegistry[reg.Kind] = schemaKind{
		t:               t,
		label:           reg.Label,
		description:     reg.Description,
		identifier:      reg.Identifier,
		identifierField: reg.IdentifierField,
		parentRef:       reg.ParentRef,
		paths:           reg.Paths,
	}
}

// ListSchemaKinds returns every registered kind's name, sorted for stable
// iteration. Used by the schema list endpoint.
func ListSchemaKinds() []string {
	names := make([]string, 0, len(schemaRegistry))
	for name := range schemaRegistry {
		names = append(names, name)
	}
	// Stable order — callers (HTTP responses, tests) compare against fixed
	// slices.
	sortStrings(names)
	return names
}

// LookupSchema returns the SchemaMeta for one registered kind, or nil if
// the kind isn't registered. The returned value is a fresh copy each call
// — UIs may mutate the slice/fields without affecting the registry.
func LookupSchema(kind string) *SchemaMeta {
	sk, ok := schemaRegistry[kind]
	if !ok {
		return nil
	}
	meta := buildSchemaMeta(sk)
	return &meta
}

// buildSchemaMeta walks a reflect.Type and produces its SchemaMeta. Exposed
// for tests; production code goes through LookupSchema.
func buildSchemaMeta(sk schemaKind) SchemaMeta {
	fields := extractFields(sk.t)
	// Prepend a synthetic identifier field for sub-rule kinds whose
	// identifier is implicit in the parent's representation (e.g.
	// QoSQueue's queue_id is the array index, not a struct field).
	if sk.identifierField != nil {
		fields = append([]FieldMeta{*sk.identifierField}, fields...)
	}
	meta := SchemaMeta{
		Kind:        sk.t.Name(),
		Label:       sk.label,
		Description: sk.description,
		Identifier:  sk.identifier,
		ParentRef:   sk.parentRef,
		Paths:       sk.paths,
		Fields:      fields,
	}
	return meta
}

// extractFields walks a struct's fields and produces a FieldMeta for each
// exported, JSON-visible field. Embedded structs are flattened — their
// fields appear at the outer level the same way encoding/json sees them.
func extractFields(t reflect.Type) []FieldMeta {
	if t.Kind() != reflect.Struct {
		return nil
	}
	var fields []FieldMeta
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		// Anonymous (embedded) fields flatten regardless of whether the
		// embedded TYPE is exported — encoding/json promotes their
		// exported fields the same way. Check anonymity first.
		if sf.Anonymous {
			fields = append(fields, extractFields(sf.Type)...)
			continue
		}
		if !sf.IsExported() {
			continue
		}
		jsonTag := sf.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}
		wireName, hasOmitEmpty := parseJSONTag(jsonTag, sf.Name)
		if wireName == "" {
			continue
		}
		fm := FieldMeta{
			Name:        wireName,
			Label:       sf.Tag.Get("label"),
			Description: sf.Tag.Get("tooltip"),
			Required:    !hasOmitEmpty && sf.Type.Kind() != reflect.Ptr,
			Pattern:     sf.Tag.Get("pattern"),
			Format:      sf.Tag.Get("format"),
			Immutable:   sf.Tag.Get("immutable") == "true",
		}
		if minTag := sf.Tag.Get("min"); minTag != "" {
			if v, ok := parseIntTag(minTag); ok {
				fm.Min = &v
			}
		}
		if maxTag := sf.Tag.Get("max"); maxTag != "" {
			if v, ok := parseIntTag(maxTag); ok {
				fm.Max = &v
			}
		}
		annotateType(&fm, sf)
		// Default label when no `label:` tag — title-case the wire name so
		// at least something renders. The struct-tag value takes precedence.
		if fm.Label == "" {
			fm.Label = humanizeName(wireName)
		}
		fields = append(fields, fm)
	}
	return fields
}

// annotateType inspects a struct field's reflect.Type and explicit tag
// hints (enum, ref, item_kind) to populate FieldMeta.Type, Enum, RefKind,
// ItemType, ItemKind.
func annotateType(fm *FieldMeta, sf reflect.StructField) {
	if enumTag := sf.Tag.Get("enum"); enumTag != "" {
		fm.Type = "enum"
		fm.Enum = splitCSV(enumTag)
		return
	}
	if refTag := sf.Tag.Get("ref"); refTag != "" {
		fm.Type = "ref"
		fm.RefKind = refTag
		return
	}
	t := sf.Type
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		fm.Type = "string"
	case reflect.Bool:
		fm.Type = "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		fm.Type = "int"
	case reflect.Float32, reflect.Float64:
		fm.Type = "float"
	case reflect.Slice, reflect.Array:
		fm.Type = "array"
		populateItemKind(fm, sf, t.Elem())
	case reflect.Map:
		fm.Type = "map"
		populateItemKind(fm, sf, t.Elem())
	case reflect.Struct:
		fm.Type = "object"
		if itemKind := sf.Tag.Get("item_kind"); itemKind != "" {
			fm.ItemKind = itemKind
		} else if t.Name() != "" {
			fm.ItemKind = t.Name()
		}
	default:
		fm.Type = "string"
	}
}

// populateItemKind fills in ItemType (primitive element) or ItemKind
// (object element) for array/map FieldMeta. The `item_kind` tag overrides
// the inferred kind name — useful when the element type is a pointer or
// interface that reflect can't name directly.
func populateItemKind(fm *FieldMeta, sf reflect.StructField, elem reflect.Type) {
	if itemKind := sf.Tag.Get("item_kind"); itemKind != "" {
		fm.ItemKind = itemKind
		return
	}
	if elem.Kind() == reflect.Ptr {
		elem = elem.Elem()
	}
	switch elem.Kind() {
	case reflect.String:
		fm.ItemType = "string"
	case reflect.Bool:
		fm.ItemType = "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		fm.ItemType = "int"
	case reflect.Float32, reflect.Float64:
		fm.ItemType = "float"
	case reflect.Struct:
		if elem.Name() != "" {
			fm.ItemKind = elem.Name()
		}
	}
}

// parseJSONTag splits a json tag into (wire name, omitempty flag). An
// empty tag means use the Go field name (the encoding/json fallback).
func parseJSONTag(tag, fieldName string) (string, bool) {
	if tag == "" {
		return fieldName, false
	}
	parts := strings.Split(tag, ",")
	name := parts[0]
	if name == "" {
		name = fieldName
	}
	omitempty := false
	for _, p := range parts[1:] {
		if p == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty
}

// parseIntTag parses a struct-tag integer value. Returns (value, true) on
// success; (0, false) on parse failure so the caller leaves the field
// unset (a malformed tag value is a bug at compile time — the silent
// skip is acceptable because the test suite catches it).
func parseIntTag(s string) (int, bool) {
	neg := false
	i := 0
	if i < len(s) && s[i] == '-' {
		neg = true
		i++
	}
	if i == len(s) {
		return 0, false
	}
	v := 0
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + int(c-'0')
	}
	if neg {
		v = -v
	}
	return v, true
}

// splitCSV trims whitespace around each comma-separated value.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// humanizeName converts a snake_case wire name to Title Case for use as a
// default label when no `label:` tag is set. "vrf_name" → "VRF Name",
// "src_ip" → "Src IP". Special-cases common acronyms (IP, VRF, ASN, BGP,
// VPN, ACL, AS, DSCP, ECN, VLAN, VNI, MAC, EVPN, ID, QoS) so they render
// in canonical form rather than "Vrf Name."
func humanizeName(name string) string {
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if up, ok := acronyms[strings.ToLower(p)]; ok {
			parts[i] = up
			continue
		}
		if len(p) == 0 {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

var acronyms = map[string]string{
	"ip":   "IP",
	"vrf":  "VRF",
	"asn":  "ASN",
	"as":   "AS",
	"bgp":  "BGP",
	"vpn":  "VPN",
	"acl":  "ACL",
	"dscp": "DSCP",
	"ecn":  "ECN",
	"vlan": "VLAN",
	"vni":  "VNI",
	"mac":  "MAC",
	"evpn": "EVPN",
	"id":   "ID",
	"qos":  "QoS",
	"irb":  "IRB",
	"sag":  "SAG",
	"cidr": "CIDR",
	"med":  "MED",
	"cos":  "CoS",
	"tc":   "TC",
	"l2":   "L2",
	"l3":   "L3",
	"l2vni": "L2VNI",
	"l3vni": "L3VNI",
}

// sortStrings — small wrapper so test code reads cleanly. Standard sort
// would work too; this avoids the import in this file.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
