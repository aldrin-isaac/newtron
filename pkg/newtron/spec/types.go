// Package spec handles loading and validating JSON specification files.
package spec

import (
	"sort"
	"strconv"
)

// ============================================================================
// QoS Policy Definitions
// ============================================================================

// QoSPolicy defines a declarative queue policy.
// Array position = queue index = traffic class.
// Unmapped DSCP values default to queue 0.
type QoSPolicy struct {
	Description string      `json:"description,omitempty" label:"Description" tooltip:"Operator-facing description of this QoS policy"`
	Queues      []*QoSQueue `json:"queues" label:"Queues" tooltip:"Ordered list of queues by traffic class (index 0–7)" item_kind:"QoSQueue"`
}

// QoSQueue defines a single queue within a QoS policy.
//
// The queue's slot ID (0–7) is implicit in the array position within
// QoSPolicy.Queues — it is NOT a field on this struct. The wire shape
// on add/update/remove still carries `queue_id` in the request body
// (see AddQoSQueueRequest in pkg/newtron); the schema metadata endpoint
// synthesizes a `queue_id` form field via SchemaMeta.Identifier rather
// than storing it twice.
type QoSQueue struct {
	Name   string `json:"name" label:"Name" tooltip:"Operator-facing queue name (e.g. \"voice\", \"best-effort\")"`
	Type   string `json:"type" label:"Scheduler Type" tooltip:"Strict-priority queues drain before DWRR queues" enum:"strict,dwrr"`
	Weight int    `json:"weight,omitempty" label:"DWRR Weight" tooltip:"Weight percentage for DWRR-scheduled queues (ignored for strict)" min:"1" max:"100"`
	DSCP   []int  `json:"dscp,omitempty" label:"DSCP Values" tooltip:"DSCP code points (0–63) mapped to this queue"`
	ECN    bool   `json:"ecn,omitempty" label:"Enable ECN/WRED" tooltip:"Mark packets instead of dropping when queue fills"`
}

// OverridableSpecs holds spec maps that participate in hierarchical resolution
// (network → zone → node). Embedded by NetworkSpecFile, ZoneSpec, and NodeSpec.
// Resolution is a union with lower-level-wins: node > zone > network.
// The `kind:"…"` tag binds each map to its spec-kind name — the same vocabulary
// as the `ref:"…"` tags and SchemaRegistration.Kind. It is the single
// declaration the referential-integrity framework (references.go) reflects over
// to enumerate specs and resolve references; adding a spec kind is one map with
// one kind tag, and forward+reverse dependency checking covers it automatically.
type OverridableSpecs struct {
	PrefixLists   map[string][]string     `json:"prefix_lists,omitempty" kind:"PrefixListSpec"`
	Filters       map[string]*FilterSpec  `json:"filters,omitempty" kind:"FilterSpec"`
	QoSPolicies   map[string]*QoSPolicy   `json:"qos_policies,omitempty" kind:"QoSPolicy"`
	RoutePolicies map[string]*RoutePolicy `json:"route_policies,omitempty" kind:"RoutePolicy"`
	IPVPNs        map[string]*IPVPNSpec   `json:"ipvpns,omitempty" kind:"IPVPNSpec"`
	MACVPNs       map[string]*MACVPNSpec  `json:"macvpns,omitempty" kind:"MACVPNSpec"`
	Services      map[string]*ServiceSpec `json:"services,omitempty" kind:"ServiceSpec"`
}

// NetworkSpecFile represents the global network specification file (network.json).
type NetworkSpecFile struct {
	Version string `json:"version"`
	// Description is operator-facing documentation for the whole network —
	// what topology or scenario this spec set exercises. Optional; omitted
	// from the wire when empty. Modeled here so it round-trips through load
	// and SaveNetwork rather than being silently dropped as an unknown field.
	Description string              `json:"description,omitempty" label:"Description" tooltip:"Operator-facing description of this network"`
	SuperUsers  []string            `json:"super_users"`
	UserGroups  map[string][]string `json:"user_groups"` // Group name → user list
	// Permissions maps each action (e.g. "device.write") to its grants.
	// Each grant scopes its allowed groups by a where clause
	// (auth-design.md L5). The legacy ["group1", "group2"] shorthand is
	// accepted on the wire and produces one PermissionGrant with an
	// empty Where (matches every Context).
	Permissions map[string]PermissionGrants `json:"permissions"`

	OverridableSpecs // Embedded — all 7 overridable spec maps

	// Zones are NOT stored here — each lives in its own zones/<name>.json,
	// loaded and owned by spec.Loader (mirroring nodes/<name>.json). Access
	// them through the Loader (Zone/Zones/CreateZoneSpec/…), never a field on
	// this type. Kept out of network.json so a ?scope=zone write localizes to
	// its zone file instead of churning the whole network.json (DPN §7/§28).
}

