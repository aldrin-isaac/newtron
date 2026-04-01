// pipeline.go implements Redis pipeline operations for atomic batch writes to CONFIG_DB.
package sonic

import (
	"fmt"
	"sort"

	"github.com/go-redis/redis/v8"
)

// PipelineSet writes multiple entries atomically via Redis MULTI/EXEC pipeline.
// All changes are applied in a single transaction — either all succeed or none.
func (c *ConfigDBClient) PipelineSet(changes []Entry) error {
	if len(changes) == 0 {
		return nil
	}

	pipe := c.client.TxPipeline()

	for _, change := range changes {
		redisKey := fmt.Sprintf("%s|%s", change.Table, change.Key)
		if change.Fields == nil {
			// Delete entry
			pipe.Del(c.ctx, redisKey)
		} else if len(change.Fields) == 0 {
			// Empty entry — write NULL sentinel (SONiC convention)
			pipe.HSet(c.ctx, redisKey, "NULL", "NULL")
		} else {
			// Write fields
			args := make([]interface{}, 0, len(change.Fields)*2)
			for k, v := range change.Fields {
				args = append(args, k, v)
			}
			pipe.HSet(c.ctx, redisKey, args...)
		}
	}

	_, err := pipe.Exec(c.ctx)
	if err != nil && err != redis.Nil {
		return fmt.Errorf("pipeline exec: %w", err)
	}
	return nil
}


// ApplyDrift applies only the drifted entries to CONFIG_DB, using a single
// atomic TxPipeline. Entries are ordered by table dependency: deletes run
// children-first (descending priority), creates/modifies run parents-first
// (ascending priority). This matches YANG leafref ordering.
//
// Actions per drift type:
//   - "extra": DEL (entry should not exist)
//   - "missing"/"modified": DEL + HSET (clean replace per CONFIG_DB Replace Semantics)
func (c *ConfigDBClient) ApplyDrift(diffs []DriftEntry) error {
	if len(diffs) == 0 {
		return nil
	}

	// Separate deletes from creates/modifies for ordering.
	var deletes, upserts []DriftEntry
	for _, d := range diffs {
		if d.Type == "extra" {
			deletes = append(deletes, d)
		} else {
			upserts = append(upserts, d)
		}
	}

	// Deletes: children first (descending priority).
	sort.Slice(deletes, func(i, j int) bool {
		pi, pj := TablePriority(deletes[i].Table), TablePriority(deletes[j].Table)
		if pi != pj {
			return pi > pj
		}
		return deletes[i].Table < deletes[j].Table
	})

	// Upserts: parents first (ascending priority).
	sort.Slice(upserts, func(i, j int) bool {
		pi, pj := TablePriority(upserts[i].Table), TablePriority(upserts[j].Table)
		if pi != pj {
			return pi < pj
		}
		return upserts[i].Table < upserts[j].Table
	})

	pipe := c.client.TxPipeline()

	// Deletes first.
	for _, d := range deletes {
		pipe.Del(c.ctx, fmt.Sprintf("%s|%s", d.Table, d.Key))
	}

	// Then creates/modifies (DEL + HSET per CONFIG_DB Replace Semantics).
	for _, d := range upserts {
		redisKey := fmt.Sprintf("%s|%s", d.Table, d.Key)
		pipe.Del(c.ctx, redisKey)
		if len(d.Expected) == 0 {
			pipe.HSet(c.ctx, redisKey, "NULL", "NULL")
		} else {
			args := make([]interface{}, 0, len(d.Expected)*2)
			for k, v := range d.Expected {
				args = append(args, k, v)
			}
			pipe.HSet(c.ctx, redisKey, args...)
		}
	}

	_, err := pipe.Exec(c.ctx)
	if err != nil && err != redis.Nil {
		return fmt.Errorf("apply drift pipeline exec: %w", err)
	}
	return nil
}

// platformMergeTables are CONFIG_DB tables managed by the SONiC platform
// (portsyncd, port_config.ini). Newtron merges its settings into these
// tables rather than replacing them, preserving platform-derived fields
// like lanes, speed, alias, and index.
var platformMergeTables = map[string]bool{
	"PORT": true,
}

// ReplaceAll merges composite entries on top of existing CONFIG_DB, removing
// only stale keys not present in the composite. Factory defaults (mac, platform,
// hwsku from init_cfg.json; FEATURE, CRM, FLEX_COUNTER_TABLE, etc.) are preserved
// because we never delete keys that appear in our composite — HSet merges our
// fields on top of any surviving factory fields.
//
// Platform-managed tables (PORT) are merge-only — their keys are never deleted
// even if absent from the composite, since port config comes from port_config.ini.
//
// ownedTables lists all tables the node manages. Tables in ownedTables that have
// zero entries in the delivery set are fully cleaned (all keys DELeted). This
// ensures Clear + Reconcile wipes all owned tables — even those with no entries
// in the empty projection. Architecture §6 (Clear): "ReplaceAll with only PORT
// entries clears all other owned tables."
func (c *ConfigDBClient) ReplaceAll(changes []Entry, ownedTables []string) error {
	// Collect the set of tables being replaced (excluding merge-only tables).
	// Start with all owned tables — even those with zero entries.
	tables := make(map[string]bool)
	for _, table := range ownedTables {
		if !platformMergeTables[table] {
			tables[table] = true
		}
	}
	for _, change := range changes {
		if !platformMergeTables[change.Table] {
			tables[change.Table] = true
		}
	}

	// Build set of composite keys (table|key format)
	compositeKeys := make(map[string]bool, len(changes))
	for _, change := range changes {
		compositeKeys[fmt.Sprintf("%s|%s", change.Table, change.Key)] = true
	}

	// Delete only stale keys: exist in DB but NOT in our composite
	pipe := c.client.TxPipeline()
	for table := range tables {
		pattern := fmt.Sprintf("%s|*", table)
		keys, err := c.client.Keys(c.ctx, pattern).Result()
		if err != nil {
			return fmt.Errorf("scanning keys for table %s: %w", table, err)
		}
		for _, key := range keys {
			if !compositeKeys[key] {
				pipe.Del(c.ctx, key) // Stale — not in composite, remove
			}
			// Keys we DO provide: skip delete, HSet will merge fields
		}
	}
	if _, err := pipe.Exec(c.ctx); err != nil && err != redis.Nil {
		return fmt.Errorf("deleting stale table entries: %w", err)
	}

	// Write all entries — HSet merges our fields on top of any
	// surviving factory fields for keys we provide
	return c.PipelineSet(changes)
}
