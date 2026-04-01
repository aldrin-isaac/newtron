package node

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// VRF Operations
// ============================================================================

// CreateVRF creates a new VRF.
// Intent-idempotent: if the vrf intent already exists, returns empty ChangeSet.
func (n *Node) CreateVRF(ctx context.Context, name string, opts VRFConfig) (*ChangeSet, error) {
	resource := "vrf|" + name
	if n.GetIntent(resource) != nil {
		return NewChangeSet(n.name, "device.create-vrf"), nil
	}
	cs, err := n.op(sonic.OpCreateVRF, name, ChangeAdd,
		nil,
		func() []sonic.Entry { return createVrfConfig(name) },
		"device.delete-vrf")
	if err != nil {
		return nil, err
	}
	if err := n.writeIntent(cs, sonic.OpCreateVRF, "vrf|"+name, map[string]string{
		sonic.FieldName: name,
	}, []string{"device"}); err != nil {
		return nil, err
	}
	cs.OperationParams = map[string]string{"vrf": name}
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
		return nil, fmt.Errorf("cannot delete VRF %s — %d interface(s) still bound: %v — remove their services or VRF bindings first",
			name, len(vrfInfo.Interfaces), vrfInfo.Interfaces)
	}

	cs := NewChangeSet(n.name, "device.delete-vrf")

	cs.Deletes(createVrfConfig(name))

	// Remove BGP_GLOBALS entry written by BindIPVPN.
	cs.Deletes(deleteBgpGlobalsConfig(name))

	if err := n.deleteIntent(cs, "vrf|"+name); err != nil {
		return nil, err
	}
	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Deleted VRF %s", name)
	return cs, nil
}

// ============================================================================
// VRF Interface Binding
// ============================================================================

// AddVRFInterface binds an interface to a VRF.
// Resolves the interface name and delegates to Interface.SetVRF.
func (n *Node) AddVRFInterface(ctx context.Context, vrfName, intfName string) (*ChangeSet, error) {
	intfName = util.NormalizeInterfaceName(intfName)
	if n.GetIntent("vrf|"+vrfName) == nil {
		return nil, fmt.Errorf("VRF '%s' does not exist", vrfName)
	}
	iface, err := n.GetInterface(intfName)
	if err != nil {
		return nil, err
	}
	return iface.SetVRF(ctx, vrfName)
}

// RemoveVRFInterface removes a VRF binding from an interface.
// Resolves the interface name and delegates to Interface.SetVRF with empty VRF.
func (n *Node) RemoveVRFInterface(ctx context.Context, vrfName, intfName string) (*ChangeSet, error) {
	intfName = util.NormalizeInterfaceName(intfName)
	iface, err := n.GetInterface(intfName)
	if err != nil {
		return nil, err
	}
	return iface.SetVRF(ctx, "")
}

// ============================================================================
// IP-VPN Binding (L3VNI)
// ============================================================================

// BindIPVPN binds a VRF to an IP-VPN definition (creates L3VNI mapping and BGP EVPN config).
// Intent-idempotent: if the ipvpn intent already exists, returns empty ChangeSet.
func (n *Node) BindIPVPN(ctx context.Context, vrfName, ipvpnName string) (*ChangeSet, error) {
	resource := "ipvpn|" + vrfName
	if n.GetIntent(resource) != nil {
		return NewChangeSet(n.name, "device.bind-ipvpn"), nil
	}
	ipvpnDef, err := n.GetIPVPN(ipvpnName)
	if err != nil {
		return nil, fmt.Errorf("bind-ipvpn: %w", err)
	}
	resolved := n.Resolved()
	cs, err := n.op(sonic.OpBindIPVPN, vrfName, ChangeModify,
		func(pc *PreconditionChecker) { pc.RequireVTEPConfigured().RequireVRFExists(vrfName) },
		func() []sonic.Entry { return bindIpvpnConfig(vrfName, ipvpnDef, resolved.UnderlayASN, resolved.RouterID) },
		"device.unbind-ipvpn")
	if err != nil {
		return nil, err
	}
	intentParams := map[string]string{
		sonic.FieldVRF:       vrfName,
		sonic.FieldIPVPN:     ipvpnName,
		sonic.FieldL3VNI:     strconv.Itoa(ipvpnDef.L3VNI),
		sonic.FieldL3VNIVlan: strconv.Itoa(ipvpnDef.L3VNIVlan),
	}
	if len(ipvpnDef.RouteTargets) > 0 {
		intentParams[sonic.FieldRouteTargets] = strings.Join(ipvpnDef.RouteTargets, ",")
	}
	if err := n.writeIntent(cs, sonic.OpBindIPVPN, "ipvpn|"+vrfName, intentParams, []string{"vrf|" + vrfName}); err != nil {
		return nil, err
	}
	cs.OperationParams = map[string]string{"vrf": vrfName}
	util.WithDevice(n.name).Infof("Bound VRF %s to IP-VPN (L3VNI %d, %d route-targets)", vrfName, ipvpnDef.L3VNI, len(ipvpnDef.RouteTargets))
	return cs, nil
}

