package node

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// InterfaceHasService checks if an interface has a service bound.
// Accepts both short (Eth0) and full (Ethernet0) interface names.
func (n *Node) InterfaceHasService(name string) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()

	name = util.NormalizeInterfaceName(name)
	if intf, ok := n.interfaces[name]; ok {
		return intf.HasService()
	}
	return false
}

// ============================================================================
// Interface Service Operations - Methods on Interface
// ============================================================================

// ApplyServiceOpts contains options for applying a service to an interface.
type ApplyServiceOpts struct {
	IPAddress string            // IP address for routed/IRB services (e.g., "10.1.1.1/30")
	VLAN      int               // VLAN ID for local types (irb, bridged) — overlay types use macvpnDef.VlanID
	PeerAS    int               // BGP peer AS number (for services with routing.peer_as="request")
	Params    map[string]string // topology params (peer_as, route_reflector_client, next_hop_self)
}

// createServiceBinding returns the NEWTRON_SERVICE_BINDING entry for tracking what service
// is applied to an interface.
func createServiceBinding(intfName string, fields map[string]string) sonic.Entry {
	return sonic.Entry{Table: "NEWTRON_SERVICE_BINDING", Key: intfName, Fields: fields}
}

// ApplyService applies a service definition to this interface.
// This is the main high-level operation that configures VPN, routing, filters, and QoS.
func (i *Interface) ApplyService(ctx context.Context, serviceName string, opts ApplyServiceOpts) (*ChangeSet, error) {
	n := i.node

	// Validate preconditions
	if err := n.precondition("apply-service", i.name).Result(); err != nil {
		return nil, err
	}

	// Get service definition via parent reference
	svc, err := i.Node().GetService(serviceName)
	if err != nil {
		return nil, fmt.Errorf("service '%s' not found", serviceName)
	}

	// Interface must not be a LAG member
	if i.IsPortChannelMember() {
		return nil, fmt.Errorf("interface %s is a PortChannel member - configure the PortChannel instead", i.name)
	}

	// Interface must not already have a service
	if i.HasService() {
		return nil, fmt.Errorf("interface %s already has service '%s' - remove it first", i.name, i.ServiceName())
	}

	// Resolve VPN definitions from service references
	var ipvpnDef *spec.IPVPNSpec
	var macvpnDef *spec.MACVPNSpec

	if svc.IPVPN != "" {
		var err error
		ipvpnDef, err = i.Node().GetIPVPN(svc.IPVPN)
		if err != nil {
			return nil, fmt.Errorf("ipvpn '%s' not found", svc.IPVPN)
		}
	}
	if svc.MACVPN != "" {
		var err error
		macvpnDef, err = i.Node().GetMACVPN(svc.MACVPN)
		if err != nil {
			return nil, fmt.Errorf("macvpn '%s' not found", svc.MACVPN)
		}
	}

	// Service-type specific validation
	switch svc.ServiceType {
	case spec.ServiceTypeEVPNIRB:
		if macvpnDef == nil {
			return nil, fmt.Errorf("service '%s' (evpn-irb) requires a macvpn reference — add 'macvpn' to the service definition via 'newtron evpn macvpn create'",
				serviceName)
		}
		if ipvpnDef == nil {
			return nil, fmt.Errorf("service '%s' (evpn-irb) requires an ipvpn reference — add 'ipvpn' to the service definition via 'newtron evpn ipvpn create'",
				serviceName)
		}
	case spec.ServiceTypeEVPNBridged:
		if macvpnDef == nil {
			return nil, fmt.Errorf("service '%s' (evpn-bridged) requires a macvpn reference — add 'macvpn' to the service definition via 'newtron evpn macvpn create'",
				serviceName)
		}
	case spec.ServiceTypeEVPNRouted:
		if ipvpnDef == nil {
			return nil, fmt.Errorf("service '%s' (evpn-routed) requires an ipvpn reference — add 'ipvpn' to the service definition via 'newtron evpn ipvpn create'",
				serviceName)
		}
		if opts.IPAddress == "" {
			return nil, fmt.Errorf("service '%s' (evpn-routed) requires an IP address — use --ip flag", serviceName)
		}
		if !util.IsValidIPv4CIDR(opts.IPAddress) {
			return nil, fmt.Errorf("invalid IP address: %s (expected CIDR notation like 10.1.1.1/30)", opts.IPAddress)
		}
	case spec.ServiceTypeRouted:
		if opts.IPAddress == "" {
			return nil, fmt.Errorf("service '%s' (routed) requires an IP address — use --ip flag", serviceName)
		}
		if !util.IsValidIPv4CIDR(opts.IPAddress) {
			return nil, fmt.Errorf("invalid IP address: %s (expected CIDR notation like 10.1.1.1/30)", opts.IPAddress)
		}
	case spec.ServiceTypeIRB:
		if opts.VLAN == 0 && macvpnDef == nil {
			return nil, fmt.Errorf("service '%s' (irb) requires a VLAN — use --vlan flag or add a macvpn reference to the service definition",
				serviceName)
		}
	case spec.ServiceTypeBridged:
		if opts.VLAN == 0 && macvpnDef == nil {
			return nil, fmt.Errorf("service '%s' (bridged) requires a VLAN — use --vlan flag or add a macvpn reference to the service definition",
				serviceName)
		}
	}

	// EVPN preconditions
	isOverlay := strings.HasPrefix(svc.ServiceType, "evpn-")
	if isOverlay {
		if !n.VTEPExists() {
			return nil, fmt.Errorf("service '%s' (%s) requires EVPN overlay, but no VTEP is configured on %s — run 'newtron -d %s evpn setup' first",
				serviceName, svc.ServiceType, n.Name(), n.Name())
		}
		if !n.BGPConfigured() {
			return nil, fmt.Errorf("service '%s' (%s) requires BGP, but no BGP_GLOBALS found on %s — run 'newtron -d %s evpn setup' or provision the device first",
				serviceName, svc.ServiceType, n.Name(), n.Name())
		}
	}

	// Filter preconditions
	if svc.IngressFilter != "" {
		if _, err := i.Node().GetFilter(svc.IngressFilter); err != nil {
			return nil, fmt.Errorf("service '%s' references ingress filter '%s' which was not found — define it via 'newtron filter create %s' or in network.json filters section",
				serviceName, svc.IngressFilter, svc.IngressFilter)
		}
	}
	if svc.EgressFilter != "" {
		if _, err := i.Node().GetFilter(svc.EgressFilter); err != nil {
			return nil, fmt.Errorf("service '%s' references egress filter '%s' which was not found — define it via 'newtron filter create %s' or in network.json filters section",
				serviceName, svc.EgressFilter, svc.EgressFilter)
		}
	}

	// QoS validation
	if svc.QoSPolicy != "" {
		if _, err := i.Node().GetQoSPolicy(svc.QoSPolicy); err != nil {
			return nil, fmt.Errorf("service '%s' references QoS policy '%s' which was not found — define it in network.json qos_policies section",
				serviceName, svc.QoSPolicy)
		}
	} else if svc.QoSProfile != "" {
		if _, err := i.Node().GetQoSProfile(svc.QoSProfile); err != nil {
			return nil, fmt.Errorf("service '%s' references QoS profile '%s' which was not found — define it in network.json qos_profiles section",
				serviceName, svc.QoSProfile)
		}
	}

	// Generate base CONFIG_DB entries via shared generator (service_gen.go).
	// This is the single source of truth for service → CONFIG_DB translation.
	resolved := n.Resolved()
	baseEntries, err := i.generateServiceEntries(ServiceEntryParams{
		ServiceName:  serviceName,
		IPAddress:    opts.IPAddress,
		VLAN:         opts.VLAN,
		Params:       opts.Params,
		PeerAS:       opts.PeerAS,
		UnderlayASN:  resolved.UnderlayASN,
		RouterID:     resolved.RouterID,
		PlatformName: resolved.Platform,
	})
	if err != nil {
		return nil, fmt.Errorf("generating service entries: %w", err)
	}

	// Determine VLAN ID for idempotency checks (overlay from macvpn, local from opts)
	vlanID := 0
	if macvpnDef != nil {
		vlanID = macvpnDef.VlanID
	} else if opts.VLAN > 0 {
		vlanID = opts.VLAN
	}

	// Build change set with idempotency filtering.
	// The shared generator always emits all entries (for topology provisioner's
	// overwrite mode).  Here we skip entries that already exist on the device.
	cs := NewChangeSet(n.Name(), "interface.apply-service")
	configDB := n.ConfigDB()

	// Track ACL names from generated entries for interface-merging
	var ingressACLName, egressACLName string
	if svc.IngressFilter != "" {
		ingressACLName = util.DeriveACLName(serviceName, "in")
	}
	if svc.EgressFilter != "" {
		egressACLName = util.DeriveACLName(serviceName, "out")
	}

	for _, e := range baseEntries {
		switch {
		// Skip VLAN + L2VNI + SUPPRESS entries if VLAN already exists
		case (e.Table == "VLAN" || e.Table == "SUPPRESS_VLAN_NEIGH") && vlanID > 0 && n.VLANExists(vlanID):
			continue
		case e.Table == "VXLAN_TUNNEL_MAP" && e.Fields["vlan"] != "" && vlanID > 0 && n.VLANExists(vlanID):
			continue

		// Skip shared VRF + L3VNI + RT entries if VRF already exists
		case e.Table == "VRF" && svc.VRFType == spec.VRFTypeShared && n.VRFExists(e.Key):
			continue
		case e.Table == "VXLAN_TUNNEL_MAP" && e.Fields["vrf"] != "" &&
			svc.VRFType == spec.VRFTypeShared && ipvpnDef != nil && n.VRFExists(ipvpnDef.VRF):
			continue
		case (e.Table == "BGP_GLOBALS_AF" || e.Table == "BGP_EVPN_VNI") &&
			svc.VRFType == spec.VRFTypeShared && ipvpnDef != nil && n.VRFExists(ipvpnDef.VRF):
			continue

		// Replace ACL entries with expanded version (prefix list Cartesian product + interface merging)
		case e.Table == "ACL_TABLE" && (e.Key == ingressACLName || e.Key == egressACLName):
			aclName := e.Key
			existingACL, aclExists := configDB.ACLTable[aclName]
			if aclExists {
				// ACL exists — merge this interface into the binding list
				merged := unbindAcl(aclName, addInterfaceToList(existingACL.Ports, i.name))
				cs.Update(merged.Table, merged.Key, merged.Fields)
			} else {
				// ACL doesn't exist - create table entry from generated fields
				cs.Add(e.Table, e.Key, e.Fields)
				// Add rules with prefix-list expansion
				filterName := svc.IngressFilter
				if aclName == egressACLName {
					filterName = svc.EgressFilter
				}
				filterSpec, _ := i.Node().GetFilter(filterName)
				if filterSpec != nil {
					i.addACLRulesFromFilterSpec(cs, aclName, filterSpec)
				}
			}
			continue
		case e.Table == "ACL_RULE":
			// Skip — ACL rules are handled above via addACLRulesFromFilterSpec
			continue

		// For NEWTRON_SERVICE_BINDING, add extra fields (ACL names, BGP neighbor)
		case e.Table == "NEWTRON_SERVICE_BINDING":
			// Handled separately below to add extra binding fields
			continue
		}

		// QoS device-wide tables need idempotent upsert in incremental mode
		if e.Table == "DSCP_TO_TC_MAP" || e.Table == "TC_TO_QUEUE_MAP" ||
			e.Table == "SCHEDULER" || e.Table == "WRED_PROFILE" {
			// For QoS device-wide entries, the shared generator doesn't emit these
			// (only per-interface entries). Generate them here for incremental mode.
		}

		cs.Add(e.Table, e.Key, e.Fields)
	}

	// QoS device-wide tables (not in shared generator, which only emits per-interface entries)
	var qosPolicyName string
	if pn, policy := ResolveServiceQoSPolicy(i.Node(), svc); policy != nil {
		qosPolicyName = pn
		for _, entry := range GenerateQoSDeviceEntries(pn, policy) {
			cs.Add(entry.Table, entry.Key, entry.Fields)
		}
	}

	// Add route policies (ROUTE_MAP, PREFIX_SET, COMMUNITY_SET) — these are only
	// needed in the incremental path, not in topology provisioner.
	var bgpNeighborIP string
	if svc.Routing != nil && svc.Routing.Protocol == spec.RoutingProtocolBGP {
		bgpNeighborIP, err = i.addBGPRoutePolicies(cs, serviceName, svc, opts)
		if err != nil {
			return nil, fmt.Errorf("BGP route policies for %s: %w", i.name, err)
		}
	}

	// Determine VRF name for binding and local state
	var vrfName string
	switch svc.VRFType {
	case spec.VRFTypeInterface:
		vrfName = util.DeriveVRFName(svc.VRFType, serviceName, i.name)
	case spec.VRFTypeShared:
		if ipvpnDef != nil {
			vrfName = ipvpnDef.VRF
		}
	}

	// Record service binding with extra fields
	bindingFields := map[string]string{
		"service_name": serviceName,
	}
	if opts.IPAddress != "" {
		bindingFields["ip_address"] = opts.IPAddress
	}
	if vrfName != "" {
		bindingFields["vrf_name"] = vrfName
	}
	if svc.IPVPN != "" {
		bindingFields["ipvpn"] = svc.IPVPN
	}
	if svc.MACVPN != "" {
		bindingFields["macvpn"] = svc.MACVPN
	}
	if ingressACLName != "" {
		bindingFields["ingress_acl"] = ingressACLName
	}
	if egressACLName != "" {
		bindingFields["egress_acl"] = egressACLName
	}
	if bgpNeighborIP != "" {
		bindingFields["bgp_neighbor"] = bgpNeighborIP
	}
	if qosPolicyName != "" {
		bindingFields["qos_policy"] = qosPolicyName
	}
	if vlanID > 0 {
		bindingFields["vlan_id"] = fmt.Sprintf("%d", vlanID)
	}
	if svc.Routing != nil && svc.Routing.Redistribute != nil {
		redistVRF := "default"
		if vrfName != "" {
			redistVRF = vrfName
		}
		bindingFields["redistribute_vrf"] = redistVRF
	}
	e := createServiceBinding(i.name, bindingFields)
	cs.Add(e.Table, e.Key, e.Fields)

	n.trackOffline(cs)
	util.WithDevice(n.Name()).Infof("Applied service '%s' to interface %s", serviceName, i.name)
	return cs, nil
}

