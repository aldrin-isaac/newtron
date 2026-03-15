package node

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"strings"
	"sync"
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
// Two modes of operation:
//   - Physical mode (offline=false): connected to real device, ConfigDB loaded from Redis
//   - Abstract mode (offline=true): shadow ConfigDB starts empty, operations build desired state
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

	// Abstract mode — no physical device, operations accumulate entries
	offline     bool
	accumulated []sonic.Entry

	// zombieOp stores an existing intent found during Lock(). Any intent
	// present at lock time indicates a crashed process — the lock
	// acquisition proves the previous holder is gone. Execute() returns
	// ErrDeviceZombieOperation to block further changes until resolved.
	zombieOp *sonic.OperationIntent

	mu sync.RWMutex
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

// NewAbstract creates an abstract Node with an empty shadow ConfigDB.
// Operations work against the shadow and accumulate entries for composite export.
// Preconditions check the shadow (no connected/locked requirement).
//
// Usage:
//
//	n := node.NewAbstract(specs, "switch1", profile, resolved)
//	n.RegisterPort("Ethernet0", map[string]string{"admin_status": "up"})
//	iface, _ := n.GetInterface("Ethernet0")
//	iface.ApplyService(ctx, "transit", node.ApplyServiceOpts{...})
//	composite := n.BuildComposite()
func NewAbstract(sp SpecProvider, name string, profile *spec.DeviceProfile, resolved *spec.ResolvedProfile) *Node {
	return &Node{
		SpecProvider: sp,
		name:         name,
		profile:      profile,
		resolved:     resolved,
		interfaces:   make(map[string]*Interface),
		configDB:     sonic.NewEmptyConfigDB(),
		offline:      true,
	}
}

// IsOffline returns true if this is an abstract node (no physical device).
func (n *Node) IsOffline() bool { return n.offline }

// RegisterPort creates a PORT entry in the shadow ConfigDB and accumulates it.
// This enables GetInterface for offline mode (GetInterface checks PORT table).
func (n *Node) RegisterPort(name string, fields map[string]string) {
	if fields == nil {
		fields = map[string]string{}
	}
	entry := sonic.Entry{Table: "PORT", Key: name, Fields: fields}
	n.configDB.ApplyEntries([]sonic.Entry{entry})
	if n.offline {
		n.accumulated = append(n.accumulated, entry)
	}
	n.interfaces[name] = &Interface{node: n, name: name}
}

// BuildComposite exports accumulated entries as a CompositeConfig.
// Only valid in offline mode.
func (n *Node) BuildComposite() *CompositeConfig {
	cb := NewCompositeBuilder(n.name, CompositeOverwrite).
		SetGeneratedBy("abstract-node")
	for _, e := range n.accumulated {
		cb.AddEntry(e.Table, e.Key, e.Fields)
	}
	return cb.Build()
}

// AddEntries accumulates entries and updates the shadow ConfigDB.
// Used by orchestrators to add entries from config functions that don't
// have corresponding Node methods (e.g., CreateVTEP, CreateBGPNeighbor).
// Only valid in offline mode; no-op for physical nodes.
func (n *Node) AddEntries(entries []sonic.Entry) {
	if !n.offline {
		return
	}
	n.configDB.ApplyEntries(entries)
	n.accumulated = append(n.accumulated, entries...)
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
	n.applyShadow(cs)
	return cs, nil
}

