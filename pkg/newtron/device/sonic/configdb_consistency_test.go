package sonic

import (
	"reflect"
	"strings"
	"testing"
)

// typedTableStructs maps each typed CONFIG_DB table name to a zero-value
// instance of its struct. Used by reflection tests to enumerate fields
// automatically — no manual field lists needed.
var typedTableStructs = map[string]any{
	"PORT":                PortEntry{},
	"VLAN":                VLANEntry{},
	"VLAN_MEMBER":         VLANMemberEntry{},
	"INTERFACE":           InterfaceEntry{},
	"PORTCHANNEL":         PortChannelEntry{},
	"VRF":                 VRFEntry{},
	"VXLAN_TUNNEL":        VXLANTunnelEntry{},
	"VXLAN_TUNNEL_MAP":    VXLANMapEntry{},
	"VXLAN_EVPN_NVO":      EVPNNVOEntry{},
	"BGP_GLOBALS":         BGPGlobalsEntry{},
	"BGP_NEIGHBOR":        BGPNeighborEntry{},
	"BGP_NEIGHBOR_AF":     BGPNeighborAFEntry{},
	"BGP_GLOBALS_AF":      BGPGlobalsAFEntry{},
	"BGP_EVPN_VNI":        BGPEVPNVNIEntry{},
	"BGP_GLOBALS_EVPN_RT": BGPGlobalsEVPNRTEntry{},
	"ROUTE_TABLE":         StaticRouteEntry{},
	"ACL_TABLE":           ACLTableEntry{},
	"ACL_RULE":            ACLRuleEntry{},
	"SCHEDULER":           SchedulerEntry{},
	"QUEUE":               QueueEntry{},
	"WRED_PROFILE":        WREDProfileEntry{},
	"PORT_QOS_MAP":        PortQoSMapEntry{},
	"ROUTE_REDISTRIBUTE":  RouteRedistributeEntry{},
	"ROUTE_MAP":           RouteMapEntry{},
	"BGP_PEER_GROUP":      BGPPeerGroupEntry{},
	"BGP_PEER_GROUP_AF":   BGPPeerGroupAFEntry{},
	"PREFIX_SET":          PrefixSetEntry{},
	"COMMUNITY_SET":       CommunitySetEntry{},
}

// jsonTagNames returns the json tag names for all fields of a struct type.
// Strips the ",omitempty" suffix. Skips fields with no json tag or "-".
func jsonTagNames(v any) []string {
	typ := reflect.TypeOf(v)
	var tags []string
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		tags = append(tags, name)
	}
	return tags
}

// allFieldsPopulated builds a map[string]string with every json tag name
// set to a unique non-empty value ("val_<tagname>").
func allFieldsPopulated(v any) map[string]string {
	fields := make(map[string]string)
	for _, tag := range jsonTagNames(v) {
		fields[tag] = "val_" + tag
	}
	return fields
}

// TestHydrateExportRoundTrip_AllTypedTables verifies that for every typed
// CONFIG_DB table, all struct fields survive the hydrateConfigTable →
// ExportEntries round-trip. This is the compile-time-equivalent check for
// FP-1: if a field exists in the struct (json tag) but the hydrator doesn't
// map it, the round-trip loses that field and this test fails.
func TestHydrateExportRoundTrip_AllTypedTables(t *testing.T) {
	for table, zeroVal := range typedTableStructs {
		t.Run(table, func(t *testing.T) {
			db := newConfigDB()
			input := allFieldsPopulated(zeroVal)
			db.hydrateConfigTable(table, "test-key", input)

			exported := db.ExportEntries()

			// Find the exported entry for this table+key.
			var found *Entry
			for i := range exported {
				if exported[i].Table == table && exported[i].Key == "test-key" {
					found = &exported[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("ExportEntries did not produce an entry for %s|test-key", table)
			}

			// Every input field must survive the round-trip.
			for field, want := range input {
				got, ok := found.Fields[field]
				if !ok {
					t.Errorf("field %q lost in round-trip (present in struct json tags, missing from hydrator or structToFields)", field)
					continue
				}
				if got != want {
					t.Errorf("field %q: got %q, want %q", field, got, want)
				}
			}

			// No extra fields should appear that weren't in the input.
			for field := range found.Fields {
				if _, ok := input[field]; !ok {
					t.Errorf("unexpected field %q in exported entry (not in struct json tags)", field)
				}
			}
		})
	}
}

// TestConfigTableHydrators_CoversAllTypedTables verifies that
// configTableHydrators has an entry for every typed table in
// typedTableStructs. If a new typed table is added but the hydrator
// registry isn't updated, this test fails.
func TestConfigTableHydrators_CoversAllTypedTables(t *testing.T) {
	for table := range typedTableStructs {
		if _, ok := configTableHydrators[table]; !ok {
			t.Errorf("table %s: no configTableHydrators entry", table)
		}
	}
}

// TestExportEntries_CoversAllTypedTables verifies that ExportEntries has a
// case for every typed table. If a new typed table is added to ConfigDB but
// ExportEntries doesn't export it, this test fails.
func TestExportEntries_CoversAllTypedTables(t *testing.T) {
	db := newConfigDB()

	// Populate one entry in every typed table via hydrateConfigTable.
	for table, zeroVal := range typedTableStructs {
		input := allFieldsPopulated(zeroVal)
		db.hydrateConfigTable(table, "test-key", input)
	}

	exported := db.ExportEntries()

	// Build set of exported tables.
	exportedTables := make(map[string]bool)
	for _, e := range exported {
		exportedTables[e.Table] = true
	}

	for table := range typedTableStructs {
		if !exportedTables[table] {
			t.Errorf("ExportEntries does not export table %s", table)
		}
	}
}

// TestDeleteEntry_CoversAllHydratedTables verifies that DeleteEntry has a
// case for every table that hydrateConfigTable handles. If a hydrator can
// create an entry, DeleteEntry must be able to remove it.
func TestDeleteEntry_CoversAllHydratedTables(t *testing.T) {
	for table, zeroVal := range typedTableStructs {
		t.Run(table, func(t *testing.T) {
			db := newConfigDB()
			input := allFieldsPopulated(zeroVal)
			db.hydrateConfigTable(table, "test-key", input)

			// Verify entry exists.
			exported := db.ExportEntries()
			found := false
			for _, e := range exported {
				if e.Table == table && e.Key == "test-key" {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("entry not created by hydrateConfigTable")
			}

			// Delete and verify removal.
			db.DeleteEntry(table, "test-key")
			exported = db.ExportEntries()
			for _, e := range exported {
				if e.Table == table && e.Key == "test-key" {
					t.Errorf("entry still present after DeleteEntry")
				}
			}
		})
	}

	// Also test raw tables that the hydrator handles.
	rawTables := []string{
		"DEVICE_METADATA", "NEWTRON_INTENT", "SUPPRESS_VLAN_NEIGH",
		"LOOPBACK_INTERFACE", "SAG_GLOBAL", "VLAN_INTERFACE",
		"PORTCHANNEL_MEMBER", "DSCP_TO_TC_MAP", "TC_TO_QUEUE_MAP",
		"STATIC_ROUTE", "SAG",
	}
	for _, table := range rawTables {
		t.Run(table, func(t *testing.T) {
			db := newConfigDB()
			db.hydrateConfigTable(table, "test-key", map[string]string{"k": "v"})
			db.DeleteEntry(table, "test-key")

			exported := db.ExportEntries()
			for _, e := range exported {
				if e.Table == table && e.Key == "test-key" {
					t.Errorf("raw entry still present after DeleteEntry")
				}
			}
		})
	}
}
