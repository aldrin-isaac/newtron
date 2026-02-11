package network

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/device"
	"github.com/newtron-network/newtron/pkg/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// Device Operations - Methods on Device
// ============================================================================

// CreateVLAN creates a new VLAN on this device.
func (d *Device) CreateVLAN(ctx context.Context, vlanID int, opts VLANConfig) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if vlanID < 1 || vlanID > 4094 {
		return nil, fmt.Errorf("invalid VLAN ID: %d (must be 1-4094)", vlanID)
	}
	if d.VLANExists(vlanID) {
		return nil, fmt.Errorf("VLAN %d already exists", vlanID)
	}

	cs := NewChangeSet(d.name, "device.create-vlan")
	vlanName := fmt.Sprintf("Vlan%d", vlanID)

	fields := map[string]string{
		"vlanid": fmt.Sprintf("%d", vlanID),
	}
	if opts.Description != "" {
		fields["description"] = opts.Description
	}

	cs.Add("VLAN", vlanName, ChangeAdd, nil, fields)

	// Configure L2VNI if specified
	if opts.L2VNI > 0 {
		mapKey := fmt.Sprintf("vtep1|map_%d_%s", opts.L2VNI, vlanName)
		cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeAdd, nil, map[string]string{
			"vlan": vlanName,
			"vni":  fmt.Sprintf("%d", opts.L2VNI),
		})
	}

	util.WithDevice(d.name).Infof("Created VLAN %d", vlanID)
	return cs, nil
}

// DeleteVLAN removes a VLAN from this device.
func (d *Device) DeleteVLAN(ctx context.Context, vlanID int) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if !d.VLANExists(vlanID) {
		return nil, fmt.Errorf("VLAN %d does not exist", vlanID)
	}

	cs := NewChangeSet(d.name, "device.delete-vlan")
	vlanName := fmt.Sprintf("Vlan%d", vlanID)

	// Remove VLAN members first
	if d.configDB != nil {
		for key := range d.configDB.VLANMember {
			parts := splitConfigDBKey(key)
			if len(parts) == 2 && parts[0] == vlanName {
				cs.Add("VLAN_MEMBER", key, ChangeDelete, nil, nil)
			}
		}
	}

	// Remove VNI mapping if exists
	if d.configDB != nil {
		for key, mapping := range d.configDB.VXLANTunnelMap {
			if mapping.VLAN == vlanName {
				cs.Add("VXLAN_TUNNEL_MAP", key, ChangeDelete, nil, nil)
			}
		}
	}

	cs.Add("VLAN", vlanName, ChangeDelete, nil, nil)

	util.WithDevice(d.name).Infof("Deleted VLAN %d", vlanID)
	return cs, nil
}

// AddVLANMember adds a port to a VLAN.
func (d *Device) AddVLANMember(ctx context.Context, vlanID int, port string, tagged bool) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	// Normalize interface name (e.g., Eth0 -> Ethernet0)
	port = util.NormalizeInterfaceName(port)

	if !d.VLANExists(vlanID) {
		return nil, fmt.Errorf("VLAN %d does not exist", vlanID)
	}
	if !d.InterfaceExists(port) {
		return nil, fmt.Errorf("interface %s does not exist", port)
	}

	cs := NewChangeSet(d.name, "device.add-vlan-member")
	vlanName := fmt.Sprintf("Vlan%d", vlanID)
	memberKey := fmt.Sprintf("%s|%s", vlanName, port)

	taggingMode := "untagged"
	if tagged {
		taggingMode = "tagged"
	}

	cs.Add("VLAN_MEMBER", memberKey, ChangeAdd, nil, map[string]string{
		"tagging_mode": taggingMode,
	})

	util.WithDevice(d.name).Infof("Added %s to VLAN %d (%s)", port, vlanID, taggingMode)
	return cs, nil
}

// CreatePortChannel creates a new LAG/PortChannel.
func (d *Device) CreatePortChannel(ctx context.Context, name string, opts PortChannelConfig) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	// Normalize PortChannel name (e.g., Po100 -> PortChannel100)
	name = util.NormalizeInterfaceName(name)

	if d.PortChannelExists(name) {
		return nil, fmt.Errorf("PortChannel %s already exists", name)
	}

	cs := NewChangeSet(d.name, "device.create-portchannel")

	fields := map[string]string{
		"admin_status": "up",
	}
	if opts.MTU > 0 {
		fields["mtu"] = fmt.Sprintf("%d", opts.MTU)
	}
	if opts.MinLinks > 0 {
		fields["min_links"] = fmt.Sprintf("%d", opts.MinLinks)
	}
	if opts.Fallback {
		fields["fallback"] = "true"
	}
	if opts.FastRate {
		fields["fast_rate"] = "true"
	}

	cs.Add("PORTCHANNEL", name, ChangeAdd, nil, fields)

	// Add members
	for _, member := range opts.Members {
		if !d.InterfaceExists(member) {
			return nil, fmt.Errorf("member interface %s does not exist", member)
		}
		if d.InterfaceIsLAGMember(member) {
			return nil, fmt.Errorf("interface %s is already a LAG member", member)
		}
		memberKey := fmt.Sprintf("%s|%s", name, member)
		cs.Add("PORTCHANNEL_MEMBER", memberKey, ChangeAdd, nil, map[string]string{})
	}

	util.WithDevice(d.name).Infof("Created PortChannel %s with members %v", name, opts.Members)
	return cs, nil
}

// DeletePortChannel removes a LAG/PortChannel.
func (d *Device) DeletePortChannel(ctx context.Context, name string) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	// Normalize PortChannel name (e.g., Po100 -> PortChannel100)
	name = util.NormalizeInterfaceName(name)

	if !d.PortChannelExists(name) {
		return nil, fmt.Errorf("PortChannel %s does not exist", name)
	}

	cs := NewChangeSet(d.name, "device.delete-portchannel")

	// Remove members first
	if d.configDB != nil {
		for key := range d.configDB.PortChannelMember {
			parts := splitConfigDBKey(key)
			if len(parts) == 2 && parts[0] == name {
				cs.Add("PORTCHANNEL_MEMBER", key, ChangeDelete, nil, nil)
			}
		}
	}

	cs.Add("PORTCHANNEL", name, ChangeDelete, nil, nil)

	util.WithDevice(d.name).Infof("Deleted PortChannel %s", name)
	return cs, nil
}

// AddPortChannelMember adds a member to a PortChannel.
func (d *Device) AddPortChannelMember(ctx context.Context, pcName, member string) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	// Normalize interface names (e.g., Po100 -> PortChannel100, Eth0 -> Ethernet0)
	pcName = util.NormalizeInterfaceName(pcName)
	member = util.NormalizeInterfaceName(member)

	if !d.PortChannelExists(pcName) {
		return nil, fmt.Errorf("PortChannel %s does not exist", pcName)
	}
	if !d.InterfaceExists(member) {
		return nil, fmt.Errorf("interface %s does not exist", member)
	}
	if d.InterfaceIsLAGMember(member) {
		return nil, fmt.Errorf("interface %s is already a LAG member", member)
	}

	cs := NewChangeSet(d.name, "device.add-portchannel-member")
	memberKey := fmt.Sprintf("%s|%s", pcName, member)
	cs.Add("PORTCHANNEL_MEMBER", memberKey, ChangeAdd, nil, map[string]string{})

	util.WithDevice(d.name).Infof("Added %s to PortChannel %s", member, pcName)
	return cs, nil
}

