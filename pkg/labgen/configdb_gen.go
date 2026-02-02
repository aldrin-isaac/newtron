package labgen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// nodeMAC generates a deterministic, locally-administered MAC address for a
// node based on its index in the topology. The 02: prefix marks it as
// locally administered (IEEE LAA bit set). Each node gets a unique last octet.
func nodeMAC(index int) string {
	return fmt.Sprintf("02:42:f0:ab:%02x:%02x", (index>>8)&0xff, index&0xff)
}

// FabricLinkIP holds the IP assignment for one end of a fabric link.
type FabricLinkIP struct {
	Node      string // node name
	Interface string // SONiC interface name (e.g. "Ethernet0")
	IP        string // IP address with prefix (e.g. "10.1.0.0/31")
	PeerNode  string // node on the other end
	PeerIP    string // peer's IP (without prefix, e.g. "10.1.0.1")
}

// ComputeFabricLinkIPs assigns /31 point-to-point IPs to all inter-switch
// (non-server) links. Returns a map of node → interface → FabricLinkIP.
func ComputeFabricLinkIPs(topo *Topology) map[string]map[string]FabricLinkIP {
	result := make(map[string]map[string]FabricLinkIP)
	linkIdx := 0

	for _, link := range topo.Links {
		if len(link.Endpoints) != 2 {
			continue
		}
		parts0 := strings.SplitN(link.Endpoints[0], ":", 2)
		parts1 := strings.SplitN(link.Endpoints[1], ":", 2)
		if len(parts0) != 2 || len(parts1) != 2 {
			continue
		}
		node0, iface0 := parts0[0], parts0[1]
		node1, iface1 := parts1[0], parts1[1]

		// Skip links involving server nodes
		n0, ok0 := topo.Nodes[node0]
		n1, ok1 := topo.Nodes[node1]
		if !ok0 || !ok1 || n0.Role == "server" || n1.Role == "server" {
			continue
		}

		// Assign /31 pair: base = 10.1.0.(linkIdx*2)
		base := linkIdx * 2
		ip0 := fmt.Sprintf("10.1.0.%d", base)
		ip1 := fmt.Sprintf("10.1.0.%d", base+1)

		if result[node0] == nil {
			result[node0] = make(map[string]FabricLinkIP)
		}
		if result[node1] == nil {
			result[node1] = make(map[string]FabricLinkIP)
		}
		result[node0][iface0] = FabricLinkIP{
			Node: node0, Interface: iface0,
			IP: ip0 + "/31", PeerNode: node1, PeerIP: ip1,
		}
		result[node1][iface1] = FabricLinkIP{
			Node: node1, Interface: iface1,
			IP: ip1 + "/31", PeerNode: node0, PeerIP: ip0,
		}
		linkIdx++
	}
	return result
}

