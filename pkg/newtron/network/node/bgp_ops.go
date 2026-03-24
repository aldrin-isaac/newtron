package node

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// BGP Config Functions (pure, no Node state)
// ============================================================================

// BGPNeighborOpts controls optional aspects of BGP neighbor configuration.
type BGPNeighborOpts struct {
	Description      string
	EBGPMultihop     bool   // set ebgp_multihop (value "true" for loopback, TTL string for explicit)
	MultihopTTL      string // explicit TTL value (e.g., "255"); if empty and EBGPMultihop=true, uses "true"
	ActivateIPv4     bool   // activate ipv4_unicast AF (default true for direct peers)
	ActivateEVPN     bool   // activate l2vpn_evpn AF
	VRF              string // VRF name (default "default")
	RRClient         bool   // route-reflector-client on ipv4_unicast AF
	NextHopSelf      bool   // next-hop-self on ipv4_unicast AF
	NextHopUnchanged bool   // nexthop_unchanged on l2vpn_evpn AF
	ActivateIPv6     bool   // activate ipv6_unicast AF
	RRClientIPv6     bool   // rrclient on ipv6_unicast AF
	NextHopSelfIPv6  bool   // nhself on ipv6_unicast AF
	RRClientEVPN     bool   // rrclient on l2vpn_evpn AF
	PeerGroup        string // peer group name (for service-level BGP neighbors, per Principle 36)
}

// CreateBGPNeighborConfig returns sonic.Entry for a BGP_NEIGHBOR + BGP_NEIGHBOR_AF.
func CreateBGPNeighborConfig(neighborIP string, asn int, localAddr string, opts BGPNeighborOpts) []sonic.Entry {
	var entries []sonic.Entry

	vrf := opts.VRF
	if vrf == "" {
		vrf = "default"
	}

	fields := map[string]string{
		"asn":          fmt.Sprintf("%d", asn),
		"admin_status": "up",
		"local_addr":   localAddr,
	}
	if opts.Description != "" {
		fields["name"] = opts.Description
	}
	if opts.EBGPMultihop {
		if opts.MultihopTTL != "" {
			fields["ebgp_multihop"] = opts.MultihopTTL
		} else {
			fields["ebgp_multihop"] = "true"
		}
	}
	if opts.PeerGroup != "" {
		fields["peer_group_name"] = opts.PeerGroup
	}

	entries = append(entries, sonic.Entry{
		Table:  "BGP_NEIGHBOR",
		Key:    fmt.Sprintf("%s|%s", vrf, neighborIP),
		Fields: fields,
	})

	// Activate IPv4 unicast (default for most peers)
	if opts.ActivateIPv4 {
		afFields := map[string]string{"admin_status": "true"}
		if opts.RRClient {
			afFields["rrclient"] = "true"
		}
		if opts.NextHopSelf {
			afFields["nhself"] = "true"
		}
		entries = append(entries, sonic.Entry{
			Table:  "BGP_NEIGHBOR_AF",
			Key:    fmt.Sprintf("%s|%s|ipv4_unicast", vrf, neighborIP),
			Fields: afFields,
		})
	}

	// Activate IPv6 unicast
	if opts.ActivateIPv6 {
		afFields := map[string]string{"admin_status": "true"}
		if opts.RRClientIPv6 {
			afFields["rrclient"] = "true"
		}
		if opts.NextHopSelfIPv6 {
			afFields["nhself"] = "true"
		}
		entries = append(entries, sonic.Entry{
			Table:  "BGP_NEIGHBOR_AF",
			Key:    fmt.Sprintf("%s|%s|ipv6_unicast", vrf, neighborIP),
			Fields: afFields,
		})
	}

	// Activate L2VPN EVPN
	if opts.ActivateEVPN {
		evpnFields := map[string]string{"admin_status": "true"}
		if opts.NextHopUnchanged {
			evpnFields["nexthop_unchanged"] = "true"
		}
		if opts.RRClientEVPN {
			evpnFields["rrclient"] = "true"
		}
		entries = append(entries, sonic.Entry{
			Table:  "BGP_NEIGHBOR_AF",
			Key:    fmt.Sprintf("%s|%s|l2vpn_evpn", vrf, neighborIP),
			Fields: evpnFields,
		})
	}

	return entries
}

