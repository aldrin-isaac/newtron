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

// getSpec is a generic helper for map-based spec lookups under a read lock.
func getSpec[V any](mu *sync.RWMutex, m map[string]V, kind, name string) (V, error) {
	mu.RLock()
	defer mu.RUnlock()
	v, ok := m[name]
	if !ok {
		var zero V
		return zero, fmt.Errorf("%s '%s' not found", kind, name)
	}
	return v, nil
}

// GetService returns a service definition by name.
func (n *Network) GetService(name string) (*spec.ServiceSpec, error) {
	return getSpec(&n.mu, n.spec.Services, "service", name)
}

// GetFilterSpec returns a filter specification by name.
func (n *Network) GetFilterSpec(name string) (*spec.FilterSpec, error) {
	return getSpec(&n.mu, n.spec.FilterSpecs, "filter spec", name)
}

// GetRegion returns a region definition by name.
func (n *Network) GetRegion(name string) (*spec.RegionSpec, error) {
	return getSpec(&n.mu, n.spec.Regions, "region", name)
}

// GetSite returns a site specification by name.
func (n *Network) GetSite(name string) (*spec.SiteSpec, error) {
	return getSpec(&n.mu, n.sites.Sites, "site", name)
}

// GetPlatform returns a platform definition by name.
func (n *Network) GetPlatform(name string) (*spec.PlatformSpec, error) {
	return getSpec(&n.mu, n.platforms.Platforms, "platform", name)
}

// GetPrefixList returns a prefix list by name.
func (n *Network) GetPrefixList(name string) ([]string, error) {
	return getSpec(&n.mu, n.spec.PrefixLists, "prefix list", name)
}

// GetPolicer returns a policer definition by name.
func (n *Network) GetPolicer(name string) (*spec.PolicerSpec, error) {
	return getSpec(&n.mu, n.spec.Policers, "policer", name)
}

// GetQoSPolicy returns a QoS policy by name.
func (n *Network) GetQoSPolicy(name string) (*spec.QoSPolicy, error) {
	return getSpec(&n.mu, n.spec.QoSPolicies, "QoS policy", name)
}

// GetQoSProfile returns a QoS profile by name (legacy).
func (n *Network) GetQoSProfile(name string) (*spec.QoSProfile, error) {
	return getSpec(&n.mu, n.spec.QoSProfiles, "QoS profile", name)
}

// GetIPVPN returns an IP-VPN definition by name.
func (n *Network) GetIPVPN(name string) (*spec.IPVPNSpec, error) {
	return getSpec(&n.mu, n.spec.IPVPN, "ipvpn", name)
}

// GetMACVPN returns a MAC-VPN definition by name.
func (n *Network) GetMACVPN(name string) (*spec.MACVPNSpec, error) {
	return getSpec(&n.mu, n.spec.MACVPN, "macvpn", name)
}

// GetRoutePolicy returns a route policy by name.
func (n *Network) GetRoutePolicy(name string) (*spec.RoutePolicy, error) {
	return getSpec(&n.mu, n.spec.RoutePolicies, "route policy", name)
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

// ============================================================================
// Spec Authoring - CLI-authored definitions persisted to network.json
// ============================================================================

// persistSpec writes the current network spec to disk atomically (temp + rename).
func (n *Network) persistSpec() error {
	if n.loader == nil {
		return fmt.Errorf("no loader configured")
	}
	return n.loader.SaveNetwork(n.spec)
}

// SaveIPVPN creates or updates an IP-VPN definition in network.json.
func (n *Network) SaveIPVPN(name string, def *spec.IPVPNSpec) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.spec.IPVPN == nil {
		n.spec.IPVPN = make(map[string]*spec.IPVPNSpec)
	}
	n.spec.IPVPN[name] = def
	return n.persistSpec()
}

// DeleteIPVPN removes an IP-VPN definition from network.json.
// Returns error if any service references it.
func (n *Network) DeleteIPVPN(name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Check for dependent services
	for svcName, svc := range n.spec.Services {
		if svc.IPVPN == name {
			return fmt.Errorf("cannot delete ipvpn '%s': referenced by service '%s'", name, svcName)
		}
	}

	delete(n.spec.IPVPN, name)
	return n.persistSpec()
}

// SaveMACVPN creates or updates a MAC-VPN definition in network.json.
func (n *Network) SaveMACVPN(name string, def *spec.MACVPNSpec) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.spec.MACVPN == nil {
		n.spec.MACVPN = make(map[string]*spec.MACVPNSpec)
	}
	n.spec.MACVPN[name] = def
	return n.persistSpec()
}

