package node

import (
	"context"
	"fmt"
	"strconv"

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

	if err := n.precondition(sonic.OpBindMACVPN, i.name).Result(); err != nil {
		return nil, err
	}
	if !i.IsVLAN() {
		return nil, fmt.Errorf("bind-macvpn only valid for VLAN interfaces")
	}
	if !n.VTEPExists() {
		return nil, fmt.Errorf("bind-macvpn '%s' on %s requires VTEP — run 'newtron -D %s evpn setup' first",
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

	cs := NewChangeSet(n.Name(), "interface."+sonic.OpBindMACVPN)
	cs.ReverseOp = "interface.unbind-macvpn"
	cs.OperationParams = map[string]string{"interface": i.name}

	vlanName := i.name // e.g., "Vlan100"

	// Add VNI mapping (delegates to evpn_ops.go config function)
	if macvpnDef.VNI > 0 {
		cs.Adds(createVniMapConfig(vlanName, macvpnDef.VNI))
	}

	// Configure ARP suppression (delegates to evpn_ops.go config function)
	if macvpnDef.ARPSuppression {
		cs.Adds(enableArpSuppressionConfig(vlanName))
	}

	// Write intent record with all values needed for teardown
	intentParams := map[string]string{
		sonic.FieldMACVPN: macvpnName,
		sonic.FieldVNI:    strconv.Itoa(macvpnDef.VNI),
	}
	if macvpnDef.ARPSuppression {
		intentParams[sonic.FieldARPSuppression] = "true"
	}
	// VLAN-scoped intent: macvpn binds to the VLAN (consistent with Node.BindMACVPN)
	var vlanID int
	fmt.Sscanf(i.name, "Vlan%d", &vlanID)
	intentKey := "macvpn|" + strconv.Itoa(vlanID)
	if err := n.writeIntent(cs, sonic.OpBindMACVPN, intentKey, intentParams, []string{"vlan|" + strconv.Itoa(vlanID)}); err != nil {
		return nil, err
	}
	n.applyShadow(cs)

	util.WithDevice(n.Name()).Infof("Bound MAC-VPN '%s' to %s (VNI: %d)", macvpnName, vlanName, macvpnDef.VNI)
	return cs, nil
}


// UnbindMACVPN removes the MAC-VPN binding from this VLAN interface.
// Reads the intent record to determine what was applied (VNI, ARP suppression).
func (i *Interface) UnbindMACVPN(ctx context.Context) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("unbind-macvpn", i.name).Result(); err != nil {
		return nil, err
	}
	if !i.IsVLAN() {
		return nil, fmt.Errorf("unbind-macvpn only valid for VLAN interfaces")
	}

	// VLAN-scoped intent key (consistent with Node.UnbindMACVPN)
	var vlanID int
	fmt.Sscanf(i.name, "Vlan%d", &vlanID)
	intentKey := "macvpn|" + strconv.Itoa(vlanID)

	// Read teardown values from intent — not from CONFIG_DB
	intent := n.GetIntent(intentKey)
	if intent == nil {
		return nil, fmt.Errorf("no MAC-VPN intent for %s", i.name)
	}

	vni, _ := strconv.Atoi(intent.Params[sonic.FieldVNI])

	cs := NewChangeSet(n.Name(), "interface.unbind-macvpn")

	// Remove L2VNI mapping using deterministic key from intent
	if vni > 0 {
		cs.Deletes(deleteVniMapConfig(vni, i.name))
	}

	// Remove ARP suppression if it was enabled
	if intent.Params[sonic.FieldARPSuppression] == "true" {
		cs.Deletes(disableArpSuppressionConfig(i.name))
	}

	if err := n.deleteIntent(cs, intentKey); err != nil {
		return nil, err
	}
	n.applyShadow(cs)
	util.WithDevice(n.Name()).Infof("Unbound MAC-VPN from %s", i.name)
	return cs, nil
}

