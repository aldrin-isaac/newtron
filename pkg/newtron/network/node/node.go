package node

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"sort"
	"strings"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// defaultLockTTL is the TTL in seconds for distributed device locks.
const defaultLockTTL = 3600 // 1 hour

// SpecProvider is the interface that Node uses to access Network-level specs.
// Network implements this interface; Node embeds it so that callers can write
// node.GetService("x") directly.
type SpecProvider interface {
	GetService(name string) (*spec.ServiceSpec, error)
	GetIPVPN(name string) (*spec.IPVPNSpec, error)
	GetMACVPN(name string) (*spec.MACVPNSpec, error)
	GetQoSPolicy(name string) (*spec.QoSPolicy, error)
	GetFilter(name string) (*spec.FilterSpec, error)
	GetPlatform(name string) (*spec.PlatformSpec, error)
	GetPrefixList(name string) ([]string, error)
	GetRoutePolicy(name string) (*spec.RoutePolicy, error)
	FindMACVPNByVNI(vni int) (string, *spec.MACVPNSpec)
}

// Node represents a SONiC switch within the context of a Network.
//
// Key design: Node embeds a SpecProvider (implemented by Network), giving it
// direct access to all Network-level configuration (services, filters, etc.)
// without importing the network package (avoiding circular imports).
//
// The Node's primary state is its intent collection (NEWTRON_INTENT records)
// and the projection (typed CONFIG_DB tables derived from intent replay).
// The projection is never loaded from the device — it is always built by
// replaying intents through config functions.
//
// Same code path, different initialization. The Interface is the point of service in both modes.
//
// Hierarchy: Network -> Node -> Interface
type Node struct {
	SpecProvider // embedded — n.GetService() just works

	// Device identity
	name     string
	profile  *spec.DeviceProfile
	resolved *spec.ResolvedProfile

	// Child objects - Interfaces created in this Node's context
	interfaces map[string]*Interface

	// Connection and state (delegated to sonic.Device)
	conn     *sonic.Device
	configDB *sonic.ConfigDB

	// State
	connected bool
	locked    bool

	// actuatedIntent is true when the node was initialized from device
	// NEWTRON_INTENT records (actuated mode). False when initialized from
	// topology.json (topology mode). Controls drift guard in Lock.
	actuatedIntent bool

	// unsavedIntents tracks whether CRUD mutations have been made since
	// the last Save or construction. Set by writeIntent/deleteIntent,
	// cleared by ClearUnsavedIntents after Save.
	unsavedIntents bool
}

// New creates a new Node with the given SpecProvider and profile.
func New(sp SpecProvider, name string, profile *spec.DeviceProfile, resolved *spec.ResolvedProfile) *Node {
	return &Node{
		SpecProvider: sp,
		name:         name,
		profile:      profile,
		resolved:     resolved,
		interfaces:   make(map[string]*Interface),
	}
}

// NewAbstract creates an abstract Node with an empty projection.
// Operations replay intents, rendering entries into the projection.
// Preconditions check the intent DB (no connected/locked requirement).
//
// Usage:
//
//	n := node.NewAbstract(specs, "switch1", profile, resolved)
//	n.RegisterPort("Ethernet0", map[string]string{"admin_status": "up"})
//	iface, _ := n.GetInterface("Ethernet0")
//	iface.ApplyService(ctx, "transit", node.ApplyServiceOpts{...})
//	n.Drift(ctx) // or n.Reconcile(ctx, ReconcileOpts{Mode: "full"})
func NewAbstract(sp SpecProvider, name string, profile *spec.DeviceProfile, resolved *spec.ResolvedProfile) *Node {
	return &Node{
		SpecProvider: sp,
		name:         name,
		profile:      profile,
		resolved:     resolved,
		interfaces:   make(map[string]*Interface),
		configDB:     sonic.NewConfigDB(),
	}
}

// HasActuatedIntent returns true if this node was initialized from device intents.
func (n *Node) HasActuatedIntent() bool { return n.actuatedIntent }

// HasUnsavedIntents returns true if CRUD mutations were made since last Save/construction.
func (n *Node) HasUnsavedIntents() bool { return n.unsavedIntents }

// ClearUnsavedIntents resets the unsaved mutation flag after Save.
func (n *Node) ClearUnsavedIntents() { n.unsavedIntents = false }

// SnapshotIntentDB returns a deep copy of the intent DB (NEWTRON_INTENT map).
// Used before dry-run operations so the intent DB can be restored afterward.
// The projection is NOT included — it is rebuilt separately via RebuildProjection.
func (n *Node) SnapshotIntentDB() map[string]map[string]string {
	snap := make(map[string]map[string]string, len(n.configDB.NewtronIntent))
	for resource, fields := range n.configDB.NewtronIntent {
		copied := make(map[string]string, len(fields))
		for k, v := range fields {
			copied[k] = v
		}
		snap[resource] = copied
	}
	return snap
}

// RestoreIntentDB replaces the intent DB with a previously snapshotted copy.
// The projection is left dirty — the caller is responsible for rebuilding it
// via RebuildProjection (or relying on the next execute() to do so).
func (n *Node) RestoreIntentDB(snapshot map[string]map[string]string) {
	n.configDB.NewtronIntent = snapshot
}

