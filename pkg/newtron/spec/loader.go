package spec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aldrin-isaac/newtron/pkg/util"
)

// Dir is the default specification directory
var Dir = "/etc/newtron"

// Loader handles loading and validating specification files.
//
// All post-Load() access to the mutable in-memory state — the lazy-loaded
// profile cache, and the network / topology / platforms pointers that get
// reassigned by SaveNetwork / SaveTopology / SavePlatforms — is guarded
// by mu. (Pre-#173, platforms was documented as read-only; the platform
// CRUD endpoints retired that invariant.)
type Loader struct {
	specDir string
	// platforms is a read-only view of the global platforms registry,
	// injected at construction. Used only by validateNodeSpec to apply
	// the relaxed-validation path on host-type profiles. Nil is safe
	// (every platform is treated as non-host).
	platforms map[string]*PlatformSpec

	mu       sync.RWMutex
	network  *NetworkSpecFile
	topology *TopologySpecFile // nil if topology.json doesn't exist
	profiles map[string]*NodeSpec
}

// NewLoader creates a new specification loader. platforms is the
// global registry that newt-server loaded from --platforms-base —
// pass nil if no platforms are known (tests, profile-only fixtures).
func NewLoader(specDir string, platforms map[string]*PlatformSpec) *Loader {
	if specDir == "" {
		specDir = Dir
	}
	return &Loader{
		specDir:   specDir,
		platforms: platforms,
		profiles:  make(map[string]*NodeSpec),
	}
}

// Load loads all specification files
func (l *Loader) Load() error {
	// A network directory must carry at least one of network.json or
	// topology.json. network.json alone is a scaffolded/offline network
	// (services/VPNs defined, no substrate yet); topology.json alone is a
	// lab-only network (newtlab deploys the VMs, an external system such as
	// netconf.pl owns device config). Neither file means the directory is not
	// a network at all — reject it rather than load an empty placeholder.
	_, netStatErr := os.Stat(filepath.Join(l.specDir, "network.json"))
	_, topoStatErr := os.Stat(filepath.Join(l.specDir, "topology.json"))
	if os.IsNotExist(netStatErr) && os.IsNotExist(topoStatErr) {
		return fmt.Errorf("not a network directory: neither network.json nor topology.json present in %s", l.specDir)
	}

	var err error

	// Load network spec (optional — empty spec when only topology.json exists).
	l.network, err = l.loadNetworkSpec()
	if err != nil {
		return fmt.Errorf("loading network spec: %w", err)
	}

	// Platform specs are loaded once at server startup by
	// LoadPlatformsFromDir and shared across networks — not the
	// per-network Loader's concern anymore.

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

// LoadNodeSpec loads a node spec, caching it for subsequent reads.
// Concurrent first-time loads for the same name may each do the disk read,
// but only one wins the cache slot; later callers see the cached value.
func (l *Loader) LoadNodeSpec(deviceName string) (*NodeSpec, error) {
	l.mu.RLock()
	if profile, ok := l.profiles[deviceName]; ok {
		l.mu.RUnlock()
		return profile, nil
	}
	l.mu.RUnlock()

	profile, err := l.readNodeSpecFromDisk(deviceName)
	if err != nil {
		return nil, err
	}

	// Double-check under the write lock — another goroutine may have raced
	// us through the disk read; if so, return its cached copy so we don't
	// re-publish a new pointer that callers already hold a reference to.
	l.mu.Lock()
	defer l.mu.Unlock()
	if existing, ok := l.profiles[deviceName]; ok {
		return existing, nil
	}
	l.profiles[deviceName] = profile
	return profile, nil
}

// readNodeSpecFromDisk reads, parses, normalizes, and validates a profile from
// nodes/<name>.json. It does NOT consult or update the cache, acquire the lock,
// or resolve secrets — the returned profile carries its ${secret:...} references
// verbatim, so it is safe to mutate and persist without leaking resolved values.
func (l *Loader) readNodeSpecFromDisk(name string) (*NodeSpec, error) {
	path := filepath.Join(l.specDir, "nodes", name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading profile %s: %w", name, err)
	}

	var profile NodeSpec
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, fmt.Errorf("parsing profile %s: %w", name, err)
	}

	// Normalize name keys and name-reference fields at load time.
	normalizeOverridableSpecs(&profile.OverridableSpecs)

	if err := l.validateNodeSpec(&profile); err != nil {
		return nil, fmt.Errorf("validating profile %s: %w", name, err)
	}
	return &profile, nil
}

