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
	if err := n.precondition(sonic.OpApplyService, i.name).Result(); err != nil {
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

	// Determine which layers this service uses
	canBridge := svc.ServiceType == spec.ServiceTypeEVPNIRB || svc.ServiceType == spec.ServiceTypeEVPNBridged ||
		svc.ServiceType == spec.ServiceTypeIRB || svc.ServiceType == spec.ServiceTypeBridged

	// Resolve profile early — needed for BGP prerequisite and entry generation.
	resolved := n.Resolved()

	// BGP prerequisite: check if BGP_GLOBALS is needed for the default VRF.
	// Without BGP_GLOBALS, frrcfgd has no `router bgp` process and silently
	// ignores BGP_NEIGHBOR entries. Entries are added to the service ChangeSet
	// (not a separate operation) so they appear in dry-run preview and apply
	// atomically with the service entries.
	//
	// For non-default VRFs, generateServiceEntries handles BGP_GLOBALS_AF via
	// the VRF creation path. This only covers the default VRF case.
	hasBGPRouting := svc.Routing != nil && svc.Routing.Protocol == spec.RoutingProtocolBGP
	needsBGPEnsure := hasBGPRouting && !isOverlay && !n.BGPConfigured()
	if needsBGPEnsure {
		if resolved.UnderlayASN == 0 {
			return nil, fmt.Errorf("service '%s' requires BGP but underlay_asn is not set in device profile", serviceName)
		}
		if resolved.RouterID == "" {
			return nil, fmt.Errorf("service '%s' requires BGP but router_id (loopback_ip) is not set in device profile", serviceName)
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
	}

	// Service-level BGP neighbors reference a peer group named after the service
	// (Principle 36). Topology-level underlay peers do NOT use peer groups.
	peerGroup := ""
	if svc.Routing != nil && svc.Routing.Protocol == spec.RoutingProtocolBGP {
		peerGroup = serviceName
	}

	// Determine VLAN ID for idempotency checks (overlay from macvpn, local from opts)
	vlanID := 0
	if macvpnDef != nil {
		vlanID = macvpnDef.VlanID
	} else if opts.VLAN > 0 {
		vlanID = opts.VLAN
	}

	// Determine VRF name for binding and infrastructure
	var vrfName string
	switch svc.VRFType {
	case spec.VRFTypeInterface:
		vrfName = util.DeriveVRFName(svc.VRFType, serviceName, i.name)
	case spec.VRFTypeShared:
		if ipvpnDef != nil {
			vrfName = ipvpnDef.VRF
		}
	}

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

	// Pre-compute QoS policy name (lookup only — entries added to CS later)
	var qosPolicyName string
	var qosPolicy *spec.QoSPolicy
	if pn, policy := GetServiceQoSPolicy(i.Node(), svc); policy != nil {
		qosPolicyName = pn
		qosPolicy = policy
	}

	// Pre-compute BGP neighbor IP for binding (deterministic from opts.IPAddress)
	var bgpNeighborIP string
	if hasBGPRouting && opts.IPAddress != "" {
		bgpNeighborIP, _ = util.DeriveNeighborIP(opts.IPAddress)
	}

	// Pre-generate BGP entries before binding params — peer AS must be in the
	// intent record (write-ahead manifest, self-sufficiency). Resolution logic
	// stays in generateBGPPeeringConfig; we read the resolved value from output.
	var bgpEntries []sonic.Entry
	var bgpPeerAS string
	if hasBGPRouting && opts.IPAddress != "" {
		var err error
		bgpEntries, err = generateBGPPeeringConfig(svc, opts.IPAddress,
			opts.PeerAS, opts.Params, resolved.UnderlayASN, peerGroup, vrfName)
		if err != nil {
			return nil, fmt.Errorf("BGP peering config for %s: %w", i.name, err)
		}
		for _, e := range bgpEntries {
			if e.Table == "BGP_NEIGHBOR" {
				if asn := e.Fields["asn"]; asn != "" {
					bgpPeerAS = asn
					break
				}
			}
		}
	}

	// =========================================================================
	// Intent record — written FIRST (write-ahead manifest).
	//
	// The intent record is the manifest of intent: it records what this
	// operation will create. Writing it first ensures that if the operation is
	// interrupted after some infrastructure entries are written, RemoveService
	// can read the intent and clean up the partial state. Without the
	// intent record, orphaned CONFIG_DB entries accumulate with no way to
	// remove them — exactly the failure mode §13 (Symmetric Operations) warns
	// about.
	//
	// On remove, the intent record is deleted LAST — after all infrastructure
	// it references has been torn down. This means an interrupted removal can
	// be re-run: the intent record still exists, so RemoveService finds its
	// input.
	// =========================================================================
	bindingParams := map[string]string{
		sonic.FieldServiceName: serviceName,
		sonic.FieldServiceType: svc.ServiceType,
	}
	if opts.IPAddress != "" {
		bindingParams[sonic.FieldIPAddress] = opts.IPAddress
	}
	if vrfName != "" {
		bindingParams[sonic.FieldVRFName] = vrfName
	}
	if svc.VRFType != "" {
		bindingParams["vrf_type"] = svc.VRFType
	}
	if svc.IPVPN != "" {
		bindingParams[sonic.FieldIPVPN] = svc.IPVPN
	}
	if svc.MACVPN != "" {
		bindingParams[sonic.FieldMACVPN] = svc.MACVPN
	}
	if ingressACLName != "" {
		bindingParams["ingress_acl"] = ingressACLName
	}
	if egressACLName != "" {
		bindingParams["egress_acl"] = egressACLName
	}
	if bgpNeighborIP != "" {
		bindingParams["bgp_neighbor"] = bgpNeighborIP
	}
	if qosPolicyName != "" {
		bindingParams["qos_policy"] = qosPolicyName
	}
	if vlanID > 0 {
		bindingParams[sonic.FieldVLANID] = fmt.Sprintf("%d", vlanID)
	}
	if ipvpnDef != nil && ipvpnDef.L3VNI > 0 {
		bindingParams[sonic.FieldL3VNI] = fmt.Sprintf("%d", ipvpnDef.L3VNI)
	}
	if ipvpnDef != nil && ipvpnDef.L3VNIVlan > 0 {
		bindingParams[sonic.FieldL3VNIVlan] = fmt.Sprintf("%d", ipvpnDef.L3VNIVlan)
	}
	if ipvpnDef != nil && len(ipvpnDef.RouteTargets) > 0 {
		bindingParams[sonic.FieldRouteTargets] = strings.Join(ipvpnDef.RouteTargets, ",")
	}
	if svc.Routing != nil && svc.Routing.Redistribute != nil {
		redistVRF := "default"
		if vrfName != "" {
			redistVRF = vrfName
		}
		bindingParams["redistribute_vrf"] = redistVRF
	}
	// Self-sufficiency fields: store everything the reverse path needs so
	// RemoveService and RefreshService never re-resolve specs.
	if macvpnDef != nil {
		if macvpnDef.VNI > 0 {
			bindingParams["l2vni"] = fmt.Sprintf("%d", macvpnDef.VNI)
		}
		if macvpnDef.AnycastIP != "" {
			bindingParams["anycast_ip"] = macvpnDef.AnycastIP
		}
		if macvpnDef.AnycastMAC != "" {
			bindingParams[sonic.FieldAnycastMAC] = macvpnDef.AnycastMAC
		}
		if macvpnDef.ARPSuppression {
			bindingParams["arp_suppression"] = "true"
		}
	}
	if peerGroup != "" {
		bindingParams["peer_group"] = peerGroup
	}
	// Topology params for RefreshService self-sufficiency (Principle 8):
	// these BGP neighbor attributes must survive remove+reapply cycles.
	if opts.Params["route_reflector_client"] == "true" {
		bindingParams["route_reflector_client"] = "true"
	}
	if opts.Params["next_hop_self"] == "true" {
		bindingParams["next_hop_self"] = "true"
	}
	if bgpPeerAS != "" {
		bindingParams[sonic.FieldBGPPeerAS] = bgpPeerAS
	}

	cs := NewChangeSet(n.Name(), "interface."+sonic.OpApplyService)
	cs.ReverseOp = "interface.remove-service"
	cs.OperationParams = map[string]string{"interface": i.name}
	configDB := n.ConfigDB()

	// Compute parents based on service type for the Intent DAG.
	var intentParents []string
	switch {
	case vlanID > 0 && vrfName != "":
		// Both VLAN and VRF (irb, evpn-irb)
		intentParents = []string{"vlan|" + strconv.Itoa(vlanID), "vrf|" + vrfName}
	case vlanID > 0:
		// VLAN only (bridged, evpn-bridged)
		intentParents = []string{"vlan|" + strconv.Itoa(vlanID)}
	case vrfName != "":
		// VRF only (routed)
		intentParents = []string{"vrf|" + vrfName}
	default:
		intentParents = []string{"device"}
	}
	if i.IsPortChannel() {
		intentParents = append(intentParents, "portchannel|"+i.name)
	}

	// =========================================================================
	// Infrastructure via intent-idempotent primitives.
	// Primitives create parent intents — must precede interface intent write (I4).
	// =========================================================================

	// VLAN infrastructure (intent-idempotent: CreateVLAN checks vlan intent)
	if canBridge && vlanID > 0 {
		l2vni := 0
		if macvpnDef != nil {
			l2vni = macvpnDef.VNI
		}
		vlanCS, err := n.CreateVLAN(ctx, vlanID, VLANConfig{L2VNI: l2vni})
		if err != nil {
			return nil, fmt.Errorf("create VLAN %d: %w", vlanID, err)
		}
		cs.Merge(vlanCS)
		// ARP suppression only when VLAN was just created (vlanCS non-empty).
		// If VLAN already existed, ARP suppression was configured on first create.
		if !vlanCS.IsEmpty() && macvpnDef != nil && macvpnDef.ARPSuppression {
			cs.Adds(enableArpSuppressionConfig(VLANName(vlanID)))
		}
	}

	// VRF infrastructure (intent-idempotent: CreateVRF checks vrf intent)
	if vrfName != "" {
		vrfCS, err := n.CreateVRF(ctx, vrfName, VRFConfig{})
		if err != nil {
			return nil, fmt.Errorf("create VRF %s: %w", vrfName, err)
		}
		cs.Merge(vrfCS)
	}

	// IPVPN binding (intent-idempotent: BindIPVPN checks ipvpn intent)
	if ipvpnDef != nil && vrfName != "" {
		ipvpnCS, err := n.BindIPVPN(ctx, vrfName, svc.IPVPN)
		if err != nil {
			return nil, fmt.Errorf("bind IPVPN %s to VRF %s: %w", svc.IPVPN, vrfName, err)
		}
		cs.Merge(ipvpnCS)
	}

	// Interface intent — write-ahead manifest for crash recovery.
	// Parents created above; I4 check passes.
	if err := n.writeIntent(cs, sonic.OpApplyService, "interface|"+i.name, bindingParams, intentParents); err != nil {
		return nil, err
	}

	// =========================================================================
	// Per-interface CONFIG_DB entries (from owning files' config functions)
	// =========================================================================

	// Auto-ensure BGP_GLOBALS for the default VRF if needed. Added first so
	// BGP_GLOBALS entries precede BGP_NEIGHBOR entries (dependency order).
	if needsBGPEnsure {
		asnStr := fmt.Sprintf("%d", resolved.UnderlayASN)
		bgpEnsureEntry := updateDeviceMetadataConfig(map[string]string{
			"bgp_asn": asnStr,
			"type":    "LeafRouter",
		})
		cs.Update(bgpEnsureEntry.Table, bgpEnsureEntry.Key, bgpEnsureEntry.Fields)
		cs.Adds(CreateBGPGlobalsConfig("default", resolved.UnderlayASN, resolved.RouterID, map[string]string{
			"ebgp_requires_policy": "false",
			"suppress_fib_pending": "false",
			"log_neighbor_changes": "true",
		}))
		cs.Adds(CreateBGPGlobalsAFConfig("default", "ipv4_unicast", nil))
		cs.Adds(CreateRouteRedistributeConfig("default", "connected", "ipv4"))
	}

	// Per-interface entries by service type (config functions from owning files)
	switch svc.ServiceType {
	case spec.ServiceTypeEVPNBridged, spec.ServiceTypeBridged:
		if vlanID > 0 {
			cs.Adds(createVlanMemberConfig(vlanID, i.name, false))
		}
	case spec.ServiceTypeEVPNRouted, spec.ServiceTypeRouted:
		if vrfName != "" {
			cs.Adds(i.bindVrf(vrfName))
		} else {
			cs.Adds(i.enableIpRouting())
		}
		if opts.IPAddress != "" {
			cs.Adds(i.assignIpAddress(opts.IPAddress))
		}
	case spec.ServiceTypeEVPNIRB:
		if vlanID > 0 {
			cs.Adds(createVlanMemberConfig(vlanID, i.name, true))
			irbOpts := IRBConfig{VRF: vrfName}
			if macvpnDef != nil {
				irbOpts.IPAddress = macvpnDef.AnycastIP
				irbOpts.AnycastMAC = macvpnDef.AnycastMAC
			}
			cs.Adds(createSviConfig(vlanID, irbOpts))
		}
	case spec.ServiceTypeIRB:
		if vlanID > 0 {
			cs.Adds(createVlanMemberConfig(vlanID, i.name, true))
			cs.Adds(createSviConfig(vlanID, IRBConfig{
				VRF:       vrfName,
				IPAddress: opts.IPAddress,
			}))
		}
	}

	// BGP neighbor entries (pre-generated in step 5 for peer AS extraction)
	cs.Adds(bgpEntries)

	// ACL handling — skip if platform doesn't support ACLs
	skipACL := false
	if resolved.Platform != "" {
		if platform, err := n.GetPlatform(resolved.Platform); err == nil {
			skipACL = !platform.SupportsFeature("acl")
		}
	}
	if !skipACL && ingressACLName != "" {
		existingACL, aclExists := configDB.ACLTable[ingressACLName]
		if aclExists {
			merged := updateAclPorts(ingressACLName, util.AddToCSV(existingACL.Ports, i.name))
			cs.Update(merged.Table, merged.Key, merged.Fields)
		} else {
			filterSpec, _ := n.GetFilter(svc.IngressFilter)
			if filterSpec != nil {
				desc := fmt.Sprintf("Ingress filter for %s", serviceName)
				cs.Adds(createAclTableConfig(ingressACLName, mapFilterType(filterSpec.Type), "ingress", i.name, desc))
				ruleNames := i.addACLRulesFromFilterSpec(cs, ingressACLName, filterSpec)
				if err := n.writeIntent(cs, sonic.OpCreateACL, "acl|"+ingressACLName, map[string]string{
					sonic.FieldRules: strings.Join(ruleNames, ","),
				}, []string{"device"}); err != nil {
					return nil, err
				}
			}
		}
	}
	if !skipACL && egressACLName != "" {
		existingACL, aclExists := configDB.ACLTable[egressACLName]
		if aclExists {
			merged := updateAclPorts(egressACLName, util.AddToCSV(existingACL.Ports, i.name))
			cs.Update(merged.Table, merged.Key, merged.Fields)
		} else {
			filterSpec, _ := n.GetFilter(svc.EgressFilter)
			if filterSpec != nil {
				desc := fmt.Sprintf("Egress filter for %s", serviceName)
				cs.Adds(createAclTableConfig(egressACLName, mapFilterType(filterSpec.Type), "egress", i.name, desc))
				ruleNames := i.addACLRulesFromFilterSpec(cs, egressACLName, filterSpec)
				if err := n.writeIntent(cs, sonic.OpCreateACL, "acl|"+egressACLName, map[string]string{
					sonic.FieldRules: strings.Join(ruleNames, ","),
				}, []string{"device"}); err != nil {
					return nil, err
				}
			}
		}
	}

	// QoS entries (per-interface binding + device-wide tables)
	if qosPolicy != nil {
		cs.Adds(i.bindQos(qosPolicyName, qosPolicy))
		cs.Adds(GenerateDeviceQoSConfig(qosPolicyName, qosPolicy))
	}

	// Add route policies (ROUTE_MAP, PREFIX_SET, COMMUNITY_SET) — these are only
	// needed in the incremental path, not in topology provisioner.
	// Route map names (for informational binding fields) are computed here but
	// the binding was already written above with all fields needed for teardown.
	if svc.Routing != nil && svc.Routing.Protocol == spec.RoutingProtocolBGP {
		bgpResult, err := i.addBGPRoutePolicies(cs, serviceName, svc)
		if err != nil {
			return nil, fmt.Errorf("BGP route policies for %s: %w", i.name, err)
		}
		// Route map names are informational — RemoveService uses prefix scan
		// Update the intent entry (cs.Changes[0]) directly with computed names
		// and route policy keys for intent-driven teardown.
		if bgpResult.routeMapIn != "" {
			cs.Changes[0].Fields["route_map_in"] = bgpResult.routeMapIn
		}
		if bgpResult.routeMapOut != "" {
			cs.Changes[0].Fields["route_map_out"] = bgpResult.routeMapOut
		}
		if len(bgpResult.routePolicyKeys) > 0 {
			cs.Changes[0].Fields["route_policy_keys"] = strings.Join(bgpResult.routePolicyKeys, ";")
		}
	}

	n.applyShadow(cs)
	util.WithDevice(n.Name()).Infof("Applied service '%s' to interface %s", serviceName, i.name)
	return cs, nil
}

// bgpRoutePolicyResult holds the outputs of addBGPRoutePolicies needed by the caller.
type bgpRoutePolicyResult struct {
	routeMapIn      string   // content-hashed import route map name (for binding self-sufficiency)
	routeMapOut     string   // content-hashed export route map name (for binding self-sufficiency)
	routePolicyKeys []string // all ROUTE_MAP/PREFIX_SET/COMMUNITY_SET keys (for intent tracking)
}

// addBGPRoutePolicies creates the BGP peer group (if first use), adds route policy
// entries (ROUTE_MAP, PREFIX_SET, COMMUNITY_SET), and attaches route maps to the
// peer group AF (Principle 36). Also handles redistribution config.
//
// Returns the route map names (for the service binding record).
func (i *Interface) addBGPRoutePolicies(cs *ChangeSet, serviceName string, svc *spec.ServiceSpec) (bgpRoutePolicyResult, error) {
	if svc.Routing == nil || svc.Routing.Protocol != spec.RoutingProtocolBGP {
		return bgpRoutePolicyResult{}, nil
	}

	routing := svc.Routing

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
	// Also track route map names for binding self-sufficiency (stale cleanup).
	afFields := map[string]string{}
	var routeMapIn, routeMapOut string

	if routing.ImportPolicy != "" {
		entries, rmName := i.createRoutePolicy(serviceName, "import", routing.ImportPolicy, routing.ImportCommunity, routing.ImportPrefixList)
		cs.Adds(entries)
		if rmName != "" {
			afFields["route_map_in"] = rmName
			routeMapIn = rmName
		}
	} else if routing.ImportCommunity != "" || routing.ImportPrefixList != "" {
		entries, rmName := i.createInlineRoutePolicy(serviceName, "import", routing.ImportCommunity, routing.ImportPrefixList)
		cs.Adds(entries)
		if rmName != "" {
			afFields["route_map_in"] = rmName
			routeMapIn = rmName
		}
	}

	if routing.ExportPolicy != "" {
		entries, rmName := i.createRoutePolicy(serviceName, "export", routing.ExportPolicy, routing.ExportCommunity, routing.ExportPrefixList)
		cs.Adds(entries)
		if rmName != "" {
			afFields["route_map_out"] = rmName
			routeMapOut = rmName
		}
	} else if routing.ExportCommunity != "" || routing.ExportPrefixList != "" {
		entries, rmName := i.createInlineRoutePolicy(serviceName, "export", routing.ExportCommunity, routing.ExportPrefixList)
		cs.Adds(entries)
		if rmName != "" {
			afFields["route_map_out"] = rmName
			routeMapOut = rmName
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
		e := UpdateBGPPeerGroupAF(vrfKey, serviceName, afFields)
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

	// Collect all route policy keys from the entries just added to the ChangeSet
	// for intent tracking. This enables deleteRoutePoliciesFromIntent to read from
	// intent instead of scanning CONFIG_DB.
	var routePolicyKeys []string
	for _, c := range cs.Changes {
		switch c.Table {
		case "ROUTE_MAP", "PREFIX_SET", "COMMUNITY_SET":
			routePolicyKeys = append(routePolicyKeys, c.Table+":"+c.Key)
		}
	}

	return bgpRoutePolicyResult{
		routeMapIn:      routeMapIn,
		routeMapOut:     routeMapOut,
		routePolicyKeys: routePolicyKeys,
	}, nil
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

// scanExistingRoutePolicies returns all ROUTE_MAP, PREFIX_SET, and COMMUNITY_SET
// entries matching the service prefix. In online mode, reads live Redis (ground
// truth, not stale cache). In offline mode, scans the shadow configDB.
// Used by RefreshService to identify stale content-hashed objects (Principle 35).
func (n *Node) scanExistingRoutePolicies(serviceName string) ([]sonic.Entry, error) {
	if n.offline {
		// Offline mode: shadow configDB has all entries (applyShadow only adds)
		return n.scanRoutePoliciesByPrefix(serviceName), nil
	}

	// Online mode: read from Redis (cache may be stale if services were applied
	// after the initial ConnectNode loaded the cache)
	client := n.ConfigDBClient()
	if client == nil {
		return nil, nil
	}

	prefix := serviceName + "_"
	var entries []sonic.Entry

	for _, table := range []string{"ROUTE_MAP", "PREFIX_SET", "COMMUNITY_SET"} {
		keys, err := client.TableKeys(table)
		if err != nil {
			return nil, fmt.Errorf("scanning %s for stale policies: %w", table, err)
		}
		for _, redisKey := range keys {
			// redisKey is "TABLE|key_part" — strip table prefix
			key := strings.TrimPrefix(redisKey, table+"|")
			// Check if the base name (before first |) starts with service prefix
			baseName := strings.SplitN(key, "|", 2)[0]
			if strings.HasPrefix(baseName, prefix) {
				entries = append(entries, sonic.Entry{Table: table, Key: key})
			}
		}
	}
	return entries, nil
}

// deleteRoutePoliciesFromIntent returns delete entries for all ROUTE_MAP, PREFIX_SET, and
// COMMUNITY_SET entries created by a service. Reads keys from the service binding
// intent record (route_policy_keys field) rather than scanning CONFIG_DB.
func (n *Node) deleteRoutePoliciesFromIntent(intfName string) []sonic.Entry {
	var entries []sonic.Entry

	intent := n.GetIntent("interface|" + intfName)
	if intent == nil {
		return entries
	}

	keysCSV := intent.Params["route_policy_keys"]
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

// scanRoutePoliciesByPrefix returns all ROUTE_MAP, PREFIX_SET, and COMMUNITY_SET
// entries matching a service name prefix. Used by RefreshService to identify stale
// content-hashed objects — this is a legitimate CONFIG_DB scan because RefreshService
// needs to find what actually exists on the device (ground truth for diff).
func (n *Node) scanRoutePoliciesByPrefix(serviceName string) []sonic.Entry {
	var entries []sonic.Entry
	if n.configDB == nil {
		return entries
	}
	prefix := serviceName + "_"

	for key := range n.configDB.RouteMap {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) >= 1 && strings.HasPrefix(parts[0], prefix) {
			entries = append(entries, sonic.Entry{Table: "ROUTE_MAP", Key: key})
		}
	}
	for key := range n.configDB.PrefixSet {
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
func (i *Interface) addACLRulesFromFilterSpec(cs *ChangeSet, aclName string, filterSpec *spec.FilterSpec) []string {
	var ruleNames []string
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
				// Extract rule name from ACL_RULE key (format: "ACLNAME|RULENAME")
				if parts := strings.SplitN(e.Key, "|", 2); len(parts) == 2 {
					ruleNames = append(ruleNames, parts[1])
				}
			}
		}
	}
	return ruleNames
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


// removeSharedACL removes an ACL, handling the shared case.
// Uses DAG children of the acl|NAME intent to determine remaining users
// instead of scanning CONFIG_DB ports.
func (i *Interface) removeSharedACL(cs *ChangeSet, aclName string) error {
	configDB := i.node.ConfigDB()
	if configDB == nil {
		return nil
	}

	if _, ok := configDB.ACLTable[aclName]; !ok {
		return nil
	}

	aclIntentKey := "acl|" + aclName
	aclIntent := i.node.GetIntent(aclIntentKey)

	// Determine if this is the last user via DAG children.
	// Only binding children (interface|*) represent users of the ACL;
	// rule children (acl|*) are structural, not users.
	isLast := true
	if aclIntent != nil {
		for _, child := range aclIntent.Children {
			if !strings.HasPrefix(child, "interface|") {
				continue // skip rule children
			}
			if !strings.HasPrefix(child, "interface|"+i.name+"|") {
				isLast = false
				break
			}
		}
	}

	if isLast {
		// Last user — delete all ACL rules, bindings, table, and intents.
		// Under the DAG, rule children must be removed before the table intent
		// (I5: deleteIntent refuses if children exist).
		if aclIntent != nil {
			// Copy children — deleteIntent modifies parent's Children
			children := make([]string, len(aclIntent.Children))
			copy(children, aclIntent.Children)

			// Delete rule entries. Two sources: per-rule DAG children (acl|NAME|RULE)
			// or FieldRules CSV in intent params (ApplyService creates rules without
			// per-rule intents — §10.16 integration is deferred).
			hasRuleChildren := false
			for _, child := range children {
				if intentKind(child) == "acl" {
					hasRuleChildren = true
					// Rule child: "acl|ACLNAME|RULENAME" → delete ACL_RULE
					parts := strings.SplitN(child, "|", 3)
					if len(parts) == 3 {
						ruleKey := parts[1] + "|" + parts[2]
						cs.Delete("ACL_RULE", ruleKey)
					}
				}
			}
			if !hasRuleChildren {
				// Fallback: read rule names from FieldRules CSV in intent params.
				if rulesCSV := aclIntent.Params[sonic.FieldRules]; rulesCSV != "" {
					for _, ruleName := range strings.Split(rulesCSV, ",") {
						ruleName = strings.TrimSpace(ruleName)
						if ruleName != "" {
							cs.Delete("ACL_RULE", aclName+"|"+ruleName)
						}
					}
				}
			}

			// Delete all child intents (bindings and rules)
			for _, child := range children {
				if err := i.node.deleteIntent(cs, child); err != nil {
					return err
				}
			}
		}
		cs.Deletes(i.node.deleteAclTableConfig(aclName))
		if err := i.node.deleteIntent(cs, aclIntentKey); err != nil {
			return err
		}
	} else {
		// Other users exist — extract remaining interfaces from DAG children
		var remaining []string
		for _, child := range aclIntent.Children {
			if !strings.HasPrefix(child, "interface|") {
				continue // skip rule children
			}
			if strings.HasPrefix(child, "interface|"+i.name+"|") {
				continue // skip current interface
			}
			// child format: "interface|NAME|acl|DIR"
			parts := strings.SplitN(child, "|", 4)
			if len(parts) >= 2 {
				remaining = append(remaining, parts[1])
			}
		}
		e := updateAclPorts(aclName, strings.Join(remaining, ","))
		cs.Update(e.Table, e.Key, e.Fields)

		// Delete this interface's ACL binding intent (child of both
		// interface|INTF and acl|NAME). Must happen before interface intent
		// deletion to satisfy I5.
		for _, child := range aclIntent.Children {
			if strings.HasPrefix(child, "interface|"+i.name+"|") {
				if err := i.node.deleteIntent(cs, child); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// RemoveService removes the service from this interface.
// Uses the stored intent record (NEWTRON_INTENT) to know exactly
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
	serviceName := b[sonic.FieldServiceName]
	vrfName := b[sonic.FieldVRFName]
	ingressACL := b["ingress_acl"]
	egressACL := b["egress_acl"]
	bgpNeighbor := b["bgp_neighbor"]

	// Determine if this is the last interface using this service (for shared resources).
	// Scan intent records directly instead of DependencyChecker.
	excludeKey := "interface|" + i.name
	isLastServiceUser := true
	for intentKey, fields := range n.ConfigDB().NewtronIntent {
		if fields[sonic.FieldServiceName] == serviceName && intentKey != excludeKey {
			isLastServiceUser = false
			break
		}
	}

	serviceType := b[sonic.FieldServiceType]
	vrfType := b["vrf_type"]

	// Derived booleans from serviceType
	canRoute := serviceType == spec.ServiceTypeRouted || serviceType == spec.ServiceTypeEVPNRouted
	canBridge := serviceType == spec.ServiceTypeEVPNIRB || serviceType == spec.ServiceTypeEVPNBridged ||
		serviceType == spec.ServiceTypeIRB || serviceType == spec.ServiceTypeBridged
	hasIRB := serviceType == spec.ServiceTypeEVPNIRB || serviceType == spec.ServiceTypeIRB

	l2vni := bindingInt(b["l2vni"])
	anycastIP := b["anycast_ip"]
	anycastMAC := b[sonic.FieldAnycastMAC]
	arpSuppression := b["arp_suppression"] == "true"

	// Track which infrastructure intents to clean up after the interface
	// intent is deleted (must happen in children-first order per I5).
	var destroyedVRF string  // VRF whose config was destroyed (needs intent cleanup)
	var destroyedVLAN int    // VLAN whose config was destroyed (needs intent cleanup)

	// (isLastServiceUser computed above)

	// =========================================================================
	// Per-interface resources (always delete)
	// =========================================================================

	// Remove QoS mapping and per-interface QUEUE entries
	cs.Deletes(i.unbindQos())

	// Remove QoS device-wide entries if no other interface references this policy
	if b["qos_policy"] != "" {
		if !n.isQoSPolicyReferenced(b["qos_policy"], i.name) {
			cs.Deletes(n.deleteDeviceQoSConfig(b["qos_policy"]))
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
		if err := i.removeSharedACL(cs, ingressACL); err != nil {
			return nil, err
		}
	}
	if egressACL != "" {
		if err := i.removeSharedACL(cs, egressACL); err != nil {
			return nil, err
		}
	}

	// Remove route policies (ROUTE_MAP, PREFIX_SET, COMMUNITY_SET)
	if isLastServiceUser {
		cs.Deletes(n.deleteRoutePoliciesFromIntent(i.name))
	}

	// Remove BGP peer group (Principle 36) — created per-service, deleted when last user removed.
	// Peer group must be deleted AFTER all BGP_NEIGHBORs referencing it are deleted.
	if b["peer_group"] != "" && isLastServiceUser {
		vrfKey := "default"
		if vrfName != "" && vrfName != "default" {
			vrfKey = vrfName
		}
		cs.Deletes(DeleteBGPPeerGroupConfig(vrfKey, b["peer_group"]))
	}

	// Revert BGP_GLOBALS_AF redistribution override if this service set it.
	// For per-interface VRFs, destroyVrf cascades this anyway — harmless redundancy.
	if b["redistribute_vrf"] != "" && isLastServiceUser {
		cs.Updates(revertRedistributionConfig(b["redistribute_vrf"]))
	}

	// =========================================================================
	// Per-interface VRF (vrf_type: interface or shared)
	// =========================================================================

	if vrfName != "" && vrfName != "default" {
		// For routed services, delete the INTERFACE base entry entirely.
		// For IRB/bridged types, the VRF binding is on the IRB (VLAN_INTERFACE),
		// not the physical INTERFACE — IRB cleanup happens in the VLAN section below.
		if canRoute {
			cs.Deletes(i.enableIpRouting())
		}

		// Per-interface VRF: delete VRF and related config
		if vrfType == spec.VRFTypeInterface {
			derivedVRF := util.DeriveVRFName(vrfType, serviceName, i.name)
			l3vni, l3vniVlan := bindingInt(b[sonic.FieldL3VNI]), bindingInt(b[sonic.FieldL3VNIVlan])
			cs.Deletes(n.destroyVrfConfig(derivedVRF, l3vni, l3vniVlan))
			destroyedVRF = derivedVRF
		}

		// Shared VRF: delete when last ipvpn user is removed.
		// The shared VRF was auto-created by the first service apply and should
		// be cleaned up when no service bindings reference the ipvpn anymore.
		if vrfType == spec.VRFTypeShared && b[sonic.FieldIPVPN] != "" {
			// Check if this is the last IPVPN user via interface intent scan.
			// Only count interface|* intents — ipvpn|* intents are infrastructure,
			// not service bindings.
			isLastIPVPN := true
			for ik, fields := range n.ConfigDB().NewtronIntent {
				if strings.HasPrefix(ik, "interface|") && fields[sonic.FieldIPVPN] == b[sonic.FieldIPVPN] && ik != excludeKey {
					isLastIPVPN = false
					break
				}
			}
			if isLastIPVPN {
				l3vni, l3vniVlan := bindingInt(b[sonic.FieldL3VNI]), bindingInt(b[sonic.FieldL3VNIVlan])
				cs.Deletes(n.destroyVrfConfig(vrfName, l3vni, l3vniVlan))
				destroyedVRF = vrfName
			}
		}
	}

	// =========================================================================
	// Per-VLAN resources (delete only if last VLAN member)
	// =========================================================================

	vlanID := bindingInt(b[sonic.FieldVLANID])

	if canBridge && vlanID > 0 {
		vlanName := VLANName(vlanID)

		// Always remove this interface's VLAN membership
		cs.Deletes(deleteVlanMemberConfig(vlanID, i.name))

		// Check if this is the last VLAN member via DAG children
		vlanIntent := n.GetIntent("vlan|" + strconv.Itoa(vlanID))
		isLastVLANMember := true
		if vlanIntent != nil {
			for _, child := range vlanIntent.Children {
				if strings.HasPrefix(child, "interface|") && child != "interface|"+i.name {
					isLastVLANMember = false
					break
				}
			}
		}
		if isLastVLANMember {
			// Last member - clean up all VLAN-related config

			// IRB (for IRB types)
			if hasIRB {
				if anycastIP != "" {
					cs.Deletes(deleteSviIPConfig(vlanID, anycastIP))
				} else if b[sonic.FieldIPAddress] != "" {
					// Local IRB: IRB IP comes from opts.IPAddress (stored in binding)
					cs.Deletes(deleteSviIPConfig(vlanID, b[sonic.FieldIPAddress]))
				}
				cs.Deletes(deleteSviBaseConfig(vlanID))

				// SAG_GLOBAL: clean up when last anycast MAC user is removed
				isLastAnycastMAC := true
				for ik, fields := range n.ConfigDB().NewtronIntent {
					if ik != excludeKey && fields[sonic.FieldAnycastMAC] != "" {
						isLastAnycastMAC = false
						break
					}
				}
				if anycastMAC != "" && isLastAnycastMAC {
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
			destroyedVLAN = vlanID
		}
	}

	// =========================================================================
	// Service binding tracking (always delete)
	// =========================================================================

	if err := n.deleteIntent(cs, "interface|"+i.name); err != nil {
		return nil, err
	}

	// Clean up infrastructure intents whose CONFIG_DB entries were destroyed
	// above. Must happen AFTER deleting the interface intent (which deregisters
	// from its parents), so the parent intents satisfy I5 (no children).
	// Explicit ordered deletion: ipvpn (child of vrf) → vrf → vlan.
	if destroyedVRF != "" {
		// Delete ipvpn intent first (child of vrf per DAG)
		if err := n.deleteIntent(cs, "ipvpn|"+destroyedVRF); err != nil {
			return nil, err
		}
		if err := n.deleteIntent(cs, "vrf|"+destroyedVRF); err != nil {
			return nil, err
		}
	}
	if destroyedVLAN > 0 {
		if err := n.deleteIntent(cs, "vlan|"+strconv.Itoa(destroyedVLAN)); err != nil {
			return nil, err
		}
	}

	n.applyShadow(cs)

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
	serviceName := b[sonic.FieldServiceName]
	serviceIP := b[sonic.FieldIPAddress]
	peerAS := bindingInt(b[sonic.FieldBGPPeerAS])
	vlanID := bindingInt(b[sonic.FieldVLANID])

	// Remove the current service
	removeCS, err := i.RemoveService(ctx)
	if err != nil {
		return nil, fmt.Errorf("removing old service: %w", err)
	}

	// Clear the binding from the ConfigDB cache so ApplyService's
	// HasService() check passes. The delete change is already recorded
	// above; this cache mutation only affects reads within this episode.
	configDB := n.ConfigDB()
	delete(configDB.NewtronIntent, i.name)

	// Restore topology params from the binding (Principle 8: binding self-sufficiency).
	// route_reflector_client and next_hop_self are topology attributes that must
	// survive the remove+reapply cycle.
	var params map[string]string
	if b["route_reflector_client"] == "true" || b["next_hop_self"] == "true" {
		params = make(map[string]string)
		if b["route_reflector_client"] == "true" {
			params["route_reflector_client"] = "true"
		}
		if b["next_hop_self"] == "true" {
			params["next_hop_self"] = "true"
		}
	}

	// Reapply the service with preserved parameters. RemoveService deletes
	// the BGP neighbor, so PeerAS must be passed to recreate it.
	applyCS, err := i.ApplyService(ctx, serviceName, ApplyServiceOpts{
		IPAddress: serviceIP,
		PeerAS:    peerAS,
		VLAN:      vlanID,
		Params:    params,
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

	// Clean up stale content-hashed route policy objects (Principle 35, Phase 4).
	//
	// When specs change, RefreshService creates new-hash objects but RemoveService
	// (called above) skips shared policy deletion if other users exist. The peer
	// group AF was updated to reference new route maps, leaving old objects orphaned.
	//
	// Scan for existing route policy objects in CONFIG_DB, compare against what
	// the apply phase just created, and delete the difference.
	existing, err := n.scanExistingRoutePolicies(serviceName)
	if err != nil {
		// Non-fatal: stale policies might persist but don't break functionality
		util.WithDevice(n.Name()).Warnf("Could not scan for stale route policies: %v", err)
	} else if len(existing) > 0 {
		activeKeys := make(map[string]bool)
		for _, c := range applyCS.Changes {
			if c.Type == sonic.ChangeTypeAdd {
				switch c.Table {
				case "ROUTE_MAP", "PREFIX_SET", "COMMUNITY_SET":
					activeKeys[c.Table+"|"+c.Key] = true
				}
			}
		}
		staleCS := NewChangeSet(n.Name(), "stale-policy-cleanup")
		for _, e := range existing {
			if !activeKeys[e.Table+"|"+e.Key] {
				staleCS.Delete(e.Table, e.Key)
			}
		}
		n.applyShadow(staleCS)
		cs.Merge(staleCS)
	}

	util.WithDevice(n.Name()).Infof("Refreshed service '%s' on interface %s", serviceName, i.name)
	return cs, nil
}
