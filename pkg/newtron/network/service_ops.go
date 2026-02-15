package network

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// Interface Service Operations - Methods on Interface
// ============================================================================

// ApplyServiceOpts contains options for applying a service to an interface.
type ApplyServiceOpts struct {
	IPAddress string // IP address for L3 services (e.g., "10.1.1.1/30")
	PeerAS    int    // BGP peer AS number (for services with routing.peer_as="request")
}

// ApplyService applies a service definition to this interface.
// This is the main high-level operation that configures VPN, routing, filters, and QoS.
func (i *Interface) ApplyService(ctx context.Context, serviceName string, opts ApplyServiceOpts) (*ChangeSet, error) {
	d := i.device

	// Validate preconditions
	if err := d.precondition("apply-service", i.name).Result(); err != nil {
		return nil, err
	}

	// Get service definition via parent reference
	svc, err := i.Network().GetService(serviceName)
	if err != nil {
		return nil, fmt.Errorf("service '%s' not found", serviceName)
	}

	// Interface must not be a LAG member
	if i.IsLAGMember() {
		return nil, fmt.Errorf("interface %s is a LAG member - configure the LAG instead", i.name)
	}

	// Interface must not already have a service
	if i.HasService() {
		return nil, fmt.Errorf("interface %s already has service '%s' - remove it first", i.name, i.serviceName)
	}

	// Resolve VPN definitions from service references
	var ipvpnDef *spec.IPVPNSpec
	var macvpnDef *spec.MACVPNSpec

	if svc.IPVPN != "" {
		var err error
		ipvpnDef, err = i.Network().GetIPVPN(svc.IPVPN)
		if err != nil {
			return nil, fmt.Errorf("ipvpn '%s' not found", svc.IPVPN)
		}
	}
	if svc.MACVPN != "" {
		var err error
		macvpnDef, err = i.Network().GetMACVPN(svc.MACVPN)
		if err != nil {
			return nil, fmt.Errorf("macvpn '%s' not found", svc.MACVPN)
		}
	}

	// Service-type specific validation
	switch svc.ServiceType {
	case spec.ServiceTypeL3:
		if opts.IPAddress == "" {
			return nil, fmt.Errorf("L3 service requires IP address")
		}
		if !util.IsValidIPv4CIDR(opts.IPAddress) {
			return nil, fmt.Errorf("invalid IP address: %s", opts.IPAddress)
		}
	case spec.ServiceTypeL2, spec.ServiceTypeIRB:
		if svc.VLAN == 0 {
			return nil, fmt.Errorf("L2/IRB service requires 'vlan' field in service definition")
		}
		if macvpnDef == nil {
			return nil, fmt.Errorf("L2/IRB service requires macvpn reference")
		}
	}

	// EVPN preconditions
	hasEVPN := (ipvpnDef != nil && ipvpnDef.L3VNI > 0) || (macvpnDef != nil && macvpnDef.L2VNI > 0)
	if hasEVPN {
		if !d.VTEPExists() {
			return nil, fmt.Errorf("EVPN requires VTEP configuration")
		}
		if !d.BGPConfigured() {
			return nil, fmt.Errorf("EVPN requires BGP configuration")
		}
	}

	// Filter preconditions
	if svc.IngressFilter != "" {
		if _, err := i.Network().GetFilterSpec(svc.IngressFilter); err != nil {
			return nil, fmt.Errorf("ingress filter '%s' not found", svc.IngressFilter)
		}
	}
	if svc.EgressFilter != "" {
		if _, err := i.Network().GetFilterSpec(svc.EgressFilter); err != nil {
			return nil, fmt.Errorf("egress filter '%s' not found", svc.EgressFilter)
		}
	}

	// QoS validation
	if svc.QoSPolicy != "" {
		if _, err := i.Network().GetQoSPolicy(svc.QoSPolicy); err != nil {
			return nil, fmt.Errorf("QoS policy '%s' not found", svc.QoSPolicy)
		}
	} else if svc.QoSProfile != "" {
		if _, err := i.Network().GetQoSProfile(svc.QoSProfile); err != nil {
			return nil, fmt.Errorf("QoS profile '%s' not found", svc.QoSProfile)
		}
	}

	// Generate base CONFIG_DB entries via shared generator (service_gen.go).
	// This is the single source of truth for service → CONFIG_DB translation.
	resolved := d.Resolved()
	baseEntries, err := GenerateServiceEntries(i.Network(), ServiceEntryParams{
		ServiceName:   serviceName,
		InterfaceName: i.name,
		IPAddress:     opts.IPAddress,
		PeerAS:        opts.PeerAS,
		LocalAS:       resolved.ASNumber,
		UnderlayASN:   resolved.UnderlayASN,
		PlatformName:  resolved.Platform,
	})
	if err != nil {
		return nil, fmt.Errorf("generating service entries: %w", err)
	}

	// Determine VLAN ID for idempotency checks
	vlanID := svc.VLAN

	// Build change set with idempotency filtering.
	// The shared generator always emits all entries (for topology provisioner's
	// overwrite mode).  Here we skip entries that already exist on the device.
	cs := NewChangeSet(d.Name(), "interface.apply-service")
	configDB := d.ConfigDB()

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
		case (e.Table == "VLAN" || e.Table == "SUPPRESS_VLAN_NEIGH") && vlanID > 0 && d.VLANExists(vlanID):
			continue
		case e.Table == "VXLAN_TUNNEL_MAP" && e.Fields["vlan"] != "" && vlanID > 0 && d.VLANExists(vlanID):
			continue

		// Skip shared VRF + L3VNI + RT entries if VRF already exists
		case e.Table == "VRF" && svc.VRFType == spec.VRFTypeShared && d.VRFExists(e.Key):
			continue
		case e.Table == "VXLAN_TUNNEL_MAP" && e.Fields["vrf"] == svc.IPVPN &&
			svc.VRFType == spec.VRFTypeShared && d.VRFExists(svc.IPVPN):
			continue
		case (e.Table == "BGP_GLOBALS_AF" || e.Table == "BGP_EVPN_VNI") &&
			svc.VRFType == spec.VRFTypeShared && svc.IPVPN != "" && d.VRFExists(svc.IPVPN):
			continue

		// Replace ACL entries with expanded version (prefix list Cartesian product + interface merging)
		case e.Table == "ACL_TABLE" && (e.Key == ingressACLName || e.Key == egressACLName):
			aclName := e.Key
			existingACL, aclExists := configDB.ACLTable[aclName]
			if aclExists {
				// ACL exists - merge this interface into the binding list
				newBindings := addInterfaceToList(existingACL.Ports, i.name)
				cs.Add("ACL_TABLE", aclName, ChangeModify, nil, map[string]string{
					"ports": newBindings,
				})
			} else {
				// ACL doesn't exist - create table entry from generated fields
				cs.Add(e.Table, e.Key, ChangeAdd, nil, e.Fields)
				// Add rules with prefix-list expansion
				filterName := svc.IngressFilter
				if aclName == egressACLName {
					filterName = svc.EgressFilter
				}
				filterSpec, _ := i.Network().GetFilterSpec(filterName)
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

		cs.Add(e.Table, e.Key, ChangeAdd, nil, e.Fields)
	}

	// QoS device-wide tables (not in shared generator, which only emits per-interface entries)
	if policyName, policy := resolveServiceQoSPolicy(i.Network(), svc); policy != nil {
		for _, entry := range generateQoSDeviceEntries(policyName, policy) {
			cs.Add(entry.Table, entry.Key, ChangeAdd, nil, entry.Fields)
		}
	}

	// Add route policies (ROUTE_MAP, PREFIX_SET, COMMUNITY_SET) — these are only
	// needed in the incremental path, not in topology provisioner.
	var bgpNeighborIP string
	if svc.Routing != nil && svc.Routing.Protocol == spec.RoutingProtocolBGP {
		bgpNeighborIP, err = i.addBGPRoutePolicies(cs, svc, opts)
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
		vrfName = svc.IPVPN
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
	cs.Add("NEWTRON_SERVICE_BINDING", i.name, ChangeAdd, nil, bindingFields)

	// Update local state
	i.serviceName = serviceName
	i.serviceIP = opts.IPAddress
	i.serviceVRF = vrfName
	i.serviceIPVPN = svc.IPVPN
	i.serviceMACVPN = svc.MACVPN
	i.ingressACL = ingressACLName
	i.egressACL = egressACLName
	if opts.IPAddress != "" {
		i.ipAddresses = append(i.ipAddresses, opts.IPAddress)
	}
	i.vrf = vrfName

	util.WithDevice(d.Name()).Infof("Applied service '%s' to interface %s", serviceName, i.name)
	return cs, nil
}

// addBGPRoutePolicies adds route policy entries (ROUTE_MAP, PREFIX_SET, COMMUNITY_SET)
// and redistribution config for BGP services.  The BGP_NEIGHBOR and BGP_NEIGHBOR_AF
// entries are now generated by GenerateServiceEntries in service_gen.go.
//
// Returns the neighbor IP (for the service binding record).
func (i *Interface) addBGPRoutePolicies(cs *ChangeSet, svc *spec.ServiceSpec, opts ApplyServiceOpts) (string, error) {
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
		vrfName = util.DeriveVRFName(svc.VRFType, svc.Description, i.name)
	} else if svc.VRFType == spec.VRFTypeShared {
		vrfName = svc.IPVPN
	}
	vrfKey := "default"
	if vrfName != "" {
		vrfKey = vrfName
	}

	// Build route-map references for the BGP_NEIGHBOR_AF entry that was
	// already created by GenerateServiceEntries.  We add them as a modify
	// to layer on route_map_in / route_map_out.
	afFields := map[string]string{}

	if routing.ImportPolicy != "" {
		rmName := i.addRoutePolicyConfig(cs, svc.Description, "import", routing.ImportPolicy, routing.ImportCommunity, routing.ImportPrefixList)
		if rmName != "" {
			afFields["route_map_in"] = rmName
		}
	} else if routing.ImportCommunity != "" || routing.ImportPrefixList != "" {
		rmName := i.addInlineRoutePolicy(cs, svc.Description, "import", routing.ImportCommunity, routing.ImportPrefixList)
		if rmName != "" {
			afFields["route_map_in"] = rmName
		}
	}

	if routing.ExportPolicy != "" {
		rmName := i.addRoutePolicyConfig(cs, svc.Description, "export", routing.ExportPolicy, routing.ExportCommunity, routing.ExportPrefixList)
		if rmName != "" {
			afFields["route_map_out"] = rmName
		}
	} else if routing.ExportCommunity != "" || routing.ExportPrefixList != "" {
		rmName := i.addInlineRoutePolicy(cs, svc.Description, "export", routing.ExportCommunity, routing.ExportPrefixList)
		if rmName != "" {
			afFields["route_map_out"] = rmName
		}
	}

	// Merge route-map references into the BGP_NEIGHBOR_AF entry
	if len(afFields) > 0 && peerIP != "" {
		afKey := fmt.Sprintf("%s|%s|ipv4_unicast", vrfKey, peerIP)
		cs.Add("BGP_NEIGHBOR_AF", afKey, ChangeModify, nil, afFields)
	}

	// Override default redistribution if specified
	if routing.Redistribute != nil {
		redistKey := fmt.Sprintf("%s|ipv4_unicast", vrfKey)
		if *routing.Redistribute {
			cs.Add("BGP_GLOBALS_AF", redistKey, ChangeModify, nil, map[string]string{
				"redistribute_connected": "true",
				"redistribute_static":    "true",
			})
		} else {
			cs.Add("BGP_GLOBALS_AF", redistKey, ChangeModify, nil, map[string]string{
				"redistribute_connected": "false",
				"redistribute_static":    "false",
			})
		}
	}

	return peerIP, nil
}

