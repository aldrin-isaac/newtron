package labgen

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// GenerateTopologySpec converts the labgen YAML topology into a newtron
// TopologySpecFile (topology.json) for use by the TopologyProvisioner.
//
// It computes fabric link IPs and creates topology entries for each SONiC node
// with fabric-underlay service bindings. Server-facing interfaces are excluded
// (they will be provisioned manually later).
func GenerateTopologySpec(topo *Topology, outputDir string) error {
	specsDir := filepath.Join(outputDir, "specs")

	// Compute fabric link IPs for all inter-switch links
	allLinkIPs := ComputeFabricLinkIPs(topo)

	devices := make(map[string]interface{})
	var links []map[string]string

	names := sortedNodeNames(topo)

	// Build underlay ASN map: spines share one ASN (base), each leaf
	// gets a unique ASN (base + 1 + leafIndex). This follows the standard
	// RFC 7938 Clos convention: unique ASN per leaf, shared ASN per tier.
	underlayASNs := make(map[string]int)
	if topo.Network.UnderlayASBase > 0 {
		leafIdx := 0
		for _, name := range names {
			node := topo.Nodes[name]
			if node.Role == "server" {
				continue
			}
			if node.Role == "spine" {
				underlayASNs[name] = topo.Network.UnderlayASBase
			} else {
				underlayASNs[name] = topo.Network.UnderlayASBase + 1 + leafIdx
				leafIdx++
			}
		}
	} else {
		for _, name := range names {
			if topo.Nodes[name].Role != "server" {
				underlayASNs[name] = topo.Network.ASNumber
			}
		}
	}

	for _, nodeName := range names {
		node := topo.Nodes[nodeName]

		// Only SONiC nodes (spine/leaf) get topology entries
		if node.Role == "server" {
			continue
		}

		isSpine := node.Role == "spine"

		dev := map[string]interface{}{}

		// Device config for spines (route reflectors)
		if isSpine {
			dev["device_config"] = map[string]interface{}{
				"route_reflector": true,
			}
		}

		// Build interfaces from fabric link IPs
		interfaces := make(map[string]interface{})
		nodeLinkIPs := allLinkIPs[nodeName]

		// Sort interface names for deterministic output
		intfNames := make([]string, 0, len(nodeLinkIPs))
		for intfName := range nodeLinkIPs {
			intfNames = append(intfNames, intfName)
		}
		sort.Strings(intfNames)

		for _, intfName := range intfNames {
			lip := nodeLinkIPs[intfName]

			intf := map[string]interface{}{
				"service": "fabric-underlay",
				"ip":      lip.IP,
				"link":    fmt.Sprintf("%s:%s", lip.PeerNode, peerInterfaceName(allLinkIPs, lip.PeerNode, nodeName, intfName)),
				"params": map[string]string{
					"peer_as": fmt.Sprintf("%d", underlayASNs[lip.PeerNode]),
				},
			}

			// Spines get additional RR params for their leaf-facing interfaces
			if isSpine {
				intf["params"] = map[string]string{
					"peer_as":                fmt.Sprintf("%d", underlayASNs[lip.PeerNode]),
					"route_reflector_client": "true",
					"next_hop_self":          "true",
				}
			}

			interfaces[intfName] = intf
		}

		if len(interfaces) > 0 {
			dev["interfaces"] = interfaces
		}
		devices[nodeName] = dev
	}

	// Build topology links from YAML links (inter-switch only)
	for _, link := range topo.Links {
		if len(link.Endpoints) != 2 {
			continue
		}
		parts0 := strings.SplitN(link.Endpoints[0], ":", 2)
		parts1 := strings.SplitN(link.Endpoints[1], ":", 2)
		if len(parts0) != 2 || len(parts1) != 2 {
			continue
		}

		// Skip links involving server nodes
		n0, ok0 := topo.Nodes[parts0[0]]
		n1, ok1 := topo.Nodes[parts1[0]]
		if !ok0 || !ok1 || n0.Role == "server" || n1.Role == "server" {
			continue
		}

		links = append(links, map[string]string{
			"a": link.Endpoints[0],
			"z": link.Endpoints[1],
		})
	}

	topoSpec := map[string]interface{}{
		"version":     "1.0",
		"description": fmt.Sprintf("Generated from %s topology", topo.Name),
		"devices":     devices,
		"links":       links,
	}

	return writeJSON(filepath.Join(specsDir, "topology.json"), topoSpec)
}

// peerInterfaceName finds the interface name on peerNode that connects back
// to localNode. This is used to populate the "link" field in topology entries.
func peerInterfaceName(allLinkIPs map[string]map[string]FabricLinkIP, peerNode, localNode, localIntf string) string {
	if peerLinks, ok := allLinkIPs[peerNode]; ok {
		for intfName, lip := range peerLinks {
			if lip.PeerNode == localNode {
				return intfName
			}
		}
	}
	// Fallback: return localIntf (shouldn't happen with valid topology)
	return localIntf
}
