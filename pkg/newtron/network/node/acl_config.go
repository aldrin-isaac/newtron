package node

import (
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

// deleteAclTableConfig returns a delete entry for an ACL_TABLE.
func deleteAclTableConfig(name string) []sonic.Entry {
	return []sonic.Entry{{Table: "ACL_TABLE", Key: name}}
}

// deleteAclRuleConfig returns a delete entry for a single ACL rule.
func deleteAclRuleConfig(tableName, ruleName string) []sonic.Entry {
	return []sonic.Entry{{Table: "ACL_RULE", Key: fmt.Sprintf("%s|%s", tableName, ruleName)}}
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
