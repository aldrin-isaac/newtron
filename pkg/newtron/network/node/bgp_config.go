package node

import (
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/util"
)

// BGPNeighborOpts controls optional aspects of BGP neighbor configuration.
type BGPNeighborOpts struct {
	Description      string
	EBGPMultihop     bool   // set ebgp_multihop (value "true" for loopback, TTL string for explicit)
	MultihopTTL      string // explicit TTL value (e.g., "255"); if empty and EBGPMultihop=true, uses "true"
	ActivateIPv4     bool   // activate ipv4_unicast AF (default true for direct peers)
	ActivateEVPN     bool   // activate l2vpn_evpn AF
	VRF              string // VRF name (default "default")
	RRClient         bool   // route-reflector-client on ipv4_unicast AF
	NextHopSelf      bool   // next-hop-self on ipv4_unicast AF
	NextHopUnchanged bool   // nexthop_unchanged on l2vpn_evpn AF
	ActivateIPv6     bool   // activate ipv6_unicast AF
	RRClientIPv6     bool   // rrclient on ipv6_unicast AF
	NextHopSelfIPv6  bool   // nhself on ipv6_unicast AF
	RRClientEVPN     bool   // rrclient on l2vpn_evpn AF
	PeerGroup        string // peer group name (for service-level BGP neighbors, per Principle 36)
}

// CreateBGPNeighborConfig returns sonic.Entry for a BGP_NEIGHBOR + BGP_NEIGHBOR_AF.
func CreateBGPNeighborConfig(neighborIP string, asn int, localAddr string, opts BGPNeighborOpts) []sonic.Entry {
	var entries []sonic.Entry

	vrf := opts.VRF
	if vrf == "" {
		vrf = "default"
	}

	fields := map[string]string{
		"asn":          fmt.Sprintf("%d", asn),
		"admin_status": "up",
	}
	if localAddr != "" {
		fields["local_addr"] = localAddr
	}
	if opts.Description != "" {
		fields["name"] = opts.Description
	}
	if opts.EBGPMultihop {
		if opts.MultihopTTL != "" {
			fields["ebgp_multihop"] = opts.MultihopTTL
		} else {
			fields["ebgp_multihop"] = "true"
		}
	}
	if opts.PeerGroup != "" {
		fields["peer_group_name"] = opts.PeerGroup
	}

	entries = append(entries, sonic.Entry{
		Table:  "BGP_NEIGHBOR",
		Key:    fmt.Sprintf("%s|%s", vrf, neighborIP),
		Fields: fields,
	})

	// Activate IPv4 unicast (default for most peers)
	if opts.ActivateIPv4 {
		afFields := map[string]string{"admin_status": "true"}
		if opts.RRClient {
			afFields["rrclient"] = "true"
		}
		if opts.NextHopSelf {
			afFields["nhself"] = "true"
		}
		entries = append(entries, sonic.Entry{
			Table:  "BGP_NEIGHBOR_AF",
			Key:    fmt.Sprintf("%s|%s|ipv4_unicast", vrf, neighborIP),
			Fields: afFields,
		})
	}

	// Activate IPv6 unicast
	if opts.ActivateIPv6 {
		afFields := map[string]string{"admin_status": "true"}
		if opts.RRClientIPv6 {
			afFields["rrclient"] = "true"
		}
		if opts.NextHopSelfIPv6 {
			afFields["nhself"] = "true"
		}
		entries = append(entries, sonic.Entry{
			Table:  "BGP_NEIGHBOR_AF",
			Key:    fmt.Sprintf("%s|%s|ipv6_unicast", vrf, neighborIP),
			Fields: afFields,
		})
	}

	// Activate L2VPN EVPN
	if opts.ActivateEVPN {
		evpnFields := map[string]string{"admin_status": "true"}
		if opts.NextHopUnchanged {
			evpnFields["nexthop_unchanged"] = "true"
		}
		if opts.RRClientEVPN {
			evpnFields["rrclient"] = "true"
		}
		entries = append(entries, sonic.Entry{
			Table:  "BGP_NEIGHBOR_AF",
			Key:    fmt.Sprintf("%s|%s|l2vpn_evpn", vrf, neighborIP),
			Fields: evpnFields,
		})
	}

	return entries
}

// DeleteBGPNeighborConfig returns sonic.Entry for deleting a BGP neighbor
// and all its address-family entries.
func DeleteBGPNeighborConfig(vrf, neighborIP string) []sonic.Entry {
	if vrf == "" {
		vrf = "default"
	}

	var entries []sonic.Entry

	// Remove address-family entries first
	for _, af := range []string{"ipv4_unicast", "ipv6_unicast", "l2vpn_evpn"} {
		entries = append(entries, sonic.Entry{
			Table: "BGP_NEIGHBOR_AF",
			Key:   BGPNeighborAFKey(vrf, neighborIP, af),
		})
	}

	// Remove neighbor entry
	entries = append(entries, sonic.Entry{
		Table: "BGP_NEIGHBOR",
		Key:   fmt.Sprintf("%s|%s", vrf, neighborIP),
	})

	return entries
}

