package node

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// ============================================================================
// VLAN Operations
// ============================================================================

// CreateVLAN creates a new VLAN on this device.
// Intent-idempotent: if the vlan intent already exists, returns empty ChangeSet.
func (n *Node) CreateVLAN(ctx context.Context, vlanID int, opts VLANConfig) (*ChangeSet, error) {
	resource := "vlan|" + strconv.Itoa(vlanID)
	if n.GetIntent(resource) != nil {
		return NewChangeSet(n.name, "device.create-vlan"), nil
	}
	cs, err := n.op(sonic.OpCreateVLAN, vlanResource(vlanID), ChangeAdd,
		func(pc *PreconditionChecker) {
			pc.Check(vlanID >= 1 && vlanID <= 4094, "valid VLAN ID", fmt.Sprintf("must be 1-4094, got %d", vlanID))
		},
		func() []sonic.Entry { return createVlanConfig(vlanID, opts) },
		"device.delete-vlan")
	if err != nil {
		return nil, err
	}
	intentParams := map[string]string{
		sonic.FieldVLANID: strconv.Itoa(vlanID),
	}
	if opts.Description != "" {
		intentParams[sonic.FieldDescription] = opts.Description
	}
	if opts.L2VNI > 0 {
		intentParams[sonic.FieldVNI] = strconv.Itoa(opts.L2VNI)
	}
	if err := n.writeIntent(cs, sonic.OpCreateVLAN, "vlan|"+strconv.Itoa(vlanID), intentParams, []string{"device"}); err != nil {
		return nil, err
	}
	cs.OperationParams = map[string]string{"vlan_id": fmt.Sprintf("%d", vlanID)}
	util.WithDevice(n.name).Infof("Created VLAN %d", vlanID)
	return cs, nil
}

// DeleteVLAN removes a VLAN from this device.
// Checks DAG children first (I5) — if children exist, returns an error
// before issuing any CONFIG_DB deletes.
func (n *Node) DeleteVLAN(ctx context.Context, vlanID int) (*ChangeSet, error) {
	intentKey := "vlan|" + strconv.Itoa(vlanID)

	// Pre-check: if intent has children, deleteIntent would fail (I5).
	// Check here so we fail before n.op() issues CONFIG_DB deletes.
	if intent := n.GetIntent(intentKey); intent != nil && len(intent.Children) > 0 {
		return nil, fmt.Errorf("deleteIntent %q: has children %v", intentKey, intent.Children)
	}

	cs, err := n.op("delete-vlan", vlanResource(vlanID), ChangeDelete,
		func(pc *PreconditionChecker) { pc.RequireVLANExists(vlanID) },
		func() []sonic.Entry { return deleteVlanConfig(vlanID) })
	if err != nil {
		return nil, err
	}
	if err := n.deleteIntent(cs, intentKey); err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Deleted VLAN %d", vlanID)
	return cs, nil
}