// applyShadow updates the shadow ConfigDB so subsequent operations see the
// effects of prior ones.  Also accumulates entries for BuildComposite export.
// No-op when the Node is connected to a physical device.
func (n *Node) applyShadow(cs *ChangeSet) {
	if !n.offline || cs == nil {
		return
	}
	for _, c := range cs.Changes {
		entry := sonic.Entry{Table: c.Table, Key: c.Key, Fields: c.Fields}
		if c.Type == sonic.ChangeTypeDelete {
			n.configDB.DeleteEntry(c.Table, c.Key)
		} else {
			n.configDB.ApplyEntries([]sonic.Entry{entry})
		}
		n.accumulated = append(n.accumulated, entry)
	}
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
// nodes; checks shadow ConfigDB for abstract/offline nodes.
func (n *Node) IsUnifiedConfigMode() bool {
	if n.conn != nil {
		return n.conn.IsUnifiedConfigMode()
	}
	// Offline/abstract mode — no sonic.Device, check shadow directly.
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

// Connect establishes connection to the device via Redis/config_db.
func (n *Node) Connect(ctx context.Context) error {
	return n.connectWithOpts(ctx, false)
}

// ConnectForSetup connects to the device without requiring frrcfgd.
// Used by provisioning and InitDevice — both write unified config mode
// to DEVICE_METADATA and restart bgp afterward, so the check is skipped.
func (n *Node) ConnectForSetup(ctx context.Context) error {
	return n.connectWithOpts(ctx, true)
}

func (n *Node) connectWithOpts(ctx context.Context, skipFrrcfgdCheck bool) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.connected {
		return nil
	}

	// Create connection using sonic package
	n.conn = sonic.NewDevice(n.name, n.resolved)
	n.conn.SkipFrrcfgdCheck = skipFrrcfgdCheck
	if err := n.conn.Connect(ctx); err != nil {
		return err
	}

	n.configDB = n.conn.ConfigDB
	n.connected = true

	// Load interfaces
	n.loadInterfaces()

	util.WithDevice(n.name).Info("Connected")
	return nil
}

// Disconnect closes the connection.
func (n *Node) Disconnect() error {
	n.mu.Lock()
	defer n.mu.Unlock()

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
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.connected
}

// Refresh reloads CONFIG_DB from Redis and rebuilds the interface list.
// Call after operations that write to CONFIG_DB outside the normal device flow
// (e.g., composite provisioning).
func (n *Node) Refresh() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.connected || n.conn == nil {
		return util.ErrNotConnected
	}

	configDB, err := n.conn.Client().GetAll()
	if err != nil {
		return fmt.Errorf("reloading config_db: %w", err)
	}
	n.conn.ConfigDB = configDB
	n.configDB = configDB

	// Rebuild interfaces from the refreshed CONFIG_DB
	n.interfaces = make(map[string]*Interface)
	n.loadInterfaces()

	return nil
}

// RefreshWithRetry polls Refresh until CONFIG_DB is available or timeout
// expires. Used after config reload when Redis may be briefly unavailable
// as services restart.
func (n *Node) RefreshWithRetry(ctx context.Context, timeout time.Duration) error {
	if err := n.Refresh(); err == nil {
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
			return n.Refresh() // final attempt
		case <-ticker.C:
			if err := n.Refresh(); err == nil {
				return nil
			}
		}
	}
}

