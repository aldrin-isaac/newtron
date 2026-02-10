// Package network provides the top-level Network object that owns all specs
// and provides hierarchical access: Network -> Device -> Interface
//
// This follows the original Perl design where objects have parent references,
// allowing natural access to specs at any level:
//   - Interface can access its Device's properties
//   - Interface can access Network-level services, filters, etc.
//   - Device can access Network-level specs
package network

import (
	"context"
	"fmt"
	"sync"

	"github.com/newtron-network/newtron/pkg/model"
	"github.com/newtron-network/newtron/pkg/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// Network is the top-level object representing the entire network.
// It loads and owns all specs, and creates Device instances within its context.
//
// Design principle: Network-level specs (services, filters, regions, etc.)
// are accessible to all Device and Interface objects through parent references.
type Network struct {
	// Specs (from JSON files)
	spec      *spec.NetworkSpecFile
	sites     *spec.SiteSpecFile
	platforms *spec.PlatformSpecFile

	// Topology spec (nil if topology.json doesn't exist)
	topology *spec.TopologySpecFile

	// Loader for loading device profiles (already initialized with Load())
	loader *spec.Loader

	// Connected devices (created in this Network's context)
	devices map[string]*Device

	// Mutex for thread safety
	mu sync.RWMutex
}

// NewNetwork creates a new Network instance by loading all spec files.
// This is the entry point for the application.
func NewNetwork(specDir string) (*Network, error) {
	loader := spec.NewLoader(specDir)

	// Load all spec files
	if err := loader.Load(); err != nil {
		return nil, fmt.Errorf("loading specs: %w", err)
	}

	return &Network{
		spec:      loader.GetNetwork(),
		sites:     loader.GetSite(),
		platforms: loader.GetPlatforms(),
		topology:  loader.GetTopology(),
		loader:    loader,
		devices:   make(map[string]*Device),
	}, nil
}

// ============================================================================
// Network-level Spec Accessors
// These are available to Device and Interface objects through parent reference
// ============================================================================

// GetService returns a service definition by name.
// Services define interface templates (VPN, filters, QoS).
func (n *Network) GetService(name string) (*spec.ServiceSpec, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	svc, ok := n.spec.Services[name]
	if !ok {
		return nil, fmt.Errorf("service '%s' not found", name)
	}
	return svc, nil
}

// GetFilterSpec returns a filter specification by name.
// Filter specs define ACL rules that can be applied to interfaces.
func (n *Network) GetFilterSpec(name string) (*spec.FilterSpec, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	fs, ok := n.spec.FilterSpecs[name]
	if !ok {
		return nil, fmt.Errorf("filter spec '%s' not found", name)
	}
	return fs, nil
}

// GetRegion returns a region definition by name.
func (n *Network) GetRegion(name string) (*spec.RegionSpec, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	region, ok := n.spec.Regions[name]
	if !ok {
		return nil, fmt.Errorf("region '%s' not found", name)
	}
	return region, nil
}

// GetSite returns a site specification by name.
func (n *Network) GetSite(name string) (*spec.SiteSpec, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	site, ok := n.sites.Sites[name]
	if !ok {
		return nil, fmt.Errorf("site '%s' not found", name)
	}
	return site, nil
}

// GetPlatform returns a platform definition by name.
func (n *Network) GetPlatform(name string) (*spec.PlatformSpec, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	platform, ok := n.platforms.Platforms[name]
	if !ok {
		return nil, fmt.Errorf("platform '%s' not found", name)
	}
	return platform, nil
}

// GetPrefixList returns a prefix list by name.
func (n *Network) GetPrefixList(name string) ([]string, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	prefixes, ok := n.spec.PrefixLists[name]
	if !ok {
		return nil, fmt.Errorf("prefix list '%s' not found", name)
	}
	return prefixes, nil
}

// GetPolicer returns a policer definition by name.
func (n *Network) GetPolicer(name string) (*spec.PolicerSpec, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	policer, ok := n.spec.Policers[name]
	if !ok {
		return nil, fmt.Errorf("policer '%s' not found", name)
	}
	return policer, nil
}

// GetQoSProfile returns a QoS profile by name.
func (n *Network) GetQoSProfile(name string) (*model.QoSProfile, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	profile, ok := n.spec.QoSProfiles[name]
	if !ok {
		return nil, fmt.Errorf("QoS profile '%s' not found", name)
	}
	return profile, nil
}

// GetIPVPN returns an IP-VPN definition by name.
// IP-VPN definitions contain L3VNI and route targets for L3 routing.
func (n *Network) GetIPVPN(name string) (*spec.IPVPNSpec, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	ipvpn, ok := n.spec.IPVPN[name]
	if !ok {
		return nil, fmt.Errorf("ipvpn '%s' not found", name)
	}
	return ipvpn, nil
}

// GetMACVPN returns a MAC-VPN definition by name.
// MAC-VPN definitions contain VLAN, L2VNI, and ARP suppression for L2 bridging.
func (n *Network) GetMACVPN(name string) (*spec.MACVPNSpec, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	macvpn, ok := n.spec.MACVPN[name]
	if !ok {
		return nil, fmt.Errorf("macvpn '%s' not found", name)
	}
	return macvpn, nil
}

// GetRoutePolicy returns a route policy by name.
// Route policies define BGP import/export rules for route filtering.
func (n *Network) GetRoutePolicy(name string) (*spec.RoutePolicy, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if n.spec.RoutePolicies == nil {
		return nil, fmt.Errorf("route policy '%s' not found (no route policies defined)", name)
	}
	policy, ok := n.spec.RoutePolicies[name]
	if !ok {
		return nil, fmt.Errorf("route policy '%s' not found", name)
	}
	return policy, nil
}

// ListServices returns all available service names.
func (n *Network) ListServices() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()

	names := make([]string, 0, len(n.spec.Services))
	for name := range n.spec.Services {
		names = append(names, name)
	}
	return names
}

