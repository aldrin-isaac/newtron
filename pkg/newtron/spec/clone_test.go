package spec

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestOverridableSpecsClone pins what Clone relies on: every type reachable
// from OverridableSpecs survives a JSON round trip unchanged, and the copy
// shares nothing with the original.
func TestOverridableSpecsClone(t *testing.T) {
	// No reachable field may be dropped by its JSON tag or re-encoded by a
	// custom marshaller, or the round trip would silently lose it.
	marshaler := reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	unmarshaler := reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type, string)
	walk = func(typ reflect.Type, path string) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
			typ = typ.Elem()
		}
		if seen[typ] {
			return
		}
		seen[typ] = true
		if typ.Implements(marshaler) || reflect.PointerTo(typ).Implements(unmarshaler) {
			t.Errorf("%s (%s) customizes its JSON encoding; Clone's round trip may not preserve it", path, typ)
		}
		if typ.Kind() != reflect.Struct {
			return
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				t.Errorf("%s.%s is unexported; JSON drops it", path, f.Name)
				continue
			}
			if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name == "-" {
				t.Errorf("%s.%s is tagged json:\"-\"; JSON drops it", path, f.Name)
			}
			walk(f.Type, path+"."+f.Name)
		}
	}
	walk(reflect.TypeOf(OverridableSpecs{}), "OverridableSpecs")

	orig := &OverridableSpecs{
		PrefixLists: map[string][]string{"PL": {"10.0.0.0/8"}},
		QoSPolicies: map[string]*QoSPolicy{"GOLD": {Queues: []*QoSQueue{
			{Name: "q0", Type: "dwrr", Weight: 50, DSCP: []int{0}}, nil,
		}}},
		Services: map[string]*ServiceSpec{"TRANSIT": {ServiceType: "routed", Description: "d"}},
	}
	cp, err := orig.Clone()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(orig, cp) {
		t.Fatalf("clone differs from original:\norig %+v\ncopy %+v", orig, cp)
	}

	cp.PrefixLists["PL"][0] = "changed"
	cp.QoSPolicies["GOLD"].Queues[0].Weight = 1
	cp.Services["TRANSIT"].Description = "changed"
	if orig.PrefixLists["PL"][0] != "10.0.0.0/8" || orig.QoSPolicies["GOLD"].Queues[0].Weight != 50 ||
		orig.Services["TRANSIT"].Description != "d" {
		t.Error("editing the clone changed the original; Clone shares memory")
	}
}