// addRoutePolicyConfig translates a named RoutePolicy into CONFIG_DB ROUTE_MAP,
// PREFIX_SET, and COMMUNITY_SET entries. Returns the generated route-map name.
func (i *Interface) addRoutePolicyConfig(cs *ChangeSet, serviceName, direction, policyName, extraCommunity, extraPrefixList string) string {
	policy, err := i.Network().GetRoutePolicy(policyName)
	if err != nil {
		util.WithDevice(i.device.Name()).Warnf("Route policy '%s' not found: %v", policyName, err)
		return ""
	}

	rmName := fmt.Sprintf("svc-%s-%s", sanitizeName(serviceName), direction)

	for _, rule := range policy.Rules {
		ruleKey := fmt.Sprintf("%s|%d", rmName, rule.Sequence)
		fields := map[string]string{
			"route_operation": rule.Action,
		}

		if rule.PrefixList != "" {
			prefixSetName := fmt.Sprintf("%s-pl-%d", rmName, rule.Sequence)
			i.addPrefixSetFromList(cs, prefixSetName, rule.PrefixList)
			fields["match_prefix_set"] = prefixSetName
		}

		if rule.Community != "" {
			csName := fmt.Sprintf("%s-cs-%d", rmName, rule.Sequence)
			cs.Add("COMMUNITY_SET", csName, ChangeAdd, nil, map[string]string{
				"set_type":         "standard",
				"match_action":     "any",
				"community_member": rule.Community,
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

		cs.Add("ROUTE_MAP", ruleKey, ChangeAdd, nil, fields)
	}

	// Extra community AND condition from service routing spec
	if extraCommunity != "" {
		csName := fmt.Sprintf("%s-extra-cs", rmName)
		cs.Add("COMMUNITY_SET", csName, ChangeAdd, nil, map[string]string{
			"set_type":         "standard",
			"match_action":     "any",
			"community_member": extraCommunity,
		})
		extraFields := map[string]string{
			"route_operation": "permit",
			"match_community": csName,
		}
		if direction == "export" {
			extraFields["set_community"] = extraCommunity
		}
		cs.Add("ROUTE_MAP", fmt.Sprintf("%s|9000", rmName), ChangeAdd, nil, extraFields)
	}

	// Extra prefix list AND condition
	if extraPrefixList != "" {
		plName := fmt.Sprintf("%s-extra-pl", rmName)
		i.addPrefixSetFromList(cs, plName, extraPrefixList)
		cs.Add("ROUTE_MAP", fmt.Sprintf("%s|9100", rmName), ChangeAdd, nil, map[string]string{
			"route_operation":  "permit",
			"match_prefix_set": plName,
		})
	}

	return rmName
}

// addInlineRoutePolicy creates a route-map from standalone community/prefix filters.
func (i *Interface) addInlineRoutePolicy(cs *ChangeSet, serviceName, direction, community, prefixList string) string {
	rmName := fmt.Sprintf("svc-%s-%s", sanitizeName(serviceName), direction)
	seq := 10

	if community != "" {
		csName := fmt.Sprintf("%s-cs", rmName)
		cs.Add("COMMUNITY_SET", csName, ChangeAdd, nil, map[string]string{
			"set_type":         "standard",
			"match_action":     "any",
			"community_member": community,
		})
		fields := map[string]string{
			"route_operation": "permit",
			"match_community": csName,
		}
		if direction == "export" {
			fields["set_community"] = community
		}
		cs.Add("ROUTE_MAP", fmt.Sprintf("%s|%d", rmName, seq), ChangeAdd, nil, fields)
		seq += 10
	}

	if prefixList != "" {
		plName := fmt.Sprintf("%s-pl", rmName)
		i.addPrefixSetFromList(cs, plName, prefixList)
		cs.Add("ROUTE_MAP", fmt.Sprintf("%s|%d", rmName, seq), ChangeAdd, nil, map[string]string{
			"route_operation":  "permit",
			"match_prefix_set": plName,
		})
	}

	return rmName
}

// addPrefixSetFromList resolves a prefix list and creates PREFIX_SET entries.
func (i *Interface) addPrefixSetFromList(cs *ChangeSet, prefixSetName, prefixListName string) {
	prefixes, err := i.Network().GetPrefixList(prefixListName)
	if err != nil || len(prefixes) == 0 {
		util.WithDevice(i.device.Name()).Warnf("Prefix list '%s' not found or empty", prefixListName)
		return
	}
	for seq, prefix := range prefixes {
		entryKey := fmt.Sprintf("%s|%d", prefixSetName, (seq+1)*10)
		cs.Add("PREFIX_SET", entryKey, ChangeAdd, nil, map[string]string{
			"ip_prefix": prefix,
			"action":    "permit",
		})
	}
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
				var ruleKey string
				if len(srcIPs) == 1 && len(dstIPs) == 1 {
					ruleKey = fmt.Sprintf("%s|RULE_%d", aclName, rule.Sequence)
				} else {
					// Multiple rules from prefix list expansion
					ruleKey = fmt.Sprintf("%s|RULE_%d_%d", aclName, rule.Sequence, ruleIdx)
					ruleIdx++
				}

				fields := buildACLRuleFields(rule, srcIP, dstIP)
				cs.Add("ACL_RULE", ruleKey, ChangeAdd, nil, fields)
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

	prefixes, err := i.Network().GetPrefixList(prefixListName)
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
	configDB := i.device.ConfigDB()
	if configDB == nil {
		return
	}

	if _, ok := configDB.ACLTable[aclName]; !ok {
		return
	}

	if depCheck.IsLastACLUser(aclName) {
		// Last user - delete all ACL rules and table
		prefix := aclName + "|"
		for ruleKey := range configDB.ACLRule {
			if strings.HasPrefix(ruleKey, prefix) {
				cs.Add("ACL_RULE", ruleKey, ChangeDelete, nil, nil)
			}
		}
		cs.Add("ACL_TABLE", aclName, ChangeDelete, nil, nil)
	} else {
		// Other users exist - just remove this interface from the binding list
		remainingBindings := depCheck.GetACLRemainingInterfaces(aclName)
		cs.Add("ACL_TABLE", aclName, ChangeModify, nil, map[string]string{
			"ports": remainingBindings,
		})
	}
}

// RemoveService removes the service from this interface.
// Uses the stored service binding (NEWTRON_SERVICE_BINDING) to know exactly
// what was applied and needs to be removed.
// Shared resources (ACLs, VLANs) are only deleted when this is the last user.
func (i *Interface) RemoveService(ctx context.Context) (*ChangeSet, error) {
	d := i.device

	if err := d.precondition("remove-service", i.name).Result(); err != nil {
		return nil, err
	}
	if !i.HasService() {
		return nil, fmt.Errorf("interface %s has no service to remove", i.name)
	}

	cs := NewChangeSet(d.Name(), "interface.remove-service")
	configDB := d.ConfigDB()

	// Create dependency checker to determine what can be safely deleted
	depCheck := NewDependencyChecker(d, i.name)

	// Get service definition for cleanup logic
	svc, _ := i.Network().GetService(i.serviceName)

	// Resolve VPN definitions - prefer stored binding, fall back to service lookup
	var ipvpnDef *spec.IPVPNSpec
	var macvpnDef *spec.MACVPNSpec

	if i.serviceIPVPN != "" {
		ipvpnDef, _ = i.Network().GetIPVPN(i.serviceIPVPN)
	} else if svc != nil && svc.IPVPN != "" {
		ipvpnDef, _ = i.Network().GetIPVPN(svc.IPVPN)
	}

	if i.serviceMACVPN != "" {
		macvpnDef, _ = i.Network().GetMACVPN(i.serviceMACVPN)
	} else if svc != nil && svc.MACVPN != "" {
		macvpnDef, _ = i.Network().GetMACVPN(svc.MACVPN)
	}

	// Check if this is the last interface using this service (for shared resources)
	isLastServiceUser := depCheck.IsLastServiceUser(i.serviceName)

	// =========================================================================
	// Per-interface resources (always delete)
	// =========================================================================

	// Remove QoS mapping and per-interface QUEUE entries
	if configDB != nil {
		if _, ok := configDB.PortQoSMap[i.name]; ok {
			cs.Add("PORT_QOS_MAP", i.name, ChangeDelete, nil, nil)
		}
	}
	// Delete QUEUE entries for this interface (QUEUE|{intf}|{N})
	if svc != nil {
		if _, policy := resolveServiceQoSPolicy(i.Network(), svc); policy != nil {
			for qi := range policy.Queues {
				queueKey := fmt.Sprintf("%s|%d", i.name, qi)
				cs.Add("QUEUE", queueKey, ChangeDelete, nil, nil)
			}
		}
	}

	// Remove IP addresses from interface
	for _, ipAddr := range i.ipAddresses {
		ipKey := fmt.Sprintf("%s|%s", i.name, ipAddr)
		cs.Add("INTERFACE", ipKey, ChangeDelete, nil, nil)
	}

	// =========================================================================
	// Per-service resources (delete only if last user)
	// =========================================================================

	// Handle shared ACLs
	if i.ingressACL != "" {
		i.removeSharedACL(cs, depCheck, i.ingressACL)
	}
	if i.egressACL != "" {
		i.removeSharedACL(cs, depCheck, i.egressACL)
	}

	// =========================================================================
	// Per-interface VRF (vrf_type: interface)
	// =========================================================================

	if i.vrf != "" && i.vrf != "default" {
		cs.Add("INTERFACE", i.name, ChangeModify, nil, map[string]string{
			"vrf_name": "",
		})

		// Per-interface VRF: delete VRF and related config
		if svc != nil && svc.VRFType == spec.VRFTypeInterface {
			vrfName := util.DeriveVRFName(svc.VRFType, i.serviceName, i.name)

			// Remove BGP EVPN config for this VRF
			if ipvpnDef != nil && ipvpnDef.L3VNI > 0 {
				vniKey := fmt.Sprintf("%s|%d", vrfName, ipvpnDef.L3VNI)
				cs.Add("BGP_EVPN_VNI", vniKey, ChangeDelete, nil, nil)

				afKey := fmt.Sprintf("%s|l2vpn_evpn", vrfName)
				cs.Add("BGP_GLOBALS_AF", afKey, ChangeDelete, nil, nil)

				mapKey := fmt.Sprintf("vtep1|map_%d_%s", ipvpnDef.L3VNI, vrfName)
				cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeDelete, nil, nil)
			}

			cs.Add("VRF", vrfName, ChangeDelete, nil, nil)
		}

		// Shared VRF: delete when last ipvpn user is removed.
		// The shared VRF was auto-created by the first service apply and should
		// be cleaned up when no service bindings reference the ipvpn anymore.
		if svc != nil && svc.VRFType == spec.VRFTypeShared && svc.IPVPN != "" {
			if depCheck.IsLastIPVPNUser(svc.IPVPN) {
				sharedVRF := svc.IPVPN

				if ipvpnDef != nil && ipvpnDef.L3VNI > 0 {
					vniKey := fmt.Sprintf("%s|%d", sharedVRF, ipvpnDef.L3VNI)
					cs.Add("BGP_EVPN_VNI", vniKey, ChangeDelete, nil, nil)

					afKey := fmt.Sprintf("%s|l2vpn_evpn", sharedVRF)
					cs.Add("BGP_GLOBALS_AF", afKey, ChangeDelete, nil, nil)

					mapKey := fmt.Sprintf("vtep1|map_%d_%s", ipvpnDef.L3VNI, sharedVRF)
					cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeDelete, nil, nil)
				}

				cs.Add("VRF", sharedVRF, ChangeDelete, nil, nil)
			}
		}
	}

	// =========================================================================
	// Per-VLAN resources (delete only if last VLAN member)
	// =========================================================================

	if svc != nil && macvpnDef != nil {
		switch svc.ServiceType {
		case spec.ServiceTypeL2, spec.ServiceTypeIRB:
			vlanID := svc.VLAN
			vlanName := fmt.Sprintf("Vlan%d", vlanID)

			// Always remove this interface's VLAN membership
			memberKey := fmt.Sprintf("%s|%s", vlanName, i.name)
			cs.Add("VLAN_MEMBER", memberKey, ChangeDelete, nil, nil)

			// Check if this is the last VLAN member
			if depCheck.IsLastVLANMember(vlanID) {
				// Last member - clean up all VLAN-related config

				// SVI (for IRB)
				if svc.ServiceType == spec.ServiceTypeIRB {
					if svc.AnycastGateway != "" {
						sviIPKey := fmt.Sprintf("%s|%s", vlanName, svc.AnycastGateway)
						cs.Add("VLAN_INTERFACE", sviIPKey, ChangeDelete, nil, nil)
					}
					cs.Add("VLAN_INTERFACE", vlanName, ChangeDelete, nil, nil)
				}

				// ARP suppression
				if macvpnDef.ARPSuppression {
					cs.Add("SUPPRESS_VLAN_NEIGH", vlanName, ChangeDelete, nil, nil)
				}

				// L2VNI mapping
				if macvpnDef.L2VNI > 0 {
					mapKey := fmt.Sprintf("vtep1|map_%d_%s", macvpnDef.L2VNI, vlanName)
					cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeDelete, nil, nil)
				}

				// VLAN itself
				cs.Add("VLAN", vlanName, ChangeDelete, nil, nil)
			}
		}
	}

	// =========================================================================
	// Service binding tracking (always delete)
	// =========================================================================

	cs.Add("NEWTRON_SERVICE_BINDING", i.name, ChangeDelete, nil, nil)

	// Log if this was the last user of the service
	if isLastServiceUser {
		util.WithDevice(d.Name()).Infof("Last interface removed from service '%s' - all service resources cleaned up", i.serviceName)
	}

	// Clear local state
	prevService := i.serviceName
	i.serviceName = ""
	i.serviceIP = ""
	i.serviceVRF = ""
	i.serviceIPVPN = ""
	i.serviceMACVPN = ""
	i.ingressACL = ""
	i.egressACL = ""
	i.ipAddresses = nil
	i.vrf = ""

	util.WithDevice(d.Name()).Infof("Removed service '%s' from interface %s", prevService, i.name)
	return cs, nil
}

