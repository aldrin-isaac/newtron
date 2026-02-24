package node

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
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

// aclTable returns sonic.Entry for an ACL_TABLE.
func createAclTableConfig(name, aclType, stage, ports, description string) []sonic.Entry {
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
	return []sonic.Entry{{Table: "ACL_TABLE", Key: name, Fields: fields}}
}

// createAclRuleConfig returns sonic.Entry for an ACL_RULE.
func createAclRuleConfig(tableName, ruleName string, opts ACLRuleConfig) []sonic.Entry {
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

	return []sonic.Entry{{Table: "ACL_RULE", Key: ruleKey, Fields: fields}}
}

// buildAclRuleFields returns the ACL_RULE field map for a filter rule spec.
// Takes explicit srcIP/dstIP to support prefix-list expansion (Cartesian product)
// in the service_ops path.
func buildAclRuleFields(rule *spec.FilterRule, srcIP, dstIP string) map[string]string {
	fields := map[string]string{
		"PRIORITY": fmt.Sprintf("%d", 10000-rule.Sequence),
	}

	if rule.Action == "permit" {
		fields["PACKET_ACTION"] = "FORWARD"
	} else {
		fields["PACKET_ACTION"] = "DROP"
	}

	if srcIP != "" {
		fields["SRC_IP"] = srcIP
	}
	if dstIP != "" {
		fields["DST_IP"] = dstIP
	}
	if rule.Protocol != "" {
		if proto, ok := ProtoMap[rule.Protocol]; ok {
			fields["IP_PROTOCOL"] = fmt.Sprintf("%d", proto)
		} else {
			fields["IP_PROTOCOL"] = rule.Protocol
		}
	}
	if rule.DstPort != "" {
		fields["L4_DST_PORT"] = rule.DstPort
	}
	if rule.SrcPort != "" {
		fields["L4_SRC_PORT"] = rule.SrcPort
	}
	if rule.DSCP != "" {
		fields["DSCP"] = rule.DSCP
	}

	// CoS/TC marking for QoS-aware ACL rules
	if rule.CoS != "" {
		cosToTC := map[string]string{
			"be": "0", "cs1": "1", "cs2": "2", "cs3": "3",
			"cs4": "4", "ef": "5", "cs6": "6", "cs7": "7",
		}
		if tc, ok := cosToTC[rule.CoS]; ok {
			fields["TC"] = tc
		}
	}

	return fields
}

// createAclRuleFromFilterConfig returns an ACL_RULE entry built from a filter rule spec.
// The suffix parameter supports prefix-list expansion (e.g., "_0", "_1") — pass "" for single rules.
func createAclRuleFromFilterConfig(aclName string, rule *spec.FilterRule, srcIP, dstIP, suffix string) sonic.Entry {
	ruleKey := fmt.Sprintf("%s|RULE_%d%s", aclName, rule.Sequence, suffix)
	return sonic.Entry{
		Table:  "ACL_RULE",
		Key:    ruleKey,
		Fields: buildAclRuleFields(rule, srcIP, dstIP),
	}
}

// bindAclConfig returns the ACL_TABLE entry for binding interfaces with a stage direction.
func bindAclConfig(aclName, ports, stage string) sonic.Entry {
	return sonic.Entry{Table: "ACL_TABLE", Key: aclName, Fields: map[string]string{
		"ports": ports,
		"stage": stage,
	}}
}

// unbindAclConfig returns the ACL_TABLE entry for updating the ports binding list.
func unbindAclConfig(aclName, ports string) sonic.Entry {
	return sonic.Entry{Table: "ACL_TABLE", Key: aclName, Fields: map[string]string{
		"ports": ports,
	}}
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
	if opts.Type == "" {
		opts.Type = "L3"
	}
	if opts.Stage == "" {
		opts.Stage = "ingress"
	}
	cs, err := n.op("create-acl-table", name, ChangeAdd,
		func(pc *PreconditionChecker) { pc.RequireACLTableNotExists(name) },
		func() []sonic.Entry { return createAclTableConfig(name, opts.Type, opts.Stage, opts.Ports, opts.Description) })
	if err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Created ACL table %s", name)
	return cs, nil
}

// AddACLRule adds a rule to an ACL table.
func (n *Node) AddACLRule(ctx context.Context, tableName, ruleName string, opts ACLRuleConfig) (*ChangeSet, error) {
	cs, err := n.op("add-acl-rule", tableName, ChangeAdd,
		func(pc *PreconditionChecker) { pc.RequireACLTableExists(tableName) },
		func() []sonic.Entry { return createAclRuleConfig(tableName, ruleName, opts) })
	if err != nil {
		return nil, err
	}
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
	cs.Delete("ACL_RULE", ruleKey)

	util.WithDevice(n.name).Infof("Deleted rule %s from ACL table %s", ruleName, tableName)
	return cs, nil
}

// deleteAclTable returns delete entries for an ACL table: all its rules and the table itself.
func (n *Node) deleteAclTableConfig(name string) []sonic.Entry {
	var entries []sonic.Entry

	// Remove all rules first
	if n.configDB != nil {
		prefix := name + "|"
		for ruleKey := range n.configDB.ACLRule {
			if len(ruleKey) > len(prefix) && ruleKey[:len(prefix)] == prefix {
				entries = append(entries, sonic.Entry{Table: "ACL_RULE", Key: ruleKey})
			}
		}
	}

	// Remove the table
	entries = append(entries, sonic.Entry{Table: "ACL_TABLE", Key: name})
	return entries
}

// DeleteACLTable removes an ACL table and all its rules.
func (n *Node) DeleteACLTable(ctx context.Context, name string) (*ChangeSet, error) {
	cs, err := n.op("delete-acl-table", name, ChangeDelete,
		func(pc *PreconditionChecker) { pc.RequireACLTableExists(name) },
		func() []sonic.Entry { return n.deleteAclTableConfig(name) })
	if err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Deleted ACL table %s", name)
	return cs, nil
}

// UnbindACLFromInterface removes an interface from an ACL table's binding.
// Node convenience method — delegates to Interface.UnbindACL.
func (n *Node) UnbindACLFromInterface(ctx context.Context, aclName, interfaceName string) (*ChangeSet, error) {
	interfaceName = util.NormalizeInterfaceName(interfaceName)
	iface, err := n.GetInterface(interfaceName)
	if err != nil {
		return nil, err
	}
	return iface.UnbindACL(ctx, aclName)
}