// RemovePortChannelMember removes a member from a PortChannel.
func (d *Device) RemovePortChannelMember(ctx context.Context, pcName, member string) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	// Normalize interface names (e.g., Po100 -> PortChannel100, Eth0 -> Ethernet0)
	pcName = util.NormalizeInterfaceName(pcName)
	member = util.NormalizeInterfaceName(member)

	if !d.PortChannelExists(pcName) {
		return nil, fmt.Errorf("PortChannel %s does not exist", pcName)
	}

	cs := NewChangeSet(d.name, "device.remove-portchannel-member")
	memberKey := fmt.Sprintf("%s|%s", pcName, member)
	cs.Add("PORTCHANNEL_MEMBER", memberKey, ChangeDelete, nil, nil)

	util.WithDevice(d.name).Infof("Removed %s from PortChannel %s", member, pcName)
	return cs, nil
}

// CreateVRF creates a new VRF.
func (d *Device) CreateVRF(ctx context.Context, name string, opts VRFConfig) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if d.VRFExists(name) {
		return nil, fmt.Errorf("VRF %s already exists", name)
	}

	cs := NewChangeSet(d.name, "device.create-vrf")

	fields := map[string]string{}
	if opts.L3VNI > 0 {
		fields["vni"] = fmt.Sprintf("%d", opts.L3VNI)
	}

	cs.Add("VRF", name, ChangeAdd, nil, fields)

	// Add L3VNI mapping if specified
	if opts.L3VNI > 0 {
		mapKey := fmt.Sprintf("vtep1|map_%d_%s", opts.L3VNI, name)
		cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeAdd, nil, map[string]string{
			"vrf": name,
			"vni": fmt.Sprintf("%d", opts.L3VNI),
		})
	}

	util.WithDevice(d.name).Infof("Created VRF %s", name)
	return cs, nil
}

// DeleteVRF removes a VRF.
func (d *Device) DeleteVRF(ctx context.Context, name string) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if !d.VRFExists(name) {
		return nil, fmt.Errorf("VRF %s does not exist", name)
	}

	// Check no interfaces are bound to this VRF
	vrfInfo, _ := d.GetVRF(name)
	if vrfInfo != nil && len(vrfInfo.Interfaces) > 0 {
		return nil, fmt.Errorf("VRF %s has interfaces bound: %v", name, vrfInfo.Interfaces)
	}

	cs := NewChangeSet(d.name, "device.delete-vrf")

	// Remove VNI mapping if exists
	if d.configDB != nil {
		for key, mapping := range d.configDB.VXLANTunnelMap {
			if mapping.VRF == name {
				cs.Add("VXLAN_TUNNEL_MAP", key, ChangeDelete, nil, nil)
			}
		}
	}

	cs.Add("VRF", name, ChangeDelete, nil, nil)

	util.WithDevice(d.name).Infof("Deleted VRF %s", name)
	return cs, nil
}

// CreateACLTable creates a new ACL table.
func (d *Device) CreateACLTable(ctx context.Context, name string, opts ACLTableConfig) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if d.ACLTableExists(name) {
		return nil, fmt.Errorf("ACL table %s already exists", name)
	}
	if opts.Type == "" {
		opts.Type = "L3"
	}
	if opts.Stage == "" {
		opts.Stage = "ingress"
	}

	cs := NewChangeSet(d.name, "device.create-acl-table")

	fields := map[string]string{
		"type":  opts.Type,
		"stage": opts.Stage,
	}
	if opts.Description != "" {
		fields["policy_desc"] = opts.Description
	}
	if opts.Ports != "" {
		fields["ports"] = opts.Ports
	}

	cs.Add("ACL_TABLE", name, ChangeAdd, nil, fields)

	util.WithDevice(d.name).Infof("Created ACL table %s", name)
	return cs, nil
}

// AddACLRule adds a rule to an ACL table.
func (d *Device) AddACLRule(ctx context.Context, tableName, ruleName string, opts ACLRuleConfig) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if !d.ACLTableExists(tableName) {
		return nil, fmt.Errorf("ACL table %s does not exist", tableName)
	}

	cs := NewChangeSet(d.name, "device.add-acl-rule")

	ruleKey := fmt.Sprintf("%s|%s", tableName, ruleName)

	// Map action
	action := "DROP"
	if opts.Action == "permit" || opts.Action == "FORWARD" {
		action = "FORWARD"
	}

	fields := map[string]string{
		"PRIORITY":      fmt.Sprintf("%d", opts.Priority),
		"PACKET_ACTION": action,
	}
	if opts.SrcIP != "" {
		fields["SRC_IP"] = opts.SrcIP
	}
	if opts.DstIP != "" {
		fields["DST_IP"] = opts.DstIP
	}
	if opts.Protocol != "" {
		// Map protocol name to number
		protoMap := map[string]int{
			"tcp": 6, "udp": 17, "icmp": 1, "ospf": 89, "vrrp": 112, "gre": 47,
		}
		if proto, ok := protoMap[opts.Protocol]; ok {
			fields["IP_PROTOCOL"] = fmt.Sprintf("%d", proto)
		} else {
			// Assume it's already a number
			fields["IP_PROTOCOL"] = opts.Protocol
		}
	}
	if opts.DstPort != "" {
		fields["L4_DST_PORT"] = opts.DstPort
	}
	if opts.SrcPort != "" {
		fields["L4_SRC_PORT"] = opts.SrcPort
	}

	cs.Add("ACL_RULE", ruleKey, ChangeAdd, nil, fields)

	util.WithDevice(d.name).Infof("Added rule %s to ACL table %s", ruleName, tableName)
	return cs, nil
}

// DeleteACLRule removes a single rule from an ACL table.
func (d *Device) DeleteACLRule(ctx context.Context, tableName, ruleName string) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if !d.ACLTableExists(tableName) {
		return nil, fmt.Errorf("ACL table %s does not exist", tableName)
	}

	ruleKey := fmt.Sprintf("%s|%s", tableName, ruleName)

	// Verify rule exists
	if d.configDB != nil {
		if _, ok := d.configDB.ACLRule[ruleKey]; !ok {
			return nil, fmt.Errorf("rule %s not found in ACL table %s", ruleName, tableName)
		}
	}

	cs := NewChangeSet(d.name, "device.delete-acl-rule")
	cs.Add("ACL_RULE", ruleKey, ChangeDelete, nil, nil)

	util.WithDevice(d.name).Infof("Deleted rule %s from ACL table %s", ruleName, tableName)
	return cs, nil
}

// DeleteACLTable removes an ACL table and all its rules.
func (d *Device) DeleteACLTable(ctx context.Context, name string) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if !d.ACLTableExists(name) {
		return nil, fmt.Errorf("ACL table %s does not exist", name)
	}

	cs := NewChangeSet(d.name, "device.delete-acl-table")

	// Remove all rules first
	if d.configDB != nil {
		prefix := name + "|"
		for ruleKey := range d.configDB.ACLRule {
			if len(ruleKey) > len(prefix) && ruleKey[:len(prefix)] == prefix {
				cs.Add("ACL_RULE", ruleKey, ChangeDelete, nil, nil)
			}
		}
	}

	// Remove the table
	cs.Add("ACL_TABLE", name, ChangeDelete, nil, nil)

	util.WithDevice(d.name).Infof("Deleted ACL table %s", name)
	return cs, nil
}

