package node

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/util"
)

// VLANName returns the SONiC name for a VLAN (e.g., "Vlan100").
func VLANName(vlanID int) string { return fmt.Sprintf("Vlan%d", vlanID) }

// VLANMemberKey returns the CONFIG_DB key for a VLAN_MEMBER entry.
func VLANMemberKey(vlanID int, intfName string) string {
	return fmt.Sprintf("%s|%s", VLANName(vlanID), intfName)
}

// SVIIPKey returns the CONFIG_DB key for a VLAN_INTERFACE IP entry.
func SVIIPKey(vlanID int, ipAddr string) string {
	return fmt.Sprintf("%s|%s", VLANName(vlanID), ipAddr)
}

// vlanResource returns the canonical resource name for a VLAN (precondition locking).
func vlanResource(id int) string { return VLANName(id) }

// ============================================================================
// VLAN Operations
// ============================================================================

// SVIConfig holds configuration options for ConfigureSVI.
type SVIConfig struct {
	VRF        string // VRF to bind the SVI to
	IPAddress  string // IP address with prefix (e.g., "10.1.100.1/24")
	AnycastMAC string // SAG anycast gateway MAC (e.g., "00:00:00:00:01:01")
}

// VLANConfig holds configuration options for CreateVLAN.
type VLANConfig struct {
	Name        string // VLAN name (alias for Description)
	Description string
	L2VNI       int
}

// createVlanConfig returns CONFIG_DB entries for a VLAN: a VLAN entry and an optional
// VXLAN_TUNNEL_MAP entry when L2VNI is specified.
func createVlanConfig(vlanID int, opts VLANConfig) []sonic.Entry {
	vlanName := VLANName(vlanID)
	fields := map[string]string{
		"vlanid": fmt.Sprintf("%d", vlanID),
	}
	if opts.Description != "" {
		fields["description"] = opts.Description
	}

	entries := []sonic.Entry{
		{Table: "VLAN", Key: vlanName, Fields: fields},
	}

	if opts.L2VNI > 0 {
		entries = append(entries, createVniMapConfig(vlanName, opts.L2VNI)...)
	}

	return entries
}

// createVlanMemberConfig returns a CONFIG_DB VLAN_MEMBER entry for adding an
// interface to a VLAN with the specified tagging mode.
func createVlanMemberConfig(vlanID int, interfaceName string, tagged bool) []sonic.Entry {
	taggingMode := "untagged"
	if tagged {
		taggingMode = "tagged"
	}

	return []sonic.Entry{
		{Table: "VLAN_MEMBER", Key: VLANMemberKey(vlanID, interfaceName), Fields: map[string]string{
			"tagging_mode": taggingMode,
		}},
	}
}

// createSviConfig returns CONFIG_DB entries for an SVI: a VLAN_INTERFACE base entry
// with optional VRF binding, an optional IP address entry, and an optional
// SAG_GLOBAL entry for anycast gateway MAC.
func createSviConfig(vlanID int, opts SVIConfig) []sonic.Entry {
	vlanName := VLANName(vlanID)

	// VLAN_INTERFACE base entry with optional VRF binding
	fields := map[string]string{}
	if opts.VRF != "" {
		fields["vrf_name"] = opts.VRF
	}
	entries := []sonic.Entry{
		{Table: "VLAN_INTERFACE", Key: vlanName, Fields: fields},
	}

	// IP address binding
	if opts.IPAddress != "" {
		entries = append(entries, sonic.Entry{
			Table: "VLAN_INTERFACE", Key: SVIIPKey(vlanID, opts.IPAddress), Fields: map[string]string{},
		})
	}

	// Anycast gateway MAC (SAG)
	if opts.AnycastMAC != "" {
		entries = append(entries, sonic.Entry{
			Table: "SAG_GLOBAL", Key: "IPv4", Fields: map[string]string{
				"gwmac": opts.AnycastMAC,
			},
		})
	}

	return entries
}

// deleteSagGlobalConfig returns the delete entry for the SAG_GLOBAL IPv4 singleton.
func deleteSagGlobalConfig() []sonic.Entry {
	return []sonic.Entry{{Table: "SAG_GLOBAL", Key: "IPv4"}}
}

