// configdb_diff.go compares expected vs actual CONFIG_DB state for drift detection.
//
// Only tables in newtron's ownership map are compared. Excluded tables:
// NEWTRON_INTENT, NEWTRON_HISTORY (ephemeral/rolling, not configuration).
package sonic

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// RawConfigDB is a raw representation of CONFIG_DB: table → key → field → value.
// Used for drift detection where typed structs are unnecessary.
type RawConfigDB map[string]map[string]map[string]string

// DriftEntry describes a single difference between expected and actual CONFIG_DB.
type DriftEntry struct {
	Table    string            `json:"table"`
	Key      string            `json:"key"`
	Type     string            `json:"type"` // "missing", "extra", "modified"
	Expected map[string]string `json:"expected,omitempty"`
	Actual   map[string]string `json:"actual,omitempty"`
}

// excludedFromDrift lists tables that should not be compared for drift.
//   - NEWTRON_INTENT, NEWTRON_HISTORY, NEWTRON_SETTINGS: ephemeral/rolling
//   - PORT: factory-managed (all HWSKU ports exist in config_db.json)
//   - DEVICE_METADATA: partially factory, partially newtron — too noisy
var excludedFromDrift = map[string]bool{
	"NEWTRON_INTENT":   true,
	"NEWTRON_HISTORY":  true,
	"NEWTRON_SETTINGS": true,
	"PORT":             true,
	"DEVICE_METADATA":  true,
}

// DiffConfigDB compares expected vs actual CONFIG_DB, returning differences.
// Only tables present in ownedTables are compared. Tables in excludedFromDrift
// are always skipped.
//
// Returns three categories:
//   - Missing: expected entry absent from actual
//   - Extra: actual entry not in expected
//   - Modified: entry exists in both but fields differ
func DiffConfigDB(expected, actual RawConfigDB, ownedTables []string) []DriftEntry {
	var diffs []DriftEntry

	for _, table := range ownedTables {
		if excludedFromDrift[table] {
			continue
		}

		expectedKeys := expected[table]
		actualKeys := actual[table]

		if expectedKeys == nil {
			expectedKeys = map[string]map[string]string{}
		}
		if actualKeys == nil {
			actualKeys = map[string]map[string]string{}
		}

		// Find missing entries (in expected but not actual)
		for key, expectedFields := range expectedKeys {
			actualFields, exists := actualKeys[key]
			if !exists {
				diffs = append(diffs, DriftEntry{
					Table:    table,
					Key:      key,
					Type:     "missing",
					Expected: copyMap(expectedFields),
				})
				continue
			}

			// Check for modified fields (subset: expected fields must match actual)
			if !fieldsMatch(expectedFields, actualFields) {
				diffs = append(diffs, DriftEntry{
					Table:    table,
					Key:      key,
					Type:     "modified",
					Expected: copyMap(expectedFields),
					Actual:   copyMap(actualFields),
				})
			}
		}

		// Find extra entries (in actual but not expected)
		for key, actualFields := range actualKeys {
			if _, exists := expectedKeys[key]; !exists {
				diffs = append(diffs, DriftEntry{
					Table:  table,
					Key:    key,
					Type:   "extra",
					Actual: copyMap(actualFields),
				})
			}
		}
	}

	// Sort for deterministic output: by table, then key
	sort.Slice(diffs, func(i, j int) bool {
		if diffs[i].Table != diffs[j].Table {
			return diffs[i].Table < diffs[j].Table
		}
		return diffs[i].Key < diffs[j].Key
	})

	return diffs
}

// fieldsMatch checks that every field in expected is present in actual with the
// same value. Extra fields in actual are ignored — the device may have fields
// from factory config or config-reload that the provisioner doesn't manage.
func fieldsMatch(expected, actual map[string]string) bool {
	for k, v := range expected {
		if actual[k] != v {
			return false
		}
	}
	return true
}