// RebuildProjection rebuilds the projection from the intent DB.
// In actuated mode (transport connected), re-reads NEWTRON_INTENT from the
// device's CONFIG_DB via Redis — the device's intents are the authority.
// In topology mode (no transport), replays from the cached intent DB.
//
// Creates a fresh configDB, re-registers ports, and replays all intents.
// The SSH connection and transport state are preserved.
//
// Architecture §1: "Intent DB is primary state. The projection is derived
// from intent replay."
// CLAUDE.md: "In actuated mode, the device's own NEWTRON_INTENT records
// ARE the authoritative state."
func (n *Node) RebuildProjection(ctx context.Context) error {
	// In actuated mode, re-read intents from the device — they are the authority.
	intents := n.configDB.NewtronIntent
	if n.conn != nil {
		client := n.conn.Client()
		if client != nil {
			fresh, err := client.GetRawTable("NEWTRON_INTENT")
			if err != nil {
				return fmt.Errorf("re-reading intents from device: %w", err)
			}
			intents = fresh
		}
	}

	// Save port entries — ports come from init, not from intents.
	ports := n.configDB.ExportPorts()

	// Fresh projection.
	n.configDB = sonic.NewConfigDB()
	n.interfaces = make(map[string]*Interface)

	// Re-register ports.
	for portName, fields := range ports {
		n.RegisterPort(portName, fields)
	}

	// Replay is reconstruction, not mutation. Temporarily clear actuatedIntent
	// so precondition() skips the Connected+Locked check during replay — same
	// approach as InitFromDeviceIntent which sets actuatedIntent AFTER replay.
	wasActuated := n.actuatedIntent
	wasUnsaved := n.unsavedIntents
	n.actuatedIntent = false

	// Do NOT pre-populate configDB.NewtronIntent — let writeIntent populate it
	// during replay. Pre-populating causes idempotency guards (GetIntent check
	// at the top of config methods) to see existing intents and skip rendering,
	// which leaves the projection empty. This matches BuildAbstractNode and
	// InitFromDeviceIntent where the intent DB starts empty before replay.
	steps := IntentsToSteps(intents)
	for _, step := range steps {
		if err := ReplayStep(ctx, n, step); err != nil {
			n.actuatedIntent = wasActuated
			return fmt.Errorf("rebuilding projection, replay %s: %w", step.URL, err)
		}
	}

	// Restore actuated state and unsavedIntents. writeIntent sets
	// unsavedIntents = true during replay, but replay is reconstruction,
	// not a new CRUD mutation. Restoring from the saved value preserves the
	// pre-rebuild semantics:
	// - After initial construction (BuildAbstractNode/InitFromDeviceIntent
	//   cleared it): stays false — topology replay is not a mutation.
	// - After CRUD (user created a VLAN without saving): stays true — the
	//   unsaved intent guard must still block mode switching.
	// - After re-reading from device (actuated rebuild): stays false —
	//   device intents are persisted, not unsaved.
	n.actuatedIntent = wasActuated
	n.unsavedIntents = wasUnsaved
	return nil
}

// DisconnectTransport closes the SSH tunnel + Redis connection without
// disturbing the projection. Used when switching from topology-online
// back to topology-offline.
func (n *Node) DisconnectTransport() {
	if n.conn != nil {
		n.conn.Disconnect()
		n.conn = nil
		n.connected = false
		n.locked = false
	}
}

// ============================================================================
// Intent Accessors
// ============================================================================
//
// Intent state is ConfigDB state — configDB.NewtronIntent is the single source.
// No separate in-memory map. render already updates ConfigDB, so writes
// via ChangeSet are automatically visible to reads.

// GetIntent returns the intent for the given resource, or nil if none exists.
// Reads from configDB.NewtronIntent and constructs sonic.Intent on demand.
func (n *Node) GetIntent(resource string) *sonic.Intent {
	if n.configDB == nil {
		return nil
	}
	fields, ok := n.configDB.NewtronIntent[resource]
	if !ok {
		return nil
	}
	return sonic.NewIntent(resource, fields)
}

// Intents returns all intents on this node.
func (n *Node) Intents() map[string]*sonic.Intent {
	if n.configDB == nil || len(n.configDB.NewtronIntent) == 0 {
		return nil
	}
	result := make(map[string]*sonic.Intent, len(n.configDB.NewtronIntent))
	for resource, fields := range n.configDB.NewtronIntent {
		result[resource] = sonic.NewIntent(resource, fields)
	}
	return result
}

// ServiceIntents returns all actuated service intents (apply-service).
func (n *Node) ServiceIntents() map[string]*sonic.Intent {
	result := make(map[string]*sonic.Intent)
	if n.configDB == nil {
		return result
	}
	for resource, fields := range n.configDB.NewtronIntent {
		intent := sonic.NewIntent(resource, fields)
		if intent.IsService() && intent.IsActuated() {
			result[resource] = intent
		}
	}
	return result
}

// IntentsByPrefix returns all intents whose resource key starts with prefix.
// Example: IntentsByPrefix("vlan|") → all VLAN intents.
// Scans the intent DB (NEWTRON_INTENT), not the projection.
func (n *Node) IntentsByPrefix(prefix string) map[string]*sonic.Intent {
	result := make(map[string]*sonic.Intent)
	if n.configDB == nil {
		return result
	}
	for resource, fields := range n.configDB.NewtronIntent {
		if strings.HasPrefix(resource, prefix) {
			result[resource] = sonic.NewIntent(resource, fields)
		}
	}
	return result
}

