package node

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// BGP Config Functions (pure, no Node state)
// ============================================================================

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
}

// BGPNeighborConfig returns sonic.Entry for a BGP_NEIGHBOR + BGP_NEIGHBOR_AF.
func BGPNeighborConfig(neighborIP string, asn int, localAddr string, opts BGPNeighborOpts) []sonic.Entry {
	var entries []sonic.Entry

	vrf := opts.VRF
	if vrf == "" {
		vrf = "default"
	}

	fields := map[string]string{
		"asn":          fmt.Sprintf("%d", asn),
		"admin_status": "up",
		"local_addr":   localAddr,
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

// BGPNeighborDeleteConfig returns sonic.Entry for deleting a BGP neighbor
// and all its address-family entries.
func BGPNeighborDeleteConfig(vrf, neighborIP string) []sonic.Entry {
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

// BGPGlobalsConfig returns sonic.Entry for BGP_GLOBALS.
func BGPGlobalsConfig(vrf string, asn int, routerID string, extra map[string]string) []sonic.Entry {
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

// BGPGlobalsAFConfig returns sonic.Entry for BGP_GLOBALS_AF.
func BGPGlobalsAFConfig(vrf, af string, fields map[string]string) []sonic.Entry {
	if fields == nil {
		fields = map[string]string{}
	}
	return []sonic.Entry{
		{Table: "BGP_GLOBALS_AF", Key: BGPGlobalsAFKey(vrf, af), Fields: fields},
	}
}

// RouteRedistributeKey returns the CONFIG_DB key for a ROUTE_REDISTRIBUTE entry.
func RouteRedistributeKey(vrf, protocol, af string) string {
	return fmt.Sprintf("%s|%s|bgp|%s", vrf, protocol, af)
}

// RouteRedistributeConfig returns sonic.Entry for ROUTE_REDISTRIBUTE.
func RouteRedistributeConfig(vrf, protocol, af string) []sonic.Entry {
	return []sonic.Entry{
		{Table: "ROUTE_REDISTRIBUTE", Key: RouteRedistributeKey(vrf, protocol, af), Fields: map[string]string{}},
	}
}

// BGPNeighborAFKey returns the CONFIG_DB key for a BGP_NEIGHBOR_AF entry.
// Used by callers that need to modify an existing AF entry (e.g. adding route-maps).
func BGPNeighborAFKey(vrf, neighborIP, af string) string {
	return fmt.Sprintf("%s|%s|%s", vrf, neighborIP, af)
}

// BGPConfigured checks if BGP is configured.
// Checks both CONFIG_DB BGP_NEIGHBOR table (CONFIG_DB-managed BGP) and
// DEVICE_METADATA bgp_asn (FRR-managed BGP with frr_split_config_enabled).
func (n *Node) BGPConfigured() bool { return n.configDB.BGPConfigured() }

// BGPNeighborExists checks if a BGP neighbor exists.
// Looks up using the SONiC key format: "default|<IP>" (vrf|neighborIP).
func (n *Node) BGPNeighborExists(neighborIP string) bool {
	return n.configDB.HasBGPNeighbor(fmt.Sprintf("default|%s", neighborIP))
}

// ============================================================================
// BGP Global Configuration
// ============================================================================

// ConfigureBGP writes the BGP_GLOBALS, BGP_GLOBALS_AF, and ROUTE_REDISTRIBUTE
// entries needed to bring up a BGP instance. Values are read entirely from the
// node's resolved profile — no YAML params needed.
//
// This is a lightweight primitive that only sets up the BGP instance itself,
// without adding any neighbors. Use bgp-add-neighbor / setup-evpn for peers.
func (n *Node) ConfigureBGP(ctx context.Context) (*ChangeSet, error) {
	if err := n.precondition("configure-bgp", "bgp").Result(); err != nil {
		return nil, err
	}

	resolved := n.Resolved()
	if resolved.UnderlayASN == 0 {
		return nil, fmt.Errorf("underlay_asn not set in device profile")
	}
	if resolved.RouterID == "" {
		return nil, fmt.Errorf("router_id not set in device profile")
	}

	asnStr := fmt.Sprintf("%d", resolved.UnderlayASN)
	cs := NewChangeSet(n.name, "device.configure-bgp")

	// bgpcfgd.BGPPeerMgrBase requires DEVICE_METADATA["localhost"]["bgp_asn"] and
	// DEVICE_METADATA["localhost"]["type"] as explicit dependencies (checked via
	// directory.available_deps). Without "type", the dependency check fails and
	// bgpcfgd silently defers all BGP_NEIGHBOR entries permanently.
	//
	// docker_routing_config_mode and frr_mgmt_framework_config MUST be in
	// DEVICE_METADATA (not BGP_GLOBALS). bgpcfgd reads them from DEVICE_METADATA
	// to decide which FRR template to use. Writing them to BGP_GLOBALS has no
	// effect — bgpcfgd ignores unknown BGP_GLOBALS fields.
	//
	// With docker_routing_config_mode=unified + frr_mgmt_framework_config=true,
	// bgpcfgd uses the frrcfgd Jinja2 db template (bgpd.conf.db.j2) which:
	//   - Generates "service integrated-vtysh-config"
	//   - Handles local_addr → "update-source <IP>" for loopback-sourced peers
	//   - Handles ebgp_multihop → "ebgp-multihop"
	//   - Generates proper address-family l2vpn evpn sections
	// Without these flags, bgpcfgd uses the legacy "general" template which
	// adds PEER_V4/PEER_V6 peer-groups but does NOT handle local_addr or
	// ebgp_multihop, causing EVPN loopback sessions to stay "Active".
	cs.Add("DEVICE_METADATA", "localhost", ChangeModify, map[string]string{
		"bgp_asn":                    asnStr,
		"type":                       "LeafRouter",
		"docker_routing_config_mode": "unified",
		"frr_mgmt_framework_config":  "true",
	})

	// BGP global instance + address-family + redistribution via config functions
	for _, e := range BGPGlobalsConfig("default", resolved.UnderlayASN, resolved.RouterID, map[string]string{
		"ebgp_requires_policy": "false",
		"suppress_fib_pending": "false",
		"log_neighbor_changes": "true",
	}) {
		cs.Add(e.Table, e.Key, ChangeModify, e.Fields)
	}
	for _, e := range BGPGlobalsAFConfig("default", "ipv4_unicast", nil) {
		cs.Add(e.Table, e.Key, ChangeModify, e.Fields)
	}
	for _, e := range RouteRedistributeConfig("default", "connected", "ipv4") {
		cs.Add(e.Table, e.Key, ChangeModify, e.Fields)
	}

	n.trackOffline(cs)
	util.WithDevice(n.name).Infof("Configured BGP (AS %d, router-id %s)", resolved.UnderlayASN, resolved.RouterID)
	return cs, nil
}

// ============================================================================
// BGP Neighbor Operations
// ============================================================================

// AddLoopbackBGPNeighbor adds an indirect BGP neighbor using loopback as update-source.
// This is used for iBGP or multi-hop eBGP sessions (EVPN overlay peers).
//
// For DIRECT BGP neighbors that use a link IP as the update-source (typical
// eBGP on point-to-point links), use Interface.AddBGPNeighbor() instead.
func (n *Node) AddLoopbackBGPNeighbor(ctx context.Context, neighborIP string, asn int, description string, evpn bool) (*ChangeSet, error) {
	if err := n.precondition("add-loopback-bgp-neighbor", neighborIP).Result(); err != nil {
		return nil, err
	}

	if !util.IsValidIPv4(neighborIP) {
		return nil, fmt.Errorf("invalid neighbor IP: %s", neighborIP)
	}
	if n.BGPNeighborExists(neighborIP) {
		return nil, fmt.Errorf("BGP neighbor %s already exists", neighborIP)
	}

	isEBGP := asn != n.resolved.UnderlayASN
	config := BGPNeighborConfig(neighborIP, asn, n.resolved.LoopbackIP, BGPNeighborOpts{
		Description:  description,
		EBGPMultihop: isEBGP,
		MultihopTTL:  "255",
		ActivateEVPN: evpn,
	})
	cs := configToChangeSet(n.name, "bgp.add-loopback-neighbor", config, ChangeAdd)
	n.trackOffline(cs)

	util.WithDevice(n.name).Infof("Adding loopback BGP neighbor %s (AS %d, update-source: %s)",
		neighborIP, asn, n.resolved.LoopbackIP)
	return cs, nil
}

// RemoveBGPNeighbor removes a BGP neighbor from the device.
// This works for both direct (interface-level) and indirect (loopback-level) neighbors.
func (n *Node) RemoveBGPNeighbor(ctx context.Context, neighborIP string) (*ChangeSet, error) {
	cs, err := n.op("remove-bgp-neighbor", neighborIP, ChangeDelete,
		func(pc *PreconditionChecker) {
			pc.Check(n.BGPNeighborExists(neighborIP), "BGP neighbor must exist",
				fmt.Sprintf("BGP neighbor %s not found", neighborIP))
		},
		func() []sonic.Entry { return BGPNeighborDeleteConfig("default", neighborIP) })
	if err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Removing BGP neighbor %s", neighborIP)
	return cs, nil
}

// RemoveBGPGlobals removes the default BGP instance, reversing ConfigureBGP.
// Deletes ROUTE_REDISTRIBUTE, BGP_GLOBALS_AF (ipv4_unicast), BGP_GLOBALS,
// and clears bgp_asn from DEVICE_METADATA.
//
// Does NOT touch l2vpn_evpn AF or EVPN neighbors (owned by TeardownEVPN).
// Does NOT touch per-VRF BGP_GLOBALS (owned by UnbindIPVPN/DeleteVRF).
func (n *Node) RemoveBGPGlobals(ctx context.Context) (*ChangeSet, error) {
	if err := n.precondition("remove-bgp-globals", "bgp").Result(); err != nil {
		return nil, err
	}

	if !n.BGPConfigured() {
		return nil, fmt.Errorf("BGP is not configured on %s", n.name)
	}

	cs := NewChangeSet(n.name, "device.remove-bgp-globals")

	// Reverse order of ConfigureBGP — use config functions for key consistency
	for _, e := range RouteRedistributeConfig("default", "connected", "ipv4") {
		cs.Add(e.Table, e.Key, ChangeDelete, nil)
	}
	for _, e := range BGPGlobalsAFConfig("default", "ipv4_unicast", nil) {
		cs.Add(e.Table, e.Key, ChangeDelete, nil)
	}
	for _, e := range BGPGlobalsConfig("default", 0, "", nil) {
		cs.Add(e.Table, e.Key, ChangeDelete, nil)
	}

	// Clear bgp_asn from DEVICE_METADATA (set to empty)
	cs.Add("DEVICE_METADATA", "localhost", ChangeModify, map[string]string{
		"bgp_asn": "",
	})

	util.WithDevice(n.name).Infof("Removed BGP globals")
	return cs, nil
}