// GenerateStartupConfigs generates a config_db.json for each SONiC node.
// The config contains everything needed for a functional EVPN-VXLAN fabric:
//
//   - DEVICE_METADATA — hostname, ASN, platform identifiers
//   - LOOPBACK_INTERFACE — router-id / VTEP source IP
//   - PORT — physical interface definitions
//   - INTERFACE — fabric link IP assignments (/31 point-to-point)
//   - BGP_GLOBALS — underlay ASN, router-id, route-reflector settings
//   - BGP_GLOBALS_AF — ipv4_unicast + l2vpn_evpn address families
//   - BGP_NEIGHBOR — eBGP underlay peers + iBGP overlay peers
//   - BGP_NEIGHBOR_AF — per-peer address-family activation
//   - ROUTE_REDISTRIBUTE — connected route redistribution into BGP
//   - VXLAN_TUNNEL + VXLAN_EVPN_NVO — VTEP for leaves
//
// BGP design:
//
//	Underlay eBGP on fabric /31 links (unique ASN per device from underlay_as_base).
//	Overlay iBGP via loopback-to-loopback (shared overlay ASN from as_number)
//	with spines as route reflectors carrying the l2vpn_evpn address family.
func GenerateStartupConfigs(topo *Topology, outputDir string) error {
	nodeNames := make([]string, 0, len(topo.Nodes))
	for name := range topo.Nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames)

	hwsku := topo.Defaults.HWSKU
	if hwsku == "" {
		hwsku = "cisco-8101-p4-32x100-vs"
	}

	platform := topo.Defaults.Platform
	if platform == "" {
		platform = "x86_64-kvm_x86_64-r0"
	}

	// Compute fabric link IPs for INTERFACE entries and eBGP peer discovery.
	allLinkIPs := ComputeFabricLinkIPs(topo)

	// Compute per-node underlay ASN: spines share base, each leaf gets base + 1 + index.
	underlayASNs := make(map[string]int)
	overlayASN := topo.Network.ASNumber
	leafIdx := 0
	for _, name := range nodeNames {
		node := topo.Nodes[name]
		if node.Role == "server" {
			continue
		}
		if topo.Network.UnderlayASBase > 0 {
			if node.Role == "spine" {
				underlayASNs[name] = topo.Network.UnderlayASBase
			} else {
				underlayASNs[name] = topo.Network.UnderlayASBase + 1 + leafIdx
				leafIdx++
			}
		} else {
			underlayASNs[name] = overlayASN
		}
	}

	// Collect loopback IPs by role for overlay iBGP mesh.
	var spineLoopbacks []string // sorted
	var leafLoopbacks []string  // sorted
	for _, name := range nodeNames {
		node := topo.Nodes[name]
		if node.LoopbackIP == "" {
			continue
		}
		switch node.Role {
		case "spine":
			spineLoopbacks = append(spineLoopbacks, node.LoopbackIP)
		case "leaf":
			leafLoopbacks = append(leafLoopbacks, node.LoopbackIP)
		}
	}

	nodeIndex := 0
	leafIndex := 0
	for _, nodeName := range nodeNames {
		node := topo.Nodes[nodeName]

		// Server nodes don't run SONiC — skip config_db generation.
		if node.Role == "server" {
			continue
		}

		configDB := make(map[string]map[string]map[string]string)
		bgpASN := underlayASNs[nodeName]

		// DEVICE_METADATA — identity + unified routing mode
		deviceType := "LeafRouter"
		if node.Role == "spine" {
			deviceType = "SpineRouter"
		}
		configDB["DEVICE_METADATA"] = map[string]map[string]string{
			"localhost": {
				"hostname":                   nodeName,
				"hwsku":                      hwsku,
				"platform":                   platform,
				"mac":                        nodeMAC(nodeIndex),
				"type":                       deviceType,
				"bgp_asn":                    fmt.Sprintf("%d", bgpASN),
				"docker_routing_config_mode": "unified",
				"frr_mgmt_framework_config":  "true",
			},
		}

		// LOOPBACK_INTERFACE — router-id and VTEP source IP
		if node.LoopbackIP != "" {
			configDB["LOOPBACK_INTERFACE"] = map[string]map[string]string{
				"Loopback0":                                      {},
				fmt.Sprintf("Loopback0|%s/32", node.LoopbackIP): {},
			}
		}

		// VXLAN_TUNNEL + EVPN NVO — leaf nodes get VTEP for EVPN overlay.
		// Depends on: LOOPBACK_INTERFACE (provides src_ip for VTEP).
		if node.Role == "leaf" && node.LoopbackIP != "" {
			configDB["VXLAN_TUNNEL"] = map[string]map[string]string{
				"vtep1": {"src_ip": node.LoopbackIP},
			}
			configDB["VXLAN_EVPN_NVO"] = map[string]map[string]string{
				"nvo1": {"source_vtep": "vtep1"},
			}
		}

		// PORT entries — all linked interfaces + minimum 8 ports
		addPortEntries(topo, nodeName, configDB)

		// INTERFACE — fabric link IP assignments.
		// Each inter-switch link gets a /31 point-to-point IP.
		nodeLinkIPs := allLinkIPs[nodeName]
		if len(nodeLinkIPs) > 0 {
			intfDB := make(map[string]map[string]string)
			intfNames := sortedMapKeys(nodeLinkIPs)
			for _, intfName := range intfNames {
				lip := nodeLinkIPs[intfName]
				intfDB[intfName] = map[string]string{}
				intfDB[intfName+"|"+lip.IP] = map[string]string{}
			}
			configDB["INTERFACE"] = intfDB
		}

		// BGP_GLOBALS — underlay ASN, router-id, and spine RR settings.
		if node.LoopbackIP != "" {
			bgpGlobals := map[string]string{
				"local_asn":            fmt.Sprintf("%d", bgpASN),
				"router_id":            node.LoopbackIP,
				"ebgp_requires_policy": "false",
				"log_neighbor_changes": "true",
			}
			if node.Role == "spine" {
				bgpGlobals["rr_cluster_id"] = node.LoopbackIP
				bgpGlobals["load_balance_mp_relax"] = "true"
			}
			configDB["BGP_GLOBALS"] = map[string]map[string]string{
				"default": bgpGlobals,
			}

			// BGP_GLOBALS_AF — ipv4 unicast + l2vpn EVPN with advertise-all-vni.
			configDB["BGP_GLOBALS_AF"] = map[string]map[string]string{
				"default|ipv4_unicast": {},
				"default|l2vpn_evpn":  {"advertise-all-vni": "true"},
			}
		}

		// BGP_NEIGHBOR + BGP_NEIGHBOR_AF
		bgpNeighbors := make(map[string]map[string]string)
		bgpNeighborAFs := make(map[string]map[string]string)

		// Underlay eBGP peers — one per fabric link, using interface IPs.
		if len(nodeLinkIPs) > 0 {
			intfNames := sortedMapKeys(nodeLinkIPs)
			for _, intfName := range intfNames {
				lip := nodeLinkIPs[intfName]
				peerASN := underlayASNs[lip.PeerNode]
				bgpNeighbors["default|"+lip.PeerIP] = map[string]string{
					"asn":          fmt.Sprintf("%d", peerASN),
					"local_asn":    fmt.Sprintf("%d", bgpASN),
					"admin_status": "up",
				}
				bgpNeighborAFs["default|"+lip.PeerIP+"|ipv4_unicast"] = map[string]string{
					"activate": "true",
				}
			}
		}

		// Overlay iBGP peers — loopback-to-loopback with local_asn override
		// to the overlay ASN. Leaves peer with all spines; spines peer with
		// all leaves (as route-reflector clients).
		if node.LoopbackIP != "" {
			if node.Role == "leaf" {
				for _, spineIP := range spineLoopbacks {
					bgpNeighbors["default|"+spineIP] = map[string]string{
						"asn":          fmt.Sprintf("%d", overlayASN),
						"local_addr":   node.LoopbackIP,
						"local_asn":    fmt.Sprintf("%d", overlayASN),
						"admin_status": "up",
					}
					bgpNeighborAFs["default|"+spineIP+"|ipv4_unicast"] = map[string]string{
						"activate": "true",
					}
					bgpNeighborAFs["default|"+spineIP+"|l2vpn_evpn"] = map[string]string{
						"activate": "true",
					}
				}
			} else if node.Role == "spine" {
				for _, leafIP := range leafLoopbacks {
					bgpNeighbors["default|"+leafIP] = map[string]string{
						"asn":          fmt.Sprintf("%d", overlayASN),
						"local_addr":   node.LoopbackIP,
						"local_asn":    fmt.Sprintf("%d", overlayASN),
						"admin_status": "up",
					}
					rrFields := map[string]string{
						"activate":              "true",
						"route_reflector_client": "true",
						"next_hop_self":          "true",
					}
					bgpNeighborAFs["default|"+leafIP+"|ipv4_unicast"] = copyMap(rrFields)
					bgpNeighborAFs["default|"+leafIP+"|l2vpn_evpn"] = copyMap(rrFields)
				}
			}
		}

		if len(bgpNeighbors) > 0 {
			configDB["BGP_NEIGHBOR"] = bgpNeighbors
		}
		if len(bgpNeighborAFs) > 0 {
			configDB["BGP_NEIGHBOR_AF"] = bgpNeighborAFs
		}

		// ROUTE_REDISTRIBUTE — connected routes (loopback + fabric link subnets) into BGP.
		configDB["ROUTE_REDISTRIBUTE"] = map[string]map[string]string{
			"default|connected|bgp|ipv4": {},
		}

		// Write config_db.json
		nodeDir := filepath.Join(outputDir, nodeName)
		if err := os.MkdirAll(nodeDir, 0755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", nodeName, err)
		}

		data, err := json.MarshalIndent(configDB, "", "  ")
		if err != nil {
			return fmt.Errorf("marshalling config_db for %s: %w", nodeName, err)
		}

		path := filepath.Join(nodeDir, "config_db.json")
		if err := os.WriteFile(path, data, 0644); err != nil {
			return fmt.Errorf("writing config_db for %s: %w", nodeName, err)
		}

		if node.Role != "spine" {
			leafIndex++
		}
		nodeIndex++
	}

	return nil
}