// ZoneSpec defines zone settings (AS number, defaults).
type ZoneSpec struct {
	// Embedded — zone-level overrides. `schema:"-"`: overrides are authored via
	// the flat create-<kind>?scope=zone API, not by editing these maps, so they
	// are storage, not authoring-schema fields. Still serialized to JSON.
	OverridableSpecs `schema:"-"`
}

// ============================================================================
// VPN Definitions
// ============================================================================

// IPVPNSpec defines IP-VPN parameters for L3 routing (EVPN Type-5 routes).
// Referenced by services via the "ipvpn" field.
//
// The IPVPN's own name (its map key in the IPVPNs map) IS the SONiC
// VRF name used on-device — one concept, one name (§13 / §32). Names
// must match SONiC's VRF pattern: start with "Vrf" (case-sensitive,
// per sonic-vrf.yang). RCA-044 documents the silent intfmgrd drop
// that happens otherwise. Validation runs at spec load time; see
// validateIPVPNName in loader.go.
//
// For vrf_type "shared": VRF name = the IPVPN name itself.
// For vrf_type "interface": VRF name = derived from service + interface
// (separate path; this is the only mode where no explicit IPVPN is named).
type IPVPNSpec struct {
	Description  string   `json:"description,omitempty" label:"Description" tooltip:"Operator-facing description of this IP-VPN"`
	L3VNI        int      `json:"l3vni" label:"L3VNI" tooltip:"VXLAN Network Identifier for the L3 EVPN overlay" min:"1" max:"16777215"`
	L3VNIVlan    int      `json:"l3vni_vlan,omitempty" label:"L3VNI Transit VLAN" tooltip:"Dedicated transit VLAN ID for L3VNI decap (no ports, no IP)" min:"1" max:"4094"`
	RouteTargets []string `json:"route_targets" label:"Route Targets" tooltip:"BGP extended-community route targets controlling import/export"`
}

// MACVPNSpec defines MAC-VPN parameters for L2 bridging (EVPN Type-2 routes).
// Referenced by services via the "macvpn" field.
//
// VlanID is the local bridge domain ID, identical on all devices where
// this MAC-VPN is instantiated (opinionated choice for simplicity).
// AnycastIP is the shared gateway IP configured on all leafs (EVPN
// symmetric IRB anycast gateway). Omit for pure L2 (no routing).
type MACVPNSpec struct {
	Description    string   `json:"description,omitempty" label:"Description" tooltip:"Operator-facing description of this MAC-VPN"`
	VlanID         int      `json:"vlan_id" label:"VLAN ID" tooltip:"Local bridge-domain VLAN ID, identical on every leaf in this MAC-VPN" min:"1" max:"4094"`
	VNI            int      `json:"vni" label:"L2VNI" tooltip:"VXLAN Network Identifier for the L2 EVPN overlay" min:"1" max:"16777215"`
	AnycastIP      string   `json:"anycast_ip,omitempty" label:"Anycast Gateway IP" tooltip:"Shared anycast gateway IP for symmetric IRB; omit for pure L2" format:"cidr"`
	AnycastMAC     string   `json:"anycast_mac,omitempty" label:"Anycast Gateway MAC" tooltip:"Shared anycast gateway MAC for symmetric IRB" format:"mac"`
	RouteTargets   []string `json:"route_targets,omitempty" label:"Route Targets" tooltip:"BGP extended-community route targets controlling import/export"`
	ARPSuppression bool     `json:"arp_suppression,omitempty" label:"Enable ARP Suppression" tooltip:"Suppress ARP flooding by answering from the EVPN MAC/IP table"`
}

// ============================================================================
// Service Definition
// ============================================================================

