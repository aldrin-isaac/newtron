package network

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// Interface Operations - Methods on Interface
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
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked - call Lock() first")
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

// ============================================================================
// DependencyChecker - checks reverse dependencies for safe deletion
// ============================================================================

// DependencyChecker checks if resources can be safely deleted.
// Used by Interface.RemoveService() to determine when shared resources
// (ACLs, VLANs, VRFs) can be deleted vs just having this interface removed.
//
// NOTE: There is also a DependencyChecker in pkg/operations/operation.go
// for use by standalone Operation implementations. This version is the
// primary one used by interface operations.
type DependencyChecker struct {
	device           *Device
	excludeInterface string
}

// NewDependencyChecker creates a dependency checker for the given interface
func NewDependencyChecker(d *Device, excludeInterface string) *DependencyChecker {
	return &DependencyChecker{
		device:           d,
		excludeInterface: excludeInterface,
	}
}

// IsLastACLUser returns true if this is the last interface using the ACL
func (dc *DependencyChecker) IsLastACLUser(aclName string) bool {
	configDB := dc.device.ConfigDB()
	if configDB == nil {
		return true
	}

	acl, ok := configDB.ACLTable[aclName]
	if !ok {
		return true
	}

	remaining := removeInterfaceFromList(acl.Ports, dc.excludeInterface)
	return remaining == ""
}

// GetACLRemainingInterfaces returns interfaces remaining after removing this one
func (dc *DependencyChecker) GetACLRemainingInterfaces(aclName string) string {
	configDB := dc.device.ConfigDB()
	if configDB == nil {
		return ""
	}

	acl, ok := configDB.ACLTable[aclName]
	if !ok {
		return ""
	}

	return removeInterfaceFromList(acl.Ports, dc.excludeInterface)
}

// IsLastVLANMember returns true if this is the last member of the VLAN
func (dc *DependencyChecker) IsLastVLANMember(vlanID int) bool {
	configDB := dc.device.ConfigDB()
	if configDB == nil {
		return true
	}

	vlanName := fmt.Sprintf("Vlan%d", vlanID)
	count := 0
	for key := range configDB.VLANMember {
		if strings.HasPrefix(key, vlanName+"|") {
			memberPort := key[len(vlanName)+1:]
			if memberPort != dc.excludeInterface {
				count++
			}
		}
	}
	return count == 0
}

// IsLastServiceUser returns true if this is the last interface using the service
func (dc *DependencyChecker) IsLastServiceUser(serviceName string) bool {
	configDB := dc.device.ConfigDB()
	if configDB == nil {
		return true
	}

	count := 0
	for intfName, binding := range configDB.NewtronServiceBinding {
		if binding.ServiceName == serviceName && intfName != dc.excludeInterface {
			count++
		}
	}
	return count == 0
}

// IsLastIPVPNUser returns true if this is the last interface bound to a service
// that references the given ipvpn name. Used for shared VRF cleanup — the VRF
// is only deleted when no service binding references the ipvpn.
func (dc *DependencyChecker) IsLastIPVPNUser(ipvpnName string) bool {
	configDB := dc.device.ConfigDB()
	if configDB == nil {
		return true
	}

	count := 0
	for intfName, binding := range configDB.NewtronServiceBinding {
		if binding.IPVPN == ipvpnName && intfName != dc.excludeInterface {
			count++
		}
	}
	return count == 0
}