// IntentsByParam returns intents where params[key] == value.
// Example: IntentsByParam("vrf", "CUSTOMER") → intents bound to that VRF.
// Scans the intent DB (NEWTRON_INTENT), not the projection.
func (n *Node) IntentsByParam(key, value string) map[string]*sonic.Intent {
	result := make(map[string]*sonic.Intent)
	if n.configDB == nil {
		return result
	}
	for resource, fields := range n.configDB.NewtronIntent {
		intent := sonic.NewIntent(resource, fields)
		if intent.Params[key] == value {
			result[resource] = intent
		}
	}
	return result
}

// IntentsByOp returns intents with the given operation type.
// Example: IntentsByOp("configure-irb") → all IRB configuration intents.
// Scans the intent DB (NEWTRON_INTENT), not the projection.
func (n *Node) IntentsByOp(op string) map[string]*sonic.Intent {
	result := make(map[string]*sonic.Intent)
	if n.configDB == nil {
		return result
	}
	for resource, fields := range n.configDB.NewtronIntent {
		intent := sonic.NewIntent(resource, fields)
		if intent.Operation == op {
			result[resource] = intent
		}
	}
	return result
}

// Tree reads the intent DB and returns the ordered intent steps.
// Works in both modes — intents exist from topology replay (offline) or
// device intent replay (online).
func (n *Node) Tree() *spec.TopologyDevice {
	dev := &spec.TopologyDevice{}
	if n.configDB == nil || len(n.configDB.NewtronIntent) == 0 {
		return dev
	}

	// Convert all actuated intents to ordered topology steps.
	dev.Steps = IntentsToSteps(n.configDB.NewtronIntent)
	return dev
}

// Drift compares the projection (expected state from intent replay) against
// the device's actual CONFIG_DB. Auto-connects transport if needed.
func (n *Node) Drift(ctx context.Context) ([]sonic.DriftEntry, error) {
	if n.conn == nil {
		if err := n.ConnectTransport(ctx); err != nil {
			return nil, fmt.Errorf("connecting transport for drift: %w", err)
		}
	}

	expected := n.configDB.ExportRaw()
	actual, err := n.conn.Client().GetRawOwnedTables(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading actual CONFIG_DB: %w", err)
	}
	return sonic.DiffConfigDB(expected, actual, sonic.OwnedTables()), nil
}

// ReconcileOpts controls the Reconcile delivery mechanism.
type ReconcileOpts struct {
	Mode string // "full" or "delta"
}

// ReconcileResult reports the outcome of a Reconcile operation.
type ReconcileResult struct {
	Mode     string // "full" or "delta"
	Applied  int
	Missing  int // entries added (delta only)
	Extra    int // entries removed (delta only)
	Modified int // entries corrected (delta only)
}

// Reconcile delivers the projection to the device, eliminating drift.
// Auto-connects transport if needed.
//
// Two modes:
//   - "full": config reload → ExportEntries → ReplaceAll (rewrites everything)
//   - "delta": Drift → ApplyDrift (patches only drifted entries, no reload)
//
// Reconcile acquires the Redis lock directly (bypassing the drift guard in
// Lock) — its purpose IS to fix drift.
func (n *Node) Reconcile(ctx context.Context, opts ReconcileOpts) (*ReconcileResult, error) {
	if n.conn == nil {
		if err := n.ConnectTransport(ctx); err != nil {
			return nil, fmt.Errorf("connecting transport for reconcile: %w", err)
		}
	}

	if opts.Mode == "full" {
		return n.reconcileFull(ctx)
	}
	return n.reconcileDelta(ctx)
}

// reconcileFull is the original Reconcile path: config reload → ReplaceAll.
func (n *Node) reconcileFull(ctx context.Context) (*ReconcileResult, error) {
	// Config reload — reset device to factory baseline (best-effort).
	if err := n.ConfigReload(ctx); err != nil {
		util.WithDevice(n.name).Warnf("config reload failed (continuing): %v", err)
	}

	// Wait for Redis after config reload.
	if err := n.PingWithRetry(ctx, 60*time.Second); err != nil {
		return nil, fmt.Errorf("waiting for Redis after config reload: %w", err)
	}

	// Acquire lock directly — bypass Node-level Lock() to avoid drift guard.
	// Reconcile's purpose IS to fix drift; circular to refuse it.
	holder := BuildLockHolder()
	if err := n.conn.Lock(holder, defaultLockTTL); err != nil {
		return nil, fmt.Errorf("acquiring lock for reconcile: %w", err)
	}
	n.locked = true

	// Export projection and deliver atomically.
	// OwnedTables() excludes NEWTRON_INTENT (correctly — it's not compared for
	// drift). But Reconcile delivers the full state including intent records
	// (architecture §4: "In Redis delivery order, intent records arrive BEFORE
	// config entries"). ExportEntries includes NEWTRON_INTENT; ReplaceAll must
	// clean stale intent keys too — otherwise Clear + Reconcile leaves orphaned
	// NEWTRON_INTENT records on the device.
	entries := n.configDB.ExportEntries()
	deliveryTables := append(sonic.OwnedTables(), "NEWTRON_INTENT")
	if err := n.conn.Client().ReplaceAll(entries, deliveryTables); err != nil {
		n.conn.Unlock()
		n.locked = false
		return nil, fmt.Errorf("delivering projection: %w", err)
	}

	// Persist to config_db.json.
	if err := n.SaveConfig(ctx); err != nil {
		util.WithDevice(n.name).Warnf("config save after reconcile failed: %v", err)
	}

	// Ensure unified config mode (restart bgp if needed).
	if err := n.EnsureUnifiedConfigMode(ctx); err != nil {
		util.WithDevice(n.name).Warnf("ensure unified config mode failed: %v", err)
	}

	n.conn.Unlock()
	n.locked = false

	return &ReconcileResult{Mode: "full", Applied: len(entries)}, nil
}