// ServiceSpec defines an interface service type.
//
// Services are the composition layer — they bind overlay references (ipvpn,
// macvpn) with routing, filters, QoS, and permissions into a reusable
// template that can be applied to interfaces.
//
// Service Types (overlay-backed — all config from specs):
//   - "evpn-irb":     EVPN IRB with anycast gateway (requires ipvpn + macvpn)
//   - "evpn-bridged": EVPN L2 stretch, no routing (requires macvpn)
//   - "evpn-routed":  EVPN L3VPN, no VLAN (requires ipvpn)
//
// Service Types (local — params at apply time):
//   - "irb":     Local VLAN + SVI gateway (--vlan and --ip at apply time)
//   - "bridged": Local VLAN, L2 only (--vlan at apply time)
//   - "routed":  Direct L3 interface (--ip at apply time)
//
// VRF Instantiation (vrf_type, overlay types only):
//   - "interface": Creates per-interface VRF named {service}-{interface}
//   - "shared":    Uses VRF named in IPVPNSpec.VRF
//   - (omitted):   No VRF, uses global routing table
type ServiceSpec struct {
	Description string `json:"description" label:"Description" tooltip:"Operator-facing description of this service"`
	ServiceType string `json:"service_type" label:"Service Type" tooltip:"How the service is delivered: EVPN overlay (evpn-*) or local-only (irb/bridged/routed)" enum:"evpn-irb,evpn-bridged,evpn-routed,irb,bridged,routed"`

	// VPN references (names from ipvpn/macvpn sections)
	IPVPN   string `json:"ipvpn,omitempty" label:"IP-VPN" tooltip:"Reference to an ipvpn definition (required for evpn-irb / evpn-routed)" ref:"IPVPNSpec"`
	MACVPN  string `json:"macvpn,omitempty" label:"MAC-VPN" tooltip:"Reference to a macvpn definition (required for evpn-irb / evpn-bridged)" ref:"MACVPNSpec"`
	VRFType string `json:"vrf_type,omitempty" label:"VRF Type" tooltip:"How the per-service VRF is instantiated on-device" enum:"interface,shared"`

	// Routing protocol specification
	Routing *RoutingSpec `json:"routing,omitempty" label:"Routing" tooltip:"BGP / static-routing protocol parameters"`

	// Filters (references to filters)
	IngressFilter string `json:"ingress_filter,omitempty" label:"Ingress Filter" tooltip:"Reference to a filter applied to ingress traffic on this service" ref:"FilterSpec"`
	EgressFilter  string `json:"egress_filter,omitempty" label:"Egress Filter" tooltip:"Reference to a filter applied to egress traffic on this service" ref:"FilterSpec"`

	// QoS
	QoSPolicy string `json:"qos_policy,omitempty" label:"QoS Policy" tooltip:"Reference to a QoS policy bound to interfaces using this service" ref:"QoSPolicy"`
}

// RoutingSpec defines routing protocol specification for a service.
//
// For BGP services:
//   - Local AS is always from node spec (ResolvedNodeSpec.UnderlayASN)
//   - Peer AS can be fixed (number), or "request" (provided at apply time)
//   - Peer IP is derived from interface IP for point-to-point links
type RoutingSpec struct {
	Protocol string `json:"protocol" label:"Protocol" tooltip:"Routing protocol to use on this service" enum:"bgp,static"`

	// BGP-specific
	PeerAS       string `json:"peer_as,omitempty" label:"Peer AS" tooltip:"Remote BGP AS number, or the literal \"request\" to require it at apply time" pattern:"^(\\d+|request)$"`
	ImportPolicy string `json:"import_policy,omitempty" label:"Import Policy" tooltip:"Reference to a route-policy applied to inbound BGP updates" ref:"RoutePolicy"`
	ExportPolicy string `json:"export_policy,omitempty" label:"Export Policy" tooltip:"Reference to a route-policy applied to outbound BGP updates" ref:"RoutePolicy"`

	// Additional BGP filtering (compose as AND conditions with policies)
	ImportCommunity  string `json:"import_community,omitempty" label:"Import Community Match" tooltip:"Additional community match (composes as AND) on the import route-map"`
	ExportCommunity  string `json:"export_community,omitempty" label:"Export Community" tooltip:"Community attached as a set-action on the export route-map"`
	ImportPrefixList string `json:"import_prefix_list,omitempty" label:"Import Prefix List" tooltip:"Additional prefix-list match (composes as AND) on the import route-map" ref:"PrefixListSpec"`
	ExportPrefixList string `json:"export_prefix_list,omitempty" label:"Export Prefix List" tooltip:"Additional prefix-list match (composes as AND) on the export route-map" ref:"PrefixListSpec"`
	Redistribute     *bool  `json:"redistribute,omitempty" label:"Redistribute Connected/Static" tooltip:"Override the service-type default redistribution behavior"`
}

// ============================================================================
// Filter Definitions
// ============================================================================

// FilterSpec defines a reusable set of ACL rules.
// Referenced by services via ingress_filter/egress_filter fields.
type FilterSpec struct {
	Description string        `json:"description" label:"Description" tooltip:"Operator-facing description of this filter"`
	Type        string        `json:"type" label:"Address Family" tooltip:"Address family the filter rules match" enum:"ipv4,ipv6"`
	Rules       []*FilterRule `json:"rules" label:"Rules" tooltip:"Ordered list of rules evaluated by sequence number" item_kind:"FilterRule"`
}