// IsLastVRFUser returns true if this is the last interface bound to the VRF
func (dc *DependencyChecker) IsLastVRFUser(vrfName string) bool {
	configDB := dc.device.ConfigDB()
	if configDB == nil {
		return true
	}

	// Count interfaces bound to this VRF
	count := 0
	for intfName, intf := range configDB.Interface {
		// Skip composite keys (with |) - those are IP bindings
		if strings.Contains(intfName, "|") {
			continue
		}
		if intf.VRFName == vrfName && intfName != dc.excludeInterface {
			count++
		}
	}
	return count == 0
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

	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
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

// SetIP configures an IP address on this interface.
func (i *Interface) SetIP(ctx context.Context, ipAddr string) (*ChangeSet, error) {
	d := i.device

	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if !util.IsValidIPv4CIDR(ipAddr) {
		return nil, fmt.Errorf("invalid IP address: %s", ipAddr)
	}
	if i.IsLAGMember() {
		return nil, fmt.Errorf("cannot configure IP on LAG member")
	}

	cs := NewChangeSet(d.Name(), "interface.set-ip")
	ipKey := fmt.Sprintf("%s|%s", i.name, ipAddr)
	cs.Add("INTERFACE", ipKey, ChangeAdd, nil, map[string]string{})

	i.ipAddresses = append(i.ipAddresses, ipAddr)

	util.WithDevice(d.Name()).Infof("Configured IP %s on interface %s", ipAddr, i.name)
	return cs, nil
}

// SetVRF binds this interface to a VRF.
func (i *Interface) SetVRF(ctx context.Context, vrfName string) (*ChangeSet, error) {
	d := i.device

	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if vrfName != "" && vrfName != "default" && !d.VRFExists(vrfName) {
		return nil, fmt.Errorf("VRF '%s' does not exist", vrfName)
	}
	if i.IsLAGMember() {
		return nil, fmt.Errorf("cannot bind LAG member to VRF")
	}

	cs := NewChangeSet(d.Name(), "interface.set-vrf")
	cs.Add("INTERFACE", i.name, ChangeModify, nil, map[string]string{
		"vrf_name": vrfName,
	})

	i.vrf = vrfName

	util.WithDevice(d.Name()).Infof("Bound interface %s to VRF %s", i.name, vrfName)
	return cs, nil
}

// BindACL binds an ACL to this interface.
// ACLs are shared - adds this interface to the ACL's binding list.
func (i *Interface) BindACL(ctx context.Context, aclName, direction string) (*ChangeSet, error) {
	d := i.device

	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if !d.ACLTableExists(aclName) {
		return nil, fmt.Errorf("ACL table '%s' does not exist", aclName)
	}
	if direction != "ingress" && direction != "egress" {
		return nil, fmt.Errorf("direction must be 'ingress' or 'egress'")
	}

	cs := NewChangeSet(d.Name(), "interface.bind-acl")

	// ACLs are shared - add this interface to existing binding list
	configDB := d.ConfigDB()
	existingACL, ok := configDB.ACLTable[aclName]
	var newBindings string
	if ok && existingACL.Ports != "" {
		newBindings = addInterfaceToList(existingACL.Ports, i.name)
	} else {
		newBindings = i.name
	}

	cs.Add("ACL_TABLE", aclName, ChangeModify, nil, map[string]string{
		"ports": newBindings,
		"stage": direction,
	})

	if direction == "ingress" {
		i.ingressACL = aclName
	} else {
		i.egressACL = aclName
	}

	util.WithDevice(d.Name()).Infof("Bound ACL %s to interface %s (%s)", aclName, i.name, direction)
	return cs, nil
}

// Configure sets basic interface properties.
func (i *Interface) Configure(ctx context.Context, opts InterfaceConfig) (*ChangeSet, error) {
	d := i.device

	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if i.IsLAGMember() {
		return nil, fmt.Errorf("cannot configure LAG member directly")
	}

	if opts.MTU > 0 {
		if err := util.ValidateMTU(opts.MTU); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), "interface.configure")
	fields := make(map[string]string)

	if opts.Description != "" {
		fields["description"] = opts.Description
	}
	if opts.MTU > 0 {
		fields["mtu"] = fmt.Sprintf("%d", opts.MTU)
		i.mtu = opts.MTU
	}
	if opts.Speed != "" {
		fields["speed"] = opts.Speed
		i.speed = opts.Speed
	}
	if opts.AdminStatus != "" {
		fields["admin_status"] = opts.AdminStatus
		i.adminStatus = opts.AdminStatus
	}

	if len(fields) > 0 {
		cs.Add("PORT", i.name, ChangeModify, nil, fields)
	}

	util.WithDevice(d.Name()).Infof("Configured interface %s: %v", i.name, fields)
	return cs, nil
}

