# Newtron Device Layer LLD

The device connection layer handles SSH tunnels, Redis client connections, and state access for SONiC devices. This document covers `pkg/newtron/device/sonic/` — the low-level plumbing that connects newtron to a SONiC switch's Redis databases.

For the architectural principles behind newtron, see [Design Principles](../DESIGN_PRINCIPLES_NEWTRON.md). For network-level operations (service apply, topology provisioning, intent management), see [newtron LLD](lld.md). For the HTTP API, see [API Reference](api.md).

**Scope:** This document describes the `sonic` package only — types, clients, connection management. Operations that *use* these primitives (ChangeSet apply, config save, health checks, route verification) live at the `node` layer (`pkg/newtron/network/node/`) and are documented in the [newtron LLD](lld.md).

---

## 1. Device (`pkg/newtron/device/sonic/device.go`)

The `Device` struct is the central object in the device layer. It manages the SSH tunnel, four Redis client connections (CONFIG_DB, STATE_DB, APP_DB, ASIC_DB), distributed locking, and in-memory snapshots of CONFIG_DB and STATE_DB.

Device is a **connection and lock manager** — it does not contain write, save, or verification logic. Those operations live at the `node.Node` layer, which holds a `*sonic.Device` and orchestrates operations through it.

### 1.1 Device Struct

```go
type Device struct {
    Name     string
    Profile  *spec.ResolvedProfile
    ConfigDB *ConfigDB   // In-memory snapshot, loaded on Connect
    StateDB  *StateDB    // In-memory snapshot, loaded on Connect (may be nil)

    // Redis connections (private)
    client      *ConfigDBClient   // DB 4
    stateClient *StateDBClient    // DB 6
    applClient  *AppDBClient      // DB 0 (nil if connect failed)
    asicClient  *AsicDBClient     // DB 1 (nil if connect failed)
    tunnel      *SSHTunnel        // SSH tunnel (nil if direct Redis)
    connected   bool
    locked      bool
    lockHolder  string            // holder identity for distributed lock
    mu          sync.RWMutex
}

func NewDevice(name string, profile *spec.ResolvedProfile) *Device
```

### 1.2 Connection Lifecycle

`Connect()` establishes all Redis connections through a single SSH tunnel (or direct, for integration tests). `Disconnect()` tears everything down in reverse order.

```go
// Connect establishes connection to the device's Redis databases via SSH tunnel.
// Creates an SSH tunnel when SSHUser/SSHPass are present in the profile;
// otherwise connects directly to <mgmt_ip>:6379 (for integration tests).
//
// Connection order:
//   1. SSH tunnel (if SSH credentials present)
//   2. CONFIG_DB (DB 4) — required, fatal on failure
//   3. Load full CONFIG_DB snapshot into d.ConfigDB
//   4. STATE_DB (DB 6) — non-fatal, warns on failure
//   5. Load full STATE_DB snapshot into d.StateDB
//   6. APP_DB (DB 0) — non-fatal, warns on failure, nil on failure
//   7. ASIC_DB (DB 1) — non-fatal, debug-level log on failure (expected on VPP), nil on failure
//
// Idempotent: returns nil if already connected.
func (d *Device) Connect(ctx context.Context) error

// Disconnect closes all connections and releases the lock if held.
// Teardown order: unlock → ConfigDB → StateDB → AppDB → AsicDB → SSH tunnel.
// Idempotent: returns nil if already disconnected.
func (d *Device) Disconnect() error
```

**Key behaviors:**
- All Redis clients share the same address (same SSH tunnel)
- CONFIG_DB is the only fatal connection — if it fails, Connect returns an error
- STATE_DB, APP_DB, and ASIC_DB failures are non-fatal: the device remains usable for config operations
- CONFIG_DB and StateDB snapshots are loaded in full on Connect
- APP_DB and ASIC_DB clients connect but do not bulk-load — routes are read on demand
- A single SSH tunnel multiplexes all four Redis database connections

**When tunnels are used:**

| Scenario | SSH Tunnel | Direct Redis |
|----------|-----------|--------------|
| Lab E2E tests (SONiC-VS in QEMU) | Yes — port 6379 not forwarded by QEMU | No |
| Integration tests (standalone Redis) | No — Redis exposed directly | Yes |

The decision is made based on the presence of `SSHUser` and `SSHPass` in the resolved profile.

### 1.3 Distributed Locking

Device implements distributed locking via STATE_DB (§5.3). The lock prevents concurrent writers from corrupting CONFIG_DB state.

```go
// Lock acquires a distributed lock on the device via STATE_DB.
// The holder string identifies who holds the lock; ttlSeconds controls expiry.
// Idempotent: returns nil if already locked.
func (d *Device) Lock(holder string, ttlSeconds int) error

// Unlock releases the device lock via STATE_DB.
// Verifies holder identity before releasing (prevents releasing another's lock).
func (d *Device) Unlock() error
```

`Lock()` stores the holder string internally so `Unlock()` does not require the caller to pass it again. The `node.Node.Lock()` wrapper constructs the holder string as `"user@hostname"` and delegates to `d.Lock(holder, ttl)`.

### 1.4 Precondition Checks

```go
func (d *Device) IsConnected() bool
func (d *Device) RequireConnected() error  // returns PreconditionError if not connected
func (d *Device) IsLocked() bool
func (d *Device) RequireLocked() error     // returns PreconditionError if not connected+locked
```

`RequireLocked()` checks both connected AND locked — a locked-but-disconnected state is impossible (Disconnect releases the lock).

### 1.5 Accessor Methods

These expose the underlying clients for direct access by the `node` layer.

```go
func (d *Device) Client() *ConfigDBClient      // CONFIG_DB client (DB 4)
func (d *Device) StateClient() *StateDBClient   // STATE_DB client (DB 6)
func (d *Device) AppDBClient() *AppDBClient     // APP_DB client (DB 0), may be nil
func (d *Device) AsicDBClient() *AsicDBClient   // ASIC_DB client (DB 1), may be nil
func (d *Device) ConnAddr() string              // Redis address (tunnel local or direct)
func (d *Device) Tunnel() *SSHTunnel            // SSH tunnel, nil if direct connection
```

The `node.Node` layer uses these accessors to perform operations:
- `d.Client().PipelineSet(entries)` for atomic writes (ChangeSet delivery)
- `d.Client().ReplaceAll(entries, ownedTables)` for reconcile delivery
- `d.Client().Set(table, key, fields)` for individual writes
- `d.Client().GetAll()` for CONFIG_DB refresh
- `d.AppDBClient().GetRoute(vrf, prefix)` for route observation
- `d.AsicDBClient().GetRouteASIC(vrf, prefix, configDB)` for ASIC verification
- `d.Tunnel().ExecCommand(cmd)` for SSH commands (config save, config reload)

**Reconnection policy:** No automatic reconnection. If the SSH tunnel or any Redis client disconnects, the caller must call `Disconnect()` and then `Connect()` again. This is a deliberate simplicity choice — reconnection with state recovery adds complexity not needed for lab/test workloads where a connection drop typically means the VM crashed.

---

## 2. SSH Tunnel (`pkg/newtron/device/sonic/types.go`)

SONiC devices in the lab run inside QEMU VMs managed by newtlab. Redis listens on `127.0.0.1:6379` inside the VM, but QEMU SLiRP networking does not forward port 6379. The SSH tunnel solves this by forwarding a random local port through SSH to the in-VM Redis.

**Consumer note:** SONiC device connections are established on demand by newtron-server when the first API request for a device arrives. newtrun's `Runner.connectDevices()` registers the network with the server and pre-connects host devices only — it does not pre-connect SONiC switches. When the server creates a connection, it calls `Device.Connect()` (§1.2), which creates an SSH tunnel using the newtlab-allocated `SSHPort`. All Redis clients (CONFIG_DB, STATE_DB, APP_DB, ASIC_DB) then multiplex over this single tunnel.

### 2.1 SSHTunnel Type

```go
// SSHTunnel forwards a local TCP port to a remote address through an SSH connection.
// Used to access Redis (127.0.0.1:6379) inside SONiC containers via SSH,
// since Redis has no authentication and port 6379 is not forwarded by QEMU.
type SSHTunnel struct {
    localAddr string         // "127.0.0.1:<port>"
    sshClient *ssh.Client
    listener  net.Listener
    done      chan struct{}
    wg        sync.WaitGroup
}

// NewSSHTunnel dials SSH on host:port and opens a local listener on a random port.
// Connections to the local port are forwarded to 127.0.0.1:6379 inside the SSH host.
// If port is 0, defaults to 22.
func NewSSHTunnel(host, user, pass string, port int) (*SSHTunnel, error)

func (t *SSHTunnel) LocalAddr() string      // "127.0.0.1:54321"
func (t *SSHTunnel) Close() error           // stops listener, closes SSH, waits for goroutines
func (t *SSHTunnel) SSHClient() *ssh.Client // for opening command sessions directly

// ExecCommand runs a command on the remote device via SSH and returns the combined output.
// The SSH session is created per-call (stateless). Used by the node layer for
// config save, config reload, and service restart operations.
func (t *SSHTunnel) ExecCommand(cmd string) (string, error)

// ExecCommandContext runs a command with context cancellation support.
// If the context is cancelled or times out, the SSH session is killed.
// Used by operations that need timeout control (e.g., config reload with retry).
func (t *SSHTunnel) ExecCommandContext(ctx context.Context, cmd string) (string, error)
```