// DeleteBGPNeighborConfig returns sonic.Entry for deleting a BGP neighbor
// and all its address-family entries.
func DeleteBGPNeighborConfig(vrf, neighborIP string) []sonic.Entry {
	if vrf == "" {
		vrf = "default"
	}

	var entries []sonic.Entry

	// Remove address-family entries first
	for _, af := range []string{"ipv4_unicast", "ipv6_unicast", "l2vpn_evpn"} {
		entries = append(entries, sonic.Entry{
			Table: "BGP_NEIGHBOR_AF",
			Key:   BGPNeighborAFKey(vrf, neighborIP, af),
		})
	}

	// Remove neighbor entry
	entries = append(entries, sonic.Entry{
		Table: "BGP_NEIGHBOR",
		Key:   fmt.Sprintf("%s|%s", vrf, neighborIP),
	})

	return entries
}

// CreateBGPGlobalsConfig returns sonic.Entry for BGP_GLOBALS.
func CreateBGPGlobalsConfig(vrf string, asn int, routerID string, extra map[string]string) []sonic.Entry {
	fields := map[string]string{
		"local_asn": fmt.Sprintf("%d", asn),
		"router_id": routerID,
	}
	for k, v := range extra {
		fields[k] = v
	}
	return []sonic.Entry{
		{Table: "BGP_GLOBALS", Key: vrf, Fields: fields},
	}
}

// BGPGlobalsAFKey returns the CONFIG_DB key for a BGP_GLOBALS_AF entry.
func BGPGlobalsAFKey(vrf, af string) string {
	return fmt.Sprintf("%s|%s", vrf, af)
}

// CreateBGPGlobalsAFConfig returns sonic.Entry for BGP_GLOBALS_AF.
func CreateBGPGlobalsAFConfig(vrf, af string, fields map[string]string) []sonic.Entry {
	if fields == nil {
		fields = map[string]string{}
	}
	return []sonic.Entry{
		{Table: "BGP_GLOBALS_AF", Key: BGPGlobalsAFKey(vrf, af), Fields: fields},
	}
}

// revertRedistributionConfig resets BGP_GLOBALS_AF redistribution fields to false.
// This is the reverse of the redistribution override in addBGPRoutePolicies.
func revertRedistributionConfig(vrfKey string) []sonic.Entry {
	return CreateBGPGlobalsAFConfig(vrfKey, "ipv4_unicast", map[string]string{
		"redistribute_connected": "false",
		"redistribute_static":    "false",
	})
}

// RouteRedistributeKey returns the CONFIG_DB key for a ROUTE_REDISTRIBUTE entry.
func RouteRedistributeKey(vrf, protocol, af string) string {
	return fmt.Sprintf("%s|%s|bgp|%s", vrf, protocol, af)
}

// CreateRouteRedistributeConfig returns sonic.Entry for ROUTE_REDISTRIBUTE.
func CreateRouteRedistributeConfig(vrf, protocol, af string) []sonic.Entry {
	return []sonic.Entry{
		{Table: "ROUTE_REDISTRIBUTE", Key: RouteRedistributeKey(vrf, protocol, af), Fields: map[string]string{}},
	}
}

// BGPNeighborAFKey returns the CONFIG_DB key for a BGP_NEIGHBOR_AF entry.
// Used by callers that need to modify an existing AF entry (e.g. adding route-maps).
func BGPNeighborAFKey(vrf, neighborIP, af string) string {
	return fmt.Sprintf("%s|%s|%s", vrf, neighborIP, af)
}

// deleteBgpGlobalsConfig returns the delete entry for a BGP_GLOBALS entry.
func deleteBgpGlobalsConfig(vrf string) []sonic.Entry {
	return []sonic.Entry{{Table: "BGP_GLOBALS", Key: vrf}}
}

// deleteBgpGlobalsAFConfig returns the delete entry for a BGP_GLOBALS_AF entry.
func deleteBgpGlobalsAFConfig(vrf, af string) []sonic.Entry {
	return []sonic.Entry{{Table: "BGP_GLOBALS_AF", Key: BGPGlobalsAFKey(vrf, af)}}
}

