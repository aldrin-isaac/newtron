// topology.go implements topology-driven provisioning from topology.json specs.
//
// ProvisionDevice generates a complete CONFIG_DB offline and delivers it
// atomically via node.CompositeOverwrite (no device interrogation needed).
package network

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/network/node"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// TopologyProvisioner generates and delivers configuration from topology specs.
type TopologyProvisioner struct {
	network *Network
}

// NewTopologyProvisioner creates a provisioner from a Network with a loaded topology.
func NewTopologyProvisioner(network *Network) (*TopologyProvisioner, error) {
	if !network.HasTopology() {
		return nil, fmt.Errorf("no topology loaded — ensure topology.json exists in spec directory")
	}
	return &TopologyProvisioner{network: network}, nil
}

// GenerateDeviceComposite generates a node.CompositeConfig for a device without delivering it.
// Useful for inspection, serialization, or deferred delivery.
// Returns error for host devices (no SONiC CONFIG_DB).
func (tp *TopologyProvisioner) GenerateDeviceComposite(deviceName string) (*node.CompositeConfig, error) {
	if tp.network.IsHostDevice(deviceName) {
		return nil, fmt.Errorf("device '%s' is a host — cannot generate SONiC composite", deviceName)
	}
	topoDev, _ := tp.network.GetTopologyDevice(deviceName)

	// Load and resolve device profile
	profile, err := tp.network.loadProfile(deviceName)
	if err != nil {
		return nil, fmt.Errorf("loading profile: %w", err)
	}
	resolved, err := tp.network.resolveProfile(deviceName, profile)
	if err != nil {
		return nil, fmt.Errorf("resolving profile: %w", err)
	}

	// Build per-device ResolvedSpecs for hierarchical spec lookups
	resolvedSpecs := tp.network.buildResolvedSpecs(profile)

	// Create composite builder in overwrite mode
	cb := node.NewCompositeBuilder(deviceName, node.CompositeOverwrite).
		SetGeneratedBy("topology-provisioner").
		SetDescription(fmt.Sprintf("Full device provisioning from topology.json for %s", deviceName))

	// Step 1: Device-level entries
	if err := tp.addDeviceEntries(cb, deviceName, resolved, topoDev, resolvedSpecs); err != nil {
		return nil, fmt.Errorf("adding device entries: %w", err)
	}

	// Step 1b: QoS device-wide tables (DSCP maps, schedulers, WRED profiles)
	tp.addQoSDeviceEntries(cb, topoDev, resolvedSpecs)

	// Step 2: Per-interface service entries (skip stub interfaces with no service)
	for intfName, ti := range topoDev.Interfaces {
		if ti.Service == "" {
			continue
		}
		if err := tp.addInterfaceEntries(cb, intfName, ti, resolved, resolvedSpecs); err != nil {
			return nil, fmt.Errorf("interface %s: %w", intfName, err)
		}
	}

	return cb.Build(), nil
}

// ProvisionDevice generates a complete CONFIG_DB for the named device from the
// topology spec and delivers it atomically with node.CompositeOverwrite mode.
//
// This mode:
//   - Does NOT interrogate the device for existing configuration
//   - Generates all CONFIG_DB entries offline from specs + topology
//   - Connects to the device only for delivery
//   - Wipes existing CONFIG_DB and replaces with generated config
func (tp *TopologyProvisioner) ProvisionDevice(ctx context.Context, deviceName string) (*node.CompositeDeliveryResult, error) {
	// Generate the composite config offline
	composite, err := tp.GenerateDeviceComposite(deviceName)
	if err != nil {
		return nil, fmt.Errorf("generating composite: %w", err)
	}

	// Connect to device for delivery only
	dev, err := tp.network.ConnectNode(ctx, deviceName)
	if err != nil {
		return nil, fmt.Errorf("connecting to device: %w", err)
	}
	defer dev.Disconnect()

	// Lock for writing
	if err := dev.Lock(); err != nil {
		return nil, fmt.Errorf("locking device: %w", err)
	}
	defer dev.Unlock()

	// Deliver with overwrite mode (replace entire CONFIG_DB)
	result, err := dev.DeliverComposite(composite, node.CompositeOverwrite)
	if err != nil {
		return nil, fmt.Errorf("delivering composite: %w", err)
	}

	util.WithDevice(deviceName).Infof("Provisioned device from topology: %d entries applied", result.Applied)
	return result, nil
}

// ============================================================================
// Device-level entry generation
// ============================================================================