### 2.2 How It Works

1. `ssh.Dial("tcp", host:port, config)` establishes the SSH connection with password auth
2. `net.Listen("tcp", "127.0.0.1:0")` opens a local listener on a random available port
3. A background goroutine (`acceptLoop`) accepts incoming local connections
4. Each accepted connection is forwarded via `sshClient.Dial("tcp", "127.0.0.1:6379")`
5. Bidirectional `io.Copy` relays data between the local and remote connections
6. `Close()` signals the done channel, closes the SSH client (tearing down all forwarded connections), then waits for goroutines

**Security note:** `HostKeyCallback: ssh.InsecureIgnoreHostKey()` is used because this is a lab/test environment only. SONiC-VS VMs regenerate host keys on each boot.

**Timeout:** SSH dial timeout is 30 seconds (`ssh.ClientConfig.Timeout`).

---

## 3. CONFIG_DB (`pkg/newtron/device/sonic/configdb.go`, `configdb_parsers.go`)

CONFIG_DB (Redis DB 4) is the primary database for device configuration. newtron reads the entire CONFIG_DB into an in-memory `ConfigDB` struct on connect, and writes changes via the `ConfigDBClient`.

### 3.1 ConfigDB Struct

The in-memory mirror of SONiC's CONFIG_DB. Each map corresponds to one CONFIG_DB table, with keys matching SONiC key formats. Loaded in full by `ConfigDBClient.GetAll()` during `Device.Connect()`.

```go
type ConfigDB struct {
    // Core infrastructure
    DeviceMetadata    map[string]map[string]string  // DEVICE_METADATA (e.g., "localhost" → {hostname, bgp_asn, ...})
    Port              map[string]PortEntry           // PORT (e.g., "Ethernet0" → {admin_status, speed, ...})
    Interface         map[string]InterfaceEntry      // INTERFACE (base + IP sub-entries)
    PortChannel       map[string]PortChannelEntry    // PORTCHANNEL
    PortChannelMember map[string]map[string]string   // PORTCHANNEL_MEMBER
    LoopbackInterface map[string]map[string]string   // LOOPBACK_INTERFACE

    // L2
    VLAN              map[string]VLANEntry           // VLAN (e.g., "Vlan100" → {vlanid, description})
    VLANMember        map[string]VLANMemberEntry     // VLAN_MEMBER (e.g., "Vlan100|Ethernet0")
    VLANInterface     map[string]map[string]string   // VLAN_INTERFACE (SVI config + IP sub-entries)

    // VRF
    VRF               map[string]VRFEntry            // VRF (e.g., "Vrf_CUST1" → {vni})

    // VXLAN / EVPN
    VXLANTunnel       map[string]VXLANTunnelEntry    // VXLAN_TUNNEL (VTEP)
    VXLANTunnelMap    map[string]VXLANMapEntry       // VXLAN_TUNNEL_MAP
    VXLANEVPNNVO      map[string]EVPNNVOEntry        // VXLAN_EVPN_NVO
    SuppressVLANNeigh map[string]map[string]string   // SUPPRESS_VLAN_NEIGH
    SAG               map[string]map[string]string   // SAG
    SAGGlobal         map[string]map[string]string   // SAG_GLOBAL

    // BGP
    BGPGlobals        map[string]BGPGlobalsEntry     // BGP_GLOBALS (per-VRF)
    BGPGlobalsAF      map[string]BGPGlobalsAFEntry   // BGP_GLOBALS_AF
    BGPNeighbor       map[string]BGPNeighborEntry    // BGP_NEIGHBOR
    BGPNeighborAF     map[string]BGPNeighborAFEntry  // BGP_NEIGHBOR_AF
    BGPEVPNVNI        map[string]BGPEVPNVNIEntry     // BGP_EVPN_VNI
    BGPGlobalsEVPNRT  map[string]BGPGlobalsEVPNRTEntry // BGP_GLOBALS_EVPN_RT

    // Routing
    RouteTable        map[string]StaticRouteEntry    // ROUTE_TABLE (static routes in CONFIG_DB)
    StaticRoute       map[string]map[string]string   // STATIC_ROUTE
    RouteRedistribute map[string]RouteRedistributeEntry // ROUTE_REDISTRIBUTE
    RouteMap          map[string]RouteMapEntry       // ROUTE_MAP

    // BGP peer groups and policy
    BGPPeerGroup      map[string]BGPPeerGroupEntry   // BGP_PEER_GROUP
    BGPPeerGroupAF    map[string]BGPPeerGroupAFEntry // BGP_PEER_GROUP_AF
    PrefixSet         map[string]PrefixSetEntry      // PREFIX_SET
    CommunitySet      map[string]CommunitySetEntry   // COMMUNITY_SET

    // ACL
    ACLTable          map[string]ACLTableEntry       // ACL_TABLE
    ACLRule           map[string]ACLRuleEntry         // ACL_RULE

    // QoS
    Scheduler         map[string]SchedulerEntry      // SCHEDULER
    Queue             map[string]QueueEntry           // QUEUE
    WREDProfile       map[string]WREDProfileEntry    // WRED_PROFILE
    PortQoSMap        map[string]PortQoSMapEntry     // PORT_QOS_MAP
    DSCPToTCMap       map[string]map[string]string   // DSCP_TO_TC_MAP
    TCToQueueMap      map[string]map[string]string   // TC_TO_QUEUE_MAP

    // Newtron-specific — unified intent model (§39)
    NewtronIntent map[string]map[string]string // NEWTRON_INTENT
}

// NewConfigDB returns a ConfigDB with all map fields initialized (no nil maps).
// Used by abstract Node (offline mode) to start with an empty projection.
func NewConfigDB() *ConfigDB
```

### 3.2 ConfigDB Query Methods

Nil-safe convenience methods for precondition checks. All return `false` when `db` is nil (disconnected state).

```go
func (db *ConfigDB) HasVLAN(id int) bool            // checks VLAN table for "Vlan<id>"
func (db *ConfigDB) HasVRF(name string) bool         // checks VRF table
func (db *ConfigDB) HasPortChannel(name string) bool  // checks PORTCHANNEL table
func (db *ConfigDB) HasACLTable(name string) bool     // checks ACL_TABLE
func (db *ConfigDB) HasVTEP() bool                    // checks if any VXLAN_TUNNEL exists
func (db *ConfigDB) HasBGPNeighbor(key string) bool   // key format: "vrf|ip"
func (db *ConfigDB) HasInterface(name string) bool     // checks PORT, PORTCHANNEL, or VLAN tables
func (db *ConfigDB) BGPConfigured() bool               // checks BGP_NEIGHBOR or DEVICE_METADATA bgp_asn
```

`HasInterface` checks three tables: PORT (physical ports), PORTCHANNEL (LAGs), and VLAN (SVIs like `Vlan100`). This covers all interface types that can be targets of service operations.

### 3.3 Projection Updates

The abstract Node uses `ApplyEntries` to keep its projection (in-memory ConfigDB) in sync as operations generate entries. This allows subsequent operations to pass precondition checks (e.g., `HasVTEP()` returns true after `SetupEVPN` generates VXLAN_TUNNEL entries).

```go
// ApplyEntries updates the ConfigDB's typed maps from a slice of entries.
// Only tables needed for precondition checks are handled — unrecognized tables
// are silently skipped (entries still accumulate for projection export).
func (db *ConfigDB) ApplyEntries(entries []Entry)
```

### 3.4 ConfigDBClient

The Redis client for CONFIG_DB (DB 4). All methods operate on Redis hash keys with format `<TABLE>|<KEY>`.

```go
type ConfigDBClient struct {
    client *redis.Client
    ctx    context.Context
}

func NewConfigDBClient(addr string) *ConfigDBClient
func (c *ConfigDBClient) Connect() error   // Ping test
func (c *ConfigDBClient) Close() error

// Read operations
func (c *ConfigDBClient) GetAll() (*ConfigDB, error)                      // Full DB snapshot via SCAN + HGETALL
func (c *ConfigDBClient) Get(table, key string) (map[string]string, error) // Single entry
func (c *ConfigDBClient) TableKeys(table string) ([]string, error)         // All keys for a table
func (c *ConfigDBClient) Exists(table, key string) (bool, error)           // Key existence check

// Write operations
func (c *ConfigDBClient) Set(table, key string, fields map[string]string) error  // Single entry write
func (c *ConfigDBClient) Delete(table, key string) error                         // Single entry delete
func (c *ConfigDBClient) PipelineSet(changes []Entry) error                      // Atomic batch write (§3.7)
func (c *ConfigDBClient) ReplaceAll(changes []Entry, ownedTables []string) error // Full reconcile delivery (§3.7)
func (c *ConfigDBClient) ApplyDrift(diffs []DriftEntry) error                    // Delta reconcile delivery (§3.7, §3.9)
```