// reconcileDelta patches only drifted entries: no config reload, no full rewrite.
func (n *Node) reconcileDelta(ctx context.Context) (*ReconcileResult, error) {
	// Acquire lock directly — bypass Node-level Lock() to avoid drift guard.
	holder := BuildLockHolder()
	if err := n.conn.Lock(holder, defaultLockTTL); err != nil {
		return nil, fmt.Errorf("acquiring lock for reconcile: %w", err)
	}
	n.locked = true

	// Deliver intent records first (excluded from DiffConfigDB).
	// ReplaceAll on NEWTRON_INTENT alone — lightweight, ensures intent records
	// are authoritative before config corrections.
	intentEntries := n.configDB.ExportIntentEntries()
	if err := n.conn.Client().ReplaceAll(intentEntries, []string{"NEWTRON_INTENT"}); err != nil {
		n.conn.Unlock()
		n.locked = false
		return nil, fmt.Errorf("delivering intent records: %w", err)
	}

	// Compute drift: projection vs actual device CONFIG_DB.
	expected := n.configDB.ExportRaw()
	actual, err := n.conn.Client().GetRawOwnedTables(ctx)
	if err != nil {
		n.conn.Unlock()
		n.locked = false
		return nil, fmt.Errorf("reading actual CONFIG_DB for delta: %w", err)
	}
	diffs := sonic.DiffConfigDB(expected, actual, sonic.OwnedTables())

	// Apply only the drifted entries.
	if err := n.conn.Client().ApplyDrift(diffs); err != nil {
		n.conn.Unlock()
		n.locked = false
		return nil, fmt.Errorf("applying drift: %w", err)
	}

	// Persist to config_db.json.
	if err := n.SaveConfig(ctx); err != nil {
		util.WithDevice(n.name).Warnf("config save after reconcile failed: %v", err)
	}

	// Ensure unified config mode (restart bgp if needed).
	if err := n.EnsureUnifiedConfigMode(ctx); err != nil {
		util.WithDevice(n.name).Warnf("ensure unified config mode failed: %v", err)
	}

	n.conn.Unlock()
	n.locked = false

	// Build result with breakdown.
	result := &ReconcileResult{Mode: "delta"}
	for _, d := range diffs {
		switch d.Type {
		case "missing":
			result.Missing++
		case "extra":
			result.Extra++
		case "modified":
			result.Modified++
		}
	}
	result.Applied = result.Missing + result.Extra + result.Modified
	return result, nil
}

// RegisterPort creates a PORT entry in the projection.
// This enables GetInterface for offline mode (GetInterface checks PORT table).
func (n *Node) RegisterPort(name string, fields map[string]string) {
	if fields == nil {
		fields = map[string]string{}
	}
	entry := sonic.Entry{Table: "PORT", Key: name, Fields: fields}
	n.configDB.ApplyEntries([]sonic.Entry{entry})
	n.interfaces[name] = &Interface{node: n, name: name}
}

// WiredInterfaces returns sorted Ethernet and PortChannel interface names
// from the projection's PORT table.
func (n *Node) WiredInterfaces() []string {
	var interfaces []string
	for portName := range n.configDB.Port {
		if strings.HasPrefix(portName, "Ethernet") || strings.HasPrefix(portName, "PortChannel") {
			interfaces = append(interfaces, portName)
		}
	}
	sort.Strings(interfaces)
	return interfaces
}

// SetDeviceMetadata writes fields to DEVICE_METADATA|localhost.
// In offline mode, this accumulates the entry. In online mode,
// it requires connected+locked and writes to CONFIG_DB.
func (n *Node) SetDeviceMetadata(ctx context.Context, fields map[string]string) (*ChangeSet, error) {
	if err := n.precondition("set-device-metadata", "localhost").Result(); err != nil {
		return nil, err
	}
	cs := NewChangeSet(n.name, "device.set-device-metadata")
	e := updateDeviceMetadataConfig(fields)
	cs.Update(e.Table, e.Key, e.Fields)
	if err := n.render(cs); err != nil {
		return nil, err
	}
	return cs, nil
}

// render validates entries against the schema, then updates the projection.
// Runs on every path — replay, interactive, online, offline. Invalid entries
// are rejected before they enter the projection.
//
// Architecture §5: "render(cs) validates entries against the schema and
// updates the typed CONFIG_DB tables."
func (n *Node) render(cs *ChangeSet) error {
	if cs == nil {
		return nil
	}
	if err := cs.validate(); err != nil {
		return err
	}
	for _, c := range cs.Changes {
		if c.Type == sonic.ChangeTypeDelete {
			n.configDB.DeleteEntry(c.Table, c.Key)
		} else {
			n.configDB.ApplyEntries([]sonic.Entry{{Table: c.Table, Key: c.Key, Fields: c.Fields}})
		}
	}
	return nil
}

