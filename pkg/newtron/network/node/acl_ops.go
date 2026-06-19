package node

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// aclPortsFromIntents collects all interface names bound to an ACL by scanning
// intent records. Checks both standalone ACL binding intents (interface|X|acl|DIR)
// and service intents (interface|X with ingress_acl/egress_acl params).
// Returns the ports as a comma-separated string.
func (n *Node) aclPortsFromIntents(aclName, direction string) string {
	var ports []string
	aclField := direction + "_acl" // "ingress_acl" or "egress_acl"
	for resource, intent := range n.IntentsByPrefix("interface|") {
		// Standalone ACL binding intents: "interface|Ethernet0|acl|ingress"
		if strings.HasSuffix(resource, "|acl|"+direction) {
			if intent.Params[sonic.FieldACLName] == aclName {
				parts := strings.SplitN(resource, "|", 3)
				if len(parts) >= 2 {
					ports = append(ports, parts[1])
				}
			}
			continue
		}
		// Service intents with ACL: "interface|Ethernet0" (OpApplyService with ingress_acl/egress_acl)
		if intent.Operation == sonic.OpApplyService && intent.Params[aclField] == aclName {
			parts := strings.SplitN(resource, "|", 2)
			if len(parts) == 2 {
				ports = append(ports, parts[1])
			}
		}
	}
	sort.Strings(ports)
	return strings.Join(ports, ",")
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

	intentParams := map[string]string{
		sonic.FieldName: ruleName,
		"acl":           tableName,
	}
	if opts.Priority > 0 {
		intentParams["priority"] = strconv.Itoa(opts.Priority)
	}
	if opts.Action != "" {
		intentParams["action"] = opts.Action
	}
	if opts.SrcIP != "" {
		intentParams["src_ip"] = opts.SrcIP
	}
	if opts.DstIP != "" {
		intentParams["dst_ip"] = opts.DstIP
	}
	if opts.Protocol != "" {
		intentParams["protocol"] = opts.Protocol
	}
	if opts.SrcPort != "" {
		intentParams["src_port"] = opts.SrcPort
	}
	if opts.DstPort != "" {
		intentParams["dst_port"] = opts.DstPort
	}
	if err := n.writeIntent(cs, sonic.OpAddACLRule, "acl|"+tableName+"|"+ruleName,
		intentParams,
		[]string{"acl|" + tableName}); err != nil {
		return nil, err
	}

	util.WithDevice(n.name).Infof("Added rule %s to ACL table %s", ruleName, tableName)
	return cs, nil
}

// UpdateACLRule atomically mutates an existing ACL rule under the per-device
// intent lock — eliminates the read-modify-write window that AddACLRule +
// DeleteACLRule + AddACLRule exposes today (packet leak during the rebuild
// window, plus rule ordering renumbers required the remove+add dance).
//
// Reads the existing intent record by ruleName, validates that the new
// values don't collide with siblings (only on priority change — rule_name
// stays as the dictionary key unless newRuleName is supplied), and emits
// a single ChangeSet that deletes the prior ACL_RULE entry and writes the
// new one. The intent record is replaced via writeIntent's idempotent path
// (DEL+HSET — #228) so dropped params don't ghost.
//
// When newRuleName is supplied and differs from ruleName, the operation is
// a re-key: the intent record's resource changes from acl|<table>|<old>
// to acl|<table>|<new>. The new key must not already exist. Mirrors the
// in-place field mutation pattern used by the spec-level update verbs
// (#209/#210/#211). Issue #227.
func (n *Node) UpdateACLRule(ctx context.Context, tableName, ruleName string, opts ACLRuleConfig, newRuleName string) (*ChangeSet, error) {
	resource := "acl|" + tableName + "|" + ruleName
	existing := n.GetIntent(resource)
	if existing == nil {
		return nil, fmt.Errorf("rule %s not found in ACL table %s", ruleName, tableName)
	}
	// Re-key validation: target must be free.
	targetRule := ruleName
	if newRuleName != "" && newRuleName != ruleName {
		targetResource := "acl|" + tableName + "|" + newRuleName
		if n.GetIntent(targetResource) != nil {
			return nil, fmt.Errorf("rule %s already exists in ACL table %s", newRuleName, tableName)
		}
		targetRule = newRuleName
	}

	cs, err := n.op(sonic.OpUpdateACLRule, tableName, ChangeAdd,
		func(pc *PreconditionChecker) { pc.RequireACLTableExists(tableName) },
		func() []sonic.Entry { return nil })
	if err != nil {
		return nil, err
	}

	// CONFIG_DB: delete prior entry, write new one. When the rule_name
	// changes, the CONFIG_DB key changes too — the prior ACL_RULE row
	// vanishes by the same DEL.
	cs.Deletes(deleteAclRuleConfig(tableName, ruleName))
	cs.Adds(createAclRuleConfig(tableName, targetRule, opts))

	// Intent: when re-keying, the old resource is deleted and a fresh
	// resource is created. Otherwise the existing resource is updated
	// in place via writeIntent's idempotent-update path.
	intentParams := map[string]string{
		sonic.FieldName: targetRule,
		"acl":           tableName,
	}
	if opts.Priority > 0 {
		intentParams["priority"] = strconv.Itoa(opts.Priority)
	}
	if opts.Action != "" {
		intentParams["action"] = opts.Action
	}
	if opts.SrcIP != "" {
		intentParams["src_ip"] = opts.SrcIP
	}
	if opts.DstIP != "" {
		intentParams["dst_ip"] = opts.DstIP
	}
	if opts.Protocol != "" {
		intentParams["protocol"] = opts.Protocol
	}
	if opts.SrcPort != "" {
		intentParams["src_port"] = opts.SrcPort
	}
	if opts.DstPort != "" {
		intentParams["dst_port"] = opts.DstPort
	}
	if targetRule != ruleName {
		if err := n.deleteIntent(cs, resource); err != nil {
			return nil, err
		}
		if err := n.writeIntent(cs, sonic.OpAddACLRule, "acl|"+tableName+"|"+targetRule,
			intentParams,
			[]string{"acl|" + tableName}); err != nil {
			return nil, err
		}
	} else {
		if err := n.writeIntent(cs, sonic.OpAddACLRule, resource,
			intentParams,
			[]string{"acl|" + tableName}); err != nil {
			return nil, err
		}
	}

	cs.OperationParams = map[string]string{"table_name": tableName, "rule_name": targetRule}
	util.WithDevice(n.name).Infof("Updated rule %s in ACL table %s", targetRule, tableName)
	return cs, nil
}

// DeleteACLRule removes a single rule from an ACL table.
func (n *Node) DeleteACLRule(ctx context.Context, tableName, ruleName string) (*ChangeSet, error) {
	// Verify rule exists via intent DB
	if n.GetIntent("acl|"+tableName+"|"+ruleName) == nil {
		return nil, fmt.Errorf("rule %s not found in ACL table %s", ruleName, tableName)
	}

	cs, err := n.op("delete-acl-rule", tableName, ChangeDelete,
		func(pc *PreconditionChecker) { pc.RequireACLTableExists(tableName) },
		func() []sonic.Entry { return deleteAclRuleConfig(tableName, ruleName) })
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

// DeleteACL removes an ACL table and all its rules.
// Under the DAG, rules are removed as children before the table can be deleted.
func (n *Node) DeleteACL(ctx context.Context, name string) (*ChangeSet, error) {
	cs, err := n.op("delete-acl", name, ChangeDelete,
		func(pc *PreconditionChecker) { pc.RequireACLTableExists(name) },
		func() []sonic.Entry { return deleteAclTableConfig(name) })
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
