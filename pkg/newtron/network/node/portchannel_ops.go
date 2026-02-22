package node

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
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
func (n *Node) CreatePortChannel(ctx context.Context, name string, opts PortChannelConfig) (*ChangeSet, error) {
	// Normalize PortChannel name (e.g., Po100 -> PortChannel100)
	name = util.NormalizeInterfaceName(name)

	if err := n.precondition("create-portchannel", name).
		RequirePortChannelNotExists(name).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.create-portchannel")

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
		if !n.InterfaceExists(member) {
			return nil, fmt.Errorf("member interface %s does not exist", member)
		}
		if n.InterfaceIsPortChannelMember(member) {
			return nil, fmt.Errorf("interface %s is already a PortChannel member", member)
		}
		memberKey := fmt.Sprintf("%s|%s", name, member)
		cs.Add("PORTCHANNEL_MEMBER", memberKey, ChangeAdd, nil, map[string]string{})
	}

	util.WithDevice(n.name).Infof("Created PortChannel %s with members %v", name, opts.Members)
	return cs, nil
}

// portChannelDeleteConfig returns delete entries for a PortChannel: its members and the PortChannel itself.
func portChannelDeleteConfig(configDB *sonic.ConfigDB, name string) []CompositeEntry {
	var entries []CompositeEntry

	// Remove members first
	if configDB != nil {
		for key := range configDB.PortChannelMember {
			parts := splitConfigDBKey(key)
			if len(parts) == 2 && parts[0] == name {
				entries = append(entries, CompositeEntry{Table: "PORTCHANNEL_MEMBER", Key: key})
			}
		}
	}

	entries = append(entries, CompositeEntry{Table: "PORTCHANNEL", Key: name})
	return entries
}

// DeletePortChannel removes a LAG/PortChannel.
func (n *Node) DeletePortChannel(ctx context.Context, name string) (*ChangeSet, error) {
	name = util.NormalizeInterfaceName(name)

	cs, err := n.op("delete-portchannel", name, ChangeDelete,
		func(pc *PreconditionChecker) { pc.RequirePortChannelExists(name) },
		func() []CompositeEntry { return portChannelDeleteConfig(n.configDB, name) })
	if err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Deleted PortChannel %s", name)
	return cs, nil
}

// AddPortChannelMember adds a member to a PortChannel.
func (n *Node) AddPortChannelMember(ctx context.Context, pcName, member string) (*ChangeSet, error) {
	pcName = util.NormalizeInterfaceName(pcName)
	member = util.NormalizeInterfaceName(member)

	cs, err := n.op("add-portchannel-member", pcName, ChangeAdd,
		func(pc *PreconditionChecker) {
			pc.RequirePortChannelExists(pcName).
				RequireInterfaceExists(member).
				RequireInterfaceNotPortChannelMember(member)
		},
		func() []CompositeEntry {
			return []CompositeEntry{{Table: "PORTCHANNEL_MEMBER", Key: fmt.Sprintf("%s|%s", pcName, member), Fields: map[string]string{}}}
		})
	if err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Added %s to PortChannel %s", member, pcName)
	return cs, nil
}

// RemovePortChannelMember removes a member from a PortChannel.
func (n *Node) RemovePortChannelMember(ctx context.Context, pcName, member string) (*ChangeSet, error) {
	pcName = util.NormalizeInterfaceName(pcName)
	member = util.NormalizeInterfaceName(member)

	cs, err := n.op("remove-portchannel-member", pcName, ChangeDelete,
		func(pc *PreconditionChecker) { pc.RequirePortChannelExists(pcName) },
		func() []CompositeEntry {
			return []CompositeEntry{{Table: "PORTCHANNEL_MEMBER", Key: fmt.Sprintf("%s|%s", pcName, member)}}
		})
	if err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Removed %s from PortChannel %s", member, pcName)
	return cs, nil
}

// ============================================================================
// PortChannel Data Types and Queries
// ============================================================================

// PortChannelInfo represents PortChannel data assembled from config_db.
type PortChannelInfo struct {
	Name          string
	Members       []string
	ActiveMembers []string
	AdminStatus   string
}

// PortChannelExists checks if a PortChannel exists.
// Accepts both short (Po100) and full (PortChannel100) names.
func (n *Node) PortChannelExists(name string) bool {
	return n.configDB.HasPortChannel(util.NormalizeInterfaceName(name))
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

// InterfaceIsPortChannelMember checks if an interface is a PortChannel member.
// Accepts both short (Eth0) and full (Ethernet0) interface names.
func (n *Node) InterfaceIsPortChannelMember(name string) bool {
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

// GetInterfacePortChannel returns the PortChannel that an interface belongs to (empty if not a member).
// Accepts both short (Eth0) and full (Ethernet0) interface names.
func (n *Node) GetInterfacePortChannel(name string) string {
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