// UnbindACLFromPort removes a port from an ACL table's binding.
func (d *Device) UnbindACLFromPort(ctx context.Context, aclName, portName string) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	// Normalize interface name (e.g., Eth0 -> Ethernet0)
	portName = util.NormalizeInterfaceName(portName)

	if !d.ACLTableExists(aclName) {
		return nil, fmt.Errorf("ACL table %s does not exist", aclName)
	}

	cs := NewChangeSet(d.name, "device.unbind-acl")

	// Get current ports and remove the specified one
	if d.configDB != nil {
		if table, ok := d.configDB.ACLTable[aclName]; ok {
			currentPorts := table.Ports
			// Parse and filter out the port
			var newPorts []string
			for _, p := range splitPorts(currentPorts) {
				if p != portName {
					newPorts = append(newPorts, p)
				}
			}

			cs.Add("ACL_TABLE", aclName, ChangeModify, nil, map[string]string{
				"ports": joinPorts(newPorts),
			})
		}
	}

	util.WithDevice(d.name).Infof("Unbound ACL %s from port %s", aclName, portName)
	return cs, nil
}

// ============================================================================
// EVPN Operations
// ============================================================================

// CreateVTEP creates a VXLAN Tunnel Endpoint.
func (d *Device) CreateVTEP(ctx context.Context, opts VTEPConfig) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if d.VTEPExists() {
		return nil, fmt.Errorf("VTEP already configured")
	}
	if opts.SourceIP == "" {
		return nil, fmt.Errorf("source IP is required")
	}

	cs := NewChangeSet(d.name, "device.create-vtep")

	// Create VXLAN tunnel
	cs.Add("VXLAN_TUNNEL", "vtep1", ChangeAdd, nil, map[string]string{
		"src_ip": opts.SourceIP,
	})

	// Create EVPN NVO
	cs.Add("VXLAN_EVPN_NVO", "nvo1", ChangeAdd, nil, map[string]string{
		"source_vtep": "vtep1",
	})

	util.WithDevice(d.name).Infof("Created VTEP with source IP %s", opts.SourceIP)
	return cs, nil
}

// DeleteVTEP removes the VXLAN Tunnel Endpoint.
func (d *Device) DeleteVTEP(ctx context.Context) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if !d.VTEPExists() {
		return nil, fmt.Errorf("VTEP not configured")
	}

	// Check for existing VNI mappings
	if d.configDB != nil && len(d.configDB.VXLANTunnelMap) > 0 {
		return nil, fmt.Errorf("cannot delete VTEP with existing VNI mappings")
	}

	cs := NewChangeSet(d.name, "device.delete-vtep")

	// Remove NVO first
	for name := range d.configDB.VXLANEVPNNVO {
		cs.Add("VXLAN_EVPN_NVO", name, ChangeDelete, nil, nil)
	}

	// Remove tunnel
	for name := range d.configDB.VXLANTunnel {
		cs.Add("VXLAN_TUNNEL", name, ChangeDelete, nil, nil)
	}

	util.WithDevice(d.name).Info("Deleted VTEP")
	return cs, nil
}

// MapL2VNI maps a VLAN to an L2VNI for EVPN.
func (d *Device) MapL2VNI(ctx context.Context, vlanID, vni int) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if !d.VTEPExists() {
		return nil, fmt.Errorf("VTEP must be configured first")
	}
	if !d.VLANExists(vlanID) {
		return nil, fmt.Errorf("VLAN %d does not exist", vlanID)
	}

	cs := NewChangeSet(d.name, "device.map-l2vni")

	vlanName := fmt.Sprintf("Vlan%d", vlanID)
	mapKey := fmt.Sprintf("vtep1|map_%d_%s", vni, vlanName)

	cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeAdd, nil, map[string]string{
		"vlan": vlanName,
		"vni":  fmt.Sprintf("%d", vni),
	})

	util.WithDevice(d.name).Infof("Mapped VLAN %d to L2VNI %d", vlanID, vni)
	return cs, nil
}

// MapL3VNI maps a VRF to an L3VNI for EVPN.
func (d *Device) MapL3VNI(ctx context.Context, vrfName string, vni int) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if !d.VTEPExists() {
		return nil, fmt.Errorf("VTEP must be configured first")
	}
	if !d.VRFExists(vrfName) {
		return nil, fmt.Errorf("VRF %s does not exist", vrfName)
	}

	cs := NewChangeSet(d.name, "device.map-l3vni")

	// Update VRF with VNI
	cs.Add("VRF", vrfName, ChangeModify, nil, map[string]string{
		"vni": fmt.Sprintf("%d", vni),
	})

	// Add VXLAN tunnel map
	mapKey := fmt.Sprintf("vtep1|map_%d_%s", vni, vrfName)
	cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeAdd, nil, map[string]string{
		"vrf": vrfName,
		"vni": fmt.Sprintf("%d", vni),
	})

	util.WithDevice(d.name).Infof("Mapped VRF %s to L3VNI %d", vrfName, vni)
	return cs, nil
}

// UnmapVNI removes a VNI mapping.
func (d *Device) UnmapVNI(ctx context.Context, vni int) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	cs := NewChangeSet(d.name, "device.unmap-vni")

	// Find and remove the mapping
	if d.configDB != nil {
		for key, mapping := range d.configDB.VXLANTunnelMap {
			if mapping.VNI == fmt.Sprintf("%d", vni) {
				cs.Add("VXLAN_TUNNEL_MAP", key, ChangeDelete, nil, nil)
				break
			}
		}
	}

	if cs.IsEmpty() {
		return nil, fmt.Errorf("VNI %d mapping not found", vni)
	}

	util.WithDevice(d.name).Infof("Unmapped VNI %d", vni)
	return cs, nil
}

// UnmapL2VNI removes the L2VNI mapping for a VLAN.
func (d *Device) UnmapL2VNI(ctx context.Context, vlanID int) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if !d.VLANExists(vlanID) {
		return nil, fmt.Errorf("VLAN %d does not exist", vlanID)
	}

	vlanName := fmt.Sprintf("Vlan%d", vlanID)
	cs := NewChangeSet(d.name, "device.unmap-l2vni")

	// Find the tunnel map entry for this VLAN
	if d.configDB != nil {
		for key, mapping := range d.configDB.VXLANTunnelMap {
			if mapping.VLAN == vlanName {
				cs.Add("VXLAN_TUNNEL_MAP", key, ChangeDelete, nil, nil)
				break
			}
		}
	}

	if cs.IsEmpty() {
		return nil, fmt.Errorf("no L2VNI mapping found for VLAN %d", vlanID)
	}

	util.WithDevice(d.name).Infof("Unmapped L2VNI for VLAN %d", vlanID)
	return cs, nil
}

// ConfigureSVI configures a VLAN's SVI (Layer 3 interface).
// This creates VLAN_INTERFACE entries for VRF binding and IP assignment,
// and optionally sets up SAG (Static Anycast Gateway) for anycast MAC.
func (d *Device) ConfigureSVI(ctx context.Context, vlanID int, opts SVIConfig) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if !d.VLANExists(vlanID) {
		return nil, fmt.Errorf("VLAN %d does not exist", vlanID)
	}
	if opts.VRF != "" && !d.VRFExists(opts.VRF) {
		return nil, fmt.Errorf("VRF %s does not exist", opts.VRF)
	}

	vlanName := fmt.Sprintf("Vlan%d", vlanID)
	cs := NewChangeSet(d.name, "device.configure-svi")

	// VLAN_INTERFACE entry with optional VRF binding
	fields := map[string]string{}
	if opts.VRF != "" {
		fields["vrf_name"] = opts.VRF
	}
	cs.Add("VLAN_INTERFACE", vlanName, ChangeAdd, nil, fields)

	// IP address binding
	if opts.IPAddress != "" {
		ipKey := fmt.Sprintf("%s|%s", vlanName, opts.IPAddress)
		cs.Add("VLAN_INTERFACE", ipKey, ChangeAdd, nil, map[string]string{})
	}

	// Anycast gateway MAC (SAG)
	if opts.AnycastMAC != "" {
		cs.Add("SAG_GLOBAL", "IPv4", ChangeAdd, nil, map[string]string{
			"gwmac": opts.AnycastMAC,
		})
	}

	util.WithDevice(d.name).Infof("Configured SVI for VLAN %d", vlanID)
	return cs, nil
}