func (l *Loader) loadNetworkSpec() (*NetworkSpecFile, error) {
	path := filepath.Join(l.specDir, "network.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// network.json is optional — symmetric with topology.json. A
			// directory with only a topology.json is a lab-only network:
			// newtlab deploys the VMs from the topology, node profiles, and
			// global platforms, while an external system owns device config
			// (e.g. the vJunos topologies configured by netconf.pl). No
			// services, VPNs, or zones are defined, so the projection starts
			// empty — the §1 abstract-node "topology offline" state. validate()
			// and validateTopology() both tolerate the empty spec (they range
			// over the empty maps and check only topology-local references).
			return &NetworkSpecFile{}, nil
		}
		return nil, err
	}

	var spec NetworkSpecFile
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}

	// Normalize all name keys and name-reference fields at load time.
	normalizeOverridableSpecs(&spec.OverridableSpecs)
	for _, zone := range spec.Zones {
		normalizeOverridableSpecs(&zone.OverridableSpecs)
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
		seenNames := make(map[string]bool) // queue name uniqueness

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

// isHostPlatform returns true if the given platform name refers to a host device type.
func (l *Loader) isHostPlatform(platformName string) bool {
	if l.platforms == nil || platformName == "" {
		return false
	}
	platform, ok := l.platforms[platformName]
	if !ok {
		return false
	}
	return platform.IsHost()
}

func (l *Loader) validateNodeSpec(profile *NodeSpec) error {
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

	return v.Build()
}

// GetNetwork returns the network spec. Reads l.network under RLock — the
// pointer is reassigned by SaveNetwork.
func (l *Loader) GetNetwork() *NetworkSpecFile {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.network
}

// GetTopology returns the topology spec, or nil if no topology.json was found.
// Reads l.topology under RLock — the pointer is reassigned by SaveTopology.
func (l *Loader) GetTopology() *TopologySpecFile {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.topology
}

// ListNodeSpecs returns the names of all profile files in the nodes directory.
func (l *Loader) ListNodeSpecs() []string {
	profileDir := filepath.Join(l.specDir, "nodes")
	entries, err := os.ReadDir(profileDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".json") {
			names = append(names, strings.TrimSuffix(name, ".json"))
		}
	}
	return names
}

// UpdateNodeSpec atomically overwrites an existing profile file with the
// given replacement. Returns an error if no profile with that name
// exists (either in the in-memory cache or on disk). The whole check +
// write runs under l.mu.Lock so a concurrent CreateNodeSpec/UpdateNodeSpec
// for the same name can't both succeed against the same starting state.
//
// Race-safe alternative to LoadNodeSpec-then-SaveNodeSpec composed at a
// higher layer.
func (l *Loader) UpdateNodeSpec(name string, profile *NodeSpec) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	profileDir := filepath.Join(l.specDir, "nodes")
	path := filepath.Join(profileDir, name+".json")

	// Existence check: cache hit, OR on-disk file present (profile may
	// have been written before this Loader started and never loaded).
	if _, cached := l.profiles[name]; !cached {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("profile '%s' does not exist", name)
		}
	}

	// Inline what SaveNodeSpec does (we already hold the lock).
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		return fmt.Errorf("creating nodes directory: %w", err)
	}
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling profile %s: %w", name, err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(profileDir, "profile-*.json.tmp")
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
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming temp file: %w", err)
	}
	l.profiles[name] = profile
	return nil
}

// SaveNodeSpec writes a node spec to disk atomically (temp file + rename).
func (l *Loader) SaveNodeSpec(name string, profile *NodeSpec) error {
	if err := l.writeNodeSpecFile(name, profile); err != nil {
		return err
	}

	// Update the in-memory cache under the write lock so concurrent
	// LoadNodeSpec readers see the new pointer atomically.
	l.mu.Lock()
	l.profiles[name] = profile
	l.mu.Unlock()

	return nil
}

// writeNodeSpecFile marshals profile to nodes/<name>.json atomically (temp +
// rename). It touches neither the lock nor the cache — callers handle cache
// coherence around it.
func (l *Loader) writeNodeSpecFile(name string, profile *NodeSpec) error {
	profileDir := filepath.Join(l.specDir, "nodes")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		return fmt.Errorf("creating nodes directory: %w", err)
	}

	path := filepath.Join(profileDir, name+".json")

	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling profile %s: %w", name, err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(profileDir, "profile-*.json.tmp")
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

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

