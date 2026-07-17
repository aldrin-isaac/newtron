package sonic

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/go-redis/redis/v8"
)

// ============================================================================
// Generic operational-DB reads (device-lld: the observation surface)
//
// newtron already holds per-purpose clients into APP_DB (routes), STATE_DB
// (health), and ASIC_DB (verification). This file adds the GENERAL read the
// curated endpoints cannot replace: any table, any key, of any operational
// DB — so a console can localize a failure without SSH (§4: the device is
// the source of reality; observation returns it as-is). CONFIG_DB is
// deliberately NOT served here — /configdb owns the config surface with its
// own semantics (owned_only, the observed ⊇ intended invariant).
// ============================================================================

// operDBSpec describes one readable operational DB: its Redis index and the
// separator its keys use between table and entry-key. The separator is a
// per-DB fact of SONiC's schema, not a convention newtron may choose:
// STATE_DB keys read "PORT_TABLE|Ethernet0", APPL_DB keys read
// "NEIGH_TABLE:Ethernet4:10.255.255.4".
type operDBSpec struct {
	Index     int
	Separator string
}

// operDBs is the closed set of DBs the generic read serves — fail-closed on
// the DB name exactly as the CONFIG_DB schema is fail-closed on tables.
var operDBs = map[string]operDBSpec{
	"STATE_DB":    {Index: 6, Separator: "|"},
	"APPL_DB":     {Index: 0, Separator: ":"},
	"COUNTERS_DB": {Index: 2, Separator: ":"},
	// ASIC_DB's keys are "ASIC_STATE:SAI_OBJECT_TYPE_X:{oid}" — a first-
	// separator split yields one table ("ASIC_STATE") whose entry keys carry
	// the object type. That IS the DB's structure; the read reports it
	// honestly rather than inventing a prettier shape.
	"ASIC_DB": {Index: 1, Separator: ":"},
}

// KnownOperDB reports whether name is a servable operational DB — the
// membership check the public API uses to reject a bad name as a
// validation error before any device I/O.
func KnownOperDB(name string) bool {
	_, ok := operDBs[name]
	return ok
}

