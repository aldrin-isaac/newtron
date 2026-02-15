package node

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// EVPN Operations
// ============================================================================

// VTEPConfig holds configuration options for CreateVTEP.
type VTEPConfig struct {
	SourceIP string // VTEP source IP (typically loopback)
}

// SVIConfig holds configuration options for ConfigureSVI.
type SVIConfig struct {
	VRF        string // VRF to bind the SVI to
	IPAddress  string // IP address with prefix (e.g., "10.1.100.1/24")
	AnycastMAC string // SAG anycast gateway MAC (e.g., "00:00:00:00:01:01")
}

// CreateVTEP creates a VXLAN Tunnel Endpoint.
func (n *Node) CreateVTEP(ctx context.Context, opts VTEPConfig) (*ChangeSet, error) {
	if err := n.precondition("create-vtep", "vtep1").
		Check(!n.VTEPExists(), "VTEP must not exist", "VTEP already configured").
		Check(opts.SourceIP != "", "source IP required", "source IP must be specified").
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.create-vtep")

	// Create VXLAN tunnel
	cs.Add("VXLAN_TUNNEL", "vtep1", ChangeAdd, nil, map[string]string{
		"src_ip": opts.SourceIP,
	})

	// Create EVPN NVO
	cs.Add("VXLAN_EVPN_NVO", "nvo1", ChangeAdd, nil, map[string]string{
		"source_vtep": "vtep1",
	})

	util.WithDevice(n.name).Infof("Created VTEP with source IP %s", opts.SourceIP)
	return cs, nil
}

// DeleteVTEP removes the VXLAN Tunnel Endpoint.
func (n *Node) DeleteVTEP(ctx context.Context) (*ChangeSet, error) {
	if err := n.precondition("delete-vtep", "vtep1").
		RequireVTEPConfigured().
		Result(); err != nil {
		return nil, err
	}

	// Check for existing VNI mappings
	if n.configDB != nil && len(n.configDB.VXLANTunnelMap) > 0 {
		return nil, fmt.Errorf("cannot delete VTEP with existing VNI mappings")
	}

	cs := NewChangeSet(n.name, "device.delete-vtep")

	// Remove NVO first
	for name := range n.configDB.VXLANEVPNNVO {
		cs.Add("VXLAN_EVPN_NVO", name, ChangeDelete, nil, nil)
	}

	// Remove tunnel
	for name := range n.configDB.VXLANTunnel {
		cs.Add("VXLAN_TUNNEL", name, ChangeDelete, nil, nil)
	}

	util.WithDevice(n.name).Info("Deleted VTEP")
	return cs, nil
}

// MapL2VNI maps a VLAN to an L2VNI for EVPN.
func (n *Node) MapL2VNI(ctx context.Context, vlanID, vni int) (*ChangeSet, error) {
	if err := n.precondition("map-l2vni", vlanResource(vlanID)).
		RequireVTEPConfigured().
		RequireVLANExists(vlanID).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.map-l2vni")

	vlanName := fmt.Sprintf("Vlan%d", vlanID)
	mapKey := fmt.Sprintf("vtep1|map_%d_%s", vni, vlanName)

	cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeAdd, nil, map[string]string{
		"vlan": vlanName,
		"vni":  fmt.Sprintf("%d", vni),
	})

	util.WithDevice(n.name).Infof("Mapped VLAN %d to L2VNI %d", vlanID, vni)
	return cs, nil
}

// MapL3VNI maps a VRF to an L3VNI for EVPN.
func (n *Node) MapL3VNI(ctx context.Context, vrfName string, vni int) (*ChangeSet, error) {
	if err := n.precondition("map-l3vni", vrfName).
		RequireVTEPConfigured().
		RequireVRFExists(vrfName).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.map-l3vni")

	// Update VRF with VNI
	cs.Add("VRF", vrfName, ChangeModify, nil, map[string]string{
		"vni": fmt.Sprintf("%d", vni),
	})

	// Add VXLAN tunnel map
	mapKey := fmt.Sprintf("vtep1|map_%d_%s", vni, vrfName)
	cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeAdd, nil, map[string]string{
		"vrf": vrfName,
		"vni": fmt.Sprintf("%d", vni),
	})

	util.WithDevice(n.name).Infof("Mapped VRF %s to L3VNI %d", vrfName, vni)
	return cs, nil
}

