package sonic

import (
	"strings"
	"testing"
)

// The generic read is fail-closed on the DB name (like the CONFIG_DB schema
// is on tables): only registered operational DBs are servable, and the
// rejection names the allowed set.
func TestOperDBFailClosedOnName(t *testing.T) {
	for _, name := range []string{"STATE_DB", "APPL_DB", "COUNTERS_DB", "ASIC_DB"} {
		if !KnownOperDB(name) {
			t.Errorf("KnownOperDB(%q) = false, want true", name)
		}
		c, err := NewOperDBClient("127.0.0.1:6379", name)
		if err != nil {
			t.Errorf("NewOperDBClient(%q): %v", name, err)
		} else {
			c.Close()
		}
	}

	// CONFIG_DB is deliberately NOT served here — /configdb owns the config
	// surface. A registry entry for it would silently create a second read
	// path with different semantics (§27).
	for _, name := range []string{"CONFIG_DB", "FLEX_COUNTER_DB", "", "state_db"} {
		if KnownOperDB(name) {
			t.Errorf("KnownOperDB(%q) = true, want false", name)
		}
		if _, err := NewOperDBClient("127.0.0.1:6379", name); err == nil {
			t.Errorf("NewOperDBClient(%q) succeeded, want error", name)
		} else if !strings.Contains(err.Error(), "APPL_DB, ASIC_DB, COUNTERS_DB, STATE_DB") {
			t.Errorf("NewOperDBClient(%q) error %q does not enumerate the allowed set", name, err)
		}
	}
}

// Redis DB indexes and separators are per-DB facts of SONiC's schema —
// pin them so a registry edit can't silently repoint a name.
func TestOperDBRegistryPinnedToSONiCLayout(t *testing.T) {
	want := map[string]operDBSpec{
		"APPL_DB":     {Index: 0, Separator: ":"},
		"ASIC_DB":     {Index: 1, Separator: ":"},
		"COUNTERS_DB": {Index: 2, Separator: ":"},
		"STATE_DB":    {Index: 6, Separator: "|"},
	}
	if len(operDBs) != len(want) {
		t.Fatalf("operDBs has %d entries, want %d — new DBs need a pinned index+separator here", len(operDBs), len(want))
	}
	for name, spec := range want {
		if operDBs[name] != spec {
			t.Errorf("operDBs[%q] = %+v, want %+v", name, operDBs[name], spec)
		}
	}
}

// splitKey: a key with the DB's separator splits at the FIRST occurrence
// (the remainder is the entry key, separators included); a key without one
// is a flat hash — the whole key is the table, entry key "".
func TestOperDBSplitKey(t *testing.T) {
	tests := []struct {
		db, raw, table, key string
	}{
		{"STATE_DB", "PORT_TABLE|Ethernet0", "PORT_TABLE", "Ethernet0"},
		{"STATE_DB", "TRANSCEIVER_DOM_SENSOR|Ethernet4", "TRANSCEIVER_DOM_SENSOR", "Ethernet4"},
		{"APPL_DB", "NEIGH_TABLE:Ethernet4:10.255.255.4", "NEIGH_TABLE", "Ethernet4:10.255.255.4"},
		{"APPL_DB", "LLDP_ENTRY_TABLE:Ethernet0", "LLDP_ENTRY_TABLE", "Ethernet0"},
		{"COUNTERS_DB", "COUNTERS:oid:0x1000000000002", "COUNTERS", "oid:0x1000000000002"},
		{"COUNTERS_DB", "COUNTERS_PORT_NAME_MAP", "COUNTERS_PORT_NAME_MAP", ""}, // flat hash
		{"ASIC_DB", "ASIC_STATE:SAI_OBJECT_TYPE_SWITCH:oid:0x21000000000000", "ASIC_STATE", "SAI_OBJECT_TYPE_SWITCH:oid:0x21000000000000"},
	}
	for _, tt := range tests {
		c := &OperDBClient{spec: operDBs[tt.db]}
		table, key := c.splitKey(tt.raw)
		if table != tt.table || key != tt.key {
			t.Errorf("%s splitKey(%q) = (%q, %q), want (%q, %q)", tt.db, tt.raw, table, key, tt.table, tt.key)
		}
	}
}
