package network

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/util"
)

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
func (d *Device) CreateVLAN(ctx context.Context, vlanID int, opts VLANConfig) (*ChangeSet, error) {
	if err := requireWritable(d); err != nil {
		return nil, err
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
	if err := requireWritable(d); err != nil {
		return nil, err
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

// AddVLANMember adds an interface to a VLAN as a tagged or untagged member.
func (d *Device) AddVLANMember(ctx context.Context, vlanID int, interfaceName string, tagged bool) (*ChangeSet, error) {
	if err := requireWritable(d); err != nil {
		return nil, err
	}

	// Normalize interface name (e.g., Eth0 -> Ethernet0)
	interfaceName = util.NormalizeInterfaceName(interfaceName)

	if !d.VLANExists(vlanID) {
		return nil, fmt.Errorf("VLAN %d does not exist", vlanID)
	}
	if !d.InterfaceExists(interfaceName) {
		return nil, fmt.Errorf("interface %s does not exist", interfaceName)
	}

	cs := NewChangeSet(d.name, "device.add-vlan-member")
	vlanName := fmt.Sprintf("Vlan%d", vlanID)
	memberKey := fmt.Sprintf("%s|%s", vlanName, interfaceName)

	taggingMode := "untagged"
	if tagged {
		taggingMode = "tagged"
	}

	cs.Add("VLAN_MEMBER", memberKey, ChangeAdd, nil, map[string]string{
		"tagging_mode": taggingMode,
	})

	util.WithDevice(d.name).Infof("Added %s to VLAN %d (%s)", interfaceName, vlanID, taggingMode)
	return cs, nil
}

// RemoveVLANMember removes an interface from a VLAN.
func (d *Device) RemoveVLANMember(ctx context.Context, vlanID int, interfaceName string) (*ChangeSet, error) {
	if err := requireWritable(d); err != nil {
		return nil, err
	}

	interfaceName = util.NormalizeInterfaceName(interfaceName)

	if !d.VLANExists(vlanID) {
		return nil, fmt.Errorf("VLAN %d does not exist", vlanID)
	}

	cs := NewChangeSet(d.name, "device.remove-vlan-member")
	vlanName := fmt.Sprintf("Vlan%d", vlanID)
	memberKey := fmt.Sprintf("%s|%s", vlanName, interfaceName)

	cs.Add("VLAN_MEMBER", memberKey, ChangeDelete, nil, nil)

	util.WithDevice(d.name).Infof("Removed %s from VLAN %d", interfaceName, vlanID)
	return cs, nil
}