// ListFilterSpecs returns all available filter spec names.
func (n *Network) ListFilterSpecs() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()

	names := make([]string, 0, len(n.spec.FilterSpecs))
	for name := range n.spec.FilterSpecs {
		names = append(names, name)
	}
	return names
}

// Spec returns the raw network spec (for advanced access).
func (n *Network) Spec() *spec.NetworkSpecFile {
	return n.spec
}

// GetTopology returns the topology spec, or nil if no topology.json was loaded.
func (n *Network) GetTopology() *spec.TopologySpecFile {
	return n.topology
}

// HasTopology returns true if a topology.json was loaded.
func (n *Network) HasTopology() bool {
	return n.topology != nil
}

// GetTopologyDevice returns the topology definition for a named device.
func (n *Network) GetTopologyDevice(name string) (*spec.TopologyDevice, error) {
	if n.topology == nil {
		return nil, fmt.Errorf("no topology loaded")
	}
	dev, ok := n.topology.Devices[name]
	if !ok {
		return nil, fmt.Errorf("device '%s' not found in topology", name)
	}
	return dev, nil
}

// GetTopologyInterface returns the topology definition for an interface on a device.
func (n *Network) GetTopologyInterface(device, intf string) (*spec.TopologyInterface, error) {
	dev, err := n.GetTopologyDevice(device)
	if err != nil {
		return nil, err
	}
	ti, ok := dev.Interfaces[intf]
	if !ok {
		return nil, fmt.Errorf("interface '%s' not found in topology for device '%s'", intf, device)
	}
	return ti, nil
}

// ============================================================================
// Device (Node) Management
// ============================================================================

// GetDevice returns an existing device or loads it from profile.
// The Device is created in this Network's context and has access to all
// Network-level specs through its parent reference.
func (n *Network) GetDevice(name string) (*Device, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Return existing device if already loaded
	if dev, ok := n.devices[name]; ok {
		return dev, nil
	}

	// Load device profile and create new Device in this Network's context
	profile, err := n.loadProfile(name)
	if err != nil {
		return nil, fmt.Errorf("loading profile for %s: %w", name, err)
	}

	// Resolve profile with inheritance
	resolved, err := n.resolveProfile(name, profile)
	if err != nil {
		return nil, fmt.Errorf("resolving profile for %s: %w", name, err)
	}

	// Create Device with parent reference to this Network
	dev := &Device{
		network:    n, // Parent reference - key to OO design
		name:       name,
		profile:    profile,
		resolved:   resolved,
		interfaces: make(map[string]*Interface),
	}

	n.devices[name] = dev
	return dev, nil
}

