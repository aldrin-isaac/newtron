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

// aclPortsFromIntents computes the port set an ACL binds to by scanning the
// intent DB — derived, never recorded (§21). It reads two binding kinds:
//
//   - Standalone ACL bindings ("interface|X|acl|DIR") bind X directly.
//   - Service bindings ("interface|X|service", OpApplyService) that reference the
//     ACL. A per-port service (routed/bridged) binds its own interface. An
//     irb-type service binds the IRB (VlanN) — but a VLAN interface is not an ACL
//     bind point (§7), so an irb service's ACL is bound to the VLAN's member ports
//     instead: the IRB binding is expanded to the current members. Order-
//     independent by construction: whether the members or the binding was
//     authored first, the set is the same because both are read from the DB here.
//
// Returns the sorted, comma-separated ports for the ACL_TABLE ports field.
func (n *Node) aclPortsFromIntents(aclName, direction string) string {
	aclField := direction + "_acl" // "ingress_acl" or "egress_acl"
	set := map[string]bool{}
	for resource, intent := range n.IntentsByPrefix("interface|") {
		// Standalone ACL binding intents: "interface|Ethernet0|acl|ingress"
		if strings.HasSuffix(resource, "|acl|"+direction) {
			if intent.Params[sonic.FieldACLName] == aclName {
				parts := strings.SplitN(resource, "|", 3)
				if len(parts) >= 2 {
					set[parts[1]] = true
				}
			}
			continue
		}
		// Service bindings ("interface|X|service") referencing the ACL.
		if intent.Operation == sonic.OpApplyService && intent.Params[aclField] == aclName {
			ifName := resourceInterfaceName(resource)
			if ifName == "" {
				continue
			}
			if interfaceKindOf(ifName) == KindIRB {
				// An irb service's ACL binds to the VLAN's member ports.
				for _, m := range n.vlanMemberPorts(bindingInt(intent.Params[sonic.FieldVLANID])) {
					set[m] = true
				}
			} else {
				set[ifName] = true
			}
		}
	}
	ports := make([]string, 0, len(set))
	for p := range set {
		ports = append(ports, p)
	}
	sort.Strings(ports)
	return strings.Join(ports, ",")
}

// rebindMemberACLs updates the ACL_TABLE ports-list for every ACL an irb-type
// service binds on this VLAN — the membership-delta entry point (§4). A member
// joining or leaving the VLAN changes which ports the ACL binds to, so each
// affected ACL's ports are recomputed from the intent DB and delivered as an
// in-place field edit (§48 — the ACL must not bounce; a rule flush would drop
// traffic on the other members). Call it after the membership intent is written
// (join) or removed (leave); either way aclPortsFromIntents reflects the change,
// because writeIntent/deleteIntent update the projection in place. A no-op when
// no irb service on the VLAN carries a filter.
func (n *Node) rebindMemberACLs(cs *ChangeSet, vlanID int) {
	if vlanID <= 0 {
		return
	}
	rendered := map[string]bool{} // "dir|acl" — render each ACL once
	for resource, intent := range n.IntentsByParam(sonic.FieldVLANID, strconv.Itoa(vlanID)) {
		if intent.Operation != sonic.OpApplyService {
			continue
		}
		// Only an irb binding (on the IRB) binds its ACL to the members; a
		// per-port service binds its own interface, unaffected by another
		// member's join or leave.
		if interfaceKindOf(resourceInterfaceName(resource)) != KindIRB {
			continue
		}
		for _, dir := range []string{"ingress", "egress"} {
			aclName := intent.Params[dir+"_acl"]
			if aclName == "" || rendered[dir+"|"+aclName] {
				continue
			}
			rendered[dir+"|"+aclName] = true
			if n.GetIntent("acl|"+aclName) == nil {
				continue // table not created (the service carries no filter this direction)
			}
			merged := updateAclPorts(aclName, n.aclPortsFromIntents(aclName, dir))
			cs.Update(merged.Table, merged.Key, merged.Fields)
		}
	}
}