// MutateNodeSpec atomically applies fn to a profile and persists it, serialized
// against every other profile write under the loader lock.
//
// It reads the profile FRESH from disk rather than from the cache, on purpose:
// the cached pointer may have had its ${secret:...} fields resolved in place by
// a prior read (network.loadNodeSpec resolves secrets on the returned pointer),
// and persisting that would write resolved secrets to disk. The on-disk form
// keeps the references, so a round-trip through disk is secret-safe. After a
// successful write the cache entry is invalidated, so the next LoadNodeSpec
// re-reads the updated file.
//
// fn must not call back into the loader (it would re-enter l.mu). It receives the
// raw profile; callers mutate profile.OverridableSpecs for scoped spec writes.
func (l *Loader) MutateNodeSpec(name string, fn func(*NodeSpec) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	profile, err := l.readNodeSpecFromDisk(name)
	if err != nil {
		return err
	}
	if err := fn(profile); err != nil {
		return err
	}
	if err := l.writeNodeSpecFile(name, profile); err != nil {
		return err
	}
	delete(l.profiles, name) // invalidate; next LoadNodeSpec re-reads fresh
	return nil
}

// CreateNodeSpec atomically creates a new profile file. Returns an error if
// a profile with that name already exists (either in the in-memory cache
// or on disk). The whole check + write runs under l.mu.Lock so concurrent
// CreateNodeSpec calls for the same name can't both succeed.
//
// Race-safe alternative to LoadNodeSpec-then-SaveNodeSpec composed at a
// higher layer — the same composition used to live in public
// (*newtron.Network).CreateNodeSpec, which raced.
func (l *Loader) CreateNodeSpec(name string, profile *NodeSpec) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Cache hit means it exists in memory.
	if _, exists := l.profiles[name]; exists {
		return fmt.Errorf("profile '%s' already exists", name)
	}

	// On-disk file may exist even when the cache hasn't seen it yet —
	// e.g. profile was written before this Loader started, then not
	// loaded yet. Check the filesystem too.
	profileDir := filepath.Join(l.specDir, "nodes")
	path := filepath.Join(profileDir, name+".json")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("profile '%s' already exists", name)
	}

	// Inline what SaveNodeSpec does (we already hold the lock).
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		return fmt.Errorf("creating nodes directory: %w", err)
	}
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling profile %s: %w", name, err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(profileDir, "profile-*.json.tmp")
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
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming temp file: %w", err)
	}
	l.profiles[name] = profile
	return nil
}

// DeleteNodeSpec removes a node spec file and its cache entry.
func (l *Loader) DeleteNodeSpec(name string) error {
	path := filepath.Join(l.specDir, "nodes", name+".json")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("deleting profile %s: %w", name, err)
	}
	l.mu.Lock()
	delete(l.profiles, name)
	l.mu.Unlock()
	return nil
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

	// Reassign the in-memory pointer under the write lock so concurrent
	// GetNetwork readers see the new pointer atomically.
	l.mu.Lock()
	l.network = spec
	l.mu.Unlock()

	return nil
}

// SaveTopology writes the topology spec to disk atomically (temp file + rename).
func (l *Loader) SaveTopology(spec *TopologySpecFile) error {
	path := filepath.Join(l.specDir, "topology.json")

	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling topology spec: %w", err)
	}
	data = append(data, '\n')

	// Write to temp file in the same directory (ensures same filesystem for atomic rename)
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "topology-*.json.tmp")
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

	// Reassign the in-memory pointer under the write lock so concurrent
	// GetTopology readers see the new pointer atomically.
	l.mu.Lock()
	l.topology = spec
	l.mu.Unlock()

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

	for deviceName := range l.topology.Nodes {
		// All device names must have profiles in profiles/
		profilePath := filepath.Join(l.specDir, "nodes", deviceName+".json")
		if _, err := os.Stat(profilePath); os.IsNotExist(err) {
			v.AddErrorf("topology device '%s' has no profile at %s", deviceName, profilePath)
		}
	}

	// Validate links: both endpoints must reference devices in the topology
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
	deviceName := parts[0]
	if _, ok := l.topology.Nodes[deviceName]; !ok {
		v.AddErrorf("link[%d].%s: device '%s' not found in topology", linkIdx, side, deviceName)
	}
}

