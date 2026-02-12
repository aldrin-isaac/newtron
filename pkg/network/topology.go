// topology.go implements topology-driven provisioning from topology.json specs.
//
// Two provisioning modes:
//   - ProvisionDevice: generates a complete CONFIG_DB offline and delivers it
//     atomically via CompositeOverwrite (no device interrogation needed)
//   - ProvisionInterface: provisions a single interface using the topology spec
//     for parameters, but connects to the device and calls ApplyService()
package network

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/spec"
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

// ValidateTopologyDevice validates that all references in the topology for a device
// are resolvable (services exist, IPs valid, required params present).
func (tp *TopologyProvisioner) ValidateTopologyDevice(deviceName string) error {
	topoDev, err := tp.network.GetTopologyDevice(deviceName)
	if err != nil {
		return err
	}

	// Verify device profile exists
	if _, err := tp.network.loadProfile(deviceName); err != nil {
		return fmt.Errorf("device profile '%s' not found: %w", deviceName, err)
	}

	for intfName, ti := range topoDev.Interfaces {
		// Skip interfaces with no service assignment (stub ports)
		if ti.Service == "" {
			continue
		}

		// Verify service exists
		svc, err := tp.network.GetService(ti.Service)
		if err != nil {
			return fmt.Errorf("interface %s: service '%s' not found", intfName, ti.Service)
		}

		// Validate IP if required by service type
		if svc.ServiceType == spec.ServiceTypeL3 || svc.ServiceType == spec.ServiceTypeIRB {
			if ti.IP != "" && !util.IsValidIPv4CIDR(ti.IP) {
				return fmt.Errorf("interface %s: invalid IP address '%s'", intfName, ti.IP)
			}
			if svc.ServiceType == spec.ServiceTypeL3 && ti.IP == "" {
				return fmt.Errorf("interface %s: L3 service '%s' requires IP address", intfName, ti.Service)
			}
		}

		// Validate required params
		if svc.Routing != nil && svc.Routing.PeerAS == spec.PeerASRequest {
			if _, ok := ti.Params["peer_as"]; !ok {
				return fmt.Errorf("interface %s: service '%s' requires 'peer_as' param", intfName, ti.Service)
			}
		}
	}

	return nil
}

// GenerateDeviceComposite generates a CompositeConfig for a device without delivering it.
// Useful for inspection, serialization, or deferred delivery.
func (tp *TopologyProvisioner) GenerateDeviceComposite(deviceName string) (*CompositeConfig, error) {
	// Validate first
	if err := tp.ValidateTopologyDevice(deviceName); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
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

	// Create composite builder in overwrite mode
	cb := NewCompositeBuilder(deviceName, CompositeOverwrite).
		SetGeneratedBy("topology-provisioner").
		SetDescription(fmt.Sprintf("Full device provisioning from topology.json for %s", deviceName))

	// Step 1: Device-level entries
	tp.addDeviceEntries(cb, deviceName, resolved, topoDev)

	// Step 1b: QoS device-wide tables (DSCP maps, schedulers, WRED profiles)
	tp.addQoSDeviceEntries(cb, topoDev)

	// Step 2: Per-interface service entries (skip stub interfaces with no service)
	for intfName, ti := range topoDev.Interfaces {
		if ti.Service == "" {
			continue
		}
		if err := tp.addInterfaceEntries(cb, intfName, ti, resolved); err != nil {
			return nil, fmt.Errorf("interface %s: %w", intfName, err)
		}
	}

	return cb.Build(), nil
}

// ProvisionDevice generates a complete CONFIG_DB for the named device from the
// topology spec and delivers it atomically with CompositeOverwrite mode.
//
// This mode:
//   - Does NOT interrogate the device for existing configuration
//   - Generates all CONFIG_DB entries offline from specs + topology
//   - Connects to the device only for delivery
//   - Wipes existing CONFIG_DB and replaces with generated config
func (tp *TopologyProvisioner) ProvisionDevice(ctx context.Context, deviceName string) (*CompositeDeliveryResult, error) {
	// Generate the composite config offline
	composite, err := tp.GenerateDeviceComposite(deviceName)
	if err != nil {
		return nil, fmt.Errorf("generating composite: %w", err)
	}

	// Connect to device for delivery only
	dev, err := tp.network.ConnectDevice(ctx, deviceName)
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
	result, err := dev.DeliverComposite(composite, CompositeOverwrite)
	if err != nil {
		return nil, fmt.Errorf("delivering composite: %w", err)
	}

	util.WithDevice(deviceName).Infof("Provisioned device from topology: %d entries applied", result.Applied)
	return result, nil
}

