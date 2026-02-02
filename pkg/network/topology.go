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

// CompositeEntry is a single CONFIG_DB entry (table + key + fields).
// Used by generateServiceEntries to return entries without coupling to
// the CompositeBuilder or ChangeSet types.
type CompositeEntry struct {
	Table  string
	Key    string
	Fields map[string]string
}

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

	// Step 2: Per-interface service entries
	for intfName, ti := range topoDev.Interfaces {
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
	if err := dev.Lock(ctx); err != nil {
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
	if err := dev.Lock(ctx); err != nil {
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

	// PORT entries for each interface in topology
	for intfName := range topoDev.Interfaces {
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

	// BGP neighbors from route reflectors (iBGP overlay via loopback)
	for _, rrIP := range resolved.BGPNeighbors {
		cb.AddBGPNeighbor("default", rrIP, map[string]string{
			"asn":          fmt.Sprintf("%d", resolved.ASNumber),
			"local_addr":   resolved.LoopbackIP,
			"local_asn":    fmt.Sprintf("%d", resolved.ASNumber),
			"admin_status": "up",
		})

		// Activate IPv4 unicast
		cb.AddBGPNeighborAF("default", rrIP, "ipv4_unicast", map[string]string{
			"activate": "true",
		})

		if hasEVPN {
			// Activate L2VPN EVPN
			cb.AddBGPNeighborAF("default", rrIP, "l2vpn_evpn", map[string]string{
				"activate": "true",
			})
		}
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

	// Update BGP_GLOBALS with RR-specific settings (ebgp_requires_policy and
	// log_neighbor_changes are already set in addDeviceEntries for all devices)
	cb.AddBGPGlobals("default", map[string]string{
		"local_asn":              fmt.Sprintf("%d", underlayASN),
		"router_id":             resolved.RouterID,
		"rr_cluster_id":         resolved.LoopbackIP,
		"load_balance_mp_relax": "true",
		"ebgp_requires_policy":  "false",
		"log_neighbor_changes":  "true",
	})

	// For RR neighbors, enable route-reflector-client on all AFs
	for _, rrIP := range resolved.BGPNeighbors {
		cb.AddBGPNeighborAF("default", rrIP, "ipv4_unicast", map[string]string{
			"activate":               "true",
			"route_reflector_client": "true",
			"next_hop_self":          "true",
		})
		cb.AddBGPNeighborAF("default", rrIP, "ipv6_unicast", map[string]string{
			"activate":               "true",
			"route_reflector_client": "true",
			"next_hop_self":          "true",
		})
		cb.AddBGPNeighborAF("default", rrIP, "l2vpn_evpn", map[string]string{
			"activate":               "true",
			"route_reflector_client": "true",
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
// and adds them to the composite builder.
func (tp *TopologyProvisioner) addInterfaceEntries(cb *CompositeBuilder, intfName string, ti *spec.TopologyInterface, resolved *spec.ResolvedProfile) error {
	entries, err := tp.generateServiceEntries(ti.Service, intfName, ti.IP, ti.Params, resolved)
	if err != nil {
		return err
	}

	for _, e := range entries {
		cb.AddEntry(e.Table, e.Key, e.Fields)
	}

	return nil
}

// generateServiceEntries returns the CONFIG_DB entries that ApplyService would create,
// without requiring a device connection. Used by the topology provisioner for
// offline composite generation.
//
// This encapsulates the spec → config_db translation for a single interface service.
func (tp *TopologyProvisioner) generateServiceEntries(
	serviceName string,
	interfaceName string,
	ipAddr string,
	params map[string]string,
	resolved *spec.ResolvedProfile,
) ([]CompositeEntry, error) {
	svc, err := tp.network.GetService(serviceName)
	if err != nil {
		return nil, fmt.Errorf("service '%s' not found", serviceName)
	}

	var entries []CompositeEntry

	// Resolve VPN definitions
	var ipvpnDef *spec.IPVPNSpec
	var macvpnDef *spec.MACVPNSpec

	if svc.IPVPN != "" {
		ipvpnDef, err = tp.network.GetIPVPN(svc.IPVPN)
		if err != nil {
			return nil, fmt.Errorf("ipvpn '%s' not found", svc.IPVPN)
		}
	}
	if svc.MACVPN != "" {
		macvpnDef, err = tp.network.GetMACVPN(svc.MACVPN)
		if err != nil {
			return nil, fmt.Errorf("macvpn '%s' not found", svc.MACVPN)
		}
	}

	// VLAN creation (for L2/IRB)
	if (svc.ServiceType == spec.ServiceTypeL2 || svc.ServiceType == spec.ServiceTypeIRB) && macvpnDef != nil {
		vlanName := fmt.Sprintf("Vlan%d", macvpnDef.VLAN)
		entries = append(entries, CompositeEntry{
			Table:  "VLAN",
			Key:    vlanName,
			Fields: map[string]string{"vlanid": fmt.Sprintf("%d", macvpnDef.VLAN)},
		})

		// L2VNI mapping
		if macvpnDef.L2VNI > 0 {
			mapKey := fmt.Sprintf("vtep1|map_%d_%s", macvpnDef.L2VNI, vlanName)
			entries = append(entries, CompositeEntry{
				Table:  "VXLAN_TUNNEL_MAP",
				Key:    mapKey,
				Fields: map[string]string{"vlan": vlanName, "vni": fmt.Sprintf("%d", macvpnDef.L2VNI)},
			})
		}

		// ARP suppression
		if macvpnDef.ARPSuppression {
			entries = append(entries, CompositeEntry{
				Table:  "SUPPRESS_VLAN_NEIGH",
				Key:    vlanName,
				Fields: map[string]string{"suppress": "on"},
			})
		}
	}

	// VRF creation (for L3/IRB with per-interface VRF)
	vrfName := ""
	if (svc.ServiceType == spec.ServiceTypeL3 || svc.ServiceType == spec.ServiceTypeIRB) &&
		svc.VRFType == spec.VRFTypeInterface {
		vrfName = util.DeriveVRFName(svc.VRFType, serviceName, interfaceName)
		vrfFields := map[string]string{}
		if ipvpnDef != nil && ipvpnDef.L3VNI > 0 {
			vrfFields["vni"] = fmt.Sprintf("%d", ipvpnDef.L3VNI)
		}
		entries = append(entries, CompositeEntry{
			Table:  "VRF",
			Key:    vrfName,
			Fields: vrfFields,
		})

		// L3VNI mapping
		if ipvpnDef != nil && ipvpnDef.L3VNI > 0 {
			mapKey := fmt.Sprintf("vtep1|map_%d_%s", ipvpnDef.L3VNI, vrfName)
			entries = append(entries, CompositeEntry{
				Table:  "VXLAN_TUNNEL_MAP",
				Key:    mapKey,
				Fields: map[string]string{"vrf": vrfName, "vni": fmt.Sprintf("%d", ipvpnDef.L3VNI)},
			})

			// BGP EVPN route targets for the VRF
			entries = append(entries, generateRouteTargetEntries(vrfName, ipvpnDef)...)
		}
	} else if svc.VRFType == spec.VRFTypeShared {
		vrfName = svc.IPVPN
	}

	// Interface configuration based on service type
	switch svc.ServiceType {
	case spec.ServiceTypeL2:
		vlanName := fmt.Sprintf("Vlan%d", macvpnDef.VLAN)
		memberKey := fmt.Sprintf("%s|%s", vlanName, interfaceName)
		entries = append(entries, CompositeEntry{
			Table:  "VLAN_MEMBER",
			Key:    memberKey,
			Fields: map[string]string{"tagging_mode": "untagged"},
		})

	case spec.ServiceTypeL3:
		intfFields := map[string]string{}
		if vrfName != "" {
			intfFields["vrf_name"] = vrfName
		}
		entries = append(entries, CompositeEntry{
			Table:  "INTERFACE",
			Key:    interfaceName,
			Fields: intfFields,
		})
		if ipAddr != "" {
			ipKey := fmt.Sprintf("%s|%s", interfaceName, ipAddr)
			entries = append(entries, CompositeEntry{
				Table:  "INTERFACE",
				Key:    ipKey,
				Fields: map[string]string{},
			})
		}

	case spec.ServiceTypeIRB:
		if macvpnDef != nil {
			vlanName := fmt.Sprintf("Vlan%d", macvpnDef.VLAN)
			memberKey := fmt.Sprintf("%s|%s", vlanName, interfaceName)
			entries = append(entries, CompositeEntry{
				Table:  "VLAN_MEMBER",
				Key:    memberKey,
				Fields: map[string]string{"tagging_mode": "tagged"},
			})

			vlanIntfFields := map[string]string{}
			if vrfName != "" {
				vlanIntfFields["vrf_name"] = vrfName
			}
			entries = append(entries, CompositeEntry{
				Table:  "VLAN_INTERFACE",
				Key:    vlanName,
				Fields: vlanIntfFields,
			})

			if svc.AnycastGateway != "" {
				sviIPKey := fmt.Sprintf("%s|%s", vlanName, svc.AnycastGateway)
				entries = append(entries, CompositeEntry{
					Table:  "VLAN_INTERFACE",
					Key:    sviIPKey,
					Fields: map[string]string{},
				})
			}

			if svc.AnycastMAC != "" {
				entries = append(entries, CompositeEntry{
					Table:  "SAG_GLOBAL",
					Key:    "IPv4",
					Fields: map[string]string{"gwmac": svc.AnycastMAC},
				})
			}
		}
	}

	// ACL configuration
	if svc.IngressFilter != "" {
		filterEntries, err := tp.generateACLEntries(serviceName, svc.IngressFilter, interfaceName, "ingress")
		if err != nil {
			return nil, err
		}
		entries = append(entries, filterEntries...)
	}
	if svc.EgressFilter != "" {
		filterEntries, err := tp.generateACLEntries(serviceName, svc.EgressFilter, interfaceName, "egress")
		if err != nil {
			return nil, err
		}
		entries = append(entries, filterEntries...)
	}

	// QoS configuration
	if svc.QoSProfile != "" {
		qosProfile, err := tp.network.GetQoSProfile(svc.QoSProfile)
		if err == nil && qosProfile != nil {
			qosFields := map[string]string{}
			if qosProfile.DSCPToTCMap != "" {
				qosFields["dscp_to_tc_map"] = qosProfile.DSCPToTCMap
			}
			if qosProfile.TCToQueueMap != "" {
				qosFields["tc_to_queue_map"] = qosProfile.TCToQueueMap
			}
			if len(qosFields) > 0 {
				entries = append(entries, CompositeEntry{
					Table:  "PORT_QOS_MAP",
					Key:    interfaceName,
					Fields: qosFields,
				})
			}
		}
	}

	// BGP routing configuration
	if svc.Routing != nil && svc.Routing.Protocol == spec.RoutingProtocolBGP {
		bgpEntries := tp.generateBGPEntries(svc, ipAddr, params, vrfName, resolved)
		entries = append(entries, bgpEntries...)
	}

	// Service binding record
	bindingFields := map[string]string{
		"service_name": serviceName,
	}
	if ipAddr != "" {
		bindingFields["ip_address"] = ipAddr
	}
	if vrfName != "" {
		bindingFields["vrf_name"] = vrfName
	}
	if svc.IPVPN != "" {
		bindingFields["ipvpn"] = svc.IPVPN
	}
	if svc.MACVPN != "" {
		bindingFields["macvpn"] = svc.MACVPN
	}
	entries = append(entries, CompositeEntry{
		Table:  "NEWTRON_SERVICE_BINDING",
		Key:    interfaceName,
		Fields: bindingFields,
	})

	return entries, nil
}

// generateRouteTargetEntries generates BGP EVPN route target entries for a VRF.
func generateRouteTargetEntries(vrfName string, ipvpnDef *spec.IPVPNSpec) []CompositeEntry {
	var entries []CompositeEntry

	// BGP_GLOBALS_AF for L2VPN EVPN on this VRF
	afKey := fmt.Sprintf("%s|l2vpn_evpn", vrfName)
	afFields := map[string]string{
		"advertise_ipv4_unicast": "true",
	}
	if len(ipvpnDef.ImportRT) > 0 {
		afFields["route_target_import_evpn"] = strings.Join(ipvpnDef.ImportRT, ",")
	}
	if len(ipvpnDef.ExportRT) > 0 {
		afFields["route_target_export_evpn"] = strings.Join(ipvpnDef.ExportRT, ",")
	}
	entries = append(entries, CompositeEntry{
		Table:  "BGP_GLOBALS_AF",
		Key:    afKey,
		Fields: afFields,
	})

	// BGP_EVPN_VNI per-VNI route targets
	if ipvpnDef.L3VNI > 0 && (len(ipvpnDef.ImportRT) > 0 || len(ipvpnDef.ExportRT) > 0) {
		vniKey := fmt.Sprintf("%s|%d", vrfName, ipvpnDef.L3VNI)
		vniFields := map[string]string{
			"rd": "auto",
		}
		if len(ipvpnDef.ImportRT) > 0 {
			vniFields["route_target_import"] = strings.Join(ipvpnDef.ImportRT, ",")
		}
		if len(ipvpnDef.ExportRT) > 0 {
			vniFields["route_target_export"] = strings.Join(ipvpnDef.ExportRT, ",")
		}
		entries = append(entries, CompositeEntry{
			Table:  "BGP_EVPN_VNI",
			Key:    vniKey,
			Fields: vniFields,
		})
	}

	return entries
}

// generateACLEntries generates ACL table and rule entries for a service filter.
func (tp *TopologyProvisioner) generateACLEntries(serviceName, filterSpecName, interfaceName, stage string) ([]CompositeEntry, error) {
	filterSpec, err := tp.network.GetFilterSpec(filterSpecName)
	if err != nil {
		return nil, fmt.Errorf("filter spec '%s' not found", filterSpecName)
	}

	var entries []CompositeEntry
	direction := "in"
	if stage == "egress" {
		direction = "out"
	}
	aclName := util.DeriveACLName(serviceName, direction)

	// ACL table
	entries = append(entries, CompositeEntry{
		Table: "ACL_TABLE",
		Key:   aclName,
		Fields: map[string]string{
			"type":        "L3",
			"stage":       stage,
			"ports":       interfaceName,
			"policy_desc": fmt.Sprintf("%s filter for %s", capitalizeFirst(stage), serviceName),
		},
	})

	// ACL rules from filter spec
	for _, rule := range filterSpec.Rules {
		ruleKey := fmt.Sprintf("%s|RULE_%d", aclName, rule.Sequence)
		fields := buildACLRuleFields(rule)
		entries = append(entries, CompositeEntry{
			Table:  "ACL_RULE",
			Key:    ruleKey,
			Fields: fields,
		})
	}

	return entries, nil
}

// buildACLRuleFields builds ACL rule fields from a filter rule spec.
func buildACLRuleFields(rule *spec.FilterRule) map[string]string {
	fields := map[string]string{
		"PRIORITY": fmt.Sprintf("%d", 10000-rule.Sequence),
	}

	if rule.Action == "permit" {
		fields["PACKET_ACTION"] = "FORWARD"
	} else {
		fields["PACKET_ACTION"] = "DROP"
	}

	if rule.SrcIP != "" {
		fields["SRC_IP"] = rule.SrcIP
	}
	if rule.DstIP != "" {
		fields["DST_IP"] = rule.DstIP
	}
	if rule.Protocol != "" {
		protoMap := map[string]int{
			"tcp": 6, "udp": 17, "icmp": 1, "ospf": 89, "vrrp": 112, "bgp": 179, "gre": 47,
		}
		if proto, ok := protoMap[rule.Protocol]; ok {
			fields["IP_PROTOCOL"] = fmt.Sprintf("%d", proto)
		} else {
			fields["IP_PROTOCOL"] = rule.Protocol
		}
	}
	if rule.DstPort != "" {
		fields["L4_DST_PORT"] = rule.DstPort
	}
	if rule.SrcPort != "" {
		fields["L4_SRC_PORT"] = rule.SrcPort
	}
	if rule.DSCP != "" {
		fields["DSCP"] = rule.DSCP
	}
	if rule.Policer != "" {
		fields["POLICER"] = rule.Policer
	}

	return fields
}

// generateBGPEntries generates BGP neighbor entries for a service with routing.
func (tp *TopologyProvisioner) generateBGPEntries(
	svc *spec.ServiceSpec,
	ipAddr string,
	params map[string]string,
	vrfName string,
	resolved *spec.ResolvedProfile,
) []CompositeEntry {
	if svc.Routing == nil || svc.Routing.Protocol != spec.RoutingProtocolBGP {
		return nil
	}

	var entries []CompositeEntry
	routing := svc.Routing

	// Derive peer IP from interface IP
	var peerIP string
	if ipAddr != "" {
		var err error
		peerIP, err = util.DeriveNeighborIP(ipAddr)
		if err != nil {
			return nil
		}
	}
	if peerIP == "" {
		return nil
	}

	// Determine peer AS
	var peerAS int
	if routing.PeerAS == spec.PeerASRequest {
		if peerASStr, ok := params["peer_as"]; ok {
			fmt.Sscanf(peerASStr, "%d", &peerAS)
		}
	} else if routing.PeerAS != "" {
		fmt.Sscanf(routing.PeerAS, "%d", &peerAS)
	}
	if peerAS == 0 {
		return nil
	}

	localAS := resolved.ASNumber
	if resolved.UnderlayASN > 0 {
		localAS = resolved.UnderlayASN
	}
	if localAS == 0 {
		return nil
	}

	localIP, _ := util.SplitIPMask(ipAddr)

	// BGP neighbor
	neighborFields := map[string]string{
		"asn":          fmt.Sprintf("%d", peerAS),
		"local_asn":    fmt.Sprintf("%d", localAS),
		"local_addr":   localIP,
		"admin_status": "up",
	}
	if vrfName != "" {
		neighborFields["vrf_name"] = vrfName
	}
	// Determine the VRF key prefix: "default" for default VRF, actual name otherwise
	vrfKey := "default"
	if vrfName != "" {
		vrfKey = vrfName
	}
	entries = append(entries, CompositeEntry{
		Table:  "BGP_NEIGHBOR",
		Key:    fmt.Sprintf("%s|%s", vrfKey, peerIP),
		Fields: neighborFields,
	})

	// IPv4 unicast activation with optional RR-client and next-hop-self params
	afKey := fmt.Sprintf("%s|%s|ipv4_unicast", vrfKey, peerIP)
	afFields := map[string]string{"activate": "true"}
	if val, ok := params["route_reflector_client"]; ok && val == "true" {
		afFields["route_reflector_client"] = "true"
	}
	if val, ok := params["next_hop_self"]; ok && val == "true" {
		afFields["next_hop_self"] = "true"
	}
	entries = append(entries, CompositeEntry{
		Table:  "BGP_NEIGHBOR_AF",
		Key:    afKey,
		Fields: afFields,
	})

	return entries
}

// capitalizeFirst returns s with the first letter uppercased.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