// deleteVlanMemberConfig returns the delete entry for a single VLAN member.
func deleteVlanMemberConfig(vlanID int, intfName string) []sonic.Entry {
	return []sonic.Entry{{Table: "VLAN_MEMBER", Key: VLANMemberKey(vlanID, intfName)}}
}

// deleteSviIPConfig returns the delete entry for a specific SVI IP binding.
func deleteSviIPConfig(vlanID int, ipAddr string) []sonic.Entry {
	return []sonic.Entry{{Table: "VLAN_INTERFACE", Key: SVIIPKey(vlanID, ipAddr)}}
}

// deleteSviBaseConfig returns the delete entry for a VLAN_INTERFACE base entry.
func deleteSviBaseConfig(vlanID int) []sonic.Entry {
	return []sonic.Entry{{Table: "VLAN_INTERFACE", Key: VLANName(vlanID)}}
}

// deleteVlanConfig returns the delete entry for a VLAN table entry.
// Unlike destroyVlan, this does not scan configDB for members or VNI mappings.
func deleteVlanConfig(vlanID int) []sonic.Entry {
	return []sonic.Entry{{Table: "VLAN", Key: VLANName(vlanID)}}
}

// CreateVLAN creates a new VLAN on this device.
func (n *Node) CreateVLAN(ctx context.Context, vlanID int, opts VLANConfig) (*ChangeSet, error) {
	cs, err := n.op("create-vlan", vlanResource(vlanID), ChangeAdd,
		func(pc *PreconditionChecker) {
			pc.Check(vlanID >= 1 && vlanID <= 4094, "valid VLAN ID", fmt.Sprintf("must be 1-4094, got %d", vlanID)).
				RequireVLANNotExists(vlanID)
		},
		func() []sonic.Entry { return createVlanConfig(vlanID, opts) })
	if err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Created VLAN %d", vlanID)
	return cs, nil
}

// destroyVlanConfig returns delete entries for a VLAN: its members, VNI mapping, and the VLAN itself.
func (n *Node) destroyVlanConfig(vlanID int) []sonic.Entry {
	vlanName := VLANName(vlanID)
	var entries []sonic.Entry

	// Remove VLAN members first
	if n.configDB != nil {
		for key := range n.configDB.VLANMember {
			parts := splitKey(key)
			if len(parts) == 2 && parts[0] == vlanName {
				entries = append(entries, sonic.Entry{Table: "VLAN_MEMBER", Key: key})
			}
		}
	}

	// Remove VNI mapping if exists (delegates to evpn_ops.go)
	if n.configDB != nil {
		for key, mapping := range n.configDB.VXLANTunnelMap {
			if mapping.VLAN == vlanName {
				entries = append(entries, deleteVniMapByKeyConfig(key)...)
			}
		}
	}

	entries = append(entries, sonic.Entry{Table: "VLAN", Key: vlanName})
	return entries
}

// DeleteVLAN removes a VLAN from this device.
func (n *Node) DeleteVLAN(ctx context.Context, vlanID int) (*ChangeSet, error) {
	cs, err := n.op("delete-vlan", vlanResource(vlanID), ChangeDelete,
		func(pc *PreconditionChecker) { pc.RequireVLANExists(vlanID) },
		func() []sonic.Entry { return n.destroyVlanConfig(vlanID) })
	if err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Deleted VLAN %d", vlanID)
	return cs, nil
}

// AddVLANMember adds an interface to a VLAN as a tagged or untagged member.
func (n *Node) AddVLANMember(ctx context.Context, vlanID int, interfaceName string, tagged bool) (*ChangeSet, error) {
	interfaceName = util.NormalizeInterfaceName(interfaceName)

	cs, err := n.op("add-vlan-member", vlanResource(vlanID), ChangeAdd,
		func(pc *PreconditionChecker) {
			pc.RequireVLANExists(vlanID).RequireInterfaceExists(interfaceName)
		},
		func() []sonic.Entry { return createVlanMemberConfig(vlanID, interfaceName, tagged) })
	if err != nil {
		return nil, err
	}
	taggingMode := "untagged"
	if tagged {
		taggingMode = "tagged"
	}
	util.WithDevice(n.name).Infof("Added %s to VLAN %d (%s)", interfaceName, vlanID, taggingMode)
	return cs, nil
}

