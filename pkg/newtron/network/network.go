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
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/network/node"
	"github.com/aldrin-isaac/newtron/pkg/newtron/secret"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// Lock keys identify the persistence-boundary slices of Network state that
// the lockManager hands out distinct *sync.RWMutex instances for. Each key
// matches a single ownership scope:
//
//   - keyNetworkSpec covers everything in network.json (the OverridableSpecs
//     maps plus Zones) and the file write that persists them.
//   - keyPlatforms covers platforms.json (the cached *PlatformSpecFile this
//     Network holds and the file write that persists it). Added by #173 when
//     CRUD endpoints retired the "platforms is immutable" invariant; every
//     read site that previously accessed n.platforms directly is now
//     RLock-guarded.
//   - keyTopology covers topology.json (Devices + Links) and the file write
//     that persists them.
//   - keyNodes covers the runtime *node.Node cache populated by GetNode.
//
// Profiles are not in this set — spec.Loader has its own RWMutex (added in
// PR #100) and serializes per-profile correctly on its own.
//
// Lock-ordering rule (multi-key acquisitions): alphabetical by key string.
// Verified for every cross-key call site in this file. Single-key callers
// are exempt; the rule only kicks in when a caller needs more than one.
const (
	keyNetworkSpec lockKey = "network.json"
	keyNodes       lockKey = "nodes"
	keyPlatforms   lockKey = "platforms.json"
	keyTopology    lockKey = "topology.json"
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

	// Topology name (e.g., "1node-vs"). Used as the topology argument
	// when consulting portResolver. Empty for tests or real-hardware
	// deployments where ports are static.
	topologyName string

	// portResolver supplies runtime SSH port allocations. Non-nil when
	// the runtime is configured to consult an external authority (the
	// newtlab-backed implementation, today). Nil for tests and real-
	// hardware deployments where the profile's mgmt_ip is the device's
	// real address. The implementation is injected from above
	// (DESIGN_PRINCIPLES §33: orchestrators are API consumers; §34:
	// transparent transport — the middle layer has no logic).
	portResolver sonic.PortResolver

	// secretStore is the operator-configured secret backend
	// (auth-design.md L0). When non-nil, ${secret:KEY} references in
	// profile and platform values are resolved at load time. When
	// nil (the L0 disabled state), references are an error and
	// plaintext values pass through — preserving the pre-L0
	// behavior exactly.
	secretStore secret.Store

	// Loader for loading device profiles (already initialized with Load())
	loader *spec.Loader

	// Connected devices (created in this Network's context). Protected by
	// the keyNodes lock.
	devices map[string]*node.Node

	// locks hands out per-key *sync.RWMutex instances. See the lock key
	// constants above for the keys in use here. Embedded as a value so
	// Network's zero value works for tests that construct &Network{...}
	// literally; lockManager itself is zero-value safe.
	locks lockManager
}

// NewNetwork creates a new Network instance by loading all spec files.
// This is the entry point for the application.
//
// topologyName identifies this network when calling portResolver
// (e.g., as a path segment in any consulted external API). Empty
// string disables resolver consultation regardless of pr.
//
// pr is the port resolver used at Device.Connect time. Pass nil for
// tests and real-hardware deployments.
//
// secretStore (auth-design.md L0) is the operator-configured secret
// backend. When non-nil, ${secret:KEY} references in spec values
// (currently DeviceProfile.SSHPass and PlatformSpec.VMCredentials)
// are resolved at network load. nil triggers spec-dir auto-discovery
// (#176): if <specDir>/secrets.json exists, it's opened as a
// FileStore and used; otherwise resolution stays disabled — plaintext
// spec values keep working, but a reference under a nil store is a
// hard error from secret.Resolve so the operator finds out
// immediately rather than silently sending "${secret:KEY}" as a
// password.
//
// The explicit `secretStore` argument always wins over auto-discovery
// — an operator who passes an explicit FileStore (typically from
// --secret-store=PATH on cmd/newt-server) gets that store regardless
// of what's next to network.json. The convention only kicks in when
// no flag is set.
func NewNetwork(specDir, topologyName string, pr sonic.PortResolver, secretStore secret.Store) (*Network, error) {
	loader := spec.NewLoader(specDir)

	// Load all spec files
	if err := loader.Load(); err != nil {
		return nil, fmt.Errorf("loading specs: %w", err)
	}

	// auth-design.md L0: spec-dir auto-discovery (#176). When the
	// operator didn't pass an explicit store, check the conventional
	// path <specDir>/secrets.json. Uses NewFileStoreLooseMode so a
	// git-checked-in test fixture (which always emerges from `git
	// checkout` at the umask default, typically 0644) loads without
	// requiring a separate chmod step — operators who want the strict
	// 0600 hygiene check use --secret-store=PATH (which routes
	// through the strict NewFileStore).
	if secretStore == nil {
		candidatePath := filepath.Join(specDir, "secrets.json")
		if _, statErr := os.Stat(candidatePath); statErr == nil {
			fs, err := secret.NewFileStoreLooseMode(candidatePath)
			if err != nil {
				return nil, fmt.Errorf("opening spec-dir secret store %s: %w", candidatePath, err)
			}
			secretStore = fs
		}
	}

	platforms := loader.GetPlatforms()
	if err := resolvePlatformSecrets(platforms, secretStore); err != nil {
		return nil, fmt.Errorf("resolving platform secrets: %w", err)
	}

	return &Network{
		spec:         loader.GetNetwork(),
		platforms:    platforms,
		topology:     loader.GetTopology(),
		topologyName: topologyName,
		portResolver: pr,
		secretStore:  secretStore,
		loader:       loader,
		devices:      make(map[string]*node.Node),
	}, nil
}

// resolvePlatformSecrets walks every PlatformSpec.VMCredentials and
// resolves any ${secret:KEY} references in the User and Pass fields
// against the configured store. Mutates the spec in place — the
// resolved plaintext is what subsequent reads of platforms.json see
// (whether through Network.Platforms() or the HTTP /platforms
// endpoint).
//
// Per auth-design.md L0, plaintext values pass through unchanged
// regardless of whether a store is configured; references with no
// store configured are an error.
func resolvePlatformSecrets(p *spec.PlatformSpecFile, store secret.Store) error {
	if p == nil {
		return nil
	}
	for name, platform := range p.Platforms {
		if platform == nil || platform.VMCredentials == nil {
			continue
		}
		user, err := secret.Resolve(platform.VMCredentials.User, store)
		if err != nil {
			return fmt.Errorf("platform %q vm_credentials.user: %w", name, err)
		}
		pass, err := secret.Resolve(platform.VMCredentials.Pass, store)
		if err != nil {
			return fmt.Errorf("platform %q vm_credentials.pass: %w", name, err)
		}
		platform.VMCredentials.User = user
		platform.VMCredentials.Pass = pass
	}
	return nil
}

// resolveProfileSecrets walks a DeviceProfile's SSH credentials and
// resolves any ${secret:KEY} references in the SSHUser and SSHPass
// fields. Closes the L0 coverage gap surfaced by 1node-vs-auth: pre-
// fix, only platform credentials were resolved; profile references
// reached SSH-tunnel construction untouched and SSH'd with the
// literal "${secret:KEY}" as the password.
//
// Called by Network.loadProfile after the loader's per-profile cache
// hit/miss path returns, so the in-memory profile cached by the
// loader carries the resolved value; subsequent cache reads return
// the resolved bytes without re-resolving. resolve is idempotent —
// a value with no "${secret:" prefix returns unchanged — so
// re-running over a cached profile is a no-op.
func resolveProfileSecrets(profile *spec.DeviceProfile, store secret.Store) error {
	if profile == nil {
		return nil
	}
	user, err := secret.Resolve(profile.SSHUser, store)
	if err != nil {
		return fmt.Errorf("profile ssh_user: %w", err)
	}
	pass, err := secret.Resolve(profile.SSHPass, store)
	if err != nil {
		return fmt.Errorf("profile ssh_pass: %w", err)
	}
	profile.SSHUser = user
	profile.SSHPass = pass
	return nil
}

// TopologyName returns the topology name this Network is associated with.
func (n *Network) TopologyName() string {
	return n.topologyName
}

