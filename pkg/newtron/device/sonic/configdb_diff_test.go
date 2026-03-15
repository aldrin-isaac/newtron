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

func TestFieldsEqual_DifferentLengths(t *testing.T) {
	a := map[string]string{"x": "1"}
	b := map[string]string{"x": "1", "y": "2"}
	if fieldsEqual(a, b) {
		t.Error("different lengths should not be equal")
	}
}

func TestFieldsEqual_SameContent(t *testing.T) {
	a := map[string]string{"x": "1", "y": "2"}
	b := map[string]string{"y": "2", "x": "1"}
	if !fieldsEqual(a, b) {
		t.Error("same content should be equal")
	}
}

func TestOwnedTables_ContainsCriticalTables(t *testing.T) {
	tables := OwnedTables()
	required := map[string]bool{
		"VLAN": false, "VRF": false, "BGP_GLOBALS": false,
		"NEWTRON_SERVICE_BINDING": false,
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
