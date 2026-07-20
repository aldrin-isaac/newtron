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

// BindIPVPN enrolls a VRF as a member of an IP-VPN on this device — it
// materializes the VRF's L3VNI binding, the L3VNI transit infrastructure, and
// the BGP EVPN route-target config that make the VRF carry the VPN's shared
// L3VNI. An IP-VPN is the virtual network; a VRF is a member (virtual router)
// that joins it — the L3VNI and route-targets belong to the VPN, the VRF joins
// by carrying them.
//
// vrfName is the (required) on-device VRF that joins the VPN — it must already
// exist (RequireVRFExists below); BindIPVPN enrolls it, it does not create it:
//   - a service (via the composite) passes its own VRF, named after the service:
//     "Vrf_<SERVICE>" (shared) or "Vrf_<SERVICE>_<IFACE>" (interface) —
//     util.DeriveVRFName. The VPN's L3VNI lands on the service's VRF.
//   - the standalone `vrf bind-ipvpn <vrf-name> <ipvpn-name>` primitive passes
//     the operator's chosen VRF (normalized to the "Vrf_" prefix). Any existing
//     VRF may join a VPN; there is no ipvpn-derived VRF name.
//
// vrfName is recorded in the intent (not re-derivable from the ipvpn name in
// interface mode — §20) so replay and UnbindIPVPN target the same VRF. The
// intent record is keyed by the spec name (ipvpn|<name>); every CONFIG_DB write
// and the vrf| parent reference use vrfName.
//
// Intent-idempotent: if the ipvpn intent already exists, returns empty
// ChangeSet. This keeps the VPN bound once per device — a second interface-mode
// VRF cannot also carry the same L3VNI (one L3VNI per device); it is left
// unenrolled rather than colliding.
func (n *Node) BindIPVPN(ctx context.Context, ipvpnName, vrfName string) (*ChangeSet, error) {
	resource := "ipvpn|" + ipvpnName
	if n.GetIntent(resource) != nil {
		return NewChangeSet(n.name, "device.bind-ipvpn"), nil
	}
	ipvpnDef, err := n.GetIPVPN(ipvpnName)
	if err != nil {
		return nil, fmt.Errorf("bind-ipvpn: %w", err)
	}
	if vrfName == "" {
		return nil, fmt.Errorf("bind-ipvpn: vrfName is required — the VRF that joins the VPN must be named by the caller (the service composite, or the vrf-name argument of `vrf bind-ipvpn`)")
	}
	resolved := n.Resolved()
	cs, err := n.op(sonic.OpBindIPVPN, ipvpnName, ChangeModify,
		func(pc *PreconditionChecker) { pc.RequireVTEPConfigured().RequireVRFExists(vrfName) },
		func() []sonic.Entry { return bindIpvpnConfig(vrfName, ipvpnDef, resolved.UnderlayASN, resolved.RouterID) },
		"device.unbind-ipvpn")
	if err != nil {
		return nil, err
	}
	intentParams := map[string]string{
		sonic.FieldIPVPN:     ipvpnName,
		sonic.FieldVRFName:   vrfName,
		sonic.FieldL3VNI:     strconv.Itoa(ipvpnDef.L3VNI),
		sonic.FieldL3VNIVlan: strconv.Itoa(ipvpnDef.L3VNIVlan),
	}
	if len(ipvpnDef.RouteTargets) > 0 {
		intentParams[sonic.FieldRouteTargets] = strings.Join(ipvpnDef.RouteTargets, ",")
	}
	if err := n.writeIntent(cs, sonic.OpBindIPVPN, resource, intentParams, []string{"vrf|" + vrfName}); err != nil {
		return nil, err
	}
	cs.OperationParams = map[string]string{"ipvpn": ipvpnName}
	util.WithDevice(n.name).Infof("Bound IP-VPN %s → VRF %s (L3VNI %d, %d route-targets)", ipvpnName, vrfName, ipvpnDef.L3VNI, len(ipvpnDef.RouteTargets))
	return cs, nil
}