**`Set` behavior:** Writes all fields in a single `HSET` command to fire exactly one keyspace notification. Writing fields individually fires N notifications, causing SONiC daemons (e.g., bgpcfgd) to process partial state. If `fields` is empty, writes the `NULL:NULL` sentinel (SONiC convention for field-less entries like `PORTCHANNEL_MEMBER` or `INTERFACE` IP keys).

**`GetAll` implementation:** Uses cursor-based `SCAN` (not `KEYS *`) to avoid blocking on large databases. Each key is parsed via the table-driven parser registry (§3.6).

### 3.5 Entry and ConfigChange Types

Two types represent configuration data at the device layer. Used by `configdb.go` and `types.go`.

```go
// Entry is a single CONFIG_DB entry: table + key + fields.
// Used by config generators, intent projection export, and pipeline delivery.
// A nil Fields map means "delete this entry" (in PipelineSet).
type Entry struct {
    Table  string
    Key    string
    Fields map[string]string
}

// ConfigChange represents a single configuration change with explicit type.
// Used by the ChangeSet at the node layer for tracking and verification.
type ConfigChange struct {
    Table  string            `json:"table"`
    Key    string            `json:"key"`
    Type   ChangeType        `json:"type"`             // "add", "modify", "delete"
    Fields map[string]string `json:"fields,omitempty"`
}

type ChangeType string

const (
    ChangeTypeAdd    ChangeType = "add"
    ChangeTypeModify ChangeType = "modify"
    ChangeTypeDelete ChangeType = "delete"
)
```

### 3.6 Table-Driven Parser Registry

CONFIG_DB entries are parsed from Redis hashes into typed Go structs via a registry in `configdb_parsers.go`. This avoids a giant switch statement and makes adding new tables mechanical.

**39 registered parsers:**
- **28 typed struct parsers**: PORT, VLAN, VLAN_MEMBER, INTERFACE, PORTCHANNEL, VRF, VXLAN_TUNNEL, VXLAN_TUNNEL_MAP, VXLAN_EVPN_NVO, BGP_NEIGHBOR, BGP_NEIGHBOR_AF, BGP_GLOBALS, BGP_GLOBALS_AF, BGP_EVPN_VNI, BGP_GLOBALS_EVPN_RT, ROUTE_TABLE, ACL_TABLE, ACL_RULE, SCHEDULER, QUEUE, WRED_PROFILE, PORT_QOS_MAP, ROUTE_REDISTRIBUTE, ROUTE_MAP, BGP_PEER_GROUP, BGP_PEER_GROUP_AF, PREFIX_SET, COMMUNITY_SET
- **1 copy parser**: STATIC_ROUTE (copies into `map[string]map[string]string`)
- **10 hash-merge parsers**: DEVICE_METADATA, VLAN_INTERFACE, LOOPBACK_INTERFACE, PORTCHANNEL_MEMBER, SUPPRESS_VLAN_NEIGH, SAG, SAG_GLOBAL, DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, NEWTRON_INTENT

Hash-merge hydrators (`mergeHydrator`) copy all key-value pairs into `map[string]map[string]string` for tables with variable or unknown field names.

**Redis serialization note:** Redis hashes store field names and values as strings. The Go struct `json` tags serve double duty: they define both the Redis hash field name mapping and JSON serialization format (for display/logging). Parsing uses the registry functions, not `json.Unmarshal` — there is no JSON to unmarshal from flat `map[string]string`.

Map initialization uses reflection (`initMaps`) to ensure all map fields are non-nil after `newConfigDB()`, preventing nil-map panics in operations and precondition checks.

### 3.7 Pipeline Operations (`pkg/newtron/device/sonic/pipeline.go`)

Bulk writes use Redis pipelines for atomicity and performance. Two modes are available.

**`PipelineSet` — atomic incremental writes:**

```go
// PipelineSet writes multiple entries atomically via Redis MULTI/EXEC pipeline.
// Entry semantics:
//   - Fields == nil → DEL (delete entry)
//   - Fields == {} (empty) → HSET NULL:NULL (SONiC empty-entry sentinel)
//   - Fields has values → HSET all fields
func (c *ConfigDBClient) PipelineSet(changes []Entry) error
```

Used by `node.Node` for ChangeSet application — the accumulated config changes from an operation are written in a single atomic transaction.

**`ReplaceAll` — reconcile delivery:**

```go
// ReplaceAll merges projection entries on top of existing CONFIG_DB.
// 1. Starts from the union of ownedTables and tables present in changes
//    (excluding platform-managed tables)
// 2. Deletes stale keys: exist in DB but NOT in the projection
// 3. Writes all projection entries via PipelineSet (HSET merges fields)
//
// Platform-managed tables (PORT) are merge-only — their keys are never deleted,
// since port config comes from port_config.ini / portsyncd.
//
// ownedTables lists all tables the node manages. Tables in ownedTables that have
// zero entries in the delivery set are fully cleaned (all keys DELeted). This
// ensures Clear + Reconcile wipes all owned tables — even those with no entries
// in the empty projection.
func (c *ConfigDBClient) ReplaceAll(changes []Entry, ownedTables []string) error
```

Used by full-mode `Reconcile()` at the node layer to deliver the full projection to the device, eliminating drift. Factory defaults (DEVICE_METADATA `mac`, `platform`, `hwsku`) are preserved because `ReplaceAll` only deletes keys from owned tables — factory-only tables are untouched.

**`ApplyDrift` — delta reconcile delivery:**

```go
// ApplyDrift applies only the drifted entries to CONFIG_DB using a single
// atomic TxPipeline. Entries are ordered by table dependency (tablePriority
// from configdb_diff.go): deletes run children-first (descending priority),
// creates/modifies run parents-first (ascending priority). This matches YANG
// leafref ordering so dependent entries are never written before their parents.
//
// Actions per drift type:
//   - "extra":    DEL (entry should not exist)
//   - "missing":  DEL + HSET (clean replace per CONFIG_DB Replace Semantics)
//   - "modified": DEL + HSET (clean replace per CONFIG_DB Replace Semantics)
func (c *ConfigDBClient) ApplyDrift(diffs []DriftEntry) error
```

Used by delta-mode `Reconcile()` at the node layer. Delta reconcile calls `DiffConfigDB` (§3.9) to identify drifted entries, then passes the diff directly to `ApplyDrift` — no config reload, no full projection delivery. NEWTRON_INTENT entries are excluded from drift detection and delivered separately via `PipelineSet` after `ApplyDrift`. See §3.9 for the full delta reconcile pipeline.

**Why MULTI/EXEC pipelines:**
- **Atomicity**: Either all changes apply or none. Prevents partial config states.
- **Performance**: Single round-trip vs one per entry. A projection with 200 entries takes 1 round-trip.
- **Notification batching**: SONiC daemons see the complete change set at once via keyspace notifications.

### 3.8 Projection Export (`pkg/newtron/device/sonic/configdb.go`)

The ConfigDB struct serves as both the in-memory mirror of a device's CONFIG_DB and the projection (expected CONFIG_DB state) for abstract nodes. Two export methods extract data from the projection for delivery and drift detection.

```go
// ExportEntries returns all entries from all tables in the ConfigDB as a flat
// slice of Entry values. This is the inverse of ApplyEntries — it serializes
// the typed struct maps back to table/key/fields triples.
//
// Used by Reconcile() to extract the projection for delivery via ReplaceAll.
// Even entries with empty fields are exported — SONiC uses field-less entries
// for IP assignments (INTERFACE|Eth0|10.0.0.1/31), portchannel members, etc.
func (db *ConfigDB) ExportEntries() []Entry

// ExportRaw converts the ConfigDB to a RawConfigDB (table → key → fields map)
// for drift detection. Built from ExportEntries — same data, different shape.
//
// Used by drift detection: the projection's ExportRaw() output is compared
// against the device's actual CONFIG_DB via DiffConfigDB().
func (db *ConfigDB) ExportRaw() RawConfigDB

// ExportIntentEntries returns only the NEWTRON_INTENT entries from the ConfigDB.
// Used by delta Reconcile: NEWTRON_INTENT is excluded from DiffConfigDB and
// thus from ApplyDrift, so intents are delivered separately via PipelineSet
// after the drift patch is applied.
func (db *ConfigDB) ExportIntentEntries() []Entry
```

### 3.9 Drift Detection (`pkg/newtron/device/sonic/configdb_diff.go`)

Drift detection compares the expected CONFIG_DB state (projection) against the actual CONFIG_DB state (device). Only tables in newtron's ownership map are compared.

