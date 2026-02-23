// service_gen.go provides a single source of truth for translating a service
// spec into CONFIG_DB entries.  Both the topology provisioner (offline composite
// generation via Interface.ApplyService) and the online CLI path delegate here
// through Interface.generateServiceEntries().
//
// The method does NOT handle:
//   - Precondition checks (connected, locked, PortChannel member, existing service, VTEP)
//   - Idempotency guards (VLAN/VRF already exists, ACL port merging)
//   - Prefix-list expansion for ACLs (Cartesian product)
//   - Route policy generation (ROUTE_MAP, COMMUNITY_SET, PREFIX_SET)
//   - Local state updates (Interface field mutations)
package node

import (
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
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

// ServiceEntryParams contains everything needed to generate CONFIG_DB entries
// for a service binding on a single interface.
type ServiceEntryParams struct {
	ServiceName  string
	IPAddress    string
	VLAN         int               // VLAN ID for local types (irb, bridged) — overlay types use macvpnDef.VlanID
	Params       map[string]string // topology params (peer_as, route_reflector_client, next_hop_self)
	PeerAS       int               // CLI peer-as (interface_ops path only)
	UnderlayASN  int               // device AS number (required in all-eBGP design)
	RouterID     string            // device router ID (required for per-VRF BGP_GLOBALS)
	PlatformName string            // for feature gating (ACL skip)
}

// generateServiceEntries produces the CONFIG_DB entries for applying a service
// to this interface. The returned slice is ordered by table dependency (VLANs
// before members, VRFs before interfaces, etc.).
func (i *Interface) generateServiceEntries(p ServiceEntryParams) ([]sonic.Entry, error) {
	sp := i.node
	svc, err := sp.GetService(p.ServiceName)
	if err != nil {
		return nil, fmt.Errorf("service '%s' not found", p.ServiceName)
	}

	var entries []sonic.Entry

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
	canBridge := svc.ServiceType == spec.ServiceTypeEVPNIRB || svc.ServiceType == spec.ServiceTypeEVPNBridged ||
		svc.ServiceType == spec.ServiceTypeIRB || svc.ServiceType == spec.ServiceTypeBridged
	canRoute := svc.ServiceType == spec.ServiceTypeEVPNIRB || svc.ServiceType == spec.ServiceTypeEVPNRouted ||
		svc.ServiceType == spec.ServiceTypeIRB || svc.ServiceType == spec.ServiceTypeRouted

	// VLAN creation (for bridging-capable types)
	if canBridge && vlanID > 0 {
		l2vni := 0
		if macvpnDef != nil && macvpnDef.VNI > 0 {
			l2vni = macvpnDef.VNI
		}
		entries = append(entries, createVlan(vlanID, VLANConfig{L2VNI: l2vni})...)

		if macvpnDef != nil && macvpnDef.ARPSuppression {
			entries = append(entries, enableArpSuppression(VLANName(vlanID))...)
		}
	}

	// VRF creation (for routed types)
	vrfName := ""
	if canRoute && svc.VRFType == spec.VRFTypeInterface {
		vrfName = util.DeriveVRFName(svc.VRFType, p.ServiceName, i.name)
		if ipvpnDef != nil {
			entries = append(entries, bindIpvpn(vrfName, ipvpnDef, p.UnderlayASN, p.RouterID)...)
		} else {
			entries = append(entries, createVrf(vrfName)...)
		}
	} else if svc.VRFType == spec.VRFTypeShared && ipvpnDef != nil {
		vrfName = ipvpnDef.VRF
	}

	// Shared VRF creation (for routed types with shared VRF)
	// The topology provisioner always emits these entries (idempotent overwrite).
	// The interface_ops caller filters them out when the VRF already exists.
	if canRoute && svc.VRFType == spec.VRFTypeShared && ipvpnDef != nil && ipvpnDef.VRF != "" {
		entries = append(entries, bindIpvpn(ipvpnDef.VRF, ipvpnDef, p.UnderlayASN, p.RouterID)...)
	}

	// Interface configuration based on service type
	switch svc.ServiceType {
	case spec.ServiceTypeEVPNBridged, spec.ServiceTypeBridged:
		// Bridged access: untagged VLAN member
		if vlanID > 0 {
			entries = append(entries, createVlanMember(vlanID, i.name, false)...)
		}

	case spec.ServiceTypeEVPNRouted, spec.ServiceTypeRouted:
		// Routed: interface with VRF and IP
		// Bug fix #3: always emit base INTERFACE entry (SONiC intfmgrd requires it)
		if vrfName != "" {
			entries = append(entries, i.bindVrf(vrfName)...)
		} else {
			entries = append(entries, i.enableIpRouting()...)
		}
		if p.IPAddress != "" {
			entries = append(entries, i.assignIpAddress(p.IPAddress)...)
		}

	case spec.ServiceTypeEVPNIRB:
		// IRB overlay: tagged VLAN member + SVI with anycast gateway
		if vlanID > 0 {
			entries = append(entries, createVlanMember(vlanID, i.name, true)...)
			sviOpts := SVIConfig{VRF: vrfName}
			if macvpnDef != nil {
				sviOpts.IPAddress = macvpnDef.AnycastIP
				sviOpts.AnycastMAC = macvpnDef.AnycastMAC
			}
			entries = append(entries, createSvi(vlanID, sviOpts)...)
		}

	case spec.ServiceTypeIRB:
		// Local IRB: tagged VLAN member + SVI with IP from params
		if vlanID > 0 {
			entries = append(entries, createVlanMember(vlanID, i.name, true)...)
			entries = append(entries, createSvi(vlanID, SVIConfig{
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
			filterEntries, err := i.generateAclBinding(p.ServiceName, svc.IngressFilter, "ingress")
			if err != nil {
				return nil, err
			}
			entries = append(entries, filterEntries...)
		}
		if svc.EgressFilter != "" {
			filterEntries, err := i.generateAclBinding(p.ServiceName, svc.EgressFilter, "egress")
			if err != nil {
				return nil, err
			}
			entries = append(entries, filterEntries...)
		}
	}

	// QoS configuration: new-style policy takes precedence over legacy profile
	if policyName, policy := ResolveServiceQoSPolicy(sp, svc); policy != nil {
		entries = append(entries, i.bindQos(policyName, policy)...)
	} else if svc.QoSProfile != "" {
		qosProfile, err := sp.GetQoSProfile(svc.QoSProfile)
		if err == nil && qosProfile != nil {
			entries = append(entries, i.bindQosProfile(qosProfile)...)
		}
	}

	// BGP routing configuration
	if svc.Routing != nil && svc.Routing.Protocol == spec.RoutingProtocolBGP {
		bgpEntries, err := generateBGPPeering(svc, p, vrfName)
		if err != nil {
			return nil, fmt.Errorf("interface %s: BGP routing: %w", i.name, err)
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
	entries = append(entries, createServiceBinding(i.name, bindingFields))

	return entries, nil
}

// generateBGPPeering resolves BGP peer parameters from the service spec and
// topology params, then delegates to BGPNeighbor for entry construction.
func generateBGPPeering(svc *spec.ServiceSpec, p ServiceEntryParams, vrfName string) ([]sonic.Entry, error) {
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

	return CreateBGPNeighbor(peerIP, peerAS, localIP, BGPNeighborOpts{
		VRF:          vrfName,
		ActivateIPv4: true,
		RRClient:     p.Params["route_reflector_client"] == "true",
		NextHopSelf:  p.Params["next_hop_self"] == "true",
	}), nil
}

// generateAclBinding generates ACL table and rule entries for a service filter on this interface.
// Delegates to acl_ops.go config functions: aclTable for the ACL_TABLE entry,
// aclRuleFields for the full ACL_RULE field set (including CoS→TC mapping).
func (i *Interface) generateAclBinding(serviceName, filterName, stage string) ([]sonic.Entry, error) {
	filterSpec, err := i.node.GetFilter(filterName)
	if err != nil {
		return nil, fmt.Errorf("filter spec '%s' not found", filterName)
	}

	direction := "in"
	if stage == "egress" {
		direction = "out"
	}
	aclName := util.DeriveACLName(serviceName, direction)
	desc := fmt.Sprintf("%s filter for %s", capitalizeFirst(stage), serviceName)

	entries := createAclTable(aclName, filterTypeToSONiC(filterSpec.Type), stage, i.name, desc)

	for _, rule := range filterSpec.Rules {
		entries = append(entries, createAclRuleFromFilter(aclName, rule, rule.SrcIP, rule.DstIP, ""))
	}

	return entries, nil
}

// capitalizeFirst returns s with the first letter uppercased.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
