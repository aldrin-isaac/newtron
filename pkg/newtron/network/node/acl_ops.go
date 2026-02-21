package node

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/util"
)

// ProtoMap is the canonical mapping from protocol name to IP protocol number.
// BGP is intentionally absent: BGP uses TCP (protocol 6) on port 179.
// Filter rules for BGP should use protocol: "tcp" with dst_port: "179".
var ProtoMap = map[string]int{
	"tcp":  6,
	"udp":  17,
	"icmp": 1,
	"gre":  47,
	"ospf": 89,
	"vrrp": 112,
}

// ACLTableExists checks if an ACL table exists.
func (n *Node) ACLTableExists(name string) bool { return n.configDB.HasACLTable(name) }

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
// ACL Config Functions (pure, no Node state)
// ============================================================================

// aclTableConfig returns CompositeEntry for an ACL_TABLE.
func aclTableConfig(name, aclType, stage, ports, description string) []CompositeEntry {
	fields := map[string]string{
		"type":  aclType,
		"stage": stage,
	}
	if description != "" {
		fields["policy_desc"] = description
	}
	if ports != "" {
		fields["ports"] = ports
	}
	return []CompositeEntry{{Table: "ACL_TABLE", Key: name, Fields: fields}}
}

// aclRuleConfig returns CompositeEntry for an ACL_RULE.
func aclRuleConfig(tableName, ruleName string, opts ACLRuleConfig) []CompositeEntry {
	ruleKey := fmt.Sprintf("%s|%s", tableName, ruleName)

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
		if proto, ok := ProtoMap[opts.Protocol]; ok {
			fields["IP_PROTOCOL"] = fmt.Sprintf("%d", proto)
		} else {
			fields["IP_PROTOCOL"] = opts.Protocol
		}
	}
	if opts.DstPort != "" {
		fields["L4_DST_PORT"] = opts.DstPort
	}
	if opts.SrcPort != "" {
		fields["L4_SRC_PORT"] = opts.SrcPort
	}

	return []CompositeEntry{{Table: "ACL_RULE", Key: ruleKey, Fields: fields}}
}

// ============================================================================
// ACL Operations
// ============================================================================

// ACLTableConfig holds configuration options for CreateACLTable.
type ACLTableConfig struct {
	Type        string // L3, L3V6
	Stage       string // ingress, egress
	Description string
	Ports       string // Comma-separated interface names (maps to CONFIG_DB ACL_TABLE.ports)
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

// CreateACLTable creates a new ACL table.
func (n *Node) CreateACLTable(ctx context.Context, name string, opts ACLTableConfig) (*ChangeSet, error) {
	if err := n.precondition("create-acl-table", name).
		RequireACLTableNotExists(name).
		Result(); err != nil {
		return nil, err
	}
	if opts.Type == "" {
		opts.Type = "L3"
	}
	if opts.Stage == "" {
		opts.Stage = "ingress"
	}

	config := aclTableConfig(name, opts.Type, opts.Stage, opts.Ports, opts.Description)
	cs := configToChangeSet(n.name, "device.create-acl-table", config, ChangeAdd)

	util.WithDevice(n.name).Infof("Created ACL table %s", name)
	return cs, nil
}

// AddACLRule adds a rule to an ACL table.
func (n *Node) AddACLRule(ctx context.Context, tableName, ruleName string, opts ACLRuleConfig) (*ChangeSet, error) {
	if err := n.precondition("add-acl-rule", tableName).
		RequireACLTableExists(tableName).
		Result(); err != nil {
		return nil, err
	}

	config := aclRuleConfig(tableName, ruleName, opts)
	cs := configToChangeSet(n.name, "device.add-acl-rule", config, ChangeAdd)

	util.WithDevice(n.name).Infof("Added rule %s to ACL table %s", ruleName, tableName)
	return cs, nil
}

// DeleteACLRule removes a single rule from an ACL table.
func (n *Node) DeleteACLRule(ctx context.Context, tableName, ruleName string) (*ChangeSet, error) {
	if err := n.precondition("delete-acl-rule", tableName).
		RequireACLTableExists(tableName).
		Result(); err != nil {
		return nil, err
	}

	ruleKey := fmt.Sprintf("%s|%s", tableName, ruleName)

	// Verify rule exists
	if n.configDB != nil {
		if _, ok := n.configDB.ACLRule[ruleKey]; !ok {
			return nil, fmt.Errorf("rule %s not found in ACL table %s", ruleName, tableName)
		}
	}

	cs := NewChangeSet(n.name, "device.delete-acl-rule")
	cs.Add("ACL_RULE", ruleKey, ChangeDelete, nil, nil)

	util.WithDevice(n.name).Infof("Deleted rule %s from ACL table %s", ruleName, tableName)
	return cs, nil
}

// DeleteACLTable removes an ACL table and all its rules.
func (n *Node) DeleteACLTable(ctx context.Context, name string) (*ChangeSet, error) {
	if err := n.precondition("delete-acl-table", name).
		RequireACLTableExists(name).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.delete-acl-table")

	// Remove all rules first
	if n.configDB != nil {
		prefix := name + "|"
		for ruleKey := range n.configDB.ACLRule {
			if len(ruleKey) > len(prefix) && ruleKey[:len(prefix)] == prefix {
				cs.Add("ACL_RULE", ruleKey, ChangeDelete, nil, nil)
			}
		}
	}

	// Remove the table
	cs.Add("ACL_TABLE", name, ChangeDelete, nil, nil)

	util.WithDevice(n.name).Infof("Deleted ACL table %s", name)
	return cs, nil
}

// UnbindACLFromInterface removes an interface from an ACL table's binding.
func (n *Node) UnbindACLFromInterface(ctx context.Context, aclName, interfaceName string) (*ChangeSet, error) {
	// Normalize interface name (e.g., Eth0 -> Ethernet0)
	interfaceName = util.NormalizeInterfaceName(interfaceName)

	if err := n.precondition("unbind-acl", aclName).
		RequireACLTableExists(aclName).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.unbind-acl")

	// Get current binding list and remove the specified interface
	if n.configDB != nil {
		if table, ok := n.configDB.ACLTable[aclName]; ok {
			currentBindings := table.Ports
			var remaining []string
			for _, p := range util.SplitCommaSeparated(currentBindings) {
				if p != interfaceName {
					remaining = append(remaining, p)
				}
			}

			cs.Add("ACL_TABLE", aclName, ChangeModify, nil, map[string]string{
				"ports": strings.Join(remaining, ","),
			})
		}
	}

	util.WithDevice(n.name).Infof("Unbound ACL %s from interface %s", aclName, interfaceName)
	return cs, nil
}