// InterfaceConfig holds configuration options for Configure().
type InterfaceConfig struct {
	Description string
	MTU         int
	Speed       string
	AdminStatus string
}

// ============================================================================
// Direct BGP Neighbor Operations (Interface-level, uses link IP as source)
// ============================================================================
// These operations are for eBGP neighbors where the BGP session is sourced
// from the interface IP (direct peering over a link).
//
// For iBGP neighbors using loopback IPs (indirect peering), use the
// device-level BGP operations: Device.AddLoopbackBGPNeighbor() or
// Device.SetupBGPEVPN().

// DirectBGPNeighborConfig holds configuration for a direct BGP neighbor.
type DirectBGPNeighborConfig struct {
	NeighborIP  string // Neighbor IP (auto-derived for /30, /31 if empty)
	RemoteAS    int    // Remote AS number (required for eBGP)
	Description string // Optional description
	Password    string // Optional MD5 password
	BFD         bool   // Enable BFD for fast failure detection
	Multihop    int    // eBGP multihop TTL (0 = directly connected)
}

// AddBGPNeighbor adds a direct BGP neighbor on this interface.
// The BGP session will use this interface's IP as the update-source.
// For point-to-point links (/30, /31), the neighbor IP is auto-derived if not specified.
func (i *Interface) AddBGPNeighbor(ctx context.Context, cfg DirectBGPNeighborConfig) (*ChangeSet, error) {
	d := i.device

	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if cfg.RemoteAS == 0 {
		return nil, fmt.Errorf("remote AS number is required")
	}

	// Interface must have an IP address
	if len(i.ipAddresses) == 0 {
		return nil, fmt.Errorf("interface %s has no IP address configured", i.name)
	}

	// Get the interface's IP address (use first one)
	localIP := i.ipAddresses[0]

	// Auto-derive neighbor IP for point-to-point links if not specified
	neighborIP := cfg.NeighborIP
	if neighborIP == "" {
		derivedIP, err := util.DeriveNeighborIP(localIP)
		if err != nil {
			return nil, fmt.Errorf("cannot auto-derive neighbor IP from %s: %v (specify neighbor IP explicitly)", localIP, err)
		}
		neighborIP = derivedIP
	}

	// Validate neighbor IP
	if !util.IsValidIPv4(neighborIP) {
		return nil, fmt.Errorf("invalid neighbor IP: %s", neighborIP)
	}

	// Check if neighbor already exists
	if d.BGPNeighborExists(neighborIP) {
		return nil, fmt.Errorf("BGP neighbor %s already exists", neighborIP)
	}

	cs := NewChangeSet(d.Name(), "interface.add-bgp-neighbor")

	// Extract local IP without mask for update-source
	localIPOnly, _ := util.SplitIPMask(localIP)

	// Add BGP neighbor entry
	fields := map[string]string{
		"asn":          fmt.Sprintf("%d", cfg.RemoteAS),
		"admin_status": "up",
		"local_addr":   localIPOnly, // Update source = interface IP
	}
	if cfg.Description != "" {
		fields["name"] = cfg.Description
	}
	if cfg.Multihop > 0 {
		fields["ebgp_multihop"] = fmt.Sprintf("%d", cfg.Multihop)
	}
	// Note: Password and BFD would be configured separately in SONiC

	// Key format: vrf|neighborIP (per SONiC Unified FRR Mgmt schema)
	cs.Add("BGP_NEIGHBOR", fmt.Sprintf("default|%s", neighborIP), ChangeAdd, nil, fields)

	// Activate IPv4 unicast for this neighbor
	afKey := fmt.Sprintf("default|%s|ipv4_unicast", neighborIP)
	cs.Add("BGP_NEIGHBOR_AF", afKey, ChangeAdd, nil, map[string]string{
		"activate": "true",
	})

	util.WithDevice(d.Name()).Infof("Adding direct BGP neighbor %s (AS %d) on interface %s",
		neighborIP, cfg.RemoteAS, i.name)
	return cs, nil
}