// PortResolver returns the port resolver (may be nil).
func (n *Network) PortResolver() sonic.PortResolver {
	return n.portResolver
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

// Authorization is a snapshot of the network's authorization table —
// the user_groups, permissions, and super_users an operator authors
// in network.json and that newtron's authorization checker consumes
// at every mutation (auth-design.md §L3). The three fields share
// underlying memory with the live NetworkSpecFile; callers receive
// a read-only view suitable for serialization but must not mutate
// the returned maps or slice.
type Authorization struct {
	UserGroups  map[string][]string
	Permissions map[string]spec.PermissionGrants
	SuperUsers  []string
}

// GetAuthorization returns the network's authorization table. The
// table is one cohesive object owned by the network (DPN §27) —
// authored together in network.json, applied together on
// --enforce-authorization + reload, consumed together by the
// auth.Checker — so one accessor returns all three fields, mirroring
// the network.json shape.
func (n *Network) GetAuthorization() Authorization {
	mu := n.locks.lock(keyNetworkSpec)
	mu.RLock()
	defer mu.RUnlock()
	return Authorization{
		UserGroups:  n.spec.UserGroups,
		Permissions: n.spec.Permissions,
		SuperUsers:  n.spec.SuperUsers,
	}
}

// GetService returns a service definition by name.
func (n *Network) GetService(name string) (*spec.ServiceSpec, error) {
	return getSpec(n.locks.lock(keyNetworkSpec), n.spec.Services, "service", util.NormalizeName(name))
}

// GetFilter returns a filter specification by name.
func (n *Network) GetFilter(name string) (*spec.FilterSpec, error) {
	return getSpec(n.locks.lock(keyNetworkSpec), n.spec.Filters, "filter", util.NormalizeName(name))
}

// GetPlatform returns a platform definition by name. Reads
// n.platforms.Platforms under RLock — the map can be mutated by
// the CreatePlatform / UpdatePlatform / DeletePlatform methods
// added in #173.
func (n *Network) GetPlatform(name string) (*spec.PlatformSpec, error) {
	mu := n.locks.lock(keyPlatforms)
	mu.RLock()
	defer mu.RUnlock()
	p, ok := n.platforms.Platforms[name]
	if !ok {
		return nil, fmt.Errorf("platform '%s' not found", name)
	}
	return p, nil
}

// Platforms returns a snapshot of all platform definitions. Returns
// a defensive copy of the map so callers can iterate without holding
// the platforms lock — necessary now that the map is mutable (#173).
// The underlying *PlatformSpec pointers are shared, not copied; their
// contents are treated as immutable after CreatePlatform/UpdatePlatform
// stores them.
func (n *Network) Platforms() map[string]*spec.PlatformSpec {
	mu := n.locks.lock(keyPlatforms)
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]*spec.PlatformSpec, len(n.platforms.Platforms))
	for k, v := range n.platforms.Platforms {
		out[k] = v
	}
	return out
}

// ============================================================================
// Platform CRUD (#173)
// ============================================================================

// CreatePlatform atomically adds a new platform definition. Returns an
// error if a platform with the given name already exists. Persists the
// updated platforms.json under keyPlatforms.Lock().
func (n *Network) CreatePlatform(name string, def *spec.PlatformSpec) error {
	mu := n.locks.lock(keyPlatforms)
	mu.Lock()
	defer mu.Unlock()

	if _, exists := n.platforms.Platforms[name]; exists {
		return fmt.Errorf("platform '%s' already exists", name)
	}
	if n.platforms.Platforms == nil {
		n.platforms.Platforms = make(map[string]*spec.PlatformSpec)
	}
	n.platforms.Platforms[name] = def
	return n.persistPlatforms()
}

// UpdatePlatform atomically replaces an existing platform definition.
// Returns *newtronErrors{notFound: true} when the named platform does
// not exist — the public layer translates this to NotFoundError → 404.
// Full-replacement semantics mirror UpdateTopologyDevice + the #152
// update-X family: every field of def becomes the new content.
func (n *Network) UpdatePlatform(name string, def *spec.PlatformSpec) error {
	mu := n.locks.lock(keyPlatforms)
	mu.Lock()
	defer mu.Unlock()

	if _, exists := n.platforms.Platforms[name]; !exists {
		return &newtronErrors{notFound: true, resource: "platform", id: name}
	}
	n.platforms.Platforms[name] = def
	return n.persistPlatforms()
}

// DeletePlatform removes a platform definition. Refuses with
// *util.ConflictError when any profile still references this platform
// (matches the DeleteProfile referential-integrity pattern). There is
// no force=true cascade — a profile's Platform field is mandatory in
// practice, so cascading would orphan the profile; the operator must
// retarget or delete the referring profiles first.
func (n *Network) DeletePlatform(name string) error {
	// Profiles are owned by spec.Loader (one file per profile). Scan
	// every loaded profile for a match on Platform == name.
	referrers := make([]string, 0)
	for _, profName := range n.loader.ListProfiles() {
		prof, err := n.loader.LoadProfile(profName)
		if err != nil {
			continue
		}
		if prof.Platform == name {
			referrers = append(referrers, "profile '"+profName+"'")
		}
	}
	if len(referrers) > 0 {
		return &util.ConflictError{
			Resource:   "platform",
			Name:       name,
			References: referrers,
		}
	}

	mu := n.locks.lock(keyPlatforms)
	mu.Lock()
	defer mu.Unlock()

	if _, exists := n.platforms.Platforms[name]; !exists {
		return &newtronErrors{notFound: true, resource: "platform", id: name}
	}
	delete(n.platforms.Platforms, name)
	return n.persistPlatforms()
}

// persistPlatforms writes the current platforms snapshot to disk
// atomically. Mirrors persistSpec in shape — the caller holds
// keyPlatforms.Lock around the in-memory mutation and this persist
// call so concurrent CreatePlatform/UpdatePlatform/DeletePlatform
// can't interleave their persists.
func (n *Network) persistPlatforms() error {
	if n.loader == nil {
		return fmt.Errorf("no loader (in-memory only)")
	}
	return n.loader.SavePlatforms(n.platforms)
}

// GetPrefixList returns a prefix list by name.
func (n *Network) GetPrefixList(name string) ([]string, error) {
	return getSpec(n.locks.lock(keyNetworkSpec), n.spec.PrefixLists, "prefix list", util.NormalizeName(name))
}

// GetQoSPolicy returns a QoS policy by name.
func (n *Network) GetQoSPolicy(name string) (*spec.QoSPolicy, error) {
	return getSpec(n.locks.lock(keyNetworkSpec), n.spec.QoSPolicies, "QoS policy", util.NormalizeName(name))
}

// GetIPVPN returns an IP-VPN definition by name.
func (n *Network) GetIPVPN(name string) (*spec.IPVPNSpec, error) {
	return getSpec(n.locks.lock(keyNetworkSpec), n.spec.IPVPNs, "ipvpn", util.NormalizeName(name))
}

// GetMACVPN returns a MAC-VPN definition by name.
func (n *Network) GetMACVPN(name string) (*spec.MACVPNSpec, error) {
	return getSpec(n.locks.lock(keyNetworkSpec), n.spec.MACVPNs, "macvpn", util.NormalizeName(name))
}

// GetRoutePolicy returns a route policy by name.
func (n *Network) GetRoutePolicy(name string) (*spec.RoutePolicy, error) {
	return getSpec(n.locks.lock(keyNetworkSpec), n.spec.RoutePolicies, "route policy", util.NormalizeName(name))
}

// FindMACVPNByVNI returns the MACVPN name and spec for a given VNI.
// Returns ("", nil) if no MACVPN matches.
func (n *Network) FindMACVPNByVNI(vni int) (string, *spec.MACVPNSpec) {
	mu := n.locks.lock(keyNetworkSpec)
	mu.RLock()
	defer mu.RUnlock()

	for name, def := range n.spec.MACVPNs {
		if def.VNI == vni {
			return name, def
		}
	}
	return "", nil
}

// ListServices returns all available service names.
func (n *Network) ListServices() []string {
	mu := n.locks.lock(keyNetworkSpec)
	mu.RLock()
	defer mu.RUnlock()

	names := make([]string, 0, len(n.spec.Services))
	for name := range n.spec.Services {
		names = append(names, name)
	}
	return names
}

