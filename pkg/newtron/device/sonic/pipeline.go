// pipeline.go implements Redis pipeline operations for atomic batch writes to CONFIG_DB.
package sonic

import (
	"fmt"

	"github.com/go-redis/redis/v8"
)

// TableChange represents a single change for pipeline execution.
type TableChange struct {
	Table  string
	Key    string
	Fields map[string]string // nil means delete
}

// TableKey identifies a CONFIG_DB entry for deletion.
type TableKey struct {
	Table string
	Key   string
}

// PipelineSet writes multiple entries atomically via Redis MULTI/EXEC pipeline.
// All changes are applied in a single transaction — either all succeed or none.
func (c *ConfigDBClient) PipelineSet(changes []TableChange) error {
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


// platformMergeTables are CONFIG_DB tables managed by the SONiC platform
// (portsyncd, port_config.ini). Newtron merges its settings into these
// tables rather than replacing them, preserving platform-derived fields
// like lanes, speed, alias, and index.
var platformMergeTables = map[string]bool{
	"PORT": true,
}

// ReplaceAll replaces newtron-managed tables in CONFIG_DB with the given
// configuration. Only tables present in the changes are affected; platform
// defaults loaded from init_cfg.json (FEATURE, CRM, FLEX_COUNTER_TABLE, etc.)
// are preserved.
//
// For most tables, all existing keys are deleted first, then new entries are
// written. Platform-managed tables (PORT) are merged instead — existing
// fields are preserved and newtron's fields are overlaid.
func (c *ConfigDBClient) ReplaceAll(changes []TableChange) error {
	// Collect the set of tables being replaced (excluding merge-only tables)
	tables := make(map[string]bool)
	for _, change := range changes {
		if !platformMergeTables[change.Table] {
			tables[change.Table] = true
		}
	}

	// Scan for existing keys in replace-mode tables and delete them
	pipe := c.client.TxPipeline()
	for table := range tables {
		pattern := fmt.Sprintf("%s|*", table)
		keys, err := c.client.Keys(c.ctx, pattern).Result()
		if err != nil {
			return fmt.Errorf("scanning keys for table %s: %w", table, err)
		}
		for _, key := range keys {
			pipe.Del(c.ctx, key)
		}
	}
	if _, err := pipe.Exec(c.ctx); err != nil && err != redis.Nil {
		return fmt.Errorf("deleting old table entries: %w", err)
	}

	// Write all new entries (PipelineSet uses HSet which merges fields
	// for platform tables, and creates fresh entries for replaced tables)
	return c.PipelineSet(changes)
}
