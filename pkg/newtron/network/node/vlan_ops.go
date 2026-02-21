package node

import (
	"context"
	"fmt"
	"strings"

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

// vlanConfig returns CONFIG_DB entries for a VLAN: a VLAN entry and an optional
// VXLAN_TUNNEL_MAP entry when L2VNI is specified.
func vlanConfig(vlanID int, opts VLANConfig) []CompositeEntry {
	vlanName := VLANName(vlanID)
	fields := map[string]string{
		"vlanid": fmt.Sprintf("%d", vlanID),
	}
	if opts.Description != "" {
		fields["description"] = opts.Description
	}

	entries := []CompositeEntry{
		{Table: "VLAN", Key: vlanName, Fields: fields},
	}

	if opts.L2VNI > 0 {
		entries = append(entries, CompositeEntry{
			Table: "VXLAN_TUNNEL_MAP",
			Key:   VNIMapKey(opts.L2VNI, vlanName),
			Fields: map[string]string{
				"vlan": vlanName,
				"vni":  fmt.Sprintf("%d", opts.L2VNI),
			},
		})
	}

	return entries
}

// vlanMemberConfig returns a CONFIG_DB VLAN_MEMBER entry for adding an
// interface to a VLAN with the specified tagging mode.
func vlanMemberConfig(vlanID int, interfaceName string, tagged bool) []CompositeEntry {
	taggingMode := "untagged"
	if tagged {
		taggingMode = "tagged"
	}

	return []CompositeEntry{
		{Table: "VLAN_MEMBER", Key: VLANMemberKey(vlanID, interfaceName), Fields: map[string]string{
			"tagging_mode": taggingMode,
		}},
	}
}

// sviConfig returns CONFIG_DB entries for an SVI: a VLAN_INTERFACE base entry
// with optional VRF binding, an optional IP address entry, and an optional
// SAG_GLOBAL entry for anycast gateway MAC.
func sviConfig(vlanID int, opts SVIConfig) []CompositeEntry {
	vlanName := VLANName(vlanID)

	// VLAN_INTERFACE base entry with optional VRF binding
	fields := map[string]string{}
	if opts.VRF != "" {
		fields["vrf_name"] = opts.VRF
	}
	entries := []CompositeEntry{
		{Table: "VLAN_INTERFACE", Key: vlanName, Fields: fields},
	}

	// IP address binding
	if opts.IPAddress != "" {
		entries = append(entries, CompositeEntry{
			Table: "VLAN_INTERFACE", Key: SVIIPKey(vlanID, opts.IPAddress), Fields: map[string]string{},
		})
	}

	// Anycast gateway MAC (SAG)
	if opts.AnycastMAC != "" {
		entries = append(entries, CompositeEntry{
			Table: "SAG_GLOBAL", Key: "IPv4", Fields: map[string]string{
				"gwmac": opts.AnycastMAC,
			},
		})
	}

	return entries
}

// CreateVLAN creates a new VLAN on this device.
func (n *Node) CreateVLAN(ctx context.Context, vlanID int, opts VLANConfig) (*ChangeSet, error) {
	if err := n.precondition("create-vlan", vlanResource(vlanID)).
		Check(vlanID >= 1 && vlanID <= 4094, "valid VLAN ID", fmt.Sprintf("must be 1-4094, got %d", vlanID)).
		RequireVLANNotExists(vlanID).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.create-vlan")
	for _, e := range vlanConfig(vlanID, opts) {
		cs.Add(e.Table, e.Key, ChangeAdd, nil, e.Fields)
	}

	util.WithDevice(n.name).Infof("Created VLAN %d", vlanID)
	return cs, nil
}

// DeleteVLAN removes a VLAN from this device.
func (n *Node) DeleteVLAN(ctx context.Context, vlanID int) (*ChangeSet, error) {
	if err := n.precondition("delete-vlan", vlanResource(vlanID)).
		RequireVLANExists(vlanID).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.delete-vlan")
	vlanName := VLANName(vlanID)

	// Remove VLAN members first
	if n.configDB != nil {
		for key := range n.configDB.VLANMember {
			parts := splitConfigDBKey(key)
			if len(parts) == 2 && parts[0] == vlanName {
				cs.Add("VLAN_MEMBER", key, ChangeDelete, nil, nil)
			}
		}
	}

	// Remove VNI mapping if exists
	if n.configDB != nil {
		for key, mapping := range n.configDB.VXLANTunnelMap {
			if mapping.VLAN == vlanName {
				cs.Add("VXLAN_TUNNEL_MAP", key, ChangeDelete, nil, nil)
			}
		}
	}

	cs.Add("VLAN", vlanName, ChangeDelete, nil, nil)

	util.WithDevice(n.name).Infof("Deleted VLAN %d", vlanID)
	return cs, nil
}

// AddVLANMember adds an interface to a VLAN as a tagged or untagged member.
func (n *Node) AddVLANMember(ctx context.Context, vlanID int, interfaceName string, tagged bool) (*ChangeSet, error) {
	// Normalize interface name (e.g., Eth0 -> Ethernet0)
	interfaceName = util.NormalizeInterfaceName(interfaceName)

	if err := n.precondition("add-vlan-member", vlanResource(vlanID)).
		RequireVLANExists(vlanID).
		RequireInterfaceExists(interfaceName).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.add-vlan-member")
	for _, e := range vlanMemberConfig(vlanID, interfaceName, tagged) {
		cs.Add(e.Table, e.Key, ChangeAdd, nil, e.Fields)
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

	if err := n.precondition("remove-vlan-member", vlanResource(vlanID)).
		RequireVLANExists(vlanID).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.remove-vlan-member")

	cs.Add("VLAN_MEMBER", VLANMemberKey(vlanID, interfaceName), ChangeDelete, nil, nil)

	util.WithDevice(n.name).Infof("Removed %s from VLAN %d", interfaceName, vlanID)
	return cs, nil
}

// ConfigureSVI configures a VLAN's SVI (Layer 3 interface).
// This creates VLAN_INTERFACE entries for VRF binding and IP assignment,
// and optionally sets up SAG (Static Anycast Gateway) for anycast MAC.
func (n *Node) ConfigureSVI(ctx context.Context, vlanID int, opts SVIConfig) (*ChangeSet, error) {
	pc := n.precondition("configure-svi", vlanResource(vlanID)).
		RequireVLANExists(vlanID)
	if opts.VRF != "" {
		pc.RequireVRFExists(opts.VRF)
	}
	if err := pc.Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.configure-svi")
	for _, e := range sviConfig(vlanID, opts) {
		cs.Add(e.Table, e.Key, ChangeAdd, nil, e.Fields)
	}

	util.WithDevice(n.name).Infof("Configured SVI for VLAN %d", vlanID)
	return cs, nil
}

// RemoveSVI removes a VLAN's SVI (Layer 3 interface) configuration.
// This deletes VLAN_INTERFACE entries (base + IP) and SAG_GLOBAL if no other SVIs use it.
func (n *Node) RemoveSVI(ctx context.Context, vlanID int) (*ChangeSet, error) {
	if err := n.precondition("remove-svi", vlanResource(vlanID)).Result(); err != nil {
		return nil, err
	}

	vlanName := VLANName(vlanID)
	cs := NewChangeSet(n.name, "device.remove-svi")

	configDB := n.ConfigDB()
	if configDB == nil {
		return nil, fmt.Errorf("no CONFIG_DB available")
	}

	// Delete all VLAN_INTERFACE IP entries (e.g., Vlan100|10.1.1.1/24)
	for key := range configDB.VLANInterface {
		if strings.HasPrefix(key, vlanName+"|") {
			cs.Add("VLAN_INTERFACE", key, ChangeDelete, nil, nil)
		}
	}

	// Delete VLAN_INTERFACE base entry (e.g., Vlan100)
	if _, ok := configDB.VLANInterface[vlanName]; ok {
		cs.Add("VLAN_INTERFACE", vlanName, ChangeDelete, nil, nil)
	}

	if cs.IsEmpty() {
		return nil, fmt.Errorf("no SVI configuration found for VLAN %d", vlanID)
	}

	util.WithDevice(n.name).Infof("Removed SVI for VLAN %d", vlanID)
	return cs, nil
}