// ============================================================================
// Configuration Types
// ============================================================================

// VLANConfig holds configuration options for CreateVLAN.
type VLANConfig struct {
	Name        string // VLAN name (alias for Description)
	Description string
	L2VNI       int
}

// VTEPConfig holds configuration options for CreateVTEP.
type VTEPConfig struct {
	SourceIP string // VTEP source IP (typically loopback)
	UDPPort  int    // UDP port (default 4789)
}

// PortChannelConfig holds configuration options for CreatePortChannel.
type PortChannelConfig struct {
	Members  []string
	MTU      int
	MinLinks int
	Fallback bool
	FastRate bool
}

// VRFConfig holds configuration options for CreateVRF.
type VRFConfig struct {
	L3VNI    int
	ImportRT []string
	ExportRT []string
}

// ACLTableConfig holds configuration options for CreateACLTable.
type ACLTableConfig struct {
	Type        string // L3, L3V6
	Stage       string // ingress, egress
	Description string
	Ports       string // Comma-separated list or single port
}

// SVIConfig holds configuration options for ConfigureSVI.
type SVIConfig struct {
	VRF        string // VRF to bind the SVI to
	IPAddress  string // IP address with prefix (e.g., "10.1.100.1/24")
	AnycastMAC string // SAG anycast gateway MAC (e.g., "00:00:00:00:01:01")
}

// ACLRuleConfig holds configuration options for AddACLRule.
type ACLRuleConfig struct {
	Priority int
	Action   string // permit, deny (or FORWARD, DROP)
	SrcIP    string
	DstIP    string
	Protocol string // tcp, udp, icmp, or number
	SrcPort  string
	DstPort  string
}

// ============================================================================
// Helpers
// ============================================================================

func joinPorts(ports []string) string {
	result := ""
	for i, p := range ports {
		if i > 0 {
			result += ","
		}
		result += p
	}
	return result
}

