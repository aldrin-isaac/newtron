package node

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// EVPN Key Helpers
// ============================================================================

// VNIMapKey returns the CONFIG_DB key for a VXLAN_TUNNEL_MAP entry.
// Target is a VLAN name (e.g., "Vlan100") or VRF name.
func VNIMapKey(vni int, target string) string {
	return fmt.Sprintf("vtep1|map_%d_%s", vni, target)
}

// BGPEVPNVNIKey returns the CONFIG_DB key for a BGP_EVPN_VNI entry.
func BGPEVPNVNIKey(vrfName string, vni int) string {
	return fmt.Sprintf("%s|%d", vrfName, vni)
}

// ============================================================================
// EVPN Operations — Pure config generators
// ============================================================================

// VTEPConfig returns the VXLAN_TUNNEL + VXLAN_EVPN_NVO entries for a VTEP.
func VTEPConfig(sourceIP string) []CompositeEntry {
	return []CompositeEntry{
		{Table: "VXLAN_TUNNEL", Key: "vtep1", Fields: map[string]string{"src_ip": sourceIP}},
		{Table: "VXLAN_EVPN_NVO", Key: "nvo1", Fields: map[string]string{"source_vtep": "vtep1"}},
	}
}

// vniMapConfig returns the VXLAN_TUNNEL_MAP entry that maps a VLAN to an L2VNI.
// vlanName is the SONiC VLAN name (e.g., "Vlan100"); callers with an integer
// should pass VLANName(vlanID).
func vniMapConfig(vlanName string, vni int) []CompositeEntry {
	return []CompositeEntry{
		{Table: "VXLAN_TUNNEL_MAP", Key: VNIMapKey(vni, vlanName), Fields: map[string]string{
			"vlan": vlanName,
			"vni":  fmt.Sprintf("%d", vni),
		}},
	}
}

// arpSuppressionConfig returns the SUPPRESS_VLAN_NEIGH entry for a VLAN.
// vlanName is the SONiC VLAN name (e.g., "Vlan100"); callers with an integer
// should pass VLANName(vlanID).
func arpSuppressionConfig(vlanName string) []CompositeEntry {
	return []CompositeEntry{
		{Table: "SUPPRESS_VLAN_NEIGH", Key: vlanName, Fields: map[string]string{
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

	for _, e := range vniMapConfig(VLANName(vlanID), vni) {
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

	vlanName := VLANName(vlanID)
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
		for _, e := range VTEPConfig(sourceIP) {
			cs.Add(e.Table, e.Key, ChangeAdd, nil, e.Fields)
		}
	}

	// Create BGP EVPN sessions with route reflectors (skip if already exist)
	if len(resolved.BGPNeighbors) > 0 {
		// Ensure BGP globals are set
		for _, e := range BGPGlobalsConfig("default", resolved.UnderlayASN, resolved.RouterID, nil) {
			cs.Add(e.Table, e.Key, ChangeAdd, nil, e.Fields)
		}

		// Enable L2VPN EVPN address-family
		for _, e := range BGPGlobalsAFConfig("default", "l2vpn_evpn", map[string]string{
			"advertise-all-vni": "true",
		}) {
			cs.Add(e.Table, e.Key, ChangeAdd, nil, e.Fields)
		}

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
			for _, e := range BGPNeighborConfig(rrIP, peerASN, resolved.LoopbackIP, BGPNeighborOpts{
				EBGPMultihop: true,
				ActivateIPv4: true,
				ActivateEVPN: true,
			}) {
				cs.Add(e.Table, e.Key, ChangeAdd, nil, e.Fields)
			}
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
		for _, e := range BGPNeighborDeleteConfig(rrIP) {
			cs.Add(e.Table, e.Key, ChangeDelete, nil, nil)
		}
	}

	// Remove L2VPN EVPN address-family
	for _, e := range BGPGlobalsAFConfig("default", "l2vpn_evpn", nil) {
		cs.Add(e.Table, e.Key, ChangeDelete, nil, nil)
	}

	// Remove VXLAN NVO and tunnel
	cs.Add("VXLAN_EVPN_NVO", "nvo1", ChangeDelete, nil, nil)
	cs.Add("VXLAN_TUNNEL", "vtep1", ChangeDelete, nil, nil)

	util.WithDevice(n.name).Infof("Tore down EVPN overlay (%d neighbors removed)", len(resolved.BGPNeighbors))
	return cs, nil
}