// FilterRule defines a single rule within a FilterSpec.
type FilterRule struct {
	Sequence      int    `json:"seq" label:"Sequence" tooltip:"Evaluation order — lower numbers evaluated first" min:"1" max:"65535"`
	SrcPrefixList string `json:"src_prefix_list,omitempty" label:"Source Prefix List" tooltip:"Reference to a prefix-list for the source-IP match (mutually exclusive with src_ip)" ref:"PrefixListSpec"`
	DstPrefixList string `json:"dst_prefix_list,omitempty" label:"Destination Prefix List" tooltip:"Reference to a prefix-list for the destination-IP match (mutually exclusive with dst_ip)" ref:"PrefixListSpec"`
	SrcIP         string `json:"src_ip,omitempty" label:"Source IP/CIDR" tooltip:"Inline source IP or CIDR (mutually exclusive with src_prefix_list)" format:"cidr"`
	DstIP         string `json:"dst_ip,omitempty" label:"Destination IP/CIDR" tooltip:"Inline destination IP or CIDR (mutually exclusive with dst_prefix_list)" format:"cidr"`
	Protocol      string `json:"protocol,omitempty" label:"Protocol" tooltip:"IP protocol — name (tcp/udp/icmp) or IANA number"`
	SrcPort       string `json:"src_port,omitempty" label:"Source Port" tooltip:"Source TCP/UDP port or range (e.g. \"1024-65535\")"`
	DstPort       string `json:"dst_port,omitempty" label:"Destination Port" tooltip:"Destination TCP/UDP port or range"`
	DSCP          string `json:"dscp,omitempty" label:"DSCP Match" tooltip:"DSCP code-point match (name or number)"`
	Action        string `json:"action" label:"Action" tooltip:"Permit or deny matched traffic" enum:"permit,deny"`
	CoS           string `json:"cos,omitempty" label:"CoS" tooltip:"Class-of-service value to set on matched traffic"`
}

// ============================================================================
// Route Policy Definitions (BGP)
// ============================================================================

// RoutePolicy defines a BGP route policy for import/export filtering.
// Referenced by service routing via import_policy/export_policy.
type RoutePolicy struct {
	Description string             `json:"description,omitempty" label:"Description" tooltip:"Operator-facing description of this route policy"`
	Rules       []*RoutePolicyRule `json:"rules" label:"Rules" tooltip:"Ordered list of rules evaluated by sequence number" item_kind:"RoutePolicyRule"`
}

// RoutePolicyRule defines a single rule within a RoutePolicy.
type RoutePolicyRule struct {
	Sequence int    `json:"seq" label:"Sequence" tooltip:"Evaluation order — lower numbers evaluated first" min:"1" max:"65535"`
	Action   string `json:"action" label:"Action" tooltip:"Permit matched routes (continue with set-actions) or deny" enum:"permit,deny"`

	// Match conditions (all conditions must match)
	PrefixList string `json:"prefix_list,omitempty" label:"Match Prefix List" tooltip:"Reference to a prefix-list to match the route's NLRI" ref:"PrefixListSpec"`
	Community  string `json:"community,omitempty" label:"Match Community" tooltip:"Community string the route must carry"`

	// Set actions (for permit rules)
	Set *RoutePolicySet `json:"set,omitempty" label:"Set Actions" tooltip:"Attributes applied to permitted routes"`
}

// RoutePolicySet defines attributes to set on matching routes.
type RoutePolicySet struct {
	LocalPref int    `json:"local_pref,omitempty" label:"Local Preference" tooltip:"Set BGP LOCAL_PREF on matched routes"`
	Community string `json:"community,omitempty" label:"Community" tooltip:"Append a community attribute to matched routes"`
	MED       int    `json:"med,omitempty" label:"MED" tooltip:"Set BGP Multi-Exit Discriminator on matched routes"`
}

// ============================================================================
// Platform Specification
// ============================================================================

// PlatformSpecFile represents the hardware platform specification file (platforms.json).
// This defines what types of switches are supported (HWSKU, ports, speeds).
type PlatformSpecFile struct {
	Version   string                   `json:"version"`
	Platforms map[string]*PlatformSpec `json:"platforms"`
}

