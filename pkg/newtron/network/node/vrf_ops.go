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
type VRFConfig struct {
	L3VNI int
}

// CreateVRF creates a new VRF.
func (n *Node) CreateVRF(ctx context.Context, name string, opts VRFConfig) (*ChangeSet, error) {
	if err := n.precondition("create-vrf", name).
		RequireVRFNotExists(name).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.create-vrf")

	fields := map[string]string{}
	if opts.L3VNI > 0 {
		fields["vni"] = fmt.Sprintf("%d", opts.L3VNI)
	}

	cs.Add("VRF", name, ChangeAdd, nil, fields)

	// Add L3VNI mapping if specified
	if opts.L3VNI > 0 {
		mapKey := fmt.Sprintf("vtep1|map_%d_%s", opts.L3VNI, name)
		cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeAdd, nil, map[string]string{
			"vrf": name,
			"vni": fmt.Sprintf("%d", opts.L3VNI),
		})
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

	// Remove VNI mapping if exists
	if n.configDB != nil {
		for key, mapping := range n.configDB.VXLANTunnelMap {
			if mapping.VRF == name {
				cs.Add("VXLAN_TUNNEL_MAP", key, ChangeDelete, nil, nil)
			}
		}
	}

	cs.Add("VRF", name, ChangeDelete, nil, nil)

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

// BindIPVPN binds a VRF to an IP-VPN definition (creates L3VNI mapping).
func (n *Node) BindIPVPN(ctx context.Context, vrfName string, ipvpnDef *spec.IPVPNSpec) (*ChangeSet, error) {
	if err := n.precondition("bind-ipvpn", vrfName).
		RequireVTEPConfigured().
		RequireVRFExists(vrfName).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.bind-ipvpn")

	// Set VNI on the VRF
	cs.Add("VRF", vrfName, ChangeModify, nil, map[string]string{
		"vni": fmt.Sprintf("%d", ipvpnDef.L3VNI),
	})

	// Add VXLAN_TUNNEL_MAP entry for L3VNI
	mapKey := fmt.Sprintf("vtep1|map_%d_%s", ipvpnDef.L3VNI, vrfName)
	cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeAdd, nil, map[string]string{
		"vrf": vrfName,
		"vni": fmt.Sprintf("%d", ipvpnDef.L3VNI),
	})

	util.WithDevice(n.name).Infof("Bound VRF %s to IP-VPN (L3VNI %d)", vrfName, ipvpnDef.L3VNI)
	return cs, nil
}

// UnbindIPVPN removes the IP-VPN binding from a VRF (removes L3VNI mapping).
func (n *Node) UnbindIPVPN(ctx context.Context, vrfName string) (*ChangeSet, error) {
	if err := n.precondition("unbind-ipvpn", vrfName).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.unbind-ipvpn")

	// Find and remove the VXLAN_TUNNEL_MAP entry for this VRF
	if n.configDB != nil {
		for key, mapping := range n.configDB.VXLANTunnelMap {
			if mapping.VRF == vrfName {
				cs.Add("VXLAN_TUNNEL_MAP", key, ChangeDelete, nil, nil)
				break
			}
		}
	}

	// Clear VNI on the VRF
	cs.Add("VRF", vrfName, ChangeModify, nil, map[string]string{
		"vni": "",
	})

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

	// Key format: "vrfName|prefix" for non-default VRF, just "prefix" for default
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

	cs.Add("STATIC_ROUTE", routeKey, ChangeAdd, nil, fields)

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
