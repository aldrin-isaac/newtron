package node

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"sync"

	"github.com/newtron-network/newtron/pkg/newtron/device"
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
	GetFilterSpec(name string) (*spec.FilterSpec, error)
	GetPlatform(name string) (*spec.PlatformSpec, error)
	GetPrefixList(name string) ([]string, error)
	GetRoutePolicy(name string) (*spec.RoutePolicy, error)
	FindMACVPNByL2VNI(vni int) (string, *spec.MACVPNSpec)
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
	return n.resolved.ASNumber
}

// RouterID returns the BGP router ID.
func (n *Node) RouterID() string {
	return n.resolved.RouterID
}

// Region returns the region name.
func (n *Node) Region() string {
	return n.resolved.Region
}

// Site returns the site name.
func (n *Node) Site() string {
	return n.resolved.Site
}

// BGPNeighbors returns the list of BGP neighbor IPs (derived from route reflectors).
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

// ============================================================================
// Existence Checks (convenience methods for preconditions)
// ============================================================================

// InterfaceExists checks if an interface exists.
// Accepts both short (Eth0) and full (Ethernet0) interface names.
func (n *Node) InterfaceExists(name string) bool {
	return n.configDB.HasInterface(util.NormalizeInterfaceName(name))
}

// VLANExists checks if a VLAN exists.
func (n *Node) VLANExists(id int) bool { return n.configDB.HasVLAN(id) }

// VRFExists checks if a VRF exists.
func (n *Node) VRFExists(name string) bool { return n.configDB.HasVRF(name) }

// PortChannelExists checks if a PortChannel exists.
// Accepts both short (Po100) and full (PortChannel100) names.
func (n *Node) PortChannelExists(name string) bool {
	return n.configDB.HasPortChannel(util.NormalizeInterfaceName(name))
}

// VTEPExists checks if VTEP is configured.
func (n *Node) VTEPExists() bool { return n.configDB.HasVTEP() }

// BGPConfigured checks if BGP is configured.
// Checks both CONFIG_DB BGP_NEIGHBOR table (CONFIG_DB-managed BGP) and
// DEVICE_METADATA bgp_asn (FRR-managed BGP with frr_split_config_enabled).
func (n *Node) BGPConfigured() bool { return n.configDB.BGPConfigured() }

// ACLTableExists checks if an ACL table exists.
func (n *Node) ACLTableExists(name string) bool { return n.configDB.HasACLTable(name) }

// InterfaceIsLAGMember checks if an interface is a LAG member.
// Accepts both short (Eth0) and full (Ethernet0) interface names.
func (n *Node) InterfaceIsLAGMember(name string) bool {
	if n.configDB == nil {
		return false
	}
	name = util.NormalizeInterfaceName(name)
	for key := range n.configDB.PortChannelMember {
		// Key format: PortChannel100|Ethernet0
		parts := splitConfigDBKey(key)
		if len(parts) == 2 && parts[1] == name {
			return true
		}
	}
	return false
}

// GetInterfaceLAG returns the LAG that an interface belongs to (empty if not a member).
// Accepts both short (Eth0) and full (Ethernet0) interface names.
func (n *Node) GetInterfaceLAG(name string) string {
	if n.configDB == nil {
		return ""
	}
	name = util.NormalizeInterfaceName(name)
	for key := range n.configDB.PortChannelMember {
		// Key format: PortChannel100|Ethernet0
		parts := splitConfigDBKey(key)
		if len(parts) == 2 && parts[1] == name {
			return parts[0]
		}
	}
	return ""
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

// InterfaceHasService checks if an interface has a service bound.
// Accepts both short (Eth0) and full (Ethernet0) interface names.
func (n *Node) InterfaceHasService(name string) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()

	name = util.NormalizeInterfaceName(name)
	if intf, ok := n.interfaces[name]; ok {
		return intf.HasService()
	}
	return false
}

// ============================================================================
// Data Retrieval Methods (for operations)
// ============================================================================

// VLANInfo represents VLAN data assembled from config_db for operations.
type VLANInfo struct {
	ID         int
	Name       string      // VLAN name from config
	Members    []string    // All member interfaces
	SVIStatus  string      // "up" if VLAN_INTERFACE exists, empty otherwise
	MACVPNInfo *MACVPNInfo // MAC-VPN binding info (L2VNI, ARP suppression)
}

// L2VNI returns the L2VNI for this VLAN (0 if not configured).
func (v *VLANInfo) L2VNI() int {
	if v.MACVPNInfo != nil {
		return v.MACVPNInfo.L2VNI
	}
	return 0
}

