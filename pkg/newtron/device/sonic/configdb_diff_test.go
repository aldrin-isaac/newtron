package sonic

import (
	"testing"
)

func TestDiffConfigDB_NoDifferences(t *testing.T) {
	expected := RawConfigDB{
		"VLAN": {
			"Vlan100": {"vlanid": "100"},
		},
	}
	actual := RawConfigDB{
		"VLAN": {
			"Vlan100": {"vlanid": "100"},
		},
	}
	diffs := DiffConfigDB(expected, actual, []string{"VLAN"})
	if len(diffs) != 0 {
		t.Errorf("expected no diffs, got %d: %+v", len(diffs), diffs)
	}
}

func TestDiffConfigDB_MissingEntry(t *testing.T) {
	expected := RawConfigDB{
		"VLAN": {
			"Vlan100": {"vlanid": "100"},
		},
	}
	actual := RawConfigDB{}

	diffs := DiffConfigDB(expected, actual, []string{"VLAN"})
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	if diffs[0].Type != "missing" {
		t.Errorf("expected type=missing, got %s", diffs[0].Type)
	}
	if diffs[0].Table != "VLAN" || diffs[0].Key != "Vlan100" {
		t.Errorf("wrong entry: %s|%s", diffs[0].Table, diffs[0].Key)
	}
}

func TestDiffConfigDB_ExtraEntry(t *testing.T) {
	expected := RawConfigDB{}
	actual := RawConfigDB{
		"VLAN": {
			"Vlan999": {"vlanid": "999"},
		},
	}

	diffs := DiffConfigDB(expected, actual, []string{"VLAN"})
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	if diffs[0].Type != "extra" {
		t.Errorf("expected type=extra, got %s", diffs[0].Type)
	}
}

func TestDiffConfigDB_ModifiedEntry(t *testing.T) {
	expected := RawConfigDB{
		"BGP_GLOBALS": {
			"default": {"local_asn": "65001", "router_id": "10.0.0.1"},
		},
	}
	actual := RawConfigDB{
		"BGP_GLOBALS": {
			"default": {"local_asn": "65099", "router_id": "10.0.0.1"},
		},
	}

	diffs := DiffConfigDB(expected, actual, []string{"BGP_GLOBALS"})
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	if diffs[0].Type != "modified" {
		t.Errorf("expected type=modified, got %s", diffs[0].Type)
	}
	if diffs[0].Expected["local_asn"] != "65001" || diffs[0].Actual["local_asn"] != "65099" {
		t.Errorf("wrong field values in diff")
	}
}

func TestDiffConfigDB_SkipsUnownedTables(t *testing.T) {
	expected := RawConfigDB{}
	actual := RawConfigDB{
		"NTP": {
			"server1": {"address": "1.2.3.4"},
		},
	}

	// NTP is not in ownedTables
	diffs := DiffConfigDB(expected, actual, []string{"VLAN", "VRF"})
	if len(diffs) != 0 {
		t.Errorf("expected no diffs for unowned table, got %d", len(diffs))
	}
}

func TestDiffConfigDB_SkipsExcludedTables(t *testing.T) {
	expected := RawConfigDB{
		"NEWTRON_INTENT": {
			"leaf1": {"holder": "admin@mgmt"},
		},
	}
	actual := RawConfigDB{}

	diffs := DiffConfigDB(expected, actual, []string{"NEWTRON_INTENT"})
	if len(diffs) != 0 {
		t.Errorf("expected no diffs for excluded table, got %d", len(diffs))
	}
}

func TestDiffConfigDB_MultipleDifferences(t *testing.T) {
	expected := RawConfigDB{
		"VLAN": {
			"Vlan100": {"vlanid": "100"},
			"Vlan200": {"vlanid": "200"},
		},
		"VRF": {
			"CUSTOMER": {"vni": "1001"},
		},
	}
	actual := RawConfigDB{
		"VLAN": {
			"Vlan100": {"vlanid": "100"},
			"Vlan999": {"vlanid": "999"},
		},
		"VRF": {
			"CUSTOMER": {"vni": "9999"},
		},
	}

	diffs := DiffConfigDB(expected, actual, []string{"VLAN", "VRF"})
	// Missing: Vlan200, Extra: Vlan999, Modified: CUSTOMER VRF
	if len(diffs) != 3 {
		t.Fatalf("expected 3 diffs, got %d: %+v", len(diffs), diffs)
	}

	// Check sorted order (table then key)
	types := map[string]int{}
	for _, d := range diffs {
		types[d.Type]++
	}
	if types["missing"] != 1 || types["extra"] != 1 || types["modified"] != 1 {
		t.Errorf("wrong diff type counts: %v", types)
	}
}

func TestFieldsMatch_SubsetWithExtra(t *testing.T) {
	expected := map[string]string{"x": "1"}
	actual := map[string]string{"x": "1", "y": "2"}
	if !fieldsMatch(expected, actual) {
		t.Error("expected is a subset of actual — should match")
	}
}

