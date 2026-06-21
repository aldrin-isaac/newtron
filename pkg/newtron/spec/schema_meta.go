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
	"fmt"
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
	ReadOnly  bool   `json:"read_only,omitempty"` // value is derived/computed server-side — UI displays it but never offers an input and never submits it (e.g. an IP-VPN's vrf_name)

	// RequiredWhen declares a predicate over sibling field values that —
	// when true — makes this field required even though the static
	// `required` is false. UIs evaluate against the live form state and
	// toggle the input's required affordance. Newtron does NOT evaluate
	// this server-side at request time; the existing 400 on missing
	// required field is the back-stop. See type RequiredWhen.
	RequiredWhen *RequiredWhen `json:"required_when,omitempty"`

	// AppliesWhen declares a predicate over sibling field values that —
	// when false — makes this field NOT APPLICABLE to the current form
	// shape. UIs hide or disable the field and omit it from the submitted
	// payload. This is a different axis from RequiredWhen: required_when
	// answers "must this be filled?", applies_when answers "is this field
	// relevant at all?". A static service's peer_as isn't "required when
	// bgp" — it's "not applicable when static", which is stronger (the
	// field disappears, not just loses its required affordance).
	//
	// Applicability gates requiredness: a field whose AppliesWhen is false
	// is treated as absent, so RequiredWhen is not consulted for it. The
	// two compose without contradiction.
	//
	// Same tree grammar as RequiredWhen (atomic Field+operand / AllOf /
	// AnyOf), validated at registration time. Newtron does NOT evaluate it
	// server-side; the apply path naturally ignores fields irrelevant to
	// the chosen shape (e.g. a static service never reads peer_as).
	AppliesWhen *RequiredWhen `json:"applies_when,omitempty"`
}

// RequiredWhen is a conditional-required predicate evaluated against the
// form's sibling field values. Scope is the current SchemaMeta — nested
// forms (RoutingSpec inside ServiceSpec) evaluate against their own
// sibling set, not the parent's.
//
// One of two shapes per node — never both:
//
//	Atomic.    Field names a sibling on the same SchemaMeta. Exactly
//	           one of Equals / NotEquals / In / NotIn is set; the
//	           predicate compares the sibling's current form value
//	           against that operand.
//
//	Combinator. Exactly one of AllOf / AnyOf is set. The condition is
//	           the conjunction / disjunction of the listed sub-conditions.
//
// `required: true` wins over `required_when` — the evaluator only
// consults RequiredWhen when the static Required is false, so the two
// never contradict.
//
// When the referenced sibling has no value yet (operator hasn't filled
// it in), atomic conditions evaluate against the field's zero value
// for its declared type. So a `service_type in ("evpn-irb")` predicate
// reads as `false` for an unfilled `service_type` — required-ness
// can't trigger on an unspecified state.
type RequiredWhen struct {
	// Atomic shape — Field references a sibling by wire name.
	Field     string `json:"field,omitempty"`
	Equals    any    `json:"equals,omitempty"`
	NotEquals any    `json:"not_equals,omitempty"`
	In        []any  `json:"in,omitempty"`
	NotIn     []any  `json:"not_in,omitempty"`

	// Combinator shape — exactly one of AllOf / AnyOf is set.
	AllOf []*RequiredWhen `json:"all_of,omitempty"`
	AnyOf []*RequiredWhen `json:"any_of,omitempty"`
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

	// RequiredWhen maps a target wire field name (e.g. "ipvpn") to the
	// conditional-required predicate UIs evaluate against the form's
	// sibling values. Init-time validation walks the map and panics on
	// any unknown field reference — both the map key and every Field
	// referenced inside the predicate must exist as a wire name on the
	// kind's Sample struct (or as the synthetic IdentifierField). Typos
	// fail at server start rather than silently in the UI.
	RequiredWhen map[string]*RequiredWhen

	// AppliesWhen maps a target wire field name to the field-applicability
	// predicate (see FieldMeta.AppliesWhen). Same tree grammar and the
	// same init-time validation as RequiredWhen.
	AppliesWhen map[string]*RequiredWhen

	// ComputedFields are read-only, server-derived fields that are not part
	// of the kind's Sample struct (so they're never authored or persisted)
	// but are surfaced in the schema and the kind's API view so UIs can
	// display them. Each should have ReadOnly set. Appended after the
	// struct-reflected fields. Example: an IP-VPN's vrf_name, derived as
	// "Vrf_"+name.
	ComputedFields []FieldMeta
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
	requiredWhen    map[string]*RequiredWhen
	appliesWhen     map[string]*RequiredWhen
	computedFields  []FieldMeta
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
	if len(reg.RequiredWhen) > 0 {
		validateConditionMap(reg.Kind, "RequiredWhen", reg.RequiredWhen, reg.IdentifierField, t)
	}
	if len(reg.AppliesWhen) > 0 {
		validateConditionMap(reg.Kind, "AppliesWhen", reg.AppliesWhen, reg.IdentifierField, t)
	}
	schemaRegistry[reg.Kind] = schemaKind{
		t:               t,
		label:           reg.Label,
		description:     reg.Description,
		identifier:      reg.Identifier,
		identifierField: reg.IdentifierField,
		parentRef:       reg.ParentRef,
		paths:           reg.Paths,
		requiredWhen:    reg.RequiredWhen,
		appliesWhen:     reg.AppliesWhen,
		computedFields:  reg.ComputedFields,
	}
}