// deleteRouteRedistributeConfig returns the delete entry for a ROUTE_REDISTRIBUTE entry.
func deleteRouteRedistributeConfig(vrf, protocol, af string) []sonic.Entry {
	return []sonic.Entry{{Table: "ROUTE_REDISTRIBUTE", Key: RouteRedistributeKey(vrf, protocol, af)}}
}

// updateDeviceMetadataConfig returns a DEVICE_METADATA entry for updating localhost fields.
func updateDeviceMetadataConfig(fields map[string]string) sonic.Entry {
	return sonic.Entry{Table: "DEVICE_METADATA", Key: "localhost", Fields: fields}
}

// createBgpNeighborAFConfig returns a BGP_NEIGHBOR_AF entry for a specific address family.
func createBgpNeighborAFConfig(vrf, neighborIP, af string, fields map[string]string) sonic.Entry {
	return sonic.Entry{Table: "BGP_NEIGHBOR_AF", Key: BGPNeighborAFKey(vrf, neighborIP, af), Fields: fields}
}

// ============================================================================
// BGP Peer Group Config Functions (pure, no Node state)
// ============================================================================

// BGPPeerGroupKey returns the CONFIG_DB key for a BGP_PEER_GROUP entry.
// Format: vrf|peer_group_name (e.g., "default|TRANSIT")
func BGPPeerGroupKey(vrf, name string) string {
	if vrf == "" {
		vrf = "default"
	}
	return fmt.Sprintf("%s|%s", vrf, name)
}

// BGPPeerGroupAFKey returns the CONFIG_DB key for a BGP_PEER_GROUP_AF entry.
// Format: vrf|peer_group_name|af (e.g., "default|TRANSIT|ipv4_unicast")
func BGPPeerGroupAFKey(vrf, name, af string) string {
	if vrf == "" {
		vrf = "default"
	}
	return fmt.Sprintf("%s|%s|%s", vrf, name, af)
}

// CreateBGPPeerGroupConfig returns entries for a BGP_PEER_GROUP + BGP_PEER_GROUP_AF.
// Peer groups are service-named templates: shared attributes live here, not on neighbors.
// Per DESIGN_PRINCIPLES_NEWTRON.md §17 (BGP Peer Groups).
func CreateBGPPeerGroupConfig(vrf, name string, afFields map[string]string) []sonic.Entry {
	if vrf == "" {
		vrf = "default"
	}
	entries := []sonic.Entry{
		{Table: "BGP_PEER_GROUP", Key: BGPPeerGroupKey(vrf, name), Fields: map[string]string{
			"admin_status": "up",
		}},
		{Table: "BGP_PEER_GROUP_AF", Key: BGPPeerGroupAFKey(vrf, name, "ipv4_unicast"), Fields: util.MergeMaps(
			map[string]string{"admin_status": "true"},
			afFields,
		)},
	}
	return entries
}

// UpdateBGPPeerGroupAF returns an update entry for a peer group's address-family fields.
// Used when the peer group already exists but AF attributes (e.g., route maps) need updating.
func UpdateBGPPeerGroupAF(vrf, name string, afFields map[string]string) sonic.Entry {
	return sonic.Entry{
		Table:  "BGP_PEER_GROUP_AF",
		Key:    BGPPeerGroupAFKey(vrf, name, "ipv4_unicast"),
		Fields: afFields,
	}
}

// DeleteBGPPeerGroupConfig returns delete entries for a peer group and its AF entry.
func DeleteBGPPeerGroupConfig(vrf, name string) []sonic.Entry {
	if vrf == "" {
		vrf = "default"
	}
	return []sonic.Entry{
		{Table: "BGP_PEER_GROUP_AF", Key: BGPPeerGroupAFKey(vrf, name, "ipv4_unicast")},
		{Table: "BGP_PEER_GROUP", Key: BGPPeerGroupKey(vrf, name)},
	}
}

// BGPConfigured reports whether BGP_GLOBALS|default exists (a working frrcfgd instance).
func (n *Node) BGPConfigured() bool { return n.configDB.BGPConfigured() }

