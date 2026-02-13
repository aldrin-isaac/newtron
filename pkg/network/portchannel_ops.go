package network

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// PortChannel Operations
// ============================================================================

// PortChannelConfig holds configuration options for CreatePortChannel.
type PortChannelConfig struct {
	Members  []string
	MTU      int
	MinLinks int
	Fallback bool
	FastRate bool
}

// CreatePortChannel creates a new LAG/PortChannel.
func (d *Device) CreatePortChannel(ctx context.Context, name string, opts PortChannelConfig) (*ChangeSet, error) {
	if err := requireWritable(d); err != nil {
		return nil, err
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
	if err := requireWritable(d); err != nil {
		return nil, err
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
	if err := requireWritable(d); err != nil {
		return nil, err
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
	if err := requireWritable(d); err != nil {
		return nil, err
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
