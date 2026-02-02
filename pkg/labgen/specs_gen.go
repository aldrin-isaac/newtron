package labgen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// GenerateLabSpecs generates newtron spec files (network, site, platforms, profiles)
// for the lab topology.
func GenerateLabSpecs(topo *Topology, outputDir string) error {
	specsDir := filepath.Join(outputDir, "specs")
	profilesDir := filepath.Join(specsDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0755); err != nil {
		return fmt.Errorf("creating specs directories: %w", err)
	}

	if err := generateNetworkSpec(topo, specsDir); err != nil {
		return err
	}
	if err := generateSiteSpec(topo, specsDir); err != nil {
		return err
	}
	if err := generatePlatformsSpec(topo, specsDir); err != nil {
		return err
	}
	if err := generateProfiles(topo, profilesDir); err != nil {
		return err
	}

	return nil
}

func generateNetworkSpec(topo *Topology, specsDir string) error {
	// Build a minimal network spec with the lab's region and AS number
	spec := map[string]interface{}{
		"version":    "1.0",
		"lock_dir":   "/tmp/newtron-lab-locks",
		"super_users": []string{"labuser"},
		"user_groups": map[string][]string{
			"neteng": {"labuser"},
		},
		"permissions": map[string][]string{
			"all": {"neteng"},
		},
		"generic_alias": map[string]string{},
		"regions": map[string]interface{}{
			topo.Network.Region: map[string]interface{}{
				"as_number": topo.Network.ASNumber,
				"as_name":   topo.Network.Region,
			},
		},
		"prefix_lists": map[string][]string{},
		"filter_specs": map[string]interface{}{
			"customer-l3-in": map[string]interface{}{
				"description": "Ingress filter for customer L3",
				"type":        "L3",
				"rules": []map[string]interface{}{
					{"seq": 100, "action": "deny", "src_ip": "0.0.0.0/8"},
					{"seq": 9999, "action": "permit"},
				},
			},
			"customer-l3-out": map[string]interface{}{
				"description": "Egress filter for customer L3",
				"type":        "L3",
				"rules": []map[string]interface{}{
					{"seq": 9999, "action": "permit"},
				},
			},
		},
		"policers": map[string]interface{}{},
		"ipvpn": map[string]interface{}{
			"customer-vpn": map[string]interface{}{
				"description": "Customer L3 VPN",
				"l3_vni":      10001,
				"import_rt":   []string{fmt.Sprintf("%d:10001", topo.Network.ASNumber)},
				"export_rt":   []string{fmt.Sprintf("%d:10001", topo.Network.ASNumber)},
			},
			"server-vpn": map[string]interface{}{
				"description": "Server shared VRF",
				"l3_vni":      10100,
				"import_rt":   []string{fmt.Sprintf("%d:10100", topo.Network.ASNumber)},
				"export_rt":   []string{fmt.Sprintf("%d:10100", topo.Network.ASNumber)},
			},
		},
		"macvpn": map[string]interface{}{
			"servers-vlan100": map[string]interface{}{
				"description":     "Server VLAN 100 L2 extension",
				"vlan":            100,
				"l2_vni":          10100,
				"arp_suppression": true,
			},
		},
		"services": map[string]interface{}{
			"fabric-underlay": map[string]interface{}{
				"description":  "Fabric underlay point-to-point link with iBGP",
				"service_type": "l3",
				"routing": map[string]interface{}{
					"protocol": "bgp",
					"peer_as":  "request",
				},
			},
			"customer-l3": map[string]interface{}{
				"description":    "L3 routed customer interface",
				"service_type":   "l3",
				"vrf_type":       "interface",
				"ipvpn":          "customer-vpn",
				"ingress_filter": "customer-l3-in",
				"egress_filter":  "customer-l3-out",
			},
			"server-irb": map[string]interface{}{
				"description":     "IRB service with shared VRF and anycast gateway",
				"service_type":    "irb",
				"vrf_type":        "shared",
				"ipvpn":           "server-vpn",
				"macvpn":          "servers-vlan100",
				"anycast_gateway": "10.1.100.1/24",
				"anycast_mac":     "00:00:00:01:02:03",
			},
			"l2-extend": map[string]interface{}{
				"description":  "Pure L2 VLAN extension",
				"service_type": "l2",
				"macvpn":       "servers-vlan100",
			},
		},
	}

	return writeJSON(filepath.Join(specsDir, "network.json"), spec)
}

