package network

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"sync"
	"time"

	"github.com/newtron-network/newtron/pkg/device"
	"github.com/newtron-network/newtron/pkg/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// Device represents a SONiC switch within the context of a Network.
//
// Key design: Device has a parent reference to Network, allowing it to access
// all Network-level configuration (services, filters, regions, etc.).
// This mirrors the original Perl design where Node objects had access to
// the parent Netconf object's configuration.
//
// Hierarchy: Network -> Device -> Interface
type Device struct {
	// Parent reference - provides access to Network-level configuration
	network *Network

	// Device identity
	name     string
	profile  *spec.DeviceProfile
	resolved *spec.ResolvedProfile

	// Child objects - Interfaces created in this Device's context
	interfaces map[string]*Interface

	// Connection and state (delegated to device.Device)
	conn     *device.Device
	configDB *device.ConfigDB

	// State
	connected bool
	locked    bool

	mu sync.RWMutex
}

// ============================================================================
// Parent Accessor - Key to OO Design
// ============================================================================

// Network returns the parent Network object.
// This allows access to all Network-level configuration from Device operations.
//
// Example usage in operations:
//
//	svc, _ := dev.Network().GetService("customer-l3")
//	filter, _ := dev.Network().GetFilterSpec("customer-edge-in")
func (d *Device) Network() *Network {
	return d.network
}

// ============================================================================
// Device Properties
// ============================================================================

// Name returns the device name.
func (d *Device) Name() string {
	return d.name
}

// Profile returns the device profile.
func (d *Device) Profile() *spec.DeviceProfile {
	return d.profile
}

// Resolved returns the resolved configuration (after inheritance).
func (d *Device) Resolved() *spec.ResolvedProfile {
	return d.resolved
}

// MgmtIP returns the management IP address.
func (d *Device) MgmtIP() string {
	return d.resolved.MgmtIP
}

// LoopbackIP returns the loopback IP address.
func (d *Device) LoopbackIP() string {
	return d.resolved.LoopbackIP
}

// ASNumber returns the BGP AS number.
func (d *Device) ASNumber() int {
	return d.resolved.ASNumber
}

// RouterID returns the BGP router ID.
func (d *Device) RouterID() string {
	return d.resolved.RouterID
}

// Region returns the region name.
func (d *Device) Region() string {
	return d.resolved.Region
}

// Site returns the site name.
func (d *Device) Site() string {
	return d.resolved.Site
}

// BGPNeighbors returns the list of BGP neighbor IPs (derived from route reflectors).
func (d *Device) BGPNeighbors() []string {
	return d.resolved.BGPNeighbors
}

// IsRouteReflector returns true if this device is a route reflector.
func (d *Device) IsRouteReflector() bool {
	return d.resolved.IsRouteReflector
}

// ConfigDB returns the config_db state.
func (d *Device) ConfigDB() *device.ConfigDB {
	return d.configDB
}

// ============================================================================
// Connection Management
// ============================================================================

// Connect establishes connection to the device via Redis/config_db.
func (d *Device) Connect(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.connected {
		return nil
	}

	// Create connection using device package
	d.conn = device.NewDevice(d.name, d.resolved)
	if err := d.conn.Connect(ctx); err != nil {
		return err
	}

	d.configDB = d.conn.ConfigDB
	d.connected = true

	// Load interfaces
	d.loadInterfaces()

	util.WithDevice(d.name).Info("Connected")
	return nil
}

// Disconnect closes the connection.
func (d *Device) Disconnect() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.connected {
		return nil
	}

	if d.conn != nil {
		d.conn.Disconnect()
	}

	d.connected = false
	util.WithDevice(d.name).Info("Disconnected")
	return nil
}

// IsConnected returns true if connected.
func (d *Device) IsConnected() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.connected
}

// Lock acquires a distributed lock for configuration changes.
// Constructs a holder identity from the current user and hostname,
// and acquires the lock with a default TTL of 3600 seconds.
func (d *Device) Lock() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.connected {
		return util.ErrNotConnected
	}
	if d.locked {
		return nil
	}

	holder := lockHolder()
	if err := d.conn.Lock(holder, 3600); err != nil {
		return err
	}

	d.locked = true
	return nil
}

