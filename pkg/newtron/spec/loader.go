package spec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/newtron-network/newtron/pkg/util"
)

// SpecDir is the default specification directory
var SpecDir = "/etc/newtron"

// Loader handles loading and validating specification files
type Loader struct {
	specDir   string
	network   *NetworkSpecFile
	platforms *PlatformSpecFile
	topology  *TopologySpecFile // nil if topology.json doesn't exist
	profiles  map[string]*DeviceProfile
}

// NewLoader creates a new specification loader
func NewLoader(specDir string) *Loader {
	if specDir == "" {
		specDir = SpecDir
	}
	return &Loader{
		specDir:  specDir,
		profiles: make(map[string]*DeviceProfile),
	}
}

// Load loads all specification files
func (l *Loader) Load() error {
	var err error

	// Load network spec
	l.network, err = l.loadNetworkSpec()
	if err != nil {
		return fmt.Errorf("loading network spec: %w", err)
	}

	// Load platform spec
	l.platforms, err = l.loadPlatformSpec()
	if err != nil {
		return fmt.Errorf("loading platform spec: %w", err)
	}

	// Load topology spec (optional — returns nil if file doesn't exist)
	l.topology, err = l.loadTopologySpec()
	if err != nil {
		return fmt.Errorf("loading topology spec: %w", err)
	}

	// Validate cross-references
	if err := l.validate(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// Validate topology references (if loaded)
	if l.topology != nil {
		if err := l.validateTopology(); err != nil {
			return fmt.Errorf("topology validation failed: %w", err)
		}
	}

	return nil
}

// LoadProfile loads a device profile
func (l *Loader) LoadProfile(deviceName string) (*DeviceProfile, error) {
	if profile, ok := l.profiles[deviceName]; ok {
		return profile, nil
	}

	path := filepath.Join(l.specDir, "profiles", deviceName+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading profile %s: %w", deviceName, err)
	}

	var profile DeviceProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, fmt.Errorf("parsing profile %s: %w", deviceName, err)
	}

	// Validate profile
	if err := l.validateProfile(&profile); err != nil {
		return nil, fmt.Errorf("validating profile %s: %w", deviceName, err)
	}

	l.profiles[deviceName] = &profile
	return &profile, nil
}

func (l *Loader) loadNetworkSpec() (*NetworkSpecFile, error) {
	path := filepath.Join(l.specDir, "network.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var spec NetworkSpecFile
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}

	return &spec, nil
}

func (l *Loader) loadPlatformSpec() (*PlatformSpecFile, error) {
	path := filepath.Join(l.specDir, "platforms.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var spec PlatformSpecFile
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}

	return &spec, nil
}

func (l *Loader) validate() error {
	v := &util.ValidationBuilder{}

	// Validate QoS policies (network-level)
	l.validateQoSPolicies(v)

	// Validate network-level cross-references against network-level maps
	l.validateSpecRefs(v, "", &l.network.OverridableSpecs, &l.network.OverridableSpecs)

	// Validate zone-level cross-references against merged (zone + network) maps
	for zoneName, zone := range l.network.Zones {
		merged := mergeOverridableSpecs(&l.network.OverridableSpecs, &zone.OverridableSpecs)
		l.validateSpecRefs(v, "zone '"+zoneName+"': ", &zone.OverridableSpecs, merged)
	}

	return v.Build()
}

// mergeOverridableSpecs merges parent and child spec maps (child wins).
func mergeOverridableSpecs(parent, child *OverridableSpecs) *OverridableSpecs {
	return &OverridableSpecs{
		PrefixLists:   util.MergeMaps(parent.PrefixLists, child.PrefixLists),
		Filters:       util.MergeMaps(parent.Filters, child.Filters),
		Services:      util.MergeMaps(parent.Services, child.Services),
		IPVPNs:        util.MergeMaps(parent.IPVPNs, child.IPVPNs),
		MACVPNs:       util.MergeMaps(parent.MACVPNs, child.MACVPNs),
		QoSPolicies:   util.MergeMaps(parent.QoSPolicies, child.QoSPolicies),
		QoSProfiles:   util.MergeMaps(parent.QoSProfiles, child.QoSProfiles),
		RoutePolicies: util.MergeMaps(parent.RoutePolicies, child.RoutePolicies),
	}
}