// MACVPNInfo contains MAC-VPN binding information for a VLAN.
// This is populated from VXLAN_TUNNEL_MAP and SUPPRESS_VLAN_NEIGH tables.
type MACVPNInfo struct {
	Name           string `json:"name,omitempty"`   // MAC-VPN definition name (from network.json)
	L2VNI          int    `json:"l2_vni,omitempty"` // L2VNI from VXLAN_TUNNEL_MAP
	ARPSuppression bool   `json:"arp_suppression"`  // ARP suppression enabled
}

// GetVLAN retrieves VLAN information from config_db.
func (n *Node) GetVLAN(id int) (*VLANInfo, error) {
	if n.configDB == nil {
		return nil, util.ErrNotConnected
	}

	vlanKey := fmt.Sprintf("Vlan%d", id)
	vlanEntry, ok := n.configDB.VLAN[vlanKey]
	if !ok {
		return nil, fmt.Errorf("VLAN %d not found", id)
	}

	info := &VLANInfo{ID: id, Name: vlanEntry.Description}

	// Collect member interfaces from VLAN_MEMBER
	for key, member := range n.configDB.VLANMember {
		parts := splitConfigDBKey(key)
		if len(parts) == 2 && parts[0] == vlanKey {
			iface := parts[1]
			if member.TaggingMode == "tagged" {
				info.Members = append(info.Members, iface+"(t)")
			} else {
				info.Members = append(info.Members, iface)
			}
		}
	}

	// Check for SVI (VLAN_INTERFACE)
	if _, ok := n.configDB.VLANInterface[vlanKey]; ok {
		info.SVIStatus = "up"
	}

	// Build MAC-VPN info from VXLAN_TUNNEL_MAP and SUPPRESS_VLAN_NEIGH
	macvpn := &MACVPNInfo{}

	// Find L2VNI from VXLAN_TUNNEL_MAP
	for _, mapping := range n.configDB.VXLANTunnelMap {
		if mapping.VLAN == vlanKey && mapping.VNI != "" {
			fmt.Sscanf(mapping.VNI, "%d", &macvpn.L2VNI)
			break
		}
	}

	// Check ARP suppression
	if _, ok := n.configDB.SuppressVLANNeigh[vlanKey]; ok {
		macvpn.ARPSuppression = true
	}

	// Try to match to a macvpn definition by L2VNI
	if macvpn.L2VNI > 0 && n.SpecProvider != nil {
		if name, _ := n.FindMACVPNByL2VNI(macvpn.L2VNI); name != "" {
			macvpn.Name = name
		}
	}

	// Only set MACVPNInfo if there's actually some data
	if macvpn.L2VNI > 0 || macvpn.ARPSuppression {
		info.MACVPNInfo = macvpn
	}

	return info, nil
}

// VRFInfo represents VRF data assembled from config_db for operations.
type VRFInfo struct {
	Name       string
	L3VNI      int
	Interfaces []string
}

// GetVRF retrieves VRF information from config_db.
func (n *Node) GetVRF(name string) (*VRFInfo, error) {
	if n.configDB == nil {
		return nil, util.ErrNotConnected
	}

	vrfEntry, ok := n.configDB.VRF[name]
	if !ok {
		return nil, fmt.Errorf("VRF %s not found", name)
	}

	info := &VRFInfo{Name: name}

	// Parse L3VNI
	if vrfEntry.VNI != "" {
		fmt.Sscanf(vrfEntry.VNI, "%d", &info.L3VNI)
	}

	// Find interfaces bound to this VRF from INTERFACE table
	seen := make(map[string]bool)
	for key, intf := range n.configDB.Interface {
		// Key could be "Ethernet0" or "Ethernet0|10.1.1.1/24"
		parts := splitConfigDBKey(key)
		intfName := parts[0]
		if intf.VRFName == name && !seen[intfName] {
			seen[intfName] = true
			info.Interfaces = append(info.Interfaces, intfName)
		}
	}

	// Also check VLAN_INTERFACE for SVIs in this VRF
	for key := range n.configDB.VLANInterface {
		parts := splitConfigDBKey(key)
		vlanName := parts[0]
		// VLANInterface value contains vrf_name
		if vals, ok := n.configDB.VLANInterface[vlanName]; ok {
			if vals["vrf_name"] == name && !seen[vlanName] {
				seen[vlanName] = true
				info.Interfaces = append(info.Interfaces, vlanName)
			}
		}
	}

	return info, nil
}

// PortChannelInfo represents PortChannel data assembled from config_db.
type PortChannelInfo struct {
	Name          string
	Members       []string
	ActiveMembers []string
	AdminStatus   string
}