// LockHolder returns the current lock holder and acquisition time.
// Returns ("", zero, nil) if no lock is held.
func (d *Device) LockHolder() (string, time.Time, error) {
	if !d.connected {
		return "", time.Time{}, fmt.Errorf("device not connected")
	}
	return d.conn.LockHolder()
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
func (d *Device) Unlock() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.locked {
		return nil
	}

	if err := d.conn.Unlock(); err != nil {
		return err
	}

	d.locked = false
	return nil
}

// IsLocked returns true if locked.
func (d *Device) IsLocked() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.locked
}

// ============================================================================
// Interface (Child) Management
// ============================================================================

// GetInterface returns an Interface object created in this Device's context.
// The Interface has access to Device properties AND Network configuration.
// Accepts both short (Eth0, Po100) and full (Ethernet0, PortChannel100) interface names.
func (d *Device) GetInterface(name string) (*Interface, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Normalize interface name (e.g., Eth0 -> Ethernet0, Po100 -> PortChannel100)
	name = util.NormalizeInterfaceName(name)

	// Return existing interface if already loaded
	if intf, ok := d.interfaces[name]; ok {
		return intf, nil
	}

	// Verify interface exists in config_db
	if !d.interfaceExistsInConfigDB(name) {
		return nil, fmt.Errorf("interface %s not found on device %s", name, d.name)
	}

	// Create Interface with parent reference to this Device
	intf := &Interface{
		device: d, // Parent reference - key to OO design
		name:   name,
	}

	// Load interface state from config_db
	intf.loadState()

	d.interfaces[name] = intf
	return intf, nil
}

// ListInterfaces returns all interface names.
func (d *Device) ListInterfaces() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var names []string

	// Physical interfaces from PORT table
	if d.configDB != nil {
		for name := range d.configDB.Port {
			names = append(names, name)
		}
		// Port channels
		for name := range d.configDB.PortChannel {
			names = append(names, name)
		}
	}

	return names
}

// interfaceExistsInConfigDB checks if interface exists.
func (d *Device) interfaceExistsInConfigDB(name string) bool {
	if d.configDB == nil {
		return false
	}
	if _, ok := d.configDB.Port[name]; ok {
		return true
	}
	if _, ok := d.configDB.PortChannel[name]; ok {
		return true
	}
	return false
}

// loadInterfaces populates the interfaces map from config_db.
func (d *Device) loadInterfaces() {
	if d.configDB == nil {
		return
	}

	for name := range d.configDB.Port {
		intf := &Interface{
			device: d,
			name:   name,
		}
		intf.loadState()
		d.interfaces[name] = intf
	}

	for name := range d.configDB.PortChannel {
		intf := &Interface{
			device: d,
			name:   name,
		}
		intf.loadState()
		d.interfaces[name] = intf
	}
}

// ============================================================================
// Existence Checks (convenience methods for preconditions)
// ============================================================================

// InterfaceExists checks if an interface exists.
// Accepts both short (Eth0) and full (Ethernet0) interface names.
func (d *Device) InterfaceExists(name string) bool {
	name = util.NormalizeInterfaceName(name)
	return d.interfaceExistsInConfigDB(name)
}

// VLANExists checks if a VLAN exists.
func (d *Device) VLANExists(id int) bool {
	if d.configDB == nil {
		return false
	}
	key := fmt.Sprintf("Vlan%d", id)
	_, ok := d.configDB.VLAN[key]
	return ok
}

// VRFExists checks if a VRF exists.
func (d *Device) VRFExists(name string) bool {
	if d.configDB == nil {
		return false
	}
	_, ok := d.configDB.VRF[name]
	return ok
}

// PortChannelExists checks if a PortChannel exists.
// Accepts both short (Po100) and full (PortChannel100) names.
func (d *Device) PortChannelExists(name string) bool {
	if d.configDB == nil {
		return false
	}
	name = util.NormalizeInterfaceName(name)
	_, ok := d.configDB.PortChannel[name]
	return ok
}

// VTEPExists checks if VTEP is configured.
func (d *Device) VTEPExists() bool {
	if d.configDB == nil {
		return false
	}
	return len(d.configDB.VXLANTunnel) > 0
}