// OperDBNames returns the servable DB names, sorted — the 400 message and
// the docs both enumerate from here so the list has one owner.
func OperDBNames() []string {
	names := make([]string, 0, len(operDBs))
	for name := range operDBs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// OperDBClient reads one operational DB over the device tunnel.
type OperDBClient struct {
	client *redis.Client
	ctx    context.Context
	spec   operDBSpec
}

// NewOperDBClient returns a client for the named operational DB, or an error
// naming the allowed set — the single validation point for the DB name.
func NewOperDBClient(addr, dbName string) (*OperDBClient, error) {
	spec, ok := operDBs[dbName]
	if !ok {
		return nil, fmt.Errorf("unknown operational DB %q (one of %s)", dbName, strings.Join(OperDBNames(), ", "))
	}
	return &OperDBClient{
		client: redis.NewClient(&redis.Options{Addr: addr, DB: spec.Index}),
		ctx:    context.Background(),
		spec:   spec,
	}, nil
}

// Close releases the client's connection.
func (c *OperDBClient) Close() error { return c.client.Close() }

// splitKey separates a raw Redis key into (table, entryKey) on the DB's
// separator. A key with no separator is a FLAT HASH (e.g. COUNTERS_DB's
// COUNTERS_PORT_NAME_MAP): the whole key is the table and the entry key is
// "" — Table() and Snapshot() surface its fields under that empty key so
// nothing on the device is unreachable.
func (c *OperDBClient) splitKey(raw string) (table, key string) {
	parts := strings.SplitN(raw, c.spec.Separator, 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

// isNonHashKey reports whether an HGETALL error means the key holds a
// non-hash value. Operational DBs carry such keys as internal plumbing —
// ProducerStateTable's _KEY_SET/_DEL_SET sets in APPL_DB, pub/sub queues —
// which don't fit the table→entry→fields model; the scan reads skip them.
// (Verified live: APPL_DB GEARBOX_TABLE_KEY_SET is a SET.)
func isNonHashKey(err error) bool {
	return err != nil && strings.Contains(err.Error(), "WRONGTYPE")
}

// Snapshot reads the entire DB as table → entryKey → fields. Non-hash keys
// (internal plumbing) are skipped.
func (c *OperDBClient) Snapshot(ctx context.Context) (RawConfigDB, error) {
	keys, err := scanKeys(c.ctx, c.client, "*", 100)
	if err != nil {
		return nil, fmt.Errorf("scanning: %w", err)
	}
	out := make(RawConfigDB)
	for _, raw := range keys {
		table, key := c.splitKey(raw)
		fields, err := c.client.HGetAll(c.ctx, raw).Result()
		if err != nil {
			if isNonHashKey(err) {
				continue
			}
			return nil, fmt.Errorf("reading %s: %w", raw, err)
		}
		if out[table] == nil {
			out[table] = make(map[string]map[string]string)
		}
		out[table][key] = fields
	}
	return out, nil
}

// Table reads every entry of one table: entryKey → fields. A flat hash
// (whole-key table) comes back as a single "" entry.
func (c *OperDBClient) Table(ctx context.Context, table string) (map[string]map[string]string, error) {
	out := make(map[string]map[string]string)
	keys, err := scanKeys(c.ctx, c.client, table+c.spec.Separator+"*", 100)
	if err != nil {
		return nil, fmt.Errorf("scanning table %s: %w", table, err)
	}
	for _, raw := range keys {
		_, key := c.splitKey(raw)
		fields, err := c.client.HGetAll(c.ctx, raw).Result()
		if err != nil {
			if isNonHashKey(err) {
				continue
			}
			return nil, fmt.Errorf("reading %s: %w", raw, err)
		}
		out[key] = fields
	}
	// The flat-hash form: the table name IS the full key.
	if flat, err := c.client.HGetAll(c.ctx, table).Result(); err == nil && len(flat) > 0 {
		out[""] = flat
	}
	return out, nil
}

// Entry reads one entry's fields; key "" reads the table's flat-hash form.
func (c *OperDBClient) Entry(ctx context.Context, table, key string) (map[string]string, error) {
	raw := table
	if key != "" {
		raw = table + c.spec.Separator + key
	}
	fields, err := c.client.HGetAll(c.ctx, raw).Result()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", raw, err)
	}
	return fields, nil
}

// ----------------------------------------------------------------------------
// Device-level reads. Unlike CONFIG_DB/STATE_DB (persistent clients held for
// the connection's lifetime), operational reads are ad-hoc diagnostics: each
// call opens a client over the already-established tunnel address and closes
// it, so an idle Device carries no extra connections.
// ----------------------------------------------------------------------------

// withOperDB runs fn against a short-lived client for the named DB.
func (d *Device) withOperDB(dbName string, fn func(*OperDBClient) error) error {
	d.mu.RLock()
	connected, addr := d.connected, d.redisAddr
	d.mu.RUnlock()
	if !connected {
		return fmt.Errorf("device %s not connected", d.Name)
	}
	c, err := NewOperDBClient(addr, dbName)
	if err != nil {
		return err
	}
	defer c.Close()
	return fn(c)
}

// OperDBSnapshot reads the entire named operational DB as table → key → fields.
func (d *Device) OperDBSnapshot(ctx context.Context, dbName string) (RawConfigDB, error) {
	var out RawConfigDB
	err := d.withOperDB(dbName, func(c *OperDBClient) error {
		var err error
		out, err = c.Snapshot(ctx)
		return err
	})
	return out, err
}

// OperDBTable reads one table of the named operational DB.
func (d *Device) OperDBTable(ctx context.Context, dbName, table string) (map[string]map[string]string, error) {
	var out map[string]map[string]string
	err := d.withOperDB(dbName, func(c *OperDBClient) error {
		var err error
		out, err = c.Table(ctx, table)
		return err
	})
	return out, err
}

// OperDBEntry reads one entry of the named operational DB.
func (d *Device) OperDBEntry(ctx context.Context, dbName, table, key string) (map[string]string, error) {
	var out map[string]string
	err := d.withOperDB(dbName, func(c *OperDBClient) error {
		var err error
		out, err = c.Entry(ctx, table, key)
		return err
	})
	return out, err
}