// routeTargetsFromIntent extracts route targets from the ipvpn intent record,
// keyed by the IP-VPN spec name.
func (n *Node) routeTargetsFromIntent(ipvpnName string) []string {
	intent := n.GetIntent("ipvpn|" + ipvpnName)
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
//
// The argument is the IP-VPN spec name; the on-device VRF that joined the VPN is
// read back from the intent record (BindIPVPN recorded it in FieldVRFName — the
// VRF is named after the service, not the VPN, so it is not re-derivable from the
// name). CONFIG_DB teardown and the FieldVRFName reference scan use that VRF name.
func (n *Node) UnbindIPVPN(ctx context.Context, ipvpnName string) (*ChangeSet, error) {
	var vrfName string
	if intent := n.GetIntent("ipvpn|" + ipvpnName); intent != nil {
		vrfName = intent.Params[sonic.FieldVRFName]
	}
	if vrfName == "" {
		return nil, fmt.Errorf("unbind-ipvpn: IP-VPN %q is not bound on this device (no ipvpn intent with a recorded VRF)", ipvpnName)
	}
	if err := n.precondition("unbind-ipvpn", ipvpnName).Result(); err != nil {
		return nil, err
	}

	// Refuse if any OTHER intent record still references this VRF (a service
	// binding routing in it). The ipvpn intent being unbound records its own
	// vrf_name too, so exclude it — otherwise the VPN's own binding reads as a
	// consumer and no standalone unbind could ever proceed.
	refs := n.IntentsByParam(sonic.FieldVRFName, vrfName)
	delete(refs, "ipvpn|"+ipvpnName)
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
	if intent := n.GetIntent("ipvpn|" + ipvpnName); intent != nil {
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
	cs.Deletes(unbindIpvpnConfig(vrfName, n.routeTargetsFromIntent(ipvpnName)))

	// Delete L3VNI transit VLAN infrastructure (reverse of bindIpvpnConfig).
	// BindIPVPN creates: transit VLAN, IRB binding VLAN→VRF, VXLAN_TUNNEL_MAP.
	// UnbindIPVPN must remove all three to satisfy operational symmetry.
	if l3vni > 0 && l3vniVlan > 0 {
		cs.Deletes(deleteVniMapConfig(l3vni, VLANName(l3vniVlan)))
		cs.Deletes(deleteSviBaseConfig(l3vniVlan))
		cs.Deletes(deleteVlanConfig(l3vniVlan))
	}

	if err := n.deleteIntent(cs, "ipvpn|"+ipvpnName); err != nil {
		return nil, err
	}
	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Unbound IP-VPN %s from VRF %s", ipvpnName, vrfName)
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

// UpdateStaticRoute atomically mutates an existing static route under the
// per-device intent lock — closes the forwarding black hole the
// RemoveStaticRoute + AddStaticRoute sequence exposes today (traffic
// destined to the prefix is forwarded nowhere during the window between
// DEL and ADD).
//
// Reads the existing intent record (keyed by vrf + prefix), validates,
// and emits a single ChangeSet that deletes the prior STATIC_ROUTE entry
// and writes the new one. The intent record is replaced via writeIntent's
// idempotent path (DEL+HSET — #228 fix) so dropped params don't ghost.
//
// Per §47 (CONFIG_DB Composite Key Is the Identity) the key
// (vrf, prefix) is immutable. Issue #227.
func (n *Node) UpdateStaticRoute(ctx context.Context, vrfName, prefix, nextHop string, metric int) (*ChangeSet, error) {
	resource := "route|" + vrfName + "|" + prefix
	existing := n.GetIntent(resource)
	if existing == nil {
		return nil, fmt.Errorf("static route %s not found in VRF %s", prefix, vrfName)
	}

	cs, err := n.op(sonic.OpUpdateStaticRoute, prefix, ChangeAdd,
		func(pc *PreconditionChecker) {
			pc.Check(vrfName == "" || vrfName == "default" || n.GetIntent("vrf|"+vrfName) != nil,
				"VRF must exist", fmt.Sprintf("VRF '%s' not found", vrfName))
		},
		func() []sonic.Entry { return nil })
	if err != nil {
		return nil, err
	}

	// In-place replace of the same (vrf, prefix) key — the prefix is the row's
	// identity (§47), and the update is delivered without ever DELeting the key
	// so fpmsyncd never sees a FIB gap (§48).
	cs.Replace(n,
		deleteStaticRouteConfig(vrfName, prefix),
		createStaticRouteConfig(vrfName, prefix, nextHop, metric))

	intentParams := map[string]string{
		sonic.FieldVRF:     vrfName,
		sonic.FieldPrefix:  prefix,
		sonic.FieldNextHop: nextHop,
	}
	if metric > 0 {
		intentParams[sonic.FieldMetric] = strconv.Itoa(metric)
	}
	var routeParents []string
	if vrfName == "" || vrfName == "default" {
		routeParents = []string{"device"}
	} else {
		routeParents = []string{"vrf|" + vrfName}
	}
	if err := n.writeIntent(cs, sonic.OpAddStaticRoute, resource, intentParams, routeParents); err != nil {
		return nil, err
	}

	cs.OperationParams = map[string]string{"vrf": vrfName, "prefix": prefix}
	util.WithDevice(n.name).Infof("Updated static route %s via %s (VRF %s)", prefix, nextHop, vrfName)
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
