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

// createServiceBindingConfig returns the NEWTRON_SERVICE_BINDING entry for tracking what service
// is applied to an interface.
func createServiceBindingConfig(intfName string, fields map[string]string) sonic.Entry {
	return sonic.Entry{Table: "NEWTRON_SERVICE_BINDING", Key: intfName, Fields: fields}
}

// bindingInt parses a string field from a service binding record as int (0 if absent/invalid).
func bindingInt(s string) int {
	v, _ := strconv.Atoi(s)
	return v
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
			return nil, fmt.Errorf("service '%s' (%s) requires EVPN overlay, but no VTEP is configured on %s — run 'newtron -D %s evpn setup' first",
				serviceName, svc.ServiceType, n.Name(), n.Name())
		}
		if !n.BGPConfigured() {
			return nil, fmt.Errorf("service '%s' (%s) requires BGP, but no BGP_GLOBALS found on %s — run 'newtron -D %s evpn setup' or provision the device first",
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

	// Service-level BGP neighbors reference a peer group named after the service
	// (Principle 36). Topology-level underlay peers do NOT use peer groups.
	peerGroup := ""
	if svc.Routing != nil && svc.Routing.Protocol == spec.RoutingProtocolBGP {
		peerGroup = serviceName
	}

	baseEntries, err := i.generateServiceEntries(ServiceEntryParams{
		ServiceName:  serviceName,
		IPAddress:    opts.IPAddress,
		VLAN:         opts.VLAN,
		Params:       opts.Params,
		PeerAS:       opts.PeerAS,
		UnderlayASN:  resolved.UnderlayASN,
		RouterID:     resolved.RouterID,
		PlatformName: resolved.Platform,
		PeerGroup:    peerGroup,
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

	// Track ACL names from generated entries for interface-merging.
	// ACL names are content-hashed from the filter spec (Principle 35).
	var ingressACLName, egressACLName string
	if svc.IngressFilter != "" {
		if filterSpec, err := n.GetFilter(svc.IngressFilter); err == nil {
			ingressACLName = util.DeriveACLName(svc.IngressFilter, "in", computeFilterHash(filterSpec))
		}
	}
	if svc.EgressFilter != "" {
		if filterSpec, err := n.GetFilter(svc.EgressFilter); err == nil {
			egressACLName = util.DeriveACLName(svc.EgressFilter, "out", computeFilterHash(filterSpec))
		}
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
				merged := unbindAclConfig(aclName, util.AddToCSV(existingACL.Ports, i.name))
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
	if pn, policy := GetServiceQoSPolicy(i.Node(), svc); policy != nil {
		qosPolicyName = pn
		for _, entry := range GenerateDeviceQoSConfig(pn, policy) {
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
	if ipvpnDef != nil && ipvpnDef.L3VNI > 0 {
		bindingFields["l3vni"] = fmt.Sprintf("%d", ipvpnDef.L3VNI)
	}
	if ipvpnDef != nil && ipvpnDef.L3VNIVlan > 0 {
		bindingFields["l3vni_vlan"] = fmt.Sprintf("%d", ipvpnDef.L3VNIVlan)
	}
	if svc.Routing != nil && svc.Routing.Redistribute != nil {
		redistVRF := "default"
		if vrfName != "" {
			redistVRF = vrfName
		}
		bindingFields["redistribute_vrf"] = redistVRF
	}

	// Self-sufficiency fields: store everything the reverse path needs so
	// RemoveService and RefreshService never re-resolve specs.
	bindingFields["service_type"] = svc.ServiceType
	if svc.VRFType != "" {
		bindingFields["vrf_type"] = svc.VRFType
	}
	if macvpnDef != nil {
		if macvpnDef.VNI > 0 {
			bindingFields["l2vni"] = fmt.Sprintf("%d", macvpnDef.VNI)
		}
		if macvpnDef.AnycastIP != "" {
			bindingFields["anycast_ip"] = macvpnDef.AnycastIP
		}
		if macvpnDef.AnycastMAC != "" {
			bindingFields["anycast_mac"] = macvpnDef.AnycastMAC
		}
		if macvpnDef.ARPSuppression {
			bindingFields["arp_suppression"] = "true"
		}
	}
	if peerGroup != "" {
		bindingFields["peer_group"] = peerGroup
	}
	// Extract resolved peer AS from the generated BGP_NEIGHBOR entry (DRY — the
	// resolution logic stays in generateBGPPeeringConfig; we read the result).
	for _, be := range baseEntries {
		if be.Table == "BGP_NEIGHBOR" {
			if asn := be.Fields["asn"]; asn != "" {
				bindingFields["bgp_peer_as"] = asn
				break
			}
		}
	}

	e := createServiceBindingConfig(i.name, bindingFields)
	cs.Add(e.Table, e.Key, e.Fields)

	n.applyShadow(cs)
	util.WithDevice(n.Name()).Infof("Applied service '%s' to interface %s", serviceName, i.name)
	return cs, nil
}

// addBGPRoutePolicies creates the BGP peer group (if first use), adds route policy
// entries (ROUTE_MAP, PREFIX_SET, COMMUNITY_SET), and attaches route maps to the
// peer group AF (Principle 36). Also handles redistribution config.
//
// Returns the neighbor IP (for the service binding record).
func (i *Interface) addBGPRoutePolicies(cs *ChangeSet, serviceName string, svc *spec.ServiceSpec, opts ApplyServiceOpts) (string, error) {
	if svc.Routing == nil || svc.Routing.Protocol != spec.RoutingProtocolBGP {
		return "", nil
	}

	routing := svc.Routing

	// Derive peer IP (same logic as generateBGPPeering, needed for return value)
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

	// Build route-map references. These go on the peer group AF (shared),
	// not on individual neighbor AF entries (Principle 36).
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

	// Create peer group + AF with route maps (Principle 36).
	// The peer group is created on first ApplyService for this service; subsequent
	// applies reuse it. The peer group must be created BEFORE the BGP_NEIGHBOR
	// that references it via peer_group_name.
	pgKey := BGPPeerGroupKey(vrfKey, serviceName)
	if _, exists := i.node.ConfigDB().BGPPeerGroup[pgKey]; !exists {
		cs.Adds(CreateBGPPeerGroupConfig(vrfKey, serviceName, afFields))
	} else if len(afFields) > 0 {
		// Peer group exists — update AF with route map references if needed
		e := sonic.Entry{
			Table:  "BGP_PEER_GROUP_AF",
			Key:    BGPPeerGroupAFKey(vrfKey, serviceName, "ipv4_unicast"),
			Fields: afFields,
		}
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
		cs.Updates(CreateBGPGlobalsAFConfig(vrfKey, "ipv4_unicast", fields))
	}

	return peerIP, nil
}

// routeMapRule holds a single route-map rule's sequence and fields,
// used during bottom-up Merkle hash computation (Principle 35).
type routeMapRule struct {
	seq    int
	fields map[string]string
}

// createRoutePolicy translates a named RoutePolicy into CONFIG_DB ROUTE_MAP,
// PREFIX_SET, and COMMUNITY_SET entries with content-hashed names (Principle 35).
// Bottom-up Merkle: PREFIX_SET/COMMUNITY_SET hashes computed first (leaves),
// then ROUTE_MAP hash includes those hashed names. Returns entries and the
// route-map name.
func (i *Interface) createRoutePolicy(serviceName, direction, policyName, extraCommunity, extraPrefixList string) ([]sonic.Entry, string) {
	policy, err := i.Node().GetRoutePolicy(policyName)
	if err != nil {
		util.WithDevice(i.node.Name()).Warnf("Route policy '%s' not found: %v", policyName, err)
		return nil, ""
	}

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
			plEntries, plName := i.createHashedPrefixSet(plBase, rule.PrefixList)
			leafEntries = append(leafEntries, plEntries...)
			if plName != "" {
				fields["match_prefix_set"] = plName
			}
		}

		if rule.Community != "" {
			csBase := fmt.Sprintf("%s_CS_%d", baseRMName, rule.Sequence)
			csFields := map[string]string{
				"set_type":         "standard",
				"match_action":     "any",
				"community_member": rule.Community,
			}
			csHash := util.ContentHash([]map[string]string{csFields})
			csName := fmt.Sprintf("%s_%s", csBase, csHash)
			leafEntries = append(leafEntries, sonic.Entry{
				Table: "COMMUNITY_SET", Key: csName, Fields: csFields,
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

		rules = append(rules, routeMapRule{seq: rule.Sequence, fields: fields})
	}

	// Extra community AND condition from service routing spec
	if extraCommunity != "" {
		csBase := fmt.Sprintf("%s_EXTRA_CS", baseRMName)
		csFields := map[string]string{
			"set_type":         "standard",
			"match_action":     "any",
			"community_member": extraCommunity,
		}
		csHash := util.ContentHash([]map[string]string{csFields})
		csName := fmt.Sprintf("%s_%s", csBase, csHash)
		leafEntries = append(leafEntries, sonic.Entry{
			Table: "COMMUNITY_SET", Key: csName, Fields: csFields,
		})
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
		plEntries, plName := i.createHashedPrefixSet(plBase, extraPrefixList)
		leafEntries = append(leafEntries, plEntries...)
		if plName != "" {
			rules = append(rules, routeMapRule{seq: 9100, fields: map[string]string{
				"route_operation":  "permit",
				"match_prefix_set": plName,
			}})
		}
	}

	// Phase 2: Compute route-map hash from all rule fields (Merkle: includes hashed leaf names).
	var rmFieldMaps []map[string]string
	for _, r := range rules {
		rmFieldMaps = append(rmFieldMaps, r.fields)
	}
	rmHash := util.ContentHash(rmFieldMaps)
	rmName := fmt.Sprintf("%s_%s", baseRMName, rmHash)

	// Phase 3: Build final entries — leaves first, then route-map rules.
	entries := make([]sonic.Entry, 0, len(leafEntries)+len(rules))
	entries = append(entries, leafEntries...)
	for _, r := range rules {
		entries = append(entries, sonic.Entry{
			Table: "ROUTE_MAP", Key: fmt.Sprintf("%s|%d", rmName, r.seq), Fields: r.fields,
		})
	}

	return entries, rmName
}

// createInlineRoutePolicy creates a route-map from standalone community/prefix
// filters with content-hashed names (Principle 35). Returns entries and the
// route-map name.
func (i *Interface) createInlineRoutePolicy(serviceName, direction, community, prefixList string) ([]sonic.Entry, string) {
	// serviceName is already normalized (uppercase, underscores) by the spec loader.
	baseRMName := fmt.Sprintf("%s_%s", serviceName, strings.ToUpper(direction))
	var leafEntries []sonic.Entry
	var rules []routeMapRule
	seq := 10

	if community != "" {
		csBase := fmt.Sprintf("%s_CS", baseRMName)
		csFields := map[string]string{
			"set_type":         "standard",
			"match_action":     "any",
			"community_member": community,
		}
		csHash := util.ContentHash([]map[string]string{csFields})
		csName := fmt.Sprintf("%s_%s", csBase, csHash)
		leafEntries = append(leafEntries, sonic.Entry{
			Table: "COMMUNITY_SET", Key: csName, Fields: csFields,
		})
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

	if prefixList != "" {
		plBase := fmt.Sprintf("%s_PL", baseRMName)
		plEntries, plName := i.createHashedPrefixSet(plBase, prefixList)
		leafEntries = append(leafEntries, plEntries...)
		if plName != "" {
			rules = append(rules, routeMapRule{seq: seq, fields: map[string]string{
				"route_operation":  "permit",
				"match_prefix_set": plName,
			}})
		}
	}

	// Compute route-map hash from all rule fields (Merkle: includes hashed leaf names).
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

// createHashedPrefixSet resolves a prefix list and returns PREFIX_SET entries
// with a content-hashed name (Principle 35). Returns entries and the hashed name.
func (i *Interface) createHashedPrefixSet(baseName, prefixListName string) ([]sonic.Entry, string) {
	prefixes, err := i.Node().GetPrefixList(prefixListName)
	if err != nil || len(prefixes) == 0 {
		util.WithDevice(i.node.Name()).Warnf("Prefix list '%s' not found or empty", prefixListName)
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

// deleteRoutePoliciesConfig returns delete entries for all ROUTE_MAP, PREFIX_SET, and
// COMMUNITY_SET entries created by a service (keyed by the deterministic prefix
// "{serviceName}_" where serviceName is already normalized by the spec loader).
func (n *Node) deleteRoutePoliciesConfig(serviceName string) []sonic.Entry {
	var entries []sonic.Entry
	if n.configDB == nil {
		return entries
	}
	// serviceName is already normalized (uppercase, underscores) by the spec loader.
	prefix := serviceName + "_"

	for key := range n.configDB.RouteMap {
		// RouteMap keys are "rmName|seq" — check if rmName starts with prefix
		parts := strings.SplitN(key, "|", 2)
		if len(parts) >= 1 && strings.HasPrefix(parts[0], prefix) {
			entries = append(entries, sonic.Entry{Table: "ROUTE_MAP", Key: key})
		}
	}
	for key := range n.configDB.PrefixSet {
		// PrefixSet keys are "setName|seq" — check if setName starts with prefix
		parts := strings.SplitN(key, "|", 2)
		if len(parts) >= 1 && strings.HasPrefix(parts[0], prefix) {
			entries = append(entries, sonic.Entry{Table: "PREFIX_SET", Key: key})
		}
	}
	for key := range n.configDB.CommunitySet {
		if strings.HasPrefix(key, prefix) {
			entries = append(entries, sonic.Entry{Table: "COMMUNITY_SET", Key: key})
		}
	}
	return entries
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
				e := createAclRuleFromFilterConfig(aclName, rule, srcIP, dstIP, suffix)
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
		cs.Deletes(i.node.deleteAclTableConfig(aclName))
	} else {
		// Other users exist — just remove this interface from the binding list
		e := unbindAclConfig(aclName, depCheck.GetACLRemainingInterfaces(aclName))
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

	// Decision fields — prefer binding (self-sufficient), fall back to spec (legacy bindings)
	serviceType := b.ServiceType
	vrfType := b.VRFType
	if serviceType == "" {
		if svc, _ := i.Node().GetService(serviceName); svc != nil {
			serviceType = svc.ServiceType
			vrfType = svc.VRFType
		}
	}

	// Derived booleans from serviceType
	canRoute := serviceType == spec.ServiceTypeRouted || serviceType == spec.ServiceTypeEVPNRouted
	canBridge := serviceType == spec.ServiceTypeEVPNIRB || serviceType == spec.ServiceTypeEVPNBridged ||
		serviceType == spec.ServiceTypeIRB || serviceType == spec.ServiceTypeBridged
	hasIRB := serviceType == spec.ServiceTypeEVPNIRB || serviceType == spec.ServiceTypeIRB

	// VLAN cleanup values — prefer binding (self-sufficient), fall back to macvpn spec (legacy)
	l2vni := bindingInt(b.L2VNI)
	anycastIP := b.AnycastIP
	anycastMAC := b.AnycastMAC
	arpSuppression := b.ARPSuppression == "true"

	if l2vni == 0 && anycastIP == "" && b.L2VNI == "" {
		// Legacy binding without self-sufficiency fields — fall back to macvpn spec
		macvpnName := b.MACVPN
		if macvpnName != "" {
			if macvpnDef, err := i.Node().GetMACVPN(macvpnName); err == nil && macvpnDef != nil {
				l2vni = macvpnDef.VNI
				anycastIP = macvpnDef.AnycastIP
				anycastMAC = macvpnDef.AnycastMAC
				arpSuppression = macvpnDef.ARPSuppression
			}
		}
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
		if !n.isQoSPolicyReferenced(b.QoSPolicy, i.name) {
			cs.Deletes(n.deleteDeviceQoSConfig(b.QoSPolicy))
		}
	}

	// Remove IP addresses from interface
	for _, ipAddr := range i.IPAddresses() {
		cs.Deletes(i.assignIpAddress(ipAddr))
	}

	// Remove INTERFACE base entry for routed services (created by service).
	// Must come after IP deletions since intfmgrd enforces parent-child ordering.
	if canRoute && (vrfName == "" || vrfName == "default") {
		cs.Deletes(i.enableIpRouting())
	}

	// Remove BGP neighbor created by this service (tracked in binding)
	if bgpNeighbor != "" {
		vrfKey := "default"
		if vrfName != "" && vrfName != "default" {
			vrfKey = vrfName
		}
		cs.Deletes(DeleteBGPNeighborConfig(vrfKey, bgpNeighbor))
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
		cs.Deletes(n.deleteRoutePoliciesConfig(serviceName))
	}

	// Remove BGP peer group (Principle 36) — created per-service, deleted when last user removed.
	// Peer group must be deleted AFTER all BGP_NEIGHBORs referencing it are deleted.
	if b.PeerGroup != "" && isLastServiceUser {
		vrfKey := "default"
		if vrfName != "" && vrfName != "default" {
			vrfKey = vrfName
		}
		cs.Deletes(DeleteBGPPeerGroupConfig(vrfKey, b.PeerGroup))
	}

	// Revert BGP_GLOBALS_AF redistribution override if this service set it.
	// For per-interface VRFs, destroyVrf cascades this anyway — harmless redundancy.
	if b.RedistributeVRF != "" && isLastServiceUser {
		cs.Updates(revertRedistributionConfig(b.RedistributeVRF))
	}

	// =========================================================================
	// Per-interface VRF (vrf_type: interface or shared)
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
		if vrfType == spec.VRFTypeInterface {
			derivedVRF := util.DeriveVRFName(vrfType, serviceName, i.name)
			l3vni, l3vniVlan := bindingInt(b.L3VNI), bindingInt(b.L3VNIVlan)
			cs.Deletes(n.destroyVrfConfig(derivedVRF, l3vni, l3vniVlan))
		}

		// Shared VRF: delete when last ipvpn user is removed.
		// The shared VRF was auto-created by the first service apply and should
		// be cleaned up when no service bindings reference the ipvpn anymore.
		if vrfType == spec.VRFTypeShared && b.IPVPN != "" {
			if depCheck.IsLastIPVPNUser(b.IPVPN) {
				l3vni, l3vniVlan := bindingInt(b.L3VNI), bindingInt(b.L3VNIVlan)
				cs.Deletes(n.destroyVrfConfig(vrfName, l3vni, l3vniVlan))
			}
		}
	}

	// =========================================================================
	// Per-VLAN resources (delete only if last VLAN member)
	// =========================================================================

	vlanID := bindingInt(b.VlanID)

	if canBridge && vlanID > 0 {
		vlanName := VLANName(vlanID)

		// Always remove this interface's VLAN membership
		cs.Deletes(deleteVlanMemberConfig(vlanID, i.name))

		// Check if this is the last VLAN member
		if depCheck.IsLastVLANMember(vlanID) {
			// Last member - clean up all VLAN-related config

			// SVI (for IRB types)
			if hasIRB {
				if anycastIP != "" {
					cs.Deletes(deleteSviIPConfig(vlanID, anycastIP))
				} else if b.IPAddress != "" {
					// Local IRB: SVI IP comes from opts.IPAddress (stored in binding)
					cs.Deletes(deleteSviIPConfig(vlanID, b.IPAddress))
				}
				cs.Deletes(deleteSviBaseConfig(vlanID))

				// SAG_GLOBAL: clean up when last anycast MAC user is removed
				if anycastMAC != "" && depCheck.IsLastAnycastMACUser() {
					cs.Deletes(deleteSagGlobalConfig())
				}
			}

			// ARP suppression
			if arpSuppression {
				cs.Deletes(disableArpSuppressionConfig(vlanName))
			}

			// VNI mapping
			if l2vni > 0 {
				cs.Deletes(deleteVniMapConfig(l2vni, vlanName))
			}

			// VLAN itself
			cs.Deletes(deleteVlanConfig(vlanID))
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

	// Capture all binding values before RemoveService deletes the binding.
	// These values are needed to reconstruct the same ApplyService call.
	b := i.binding()
	serviceName := b.ServiceName
	serviceIP := b.IPAddress
	peerAS := bindingInt(b.BGPPeerAS)
	vlanID := bindingInt(b.VlanID)

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

	// Reapply the service with preserved parameters. RemoveService deletes
	// the BGP neighbor, so PeerAS must be passed to recreate it.
	applyCS, err := i.ApplyService(ctx, serviceName, ApplyServiceOpts{
		IPAddress: serviceIP,
		PeerAS:    peerAS,
		VLAN:      vlanID,
	})
	if err != nil {
		return nil, fmt.Errorf("reapplying service: %w", err)
	}

	// Merge the change sets. The remove+apply creates overlapping delete/add
	// operations on the same keys. verifyConfigChanges handles this correctly
	// by computing final state per key (last operation wins).
	cs := NewChangeSet(n.Name(), "interface.refresh-service")
	cs.Merge(removeCS)
	cs.Merge(applyCS)

	util.WithDevice(n.Name()).Infof("Refreshed service '%s' on interface %s", serviceName, i.name)
	return cs, nil
}