// ConfigureIRB configures a VLAN's IRB (Integrated Routing and Bridging) interface.
// This creates VLAN_INTERFACE entries for VRF binding and IP assignment,
// and optionally sets up SAG (Static Anycast Gateway) for anycast MAC.
// Intent-idempotent: if the IRB intent already exists, returns empty ChangeSet.
//
// configure-irb is the SVI's sole author (§6). Under the delivery-point flip an
// irb-type service binds to the IRB and never writes VLAN_INTERFACE, so the
// single-author guarantee is structural, not defensive: there is no rival
// writer to consult before authoring the gateway, and unconfigure-irb is
// refused while a service is bound because the binding is a DAG child of this
// identity (I5), not because of a bespoke check.
func (n *Node) ConfigureIRB(ctx context.Context, vlanID int, opts IRBConfig) (*ChangeSet, error) {
	if n.GetIntent("interface|"+VLANName(vlanID)) != nil {
		return NewChangeSet(n.name, "device."+sonic.OpConfigureIRB), nil
	}

	cs, err := n.op(sonic.OpConfigureIRB, vlanResource(vlanID), ChangeAdd,
		func(pc *PreconditionChecker) {
			pc.RequireVLANExists(vlanID)
			if opts.VRF != "" {
				pc.RequireVRFExists(opts.VRF)
			}
		},
		func() []sonic.Entry { return createSviConfig(vlanID, opts) },
		"device.unconfigure-irb")
	if err != nil {
		return nil, err
	}
	irbParents := []string{"vlan|" + strconv.Itoa(vlanID)}
	if opts.VRF != "" {
		irbParents = append(irbParents, "vrf|"+opts.VRF)
	}
	if err := n.writeIntent(cs, sonic.OpConfigureIRB, "interface|"+VLANName(vlanID), map[string]string{
		sonic.FieldVLANID:     strconv.Itoa(vlanID),
		sonic.FieldVRF:        opts.VRF,
		sonic.FieldIPAddress:  opts.IPAddress,
		sonic.FieldAnycastMAC: opts.AnycastMAC,
	}, irbParents); err != nil {
		return nil, err
	}
	cs.OperationParams = map[string]string{"vlan_id": fmt.Sprintf("%d", vlanID)}
	util.WithDevice(n.name).Infof("Configured IRB for VLAN %d", vlanID)
	return cs, nil
}


// UpdateIRB atomically mutates the operator-authored IRB identity for a
// VLAN — the §48 in-place path: the VLAN_INTERFACE base row is never
// touched, so intfmgrd observes an edit to the gateway's sub-entries, not
// a teardown of the SVI. Two mutable fields:
//
//   - Gateway IP: the IP is the sub-entry's key (§47), so a change is
//     delivered as delete-old-key + add-new-key in one ChangeSet — a move,
//     never a whole-SVI bounce.
//   - Anycast MAC: a field edit on the SAG_GLOBAL singleton — refused when
//     other anycast IRBs share it (device-wide value; changing it through
//     one VLAN's update would silently retarget every anycast gateway).
//
// A VRF move is refused: rebinding an SVI re-originates its routes, which
// is a teardown-replace by nature — unconfigure-irb + configure-irb states
// that intent honestly (§48: the delivery must match the intent).
func (n *Node) UpdateIRB(ctx context.Context, vlanID int, opts IRBConfig) (*ChangeSet, error) {
	if err := n.precondition(sonic.OpUpdateIRB, vlanResource(vlanID)).Result(); err != nil {
		return nil, err
	}
	intentKey := "interface|" + VLANName(vlanID)
	intent := n.GetIntent(intentKey)
	if intent == nil || intent.Operation != sonic.OpConfigureIRB {
		return nil, fmt.Errorf("no operator-authored IRB for VLAN %d — use configure-irb first", vlanID)
	}

	oldVRF := intent.Params[sonic.FieldVRF]
	if opts.VRF != oldVRF {
		return nil, fmt.Errorf("update-irb cannot move VLAN %d between VRFs (%q → %q): a VRF move re-originates the SVI's routes and is a teardown-replace by nature — use unconfigure-irb then configure-irb", vlanID, oldVRF, opts.VRF)
	}
	oldIP := intent.Params[sonic.FieldIPAddress]
	oldMAC := intent.Params[sonic.FieldAnycastMAC]
	if opts.IPAddress == oldIP && opts.AnycastMAC == oldMAC {
		return NewChangeSet(n.name, "device."+sonic.OpUpdateIRB), nil
	}
	if opts.IPAddress != "" && !util.IsValidIPv4CIDR(opts.IPAddress) {
		return nil, fmt.Errorf("invalid IP address: %s", opts.IPAddress)
	}

	cs := NewChangeSet(n.name, "device."+sonic.OpUpdateIRB)

	if opts.IPAddress != oldIP {
		if oldIP != "" {
			cs.Deletes(deleteSviIPConfig(vlanID, oldIP))
		}
		if opts.IPAddress != "" {
			cs.Adds(assignSviIPConfig(vlanID, opts.IPAddress))
		}
	}

	if opts.AnycastMAC != oldMAC {
		// SAG_GLOBAL is one device-wide row. Only this VLAN's IRB may
		// reference it, or the change silently retargets the others.
		for resource, other := range n.IntentsByOp(sonic.OpConfigureIRB) {
			if resource != intentKey && other.Params[sonic.FieldAnycastMAC] != "" {
				return nil, fmt.Errorf("anycast MAC is the device-wide SAG_GLOBAL value and %s also references it — updating it through VLAN %d would retarget every anycast gateway", resource, vlanID)
			}
		}
		switch {
		case oldMAC == "":
			cs.Adds(setSagGwmacConfig(opts.AnycastMAC))
		case opts.AnycastMAC == "":
			cs.Deletes(deleteSagGlobalConfig())
		default:
			cs.Replace(n, setSagGwmacConfig(oldMAC), setSagGwmacConfig(opts.AnycastMAC))
		}
	}

	// Re-record under the creating verb on the same key (the update-verb
	// convention — see UpdateBGPPeer): replay reproduces the updated state.
	if err := n.writeIntent(cs, sonic.OpConfigureIRB, intentKey, map[string]string{
		sonic.FieldVLANID:     strconv.Itoa(vlanID),
		sonic.FieldVRF:        oldVRF,
		sonic.FieldIPAddress:  opts.IPAddress,
		sonic.FieldAnycastMAC: opts.AnycastMAC,
	}, intent.Parents); err != nil {
		return nil, err
	}
	cs.ReverseOp = "device.unconfigure-irb"
	cs.OperationParams = map[string]string{"vlan_id": strconv.Itoa(vlanID)}

	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Updated IRB for VLAN %d", vlanID)
	return cs, nil
}

