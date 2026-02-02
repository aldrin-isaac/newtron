//go:build integration || e2e

package testutil

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/go-redis/redis/v8"
)

// SeedRedis loads a JSON seed file into a specific Redis database.
// The JSON format is: { "TABLE": { "key": { "field": "value", ... }, ... }, ... }
// Each entry becomes a Redis hash at key "TABLE|key" with the given fields.
func SeedRedis(t *testing.T, addr string, db int, seedFile string) {
	t.Helper()

	data, err := os.ReadFile(seedFile)
	if err != nil {
		t.Fatalf("reading seed file %s: %v", seedFile, err)
	}

	var tables map[string]map[string]map[string]string
	if err := json.Unmarshal(data, &tables); err != nil {
		t.Fatalf("parsing seed file %s: %v", seedFile, err)
	}

	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	defer client.Close()

	ctx := context.Background()

	for table, entries := range tables {
		for key, fields := range entries {
			redisKey := table + "|" + key
			if len(fields) == 0 {
				// Empty hash - still need to create the key
				// Use HSET with a placeholder then delete it
				if err := client.HSet(ctx, redisKey, "NULL", "").Err(); err != nil {
					t.Fatalf("seeding %s: %v", redisKey, err)
				}
				continue
			}
			// Convert map[string]string to []interface{} for HSet
			args := make([]interface{}, 0, len(fields)*2)
			for k, v := range fields {
				args = append(args, k, v)
			}
			if err := client.HSet(ctx, redisKey, args...).Err(); err != nil {
				t.Fatalf("seeding %s: %v", redisKey, err)
			}
		}
	}
}

// FlushDB flushes a specific Redis database.
func FlushDB(t *testing.T, addr string, db int) {
	t.Helper()

	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	defer client.Close()

	if err := client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("flushing DB %d: %v", db, err)
	}
}

// SetupConfigDB flushes DB 4 and seeds it with configdb.json.
func SetupConfigDB(t *testing.T) {
	t.Helper()

	addr := RedisAddr()
	FlushDB(t, addr, 4)
	SeedRedis(t, addr, 4, SeedPath("configdb.json"))
}

// SetupStateDB flushes DB 6 and seeds it with statedb.json.
func SetupStateDB(t *testing.T) {
	t.Helper()

	addr := RedisAddr()
	FlushDB(t, addr, 6)
	SeedRedis(t, addr, 6, SeedPath("statedb.json"))
}

// SetupBothDBs flushes and seeds both CONFIG_DB (4) and STATE_DB (6).
func SetupBothDBs(t *testing.T) {
	t.Helper()

	SetupConfigDB(t)
	SetupStateDB(t)
}

// WriteSingleEntry writes a single hash entry to a specific Redis DB.
func WriteSingleEntry(t *testing.T, addr string, db int, table, key string, fields map[string]string) {
	t.Helper()

	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	defer client.Close()

	redisKey := table + "|" + key
	args := make([]interface{}, 0, len(fields)*2)
	for k, v := range fields {
		args = append(args, k, v)
	}
	if err := client.HSet(context.Background(), redisKey, args...).Err(); err != nil {
		t.Fatalf("writing %s: %v", redisKey, err)
	}
}

// DeleteEntry removes a key from a specific Redis DB.
func DeleteEntry(t *testing.T, addr string, db int, table, key string) {
	t.Helper()

	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	defer client.Close()

	redisKey := table + "|" + key
	if err := client.Del(context.Background(), redisKey).Err(); err != nil {
		t.Fatalf("deleting %s: %v", redisKey, err)
	}
}

// ReadEntry reads a hash entry from a specific Redis DB.
func ReadEntry(t *testing.T, addr string, db int, table, key string) map[string]string {
	t.Helper()

	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	defer client.Close()

	redisKey := table + "|" + key
	vals, err := client.HGetAll(context.Background(), redisKey).Result()
	if err != nil {
		t.Fatalf("reading %s: %v", redisKey, err)
	}
	return vals
}

// EntryExists checks if a key exists in a specific Redis DB.
func EntryExists(t *testing.T, addr string, db int, table, key string) bool {
	t.Helper()

	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	defer client.Close()

	redisKey := table + "|" + key
	n, err := client.Exists(context.Background(), redisKey).Result()
	if err != nil {
		t.Fatalf("checking existence of %s: %v", redisKey, err)
	}
	return n > 0
}