// RemoveBGPNeighbor removes a direct BGP neighbor from this interface.
func (i *Interface) RemoveBGPNeighbor(ctx context.Context, neighborIP string) (*ChangeSet, error) {
	d := i.device

	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	// If no neighbor IP specified, try to derive it
	if neighborIP == "" && len(i.ipAddresses) > 0 {
		var err error
		neighborIP, err = util.DeriveNeighborIP(i.ipAddresses[0])
		if err != nil {
			return nil, fmt.Errorf("specify neighbor IP to remove")
		}
	}

	// Check neighbor exists
	if !d.BGPNeighborExists(neighborIP) {
		return nil, fmt.Errorf("BGP neighbor %s not found", neighborIP)
	}

	cs := NewChangeSet(d.Name(), "interface.remove-bgp-neighbor")

	// Remove address-family entries first
	// Key format: vrf|neighborIP|af (per SONiC Unified FRR Mgmt schema)
	for _, af := range []string{"ipv4_unicast", "ipv6_unicast", "l2vpn_evpn"} {
		afKey := fmt.Sprintf("default|%s|%s", neighborIP, af)
		cs.Add("BGP_NEIGHBOR_AF", afKey, ChangeDelete, nil, nil)
	}

	// Remove neighbor entry
	cs.Add("BGP_NEIGHBOR", fmt.Sprintf("default|%s", neighborIP), ChangeDelete, nil, nil)

	util.WithDevice(d.Name()).Infof("Removing direct BGP neighbor %s from interface %s", neighborIP, i.name)
	return cs, nil
}

// GetBGPNeighborIP returns the BGP neighbor IP for this interface (if any).
// Returns empty string if no BGP neighbor is configured on this interface.
func (i *Interface) GetBGPNeighborIP() string {
	d := i.device
	if d.ConfigDB() == nil || len(i.ipAddresses) == 0 {
		return ""
	}

	localIP, _ := util.SplitIPMask(i.ipAddresses[0])

	// Find a BGP neighbor that uses this interface's IP as local_addr
	for neighborIP, neighbor := range d.ConfigDB().BGPNeighbor {
		if neighbor.LocalAddr == localIP {
			return neighborIP
		}
	}

	return ""
}

// ============================================================================
// MAC-VPN (L2 EVPN) Operations
// ============================================================================

// BindMACVPN binds this VLAN interface to a MAC-VPN definition.
// This configures the L2VNI mapping and ARP suppression from the macvpn definition.
func (i *Interface) BindMACVPN(ctx context.Context, macvpnName string, macvpnDef *spec.MACVPNSpec) (*ChangeSet, error) {
	d := i.device

	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if !i.IsVLAN() {
		return nil, fmt.Errorf("bind-macvpn only valid for VLAN interfaces")
	}
	if !d.VTEPExists() {
		return nil, fmt.Errorf("MAC-VPN requires VTEP configuration")
	}

	cs := NewChangeSet(d.Name(), "interface.bind-macvpn")

	vlanName := i.name // e.g., "Vlan100"

	// Add L2VNI mapping
	if macvpnDef.L2VNI > 0 {
		mapKey := fmt.Sprintf("vtep1|map_%d_%s", macvpnDef.L2VNI, vlanName)
		cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeAdd, nil, map[string]string{
			"vlan": vlanName,
			"vni":  fmt.Sprintf("%d", macvpnDef.L2VNI),
		})
	}

	// Configure ARP suppression
	if macvpnDef.ARPSuppression {
		cs.Add("SUPPRESS_VLAN_NEIGH", vlanName, ChangeAdd, nil, map[string]string{
			"suppress": "on",
		})
	}

	util.WithDevice(d.Name()).Infof("Bound MAC-VPN '%s' to %s (L2VNI: %d)", macvpnName, vlanName, macvpnDef.L2VNI)
	return cs, nil
}

