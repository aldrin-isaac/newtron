package node

import (
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

// parseRouteTargets splits a comma-separated route target string into a slice.
func parseRouteTargets(csv string) []string {
	if csv == "" {
		return nil
	}
	var rts []string
	for _, rt := range strings.Split(csv, ",") {
		rt = strings.TrimSpace(rt)
		if rt != "" {
			rts = append(rts, rt)
		}
	}
	return rts
}

// VRFConfig holds configuration options for CreateVRF.
type VRFConfig struct{}

// bindIpvpnConfig returns the CONFIG_DB entries for binding a VRF to an IP-VPN.
// This includes VRF|vni, BGP_GLOBALS, BGP_GLOBALS_AF (ipv4 + l2vpn_evpn),
// ROUTE_REDISTRIBUTE, and BGP_GLOBALS_EVPN_RT entries.
func bindIpvpnConfig(vrfName string, ipvpnDef *spec.IPVPNSpec, underlayASN int, routerID string) []sonic.Entry {
	var entries []sonic.Entry

	// VRF|vni (standard SONiC L3VNI binding).
	// vrfmgrd reads this and writes VRF_TABLE|vni to APP_DB.
	// VRFOrch reads VRF_TABLE|vni and sets l3_vni on the VRF object.
	// RouteOrch requires l3_vni != 0 before programming EVPN type-5 routes.
	// frrcfgd vrf_handler should also read this and call 'vrf X; vni N' in
	// zebra — but the pub/sub path is broken on CiscoVS (see RCA-039).  The
	// newtron-vni-poll thread in frrcfgd.py.tmpl polls VRF table directly as a
	// bug-fix fallback.
	entries = append(entries, sonic.Entry{
		Table:  "VRF",
		Key:    vrfName,
		Fields: map[string]string{"vni": fmt.Sprintf("%d", ipvpnDef.L3VNI)},
	})

	// BGP_GLOBALS for the VRF — frrcfgd's __get_vrf_asn() reads local_asn here.
	if underlayASN != 0 {
		entries = append(entries, CreateBGPGlobalsConfig(vrfName, underlayASN, routerID, nil)...)
	}

	// BGP_GLOBALS_AF|ipv4_unicast — opens 'address-family ipv4 unicast' block in FRR.
	entries = append(entries, CreateBGPGlobalsAFConfig(vrfName, "ipv4_unicast", nil)...)

	// BGP_GLOBALS_AF|l2vpn_evpn — frrcfgd global_af_key_map maps 'advertise-ipv4-unicast'
	// (HYPHEN, not underscore) to 'advertise ipv4 unicast' in 'address-family l2vpn evpn'.
	entries = append(entries, CreateBGPGlobalsAFConfig(vrfName, "l2vpn_evpn", map[string]string{
		"advertise-ipv4-unicast": "true",
	})...)

	// ROUTE_REDISTRIBUTE → 'redistribute connected' in ipv4 unicast AF for this VRF.
	entries = append(entries, CreateRouteRedistributeConfig(vrfName, "connected", "ipv4")...)

	// BGP_GLOBALS_EVPN_RT → 'route-target both {rt}' in 'address-family l2vpn evpn'.
	// frrcfgd bgp_globals_evpn_rt_handler watches this table (NOT BGP_EVPN_VNI).
	// Key: {vrf}|L2VPN_EVPN|{rt} (uppercase AF); field: route-target-type (HYPHEN).
	for _, rt := range ipvpnDef.RouteTargets {
		entries = append(entries, sonic.Entry{
			Table:  "BGP_GLOBALS_EVPN_RT",
			Key:    fmt.Sprintf("%s|L2VPN_EVPN|%s", vrfName, rt),
			Fields: map[string]string{"route-target-type": "both"},
		})
	}

	// L3VNI transit VLAN infrastructure for VXLAN data plane decap.
	// FRR knows the VNI (VRF|vni above) but the kernel/SAI needs a bridge domain
	// and VXLAN tunnel map to actually route encapsulated L3 traffic through the VRF.
	// Without these entries, 'show evpn vni {l3vni}' shows State: Down.
	if ipvpnDef.L3VNIVlan > 0 {
		// Transit VLAN (no ports, no IP — purely for VXLAN decap)
		entries = append(entries, createVlanConfig(ipvpnDef.L3VNIVlan, VLANConfig{})...)
		// IRB binding transit VLAN to VRF (enables VRF routing for decapped packets)
		entries = append(entries, createSviConfig(ipvpnDef.L3VNIVlan, IRBConfig{VRF: vrfName})...)
		// VXLAN tunnel map for L3VNI
		entries = append(entries, createVniMapConfig(VLANName(ipvpnDef.L3VNIVlan), ipvpnDef.L3VNI)...)
	}

	return entries
}

// createVrfConfig returns the CONFIG_DB entries for creating a VRF.
// The VRF entry has no vni — L3VNI is added by ipvpn.
func createVrfConfig(name string) []sonic.Entry {
	return []sonic.Entry{
		{Table: "VRF", Key: name, Fields: map[string]string{}},
	}
}

// createStaticRouteConfig returns the CONFIG_DB entries for a static route.
// Key format: "vrfName|prefix" for non-default VRF, just "prefix" for default.
func createStaticRouteConfig(vrfName, prefix, nextHop string, metric int) []sonic.Entry {
	fields := map[string]string{
		"nexthop": nextHop,
	}
	if metric > 0 {
		fields["distance"] = fmt.Sprintf("%d", metric)
	}

	return []sonic.Entry{
		{Table: "STATIC_ROUTE", Key: staticRouteKey(vrfName, prefix), Fields: fields},
	}
}

// deleteStaticRouteConfig returns delete entries for a static route.
func deleteStaticRouteConfig(vrfName, prefix string) []sonic.Entry {
	return []sonic.Entry{{Table: "STATIC_ROUTE", Key: staticRouteKey(vrfName, prefix)}}
}

// staticRouteKey builds the STATIC_ROUTE key for a VRF and prefix.
func staticRouteKey(vrfName, prefix string) string {
	if vrfName == "" || vrfName == "default" {
		return prefix
	}
	return fmt.Sprintf("%s|%s", vrfName, prefix)
}

// clearVrfVniConfig returns an update entry that clears the VNI from a VRF.
// SONiC convention: writing "" clears the field.
func clearVrfVniConfig(vrfName string) []sonic.Entry {
	return []sonic.Entry{{Table: "VRF", Key: vrfName, Fields: map[string]string{"vni": ""}}}
}

// unbindIpvpnConfig returns the delete entries for unbinding an IP-VPN from a VRF.
// Does NOT include the VRF|vni modify (clearing vni) — that's a ChangeModify, not delete.
// routeTargets are extracted from the intent by the caller.
func unbindIpvpnConfig(vrfName string, routeTargets []string) []sonic.Entry {
	var entries []sonic.Entry

	// Remove BGP_GLOBALS_AF l2vpn_evpn and ipv4_unicast entries (children first).
	entries = append(entries, deleteBgpGlobalsAFConfig(vrfName, "l2vpn_evpn")...)
	entries = append(entries, deleteBgpGlobalsAFConfig(vrfName, "ipv4_unicast")...)

	// Remove ROUTE_REDISTRIBUTE entry.
	entries = append(entries, deleteRouteRedistributeConfig(vrfName, "connected", "ipv4")...)

	// Remove BGP_GLOBALS for the VRF (parent — after children above).
	// bindIpvpnConfig creates BGP_GLOBALS|{vrfName} with local_asn + router_id;
	// operational symmetry requires unbind to delete it.
	entries = append(entries, deleteBgpGlobalsConfig(vrfName)...)

	// Remove BGP_GLOBALS_EVPN_RT entries.
	for _, rt := range routeTargets {
		entries = append(entries, sonic.Entry{
			Table: "BGP_GLOBALS_EVPN_RT",
			Key:   fmt.Sprintf("%s|L2VPN_EVPN|%s", vrfName, rt),
		})
	}

	return entries
}

// destroyVrfConfig returns all delete entries for fully removing a VRF:
// L3VNI transit VLAN, VXLAN tunnel map, BGP EVPN VNI, IP-VPN entries
// (BGP AFs, route redistribution, EVPN RTs), BGP_GLOBALS for the VRF,
// and the VRF itself.
func destroyVrfConfig(vrfName string, l3vni, l3vniVlan int, routeTargets []string) []sonic.Entry {
	var entries []sonic.Entry

	// L3VNI EVPN entries (only if L3VNI was configured)
	if l3vni > 0 {
		entries = append(entries, deleteBgpEvpnVNIConfig(vrfName, l3vni)...)
		// L3VNI transit VLAN infrastructure (reverse of bindIpvpnConfig)
		if l3vniVlan > 0 {
			entries = append(entries, deleteVniMapConfig(l3vni, VLANName(l3vniVlan))...)
			entries = append(entries, deleteSviBaseConfig(l3vniVlan)...)
			entries = append(entries, deleteVlanConfig(l3vniVlan)...)
		}
	}

	// IP-VPN entries (BGP_GLOBALS, BGP_GLOBALS_AF, ROUTE_REDISTRIBUTE, BGP_GLOBALS_EVPN_RT)
	entries = append(entries, unbindIpvpnConfig(vrfName, routeTargets)...)

	// VRF itself
	entries = append(entries, createVrfConfig(vrfName)...)

	return entries
}