// validateConditionMap walks a conditional-predicate map (RequiredWhen or
// AppliesWhen — both share the RequiredWhen tree grammar) at registration
// time and panics on any reference to a wire field name that doesn't exist
// on the kind's Sample struct (plus its synthetic IdentifierField if set).
// Atomic-vs-combinator shape XOR is enforced too. The `label` argument
// ("RequiredWhen" / "AppliesWhen") is woven into the panic so the message
// points at the broken registration site and the offending key.
//
// Runs ONCE at init() time when RegisterSchemaKind is called. Per the
// agreement with newtcon, newtron does not evaluate either predicate at
// request time — this init pass is the only server-side safety net
// catching typos before the UI does.
func validateConditionMap(kind, label string, m map[string]*RequiredWhen, identifierField *FieldMeta, t reflect.Type) {
	known := wireNames(t)
	if identifierField != nil {
		known[identifierField.Name] = struct{}{}
	}
	for target, cond := range m {
		if _, ok := known[target]; !ok {
			panic(schemaRegistrationError(kind, "%s[%q]: target field is not a wire name on %s",
				label, target, t.Name()))
		}
		if cond == nil {
			panic(schemaRegistrationError(kind, "%s[%q]: nil condition", label, target))
		}
		if err := validateRequiredWhenCondition(cond, known); err != "" {
			panic(schemaRegistrationError(kind, "%s[%q]: %s", label, target, err))
		}
	}
}

// validateRequiredWhenCondition returns "" when the condition is well-formed,
// or an actionable error message describing the first problem found.
// Walks combinators recursively against the same `known` field set —
// nested conditions still reference siblings on the OUTER form (newtcon's
// "scope is current form's siblings" rule).
func validateRequiredWhenCondition(c *RequiredWhen, known map[string]struct{}) string {
	if c == nil {
		return "nil condition"
	}
	atomic := c.Field != "" || c.Equals != nil || c.NotEquals != nil || c.In != nil || c.NotIn != nil
	combinator := len(c.AllOf) > 0 || len(c.AnyOf) > 0
	switch {
	case !atomic && !combinator:
		return "empty condition (must set Field+operand, AllOf, or AnyOf)"
	case atomic && combinator:
		return "mixed shape (cannot set both atomic Field/operand and combinator AllOf/AnyOf on the same node)"
	case atomic:
		if c.Field == "" {
			return "atomic condition missing Field"
		}
		if _, ok := known[c.Field]; !ok {
			return "atomic condition references unknown field " + c.Field
		}
		operands := 0
		if c.Equals != nil {
			operands++
		}
		if c.NotEquals != nil {
			operands++
		}
		if c.In != nil {
			operands++
		}
		if c.NotIn != nil {
			operands++
		}
		switch operands {
		case 0:
			return "atomic condition missing operand (set one of Equals / NotEquals / In / NotIn)"
		case 1:
			return ""
		default:
			return "atomic condition has multiple operands (set exactly one of Equals / NotEquals / In / NotIn)"
		}
	default: // combinator
		if len(c.AllOf) > 0 && len(c.AnyOf) > 0 {
			return "combinator mixes AllOf and AnyOf on the same node"
		}
		children := c.AllOf
		if len(c.AnyOf) > 0 {
			children = c.AnyOf
		}
		for i, sub := range children {
			if msg := validateRequiredWhenCondition(sub, known); msg != "" {
				return fmt.Sprintf("child %d: %s", i, msg)
			}
		}
		return ""
	}
}

// wireNames collects the JSON wire names of every exported, json-tagged
// field on a struct type. Embedded structs are flattened the same way
// extractFields() flattens them.
func wireNames(t reflect.Type) map[string]struct{} {
	out := make(map[string]struct{})
	collectWireNames(t, out)
	return out
}

func collectWireNames(t reflect.Type, out map[string]struct{}) {
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.Anonymous {
			collectWireNames(sf.Type, out)
			continue
		}
		if !sf.IsExported() {
			continue
		}
		jsonTag := sf.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}
		name, _ := parseJSONTag(jsonTag, sf.Name)
		if name != "" {
			out[name] = struct{}{}
		}
	}
}

func schemaRegistrationError(kind, format string, args ...any) string {
	return fmt.Sprintf("RegisterSchemaKind(%s): ", kind) + fmt.Sprintf(format, args...)
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
	// Append read-only, server-derived fields (not on the Sample struct).
	fields = append(fields, sk.computedFields...)
	// Attach RequiredWhen / AppliesWhen predicates to their target fields.
	// Validated at registration time, so every key here matches a real field.
	for i := range fields {
		if cond, ok := sk.requiredWhen[fields[i].Name]; ok {
			fields[i].RequiredWhen = cond
		}
		if cond, ok := sk.appliesWhen[fields[i].Name]; ok {
			fields[i].AppliesWhen = cond
		}
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