// ============================================================================
// Device Properties
// ============================================================================

// Name returns the device name.
func (n *Node) Name() string {
	return n.name
}

// Profile returns the device profile.
func (n *Node) Profile() *spec.DeviceProfile {
	return n.profile
}

// Resolved returns the resolved configuration (after inheritance).
func (n *Node) Resolved() *spec.ResolvedProfile {
	return n.resolved
}

// MgmtIP returns the management IP address.
func (n *Node) MgmtIP() string {
	return n.resolved.MgmtIP
}

// LoopbackIP returns the loopback IP address.
func (n *Node) LoopbackIP() string {
	return n.resolved.LoopbackIP
}

// ASNumber returns the BGP AS number.
func (n *Node) ASNumber() int {
	return n.resolved.UnderlayASN
}

// RouterID returns the BGP router ID.
func (n *Node) RouterID() string {
	return n.resolved.RouterID
}

// Zone returns the zone name.
func (n *Node) Zone() string {
	return n.resolved.Zone
}

// BGPNeighbors returns the list of BGP neighbor IPs (derived from EVPN peers).
func (n *Node) BGPNeighbors() []string {
	return n.resolved.BGPNeighbors
}

// ConfigDB returns the config_db state.
func (n *Node) ConfigDB() *sonic.ConfigDB {
	return n.configDB
}

// IsUnifiedConfigMode returns true if the device has frrcfgd (unified config
// mode) enabled in DEVICE_METADATA. Delegates to sonic.Device for connected
// nodes; checks projection for abstract/offline nodes.
func (n *Node) IsUnifiedConfigMode() bool {
	if n.conn != nil {
		return n.conn.IsUnifiedConfigMode()
	}
	// Offline/abstract mode — no sonic.Device, check projection directly.
	// This is the only place that duplicates the check logic; physical
	// nodes always delegate to sonic.Device.IsUnifiedConfigMode().
	if n.configDB == nil || n.configDB.DeviceMetadata == nil {
		return false
	}
	localhost, ok := n.configDB.DeviceMetadata["localhost"]
	if !ok {
		return false
	}
	return localhost["docker_routing_config_mode"] == "unified"
}

// Tunnel returns the SSH tunnel for direct command execution.
// Returns nil if no SSH tunnel is configured.
func (n *Node) Tunnel() *sonic.SSHTunnel {
	if n.conn == nil {
		return nil
	}
	return n.conn.Tunnel()
}

// StateDBClient returns the STATE_DB client for operational state queries.
func (n *Node) StateDBClient() *sonic.StateDBClient {
	if n.conn == nil {
		return nil
	}
	return n.conn.StateClient()
}

// ConfigDBClient returns the CONFIG_DB client for direct Redis access.
func (n *Node) ConfigDBClient() *sonic.ConfigDBClient {
	if n.conn == nil {
		return nil
	}
	return n.conn.Client()
}

// StateDB returns the STATE_DB snapshot loaded at connect time.
func (n *Node) StateDB() *sonic.StateDB {
	if n.conn == nil {
		return nil
	}
	return n.conn.StateDB
}

// ============================================================================
// Connection Management
// ============================================================================

// ConnectForSetup connects to the device for one-time bootstrap (InitDevice).
// Unlike intent-first operations, InitDevice needs the actual CONFIG_DB as
// working state — this is pre-intent, before any intents exist on the device.
func (n *Node) ConnectForSetup(ctx context.Context) error {
	if n.connected {
		return nil
	}

	n.conn = sonic.NewDevice(n.name, n.resolved)
	n.conn.SkipFrrcfgdCheck = true
	if err := n.conn.Connect(ctx); err != nil {
		return err
	}

	// InitDevice is pre-intent bootstrap — the device's actual CONFIG_DB
	// IS the working state. Load it into the projection.
	n.configDB = n.conn.ConfigDB
	n.connected = true
	n.loadInterfaces()

	util.WithDevice(n.name).Info("Connected for setup")
	return nil
}

// ConnectTransport establishes SSH tunnel + Redis connection without
// overwriting the projection. Transport is additive — it enables device
// I/O without disturbing expected state built from intent replay.
func (n *Node) ConnectTransport(ctx context.Context) error {
	if n.conn != nil {
		return nil // already connected
	}

	n.conn = sonic.NewDevice(n.name, n.resolved)
	if err := n.conn.Connect(ctx); err != nil {
		return err
	}

	n.connected = true
	util.WithDevice(n.name).Info("Transport connected")
	return nil
}

