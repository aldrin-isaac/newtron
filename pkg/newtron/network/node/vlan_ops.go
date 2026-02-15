package node

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/util"
)

// vlanResource returns the canonical resource name for a VLAN.
func vlanResource(id int) string { return fmt.Sprintf("Vlan%d", id) }

// ============================================================================
// VLAN Operations
// ============================================================================

// VLANConfig holds configuration options for CreateVLAN.
type VLANConfig struct {
	Name        string // VLAN name (alias for Description)
	Description string
	L2VNI       int
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
	vlanName := fmt.Sprintf("Vlan%d", vlanID)

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
	vlanName := fmt.Sprintf("Vlan%d", vlanID)
	memberKey := fmt.Sprintf("%s|%s", vlanName, interfaceName)

	taggingMode := "untagged"
	if tagged {
		taggingMode = "tagged"
	}

	cs.Add("VLAN_MEMBER", memberKey, ChangeAdd, nil, map[string]string{
		"tagging_mode": taggingMode,
	})

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
	vlanName := fmt.Sprintf("Vlan%d", vlanID)
	memberKey := fmt.Sprintf("%s|%s", vlanName, interfaceName)

	cs.Add("VLAN_MEMBER", memberKey, ChangeDelete, nil, nil)

	util.WithDevice(n.name).Infof("Removed %s from VLAN %d", interfaceName, vlanID)
	return cs, nil
}
