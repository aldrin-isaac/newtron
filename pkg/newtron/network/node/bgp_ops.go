package node

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/util"
)

// BGPConfigured reports whether BGP is configured on this device.
// Checks the device intent — BGP globals are created by SetupDevice.
func (n *Node) BGPConfigured() bool { return n.GetIntent("device") != nil }

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
// Checks overlay peers (evpn-peer|IP intents) and underlay peers
// (interface|*|bgp-peer intents with matching neighbor_ip).
func (n *Node) BGPNeighborExists(neighborIP string) bool {
	// Overlay peers: evpn-peer|{ip}
	if n.GetIntent("evpn-peer|"+neighborIP) != nil {
		return true
	}
	// Underlay peers: interface|{name}|bgp-peer with neighbor_ip param
	for _, intent := range n.IntentsByPrefix("interface|") {
		if intent.Operation == "add-bgp-peer" && intent.Params["neighbor_ip"] == neighborIP {
			return true
		}
	}
	return false
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
	//   - newtlab boot patch (lab VMs)
	// ConnectTransport() enforces frrcfgd as a precondition before any operation.
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

	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Configured BGP (AS %d, router-id %s)", resolved.UnderlayASN, resolved.RouterID)
	return cs, nil
}

// ============================================================================
// BGP Neighbor Operations
// ============================================================================

// AddBGPEVPNPeer adds an indirect BGP neighbor using loopback as update-source.
// This is used for multi-hop eBGP sessions (EVPN overlay peers).
//
// For DIRECT BGP peers that use a link IP as the update-source (typical
// eBGP on point-to-point links), use Interface.AddBGPPeer() instead.
func (n *Node) AddBGPEVPNPeer(ctx context.Context, neighborIP string, asn int, description string, evpn bool) (*ChangeSet, error) {
	if err := n.precondition(sonic.OpAddBGPEVPNPeer, neighborIP).Result(); err != nil {
		return nil, err
	}

	if !util.IsValidIPv4(neighborIP) {
		return nil, fmt.Errorf("invalid neighbor IP: %s", neighborIP)
	}
	if n.BGPNeighborExists(neighborIP) {
		return nil, fmt.Errorf("BGP peer %s already exists", neighborIP)
	}

	// EVPN peer group required (created by ConfigureBGPOverlay, called by SetupDevice with source_ip).
	// Check device intent for source_ip — if present, SetupDevice ran ConfigureBGPOverlay which created the peer group.
	deviceIntent := n.GetIntent("device")
	if deviceIntent == nil || deviceIntent.Params["source_ip"] == "" {
		return nil, fmt.Errorf("EVPN peer group does not exist; run setup-device with source_ip first")
	}
	config := CreateBGPNeighborConfig(neighborIP, asn, "", BGPNeighborOpts{
		Description:  description,
		PeerGroup:    "EVPN",
		ActivateEVPN: evpn,
	})
	cs := buildChangeSet(n.name, "device."+sonic.OpAddBGPEVPNPeer, config, ChangeAdd)
	intentParams := map[string]string{
		sonic.FieldNeighborIP:  neighborIP,
		sonic.FieldASN:         strconv.Itoa(asn),
		sonic.FieldDescription: description,
	}
	if evpn {
		intentParams[sonic.FieldEVPN] = "true"
	}
	if err := n.writeIntent(cs, sonic.OpAddBGPEVPNPeer, "evpn-peer|"+neighborIP, intentParams, []string{"device"}); err != nil {
		return nil, err
	}
	if err := n.render(cs); err != nil {
		return nil, err
	}

	util.WithDevice(n.name).Infof("Adding EVPN BGP peer %s (AS %d, update-source: %s)",
		neighborIP, asn, n.resolved.LoopbackIP)
	return cs, nil
}

// RemoveBGPEVPNPeer removes an EVPN BGP peer from the device.
// Also used internally by Interface.RemoveBGPPeer for direct peer removal
// (the CONFIG_DB operation — deleting BGP_NEIGHBOR entries — is identical).
func (n *Node) RemoveBGPEVPNPeer(ctx context.Context, neighborIP string) (*ChangeSet, error) {
	cs, err := n.op("remove-bgp-evpn-peer", neighborIP, ChangeDelete,
		func(pc *PreconditionChecker) {
			pc.Check(n.BGPNeighborExists(neighborIP), "BGP peer must exist",
				fmt.Sprintf("BGP peer %s not found", neighborIP))
		},
		func() []sonic.Entry { return DeleteBGPNeighborConfig("default", neighborIP) })
	if err != nil {
		return nil, err
	}
	if err := n.deleteIntent(cs, "evpn-peer|"+neighborIP); err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Removing EVPN BGP peer %s", neighborIP)
	return cs, nil
}

