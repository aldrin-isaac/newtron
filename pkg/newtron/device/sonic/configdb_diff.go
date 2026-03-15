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
// Intent and history are ephemeral/rolling — not device configuration.
var excludedFromDrift = map[string]bool{
	"NEWTRON_INTENT":   true,
	"NEWTRON_HISTORY":  true,
	"NEWTRON_SETTINGS": true,
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

			// Check for modified fields
			if !fieldsEqual(expectedFields, actualFields) {
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

// fieldsEqual compares two field maps for equality.
// Both must have the same keys with the same values.
func fieldsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
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