// ProvisionInterface provisions a single interface on a device using topology data.
//
// This mode:
//   - Connects to the device and loads current state
//   - Reads service name and parameters from topology (not from user)
//   - Calls Interface.ApplyService() with those parameters
//   - Returns a ChangeSet for dry-run/execute
func (tp *TopologyProvisioner) ProvisionInterface(ctx context.Context, deviceName, interfaceName string) (*ChangeSet, error) {
	ti, err := tp.network.GetTopologyInterface(deviceName, interfaceName)
	if err != nil {
		return nil, err
	}

	// Connect and interrogate device
	dev, err := tp.network.ConnectDevice(ctx, deviceName)
	if err != nil {
		return nil, fmt.Errorf("connecting to device: %w", err)
	}

	// Lock for writing
	if err := dev.Lock(); err != nil {
		return nil, fmt.Errorf("locking device: %w", err)
	}

	// Get interface object
	intf, err := dev.GetInterface(interfaceName)
	if err != nil {
		return nil, fmt.Errorf("getting interface: %w", err)
	}

	// Build ApplyServiceOpts from topology
	opts := ApplyServiceOpts{
		IPAddress: ti.IP,
	}
	if peerAS, ok := ti.Params["peer_as"]; ok {
		fmt.Sscanf(peerAS, "%d", &opts.PeerAS)
	}

	// Apply service using the standard interface operation
	cs, err := intf.ApplyService(ctx, ti.Service, opts)
	if err != nil {
		return nil, fmt.Errorf("applying service '%s': %w", ti.Service, err)
	}

	util.WithDevice(deviceName).Infof("Provisioned interface %s with service '%s' from topology",
		interfaceName, ti.Service)
	return cs, nil
}

// ============================================================================
// Device-level entry generation
// ============================================================================