// UnbindMACVPN removes the MAC-VPN binding from this VLAN interface.
// This removes the L2VNI mapping and ARP suppression settings.
func (i *Interface) UnbindMACVPN(ctx context.Context) (*ChangeSet, error) {
	d := i.device

	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if !i.IsVLAN() {
		return nil, fmt.Errorf("unbind-macvpn only valid for VLAN interfaces")
	}

	cs := NewChangeSet(d.Name(), "interface.unbind-macvpn")

	vlanName := i.name
	configDB := d.ConfigDB()

	// Remove L2VNI mapping
	if configDB != nil {
		for key, mapping := range configDB.VXLANTunnelMap {
			if mapping.VLAN == vlanName {
				cs.Add("VXLAN_TUNNEL_MAP", key, ChangeDelete, nil, nil)
				break
			}
		}
	}

	// Remove ARP suppression
	if configDB != nil {
		if _, ok := configDB.SuppressVLANNeigh[vlanName]; ok {
			cs.Add("SUPPRESS_VLAN_NEIGH", vlanName, ChangeDelete, nil, nil)
		}
	}

	util.WithDevice(d.Name()).Infof("Unbound MAC-VPN from %s", vlanName)
	return cs, nil
}

// ============================================================================
// Generic Property Setting
// ============================================================================

// Set sets a property on this interface.
// Supported properties: mtu, speed, admin-status, description
func (i *Interface) Set(ctx context.Context, property, value string) (*ChangeSet, error) {
	d := i.device

	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if i.IsLAGMember() {
		return nil, fmt.Errorf("cannot configure LAG member directly - configure the parent LAG")
	}

	cs := NewChangeSet(d.Name(), "interface.set")
	fields := make(map[string]string)

	switch property {
	case "mtu":
		mtuVal := 0
		if _, err := fmt.Sscanf(value, "%d", &mtuVal); err != nil {
			return nil, fmt.Errorf("invalid MTU value: %s", value)
		}
		if err := util.ValidateMTU(mtuVal); err != nil {
			return nil, err
		}
		fields["mtu"] = value
		i.mtu = mtuVal

	case "speed":
		// Validate speed format (e.g., 10G, 25G, 40G, 100G)
		validSpeeds := map[string]bool{
			"1G": true, "10G": true, "25G": true, "40G": true, "50G": true, "100G": true, "200G": true, "400G": true,
		}
		if !validSpeeds[value] {
			return nil, fmt.Errorf("invalid speed: %s (valid: 1G, 10G, 25G, 40G, 50G, 100G, 200G, 400G)", value)
		}
		fields["speed"] = value
		i.speed = value

	case "admin-status", "admin_status":
		if value != "up" && value != "down" {
			return nil, fmt.Errorf("admin-status must be 'up' or 'down'")
		}
		fields["admin_status"] = value
		i.adminStatus = value

	case "description":
		fields["description"] = value

	default:
		return nil, fmt.Errorf("unknown property: %s (valid: mtu, speed, admin-status, description)", property)
	}

	// Determine which table to update based on interface type
	tableName := "PORT"
	if i.IsPortChannel() {
		tableName = "PORTCHANNEL"
	}

	cs.Add(tableName, i.name, ChangeModify, nil, fields)

	util.WithDevice(d.Name()).Infof("Set %s=%s on interface %s", property, value, i.name)
	return cs, nil
}

// ============================================================================
// LAG/VLAN Member Operations
// ============================================================================