```go
// RawConfigDB is a raw representation of CONFIG_DB: table → key → field → value.
type RawConfigDB map[string]map[string]map[string]string

// DriftEntry describes a single difference between expected and actual CONFIG_DB.
type DriftEntry struct {
    Table    string            `json:"table"`
    Key      string            `json:"key"`
    Type     string            `json:"type"` // "missing", "extra", "modified"
    Expected map[string]string `json:"expected,omitempty"`
    Actual   map[string]string `json:"actual,omitempty"`
}

// DiffConfigDB compares expected vs actual CONFIG_DB, returning differences.
// Only tables present in ownedTables are compared. Tables in excludedFromDrift
// (NEWTRON_INTENT, NEWTRON_HISTORY, NEWTRON_SETTINGS, PORT, DEVICE_METADATA)
// are always skipped.
//
// Returns three categories:
//   - Missing: expected entry absent from actual
//   - Extra: actual entry not in expected
//   - Modified: entry exists in both but fields differ
func DiffConfigDB(expected, actual RawConfigDB, ownedTables []string) []DriftEntry

// OwnedTables returns the list of CONFIG_DB tables that newtron owns,
// derived from the schema registry. Excludes drift-excluded tables.
func OwnedTables() []string

// GetRawOwnedTables reads all newtron-owned tables from CONFIG_DB as raw data.
// Used by drift detection to get the actual device state.
func (c *ConfigDBClient) GetRawOwnedTables(ctx context.Context) (RawConfigDB, error)
```

**Drift detection pipeline:** `intent drift` works by:
1. Replaying all NEWTRON_INTENT records to rebuild the projection
2. Calling `projection.ExportRaw()` to get expected state
3. Calling `client.GetRawOwnedTables()` to get actual state
4. Calling `DiffConfigDB(expected, actual, ownedTables)` to produce the diff

**Delta reconcile pipeline:** delta-mode `Reconcile()` extends drift detection with apply:
1. Steps 1–4 above to produce the `[]DriftEntry` diff
2. Calling `client.ApplyDrift(diffs)` to patch only the drifted entries (§3.7)
3. Calling `client.PipelineSet(intentEntries)` to deliver NEWTRON_INTENT separately
   (intents are excluded from `DiffConfigDB` via `excludedFromDrift`)
4. Calling `config save -y` to persist the patched state

No config reload is performed in delta mode — only drifted keys are touched. Full-mode `Reconcile()` still uses config reload + `ReplaceAll` (§3.7) for a clean-slate delivery.

**Table ordering in `ApplyDrift`:** entries are sorted by `tablePriority`, a 4-tier map in `configdb_diff.go` derived from YANG leafref dependency chains covering 38 CONFIG_DB tables. Tier 0 = root tables (no parents); Tier 3 = deepest children. Deletes run in descending tier order (children first); creates/modifies run in ascending tier order (parents first).

**Field matching is subset-based:** `fieldsMatch` checks that every field in expected is present in actual with the same value. Extra fields in actual are ignored — the device may have fields from factory config or `config reload` that the projection doesn't manage.

### 3.10 Schema Validation (`pkg/newtron/device/sonic/schema.go`)

Every CONFIG_DB write passes through `ChangeSet.Validate()` before reaching
Redis. The schema is a static Go data structure encoding per-table, per-field
constraints derived from SONiC YANG models:

```go
type FieldConstraint struct {
    Type       FieldType         // String, Int, Enum, IP, CIDR, MAC, Bool
    Required   bool
    Range      *[2]int           // min, max (for Int fields)
    Pattern    string            // regex (for string validation)
    Enum       []string          // allowed values (for Enum fields)
}

type TableSchema struct {
    KeyPattern string                       // regex for key format
    Fields     map[string]FieldConstraint   // field name → constraint
}
```

**Fail-closed design:** Unknown tables and unknown fields are validation errors.
When adding a new CONFIG_DB write in `*_ops.go`, the corresponding table and
field must also be added to `schema.go` — tests fail until they are.

**YANG reference:** Constraints are derived from
`sonic-buildimage/src/sonic-yang-models/yang-models/*.yang`. The mapping is
documented in `pkg/newtron/device/sonic/yang/constraints.md`. Tables without
YANG models (NEWTRON_INTENT, SAG_GLOBAL, SUPPRESS_VLAN_NEIGH,
BGP_EVPN_VNI) derive constraints from newtron usage patterns.

---

## 4. CONFIG_DB Entry Types

All CONFIG_DB entry types are defined in `configdb.go`. Each type has `json` tags matching the Redis hash field names used by SONiC. Types are grouped by domain.

### 4.1 Infrastructure Types

```go
type PortEntry struct {
    AdminStatus string `json:"admin_status,omitempty"`
    Alias       string `json:"alias,omitempty"`
    Description string `json:"description,omitempty"`
    FEC         string `json:"fec,omitempty"`
    Index       string `json:"index,omitempty"`
    Lanes       string `json:"lanes,omitempty"`
    MTU         string `json:"mtu,omitempty"`
    Speed       string `json:"speed,omitempty"`
    Autoneg     string `json:"autoneg,omitempty"`
}

type InterfaceEntry struct {         // Key: "Ethernet0" (base) or "Ethernet0|10.1.1.1/30" (IP)
    VRFName     string `json:"vrf_name,omitempty"`
    NATZone     string `json:"nat_zone,omitempty"`
    ProxyArp    string `json:"proxy_arp,omitempty"`
    MPLSEnabled string `json:"mpls,omitempty"`
}

type PortChannelEntry struct {
    AdminStatus string `json:"admin_status,omitempty"`
    MTU         string `json:"mtu,omitempty"`
    MinLinks    string `json:"min_links,omitempty"`
    Fallback    string `json:"fallback,omitempty"`
    FastRate    string `json:"fast_rate,omitempty"`
    LACPKey     string `json:"lacp_key,omitempty"`
    Description string `json:"description,omitempty"`
}
```

### 4.2 L2 Types

```go
type VLANEntry struct {              // Key: "Vlan100"
    VLANID      string `json:"vlanid"`
    Description string `json:"description,omitempty"`
    MTU         string `json:"mtu,omitempty"`
    AdminStatus string `json:"admin_status,omitempty"`
    DHCPServers string `json:"dhcp_servers,omitempty"`
}

type VLANMemberEntry struct {        // Key: "Vlan100|Ethernet0"
    TaggingMode string `json:"tagging_mode"`  // "tagged" or "untagged"
}
```

### 4.3 VRF and VXLAN Types

```go
type VRFEntry struct {               // Key: "Vrf_CUST1"
    VNI      string `json:"vni,omitempty"`
    Fallback string `json:"fallback,omitempty"`
}

type VXLANTunnelEntry struct {        // Key: "vtep1"
    SrcIP string `json:"src_ip"`
}

type VXLANMapEntry struct {           // Key: "vtep1|VNI100_Vlan100"
    VLAN string `json:"vlan,omitempty"`
    VRF  string `json:"vrf,omitempty"`
    VNI  string `json:"vni"`
}

type EVPNNVOEntry struct {            // Key: "nvo1"
    SourceVTEP string `json:"source_vtep"`
}
```

### 4.4 BGP Types

```go
type BGPGlobalsEntry struct {         // Key: VRF name (e.g., "default", "Vrf_CUST1")
    RouterID            string `json:"router_id,omitempty"`
    LocalASN            string `json:"local_asn,omitempty"`
    ConfedID            string `json:"confed_id,omitempty"`
    ConfedPeers         string `json:"confed_peers,omitempty"`
    GracefulRestart     string `json:"graceful_restart,omitempty"`
    LoadBalanceMPRelax  string `json:"load_balance_mp_relax,omitempty"`
    RRClusterID         string `json:"rr_cluster_id,omitempty"`
    EBGPRequiresPolicy  string `json:"ebgp_requires_policy,omitempty"`
    DefaultIPv4Unicast  string `json:"default_ipv4_unicast,omitempty"`
    LogNeighborChanges  string `json:"log_neighbor_changes,omitempty"`
    SuppressFIBPending  string `json:"suppress_fib_pending,omitempty"`
}

type BGPNeighborEntry struct {        // Key: "vrf|ip" (e.g., "default|10.0.0.2")
    LocalAddr     string `json:"local_addr,omitempty"`
    Name          string `json:"name,omitempty"`
    ASN           string `json:"asn,omitempty"`
    HoldTime      string `json:"holdtime,omitempty"`
    KeepaliveTime string `json:"keepalive,omitempty"`
    AdminStatus   string `json:"admin_status,omitempty"`
    PeerGroup     string `json:"peer_group_name,omitempty"`
    EBGPMultihop  string `json:"ebgp_multihop,omitempty"`
    Password      string `json:"password,omitempty"`
}

type BGPNeighborAFEntry struct {      // Key: "neighbor_ip|address_family"
    AdminStatus         string `json:"admin_status,omitempty"`
    RRClient            string `json:"rrclient,omitempty"`
    NHSelf              string `json:"nhself,omitempty"`
    NextHopUnchanged    string `json:"nexthop_unchanged,omitempty"`
    SoftReconfiguration string `json:"soft_reconfiguration,omitempty"`
    AllowASIn           string `json:"allowas_in,omitempty"`
    RouteMapIn          string `json:"route_map_in,omitempty"`
    RouteMapOut         string `json:"route_map_out,omitempty"`
    PrefixListIn        string `json:"prefix_list_in,omitempty"`
    PrefixListOut       string `json:"prefix_list_out,omitempty"`
    DefaultOriginate    string `json:"default_originate,omitempty"`
    AddpathTxAll        string `json:"addpath_tx_all_paths,omitempty"`
}

type BGPGlobalsAFEntry struct {       // Key: "vrf_name|address_family"
    AdvertiseAllVNI       string `json:"advertise-all-vni,omitempty"`
    AdvertiseDefaultGW    string `json:"advertise-default-gw,omitempty"`
    AdvertiseSVIIP        string `json:"advertise-svi-ip,omitempty"`
    AdvertiseIPv4         string `json:"advertise_ipv4_unicast,omitempty"`
    AdvertiseIPv6         string `json:"advertise_ipv6_unicast,omitempty"`
    RD                    string `json:"rd,omitempty"`
    RTImport              string `json:"rt_import,omitempty"`
    RTExport              string `json:"rt_export,omitempty"`
    RTImportEVPN          string `json:"route_target_import_evpn,omitempty"`
    RTExportEVPN          string `json:"route_target_export_evpn,omitempty"`
    MaxEBGPPaths          string `json:"max_ebgp_paths,omitempty"`
    MaxIBGPPaths          string `json:"max_ibgp_paths,omitempty"`
    RedistributeConnected string `json:"redistribute_connected,omitempty"`
    RedistributeStatic    string `json:"redistribute_static,omitempty"`
}

type BGPEVPNVNIEntry struct {         // Key: "vrf_name|vni"
    RD                 string `json:"rd,omitempty"`
    RTImport           string `json:"route_target_import,omitempty"`
    RTExport           string `json:"route_target_export,omitempty"`
    AdvertiseDefaultGW string `json:"advertise_default_gw,omitempty"`
}

type BGPGlobalsEVPNRTEntry struct {   // Key: "vrf_name|L2VPN_EVPN|rt"
    RouteTargetType string `json:"route-target-type,omitempty"` // "both", "import", "export"
}
```