func splitPorts(ports string) []string {
	if ports == "" {
		return nil
	}
	var result []string
	current := ""
	for _, c := range ports {
		if c == ',' {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else if c != ' ' {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

// ============================================================================
// Health Checks
// ============================================================================

// HealthCheckResult represents the result of a single health check.
type HealthCheckResult struct {
	Check   string `json:"check"`   // Check name (e.g., "bgp", "interfaces")
	Status  string `json:"status"`  // "pass", "warn", "fail"
	Message string `json:"message"` // Human-readable message
}

// RunHealthChecks runs health checks on the device.
// If checkType is empty, all checks are run.
//
// Starts a fresh read-only episode by calling Refresh() to ensure health
// checks (checkBGP, checkInterfaces, etc.) read current CONFIG_DB state.
func (d *Device) RunHealthChecks(ctx context.Context, checkType string) ([]HealthCheckResult, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}

	// Start a fresh read-only episode
	if err := d.Refresh(); err != nil {
		return nil, fmt.Errorf("refresh: %w", err)
	}

	var results []HealthCheckResult

	// Run checks based on type
	if checkType == "" || checkType == "bgp" {
		results = append(results, d.checkBGP()...)
	}
	if checkType == "" || checkType == "interfaces" {
		results = append(results, d.checkInterfaces()...)
	}
	if checkType == "" || checkType == "evpn" {
		results = append(results, d.checkEVPN()...)
	}
	if checkType == "" || checkType == "lag" {
		results = append(results, d.checkLAG()...)
	}

	return results, nil
}

func (d *Device) checkBGP() []HealthCheckResult {
	var results []HealthCheckResult

	if d.configDB == nil {
		return []HealthCheckResult{{Check: "bgp", Status: "fail", Message: "Config not loaded"}}
	}

	if len(d.configDB.BGPNeighbor) == 0 {
		return []HealthCheckResult{{Check: "bgp", Status: "warn", Message: "No BGP neighbors configured"}}
	}

	stateClient := d.conn.StateClient()
	for key := range d.configDB.BGPNeighbor {
		// Key format: "vrf|ip" (e.g., "default|10.1.0.1")
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 {
			results = append(results, HealthCheckResult{
				Check:   "bgp",
				Status:  "fail",
				Message: fmt.Sprintf("Malformed BGP neighbor key: %s", key),
			})
			continue
		}
		vrf, neighbor := parts[0], parts[1]

		entry, err := stateClient.GetBGPNeighborState(vrf, neighbor)
		if err != nil {
			results = append(results, HealthCheckResult{
				Check:   "bgp",
				Status:  "fail",
				Message: fmt.Sprintf("BGP neighbor %s (vrf %s): not found in STATE_DB", neighbor, vrf),
			})
			continue
		}

		if entry.State == "Established" {
			results = append(results, HealthCheckResult{
				Check:   "bgp",
				Status:  "pass",
				Message: fmt.Sprintf("BGP neighbor %s (vrf %s): Established", neighbor, vrf),
			})
		} else {
			results = append(results, HealthCheckResult{
				Check:   "bgp",
				Status:  "fail",
				Message: fmt.Sprintf("BGP neighbor %s (vrf %s): %s", neighbor, vrf, entry.State),
			})
		}
	}

	return results
}

func (d *Device) checkInterfaces() []HealthCheckResult {
	var results []HealthCheckResult

	if d.configDB == nil {
		return []HealthCheckResult{{Check: "interfaces", Status: "fail", Message: "Config not loaded"}}
	}

	total := len(d.configDB.Port)
	adminDown := 0
	for _, port := range d.configDB.Port {
		if port.AdminStatus == "down" {
			adminDown++
		}
	}

	if adminDown > 0 {
		results = append(results, HealthCheckResult{
			Check:   "interfaces",
			Status:  "warn",
			Message: fmt.Sprintf("%d of %d interfaces admin down", adminDown, total),
		})
	} else {
		results = append(results, HealthCheckResult{
			Check:   "interfaces",
			Status:  "pass",
			Message: fmt.Sprintf("All %d interfaces admin up", total),
		})
	}

	return results
}

func (d *Device) checkEVPN() []HealthCheckResult {
	var results []HealthCheckResult

	if d.configDB == nil {
		return []HealthCheckResult{{Check: "evpn", Status: "fail", Message: "Config not loaded"}}
	}

	if !d.VTEPExists() {
		results = append(results, HealthCheckResult{
			Check:   "evpn",
			Status:  "warn",
			Message: "No VTEP configured",
		})
	} else {
		vniCount := len(d.configDB.VXLANTunnelMap)
		results = append(results, HealthCheckResult{
			Check:   "evpn",
			Status:  "pass",
			Message: fmt.Sprintf("VTEP configured with %d VNI mappings", vniCount),
		})
	}

	return results
}

func (d *Device) checkLAG() []HealthCheckResult {
	var results []HealthCheckResult

	if d.configDB == nil {
		return []HealthCheckResult{{Check: "lag", Status: "fail", Message: "Config not loaded"}}
	}

	lagCount := len(d.configDB.PortChannel)
	if lagCount == 0 {
		results = append(results, HealthCheckResult{
			Check:   "lag",
			Status:  "pass",
			Message: "No LAGs configured",
		})
	} else {
		// Count members
		memberCount := len(d.configDB.PortChannelMember)
		results = append(results, HealthCheckResult{
			Check:   "lag",
			Status:  "pass",
			Message: fmt.Sprintf("%d LAGs configured with %d total members", lagCount, memberCount),
		})
	}

	return results
}

// ============================================================================
// Baseline Configuration
// ============================================================================

// ApplyBaseline applies a baseline configlet to the device.
func (d *Device) ApplyBaseline(ctx context.Context, configletName string, vars []string) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	// Parse vars into a map
	varMap := make(map[string]string)
	for _, v := range vars {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) == 2 {
			varMap[parts[0]] = parts[1]
		}
	}

	// Add default variables from resolved config
	if d.resolved != nil {
		if _, ok := varMap["loopback_ip"]; !ok {
			varMap["loopback_ip"] = d.resolved.LoopbackIP
		}
		if _, ok := varMap["device_name"]; !ok {
			varMap["device_name"] = d.name
		}
	}

	cs := NewChangeSet(d.name, "device.apply-baseline")

	// Load configlet based on name (simplified - in production would load from file)
	switch configletName {
	case "sonic-baseline":
		// Basic SONiC baseline
		cs.Add("DEVICE_METADATA", "localhost", ChangeModify, nil, map[string]string{
			"hostname": varMap["device_name"],
		})
		if loopbackIP, ok := varMap["loopback_ip"]; ok && loopbackIP != "" {
			cs.Add("LOOPBACK_INTERFACE", fmt.Sprintf("Loopback0|%s/32", loopbackIP), ChangeAdd, nil, map[string]string{})
		}

	case "sonic-evpn":
		// EVPN baseline - create VTEP
		if loopbackIP, ok := varMap["loopback_ip"]; ok && loopbackIP != "" {
			cs.Add("VXLAN_TUNNEL", "vtep1", ChangeAdd, nil, map[string]string{
				"src_ip": loopbackIP,
			})
			cs.Add("VXLAN_EVPN_NVO", "nvo1", ChangeAdd, nil, map[string]string{
				"source_vtep": "vtep1",
			})
		}

	default:
		return nil, fmt.Errorf("unknown configlet: %s", configletName)
	}

	util.WithDevice(d.name).Infof("Applied baseline configlet '%s'", configletName)
	return cs, nil
}

// ============================================================================
// Cleanup (Orphaned Resource Removal)
// ============================================================================

// CleanupSummary provides details about orphaned resources found.
type CleanupSummary struct {
	OrphanedACLs        []string
	OrphanedVRFs        []string
	OrphanedVNIMappings []string
}

// Cleanup identifies and removes orphaned configurations.
// Returns a changeset to remove them and a summary of what was found.
func (d *Device) Cleanup(ctx context.Context, cleanupType string) (*ChangeSet, *CleanupSummary, error) {
	if !d.IsConnected() {
		return nil, nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, nil, fmt.Errorf("device not locked")
	}

	cs := NewChangeSet(d.name, "device.cleanup")
	summary := &CleanupSummary{}

	configDB := d.ConfigDB()
	if configDB == nil {
		return cs, summary, nil
	}

	// Find orphaned ACLs (no ports bound)
	if cleanupType == "" || cleanupType == "acl" {
		for aclName, acl := range configDB.ACLTable {
			if acl.Ports == "" {
				summary.OrphanedACLs = append(summary.OrphanedACLs, aclName)

				// Delete rules first
				prefix := aclName + "|"
				for ruleKey := range configDB.ACLRule {
					if strings.HasPrefix(ruleKey, prefix) {
						cs.Add("ACL_RULE", ruleKey, ChangeDelete, nil, nil)
					}
				}
				cs.Add("ACL_TABLE", aclName, ChangeDelete, nil, nil)
			}
		}
	}

	// Find orphaned VRFs (no interfaces bound)
	if cleanupType == "" || cleanupType == "vrf" {
		for vrfName := range configDB.VRF {
			if vrfName == "default" {
				continue
			}
			hasUsers := false
			for intfName, intf := range configDB.Interface {
				if strings.Contains(intfName, "|") {
					continue
				}
				if intf.VRFName == vrfName {
					hasUsers = true
					break
				}
			}
			if !hasUsers {
				summary.OrphanedVRFs = append(summary.OrphanedVRFs, vrfName)
				cs.Add("VRF", vrfName, ChangeDelete, nil, nil)
			}
		}
	}

	// Find orphaned VNI mappings (VRF or VLAN doesn't exist)
	if cleanupType == "" || cleanupType == "vni" {
		for mapKey, mapping := range configDB.VXLANTunnelMap {
			orphaned := false
			if mapping.VRF != "" {
				if _, ok := configDB.VRF[mapping.VRF]; !ok {
					orphaned = true
				}
			}
			if mapping.VLAN != "" {
				if _, ok := configDB.VLAN[mapping.VLAN]; !ok {
					orphaned = true
				}
			}
			if orphaned {
				summary.OrphanedVNIMappings = append(summary.OrphanedVNIMappings, mapKey)
				cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeDelete, nil, nil)
			}
		}
	}

	return cs, summary, nil
}

// ============================================================================
// v3: BGP Management Operations (frrcfgd)
// ============================================================================

// BGPGlobalsConfig holds configuration for SetBGPGlobals.
type BGPGlobalsConfig struct {
	VRF                string // VRF name ("default" for global)
	LocalASN           int    // Local AS number
	RouterID           string // Router ID (typically loopback IP)
	LoadBalanceMPRelax bool   // Enable multipath relax for ECMP
	RRClusterID        string // Route reflector cluster ID
	EBGPRequiresPolicy bool   // Require policy for eBGP (FRR 8.x default)
	DefaultIPv4Unicast bool   // Auto-activate IPv4 unicast
	LogNeighborChanges bool   // Log neighbor state changes
	SuppressFIBPending bool   // Suppress routes until FIB confirmed
}

// SetBGPGlobals configures BGP global settings via CONFIG_DB (frrcfgd).
func (d *Device) SetBGPGlobals(ctx context.Context, cfg BGPGlobalsConfig) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	cs := NewChangeSet(d.Name(), "device.set-bgp-globals")

	vrf := cfg.VRF
	if vrf == "" {
		vrf = "default"
	}

	fields := map[string]string{
		"local_asn": fmt.Sprintf("%d", cfg.LocalASN),
		"router_id": cfg.RouterID,
	}

	if cfg.LoadBalanceMPRelax {
		fields["load_balance_mp_relax"] = "true"
	}
	if cfg.RRClusterID != "" {
		fields["rr_cluster_id"] = cfg.RRClusterID
	}
	if !cfg.EBGPRequiresPolicy {
		fields["ebgp_requires_policy"] = "false"
	}
	if !cfg.DefaultIPv4Unicast {
		fields["default_ipv4_unicast"] = "false"
	}
	if cfg.LogNeighborChanges {
		fields["log_neighbor_changes"] = "true"
	}
	if cfg.SuppressFIBPending {
		fields["suppress_fib_pending"] = "true"
	}

	cs.Add("BGP_GLOBALS", vrf, ChangeAdd, nil, fields)

	util.WithDevice(d.Name()).Infof("Set BGP globals for VRF %s (ASN %d)", vrf, cfg.LocalASN)
	return cs, nil
}

