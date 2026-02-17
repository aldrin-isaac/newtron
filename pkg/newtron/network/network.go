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

	"github.com/newtron-network/newtron/pkg/newtron/network/node"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
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
	platforms *spec.PlatformSpecFile

	// Topology spec (nil if topology.json doesn't exist)
	topology *spec.TopologySpecFile

	// Loader for loading device profiles (already initialized with Load())
	loader *spec.Loader

	// Connected devices (created in this Network's context)
	devices map[string]*node.Node

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
		platforms: loader.GetPlatforms(),
		topology:  loader.GetTopology(),
		loader:    loader,
		devices:   make(map[string]*node.Node),
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

// GetFilter returns a filter specification by name.
func (n *Network) GetFilter(name string) (*spec.FilterSpec, error) {
	return getSpec(&n.mu, n.spec.Filters, "filter", name)
}

// GetPlatform returns a platform definition by name.
func (n *Network) GetPlatform(name string) (*spec.PlatformSpec, error) {
	return getSpec(&n.mu, n.platforms.Platforms, "platform", name)
}

// Platforms returns all platform definitions.
func (n *Network) Platforms() map[string]*spec.PlatformSpec {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.platforms.Platforms
}

// GetPrefixList returns a prefix list by name.
func (n *Network) GetPrefixList(name string) ([]string, error) {
	return getSpec(&n.mu, n.spec.PrefixLists, "prefix list", name)
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
	return getSpec(&n.mu, n.spec.IPVPNs, "ipvpn", name)
}

// GetMACVPN returns a MAC-VPN definition by name.
func (n *Network) GetMACVPN(name string) (*spec.MACVPNSpec, error) {
	return getSpec(&n.mu, n.spec.MACVPNs, "macvpn", name)
}

// GetRoutePolicy returns a route policy by name.
func (n *Network) GetRoutePolicy(name string) (*spec.RoutePolicy, error) {
	return getSpec(&n.mu, n.spec.RoutePolicies, "route policy", name)
}

// FindMACVPNByVNI returns the MACVPN name and spec for a given VNI.
// Returns ("", nil) if no MACVPN matches.
func (n *Network) FindMACVPNByVNI(vni int) (string, *spec.MACVPNSpec) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	for name, def := range n.spec.MACVPNs {
		if def.VNI == vni {
			return name, def
		}
	}
	return "", nil
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

// ListFilters returns all available filter names.
func (n *Network) ListFilters() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()

	names := make([]string, 0, len(n.spec.Filters))
	for name := range n.spec.Filters {
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

	if n.spec.IPVPNs == nil {
		n.spec.IPVPNs = make(map[string]*spec.IPVPNSpec)
	}
	n.spec.IPVPNs[name] = def
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

	delete(n.spec.IPVPNs, name)
	return n.persistSpec()
}

// SaveMACVPN creates or updates a MAC-VPN definition in network.json.
func (n *Network) SaveMACVPN(name string, def *spec.MACVPNSpec) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.spec.MACVPNs == nil {
		n.spec.MACVPNs = make(map[string]*spec.MACVPNSpec)
	}
	n.spec.MACVPNs[name] = def
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

	delete(n.spec.MACVPNs, name)
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

// SaveFilter creates or updates a filter in network.json.
func (n *Network) SaveFilter(name string, def *spec.FilterSpec) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.spec.Filters == nil {
		n.spec.Filters = make(map[string]*spec.FilterSpec)
	}
	n.spec.Filters[name] = def
	return n.persistSpec()
}