// ListFilters returns all available filter names.
func (n *Network) ListFilters() []string {
	mu := n.locks.lock(keyNetworkSpec)
	mu.RLock()
	defer mu.RUnlock()

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
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	spec.NormalizeIPVPNRefs(def)

	if n.spec.IPVPNs == nil {
		n.spec.IPVPNs = make(map[string]*spec.IPVPNSpec)
	}
	n.spec.IPVPNs[name] = def
	return n.persistSpec()
}

// DeleteIPVPN removes an IP-VPN definition from network.json.
// Returns error if any service references it.
func (n *Network) DeleteIPVPN(name string) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)

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
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	// MACVPNSpec has no name-reference fields to normalize.

	if n.spec.MACVPNs == nil {
		n.spec.MACVPNs = make(map[string]*spec.MACVPNSpec)
	}
	n.spec.MACVPNs[name] = def
	return n.persistSpec()
}

// DeleteMACVPN removes a MAC-VPN definition from network.json.
// Returns error if any service references it.
func (n *Network) DeleteMACVPN(name string) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)

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
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	// QoSPolicy has no name-reference fields to normalize.

	if n.spec.QoSPolicies == nil {
		n.spec.QoSPolicies = make(map[string]*spec.QoSPolicy)
	}
	n.spec.QoSPolicies[name] = def
	return n.persistSpec()
}

// DeleteQoSPolicy removes a QoS policy from network.json.
// Returns error if any service references it.
func (n *Network) DeleteQoSPolicy(name string) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)

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
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	spec.NormalizeFilterRefs(def)

	if n.spec.Filters == nil {
		n.spec.Filters = make(map[string]*spec.FilterSpec)
	}
	n.spec.Filters[name] = def
	return n.persistSpec()
}

// DeleteFilter removes a filter from network.json.
// Returns error if any service references it.
func (n *Network) DeleteFilter(name string) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)

	// Check for dependent services
	for svcName, svc := range n.spec.Services {
		if svc.IngressFilter == name || svc.EgressFilter == name {
			return fmt.Errorf("cannot delete filter '%s': referenced by service '%s'", name, svcName)
		}
	}

	delete(n.spec.Filters, name)
	return n.persistSpec()
}

// SavePrefixList saves a prefix list to the network spec.
func (n *Network) SavePrefixList(name string, prefixes []string) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)

	if n.spec.PrefixLists == nil {
		n.spec.PrefixLists = make(map[string][]string)
	}
	n.spec.PrefixLists[name] = prefixes
	return n.persistSpec()
}

// DeletePrefixList deletes a prefix list from the network spec.
func (n *Network) DeletePrefixList(name string) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)

	// Check for dependent filters
	for filterName, f := range n.spec.Filters {
		for _, r := range f.Rules {
			if r.SrcPrefixList == name || r.DstPrefixList == name {
				return fmt.Errorf("cannot delete prefix list '%s': referenced by filter '%s'", name, filterName)
			}
		}
	}
	// Check for dependent route policies
	for policyName, rp := range n.spec.RoutePolicies {
		for _, r := range rp.Rules {
			if r.PrefixList == name {
				return fmt.Errorf("cannot delete prefix list '%s': referenced by route policy '%s'", name, policyName)
			}
		}
	}

	delete(n.spec.PrefixLists, name)
	return n.persistSpec()
}

// SaveRoutePolicy saves a route policy to the network spec.
func (n *Network) SaveRoutePolicy(name string, def *spec.RoutePolicy) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)

	if n.spec.RoutePolicies == nil {
		n.spec.RoutePolicies = make(map[string]*spec.RoutePolicy)
	}
	spec.NormalizeRoutePolicyRefs(def)
	n.spec.RoutePolicies[name] = def
	return n.persistSpec()
}

// DeleteRoutePolicy deletes a route policy from the network spec.
func (n *Network) DeleteRoutePolicy(name string) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)

	// Check for dependent services
	for svcName, svc := range n.spec.Services {
		if svc.Routing != nil {
			if svc.Routing.ImportPolicy == name || svc.Routing.ExportPolicy == name {
				return fmt.Errorf("cannot delete route policy '%s': referenced by service '%s'", name, svcName)
			}
		}
	}

	delete(n.spec.RoutePolicies, name)
	return n.persistSpec()
}

// ListPrefixLists returns all prefix list names.
func (n *Network) ListPrefixLists() []string {
	mu := n.locks.lock(keyNetworkSpec)
	mu.RLock()
	defer mu.RUnlock()

	names := make([]string, 0, len(n.spec.PrefixLists))
	for name := range n.spec.PrefixLists {
		names = append(names, name)
	}
	return names
}

// ListRoutePolicies returns all route policy names.
func (n *Network) ListRoutePolicies() []string {
	mu := n.locks.lock(keyNetworkSpec)
	mu.RLock()
	defer mu.RUnlock()

	names := make([]string, 0, len(n.spec.RoutePolicies))
	for name := range n.spec.RoutePolicies {
		names = append(names, name)
	}
	return names
}

// SaveService creates or updates a service definition in network.json.
func (n *Network) SaveService(name string, def *spec.ServiceSpec) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	spec.NormalizeServiceRefs(def)

	if n.spec.Services == nil {
		n.spec.Services = make(map[string]*spec.ServiceSpec)
	}
	n.spec.Services[name] = def
	return n.persistSpec()
}

// DeleteService removes a service definition from network.json.
// Returns error if any interface has it applied (caller checks this).
func (n *Network) DeleteService(name string) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)

	delete(n.spec.Services, name)
	return n.persistSpec()
}

// ListQoSPolicies returns all QoS policy names.
func (n *Network) ListQoSPolicies() []string {
	mu := n.locks.lock(keyNetworkSpec)
	mu.RLock()
	defer mu.RUnlock()

	if n.spec.QoSPolicies == nil {
		return nil
	}
	names := make([]string, 0, len(n.spec.QoSPolicies))
	for name := range n.spec.QoSPolicies {
		names = append(names, name)
	}
	return names
}

// ============================================================================
// Atomic Create methods — single-Lock check + write + persist
// ============================================================================
//
// Each method holds keyNetworkSpec.Lock from the existence check through
// the in-memory mutation and the disk persist. This replaces the pre-PR-B
// pattern where the public layer composed internal.GetX (RLock + release)
// with internal.SaveX (Lock + release) — two concurrent CreateX(name) calls
// could both pass the existence check and both write, race-y. With the
// lock held across the whole operation, exactly one CreateX wins.
//
// Error shape is preserved for callers: the existing public layer returned
// `fmt.Errorf("X 'name' already exists", ...)` and so do these.

// CreateService atomically creates a new service definition. Returns an
// error if a service with the given name already exists.
func (n *Network) CreateService(name string, def *spec.ServiceSpec) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	if _, exists := n.spec.Services[name]; exists {
		return fmt.Errorf("service '%s' already exists", name)
	}
	spec.NormalizeServiceRefs(def)
	if n.spec.Services == nil {
		n.spec.Services = make(map[string]*spec.ServiceSpec)
	}
	n.spec.Services[name] = def
	return n.persistSpec()
}

// CreateIPVPN atomically creates a new IP-VPN definition. Returns an error
// if an IPVPN with the given name already exists.
func (n *Network) CreateIPVPN(name string, def *spec.IPVPNSpec) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	if _, exists := n.spec.IPVPNs[name]; exists {
		return fmt.Errorf("ipvpn '%s' already exists", name)
	}
	spec.NormalizeIPVPNRefs(def)
	if n.spec.IPVPNs == nil {
		n.spec.IPVPNs = make(map[string]*spec.IPVPNSpec)
	}
	n.spec.IPVPNs[name] = def
	return n.persistSpec()
}

// CreateMACVPN atomically creates a new MAC-VPN definition. Returns an error
// if a MACVPN with the given name already exists.
func (n *Network) CreateMACVPN(name string, def *spec.MACVPNSpec) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	if _, exists := n.spec.MACVPNs[name]; exists {
		return fmt.Errorf("macvpn '%s' already exists", name)
	}
	if n.spec.MACVPNs == nil {
		n.spec.MACVPNs = make(map[string]*spec.MACVPNSpec)
	}
	n.spec.MACVPNs[name] = def
	return n.persistSpec()
}