// RemoveVLANMember removes an interface from a VLAN.
func (n *Node) RemoveVLANMember(ctx context.Context, vlanID int, interfaceName string) (*ChangeSet, error) {
	interfaceName = util.NormalizeInterfaceName(interfaceName)

	cs, err := n.op("remove-vlan-member", vlanResource(vlanID), ChangeDelete,
		func(pc *PreconditionChecker) { pc.RequireVLANExists(vlanID) },
		func() []sonic.Entry {
			return []sonic.Entry{{Table: "VLAN_MEMBER", Key: VLANMemberKey(vlanID, interfaceName)}}
		})
	if err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Removed %s from VLAN %d", interfaceName, vlanID)
	return cs, nil
}

// ConfigureSVI configures a VLAN's SVI (Layer 3 interface).
// This creates VLAN_INTERFACE entries for VRF binding and IP assignment,
// and optionally sets up SAG (Static Anycast Gateway) for anycast MAC.
func (n *Node) ConfigureSVI(ctx context.Context, vlanID int, opts SVIConfig) (*ChangeSet, error) {
	cs, err := n.op("configure-svi", vlanResource(vlanID), ChangeAdd,
		func(pc *PreconditionChecker) {
			pc.RequireVLANExists(vlanID)
			if opts.VRF != "" {
				pc.RequireVRFExists(opts.VRF)
			}
		},
		func() []sonic.Entry { return createSviConfig(vlanID, opts) })
	if err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Configured SVI for VLAN %d", vlanID)
	return cs, nil
}

// destroySviConfig returns delete entries for a VLAN's SVI: IP entries and base entry.
func (n *Node) destroySviConfig(vlanID int) []sonic.Entry {
	vlanName := VLANName(vlanID)
	var entries []sonic.Entry

	if n.configDB == nil {
		return nil
	}

	// Delete all VLAN_INTERFACE IP entries (e.g., Vlan100|10.1.1.1/24)
	for key := range n.configDB.VLANInterface {
		if strings.HasPrefix(key, vlanName+"|") {
			entries = append(entries, sonic.Entry{Table: "VLAN_INTERFACE", Key: key})
		}
	}

	// Delete VLAN_INTERFACE base entry (e.g., Vlan100)
	if _, ok := n.configDB.VLANInterface[vlanName]; ok {
		entries = append(entries, sonic.Entry{Table: "VLAN_INTERFACE", Key: vlanName})
	}

	return entries
}

// RemoveSVI removes a VLAN's SVI (Layer 3 interface) configuration.
// This deletes VLAN_INTERFACE entries (base + IP) and SAG_GLOBAL if no other SVIs use it.
func (n *Node) RemoveSVI(ctx context.Context, vlanID int) (*ChangeSet, error) {
	if err := n.precondition("remove-svi", vlanResource(vlanID)).Result(); err != nil {
		return nil, err
	}

	if n.configDB == nil {
		return nil, fmt.Errorf("no CONFIG_DB available")
	}

	cs := buildChangeSet(n.name, "device.remove-svi", n.destroySviConfig(vlanID), ChangeDelete)

	if cs.IsEmpty() {
		return nil, fmt.Errorf("no SVI configuration found for VLAN %d", vlanID)
	}

	util.WithDevice(n.name).Infof("Removed SVI for VLAN %d", vlanID)
	return cs, nil
}

// ============================================================================
// VLAN Data Types and Queries
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

// VLANExists checks if a VLAN exists.
func (n *Node) VLANExists(id int) bool { return n.configDB.HasVLAN(id) }

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
		parts := splitKey(key)
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

	// Try to match to a macvpn definition by VNI
	if macvpn.L2VNI > 0 && n.SpecProvider != nil {
		if name, _ := n.FindMACVPNByVNI(macvpn.L2VNI); name != "" {
			macvpn.Name = name
		}
	}

	// Only set MACVPNInfo if there's actually some data
	if macvpn.L2VNI > 0 || macvpn.ARPSuppression {
		info.MACVPNInfo = macvpn
	}

	return info, nil
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