// BGPConfigured checks if BGP is configured.
// Checks both CONFIG_DB BGP_NEIGHBOR table (CONFIG_DB-managed BGP) and
// DEVICE_METADATA bgp_asn (FRR-managed BGP with frr_split_config_enabled).
func (d *Device) BGPConfigured() bool {
	if d.configDB == nil {
		return false
	}
	if len(d.configDB.BGPNeighbor) > 0 {
		return true
	}
	if meta, ok := d.configDB.DeviceMetadata["localhost"]; ok {
		if asn, ok := meta["bgp_asn"]; ok && asn != "" {
			return true
		}
	}
	return false
}

// ACLTableExists checks if an ACL table exists.
func (d *Device) ACLTableExists(name string) bool {
	if d.configDB == nil {
		return false
	}
	_, ok := d.configDB.ACLTable[name]
	return ok
}

// InterfaceIsLAGMember checks if an interface is a LAG member.
// Accepts both short (Eth0) and full (Ethernet0) interface names.
func (d *Device) InterfaceIsLAGMember(name string) bool {
	if d.configDB == nil {
		return false
	}
	name = util.NormalizeInterfaceName(name)
	for key := range d.configDB.PortChannelMember {
		// Key format: PortChannel100|Ethernet0
		if len(key) > len(name) && key[len(key)-len(name):] == name {
			return true
		}
	}
	return false
}

