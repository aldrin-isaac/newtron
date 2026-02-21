package node

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// EVPN Operations — Pure config generators
// ============================================================================

// vtepConfig returns the VXLAN_TUNNEL + VXLAN_EVPN_NVO entries for a VTEP.
func vtepConfig(sourceIP string) []CompositeEntry {
	return []CompositeEntry{
		{Table: "VXLAN_TUNNEL", Key: "vtep1", Fields: map[string]string{"src_ip": sourceIP}},
		{Table: "VXLAN_EVPN_NVO", Key: "nvo1", Fields: map[string]string{"source_vtep": "vtep1"}},
	}
}

// vniMapConfig returns the VXLAN_TUNNEL_MAP entry that maps a VLAN to an L2VNI.
func vniMapConfig(vlanID, vni int) []CompositeEntry {
	vlanName := fmt.Sprintf("Vlan%d", vlanID)
	mapKey := fmt.Sprintf("vtep1|map_%d_%s", vni, vlanName)
	return []CompositeEntry{
		{Table: "VXLAN_TUNNEL_MAP", Key: mapKey, Fields: map[string]string{
			"vlan": vlanName,
			"vni":  fmt.Sprintf("%d", vni),
		}},
	}
}

// arpSuppressionConfig returns the SUPPRESS_VLAN_NEIGH entry for a VLAN.
func arpSuppressionConfig(vlanID int) []CompositeEntry {
	return []CompositeEntry{
		{Table: "SUPPRESS_VLAN_NEIGH", Key: fmt.Sprintf("Vlan%d", vlanID), Fields: map[string]string{
			"suppress": "on",
		}},
	}
}

// ============================================================================
// EVPN Operations
// ============================================================================

// MapL2VNI maps a VLAN to an L2VNI for EVPN.
func (n *Node) MapL2VNI(ctx context.Context, vlanID, vni int) (*ChangeSet, error) {
	if err := n.precondition("map-l2vni", vlanResource(vlanID)).
		RequireVTEPConfigured().
		RequireVLANExists(vlanID).
		Result(); err != nil {
		return nil, err
	}

	// Check platform support for EVPN VXLAN
	resolved := n.Resolved()
	if resolved.Platform != "" {
		if platform, err := n.GetPlatform(resolved.Platform); err == nil {
			if !platform.SupportsFeature("evpn-vxlan") {
				return nil, fmt.Errorf("platform %s does not support EVPN VXLAN", resolved.Platform)
			}
		}
	}

	cs := NewChangeSet(n.name, "device.map-l2vni")

	for _, e := range vniMapConfig(vlanID, vni) {
		cs.Add(e.Table, e.Key, ChangeAdd, nil, e.Fields)
	}

	util.WithDevice(n.name).Infof("Mapped VLAN %d to L2VNI %d", vlanID, vni)
	return cs, nil
}

// UnmapL2VNI removes the L2VNI mapping for a VLAN.
func (n *Node) UnmapL2VNI(ctx context.Context, vlanID int) (*ChangeSet, error) {
	if err := n.precondition("unmap-l2vni", vlanResource(vlanID)).
		RequireVLANExists(vlanID).
		Result(); err != nil {
		return nil, err
	}

	vlanName := fmt.Sprintf("Vlan%d", vlanID)
	cs := NewChangeSet(n.name, "device.unmap-l2vni")

	// Find the tunnel map entry for this VLAN
	if n.configDB != nil {
		for key, mapping := range n.configDB.VXLANTunnelMap {
			if mapping.VLAN == vlanName {
				cs.Add("VXLAN_TUNNEL_MAP", key, ChangeDelete, nil, nil)
				break
			}
		}
	}

	if cs.IsEmpty() {
		return nil, fmt.Errorf("no L2VNI mapping found for VLAN %d", vlanID)
	}

	util.WithDevice(n.name).Infof("Unmapped L2VNI for VLAN %d", vlanID)
	return cs, nil
}

