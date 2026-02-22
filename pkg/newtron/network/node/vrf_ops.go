package node

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
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
		Key:    BGPGlobalsAFKey(vrfName, "ipv4_unicast"),
		Fields: map[string]string{},
	})

	// BGP_GLOBALS_AF|l2vpn_evpn — frrcfgd global_af_key_map maps 'advertise-ipv4-unicast'
	// (HYPHEN, not underscore) to 'advertise ipv4 unicast' in 'address-family l2vpn evpn'.
	entries = append(entries, CompositeEntry{
		Table:  "BGP_GLOBALS_AF",
		Key:    BGPGlobalsAFKey(vrfName, "l2vpn_evpn"),
		Fields: map[string]string{"advertise-ipv4-unicast": "true"},
	})

	// ROUTE_REDISTRIBUTE → 'redistribute connected' in ipv4 unicast AF for this VRF.
	entries = append(entries, CompositeEntry{
		Table:  "ROUTE_REDISTRIBUTE",
		Key:    RouteRedistributeKey(vrfName, "connected", "ipv4"),
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
	cs, err := n.op("create-vrf", name, ChangeAdd,
		func(pc *PreconditionChecker) { pc.RequireVRFNotExists(name) },
		func() []CompositeEntry { return vrfConfig(name) })
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("cannot delete VRF %s — %d interface(s) still bound: %v — remove their services or VRF bindings first",
			name, len(vrfInfo.Interfaces), vrfInfo.Interfaces)
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

	cs, err := n.op("add-vrf-interface", vrfName, ChangeModify,
		func(pc *PreconditionChecker) { pc.RequireVRFExists(vrfName).RequireInterfaceExists(intfName) },
		func() []CompositeEntry {
			return []CompositeEntry{{Table: "INTERFACE", Key: intfName, Fields: map[string]string{"vrf_name": vrfName}}}
		})
	if err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Bound interface %s to VRF %s", intfName, vrfName)
	return cs, nil
}

// RemoveVRFInterface removes a VRF binding from an interface.
func (n *Node) RemoveVRFInterface(ctx context.Context, vrfName, intfName string) (*ChangeSet, error) {
	intfName = util.NormalizeInterfaceName(intfName)

	cs, err := n.op("remove-vrf-interface", vrfName, ChangeModify, nil,
		func() []CompositeEntry {
			return []CompositeEntry{{Table: "INTERFACE", Key: intfName, Fields: map[string]string{"vrf_name": ""}}}
		})
	if err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Removed VRF binding from interface %s", intfName)
	return cs, nil
}

// ============================================================================
// IP-VPN Binding (L3VNI)
// ============================================================================

// BindIPVPN binds a VRF to an IP-VPN definition (creates L3VNI mapping and BGP EVPN config).
func (n *Node) BindIPVPN(ctx context.Context, vrfName string, ipvpnDef *spec.IPVPNSpec) (*ChangeSet, error) {
	resolved := n.Resolved()
	cs, err := n.op("bind-ipvpn", vrfName, ChangeModify,
		func(pc *PreconditionChecker) { pc.RequireVTEPConfigured().RequireVRFExists(vrfName) },
		func() []CompositeEntry { return ipvpnConfig(vrfName, ipvpnDef, resolved.UnderlayASN, resolved.RouterID) })
	if err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Bound VRF %s to IP-VPN (L3VNI %d, %d route-targets)", vrfName, ipvpnDef.L3VNI, len(ipvpnDef.RouteTargets))
	return cs, nil
}

// ipvpnUnbindConfig returns the delete entries for unbinding an IP-VPN from a VRF.
// Does NOT include the VRF|vni modify (clearing vni) — that's a ChangeModify, not delete.
func ipvpnUnbindConfig(configDB *sonic.ConfigDB, vrfName string) []CompositeEntry {
	var entries []CompositeEntry

	// Remove BGP_GLOBALS_AF l2vpn_evpn and ipv4_unicast entries.
	entries = append(entries, CompositeEntry{Table: "BGP_GLOBALS_AF", Key: BGPGlobalsAFKey(vrfName, "l2vpn_evpn")})
	entries = append(entries, CompositeEntry{Table: "BGP_GLOBALS_AF", Key: BGPGlobalsAFKey(vrfName, "ipv4_unicast")})

	// Remove ROUTE_REDISTRIBUTE entry.
	entries = append(entries, CompositeEntry{Table: "ROUTE_REDISTRIBUTE", Key: RouteRedistributeKey(vrfName, "connected", "ipv4")})

	// Remove BGP_GLOBALS_EVPN_RT entries for this VRF (scan configDB for matching keys).
	if configDB != nil {
		for key := range configDB.BGPGlobalsEVPNRT {
			// Key format: {vrf}|L2VPN_EVPN|{rt} — prefix-match on VRF.
			prefix := vrfName + "|"
			if len(key) > len(prefix) && key[:len(prefix)] == prefix {
				entries = append(entries, CompositeEntry{Table: "BGP_GLOBALS_EVPN_RT", Key: key})
			}
		}
	}

	return entries
}

// UnbindIPVPN removes the IP-VPN binding from a VRF (removes L3VNI mapping and BGP EVPN config).
func (n *Node) UnbindIPVPN(ctx context.Context, vrfName string) (*ChangeSet, error) {
	if err := n.precondition("unbind-ipvpn", vrfName).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.unbind-ipvpn")

	// Clear VRF|vni (standard SONiC: clear L3VNI binding) — this is a modify, not delete.
	cs.Add("VRF", vrfName, ChangeModify, nil, map[string]string{
		"vni": "",
	})

	// Delete the remaining IP-VPN entries.
	for _, e := range ipvpnUnbindConfig(n.configDB, vrfName) {
		cs.Add(e.Table, e.Key, ChangeDelete, nil, nil)
	}

	util.WithDevice(n.name).Infof("Unbound IP-VPN from VRF %s", vrfName)
	return cs, nil
}

// ============================================================================
// Static Routes
// ============================================================================

// AddStaticRoute adds a static route to a VRF.
func (n *Node) AddStaticRoute(ctx context.Context, vrfName, prefix, nextHop string, metric int) (*ChangeSet, error) {
	cs, err := n.op("add-static-route", prefix, ChangeAdd,
		func(pc *PreconditionChecker) {
			pc.Check(vrfName == "" || vrfName == "default" || n.VRFExists(vrfName),
				"VRF must exist", fmt.Sprintf("VRF '%s' not found", vrfName))
		},
		func() []CompositeEntry { return staticRouteConfig(vrfName, prefix, nextHop, metric) })
	if err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Added static route %s via %s (VRF %s)", prefix, nextHop, vrfName)
	return cs, nil
}

// RemoveStaticRoute removes a static route from a VRF.
func (n *Node) RemoveStaticRoute(ctx context.Context, vrfName, prefix string) (*ChangeSet, error) {
	cs, err := n.op("remove-static-route", prefix, ChangeDelete, nil,
		func() []CompositeEntry {
			var routeKey string
			if vrfName == "" || vrfName == "default" {
				routeKey = prefix
			} else {
				routeKey = fmt.Sprintf("%s|%s", vrfName, prefix)
			}
			return []CompositeEntry{{Table: "STATIC_ROUTE", Key: routeKey}}
		})
	if err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Removed static route %s (VRF %s)", prefix, vrfName)
	return cs, nil
}

// ============================================================================
// VRF Data Types and Queries
// ============================================================================

// VRFInfo represents VRF data assembled from config_db for operations.
type VRFInfo struct {
	Name       string
	L3VNI      int
	Interfaces []string
}

// VRFExists checks if a VRF exists.
func (n *Node) VRFExists(name string) bool { return n.configDB.HasVRF(name) }

// GetVRF retrieves VRF information from config_db.
func (n *Node) GetVRF(name string) (*VRFInfo, error) {
	if n.configDB == nil {
		return nil, util.ErrNotConnected
	}

	vrfEntry, ok := n.configDB.VRF[name]
	if !ok {
		return nil, fmt.Errorf("VRF %s not found", name)
	}

	info := &VRFInfo{Name: name}

	// Parse L3VNI
	if vrfEntry.VNI != "" {
		fmt.Sscanf(vrfEntry.VNI, "%d", &info.L3VNI)
	}

	// Find interfaces bound to this VRF from INTERFACE table
	seen := make(map[string]bool)
	for key, intf := range n.configDB.Interface {
		// Key could be "Ethernet0" or "Ethernet0|10.1.1.1/24"
		parts := splitConfigDBKey(key)
		intfName := parts[0]
		if intf.VRFName == name && !seen[intfName] {
			seen[intfName] = true
			info.Interfaces = append(info.Interfaces, intfName)
		}
	}

	// Also check VLAN_INTERFACE for SVIs in this VRF
	for key := range n.configDB.VLANInterface {
		parts := splitConfigDBKey(key)
		vlanName := parts[0]
		// VLANInterface value contains vrf_name
		if vals, ok := n.configDB.VLANInterface[vlanName]; ok {
			if vals["vrf_name"] == name && !seen[vlanName] {
				seen[vlanName] = true
				info.Interfaces = append(info.Interfaces, vlanName)
			}
		}
	}

	return info, nil
}

// ListVRFs returns all VRF names on this device.
func (n *Node) ListVRFs() []string {
	if n.configDB == nil {
		return nil
	}

	names := make([]string, 0, len(n.configDB.VRF))
	for name := range n.configDB.VRF {
		names = append(names, name)
	}
	return names
}

// ============================================================================
// Route and Neighbor Observations (VRF-scoped)
// ============================================================================

// GetRoute reads a route from APP_DB (Redis DB 0).
// Returns nil RouteEntry (not error) if the prefix is not present.
// Single-shot read — does not poll or retry.
func (n *Node) GetRoute(ctx context.Context, vrf, prefix string) (*sonic.RouteEntry, error) {
	if !n.connected {
		return nil, util.ErrNotConnected
	}
	if n.conn == nil || n.conn.AppDBClient() == nil {
		return nil, fmt.Errorf("APP_DB client not connected on %s", n.name)
	}
	return n.conn.AppDBClient().GetRoute(vrf, prefix)
}

// GetRouteASIC reads a route from ASIC_DB (Redis DB 1) by resolving the SAI
// object chain. Returns nil RouteEntry (not error) if not programmed in ASIC.
// Single-shot read — does not poll or retry.
func (n *Node) GetRouteASIC(ctx context.Context, vrf, prefix string) (*sonic.RouteEntry, error) {
	if !n.connected {
		return nil, util.ErrNotConnected
	}
	if n.conn == nil || n.conn.AsicDBClient() == nil {
		return nil, fmt.Errorf("ASIC_DB client not connected on %s", n.name)
	}
	return n.conn.AsicDBClient().GetRouteASIC(vrf, prefix, n.configDB)
}

// GetNeighbor reads a neighbor (ARP/NDP) entry from STATE_DB.
// Returns nil (not error) if the entry does not exist.
func (n *Node) GetNeighbor(ctx context.Context, iface, ip string) (*sonic.NeighEntry, error) {
	if !n.connected {
		return nil, util.ErrNotConnected
	}
	if n.conn == nil || n.conn.StateClient() == nil {
		return nil, fmt.Errorf("STATE_DB client not connected on %s", n.name)
	}
	return n.conn.StateClient().GetNeighbor(iface, ip)
}
