package labgen

import (
	"fmt"
	"net"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadTopology parses a topology YAML file and validates required fields.
func LoadTopology(path string) (*Topology, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading topology file: %w", err)
	}

	var topo Topology
	if err := yaml.Unmarshal(data, &topo); err != nil {
		return nil, fmt.Errorf("parsing topology YAML: %w", err)
	}

	if err := validateTopology(&topo); err != nil {
		return nil, fmt.Errorf("validating topology: %w", err)
	}

	return &topo, nil
}

func validateTopology(topo *Topology) error {
	if topo.Name == "" {
		return fmt.Errorf("topology name is required")
	}
	if len(topo.Nodes) == 0 {
		return fmt.Errorf("at least one node is required")
	}
	if topo.Defaults.Image == "" {
		return fmt.Errorf("defaults.image is required")
	}
	if topo.Network.ASNumber == 0 {
		return fmt.Errorf("network.as_number is required")
	}
	if topo.Network.Region == "" {
		return fmt.Errorf("network.region is required")
	}

	// Validate nodes
	for name, node := range topo.Nodes {
		if node.Role == "" {
			return fmt.Errorf("node %s: role is required", name)
		}
		if node.Role != "spine" && node.Role != "leaf" && node.Role != "server" {
			return fmt.Errorf("node %s: role must be 'spine', 'leaf', or 'server', got %q", name, node.Role)
		}
		// Server nodes don't need a loopback IP
		if node.Role != "server" {
			if node.LoopbackIP == "" {
				return fmt.Errorf("node %s: loopback_ip is required", name)
			}
			if ip := net.ParseIP(node.LoopbackIP); ip == nil {
				return fmt.Errorf("node %s: invalid loopback_ip %q", name, node.LoopbackIP)
			}
		}
	}

	// Validate links: all endpoints must reference defined nodes
	for i, link := range topo.Links {
		if len(link.Endpoints) != 2 {
			return fmt.Errorf("link %d: must have exactly 2 endpoints", i)
		}
		for _, ep := range link.Endpoints {
			parts := strings.SplitN(ep, ":", 2)
			if len(parts) != 2 {
				return fmt.Errorf("link %d: endpoint %q must be in 'node:interface' format", i, ep)
			}
			nodeName := parts[0]
			if _, ok := topo.Nodes[nodeName]; !ok {
				return fmt.Errorf("link %d: endpoint %q references undefined node %q", i, ep, nodeName)
			}
		}
	}

	return nil
}

// NodeInterfaces returns the set of SONiC interface names used by a node across all links.
func NodeInterfaces(topo *Topology, nodeName string) []string {
	seen := make(map[string]bool)
	var ifaces []string
	for _, link := range topo.Links {
		for _, ep := range link.Endpoints {
			parts := strings.SplitN(ep, ":", 2)
			if parts[0] == nodeName && !seen[parts[1]] {
				seen[parts[1]] = true
				ifaces = append(ifaces, parts[1])
			}
		}
	}
	return ifaces
}