// CreateQoSPolicy atomically creates a new QoS policy. Returns an error
// if a policy with the given name already exists.
func (n *Network) CreateQoSPolicy(name string, def *spec.QoSPolicy) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	if _, exists := n.spec.QoSPolicies[name]; exists {
		return fmt.Errorf("QoS policy '%s' already exists", name)
	}
	if n.spec.QoSPolicies == nil {
		n.spec.QoSPolicies = make(map[string]*spec.QoSPolicy)
	}
	n.spec.QoSPolicies[name] = def
	return n.persistSpec()
}

// CreateFilter atomically creates a new filter. Returns an error if a
// filter with the given name already exists.
func (n *Network) CreateFilter(name string, def *spec.FilterSpec) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	if _, exists := n.spec.Filters[name]; exists {
		return fmt.Errorf("filter '%s' already exists", name)
	}
	spec.NormalizeFilterRefs(def)
	if n.spec.Filters == nil {
		n.spec.Filters = make(map[string]*spec.FilterSpec)
	}
	n.spec.Filters[name] = def
	return n.persistSpec()
}

// CreatePrefixList atomically creates a new prefix list. Returns an error
// if a prefix list with the given name already exists.
func (n *Network) CreatePrefixList(name string, prefixes []string) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	if _, exists := n.spec.PrefixLists[name]; exists {
		return fmt.Errorf("prefix list '%s' already exists", name)
	}
	if n.spec.PrefixLists == nil {
		n.spec.PrefixLists = make(map[string][]string)
	}
	n.spec.PrefixLists[name] = prefixes
	return n.persistSpec()
}

// CreateRoutePolicy atomically creates a new route policy. Returns an error
// if a route policy with the given name already exists.
func (n *Network) CreateRoutePolicy(name string, def *spec.RoutePolicy) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	if _, exists := n.spec.RoutePolicies[name]; exists {
		return fmt.Errorf("route policy '%s' already exists", name)
	}
	spec.NormalizeRoutePolicyRefs(def)
	if n.spec.RoutePolicies == nil {
		n.spec.RoutePolicies = make(map[string]*spec.RoutePolicy)
	}
	n.spec.RoutePolicies[name] = def
	return n.persistSpec()
}

// CreateZone atomically creates a new zone. Returns an error if a zone
// with the given name already exists.
func (n *Network) CreateZone(name string, zone *spec.ZoneSpec) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	if _, exists := n.spec.Zones[name]; exists {
		return fmt.Errorf("zone '%s' already exists", name)
	}
	if n.spec.Zones == nil {
		n.spec.Zones = make(map[string]*spec.ZoneSpec)
	}
	n.spec.Zones[name] = zone
	return n.persistSpec()
}

// CreateProfile atomically creates a new device profile. Delegates to
// spec.Loader.CreateProfile which holds Loader's RWMutex across the
// existence check and the file write.
func (n *Network) CreateProfile(name string, profile *spec.DeviceProfile) error {
	return n.loader.CreateProfile(name, profile)
}

// ============================================================================
// Update — full-replacement spec mutation (#152)
// ============================================================================
//
// Each Update method holds keyNetworkSpec.Lock across the existence
// check, the entry replacement, and the disk persist (matching the
// Create methods' invariant). Returns *newtronErrors{notFound: true,
// ...} when the named entry does not exist — the public layer
// translates this to NotFoundError → 404 at the HTTP boundary.
//
// Semantics are deliberately full-replacement, mirroring
// UpdateTopologyDevice (network.go:1550): every field on the given
// def becomes the new content for that name. Operators wanting
// patch-merge semantics build the merged structure client-side
// before calling Update.

// UpdateService atomically replaces an existing service definition.
func (n *Network) UpdateService(name string, def *spec.ServiceSpec) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	if _, exists := n.spec.Services[name]; !exists {
		return &newtronErrors{notFound: true, resource: "service", id: name}
	}
	spec.NormalizeServiceRefs(def)
	n.spec.Services[name] = def
	return n.persistSpec()
}

// UpdateIPVPN atomically replaces an existing IP-VPN definition.
func (n *Network) UpdateIPVPN(name string, def *spec.IPVPNSpec) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	if _, exists := n.spec.IPVPNs[name]; !exists {
		return &newtronErrors{notFound: true, resource: "ipvpn", id: name}
	}
	spec.NormalizeIPVPNRefs(def)
	n.spec.IPVPNs[name] = def
	return n.persistSpec()
}

// UpdateMACVPN atomically replaces an existing MAC-VPN definition.
func (n *Network) UpdateMACVPN(name string, def *spec.MACVPNSpec) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	if _, exists := n.spec.MACVPNs[name]; !exists {
		return &newtronErrors{notFound: true, resource: "macvpn", id: name}
	}
	n.spec.MACVPNs[name] = def
	return n.persistSpec()
}

// UpdateQoSPolicy atomically replaces an existing QoS policy.
func (n *Network) UpdateQoSPolicy(name string, def *spec.QoSPolicy) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	if _, exists := n.spec.QoSPolicies[name]; !exists {
		return &newtronErrors{notFound: true, resource: "qos-policy", id: name}
	}
	n.spec.QoSPolicies[name] = def
	return n.persistSpec()
}

// UpdateFilter atomically replaces an existing filter.
func (n *Network) UpdateFilter(name string, def *spec.FilterSpec) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	if _, exists := n.spec.Filters[name]; !exists {
		return &newtronErrors{notFound: true, resource: "filter", id: name}
	}
	spec.NormalizeFilterRefs(def)
	n.spec.Filters[name] = def
	return n.persistSpec()
}

// UpdatePrefixList atomically replaces an existing prefix list.
func (n *Network) UpdatePrefixList(name string, prefixes []string) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	if _, exists := n.spec.PrefixLists[name]; !exists {
		return &newtronErrors{notFound: true, resource: "prefix-list", id: name}
	}
	n.spec.PrefixLists[name] = prefixes
	return n.persistSpec()
}

// UpdateRoutePolicy atomically replaces an existing route policy.
func (n *Network) UpdateRoutePolicy(name string, def *spec.RoutePolicy) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	name = util.NormalizeName(name)
	if _, exists := n.spec.RoutePolicies[name]; !exists {
		return &newtronErrors{notFound: true, resource: "route-policy", id: name}
	}
	spec.NormalizeRoutePolicyRefs(def)
	n.spec.RoutePolicies[name] = def
	return n.persistSpec()
}

// UpdateZone atomically replaces an existing zone.
func (n *Network) UpdateZone(name string, zone *spec.ZoneSpec) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	if _, exists := n.spec.Zones[name]; !exists {
		return &newtronErrors{notFound: true, resource: "zone", id: name}
	}
	n.spec.Zones[name] = zone
	return n.persistSpec()
}

// UpdateProfile atomically replaces an existing device profile.
// Delegates to spec.Loader.UpdateProfile, which holds Loader's RWMutex
// across the existence check and the file write.
func (n *Network) UpdateProfile(name string, profile *spec.DeviceProfile) error {
	return n.loader.UpdateProfile(name, profile)
}

// ============================================================================
// Atomic Add / Remove methods — read-modify-write under one Lock
// ============================================================================
//
// Each method holds keyNetworkSpec.Lock across the parent lookup, the
// child-collection mutation, and the disk persist. This replaces the
// pre-PR-B pattern where the public layer composed internal.GetX + mutate
// + internal.SaveX as separate critical sections — two concurrent AddX
// calls could overwrite each other's mutations (last writer wins).

// AddQoSQueueToPolicy atomically inserts a QoS queue at the given index in
// a QoS policy. Returns an error if the policy doesn't exist or the index
// is already populated.
func (n *Network) AddQoSQueueToPolicy(policy string, queueID int, queue *spec.QoSQueue) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	policy = util.NormalizeName(policy)
	p, ok := n.spec.QoSPolicies[policy]
	if !ok {
		return fmt.Errorf("QoS policy '%s' not found", policy)
	}
	for len(p.Queues) <= queueID {
		p.Queues = append(p.Queues, nil)
	}
	if p.Queues[queueID] != nil {
		return fmt.Errorf("queue %d already exists in policy '%s'", queueID, policy)
	}
	p.Queues[queueID] = queue
	return n.persistSpec()
}