func TestFieldsMatch_SameContent(t *testing.T) {
	a := map[string]string{"x": "1", "y": "2"}
	b := map[string]string{"y": "2", "x": "1"}
	if !fieldsMatch(a, b) {
		t.Error("same content should match")
	}
}

func TestFieldsMatch_ValueDiffers(t *testing.T) {
	expected := map[string]string{"x": "1"}
	actual := map[string]string{"x": "2"}
	if fieldsMatch(expected, actual) {
		t.Error("different value for expected field — should not match")
	}
}

func TestFieldsMatch_MissingInActual(t *testing.T) {
	expected := map[string]string{"x": "1", "y": "2"}
	actual := map[string]string{"x": "1"}
	if fieldsMatch(expected, actual) {
		t.Error("actual missing expected field — should not match")
	}
}

func TestOwnedTables_ContainsCriticalTables(t *testing.T) {
	tables := OwnedTables()
	required := map[string]bool{
		"VLAN": false, "VRF": false, "BGP_GLOBALS": false,
	}
	for _, table := range tables {
		if _, ok := required[table]; ok {
			required[table] = true
		}
	}
	for table, found := range required {
		if !found {
			t.Errorf("critical table %s missing from OwnedTables()", table)
		}
	}
}

func TestOwnedTables_ExcludesInternalTables(t *testing.T) {
	tables := OwnedTables()
	excluded := map[string]bool{
		"NEWTRON_INTENT":   true,
		"NEWTRON_HISTORY":  true,
		"NEWTRON_SETTINGS": true,
	}
	for _, table := range tables {
		if excluded[table] {
			t.Errorf("OwnedTables should exclude %s", table)
		}
	}
}

func TestTablePriority_AllSchemaTablesHavePriority(t *testing.T) {
	for table := range Schema {
		if _, ok := tablePriority[table]; !ok {
			t.Errorf("table %s in Schema has no entry in tablePriority", table)
		}
	}
}

func TestTablePriority_ParentBeforeChild(t *testing.T) {
	// Verify YANG leafref chains: parent priority < child priority.
	chains := [][2]string{
		{"VLAN", "VLAN_MEMBER"},
		{"VLAN", "VLAN_INTERFACE"},
		{"VRF", "BGP_GLOBALS"},
		{"BGP_GLOBALS", "BGP_NEIGHBOR"},
		{"BGP_NEIGHBOR", "BGP_NEIGHBOR_AF"},
		{"BGP_GLOBALS", "BGP_GLOBALS_AF"},
		{"VXLAN_TUNNEL", "VXLAN_EVPN_NVO"},
		{"VXLAN_EVPN_NVO", "VXLAN_TUNNEL_MAP"},
		{"ACL_TABLE", "ACL_RULE"},
		{"BGP_PEER_GROUP", "BGP_PEER_GROUP_AF"},
	}
	for _, pair := range chains {
		parent, child := pair[0], pair[1]
		if TablePriority(parent) >= TablePriority(child) {
			t.Errorf("%s (priority %d) should be lower than %s (priority %d)",
				parent, TablePriority(parent), child, TablePriority(child))
		}
	}
}

func TestExportRaw_RoundTrip(t *testing.T) {
	// Build a RawConfigDB with known data across three tables. Field names must
	// match the json tags on the corresponding typed structs so they survive the
	// hydrate → structToFields round-trip without loss.
	want := RawConfigDB{
		"VLAN": {
			"Vlan100": {"vlanid": "100", "description": "uplink"},
			"Vlan200": {"vlanid": "200"},
		},
		"VRF": {
			"CUSTOMER": {"vni": "1001"},
			"MGMT":     {"vni": "1002"},
		},
		"BGP_GLOBALS": {
			"default": {"local_asn": "65001", "router_id": "10.0.0.1"},
		},
	}

	db := NewConfigDB()
	for table, keys := range want {
		for key, fields := range keys {
			db.ApplyEntries([]Entry{{Table: table, Key: key, Fields: fields}})
		}
	}

	got := db.ExportRaw()

	// Verify every table/key/field in want appears in got with the same value.
	for table, keys := range want {
		for key, wantFields := range keys {
			gotFields, ok := got[table][key]
			if !ok {
				t.Errorf("ExportRaw missing %s|%s", table, key)
				continue
			}
			for field, wantVal := range wantFields {
				if gotVal := gotFields[field]; gotVal != wantVal {
					t.Errorf("%s|%s field %q: want %q, got %q", table, key, field, wantVal, gotVal)
				}
			}
		}
	}

	// Verify no unexpected tables appear that we did not write.
	for table := range got {
		if _, ok := want[table]; !ok {
			t.Errorf("ExportRaw produced unexpected table %q", table)
		}
	}
}
