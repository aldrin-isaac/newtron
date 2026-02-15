package network

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
func (d *Device) CreateVRF(ctx context.Context, name string, opts VRFConfig) (*ChangeSet, error) {
	if err := d.precondition("create-vrf", name).
		RequireVRFNotExists(name).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(d.name, "device.create-vrf")

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

	util.WithDevice(d.name).Infof("Created VRF %s", name)
	return cs, nil
}

// DeleteVRF removes a VRF.
func (d *Device) DeleteVRF(ctx context.Context, name string) (*ChangeSet, error) {
	if err := d.precondition("delete-vrf", name).
		RequireVRFExists(name).
		Result(); err != nil {
		return nil, err
	}

	// Check no interfaces are bound to this VRF
	vrfInfo, _ := d.GetVRF(name)
	if vrfInfo != nil && len(vrfInfo.Interfaces) > 0 {
		return nil, fmt.Errorf("VRF %s has interfaces bound: %v", name, vrfInfo.Interfaces)
	}

	cs := NewChangeSet(d.name, "device.delete-vrf")

	// Remove VNI mapping if exists
	if d.configDB != nil {
		for key, mapping := range d.configDB.VXLANTunnelMap {
			if mapping.VRF == name {
				cs.Add("VXLAN_TUNNEL_MAP", key, ChangeDelete, nil, nil)
			}
		}
	}

	cs.Add("VRF", name, ChangeDelete, nil, nil)

	util.WithDevice(d.name).Infof("Deleted VRF %s", name)
	return cs, nil
}

// ============================================================================
// VRF Interface Binding
// ============================================================================

// AddVRFInterface binds an interface to a VRF.
func (d *Device) AddVRFInterface(ctx context.Context, vrfName, intfName string) (*ChangeSet, error) {
	intfName = util.NormalizeInterfaceName(intfName)

	if err := d.precondition("add-vrf-interface", vrfName).
		RequireVRFExists(vrfName).
		RequireInterfaceExists(intfName).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(d.name, "device.add-vrf-interface")

	cs.Add("INTERFACE", intfName, ChangeModify, nil, map[string]string{
		"vrf_name": vrfName,
	})

	util.WithDevice(d.name).Infof("Bound interface %s to VRF %s", intfName, vrfName)
	return cs, nil
}

// RemoveVRFInterface removes a VRF binding from an interface.
func (d *Device) RemoveVRFInterface(ctx context.Context, vrfName, intfName string) (*ChangeSet, error) {
	if err := d.precondition("remove-vrf-interface", vrfName).Result(); err != nil {
		return nil, err
	}

	intfName = util.NormalizeInterfaceName(intfName)

	cs := NewChangeSet(d.name, "device.remove-vrf-interface")

	cs.Add("INTERFACE", intfName, ChangeModify, nil, map[string]string{
		"vrf_name": "",
	})

	util.WithDevice(d.name).Infof("Removed VRF binding from interface %s", intfName)
	return cs, nil
}

// ============================================================================
// IP-VPN Binding (L3VNI)
// ============================================================================

// BindIPVPN binds a VRF to an IP-VPN definition (creates L3VNI mapping).
func (d *Device) BindIPVPN(ctx context.Context, vrfName string, ipvpnDef *spec.IPVPNSpec) (*ChangeSet, error) {
	if err := d.precondition("bind-ipvpn", vrfName).
		RequireVTEPConfigured().
		RequireVRFExists(vrfName).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(d.name, "device.bind-ipvpn")

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

	util.WithDevice(d.name).Infof("Bound VRF %s to IP-VPN (L3VNI %d)", vrfName, ipvpnDef.L3VNI)
	return cs, nil
}

// UnbindIPVPN removes the IP-VPN binding from a VRF (removes L3VNI mapping).
func (d *Device) UnbindIPVPN(ctx context.Context, vrfName string) (*ChangeSet, error) {
	if err := d.precondition("unbind-ipvpn", vrfName).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(d.name, "device.unbind-ipvpn")

	// Find and remove the VXLAN_TUNNEL_MAP entry for this VRF
	if d.configDB != nil {
		for key, mapping := range d.configDB.VXLANTunnelMap {
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

	util.WithDevice(d.name).Infof("Unbound IP-VPN from VRF %s", vrfName)
	return cs, nil
}

// ============================================================================
// Static Routes
// ============================================================================

// AddStaticRoute adds a static route to a VRF.
func (d *Device) AddStaticRoute(ctx context.Context, vrfName, prefix, nextHop string, metric int) (*ChangeSet, error) {
	if err := d.precondition("add-static-route", prefix).
		Check(vrfName == "" || vrfName == "default" || d.VRFExists(vrfName),
			"VRF must exist", fmt.Sprintf("VRF '%s' not found", vrfName)).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(d.name, "device.add-static-route")

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

	util.WithDevice(d.name).Infof("Added static route %s via %s (VRF %s)", prefix, nextHop, vrfName)
	return cs, nil
}

// RemoveStaticRoute removes a static route from a VRF.
func (d *Device) RemoveStaticRoute(ctx context.Context, vrfName, prefix string) (*ChangeSet, error) {
	if err := d.precondition("remove-static-route", prefix).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(d.name, "device.remove-static-route")

	// Key format: "vrfName|prefix" for non-default VRF, just "prefix" for default
	var routeKey string
	if vrfName == "" || vrfName == "default" {
		routeKey = prefix
	} else {
		routeKey = fmt.Sprintf("%s|%s", vrfName, prefix)
	}

	cs.Add("STATIC_ROUTE", routeKey, ChangeDelete, nil, nil)

	util.WithDevice(d.name).Infof("Removed static route %s (VRF %s)", prefix, vrfName)
	return cs, nil
}
