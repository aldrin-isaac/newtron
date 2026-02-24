// topology.go implements topology-driven provisioning from topology.json specs.
//
// ProvisionDevice generates a complete CONFIG_DB offline and delivers it
// atomically via node.CompositeOverwrite (no device interrogation needed).
//
// Uses the Abstract Node pattern: creates an offline Node with a shadow ConfigDB,
// calls the same Node/Interface methods used in the online path, and exports the
// accumulated entries as a CompositeConfig. topology.json represents an abstract
// topology in which abstract nodes live — the same code path handles both offline
// provisioning and online operations.
//
// VerifyDeviceHealth generates the expected CONFIG_DB from the topology (same
// as the provisioner), then compares against the live device. Operational state
// checks (BGP sessions, interface oper-up) complement the config intent check.
package network

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
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
//
// Uses the Abstract Node pattern: creates an offline Node, calls the same
// Node/Interface methods used by the online CLI path, and exports accumulated
// entries as a CompositeConfig. Zero inline CONFIG_DB key construction.
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

	// All-eBGP design: router runs underlay_asn (required)
	if resolved.UnderlayASN == 0 {
		return nil, fmt.Errorf("underlay_asn required for device %s (all-eBGP design)", deviceName)
	}

	ctx := context.Background()

	// Create abstract node with empty shadow ConfigDB.
	// Operations build desired state; BuildComposite exports it.
	n := node.NewAbstract(resolvedSpecs, deviceName, profile, resolved)

	// =========================================================================
	// Step 1: Register physical ports (enables GetInterface for later use)
	// =========================================================================
	for intfName, ti := range topoDev.Interfaces {
		if ti.Service == "" && ti.Link == "" {
			continue
		}
		if strings.HasPrefix(intfName, "Ethernet") {
			n.RegisterPort(intfName, map[string]string{
				"admin_status": "up",
				"mtu":          "9100",
			})
		}
	}

	// =========================================================================
	// Step 2: PortChannel creation (before services, since services bind to them)
	// =========================================================================
	for pcName, pc := range topoDev.PortChannels {
		if _, err := n.CreatePortChannel(ctx, pcName, node.PortChannelConfig{
			Members: pc.Members,
		}); err != nil {
			return nil, fmt.Errorf("portchannel %s: %w", pcName, err)
		}
	}

	// =========================================================================
	// Step 3: Loopback interface
	// =========================================================================
	if _, err := n.ConfigureLoopback(ctx); err != nil {
		return nil, fmt.Errorf("configuring loopback: %w", err)
	}

	// =========================================================================
	// Step 4: Full device metadata (bgp_asn, hwsku, type, frrcfgd flags)
	// =========================================================================
	metaFields := map[string]string{
		"hostname":                   deviceName,
		"bgp_asn":                    fmt.Sprintf("%d", resolved.UnderlayASN),
		"docker_routing_config_mode": "unified",
		"frr_mgmt_framework_config":  "true",
		"type":                       "LeafRouter",
	}
	// Platform is a factory value (baked into the SONiC image) — do NOT
	// write it to DEVICE_METADATA. SONiC components may read it to find
	// platform-specific configs; overwriting with our internal label
	// ("sonic-ciscovs") would be incorrect.
	if resolved.MAC != "" {
		metaFields["mac"] = resolved.MAC
	}
	if resolved.Platform != "" {
		if platform, err := tp.network.GetPlatform(resolved.Platform); err == nil {
			metaFields["hwsku"] = platform.HWSKU
		}
	}
	if topoDev.DeviceConfig != nil && topoDev.DeviceConfig.RouteReflector {
		metaFields["type"] = "SpineRouter"
	}
	if _, err := n.SetDeviceMetadata(ctx, metaFields); err != nil {
		return nil, fmt.Errorf("setting device metadata: %w", err)
	}

	// =========================================================================
	// Step 5: EVPN infrastructure (VTEP + NVO, if any service needs it)
	// =========================================================================
	hasEVPN := tp.deviceHasEVPN(topoDev, resolvedSpecs)
	if hasEVPN {
		n.AddEntries(node.CreateVTEPConfig(resolved.VTEPSourceIP))
	}

	// =========================================================================
	// Step 6: BGP globals + address families + redistribution
	// =========================================================================
	n.AddEntries(node.CreateBGPGlobalsConfig("default", resolved.UnderlayASN, resolved.RouterID, map[string]string{
		"ebgp_requires_policy": "false",
		"suppress_fib_pending": "false",
		"log_neighbor_changes": "true",
	}))
	n.AddEntries(node.CreateBGPGlobalsAFConfig("default", "ipv4_unicast", nil))
	if hasEVPN {
		n.AddEntries(node.CreateBGPGlobalsAFConfig("default", "l2vpn_evpn", map[string]string{
			"advertise-all-vni": "true",
		}))
	}
	n.AddEntries(node.CreateRouteRedistributeConfig("default", "connected", "ipv4"))

	// =========================================================================
	// Step 7: BGP overlay peers (EVPN, from profile evpn.peers)
	// =========================================================================
	for _, peerName := range getEVPNPeerNames(tp.network, deviceName) {
		peerProfile, err := tp.network.loadProfile(peerName)
		if err != nil {
			util.Logger.Warnf("Could not load EVPN peer profile %s: %v", peerName, err)
			continue
		}
		if peerProfile.UnderlayASN == 0 {
			util.Logger.Warnf("EVPN peer %s missing underlay_asn, skipping", peerName)
			continue
		}
		n.AddEntries(node.CreateBGPNeighborConfig(peerProfile.LoopbackIP, peerProfile.UnderlayASN, resolved.LoopbackIP, node.BGPNeighborOpts{
			EBGPMultihop:     true,
			ActivateIPv4:     true,
			ActivateEVPN:     hasEVPN,
			NextHopUnchanged: hasEVPN, // Critical for eBGP overlay
		}))
	}

	// =========================================================================
	// Step 8: Route reflector configuration (RR clients + RR-to-RR overlays)
	// =========================================================================
	if topoDev.DeviceConfig != nil && topoDev.DeviceConfig.RouteReflector {
		tp.addRouteReflectorEntries(n, resolved, topoDev)
	}

	// =========================================================================
	// Step 9: Non-service interfaces with VRF + IP
	// =========================================================================
	for intfName, ti := range topoDev.Interfaces {
		if ti.Service != "" || ti.IP == "" {
			continue
		}
		if !strings.HasPrefix(intfName, "Ethernet") {
			continue
		}
		// Create VRF if specified and not yet in shadow
		if ti.VRF != "" && !n.VRFExists(ti.VRF) {
			if _, err := n.CreateVRF(ctx, ti.VRF, node.VRFConfig{}); err != nil {
				return nil, fmt.Errorf("creating VRF %s: %w", ti.VRF, err)
			}
		}
		iface, err := n.GetInterface(intfName)
		if err != nil {
			return nil, fmt.Errorf("interface %s: %w", intfName, err)
		}
		if ti.VRF != "" {
			if _, err := iface.SetVRF(ctx, ti.VRF); err != nil {
				return nil, fmt.Errorf("interface %s set-vrf: %w", intfName, err)
			}
		}
		if _, err := iface.SetIP(ctx, ti.IP); err != nil {
			return nil, fmt.Errorf("interface %s set-ip: %w", intfName, err)
		}
	}

	// =========================================================================
	// Step 10: Per-interface service application
	// =========================================================================
	for intfName, ti := range topoDev.Interfaces {
		if ti.Service == "" {
			continue
		}
		iface, err := n.GetInterface(intfName)
		if err != nil {
			return nil, fmt.Errorf("interface %s: %w", intfName, err)
		}

		// Resolve peer ASN from topology link
		var peerAS int
		if ti.Link != "" {
			parts := strings.SplitN(ti.Link, ":", 2)
			if len(parts) == 2 {
				if peerProfile, err := tp.network.loadProfile(parts[0]); err == nil {
					peerAS = peerProfile.UnderlayASN
				}
			}
		}

		if _, err := iface.ApplyService(ctx, ti.Service, node.ApplyServiceOpts{
			IPAddress: ti.IP,
			Params:    ti.Params,
			PeerAS:    peerAS,
		}); err != nil {
			return nil, fmt.Errorf("interface %s: %w", intfName, err)
		}
	}

	// =========================================================================
	// Step 11: Per-portchannel service application
	// =========================================================================
	for pcName, pc := range topoDev.PortChannels {
		if pc.Service == "" {
			continue
		}
		iface, err := n.GetInterface(pcName)
		if err != nil {
			return nil, fmt.Errorf("portchannel %s: %w", pcName, err)
		}
		peerAS := tp.resolvePortChannelPeerASN(pc, topoDev)
		if _, err := iface.ApplyService(ctx, pc.Service, node.ApplyServiceOpts{
			IPAddress: pc.IP,
			Params:    pc.Params,
			PeerAS:    peerAS,
		}); err != nil {
			return nil, fmt.Errorf("portchannel %s: %w", pcName, err)
		}
	}

	// =========================================================================
	// Export accumulated entries as CompositeConfig
	// =========================================================================
	composite := n.BuildComposite()
	composite.Metadata.GeneratedBy = "topology-provisioner"
	composite.Metadata.Description = fmt.Sprintf("Full device provisioning from topology.json for %s", deviceName)

	return composite, nil
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

	// Read system MAC and inject into DEVICE_METADATA before delivery.
	// Inherent: the system MAC is platform-initialized (not user config) and stored in
	// /etc/sonic/config_db.json. CompositeOverwrite replaces DEVICE_METADATA entirely;
	// the MAC must be re-injected so vlanmgrd can read it at startup.
	if mac := dev.ReadSystemMAC(); mac != "" {
		if composite.Tables != nil {
			if dm, ok := composite.Tables["DEVICE_METADATA"]; ok {
				if localhost, ok := dm["localhost"]; ok {
					localhost["mac"] = mac
				}
			}
		}
	}

	// Deliver with overwrite mode (replace entire CONFIG_DB)
	result, err := dev.DeliverComposite(composite, node.CompositeOverwrite)
	if err != nil {
		return nil, fmt.Errorf("delivering composite: %w", err)
	}

	util.WithDevice(deviceName).Infof("Provisioned device from topology: %d entries applied", result.Applied)
	return result, nil
}