// addDeviceEntries adds device-level CONFIG_DB entries to the composite builder.
func (tp *TopologyProvisioner) addDeviceEntries(cb *node.CompositeBuilder, deviceName string, resolved *spec.ResolvedProfile, topoDev *spec.TopologyDevice, resolvedSpecs node.SpecProvider) error {
	// All-eBGP design: router runs underlay_asn (required)
	// Overlay eBGP peers use next-hop-unchanged to preserve VTEP addresses
	if resolved.UnderlayASN == 0 {
		return fmt.Errorf("underlay_asn required for device %s (all-eBGP design)", deviceName)
	}

	// DEVICE_METADATA — complete fields for SONiC unified mode
	metaFields := map[string]string{
		"hostname":                   deviceName,
		"bgp_asn":                    fmt.Sprintf("%d", resolved.UnderlayASN),
		"docker_routing_config_mode": "unified",
		"frr_mgmt_framework_config":  "true",
		"type":                       "LeafRouter",
	}
	if resolved.Platform != "" {
		metaFields["platform"] = resolved.Platform
	}
	if resolved.MAC != "" {
		metaFields["mac"] = resolved.MAC
	}
	// Lookup HWSKU from platform spec
	if resolved.Platform != "" {
		if platform, err := tp.network.GetPlatform(resolved.Platform); err == nil {
			metaFields["hwsku"] = platform.HWSKU
		}
	}
	// Route reflectors are SpineRouter type
	if topoDev.DeviceConfig != nil && topoDev.DeviceConfig.RouteReflector {
		metaFields["type"] = "SpineRouter"
	}
	cb.AddEntry("DEVICE_METADATA", "localhost", metaFields)

	// LOOPBACK_INTERFACE with loopback IP
	cb.AddEntry("LOOPBACK_INTERFACE", "Loopback0", map[string]string{})
	loopbackIPKey := fmt.Sprintf("Loopback0|%s/32", resolved.LoopbackIP)
	cb.AddEntry("LOOPBACK_INTERFACE", loopbackIPKey, map[string]string{})

	// PORT entries for each interface in topology.
	// Skip stub interfaces (no service AND no link) — they may have no
	// physical backing in VPP and creating PORT entries for non-existent
	// ports crashes orchagent.
	for intfName, ti := range topoDev.Interfaces {
		if ti.Service == "" && ti.Link == "" {
			continue
		}
		// Only add PORT entries for physical interfaces (Ethernet*)
		if strings.HasPrefix(intfName, "Ethernet") {
			cb.AddPortConfig(intfName, map[string]string{
				"admin_status": "up",
				"mtu":          "9100",
			})
		}
	}

	// Determine if device needs EVPN infrastructure
	hasEVPN := tp.deviceHasEVPN(topoDev, resolvedSpecs)

	// VXLAN_TUNNEL + VXLAN_EVPN_NVO (if device has EVPN services)
	if hasEVPN {
		cb.AddEntry("VXLAN_TUNNEL", "vtep1", map[string]string{
			"src_ip": resolved.VTEPSourceIP,
		})
		cb.AddEntry("VXLAN_EVPN_NVO", "nvo1", map[string]string{
			"source_vtep": "vtep1",
		})
	}

	// BGP_GLOBALS — underlay AS + eBGP settings
	cb.AddBGPGlobals("default", map[string]string{
		"local_asn":            fmt.Sprintf("%d", resolved.UnderlayASN),
		"router_id":            resolved.RouterID,
		"ebgp_requires_policy": "false",
		"log_neighbor_changes": "true",
	})

	// BGP address-family globals
	cb.AddBGPGlobalsAF("default", "ipv4_unicast", map[string]string{})
	if hasEVPN {
		cb.AddBGPGlobalsAF("default", "l2vpn_evpn", map[string]string{
			"advertise-all-vni": "true",
		})
	}

	// BGP neighbors from EVPN peers (eBGP overlay via loopback).
	// Profile-driven: peers specified in profile.evpn.peers
	// All-eBGP design: overlay uses peer's underlay_asn (no local-as needed)
	for _, peerName := range getEVPNPeerNames(tp.network, deviceName) {
		peerProfile, err := tp.network.loadProfile(peerName)
		if err != nil {
			util.Logger.Warnf("Could not load EVPN peer profile %s: %v", peerName, err)
			continue
		}

		// All-eBGP: peer must have underlay_asn
		if peerProfile.UnderlayASN == 0 {
			util.Logger.Warnf("EVPN peer %s missing underlay_asn, skipping", peerName)
			continue
		}

		cb.AddBGPNeighbor("default", peerProfile.LoopbackIP, map[string]string{
			"asn":            fmt.Sprintf("%d", peerProfile.UnderlayASN),
			"local_addr":     resolved.LoopbackIP,
			"admin_status":   "up",
			"ebgp_multihop":  "true",
		})

		// Activate IPv4 unicast (for loopback distribution)
		cb.AddBGPNeighborAF("default", peerProfile.LoopbackIP, "ipv4_unicast", map[string]string{
			"admin_status": "true",
		})

		if hasEVPN {
			// Activate L2VPN EVPN with next-hop-unchanged (preserve VTEP across eBGP)
			cb.AddBGPNeighborAF("default", peerProfile.LoopbackIP, "l2vpn_evpn", map[string]string{
				"admin_status":        "true",
				"nexthop_unchanged":   "true", // Critical for eBGP overlay
			})
		}
	}

	// eBGP underlay neighbors from topology links (hop-by-hop)
	// For each interface with a link and IP, derive the peer's underlay_asn
	// and create a BGP_NEIGHBOR entry for the eBGP underlay session.
	for _, ti := range topoDev.Interfaces {
		if ti.Link == "" || ti.IP == "" {
			continue
		}
		// Parse "peerDevice:peerInterface" from the link field
		parts := strings.SplitN(ti.Link, ":", 2)
		if len(parts) != 2 {
			continue
		}
		peerDeviceName := parts[0]

		// Load peer device's profile to get its underlay_asn
		peerProfile, err := tp.network.loadProfile(peerDeviceName)
		if err != nil {
			util.Logger.Warnf("Could not load peer profile %s for underlay BGP: %v", peerDeviceName, err)
			continue
		}
		peerASN := peerProfile.UnderlayASN
		if peerASN == 0 {
			continue // No underlay ASN — skip eBGP neighbor
		}

		// Derive peer IP from our interface IP (/31 or /30)
		peerIP, err := util.DeriveNeighborIP(ti.IP)
		if err != nil {
			continue
		}

		localIP, _ := util.SplitIPMask(ti.IP)
		cb.AddBGPNeighbor("default", peerIP, map[string]string{
			"asn":          fmt.Sprintf("%d", peerASN),
			"local_addr":   localIP,
			"admin_status": "up",
		})
		cb.AddBGPNeighborAF("default", peerIP, "ipv4_unicast", map[string]string{
			"admin_status": "true",
		})
	}

	// Route redistribution for connected (loopback + service subnets)
	cb.AddRouteRedistribution("default", "connected", "ipv4", map[string]string{})

	// Route reflector setup if device_config.route_reflector is true
	if topoDev.DeviceConfig != nil && topoDev.DeviceConfig.RouteReflector {
		tp.addRouteReflectorEntries(cb, resolved, topoDev)
	}

	return nil
}