// UnconfigureIRB removes a VLAN's IRB (Integrated Routing and Bridging) interface configuration.
// Reads the intent record to determine what was applied (VRF, IP, anycast MAC).
func (n *Node) UnconfigureIRB(ctx context.Context, vlanID int) (*ChangeSet, error) {
	if err := n.precondition("unconfigure-irb", vlanResource(vlanID)).Result(); err != nil {
		return nil, err
	}

	intentKey := "interface|" + VLANName(vlanID)
	intent := n.GetIntent(intentKey)
	if intent == nil {
		return nil, fmt.Errorf("no IRB intent for VLAN %d", vlanID)
	}

	cs := NewChangeSet(n.name, "device.unconfigure-irb")

	// Remove IP address entry (children before parents)
	if ip := intent.Params[sonic.FieldIPAddress]; ip != "" {
		cs.Deletes(deleteSviIPConfig(vlanID, ip))
	}

	// Remove base VLAN_INTERFACE entry
	cs.Deletes(deleteSviBaseConfig(vlanID))

	// Remove SAG_GLOBAL if anycast MAC was set and no other IRB intent uses it
	if intent.Params[sonic.FieldAnycastMAC] != "" {
		// SAG_GLOBAL is shared — only remove if no other IRB intent uses anycast MAC
		otherSAG := false
		for resource, irbIntent := range n.IntentsByOp(sonic.OpConfigureIRB) {
			if resource != intentKey && irbIntent.Params[sonic.FieldAnycastMAC] != "" {
				otherSAG = true
				break
			}
		}
		if !otherSAG {
			cs.Deletes(deleteSagGlobalConfig())
		}
	}

	if err := n.deleteIntent(cs, intentKey); err != nil {
		return nil, err
	}
	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Removed IRB for VLAN %d", vlanID)
	return cs, nil
}

// ============================================================================
// VLAN Data Types and Queries
// ============================================================================