### 4.5 Routing Policy Types (frrcfgd)

```go
type RouteRedistributeEntry struct {  // Key: "vrf|src_protocol|address_family"
    RouteMap string `json:"route_map,omitempty"`
    Metric   string `json:"metric,omitempty"`
}

type RouteMapEntry struct {           // Key: "map_name|seq"
    Action         string `json:"route_operation"`
    MatchPrefixSet string `json:"match_prefix_set,omitempty"`
    MatchCommunity string `json:"match_community,omitempty"`
    MatchASPath    string `json:"match_as_path,omitempty"`
    MatchNextHop   string `json:"match_next_hop,omitempty"`
    SetLocalPref   string `json:"set_local_pref,omitempty"`
    SetCommunity   string `json:"set_community,omitempty"`
    SetMED         string `json:"set_med,omitempty"`
    SetNextHop     string `json:"set_next_hop,omitempty"`
}

type BGPPeerGroupEntry struct {       // Key: peer_group_name
    ASN          string `json:"asn,omitempty"`
    LocalAddr    string `json:"local_addr,omitempty"`
    AdminStatus  string `json:"admin_status,omitempty"`
    HoldTime     string `json:"holdtime,omitempty"`
    Keepalive    string `json:"keepalive,omitempty"`
    Password     string `json:"password,omitempty"`
    EBGPMultihop string `json:"ebgp_multihop,omitempty"`
}

type BGPPeerGroupAFEntry struct {     // Key: "peer_group_name|address_family"
    AdminStatus         string `json:"admin_status,omitempty"`
    RRClient            string `json:"rrclient,omitempty"`
    NHSelf              string `json:"nhself,omitempty"`
    NextHopUnchanged    string `json:"nexthop_unchanged,omitempty"`
    RouteMapIn          string `json:"route_map_in,omitempty"`
    RouteMapOut         string `json:"route_map_out,omitempty"`
    SoftReconfiguration string `json:"soft_reconfiguration,omitempty"`
}

type PrefixSetEntry struct {          // Key: "set_name|seq"
    IPPrefix     string `json:"ip_prefix"`
    Action       string `json:"action"`
    MaskLenRange string `json:"masklength_range,omitempty"`
}

type CommunitySetEntry struct {       // Key: set_name
    SetType         string `json:"set_type,omitempty"`
    MatchAction     string `json:"match_action,omitempty"`
    CommunityMember string `json:"community_member,omitempty"`
}

type ASPathSetEntry struct {          // Key: set_name
    ASPathMember string `json:"as_path_member,omitempty"`
}

type StaticRouteEntry struct {        // Key: "vrf|prefix" (e.g., "Vrf_CUST1|192.168.0.0/24")
    NextHop    string `json:"nexthop,omitempty"`
    Interface  string `json:"ifname,omitempty"`
    Distance   string `json:"distance,omitempty"`
    NextHopVRF string `json:"nexthop-vrf,omitempty"`
    Blackhole  string `json:"blackhole,omitempty"`
}

```

### 4.6 ACL Types

```go
type ACLTableEntry struct {           // Key: table_name (e.g., "SVC_Ethernet0_INGRESS")
    PolicyDesc string `json:"policy_desc,omitempty"`
    Type       string `json:"type"`
    Stage      string `json:"stage,omitempty"`
    Ports      string `json:"ports,omitempty"`
    Services   string `json:"services,omitempty"`
}

type ACLRuleEntry struct {            // Key: "table_name|rule_name"
    Priority       string `json:"PRIORITY,omitempty"`
    PacketAction   string `json:"PACKET_ACTION,omitempty"`
    SrcIP          string `json:"SRC_IP,omitempty"`
    DstIP          string `json:"DST_IP,omitempty"`
    IPProtocol     string `json:"IP_PROTOCOL,omitempty"`
    L4SrcPort      string `json:"L4_SRC_PORT,omitempty"`
    L4DstPort      string `json:"L4_DST_PORT,omitempty"`
    L4SrcPortRange string `json:"L4_SRC_PORT_RANGE,omitempty"`
    L4DstPortRange string `json:"L4_DST_PORT_RANGE,omitempty"`
    TCPFlags       string `json:"TCP_FLAGS,omitempty"`
    DSCP           string `json:"DSCP,omitempty"`
    ICMPType       string `json:"ICMP_TYPE,omitempty"`
    ICMPCode       string `json:"ICMP_CODE,omitempty"`
    EtherType      string `json:"ETHER_TYPE,omitempty"`
    InPorts        string `json:"IN_PORTS,omitempty"`
    RedirectPort   string `json:"REDIRECT_PORT,omitempty"`
}

```

### 4.7 QoS Types

```go
type SchedulerEntry struct {          // Key: "SCHEDULER|sched_name"
    Type   string `json:"type"`             // DWRR, STRICT
    Weight string `json:"weight,omitempty"`
}

type QueueEntry struct {              // Key: "Ethernet0|0" (port|queue_id)
    Scheduler   string `json:"scheduler,omitempty"`
    WREDProfile string `json:"wred_profile,omitempty"`
}

type WREDProfileEntry struct {        // Key: profile_name
    GreenMinThreshold     string `json:"green_min_threshold,omitempty"`
    GreenMaxThreshold     string `json:"green_max_threshold,omitempty"`
    GreenDropProbability  string `json:"green_drop_probability,omitempty"`
    YellowMinThreshold    string `json:"yellow_min_threshold,omitempty"`
    YellowMaxThreshold    string `json:"yellow_max_threshold,omitempty"`
    YellowDropProbability string `json:"yellow_drop_probability,omitempty"`
    RedMinThreshold       string `json:"red_min_threshold,omitempty"`
    RedMaxThreshold       string `json:"red_max_threshold,omitempty"`
    RedDropProbability    string `json:"red_drop_probability,omitempty"`
    ECN                   string `json:"ecn,omitempty"`
}

type PortQoSMapEntry struct {         // Key: port_name
    DSCPToTCMap  string `json:"dscp_to_tc_map,omitempty"`
    TCToQueueMap string `json:"tc_to_queue_map,omitempty"`
}
```

### 4.8 Intent Record Type (Unified Intent Model)

Intent records are stored in `NEWTRON_INTENT` as flat Redis hashes with
identity fields alongside resolved parameters. The `Intent` struct is the
internal domain model constructed via `NewIntent` / serialized via
`Intent.ToFields()`.