// addBGPRoutePolicies adds route policy entries (ROUTE_MAP, PREFIX_SET, COMMUNITY_SET)
// and redistribution config for BGP services.  The BGP_NEIGHBOR and BGP_NEIGHBOR_AF
// entries are now generated by i.generateServiceEntries() in service_gen.go.
//
// Returns the neighbor IP (for the service binding record).
func (i *Interface) addBGPRoutePolicies(cs *ChangeSet, serviceName string, svc *spec.ServiceSpec, opts ApplyServiceOpts) (string, error) {
	if svc.Routing == nil || svc.Routing.Protocol != spec.RoutingProtocolBGP {
		return "", nil
	}

	routing := svc.Routing

	// Derive peer IP (same logic as generateBGPEntries, needed for return value)
	var peerIP string
	if opts.IPAddress != "" {
		var err error
		peerIP, err = util.DeriveNeighborIP(opts.IPAddress)
		if err != nil {
			return "", fmt.Errorf("could not derive BGP peer IP: %w", err)
		}
	}

	// Determine VRF key for route-map AF entries
	vrfName := ""
	if svc.VRFType == spec.VRFTypeInterface {
		vrfName = util.DeriveVRFName(svc.VRFType, serviceName, i.name)
	} else if svc.VRFType == spec.VRFTypeShared && svc.IPVPN != "" {
		if def, err := i.Node().GetIPVPN(svc.IPVPN); err == nil {
			vrfName = def.VRF
		}
	}
	vrfKey := "default"
	if vrfName != "" {
		vrfKey = vrfName
	}

	// Build route-map references for the BGP_NEIGHBOR_AF entry that was
	// already created by i.generateServiceEntries().  We add them as a modify
	// to layer on route_map_in / route_map_out.
	afFields := map[string]string{}

	if routing.ImportPolicy != "" {
		entries, rmName := i.createRoutePolicy(serviceName, "import", routing.ImportPolicy, routing.ImportCommunity, routing.ImportPrefixList)
		cs.Adds(entries)
		if rmName != "" {
			afFields["route_map_in"] = rmName
		}
	} else if routing.ImportCommunity != "" || routing.ImportPrefixList != "" {
		entries, rmName := i.createInlineRoutePolicy(serviceName, "import", routing.ImportCommunity, routing.ImportPrefixList)
		cs.Adds(entries)
		if rmName != "" {
			afFields["route_map_in"] = rmName
		}
	}

	if routing.ExportPolicy != "" {
		entries, rmName := i.createRoutePolicy(serviceName, "export", routing.ExportPolicy, routing.ExportCommunity, routing.ExportPrefixList)
		cs.Adds(entries)
		if rmName != "" {
			afFields["route_map_out"] = rmName
		}
	} else if routing.ExportCommunity != "" || routing.ExportPrefixList != "" {
		entries, rmName := i.createInlineRoutePolicy(serviceName, "export", routing.ExportCommunity, routing.ExportPrefixList)
		cs.Adds(entries)
		if rmName != "" {
			afFields["route_map_out"] = rmName
		}
	}

	// Merge route-map references into the BGP_NEIGHBOR_AF entry
	if len(afFields) > 0 && peerIP != "" {
		e := createBgpNeighborAF(vrfKey, peerIP, "ipv4_unicast", afFields)
		cs.Update(e.Table, e.Key, e.Fields)
	}

	// Override default redistribution if specified
	if routing.Redistribute != nil {
		var fields map[string]string
		if *routing.Redistribute {
			fields = map[string]string{
				"redistribute_connected": "true",
				"redistribute_static":    "true",
			}
		} else {
			fields = map[string]string{
				"redistribute_connected": "false",
				"redistribute_static":    "false",
			}
		}
		cs.Updates(CreateBGPGlobalsAF(vrfKey, "ipv4_unicast", fields))
	}

	return peerIP, nil
}

