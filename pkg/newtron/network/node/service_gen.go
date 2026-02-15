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
	Params        map[string]string // topology params (peer_as, route_reflector_client, next_hop_self)
	PeerAS        int               // CLI peer-as (interface_ops path only)
	LocalAS       int               // device AS number
	UnderlayASN   int               // eBGP underlay ASN (0 = use LocalAS)
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

	// VLAN creation (for L2/IRB)
	if (svc.ServiceType == spec.ServiceTypeL2 || svc.ServiceType == spec.ServiceTypeIRB) && svc.VLAN > 0 {
		vlanName := fmt.Sprintf("Vlan%d", svc.VLAN)
		entries = append(entries, CompositeEntry{
			Table:  "VLAN",
			Key:    vlanName,
			Fields: map[string]string{"vlanid": fmt.Sprintf("%d", svc.VLAN)},
		})

		// L2VNI mapping
		if macvpnDef.L2VNI > 0 {
			mapKey := fmt.Sprintf("vtep1|map_%d_%s", macvpnDef.L2VNI, vlanName)
			entries = append(entries, CompositeEntry{
				Table:  "VXLAN_TUNNEL_MAP",
				Key:    mapKey,
				Fields: map[string]string{"vlan": vlanName, "vni": fmt.Sprintf("%d", macvpnDef.L2VNI)},
			})
		}

		// ARP suppression
		if macvpnDef.ARPSuppression {
			entries = append(entries, CompositeEntry{
				Table:  "SUPPRESS_VLAN_NEIGH",
				Key:    vlanName,
				Fields: map[string]string{"suppress": "on"},
			})
		}
	}

	// VRF creation (for L3/IRB with per-interface VRF)
	vrfName := ""
	if (svc.ServiceType == spec.ServiceTypeL3 || svc.ServiceType == spec.ServiceTypeIRB) &&
		svc.VRFType == spec.VRFTypeInterface {
		vrfName = util.DeriveVRFName(svc.VRFType, p.ServiceName, p.InterfaceName)
		vrfFields := map[string]string{}
		if ipvpnDef != nil && ipvpnDef.L3VNI > 0 {
			vrfFields["vni"] = fmt.Sprintf("%d", ipvpnDef.L3VNI)
		}
		entries = append(entries, CompositeEntry{
			Table:  "VRF",
			Key:    vrfName,
			Fields: vrfFields,
		})

		// L3VNI mapping
		if ipvpnDef != nil && ipvpnDef.L3VNI > 0 {
			mapKey := fmt.Sprintf("vtep1|map_%d_%s", ipvpnDef.L3VNI, vrfName)
			entries = append(entries, CompositeEntry{
				Table:  "VXLAN_TUNNEL_MAP",
				Key:    mapKey,
				Fields: map[string]string{"vrf": vrfName, "vni": fmt.Sprintf("%d", ipvpnDef.L3VNI)},
			})

			// BGP EVPN route targets for the VRF
			entries = append(entries, generateRouteTargetEntries(vrfName, ipvpnDef)...)
		}
	} else if svc.VRFType == spec.VRFTypeShared {
		vrfName = svc.IPVPN
	}

	// Shared VRF creation (for L3/IRB with shared VRF)
	// The topology provisioner always emits these entries (idempotent overwrite).
	// The interface_ops caller filters them out when the VRF already exists.
	if (svc.ServiceType == spec.ServiceTypeL3 || svc.ServiceType == spec.ServiceTypeIRB) &&
		svc.VRFType == spec.VRFTypeShared && svc.IPVPN != "" {
		vrfFields := map[string]string{}
		if ipvpnDef != nil && ipvpnDef.L3VNI > 0 {
			vrfFields["vni"] = fmt.Sprintf("%d", ipvpnDef.L3VNI)
		}
		entries = append(entries, CompositeEntry{
			Table:  "VRF",
			Key:    svc.IPVPN,
			Fields: vrfFields,
		})

		if ipvpnDef != nil && ipvpnDef.L3VNI > 0 {
			mapKey := fmt.Sprintf("vtep1|map_%d_%s", ipvpnDef.L3VNI, svc.IPVPN)
			entries = append(entries, CompositeEntry{
				Table:  "VXLAN_TUNNEL_MAP",
				Key:    mapKey,
				Fields: map[string]string{"vrf": svc.IPVPN, "vni": fmt.Sprintf("%d", ipvpnDef.L3VNI)},
			})

			entries = append(entries, generateRouteTargetEntries(svc.IPVPN, ipvpnDef)...)
		}
	}

	// Interface configuration based on service type
	switch svc.ServiceType {
	case spec.ServiceTypeL2:
		if svc.VLAN > 0 {
			vlanName := fmt.Sprintf("Vlan%d", svc.VLAN)
			memberKey := fmt.Sprintf("%s|%s", vlanName, p.InterfaceName)
			entries = append(entries, CompositeEntry{
				Table:  "VLAN_MEMBER",
				Key:    memberKey,
				Fields: map[string]string{"tagging_mode": "untagged"},
			})
		}

	case spec.ServiceTypeL3:
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
			ipKey := fmt.Sprintf("%s|%s", p.InterfaceName, p.IPAddress)
			entries = append(entries, CompositeEntry{
				Table:  "INTERFACE",
				Key:    ipKey,
				Fields: map[string]string{},
			})
		}

	case spec.ServiceTypeIRB:
		if svc.VLAN > 0 {
			vlanName := fmt.Sprintf("Vlan%d", svc.VLAN)
			memberKey := fmt.Sprintf("%s|%s", vlanName, p.InterfaceName)
			entries = append(entries, CompositeEntry{
				Table:  "VLAN_MEMBER",
				Key:    memberKey,
				Fields: map[string]string{"tagging_mode": "tagged"},
			})

			vlanIntfFields := map[string]string{}
			if vrfName != "" {
				vlanIntfFields["vrf_name"] = vrfName
			}
			entries = append(entries, CompositeEntry{
				Table:  "VLAN_INTERFACE",
				Key:    vlanName,
				Fields: vlanIntfFields,
			})

			if svc.AnycastGateway != "" {
				sviIPKey := fmt.Sprintf("%s|%s", vlanName, svc.AnycastGateway)
				entries = append(entries, CompositeEntry{
					Table:  "VLAN_INTERFACE",
					Key:    sviIPKey,
					Fields: map[string]string{},
				})
			}

			if svc.AnycastMAC != "" {
				entries = append(entries, CompositeEntry{
					Table:  "SAG_GLOBAL",
					Key:    "IPv4",
					Fields: map[string]string{"gwmac": svc.AnycastMAC},
				})
			}
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
		bgpEntries, err := generateBGPEntries(svc, p)
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

// generateBGPEntries generates BGP_NEIGHBOR and BGP_NEIGHBOR_AF entries.
//
// Bug fixes vs prior duplicated code:
//   - Uses UnderlayASN with fallback to LocalAS (fix #2)
//   - Uses "admin_status": "true" for BGP_NEIGHBOR_AF activation (fix #1)
func generateBGPEntries(svc *spec.ServiceSpec, p ServiceEntryParams) ([]CompositeEntry, error) {
	if svc.Routing == nil || svc.Routing.Protocol != spec.RoutingProtocolBGP {
		return nil, nil
	}

	var entries []CompositeEntry
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
		// First try topology params, then CLI PeerAS
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

	// Bug fix #2: use UnderlayASN with fallback to LocalAS
	localAS := p.UnderlayASN
	if localAS == 0 {
		localAS = p.LocalAS
	}
	if localAS == 0 {
		return nil, fmt.Errorf("device has no AS number configured")
	}

	localIP, _ := util.SplitIPMask(p.IPAddress)

	// Determine VRF name for BGP key
	vrfName := ""
	if (svc.ServiceType == spec.ServiceTypeL3 || svc.ServiceType == spec.ServiceTypeIRB) &&
		svc.VRFType == spec.VRFTypeInterface {
		vrfName = util.DeriveVRFName(svc.VRFType, p.ServiceName, p.InterfaceName)
	} else if svc.VRFType == spec.VRFTypeShared {
		vrfName = svc.IPVPN
	}

	// BGP neighbor
	neighborFields := map[string]string{
		"asn":          fmt.Sprintf("%d", peerAS),
		"local_asn":    fmt.Sprintf("%d", localAS),
		"local_addr":   localIP,
		"admin_status": "up",
	}
	if vrfName != "" {
		neighborFields["vrf_name"] = vrfName
	}

	vrfKey := "default"
	if vrfName != "" {
		vrfKey = vrfName
	}
	entries = append(entries, CompositeEntry{
		Table:  "BGP_NEIGHBOR",
		Key:    fmt.Sprintf("%s|%s", vrfKey, peerIP),
		Fields: neighborFields,
	})

	// Bug fix #1: use "admin_status" (not "activate") for BGP_NEIGHBOR_AF
	afKey := fmt.Sprintf("%s|%s|ipv4_unicast", vrfKey, peerIP)
	afFields := map[string]string{"admin_status": "true"}

	// Optional RR-client and next-hop-self from topology params
	if val, ok := p.Params["route_reflector_client"]; ok && val == "true" {
		afFields["rrclient"] = "true"
	}
	if val, ok := p.Params["next_hop_self"]; ok && val == "true" {
		afFields["nhself"] = "true"
	}

	entries = append(entries, CompositeEntry{
		Table:  "BGP_NEIGHBOR_AF",
		Key:    afKey,
		Fields: afFields,
	})

	return entries, nil
}

// generateServiceACLEntries generates ACL table and rule entries for a service filter.
func generateServiceACLEntries(sp SpecProvider, serviceName, filterSpecName, interfaceName, stage string) ([]CompositeEntry, error) {
	filterSpec, err := sp.GetFilterSpec(filterSpecName)
	if err != nil {
		return nil, fmt.Errorf("filter spec '%s' not found", filterSpecName)
	}

	var entries []CompositeEntry
	direction := "in"
	if stage == "egress" {
		direction = "out"
	}
	aclName := util.DeriveACLName(serviceName, direction)

	// ACL table
	entries = append(entries, CompositeEntry{
		Table: "ACL_TABLE",
		Key:   aclName,
		Fields: map[string]string{
			"type":        "L3",
			"stage":       stage,
			"ports":       interfaceName,
			"policy_desc": fmt.Sprintf("%s filter for %s", capitalizeFirst(stage), serviceName),
		},
	})

	// ACL rules from filter spec — unified version with CoS→TC support (fix #4)
	for _, rule := range filterSpec.Rules {
		ruleKey := fmt.Sprintf("%s|RULE_%d", aclName, rule.Sequence)
		fields := buildACLRuleFields(rule, rule.SrcIP, rule.DstIP)
		entries = append(entries, CompositeEntry{
			Table:  "ACL_RULE",
			Key:    ruleKey,
			Fields: fields,
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
	if rule.Policer != "" {
		fields["POLICER"] = rule.Policer
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

// generateRouteTargetEntries generates BGP EVPN route target entries for a VRF.
func generateRouteTargetEntries(vrfName string, ipvpnDef *spec.IPVPNSpec) []CompositeEntry {
	var entries []CompositeEntry

	afKey := fmt.Sprintf("%s|l2vpn_evpn", vrfName)
	afFields := map[string]string{
		"advertise_ipv4_unicast": "true",
	}
	if len(ipvpnDef.ImportRT) > 0 {
		afFields["route_target_import_evpn"] = strings.Join(ipvpnDef.ImportRT, ",")
	}
	if len(ipvpnDef.ExportRT) > 0 {
		afFields["route_target_export_evpn"] = strings.Join(ipvpnDef.ExportRT, ",")
	}
	entries = append(entries, CompositeEntry{
		Table:  "BGP_GLOBALS_AF",
		Key:    afKey,
		Fields: afFields,
	})

	if ipvpnDef.L3VNI > 0 && (len(ipvpnDef.ImportRT) > 0 || len(ipvpnDef.ExportRT) > 0) {
		vniKey := fmt.Sprintf("%s|%d", vrfName, ipvpnDef.L3VNI)
		vniFields := map[string]string{
			"rd": "auto",
		}
		if len(ipvpnDef.ImportRT) > 0 {
			vniFields["route_target_import"] = strings.Join(ipvpnDef.ImportRT, ",")
		}
		if len(ipvpnDef.ExportRT) > 0 {
			vniFields["route_target_export"] = strings.Join(ipvpnDef.ExportRT, ",")
		}
		entries = append(entries, CompositeEntry{
			Table:  "BGP_EVPN_VNI",
			Key:    vniKey,
			Fields: vniFields,
		})
	}

	return entries
}

// capitalizeFirst returns s with the first letter uppercased.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
