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
	return fmt.Sprintf("vtep1|VNI%d_%s", vni, target)
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

// CreateVTEPConfig returns the VXLAN_TUNNEL + VXLAN_EVPN_NVO entries for a VTEP.
func CreateVTEPConfig(sourceIP string) []sonic.Entry {
	return []sonic.Entry{
		{Table: "VXLAN_TUNNEL", Key: "vtep1", Fields: map[string]string{"src_ip": sourceIP}},
		{Table: "VXLAN_EVPN_NVO", Key: "nvo1", Fields: map[string]string{"source_vtep": "vtep1"}},
	}
}

// createVniMapConfig returns the VXLAN_TUNNEL_MAP entry that maps a VLAN to an L2VNI.
// vlanName is the SONiC VLAN name (e.g., "Vlan100"); callers with an integer
// should pass VLANName(vlanID).
func createVniMapConfig(vlanName string, vni int) []sonic.Entry {
	return []sonic.Entry{
		{Table: "VXLAN_TUNNEL_MAP", Key: VNIMapKey(vni, vlanName), Fields: map[string]string{
			"vlan": vlanName,
			"vni":  fmt.Sprintf("%d", vni),
		}},
	}
}

// enableArpSuppressionConfig returns the SUPPRESS_VLAN_NEIGH entry for a VLAN.
// vlanName is the SONiC VLAN name (e.g., "Vlan100"); callers with an integer
// should pass VLANName(vlanID).
func enableArpSuppressionConfig(vlanName string) []sonic.Entry {
	return []sonic.Entry{
		{Table: "SUPPRESS_VLAN_NEIGH", Key: vlanName, Fields: map[string]string{
			"suppress": "on",
		}},
	}
}

// disableArpSuppressionConfig returns the delete entry for ARP suppression on a VLAN.
func disableArpSuppressionConfig(vlanName string) []sonic.Entry {
	return []sonic.Entry{{Table: "SUPPRESS_VLAN_NEIGH", Key: vlanName}}
}

// deleteVniMapConfig returns the delete entry for a specific VXLAN_TUNNEL_MAP entry.
func deleteVniMapConfig(vni int, target string) []sonic.Entry {
	return []sonic.Entry{{Table: "VXLAN_TUNNEL_MAP", Key: VNIMapKey(vni, target)}}
}

// deleteVniMapByKeyConfig returns the delete entry for a VXLAN_TUNNEL_MAP given a raw key.
// Used by cleanup paths that iterate configDB and already have the key.
func deleteVniMapByKeyConfig(key string) []sonic.Entry {
	return []sonic.Entry{{Table: "VXLAN_TUNNEL_MAP", Key: key}}
}

// deleteBgpEvpnVNIConfig returns the delete entry for a BGP_EVPN_VNI entry.
func deleteBgpEvpnVNIConfig(vrfName string, vni int) []sonic.Entry {
	return []sonic.Entry{{Table: "BGP_EVPN_VNI", Key: BGPEVPNVNIKey(vrfName, vni)}}
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
		func() []sonic.Entry { return createVniMapConfig(VLANName(vlanID), vni) },
		"device.unmap-l2vni")
	if err != nil {
		return nil, err
	}
	cs.OperationParams = map[string]string{"vlan_id": fmt.Sprintf("%d", vlanID)}
	util.WithDevice(n.name).Infof("Mapped VLAN %d to L2VNI %d", vlanID, vni)
	return cs, nil
}

// unmapVniConfig returns the delete entry for a VLAN's L2VNI mapping.
func (n *Node) unmapVniConfig(vlanID int) []sonic.Entry {
	vlanName := VLANName(vlanID)
	var entries []sonic.Entry

	if n.configDB != nil {
		for key, mapping := range n.configDB.VXLANTunnelMap {
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

	cs := buildChangeSet(n.name, "device.unmap-l2vni", n.unmapVniConfig(vlanID), ChangeDelete)

	if cs.IsEmpty() {
		return nil, fmt.Errorf("no L2VNI mapping found for VLAN %d", vlanID)
	}

	util.WithDevice(n.name).Infof("Unmapped L2VNI for VLAN %d", vlanID)
	return cs, nil
}

// SetupVTEP is an idempotent composite that creates VTEP + NVO + BGP EVPN sessions.
// If sourceIP is empty, uses the device's resolved VTEP source IP (loopback).
func (n *Node) SetupVTEP(ctx context.Context, sourceIP string) (*ChangeSet, error) {
	if err := n.precondition("setup-vtep", "evpn").Result(); err != nil {
		return nil, err
	}

	resolved := n.Resolved()
	if sourceIP == "" {
		sourceIP = resolved.VTEPSourceIP
	}
	if sourceIP == "" {
		return nil, fmt.Errorf("no VTEP source IP available (specify sourceIP or set loopback_ip in profile)")
	}

	cs := NewChangeSet(n.name, "device.setup-vtep")
	cs.ReverseOp = "device.teardown-vtep"

	// Create VTEP (skip if exists)
	if !n.VTEPExists() {
		cs.Adds(CreateVTEPConfig(sourceIP))
	}

	// Create BGP EVPN sessions with route reflectors (skip if already exist)
	if len(resolved.BGPNeighbors) > 0 {
		// Ensure BGP globals are set
		cs.Adds(CreateBGPGlobalsConfig("default", resolved.UnderlayASN, resolved.RouterID, nil))

		// Enable L2VPN EVPN address-family
		cs.Adds(CreateBGPGlobalsAFConfig("default", "l2vpn_evpn", map[string]string{
			"advertise-all-vni": "true",
		}))

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
				// Ensure the l2vpn_evpn AF entry is present — Add is
				// idempotent so this is safe even if it already exists.
				e := createBgpNeighborAFConfig("default", rrIP, "l2vpn_evpn", map[string]string{"admin_status": "true"})
				cs.Add(e.Table, e.Key, e.Fields)
			} else {
				cs.Adds(CreateBGPNeighborConfig(rrIP, peerASN, resolved.LoopbackIP, BGPNeighborOpts{
					EBGPMultihop:     true,
					ActivateIPv4:     true,
					ActivateEVPN:     true,
					NextHopUnchanged: true, // Critical for eBGP overlay (RCA-026)
				}))
			}
		}
	}

	n.applyShadow(cs)
	util.WithDevice(n.name).Infof("Setup EVPN (source IP %s, %d route reflectors)", sourceIP, len(resolved.BGPNeighbors))
	return cs, nil
}

