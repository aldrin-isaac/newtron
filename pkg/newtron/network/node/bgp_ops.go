package node

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// BGP Config Functions (pure, no Node state)
// ============================================================================

// bgpNeighborOpts controls optional aspects of BGP neighbor configuration.
type bgpNeighborOpts struct {
	Description  string
	EBGPMultihop bool   // set ebgp_multihop (value "true" for loopback, TTL string for explicit)
	MultihopTTL  string // explicit TTL value (e.g., "255"); if empty and EBGPMultihop=true, uses "true"
	ActivateIPv4 bool   // activate ipv4_unicast AF (default true for direct peers)
	ActivateEVPN bool   // activate l2vpn_evpn AF
	VRF          string // VRF name (default "default")
	RRClient    bool   // route-reflector-client on ipv4_unicast AF
	NextHopSelf bool   // next-hop-self on ipv4_unicast AF
}

// bgpNeighborConfig returns CompositeEntry for a BGP_NEIGHBOR + BGP_NEIGHBOR_AF.
func bgpNeighborConfig(neighborIP string, asn int, localAddr string, opts bgpNeighborOpts) []CompositeEntry {
	var entries []CompositeEntry

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

	entries = append(entries, CompositeEntry{
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
		entries = append(entries, CompositeEntry{
			Table:  "BGP_NEIGHBOR_AF",
			Key:    fmt.Sprintf("%s|%s|ipv4_unicast", vrf, neighborIP),
			Fields: afFields,
		})
	}

	// Activate L2VPN EVPN
	if opts.ActivateEVPN {
		entries = append(entries, CompositeEntry{
			Table:  "BGP_NEIGHBOR_AF",
			Key:    fmt.Sprintf("%s|%s|l2vpn_evpn", vrf, neighborIP),
			Fields: map[string]string{"admin_status": "true"},
		})
	}

	return entries
}

// bgpNeighborDeleteConfig returns CompositeEntry for deleting a BGP neighbor
// and all its address-family entries.
func bgpNeighborDeleteConfig(neighborIP string) []CompositeEntry {
	var entries []CompositeEntry

	// Remove address-family entries first
	for _, af := range []string{"ipv4_unicast", "ipv6_unicast", "l2vpn_evpn"} {
		entries = append(entries, CompositeEntry{
			Table:  "BGP_NEIGHBOR_AF",
			Key:    fmt.Sprintf("default|%s|%s", neighborIP, af),
			Fields: nil,
		})
	}

	// Remove neighbor entry
	entries = append(entries, CompositeEntry{
		Table:  "BGP_NEIGHBOR",
		Key:    fmt.Sprintf("default|%s", neighborIP),
		Fields: nil,
	})

	return entries
}

// interfaceIPConfig returns CompositeEntry for configuring an IP on an interface.
// Creates the INTERFACE base entry + IP sub-entry.
func interfaceIPConfig(intfName, ipAddr string) []CompositeEntry {
	return []CompositeEntry{
		{Table: "INTERFACE", Key: intfName, Fields: map[string]string{}},
		{Table: "INTERFACE", Key: fmt.Sprintf("%s|%s", intfName, ipAddr), Fields: map[string]string{}},
	}
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
	cs.Add("DEVICE_METADATA", "localhost", ChangeModify, nil, map[string]string{
		"bgp_asn":                    asnStr,
		"type":                       "LeafRouter",
		"docker_routing_config_mode": "unified",
		"frr_mgmt_framework_config":  "true",
	})

	// BGP global instance
	cs.Add("BGP_GLOBALS", "default", ChangeModify, nil, map[string]string{
		"local_asn":              asnStr,
		"router_id":              resolved.RouterID,
		"ebgp_requires_policy":  "false",
		"log_neighbor_changes":  "true",
	})

	// Enable IPv4 unicast address-family
	cs.Add("BGP_GLOBALS_AF", "default|ipv4_unicast", ChangeModify, nil, map[string]string{})

	// Redistribute connected routes (required for loopback reachability)
	cs.Add("ROUTE_REDISTRIBUTE", "default|connected|bgp|ipv4", ChangeModify, nil, map[string]string{})

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
	config := bgpNeighborConfig(neighborIP, asn, n.resolved.LoopbackIP, bgpNeighborOpts{
		Description:  description,
		EBGPMultihop: isEBGP,
		MultihopTTL:  "255",
		ActivateEVPN: evpn,
	})
	cs := configToChangeSet(n.name, "bgp.add-loopback-neighbor", config, ChangeAdd)

	util.WithDevice(n.name).Infof("Adding loopback BGP neighbor %s (AS %d, update-source: %s)",
		neighborIP, asn, n.resolved.LoopbackIP)
	return cs, nil
}

// RemoveBGPNeighbor removes a BGP neighbor from the device.
// This works for both direct (interface-level) and indirect (loopback-level) neighbors.
func (n *Node) RemoveBGPNeighbor(ctx context.Context, neighborIP string) (*ChangeSet, error) {
	if err := n.precondition("remove-bgp-neighbor", neighborIP).Result(); err != nil {
		return nil, err
	}

	if !n.BGPNeighborExists(neighborIP) {
		return nil, fmt.Errorf("BGP neighbor %s not found", neighborIP)
	}

	config := bgpNeighborDeleteConfig(neighborIP)
	cs := configToChangeSet(n.name, "bgp.remove-neighbor", config, ChangeDelete)

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

	// Reverse order of ConfigureBGP
	cs.Add("ROUTE_REDISTRIBUTE", "default|connected|bgp|ipv4", ChangeDelete, nil, nil)
	cs.Add("BGP_GLOBALS_AF", "default|ipv4_unicast", ChangeDelete, nil, nil)
	cs.Add("BGP_GLOBALS", "default", ChangeDelete, nil, nil)

	// Clear bgp_asn from DEVICE_METADATA (set to empty)
	cs.Add("DEVICE_METADATA", "localhost", ChangeModify, nil, map[string]string{
		"bgp_asn": "",
	})

	util.WithDevice(n.name).Infof("Removed BGP globals")
	return cs, nil
}