// createRoutePolicy translates a named RoutePolicy into CONFIG_DB ROUTE_MAP,
// PREFIX_SET, and COMMUNITY_SET entries. Returns entries and the route-map name.
func (i *Interface) createRoutePolicy(serviceName, direction, policyName, extraCommunity, extraPrefixList string) ([]sonic.Entry, string) {
	policy, err := i.Node().GetRoutePolicy(policyName)
	if err != nil {
		util.WithDevice(i.node.Name()).Warnf("Route policy '%s' not found: %v", policyName, err)
		return nil, ""
	}

	rmName := fmt.Sprintf("svc-%s-%s", sanitizeName(serviceName), direction)
	var entries []sonic.Entry

	for _, rule := range policy.Rules {
		ruleKey := fmt.Sprintf("%s|%d", rmName, rule.Sequence)
		fields := map[string]string{
			"route_operation": rule.Action,
		}

		if rule.PrefixList != "" {
			prefixSetName := fmt.Sprintf("%s-pl-%d", rmName, rule.Sequence)
			entries = append(entries, i.createPrefixSet(prefixSetName, rule.PrefixList)...)
			fields["match_prefix_set"] = prefixSetName
		}

		if rule.Community != "" {
			csName := fmt.Sprintf("%s-cs-%d", rmName, rule.Sequence)
			entries = append(entries, sonic.Entry{
				Table: "COMMUNITY_SET", Key: csName, Fields: map[string]string{
					"set_type":         "standard",
					"match_action":     "any",
					"community_member": rule.Community,
				},
			})
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

		entries = append(entries, sonic.Entry{Table: "ROUTE_MAP", Key: ruleKey, Fields: fields})
	}

	// Extra community AND condition from service routing spec
	if extraCommunity != "" {
		csName := fmt.Sprintf("%s-extra-cs", rmName)
		entries = append(entries, sonic.Entry{
			Table: "COMMUNITY_SET", Key: csName, Fields: map[string]string{
				"set_type":         "standard",
				"match_action":     "any",
				"community_member": extraCommunity,
			},
		})
		extraFields := map[string]string{
			"route_operation": "permit",
			"match_community": csName,
		}
		if direction == "export" {
			extraFields["set_community"] = extraCommunity
		}
		entries = append(entries, sonic.Entry{Table: "ROUTE_MAP", Key: fmt.Sprintf("%s|9000", rmName), Fields: extraFields})
	}

	// Extra prefix list AND condition
	if extraPrefixList != "" {
		plName := fmt.Sprintf("%s-extra-pl", rmName)
		entries = append(entries, i.createPrefixSet(plName, extraPrefixList)...)
		entries = append(entries, sonic.Entry{
			Table: "ROUTE_MAP", Key: fmt.Sprintf("%s|9100", rmName), Fields: map[string]string{
				"route_operation":  "permit",
				"match_prefix_set": plName,
			},
		})
	}

	return entries, rmName
}

// createInlineRoutePolicy creates a route-map from standalone community/prefix filters.
// Returns entries and the route-map name.
func (i *Interface) createInlineRoutePolicy(serviceName, direction, community, prefixList string) ([]sonic.Entry, string) {
	rmName := fmt.Sprintf("svc-%s-%s", sanitizeName(serviceName), direction)
	var entries []sonic.Entry
	seq := 10

	if community != "" {
		csName := fmt.Sprintf("%s-cs", rmName)
		entries = append(entries, sonic.Entry{
			Table: "COMMUNITY_SET", Key: csName, Fields: map[string]string{
				"set_type":         "standard",
				"match_action":     "any",
				"community_member": community,
			},
		})
		fields := map[string]string{
			"route_operation": "permit",
			"match_community": csName,
		}
		if direction == "export" {
			fields["set_community"] = community
		}
		entries = append(entries, sonic.Entry{Table: "ROUTE_MAP", Key: fmt.Sprintf("%s|%d", rmName, seq), Fields: fields})
		seq += 10
	}

	if prefixList != "" {
		plName := fmt.Sprintf("%s-pl", rmName)
		entries = append(entries, i.createPrefixSet(plName, prefixList)...)
		entries = append(entries, sonic.Entry{
			Table: "ROUTE_MAP", Key: fmt.Sprintf("%s|%d", rmName, seq), Fields: map[string]string{
				"route_operation":  "permit",
				"match_prefix_set": plName,
			},
		})
	}

	return entries, rmName
}

// createPrefixSet resolves a prefix list and returns PREFIX_SET entries.
func (i *Interface) createPrefixSet(prefixSetName, prefixListName string) []sonic.Entry {
	prefixes, err := i.Node().GetPrefixList(prefixListName)
	if err != nil || len(prefixes) == 0 {
		util.WithDevice(i.node.Name()).Warnf("Prefix list '%s' not found or empty", prefixListName)
		return nil
	}
	var entries []sonic.Entry
	for seq, prefix := range prefixes {
		entryKey := fmt.Sprintf("%s|%d", prefixSetName, (seq+1)*10)
		entries = append(entries, sonic.Entry{
			Table: "PREFIX_SET", Key: entryKey, Fields: map[string]string{
				"ip_prefix": prefix,
				"action":    "permit",
			},
		})
	}
	return entries
}

// deleteRoutePolicies returns delete entries for all ROUTE_MAP, PREFIX_SET, and
// COMMUNITY_SET entries created by a service (keyed by the deterministic prefix
// "svc-{sanitizeName(serviceName)}-").
func deleteRoutePolicies(configDB *sonic.ConfigDB, serviceName string) []sonic.Entry {
	var entries []sonic.Entry
	if configDB == nil {
		return entries
	}
	prefix := "svc-" + sanitizeName(serviceName) + "-"

	for key := range configDB.RouteMap {
		// RouteMap keys are "rmName|seq" — check if rmName starts with prefix
		parts := strings.SplitN(key, "|", 2)
		if len(parts) >= 1 && strings.HasPrefix(parts[0], prefix) {
			entries = append(entries, sonic.Entry{Table: "ROUTE_MAP", Key: key})
		}
	}
	for key := range configDB.PrefixSet {
		// PrefixSet keys are "setName|seq" — check if setName starts with prefix
		parts := strings.SplitN(key, "|", 2)
		if len(parts) >= 1 && strings.HasPrefix(parts[0], prefix) {
			entries = append(entries, sonic.Entry{Table: "PREFIX_SET", Key: key})
		}
	}
	for key := range configDB.CommunitySet {
		if strings.HasPrefix(key, prefix) {
			entries = append(entries, sonic.Entry{Table: "COMMUNITY_SET", Key: key})
		}
	}
	return entries
}

// sanitizeName replaces non-alphanumeric chars with hyphens for config key names.
func sanitizeName(name string) string {
	result := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			result = append(result, c)
		} else {
			result = append(result, '-')
		}
	}
	return string(result)
}