// reconcileMemberACLPorts recomputes every service ACL's ports-list from the
// fully-loaded intent DB — the order-independence guarantee (§4), enforced after
// an incremental replay. During RebuildProjectionFromIntents the intent DB is
// built step by step, so a per-step update (create-acl, membership, or binding)
// can compute the ports against a partially-loaded DB; whichever ran last wins,
// and it may disagree with the final truth. Once every intent is loaded, this
// pass recomputes each service ACL's bound ports from (binding × members) and
// delivers the correction as an in-place field edit (§48). A no-op when the ports
// already match.
func (n *Node) reconcileMemberACLPorts(ctx context.Context) error {
	cs := NewChangeSet(n.Name(), "reconcile-acl-ports")
	done := map[string]bool{}
	// Scan the service bindings — they name their ingress/egress ACL — rather
	// than the acl intents: a service ACL is one a binding references, and its
	// bound ports are the VLAN's members. A standalone ACL (no binding) authors
	// its own ports and is left untouched.
	for _, intent := range n.IntentsByPrefix("interface|") {
		if intent.Operation != sonic.OpApplyService {
			continue
		}
		for _, dir := range []string{"ingress", "egress"} {
			name := intent.Params[dir+"_acl"]
			if name == "" || done[dir+"|"+name] {
				continue
			}
			done[dir+"|"+name] = true
			if _, ok := n.configDB.ACLTable[name]; !ok {
				continue
			}
			want := n.aclPortsFromIntents(name, dir)
			if n.configDB.ACLTable[name].Ports == want {
				continue
			}
			merged := updateAclPorts(name, want)
			cs.Update(merged.Table, merged.Key, merged.Fields)
		}
	}
	if len(cs.Changes) == 0 {
		return nil
	}
	return n.render(cs)
}

// ============================================================================
// ACL Operations
// ============================================================================

// ACLConfig holds configuration options for CreateACL.
type ACLConfig struct {
	Type        string
	Stage       string
	Ports       string
	Description string
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
		"device.remove-acl-rule")
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
// Reads the existing intent record by ruleName and emits a single
// ChangeSet that deletes the prior ACL_RULE entry and writes the new one.
// The intent record is replaced via writeIntent's idempotent path
// (DEL+HSET — #228) so dropped params don't ghost.
//
// Per §47 (CONFIG_DB Composite Key Is the Identity) the key
// (acl_table, rule_name) is immutable. Renaming a rule is remove + add,
// not update. Issue #227.
func (n *Node) UpdateACLRule(ctx context.Context, tableName, ruleName string, opts ACLRuleConfig) (*ChangeSet, error) {
	resource := "acl|" + tableName + "|" + ruleName
	existing := n.GetIntent(resource)
	if existing == nil {
		return nil, fmt.Errorf("rule %s not found in ACL table %s", ruleName, tableName)
	}

	cs, err := n.op(sonic.OpUpdateACLRule, tableName, ChangeAdd,
		func(pc *PreconditionChecker) { pc.RequireACLTableExists(tableName) },
		func() []sonic.Entry { return nil })
	if err != nil {
		return nil, err
	}

	// In-place replace of the same rule key — the rule_name is the row's
	// identity (§47), and the update is delivered without ever DELeting the key
	// so aclorch never sees a leak/deny window (§48).
	cs.Replace(n,
		deleteAclRuleConfig(tableName, ruleName),
		createAclRuleConfig(tableName, ruleName, opts))

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
	if err := n.writeIntent(cs, sonic.OpAddACLRule, resource,
		intentParams,
		[]string{"acl|" + tableName}); err != nil {
		return nil, err
	}

	cs.OperationParams = map[string]string{"table_name": tableName, "rule_name": ruleName}
	util.WithDevice(n.name).Infof("Updated rule %s in ACL table %s", ruleName, tableName)
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