// GetPortChannel retrieves PortChannel information from config_db.
func (n *Node) GetPortChannel(name string) (*PortChannelInfo, error) {
	if n.configDB == nil {
		return nil, util.ErrNotConnected
	}

	// Normalize PortChannel name (e.g., Po100 -> PortChannel100)
	name = util.NormalizeInterfaceName(name)

	pcEntry, ok := n.configDB.PortChannel[name]
	if !ok {
		return nil, fmt.Errorf("PortChannel %s not found", name)
	}

	info := &PortChannelInfo{
		Name:        name,
		AdminStatus: pcEntry.AdminStatus,
	}

	// Collect members from PORTCHANNEL_MEMBER
	for key := range n.configDB.PortChannelMember {
		parts := splitConfigDBKey(key)
		if len(parts) == 2 && parts[0] == name {
			info.Members = append(info.Members, parts[1])
		}
	}

	// For now, assume all members are active (would need state_db for real status)
	info.ActiveMembers = info.Members

	return info, nil
}

// ListPortChannels returns all PortChannel names on this device.
func (n *Node) ListPortChannels() []string {
	if n.configDB == nil {
		return nil
	}

	names := make([]string, 0, len(n.configDB.PortChannel))
	for name := range n.configDB.PortChannel {
		names = append(names, name)
	}
	return names
}

