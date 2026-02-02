package labgen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ClabTopology represents the containerlab topology YAML structure.
type ClabTopology struct {
	Name     string       `yaml:"name"`
	Topology ClabTopoSpec `yaml:"topology"`
}

// ClabTopoSpec contains the nodes and links sections.
type ClabTopoSpec struct {
	Nodes map[string]*ClabNode `yaml:"nodes"`
	Links []ClabLink           `yaml:"links"`
}

// ClabNode defines a single containerlab node.
type ClabNode struct {
	Kind          string            `yaml:"kind"`
	Image         string            `yaml:"image"`
	Cmd           string            `yaml:"cmd,omitempty"`
	CPU           int               `yaml:"cpu,omitempty"`
	Memory        string            `yaml:"memory,omitempty"`
	Binds         []string          `yaml:"binds,omitempty"`
	StartupConfig string            `yaml:"startup-config,omitempty"`
	Env           map[string]string `yaml:"env,omitempty"`
	Healthcheck   *ClabHealthcheck  `yaml:"healthcheck,omitempty"`
}

// ClabHealthcheck defines containerlab health check parameters.
type ClabHealthcheck struct {
	StartPeriod int `yaml:"start-period"`
	Interval    int `yaml:"interval"`
	Timeout     int `yaml:"timeout"`
	Retries     int `yaml:"retries"`
}

// ClabLink defines a containerlab link.
type ClabLink struct {
	Endpoints []string `yaml:"endpoints"`
}

// resolveKind determines the containerlab kind for a topology.
// If Kind is explicitly set in defaults, use that.
// Otherwise, auto-detect from the image name.
func resolveKind(topo *Topology) string {
	if topo.Defaults.Kind != "" {
		return topo.Defaults.Kind
	}
	return kindFromImage(topo.Defaults.Image)
}

// kindFromImage detects the containerlab kind from a Docker image name.
func kindFromImage(image string) string {
	lower := strings.ToLower(image)
	if strings.Contains(lower, "vrnetlab") || strings.Contains(lower, "sonic-vm") {
		return "sonic-vm"
	}
	return "sonic-vs"
}

// GenerateClabTopology generates a containerlab YAML file from the topology definition.
func GenerateClabTopology(topo *Topology, outputDir string) error {
	kind := resolveKind(topo)

	clab := ClabTopology{
		Name: topo.Name,
		Topology: ClabTopoSpec{
			Nodes: make(map[string]*ClabNode),
		},
	}

	// For vr-sonic, build per-node interface maps for sequential port assignment
	var nodeIfaceMaps map[string]map[string]string
	if kind == "sonic-vm" {
		nodeIfaceMaps = buildSequentialIfaceMaps(topo)
	}

	// Sort node names for deterministic output
	nodeNames := make([]string, 0, len(topo.Nodes))
	for name := range topo.Nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames)

	for _, name := range nodeNames {
		node := topo.Nodes[name]

		// Server nodes use kind: linux — no SONiC config
		if node.Role == "server" {
			image := node.Image
			if image == "" {
				image = "nicolaka/netshoot:latest"
			}
			cmd := node.Cmd
			if cmd == "" {
				cmd = "sleep infinity"
			}
			clab.Topology.Nodes[name] = &ClabNode{
				Kind:  "linux",
				Image: image,
				Cmd:   cmd,
			}
			continue
		}

		image := node.Image
		if image == "" {
			image = topo.Defaults.Image
		}

		nodeKind := kind
		if node.Image != "" {
			nodeKind = kindFromImage(node.Image)
		}

		clabNode := &ClabNode{
			Kind:  nodeKind,
			Image: image,
		}

		switch nodeKind {
		case "sonic-vm":
			clabNode.StartupConfig = name + "/config_db.json"
			// QEMU tuning: match proven settings from reference lab
			clabNode.CPU = 2
			clabNode.Memory = "6144mib"
			clabNode.Healthcheck = &ClabHealthcheck{
				StartPeriod: 600, // 10 min grace for VM boot
				Interval:    30,
				Timeout:     10,
				Retries:     3,
			}
			// Set credentials and QEMU args
			clabNode.Env = make(map[string]string)
			clabNode.Env["QEMU_ADDITIONAL_ARGS"] = "-cpu host"
			if topo.Defaults.Username != "" {
				clabNode.Env["USERNAME"] = topo.Defaults.Username
			}
			if topo.Defaults.Password != "" {
				clabNode.Env["PASSWORD"] = topo.Defaults.Password
			}
		default: // sonic-vs
			clabNode.StartupConfig = name + "/config_db.json"
		}

		clab.Topology.Nodes[name] = clabNode
	}

	// Convert links: SONiC interface names to containerlab names.
	// Server node interfaces pass through as-is (e.g. "eth1").
	for _, link := range topo.Links {
		if len(link.Endpoints) != 2 {
			continue
		}
		var clabEndpoints []string
		for _, ep := range link.Endpoints {
			parts := strings.SplitN(ep, ":", 2)
			if len(parts) == 2 {
				nodeName := parts[0]
				ifaceName := parts[1]

				// Server nodes use plain Linux interface names — no translation
				if n, ok := topo.Nodes[nodeName]; ok && n.Role == "server" {
					clabEndpoints = append(clabEndpoints, nodeName+":"+ifaceName)
					continue
				}

				var clabIface string
				if kind == "sonic-vm" && nodeIfaceMaps != nil {
					if m, ok := nodeIfaceMaps[nodeName]; ok {
						clabIface = m[ifaceName]
					}
				}
				if clabIface == "" {
					clabIface = SonicIfaceToClabIface(ifaceName)
				}
				clabEndpoints = append(clabEndpoints, nodeName+":"+clabIface)
			}
		}
		clab.Topology.Links = append(clab.Topology.Links, ClabLink{
			Endpoints: clabEndpoints,
		})
	}

	data, err := yaml.Marshal(&clab)
	if err != nil {
		return fmt.Errorf("marshalling containerlab YAML: %w", err)
	}

	path := filepath.Join(outputDir, topo.Name+".clab.yml")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing containerlab YAML: %w", err)
	}

	return nil
}

// buildSequentialIfaceMaps builds per-node maps from SONiC interface names
// to sequential containerlab interface names (eth1, eth2, ...).
// This is needed for vr-sonic where VM NICs are assigned sequentially,
// regardless of SONiC interface numbering (e.g., Ethernet0=eth1, Ethernet4=eth2).
func buildSequentialIfaceMaps(topo *Topology) map[string]map[string]string {
	result := make(map[string]map[string]string)

	for nodeName := range topo.Nodes {
		ifaces := NodeInterfaces(topo, nodeName)
		if len(ifaces) == 0 {
			continue
		}

		// Sort by Ethernet number for deterministic assignment
		sort.Slice(ifaces, func(i, j int) bool {
			return sonicIfaceNum(ifaces[i]) < sonicIfaceNum(ifaces[j])
		})

		m := make(map[string]string, len(ifaces))
		for i, iface := range ifaces {
			m[iface] = fmt.Sprintf("eth%d", i+1)
		}
		result[nodeName] = m
	}

	return result
}

// sonicIfaceNum extracts the numeric index from a SONiC interface name.
func sonicIfaceNum(name string) int {
	if !strings.HasPrefix(name, "Ethernet") {
		return 0
	}
	var num int
	fmt.Sscanf(strings.TrimPrefix(name, "Ethernet"), "%d", &num)
	return num
}