// validateSpecRefs validates cross-references within the specs being checked
// (own) against the resolved set (resolved, which includes parent maps).
// The prefix is prepended to error messages (e.g. "zone 'amer': ").
func (l *Loader) validateSpecRefs(v *util.ValidationBuilder, prefix string, own, resolved *OverridableSpecs) {
	// Validate services reference existing specs in the resolved set
	for svcName, svc := range own.Services {
		if svc.IngressFilter != "" {
			if _, ok := resolved.Filters[svc.IngressFilter]; !ok {
				v.AddErrorf("%sservice '%s' references unknown ingress filter '%s'", prefix, svcName, svc.IngressFilter)
			}
		}
		if svc.EgressFilter != "" {
			if _, ok := resolved.Filters[svc.EgressFilter]; !ok {
				v.AddErrorf("%sservice '%s' references unknown egress filter '%s'", prefix, svcName, svc.EgressFilter)
			}
		}
		if svc.QoSPolicy != "" {
			if _, ok := resolved.QoSPolicies[svc.QoSPolicy]; !ok {
				v.AddErrorf("%sservice '%s' references unknown QoS policy '%s'", prefix, svcName, svc.QoSPolicy)
			}
		}
		if svc.QoSProfile != "" {
			if _, ok := resolved.QoSProfiles[svc.QoSProfile]; !ok {
				v.AddErrorf("%sservice '%s' references unknown QoS profile '%s'", prefix, svcName, svc.QoSProfile)
			}
		}
		if svc.IPVPN != "" {
			if _, ok := resolved.IPVPNs[svc.IPVPN]; !ok {
				v.AddErrorf("%sservice '%s' references unknown ipvpn '%s'", prefix, svcName, svc.IPVPN)
			}
		}
		if svc.MACVPN != "" {
			if _, ok := resolved.MACVPNs[svc.MACVPN]; !ok {
				v.AddErrorf("%sservice '%s' references unknown macvpn '%s'", prefix, svcName, svc.MACVPN)
			}
		}
		// Validate service type constraints
		switch svc.ServiceType {
		case ServiceTypeEVPNIRB:
			if svc.IPVPN == "" {
				v.AddErrorf("%sservice '%s' (evpn-irb) requires ipvpn reference", prefix, svcName)
			}
			if svc.MACVPN == "" {
				v.AddErrorf("%sservice '%s' (evpn-irb) requires macvpn reference", prefix, svcName)
			}
		case ServiceTypeEVPNBridged:
			if svc.MACVPN == "" {
				v.AddErrorf("%sservice '%s' (evpn-bridged) requires macvpn reference", prefix, svcName)
			}
		case ServiceTypeEVPNRouted:
			if svc.IPVPN == "" {
				v.AddErrorf("%sservice '%s' (evpn-routed) requires ipvpn reference", prefix, svcName)
			}
		case ServiceTypeIRB, ServiceTypeBridged, ServiceTypeRouted:
			// Local types: no spec-level refs required
		default:
			v.AddErrorf("%sservice '%s' has unknown type '%s'", prefix, svcName, svc.ServiceType)
		}
	}

	// Validate filter rules reference existing prefix lists in the resolved set
	for specName, filterSpec := range own.Filters {
		for i, rule := range filterSpec.Rules {
			if rule.SrcPrefixList != "" {
				if _, ok := resolved.PrefixLists[rule.SrcPrefixList]; !ok {
					v.AddErrorf("%sfilter '%s' rule %d references unknown src prefix list '%s'",
						prefix, specName, i, rule.SrcPrefixList)
				}
			}
			if rule.DstPrefixList != "" {
				if _, ok := resolved.PrefixLists[rule.DstPrefixList]; !ok {
					v.AddErrorf("%sfilter '%s' rule %d references unknown dst prefix list '%s'",
						prefix, specName, i, rule.DstPrefixList)
				}
			}
		}
	}
}