// routeTargetsFromIntent extracts route targets from the ipvpn intent record.
func (n *Node) routeTargetsFromIntent(vrfName string) []string {
	intent := n.GetIntent("ipvpn|" + vrfName)
	if intent == nil {
		return nil
	}
	return parseRouteTargets(intent.Params[sonic.FieldRouteTargets])
}

// UnbindIPVPN removes the IP-VPN binding from a VRF (removes L3VNI mapping and BGP EVPN config).
// Also removes the L3VNI transit VLAN infrastructure (VLAN, IRB, VXLAN_TUNNEL_MAP) that
// BindIPVPN created — operational symmetry requires the reverse to undo all forward effects.
//
// Refuses if any NEWTRON_INTENT references this VRF — callers must remove
// services first. RemoveService calls destroyVrfConfig directly (not UnbindIPVPN),
// so this guard only protects standalone CLI/test usage.
func (n *Node) UnbindIPVPN(ctx context.Context, vrfName string) (*ChangeSet, error) {
	if err := n.precondition("unbind-ipvpn", vrfName).Result(); err != nil {
		return nil, err
	}

	// Refuse if intent records still reference this VRF
	refs := n.IntentsByParam(sonic.FieldVRFName, vrfName)
	if len(refs) > 0 {
		var refNames []string
		for resource := range refs {
			refNames = append(refNames, resource)
		}
		return nil, fmt.Errorf("cannot unbind IP-VPN from VRF %s — %d service binding(s) still reference it: %v",
			vrfName, len(refs), refNames)
	}

	// Read L3VNI and L3VNI VLAN from the intent record — not from CONFIG_DB.
	// Per design: "NEWTRON_INTENT must contain every value needed for teardown."
	var l3vni int
	var l3vniVlan int
	if intent := n.GetIntent("ipvpn|" + vrfName); intent != nil {
		l3vni, _ = strconv.Atoi(intent.Params[sonic.FieldL3VNI])
		l3vniVlan, _ = strconv.Atoi(intent.Params[sonic.FieldL3VNIVlan])
	}

	cs := NewChangeSet(n.name, "device.unbind-ipvpn")

	// Clear VRF|vni (standard SONiC: clear L3VNI binding) — this is a modify, not delete.
	// Write "" (SONiC convention for field clear). vrfmgrd's stoul("") throws an
	// exception and skips the entry, which is correct when the VRF is about to be
	// deleted — avoids a race between explicit L3VNI unbind and VRF deletion.
	cs.Updates(clearVrfVniConfig(vrfName))

	// Delete the remaining IP-VPN entries (BGP AFs, route redistribution, EVPN RTs).
	cs.Deletes(unbindIpvpnConfig(vrfName, n.routeTargetsFromIntent(vrfName)))

	// Delete L3VNI transit VLAN infrastructure (reverse of bindIpvpnConfig).
	// BindIPVPN creates: transit VLAN, IRB binding VLAN→VRF, VXLAN_TUNNEL_MAP.
	// UnbindIPVPN must remove all three to satisfy operational symmetry.
	if l3vni > 0 && l3vniVlan > 0 {
		cs.Deletes(deleteVniMapConfig(l3vni, VLANName(l3vniVlan)))
		cs.Deletes(deleteSviBaseConfig(l3vniVlan))
		cs.Deletes(deleteVlanConfig(l3vniVlan))
	}

	if err := n.deleteIntent(cs, "ipvpn|"+vrfName); err != nil {
		return nil, err
	}
	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Unbound IP-VPN from VRF %s", vrfName)
	return cs, nil
}

// ============================================================================
// Static Routes
// ============================================================================

// AddStaticRoute adds a static route to a VRF.
func (n *Node) AddStaticRoute(ctx context.Context, vrfName, prefix, nextHop string, metric int) (*ChangeSet, error) {
	cs, err := n.op(sonic.OpAddStaticRoute, prefix, ChangeAdd,
		func(pc *PreconditionChecker) {
			pc.Check(vrfName == "" || vrfName == "default" || n.GetIntent("vrf|"+vrfName) != nil,
				"VRF must exist", fmt.Sprintf("VRF '%s' not found", vrfName))
		},
		func() []sonic.Entry { return createStaticRouteConfig(vrfName, prefix, nextHop, metric) },
		"device.remove-static-route")
	if err != nil {
		return nil, err
	}
	intentParams := map[string]string{
		sonic.FieldVRF:     vrfName,
		sonic.FieldPrefix:  prefix,
		sonic.FieldNextHop: nextHop,
	}
	if metric > 0 {
		intentParams[sonic.FieldMetric] = strconv.Itoa(metric)
	}
	// Default VRF always exists — no CreateVRF intent. Use device as parent.
	var routeParents []string
	if vrfName == "" || vrfName == "default" {
		routeParents = []string{"device"}
	} else {
		routeParents = []string{"vrf|" + vrfName}
	}
	if err := n.writeIntent(cs, sonic.OpAddStaticRoute, "route|"+vrfName+"|"+prefix, intentParams, routeParents); err != nil {
		return nil, err
	}
	cs.OperationParams = map[string]string{"vrf": vrfName, "prefix": prefix}
	util.WithDevice(n.name).Infof("Added static route %s via %s (VRF %s)", prefix, nextHop, vrfName)
	return cs, nil
}

