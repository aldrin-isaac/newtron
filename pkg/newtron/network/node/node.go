package node

import (
	"context"
	"fmt"
	"os"
	"os/user"
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

	holder := lockHolder()
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

// lockHolder constructs a holder identity string: "user@hostname".
func lockHolder() string {
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
// (e.g., iface.ApplyService, iface.RemoveService, dev.ApplyBaseline).
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
		name:   name,
	}

	// Load interface state from config_db
	intf.loadState()

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
		intf := &Interface{
			node: n,
			name:   name,
		}
		intf.loadState()
		n.interfaces[name] = intf
	}

	for name := range n.configDB.PortChannel {
		intf := &Interface{
			node: n,
			name:   name,
		}
		intf.loadState()
		n.interfaces[name] = intf
	}
}

// splitConfigDBKey splits a config_db key on "|"
func splitConfigDBKey(key string) []string {
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
	return n.conn.SaveConfig(ctx)
}

// RestartService restarts a SONiC Docker container by name via SSH.
func (n *Node) RestartService(ctx context.Context, name string) error {
	if !n.connected {
		return util.ErrNotConnected
	}
	return n.conn.RestartService(ctx, name)
}

// ApplyFRRDefaults sets FRR runtime defaults not supported by frrcfgd templates.
func (n *Node) ApplyFRRDefaults(ctx context.Context) error {
	if !n.connected {
		return util.ErrNotConnected
	}
	return n.conn.ApplyFRRDefaults(ctx)
}


// ReadSystemMAC reads the system MAC from the device's factory config file.
// Returns an empty string if not connected or if the MAC cannot be read.
func (n *Node) ReadSystemMAC() string {
	if !n.connected || n.conn == nil {
		return ""
	}
	return n.conn.ReadSystemMAC()
}

// Underlying returns the underlying sonic.Device for low-level operations.
// This is used by ChangeSet to apply changes via Redis.
func (n *Node) Underlying() *sonic.Device {
	return n.conn
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