// GetInterfaceLAG returns the LAG that an interface belongs to (empty if not a member).
// Accepts both short (Eth0) and full (Ethernet0) interface names.
func (d *Device) GetInterfaceLAG(name string) string {
	if d.configDB == nil {
		return ""
	}
	name = util.NormalizeInterfaceName(name)
	for key := range d.configDB.PortChannelMember {
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
func (d *Device) InterfaceHasService(name string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	name = util.NormalizeInterfaceName(name)
	if intf, ok := d.interfaces[name]; ok {
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
	Ports      []string    // All member ports
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
func (d *Device) GetVLAN(id int) (*VLANInfo, error) {
	if d.configDB == nil {
		return nil, fmt.Errorf("not connected")
	}

	vlanKey := fmt.Sprintf("Vlan%d", id)
	vlanEntry, ok := d.configDB.VLAN[vlanKey]
	if !ok {
		return nil, fmt.Errorf("VLAN %d not found", id)
	}

	info := &VLANInfo{ID: id, Name: vlanEntry.Description}

	// Collect member ports from VLAN_MEMBER
	for key, member := range d.configDB.VLANMember {
		parts := splitConfigDBKey(key)
		if len(parts) == 2 && parts[0] == vlanKey {
			port := parts[1]
			if member.TaggingMode == "tagged" {
				info.Ports = append(info.Ports, port+"(t)")
			} else {
				info.Ports = append(info.Ports, port)
			}
		}
	}

	// Check for SVI (VLAN_INTERFACE)
	if _, ok := d.configDB.VLANInterface[vlanKey]; ok {
		info.SVIStatus = "up"
	}

	// Build MAC-VPN info from VXLAN_TUNNEL_MAP and SUPPRESS_VLAN_NEIGH
	macvpn := &MACVPNInfo{}

	// Find L2VNI from VXLAN_TUNNEL_MAP
	for _, mapping := range d.configDB.VXLANTunnelMap {
		if mapping.VLAN == vlanKey && mapping.VNI != "" {
			fmt.Sscanf(mapping.VNI, "%d", &macvpn.L2VNI)
			break
		}
	}

	// Check ARP suppression
	if _, ok := d.configDB.SuppressVLANNeigh[vlanKey]; ok {
		macvpn.ARPSuppression = true
	}

	// Try to match to a macvpn definition by L2VNI
	if macvpn.L2VNI > 0 && d.network != nil && d.network.spec != nil {
		for macvpnName, macvpnDef := range d.network.spec.MACVPN {
			if macvpnDef.L2VNI == macvpn.L2VNI {
				macvpn.Name = macvpnName
				break
			}
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
func (d *Device) GetVRF(name string) (*VRFInfo, error) {
	if d.configDB == nil {
		return nil, fmt.Errorf("not connected")
	}

	vrfEntry, ok := d.configDB.VRF[name]
	if !ok {
		return nil, fmt.Errorf("VRF %s not found", name)
	}

	info := &VRFInfo{Name: name}

	// Parse L3VNI
	if vrfEntry.VNI != "" {
		fmt.Sscanf(vrfEntry.VNI, "%d", &info.L3VNI)
	}

	// Find interfaces bound to this VRF from INTERFACE table
	for key, intf := range d.configDB.Interface {
		// Key could be "Ethernet0" or "Ethernet0|10.1.1.1/24"
		parts := splitConfigDBKey(key)
		intfName := parts[0]
		if intf.VRFName == name {
			// Avoid duplicates
			found := false
			for _, existing := range info.Interfaces {
				if existing == intfName {
					found = true
					break
				}
			}
			if !found {
				info.Interfaces = append(info.Interfaces, intfName)
			}
		}
	}

	// Also check VLAN_INTERFACE for SVIs in this VRF
	for key := range d.configDB.VLANInterface {
		parts := splitConfigDBKey(key)
		vlanName := parts[0]
		// VLANInterface value contains vrf_name
		if vals, ok := d.configDB.VLANInterface[vlanName]; ok {
			if vals["vrf_name"] == name {
				found := false
				for _, existing := range info.Interfaces {
					if existing == vlanName {
						found = true
						break
					}
				}
				if !found {
					info.Interfaces = append(info.Interfaces, vlanName)
				}
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
func (d *Device) GetPortChannel(name string) (*PortChannelInfo, error) {
	if d.configDB == nil {
		return nil, fmt.Errorf("not connected")
	}

	// Normalize PortChannel name (e.g., Po100 -> PortChannel100)
	name = util.NormalizeInterfaceName(name)

	pcEntry, ok := d.configDB.PortChannel[name]
	if !ok {
		return nil, fmt.Errorf("PortChannel %s not found", name)
	}

	info := &PortChannelInfo{
		Name:        name,
		AdminStatus: pcEntry.AdminStatus,
	}

	// Collect members from PORTCHANNEL_MEMBER
	for key := range d.configDB.PortChannelMember {
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
func (d *Device) ListPortChannels() []string {
	if d.configDB == nil {
		return nil
	}

	names := make([]string, 0, len(d.configDB.PortChannel))
	for name := range d.configDB.PortChannel {
		names = append(names, name)
	}
	return names
}

// ListVLANs returns all VLAN IDs on this device.
func (d *Device) ListVLANs() []int {
	if d.configDB == nil {
		return nil
	}

	var ids []int
	for name := range d.configDB.VLAN {
		var id int
		if _, err := fmt.Sscanf(name, "Vlan%d", &id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// ListVRFs returns all VRF names on this device.
func (d *Device) ListVRFs() []string {
	if d.configDB == nil {
		return nil
	}

	names := make([]string, 0, len(d.configDB.VRF))
	for name := range d.configDB.VRF {
		names = append(names, name)
	}
	return names
}

// ReloadConfig triggers a config reload on the SONiC device via SSH.
// This causes SONiC to re-read CONFIG_DB and apply changes via frrcfgd.
func (d *Device) ReloadConfig(ctx context.Context) error {
	if !d.connected {
		return fmt.Errorf("device not connected")
	}
	return d.conn.ReloadConfig(ctx)
}

// SaveConfig persists the device's running CONFIG_DB to disk via SSH.
func (d *Device) SaveConfig(ctx context.Context) error {
	if !d.connected {
		return fmt.Errorf("device not connected")
	}
	return d.conn.SaveConfig(ctx)
}

// Underlying returns the underlying device.Device for low-level operations.
// This is used by ChangeSet to apply changes via Redis.
func (d *Device) Underlying() *device.Device {
	return d.conn
}

// GetRoute reads a route from APP_DB (Redis DB 0).
// Returns nil RouteEntry (not error) if the prefix is not present.
// Single-shot read — does not poll or retry.
func (d *Device) GetRoute(ctx context.Context, vrf, prefix string) (*device.RouteEntry, error) {
	if !d.connected {
		return nil, fmt.Errorf("device not connected")
	}
	return d.conn.GetRoute(ctx, vrf, prefix)
}

// GetRouteASIC reads a route from ASIC_DB (Redis DB 1) by resolving the SAI
// object chain. Returns nil RouteEntry (not error) if not programmed in ASIC.
// Single-shot read — does not poll or retry.
func (d *Device) GetRouteASIC(ctx context.Context, vrf, prefix string) (*device.RouteEntry, error) {
	if !d.connected {
		return nil, fmt.Errorf("device not connected")
	}
	return d.conn.GetRouteASIC(ctx, vrf, prefix)
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
func (d *Device) AddLoopbackBGPNeighbor(ctx context.Context, neighborIP string, asn int, description string, evpn bool) (*ChangeSet, error) {
	if !d.connected {
		return nil, util.ErrNotConnected
	}
	if !d.locked {
		return nil, fmt.Errorf("device not locked")
	}

	// Validate neighbor IP
	if !util.IsValidIPv4(neighborIP) {
		return nil, fmt.Errorf("invalid neighbor IP: %s", neighborIP)
	}

	// Check if neighbor already exists
	if d.configDB != nil {
		if _, ok := d.configDB.BGPNeighbor[neighborIP]; ok {
			return nil, fmt.Errorf("BGP neighbor %s already exists", neighborIP)
		}
	}

	cs := NewChangeSet(d.name, "bgp.add-loopback-neighbor")

	// Add BGP neighbor entry with loopback as update-source
	fields := map[string]string{
		"asn":          fmt.Sprintf("%d", asn),
		"admin_status": "up",
		"local_addr":   d.resolved.LoopbackIP, // Update-source = loopback
	}
	if description != "" {
		fields["name"] = description
	}

	// For iBGP (same AS), no special config needed
	// For eBGP with loopback, would need ebgp_multihop
	if asn != d.resolved.ASNumber {
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

	util.WithDevice(d.name).Infof("Adding loopback BGP neighbor %s (AS %d, update-source: %s)",
		neighborIP, asn, d.resolved.LoopbackIP)
	return cs, nil
}

// AddBGPNeighbor is an alias for AddLoopbackBGPNeighbor for backward compatibility.
// Deprecated: Use AddLoopbackBGPNeighbor for clarity.
func (d *Device) AddBGPNeighbor(ctx context.Context, neighborIP string, asn int, description string, evpn bool) (*ChangeSet, error) {
	return d.AddLoopbackBGPNeighbor(ctx, neighborIP, asn, description, evpn)
}

// RemoveBGPNeighbor removes a BGP neighbor from the device.
// This works for both direct (interface-level) and indirect (loopback-level) neighbors.
func (d *Device) RemoveBGPNeighbor(ctx context.Context, neighborIP string) (*ChangeSet, error) {
	if !d.connected {
		return nil, util.ErrNotConnected
	}
	if !d.locked {
		return nil, fmt.Errorf("device not locked")
	}

	// Check if neighbor exists
	if d.configDB != nil {
		if _, ok := d.configDB.BGPNeighbor[neighborIP]; !ok {
			return nil, fmt.Errorf("BGP neighbor %s not found", neighborIP)
		}
	}

	cs := NewChangeSet(d.name, "bgp.remove-neighbor")

	// Remove all address-family entries first
	// Key format: vrf|neighborIP|af (per SONiC Unified FRR Mgmt schema)
	for _, af := range []string{"ipv4_unicast", "ipv6_unicast", "l2vpn_evpn"} {
		afKey := fmt.Sprintf("default|%s|%s", neighborIP, af)
		cs.Add("BGP_NEIGHBOR_AF", afKey, ChangeDelete, nil, nil)
	}

	// Remove neighbor entry
	cs.Add("BGP_NEIGHBOR", fmt.Sprintf("default|%s", neighborIP), ChangeDelete, nil, nil)

	util.WithDevice(d.name).Infof("Removing BGP neighbor %s", neighborIP)
	return cs, nil
}

// SetupBGPEVPN configures BGP EVPN with route reflectors from site config.
// This sets up iBGP sessions using loopback IPs (indirect neighbors) for EVPN.
func (d *Device) SetupBGPEVPN(ctx context.Context) (*ChangeSet, error) {
	if !d.connected {
		return nil, util.ErrNotConnected
	}
	if !d.locked {
		return nil, fmt.Errorf("device not locked")
	}

	resolved := d.Resolved()
	if len(resolved.BGPNeighbors) == 0 {
		return nil, fmt.Errorf("no route reflectors defined in site configuration")
	}

	cs := NewChangeSet(d.name, "bgp.setup-evpn")

	// Configure BGP globals (if not already set)
	cs.Add("BGP_GLOBALS", "default", ChangeAdd, nil, map[string]string{
		"local_asn": fmt.Sprintf("%d", resolved.ASNumber),
		"router_id": resolved.RouterID,
	})

	// Configure global L2VPN EVPN address-family
	cs.Add("BGP_GLOBALS_AF", "default|l2vpn_evpn", ChangeAdd, nil, map[string]string{
		"advertise-all-vni": "true",
	})

	// Add each route reflector as an iBGP EVPN neighbor (using loopback)
	for _, rrIP := range resolved.BGPNeighbors {
		// Skip if this is our own IP
		if rrIP == resolved.LoopbackIP {
			continue
		}

		// Check if neighbor already exists
		if d.configDB != nil {
			if _, ok := d.configDB.BGPNeighbor[rrIP]; ok {
				// Already exists, skip
				continue
			}
		}

		// Add neighbor entry with loopback as update-source (iBGP)
		fields := map[string]string{
			"asn":          fmt.Sprintf("%d", resolved.ASNumber), // iBGP (same AS)
			"admin_status": "up",
			"name":         "route-reflector",
			"local_addr":   resolved.LoopbackIP, // Update-source = loopback
		}
		// Key format: vrf|neighborIP (per SONiC Unified FRR Mgmt schema)
		cs.Add("BGP_NEIGHBOR", fmt.Sprintf("default|%s", rrIP), ChangeAdd, nil, fields)

		// Activate L2VPN EVPN for this neighbor
		afKey := fmt.Sprintf("default|%s|l2vpn_evpn", rrIP)
		cs.Add("BGP_NEIGHBOR_AF", afKey, ChangeAdd, nil, map[string]string{
			"activate": "true",
		})
	}

	util.WithDevice(d.name).Infof("Setting up BGP EVPN with %d route reflectors (via loopback %s)",
		len(resolved.BGPNeighbors), resolved.LoopbackIP)
	return cs, nil
}

// BGPNeighborExists checks if a BGP neighbor exists.
func (d *Device) BGPNeighborExists(neighborIP string) bool {
	if d.configDB == nil {
		return false
	}
	_, ok := d.configDB.BGPNeighbor[neighborIP]
	return ok
}

// VTEPSourceIP returns the VTEP source IP (from loopback).
func (d *Device) VTEPSourceIP() string {
	if d.configDB == nil {
		return d.resolved.LoopbackIP
	}
	// Check if VTEP is configured
	for _, vtep := range d.configDB.VXLANTunnel {
		if vtep.SrcIP != "" {
			return vtep.SrcIP
		}
	}
	// Fall back to resolved loopback IP
	return d.resolved.LoopbackIP
}

// ListBGPNeighbors returns all BGP neighbor IPs from config_db.
func (d *Device) ListBGPNeighbors() []string {
	if d.configDB == nil {
		return nil
	}
	neighbors := make([]string, 0, len(d.configDB.BGPNeighbor))
	for ip := range d.configDB.BGPNeighbor {
		neighbors = append(neighbors, ip)
	}
	return neighbors
}

// ListACLTables returns all ACL table names on this device.
func (d *Device) ListACLTables() []string {
	if d.configDB == nil {
		return nil
	}
	names := make([]string, 0, len(d.configDB.ACLTable))
	for name := range d.configDB.ACLTable {
		names = append(names, name)
	}
	return names
}

// ACLTableInfo represents ACL table data for display.
type ACLTableInfo struct {
	Name   string
	Type   string
	Stage  string
	Ports  string
	Policy string
}

// GetACLTable retrieves ACL table information by name.
func (d *Device) GetACLTable(name string) (*ACLTableInfo, error) {
	if d.configDB == nil {
		return nil, fmt.Errorf("not connected")
	}
	acl, ok := d.configDB.ACLTable[name]
	if !ok {
		return nil, fmt.Errorf("ACL table %s not found", name)
	}
	return &ACLTableInfo{
		Name:   name,
		Type:   acl.Type,
		Stage:  acl.Stage,
		Ports:  acl.Ports,
		Policy: acl.PolicyDesc,
	}, nil
}

// GetOrphanedACLs returns ACL tables that have no ports bound.
func (d *Device) GetOrphanedACLs() []string {
	if d.configDB == nil {
		return nil
	}
	var orphans []string
	for name, acl := range d.configDB.ACLTable {
		if acl.Ports == "" {
			orphans = append(orphans, name)
		}
	}
	return orphans
}