// PlatformSpec defines a SONiC platform or host device type.
//
// Platform support is deeply tied to the backend (HWSKU mapping, port
// stride, SAI compatibility). Platforms are exposed by the schema
// metadata endpoint as read-only — operators can view them but not
// author them via a universal UI; adding a platform requires backend
// coordination.
type PlatformSpec struct {
	// Name is the platform's identity. Authoritative on disk: the file
	// at <--platforms-base>/<Name>.json must have its Name field equal
	// to that basename (loader enforces). On the wire (GET /platforms),
	// Name doubles as the map key — both views agree by construction.
	//
	// For SONiC platforms with multiple deployment variants of the same
	// HWSKU (e.g. Force10-S6000 emulated by the community virtual
	// switch vs the VPP-flavored variant), the convention is
	// <HWSKU>_<variant> so each variant gets its own file without
	// collision. Single-variant SONiC platforms use the bare HWSKU
	// (cisco-p200-32x100-vs.json). Non-SONiC platforms (host VMs,
	// vJunos) use a descriptive name; HWSKU is empty for those.
	Name         string   `json:"name" label:"Name" tooltip:"Platform identifier — filename basename under platforms/ directory; HWSKU for single-variant SONiC, HWSKU_<variant> for multi-variant"`
	HWSKU        string   `json:"hwsku,omitempty" label:"HWSKU" tooltip:"SONiC hardware SKU identifier"`
	Description  string   `json:"description,omitempty" label:"Description" tooltip:"Operator-facing description"`
	DeviceType   string   `json:"device_type,omitempty" label:"Device Type" tooltip:"Network switch or virtual host" enum:"switch,host"`
	PortCount    int      `json:"port_count" label:"Port Count" tooltip:"Number of data ports on this platform"`
	DefaultSpeed string   `json:"default_speed" label:"Default Port Speed" tooltip:"Default speed for each data port (e.g. \"100G\")"`
	Breakouts    []string `json:"breakouts,omitempty" label:"Supported Breakouts" tooltip:"Breakout modes this platform can accept"`

	// Ports is the explicit per-port inventory — the device-native interface
	// name → QEMU NIC slot mapping for every front-panel port. Generated at
	// onboarding from the platform's port authority (SONiC port_config.ini or
	// platform.json; see platform_from_*.go). newtlab resolves every topology
	// interface against it (ResolveNICIndex); see
	// docs/newtron/platform-port-model.md.
	Ports []PortSpec `json:"ports,omitempty" label:"Ports" tooltip:"Per-port inventory: device-native name → QEMU NIC slot, generated from the platform's port authority"`

	// newtlab VM fields
	VMImage             string         `json:"vm_image,omitempty" label:"VM Image" tooltip:"Path or URL to the platform's VM disk image"`
	VMMemory            int            `json:"vm_memory,omitempty" label:"VM Memory (MiB)" tooltip:"Default VM memory size"`
	VMCPUs              int            `json:"vm_cpus,omitempty" label:"VM vCPUs" tooltip:"Default VM vCPU count"`
	VMNICDriver         string         `json:"vm_nic_driver,omitempty" label:"VM NIC Driver" tooltip:"QEMU NIC driver (e.g. \"virtio-net-pci\")"`
	VMCPUFeatures       string         `json:"vm_cpu_features,omitempty" label:"VM CPU Features" tooltip:"QEMU CPU feature flags"`
	VMCredentials       *VMCredentials `json:"vm_credentials,omitempty" label:"VM Credentials" tooltip:"Default SSH credentials baked into the VM image"`
	VMBootTimeout       int            `json:"vm_boot_timeout,omitempty" label:"VM Boot Timeout (s)" tooltip:"Seconds to wait for VM to reach SSH"`
	Dataplane           string         `json:"dataplane,omitempty" label:"Dataplane" tooltip:"Forwarding plane the platform uses" enum:"vpp,barefoot"`
	VMImageRelease      string         `json:"vm_image_release,omitempty" label:"VM Image Release" tooltip:"Release tag selecting release-specific boot patches (e.g. \"202405\")"`
	VMSkipBootstrap     bool           `json:"vm_skip_bootstrap,omitempty" label:"Skip Bootstrap" tooltip:"Image is pre-bootstrapped — skip console-driven network bring-up"`
	UnsupportedFeatures []string       `json:"unsupported_features,omitempty" label:"Unsupported Features" tooltip:"Features this platform cannot handle (e.g. \"acl\", \"evpn-vxlan\")"`
}

// PortSpec is one front-panel port in a platform's generated port model — the
// device-native interface name paired with the QEMU NIC slot that backs it.
// The ordered set of PortSpecs is the explicit name → NIC mapping newtlab
// resolves topology interfaces against — the only form that covers non-strided
// naming (e.g. vJunos "ge-0/0/0") as well as Ethernet stride layouts. Generated,
// not hand-authored, for SONiC platforms (see platform_from_*.go); the design
// is in docs/newtron/platform-port-model.md.
type PortSpec struct {
	Name     string `json:"name" label:"Port Name" tooltip:"Device-native interface name (e.g. \"Ethernet0\", \"ge-0/0/0\")"`
	NICIndex int    `json:"nic_index" label:"NIC Index" tooltip:"QEMU data-NIC slot backing this port (1-based; NIC 0 is management)" min:"1"`
	Speed    string `json:"speed,omitempty" label:"Speed" tooltip:"Port speed (e.g. \"40G\"); defaults to the platform default_speed when omitted"`
	Lanes    []int  `json:"lanes,omitempty" label:"Lanes" tooltip:"SerDes lanes backing this port, when known (informational)"`
}

// PrefixListEntry describes the form shape of one entry inside a prefix
// list. The on-disk representation of PrefixLists is `map[string][]string`
// — entries are bare CIDR strings — but the schema metadata endpoint
// exposes a single-field struct so universal UIs can render the entry's
// authoring form consistently with every other sub-rule kind.
//
// The wire shape on add/remove is `{prefix_list, prefix}`; the parent
// reference (`prefix_list`) is carried via SchemaMeta.ParentRef, not
// here. Per §47 the prefix IS the entry's identity — there is no update
// verb because there are no other mutable fields.
type PrefixListEntry struct {
	Prefix string `json:"prefix" label:"Prefix" tooltip:"CIDR prefix to add to the list (e.g. \"10.0.0.0/8\")" format:"cidr"`
}