// SetupRouteReflectorConfig holds configuration for SetupRouteReflector.
type SetupRouteReflectorConfig struct {
	Neighbors    []string // Neighbor loopback IPs
	ClusterID    string   // RR cluster ID (defaults to local loopback)
	MaxIBGPPaths int      // Max iBGP ECMP paths (default 2)
}

// SetupRouteReflector performs full route reflector setup with all 3 AFs
// (ipv4_unicast, ipv6_unicast, l2vpn_evpn). Replaces the v2 SetupBGPEVPN
// with comprehensive multi-AF route reflection.
func (d *Device) SetupRouteReflector(ctx context.Context, cfg SetupRouteReflectorConfig) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	resolved := d.Resolved()
	if resolved == nil {
		return nil, fmt.Errorf("device has no resolved profile")
	}

	cs := NewChangeSet(d.Name(), "device.setup-route-reflector")

	// Determine cluster ID
	clusterID := cfg.ClusterID
	if clusterID == "" {
		clusterID = resolved.LoopbackIP // Default to spine's loopback
	}

	// BGP_GLOBALS "default"
	cs.Add("BGP_GLOBALS", "default", ChangeAdd, nil, map[string]string{
		"local_asn":              fmt.Sprintf("%d", resolved.ASNumber),
		"router_id":             resolved.RouterID,
		"rr_cluster_id":         clusterID,
		"load_balance_mp_relax": "true",
		"ebgp_requires_policy":  "false",
		"log_neighbor_changes":  "true",
	})

	// Configure each neighbor with all 3 AFs
	// Key format: vrf|neighborIP (per SONiC Unified FRR Mgmt schema)
	for _, neighborIP := range cfg.Neighbors {
		// BGP_NEIGHBOR
		cs.Add("BGP_NEIGHBOR", fmt.Sprintf("default|%s", neighborIP), ChangeAdd, nil, map[string]string{
			"asn":          fmt.Sprintf("%d", resolved.ASNumber),
			"local_addr":   resolved.LoopbackIP,
			"admin_status": "up",
		})

		// IPv4 unicast
		cs.Add("BGP_NEIGHBOR_AF", fmt.Sprintf("default|%s|ipv4_unicast", neighborIP), ChangeAdd, nil, map[string]string{
			"activate":               "true",
			"route_reflector_client": "true",
			"next_hop_self":          "true",
		})

		// IPv6 unicast
		cs.Add("BGP_NEIGHBOR_AF", fmt.Sprintf("default|%s|ipv6_unicast", neighborIP), ChangeAdd, nil, map[string]string{
			"activate":               "true",
			"route_reflector_client": "true",
			"next_hop_self":          "true",
		})

		// L2VPN EVPN
		cs.Add("BGP_NEIGHBOR_AF", fmt.Sprintf("default|%s|l2vpn_evpn", neighborIP), ChangeAdd, nil, map[string]string{
			"activate":               "true",
			"route_reflector_client": "true",
		})
	}

	// BGP_GLOBALS_AF for all 3 AFs
	maxPaths := "2"
	if cfg.MaxIBGPPaths > 0 {
		maxPaths = fmt.Sprintf("%d", cfg.MaxIBGPPaths)
	}

	cs.Add("BGP_GLOBALS_AF", "default|ipv4_unicast", ChangeAdd, nil, map[string]string{
		"max_ibgp_paths": maxPaths,
	})
	cs.Add("BGP_GLOBALS_AF", "default|ipv6_unicast", ChangeAdd, nil, map[string]string{
		"max_ibgp_paths": maxPaths,
	})
	cs.Add("BGP_GLOBALS_AF", "default|l2vpn_evpn", ChangeAdd, nil, map[string]string{
		"advertise-all-vni": "true",
	})

	// Route redistribution for connected (loopback + service subnets)
	// Key format: vrf|src_protocol|dst_protocol|addr_family (per SONiC Unified FRR Mgmt HLD)
	cs.Add("ROUTE_REDISTRIBUTE", "default|connected|bgp|ipv4", ChangeAdd, nil, map[string]string{})
	cs.Add("ROUTE_REDISTRIBUTE", "default|connected|bgp|ipv6", ChangeAdd, nil, map[string]string{})

	util.WithDevice(d.Name()).Infof("Setup route reflector with %d neighbors, cluster-id %s",
		len(cfg.Neighbors), clusterID)
	return cs, nil
}

// PeerGroupConfig holds configuration for ConfigurePeerGroup.
type PeerGroupConfig struct {
	Name        string
	ASN         int
	LocalAddr   string
	HoldTime    int
	Keepalive   int
	Password    string
	AdminStatus string
}

// ConfigurePeerGroup creates or updates a BGP peer group template.
func (d *Device) ConfigurePeerGroup(ctx context.Context, cfg PeerGroupConfig) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	cs := NewChangeSet(d.Name(), "device.configure-peer-group")

	fields := map[string]string{}
	if cfg.ASN > 0 {
		fields["asn"] = fmt.Sprintf("%d", cfg.ASN)
	}
	if cfg.LocalAddr != "" {
		fields["local_addr"] = cfg.LocalAddr
	}
	if cfg.HoldTime > 0 {
		fields["holdtime"] = fmt.Sprintf("%d", cfg.HoldTime)
	}
	if cfg.Keepalive > 0 {
		fields["keepalive"] = fmt.Sprintf("%d", cfg.Keepalive)
	}
	if cfg.Password != "" {
		fields["password"] = cfg.Password
	}
	adminStatus := cfg.AdminStatus
	if adminStatus == "" {
		adminStatus = "up"
	}
	fields["admin_status"] = adminStatus

	cs.Add("BGP_PEER_GROUP", cfg.Name, ChangeAdd, nil, fields)

	util.WithDevice(d.Name()).Infof("Configured peer group %s", cfg.Name)
	return cs, nil
}

// DeletePeerGroup removes a BGP peer group.
func (d *Device) DeletePeerGroup(ctx context.Context, name string) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	cs := NewChangeSet(d.Name(), "device.delete-peer-group")

	// Delete AF entries first
	configDB := d.ConfigDB()
	if configDB != nil {
		prefix := name + "|"
		for key := range configDB.BGPPeerGroupAF {
			if strings.HasPrefix(key, prefix) {
				cs.Add("BGP_PEER_GROUP_AF", key, ChangeDelete, nil, nil)
			}
		}
	}

	cs.Add("BGP_PEER_GROUP", name, ChangeDelete, nil, nil)

	util.WithDevice(d.Name()).Infof("Deleted peer group %s", name)
	return cs, nil
}

// RouteRedistributionConfig holds configuration for AddRouteRedistribution.
type RouteRedistributionConfig struct {
	VRF           string // VRF name ("default" for global)
	SrcProtocol   string // Source protocol (e.g., "connected", "static")
	AddressFamily string // "ipv4" or "ipv6"
	RouteMap      string // Optional route-map reference
	Metric        string // Optional metric
}

// AddRouteRedistribution configures route redistribution into BGP.
func (d *Device) AddRouteRedistribution(ctx context.Context, cfg RouteRedistributionConfig) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	cs := NewChangeSet(d.Name(), "device.add-route-redistribution")

	vrf := cfg.VRF
	if vrf == "" {
		vrf = "default"
	}

	// Key format: vrf|src_protocol|dst_protocol|addr_family (per SONiC Unified FRR Mgmt HLD)
	key := fmt.Sprintf("%s|%s|bgp|%s", vrf, cfg.SrcProtocol, cfg.AddressFamily)
	fields := map[string]string{}
	if cfg.RouteMap != "" {
		fields["route_map"] = cfg.RouteMap
	}
	if cfg.Metric != "" {
		fields["metric"] = cfg.Metric
	}

	cs.Add("ROUTE_REDISTRIBUTE", key, ChangeAdd, nil, fields)

	util.WithDevice(d.Name()).Infof("Added route redistribution %s %s in VRF %s",
		cfg.SrcProtocol, cfg.AddressFamily, vrf)
	return cs, nil
}