// splitEndpoint splits a "device:interface" string into its components.
func splitEndpoint(endpoint string) []string {
	return strings.SplitN(endpoint, ":", 2)
}

// normalizeOverridableSpecs normalizes all map keys and name-reference fields
// in an OverridableSpecs to canonical form (uppercase, hyphens → underscores).
// Called once at spec load time so operations code sees only canonical names.
func normalizeOverridableSpecs(s *OverridableSpecs) {
	s.Services = normalizeMap(s.Services)
	s.Filters = normalizeMap(s.Filters)
	// IPVPN names are canonicalized like every other spec kind. The
	// on-device SONiC VRF name is no longer the IPVPN name itself — it
	// is derived as "Vrf_"+canonical (util.DeriveVRFNameForIPVPN), so the
	// "Vrf" prefix that sonic-vrf.yang requires (RCA-044) is supplied by
	// the derivation, not by the authored name.
	s.IPVPNs = normalizeMap(s.IPVPNs)
	s.MACVPNs = normalizeMap(s.MACVPNs)
	s.QoSPolicies = normalizeMap(s.QoSPolicies)
	s.RoutePolicies = normalizeMap(s.RoutePolicies)
	s.PrefixLists = normalizeMap(s.PrefixLists)

	// Normalize name-reference fields inside specs
	for _, svc := range s.Services {
		NormalizeServiceRefs(svc)
	}
	for _, filter := range s.Filters {
		NormalizeFilterRefs(filter)
	}
	for _, rp := range s.RoutePolicies {
		NormalizeRoutePolicyRefs(rp)
	}
}

// normalizeMap re-keys a map using NormalizeName on each key.
func normalizeMap[V any](m map[string]V) map[string]V {
	if m == nil {
		return nil
	}
	result := make(map[string]V, len(m))
	for k, v := range m {
		result[util.NormalizeName(k)] = v
	}
	return result
}

// normalizeServiceRefs normalizes name-reference fields in a ServiceSpec.
func NormalizeServiceRefs(svc *ServiceSpec) {
	if svc == nil {
		return
	}
	svc.IngressFilter = normalizeRef(svc.IngressFilter)
	svc.EgressFilter = normalizeRef(svc.EgressFilter)
	// svc.IPVPN references an IP-VPN by its (canonical) spec name; the
	// on-device VRF name is derived from it (util.DeriveVRFNameForIPVPN).
	// Validation that the referenced IPVPN exists happens in
	// validateSpecRefs.
	svc.IPVPN = normalizeRef(svc.IPVPN)
	svc.MACVPN = normalizeRef(svc.MACVPN)
	svc.QoSPolicy = normalizeRef(svc.QoSPolicy)
	if svc.Routing != nil {
		svc.Routing.ImportPolicy = normalizeRef(svc.Routing.ImportPolicy)
		svc.Routing.ExportPolicy = normalizeRef(svc.Routing.ExportPolicy)
		svc.Routing.ImportPrefixList = normalizeRef(svc.Routing.ImportPrefixList)
		svc.Routing.ExportPrefixList = normalizeRef(svc.Routing.ExportPrefixList)
		// ImportCommunity/ExportCommunity are values (e.g., "65001:100"), not spec names
	}
}

// normalizeFilterRefs normalizes prefix list references in filter rules.
func NormalizeFilterRefs(filter *FilterSpec) {
	if filter == nil {
		return
	}
	for _, rule := range filter.Rules {
		rule.SrcPrefixList = normalizeRef(rule.SrcPrefixList)
		rule.DstPrefixList = normalizeRef(rule.DstPrefixList)
	}
}

// normalizeRoutePolicyRefs normalizes prefix list references in route policy rules.
func NormalizeRoutePolicyRefs(rp *RoutePolicy) {
	if rp == nil {
		return
	}
	for _, rule := range rp.Rules {
		rule.PrefixList = normalizeRef(rule.PrefixList)
		// rule.Community is a match value (e.g., "65001:100"), not a spec name
	}
}

// normalizeRef normalizes a single name reference (returns "" for empty strings).
func normalizeRef(ref string) string {
	if ref == "" {
		return ""
	}
	return util.NormalizeName(ref)
}