```go
// Intent is the internal domain model for a desired-state record bound to
// a device resource. See DESIGN_PRINCIPLES_NEWTRON §39 for the full model.
//
// Key format: kind-prefixed resource (e.g., "interface|Ethernet0", "vlan|100", "device")
// Stored in NEWTRON_INTENT — a custom table, not standard SONiC.
//
// The intent record is self-sufficient for reverse operations and drift
// reconstruction: every value needed for teardown is stored in Params,
// not re-resolved from specs (which may have changed between apply and
// remove).
type Intent struct {
    // Identity
    Resource  string      // binding point: "interface|Ethernet0", "vlan|100", "device"
    Operation string      // composite op: "apply-service", "create-vlan", "setup-device"
    Name      string      // spec reference: "transit", "" if none

    // DAG — structural dependencies between intent records
    Parents  []string     // resource keys this intent depends on (_parents CSV)
    Children []string     // resource keys that depend on this intent (_children CSV)

    // Lifecycle
    State     IntentState // "unrealized", "in-flight", "actuated"
    Holder    string
    Created   time.Time
    AppliedAt *time.Time
    AppliedBy string

    // Resolved parameters — self-sufficient for teardown + reconstruction (§37).
    Params map[string]string

    // Composite operations — expanded primitive list for crash recovery.
    Phase           string
    RollbackHolder  string
    RollbackStarted *time.Time
    Operations      []IntentOperation
}

// NewIntent constructs an Intent from a flat CONFIG_DB field map.
// Identity fields (state, operation, name, holder, etc.) are extracted;
// all remaining fields become Params.
func NewIntent(resource string, fields map[string]string) *Intent

// ToFields serializes the Intent back to a flat map for CONFIG_DB storage.
func (i *Intent) ToFields() map[string]string
```

The CONFIG_DB representation is a flat `map[string]string` hash. Identity
fields (`state`, `operation`, `name`, `holder`, `created`, `applied_at`,
`applied_by`, `_parents`, `_children`) live alongside resolved parameters
(`service_name`, `ip_address`, `vrf_name`, etc.) in the same hash.
`NewIntent` separates them; `ToFields` merges them back.

---

## 5. STATE_DB (`pkg/newtron/device/sonic/statedb.go`)

STATE_DB (Redis DB 6) contains the operational/runtime state of the device, separate from configuration. CONFIG_DB is the device's configured state — ground reality, whether correct or not. STATE_DB is what the system is actually doing at runtime.

**Consumer note:** newtrun's `verifyStateDBExecutor` reads STATE_DB tables via the HTTP API client (`Client.QueryStateDB()`), which calls the newtron-server, which internally calls `StateDBClient.GetEntry()`. Similarly, `verifyBGPExecutor` calls `Client.CheckBGPSessions()`, which reads BGP neighbor state from STATE_DB on the server side. Both executors poll with timeout until expected values appear.

### 5.1 StateDB Struct

```go
type StateDB struct {
    PortTable         map[string]PortStateEntry
    LAGTable          map[string]LAGStateEntry
    LAGMemberTable    map[string]LAGMemberStateEntry
    VLANTable         map[string]VLANStateEntry
    VRFTable          map[string]VRFStateEntry
    VXLANTunnelTable  map[string]VXLANTunnelStateEntry
    BGPNeighborTable  map[string]BGPNeighborStateEntry
    InterfaceTable    map[string]InterfaceStateEntry
    NeighTable        map[string]NeighStateEntry
    FDBTable          map[string]FDBStateEntry
    RouteTable        map[string]RouteStateEntry
    TransceiverInfo   map[string]TransceiverInfoEntry
    TransceiverStatus map[string]TransceiverStatusEntry
}
```

**STATE_DB key formats:**

| Table | Key Pattern | Example |
|-------|------------|---------|
| `PORT_TABLE` | `PORT_TABLE\|<port>` | `PORT_TABLE\|Ethernet0` |
| `LAG_TABLE` | `LAG_TABLE\|<lag>` | `LAG_TABLE\|PortChannel100` |
| `LAG_MEMBER_TABLE` | `LAG_MEMBER_TABLE\|<lag>\|<member>` | `LAG_MEMBER_TABLE\|PortChannel100\|Ethernet0` |
| `VLAN_TABLE` | `VLAN_TABLE\|<vlan>` | `VLAN_TABLE\|Vlan100` |
| `VRF_TABLE` | `VRF_TABLE\|<vrf>` | `VRF_TABLE\|Vrf_CUST1` |
| `VXLAN_TUNNEL_TABLE` | `VXLAN_TUNNEL_TABLE\|<name>` | `VXLAN_TUNNEL_TABLE\|vtep1` |
| `BGP_NEIGHBOR_TABLE` | `BGP_NEIGHBOR_TABLE\|<vrf>\|<neighbor>` | `BGP_NEIGHBOR_TABLE\|default\|10.0.0.2` |
| `INTERFACE_TABLE` | `INTERFACE_TABLE\|<intf>` | `INTERFACE_TABLE\|Ethernet0` |
| `NEIGH_TABLE` | `NEIGH_TABLE\|<intf>\|<ip>` | `NEIGH_TABLE\|Ethernet0\|10.0.0.2` |
| `FDB_TABLE` | `FDB_TABLE\|<vlan>\|<mac>` | `FDB_TABLE\|Vlan100\|00:11:22:33:44:55` |
| `ROUTE_TABLE` | `ROUTE_TABLE\|<vrf>\|<prefix>` | `ROUTE_TABLE\|default\|10.1.0.0/31` |
| `TRANSCEIVER_INFO` | `TRANSCEIVER_INFO\|<port>` | `TRANSCEIVER_INFO\|Ethernet0` |
| `TRANSCEIVER_STATUS` | `TRANSCEIVER_STATUS\|<port>` | `TRANSCEIVER_STATUS\|Ethernet0` |
| `NEWTRON_LOCK` | `NEWTRON_LOCK\|<device>` | `NEWTRON_LOCK\|spine1` |

### 5.2 State Entry Types

```go
type PortStateEntry struct {
    AdminStatus  string `json:"admin_status,omitempty"`
    OperStatus   string `json:"oper_status,omitempty"`
    Speed        string `json:"speed,omitempty"`
    MTU          string `json:"mtu,omitempty"`
    LinkTraining string `json:"link_training,omitempty"`
}

type LAGStateEntry struct {
    OperStatus string `json:"oper_status,omitempty"`
    Speed      string `json:"speed,omitempty"`
    MTU        string `json:"mtu,omitempty"`
}

type LAGMemberStateEntry struct {
    OperStatus     string `json:"oper_status,omitempty"`
    CollectingDist string `json:"collecting_distributing,omitempty"`
    Selected       string `json:"selected,omitempty"`
    ActorPortNum   string `json:"actor_port_num,omitempty"`
    PartnerPortNum string `json:"partner_port_num,omitempty"`
}

type BGPNeighborStateEntry struct {
    State           string `json:"state,omitempty"`
    RemoteAS        string `json:"remote_asn,omitempty"`
    LocalAS         string `json:"local_asn,omitempty"`
    PeerGroup       string `json:"peer_group,omitempty"`
    PfxRcvd         string `json:"prefixes_received,omitempty"`
    PfxSent         string `json:"prefixes_sent,omitempty"`
    MsgRcvd         string `json:"msg_rcvd,omitempty"`
    MsgSent         string `json:"msg_sent,omitempty"`
    Uptime          string `json:"uptime,omitempty"`
    HoldTime        string `json:"holdtime,omitempty"`
    KeepaliveTime   string `json:"keepalive,omitempty"`
    ConnectRetry    string `json:"connect_retry,omitempty"`
    LastResetReason string `json:"last_reset_reason,omitempty"`
}

type VXLANTunnelStateEntry struct {
    SrcIP      string `json:"src_ip,omitempty"`
    OperStatus string `json:"operstatus,omitempty"`
}

type VLANStateEntry struct {
    OperStatus string `json:"oper_status,omitempty"`
    State      string `json:"state,omitempty"`
}

type VRFStateEntry struct {
    State string `json:"state,omitempty"`
}

type InterfaceStateEntry struct {
    VRF      string `json:"vrf,omitempty"`
    ProxyArp string `json:"proxy_arp,omitempty"`
}

type NeighStateEntry struct {
    Family string `json:"family,omitempty"`
    MAC    string `json:"neigh,omitempty"`
    State  string `json:"state,omitempty"`
}

type FDBStateEntry struct {
    Port       string `json:"port,omitempty"`
    Type       string `json:"type,omitempty"`
    VNI        string `json:"vni,omitempty"`
    RemoteVTEP string `json:"remote_vtep,omitempty"`
}

type RouteStateEntry struct {
    NextHop   string `json:"nexthop,omitempty"`
    Interface string `json:"ifname,omitempty"`
    Protocol  string `json:"protocol,omitempty"`
}

type TransceiverInfoEntry struct {
    Vendor          string `json:"vendor_name,omitempty"`
    Model           string `json:"model,omitempty"`
    SerialNum       string `json:"serial_num,omitempty"`
    HardwareVersion string `json:"hardware_version,omitempty"`
    Type            string `json:"type,omitempty"`
    MediaInterface  string `json:"media_interface,omitempty"`
}

type TransceiverStatusEntry struct {
    Present     string `json:"present,omitempty"`
    Temperature string `json:"temperature,omitempty"`
    Voltage     string `json:"voltage,omitempty"`
    TxPower     string `json:"tx_power,omitempty"`
    RxPower     string `json:"rx_power,omitempty"`
}
```

### 5.3 StateDBClient

