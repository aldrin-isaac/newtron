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
	site      *SiteSpecFile
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

	// Load site spec
	l.site, err = l.loadSiteSpec()
	if err != nil {
		return fmt.Errorf("loading site spec: %w", err)
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

func (l *Loader) loadSiteSpec() (*SiteSpecFile, error) {
	path := filepath.Join(l.specDir, "site.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var spec SiteSpecFile
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

	// Validate QoS policies
	l.validateQoSPolicies(v)

	// Validate services reference existing filter specs
	for svcName, svc := range l.network.Services {
		if svc.IngressFilter != "" {
			if _, ok := l.network.FilterSpecs[svc.IngressFilter]; !ok {
				v.AddErrorf("service '%s' references unknown ingress filter '%s'", svcName, svc.IngressFilter)
			}
		}
		if svc.EgressFilter != "" {
			if _, ok := l.network.FilterSpecs[svc.EgressFilter]; !ok {
				v.AddErrorf("service '%s' references unknown egress filter '%s'", svcName, svc.EgressFilter)
			}
		}
		if svc.QoSPolicy != "" {
			if l.network.QoSPolicies == nil {
				v.AddErrorf("service '%s' references unknown QoS policy '%s'", svcName, svc.QoSPolicy)
			} else if _, ok := l.network.QoSPolicies[svc.QoSPolicy]; !ok {
				v.AddErrorf("service '%s' references unknown QoS policy '%s'", svcName, svc.QoSPolicy)
			}
		}
		if svc.QoSProfile != "" {
			if _, ok := l.network.QoSProfiles[svc.QoSProfile]; !ok {
				v.AddErrorf("service '%s' references unknown QoS profile '%s'", svcName, svc.QoSProfile)
			}
		}
		// Validate ipvpn reference
		if svc.IPVPN != "" {
			if _, ok := l.network.IPVPN[svc.IPVPN]; !ok {
				v.AddErrorf("service '%s' references unknown ipvpn '%s'", svcName, svc.IPVPN)
			}
		}
		// Validate macvpn reference
		if svc.MACVPN != "" {
			if _, ok := l.network.MACVPN[svc.MACVPN]; !ok {
				v.AddErrorf("service '%s' references unknown macvpn '%s'", svcName, svc.MACVPN)
			}
		}
		// Validate service type constraints
		switch svc.ServiceType {
		case ServiceTypeL2:
			if svc.MACVPN == "" {
				v.AddErrorf("service '%s' has type 'l2' but no macvpn reference", svcName)
			}
		case ServiceTypeL3:
			if svc.IPVPN == "" && svc.VRFType != "" {
				v.AddErrorf("service '%s' has vrf_type but no ipvpn reference", svcName)
			}
		case ServiceTypeIRB:
			if svc.MACVPN == "" {
				v.AddErrorf("service '%s' has type 'irb' but no macvpn reference", svcName)
			}
		}
	}

	// Validate filter rules reference existing prefix lists and policers
	for specName, spec := range l.network.FilterSpecs {
		for i, rule := range spec.Rules {
			if rule.SrcPrefixList != "" {
				if _, ok := l.network.PrefixLists[rule.SrcPrefixList]; !ok {
					v.AddErrorf("filter '%s' rule %d references unknown src prefix list '%s'",
						specName, i, rule.SrcPrefixList)
				}
			}
			if rule.DstPrefixList != "" {
				if _, ok := l.network.PrefixLists[rule.DstPrefixList]; !ok {
					v.AddErrorf("filter '%s' rule %d references unknown dst prefix list '%s'",
						specName, i, rule.DstPrefixList)
				}
			}
			if rule.Policer != "" {
				if _, ok := l.network.Policers[rule.Policer]; !ok {
					v.AddErrorf("filter '%s' rule %d references unknown policer '%s'",
						specName, i, rule.Policer)
				}
			}
		}
	}

	// Validate regions referenced in sites exist
	for siteName, site := range l.site.Sites {
		if _, ok := l.network.Regions[site.Region]; !ok {
			v.AddErrorf("site '%s' references unknown region '%s'", siteName, site.Region)
		}
	}

	return v.Build()
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

func (l *Loader) validateProfile(profile *DeviceProfile) error {
	v := &util.ValidationBuilder{}

	// Required fields
	v.Add(profile.MgmtIP != "", "mgmt_ip is required")
	v.Add(profile.LoopbackIP != "", "loopback_ip is required")
	v.Add(profile.Site != "", "site is required")

	// Validate IP addresses
	if profile.MgmtIP != "" && !util.IsValidIPv4(profile.MgmtIP) {
		v.AddErrorf("invalid management IP: %s", profile.MgmtIP)
	}
	if profile.LoopbackIP != "" && !util.IsValidIPv4(profile.LoopbackIP) {
		v.AddErrorf("invalid loopback IP: %s", profile.LoopbackIP)
	}

	// Validate site exists (region is derived from site)
	if profile.Site != "" {
		if _, ok := l.site.Sites[profile.Site]; !ok {
			v.AddErrorf("unknown site: %s", profile.Site)
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

// GetSite returns the site spec
func (l *Loader) GetSite() *SiteSpecFile {
	return l.site
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

// GetFilterSpec returns a filter spec by name
func (l *Loader) GetFilterSpec(name string) (*FilterSpec, error) {
	spec, ok := l.network.FilterSpecs[name]
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