// InitFromDeviceIntent initializes the node's projection by reading NEWTRON_INTENT
// records from the device and replaying them. This is the actuated-mode boot sequence:
// transport → legacy migration → register ports → replay intents → mark actuated.
//
// Architecture §3: Device intents → ConnectTransport() → read PORT + NEWTRON_INTENT
// → RegisterPort() → IntentsToSteps() → ReplayStep() for each step.
func (n *Node) InitFromDeviceIntent(ctx context.Context) error {
	// Step 1: Establish SSH + Redis transport.
	if err := n.ConnectTransport(ctx); err != nil {
		return fmt.Errorf("connecting transport: %w", err)
	}

	// Step 2: Legacy STATE_DB intent migration.
	// Intents were historically stored in STATE_DB (volatile). New writes go to
	// CONFIG_DB (persistent across reboot). Migrate any legacy records before
	// reading CONFIG_DB intents so they are visible in the subsequent read.
	if stateClient := n.conn.StateClient(); stateClient != nil {
		if legacyIntents, err := stateClient.ReadIntentFromStateDB(n.name); err != nil {
			util.WithDevice(n.name).Warnf("reading legacy STATE_DB intent: %v", err)
		} else if legacyIntents != nil {
			util.WithDevice(n.name).Info("migrating intent from STATE_DB to CONFIG_DB")
			if err := n.conn.Client().WriteIntent(n.name, legacyIntents); err != nil {
				util.WithDevice(n.name).Warnf("writing intent to CONFIG_DB during migration: %v", err)
			} else {
				_ = stateClient.DeleteIntentFromStateDB(n.name)
			}
		}
	}

	// Initialize a fresh projection — intents will be replayed into it.
	// The device's ConfigDB (n.conn.ConfigDB) is the actual state; the
	// node's configDB is the projection built from intent replay.
	n.configDB = sonic.NewConfigDB()

	// Step 3: Register ports from the device's actual CONFIG_DB.
	// Ports are the bridge between physical infrastructure and the
	// abstract node — they come from the device, not from intents.
	for portName, fields := range n.conn.ConfigDB.ExportPorts() {
		n.RegisterPort(portName, fields)
	}

	// Step 4: Read NEWTRON_INTENT records from the device's actual CONFIG_DB.
	// Step 5: Convert intents to an ordered replay sequence.
	steps := IntentsToSteps(n.conn.ConfigDB.NewtronIntent)

	// Step 6: Replay each step to rebuild the projection.
	for _, step := range steps {
		if err := ReplayStep(ctx, n, step); err != nil {
			return fmt.Errorf("replaying step %s: %w", step.URL, err)
		}
	}

	// Step 7: Mark node as actuated — Lock will enforce drift guard.
	n.actuatedIntent = true
	// Step 8: Clear unsaved flag — this is loaded state, not new mutations.
	n.unsavedIntents = false

	util.WithDevice(n.name).Infof("InitFromDeviceIntent: replayed %d intent steps", len(steps))
	return nil
}

// Disconnect closes the connection.
func (n *Node) Disconnect() error {
	if !n.connected {
		return nil
	}

	if n.conn != nil {
		n.conn.Disconnect()
	}

	n.connected = false
	util.WithDevice(n.name).Info("Disconnected")
	return nil
}

// IsConnected returns true if connected.
func (n *Node) IsConnected() bool {
	return n.connected
}

// Ping checks Redis connectivity without touching the projection.
// Returns nil if transport is not connected (no-op — nothing to ping).
func (n *Node) Ping(ctx context.Context) error {
	if n.conn == nil {
		return nil
	}
	return n.conn.Client().Connect()
}

// PingWithRetry polls Ping until Redis is reachable or timeout expires.
// Used by Reconcile after config reload when Redis may be briefly unavailable.
func (n *Node) PingWithRetry(ctx context.Context, timeout time.Duration) error {
	if err := n.Ping(ctx); err == nil {
		return nil
	}

	deadline := time.After(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return n.Ping(ctx) // final attempt
		case <-ticker.C:
			if err := n.Ping(ctx); err == nil {
				return nil
			}
		}
	}
}

// Lock acquires a distributed lock for configuration changes.
// Transport guard: no-op when n.conn == nil (topology-offline mode).
// After lock acquisition, performs legacy STATE_DB migration and drift
// guard (actuated mode only).
//
// Architecture §8: Lock acquires exclusive access. No-op without transport.
// The projection is already fresh when Lock is called — execute() rebuilds
// it from device intents before any operation touches the node.
func (n *Node) Lock(ctx context.Context) error {
	// Transport guard — Lock is a no-op without a wire.
	if n.conn == nil {
		return nil
	}

	if n.locked {
		return nil
	}

	holder := BuildLockHolder()
	if err := n.conn.Lock(holder, defaultLockTTL); err != nil {
		return err
	}
	n.locked = true

	// Migrate any legacy STATE_DB intent to CONFIG_DB (one-time per device).
	// New intents are always written to CONFIG_DB (persistent across reboot).
	if stateClient := n.conn.StateClient(); stateClient != nil {
		if legacyIntent, err := stateClient.ReadIntentFromStateDB(n.name); err != nil {
			util.WithDevice(n.name).Warnf("reading legacy STATE_DB intent: %v", err)
		} else if legacyIntent != nil {
			util.WithDevice(n.name).Info("migrating intent from STATE_DB to CONFIG_DB")
			if err := n.conn.Client().WriteIntent(n.name, legacyIntent); err != nil {
				util.WithDevice(n.name).Warnf("writing intent to CONFIG_DB during migration: %v", err)
			} else {
				_ = stateClient.DeleteIntentFromStateDB(n.name)
			}
		}
	}

	// Drift guard (actuated mode only): refuse writes if device has
	// drifted from its intents. Compares the projection (rebuilt by
	// execute() from fresh device intents) against actual CONFIG_DB.
	//
	// Skip when the intent DB is empty — a fresh device with no intents
	// has no basis for drift. Factory CONFIG_DB entries are pre-intent
	// infrastructure, not drift from intents that don't exist yet.
	// Architecture §8: "the drift guard ensures new intents are never
	// applied on a drifted foundation" — but with zero intents, there
	// is no foundation to drift from.
	if n.actuatedIntent && len(n.configDB.NewtronIntent) > 0 {
		expected := n.configDB.ExportRaw()
		actual, err := n.conn.Client().GetRawOwnedTables(ctx)
		if err != nil {
			n.conn.Unlock()
			n.locked = false
			return fmt.Errorf("drift guard: reading actual CONFIG_DB: %w", err)
		}
		drift := sonic.DiffConfigDB(expected, actual, sonic.OwnedTables())
		if len(drift) > 0 {
			n.conn.Unlock()
			n.locked = false
			return fmt.Errorf("device drifted from intents (%d entries) — reconcile first", len(drift))
		}
	}

	return nil
}