// DeleteFilter removes a filter from network.json.
// Returns error if any service references it.
func (n *Network) DeleteFilter(name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Check for dependent services
	for svcName, svc := range n.spec.Services {
		if svc.IngressFilter == name || svc.EgressFilter == name {
			return fmt.Errorf("cannot delete filter '%s': referenced by service '%s'", name, svcName)
		}
	}

	delete(n.spec.Filters, name)
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

// ============================================================================
// Host Device Detection
// ============================================================================

// IsHostDevice returns true if the named device uses a host platform
// (device_type == "host"). Host devices are not SONiC switches and
// have no CONFIG_DB, APP_DB, or ASIC_DB.
func (n *Network) IsHostDevice(name string) bool {
	profile, err := n.loadProfile(name)
	if err != nil {
		return false
	}
	if profile.Platform == "" {
		return false
	}
	platform, ok := n.platforms.Platforms[profile.Platform]
	if !ok {
		return false
	}
	return platform.IsHost()
}

// GetHostProfile returns the device profile for a host device.
func (n *Network) GetHostProfile(name string) (*spec.DeviceProfile, error) {
	return n.loadProfile(name)
}

// ============================================================================
// Device (Node) Management
// ============================================================================

// GetDevice returns an existing device or loads it from profile.
// The Device is created in this Network's context and has access to all
// Network-level specs through its parent reference.
func (n *Network) GetNode(name string) (*node.Node, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Return existing device if already loaded
	if dev, ok := n.devices[name]; ok {
		return dev, nil
	}

	// Host devices have no SONiC — cannot create a Node
	if n.isHostDeviceLocked(name) {
		return nil, fmt.Errorf("device '%s' is a host (no SONiC); use GetHostProfile() instead", name)
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

	// Build per-device ResolvedSpecs (hierarchical merge: network > zone > profile)
	resolvedSpecs := n.buildResolvedSpecs(profile)

	// Create Node with ResolvedSpecs as SpecProvider for hierarchical spec access
	dev := node.New(resolvedSpecs, name, profile, resolved)

	n.devices[name] = dev
	return dev, nil
}

// ConnectDevice loads a device and establishes connection.
func (n *Network) ConnectNode(ctx context.Context, name string) (*node.Node, error) {
	dev, err := n.GetNode(name)
	if err != nil {
		return nil, err
	}

	if err := dev.Connect(ctx); err != nil {
		return nil, err
	}

	return dev, nil
}

// ListDevices returns names of all loaded devices.
func (n *Network) ListNodes() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()

	names := make([]string, 0, len(n.devices))
	for name := range n.devices {
		names = append(names, name)
	}
	return names
}

// isHostDeviceLocked checks host status without acquiring the mutex (caller must hold lock).
func (n *Network) isHostDeviceLocked(name string) bool {
	profile, err := n.loader.LoadProfile(name)
	if err != nil || profile.Platform == "" {
		return false
	}
	platform, ok := n.platforms.Platforms[profile.Platform]
	if !ok {
		return false
	}
	return platform.IsHost()
}

// loadProfile loads a device profile from the profiles directory.
func (n *Network) loadProfile(name string) (*spec.DeviceProfile, error) {
	return n.loader.LoadProfile(name)
}

// resolveProfile applies inheritance to resolve final values.
func (n *Network) resolveProfile(name string, profile *spec.DeviceProfile) (*spec.ResolvedProfile, error) {
	// Validate zone exists
	if _, ok := n.spec.Zones[profile.Zone]; !ok {
		return nil, fmt.Errorf("zone '%s' not found", profile.Zone)
	}

	resolved := &spec.ResolvedProfile{
		DeviceName: name,
		MgmtIP:     profile.MgmtIP,
		LoopbackIP: profile.LoopbackIP,
		Zone:     profile.Zone,
		Platform:   profile.Platform,
		MAC:        profile.MAC,
	}

	// Router ID and VTEP from loopback
	resolved.RouterID = profile.LoopbackIP
	resolved.VTEPSourceIP = profile.LoopbackIP

	// EVPN config from profile
	if profile.EVPN != nil {
		resolved.IsRouteReflector = profile.EVPN.RouteReflector
		resolved.ClusterID = profile.EVPN.ClusterID
	}
	// ClusterID defaults to loopback IP
	if resolved.ClusterID == "" {
		resolved.ClusterID = profile.LoopbackIP
	}

	// BGP neighbors from EVPN peers
	resolved.BGPNeighbors = n.deriveBGPNeighbors(profile, name)

	// SSH credentials (for Redis tunnel)
	resolved.SSHUser = profile.SSHUser
	resolved.SSHPass = profile.SSHPass
	resolved.SSHPort = profile.SSHPort

	// eBGP underlay ASN
	resolved.UnderlayASN = profile.UnderlayASN

	return resolved, nil
}

// buildResolvedSpecs merges all 8 overridable spec maps with hierarchical
// resolution: network → zone → profile (lower-level wins).
func (n *Network) buildResolvedSpecs(profile *spec.DeviceProfile) *ResolvedSpecs {
	zone := n.spec.Zones[profile.Zone] // already validated in resolveProfile

	merged := spec.OverridableSpecs{
		PrefixLists:   util.MergeMaps(n.spec.PrefixLists, zone.PrefixLists, profile.PrefixLists),
		Filters:       util.MergeMaps(n.spec.Filters, zone.Filters, profile.Filters),
		Services:      util.MergeMaps(n.spec.Services, zone.Services, profile.Services),
		IPVPNs:        util.MergeMaps(n.spec.IPVPNs, zone.IPVPNs, profile.IPVPNs),
		MACVPNs:       util.MergeMaps(n.spec.MACVPNs, zone.MACVPNs, profile.MACVPNs),
		QoSPolicies:   util.MergeMaps(n.spec.QoSPolicies, zone.QoSPolicies, profile.QoSPolicies),
		QoSProfiles:   util.MergeMaps(n.spec.QoSProfiles, zone.QoSProfiles, profile.QoSProfiles),
		RoutePolicies: util.MergeMaps(n.spec.RoutePolicies, zone.RoutePolicies, profile.RoutePolicies),
	}

	return newResolvedSpecs(merged, n)
}

// deriveBGPNeighbors looks up EVPN peer loopback IPs from their profiles.
// Silently skips peers that aren't in the current topology (e.g., spine2 in a 2-node topo).
func (n *Network) deriveBGPNeighbors(profile *spec.DeviceProfile, selfName string) []string {
	if profile.EVPN == nil {
		return nil
	}
	topo := n.GetTopology()
	var neighbors []string
	for _, peerName := range profile.EVPN.Peers {
		if peerName == selfName {
			continue // Don't peer with self
		}
		// Skip devices not in the current topology
		if topo != nil && !topo.HasDevice(peerName) {
			continue
		}
		// Skip host devices
		if n.isHostDeviceLocked(peerName) {
			continue
		}
		// Load peer profile to get its loopback IP
		peerProfile, err := n.loadProfile(peerName)
		if err != nil {
			util.Logger.Warnf("Could not load EVPN peer profile %s: %v", peerName, err)
			continue
		}
		neighbors = append(neighbors, peerProfile.LoopbackIP)
	}
	return neighbors
}