// validateQoSPolicies validates all QoS policy definitions.
func (l *Loader) validateQoSPolicies(v *util.ValidationBuilder) {
	for name, policy := range l.network.QoSPolicies {
		if len(policy.Queues) == 0 {
			v.AddErrorf("QoS policy '%s' has no queues", name)
			continue
		}
		if len(policy.Queues) > 8 {
			v.AddErrorf("QoS policy '%s' has %d queues (max 8)", name, len(policy.Queues))
			continue
		}

		seenDSCP := make(map[int]string)   // DSCP value → queue name (for dup detection)
		seenNames := make(map[string]bool)  // queue name uniqueness

		for i, q := range policy.Queues {
			if q.Name == "" {
				v.AddErrorf("QoS policy '%s' queue[%d] has empty name", name, i)
			} else if seenNames[q.Name] {
				v.AddErrorf("QoS policy '%s' has duplicate queue name '%s'", name, q.Name)
			}
			seenNames[q.Name] = true

			switch q.Type {
			case "dwrr":
				if q.Weight <= 0 {
					v.AddErrorf("QoS policy '%s' queue '%s': DWRR requires weight > 0", name, q.Name)
				}
			case "strict":
				if q.Weight != 0 {
					v.AddErrorf("QoS policy '%s' queue '%s': strict queue must not have weight", name, q.Name)
				}
			default:
				v.AddErrorf("QoS policy '%s' queue '%s': invalid type '%s' (must be dwrr or strict)", name, q.Name, q.Type)
			}

			for _, dscp := range q.DSCP {
				if dscp < 0 || dscp > 63 {
					v.AddErrorf("QoS policy '%s' queue '%s': DSCP value %d out of range (0-63)", name, q.Name, dscp)
				} else if prev, dup := seenDSCP[dscp]; dup {
					v.AddErrorf("QoS policy '%s': DSCP %d mapped to both '%s' and '%s'", name, dscp, prev, q.Name)
				}
				seenDSCP[dscp] = q.Name
			}
		}
	}
}

// isHostDevice returns true if the named device uses a host platform.
func (l *Loader) isHostDevice(deviceName string) bool {
	profile, ok := l.profiles[deviceName]
	if !ok {
		// Try loading — may not be cached yet during topology validation
		profilePath := filepath.Join(l.specDir, "profiles", deviceName+".json")
		data, err := os.ReadFile(profilePath)
		if err != nil {
			return false
		}
		var p DeviceProfile
		if err := json.Unmarshal(data, &p); err != nil {
			return false
		}
		profile = &p
	}
	return l.isHostPlatform(profile.Platform)
}

// isHostPlatform returns true if the given platform name refers to a host device type.
func (l *Loader) isHostPlatform(platformName string) bool {
	if l.platforms == nil || platformName == "" {
		return false
	}
	platform, ok := l.platforms.Platforms[platformName]
	if !ok {
		return false
	}
	return platform.IsHost()
}

func (l *Loader) validateProfile(profile *DeviceProfile) error {
	v := &util.ValidationBuilder{}

	// Host devices have relaxed validation — only mgmt_ip is required
	if l.isHostPlatform(profile.Platform) {
		v.Add(profile.MgmtIP != "", "mgmt_ip is required")
		if profile.MgmtIP != "" && !util.IsValidIPv4(profile.MgmtIP) {
			v.AddErrorf("invalid management IP: %s", profile.MgmtIP)
		}
		return v.Build()
	}

	// Required fields for switch devices
	v.Add(profile.MgmtIP != "", "mgmt_ip is required")
	v.Add(profile.LoopbackIP != "", "loopback_ip is required")
	v.Add(profile.Zone != "", "zone is required")

	// Validate IP addresses
	if profile.MgmtIP != "" && !util.IsValidIPv4(profile.MgmtIP) {
		v.AddErrorf("invalid management IP: %s", profile.MgmtIP)
	}
	if profile.LoopbackIP != "" && !util.IsValidIPv4(profile.LoopbackIP) {
		v.AddErrorf("invalid loopback IP: %s", profile.LoopbackIP)
	}

	// Validate zone exists in network.json
	if profile.Zone != "" {
		if _, ok := l.network.Zones[profile.Zone]; !ok {
			v.AddErrorf("unknown zone: %s", profile.Zone)
		}
	}

	// Validate AS number if specified
	if profile.ASNumber != nil {
		if err := util.ValidateASN(*profile.ASNumber); err != nil {
			v.AddError(err.Error())
		}
	}

	return v.Build()
}

// GetNetwork returns the network spec
func (l *Loader) GetNetwork() *NetworkSpecFile {
	return l.network
}

// GetPlatforms returns the platform spec
func (l *Loader) GetPlatforms() *PlatformSpecFile {
	return l.platforms
}

// GetService returns a service definition by name
func (l *Loader) GetService(name string) (*ServiceSpec, error) {
	svc, ok := l.network.Services[name]
	if !ok {
		return nil, fmt.Errorf("service '%s' not found", name)
	}
	return svc, nil
}