// addACLRulesFromFilterSpec adds ACL rules from a filter spec, expanding prefix lists
func (i *Interface) addACLRulesFromFilterSpec(cs *ChangeSet, aclName string, filterSpec *spec.FilterSpec) {
	for _, rule := range filterSpec.Rules {
		// Expand prefix lists if used
		srcIPs := i.expandPrefixList(rule.SrcPrefixList, rule.SrcIP)
		dstIPs := i.expandPrefixList(rule.DstPrefixList, rule.DstIP)

		// If no prefix lists, create single rule
		if len(srcIPs) == 0 {
			srcIPs = []string{""}
		}
		if len(dstIPs) == 0 {
			dstIPs = []string{""}
		}

		// Create rules for each combination (Cartesian product if both have multiple)
		ruleIdx := 0
		for _, srcIP := range srcIPs {
			for _, dstIP := range dstIPs {
				suffix := ""
				if len(srcIPs) > 1 || len(dstIPs) > 1 {
					suffix = fmt.Sprintf("_%d", ruleIdx)
					ruleIdx++
				}
				e := createAclRuleFromFilter(aclName, rule, srcIP, dstIP, suffix)
				cs.Add(e.Table, e.Key, e.Fields)
			}
		}
	}
}

// expandPrefixList expands a prefix list name to its IP prefixes, or returns direct IP if provided
func (i *Interface) expandPrefixList(prefixListName, directIP string) []string {
	if directIP != "" {
		return []string{directIP}
	}
	if prefixListName == "" {
		return nil
	}

	prefixes, err := i.Node().GetPrefixList(prefixListName)
	if err != nil || len(prefixes) == 0 {
		return nil
	}
	return prefixes
}

