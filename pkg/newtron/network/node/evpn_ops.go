package node

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
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

// VTEPExists checks if VTEP is configured.
func (n *Node) VTEPExists() bool { return n.configDB.HasVTEP() }

// VTEPSourceIP returns the VTEP source IP (from loopback).
func (n *Node) VTEPSourceIP() string {
	if n.configDB == nil {
		return n.resolved.LoopbackIP
	}
	// Check if VTEP is configured
	for _, vtep := range n.configDB.VXLANTunnel {
		if vtep.SrcIP != "" {
			return vtep.SrcIP
		}
	}
	// Fall back to resolved loopback IP
	return n.resolved.LoopbackIP
}

// VTEPConfig returns the VXLAN_TUNNEL + VXLAN_EVPN_NVO entries for a VTEP.
func VTEPConfig(sourceIP string) []sonic.Entry {
	return []sonic.Entry{
		{Table: "VXLAN_TUNNEL", Key: "vtep1", Fields: map[string]string{"src_ip": sourceIP}},
		{Table: "VXLAN_EVPN_NVO", Key: "nvo1", Fields: map[string]string{"source_vtep": "vtep1"}},
	}
}

// vniMapConfig returns the VXLAN_TUNNEL_MAP entry that maps a VLAN to an L2VNI.
// vlanName is the SONiC VLAN name (e.g., "Vlan100"); callers with an integer
// should pass VLANName(vlanID).
func vniMapConfig(vlanName string, vni int) []sonic.Entry {
	return []sonic.Entry{
		{Table: "VXLAN_TUNNEL_MAP", Key: VNIMapKey(vni, vlanName), Fields: map[string]string{
			"vlan": vlanName,
			"vni":  fmt.Sprintf("%d", vni),
		}},
	}
}

// arpSuppressionConfig returns the SUPPRESS_VLAN_NEIGH entry for a VLAN.
// vlanName is the SONiC VLAN name (e.g., "Vlan100"); callers with an integer
// should pass VLANName(vlanID).
func arpSuppressionConfig(vlanName string) []sonic.Entry {
	return []sonic.Entry{
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
	cs, err := n.op("map-l2vni", vlanResource(vlanID), ChangeAdd,
		func(pc *PreconditionChecker) {
			pc.RequireVTEPConfigured().RequireVLANExists(vlanID)
			// Check platform support for EVPN VXLAN
			resolved := n.Resolved()
			if resolved.Platform != "" {
				if platform, err := n.GetPlatform(resolved.Platform); err == nil {
					if !platform.SupportsFeature("evpn-vxlan") {
						pc.Check(false, "platform supports EVPN VXLAN",
							fmt.Sprintf("platform %s does not support EVPN VXLAN", resolved.Platform))
					}
				}
			}
		},
		func() []sonic.Entry { return vniMapConfig(VLANName(vlanID), vni) })
	if err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Mapped VLAN %d to L2VNI %d", vlanID, vni)
	return cs, nil
}

// vniUnmapConfig returns the delete entry for a VLAN's L2VNI mapping.
func vniUnmapConfig(configDB *sonic.ConfigDB, vlanID int) []sonic.Entry {
	vlanName := VLANName(vlanID)
	var entries []sonic.Entry

	if configDB != nil {
		for key, mapping := range configDB.VXLANTunnelMap {
			if mapping.VLAN == vlanName {
				entries = append(entries, sonic.Entry{Table: "VXLAN_TUNNEL_MAP", Key: key})
				break
			}
		}
	}

	return entries
}

// UnmapL2VNI removes the L2VNI mapping for a VLAN.
func (n *Node) UnmapL2VNI(ctx context.Context, vlanID int) (*ChangeSet, error) {
	if err := n.precondition("unmap-l2vni", vlanResource(vlanID)).
		RequireVLANExists(vlanID).
		Result(); err != nil {
		return nil, err
	}

	cs := configToChangeSet(n.name, "device.unmap-l2vni", vniUnmapConfig(n.configDB, vlanID), ChangeDelete)

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
			cs.Add(e.Table, e.Key, ChangeAdd, e.Fields)
		}
	}

	// Create BGP EVPN sessions with route reflectors (skip if already exist)
	if len(resolved.BGPNeighbors) > 0 {
		// Ensure BGP globals are set
		for _, e := range BGPGlobalsConfig("default", resolved.UnderlayASN, resolved.RouterID, nil) {
			cs.Add(e.Table, e.Key, ChangeAdd, e.Fields)
		}

		// Enable L2VPN EVPN address-family
		for _, e := range BGPGlobalsAFConfig("default", "l2vpn_evpn", map[string]string{
			"advertise-all-vni": "true",
		}) {
			cs.Add(e.Table, e.Key, ChangeAdd, e.Fields)
		}

		for _, rrIP := range resolved.BGPNeighbors {
			if rrIP == resolved.LoopbackIP {
				continue
			}

			// Use peer's ASN for eBGP overlay (all-eBGP design; docs/rca/026-bgp-all-ebgp-design.md).
			peerASN := resolved.BGPNeighborASNs[rrIP]
			if peerASN == 0 {
				return nil, fmt.Errorf("no ASN found for EVPN peer %s", rrIP)
			}

			if n.BGPNeighborExists(rrIP) {
				// Neighbor exists (e.g., provisioner created it without EVPN AF).
				// Ensure the l2vpn_evpn AF entry is present — ChangeAdd is
				// idempotent so this is safe even if it already exists.
				cs.Add("BGP_NEIGHBOR_AF",
					BGPNeighborAFKey("default", rrIP, "l2vpn_evpn"),
					ChangeAdd, map[string]string{"admin_status": "true"})
			} else {
				for _, e := range BGPNeighborConfig(rrIP, peerASN, resolved.LoopbackIP, BGPNeighborOpts{
					EBGPMultihop: true,
					ActivateIPv4: true,
					ActivateEVPN: true,
				}) {
					cs.Add(e.Table, e.Key, ChangeAdd, e.Fields)
				}
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
			cs.Add(e.Table, e.Key, ChangeDelete, nil)
		}
	}

	// Remove L2VPN EVPN address-family
	for _, e := range BGPGlobalsAFConfig("default", "l2vpn_evpn", nil) {
		cs.Add(e.Table, e.Key, ChangeDelete, nil)
	}

	// Remove VXLAN NVO and tunnel
	cs.Add("VXLAN_EVPN_NVO", "nvo1", ChangeDelete, nil)
	cs.Add("VXLAN_TUNNEL", "vtep1", ChangeDelete, nil)

	util.WithDevice(n.name).Infof("Tore down EVPN overlay (%d neighbors removed)", len(resolved.BGPNeighbors))
	return cs, nil
}