// addDeviceEntries adds device-level CONFIG_DB entries to the composite builder.
func (tp *TopologyProvisioner) addDeviceEntries(cb *CompositeBuilder, deviceName string, resolved *spec.ResolvedProfile, topoDev *spec.TopologyDevice) {
	// Determine underlay ASN (unique per device for eBGP fabric)
	underlayASN := resolved.ASNumber
	if resolved.UnderlayASN > 0 {
		underlayASN = resolved.UnderlayASN
	}

	// DEVICE_METADATA — complete fields for SONiC unified mode
	metaFields := map[string]string{
		"hostname":                   deviceName,
		"bgp_asn":                    fmt.Sprintf("%d", underlayASN),
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
	hasEVPN := tp.deviceHasEVPN(topoDev)

	// VXLAN_TUNNEL + VXLAN_EVPN_NVO (if device has EVPN services)
	if hasEVPN {
		cb.AddEntry("VXLAN_TUNNEL", "vtep1", map[string]string{
			"src_ip": resolved.VTEPSourceIP,
		})
		cb.AddEntry("VXLAN_EVPN_NVO", "nvo1", map[string]string{
			"source_vtep": "vtep1",
		})
	}

	// BGP_GLOBALS — underlay ASN + eBGP settings
	cb.AddBGPGlobals("default", map[string]string{
		"local_asn":            fmt.Sprintf("%d", underlayASN),
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

	// BGP neighbors from route reflectors (iBGP overlay via loopback).
	// These peers use the regional AS (ASNumber) with local-as override.
	// Since the router bgp uses underlayASN, FRR treats these as eBGP,
	// so ebgp_multihop is required for loopback-based peering.
	for _, rrIP := range resolved.BGPNeighbors {
		cb.AddBGPNeighbor("default", rrIP, map[string]string{
			"asn":            fmt.Sprintf("%d", resolved.ASNumber),
			"local_addr":     resolved.LoopbackIP,
			"local_asn":      fmt.Sprintf("%d", resolved.ASNumber),
			"admin_status":   "up",
			"ebgp_multihop":  "true",
		})

		// Activate IPv4 unicast (frrcfgd uses admin_status for activation)
		cb.AddBGPNeighborAF("default", rrIP, "ipv4_unicast", map[string]string{
			"admin_status": "true",
		})

		if hasEVPN {
			// Activate L2VPN EVPN
			cb.AddBGPNeighborAF("default", rrIP, "l2vpn_evpn", map[string]string{
				"admin_status": "true",
			})
		}
	}

	// eBGP underlay neighbors from topology links
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
			util.Warnf("Could not load peer profile %s for underlay BGP: %v", peerDeviceName, err)
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

		localAS := resolved.ASNumber
		if resolved.UnderlayASN > 0 {
			localAS = resolved.UnderlayASN
		}

		localIP, _ := util.SplitIPMask(ti.IP)
		cb.AddBGPNeighbor("default", peerIP, map[string]string{
			"asn":          fmt.Sprintf("%d", peerASN),
			"local_asn":    fmt.Sprintf("%d", localAS),
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
}

// addRouteReflectorEntries adds route reflector configuration to the composite.
func (tp *TopologyProvisioner) addRouteReflectorEntries(cb *CompositeBuilder, resolved *spec.ResolvedProfile, _ *spec.TopologyDevice) {
	// Determine underlay ASN for RR globals
	underlayASN := resolved.ASNumber
	if resolved.UnderlayASN > 0 {
		underlayASN = resolved.UnderlayASN
	}

	// Determine RR cluster ID: use SiteSpec.ClusterID if set, fall back to loopback IP.
	clusterID := resolved.LoopbackIP
	if resolved.Site != "" {
		if site, err := tp.network.GetSite(resolved.Site); err == nil && site.ClusterID != "" {
			clusterID = site.ClusterID
		}
	}

	// Update BGP_GLOBALS with RR-specific settings (ebgp_requires_policy and
	// log_neighbor_changes are already set in addDeviceEntries for all devices)
	cb.AddBGPGlobals("default", map[string]string{
		"local_asn":              fmt.Sprintf("%d", underlayASN),
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
		// Load client profile to get its loopback IP
		clientProfile, err := tp.network.loadProfile(clientName)
		if err != nil {
			util.Warnf("Could not load client profile %s for RR: %v", clientName, err)
			continue
		}
		clientLoopback := clientProfile.LoopbackIP
		if clientLoopback == "" {
			continue
		}

		// Add iBGP neighbor for this client (loopback-based, needs multihop)
		cb.AddBGPNeighbor("default", clientLoopback, map[string]string{
			"asn":            fmt.Sprintf("%d", resolved.ASNumber),
			"local_asn":      fmt.Sprintf("%d", resolved.ASNumber),
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

	// For RR-to-RR neighbors (from BGPNeighbors list), enable route-reflector-client
	for _, rrIP := range resolved.BGPNeighbors {
		cb.AddBGPNeighborAF("default", rrIP, "ipv4_unicast", map[string]string{
			"admin_status": "true",
			"rrclient":     "true",
			"nhself":       "true",
		})
		cb.AddBGPNeighborAF("default", rrIP, "ipv6_unicast", map[string]string{
			"admin_status": "true",
			"rrclient":     "true",
			"nhself":       "true",
		})
		cb.AddBGPNeighborAF("default", rrIP, "l2vpn_evpn", map[string]string{
			"admin_status": "true",
			"rrclient":     "true",
		})
	}

	// IPv6 route redistribution for RR
	cb.AddRouteRedistribution("default", "connected", "ipv6", map[string]string{})
}

// deviceHasEVPN checks if any interface service requires EVPN (L3VNI or L2VNI).
func (tp *TopologyProvisioner) deviceHasEVPN(topoDev *spec.TopologyDevice) bool {
	for _, ti := range topoDev.Interfaces {
		svc, err := tp.network.GetService(ti.Service)
		if err != nil {
			continue
		}
		if svc.IPVPN != "" {
			ipvpn, err := tp.network.GetIPVPN(svc.IPVPN)
			if err == nil && ipvpn.L3VNI > 0 {
				return true
			}
		}
		if svc.MACVPN != "" {
			macvpn, err := tp.network.GetMACVPN(svc.MACVPN)
			if err == nil && macvpn.L2VNI > 0 {
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
// GenerateServiceEntries function in service_gen.go.
func (tp *TopologyProvisioner) addInterfaceEntries(cb *CompositeBuilder, intfName string, ti *spec.TopologyInterface, resolved *spec.ResolvedProfile) error {
	entries, err := GenerateServiceEntries(tp.network, ServiceEntryParams{
		ServiceName:   ti.Service,
		InterfaceName: intfName,
		IPAddress:     ti.IP,
		Params:        ti.Params,
		LocalAS:       resolved.ASNumber,
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
func (tp *TopologyProvisioner) addQoSDeviceEntries(cb *CompositeBuilder, topoDev *spec.TopologyDevice) {
	seen := make(map[string]bool)
	for _, ti := range topoDev.Interfaces {
		if ti.Service == "" {
			continue
		}
		svc, err := tp.network.GetService(ti.Service)
		if err != nil {
			continue
		}
		policyName, policy := resolveServiceQoSPolicy(tp.network, svc)
		if policy == nil {
			continue
		}
		if seen[policyName] {
			continue
		}
		seen[policyName] = true
		for _, entry := range generateQoSDeviceEntries(policyName, policy) {
			cb.AddEntry(entry.Table, entry.Key, entry.Fields)
		}
	}
}