// RemoveQoSQueueFromPolicy atomically removes a QoS queue at the given
// index from a QoS policy. Returns an error if the policy doesn't exist
// or the index is out of range / empty. Trims trailing nil slots after
// removal to match the pre-refactor public behavior.
func (n *Network) RemoveQoSQueueFromPolicy(policy string, queueID int) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	policy = util.NormalizeName(policy)
	p, ok := n.spec.QoSPolicies[policy]
	if !ok {
		return fmt.Errorf("QoS policy '%s' not found", policy)
	}
	if queueID < 0 || queueID >= len(p.Queues) || p.Queues[queueID] == nil {
		return fmt.Errorf("queue %d not found in policy '%s'", queueID, policy)
	}
	p.Queues[queueID] = nil
	for len(p.Queues) > 0 && p.Queues[len(p.Queues)-1] == nil {
		p.Queues = p.Queues[:len(p.Queues)-1]
	}
	return n.persistSpec()
}

// AddFilterRule atomically appends a rule to a filter and re-sorts the rule
// slice by sequence number. Returns an error if the filter doesn't exist
// or a rule with the same sequence already exists.
func (n *Network) AddFilterRule(filter string, rule *spec.FilterRule) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	filter = util.NormalizeName(filter)
	f, ok := n.spec.Filters[filter]
	if !ok {
		return fmt.Errorf("filter '%s' not found", filter)
	}
	for _, r := range f.Rules {
		if r.Sequence == rule.Sequence {
			return fmt.Errorf("rule with priority %d already exists in filter '%s'", rule.Sequence, filter)
		}
	}
	f.Rules = append(f.Rules, rule)
	sort.Slice(f.Rules, func(i, j int) bool {
		return f.Rules[i].Sequence < f.Rules[j].Sequence
	})
	return n.persistSpec()
}

// UpdateFilterRule atomically replaces a filter rule's fields, optionally
// rotating its sequence number. Returns an error if the filter doesn't
// exist, the rule at the current sequence doesn't exist, or the requested
// new sequence collides with another rule. When newSequence is nil the
// rule keeps its current sequence; when non-nil the rule's sequence
// rotates to that value (renumber). Issue #209.
func (n *Network) UpdateFilterRule(filter string, currentSeq int, newRule *spec.FilterRule) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	filter = util.NormalizeName(filter)
	f, ok := n.spec.Filters[filter]
	if !ok {
		return fmt.Errorf("filter '%s' not found", filter)
	}
	// Locate the rule by its current sequence.
	var target *spec.FilterRule
	for _, r := range f.Rules {
		if r.Sequence == currentSeq {
			target = r
			break
		}
	}
	if target == nil {
		return fmt.Errorf("rule with priority %d not found in filter '%s'", currentSeq, filter)
	}
	// If the rule is being renumbered, ensure the target sequence isn't
	// already occupied by another rule.
	if newRule.Sequence != currentSeq {
		for _, r := range f.Rules {
			if r.Sequence == newRule.Sequence {
				return fmt.Errorf("rule with priority %d already exists in filter '%s'", newRule.Sequence, filter)
			}
		}
	}
	// Replace target's fields with newRule's. Done in place (same pointer)
	// so any external references stay valid; the slice doesn't need to
	// rebuild.
	*target = *newRule
	// Re-sort by sequence — the rule may have rotated to a different slot.
	sort.Slice(f.Rules, func(i, j int) bool {
		return f.Rules[i].Sequence < f.Rules[j].Sequence
	})
	return n.persistSpec()
}

// RemoveFilterRule atomically removes a rule from a filter by sequence
// number. Returns an error if the filter or rule doesn't exist.
func (n *Network) RemoveFilterRule(filter string, sequence int) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	filter = util.NormalizeName(filter)
	f, ok := n.spec.Filters[filter]
	if !ok {
		return fmt.Errorf("filter '%s' not found", filter)
	}
	found := false
	newRules := make([]*spec.FilterRule, 0, len(f.Rules))
	for _, r := range f.Rules {
		if r.Sequence == sequence {
			found = true
			continue
		}
		newRules = append(newRules, r)
	}
	if !found {
		return fmt.Errorf("rule with priority %d not found in filter '%s'", sequence, filter)
	}
	f.Rules = newRules
	return n.persistSpec()
}

// AddPrefixToPrefixList atomically appends a prefix to a prefix list.
// Returns an error if the list doesn't exist.
func (n *Network) AddPrefixToPrefixList(prefixList string, prefix string) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	prefixList = util.NormalizeName(prefixList)
	prefixes, ok := n.spec.PrefixLists[prefixList]
	if !ok {
		return fmt.Errorf("prefix list '%s' not found", prefixList)
	}
	prefixes = append(prefixes, prefix)
	n.spec.PrefixLists[prefixList] = prefixes
	return n.persistSpec()
}

// RemovePrefixFromPrefixList atomically removes a prefix from a prefix
// list. Returns an error if the list or prefix doesn't exist.
func (n *Network) RemovePrefixFromPrefixList(prefixList string, prefix string) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	prefixList = util.NormalizeName(prefixList)
	prefixes, ok := n.spec.PrefixLists[prefixList]
	if !ok {
		return fmt.Errorf("prefix list '%s' not found", prefixList)
	}
	found := false
	newPrefixes := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		if p == prefix {
			found = true
			continue
		}
		newPrefixes = append(newPrefixes, p)
	}
	if !found {
		return fmt.Errorf("prefix '%s' not found in prefix list '%s'", prefix, prefixList)
	}
	n.spec.PrefixLists[prefixList] = newPrefixes
	return n.persistSpec()
}

// AddRuleToRoutePolicy atomically appends a rule to a route policy and
// re-sorts the rule slice by sequence number. Returns an error if the
// policy doesn't exist or a rule with the same sequence already exists.
func (n *Network) AddRuleToRoutePolicy(policy string, rule *spec.RoutePolicyRule) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	policy = util.NormalizeName(policy)
	rp, ok := n.spec.RoutePolicies[policy]
	if !ok {
		return fmt.Errorf("route policy '%s' not found", policy)
	}
	for _, r := range rp.Rules {
		if r.Sequence == rule.Sequence {
			return fmt.Errorf("rule with sequence %d already exists in route policy '%s'", rule.Sequence, policy)
		}
	}
	rp.Rules = append(rp.Rules, rule)
	sort.Slice(rp.Rules, func(i, j int) bool {
		return rp.Rules[i].Sequence < rp.Rules[j].Sequence
	})
	return n.persistSpec()
}

// RemoveRuleFromRoutePolicy atomically removes a rule from a route policy
// by sequence number. Returns an error if the policy or rule doesn't exist.
func (n *Network) RemoveRuleFromRoutePolicy(policy string, sequence int) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	policy = util.NormalizeName(policy)
	rp, ok := n.spec.RoutePolicies[policy]
	if !ok {
		return fmt.Errorf("route policy '%s' not found", policy)
	}
	found := false
	newRules := make([]*spec.RoutePolicyRule, 0, len(rp.Rules))
	for _, r := range rp.Rules {
		if r.Sequence == sequence {
			found = true
			continue
		}
		newRules = append(newRules, r)
	}
	if !found {
		return fmt.Errorf("rule with sequence %d not found in route policy '%s'", sequence, policy)
	}
	rp.Rules = newRules
	return n.persistSpec()
}

// ============================================================================
// Snapshot methods — fresh-copy reads under RLock
// ============================================================================
//
// Each method takes keyNetworkSpec.RLock and returns a shallow copy of the
// underlying map. Callers iterate the returned map freely without racing
// any concurrent writer (the RLock blocks Lock until the snapshot is
// built). These replace the pre-PR-B pattern where public ListIPVPNs et al
// reached into net.internal.Spec() and iterated the raw map — a concurrent
// Save mutating that map would panic the runtime under -race.

// ServicesSnapshot returns a shallow copy of the Services map under read lock.
func (n *Network) ServicesSnapshot() map[string]*spec.ServiceSpec {
	mu := n.locks.lock(keyNetworkSpec)
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]*spec.ServiceSpec, len(n.spec.Services))
	for k, v := range n.spec.Services {
		out[k] = v
	}
	return out
}

// IPVPNsSnapshot returns a shallow copy of the IPVPNs map under read lock.
func (n *Network) IPVPNsSnapshot() map[string]*spec.IPVPNSpec {
	mu := n.locks.lock(keyNetworkSpec)
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]*spec.IPVPNSpec, len(n.spec.IPVPNs))
	for k, v := range n.spec.IPVPNs {
		out[k] = v
	}
	return out
}

