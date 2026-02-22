package node

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
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
		return nil, fmt.Errorf("bind-macvpn '%s' on %s requires VTEP — run 'newtron -d %s evpn setup' first",
			macvpnName, n.Name(), n.Name())
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

	// Add VNI mapping (delegates to evpn_ops.go config function)
	if macvpnDef.VNI > 0 {
		for _, e := range vniMapConfig(vlanName, macvpnDef.VNI) {
			cs.Add(e.Table, e.Key, ChangeAdd, nil, e.Fields)
		}
	}

	// Configure ARP suppression (delegates to evpn_ops.go config function)
	if macvpnDef.ARPSuppression {
		for _, e := range arpSuppressionConfig(vlanName) {
			cs.Add(e.Table, e.Key, ChangeAdd, nil, e.Fields)
		}
	}

	util.WithDevice(n.Name()).Infof("Bound MAC-VPN '%s' to %s (VNI: %d)", macvpnName, vlanName, macvpnDef.VNI)
	return cs, nil
}

// macvpnUnbindConfig returns delete entries for unbinding a MAC-VPN from a VLAN:
// the L2VNI mapping and ARP suppression entry.
func macvpnUnbindConfig(configDB *sonic.ConfigDB, vlanName string) []sonic.Entry {
	var entries []sonic.Entry

	// Remove L2VNI mapping
	if configDB != nil {
		for key, mapping := range configDB.VXLANTunnelMap {
			if mapping.VLAN == vlanName {
				entries = append(entries, sonic.Entry{Table: "VXLAN_TUNNEL_MAP", Key: key})
				break
			}
		}
	}

	// Remove ARP suppression
	if configDB != nil {
		if _, ok := configDB.SuppressVLANNeigh[vlanName]; ok {
			entries = append(entries, sonic.Entry{Table: "SUPPRESS_VLAN_NEIGH", Key: vlanName})
		}
	}

	return entries
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

	cs := configToChangeSet(n.Name(), "interface.unbind-macvpn", macvpnUnbindConfig(n.ConfigDB(), i.name), ChangeDelete)

	util.WithDevice(n.Name()).Infof("Unbound MAC-VPN from %s", i.name)
	return cs, nil
}