func generateSiteSpec(topo *Topology, specsDir string) error {
	siteName := topo.Defaults.Site
	if siteName == "" {
		siteName = topo.Name + "-site"
	}

	// Spine nodes are route reflectors
	var rrs []string
	names := sortedNodeNames(topo)
	for _, name := range names {
		if topo.Nodes[name].Role == "spine" {
			rrs = append(rrs, name)
		}
	}

	spec := map[string]interface{}{
		"version": "1.0",
		"sites": map[string]interface{}{
			siteName: map[string]interface{}{
				"region":           topo.Network.Region,
				"route_reflectors": rrs,
			},
		},
	}

	return writeJSON(filepath.Join(specsDir, "site.json"), spec)
}

func generatePlatformsSpec(topo *Topology, specsDir string) error {
	platformName := topo.Defaults.Platform
	if platformName == "" {
		platformName = "vs-platform"
	}

	hwsku := topo.Defaults.HWSKU
	if hwsku == "" {
		hwsku = "cisco-8101-p4-32x100-vs"
	}

	spec := map[string]interface{}{
		"version": "1.0",
		"platforms": map[string]interface{}{
			platformName: map[string]interface{}{
				"hwsku":         hwsku,
				"description":   "Virtual SONiC platform for containerlab",
				"port_count":    32,
				"default_speed": "40000",
			},
		},
	}

	return writeJSON(filepath.Join(specsDir, "platforms.json"), spec)
}

func generateProfiles(topo *Topology, profilesDir string) error {
	siteName := topo.Defaults.Site
	if siteName == "" {
		siteName = topo.Name + "-site"
	}

	platformName := topo.Defaults.Platform
	if platformName == "" {
		platformName = "vs-platform"
	}

	names := sortedNodeNames(topo)
	nodeIndex := 0
	leafIndex := 0
	for _, name := range names {
		node := topo.Nodes[name]

		// Server nodes don't get newtron profiles
		if node.Role == "server" {
			continue
		}

		platform := node.Platform
		if platform == "" {
			platform = platformName
		}

		profile := map[string]interface{}{
			"mgmt_ip":     "PLACEHOLDER",
			"loopback_ip": node.LoopbackIP,
			"site":        siteName,
			"platform":    platform,
			"mac":         nodeMAC(nodeIndex),
		}

		if node.Role == "spine" {
			profile["is_route_reflector"] = true
		}

		// Underlay ASN: spines share base, each leaf gets base + 1 + leafIndex
		if topo.Network.UnderlayASBase > 0 {
			if node.Role == "spine" {
				profile["underlay_asn"] = topo.Network.UnderlayASBase
			} else {
				profile["underlay_asn"] = topo.Network.UnderlayASBase + 1 + leafIndex
				leafIndex++
			}
		}

		// Preserve runtime-patched values from existing profile
		profilePath := filepath.Join(profilesDir, name+".json")
		if existing, err := readExistingProfile(profilePath); err == nil {
			if ip, ok := existing["mgmt_ip"].(string); ok && ip != "PLACEHOLDER" && ip != "" {
				profile["mgmt_ip"] = ip
			}
			if user, ok := existing["ssh_user"].(string); ok && user != "" {
				profile["ssh_user"] = user
			}
			if pass, ok := existing["ssh_pass"].(string); ok && pass != "" {
				profile["ssh_pass"] = pass
			}
		}

		if err := writeJSON(profilePath, profile); err != nil {
			return err
		}

		nodeIndex++
	}

	return nil
}

func readExistingProfile(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var profile map[string]interface{}
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, err
	}
	return profile, nil
}

func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling JSON for %s: %w", filepath.Base(path), err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}
	return nil
}

func sortedNodeNames(topo *Topology) []string {
	names := make([]string, 0, len(topo.Nodes))
	for name := range topo.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