```go
type StateDBClient struct {
    client *redis.Client
    ctx    context.Context
}

func NewStateDBClient(addr string) *StateDBClient
func (c *StateDBClient) Connect() error
func (c *StateDBClient) Close() error

// Bulk read
func (c *StateDBClient) GetAll() (*StateDB, error)

// Targeted reads
func (c *StateDBClient) GetEntry(table, key string) (map[string]string, error) // Generic raw entry
func (c *StateDBClient) GetBGPNeighborState(vrf, neighbor string) (*BGPNeighborStateEntry, error)
func (c *StateDBClient) GetNeighbor(iface, ip string) (*NeighEntry, error)

// Distributed locking
func (c *StateDBClient) AcquireLock(device, holder string, ttlSeconds int) error
func (c *StateDBClient) ReleaseLock(device, holder string) error

// Legacy intent migration (one-time, intents now live in CONFIG_DB)
func (c *StateDBClient) ReadIntentFromStateDB(device string) (*OperationIntent, error)
func (c *StateDBClient) DeleteIntentFromStateDB(device string) error
```

**`GetEntry`** reads a single STATE_DB entry as raw `map[string]string`. Returns `(nil, nil)` if the entry does not exist. Used by newtrun's `verifyStateDBExecutor` for generic table/key/field assertions.

**`GetBGPNeighborState`** looks up `BGP_NEIGHBOR_TABLE|<vrf>|<neighbor>`. If not found, retries without the VRF prefix (default VRF fallback, since some SONiC versions omit "default").

**`GetNeighbor`** reads an ARP/NDP entry from `NEIGH_TABLE|<interface>|<ip>`. Returns nil (not error) if absent.

STATE_DB bulk loading uses the same cursor-based `SCAN` pattern as CONFIG_DB, with a parallel table-driven parser registry in `statedb_parsers.go` (**13 registered parsers**: PORT_TABLE, LAG_TABLE, LAG_MEMBER_TABLE, VLAN_TABLE, VRF_TABLE, VXLAN_TUNNEL_TABLE, BGP_NEIGHBOR_TABLE, INTERFACE_TABLE, NEIGH_TABLE, FDB_TABLE, ROUTE_TABLE, TRANSCEIVER_INFO, TRANSCEIVER_STATUS).

### 5.4 Distributed Locking

Distributed locks prevent concurrent newtron sessions from writing to the same device simultaneously. Locks are stored in STATE_DB as `NEWTRON_LOCK|<device>` hash entries.

**NEWTRON_LOCK entry format** (Redis hash in STATE_DB):

```
NEWTRON_LOCK|spine1
  holder:   "aldrin@workstation1"
  acquired: "2026-02-07T16:00:00Z"
  ttl:      "3600"
```

The key has a Redis EXPIRE set to `ttl` seconds, so it auto-deletes if the holder crashes.

**Lock acquisition** uses a Lua script for atomic check-and-set:

```lua
-- Returns 1 on success, 0 if already locked by another holder
local key = KEYS[1]
if redis.call("EXISTS", key) == 1 then
    return 0
end
redis.call("HSET", key, "holder", ARGV[1], "acquired", ARGV[2], "ttl", ARGV[3])
redis.call("EXPIRE", key, tonumber(ARGV[3]))
return 1
```

**Lock release** uses a Lua script with holder verification:

```lua
-- Returns 1 on success, 0 if holder mismatch, -1 if key doesn't exist
local key = KEYS[1]
if redis.call("EXISTS", key) == 0 then
    return -1
end
local current = redis.call("HGET", key, "holder")
if current ~= ARGV[1] then
    return 0
end
redis.call("DEL", key)
return 1
```

**Why STATE_DB for locks:**
- Locks are operational state, not configuration — they should not persist across `config save -y` or device reboots
- STATE_DB is ephemeral: cleared on reboot, which is correct — a rebooted device has no active sessions
- SONiC daemons do not subscribe to `NEWTRON_LOCK` entries, so no unintended side effects