// GenerateMinimalStartupConfigs is an alias for GenerateStartupConfigs.
// Deprecated: use GenerateStartupConfigs directly.
func GenerateMinimalStartupConfigs(topo *Topology, outputDir string) error {
	return GenerateStartupConfigs(topo, outputDir)
}

// sortedMapKeys returns the keys of a map[string]FabricLinkIP sorted alphabetically.
func sortedMapKeys(m map[string]FabricLinkIP) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// copyMap returns a shallow copy of a string map.
func copyMap(m map[string]string) map[string]string {
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func addPortEntries(topo *Topology, nodeName string, configDB map[string]map[string]map[string]string) {
	if configDB["PORT"] == nil {
		configDB["PORT"] = make(map[string]map[string]string)
	}

	// Add PORT entries for interfaces that appear in links
	ifaces := NodeInterfaces(topo, nodeName)
	sort.Strings(ifaces)

	for _, iface := range ifaces {
		if _, exists := configDB["PORT"][iface]; !exists {
			configDB["PORT"][iface] = map[string]string{
				"admin_status": "up",
				"mtu":          "9100",
				"speed":        "40000",
			}
		}
	}

	// Ensure at least 8 ports exist per node (Ethernet0-7) so that
	// E2E tests have free interfaces beyond the ones used for inter-node links.
	for i := 0; i < 8; i++ {
		portName := fmt.Sprintf("Ethernet%d", i)
		if _, exists := configDB["PORT"][portName]; !exists {
			configDB["PORT"][portName] = map[string]string{
				"admin_status": "up",
				"mtu":          "9100",
				"speed":        "40000",
			}
		}
	}
}

// SonicIfaceToClabIface converts a SONiC interface name (e.g. "Ethernet0")
// to a containerlab interface name (e.g. "eth1").
// SONiC Ethernet numbering is 0-based, containerlab eth numbering is 1-based.
func SonicIfaceToClabIface(sonicName string) string {
	if !strings.HasPrefix(sonicName, "Ethernet") {
		return sonicName
	}
	numStr := strings.TrimPrefix(sonicName, "Ethernet")
	var num int
	fmt.Sscanf(numStr, "%d", &num)
	return fmt.Sprintf("eth%d", num+1)
}