// PrefixListSpec describes the form shape of a top-level prefix-list
// kind. On disk the prefix-list is `map[string][]string` (name → CIDRs);
// there is no storage struct because the wire shape on create-prefix-
// list / update-prefix-list is the inline `{name, prefixes}` body. The
// schema metadata endpoint exposes this form shape so universal UIs
// render the top-level prefix-list authoring form consistently with
// every other top-level kind. Parallels PrefixListEntry's role for the
// sub-rule kind. The struct is form-only — it is never marshaled to or
// from network.json directly (the loader handles the inline map).
type PrefixListSpec struct {
	Prefixes []string `json:"prefixes" label:"Prefixes" tooltip:"Ordered list of CIDR prefixes the list contains" item_type:"string"`
}

// IsHost returns true if the platform is a host device (not a network switch).
func (p *PlatformSpec) IsHost() bool {
	return p.DeviceType == "host"
}

// featureDependencies maps each feature to its required dependencies.
// A feature is only supported if all its dependencies are also supported.
// This creates a dependency graph where base features (like evpn-vxlan) are
// listed once, and dependent features (like macvpn) automatically inherit
// the unsupported status if their dependencies are unsupported.
var featureDependencies = map[string][]string{
	// MAC-VPN requires VXLAN dataplane support
	"macvpn": {"evpn-vxlan"},

	// IP-VPN (L3 EVPN) requires VXLAN dataplane support
	"ipvpn": {"evpn-vxlan"},

	// Base features with no dependencies
	"evpn-vxlan": {},
	"acl":        {},
}

// GetAllFeatures returns all known features from the dependency map.
// Used by platform discovery commands to avoid hardcoding the feature list.
func GetAllFeatures() []string {
	features := make([]string, 0, len(featureDependencies))
	for feat := range featureDependencies {
		features = append(features, feat)
	}
	sort.Strings(features)
	return features
}

// SupportsFeature returns true if the platform supports the named feature
// and all of its dependencies. Features are checked recursively through the
// dependency graph defined in featureDependencies.
func (p *PlatformSpec) SupportsFeature(feature string) bool {
	// Check if feature is directly unsupported
	if p.isUnsupported(feature) {
		return false
	}

	// Check if any dependencies are unsupported (recursive)
	for _, dep := range featureDependencies[feature] {
		if !p.SupportsFeature(dep) {
			return false
		}
	}

	return true
}

// isUnsupported returns true if the feature is directly listed in
// UnsupportedFeatures (does not check dependencies).
func (p *PlatformSpec) isUnsupported(feature string) bool {
	for _, f := range p.UnsupportedFeatures {
		if f == feature {
			return true
		}
	}
	return false
}

// GetUnsupportedDueTo returns all features that are unsupported due to the
// given base feature being unsupported. This is useful for documentation and
// error messages to show the cascade effect of unsupporting a base feature.
func GetUnsupportedDueTo(baseFeature string) []string {
	var affected []string
	for feat, deps := range featureDependencies {
		for _, dep := range deps {
			if dep == baseFeature {
				affected = append(affected, feat)
			}
		}
	}
	return affected
}

// GetFeatureDependencies returns the list of features that the given feature
// depends on. Returns nil if the feature has no dependencies or is not defined.
func GetFeatureDependencies(feature string) []string {
	return featureDependencies[feature]
}

// VMCredentials holds default SSH credentials for a VM platform.
type VMCredentials struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

// ============================================================================
// Node Spec
// ============================================================================

// EVPNConfig defines EVPN overlay peering for a node spec.
type EVPNConfig struct {
	Peers          []string `json:"peers,omitempty" label:"EVPN Peers" tooltip:"Loopback IPs of remote EVPN BGP peers (overlay sessions)"`
	RouteReflector bool     `json:"route_reflector,omitempty" label:"Route Reflector" tooltip:"This device acts as an EVPN route reflector for its peers"`
	ClusterID      string   `json:"cluster_id,omitempty" label:"Cluster ID" tooltip:"BGP route-reflector cluster ID (defaults to loopback IP)"`
}