// copyMap returns a shallow copy of a string map.
func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// tablePriority assigns a numeric priority to each owned CONFIG_DB table based
// on YANG leafref dependency chains. Lower number = parent (created first,
// deleted last). Used by ApplyDrift to order operations correctly.
//
// Dependency chains (from CLAUDE.md):
//   VLAN → VLAN_MEMBER, VLAN_INTERFACE
//   VRF → INTERFACE (vrf_name), BGP_GLOBALS → BGP_NEIGHBOR → BGP_NEIGHBOR_AF
//   VXLAN_TUNNEL → VXLAN_EVPN_NVO → VXLAN_TUNNEL_MAP
//   ACL_TABLE → ACL_RULE
//   DSCP_TO_TC_MAP, SCHEDULER → PORT_QOS_MAP, QUEUE
//   BGP_PEER_GROUP → BGP_PEER_GROUP_AF
//   BGP_GLOBALS → BGP_GLOBALS_AF, BGP_GLOBALS_EVPN_RT, ROUTE_REDISTRIBUTE
var tablePriority = map[string]int{
	// Tier 0 — no parents (root tables)
	"DEVICE_METADATA":    0,
	"PORT":               0,
	"PORTCHANNEL":        0,
	"LOOPBACK_INTERFACE": 0,
	"VRF":                0,
	"VLAN":               0,
	"VXLAN_TUNNEL":       0,
	"ACL_TABLE":          0,
	"DSCP_TO_TC_MAP":     0,
	"TC_TO_QUEUE_MAP":    0,
	"SCHEDULER":          0,
	"WRED_PROFILE":       0,
	"SAG_GLOBAL":         0,
	"SUPPRESS_VLAN_NEIGH": 0,
	"STATIC_ROUTE":       0,
	"PREFIX_SET":         0,
	"COMMUNITY_SET":      0,
	"NEWTRON_SETTINGS":   0,
	"NEWTRON_INTENT":     0,
	"NEWTRON_HISTORY":    0,

	// Tier 1 — depends on tier 0
	"PORTCHANNEL_MEMBER": 1, // → PORTCHANNEL
	"VLAN_MEMBER":        1, // → VLAN
	"VLAN_INTERFACE":     1, // → VLAN
	"INTERFACE":          1, // → VRF (vrf_name)
	"BGP_GLOBALS":        1, // → VRF (vrf_name)
	"VXLAN_EVPN_NVO":     1, // → VXLAN_TUNNEL
	"ACL_RULE":           1, // → ACL_TABLE
	"PORT_QOS_MAP":       1, // → DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP
	"QUEUE":              1, // → SCHEDULER
	"BGP_PEER_GROUP":     1, // → BGP_GLOBALS (implicit)
	"ROUTE_MAP":          1, // → PREFIX_SET, COMMUNITY_SET

	// Tier 2 — depends on tier 1
	"BGP_NEIGHBOR":       2, // → BGP_GLOBALS
	"BGP_GLOBALS_AF":     2, // → BGP_GLOBALS
	"BGP_GLOBALS_EVPN_RT": 2, // → BGP_GLOBALS
	"ROUTE_REDISTRIBUTE": 2, // → BGP_GLOBALS
	"BGP_PEER_GROUP_AF":  2, // → BGP_PEER_GROUP
	"VXLAN_TUNNEL_MAP":   2, // → VXLAN_EVPN_NVO
	"BGP_EVPN_VNI":       2, // → VXLAN_TUNNEL_MAP (implicit)

	// Tier 3 — depends on tier 2
	"BGP_NEIGHBOR_AF": 3, // → BGP_NEIGHBOR
}

// TablePriority returns the priority for a table (lower = parent). Returns 0
// for unknown tables (safe default — treated as root).
func TablePriority(table string) int {
	return tablePriority[table]
}

// OwnedTables returns the list of CONFIG_DB tables that newtron owns,
// derived from the schema registry. Excludes drift-excluded tables.
func OwnedTables() []string {
	var tables []string
	for table := range Schema {
		if !excludedFromDrift[table] {
			tables = append(tables, table)
		}
	}
	sort.Strings(tables)
	return tables
}

// GetRawTable reads all entries for a single CONFIG_DB table as raw field maps.
// Returns key → fields mapping.
func (c *ConfigDBClient) GetRawTable(table string) (map[string]map[string]string, error) {
	pattern := fmt.Sprintf("%s|*", table)
	keys, err := scanKeys(c.ctx, c.client, pattern, 100)
	if err != nil {
		return nil, fmt.Errorf("scanning table %s: %w", table, err)
	}

	result := make(map[string]map[string]string, len(keys))
	for _, redisKey := range keys {
		// Extract the key part after "TABLE|"
		parts := strings.SplitN(redisKey, "|", 2)
		if len(parts) < 2 {
			continue
		}
		entryKey := parts[1]

		vals, err := c.client.HGetAll(c.ctx, redisKey).Result()
		if err != nil || len(vals) == 0 {
			continue
		}
		result[entryKey] = vals
	}
	return result, nil
}

// GetRawOwnedTables reads all newtron-owned tables from CONFIG_DB as raw data.
// Used by drift detection to get the actual device state.
func (c *ConfigDBClient) GetRawOwnedTables(ctx context.Context) (RawConfigDB, error) {
	raw := make(RawConfigDB)
	for _, table := range OwnedTables() {
		entries, err := c.GetRawTable(table)
		if err != nil {
			return nil, err
		}
		if len(entries) > 0 {
			raw[table] = entries
		}
	}
	return raw, nil
}
