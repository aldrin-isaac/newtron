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

// mapFilterType translates spec filter types to SONiC ACL_TABLE type values.
func mapFilterType(specType string) string {
	switch specType {
	case "ipv6":
		return "L3V6"
	default:
		return "L3"
	}
}

// ACLTableExists checks if an ACL table exists.
func (n *Node) ACLTableExists(name string) bool { return n.configDB.HasACLTable(name) }

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

// updateAclPorts returns the ACL_TABLE entry for updating the ports binding list.
// Used in both bind (adding interface) and unbind (removing interface) paths.
func updateAclPorts(aclName, ports string) sonic.Entry {
	return sonic.Entry{Table: "ACL_TABLE", Key: aclName, Fields: map[string]string{
		"ports": ports,
	}}
}

// computeFilterHash computes the content hash for a filter spec by hashing
// the ACL_RULE field maps that would be written to CONFIG_DB.
// Per DESIGN_PRINCIPLES_NEWTRON.md §16 (Content-Hashed Naming): hash the generated fields, not the spec.
func computeFilterHash(filterSpec *spec.FilterSpec) string {
	var fieldMaps []map[string]string
	for _, rule := range filterSpec.Rules {
		fieldMaps = append(fieldMaps, buildAclRuleFields(rule, rule.SrcIP, rule.DstIP))
	}
	return util.ContentHash(fieldMaps)
}

// ============================================================================
// ACL Operations
// ============================================================================

// ACLConfig holds configuration options for CreateACL.
type ACLConfig struct {
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

// CreateACL creates a new ACL table.
func (n *Node) CreateACL(ctx context.Context, name string, opts ACLConfig) (*ChangeSet, error) {
	// Intent-idempotent: if the ACL intent already exists, returns empty ChangeSet.
	if n.GetIntent("acl|"+name) != nil {
		return NewChangeSet(n.name, "device."+sonic.OpCreateACL), nil
	}

	if opts.Type == "" {
		opts.Type = "L3"
	}
	if opts.Stage == "" {
		opts.Stage = "ingress"
	}
	cs, err := n.op(sonic.OpCreateACL, name, ChangeAdd,
		func(pc *PreconditionChecker) { pc.RequireACLTableNotExists(name) },
		func() []sonic.Entry { return createAclTableConfig(name, opts.Type, opts.Stage, opts.Ports, opts.Description) },
		"device.delete-acl")
	if err != nil {
		return nil, err
	}
	intentParams := map[string]string{
		sonic.FieldName:    name,
		sonic.FieldACLType: opts.Type,
		sonic.FieldStage:   opts.Stage,
	}
	if opts.Ports != "" {
		intentParams[sonic.FieldPorts] = opts.Ports
	}
	if opts.Description != "" {
		intentParams[sonic.FieldDescription] = opts.Description
	}
	if err := n.writeIntent(cs, sonic.OpCreateACL, "acl|"+name, intentParams, []string{"device"}); err != nil {
		return nil, err
	}
	cs.OperationParams = map[string]string{"name": name}
	util.WithDevice(n.name).Infof("Created ACL table %s", name)
	return cs, nil
}

// AddACLRule adds a rule to an ACL table.
func (n *Node) AddACLRule(ctx context.Context, tableName, ruleName string, opts ACLRuleConfig) (*ChangeSet, error) {
	cs, err := n.op("add-acl-rule", tableName, ChangeAdd,
		func(pc *PreconditionChecker) { pc.RequireACLTableExists(tableName) },
		func() []sonic.Entry { return createAclRuleConfig(tableName, ruleName, opts) },
		"device.delete-acl-rule")
	if err != nil {
		return nil, err
	}
	cs.OperationParams = map[string]string{"table_name": tableName, "rule_name": ruleName}

	if err := n.writeIntent(cs, sonic.OpAddACLRule, "acl|"+tableName+"|"+ruleName,
		map[string]string{sonic.FieldName: ruleName},
		[]string{"acl|" + tableName}); err != nil {
		return nil, err
	}

	util.WithDevice(n.name).Infof("Added rule %s to ACL table %s", ruleName, tableName)
	return cs, nil
}

// DeleteACLRule removes a single rule from an ACL table.
func (n *Node) DeleteACLRule(ctx context.Context, tableName, ruleName string) (*ChangeSet, error) {
	ruleKey := fmt.Sprintf("%s|%s", tableName, ruleName)

	// Verify rule exists before op
	if n.configDB != nil {
		if _, ok := n.configDB.ACLRule[ruleKey]; !ok {
			return nil, fmt.Errorf("rule %s not found in ACL table %s", ruleName, tableName)
		}
	}

	cs, err := n.op("delete-acl-rule", tableName, ChangeDelete,
		func(pc *PreconditionChecker) { pc.RequireACLTableExists(tableName) },
		func() []sonic.Entry { return []sonic.Entry{{Table: "ACL_RULE", Key: ruleKey}} })
	if err != nil {
		return nil, err
	}
	cs.OperationParams = map[string]string{"table_name": tableName, "rule_name": ruleName}

	if err := n.deleteIntent(cs, "acl|"+tableName+"|"+ruleName); err != nil {
		return nil, err
	}

	util.WithDevice(n.name).Infof("Deleted rule %s from ACL table %s", ruleName, tableName)
	return cs, nil
}

// deleteAclTableConfig returns delete entries for an ACL table.
// Under the DAG, rules are removed as children before the table can be deleted.
func (n *Node) deleteAclTableConfig(name string) []sonic.Entry {
	return []sonic.Entry{{Table: "ACL_TABLE", Key: name}}
}

// DeleteACL removes an ACL table and all its rules.
func (n *Node) DeleteACL(ctx context.Context, name string) (*ChangeSet, error) {
	cs, err := n.op("delete-acl", name, ChangeDelete,
		func(pc *PreconditionChecker) { pc.RequireACLTableExists(name) },
		func() []sonic.Entry { return n.deleteAclTableConfig(name) })
	if err != nil {
		return nil, err
	}
	if err := n.deleteIntent(cs, "acl|"+name); err != nil {
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
