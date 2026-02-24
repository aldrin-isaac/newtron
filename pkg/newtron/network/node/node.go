package node

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"strings"
	"sync"

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
	GetQoSProfile(name string) (*spec.QoSProfile, error)
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
	n.trackOffline(cs)
	return cs, nil
}

// trackOffline updates the shadow ConfigDB and accumulates entries for offline mode.
// Called by operations that build ChangeSets manually (not via op()).
func (n *Node) trackOffline(cs *ChangeSet) {
	if !n.offline || cs == nil {
		return
	}
	for _, c := range cs.Changes {
		if c.Type != sonic.ChangeTypeDelete {
			entry := sonic.Entry{Table: c.Table, Key: c.Key, Fields: c.Fields}
			n.configDB.ApplyEntries([]sonic.Entry{entry})
			n.accumulated = append(n.accumulated, entry)
		} else {
			// For deletes in offline mode, just accumulate (shadow doesn't need to remove)
			n.accumulated = append(n.accumulated, sonic.Entry{Table: c.Table, Key: c.Key, Fields: c.Fields})
		}
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
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.connected {
		return nil
	}

	// Create connection using sonic package
	n.conn = sonic.NewDevice(n.name, n.resolved)
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

	holder := buildLockHolder()
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

	return nil
}

// buildLockHolder constructs a holder identity string: "user@hostname".
func buildLockHolder() string {
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
		return nil, fmt.Errorf("interface %s not found on device %s", name, n.name)
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

// ConfigReload runs 'config reload -y' which stops all SONiC services,
// flushes CONFIG_DB, re-reads config_db.json, and restarts all services.
// This ensures all daemons process the config from a clean startup state,
// which is required for proper STATE_DB propagation (e.g., vrfmgrd writing
// VRF_TABLE entries that intfmgrd depends on for VRF-bound interface setup).
func (n *Node) ConfigReload(ctx context.Context) error {
	if !n.connected {
		return util.ErrNotConnected
	}
	tunnel := n.Tunnel()
	if tunnel == nil {
		return fmt.Errorf("config reload requires SSH connection (no SSH credentials configured)")
	}
	output, err := tunnel.ExecCommand("sudo config reload -y")
	if err != nil {
		return fmt.Errorf("config reload failed: %w (output: %s)", err, output)
	}
	return nil
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

// ReadSystemMAC reads the system MAC from the device's factory config file.
// Returns an empty string if not connected or if the MAC cannot be read.
func (n *Node) ReadSystemMAC() string {
	if !n.connected {
		return ""
	}
	tunnel := n.Tunnel()
	if tunnel == nil {
		return ""
	}
	cmd := `python3 -c 'import json; d=json.load(open("/etc/sonic/config_db.json")); print(d.get("DEVICE_METADATA",{}).get("localhost",{}).get("mac",""))' 2>/dev/null`
	output, err := tunnel.ExecCommand("sudo " + cmd)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
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