// RemoveBGPGlobals removes the default BGP instance, reversing ConfigureBGP.
// Deletes ROUTE_REDISTRIBUTE, BGP_GLOBALS_AF (ipv4_unicast), BGP_GLOBALS,
// and clears bgp_asn from DEVICE_METADATA.
//
// Does NOT touch l2vpn_evpn AF or EVPN neighbors (owned by TeardownBGPOverlay).
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

	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Removed BGP globals")
	return cs, nil
}

// ============================================================================
// BGP Overlay (EVPN control plane)
// ============================================================================

// ConfigureBGPOverlay sets up the BGP EVPN control plane: l2vpn_evpn address-family,
// EVPN peer group, and overlay BGP neighbors from the resolved profile.
// Called by SetupDevice after SetupVXLAN to separate data-plane (VXLAN) from
// control-plane (BGP EVPN) concerns.
func (n *Node) ConfigureBGPOverlay(ctx context.Context, sourceIP string) (*ChangeSet, error) {
	if err := n.precondition("configure-bgp-overlay", "bgp").Result(); err != nil {
		return nil, err
	}

	resolved := n.Resolved()
	if sourceIP == "" {
		sourceIP = resolved.VTEPSourceIP
	}
	if sourceIP == "" {
		return nil, fmt.Errorf("no VTEP source IP available (specify sourceIP or set loopback_ip in profile)")
	}

	cs := NewChangeSet(n.name, "device.configure-bgp-overlay")
	cs.ReverseOp = "device.teardown-bgp-overlay"

	// EVPN address-family on the default VRF's BGP instance.
	// BGP_GLOBALS|default is already created by ConfigureBGP (called earlier
	// in SetupDevice) with the correct extra fields (ebgp_requires_policy, etc.).
	// We only add the l2vpn_evpn AF here — NOT a second BGP_GLOBALS|default
	// write, which would overwrite ConfigureBGP's fields (hydrator replaces
	// the full struct, so a write with nil extra strips the fields).
	cs.Adds(CreateBGPGlobalsAFConfig("default", "l2vpn_evpn", map[string]string{
		"advertise-all-vni": "true",
	}))

	// Create EVPN peer group — shared attributes for overlay sessions.
	// eBGP determined by comparing local ASN against peer ASNs.
	// Default to eBGP when no peers are configured (safe default for all-eBGP design).
	isEBGP := true
	for _, rrIP := range resolved.BGPNeighbors {
		if rrIP == resolved.LoopbackIP {
			continue
		}
		if resolved.BGPNeighborASNs[rrIP] == resolved.UnderlayASN {
			isEBGP = false // at least one iBGP peer — don't set ebgp_multihop
			break
		}
	}
	// Peer group creation is unconditional — render() handles upserts safely.
	// SetupDevice guards cross-execution idempotency via GetIntent("device").
	cs.Adds(CreateEVPNPeerGroupConfig("default", resolved.LoopbackIP, isEBGP))

	// Add overlay peers from profile
	for _, rrIP := range resolved.BGPNeighbors {
		if rrIP == resolved.LoopbackIP {
			continue
		}

		// Use peer's ASN for eBGP overlay (all-eBGP design; docs/rca/026-bgp-all-ebgp-design.md).
		peerASN := resolved.BGPNeighborASNs[rrIP]
		if peerASN == 0 {
			return nil, fmt.Errorf("no ASN found for EVPN peer %s", rrIP)
		}

		if n.BGPNeighborExists(rrIP) {
			// Neighbor exists (e.g., provisioner created it without EVPN AF).
			// Ensure the l2vpn_evpn AF entry is present — Add is
			// idempotent so this is safe even if it already exists.
			cs.Adds([]sonic.Entry{createBgpNeighborAFConfig("default", rrIP, "l2vpn_evpn", map[string]string{"admin_status": "true"})})
		} else {
			// Neighbor references EVPN peer group — shared attrs
			// (local_addr, ebgp_multihop, nexthop_unchanged) inherited.
			cs.Adds(CreateBGPNeighborConfig(rrIP, peerASN, "", BGPNeighborOpts{
				PeerGroup:    "EVPN",
				ActivateEVPN: true,
			}))
		}
	}

	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Configured BGP overlay (source IP %s, %d peers)", sourceIP, len(resolved.BGPNeighbors))
	return cs, nil
}

