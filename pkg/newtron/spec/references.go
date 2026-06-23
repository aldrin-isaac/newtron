package spec

import (
	"reflect"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/util"
)

// references.go — the referential-integrity framework for network specs.
//
// Every cross-spec reference is already declared once, as a `ref:"KindName"`
// struct tag on the referencing field (the same tags that drive the schema-form
// dropdowns). Every spec kind's storage is declared once, as a `kind:"KindName"`
// tag on its OverridableSpecs map. This file reflects over those two
// declarations to provide both directions of dependency checking generically —
// no per-kind, per-operation hand-coded scans:
//
//   - MissingRefs (forward): a spec being created/updated may only reference
//     specs that exist.
//   - FindConsumers (reverse): a spec may not be deleted while another spec
//     references it.
//
// Adding a new spec kind or a new reference is purely declarative (one map with
// a kind tag; one field with a ref tag); both checks then cover it with no new
// code.

// ReferenceError reports that a spec being created or updated references one or
// more specs that do not exist — the forward dependency check failing. The API
// maps it to HTTP 400 (the submitted spec is invalid).
type ReferenceError struct {
	Errors []string
}

func (e *ReferenceError) Error() string {
	return "unresolved references: " + strings.Join(e.Errors, "; ")
}

// SpecRef is a single reference from one spec to another, declared by a `ref:`
// tag. Name is canonicalized (util.NormalizeName) so it compares equal to the
// referenced spec's stored key regardless of how it was authored.
type SpecRef struct {
	Kind  string // referenced spec kind — the `ref:` tag value, e.g. "PrefixListSpec"
	Name  string // referenced spec's canonical name
	Field string // json path of the referencing field, for diagnostics
}

// Consumer names a spec that references a given target — the result of a reverse
// dependency scan.
type Consumer struct {
	Kind  string // the referencing spec's kind
	Name  string // the referencing spec's name
	Field string // the field that holds the reference
}

// CollectRefs returns every reference a spec value declares via `ref:` tags,
// recursing through nested structs (e.g. ServiceSpec.Routing) and slices (e.g.
// FilterSpec.Rules). Empty reference values are skipped — an unset optional
// reference is not a dependency. Non-struct values (a prefix list is []string)
// declare no references and yield nil.
func CollectRefs(value any) []SpecRef {
	var out []SpecRef
	collectRefs(reflect.ValueOf(value), &out)
	return out
}

func collectRefs(v reflect.Value, out *[]SpecRef) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			collectRefs(v.Elem(), out)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			collectRefs(v.Index(i), out)
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			fv := v.Field(i)
			if kind := sf.Tag.Get("ref"); kind != "" && fv.Kind() == reflect.String {
				if name := fv.String(); name != "" {
					*out = append(*out, SpecRef{
						Kind:  kind,
						Name:  util.NormalizeName(name),
						Field: jsonFieldName(sf),
					})
				}
				continue
			}
			collectRefs(fv, out)
		}
	}
}

// EachSpec invokes fn for every spec in the set, yielding its kind (from the
// map's `kind:` tag), name (the map key, as stored — already canonical), and
// value. The single point that enumerates spec storage; the integrity checks
// are built on it.
func (o *OverridableSpecs) EachSpec(fn func(kind, name string, value any)) {
	v := reflect.ValueOf(o).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		kind := t.Field(i).Tag.Get("kind")
		if kind == "" {
			continue
		}
		m := v.Field(i)
		if m.Kind() != reflect.Map {
			continue
		}
		for _, key := range m.MapKeys() {
			fn(kind, key.String(), m.MapIndex(key).Interface())
		}
	}
}

// HasSpec reports whether a spec of the given kind and (canonical) name exists.
func (o *OverridableSpecs) HasSpec(kind, name string) bool {
	found := false
	o.EachSpec(func(k, n string, _ any) {
		if k == kind && n == name {
			found = true
		}
	})
	return found
}

// MissingRefs returns the references in value that do not resolve to an existing
// spec in the set — the forward dependency check for create/update. Empty when
// every reference resolves.
func (o *OverridableSpecs) MissingRefs(value any) []SpecRef {
	var missing []SpecRef
	for _, ref := range CollectRefs(value) {
		if !o.HasSpec(ref.Kind, ref.Name) {
			missing = append(missing, ref)
		}
	}
	return missing
}

// FindConsumers returns every spec in the set that references the target kind +
// (canonical) name — the reverse dependency check for delete. Empty when nothing
// references the target.
func (o *OverridableSpecs) FindConsumers(targetKind, targetName string) []Consumer {
	var consumers []Consumer
	o.EachSpec(func(kind, name string, value any) {
		for _, ref := range CollectRefs(value) {
			if ref.Kind == targetKind && ref.Name == targetName {
				consumers = append(consumers, Consumer{Kind: kind, Name: name, Field: ref.Field})
			}
		}
	})
	return consumers
}

// jsonFieldName returns the wire name of a struct field (its json tag, minus
// options), falling back to the Go field name.
func jsonFieldName(sf reflect.StructField) string {
	tag := sf.Tag.Get("json")
	if tag == "" {
		return sf.Name
	}
	if comma := strings.IndexByte(tag, ','); comma >= 0 {
		tag = tag[:comma]
	}
	if tag == "" {
		return sf.Name
	}
	return tag
}
