// service_gen.go provides a single source of truth for translating a service
// spec into CONFIG_DB entries.  Both the topology provisioner (offline composite
// generation) and Interface.ApplyService (online, incremental) delegate here.
//
// The function does NOT handle:
//   - Precondition checks (connected, locked, LAG member, existing service, VTEP)
//   - Idempotency guards (VLAN/VRF already exists, ACL port merging)
//   - Prefix-list expansion for ACLs (Cartesian product)
//   - Route policy generation (ROUTE_MAP, COMMUNITY_SET, PREFIX_SET)
//   - Local state updates (Interface field mutations)
package node

import (
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// filterTypeToSONiC translates spec filter types to SONiC ACL_TABLE type values.
func filterTypeToSONiC(specType string) string {
	switch specType {
	case "ipv6":
		return "L3V6"
	default:
		return "L3"
	}
}

// CompositeEntry is a single CONFIG_DB entry (table + key + fields).
// Used by GenerateServiceEntries and the QoS generators to return entries
// without coupling to the CompositeBuilder or ChangeSet types.
type CompositeEntry struct {
	Table  string
	Key    string
	Fields map[string]string
}

// ServiceEntryParams contains everything needed to generate CONFIG_DB entries
// for a service binding on a single interface.
type ServiceEntryParams struct {
	ServiceName   string
	InterfaceName string
	IPAddress     string
	VLAN          int               // VLAN ID for local types (irb, bridged) — overlay types use macvpnDef.VlanID
	Params        map[string]string // topology params (peer_as, route_reflector_client, next_hop_self)
	PeerAS        int               // CLI peer-as (interface_ops path only)
	UnderlayASN   int               // device AS number (required in all-eBGP design)
	RouterID      string            // device router ID (required for per-VRF BGP_GLOBALS)
	PlatformName  string            // for feature gating (ACL skip)
}

// GenerateServiceEntries produces the CONFIG_DB entries for applying a service
// to an interface.  The returned slice is ordered by table dependency (VLANs
// before members, VRFs before interfaces, etc.).
func GenerateServiceEntries(sp SpecProvider, p ServiceEntryParams) ([]CompositeEntry, error) {
	svc, err := sp.GetService(p.ServiceName)
	if err != nil {
		return nil, fmt.Errorf("service '%s' not found", p.ServiceName)
	}

	var entries []CompositeEntry

	// Resolve VPN definitions
	var ipvpnDef *spec.IPVPNSpec
	var macvpnDef *spec.MACVPNSpec

	if svc.IPVPN != "" {
		ipvpnDef, err = sp.GetIPVPN(svc.IPVPN)
		if err != nil {
			return nil, fmt.Errorf("ipvpn '%s' not found", svc.IPVPN)
		}
	}
	if svc.MACVPN != "" {
		macvpnDef, err = sp.GetMACVPN(svc.MACVPN)
		if err != nil {
			return nil, fmt.Errorf("macvpn '%s' not found", svc.MACVPN)
		}
	}

	// Resolve VLAN ID: overlay types use macvpnDef, local types use params
	vlanID := 0
	if macvpnDef != nil {
		vlanID = macvpnDef.VlanID
	} else if p.VLAN > 0 {
		vlanID = p.VLAN
	}

	// Determine which layers this service uses
	hasL2 := svc.ServiceType == spec.ServiceTypeEVPNIRB || svc.ServiceType == spec.ServiceTypeEVPNBridged ||
		svc.ServiceType == spec.ServiceTypeIRB || svc.ServiceType == spec.ServiceTypeBridged
	hasL3 := svc.ServiceType == spec.ServiceTypeEVPNIRB || svc.ServiceType == spec.ServiceTypeEVPNRouted ||
		svc.ServiceType == spec.ServiceTypeIRB || svc.ServiceType == spec.ServiceTypeRouted

	// VLAN creation (for L2 types)
	if hasL2 && vlanID > 0 {
		l2vni := 0
		if macvpnDef != nil && macvpnDef.VNI > 0 {
			l2vni = macvpnDef.VNI
		}
		entries = append(entries, vlanConfig(vlanID, VLANConfig{L2VNI: l2vni})...)

		if macvpnDef != nil && macvpnDef.ARPSuppression {
			entries = append(entries, arpSuppressionConfig(vlanID)...)
		}
	}

	// VRF creation (for L3 types)
	vrfName := ""
	if hasL3 && svc.VRFType == spec.VRFTypeInterface {
		vrfName = util.DeriveVRFName(svc.VRFType, p.ServiceName, p.InterfaceName)
		if ipvpnDef != nil {
			entries = append(entries, ipvpnConfig(vrfName, ipvpnDef, p.UnderlayASN, p.RouterID)...)
		} else {
			entries = append(entries, vrfConfig(vrfName)...)
		}
	} else if svc.VRFType == spec.VRFTypeShared && ipvpnDef != nil {
		vrfName = ipvpnDef.VRF
	}

	// Shared VRF creation (for L3 types with shared VRF)
	// The topology provisioner always emits these entries (idempotent overwrite).
	// The interface_ops caller filters them out when the VRF already exists.
	if hasL3 && svc.VRFType == spec.VRFTypeShared && ipvpnDef != nil && ipvpnDef.VRF != "" {
		entries = append(entries, ipvpnConfig(ipvpnDef.VRF, ipvpnDef, p.UnderlayASN, p.RouterID)...)
	}

	// Interface configuration based on service type
	switch svc.ServiceType {
	case spec.ServiceTypeEVPNBridged, spec.ServiceTypeBridged:
		// L2 access: untagged VLAN member
		if vlanID > 0 {
			entries = append(entries, vlanMemberConfig(vlanID, p.InterfaceName, false)...)
		}

	case spec.ServiceTypeEVPNRouted, spec.ServiceTypeRouted:
		// L3 routed: interface with VRF and IP
		// Bug fix #3: always emit base INTERFACE entry (SONiC intfmgrd requires it)
		intfFields := map[string]string{}
		if vrfName != "" {
			intfFields["vrf_name"] = vrfName
		}
		entries = append(entries, CompositeEntry{
			Table:  "INTERFACE",
			Key:    p.InterfaceName,
			Fields: intfFields,
		})
		if p.IPAddress != "" {
			entries = append(entries, CompositeEntry{
				Table:  "INTERFACE",
				Key:    fmt.Sprintf("%s|%s", p.InterfaceName, p.IPAddress),
				Fields: map[string]string{},
			})
		}

	case spec.ServiceTypeEVPNIRB:
		// L2+L3 overlay: tagged VLAN member + SVI with anycast gateway
		if vlanID > 0 {
			entries = append(entries, vlanMemberConfig(vlanID, p.InterfaceName, true)...)
			sviOpts := SVIConfig{VRF: vrfName}
			if macvpnDef != nil {
				sviOpts.IPAddress = macvpnDef.AnycastIP
				sviOpts.AnycastMAC = macvpnDef.AnycastMAC
			}
			entries = append(entries, sviConfig(vlanID, sviOpts)...)
		}

	case spec.ServiceTypeIRB:
		// Local L2+L3: tagged VLAN member + SVI with IP from params
		if vlanID > 0 {
			entries = append(entries, vlanMemberConfig(vlanID, p.InterfaceName, true)...)
			entries = append(entries, sviConfig(vlanID, SVIConfig{
				VRF:       vrfName,
				IPAddress: p.IPAddress,
			})...)
		}
	}

	// ACL configuration — skip if the platform does not support ACLs.
	skipACL := false
	if p.PlatformName != "" {
		if platform, err := sp.GetPlatform(p.PlatformName); err == nil {
			skipACL = !platform.SupportsFeature("acl")
		}
	}
	if !skipACL {
		if svc.IngressFilter != "" {
			filterEntries, err := generateServiceACLEntries(sp, p.ServiceName, svc.IngressFilter, p.InterfaceName, "ingress")
			if err != nil {
				return nil, err
			}
			entries = append(entries, filterEntries...)
		}
		if svc.EgressFilter != "" {
			filterEntries, err := generateServiceACLEntries(sp, p.ServiceName, svc.EgressFilter, p.InterfaceName, "egress")
			if err != nil {
				return nil, err
			}
			entries = append(entries, filterEntries...)
		}
	}

	// QoS configuration: new-style policy takes precedence over legacy profile
	if policyName, policy := ResolveServiceQoSPolicy(sp, svc); policy != nil {
		entries = append(entries, generateQoSInterfaceEntries(policyName, policy, p.InterfaceName)...)
	} else if svc.QoSProfile != "" {
		qosProfile, err := sp.GetQoSProfile(svc.QoSProfile)
		if err == nil && qosProfile != nil {
			qosFields := map[string]string{}
			if qosProfile.DSCPToTCMap != "" {
				qosFields["dscp_to_tc_map"] = qosProfile.DSCPToTCMap
			}
			if qosProfile.TCToQueueMap != "" {
				qosFields["tc_to_queue_map"] = qosProfile.TCToQueueMap
			}
			if len(qosFields) > 0 {
				entries = append(entries, CompositeEntry{
					Table:  "PORT_QOS_MAP",
					Key:    p.InterfaceName,
					Fields: qosFields,
				})
			}
		}
	}

	// BGP routing configuration
	if svc.Routing != nil && svc.Routing.Protocol == spec.RoutingProtocolBGP {
		bgpEntries, err := generateBGPEntries(svc, p, vrfName)
		if err != nil {
			return nil, fmt.Errorf("interface %s: BGP routing: %w", p.InterfaceName, err)
		}
		entries = append(entries, bgpEntries...)
	}

	// Service binding record
	bindingFields := map[string]string{
		"service_name": p.ServiceName,
	}
	if p.IPAddress != "" {
		bindingFields["ip_address"] = p.IPAddress
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
	entries = append(entries, CompositeEntry{
		Table:  "NEWTRON_SERVICE_BINDING",
		Key:    p.InterfaceName,
		Fields: bindingFields,
	})

	return entries, nil
}

// generateBGPEntries resolves BGP peer parameters from the service spec and
// topology params, then delegates to bgpNeighborConfig for entry construction.
func generateBGPEntries(svc *spec.ServiceSpec, p ServiceEntryParams, vrfName string) ([]CompositeEntry, error) {
	if svc.Routing == nil || svc.Routing.Protocol != spec.RoutingProtocolBGP {
		return nil, nil
	}

	routing := svc.Routing

	// Derive peer IP from interface IP
	var peerIP string
	if p.IPAddress != "" {
		var err error
		peerIP, err = util.DeriveNeighborIP(p.IPAddress)
		if err != nil {
			return nil, fmt.Errorf("could not derive BGP peer IP: %w", err)
		}
	}
	if peerIP == "" {
		return nil, fmt.Errorf("BGP routing requires an IP address")
	}

	// Determine peer AS — from service spec, topology params, or CLI opts
	var peerAS int
	if routing.PeerAS == spec.PeerASRequest {
		if peerASStr, ok := p.Params["peer_as"]; ok {
			fmt.Sscanf(peerASStr, "%d", &peerAS)
		}
		if peerAS == 0 {
			peerAS = p.PeerAS
		}
		if peerAS == 0 {
			return nil, fmt.Errorf("service requires peer_as parameter")
		}
	} else if routing.PeerAS != "" {
		fmt.Sscanf(routing.PeerAS, "%d", &peerAS)
	}
	if peerAS == 0 {
		return nil, fmt.Errorf("could not determine BGP peer AS for service routing")
	}

	// All-eBGP design: UnderlayASN is required
	if p.UnderlayASN == 0 {
		return nil, fmt.Errorf("device has no AS number configured (underlay_asn required)")
	}

	localIP, _ := util.SplitIPMask(p.IPAddress)

	return bgpNeighborConfig(peerIP, peerAS, localIP, bgpNeighborOpts{
		VRF:          vrfName,
		ActivateIPv4: true,
		RRClient:     p.Params["route_reflector_client"] == "true",
		NextHopSelf:  p.Params["next_hop_self"] == "true",
	}), nil
}

// generateServiceACLEntries generates ACL table and rule entries for a service filter.
// The ACL_TABLE entry is produced by aclTableConfig (acl_ops.go); rules use
// buildACLRuleFields for the full field set (including CoS→TC mapping).
func generateServiceACLEntries(sp SpecProvider, serviceName, filterName, interfaceName, stage string) ([]CompositeEntry, error) {
	filterSpec, err := sp.GetFilter(filterName)
	if err != nil {
		return nil, fmt.Errorf("filter spec '%s' not found", filterName)
	}

	direction := "in"
	if stage == "egress" {
		direction = "out"
	}
	aclName := util.DeriveACLName(serviceName, direction)
	desc := fmt.Sprintf("%s filter for %s", capitalizeFirst(stage), serviceName)

	entries := aclTableConfig(aclName, filterTypeToSONiC(filterSpec.Type), stage, interfaceName, desc)

	for _, rule := range filterSpec.Rules {
		ruleKey := fmt.Sprintf("%s|RULE_%d", aclName, rule.Sequence)
		entries = append(entries, CompositeEntry{
			Table:  "ACL_RULE",
			Key:    ruleKey,
			Fields: buildACLRuleFields(rule, rule.SrcIP, rule.DstIP),
		})
	}

	return entries, nil
}

// buildACLRuleFields builds ACL rule fields from a filter rule spec.
// Takes explicit srcIP/dstIP to support prefix-list expansion (Cartesian product)
// in the interface_ops path.
//
// Unified from topology.go's buildACLRuleFields (no CoS) and
// interface_ops.go's buildACLRuleFieldsExpanded (with CoS) — fix #4.
func buildACLRuleFields(rule *spec.FilterRule, srcIP, dstIP string) map[string]string {
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

	// CoS/TC marking — previously only in interface_ops (fix #4)
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

// capitalizeFirst returns s with the first letter uppercased.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