**Why Lua scripts (not plain SET NX):**
- The lock entry is a Redis hash (multiple fields: holder, acquired, ttl) — `SET NX` only works on string values
- Lua scripts execute atomically on the Redis server — no race window between EXISTS and HSET
- EXPIRE is set in the same atomic script as HSET, ensuring TTL is always applied
- Release atomically verifies holder before DEL (prevents releasing someone else's lock)

**Lock delegation from Device to Node (newtron LLD §3.2–3.3):**

`Device.Lock(holder, ttl)` stores the holder string in `d.lockHolder` and calls `d.stateClient.AcquireLock()`. `Device.Unlock()` reads `d.lockHolder` and calls `d.stateClient.ReleaseLock()` — the caller does not need to pass the holder string again.

`node.Node.Lock()` constructs the holder string as `"user@hostname"`, delegates to `d.Lock(holder, ttl)`, then refreshes the CONFIG_DB cache immediately after acquiring the lock. This starts a **write episode**: precondition checks within the subsequent operation read from this fresh snapshot. If cache refresh fails after lock acquisition, the lock is released and an error is returned — operating with a stale cache under lock is worse than failing the operation.

**Write episode lifecycle:**

`Lock()` (refresh) → `fn()` (precondition reads from cache) → `Apply()` (writes to Redis, no reload) → `Unlock()` (episode ends). No post-Apply refresh — the next episode will refresh itself.

### 5.5 Legacy Intent Migration

STATE_DB previously held `NEWTRON_INTENT|<device>` entries as write-ahead manifests for crash recovery. This model has been replaced by the unified intent model in CONFIG_DB (§4.8).

Intent records now live in CONFIG_DB as per-resource `NEWTRON_INTENT|<resource>` entries (e.g., `NEWTRON_INTENT|interface|Ethernet0`, `NEWTRON_INTENT|device`). Each record is an `Intent` struct (§4.8) that captures the operation, resolved parameters, and DAG relationships. The projection (expected CONFIG_DB state) is derived by replaying all intents — no separate crash-recovery mechanism is needed.

Two legacy migration methods remain on `StateDBClient`:

- `ReadIntentFromStateDB(device)` — reads the old `NEWTRON_INTENT|<device>` entry from STATE_DB. Used during `Lock()` for one-time migration: if a STATE_DB intent is found, it is migrated to CONFIG_DB and the STATE_DB entry is deleted.
- `DeleteIntentFromStateDB(device)` — deletes the old STATE_DB entry after migration.

**Interaction with locking:**

| Record | DB | EXPIRE | Survives reboot? | Purpose |
|--------|-----|--------|------------------|---------|
| `NEWTRON_LOCK` | STATE_DB | 1 hour | No (auto-deletes) | Mutual exclusion |
| `NEWTRON_INTENT` | CONFIG_DB | None | Yes (`config save`) | Desired-state DAG |

The lock controls *access*; intents record *what should exist*. Crash recovery is structural: NEWTRON_INTENT records in CONFIG_DB are the persistent state. The drift guard detects when projection differs from actual CONFIG_DB. `Reconcile` fixes it by delivering the full projection.

---

## 6. APP_DB (`pkg/newtron/device/sonic/appldb.go`)

APP_DB (Redis DB 0) contains application-level state written by SONiC daemons. For route verification, newtron reads `ROUTE_TABLE` entries written by `fpmsyncd` (the FPM-to-Redis daemon that syncs FRR's RIB into APP_DB).

**Consumer note:** newtrun's `verifyRouteExecutor` calls `Client.GetRoute()` (HTTP API client), which calls the newtron-server, which internally calls `AppDBClient.GetRoute()`. The executor interprets the returned `RouteEntry` to assert protocol, next-hop, and presence.

### 6.1 AppDBClient

```go
type AppDBClient struct {
    client *redis.Client
    ctx    context.Context
}

func NewAppDBClient(addr string) *AppDBClient
func (c *AppDBClient) Connect() error
func (c *AppDBClient) Close() error

// GetRoute reads a single route from ROUTE_TABLE by VRF and prefix.
// Returns nil (not error) if the prefix does not exist.
// Parses comma-separated nexthop/ifname into []NextHop.
// For /32 host routes, retries without the mask if the initial lookup fails
// (fpmsyncd sometimes omits the /32 suffix).
func (c *AppDBClient) GetRoute(vrf, prefix string) (*RouteEntry, error)
```

**APP_DB key format:**
- Default VRF: `ROUTE_TABLE:<prefix>` (e.g., `ROUTE_TABLE:10.1.0.0/31`)
- Non-default VRF: `ROUTE_TABLE:<vrf>:<prefix>` (e.g., `ROUTE_TABLE:Vrf_CUST1:192.168.1.0/24`)

**Note:** APP_DB uses colon (`:`) as the table-key separator, unlike CONFIG_DB and STATE_DB which use pipe (`|`). This is a SONiC convention — APP_DB entries are written by producer daemons that follow a different key format.

**ECMP convention:** For multi-path routes, `nexthop` and `ifname` are comma-separated with positional correspondence:
```
nexthop:  "10.0.0.1,10.0.0.3"
ifname:   "Ethernet0,Ethernet4"
→ NextHop{IP: "10.0.0.1", Interface: "Ethernet0"}
→ NextHop{IP: "10.0.0.3", Interface: "Ethernet4"}
```

### 6.2 Route Observation Types

Returned by both `AppDBClient.GetRoute()` (APP_DB) and `AsicDBClient.GetRouteASIC()` (ASIC_DB, §7). Defined in `types.go`.

```go
type RouteSource string

const (
    RouteSourceAppDB  RouteSource = "APP_DB"
    RouteSourceAsicDB RouteSource = "ASIC_DB"
)

type RouteEntry struct {
    Prefix   string      // "10.1.0.0/31"
    VRF      string      // "default", "Vrf_CUST1"
    Protocol string      // "bgp", "connected", "static" (APP_DB only)
    NextHops []NextHop
    Source   RouteSource // which database this was read from
}

type NextHop struct {
    IP        string // "10.0.0.1" (or "0.0.0.0" for connected)
    Interface string // "Ethernet0", "Vlan500" (APP_DB only)
}
```

**Single-shot reads:** Both `GetRoute` and `GetRouteASIC` perform a single Redis read and return immediately. They do not poll or retry. If the caller needs to wait for route convergence (e.g., waiting for BGP to install a route), the caller must implement its own polling loop. newtrun's `verifyRouteExecutor` does exactly this.

---

## 7. ASIC_DB (`pkg/newtron/device/sonic/asicdb.go`)

ASIC_DB (Redis DB 1) contains SAI (Switch Abstraction Interface) objects that represent what is actually programmed in hardware. Reading routes from ASIC_DB confirms that the data plane is programmed, not just the control plane.

**Consumer note:** newtrun's `verifyRouteExecutor` calls `Client.GetRouteASIC()` (HTTP API client) when `expect.source == "asic_db"`. The server internally calls `AsicDBClient.GetRouteASIC()` to confirm data-plane programming.

### 7.1 SAI Object Chain

Unlike APP_DB's flat key-value routes, ASIC_DB stores routes as a chain of SAI objects that must be resolved:

```
Step 1: ASIC_STATE:SAI_OBJECT_TYPE_ROUTE_ENTRY:{"dest":"10.1.0.0/31","vr":"oid:0x3..."}
        → SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID = "oid:0x5000..."

Step 2: ASIC_STATE:SAI_OBJECT_TYPE_NEXT_HOP_GROUP:oid:0x5000...
        → (scan for GROUP_MEMBERs referencing this group)

Step 3: ASIC_STATE:SAI_OBJECT_TYPE_NEXT_HOP:oid:0x4000...
        → SAI_NEXT_HOP_ATTR_IP = "10.0.0.1"
```

For single-path routes, step 1 points directly to a `SAI_OBJECT_TYPE_NEXT_HOP` (skipping the group). The client handles both cases via `resolveNextHops()`.

**Route entry key format:** JSON-encoded with canonical formatting (sorted keys, no whitespace):

```
ASIC_STATE:SAI_OBJECT_TYPE_ROUTE_ENTRY:{"dest":"10.1.0.0/31","switch_id":"oid:0x21...","vr":"oid:0x3..."}
```

### 7.2 OID Discovery

**switch_id OID:** The same for all routes on a device. Discovered on `Connect()` by scanning for the single `ASIC_STATE:SAI_OBJECT_TYPE_SWITCH:oid:0x...` key.

**Default VR OID:** Read from the switch object's `SAI_SWITCH_ATTR_DEFAULT_VIRTUAL_ROUTER_ID` attribute. Cached on `Connect()`.

**Named VRF VR OID:** SONiC creates a VR OID for each VRF. Resolution algorithm:
1. Read the VRF's connected prefix from CONFIG_DB (first IP entry in `INTERFACE|<vrf-member>|<ip>`)
2. Scan `ASIC_STATE:SAI_OBJECT_TYPE_ROUTE_ENTRY:*` keys for a JSON key whose `dest` matches the known connected prefix
3. Extract the `vr` OID from the matching JSON key
4. Cache the mapping in `vrfOIDs` to avoid repeated scans

### 7.3 AsicDBClient

```go
type AsicDBClient struct {
    client    *redis.Client
    ctx       context.Context
    switchOID string            // cached switch OID (discovered on Connect)
    defaultVR string            // cached default Virtual Router OID
    vrfOIDs   map[string]string // VRF name → VR OID (populated on demand, cached)
}

func NewAsicDBClient(addr string) *AsicDBClient
func (c *AsicDBClient) Close() error

// Connect establishes the Redis connection and discovers the switch and
// default VR OIDs required for all subsequent route lookups.
func (c *AsicDBClient) Connect() error

// ResolveVROID returns the VR OID for a given VRF name. Returns the cached
// value if available; otherwise performs CONFIG_DB-based discovery (§7.2).
func (c *AsicDBClient) ResolveVROID(vrfName string, configDB *ConfigDB) (string, error)

// GetRouteASIC reads a route from ASIC_DB by resolving the SAI object chain (§7.1).
// Returns nil (not error) if the route is not programmed in ASIC.
// The configDB parameter is needed for VR OID resolution of named VRFs.
func (c *AsicDBClient) GetRouteASIC(vrf, prefix string, configDB *ConfigDB) (*RouteEntry, error)
```

Key scanning uses cursor-based `SCAN` (via `scanKeys()`) to avoid O(N) `KEYS` commands.

---

## 8. Verification Types (`pkg/newtron/device/sonic/types.go`)

These types are returned by verification operations at the node layer. Defined in the `sonic` package because they describe CONFIG_DB-level verification results.

```go
// VerificationResult reports ChangeSet verification outcome.
// Returned by ChangeSet.Verify() at the node layer after re-reading CONFIG_DB.
type VerificationResult struct {
    Passed int                 // entries that matched
    Failed int                 // entries missing or mismatched
    Errors []VerificationError // details of each failure
}

// VerificationError describes a single verification failure.
type VerificationError struct {
    Table    string
    Key      string
    Field    string
    Expected string
    Actual   string // "" if missing
}

// NeighEntry represents a neighbor (ARP/NDP) entry read from a device.
// Returned by StateDBClient.GetNeighbor().
type NeighEntry struct {
    IP        string // "10.20.0.1"
    Interface string // "Ethernet1", "Vlan100"
    MAC       string // "aa:bb:cc:dd:ee:ff"
    Family    string // "IPv4", "IPv6"
}
```

---

## 9. SONiC Redis Database Layout

SONiC uses multiple Redis databases within a single Redis instance:

| DB | Name | Purpose | Newtron Access |
|----|------|---------|----------------|
| 0 | APPL_DB | Application state (routes from fpmsyncd) | **Read** — `AppDBClient.GetRoute()` |
| 1 | ASIC_DB | ASIC-programmed state (SAI objects) | **Read** — `AsicDBClient.GetRouteASIC()` |
| 2 | COUNTERS_DB | Interface/port counters | Not used |
| 3 | LOGLEVEL_DB | Logging configuration | Not used |
| 4 | CONFIG_DB | Configuration (ports, VLANs, BGP, etc.) | **Read/Write** — `ConfigDBClient.*` |
| 5 | FLEX_COUNTER_DB | Flexible counters | Not used |
| 6 | STATE_DB | Operational state + distributed locks | **Read** + lock via `StateDBClient.*` |

---

## 10. Config Persistence

Redis changes made by newtron are **runtime only**. They take effect immediately because SONiC daemons subscribe to CONFIG_DB keyspace notifications, but they do not survive a device reboot.

To persist configuration across reboots, the SONiC command `config save -y` must be run inside the VM. This writes the current CONFIG_DB contents to `/etc/sonic/config_db.json`, which is loaded at boot. The node layer runs this via `SSHTunnel.ExecCommand("sudo config save -y")`.

**Config reload:** `config reload -y` re-reads `/etc/sonic/config_db.json` and replaces the running CONFIG_DB. The node layer uses `ExecCommandContext` with retry logic — on fresh CiscoVS boot, SwSS may not be ready, so the reload is retried every 5 seconds for up to 90 seconds.

**Reconcile flow — two modes:**

- **Full mode** (default): `Reconcile()` runs `config reload -y` to restore CONFIG_DB to the saved baseline, then calls `ExportEntries()` to extract the full projection and `ReplaceAll(entries, ownedTables)` (§3.7) to deliver it. After delivery, `config save -y` persists the reconciled state so subsequent reloads re-read the reconciled config rather than factory defaults.

- **Delta mode**: `Reconcile()` skips config reload. It rebuilds the projection from intents, calls `DiffConfigDB` (§3.9) to identify drifted entries, then calls `ApplyDrift(diffs)` (§3.7) to patch only those entries. NEWTRON_INTENT records are delivered separately via `PipelineSet(ExportIntentEntries())` because they are excluded from drift detection. `config save -y` is called after to persist the patched state. Delta mode is faster and less disruptive — no daemon churn from a full config reload.

**Implications for testing:**

| Test Type | Persistence | Cleanup Strategy |
|-----------|------------|------------------|
| Unit tests | N/A (no Redis) | N/A |
| Integration tests | Ephemeral (standalone Redis) | Fresh Redis per test |
| E2E lab tests | Runtime only (SONiC-VS) | Redeploy topology for clean baseline |
