package node

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)


// ============================================================================
// MAC-VPN (L2 EVPN) Operations
// ============================================================================

// BindMACVPN binds this VLAN interface to a MAC-VPN definition.
// This configures the L2VNI mapping and ARP suppression from the macvpn definition.
func (i *Interface) BindMACVPN(ctx context.Context, macvpnName string, macvpnDef *spec.MACVPNSpec) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("bind-macvpn", i.name).Result(); err != nil {
		return nil, err
	}
	if !i.IsVLAN() {
		return nil, fmt.Errorf("bind-macvpn only valid for VLAN interfaces")
	}
	if !n.VTEPExists() {
		return nil, fmt.Errorf("MAC-VPN requires VTEP configuration")
	}

	// Check platform support for MACVPN (EVPN VXLAN)
	resolved := n.Resolved()
	if resolved.Platform != "" {
		if platform, err := n.GetPlatform(resolved.Platform); err == nil {
			if !platform.SupportsFeature("macvpn") {
				return nil, fmt.Errorf("platform %s does not support MAC-VPN (EVPN VXLAN)", resolved.Platform)
			}
		}
	}

	cs := NewChangeSet(n.Name(), "interface.bind-macvpn")

	vlanName := i.name // e.g., "Vlan100"

	// Add VNI mapping
	if macvpnDef.VNI > 0 {
		cs.Add("VXLAN_TUNNEL_MAP", VNIMapKey(macvpnDef.VNI, vlanName), ChangeAdd, nil, map[string]string{
			"vlan": vlanName,
			"vni":  fmt.Sprintf("%d", macvpnDef.VNI),
		})
	}

	// Configure ARP suppression
	if macvpnDef.ARPSuppression {
		cs.Add("SUPPRESS_VLAN_NEIGH", vlanName, ChangeAdd, nil, map[string]string{
			"suppress": "on",
		})
	}

	util.WithDevice(n.Name()).Infof("Bound MAC-VPN '%s' to %s (VNI: %d)", macvpnName, vlanName, macvpnDef.VNI)
	return cs, nil
}

// UnbindMACVPN removes the MAC-VPN binding from this VLAN interface.
// This removes the L2VNI mapping and ARP suppression settings.
func (i *Interface) UnbindMACVPN(ctx context.Context) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("unbind-macvpn", i.name).Result(); err != nil {
		return nil, err
	}
	if !i.IsVLAN() {
		return nil, fmt.Errorf("unbind-macvpn only valid for VLAN interfaces")
	}

	// Check platform support for MACVPN (EVPN VXLAN)
	resolved := n.Resolved()
	if resolved.Platform != "" {
		if platform, err := n.GetPlatform(resolved.Platform); err == nil {
			if !platform.SupportsFeature("macvpn") {
				return nil, fmt.Errorf("platform %s does not support MAC-VPN (EVPN VXLAN)", resolved.Platform)
			}
		}
	}

	cs := NewChangeSet(n.Name(), "interface.unbind-macvpn")

	vlanName := i.name
	configDB := n.ConfigDB()

	// Remove L2VNI mapping
	if configDB != nil {
		for key, mapping := range configDB.VXLANTunnelMap {
			if mapping.VLAN == vlanName {
				cs.Add("VXLAN_TUNNEL_MAP", key, ChangeDelete, nil, nil)
				break
			}
		}
	}

	// Remove ARP suppression
	if configDB != nil {
		if _, ok := configDB.SuppressVLANNeigh[vlanName]; ok {
			cs.Add("SUPPRESS_VLAN_NEIGH", vlanName, ChangeDelete, nil, nil)
		}
	}

	util.WithDevice(n.Name()).Infof("Unbound MAC-VPN from %s", vlanName)
	return cs, nil
}

