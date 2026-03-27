package node

import (
	"context"
	"fmt"
	"strconv"
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

// IRBIPKey returns the CONFIG_DB key for a VLAN_INTERFACE IP entry.
func IRBIPKey(vlanID int, ipAddr string) string {
	return fmt.Sprintf("%s|%s", VLANName(vlanID), ipAddr)
}

// vlanResource returns the canonical resource name for a VLAN (precondition locking).
func vlanResource(id int) string { return VLANName(id) }

// ============================================================================
// VLAN Operations
// ============================================================================

// IRBConfig holds configuration options for ConfigureIRB.
type IRBConfig struct {
	VRF        string // VRF to bind the IRB to
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

// createSviConfig returns CONFIG_DB entries for an IRB: a VLAN_INTERFACE base entry
// with optional VRF binding, an optional IP address entry, and an optional
// SAG_GLOBAL entry for anycast gateway MAC.
func createSviConfig(vlanID int, opts IRBConfig) []sonic.Entry {
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
			Table: "VLAN_INTERFACE", Key: IRBIPKey(vlanID, opts.IPAddress), Fields: map[string]string{},
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
	return []sonic.Entry{{Table: "VLAN_INTERFACE", Key: IRBIPKey(vlanID, ipAddr)}}
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
// Intent-idempotent: if the vlan intent already exists, returns empty ChangeSet.
func (n *Node) CreateVLAN(ctx context.Context, vlanID int, opts VLANConfig) (*ChangeSet, error) {
	resource := "vlan|" + strconv.Itoa(vlanID)
	if n.GetIntent(resource) != nil {
		return NewChangeSet(n.name, "device.create-vlan"), nil
	}
	cs, err := n.op(sonic.OpCreateVLAN, vlanResource(vlanID), ChangeAdd,
		func(pc *PreconditionChecker) {
			pc.Check(vlanID >= 1 && vlanID <= 4094, "valid VLAN ID", fmt.Sprintf("must be 1-4094, got %d", vlanID))
		},
		func() []sonic.Entry { return createVlanConfig(vlanID, opts) },
		"device.delete-vlan")
	if err != nil {
		return nil, err
	}
	intentParams := map[string]string{
		sonic.FieldVLANID: strconv.Itoa(vlanID),
	}
	if opts.Description != "" {
		intentParams[sonic.FieldDescription] = opts.Description
	}
	if opts.L2VNI > 0 {
		intentParams[sonic.FieldVNI] = strconv.Itoa(opts.L2VNI)
	}
	if err := n.writeIntent(cs, sonic.OpCreateVLAN, "vlan|"+strconv.Itoa(vlanID), intentParams, []string{"device"}); err != nil {
		return nil, err
	}
	cs.OperationParams = map[string]string{"vlan_id": fmt.Sprintf("%d", vlanID)}
	util.WithDevice(n.name).Infof("Created VLAN %d", vlanID)
	return cs, nil
}

// destroyVlanConfig returns delete entries for a VLAN.
// Under the DAG, children (members, IRB, VNI mapping) are already removed before
// VLAN deletion, so only the VLAN entry itself needs to be deleted.
func (n *Node) destroyVlanConfig(vlanID int) []sonic.Entry {
	return deleteVlanConfig(vlanID)
}

// DeleteVLAN removes a VLAN from this device.
// Checks DAG children first (I5) — if children exist, returns an error
// before issuing any CONFIG_DB deletes.
func (n *Node) DeleteVLAN(ctx context.Context, vlanID int) (*ChangeSet, error) {
	intentKey := "vlan|" + strconv.Itoa(vlanID)

	// Pre-check: if intent has children, deleteIntent would fail (I5).
	// Check here so we fail before n.op() issues CONFIG_DB deletes.
	if intent := n.GetIntent(intentKey); intent != nil && len(intent.Children) > 0 {
		return nil, fmt.Errorf("deleteIntent %q: has children %v", intentKey, intent.Children)
	}

	cs, err := n.op("delete-vlan", vlanResource(vlanID), ChangeDelete,
		func(pc *PreconditionChecker) { pc.RequireVLANExists(vlanID) },
		func() []sonic.Entry { return n.destroyVlanConfig(vlanID) })
	if err != nil {
		return nil, err
	}
	if err := n.deleteIntent(cs, intentKey); err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Deleted VLAN %d", vlanID)
	return cs, nil
}

// ConfigureIRB configures a VLAN's IRB (Integrated Routing and Bridging) interface.
// This creates VLAN_INTERFACE entries for VRF binding and IP assignment,
// and optionally sets up SAG (Static Anycast Gateway) for anycast MAC.
// Intent-idempotent: if the IRB intent already exists, returns empty ChangeSet.
func (n *Node) ConfigureIRB(ctx context.Context, vlanID int, opts IRBConfig) (*ChangeSet, error) {
	if n.GetIntent("irb|"+strconv.Itoa(vlanID)) != nil {
		return NewChangeSet(n.name, "device."+sonic.OpConfigureIRB), nil
	}

	cs, err := n.op(sonic.OpConfigureIRB, vlanResource(vlanID), ChangeAdd,
		func(pc *PreconditionChecker) {
			pc.RequireVLANExists(vlanID)
			if opts.VRF != "" {
				pc.RequireVRFExists(opts.VRF)
			}
		},
		func() []sonic.Entry { return createSviConfig(vlanID, opts) },
		"device.unconfigure-irb")
	if err != nil {
		return nil, err
	}
	irbParents := []string{"vlan|" + strconv.Itoa(vlanID)}
	if opts.VRF != "" {
		irbParents = append(irbParents, "vrf|"+opts.VRF)
	}
	if err := n.writeIntent(cs, sonic.OpConfigureIRB, "irb|"+strconv.Itoa(vlanID), map[string]string{
		sonic.FieldVLANID:     strconv.Itoa(vlanID),
		sonic.FieldVRF:        opts.VRF,
		sonic.FieldIPAddress:  opts.IPAddress,
		sonic.FieldAnycastMAC: opts.AnycastMAC,
	}, irbParents); err != nil {
		return nil, err
	}
	cs.OperationParams = map[string]string{"vlan_id": fmt.Sprintf("%d", vlanID)}
	util.WithDevice(n.name).Infof("Configured IRB for VLAN %d", vlanID)
	return cs, nil
}


// UnconfigureIRB removes a VLAN's IRB (Integrated Routing and Bridging) interface configuration.
// Reads the intent record to determine what was applied (VRF, IP, anycast MAC).
func (n *Node) UnconfigureIRB(ctx context.Context, vlanID int) (*ChangeSet, error) {
	if err := n.precondition("unconfigure-irb", vlanResource(vlanID)).Result(); err != nil {
		return nil, err
	}

	intentKey := "irb|" + strconv.Itoa(vlanID)
	intent := n.GetIntent(intentKey)
	if intent == nil {
		return nil, fmt.Errorf("no IRB intent for VLAN %d", vlanID)
	}

	vlanName := VLANName(vlanID)
	cs := NewChangeSet(n.name, "device.unconfigure-irb")

	// Remove IP address entry (children before parents)
	if ip := intent.Params[sonic.FieldIPAddress]; ip != "" {
		cs.Delete("VLAN_INTERFACE", IRBIPKey(vlanID, ip))
	}

	// Remove base VLAN_INTERFACE entry
	cs.Delete("VLAN_INTERFACE", vlanName)

	// Remove SAG_GLOBAL if anycast MAC was set and no other IRB uses it
	if intent.Params[sonic.FieldAnycastMAC] != "" {
		// SAG_GLOBAL is shared — only remove if no other VLAN_INTERFACE references exist
		otherSVI := false
		if n.configDB != nil {
			for key := range n.configDB.VLANInterface {
				if key != vlanName && !strings.Contains(key, "|") {
					otherSVI = true
					break
				}
			}
		}
		if !otherSVI {
			cs.Delete("SAG_GLOBAL", "IPv4")
		}
	}

	if err := n.deleteIntent(cs, intentKey); err != nil {
		return nil, err
	}
	n.applyShadow(cs)
	util.WithDevice(n.name).Infof("Removed IRB for VLAN %d", vlanID)
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
	IRBStatus  string      // "up" if VLAN_INTERFACE exists, empty otherwise
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

	// Check for IRB (VLAN_INTERFACE)
	if _, ok := n.configDB.VLANInterface[vlanKey]; ok {
		info.IRBStatus = "up"
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