// addRouteReflectorEntries adds route reflector configuration to the composite.
func (tp *TopologyProvisioner) addRouteReflectorEntries(cb *node.CompositeBuilder, resolved *spec.ResolvedProfile, _ *spec.TopologyDevice) {
	// RR cluster ID: from profile EVPN config, defaults to loopback IP (set during resolution).
	clusterID := resolved.ClusterID

	// RR must have underlay_asn (all-eBGP design)
	if resolved.UnderlayASN == 0 {
		util.Logger.Warnf("Route reflector %s missing underlay_asn", resolved.DeviceName)
		return
	}

	// Update BGP_GLOBALS with RR-specific settings (ebgp_requires_policy and
	// log_neighbor_changes are already set in addDeviceEntries for all devices)
	cb.AddBGPGlobals("default", map[string]string{
		"local_asn":              fmt.Sprintf("%d", resolved.UnderlayASN),
		"router_id":             resolved.RouterID,
		"rr_cluster_id":         clusterID,
		"load_balance_mp_relax": "true",
		"ebgp_requires_policy":  "false",
		"log_neighbor_changes":  "true",
	})

	// Discover RR clients: iterate all devices in the topology.
	// Any device that is NOT a route reflector and is NOT this device is a client.
	topo := tp.network.GetTopology()
	for clientName, clientTopoDev := range topo.Devices {
		if clientName == resolved.DeviceName {
			continue // skip self
		}
		if clientTopoDev.DeviceConfig != nil && clientTopoDev.DeviceConfig.RouteReflector {
			continue // skip other RRs
		}
		if tp.network.IsHostDevice(clientName) {
			continue // skip host devices
		}
		// Load client profile to get its loopback IP and AS
		clientProfile, err := tp.network.loadProfile(clientName)
		if err != nil {
			util.Logger.Warnf("Could not load client profile %s for RR: %v", clientName, err)
			continue
		}
		clientLoopback := clientProfile.LoopbackIP
		if clientLoopback == "" {
			continue
		}

		// Client must have underlay_asn (all-eBGP design)
		if clientProfile.UnderlayASN == 0 {
			util.Logger.Warnf("RR client %s missing underlay_asn, skipping", clientName)
			continue
		}

		// Add eBGP neighbor for this client (all-eBGP design)
		cb.AddBGPNeighbor("default", clientLoopback, map[string]string{
			"asn":            fmt.Sprintf("%d", clientProfile.UnderlayASN),
			"local_addr":     resolved.LoopbackIP,
			"admin_status":   "up",
			"ebgp_multihop":  "true",
		})
		cb.AddBGPNeighborAF("default", clientLoopback, "ipv4_unicast", map[string]string{
			"admin_status": "true",
			"rrclient":     "true",
			"nhself":       "true",
		})
	}

	// For RR-to-RR neighbors (from EVPN peers), enable route-reflector-client
	for _, peerName := range getEVPNPeerNames(tp.network, resolved.DeviceName) {
		peerProfile, err := tp.network.loadProfile(peerName)
		if err != nil {
			continue
		}
		cb.AddBGPNeighborAF("default", peerProfile.LoopbackIP, "ipv4_unicast", map[string]string{
			"admin_status": "true",
			"rrclient":     "true",
			"nhself":       "true",
		})
		cb.AddBGPNeighborAF("default", peerProfile.LoopbackIP, "ipv6_unicast", map[string]string{
			"admin_status": "true",
			"rrclient":     "true",
			"nhself":       "true",
		})
		cb.AddBGPNeighborAF("default", peerProfile.LoopbackIP, "l2vpn_evpn", map[string]string{
			"admin_status": "true",
			"rrclient":     "true",
		})
	}

	// IPv6 route redistribution for RR
	cb.AddRouteRedistribution("default", "connected", "ipv6", map[string]string{})
}