// TeardownVTEP removes EVPN overlay configuration: BGP overlay neighbors,
// BGP EVPN address-family, VXLAN NVO, and VXLAN tunnel.
// This is the reverse of SetupVTEP.
func (n *Node) TeardownVTEP(ctx context.Context) (*ChangeSet, error) {
	if err := n.precondition("teardown-vtep", "evpn").Result(); err != nil {
		return nil, err
	}

	resolved := n.Resolved()
	cs := NewChangeSet(n.name, "device.teardown-vtep")

	// Remove BGP EVPN overlay neighbors and their address-family entries
	for _, rrIP := range resolved.BGPNeighbors {
		if rrIP == resolved.LoopbackIP {
			continue
		}
		cs.Deletes(DeleteBGPNeighborConfig("default", rrIP))
	}

	// Remove L2VPN EVPN address-family
	cs.Deletes(CreateBGPGlobalsAFConfig("default", "l2vpn_evpn", nil))

	// Remove VXLAN NVO and tunnel
	cs.Delete("VXLAN_EVPN_NVO", "nvo1")
	cs.Delete("VXLAN_TUNNEL", "vtep1")

	util.WithDevice(n.name).Infof("Tore down EVPN overlay (%d neighbors removed)", len(resolved.BGPNeighbors))
	return cs, nil
}

// ============================================================================
// Route Reflector Configuration
// ============================================================================

// RouteReflectorPeer describes a BGP peer for route reflector configuration.
type RouteReflectorPeer struct {
	IP  string // Loopback IP of the peer
	ASN int    // Autonomous system number
}

// RouteReflectorOpts holds configuration for ConfigureRouteReflector.
type RouteReflectorOpts struct {
	ClusterID string               // RR cluster ID
	LocalASN  int                  // RR's own ASN
	RouterID  string               // RR's router ID
	LocalAddr string               // Local address for eBGP multihop (loopback IP)
	Clients   []RouteReflectorPeer // RR clients (IPv4 unicast, RR-client)
	Peers     []RouteReflectorPeer // RR-to-RR peers (IPv4+IPv6+EVPN, RR-client)
}

// ConfigureRouteReflector configures this node as a BGP route reflector.
// Sets BGP globals with RR-specific settings, creates eBGP neighbors for
// all clients (IPv4 unicast) and peers (IPv4+IPv6+EVPN), and enables
// IPv6 route redistribution.
func (n *Node) ConfigureRouteReflector(ctx context.Context, opts RouteReflectorOpts) (*ChangeSet, error) {
	if err := n.precondition("configure-route-reflector", "bgp").Result(); err != nil {
		return nil, err
	}
	if opts.LocalASN == 0 {
		return nil, fmt.Errorf("route reflector requires local ASN")
	}
	if opts.ClusterID == "" {
		return nil, fmt.Errorf("route reflector requires cluster ID")
	}

	cs := NewChangeSet(n.name, "device.configure-route-reflector")

	// BGP globals with RR-specific settings
	cs.Adds(CreateBGPGlobalsConfig("default", opts.LocalASN, opts.RouterID, map[string]string{
		"rr_cluster_id":         opts.ClusterID,
		"load_balance_mp_relax": "true",
		"ebgp_requires_policy":  "false",
		"suppress_fib_pending":  "false",
		"log_neighbor_changes":  "true",
	}))

	// RR clients: IPv4 unicast only, RR-client flag
	for _, client := range opts.Clients {
		cs.Adds(CreateBGPNeighborConfig(client.IP, client.ASN, opts.LocalAddr, BGPNeighborOpts{
			EBGPMultihop: true,
			ActivateIPv4: true,
			RRClient:     true,
			NextHopSelf:  true,
		}))
	}

	// RR-to-RR peers: full AF set (IPv4+IPv6+EVPN), RR-client
	for _, peer := range opts.Peers {
		cs.Adds(CreateBGPNeighborConfig(peer.IP, peer.ASN, opts.LocalAddr, BGPNeighborOpts{
			EBGPMultihop:    true,
			ActivateIPv4:    true,
			RRClient:        true,
			NextHopSelf:     true,
			ActivateIPv6:    true,
			RRClientIPv6:    true,
			NextHopSelfIPv6: true,
			ActivateEVPN:    true,
			RRClientEVPN:    true,
		}))
	}

	// IPv6 route redistribution for RR
	cs.Adds(CreateRouteRedistributeConfig("default", "connected", "ipv6"))

	n.applyShadow(cs)
	util.WithDevice(n.name).Infof("Configured route reflector (cluster=%s, %d clients, %d peers)",
		opts.ClusterID, len(opts.Clients), len(opts.Peers))
	return cs, nil
}