// VLANInfo represents VLAN data assembled from config_db for operations.
type VLANInfo struct {
	ID         int
	Name       string      // VLAN name from config
	Members    []string    // All member interfaces
	IRBStatus  string      // "up" if VLAN_INTERFACE exists, empty otherwise
	MACVPNInfo *MACVPNInfo // MAC-VPN binding info (L2VNI, ARP suppression)
}

// L2VNI returns the L2VNI for this VLAN (0 if not configured).
func (v *VLANInfo) L2VNI() int {
	if v.MACVPNInfo != nil {
		return v.MACVPNInfo.L2VNI
	}
	return 0
}

// MACVPNInfo contains MAC-VPN binding information for a VLAN.
// This is populated from VXLAN_TUNNEL_MAP and SUPPRESS_VLAN_NEIGH tables.
type MACVPNInfo struct {
	Name           string `json:"name,omitempty"`   // MAC-VPN definition name (from network.json)
	L2VNI          int    `json:"l2_vni,omitempty"` // L2VNI from VXLAN_TUNNEL_MAP
	ARPSuppression bool   `json:"arp_suppression"`  // ARP suppression enabled
}


// GetVLAN retrieves VLAN information from the intent DB.
func (n *Node) GetVLAN(id int) (*VLANInfo, error) {
	if n.configDB == nil {
		return nil, util.ErrNotConnected
	}

	vlanIntent := n.GetIntent("vlan|" + strconv.Itoa(id))
	if vlanIntent == nil {
		return nil, fmt.Errorf("VLAN %d not found", id)
	}

	info := &VLANInfo{ID: id, Name: vlanIntent.Params[sonic.FieldDescription]}

	// Collect member interfaces from service/configure-interface intents
	// that reference this VLAN ID.
	for resource, intent := range n.IntentsByParam(sonic.FieldVLANID, strconv.Itoa(id)) {
		// Skip the VLAN intent itself, macvpn intents, and IRB intents.
		if resource == "vlan|"+strconv.Itoa(id) ||
			strings.HasPrefix(resource, "macvpn|") ||
			strings.HasPrefix(resource, "interface|Vlan") {
			continue
		}
		if member := resourceInterfaceName(resource); member != "" {
			if intent.Params[sonic.FieldTagged] == "true" {
				info.Members = append(info.Members, member+"(t)")
			} else {
				info.Members = append(info.Members, member)
			}
		}
	}

	// Check for IRB from interface|Vlan{id} intent.
	if n.GetIntent("interface|Vlan"+strconv.Itoa(id)) != nil {
		info.IRBStatus = "up"
	}

	// Build MAC-VPN info from macvpn|{id} intent.
	macvpnIntent := n.GetIntent("macvpn|" + strconv.Itoa(id))
	if macvpnIntent != nil {
		macvpn := &MACVPNInfo{
			Name: macvpnIntent.Params[sonic.FieldMACVPN],
		}
		if vniStr := macvpnIntent.Params[sonic.FieldVNI]; vniStr != "" {
			fmt.Sscanf(vniStr, "%d", &macvpn.L2VNI)
		}
		if macvpnIntent.Params[sonic.FieldARPSuppression] == "true" {
			macvpn.ARPSuppression = true
		}
		if macvpn.L2VNI > 0 || macvpn.ARPSuppression {
			info.MACVPNInfo = macvpn
		}
	}

	return info, nil
}

// ListVLANs returns all VLAN IDs on this device.
func (n *Node) ListVLANs() []int {
	if n.configDB == nil {
		return nil
	}

	intents := n.IntentsByPrefix("vlan|")
	var ids []int
	for resource := range intents {
		parts := strings.SplitN(resource, "|", 2)
		if len(parts) == 2 {
			var id int
			if _, err := fmt.Sscanf(parts[1], "%d", &id); err == nil {
				ids = append(ids, id)
			}
		}
	}
	return ids
}