// addInterfaceToList adds an interface name to a comma-separated list
// (used for ACL_TABLE.ports which contains interface names despite the field name).
func addInterfaceToList(list, interfaceName string) string {
	if list == "" {
		return interfaceName
	}
	parts := strings.Split(list, ",")
	for _, p := range parts {
		if strings.TrimSpace(p) == interfaceName {
			return list // Already in list
		}
	}
	return list + "," + interfaceName
}

// removeInterfaceFromList removes an interface name from a comma-separated list
// (used for ACL_TABLE.ports which contains interface names despite the field name).
func removeInterfaceFromList(list, interfaceName string) string {
	parts := strings.Split(list, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" && p != interfaceName {
			result = append(result, p)
		}
	}
	return strings.Join(result, ",")
}

// removeSharedACL removes an ACL, handling the shared case
func (i *Interface) removeSharedACL(cs *ChangeSet, depCheck *DependencyChecker, aclName string) {
	configDB := i.node.ConfigDB()
	if configDB == nil {
		return
	}

	if _, ok := configDB.ACLTable[aclName]; !ok {
		return
	}

	if depCheck.IsLastACLUser(aclName) {
		// Last user — delete all ACL rules and table (delegates to acl_ops.go)
		cs.Deletes(deleteAclTable(configDB, aclName))
	} else {
		// Other users exist — just remove this interface from the binding list
		e := unbindAcl(aclName, depCheck.GetACLRemainingInterfaces(aclName))
		cs.Update(e.Table, e.Key, e.Fields)
	}
}