// Lock acquires a distributed lock for configuration changes.
// Constructs a holder identity from the current user and hostname,
// and acquires the lock with a default TTL of 3600 seconds.
//
// After acquiring the lock, Lock refreshes the CONFIG_DB cache to guarantee
// that precondition checks within the subsequent write episode see all changes
// made by prior lock holders. If the cache refresh fails, the lock is released
// and an error is returned — operating with a stale cache under lock is worse
// than failing the operation.
func (n *Node) Lock() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.connected {
		return util.ErrNotConnected
	}
	if n.locked {
		return nil
	}

	holder := BuildLockHolder()
	if err := n.conn.Lock(holder, defaultLockTTL); err != nil {
		return err
	}
	n.locked = true

	// Refresh CONFIG_DB cache — guarantees precondition checks see
	// all changes made by prior lock holders.
	configDB, err := n.conn.Client().GetAll()
	if err != nil {
		// Release lock on refresh failure — don't hold a lock with stale cache
		n.conn.Unlock()
		n.locked = false
		return fmt.Errorf("refresh config_db after lock: %w", err)
	}
	n.conn.ConfigDB = configDB
	n.configDB = configDB
	n.interfaces = make(map[string]*Interface)
	n.loadInterfaces()

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

	// Check for existing intent from a crashed process.
	// If you hold the lock and an intent exists, the previous holder
	// crashed between WriteIntent and DeleteIntent. The lock acquisition
	// proves the previous holder is gone — staleness is irrelevant.
	// Intent is now in CONFIG_DB (persistent across reboot).
	n.zombieOp = nil
	if configClient := n.conn.Client(); configClient != nil {
		intent, err := configClient.ReadIntent(n.name)
		if err != nil {
			util.WithDevice(n.name).Warnf("reading intent on lock: %v", err)
		} else if intent != nil {
			n.zombieOp = intent
			util.WithDevice(n.name).Warnf("zombie operation detected: holder=%s, created=%s, operations=%d",
				intent.Holder, intent.Created.Format(time.RFC3339), len(intent.Operations))
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
func (n *Node) Unlock() error {
	n.mu.Lock()
	defer n.mu.Unlock()

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
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.locked
}

// ExecuteOp locks the device, runs the operation function, applies the
// resulting ChangeSet to CONFIG_DB, and unlocks. This is the standard
// pattern for all state-mutating operations — used by both the CLI
// (execute mode) and the test runner, ensuring identical behavior.
//
// The operation function should build a ChangeSet without side effects
// (e.g., iface.ApplyService, iface.RemoveService, dev.ConfigureLoopback).
// ExecuteOp handles the lock/apply/unlock lifecycle.
//
// Write episode lifecycle: Lock() refreshes the CONFIG_DB cache (start of
// episode) → fn() reads preconditions from cache → Apply() writes to Redis →
// Unlock() ends the episode. No post-Apply refresh — the next episode
// (whether write or read-only) will refresh itself.
func (n *Node) ExecuteOp(fn func() (*ChangeSet, error)) (*ChangeSet, error) {
	if err := n.Lock(); err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	defer n.Unlock()

	cs, err := fn()
	if err != nil {
		return nil, err
	}

	if err := cs.Apply(n); err != nil {
		return nil, fmt.Errorf("apply: %w", err)
	}

	return cs, nil
}

// ============================================================================
// Interface (Child) Management
// ============================================================================

// GetInterface returns an Interface object created in this Device's context.
// The Interface has access to Device properties AND Network configuration.
// Accepts both short (Eth0, Po100) and full (Ethernet0, PortChannel100) interface names.
func (n *Node) GetInterface(name string) (*Interface, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Normalize interface name (e.g., Eth0 -> Ethernet0, Po100 -> PortChannel100)
	name = util.NormalizeInterfaceName(name)

	// Return existing interface if already loaded
	if intf, ok := n.interfaces[name]; ok {
		return intf, nil
	}

	// Verify interface exists in config_db
	if !n.interfaceExistsInConfigDB(name) {
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
	n.mu.RLock()
	defer n.mu.RUnlock()

	var names []string

	// Physical interfaces from PORT table
	if n.configDB != nil {
		for name := range n.configDB.Port {
			names = append(names, name)
		}
		// Port channels
		for name := range n.configDB.PortChannel {
			names = append(names, name)
		}
	}

	return names
}

// interfaceExistsInConfigDB checks if interface exists.
func (n *Node) interfaceExistsInConfigDB(name string) bool {
	return n.configDB.HasInterface(name)
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

// splitKey splits a config_db key on "|"
func splitKey(key string) []string {
	for i := range key {
		if key[i] == '|' {
			return []string{key[:i], key[i+1:]}
		}
	}
	return []string{key}
}

// SaveConfig persists the device's running CONFIG_DB to disk via SSH.
func (n *Node) SaveConfig(ctx context.Context) error {
	if !n.connected {
		return util.ErrNotConnected
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
func (n *Node) ApplyFRRDefaults(ctx context.Context) error {
	if !n.connected {
		return util.ErrNotConnected
	}
	tunnel := n.Tunnel()
	if tunnel == nil {
		return fmt.Errorf("ApplyFRRDefaults requires SSH connection")
	}

	// Read BGP ASN from CONFIG_DB — check DEVICE_METADATA first (provision path),
	// then fall back to BGP_GLOBALS.local_asn (configure-bgp path).
	asn := ""
	if n.configDB != nil {
		if meta, ok := n.configDB.DeviceMetadata["localhost"]; ok {
			asn = meta["bgp_asn"]
		}
		if asn == "" {
			if globals, ok := n.configDB.BGPGlobals["default"]; ok {
				asn = globals.LocalASN
			}
		}
	}
	if asn == "" {
		return fmt.Errorf("cannot determine BGP ASN from CONFIG_DB")
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
// Intent Record Methods
// ============================================================================

// ZombieOperation returns the existing intent found during Lock(), or nil.
func (n *Node) ZombieOperation() *sonic.OperationIntent { return n.zombieOp }

// ClearZombie deletes the zombie intent from CONFIG_DB without reversing.
func (n *Node) ClearZombie() error {
	if n.conn == nil {
		return util.ErrNotConnected
	}
	configClient := n.conn.Client()
	if configClient == nil {
		return fmt.Errorf("no CONFIG_DB client")
	}
	if err := configClient.DeleteIntent(n.name); err != nil {
		return err
	}
	n.zombieOp = nil
	return nil
}

// WriteIntent writes an operation intent to CONFIG_DB.
func (n *Node) WriteIntent(intent *sonic.OperationIntent) error {
	if n.conn == nil {
		return util.ErrNotConnected
	}
	configClient := n.conn.Client()
	if configClient == nil {
		return fmt.Errorf("no CONFIG_DB client")
	}
	return configClient.WriteIntent(n.name, intent)
}

// UpdateIntentOps updates the mutable fields of the current intent.
func (n *Node) UpdateIntentOps(intent *sonic.OperationIntent) error {
	if n.conn == nil {
		return util.ErrNotConnected
	}
	configClient := n.conn.Client()
	if configClient == nil {
		return fmt.Errorf("no CONFIG_DB client")
	}
	return configClient.UpdateIntentOps(n.name, intent)
}

// DeleteIntent removes the operation intent from CONFIG_DB.
func (n *Node) DeleteIntent() error {
	if n.conn == nil {
		return util.ErrNotConnected
	}
	configClient := n.conn.Client()
	if configClient == nil {
		return fmt.Errorf("no CONFIG_DB client")
	}
	return configClient.DeleteIntent(n.name)
}

// ReadIntent reads the current intent from CONFIG_DB (live read).
func (n *Node) ReadIntent() (*sonic.OperationIntent, error) {
	if n.conn == nil {
		return nil, util.ErrNotConnected
	}
	configClient := n.conn.Client()
	if configClient == nil {
		return nil, fmt.Errorf("no CONFIG_DB client")
	}
	return configClient.ReadIntent(n.name)
}

// ============================================================================
// Settings Methods
// ============================================================================

// ReadSettings reads NEWTRON_SETTINGS from CONFIG_DB.
func (n *Node) ReadSettings() (*sonic.DeviceSettings, error) {
	if n.conn == nil {
		return nil, util.ErrNotConnected
	}
	return n.conn.Client().ReadSettings()
}

// WriteSettings writes NEWTRON_SETTINGS to CONFIG_DB.
func (n *Node) WriteSettings(s *sonic.DeviceSettings) error {
	if n.conn == nil {
		return util.ErrNotConnected
	}
	return n.conn.Client().WriteSettings(s)
}

// ============================================================================
// History Methods
// ============================================================================

// WriteHistory writes a history entry to CONFIG_DB.
func (n *Node) WriteHistory(entry *sonic.HistoryEntry) error {
	if n.conn == nil {
		return util.ErrNotConnected
	}
	return n.conn.Client().WriteHistory(n.name, entry)
}

// ReadHistory reads all history entries for this device.
func (n *Node) ReadHistory() ([]*sonic.HistoryEntry, error) {
	if n.conn == nil {
		return nil, util.ErrNotConnected
	}
	return n.conn.Client().ReadHistory(n.name)
}

// UpdateHistory updates a history entry (e.g., marking ops as reversed).
func (n *Node) UpdateHistory(entry *sonic.HistoryEntry) error {
	if n.conn == nil {
		return util.ErrNotConnected
	}
	return n.conn.Client().UpdateHistory(n.name, entry)
}

// DeleteHistory deletes a single history entry by sequence.
func (n *Node) DeleteHistory(seq int) error {
	if n.conn == nil {
		return util.ErrNotConnected
	}
	return n.conn.Client().DeleteHistory(n.name, seq)
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
