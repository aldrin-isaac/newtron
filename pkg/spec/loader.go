package spec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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

// ResolveProfile resolves a device profile with inheritance
func (l *Loader) ResolveProfile(deviceName string) (*ResolvedProfile, error) {
	profile, err := l.LoadProfile(deviceName)
	if err != nil {
		return nil, err
	}

	// Get site info - this is the source of truth for which region the device is in
	site, ok := l.site.Sites[profile.Site]
	if !ok {
		return nil, fmt.Errorf("site '%s' not found in site spec", profile.Site)
	}

	// Derive region from site (single source of truth)
	regionName := site.Region
	region, ok := l.network.Regions[regionName]
	if !ok {
		return nil, fmt.Errorf("region '%s' (from site '%s') not found", regionName, profile.Site)
	}

	resolved := &ResolvedProfile{
		DeviceName: deviceName,
		MgmtIP:     profile.MgmtIP,
		LoopbackIP: profile.LoopbackIP,
		Region:     regionName, // Derived from site.json, not profile
		Site:       profile.Site,
		Platform:   profile.Platform,
		MAC:        profile.MAC,
	}

	// Resolve AS number: profile > region
	if profile.ASNumber != nil {
		resolved.ASNumber = *profile.ASNumber
	} else {
		resolved.ASNumber = region.ASNumber
	}

	// Resolve affinity: profile > region > default
	resolved.Affinity = util.CoalesceString(profile.Affinity, region.Affinity, "flat")

	// Resolve boolean flags
	if profile.IsRouter != nil {
		resolved.IsRouter = *profile.IsRouter
	} else {
		resolved.IsRouter = true // Default
	}
	if profile.IsBridge != nil {
		resolved.IsBridge = *profile.IsBridge
	} else {
		resolved.IsBridge = true // Default
	}
	resolved.IsBorderRouter = profile.IsBorderRouter
	resolved.IsRouteReflector = profile.IsRouteReflector

	// Derived values
	resolved.RouterID = profile.LoopbackIP
	resolved.VTEPSourceIP = profile.LoopbackIP
	resolved.VTEPSourceIntf = util.DeriveVTEPSourceInterface()

	// Derive BGP neighbors from route reflectors (lookup their profiles)
	resolved.BGPNeighbors = l.deriveBGPNeighbors(site, deviceName)

	// Merge maps: profile > region > global
	resolved.GenericAlias = util.MergeMaps(
		l.network.GenericAlias,
		region.GenericAlias,
		profile.GenericAlias,
	)
	resolved.PrefixLists = util.MergeMaps(
		l.network.PrefixLists,
		region.PrefixLists,
		profile.PrefixLists,
	)

	// SSH credentials (for Redis tunnel)
	resolved.SSHUser = profile.SSHUser
	resolved.SSHPass = profile.SSHPass
	resolved.SSHPort = profile.SSHPort

	// newtlab runtime
	resolved.ConsolePort = profile.ConsolePort

	// eBGP underlay ASN
	resolved.UnderlayASN = profile.UnderlayASN

	return resolved, nil
}

// deriveBGPNeighbors gets loopback IPs of route reflectors from their profiles
// This ensures single source of truth - loopback_ip is only in the device profile
func (l *Loader) deriveBGPNeighbors(site *SiteSpec, deviceName string) []string {
	var neighbors []string
	for _, rrName := range site.RouteReflectors {
		if rrName == deviceName {
			continue // Don't peer with self
		}
		// Load the route reflector's profile to get its loopback IP
		rrProfile, err := l.LoadProfile(rrName)
		if err != nil {
			continue // Skip if profile not found
		}
		neighbors = append(neighbors, rrProfile.LoopbackIP)
	}
	return neighbors
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

// GetPolicer returns a policer definition by name
func (l *Loader) GetPolicer(name string) (*PolicerSpec, error) {
	policer, ok := l.network.Policers[name]
	if !ok {
		return nil, fmt.Errorf("policer '%s' not found", name)
	}
	return policer, nil
}

// ListServices returns all service names
func (l *Loader) ListServices() []string {
	var names []string
	for name := range l.network.Services {
		names = append(names, name)
	}
	return names
}

// ListRegions returns all region names
func (l *Loader) ListRegions() []string {
	var names []string
	for name := range l.network.Regions {
		names = append(names, name)
	}
	return names
}

// GetTopology returns the topology spec, or nil if no topology.json was found.
func (l *Loader) GetTopology() *TopologySpecFile {
	return l.topology
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
	for i, c := range endpoint {
		if c == ':' {
			return []string{endpoint[:i], endpoint[i+1:]}
		}
	}
	return []string{endpoint}
}