// getEVPNPeerNames returns the list of EVPN peer device names from profile.
func getEVPNPeerNames(network *Network, deviceName string) []string {
	profile, err := network.loadProfile(deviceName)
	if err != nil || profile.EVPN == nil {
		return nil
	}

	topo := network.GetTopology()
	var peers []string
	for _, peerName := range profile.EVPN.Peers {
		if peerName == deviceName {
			continue // Skip self
		}
		// Skip devices not in current topology
		if topo != nil && !topo.HasDevice(peerName) {
			continue
		}
		// Skip host devices
		if network.isHostDeviceLocked(peerName) {
			continue
		}
		peers = append(peers, peerName)
	}
	return peers
}

// deviceHasEVPN checks if any interface service requires EVPN (L3VNI or L2VNI).
func (tp *TopologyProvisioner) deviceHasEVPN(topoDev *spec.TopologyDevice, sp node.SpecProvider) bool {
	for _, ti := range topoDev.Interfaces {
		svc, err := sp.GetService(ti.Service)
		if err != nil {
			continue
		}
		if svc.IPVPN != "" {
			ipvpn, err := sp.GetIPVPN(svc.IPVPN)
			if err == nil && ipvpn.L3VNI > 0 {
				return true
			}
		}
		if svc.MACVPN != "" {
			macvpn, err := sp.GetMACVPN(svc.MACVPN)
			if err == nil && macvpn.VNI > 0 {
				return true
			}
		}
	}
	return false
}

// ============================================================================
// Per-interface service entry generation
// ============================================================================

// addInterfaceEntries generates all CONFIG_DB entries for an interface service
// and adds them to the composite builder.  Delegates to the shared
// node.GenerateServiceEntries function in service_gen.go.
func (tp *TopologyProvisioner) addInterfaceEntries(cb *node.CompositeBuilder, intfName string, ti *spec.TopologyInterface, resolved *spec.ResolvedProfile, sp node.SpecProvider) error {
	entries, err := node.GenerateServiceEntries(sp, node.ServiceEntryParams{
		ServiceName:   ti.Service,
		InterfaceName: intfName,
		IPAddress:     ti.IP,
		Params:        ti.Params,
		UnderlayASN:   resolved.UnderlayASN,
		PlatformName:  resolved.Platform,
	})
	if err != nil {
		return err
	}

	for _, e := range entries {
		cb.AddEntry(e.Table, e.Key, e.Fields)
	}

	return nil
}

// addQoSDeviceEntries scans all services in the topology, collects distinct QoS
// policy names, and adds device-wide CONFIG_DB tables (DSCP maps, schedulers, WRED).
func (tp *TopologyProvisioner) addQoSDeviceEntries(cb *node.CompositeBuilder, topoDev *spec.TopologyDevice, sp node.SpecProvider) {
	seen := make(map[string]bool)
	for _, ti := range topoDev.Interfaces {
		if ti.Service == "" {
			continue
		}
		svc, err := sp.GetService(ti.Service)
		if err != nil {
			continue
		}
		policyName, policy := node.ResolveServiceQoSPolicy(sp, svc)
		if policy == nil {
			continue
		}
		if seen[policyName] {
			continue
		}
		seen[policyName] = true
		for _, entry := range node.GenerateQoSDeviceEntries(policyName, policy) {
			cb.AddEntry(entry.Table, entry.Key, entry.Fields)
		}
	}
}