// AddMember adds a member interface to this LAG or VLAN.
// For VLANs, the tagged parameter controls tagging mode.
func (i *Interface) AddMember(ctx context.Context, memberIntf string, tagged bool) (*ChangeSet, error) {
	d := i.device

	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	// Normalize member interface name (e.g., Eth0 -> Ethernet0)
	memberIntf = util.NormalizeInterfaceName(memberIntf)

	// Must be a PortChannel or VLAN
	if !i.IsPortChannel() && !i.IsVLAN() {
		return nil, fmt.Errorf("add-member only valid for PortChannel or VLAN interfaces")
	}

	// Validate member interface exists
	memberIntfObj, err := d.GetInterface(memberIntf)
	if err != nil {
		return nil, fmt.Errorf("member interface '%s' not found: %w", memberIntf, err)
	}

	cs := NewChangeSet(d.Name(), "interface.add-member")

	if i.IsPortChannel() {
		// LAG member addition
		// Member must be a physical interface
		if !memberIntfObj.IsPhysical() {
			return nil, fmt.Errorf("LAG members must be physical interfaces")
		}
		// Member must not already be in a LAG
		if memberIntfObj.IsLAGMember() {
			return nil, fmt.Errorf("interface %s is already a member of %s", memberIntf, memberIntfObj.LAGParent())
		}

		memberKey := fmt.Sprintf("%s|%s", i.name, memberIntf)
		cs.Add("PORTCHANNEL_MEMBER", memberKey, ChangeAdd, nil, map[string]string{})

		util.WithDevice(d.Name()).Infof("Added %s to LAG %s", memberIntf, i.name)

	} else if i.IsVLAN() {
		// VLAN member addition
		taggingMode := "untagged"
		if tagged {
			taggingMode = "tagged"
		}

		memberKey := fmt.Sprintf("%s|%s", i.name, memberIntf)
		cs.Add("VLAN_MEMBER", memberKey, ChangeAdd, nil, map[string]string{
			"tagging_mode": taggingMode,
		})

		util.WithDevice(d.Name()).Infof("Added %s to VLAN %s (%s)", memberIntf, i.name, taggingMode)
	}

	return cs, nil
}

// RemoveMember removes a member interface from this LAG or VLAN.
func (i *Interface) RemoveMember(ctx context.Context, memberIntf string) (*ChangeSet, error) {
	d := i.device

	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	// Normalize member interface name (e.g., Eth0 -> Ethernet0)
	memberIntf = util.NormalizeInterfaceName(memberIntf)

	// Must be a PortChannel or VLAN
	if !i.IsPortChannel() && !i.IsVLAN() {
		return nil, fmt.Errorf("remove-member only valid for PortChannel or VLAN interfaces")
	}

	configDB := d.ConfigDB()
	cs := NewChangeSet(d.Name(), "interface.remove-member")

	if i.IsPortChannel() {
		// LAG member removal
		memberKey := fmt.Sprintf("%s|%s", i.name, memberIntf)
		if configDB != nil {
			if _, ok := configDB.PortChannelMember[memberKey]; !ok {
				return nil, fmt.Errorf("interface %s is not a member of %s", memberIntf, i.name)
			}
		}

		cs.Add("PORTCHANNEL_MEMBER", memberKey, ChangeDelete, nil, nil)

		util.WithDevice(d.Name()).Infof("Removed %s from LAG %s", memberIntf, i.name)

	} else if i.IsVLAN() {
		// VLAN member removal
		memberKey := fmt.Sprintf("%s|%s", i.name, memberIntf)
		if configDB != nil {
			if _, ok := configDB.VLANMember[memberKey]; !ok {
				return nil, fmt.Errorf("interface %s is not a member of %s", memberIntf, i.name)
			}
		}

		cs.Add("VLAN_MEMBER", memberKey, ChangeDelete, nil, nil)

		util.WithDevice(d.Name()).Infof("Removed %s from VLAN %s", memberIntf, i.name)
	}

	return cs, nil
}

// ============================================================================
// Service Refresh
// ============================================================================