// ListVLANs returns all VLAN IDs on this device.
func (n *Node) ListVLANs() []int {
	if n.configDB == nil {
		return nil
	}

	var ids []int
	for name := range n.configDB.VLAN {
		var id int
		if _, err := fmt.Sscanf(name, "Vlan%d", &id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// ListVRFs returns all VRF names on this device.
func (n *Node) ListVRFs() []string {
	if n.configDB == nil {
		return nil
	}

	names := make([]string, 0, len(n.configDB.VRF))
	for name := range n.configDB.VRF {
		names = append(names, name)
	}
	return names
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

// Underlying returns the underlying sonic.Device for low-level operations.
// This is used by ChangeSet to apply changes via Redis.
func (n *Node) Underlying() *sonic.Device {
	return n.conn
}

// GetRoute reads a route from APP_DB (Redis DB 0).
// Returns nil RouteEntry (not error) if the prefix is not present.
// Single-shot read — does not poll or retry.
func (n *Node) GetRoute(ctx context.Context, vrf, prefix string) (*device.RouteEntry, error) {
	if !n.connected {
		return nil, util.ErrNotConnected
	}
	return n.conn.GetRoute(ctx, vrf, prefix)
}

// GetRouteASIC reads a route from ASIC_DB (Redis DB 1) by resolving the SAI
// object chain. Returns nil RouteEntry (not error) if not programmed in ASIC.
// Single-shot read — does not poll or retry.
func (n *Node) GetRouteASIC(ctx context.Context, vrf, prefix string) (*device.RouteEntry, error) {
	if !n.connected {
		return nil, util.ErrNotConnected
	}
	return n.conn.GetRouteASIC(ctx, vrf, prefix)
}

// ============================================================================
// BGP Operations (Device-level: Indirect/iBGP neighbors using loopback)
// ============================================================================
// Device-level BGP operations are for INDIRECT neighbors that use the device's
// loopback IP as the update-source. This is typical for:
//   - iBGP peering (same AS, loopback-to-loopback)
//   - EVPN route reflector peering
//   - Multi-hop eBGP using loopback
//
// For DIRECT BGP neighbors that use a link IP as the update-source (typical
// eBGP on point-to-point links), use Interface.AddBGPNeighbor() instead.

// AddLoopbackBGPNeighbor adds an indirect BGP neighbor using loopback as update-source.
// This is used for iBGP or multi-hop eBGP sessions.
func (n *Node) AddLoopbackBGPNeighbor(ctx context.Context, neighborIP string, asn int, description string, evpn bool) (*ChangeSet, error) {
	if !n.connected {
		return nil, util.ErrNotConnected
	}
	if !n.locked {
		return nil, fmt.Errorf("device not locked")
	}

	// Validate neighbor IP
	if !util.IsValidIPv4(neighborIP) {
		return nil, fmt.Errorf("invalid neighbor IP: %s", neighborIP)
	}

	// Check if neighbor already exists (key format: "default|<IP>")
	if n.configDB != nil {
		key := fmt.Sprintf("default|%s", neighborIP)
		if _, ok := n.configDB.BGPNeighbor[key]; ok {
			return nil, fmt.Errorf("BGP neighbor %s already exists", neighborIP)
		}
	}

	cs := NewChangeSet(n.name, "bgp.add-loopback-neighbor")

	// Add BGP neighbor entry with loopback as update-source
	fields := map[string]string{
		"asn":          fmt.Sprintf("%d", asn),
		"admin_status": "up",
		"local_addr":   n.resolved.LoopbackIP, // Update-source = loopback
	}
	if description != "" {
		fields["name"] = description
	}

	// For iBGP (same AS), no special config needed
	// For eBGP with loopback, would need ebgp_multihop
	if asn != n.resolved.ASNumber {
		fields["ebgp_multihop"] = "255" // Enable multihop for eBGP via loopback
	}

	// Key format: vrf|neighborIP (per SONiC Unified FRR Mgmt schema)
	cs.Add("BGP_NEIGHBOR", fmt.Sprintf("default|%s", neighborIP), ChangeAdd, nil, fields)

	// If EVPN enabled, add address-family activation
	if evpn {
		afKey := fmt.Sprintf("default|%s|l2vpn_evpn", neighborIP)
		cs.Add("BGP_NEIGHBOR_AF", afKey, ChangeAdd, nil, map[string]string{
			"activate": "true",
		})
	}

	util.WithDevice(n.name).Infof("Adding loopback BGP neighbor %s (AS %d, update-source: %s)",
		neighborIP, asn, n.resolved.LoopbackIP)
	return cs, nil
}

// AddBGPNeighbor is an alias for AddLoopbackBGPNeighbor for backward compatibility.
// Deprecated: Use AddLoopbackBGPNeighbor for clarity.
func (n *Node) AddBGPNeighbor(ctx context.Context, neighborIP string, asn int, description string, evpn bool) (*ChangeSet, error) {
	return n.AddLoopbackBGPNeighbor(ctx, neighborIP, asn, description, evpn)
}

// RemoveBGPNeighbor removes a BGP neighbor from the device.
// This works for both direct (interface-level) and indirect (loopback-level) neighbors.
func (n *Node) RemoveBGPNeighbor(ctx context.Context, neighborIP string) (*ChangeSet, error) {
	if !n.connected {
		return nil, util.ErrNotConnected
	}
	if !n.locked {
		return nil, fmt.Errorf("device not locked")
	}

	// Check if neighbor exists (key format: "default|<IP>")
	if n.configDB != nil {
		key := fmt.Sprintf("default|%s", neighborIP)
		if _, ok := n.configDB.BGPNeighbor[key]; !ok {
			return nil, fmt.Errorf("BGP neighbor %s not found", neighborIP)
		}
	}

	cs := NewChangeSet(n.name, "bgp.remove-neighbor")

	// Remove all address-family entries first
	// Key format: vrf|neighborIP|af (per SONiC Unified FRR Mgmt schema)
	for _, af := range []string{"ipv4_unicast", "ipv6_unicast", "l2vpn_evpn"} {
		afKey := fmt.Sprintf("default|%s|%s", neighborIP, af)
		cs.Add("BGP_NEIGHBOR_AF", afKey, ChangeDelete, nil, nil)
	}

	// Remove neighbor entry
	cs.Add("BGP_NEIGHBOR", fmt.Sprintf("default|%s", neighborIP), ChangeDelete, nil, nil)

	util.WithDevice(n.name).Infof("Removing BGP neighbor %s", neighborIP)
	return cs, nil
}

// BGPNeighborExists checks if a BGP neighbor exists.
// Looks up using the SONiC key format: "default|<IP>" (vrf|neighborIP).
func (n *Node) BGPNeighborExists(neighborIP string) bool {
	return n.configDB.HasBGPNeighbor(fmt.Sprintf("default|%s", neighborIP))
}

// VTEPSourceIP returns the VTEP source IP (from loopback).
func (n *Node) VTEPSourceIP() string {
	if n.configDB == nil {
		return n.resolved.LoopbackIP
	}
	// Check if VTEP is configured
	for _, vtep := range n.configDB.VXLANTunnel {
		if vtep.SrcIP != "" {
			return vtep.SrcIP
		}
	}
	// Fall back to resolved loopback IP
	return n.resolved.LoopbackIP
}

// GetOrphanedACLs returns ACL tables that have no interfaces bound.
func (n *Node) GetOrphanedACLs() []string {
	if n.configDB == nil {
		return nil
	}
	var orphans []string
	for name, acl := range n.configDB.ACLTable {
		if acl.Ports == "" {
			orphans = append(orphans, name)
		}
	}
	return orphans
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