// BuildLockHolder constructs a holder identity string: "user@hostname".
func BuildLockHolder() string {
	username := "unknown"
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	hostname := "unknown"
	if h, err := os.Hostname(); err == nil {
		hostname = h
	}
	return fmt.Sprintf("%s@%s", username, hostname)
}

// Unlock releases the lock.
// Transport guard: no-op when n.conn == nil (topology-offline mode).
func (n *Node) Unlock() error {
	if n.conn == nil {
		return nil
	}
	if !n.locked {
		return nil
	}

	if err := n.conn.Unlock(); err != nil {
		return err
	}

	n.locked = false
	return nil
}

// IsLocked returns true if locked.
func (n *Node) IsLocked() bool {
	return n.locked
}

// ============================================================================
// Interface (Child) Management
// ============================================================================

// GetInterface returns an Interface object created in this Device's context.
// The Interface has access to Device properties AND Network configuration.
// Accepts both short (Eth0, Po100) and full (Ethernet0, PortChannel100) interface names.
func (n *Node) GetInterface(name string) (*Interface, error) {
	// Normalize interface name (e.g., Eth0 -> Ethernet0, Po100 -> PortChannel100)
	name = util.NormalizeInterfaceName(name)

	// Return existing interface if already loaded
	if intf, ok := n.interfaces[name]; ok {
		return intf, nil
	}

	// Verify interface exists
	if !n.InterfaceExists(name) {
		return nil, util.NewPreconditionError("get-interface", name, "interface exists",
			fmt.Sprintf("not found on device %s", n.name))
	}

	// Create Interface with parent reference to this Node
	intf := &Interface{
		node: n, // Parent reference - key to OO design
		name: name,
	}

	n.interfaces[name] = intf
	return intf, nil
}

// ListInterfaces returns all interface names.
func (n *Node) ListInterfaces() []string {
	var names []string

	// Physical interfaces from PORT table (pre-intent infrastructure)
	if n.configDB != nil {
		for name := range n.configDB.Port {
			names = append(names, name)
		}
	}

	// PortChannels from intent DB
	for resource := range n.IntentsByPrefix("portchannel|") {
		parts := strings.SplitN(resource, "|", 2)
		if len(parts) == 2 && !strings.Contains(parts[1], "|") {
			names = append(names, parts[1])
		}
	}

	return names
}

// loadInterfaces populates the interfaces map from config_db.
func (n *Node) loadInterfaces() {
	if n.configDB == nil {
		return
	}

	for name := range n.configDB.Port {
		n.interfaces[name] = &Interface{node: n, name: name}
	}

	for name := range n.configDB.PortChannel {
		n.interfaces[name] = &Interface{node: n, name: name}
	}
}


// SaveConfig persists the device's running CONFIG_DB to disk via SSH.
func (n *Node) SaveConfig(ctx context.Context) error {
	// Transport guard — config save writes to the device filesystem via SSH.
	// Without transport, skip (same pattern as Apply, Lock, Unlock).
	if n.conn == nil {
		return nil
	}
	tunnel := n.Tunnel()
	if tunnel == nil {
		return fmt.Errorf("config save requires SSH connection (no SSH credentials configured)")
	}
	output, err := tunnel.ExecCommand("sudo config save -y")
	if err != nil {
		return fmt.Errorf("config save failed: %w (output: %s)", err, output)
	}
	return nil
}

// EnsureUnifiedConfigMode checks whether frrcfgd (unified config mode) is
// running. If not, restarts the bgp container so it picks up the
// docker_routing_config_mode=unified that was written to DEVICE_METADATA.
// Returns nil if frrcfgd is already running.
func (n *Node) EnsureUnifiedConfigMode(ctx context.Context) error {
	tunnel := n.Tunnel()
	if tunnel == nil {
		return fmt.Errorf("ensuring unified config mode requires SSH connection")
	}

	// CLI-WORKAROUND(frrcfgd-status): Check frrcfgd daemon status via supervisorctl.
	// Gap: No STATE_DB or CONFIG_DB key reflects whether frrcfgd is the active routing config daemon.
	// Resolution: SONiC could expose active routing config daemon in STATE_DB.
	output, err := tunnel.ExecCommand("docker exec bgp supervisorctl status frrcfgd 2>&1")
	if err == nil && strings.Contains(output, "RUNNING") {
		return nil // already running
	}

	util.WithDevice(n.name).Info("frrcfgd not running — restarting bgp container to enable unified config mode")

	// CLI-WORKAROUND(frrcfgd-restart): Restart bgp container to switch from bgpcfgd to frrcfgd.
	// Gap: No CONFIG_DB-driven way to switch the active routing config daemon at runtime.
	// Resolution: SONiC could support runtime daemon switching via CONFIG_DB or gNMI.
	if _, err := tunnel.ExecCommand("sudo systemctl restart bgp"); err != nil {
		return fmt.Errorf("restarting bgp container: %w", err)
	}

	// Wait for bgp container + frrcfgd to come up
	deadline := time.After(120 * time.Second)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled waiting for frrcfgd: %w", ctx.Err())
		case <-deadline:
			return fmt.Errorf("timed out waiting for frrcfgd to start after bgp restart")
		case <-ticker.C:
			output, err := tunnel.ExecCommand("docker exec bgp supervisorctl status frrcfgd 2>&1")
			if err == nil && strings.Contains(output, "RUNNING") {
				util.WithDevice(n.name).Info("frrcfgd is running")
				return nil
			}
		}
	}
}

