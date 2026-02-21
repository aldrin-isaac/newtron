package node

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// VRF Operations
// ============================================================================

// VRFConfig holds configuration options for CreateVRF.
type VRFConfig struct{}

// vrfConfig returns the CONFIG_DB entries for creating a VRF.
// The VRF entry has no vni — L3VNI is added by ipvpnConfig.
func vrfConfig(name string) []CompositeEntry {
	return []CompositeEntry{
		{Table: "VRF", Key: name, Fields: map[string]string{}},
	}
}

// staticRouteConfig returns the CONFIG_DB entries for a static route.
// Key format: "vrfName|prefix" for non-default VRF, just "prefix" for default.
func staticRouteConfig(vrfName, prefix, nextHop string, metric int) []CompositeEntry {
	var routeKey string
	if vrfName == "" || vrfName == "default" {
		routeKey = prefix
	} else {
		routeKey = fmt.Sprintf("%s|%s", vrfName, prefix)
	}

	fields := map[string]string{
		"nexthop": nextHop,
	}
	if metric > 0 {
		fields["distance"] = fmt.Sprintf("%d", metric)
	}

	return []CompositeEntry{
		{Table: "STATIC_ROUTE", Key: routeKey, Fields: fields},
	}
}

// ipvpnConfig returns the CONFIG_DB entries for binding a VRF to an IP-VPN.
// This includes VRF|vni, BGP_GLOBALS, BGP_GLOBALS_AF (ipv4 + l2vpn_evpn),
// ROUTE_REDISTRIBUTE, and BGP_GLOBALS_EVPN_RT entries.
func ipvpnConfig(vrfName string, ipvpnDef *spec.IPVPNSpec, underlayASN int, routerID string) []CompositeEntry {
	var entries []CompositeEntry

	// VRF|vni (standard SONiC L3VNI binding).
	// vrfmgrd reads this and writes VRF_TABLE|vni to APP_DB.
	// VRFOrch reads VRF_TABLE|vni and sets l3_vni on the VRF object.
	// RouteOrch requires l3_vni != 0 before programming EVPN type-5 routes.
	// frrcfgd vrf_handler should also read this and call 'vrf X; vni N' in
	// zebra — but the pub/sub path is broken on CiscoVS (see RCA-039).  The
	// newtron-vni-poll thread in frrcfgd.py.tmpl polls VRF table directly as a
	// bug-fix fallback.
	entries = append(entries, CompositeEntry{
		Table:  "VRF",
		Key:    vrfName,
		Fields: map[string]string{"vni": fmt.Sprintf("%d", ipvpnDef.L3VNI)},
	})

	// BGP_GLOBALS for the VRF — frrcfgd's __get_vrf_asn() reads local_asn here.
	if underlayASN != 0 {
		entries = append(entries, CompositeEntry{
			Table: "BGP_GLOBALS",
			Key:   vrfName,
			Fields: map[string]string{
				"local_asn": fmt.Sprintf("%d", underlayASN),
				"router_id": routerID,
			},
		})
	}

	// BGP_GLOBALS_AF|ipv4_unicast — opens 'address-family ipv4 unicast' block in FRR.
	entries = append(entries, CompositeEntry{
		Table:  "BGP_GLOBALS_AF",
		Key:    fmt.Sprintf("%s|ipv4_unicast", vrfName),
		Fields: map[string]string{},
	})

	// BGP_GLOBALS_AF|l2vpn_evpn — frrcfgd global_af_key_map maps 'advertise-ipv4-unicast'
	// (HYPHEN, not underscore) to 'advertise ipv4 unicast' in 'address-family l2vpn evpn'.
	entries = append(entries, CompositeEntry{
		Table:  "BGP_GLOBALS_AF",
		Key:    fmt.Sprintf("%s|l2vpn_evpn", vrfName),
		Fields: map[string]string{"advertise-ipv4-unicast": "true"},
	})

	// ROUTE_REDISTRIBUTE → 'redistribute connected' in ipv4 unicast AF for this VRF.
	entries = append(entries, CompositeEntry{
		Table:  "ROUTE_REDISTRIBUTE",
		Key:    fmt.Sprintf("%s|connected|bgp|ipv4", vrfName),
		Fields: map[string]string{},
	})

	// BGP_GLOBALS_EVPN_RT → 'route-target both {rt}' in 'address-family l2vpn evpn'.
	// frrcfgd bgp_globals_evpn_rt_handler watches this table (NOT BGP_EVPN_VNI).
	// Key: {vrf}|L2VPN_EVPN|{rt} (uppercase AF); field: route-target-type (HYPHEN).
	for _, rt := range ipvpnDef.RouteTargets {
		entries = append(entries, CompositeEntry{
			Table:  "BGP_GLOBALS_EVPN_RT",
			Key:    fmt.Sprintf("%s|L2VPN_EVPN|%s", vrfName, rt),
			Fields: map[string]string{"route-target-type": "both"},
		})
	}

	return entries
}