// TeardownBGPOverlay removes the BGP EVPN control plane: overlay neighbors,
// EVPN peer group, and l2vpn_evpn address-family.
// This is the reverse of ConfigureBGPOverlay.
func (n *Node) TeardownBGPOverlay(ctx context.Context) (*ChangeSet, error) {
	if err := n.precondition("teardown-bgp-overlay", "bgp").Result(); err != nil {
		return nil, err
	}

	resolved := n.Resolved()
	cs := NewChangeSet(n.name, "device.teardown-bgp-overlay")

	// Remove BGP EVPN overlay neighbors and their address-family entries
	for _, rrIP := range resolved.BGPNeighbors {
		if rrIP == resolved.LoopbackIP {
			continue
		}
		cs.Deletes(DeleteBGPNeighborConfig("default", rrIP))
	}

	// Remove EVPN peer group (after all neighbors that reference it)
	cs.Deletes(DeleteEVPNPeerGroupConfig("default"))

	// Remove L2VPN EVPN address-family
	cs.Deletes(CreateBGPGlobalsAFConfig("default", "l2vpn_evpn", nil))

	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Tore down BGP overlay (%d neighbors removed)", len(resolved.BGPNeighbors))
	return cs, nil
}

// ============================================================================
// Route Reflector Configuration
// ============================================================================

// RouteReflectorPeer describes a BGP peer for route reflector configuration.
type RouteReflectorPeer struct {
	IP  string // Loopback IP of the peer
	ASN int    // Autonomous system number
}

// RouteReflectorOpts holds configuration for ConfigureRouteReflector.
type RouteReflectorOpts struct {
	ClusterID string               // RR cluster ID
	LocalASN  int                  // RR's own ASN
	RouterID  string               // RR's router ID
	LocalAddr string               // Local address for eBGP multihop (loopback IP)
	Clients   []RouteReflectorPeer // RR clients (IPv4 unicast, RR-client)
	Peers     []RouteReflectorPeer // RR-to-RR peers (IPv4+IPv6+EVPN, RR-client)
}

// ConfigureRouteReflector configures this node as a BGP route reflector.
// Sets BGP globals with RR-specific settings, creates eBGP neighbors for
// all clients (IPv4 unicast) and peers (IPv4+IPv6+EVPN), and enables
// IPv6 route redistribution.
func (n *Node) ConfigureRouteReflector(ctx context.Context, opts RouteReflectorOpts) (*ChangeSet, error) {
	if err := n.precondition("configure-route-reflector", "bgp").Result(); err != nil {
		return nil, err
	}
	if opts.LocalASN == 0 {
		return nil, fmt.Errorf("route reflector requires local ASN")
	}
	if opts.ClusterID == "" {
		return nil, fmt.Errorf("route reflector requires cluster ID")
	}

	cs := NewChangeSet(n.name, "device.configure-route-reflector")

	// BGP globals with RR-specific settings
	cs.Adds(CreateBGPGlobalsConfig("default", opts.LocalASN, opts.RouterID, map[string]string{
		"rr_cluster_id":         opts.ClusterID,
		"load_balance_mp_relax": "true",
		"ebgp_requires_policy":  "false",
		"suppress_fib_pending":  "false",
		"log_neighbor_changes":  "true",
	}))

	// RR clients: IPv4 unicast only, RR-client flag
	for _, client := range opts.Clients {
		cs.Adds(CreateBGPNeighborConfig(client.IP, client.ASN, opts.LocalAddr, BGPNeighborOpts{
			EBGPMultihop: true,
			ActivateIPv4: true,
			RRClient:     true,
			NextHopSelf:  true,
		}))
	}

	// RR-to-RR peers: full AF set (IPv4+IPv6+EVPN), RR-client
	for _, peer := range opts.Peers {
		cs.Adds(CreateBGPNeighborConfig(peer.IP, peer.ASN, opts.LocalAddr, BGPNeighborOpts{
			EBGPMultihop:    true,
			ActivateIPv4:    true,
			RRClient:        true,
			NextHopSelf:     true,
			ActivateIPv6:    true,
			RRClientIPv6:    true,
			NextHopSelfIPv6: true,
			ActivateEVPN:    true,
			RRClientEVPN:    true,
		}))
	}

	// IPv6 route redistribution for RR
	cs.Adds(CreateRouteRedistributeConfig("default", "connected", "ipv6"))

	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.name).Infof("Configured route reflector (cluster=%s, %d clients, %d peers)",
		opts.ClusterID, len(opts.Clients), len(opts.Peers))
	return cs, nil
}