// NodeSpec contains per-node specification.
// This is the minimal set of node-specific data; everything else
// is inherited from region/global or derived at runtime.
type NodeSpec struct {
	// REQUIRED - must be specified
	MgmtIP     string `json:"mgmt_ip" label:"Management IP" tooltip:"Out-of-band management IP reachable from newtron" format:"cidr"`
	LoopbackIP string `json:"loopback_ip,omitempty" label:"Loopback IP" tooltip:"Loopback IP — the device's BGP router-id and VTEP source (required for switch nodes)" format:"cidr"`
	Zone       string `json:"zone,omitempty" label:"Zone" tooltip:"Zone name (must exist in network.json zones; required for switch nodes)" ref:"ZoneSpec"`

	// OPTIONAL - EVPN overlay peering
	EVPN *EVPNConfig `json:"evpn,omitempty" label:"EVPN Overlay" tooltip:"EVPN BGP overlay peering — set on leafs and route reflectors"`

	// OPTIONAL - device-specific
	MAC      string `json:"mac,omitempty" label:"Base MAC" tooltip:"Override the device base MAC (otherwise derived)" format:"mac"`
	Platform string `json:"platform,omitempty" label:"Platform" tooltip:"Reference to a platforms.json entry; determines HWSKU, ports, and VM image" ref:"PlatformSpec"`

	// Embedded — node-level overrides. `schema:"-"`: overrides are authored via
	// the flat create-<kind>?scope=node API, not by editing these maps, so they
	// are storage, not authoring-schema fields. Still serialized to JSON.
	OverridableSpecs `schema:"-"`

	// OPTIONAL - SSH credentials for Redis tunnel. SSH port is runtime
	// state owned by newtlab (§27) — not stored here; resolved through
	// newtron's PortResolver at Connect time.
	SSHUser string `json:"ssh_user,omitempty" label:"SSH User" tooltip:"Username for the SSH tunnel to Redis"`
	SSHPass string `json:"ssh_pass,omitempty" label:"SSH Password" tooltip:"Password or ${secret:KEY} reference for the SSH tunnel"`

	// OPTIONAL - newtlab per-device overrides
	VMMemory int    `json:"vm_memory,omitempty" label:"VM Memory (MiB)" tooltip:"Per-device override for VM memory size"`
	VMCPUs   int    `json:"vm_cpus,omitempty" label:"VM vCPUs" tooltip:"Per-device override for VM vCPU count"`
	VMImage  string `json:"vm_image,omitempty" label:"VM Image Path" tooltip:"Per-device override for VM disk image"`
	VMHost   string `json:"vm_host,omitempty" label:"VM Host" tooltip:"Hostname or IP of the physical host running this VM"`

	// OPTIONAL - eBGP underlay ASN (unique per device)
	UnderlayASN int `json:"underlay_asn,omitempty" label:"Underlay ASN" tooltip:"eBGP underlay AS number (must be unique per device)" min:"1" max:"4294967295" format:"asn"`

	// OPTIONAL - virtual host IP assignment (newtlab auto-derives if omitted)
	HostIP      string `json:"host_ip,omitempty" label:"Host Data-plane IP" tooltip:"Virtual host data-plane IP; newtlab auto-derives if omitted" format:"cidr"`
	HostGateway string `json:"host_gateway,omitempty" label:"Host Default Gateway" tooltip:"Default gateway for the virtual host"`
}

// ResolvedNodeSpec contains fully resolved device values
// after applying inheritance (nodeSpec > region > global) and derivation.
type ResolvedNodeSpec struct {
	// From nodeSpec
	DeviceName string
	MgmtIP     string
	LoopbackIP string
	Zone       string
	Platform   string

	// Resolved from inheritance
	IsRouteReflector bool
	ClusterID        string // RR cluster ID; from nodeSpec EVPN config or defaults to loopback IP

	// Derived at runtime
	RouterID        string         // = LoopbackIP
	VTEPSourceIP    string         // = LoopbackIP
	BGPNeighbors    []string       // From nodeSpec EVPN peers → lookup loopback IPs
	BGPNeighborASNs map[string]int // peer loopback IP → peer UnderlayASN (for eBGP overlay)

	// From nodeSpec (optional)
	MAC string

	// SSH credentials for Redis tunnel. SSH port is runtime state
	// owned by newtlab (§27) — resolved through newtron's PortResolver
	// at Connect time; not part of ResolvedNodeSpec.
	SSHUser string
	SSHPass string

	// BGP AS number (required in all-eBGP design)
	UnderlayASN int
}

// ============================================================================
// Constants
// ============================================================================

// ServiceType constants
const (
	ServiceTypeEVPNIRB     = "evpn-irb"     // L2+L3 overlay: requires ipvpn + macvpn
	ServiceTypeEVPNBridged = "evpn-bridged" // L2 overlay: requires macvpn
	ServiceTypeEVPNRouted  = "evpn-routed"  // L3 overlay: requires ipvpn
	ServiceTypeIRB         = "irb"          // Local L2+L3: vlan + ip at apply time
	ServiceTypeBridged     = "bridged"      // Local L2: vlan at apply time
	ServiceTypeRouted      = "routed"       // Local L3: ip at apply time
)

// VRFType constants
const (
	VRFTypeInterface = "interface" // Per-interface VRF: {service}-{interface}
	VRFTypeShared    = "shared"    // Shared VRF: name from ipvpn.vrf field
)

// RoutingProtocol constants
const (
	RoutingProtocolBGP = "bgp"
)