// GetFilter returns a filter spec by name
func (l *Loader) GetFilter(name string) (*FilterSpec, error) {
	spec, ok := l.network.Filters[name]
	if !ok {
		return nil, fmt.Errorf("filter spec '%s' not found", name)
	}
	return spec, nil
}

// GetPrefixList returns a prefix list by name
func (l *Loader) GetPrefixList(name string) ([]string, error) {
	list, ok := l.network.PrefixLists[name]
	if !ok {
		return nil, fmt.Errorf("prefix list '%s' not found", name)
	}
	return list, nil
}

// ListServices returns all service names, sorted for deterministic output.
func (l *Loader) ListServices() []string {
	names := make([]string, 0, len(l.network.Services))
	for name := range l.network.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetTopology returns the topology spec, or nil if no topology.json was found.
func (l *Loader) GetTopology() *TopologySpecFile {
	return l.topology
}

// SaveNetwork writes the network spec to disk atomically (temp file + rename).
func (l *Loader) SaveNetwork(spec *NetworkSpecFile) error {
	path := filepath.Join(l.specDir, "network.json")

	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling network spec: %w", err)
	}
	data = append(data, '\n')

	// Write to temp file in the same directory (ensures same filesystem for atomic rename)
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "network-*.json.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming temp file: %w", err)
	}

	// Update the in-memory copy
	l.network = spec

	return nil
}

func (l *Loader) loadTopologySpec() (*TopologySpecFile, error) {
	path := filepath.Join(l.specDir, "topology.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // topology.json is optional
		}
		return nil, err
	}

	var spec TopologySpecFile
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}

	return &spec, nil
}

// validateTopology validates that all references in the topology spec are resolvable.
func (l *Loader) validateTopology() error {
	v := &util.ValidationBuilder{}

	for deviceName, device := range l.topology.Devices {
		// All device names must have profiles in profiles/
		profilePath := filepath.Join(l.specDir, "profiles", deviceName+".json")
		if _, err := os.Stat(profilePath); os.IsNotExist(err) {
			v.AddErrorf("topology device '%s' has no profile at %s", deviceName, profilePath)
		}

		// Skip service/interface validation for host devices —
		// they have no SONiC services, zones, or loopback IPs
		if l.isHostDevice(deviceName) {
			continue
		}

		for intfName, intf := range device.Interfaces {
			// All service names must exist in network.json services
			if intf.Service != "" {
				if _, ok := l.network.Services[intf.Service]; !ok {
					v.AddErrorf("topology device '%s' interface '%s' references unknown service '%s'",
						deviceName, intfName, intf.Service)
				}
			}

			// IP addresses must be valid CIDR
			if intf.IP != "" {
				if !util.IsValidIPv4CIDR(intf.IP) {
					v.AddErrorf("topology device '%s' interface '%s' has invalid IP '%s'",
						deviceName, intfName, intf.IP)
				}
			}
		}
	}

	// Derive links from interface.link fields if not explicitly provided
	if len(l.topology.Links) == 0 {
		l.topology.Links = DeriveLinksFromInterfaces(l.topology)
		util.Logger.Infof("spec: derived %d links from interface.link fields", len(l.topology.Links))
	}

	// Validate links: both endpoints must be defined in their respective device interfaces
	for i, link := range l.topology.Links {
		l.validateLinkEndpoint(v, i, "a", link.A)
		l.validateLinkEndpoint(v, i, "z", link.Z)
	}

	return v.Build()
}

func (l *Loader) validateLinkEndpoint(v *util.ValidationBuilder, linkIdx int, side, endpoint string) {
	// endpoint format: "device:interface"
	parts := splitEndpoint(endpoint)
	if len(parts) != 2 {
		v.AddErrorf("link[%d].%s: invalid endpoint format '%s' (expected 'device:interface')",
			linkIdx, side, endpoint)
		return
	}
	deviceName, intfName := parts[0], parts[1]
	device, ok := l.topology.Devices[deviceName]
	if !ok {
		v.AddErrorf("link[%d].%s: device '%s' not found in topology", linkIdx, side, deviceName)
		return
	}
	if _, ok := device.Interfaces[intfName]; !ok {
		v.AddErrorf("link[%d].%s: interface '%s' not found on device '%s'",
			linkIdx, side, intfName, deviceName)
	}
}

// splitEndpoint splits a "device:interface" string into its components.
func splitEndpoint(endpoint string) []string {
	return strings.SplitN(endpoint, ":", 2)
}