// CreateVRF creates a new VRF.
func (n *Node) CreateVRF(ctx context.Context, name string, opts VRFConfig) (*ChangeSet, error) {
	if err := n.precondition("create-vrf", name).
		RequireVRFNotExists(name).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.create-vrf")
	for _, e := range vrfConfig(name) {
		cs.Add(e.Table, e.Key, ChangeAdd, nil, e.Fields)
	}

	util.WithDevice(n.name).Infof("Created VRF %s", name)
	return cs, nil
}

// DeleteVRF removes a VRF.
func (n *Node) DeleteVRF(ctx context.Context, name string) (*ChangeSet, error) {
	if err := n.precondition("delete-vrf", name).
		RequireVRFExists(name).
		Result(); err != nil {
		return nil, err
	}

	// Check no interfaces are bound to this VRF
	vrfInfo, _ := n.GetVRF(name)
	if vrfInfo != nil && len(vrfInfo.Interfaces) > 0 {
		return nil, fmt.Errorf("VRF %s has interfaces bound: %v", name, vrfInfo.Interfaces)
	}

	cs := NewChangeSet(n.name, "device.delete-vrf")

	cs.Add("VRF", name, ChangeDelete, nil, nil)

	// Remove BGP_GLOBALS entry written by BindIPVPN.
	cs.Add("BGP_GLOBALS", name, ChangeDelete, nil, nil)

	util.WithDevice(n.name).Infof("Deleted VRF %s", name)
	return cs, nil
}

// ============================================================================
// VRF Interface Binding
// ============================================================================

// AddVRFInterface binds an interface to a VRF.
func (n *Node) AddVRFInterface(ctx context.Context, vrfName, intfName string) (*ChangeSet, error) {
	intfName = util.NormalizeInterfaceName(intfName)

	if err := n.precondition("add-vrf-interface", vrfName).
		RequireVRFExists(vrfName).
		RequireInterfaceExists(intfName).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.add-vrf-interface")

	cs.Add("INTERFACE", intfName, ChangeModify, nil, map[string]string{
		"vrf_name": vrfName,
	})

	util.WithDevice(n.name).Infof("Bound interface %s to VRF %s", intfName, vrfName)
	return cs, nil
}

// RemoveVRFInterface removes a VRF binding from an interface.
func (n *Node) RemoveVRFInterface(ctx context.Context, vrfName, intfName string) (*ChangeSet, error) {
	if err := n.precondition("remove-vrf-interface", vrfName).Result(); err != nil {
		return nil, err
	}

	intfName = util.NormalizeInterfaceName(intfName)

	cs := NewChangeSet(n.name, "device.remove-vrf-interface")

	cs.Add("INTERFACE", intfName, ChangeModify, nil, map[string]string{
		"vrf_name": "",
	})

	util.WithDevice(n.name).Infof("Removed VRF binding from interface %s", intfName)
	return cs, nil
}

// ============================================================================
// IP-VPN Binding (L3VNI)
// ============================================================================