// RemoveLegacyBGPEntries deletes bgpcfgd-format BGP_NEIGHBOR entries from
// CONFIG_DB. These use the key format "BGP_NEIGHBOR|<ip>" (no VRF prefix),
// which frrcfgd ignores. Community sonic-vs ships with 32 such entries in
// its factory config_db.json. Called by newtron init when switching to frrcfgd.
func (n *Node) RemoveLegacyBGPEntries(ctx context.Context) (int, error) {
	client := n.ConfigDBClient()
	if client == nil {
		return 0, fmt.Errorf("not connected")
	}

	count := 0
	for key := range n.configDB.BGPNeighbor {
		// frrcfgd keys have VRF prefix: "default|10.0.0.1"
		// bgpcfgd keys are bare IPs: "10.0.0.1"
		if !strings.Contains(key, "|") {
			if err := client.Delete("BGP_NEIGHBOR", key); err != nil {
				return count, fmt.Errorf("deleting BGP_NEIGHBOR|%s: %w", key, err)
			}
			delete(n.configDB.BGPNeighbor, key)
			count++
		}
	}

	if count > 0 {
		util.WithDevice(n.name).Infof("Removed %d legacy bgpcfgd BGP_NEIGHBOR entries", count)
	}
	return count, nil
}

// BGPNeighborExists checks if a BGP neighbor exists.
// Looks up using the SONiC key format: "default|<IP>" (vrf|neighborIP).
func (n *Node) BGPNeighborExists(neighborIP string) bool {
	return n.configDB.HasBGPNeighbor(fmt.Sprintf("default|%s", neighborIP))
}

// ============================================================================
// BGP Global Configuration
// ============================================================================

// ConfigureBGP writes the BGP_GLOBALS, BGP_GLOBALS_AF, and ROUTE_REDISTRIBUTE
// entries needed to bring up a BGP instance. Values are read entirely from the
// node's resolved profile — no YAML params needed.
//
// This is a lightweight primitive that only sets up the BGP instance itself,
// without adding any peers. Use bgp-add-peer / setup-evpn for peers.
func (n *Node) ConfigureBGP(ctx context.Context) (*ChangeSet, error) {
	if err := n.precondition("configure-bgp", "bgp").Result(); err != nil {
		return nil, err
	}

	resolved := n.Resolved()
	if resolved.UnderlayASN == 0 {
		return nil, fmt.Errorf("underlay_asn not set in device profile")
	}
	if resolved.RouterID == "" {
		return nil, fmt.Errorf("router_id not set in device profile")
	}

	asnStr := fmt.Sprintf("%d", resolved.UnderlayASN)
	cs := NewChangeSet(n.name, "device.configure-bgp")
	cs.ReverseOp = "device.remove-bgp-globals"

	// bgpcfgd requires DEVICE_METADATA["localhost"]["bgp_asn"] and
	// DEVICE_METADATA["localhost"]["type"] as explicit dependencies.
	// Without "type", bgpcfgd silently defers all BGP_NEIGHBOR entries.
	//
	// frrcfgd flags (docker_routing_config_mode, frr_mgmt_framework_config)
	// are NOT written here — they are infrastructure init set by:
	//   - newtron init (manual)
	//   - topology provisioner (GenerateDeviceComposite)
	//   - newtlab boot patch (lab VMs)
	// Connect() enforces frrcfgd as a precondition before any operation.
	e := updateDeviceMetadataConfig(map[string]string{
		"bgp_asn": asnStr,
		"type":    "LeafRouter",
	})
	cs.Update(e.Table, e.Key, e.Fields)

	// BGP global instance + address-family + redistribution via config functions
	cs.Updates(CreateBGPGlobalsConfig("default", resolved.UnderlayASN, resolved.RouterID, map[string]string{
		"ebgp_requires_policy": "false",
		"suppress_fib_pending": "false",
		"log_neighbor_changes": "true",
	}))
	cs.Updates(CreateBGPGlobalsAFConfig("default", "ipv4_unicast", nil))
	cs.Updates(CreateRouteRedistributeConfig("default", "connected", "ipv4"))

	n.applyShadow(cs)
	util.WithDevice(n.name).Infof("Configured BGP (AS %d, router-id %s)", resolved.UnderlayASN, resolved.RouterID)
	return cs, nil
}