// RemoveService removes the service from this interface.
// Uses the stored service binding (NEWTRON_SERVICE_BINDING) to know exactly
// what was applied and needs to be removed.
// Shared resources (ACLs, VLANs) are only deleted when this is the last user.
func (i *Interface) RemoveService(ctx context.Context) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("remove-service", i.name).Result(); err != nil {
		return nil, err
	}
	if !i.HasService() {
		return nil, fmt.Errorf("interface %s has no service to remove", i.name)
	}

	cs := NewChangeSet(n.Name(), "interface.remove-service")

	// Read binding values from CONFIG_DB — ground truth for what was applied
	b := i.binding()
	serviceName := b.ServiceName
	vrfName := b.VRFName
	ingressACL := b.IngressACL
	egressACL := b.EgressACL
	bgpNeighbor := b.BGPNeighbor

	// Create dependency checker to determine what can be safely deleted
	depCheck := NewDependencyChecker(n, i.name)
	configDB := n.ConfigDB()

	// Get service definition for cleanup logic
	svc, _ := i.Node().GetService(serviceName)

	// Resolve VPN definitions - prefer stored binding, fall back to service lookup
	var ipvpnDef *spec.IPVPNSpec
	var macvpnDef *spec.MACVPNSpec

	if b.IPVPN != "" {
		ipvpnDef, _ = i.Node().GetIPVPN(b.IPVPN)
	} else if svc != nil && svc.IPVPN != "" {
		ipvpnDef, _ = i.Node().GetIPVPN(svc.IPVPN)
	}

	if b.MACVPN != "" {
		macvpnDef, _ = i.Node().GetMACVPN(b.MACVPN)
	} else if svc != nil && svc.MACVPN != "" {
		macvpnDef, _ = i.Node().GetMACVPN(svc.MACVPN)
	}

	// Check if this is the last interface using this service (for shared resources)
	isLastServiceUser := depCheck.IsLastServiceUser(serviceName)

	// =========================================================================
	// Per-interface resources (always delete)
	// =========================================================================

	// Remove QoS mapping and per-interface QUEUE entries
	cs.Deletes(i.unbindQos())

	// Remove QoS device-wide entries if no other interface references this policy
	if b.QoSPolicy != "" {
		if !isQoSPolicyReferenced(configDB, b.QoSPolicy, i.name) {
			cs.Deletes(deleteQoSDeviceEntries(configDB, b.QoSPolicy))
		}
	}

	// Remove IP addresses from interface
	for _, ipAddr := range i.IPAddresses() {
		cs.Deletes(i.assignIpAddress(ipAddr))
	}

	// Remove INTERFACE base entry for routed services (created by service).
	// Must come after IP deletions since intfmgrd enforces parent-child ordering.
	canRoute := svc != nil && (svc.ServiceType == spec.ServiceTypeRouted || svc.ServiceType == spec.ServiceTypeEVPNRouted)
	if canRoute && (vrfName == "" || vrfName == "default") {
		cs.Deletes(i.enableIpRouting())
	}

	// Remove BGP neighbor created by this service (tracked in binding)
	if bgpNeighbor != "" {
		vrfKey := "default"
		if vrfName != "" && vrfName != "default" {
			vrfKey = vrfName
		}
		cs.Deletes(DeleteBGPNeighbor(vrfKey, bgpNeighbor))
	}

	// =========================================================================
	// Per-service resources (delete only if last user)
	// =========================================================================

	// Handle shared ACLs
	if ingressACL != "" {
		i.removeSharedACL(cs, depCheck, ingressACL)
	}
	if egressACL != "" {
		i.removeSharedACL(cs, depCheck, egressACL)
	}

	// Remove route policies (ROUTE_MAP, PREFIX_SET, COMMUNITY_SET)
	if isLastServiceUser {
		cs.Deletes(deleteRoutePolicies(configDB, serviceName))
	}

	// Revert BGP_GLOBALS_AF redistribution override if this service set it.
	// For per-interface VRFs, destroyVrf cascades this anyway — harmless redundancy.
	if b.RedistributeVRF != "" && isLastServiceUser {
		cs.Updates(revertRedistribution(b.RedistributeVRF))
	}

	// =========================================================================
	// Per-interface VRF (vrf_type: interface)
	// =========================================================================

	if vrfName != "" && vrfName != "default" {
		// For routed services, delete the INTERFACE base entry entirely.
		// For non-routed services (IRB, bridged), just clear the VRF binding.
		if canRoute {
			cs.Deletes(i.enableIpRouting())
		} else {
			cs.Updates(i.bindVrf(""))
		}

		// Per-interface VRF: delete VRF and related config
		if svc != nil && svc.VRFType == spec.VRFTypeInterface {
			derivedVRF := util.DeriveVRFName(svc.VRFType, serviceName, i.name)
			l3vni := 0
			if ipvpnDef != nil {
				l3vni = ipvpnDef.L3VNI
			}
			cs.Deletes(destroyVrf(n.configDB, derivedVRF, l3vni))
		}

		// Shared VRF: delete when last ipvpn user is removed.
		// The shared VRF was auto-created by the first service apply and should
		// be cleaned up when no service bindings reference the ipvpn anymore.
		if svc != nil && svc.VRFType == spec.VRFTypeShared && svc.IPVPN != "" {
			if depCheck.IsLastIPVPNUser(svc.IPVPN) && ipvpnDef != nil && ipvpnDef.VRF != "" {
				cs.Deletes(destroyVrf(n.configDB, ipvpnDef.VRF, ipvpnDef.L3VNI))
			}
		}
	}

	// =========================================================================
	// Per-VLAN resources (delete only if last VLAN member)
	// =========================================================================

	// Resolve VLAN ID: prefer macvpn definition, fall back to binding record
	vlanID := 0
	if macvpnDef != nil && macvpnDef.VlanID > 0 {
		vlanID = macvpnDef.VlanID
	} else if b.VlanID != "" {
		vlanID, _ = strconv.Atoi(b.VlanID)
	}

	if svc != nil && vlanID > 0 {
		canBridge := svc.ServiceType == spec.ServiceTypeEVPNIRB || svc.ServiceType == spec.ServiceTypeEVPNBridged ||
			svc.ServiceType == spec.ServiceTypeIRB || svc.ServiceType == spec.ServiceTypeBridged
		if canBridge {
			vlanName := VLANName(vlanID)

			// Always remove this interface's VLAN membership
			cs.Deletes(deleteVlanMember(vlanID, i.name))

			// Check if this is the last VLAN member
			if depCheck.IsLastVLANMember(vlanID) {
				// Last member - clean up all VLAN-related config

				// SVI (for IRB types)
				hasIRB := svc.ServiceType == spec.ServiceTypeEVPNIRB || svc.ServiceType == spec.ServiceTypeIRB
				if hasIRB {
					if macvpnDef != nil && macvpnDef.AnycastIP != "" {
						cs.Deletes(deleteSviIP(vlanID, macvpnDef.AnycastIP))
					} else if b.IPAddress != "" {
						// Local IRB: SVI IP comes from opts.IPAddress (stored in binding)
						cs.Deletes(deleteSviIP(vlanID, b.IPAddress))
					}
					cs.Deletes(deleteSviBase(vlanID))

					// SAG_GLOBAL: clean up when last anycast MAC user is removed
					if macvpnDef != nil && macvpnDef.AnycastMAC != "" && depCheck.IsLastAnycastMACUser() {
						cs.Deletes(deleteSagGlobal())
					}
				}

				// ARP suppression (only when macvpn defines it)
				if macvpnDef != nil && macvpnDef.ARPSuppression {
					cs.Deletes(disableArpSuppression(vlanName))
				}

				// VNI mapping (only when macvpn defines it)
				if macvpnDef != nil && macvpnDef.VNI > 0 {
					cs.Deletes(deleteVniMap(macvpnDef.VNI, vlanName))
				}

				// VLAN itself
				cs.Deletes(deleteVlan(vlanID))
			}
		}
	}

	// =========================================================================
	// Service binding tracking (always delete)
	// =========================================================================

	cs.Delete("NEWTRON_SERVICE_BINDING", i.name)

	// Log if this was the last user of the service
	if isLastServiceUser {
		util.WithDevice(n.Name()).Infof("Last interface removed from service '%s' - all service resources cleaned up", serviceName)
	}

	util.WithDevice(n.Name()).Infof("Removed service '%s' from interface %s", serviceName, i.name)
	return cs, nil
}