// RefreshService reapplies the service configuration to sync with the service definition.
// This is useful when the service definition has changed.
func (i *Interface) RefreshService(ctx context.Context) (*ChangeSet, error) {
	d := i.device

	if err := d.precondition("refresh-service", i.name).Result(); err != nil {
		return nil, err
	}
	if !i.HasService() {
		return nil, fmt.Errorf("interface %s has no service to refresh", i.name)
	}

	// Get current service binding
	serviceName := i.serviceName
	serviceIP := i.serviceIP

	// Remove the current service
	removeCS, err := i.RemoveService(ctx)
	if err != nil {
		return nil, fmt.Errorf("removing old service: %w", err)
	}

	// Reapply the service with current definition
	// Note: PeerAS is 0 here since we're refreshing an existing service
	// and the BGP neighbor would already be configured
	applyCS, err := i.ApplyService(ctx, serviceName, ApplyServiceOpts{IPAddress: serviceIP})
	if err != nil {
		return nil, fmt.Errorf("reapplying service: %w", err)
	}

	// Merge the change sets
	cs := NewChangeSet(d.Name(), "interface.refresh-service")
	for _, change := range removeCS.Changes {
		cs.Changes = append(cs.Changes, change)
	}
	for _, change := range applyCS.Changes {
		cs.Changes = append(cs.Changes, change)
	}

	util.WithDevice(d.Name()).Infof("Refreshed service '%s' on interface %s", serviceName, i.name)
	return cs, nil
}