// ConnectDevice loads a device and establishes connection.
func (n *Network) ConnectDevice(ctx context.Context, name string) (*Device, error) {
	dev, err := n.GetDevice(name)
	if err != nil {
		return nil, err
	}

	if err := dev.Connect(ctx); err != nil {
		return nil, err
	}

	return dev, nil
}

// ListDevices returns names of all loaded devices.
func (n *Network) ListDevices() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()

	names := make([]string, 0, len(n.devices))
	for name := range n.devices {
		names = append(names, name)
	}
	return names
}

// loadProfile loads a device profile from the profiles directory.
func (n *Network) loadProfile(name string) (*spec.DeviceProfile, error) {
	return n.loader.LoadProfile(name)
}

// resolveProfile applies inheritance to resolve final values.
func (n *Network) resolveProfile(name string, profile *spec.DeviceProfile) (*spec.ResolvedProfile, error) {
	// Get site (determines region)
	site, ok := n.sites.Sites[profile.Site]
	if !ok {
		return nil, fmt.Errorf("site '%s' not found", profile.Site)
	}

	// Get region
	region, ok := n.spec.Regions[site.Region]
	if !ok {
		return nil, fmt.Errorf("region '%s' not found", site.Region)
	}

	resolved := &spec.ResolvedProfile{
		DeviceName: name,
		MgmtIP:     profile.MgmtIP,
		LoopbackIP: profile.LoopbackIP,
		Region:     site.Region,
		Site:       profile.Site,
		Platform:   profile.Platform,
		MAC:        profile.MAC,
	}

	// AS Number: profile > region
	if profile.ASNumber != nil {
		resolved.ASNumber = *profile.ASNumber
	} else {
		resolved.ASNumber = region.ASNumber
	}

	// Affinity: profile > region > default
	resolved.Affinity = util.Coalesce(profile.Affinity, region.Affinity, "flat")

	// Router ID and VTEP from loopback
	resolved.RouterID = profile.LoopbackIP
	resolved.VTEPSourceIP = profile.LoopbackIP
	resolved.VTEPSourceIntf = "Loopback0"

	// BGP neighbors from route reflectors
	resolved.BGPNeighbors = n.deriveBGPNeighbors(site, name)

	// Merge maps: global < region < profile
	resolved.GenericAlias = util.MergeMaps(
		n.spec.GenericAlias,
		region.GenericAlias,
		profile.GenericAlias,
	)
	resolved.PrefixLists = util.MergeMaps(
		n.spec.PrefixLists,
		region.PrefixLists,
		profile.PrefixLists,
	)

	// Boolean flags
	if profile.IsRouter != nil {
		resolved.IsRouter = *profile.IsRouter
	} else {
		resolved.IsRouter = true
	}
	if profile.IsBridge != nil {
		resolved.IsBridge = *profile.IsBridge
	} else {
		resolved.IsBridge = true
	}
	resolved.IsBorderRouter = profile.IsBorderRouter
	resolved.IsRouteReflector = profile.IsRouteReflector

	// SSH credentials (for Redis tunnel)
	resolved.SSHUser = profile.SSHUser
	resolved.SSHPass = profile.SSHPass
	resolved.SSHPort = profile.SSHPort

	// eBGP underlay ASN
	resolved.UnderlayASN = profile.UnderlayASN

	return resolved, nil
}

// deriveBGPNeighbors looks up route reflector loopback IPs from their profiles.
func (n *Network) deriveBGPNeighbors(site *spec.SiteSpec, selfName string) []string {
	var neighbors []string
	for _, rrName := range site.RouteReflectors {
		if rrName == selfName {
			continue // Don't peer with self
		}
		// Load RR profile to get its loopback IP
		rrProfile, err := n.loadProfile(rrName)
		if err != nil {
			util.Warnf("Could not load route reflector profile %s: %v", rrName, err)
			continue
		}
		neighbors = append(neighbors, rrProfile.LoopbackIP)
	}
	return neighbors
}