// RefreshService reapplies the service configuration to sync with the service definition.
// This is useful when the service definition has changed.
func (i *Interface) RefreshService(ctx context.Context) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("refresh-service", i.name).Result(); err != nil {
		return nil, err
	}
	if !i.HasService() {
		return nil, fmt.Errorf("interface %s has no service to refresh", i.name)
	}

	// Capture binding values before RemoveService records the delete
	b := i.binding()
	serviceName := b.ServiceName
	serviceIP := b.IPAddress

	// Remove the current service
	removeCS, err := i.RemoveService(ctx)
	if err != nil {
		return nil, fmt.Errorf("removing old service: %w", err)
	}

	// Clear the binding from the ConfigDB cache so ApplyService's
	// HasService() check passes. The delete change is already recorded
	// above; this cache mutation only affects reads within this episode.
	configDB := n.ConfigDB()
	delete(configDB.NewtronServiceBinding, i.name)

	// Reapply the service with current definition
	// Note: PeerAS is 0 here since we're refreshing an existing service
	// and the BGP neighbor would already be configured
	applyCS, err := i.ApplyService(ctx, serviceName, ApplyServiceOpts{IPAddress: serviceIP})
	if err != nil {
		return nil, fmt.Errorf("reapplying service: %w", err)
	}

	// Merge the change sets
	cs := NewChangeSet(n.Name(), "interface.refresh-service")
	cs.Merge(removeCS)
	cs.Merge(applyCS)

	util.WithDevice(n.Name()).Infof("Refreshed service '%s' on interface %s", serviceName, i.name)
	return cs, nil
}