// RemoveRouteRedistribution removes a route redistribution entry.
func (d *Device) RemoveRouteRedistribution(ctx context.Context, vrf, srcProtocol, af string) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	cs := NewChangeSet(d.Name(), "device.remove-route-redistribution")

	if vrf == "" {
		vrf = "default"
	}
	// Key format: vrf|src_protocol|dst_protocol|addr_family (per SONiC Unified FRR Mgmt HLD)
	key := fmt.Sprintf("%s|%s|bgp|%s", vrf, srcProtocol, af)
	cs.Add("ROUTE_REDISTRIBUTE", key, ChangeDelete, nil, nil)

	util.WithDevice(d.Name()).Infof("Removed route redistribution %s %s in VRF %s", srcProtocol, af, vrf)
	return cs, nil
}

// RouteMapConfig holds configuration for AddRouteMap.
type RouteMapConfig struct {
	Name           string
	Sequence       int
	Action         string // "permit" or "deny"
	MatchPrefixSet string // Reference to PREFIX_SET
	MatchCommunity string // Reference to COMMUNITY_SET
	MatchASPath    string // Reference to AS_PATH_SET
	SetLocalPref   int
	SetCommunity   string
	SetMED         int
}

// AddRouteMap creates a route-map with match/set rules.
func (d *Device) AddRouteMap(ctx context.Context, cfg RouteMapConfig) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	cs := NewChangeSet(d.Name(), "device.add-route-map")

	key := fmt.Sprintf("%s|%d", cfg.Name, cfg.Sequence)
	fields := map[string]string{
		"route_operation": cfg.Action,
	}
	if cfg.MatchPrefixSet != "" {
		fields["match_prefix_set"] = cfg.MatchPrefixSet
	}
	if cfg.MatchCommunity != "" {
		fields["match_community"] = cfg.MatchCommunity
	}
	if cfg.MatchASPath != "" {
		fields["match_as_path"] = cfg.MatchASPath
	}
	if cfg.SetLocalPref > 0 {
		fields["set_local_pref"] = fmt.Sprintf("%d", cfg.SetLocalPref)
	}
	if cfg.SetCommunity != "" {
		fields["set_community"] = cfg.SetCommunity
	}
	if cfg.SetMED > 0 {
		fields["set_med"] = fmt.Sprintf("%d", cfg.SetMED)
	}

	cs.Add("ROUTE_MAP", key, ChangeAdd, nil, fields)

	util.WithDevice(d.Name()).Infof("Added route-map %s seq %d", cfg.Name, cfg.Sequence)
	return cs, nil
}

// DeleteRouteMap removes a route-map.
func (d *Device) DeleteRouteMap(ctx context.Context, name string) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	cs := NewChangeSet(d.Name(), "device.delete-route-map")

	configDB := d.ConfigDB()
	if configDB != nil {
		prefix := name + "|"
		for key := range configDB.RouteMap {
			if strings.HasPrefix(key, prefix) {
				cs.Add("ROUTE_MAP", key, ChangeDelete, nil, nil)
			}
		}
	}

	util.WithDevice(d.Name()).Infof("Deleted route-map %s", name)
	return cs, nil
}

// PrefixSetConfig holds configuration for AddPrefixSet.
type PrefixSetConfig struct {
	Name         string
	Sequence     int
	IPPrefix     string // e.g., "10.0.0.0/8"
	Action       string // "permit" or "deny"
	MaskLenRange string // e.g., "24..32"
}

// AddPrefixSet creates a prefix list for route-map matching.
func (d *Device) AddPrefixSet(ctx context.Context, cfg PrefixSetConfig) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	cs := NewChangeSet(d.Name(), "device.add-prefix-set")

	key := fmt.Sprintf("%s|%d", cfg.Name, cfg.Sequence)
	fields := map[string]string{
		"ip_prefix": cfg.IPPrefix,
		"action":    cfg.Action,
	}
	if cfg.MaskLenRange != "" {
		fields["masklength_range"] = cfg.MaskLenRange
	}

	cs.Add("PREFIX_SET", key, ChangeAdd, nil, fields)

	util.WithDevice(d.Name()).Infof("Added prefix-set %s seq %d", cfg.Name, cfg.Sequence)
	return cs, nil
}

// DeletePrefixSet removes a prefix list.
func (d *Device) DeletePrefixSet(ctx context.Context, name string) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	cs := NewChangeSet(d.Name(), "device.delete-prefix-set")

	configDB := d.ConfigDB()
	if configDB != nil {
		prefix := name + "|"
		for key := range configDB.PrefixSet {
			if strings.HasPrefix(key, prefix) {
				cs.Add("PREFIX_SET", key, ChangeDelete, nil, nil)
			}
		}
	}

	util.WithDevice(d.Name()).Infof("Deleted prefix-set %s", name)
	return cs, nil
}

// AddBGPNetwork adds a BGP network statement.
func (d *Device) AddBGPNetwork(ctx context.Context, vrf, af, prefix string, policy string) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	cs := NewChangeSet(d.Name(), "device.add-bgp-network")

	if vrf == "" {
		vrf = "default"
	}
	key := fmt.Sprintf("%s|%s|%s", vrf, af, prefix)
	fields := map[string]string{}
	if policy != "" {
		fields["policy"] = policy
	}

	cs.Add("BGP_GLOBALS_AF_NETWORK", key, ChangeAdd, nil, fields)

	util.WithDevice(d.Name()).Infof("Added BGP network %s in %s/%s", prefix, vrf, af)
	return cs, nil
}

// RemoveBGPNetwork removes a BGP network statement.
func (d *Device) RemoveBGPNetwork(ctx context.Context, vrf, af, prefix string) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	cs := NewChangeSet(d.Name(), "device.remove-bgp-network")

	if vrf == "" {
		vrf = "default"
	}
	key := fmt.Sprintf("%s|%s|%s", vrf, af, prefix)
	cs.Add("BGP_GLOBALS_AF_NETWORK", key, ChangeDelete, nil, nil)

	util.WithDevice(d.Name()).Infof("Removed BGP network %s from %s/%s", prefix, vrf, af)
	return cs, nil
}

// ============================================================================
// v3: Port Creation Operations
// ============================================================================