// DeleteMACVPN removes a MAC-VPN definition from network.json.
// Returns error if any service references it.
func (n *Network) DeleteMACVPN(name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Check for dependent services
	for svcName, svc := range n.spec.Services {
		if svc.MACVPN == name {
			return fmt.Errorf("cannot delete macvpn '%s': referenced by service '%s'", name, svcName)
		}
	}

	delete(n.spec.MACVPN, name)
	return n.persistSpec()
}

// SaveQoSPolicy creates or updates a QoS policy in network.json.
func (n *Network) SaveQoSPolicy(name string, def *spec.QoSPolicy) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.spec.QoSPolicies == nil {
		n.spec.QoSPolicies = make(map[string]*spec.QoSPolicy)
	}
	n.spec.QoSPolicies[name] = def
	return n.persistSpec()
}

// DeleteQoSPolicy removes a QoS policy from network.json.
// Returns error if any service references it.
func (n *Network) DeleteQoSPolicy(name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Check for dependent services
	for svcName, svc := range n.spec.Services {
		if svc.QoSPolicy == name {
			return fmt.Errorf("cannot delete QoS policy '%s': referenced by service '%s'", name, svcName)
		}
	}

	delete(n.spec.QoSPolicies, name)
	return n.persistSpec()
}

// SaveFilterSpec creates or updates a filter spec in network.json.
func (n *Network) SaveFilterSpec(name string, def *spec.FilterSpec) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.spec.FilterSpecs == nil {
		n.spec.FilterSpecs = make(map[string]*spec.FilterSpec)
	}
	n.spec.FilterSpecs[name] = def
	return n.persistSpec()
}

// DeleteFilterSpec removes a filter spec from network.json.
// Returns error if any service references it.
func (n *Network) DeleteFilterSpec(name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Check for dependent services
	for svcName, svc := range n.spec.Services {
		if svc.IngressFilter == name || svc.EgressFilter == name {
			return fmt.Errorf("cannot delete filter spec '%s': referenced by service '%s'", name, svcName)
		}
	}

	delete(n.spec.FilterSpecs, name)
	return n.persistSpec()
}

// SaveService creates or updates a service definition in network.json.
func (n *Network) SaveService(name string, def *spec.ServiceSpec) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.spec.Services == nil {
		n.spec.Services = make(map[string]*spec.ServiceSpec)
	}
	n.spec.Services[name] = def
	return n.persistSpec()
}

// DeleteService removes a service definition from network.json.
// Returns error if any interface has it applied (caller checks this).
func (n *Network) DeleteService(name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	delete(n.spec.Services, name)
	return n.persistSpec()
}

// ListIPVPN returns all IP-VPN definition names.
func (n *Network) ListIPVPN() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()

	names := make([]string, 0, len(n.spec.IPVPN))
	for name := range n.spec.IPVPN {
		names = append(names, name)
	}
	return names
}

// ListMACVPN returns all MAC-VPN definition names.
func (n *Network) ListMACVPN() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()

	names := make([]string, 0, len(n.spec.MACVPN))
	for name := range n.spec.MACVPN {
		names = append(names, name)
	}
	return names
}

// ListQoSPolicies returns all QoS policy names.
func (n *Network) ListQoSPolicies() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if n.spec.QoSPolicies == nil {
		return nil
	}
	names := make([]string, 0, len(n.spec.QoSPolicies))
	for name := range n.spec.QoSPolicies {
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

	// Router ID and VTEP from loopback
	resolved.RouterID = profile.LoopbackIP
	resolved.VTEPSourceIP = profile.LoopbackIP

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
// Silently skips RR peers that aren't in the current topology (e.g., spine2 in a 2-node topo).
func (n *Network) deriveBGPNeighbors(site *spec.SiteSpec, selfName string) []string {
	topo := n.GetTopology()
	var neighbors []string
	for _, rrName := range site.RouteReflectors {
		if rrName == selfName {
			continue // Don't peer with self
		}
		// Skip devices not in the current topology
		if topo != nil && !topo.HasDevice(rrName) {
			continue
		}
		// Load RR profile to get its loopback IP
		rrProfile, err := n.loadProfile(rrName)
		if err != nil {
			util.Logger.Warnf("Could not load route reflector profile %s: %v", rrName, err)
			continue
		}
		neighbors = append(neighbors, rrProfile.LoopbackIP)
	}
	return neighbors
}