// ============================================================================
// BGP Neighbor Operations
// ============================================================================

// AddBGPMultihopPeer adds an indirect BGP neighbor using loopback as update-source.
// This is used for multi-hop eBGP sessions (EVPN overlay peers).
//
// For DIRECT BGP peers that use a link IP as the update-source (typical
// eBGP on point-to-point links), use Interface.AddBGPPeer() instead.
func (n *Node) AddBGPMultihopPeer(ctx context.Context, neighborIP string, asn int, description string, evpn bool) (*ChangeSet, error) {
	if err := n.precondition("add-bgp-multihop-peer", neighborIP).Result(); err != nil {
		return nil, err
	}

	if !util.IsValidIPv4(neighborIP) {
		return nil, fmt.Errorf("invalid neighbor IP: %s", neighborIP)
	}
	if n.BGPNeighborExists(neighborIP) {
		return nil, fmt.Errorf("BGP peer %s already exists", neighborIP)
	}

	isEBGP := asn != n.resolved.UnderlayASN
	config := CreateBGPNeighborConfig(neighborIP, asn, n.resolved.LoopbackIP, BGPNeighborOpts{
		Description:      description,
		EBGPMultihop:     isEBGP,
		ActivateIPv4:     true,
		ActivateEVPN:     evpn,
		NextHopUnchanged: evpn && isEBGP,
	})
	cs := buildChangeSet(n.name, "device.add-bgp-multihop-peer", config, ChangeAdd)
	n.applyShadow(cs)

	util.WithDevice(n.name).Infof("Adding multihop BGP peer %s (AS %d, update-source: %s)",
		neighborIP, asn, n.resolved.LoopbackIP)
	return cs, nil
}

// RemoveBGPPeer removes a BGP peer from the device.
// This works for both direct (interface-level) and indirect (loopback-level) peers.
func (n *Node) RemoveBGPPeer(ctx context.Context, neighborIP string) (*ChangeSet, error) {
	cs, err := n.op("remove-bgp-peer", neighborIP, ChangeDelete,
		func(pc *PreconditionChecker) {
			pc.Check(n.BGPNeighborExists(neighborIP), "BGP peer must exist",
				fmt.Sprintf("BGP peer %s not found", neighborIP))
		},
		func() []sonic.Entry { return DeleteBGPNeighborConfig("default", neighborIP) })
	if err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Removing BGP peer %s", neighborIP)
	return cs, nil
}

// RemoveBGPGlobals removes the default BGP instance, reversing ConfigureBGP.
// Deletes ROUTE_REDISTRIBUTE, BGP_GLOBALS_AF (ipv4_unicast), BGP_GLOBALS,
// and clears bgp_asn from DEVICE_METADATA.
//
// Does NOT touch l2vpn_evpn AF or EVPN neighbors (owned by TeardownVTEP).
// Does NOT touch per-VRF BGP_GLOBALS (owned by UnbindIPVPN/DeleteVRF).
func (n *Node) RemoveBGPGlobals(ctx context.Context) (*ChangeSet, error) {
	if err := n.precondition("remove-bgp-globals", "bgp").Result(); err != nil {
		return nil, err
	}

	if !n.BGPConfigured() {
		return nil, fmt.Errorf("BGP is not configured on %s", n.name)
	}

	cs := NewChangeSet(n.name, "device.remove-bgp-globals")

	// Reverse order of ConfigureBGP — use config functions for key consistency
	cs.Deletes(CreateRouteRedistributeConfig("default", "connected", "ipv4"))
	cs.Deletes(CreateBGPGlobalsAFConfig("default", "ipv4_unicast", nil))
	cs.Deletes(CreateBGPGlobalsConfig("default", 0, "", nil))

	// Clear bgp_asn from DEVICE_METADATA (set to empty)
	e := updateDeviceMetadataConfig(map[string]string{"bgp_asn": ""})
	cs.Update(e.Table, e.Key, e.Fields)

	util.WithDevice(n.name).Infof("Removed BGP globals")
	return cs, nil
}