// BindIPVPN binds a VRF to an IP-VPN definition (creates L3VNI mapping and BGP EVPN config).
func (n *Node) BindIPVPN(ctx context.Context, vrfName string, ipvpnDef *spec.IPVPNSpec) (*ChangeSet, error) {
	if err := n.precondition("bind-ipvpn", vrfName).
		RequireVTEPConfigured().
		RequireVRFExists(vrfName).
		Result(); err != nil {
		return nil, err
	}

	resolved := n.Resolved()

	cs := NewChangeSet(n.name, "device.bind-ipvpn")
	for _, e := range ipvpnConfig(vrfName, ipvpnDef, resolved.UnderlayASN, resolved.RouterID) {
		cs.Add(e.Table, e.Key, ChangeModify, nil, e.Fields)
	}

	util.WithDevice(n.name).Infof("Bound VRF %s to IP-VPN (L3VNI %d, %d route-targets)", vrfName, ipvpnDef.L3VNI, len(ipvpnDef.RouteTargets))
	return cs, nil
}

// UnbindIPVPN removes the IP-VPN binding from a VRF (removes L3VNI mapping and BGP EVPN config).
func (n *Node) UnbindIPVPN(ctx context.Context, vrfName string) (*ChangeSet, error) {
	if err := n.precondition("unbind-ipvpn", vrfName).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.unbind-ipvpn")

	// Clear VRF|vni (standard SONiC: clear L3VNI binding).
	cs.Add("VRF", vrfName, ChangeModify, nil, map[string]string{
		"vni": "",
	})

	// Remove BGP_GLOBALS_AF l2vpn_evpn and ipv4_unicast entries.
	cs.Add("BGP_GLOBALS_AF", fmt.Sprintf("%s|l2vpn_evpn", vrfName), ChangeDelete, nil, nil)
	cs.Add("BGP_GLOBALS_AF", fmt.Sprintf("%s|ipv4_unicast", vrfName), ChangeDelete, nil, nil)

	// Remove ROUTE_REDISTRIBUTE entry.
	cs.Add("ROUTE_REDISTRIBUTE", fmt.Sprintf("%s|connected|bgp|ipv4", vrfName), ChangeDelete, nil, nil)

	// Remove BGP_GLOBALS_EVPN_RT entries for this VRF (scan configDB for matching keys).
	if n.configDB != nil {
		for key := range n.configDB.BGPGlobalsEVPNRT {
			// Key format: {vrf}|L2VPN_EVPN|{rt} — prefix-match on VRF.
			prefix := vrfName + "|"
			if len(key) > len(prefix) && key[:len(prefix)] == prefix {
				cs.Add("BGP_GLOBALS_EVPN_RT", key, ChangeDelete, nil, nil)
			}
		}
	}

	util.WithDevice(n.name).Infof("Unbound IP-VPN from VRF %s", vrfName)
	return cs, nil
}

// ============================================================================
// Static Routes
// ============================================================================

// AddStaticRoute adds a static route to a VRF.
func (n *Node) AddStaticRoute(ctx context.Context, vrfName, prefix, nextHop string, metric int) (*ChangeSet, error) {
	if err := n.precondition("add-static-route", prefix).
		Check(vrfName == "" || vrfName == "default" || n.VRFExists(vrfName),
			"VRF must exist", fmt.Sprintf("VRF '%s' not found", vrfName)).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.add-static-route")
	for _, e := range staticRouteConfig(vrfName, prefix, nextHop, metric) {
		cs.Add(e.Table, e.Key, ChangeAdd, nil, e.Fields)
	}

	util.WithDevice(n.name).Infof("Added static route %s via %s (VRF %s)", prefix, nextHop, vrfName)
	return cs, nil
}

// RemoveStaticRoute removes a static route from a VRF.
func (n *Node) RemoveStaticRoute(ctx context.Context, vrfName, prefix string) (*ChangeSet, error) {
	if err := n.precondition("remove-static-route", prefix).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.remove-static-route")

	// Key format: "vrfName|prefix" for non-default VRF, just "prefix" for default
	var routeKey string
	if vrfName == "" || vrfName == "default" {
		routeKey = prefix
	} else {
		routeKey = fmt.Sprintf("%s|%s", vrfName, prefix)
	}

	cs.Add("STATIC_ROUTE", routeKey, ChangeDelete, nil, nil)

	util.WithDevice(n.name).Infof("Removed static route %s (VRF %s)", prefix, vrfName)
	return cs, nil
}