// MACVPNsSnapshot returns a shallow copy of the MACVPNs map under read lock.
func (n *Network) MACVPNsSnapshot() map[string]*spec.MACVPNSpec {
	mu := n.locks.lock(keyNetworkSpec)
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]*spec.MACVPNSpec, len(n.spec.MACVPNs))
	for k, v := range n.spec.MACVPNs {
		out[k] = v
	}
	return out
}

// FiltersSnapshot returns a shallow copy of the Filters map under read lock.
func (n *Network) FiltersSnapshot() map[string]*spec.FilterSpec {
	mu := n.locks.lock(keyNetworkSpec)
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]*spec.FilterSpec, len(n.spec.Filters))
	for k, v := range n.spec.Filters {
		out[k] = v
	}
	return out
}

// QoSPoliciesSnapshot returns a shallow copy of the QoSPolicies map under read lock.
func (n *Network) QoSPoliciesSnapshot() map[string]*spec.QoSPolicy {
	mu := n.locks.lock(keyNetworkSpec)
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]*spec.QoSPolicy, len(n.spec.QoSPolicies))
	for k, v := range n.spec.QoSPolicies {
		out[k] = v
	}
	return out
}

// RoutePoliciesSnapshot returns a shallow copy of the RoutePolicies map under read lock.
func (n *Network) RoutePoliciesSnapshot() map[string]*spec.RoutePolicy {
	mu := n.locks.lock(keyNetworkSpec)
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]*spec.RoutePolicy, len(n.spec.RoutePolicies))
	for k, v := range n.spec.RoutePolicies {
		out[k] = v
	}
	return out
}

// PrefixListsSnapshot returns a shallow copy of the PrefixLists map under read lock.
func (n *Network) PrefixListsSnapshot() map[string][]string {
	mu := n.locks.lock(keyNetworkSpec)
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string][]string, len(n.spec.PrefixLists))
	for k, v := range n.spec.PrefixLists {
		out[k] = v
	}
	return out
}

// ZonesSnapshot returns a shallow copy of the Zones map under read lock.
func (n *Network) ZonesSnapshot() map[string]*spec.ZoneSpec {
	mu := n.locks.lock(keyNetworkSpec)
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]*spec.ZoneSpec, len(n.spec.Zones))
	for k, v := range n.spec.Zones {
		out[k] = v
	}
	return out
}

// Spec returns the raw network spec (for advanced access).
//
// Deprecated for read iteration — callers iterating any of the OverridableSpecs
// maps without holding keyNetworkSpec.RLock will race with concurrent writers
// and panic the runtime. Use a *Snapshot method instead.
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
	mu := n.locks.lock(keyPlatforms)
	mu.RLock()
	platform, ok := n.platforms.Platforms[profile.Platform]
	mu.RUnlock()
	if !ok {
		return false
	}
	return platform.IsHost()
}

// GetHostProfile returns the device profile for a host device.
func (n *Network) GetHostProfile(name string) (*spec.DeviceProfile, error) {
	return n.loadProfile(name)
}

// GetProfile returns the device profile for a named device.
func (n *Network) GetProfile(name string) (*spec.DeviceProfile, error) {
	return n.loadProfile(name)
}

// ListProfiles returns the names of all profile files in the nodes directory.
func (n *Network) ListProfiles() []string {
	return n.loader.ListProfiles()
}

// SaveProfile creates or updates a device profile.
func (n *Network) SaveProfile(name string, profile *spec.DeviceProfile) error {
	return n.loader.SaveProfile(name, profile)
}

// DeleteProfile removes a device profile. Refuses with *newtron.ConflictError
// when any topology device references the profile, unless force is true. With
// force=true, cascade-deletes every referring topology device (which in turn
// cascade-deletes any links wired to those devices) before removing the
// profile. Symmetric with DeleteTopologyDevice's cascade pattern — both honor
// §15 (operational symmetry; cascade is explicit, never implicit).
func (n *Network) DeleteProfile(name string, force bool) error {
	// A profile and a topology device share their name 1:1 — the profile
	// "reference" from topology is the matching name in topology.Devices.
	topoMu := n.locks.lock(keyTopology)
	topoMu.RLock()
	topo := n.loader.GetTopology()
	hasTopoDevice := topo != nil && topo.HasDevice(name)
	topoMu.RUnlock()

	if hasTopoDevice {
		if !force {
			return &util.ConflictError{
				Resource:   "profile",
				Name:       name,
				References: []string{"topology device '" + name + "'"},
			}
		}
		// Cascade: delete the topology device (which itself cascades to any
		// links wired to that device's endpoints) before removing the
		// profile file. force=true is propagated.
		if err := n.DeleteTopologyDevice(name, true); err != nil {
			return fmt.Errorf("cascade deleting topology device %s: %w", name, err)
		}
	}

	return n.loader.DeleteProfile(name)
}

// ListZones returns all zone names from the network spec.
func (n *Network) ListZones() []string {
	mu := n.locks.lock(keyNetworkSpec)
	mu.RLock()
	defer mu.RUnlock()

	names := make([]string, 0, len(n.spec.Zones))
	for name := range n.spec.Zones {
		names = append(names, name)
	}
	return names
}

// GetZone returns a zone spec by name.
func (n *Network) GetZone(name string) (*spec.ZoneSpec, error) {
	mu := n.locks.lock(keyNetworkSpec)
	mu.RLock()
	defer mu.RUnlock()

	z, ok := n.spec.Zones[name]
	if !ok {
		return nil, fmt.Errorf("zone '%s' not found", name)
	}
	return z, nil
}

// SaveZone creates or updates a zone in network.json.
func (n *Network) SaveZone(name string, zone *spec.ZoneSpec) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	if n.spec.Zones == nil {
		n.spec.Zones = make(map[string]*spec.ZoneSpec)
	}
	n.spec.Zones[name] = zone
	return n.persistSpec()
}

// DeleteZone removes a zone from network.json.
// Returns error if any profile references it.
func (n *Network) DeleteZone(name string) error {
	mu := n.locks.lock(keyNetworkSpec)
	mu.Lock()
	defer mu.Unlock()

	// Check for profiles in this zone
	profiles := n.loader.ListProfiles()
	for _, pName := range profiles {
		p, err := n.loader.LoadProfile(pName)
		if err != nil {
			continue
		}
		if p.Zone == name {
			return fmt.Errorf("cannot delete zone '%s': referenced by profile '%s'", name, pName)
		}
	}

	delete(n.spec.Zones, name)
	return n.persistSpec()
}

// ============================================================================
// Device (Node) Management
// ============================================================================

// GetDevice returns an existing device or loads it from profile.
// The Device is created in this Network's context and has access to all
// Network-level specs through its parent reference.
func (n *Network) GetNode(name string) (*node.Node, error) {
	// Lock-ordering rule: alphabetical by key. keyNetworkSpec < keyNodes.
	// resolveProfile + buildResolvedSpecs read n.spec.Zones and other
	// network.json maps; the cache write requires keyNodes.Lock.
	netMu := n.locks.lock(keyNetworkSpec)
	netMu.RLock()
	defer netMu.RUnlock()
	nodesMu := n.locks.lock(keyNodes)
	nodesMu.Lock()
	defer nodesMu.Unlock()

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
	dev := node.New(resolvedSpecs, name, profile, resolved, n.topologyName, n.portResolver)

	n.devices[name] = dev
	return dev, nil
}

// GetAbstractNode creates an offline abstract Node for the named device.
// Same profile/spec resolution as GetNode, but the Node starts with an empty
// projection and no device connection. Used for composite generation.
func (n *Network) GetAbstractNode(name string) (*node.Node, error) {
	// Host devices have no SONiC — cannot create a Node
	if n.IsHostDevice(name) {
		return nil, fmt.Errorf("device '%s' is a host (no SONiC); use GetHostProfile() instead", name)
	}

	profile, err := n.loadProfile(name)
	if err != nil {
		return nil, fmt.Errorf("loading profile for %s: %w", name, err)
	}

	resolved, err := n.resolveProfile(name, profile)
	if err != nil {
		return nil, fmt.Errorf("resolving profile for %s: %w", name, err)
	}

	resolvedSpecs := n.buildResolvedSpecs(profile)
	return node.NewAbstract(resolvedSpecs, name, profile, resolved, n.topologyName, n.portResolver), nil
}