// ============================================================================
// Route Reflector Configuration
// ============================================================================

// addRouteReflectorEntries adds route reflector configuration to the abstract node.
func (tp *TopologyProvisioner) addRouteReflectorEntries(n *node.Node, resolved *spec.ResolvedProfile, _ *spec.TopologyDevice) {
	// RR cluster ID: from profile EVPN config, defaults to loopback IP (set during resolution).
	clusterID := resolved.ClusterID

	// RR must have underlay_asn (all-eBGP design)
	if resolved.UnderlayASN == 0 {
		util.Logger.Warnf("Route reflector %s missing underlay_asn", resolved.DeviceName)
		return
	}

	// Update BGP_GLOBALS with RR-specific settings (ebgp_requires_policy and
	// log_neighbor_changes are already set in GenerateDeviceComposite for all devices)
	n.AddEntries(node.CreateBGPGlobalsConfig("default", resolved.UnderlayASN, resolved.RouterID, map[string]string{
		"rr_cluster_id":         clusterID,
		"load_balance_mp_relax": "true",
		"ebgp_requires_policy":  "false",
		"suppress_fib_pending":  "false",
		"log_neighbor_changes":  "true",
	}))

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
		n.AddEntries(node.CreateBGPNeighborConfig(clientLoopback, clientProfile.UnderlayASN, resolved.LoopbackIP, node.BGPNeighborOpts{
			EBGPMultihop: true,
			ActivateIPv4: true,
			RRClient:     true,
			NextHopSelf:  true,
		}))
	}

	// For RR-to-RR neighbors (from EVPN peers), overlay RR-specific AF entries.
	// The base BGP_NEIGHBOR was already created in step 7; CompositeBuilder's
	// AddEntry merges fields per key, so these AF entries layer on top.
	for _, peerName := range getEVPNPeerNames(tp.network, resolved.DeviceName) {
		peerProfile, err := tp.network.loadProfile(peerName)
		if err != nil {
			continue
		}
		n.AddEntries(node.CreateBGPNeighborConfig(peerProfile.LoopbackIP, peerProfile.UnderlayASN, resolved.LoopbackIP, node.BGPNeighborOpts{
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
	n.AddEntries(node.CreateRouteRedistributeConfig("default", "connected", "ipv6"))
}

// ============================================================================
// Helper functions (orchestration logic — topology-level, not node-level)
// ============================================================================

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

// serviceHasEVPN checks if a service requires EVPN (L3VNI or L2VNI).
func serviceHasEVPN(sp node.SpecProvider, serviceName string) bool {
	svc, err := sp.GetService(serviceName)
	if err != nil {
		return false
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
	return false
}

// deviceHasEVPN checks if any interface or portchannel service requires EVPN.
func (tp *TopologyProvisioner) deviceHasEVPN(topoDev *spec.TopologyDevice, sp node.SpecProvider) bool {
	for _, ti := range topoDev.Interfaces {
		if serviceHasEVPN(sp, ti.Service) {
			return true
		}
	}
	for _, pc := range topoDev.PortChannels {
		if serviceHasEVPN(sp, pc.Service) {
			return true
		}
	}
	return false
}

// resolvePortChannelPeerASN derives the peer ASN for a portchannel by looking
// at member interface links. All members of a LAG connect to the same peer
// device, so the first member with a resolvable link determines the peer ASN.
func (tp *TopologyProvisioner) resolvePortChannelPeerASN(pc *spec.TopologyPortChannel, topoDev *spec.TopologyDevice) int {
	for _, memberName := range pc.Members {
		memberIntf, ok := topoDev.Interfaces[memberName]
		if !ok || memberIntf.Link == "" {
			continue
		}
		parts := strings.SplitN(memberIntf.Link, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if peerProfile, err := tp.network.loadProfile(parts[0]); err == nil && peerProfile.UnderlayASN > 0 {
			return peerProfile.UnderlayASN
		}
	}
	return 0
}

// ============================================================================
// Intent-based health verification
// ============================================================================

// HealthReport combines config intent verification with operational state checks.
type HealthReport struct {
	Device      string                     `json:"device"`
	Status      string                     `json:"status"` // "pass", "warn", "fail"
	ConfigCheck *sonic.VerificationResult `json:"config_check"`
	OperChecks  []node.HealthCheckResult   `json:"oper_checks"`
}

// VerifyDeviceHealth generates expected CONFIG_DB entries from the topology,
// compares them against the live device, and checks operational state.
//
// Steps:
//  1. GenerateDeviceComposite → expected CONFIG_DB entries
//  2. ToConfigChanges + VerifyChangeSet → config intent verification
//  3. CheckBGPSessions → BGP operational state
//  4. CheckInterfaceOper → wired interface oper-up
//  5. Combine into HealthReport; overall status = worst of all checks
func (tp *TopologyProvisioner) VerifyDeviceHealth(ctx context.Context, deviceName string) (*HealthReport, error) {
	// Step 1: Generate expected CONFIG_DB entries from topology
	composite, err := tp.GenerateDeviceComposite(deviceName)
	if err != nil {
		return nil, fmt.Errorf("generating expected config: %w", err)
	}

	// Get the connected node
	dev, err := tp.network.GetNode(deviceName)
	if err != nil {
		return nil, fmt.Errorf("getting device: %w", err)
	}

	// Step 2: Verify composite against live CONFIG_DB
	configResult, err := dev.VerifyComposite(ctx, composite)
	if err != nil {
		return nil, fmt.Errorf("verifying config intent: %w", err)
	}

	// Step 3: Check BGP operational state
	bgpResults, err := dev.CheckBGPSessions(ctx)
	if err != nil {
		bgpResults = []node.HealthCheckResult{{
			Check: "bgp", Status: "fail",
			Message: fmt.Sprintf("BGP check error: %s", err),
		}}
	}

	// Step 4: Check interface oper-up for wired interfaces
	wiredInterfaces := tp.getWiredInterfaces(deviceName)
	var intfResults []node.HealthCheckResult
	if len(wiredInterfaces) > 0 {
		intfResults = dev.CheckInterfaceOper(wiredInterfaces)
	}

	// Combine oper checks
	var operChecks []node.HealthCheckResult
	operChecks = append(operChecks, bgpResults...)
	operChecks = append(operChecks, intfResults...)

	// Step 5: Derive overall status (worst of config + oper)
	report := &HealthReport{
		Device:      deviceName,
		ConfigCheck: configResult,
		OperChecks:  operChecks,
	}

	report.Status = "pass"
	if configResult.Failed > 0 {
		report.Status = "fail"
	}
	for _, oc := range operChecks {
		if oc.Status == "fail" {
			report.Status = "fail"
			break
		}
		if oc.Status == "warn" && report.Status == "pass" {
			report.Status = "warn"
		}
	}

	return report, nil
}

// getWiredInterfaces returns the sorted list of interfaces that should be
// oper-up: Ethernet interfaces with a link or service, plus portchannels.
func (tp *TopologyProvisioner) getWiredInterfaces(deviceName string) []string {
	topoDev, err := tp.network.GetTopologyDevice(deviceName)
	if err != nil {
		return nil
	}
	var interfaces []string
	for intfName, ti := range topoDev.Interfaces {
		if !strings.HasPrefix(intfName, "Ethernet") {
			continue
		}
		if ti.Service != "" || ti.Link != "" {
			interfaces = append(interfaces, intfName)
		}
	}
	for pcName := range topoDev.PortChannels {
		interfaces = append(interfaces, pcName)
	}
	sort.Strings(interfaces)
	return interfaces
}
