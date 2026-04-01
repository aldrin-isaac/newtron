// service_config.go implements pure CONFIG_DB entry generation for service-level
// route policies (ROUTE_MAP, PREFIX_SET, COMMUNITY_SET) with content-hashed names.
package node

import (
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// routeMapRule holds a single route-map rule's sequence and fields,
// used during bottom-up Merkle hash computation (Principle 35).
type routeMapRule struct {
	seq    int
	fields map[string]string
}

// createRoutePolicyConfig translates a resolved RoutePolicy into CONFIG_DB ROUTE_MAP,
// PREFIX_SET, and COMMUNITY_SET entries with content-hashed names (Principle 35).
// Bottom-up Merkle: PREFIX_SET/COMMUNITY_SET hashes computed first (leaves),
// then ROUTE_MAP hash includes those hashed names. Returns entries and the
// route-map name.
//
// prefixLists maps prefix list names to their resolved prefixes. The caller
// resolves specs; this function is pure.
func createRoutePolicyConfig(serviceName, direction string, policy *spec.RoutePolicy, prefixLists map[string][]string, extraCommunity, extraPrefixList string) ([]sonic.Entry, string) {
	// serviceName is already normalized (uppercase, underscores) by the spec loader.
	baseRMName := fmt.Sprintf("%s_%s", serviceName, strings.ToUpper(direction))

	// Phase 1: Build leaf objects (PREFIX_SET, COMMUNITY_SET) with content hashes.
	// Collect route-map rule fields that reference the hashed leaf names.
	var leafEntries []sonic.Entry
	var rules []routeMapRule

	for _, rule := range policy.Rules {
		fields := map[string]string{
			"route_operation": rule.Action,
		}

		if rule.PrefixList != "" {
			plBase := fmt.Sprintf("%s_PL_%d", baseRMName, rule.Sequence)
			plEntries, plName := createHashedPrefixSetConfig(plBase, prefixLists[rule.PrefixList])
			leafEntries = append(leafEntries, plEntries...)
			if plName != "" {
				fields["match_prefix_set"] = plName
			}
		}

		if rule.Community != "" {
			csBase := fmt.Sprintf("%s_CS_%d", baseRMName, rule.Sequence)
			csEntries, csName := createCommunitySetConfig(csBase, rule.Community)
			leafEntries = append(leafEntries, csEntries...)
			fields["match_community"] = csName
		}

		if rule.Set != nil {
			if rule.Set.LocalPref > 0 {
				fields["set_local_pref"] = fmt.Sprintf("%d", rule.Set.LocalPref)
			}
			if rule.Set.Community != "" {
				fields["set_community"] = rule.Set.Community
			}
			if rule.Set.MED > 0 {
				fields["set_med"] = fmt.Sprintf("%d", rule.Set.MED)
			}
		}

		rules = append(rules, routeMapRule{seq: rule.Sequence, fields: fields})
	}

	// Extra community AND condition from service routing spec
	if extraCommunity != "" {
		csBase := fmt.Sprintf("%s_EXTRA_CS", baseRMName)
		csEntries, csName := createCommunitySetConfig(csBase, extraCommunity)
		leafEntries = append(leafEntries, csEntries...)
		extraFields := map[string]string{
			"route_operation": "permit",
			"match_community": csName,
		}
		if direction == "export" {
			extraFields["set_community"] = extraCommunity
		}
		rules = append(rules, routeMapRule{seq: 9000, fields: extraFields})
	}

	// Extra prefix list AND condition
	if extraPrefixList != "" {
		plBase := fmt.Sprintf("%s_EXTRA_PL", baseRMName)
		plEntries, plName := createHashedPrefixSetConfig(plBase, prefixLists[extraPrefixList])
		leafEntries = append(leafEntries, plEntries...)
		if plName != "" {
			rules = append(rules, routeMapRule{seq: 9100, fields: map[string]string{
				"route_operation":  "permit",
				"match_prefix_set": plName,
			}})
		}
	}

	return buildRouteMapConfig(baseRMName, leafEntries, rules)
}

// createInlineRoutePolicyConfig creates a route-map from standalone community/prefix
// filters with content-hashed names (Principle 35). Returns entries and the
// route-map name.
//
// prefixes is the resolved prefix list (nil if no prefix list). The caller
// resolves specs; this function is pure.
func createInlineRoutePolicyConfig(serviceName, direction, community string, prefixes []string) ([]sonic.Entry, string) {
	// serviceName is already normalized (uppercase, underscores) by the spec loader.
	baseRMName := fmt.Sprintf("%s_%s", serviceName, strings.ToUpper(direction))
	var leafEntries []sonic.Entry
	var rules []routeMapRule
	seq := 10

	if community != "" {
		csBase := fmt.Sprintf("%s_CS", baseRMName)
		csEntries, csName := createCommunitySetConfig(csBase, community)
		leafEntries = append(leafEntries, csEntries...)
		fields := map[string]string{
			"route_operation": "permit",
			"match_community": csName,
		}
		if direction == "export" {
			fields["set_community"] = community
		}
		rules = append(rules, routeMapRule{seq: seq, fields: fields})
		seq += 10
	}

	if len(prefixes) > 0 {
		plBase := fmt.Sprintf("%s_PL", baseRMName)
		plEntries, plName := createHashedPrefixSetConfig(plBase, prefixes)
		leafEntries = append(leafEntries, plEntries...)
		if plName != "" {
			rules = append(rules, routeMapRule{seq: seq, fields: map[string]string{
				"route_operation":  "permit",
				"match_prefix_set": plName,
			}})
		}
	}

	return buildRouteMapConfig(baseRMName, leafEntries, rules)
}

// buildRouteMapConfig computes the Merkle hash over rules and builds the final
// route-map entries. Shared by createRoutePolicyConfig and createInlineRoutePolicyConfig.
func buildRouteMapConfig(baseRMName string, leafEntries []sonic.Entry, rules []routeMapRule) ([]sonic.Entry, string) {
	if len(rules) == 0 {
		return leafEntries, ""
	}

	var rmFieldMaps []map[string]string
	for _, r := range rules {
		rmFieldMaps = append(rmFieldMaps, r.fields)
	}
	rmHash := util.ContentHash(rmFieldMaps)
	rmName := fmt.Sprintf("%s_%s", baseRMName, rmHash)

	entries := make([]sonic.Entry, 0, len(leafEntries)+len(rules))
	entries = append(entries, leafEntries...)
	for _, r := range rules {
		entries = append(entries, sonic.Entry{
			Table: "ROUTE_MAP", Key: fmt.Sprintf("%s|%d", rmName, r.seq), Fields: r.fields,
		})
	}

	return entries, rmName
}

// createHashedPrefixSetConfig returns PREFIX_SET entries with a content-hashed name (Principle 35).
// Prefixes are resolved by the caller — this function is pure.
func createHashedPrefixSetConfig(baseName string, prefixes []string) ([]sonic.Entry, string) {
	if len(prefixes) == 0 {
		return nil, ""
	}

	// Compute content hash from the fields that will be written.
	var fieldMaps []map[string]string
	for _, prefix := range prefixes {
		fieldMaps = append(fieldMaps, map[string]string{
			"ip_prefix": prefix,
			"action":    "permit",
		})
	}
	hash := util.ContentHash(fieldMaps)
	name := fmt.Sprintf("%s_%s", baseName, hash)

	var entries []sonic.Entry
	for seq, prefix := range prefixes {
		entries = append(entries, sonic.Entry{
			Table: "PREFIX_SET", Key: fmt.Sprintf("%s|%d", name, (seq+1)*10),
			Fields: map[string]string{
				"ip_prefix": prefix,
				"action":    "permit",
			},
		})
	}
	return entries, name
}

// createCommunitySetConfig returns a COMMUNITY_SET entry with a content-hashed name.
func createCommunitySetConfig(baseName, community string) ([]sonic.Entry, string) {
	csFields := map[string]string{
		"set_type":         "standard",
		"match_action":     "any",
		"community_member": community,
	}
	csHash := util.ContentHash([]map[string]string{csFields})
	csName := fmt.Sprintf("%s_%s", baseName, csHash)
	return []sonic.Entry{{Table: "COMMUNITY_SET", Key: csName, Fields: csFields}}, csName
}

// deleteRoutePoliciesConfig returns delete entries for ROUTE_MAP, PREFIX_SET, and
// COMMUNITY_SET entries given a semicolon-separated "TABLE:key" string.
func deleteRoutePoliciesConfig(keysCSV string) []sonic.Entry {
	var entries []sonic.Entry
	if keysCSV == "" {
		return entries
	}

	// Keys stored as "TABLE:key;TABLE:key;..." (semicolon-separated, colon table:key)
	for _, tk := range strings.Split(keysCSV, ";") {
		tk = strings.TrimSpace(tk)
		if tk == "" {
			continue
		}
		parts := strings.SplitN(tk, ":", 2)
		if len(parts) == 2 {
			entries = append(entries, sonic.Entry{Table: parts[0], Key: parts[1]})
		}
	}
	return entries
}