// ConnectNodeForSetup connects without requiring frrcfgd. Used by
// provisioning and InitDevice — both write unified config mode and
// restart bgp afterward, so the check is skipped.
func (n *Network) ConnectNodeForSetup(ctx context.Context, name string) (*node.Node, error) {
	dev, err := n.GetNode(name)
	if err != nil {
		return nil, err
	}

	if err := dev.ConnectForSetup(ctx); err != nil {
		return nil, err
	}

	return dev, nil
}

// InitFromDeviceIntent creates a fresh abstract node, connects to the device,
// and replays its NEWTRON_INTENT records to build the projection. The node's
// projection is derived entirely from intent replay — the device's raw CONFIG_DB
// is never assigned to the node. Returns the fully-initialized node.
//
// Architecture §3: "Device intents → New() → ConnectTransport() → read PORT +
// NEWTRON_INTENT → RegisterPort() → IntentsToSteps() → ReplayStep()"
func (n *Network) InitFromDeviceIntent(ctx context.Context, name string) (*node.Node, error) {
	dev, err := n.GetAbstractNode(name)
	if err != nil {
		return nil, err
	}
	if err := dev.InitFromDeviceIntent(ctx); err != nil {
		return nil, err
	}
	return dev, nil
}

// ListDevices returns names of all loaded devices.
func (n *Network) ListNodes() []string {
	mu := n.locks.lock(keyNodes)
	mu.RLock()
	defer mu.RUnlock()

	names := make([]string, 0, len(n.devices))
	for name := range n.devices {
		names = append(names, name)
	}
	return names
}

// ============================================================================
// Topology CRUD — §7 (network-scoped definition), §27 (single owner =
// spec.Loader.SaveTopology), §28 (file-level ownership: spec-layer mutation
// lives here, not in handlers), §15 (cascade is explicit via force).
// ============================================================================

// AddTopologyDevice adds a device entry to topology.json. Returns
// *util.ConflictError when the name already exists (re-using the conflict
// vocab for duplicate-as-conflict). Validates that the matching profile file
// exists. Persists atomically via spec.Loader.SaveTopology.
func (n *Network) AddTopologyDevice(name string, device *spec.TopologyDevice) error {
	if name == "" {
		return fmt.Errorf("topology device name required")
	}
	if device == nil {
		return fmt.Errorf("device entry required")
	}

	mu := n.locks.lock(keyTopology)
	mu.Lock()
	defer mu.Unlock()

	topo := n.loader.GetTopology()
	if topo == nil {
		topo = &spec.TopologySpecFile{Version: "1.0", Devices: map[string]*spec.TopologyDevice{}}
	}
	if _, exists := topo.Devices[name]; exists {
		return &util.ConflictError{
			Resource:   "topology-device",
			Name:       name,
			References: []string{"already declared in topology.json"},
		}
	}

	// Profile file must exist — same invariant validateTopology enforces.
	if _, err := n.loader.LoadProfile(name); err != nil {
		return fmt.Errorf("profile for topology device %s: %w", name, err)
	}

	// Stage the mutation on a working copy so persistence failure leaves
	// the in-memory state untouched.
	working := cloneTopology(topo)
	if working.Devices == nil {
		working.Devices = map[string]*spec.TopologyDevice{}
	}
	working.Devices[name] = device

	return n.applyTopology(working)
}

// DeleteTopologyDevice removes a device entry from topology.json. Refuses
// with *util.ConflictError when any link still references the device, unless
// force=true. With force=true, also removes every referring link before
// removing the device. Persists atomically.
//
// Does NOT close any api-layer NodeActor cache that may hold a built node
// for this name — that's the caller's (handler's) job.
func (n *Network) DeleteTopologyDevice(name string, force bool) error {
	if name == "" {
		return fmt.Errorf("topology device name required")
	}

	// Lock-ordering rule: alphabetical by key. keyNodes < keyTopology.
	// keyNodes covers the n.devices cache clear at the end of this method;
	// keyTopology covers the topology.json mutation.
	nodesMu := n.locks.lock(keyNodes)
	nodesMu.Lock()
	defer nodesMu.Unlock()
	topoMu := n.locks.lock(keyTopology)
	topoMu.Lock()
	defer topoMu.Unlock()

	topo := n.loader.GetTopology()
	if topo == nil || !topo.HasDevice(name) {
		return &newtronErrors{notFound: true, resource: "topology-device", id: name}
	}

	// Find referring links (either endpoint is on this device).
	var referring []*spec.TopologyLink
	for _, link := range topo.Links {
		if linkReferencesDevice(link, name) {
			referring = append(referring, link)
		}
	}
	if len(referring) > 0 && !force {
		refs := make([]string, 0, len(referring))
		for _, l := range referring {
			refs = append(refs, "link "+l.A+" ↔ "+l.Z)
		}
		sort.Strings(refs)
		return &util.ConflictError{
			Resource:   "topology-device",
			Name:       name,
			References: refs,
		}
	}

	// Stage on a working copy. Remove referring links first, then the device.
	working := cloneTopology(topo)
	if len(referring) > 0 {
		working.Links = filterLinks(working.Links, func(l *spec.TopologyLink) bool {
			return !linkReferencesDevice(l, name)
		})
	}
	delete(working.Devices, name)

	if err := n.applyTopology(working); err != nil {
		return err
	}

	// Also clear any in-memory loaded Node for this name; the spec entry is gone.
	delete(n.devices, name)
	return nil
}

// UpdateTopologyDevice replaces the device entry at name with the given
// TopologyDevice (full-replacement semantics; no partial patch). Returns
// NotFoundError when the name doesn't exist. Validates profile file.
//
// Does NOT close any api-layer NodeActor cache — handler's job (the cached
// abstract node now reflects stale spec until the actor is reset).
func (n *Network) UpdateTopologyDevice(name string, device *spec.TopologyDevice) error {
	if name == "" {
		return fmt.Errorf("topology device name required")
	}
	if device == nil {
		return fmt.Errorf("device entry required")
	}

	// Lock-ordering rule: alphabetical by key. keyNodes < keyTopology.
	// keyNodes covers the n.devices cache clear at the end; keyTopology
	// covers the topology.json mutation.
	nodesMu := n.locks.lock(keyNodes)
	nodesMu.Lock()
	defer nodesMu.Unlock()
	topoMu := n.locks.lock(keyTopology)
	topoMu.Lock()
	defer topoMu.Unlock()

	topo := n.loader.GetTopology()
	if topo == nil || !topo.HasDevice(name) {
		return &newtronErrors{notFound: true, resource: "topology-device", id: name}
	}

	if _, err := n.loader.LoadProfile(name); err != nil {
		return fmt.Errorf("profile for topology device %s: %w", name, err)
	}

	working := cloneTopology(topo)
	working.Devices[name] = device

	if err := n.applyTopology(working); err != nil {
		return err
	}
	// In-memory loaded Node (if any) is now stale — drop it so the next
	// access rebuilds from the new spec.
	delete(n.devices, name)
	return nil
}

// AddTopologyLink adds a link to topology.json. Returns *util.ConflictError
// when either endpoint is already occupied by another link (a port
// participates in at most one link). Validates that both endpoint devices
// exist in the topology AND that the interface is declared on each device's
// Ports map.
func (n *Network) AddTopologyLink(link *spec.TopologyLink) error {
	if link == nil {
		return fmt.Errorf("link entry required")
	}
	if link.A == "" || link.Z == "" {
		return fmt.Errorf("link endpoints required (a, z)")
	}

	mu := n.locks.lock(keyTopology)
	mu.Lock()
	defer mu.Unlock()

	topo := n.loader.GetTopology()
	if topo == nil {
		topo = &spec.TopologySpecFile{Version: "1.0", Devices: map[string]*spec.TopologyDevice{}}
	}

	// Endpoint format: "device:interface". Validate both ends.
	for _, ep := range []string{link.A, link.Z} {
		dev, iface, ok := splitTopologyEndpoint(ep)
		if !ok {
			return fmt.Errorf("invalid endpoint '%s' (expected 'device:interface')", ep)
		}
		d, exists := topo.Devices[dev]
		if !exists {
			return fmt.Errorf("endpoint %s: device '%s' not in topology", ep, dev)
		}
		if d.Ports != nil {
			if _, declared := d.Ports[iface]; !declared {
				return fmt.Errorf("endpoint %s: interface '%s' not declared on device '%s'",
					ep, iface, dev)
			}
		}
	}

	// Each port participates in at most one link. Refuse if either endpoint
	// is already wired.
	for _, ep := range []string{link.A, link.Z} {
		for _, existing := range topo.Links {
			if existing.A == ep || existing.Z == ep {
				return &util.ConflictError{
					Resource:   "topology-link",
					Name:       ep,
					References: []string{"already wired in link " + existing.A + " ↔ " + existing.Z},
				}
			}
		}
	}

	working := cloneTopology(topo)
	working.Links = append(working.Links, link)
	return n.applyTopology(working)
}