// ConfigReload runs 'config reload -y' which stops all SONiC services,
// flushes CONFIG_DB, re-reads config_db.json, and restarts all services.
// This ensures all daemons process the config from a clean startup state,
// which is required for proper STATE_DB propagation (e.g., vrfmgrd writing
// VRF_TABLE entries that intfmgrd depends on for VRF-bound interface setup).
//
// If SwSS is not ready (common on fresh boot), retries up to 90 seconds
// before failing.
func (n *Node) ConfigReload(ctx context.Context) error {
	if !n.connected {
		return util.ErrNotConnected
	}
	tunnel := n.Tunnel()
	if tunnel == nil {
		return fmt.Errorf("config reload requires SSH connection (no SSH credentials configured)")
	}

	// SONiC's _swss_ready() requires SwSS uptime > 120s. Timeout must exceed
	// that threshold to handle cases where SwSS was recently restarted (boot
	// patches, prior config reload).
	deadline := time.After(150 * time.Second)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		output, err := tunnel.ExecCommand("sudo config reload -y")
		if err == nil {
			return nil
		}
		if !strings.Contains(output, "not ready") {
			return fmt.Errorf("config reload failed: %w (output: %s)", err, output)
		}
		// SwSS not ready — retry until deadline
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("config reload failed (SwSS not ready after 150s): %w (output: %s)", err, output)
		case <-ticker.C:
			// retry
		}
	}
}

// RestartService restarts a SONiC Docker container by name via SSH.
func (n *Node) RestartService(ctx context.Context, name string) error {
	if !n.connected {
		return util.ErrNotConnected
	}
	tunnel := n.Tunnel()
	if tunnel == nil {
		return fmt.Errorf("restart service requires SSH connection (no SSH credentials configured)")
	}
	output, err := tunnel.ExecCommand(fmt.Sprintf("sudo systemctl restart %s", name))
	if err != nil {
		return fmt.Errorf("restart service %s failed: %w (output: %s)", name, err, output)
	}
	return nil
}

// ApplyFRRDefaults sets FRR runtime defaults not supported by frrcfgd templates.
// Handles: no bgp ebgp-requires-policy, no bgp suppress-fib-pending.
// Must be called after a BGP container restart since frr.conf is regenerated.
//
// CLI-WORKAROUND(frr-defaults): Sets FRR runtime defaults via vtysh.
// Gap: frrcfgd templates don't support ebgp-requires-policy and suppress-fib-pending.
// Resolution: Upstream frrcfgd template patch to include these defaults.
func (n *Node) ApplyFRRDefaults(ctx context.Context) error {
	if !n.connected {
		return util.ErrNotConnected
	}
	tunnel := n.Tunnel()
	if tunnel == nil {
		return fmt.Errorf("ApplyFRRDefaults requires SSH connection")
	}

	// Read BGP ASN from resolved profile (set by device profile).
	asn := ""
	if n.resolved != nil && n.resolved.UnderlayASN > 0 {
		asn = fmt.Sprintf("%d", n.resolved.UnderlayASN)
	}
	if asn == "" {
		return fmt.Errorf("cannot determine BGP ASN from device profile")
	}

	cmds := fmt.Sprintf(
		"vtysh -c 'configure terminal' -c 'router bgp %s' "+
			"-c 'no bgp ebgp-requires-policy' "+
			"-c 'no bgp suppress-fib-pending' "+
			"-c 'end' -c 'write memory'",
		asn)

	output, err := tunnel.ExecCommand(cmds)
	if err != nil {
		return fmt.Errorf("ApplyFRRDefaults failed: %w (output: %s)", err, output)
	}

	// Force route reprocessing after changing defaults.
	_, _ = tunnel.ExecCommand("vtysh -c 'clear bgp * soft'")

	return nil
}

// ============================================================================
// Test Helpers
// ============================================================================

// NewTestNode creates a Node with pre-configured ConfigDB state for testing.
// This is intended for use by external test packages (e.g., operations_test)
// that need to construct Devices without connecting to real SONiC hardware.
func NewTestNode(name string, configDB *sonic.ConfigDB, connected, locked bool) *Node {
	return &Node{
		name:       name,
		configDB:   configDB,
		connected:  connected,
		locked:     locked,
		interfaces: make(map[string]*Interface),
		resolved:   &spec.ResolvedProfile{DeviceName: name},
	}
}