// UnmapVNI removes a VNI mapping.
func (n *Node) UnmapVNI(ctx context.Context, vni int) (*ChangeSet, error) {
	if err := n.precondition("unmap-vni", fmt.Sprintf("VNI-%d", vni)).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.unmap-vni")

	// Find and remove the mapping
	if n.configDB != nil {
		for key, mapping := range n.configDB.VXLANTunnelMap {
			if mapping.VNI == fmt.Sprintf("%d", vni) {
				cs.Add("VXLAN_TUNNEL_MAP", key, ChangeDelete, nil, nil)
				break
			}
		}
	}

	if cs.IsEmpty() {
		return nil, fmt.Errorf("VNI %d mapping not found", vni)
	}

	util.WithDevice(n.name).Infof("Unmapped VNI %d", vni)
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

// ConfigureSVI configures a VLAN's SVI (Layer 3 interface).
// This creates VLAN_INTERFACE entries for VRF binding and IP assignment,
// and optionally sets up SAG (Static Anycast Gateway) for anycast MAC.
func (n *Node) ConfigureSVI(ctx context.Context, vlanID int, opts SVIConfig) (*ChangeSet, error) {
	pc := n.precondition("configure-svi", vlanResource(vlanID)).
		RequireVLANExists(vlanID)
	if opts.VRF != "" {
		pc.RequireVRFExists(opts.VRF)
	}
	if err := pc.Result(); err != nil {
		return nil, err
	}

	vlanName := fmt.Sprintf("Vlan%d", vlanID)
	cs := NewChangeSet(n.name, "device.configure-svi")

	// VLAN_INTERFACE entry with optional VRF binding
	fields := map[string]string{}
	if opts.VRF != "" {
		fields["vrf_name"] = opts.VRF
	}
	cs.Add("VLAN_INTERFACE", vlanName, ChangeAdd, nil, fields)

	// IP address binding
	if opts.IPAddress != "" {
		ipKey := fmt.Sprintf("%s|%s", vlanName, opts.IPAddress)
		cs.Add("VLAN_INTERFACE", ipKey, ChangeAdd, nil, map[string]string{})
	}

	// Anycast gateway MAC (SAG)
	if opts.AnycastMAC != "" {
		cs.Add("SAG_GLOBAL", "IPv4", ChangeAdd, nil, map[string]string{
			"gwmac": opts.AnycastMAC,
		})
	}

	util.WithDevice(n.name).Infof("Configured SVI for VLAN %d", vlanID)
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
		cs.Add("VXLAN_TUNNEL", "vtep1", ChangeAdd, nil, map[string]string{
			"src_ip": sourceIP,
		})
		cs.Add("VXLAN_EVPN_NVO", "nvo1", ChangeAdd, nil, map[string]string{
			"source_vtep": "vtep1",
		})
	}

	// Create BGP EVPN sessions with route reflectors (skip if already exist)
	if len(resolved.BGPNeighbors) > 0 {
		// Ensure BGP globals are set
		cs.Add("BGP_GLOBALS", "default", ChangeAdd, nil, map[string]string{
			"local_asn": fmt.Sprintf("%d", resolved.ASNumber),
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

			fields := map[string]string{
				"asn":          fmt.Sprintf("%d", resolved.ASNumber),
				"admin_status": "up",
				"name":         "route-reflector",
				"local_addr":   resolved.LoopbackIP,
			}
			cs.Add("BGP_NEIGHBOR", fmt.Sprintf("default|%s", rrIP), ChangeAdd, nil, fields)

			afKey := fmt.Sprintf("default|%s|l2vpn_evpn", rrIP)
			cs.Add("BGP_NEIGHBOR_AF", afKey, ChangeAdd, nil, map[string]string{
				"activate": "true",
			})
		}
	}

	util.WithDevice(n.name).Infof("Setup EVPN (source IP %s, %d route reflectors)", sourceIP, len(resolved.BGPNeighbors))
	return cs, nil
}