// RefreshService reapplies the service configuration to sync with the service definition.
// This is useful when the service definition has changed.
func (i *Interface) RefreshService(ctx context.Context) (*ChangeSet, error) {
	d := i.device

	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
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

// ============================================================================
// ACL Unbinding
// ============================================================================

// UnbindACL removes an ACL binding from this interface.
func (i *Interface) UnbindACL(ctx context.Context, aclName string) (*ChangeSet, error) {
	d := i.device

	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}

	configDB := d.ConfigDB()
	if configDB == nil {
		return nil, fmt.Errorf("config not loaded")
	}

	acl, ok := configDB.ACLTable[aclName]
	if !ok {
		return nil, fmt.Errorf("ACL '%s' not found", aclName)
	}

	// Check if this interface is actually bound to this ACL
	ports := strings.Split(acl.Ports, ",")
	found := false
	for _, p := range ports {
		if strings.TrimSpace(p) == i.name {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("ACL '%s' is not bound to interface %s", aclName, i.name)
	}

	cs := NewChangeSet(d.Name(), "interface.unbind-acl")
	depCheck := NewDependencyChecker(d, i.name)

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

	// Update local state
	if acl.Stage == "ingress" {
		i.ingressACL = ""
	} else if acl.Stage == "egress" {
		i.egressACL = ""
	}

	util.WithDevice(d.Name()).Infof("Unbound ACL %s from interface %s", aclName, i.name)
	return cs, nil
}

// ============================================================================
// v3: Route Map Binding
// ============================================================================

// SetRouteMap binds a route-map to a BGP neighbor's address-family (in/out direction).
// Used to apply import/export policies from the service routing spec.
func (i *Interface) SetRouteMap(ctx context.Context, neighborIP, af, direction, routeMapName string) (*ChangeSet, error) {
	d := i.device

	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return nil, fmt.Errorf("device not locked")
	}
	if direction != "in" && direction != "out" {
		return nil, fmt.Errorf("direction must be 'in' or 'out'")
	}

	// Verify neighbor exists
	if !d.BGPNeighborExists(neighborIP) {
		return nil, fmt.Errorf("BGP neighbor %s not found", neighborIP)
	}

	// Verify route-map exists in CONFIG_DB
	configDB := d.ConfigDB()
	if configDB != nil {
		found := false
		prefix := routeMapName + "|"
		for key := range configDB.RouteMap {
			if key == routeMapName || len(key) > len(prefix) && key[:len(prefix)] == prefix {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("route-map '%s' not found in CONFIG_DB", routeMapName)
		}
	}

	cs := NewChangeSet(d.Name(), "interface.set-route-map")

	// Key format: vrf|neighborIP|af (per SONiC Unified FRR Mgmt schema)
	afKey := fmt.Sprintf("default|%s|%s", neighborIP, af)
	field := "route_map_in"
	if direction == "out" {
		field = "route_map_out"
	}

	cs.Add("BGP_NEIGHBOR_AF", afKey, ChangeModify, nil, map[string]string{
		field: routeMapName,
	})

	util.WithDevice(d.Name()).Infof("Set route-map %s %s on neighbor %s AF %s",
		routeMapName, direction, neighborIP, af)
	return cs, nil
}

// ============================================================================
// BGP Neighbor Helpers
// ============================================================================

// BGPNeighborConfig holds configuration for adding a BGP neighbor.
type BGPNeighborConfig struct {
	NeighborIP  string // Neighbor IP address
	RemoteASN   int    // Remote AS number
	Passive     bool   // Passive mode (wait for incoming connection)
	TTL         int    // eBGP multihop TTL
	Description string // Optional description
}

// DeriveNeighborIP derives the BGP neighbor IP from this interface's IP address.
// Only works for point-to-point links (/30 or /31 subnets).
func (i *Interface) DeriveNeighborIP() (string, error) {
	if len(i.ipAddresses) == 0 {
		return "", fmt.Errorf("interface %s has no IP address", i.name)
	}
	return util.DeriveNeighborIP(i.ipAddresses[0])
}

// AddBGPNeighborWithConfig adds a BGP neighbor using the provided config.
// This is an alternative to AddBGPNeighbor with more options.
func (i *Interface) AddBGPNeighborWithConfig(ctx context.Context, cfg BGPNeighborConfig) (*ChangeSet, error) {
	// Convert to DirectBGPNeighborConfig and delegate
	directCfg := DirectBGPNeighborConfig{
		NeighborIP:  cfg.NeighborIP,
		RemoteAS:    cfg.RemoteASN,
		Description: cfg.Description,
		Multihop:    cfg.TTL,
	}

	// Handle passive mode - note: SONiC may not support this directly
	// For now, we'll create the neighbor but it won't be passive
	if cfg.Passive && cfg.NeighborIP == "" {
		return nil, fmt.Errorf("passive mode requires a neighbor IP in SONiC")
	}

	return i.AddBGPNeighbor(ctx, directCfg)
}