// DeleteTopologyLink removes the link whose A or Z endpoint matches the
// given "device:interface" string. Per scope-design Q3: a port participates
// in at most one link, so a single endpoint uniquely identifies the link.
// Returns NotFoundError when no link contains the endpoint.
func (n *Network) DeleteTopologyLink(endpoint string) error {
	if endpoint == "" {
		return fmt.Errorf("link endpoint required (device:interface)")
	}

	mu := n.locks.lock(keyTopology)
	mu.Lock()
	defer mu.Unlock()

	topo := n.loader.GetTopology()
	if topo == nil {
		return &newtronErrors{notFound: true, resource: "topology-link", id: endpoint}
	}

	idx := -1
	for i, l := range topo.Links {
		if l.A == endpoint || l.Z == endpoint {
			idx = i
			break
		}
	}
	if idx < 0 {
		return &newtronErrors{notFound: true, resource: "topology-link", id: endpoint}
	}

	working := cloneTopology(topo)
	working.Links = append(working.Links[:idx], working.Links[idx+1:]...)
	return n.applyTopology(working)
}

// ----------------------------------------------------------------------------
// topology CRUD helpers
// ----------------------------------------------------------------------------

// applyTopology persists the given working spec via SaveTopology and updates
// the cached n.topology pointer in lockstep. Caller must hold n.mu (write
// lock). Network.topology is a separate cache from the loader's copy; both
// must move together on every mutation, else GetTopologyDevice/HasTopology
// readers see stale state.
func (n *Network) applyTopology(working *spec.TopologySpecFile) error {
	if err := n.loader.SaveTopology(working); err != nil {
		return err
	}
	n.topology = working
	return nil
}

// cloneTopology returns a shallow copy of topo with the Devices map and Links
// slice copied so mutation of the working copy doesn't bleed into the
// in-memory loader cache before SaveTopology swaps it in.
func cloneTopology(topo *spec.TopologySpecFile) *spec.TopologySpecFile {
	out := &spec.TopologySpecFile{
		Version:     topo.Version,
		Description: topo.Description,
		NewtLab:     topo.NewtLab,
	}
	if topo.Devices != nil {
		out.Devices = make(map[string]*spec.TopologyDevice, len(topo.Devices))
		for k, v := range topo.Devices {
			out.Devices[k] = v
		}
	}
	if len(topo.Links) > 0 {
		out.Links = make([]*spec.TopologyLink, len(topo.Links))
		copy(out.Links, topo.Links)
	}
	return out
}

func linkReferencesDevice(l *spec.TopologyLink, deviceName string) bool {
	for _, ep := range []string{l.A, l.Z} {
		d, _, ok := splitTopologyEndpoint(ep)
		if ok && d == deviceName {
			return true
		}
	}
	return false
}

func filterLinks(in []*spec.TopologyLink, keep func(*spec.TopologyLink) bool) []*spec.TopologyLink {
	out := in[:0]
	for _, l := range in {
		if keep(l) {
			out = append(out, l)
		}
	}
	return out
}

func splitTopologyEndpoint(ep string) (device, iface string, ok bool) {
	for i := 0; i < len(ep); i++ {
		if ep[i] == ':' {
			return ep[:i], ep[i+1:], true
		}
	}
	return "", "", false
}

// newtronErrors is the network package's internal error variant for cases
// where parent newtron's NotFoundError needs to surface. Translated to
// *newtron.NotFoundError by the parent public wrapper. Stays in this package
// to avoid a circular import (network is upstream of newtron).
type newtronErrors struct {
	notFound bool
	resource string
	id       string
}

func (e *newtronErrors) Error() string {
	if e.notFound {
		return fmt.Sprintf("%s '%s' not found", e.resource, e.id)
	}
	return "unspecified network error"
}

// IsNotFound is the helper the public wrapper uses to translate.
func (e *newtronErrors) IsNotFound() bool { return e.notFound }

func (e *newtronErrors) Resource() string { return e.resource }

func (e *newtronErrors) ID() string { return e.id }


// isHostDeviceLocked checks host status without acquiring the mutex (caller
// must hold lock). The "lock" the doc-name refers to is the caller's
// existing keyTopology or keyNetworkSpec lock; platforms still needs its
// own RLock because keyPlatforms is independent (#173).
func (n *Network) isHostDeviceLocked(name string) bool {
	profile, err := n.loader.LoadProfile(name)
	if err != nil || profile.Platform == "" {
		return false
	}
	mu := n.locks.lock(keyPlatforms)
	mu.RLock()
	platform, ok := n.platforms.Platforms[profile.Platform]
	mu.RUnlock()
	if !ok {
		return false
	}
	return platform.IsHost()
}

// loadProfile loads a device profile from the nodes directory and
// resolves any ${secret:KEY} references in its SSH credentials
// (auth-design.md L0). The loader caches the in-memory profile, so
// once resolution runs the cached value carries plaintext; later
// reads return the resolved value without re-resolving. A missing
// store + a reference in the profile is a hard error from
// secret.Resolve — operators learn at the first GetProfile call
// rather than silently SSH'ing with "${secret:...}" as the password.
func (n *Network) loadProfile(name string) (*spec.DeviceProfile, error) {
	profile, err := n.loader.LoadProfile(name)
	if err != nil {
		return nil, err
	}
	if err := resolveProfileSecrets(profile, n.secretStore); err != nil {
		return nil, fmt.Errorf("profile %q: %w", name, err)
	}
	return profile, nil
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
	resolved.BGPNeighbors, resolved.BGPNeighborASNs = n.deriveBGPNeighbors(profile, name)

	// SSH credentials (for Redis tunnel). SSH port is resolved from
	// newtlab at Device.Connect time, not from the profile spec.
	// auth-design.md L0: ssh_user / ssh_pass may carry ${secret:KEY}
	// references; secret.Resolve does the lookup against the
	// configured store, or passes plaintext through unchanged.
	sshUser, err := secret.Resolve(profile.SSHUser, n.secretStore)
	if err != nil {
		return nil, fmt.Errorf("profile %q ssh_user: %w", name, err)
	}
	sshPass, err := secret.Resolve(profile.SSHPass, n.secretStore)
	if err != nil {
		return nil, fmt.Errorf("profile %q ssh_pass: %w", name, err)
	}
	resolved.SSHUser = sshUser
	resolved.SSHPass = sshPass

	// eBGP underlay ASN
	resolved.UnderlayASN = profile.UnderlayASN

	return resolved, nil
}

// buildResolvedSpecs merges all 7 overridable spec maps with hierarchical
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
		RoutePolicies: util.MergeMaps(n.spec.RoutePolicies, zone.RoutePolicies, profile.RoutePolicies),
	}

	return newResolvedSpecs(merged, n)
}

// deriveBGPNeighbors looks up EVPN peer loopback IPs and ASNs from their profiles.
// Silently skips peers that aren't in the current topology (e.g., spine2 in a 2-node topo).
func (n *Network) deriveBGPNeighbors(profile *spec.DeviceProfile, selfName string) ([]string, map[string]int) {
	if profile.EVPN == nil {
		return nil, nil
	}
	topo := n.GetTopology()
	var neighbors []string
	asns := make(map[string]int)
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
		// Load peer profile to get its loopback IP and ASN
		peerProfile, err := n.loadProfile(peerName)
		if err != nil {
			util.Logger.Warnf("Could not load EVPN peer profile %s: %v", peerName, err)
			continue
		}
		neighbors = append(neighbors, peerProfile.LoopbackIP)
		asns[peerProfile.LoopbackIP] = peerProfile.UnderlayASN
	}
	return neighbors, asns
}