// CreateBGPGlobalsConfig returns sonic.Entry for BGP_GLOBALS.
func CreateBGPGlobalsConfig(vrf string, asn int, routerID string, extra map[string]string) []sonic.Entry {
	fields := map[string]string{
		"local_asn": fmt.Sprintf("%d", asn),
		"router_id": routerID,
	}
	for k, v := range extra {
		fields[k] = v
	}
	return []sonic.Entry{
		{Table: "BGP_GLOBALS", Key: vrf, Fields: fields},
	}
}

// BGPGlobalsAFKey returns the CONFIG_DB key for a BGP_GLOBALS_AF entry.
func BGPGlobalsAFKey(vrf, af string) string {
	return fmt.Sprintf("%s|%s", vrf, af)
}

// CreateBGPGlobalsAFConfig returns sonic.Entry for BGP_GLOBALS_AF.
func CreateBGPGlobalsAFConfig(vrf, af string, fields map[string]string) []sonic.Entry {
	if fields == nil {
		fields = map[string]string{}
	}
	return []sonic.Entry{
		{Table: "BGP_GLOBALS_AF", Key: BGPGlobalsAFKey(vrf, af), Fields: fields},
	}
}

// revertRedistributionConfig resets BGP_GLOBALS_AF redistribution fields to false.
// This is the reverse of the redistribution override in addBGPRoutePolicies.
func revertRedistributionConfig(vrfKey string) []sonic.Entry {
	return CreateBGPGlobalsAFConfig(vrfKey, "ipv4_unicast", map[string]string{
		"redistribute_connected": "false",
		"redistribute_static":    "false",
	})
}

// RouteRedistributeKey returns the CONFIG_DB key for a ROUTE_REDISTRIBUTE entry.
func RouteRedistributeKey(vrf, protocol, af string) string {
	return fmt.Sprintf("%s|%s|bgp|%s", vrf, protocol, af)
}

// CreateRouteRedistributeConfig returns sonic.Entry for ROUTE_REDISTRIBUTE.
func CreateRouteRedistributeConfig(vrf, protocol, af string) []sonic.Entry {
	return []sonic.Entry{
		{Table: "ROUTE_REDISTRIBUTE", Key: RouteRedistributeKey(vrf, protocol, af), Fields: map[string]string{}},
	}
}

// BGPNeighborAFKey returns the CONFIG_DB key for a BGP_NEIGHBOR_AF entry.
// Used by callers that need to modify an existing AF entry (e.g. adding route-maps).
func BGPNeighborAFKey(vrf, neighborIP, af string) string {
	return fmt.Sprintf("%s|%s|%s", vrf, neighborIP, af)
}

// deleteBgpGlobalsConfig returns the delete entry for a BGP_GLOBALS entry.
func deleteBgpGlobalsConfig(vrf string) []sonic.Entry {
	return []sonic.Entry{{Table: "BGP_GLOBALS", Key: vrf}}
}

// deleteBgpGlobalsAFConfig returns the delete entry for a BGP_GLOBALS_AF entry.
func deleteBgpGlobalsAFConfig(vrf, af string) []sonic.Entry {
	return []sonic.Entry{{Table: "BGP_GLOBALS_AF", Key: BGPGlobalsAFKey(vrf, af)}}
}

// deleteRouteRedistributeConfig returns the delete entry for a ROUTE_REDISTRIBUTE entry.
func deleteRouteRedistributeConfig(vrf, protocol, af string) []sonic.Entry {
	return []sonic.Entry{{Table: "ROUTE_REDISTRIBUTE", Key: RouteRedistributeKey(vrf, protocol, af)}}
}

// updateDeviceMetadataConfig returns a DEVICE_METADATA entry for updating localhost fields.
func updateDeviceMetadataConfig(fields map[string]string) sonic.Entry {
	return sonic.Entry{Table: "DEVICE_METADATA", Key: "localhost", Fields: fields}
}

// createBgpNeighborAFConfig returns a BGP_NEIGHBOR_AF entry for a specific address family.
func createBgpNeighborAFConfig(vrf, neighborIP, af string, fields map[string]string) sonic.Entry {
	return sonic.Entry{Table: "BGP_NEIGHBOR_AF", Key: BGPNeighborAFKey(vrf, neighborIP, af), Fields: fields}
}