// RemoveStaticRoute removes a static route from a VRF.
func (n *Node) RemoveStaticRoute(ctx context.Context, vrfName, prefix string) (*ChangeSet, error) {
	cs, err := n.op("remove-static-route", prefix, ChangeDelete, nil,
		func() []sonic.Entry {
			return deleteStaticRouteConfig(vrfName, prefix)
		})
	if err != nil {
		return nil, err
	}
	if err := n.deleteIntent(cs, "route|"+vrfName+"|"+prefix); err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Removed static route %s (VRF %s)", prefix, vrfName)
	return cs, nil
}

// ============================================================================
// VRF Data Types and Queries
// ============================================================================

// VRFInfo represents VRF data assembled from config_db for operations.
type VRFInfo struct {
	Name       string
	L3VNI      int
	Interfaces []string
}


// GetVRF retrieves VRF information from the intent DB.
func (n *Node) GetVRF(name string) (*VRFInfo, error) {
	if n.configDB == nil {
		return nil, util.ErrNotConnected
	}

	vrfIntent := n.GetIntent("vrf|" + name)
	if vrfIntent == nil {
		return nil, fmt.Errorf("VRF %s not found", name)
	}

	info := &VRFInfo{Name: name}

	// L3VNI from ipvpn|{name} intent.
	ipvpnIntent := n.GetIntent("ipvpn|" + name)
	if ipvpnIntent != nil {
		if l3vniStr := ipvpnIntent.Params[sonic.FieldL3VNI]; l3vniStr != "" {
			fmt.Sscanf(l3vniStr, "%d", &info.L3VNI)
		}
	}

	// Interfaces bound to this VRF — scan all intents with vrf param matching this name.
	seen := make(map[string]bool)
	for resource := range n.IntentsByParam(sonic.FieldVRF, name) {
		parts := strings.SplitN(resource, "|", 2)
		if len(parts) == 2 {
			intfName := parts[1]
			if !seen[intfName] {
				seen[intfName] = true
				info.Interfaces = append(info.Interfaces, intfName)
			}
		}
	}

	return info, nil
}

// ListVRFs returns all VRF names on this device.
func (n *Node) ListVRFs() []string {
	if n.configDB == nil {
		return nil
	}

	intents := n.IntentsByPrefix("vrf|")
	names := make([]string, 0, len(intents))
	for resource := range intents {
		parts := strings.SplitN(resource, "|", 2)
		if len(parts) == 2 {
			names = append(names, parts[1])
		}
	}
	return names
}

// ============================================================================
// Route and Neighbor Observations (VRF-scoped)
// ============================================================================

// GetRoute reads a route from APP_DB (Redis DB 0).
// Returns nil RouteEntry (not error) if the prefix is not present.
// Single-shot read — does not poll or retry.
func (n *Node) GetRoute(ctx context.Context, vrf, prefix string) (*sonic.RouteEntry, error) {
	if !n.connected {
		return nil, util.ErrNotConnected
	}
	if n.conn == nil || n.conn.AppDBClient() == nil {
		return nil, fmt.Errorf("APP_DB client not connected on %s", n.name)
	}
	return n.conn.AppDBClient().GetRoute(vrf, prefix)
}

// GetRouteASIC reads a route from ASIC_DB (Redis DB 1) by resolving the SAI
// object chain. Returns nil RouteEntry (not error) if not programmed in ASIC.
// Single-shot read — does not poll or retry.
func (n *Node) GetRouteASIC(ctx context.Context, vrf, prefix string) (*sonic.RouteEntry, error) {
	if !n.connected {
		return nil, util.ErrNotConnected
	}
	if n.conn == nil || n.conn.AsicDBClient() == nil {
		return nil, fmt.Errorf("ASIC_DB client not connected on %s", n.name)
	}
	return n.conn.AsicDBClient().GetRouteASIC(vrf, prefix, n.configDB)
}

// GetNeighbor reads a neighbor (ARP/NDP) entry from STATE_DB.
// Returns nil (not error) if the entry does not exist.
func (n *Node) GetNeighbor(ctx context.Context, iface, ip string) (*sonic.NeighEntry, error) {
	if !n.connected {
		return nil, util.ErrNotConnected
	}
	if n.conn == nil || n.conn.StateClient() == nil {
		return nil, fmt.Errorf("STATE_DB client not connected on %s", n.name)
	}
	return n.conn.StateClient().GetNeighbor(iface, ip)
}