// SetupEVPN is an idempotent composite that creates VTEP + NVO + BGP EVPN sessions.
// If sourceIP is empty, uses the device's resolved VTEP source IP (loopback).
func (n *Node) SetupEVPN(ctx context.Context, sourceIP string) (*ChangeSet, error) {
	if err := n.precondition("setup-evpn", "evpn").Result(); err != nil {
		return nil, err
	}

	resolved := n.Resolved()
	if sourceIP == "" {
		sourceIP = resolved.VTEPSourceIP
	}
	if sourceIP == "" {
		return nil, fmt.Errorf("no VTEP source IP available (specify sourceIP or set loopback_ip in profile)")
	}

	cs := NewChangeSet(n.name, "device.setup-evpn")

	// Create VTEP (skip if exists)
	if !n.VTEPExists() {
		for _, e := range vtepConfig(sourceIP) {
			cs.Add(e.Table, e.Key, ChangeAdd, nil, e.Fields)
		}
	}

	// Create BGP EVPN sessions with route reflectors (skip if already exist)
	if len(resolved.BGPNeighbors) > 0 {
		// Ensure BGP globals are set
		cs.Add("BGP_GLOBALS", "default", ChangeAdd, nil, map[string]string{
			"local_asn": fmt.Sprintf("%d", resolved.UnderlayASN),
			"router_id": resolved.RouterID,
		})

		// Enable L2VPN EVPN address-family
		cs.Add("BGP_GLOBALS_AF", "default|l2vpn_evpn", ChangeAdd, nil, map[string]string{
			"advertise-all-vni": "true",
		})

		for _, rrIP := range resolved.BGPNeighbors {
			if rrIP == resolved.LoopbackIP {
				continue
			}
			// Skip if neighbor already exists
			if n.BGPNeighborExists(rrIP) {
				continue
			}

			// Use peer's ASN for eBGP overlay (all-eBGP design; docs/rca/026-bgp-all-ebgp-design.md).
			peerASN := resolved.BGPNeighborASNs[rrIP]
			if peerASN == 0 {
				return nil, fmt.Errorf("no ASN found for EVPN peer %s", rrIP)
			}
			fields := map[string]string{
				"asn":           fmt.Sprintf("%d", peerASN),
				"admin_status":  "up",
				"local_addr":    resolved.LoopbackIP,
				"ebgp_multihop": "true",
			}
			cs.Add("BGP_NEIGHBOR", fmt.Sprintf("default|%s", rrIP), ChangeAdd, nil, fields)

			// Activate IPv4 unicast for the EVPN overlay peer (frrcfgd template
			// requires "admin_status: true", not "activate: true", to generate
			// "neighbor X activate" in address-family ipv4 unicast).
			// EVPN routes are exchanged via BGP capability negotiation once
			// BGP_GLOBALS_AF|l2vpn_evpn has advertise-all-vni set.
			ipv4AfKey := fmt.Sprintf("default|%s|ipv4_unicast", rrIP)
			cs.Add("BGP_NEIGHBOR_AF", ipv4AfKey, ChangeAdd, nil, map[string]string{
				"admin_status": "true",
			})

			// Also activate l2vpn_evpn AF for explicit EVPN capability signaling.
			evpnAfKey := fmt.Sprintf("default|%s|l2vpn_evpn", rrIP)
			cs.Add("BGP_NEIGHBOR_AF", evpnAfKey, ChangeAdd, nil, map[string]string{
				"admin_status": "true",
			})
		}
	}

	util.WithDevice(n.name).Infof("Setup EVPN (source IP %s, %d route reflectors)", sourceIP, len(resolved.BGPNeighbors))
	return cs, nil
}

// TeardownEVPN removes EVPN overlay configuration: BGP overlay neighbors,
// BGP EVPN address-family, VXLAN NVO, and VXLAN tunnel.
// This is the reverse of SetupEVPN.
func (n *Node) TeardownEVPN(ctx context.Context) (*ChangeSet, error) {
	if err := n.precondition("teardown-evpn", "evpn").Result(); err != nil {
		return nil, err
	}

	resolved := n.Resolved()
	cs := NewChangeSet(n.name, "device.teardown-evpn")

	// Remove BGP EVPN overlay neighbors and their address-family entries
	for _, rrIP := range resolved.BGPNeighbors {
		if rrIP == resolved.LoopbackIP {
			continue
		}
		neighborKey := fmt.Sprintf("default|%s", rrIP)
		cs.Add("BGP_NEIGHBOR", neighborKey, ChangeDelete, nil, nil)

		// Remove address-family entries
		cs.Add("BGP_NEIGHBOR_AF", fmt.Sprintf("default|%s|ipv4_unicast", rrIP), ChangeDelete, nil, nil)
		cs.Add("BGP_NEIGHBOR_AF", fmt.Sprintf("default|%s|l2vpn_evpn", rrIP), ChangeDelete, nil, nil)
	}

	// Remove L2VPN EVPN address-family
	cs.Add("BGP_GLOBALS_AF", "default|l2vpn_evpn", ChangeDelete, nil, nil)

	// Remove VXLAN NVO and tunnel
	cs.Add("VXLAN_EVPN_NVO", "nvo1", ChangeDelete, nil, nil)
	cs.Add("VXLAN_TUNNEL", "vtep1", ChangeDelete, nil, nil)

	util.WithDevice(n.name).Infof("Tore down EVPN overlay (%d neighbors removed)", len(resolved.BGPNeighbors))
	return cs, nil
}