// ============================================================================
// BGP Peer Group Config Functions (pure, no Node state)
// ============================================================================

// BGPPeerGroupKey returns the CONFIG_DB key for a BGP_PEER_GROUP entry.
// Format: vrf|peer_group_name (e.g., "default|TRANSIT")
func BGPPeerGroupKey(vrf, name string) string {
	if vrf == "" {
		vrf = "default"
	}
	return fmt.Sprintf("%s|%s", vrf, name)
}

// BGPPeerGroupAFKey returns the CONFIG_DB key for a BGP_PEER_GROUP_AF entry.
// Format: vrf|peer_group_name|af (e.g., "default|TRANSIT|ipv4_unicast")
func BGPPeerGroupAFKey(vrf, name, af string) string {
	if vrf == "" {
		vrf = "default"
	}
	return fmt.Sprintf("%s|%s|%s", vrf, name, af)
}

// CreateBGPPeerGroupConfig returns entries for a BGP_PEER_GROUP + BGP_PEER_GROUP_AF.
// Peer groups are service-named templates: shared attributes live here, not on neighbors.
// Per DESIGN_PRINCIPLES_NEWTRON.md §17 (BGP Peer Groups).
func CreateBGPPeerGroupConfig(vrf, name string, afFields map[string]string) []sonic.Entry {
	if vrf == "" {
		vrf = "default"
	}
	entries := []sonic.Entry{
		{Table: "BGP_PEER_GROUP", Key: BGPPeerGroupKey(vrf, name), Fields: map[string]string{
			"admin_status": "up",
		}},
		{Table: "BGP_PEER_GROUP_AF", Key: BGPPeerGroupAFKey(vrf, name, "ipv4_unicast"), Fields: util.MergeMaps(
			map[string]string{"admin_status": "true"},
			afFields,
		)},
	}
	return entries
}

// UpdateBGPPeerGroupAF returns an update entry for a peer group's address-family fields.
// Used when the peer group already exists but AF attributes (e.g., route maps) need updating.
func UpdateBGPPeerGroupAF(vrf, name string, afFields map[string]string) sonic.Entry {
	return sonic.Entry{
		Table:  "BGP_PEER_GROUP_AF",
		Key:    BGPPeerGroupAFKey(vrf, name, "ipv4_unicast"),
		Fields: afFields,
	}
}

// DeleteBGPPeerGroupConfig returns delete entries for a peer group and its AF entry.
func DeleteBGPPeerGroupConfig(vrf, name string) []sonic.Entry {
	if vrf == "" {
		vrf = "default"
	}
	return []sonic.Entry{
		{Table: "BGP_PEER_GROUP_AF", Key: BGPPeerGroupAFKey(vrf, name, "ipv4_unicast")},
		{Table: "BGP_PEER_GROUP", Key: BGPPeerGroupKey(vrf, name)},
	}
}

// CreateEVPNPeerGroupConfig returns entries for the EVPN BGP_PEER_GROUP
// used by overlay (loopback-to-loopback) BGP sessions. The only address family
// is l2vpn_evpn — these peers exist solely for EVPN route exchange.
// Shared attributes live on the group; individual neighbors carry only per-peer ASN.
func CreateEVPNPeerGroupConfig(vrf, localAddr string, isEBGP bool) []sonic.Entry {
	if vrf == "" {
		vrf = "default"
	}
	pgFields := map[string]string{
		"admin_status": "up",
		"local_addr":   localAddr,
	}
	if isEBGP {
		pgFields["ebgp_multihop"] = "true"
	}

	evpnFields := map[string]string{"admin_status": "true"}
	if isEBGP {
		evpnFields["nexthop_unchanged"] = "true" // Critical for eBGP overlay (RCA-026)
	}

	return []sonic.Entry{
		{Table: "BGP_PEER_GROUP", Key: BGPPeerGroupKey(vrf, "EVPN"), Fields: pgFields},
		{Table: "BGP_PEER_GROUP_AF", Key: BGPPeerGroupAFKey(vrf, "EVPN", "l2vpn_evpn"), Fields: evpnFields},
	}
}

// DeleteEVPNPeerGroupConfig returns delete entries for the EVPN peer group
// and its AF entry. Reverse of CreateEVPNPeerGroupConfig.
func DeleteEVPNPeerGroupConfig(vrf string) []sonic.Entry {
	if vrf == "" {
		vrf = "default"
	}
	return []sonic.Entry{
		{Table: "BGP_PEER_GROUP_AF", Key: BGPPeerGroupAFKey(vrf, "EVPN", "l2vpn_evpn")},
		{Table: "BGP_PEER_GROUP", Key: BGPPeerGroupKey(vrf, "EVPN")},
	}
}
