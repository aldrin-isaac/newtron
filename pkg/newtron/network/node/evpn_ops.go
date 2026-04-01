package node

import (
	"context"
	"fmt"
	"strconv"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/util"
)

// VTEPSourceIP returns the VTEP source IP.
// Reads from the device intent params (source_ip), falling back to resolved loopback IP.
func (n *Node) VTEPSourceIP() string {
	if intent := n.GetIntent("device"); intent != nil {
		if ip := intent.Params["source_ip"]; ip != "" {
			return ip
		}
	}
	return n.resolved.LoopbackIP
}

// ============================================================================
// EVPN Operations
// ============================================================================

// BindMACVPN maps a VLAN to an L2VNI for EVPN.
func (n *Node) BindMACVPN(ctx context.Context, vlanID int, macvpnName string) (*ChangeSet, error) {
	resource := "macvpn|" + strconv.Itoa(vlanID)
	if n.GetIntent(resource) != nil {
		return NewChangeSet(n.name, "device.bind-macvpn"), nil
	}
	macvpnDef, err := n.GetMACVPN(macvpnName)
	if err != nil {
		return nil, fmt.Errorf("bind-macvpn: %w", err)
	}
	vni := macvpnDef.VNI
	cs, err := n.op(sonic.OpBindMACVPN, vlanResource(vlanID), ChangeAdd,
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
		func() []sonic.Entry {
			entries := createVniMapConfig(VLANName(vlanID), vni)
			if macvpnDef.ARPSuppression {
				entries = append(entries, enableArpSuppressionConfig(VLANName(vlanID))...)
			}
			return entries
		},
		"device.unbind-macvpn")
	if err != nil {
		return nil, err
	}
	intentParams := map[string]string{
		sonic.FieldVLANID: strconv.Itoa(vlanID),
		sonic.FieldMACVPN: macvpnName,
		sonic.FieldVNI:    strconv.Itoa(vni),
	}
	if macvpnDef.ARPSuppression {
		intentParams[sonic.FieldARPSuppression] = "true"
	}
	if err := n.writeIntent(cs, sonic.OpBindMACVPN, "macvpn|"+strconv.Itoa(vlanID), intentParams, []string{"vlan|" + strconv.Itoa(vlanID)}); err != nil {
		return nil, err
	}

	cs.OperationParams = map[string]string{"vlan_id": fmt.Sprintf("%d", vlanID)}
	util.WithDevice(n.name).Infof("Mapped VLAN %d to L2VNI %d", vlanID, vni)
	return cs, nil
}


// UnbindMACVPN removes the L2VNI mapping for a VLAN.
// Reads the VNI from the intent record to construct deterministic delete entries.
func (n *Node) UnbindMACVPN(ctx context.Context, vlanID int) (*ChangeSet, error) {
	if err := n.precondition("unbind-macvpn", vlanResource(vlanID)).
		RequireVLANExists(vlanID).
		Result(); err != nil {
		return nil, err
	}

	// Read VNI from intent — not from CONFIG_DB scan
	intentKey := "macvpn|" + strconv.Itoa(vlanID)
	intent := n.GetIntent(intentKey)
	if intent == nil {
		return nil, fmt.Errorf("no MAC-VPN intent for VLAN %d", vlanID)
	}
	vni, _ := strconv.Atoi(intent.Params[sonic.FieldVNI])

	cs := NewChangeSet(n.name, "device.unbind-macvpn")
	if vni > 0 {
		cs.Deletes(deleteVniMapConfig(vni, VLANName(vlanID)))
	}
	if intent.Params[sonic.FieldARPSuppression] == "true" {
		cs.Deletes(disableArpSuppressionConfig(VLANName(vlanID)))
	}

	if err := n.deleteIntent(cs, intentKey); err != nil {
		return nil, err
	}

	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Unbound MAC-VPN for VLAN %d", vlanID)
	return cs, nil
}

// SetupVXLAN creates the VXLAN data-plane encapsulation entries: VXLAN_TUNNEL and
// VXLAN_EVPN_NVO. This is the data-plane half of EVPN setup; the control-plane
// half (BGP EVPN AF, peer group, overlay peers) is ConfigureBGPOverlay in bgp_ops.go.
// If sourceIP is empty, uses the device's resolved VTEP source IP (loopback).
func (n *Node) SetupVXLAN(ctx context.Context, sourceIP string) (*ChangeSet, error) {
	if err := n.precondition("setup-vxlan", "evpn").Result(); err != nil {
		return nil, err
	}

	resolved := n.Resolved()
	if sourceIP == "" {
		sourceIP = resolved.VTEPSourceIP
	}
	if sourceIP == "" {
		return nil, fmt.Errorf("no VTEP source IP available (specify sourceIP or set loopback_ip in profile)")
	}

	cs := NewChangeSet(n.name, "device.setup-vxlan")
	cs.ReverseOp = "device.teardown-vxlan"

	// Create VTEP unconditionally — render handles upserts safely.
	// SetupDevice guards cross-execution idempotency via GetIntent("device").
	cs.Adds(CreateVTEPConfig(sourceIP))

	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Setup VXLAN (source IP %s)", sourceIP)
	return cs, nil
}

// TeardownVXLAN removes VXLAN data-plane encapsulation: VXLAN NVO and VXLAN tunnel.
// This is the reverse of SetupVXLAN. The BGP control-plane half is TeardownBGPOverlay.
func (n *Node) TeardownVXLAN(ctx context.Context) (*ChangeSet, error) {
	if err := n.precondition("teardown-vxlan", "evpn").Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.teardown-vxlan")

	// Remove VXLAN NVO and tunnel (children before parents)
	cs.Deletes(deleteVxlanTunnelConfig())

	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Tore down VXLAN tunnel")
	return cs, nil
}