// CreatePort creates a PORT entry validated against the device's platform.json.
func (d *Device) CreatePort(ctx context.Context, cfg device.CreatePortConfig) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	underlying := d.Underlying()

	// Validate against platform.json if loaded
	if underlying.PlatformConfig != nil {
		if err := underlying.PlatformConfig.ValidatePort(cfg); err != nil {
			return nil, fmt.Errorf("port validation failed: %w", err)
		}

		// Check for conflicting ports (shared lanes)
		conflicts := underlying.PlatformConfig.HasConflictingPorts(cfg.Name, d.ConfigDB().Port)
		if len(conflicts) > 0 {
			return nil, fmt.Errorf("port %s conflicts with existing ports: %s (shared lanes)",
				cfg.Name, strings.Join(conflicts, ", "))
		}
	}

	// Check port doesn't already exist
	if _, ok := d.ConfigDB().Port[cfg.Name]; ok {
		return nil, fmt.Errorf("port %s already exists", cfg.Name)
	}

	cs := NewChangeSet(d.Name(), "device.create-port")

	fields := map[string]string{
		"admin_status": "up",
	}
	if cfg.AdminStatus != "" {
		fields["admin_status"] = cfg.AdminStatus
	}
	if cfg.Speed != "" {
		fields["speed"] = cfg.Speed
	}
	if cfg.Lanes != "" {
		fields["lanes"] = cfg.Lanes
	} else if underlying.PlatformConfig != nil {
		// Use lanes from platform.json
		if portDef, ok := underlying.PlatformConfig.Interfaces[cfg.Name]; ok {
			fields["lanes"] = portDef.Lanes
		}
	}
	if cfg.FEC != "" {
		fields["fec"] = cfg.FEC
	}
	if cfg.MTU > 0 {
		fields["mtu"] = fmt.Sprintf("%d", cfg.MTU)
	} else {
		fields["mtu"] = "9100" // SONiC default
	}
	if cfg.Alias != "" {
		fields["alias"] = cfg.Alias
	}
	if cfg.Index != "" {
		fields["index"] = cfg.Index
	}

	cs.Add("PORT", cfg.Name, ChangeAdd, nil, fields)

	util.WithDevice(d.Name()).Infof("Created port %s", cfg.Name)
	return cs, nil
}

// DeletePort removes a PORT entry.
func (d *Device) DeletePort(ctx context.Context, name string) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	if _, ok := d.ConfigDB().Port[name]; !ok {
		return nil, fmt.Errorf("port %s does not exist", name)
	}

	// Check no services bound
	if binding, ok := d.ConfigDB().NewtronServiceBinding[name]; ok {
		return nil, fmt.Errorf("port %s has service '%s' bound — remove it first", name, binding.ServiceName)
	}

	cs := NewChangeSet(d.Name(), "device.delete-port")
	cs.Add("PORT", name, ChangeDelete, nil, nil)

	util.WithDevice(d.Name()).Infof("Deleted port %s", name)
	return cs, nil
}

// BreakoutPort applies a breakout mode to a port, creating child ports and removing the parent.
func (d *Device) BreakoutPort(ctx context.Context, cfg device.BreakoutConfig) (*ChangeSet, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	underlying := d.Underlying()
	if underlying.PlatformConfig == nil {
		return nil, fmt.Errorf("platform config not loaded — call LoadPlatformConfig first")
	}

	// Validate breakout mode
	if err := underlying.PlatformConfig.ValidateBreakout(cfg); err != nil {
		return nil, fmt.Errorf("breakout validation failed: %w", err)
	}

	// Get child ports
	childPorts, err := underlying.PlatformConfig.GetChildPorts(cfg.ParentPort, cfg.Mode)
	if err != nil {
		return nil, fmt.Errorf("cannot determine child ports: %w", err)
	}

	// Parent port must not have services
	if binding, ok := d.ConfigDB().NewtronServiceBinding[cfg.ParentPort]; ok {
		return nil, fmt.Errorf("parent port %s has service '%s' bound — remove it first",
			cfg.ParentPort, binding.ServiceName)
	}

	cs := NewChangeSet(d.Name(), "device.breakout-port")

	// Delete parent port
	cs.Add("PORT", cfg.ParentPort, ChangeDelete, nil, nil)

	// Parse breakout mode for child speed (e.g., "4x25G" -> "25000")
	childSpeed := parseBreakoutSpeed(cfg.Mode)

	// Get parent port definition for lane distribution
	parentDef := underlying.PlatformConfig.Interfaces[cfg.ParentPort]
	parentLanes := strings.Split(parentDef.Lanes, ",")
	lanesPerChild := len(parentLanes) / len(childPorts)

	// Create child ports
	for i, childName := range childPorts {
		startLane := i * lanesPerChild
		endLane := startLane + lanesPerChild
		if endLane > len(parentLanes) {
			endLane = len(parentLanes)
		}
		childLanes := strings.Join(parentLanes[startLane:endLane], ",")

		cs.Add("PORT", childName, ChangeAdd, nil, map[string]string{
			"admin_status": "up",
			"speed":        childSpeed,
			"lanes":        childLanes,
			"mtu":          "9100",
			"index":        fmt.Sprintf("%d", i),
		})
	}

	util.WithDevice(d.Name()).Infof("Breakout port %s into %s (%d child ports)",
		cfg.ParentPort, cfg.Mode, len(childPorts))
	return cs, nil
}

// LoadPlatformConfig fetches and caches platform.json from the device via SSH.
func (d *Device) LoadPlatformConfig(ctx context.Context) error {
	if !d.IsConnected() {
		return fmt.Errorf("device not connected")
	}

	underlying := d.Underlying()

	// Get platform identifier from DEVICE_METADATA
	configDB := d.ConfigDB()
	if configDB == nil {
		return fmt.Errorf("config_db not loaded")
	}

	meta, ok := configDB.DeviceMetadata["localhost"]
	if !ok {
		return fmt.Errorf("DEVICE_METADATA|localhost not found")
	}
	platform := meta["platform"]
	if platform == "" {
		return fmt.Errorf("platform field not set in DEVICE_METADATA")
	}

	// Read platform.json via SSH
	path := fmt.Sprintf("/usr/share/sonic/device/%s/platform.json", platform)
	data, err := d.readFileViaSSH(ctx, path)
	if err != nil {
		return fmt.Errorf("reading platform.json: %w", err)
	}

	config, err := device.ParsePlatformJSON(data)
	if err != nil {
		return err
	}

	underlying.PlatformConfig = config
	util.WithDevice(d.Name()).Infof("Loaded platform config: %d interfaces", len(config.Interfaces))
	return nil
}

// GeneratePlatformSpec creates a spec.PlatformSpec from the device's platform.json.
// Used to prime the spec system on first connect to a new hardware platform.
func (d *Device) GeneratePlatformSpec(ctx context.Context) (*spec.PlatformSpec, error) {
	underlying := d.Underlying()
	if underlying.PlatformConfig == nil {
		return nil, fmt.Errorf("platform config not loaded — call LoadPlatformConfig first")
	}

	configDB := d.ConfigDB()
	hwsku := ""
	if meta, ok := configDB.DeviceMetadata["localhost"]; ok {
		hwsku = meta["hwsku"]
	}

	return underlying.PlatformConfig.GeneratePlatformSpec(hwsku), nil
}

// readFileViaSSH reads a file from the device via SSH tunnel.
// This is a placeholder — the actual implementation uses the device's SSH tunnel.
func (d *Device) readFileViaSSH(ctx context.Context, path string) ([]byte, error) {
	// In production, this would execute "cat <path>" over the SSH tunnel.
	// For now, return an error indicating SSH file read is not yet implemented.
	return nil, fmt.Errorf("SSH file read not yet implemented for path: %s", path)
}

// parseBreakoutSpeed converts a breakout mode speed suffix to SONiC speed value.
// e.g., "4x25G" -> "25000", "2x50G" -> "50000"
func parseBreakoutSpeed(mode string) string {
	parts := strings.SplitN(mode, "x", 2)
	if len(parts) != 2 {
		return ""
	}
	speedStr := strings.TrimRight(parts[1], "Gg")
	speedMap := map[string]string{
		"10":  "10000",
		"25":  "25000",
		"40":  "40000",
		"50":  "50000",
		"100": "100000",
		"200": "200000",
		"400": "400000",
	}
	if speed, ok := speedMap[speedStr]; ok {
		return speed
	}
	return ""
}