// PeerAS special values
const (
	PeerASRequest = "request" // Must be provided at apply time
)

// ============================================================================
// Topology Specification (v4)
// ============================================================================

// TopologySpecFile represents the topology specification file (topology.json).
// Defines devices, interconnections, and interface service bindings for
// automated provisioning.
type TopologySpecFile struct {
	Version     string                   `json:"version"`
	Platform    string                   `json:"platform,omitempty"` // default platform for all nodes
	Description string                   `json:"description,omitempty"`
	Nodes       map[string]*TopologyNode `json:"nodes"`
	Links       []*TopologyLink          `json:"links,omitempty"`
	NewtLab     *NewtLabConfig           `json:"newtlab,omitempty"`
}

// ServerConfig defines a server in the newtlab server pool.
type ServerConfig struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	MaxNodes int    `json:"max_nodes,omitempty"` // 0 = unlimited
}

// NewtLabConfig holds newtlab orchestration settings from topology.json.
// Servers is the canonical server-pool declaration; runtime code
// derives a name→address lookup map from it. There is no separate
// legacy field — §40 (Greenfield) forbids carrying parallel input
// shapes once the canonical one exists.
type NewtLabConfig struct {
	LinkPortBase    int             `json:"link_port_base,omitempty"`
	ConsolePortBase int             `json:"console_port_base,omitempty"`
	SSHPortBase     int             `json:"ssh_port_base,omitempty"`
	Servers         []*ServerConfig `json:"servers,omitempty"` // server pool for auto-placement
}

// TopologyNode defines a device's configuration within a topology.
// Switch devices have Steps (provisioning intent) and Ports (physical port config).
// Host devices are empty entries — detection is via platform nodeSpec, not a type field.
type TopologyNode struct {
	Steps []TopologyStep         `json:"steps,omitempty"`
	Ports map[string]*PortConfig `json:"ports,omitempty"` // keyed by port name (e.g. "Ethernet0")
}

// PortConfig is the operator-configurable PORT-table config for one physical
// port, authored under a TopologyNode's `ports` map (keyed by port name).
// Its fields mirror the YANG-derived PORT constraints in
// device/sonic/schema.go — the same set the delivery layer validates — so
// authoring-time and delivery-time agree. Registered as a schema kind so a
// universal UI (newtcon) renders the form; the operator picks the port from
// the platform's `ports` inventory, configures it here, and the entry is
// written to the device's CONFIG_DB PORT table on provisioning (RegisterPort).
type PortConfig struct {
	AdminStatus string `json:"admin_status,omitempty" label:"Admin Status" tooltip:"Whether the port is administratively enabled" enum:"up,down"`
	MTU         int    `json:"mtu,omitempty" label:"MTU" tooltip:"Maximum transmission unit in bytes" min:"68" max:"9216"`
	Speed       string `json:"speed,omitempty" label:"Speed" tooltip:"Port speed; must be one the platform supports" enum:"1G,10G,25G,40G,50G,100G,200G,400G"`
	Description string `json:"description,omitempty" label:"Description" tooltip:"Operator-facing port description"`
}

// Fields renders the typed config as CONFIG_DB PORT-table string fields,
// omitting unset values. Normalize-at-the-boundary: the typed spec becomes the
// string hash SONiC stores (mtu 9100 → "9100"). Returns an empty (non-nil) map
// when nothing is set.
func (p *PortConfig) Fields() map[string]string {
	f := map[string]string{}
	if p == nil {
		return f
	}
	if p.AdminStatus != "" {
		f["admin_status"] = p.AdminStatus
	}
	if p.MTU != 0 {
		f["mtu"] = strconv.Itoa(p.MTU)
	}
	if p.Speed != "" {
		f["speed"] = p.Speed
	}
	if p.Description != "" {
		f["description"] = p.Description
	}
	return f
}

// TopologyStep is a single provisioning operation in the topology.
// URL identifies the operation (last segment = verb, e.g., "/configure-bgp").
// Interface-scoped operations include the interface name in the URL
// (e.g., "/interfaces/Ethernet0/apply-service").
// Params are structured JSON values matching the API request format.
type TopologyStep struct {
	URL    string         `json:"url"`
	Params map[string]any `json:"params,omitempty"`
}

// TopologyLink defines a point-to-point connection between two interfaces.
// Used for validation (both ends defined) and topology visualization.
type TopologyLink struct {
	A string `json:"a"` // "device:interface"
	Z string `json:"z"` // "device:interface"
}

// HasDevice returns true if the topology contains a device with the given name.
func (t *TopologySpecFile) HasDevice(name string) bool {
	_, ok := t.Nodes[name]
	return ok
}

// DeviceNames returns a sorted list of device names in the topology.
func (t *TopologySpecFile) DeviceNames() []string {
	names := make([]string, 0, len(t.Nodes))
	for name := range t.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
