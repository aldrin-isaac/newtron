// Package spec handles loading and validating JSON specification files.
package spec

import (
	"sort"

	"github.com/newtron-network/newtron/pkg/model"
)

// ============================================================================
// QoS Policy Definitions
// ============================================================================

// QoSPolicy defines a declarative queue policy.
// Array position = queue index = traffic class.
// Unmapped DSCP values default to queue 0.
type QoSPolicy struct {
	Description string      `json:"description,omitempty"`
	Queues      []*QoSQueue `json:"queues"`
}

// QoSQueue defines a single queue within a QoS policy.
type QoSQueue struct {
	Name   string `json:"name"`
	Type   string `json:"type"`             // "dwrr" or "strict"
	Weight int    `json:"weight,omitempty"` // DWRR weight (percentage)
	DSCP   []int  `json:"dscp,omitempty"`   // DSCP values mapped to this queue
	ECN    bool   `json:"ecn,omitempty"`    // Enable ECN/WRED
}

// NetworkSpecFile represents the global network specification file (network.json).
type NetworkSpecFile struct {
	Version      string                       `json:"version"`
	SuperUsers   []string                     `json:"super_users"`
	UserGroups   map[string][]string          `json:"user_groups"`   // Group name → user list
	Permissions  map[string][]string          `json:"permissions"`   // Action → allowed groups
	GenericAlias map[string]string            `json:"generic_alias"` // Global aliases
	Regions      map[string]*RegionSpec       `json:"regions"`
	PrefixLists  map[string][]string          `json:"prefix_lists"`
	FilterSpecs  map[string]*FilterSpec       `json:"filter_specs"`
	Policers     map[string]*PolicerSpec       `json:"policers"`
	QoSPolicies  map[string]*QoSPolicy         `json:"qos_policies,omitempty"`
	QoSProfiles  map[string]*model.QoSProfile `json:"qos_profiles,omitempty"` // Legacy — kept for backward compat

	// Route policies (for BGP import/export)
	RoutePolicies map[string]*RoutePolicy `json:"route_policies,omitempty"`

	// VPN definitions (referenced by services)
	IPVPN  map[string]*IPVPNSpec  `json:"ipvpn"`  // IP-VPN (L3VNI, route targets)
	MACVPN map[string]*MACVPNSpec `json:"macvpn"` // MAC-VPN (VLAN, L2VNI)

	// Service definitions (reference ipvpn/macvpn by name)
	Services map[string]*ServiceSpec `json:"services"`
}

// RegionSpec defines regional settings (AS number, defaults).
type RegionSpec struct {
	ASNumber     int                 `json:"as_number"`
	ASName       string              `json:"as_name,omitempty"`   // TODO(v4): not consumed — use as BGP AS name in DEVICE_METADATA or description fields
	PrefixLists  map[string][]string `json:"prefix_lists,omitempty"`
	GenericAlias map[string]string   `json:"generic_alias,omitempty"`
}

// ============================================================================
// VPN Definitions
// ============================================================================

// IPVPNSpec defines IP-VPN parameters for L3 routing (Type-5 routes).
// Referenced by services via the "ipvpn" field.
//
// For vrf_type "shared": VRF name = ipvpn definition name
// For vrf_type "interface": VRF name = {service}-{interface}
type IPVPNSpec struct {
	Description string   `json:"description,omitempty"`
	L3VNI       int      `json:"l3_vni"`
	ImportRT    []string `json:"import_rt"`
	ExportRT    []string `json:"export_rt"`
}

// MACVPNSpec defines MAC-VPN parameters for L2 bridging (Type-2 routes).
// Referenced by services via the "macvpn" field.
//
// Note: VLAN ID is NOT part of MAC-VPN — it's a local bridge domain concept.
// VLAN IDs live in ServiceSpec (for L2/IRB services) or are specified
// when binding a VLAN to a MAC-VPN via `vlan bind-macvpn`.
type MACVPNSpec struct {
	Description    string `json:"description,omitempty"`
	L2VNI          int    `json:"l2_vni"`
	ARPSuppression bool   `json:"arp_suppression,omitempty"`
}

// ============================================================================
// Service Definition
// ============================================================================

// ServiceSpec defines an interface service type.
//
// Services bundle VPN references, routing, filters, QoS, and permissions
// into a reusable template that can be applied to interfaces.
//
// Service Types:
//   - "l3":  L3 routed interface (requires ipvpn, optional vrf_type)
//   - "l2":  L2 bridged interface (requires macvpn)
//   - "irb": Integrated routing and bridging (requires both ipvpn and macvpn)
//
// VRF Instantiation (vrf_type):
//   - "interface": Creates per-interface VRF named {service}-{interface}
//   - "shared":    Uses shared VRF named after the ipvpn definition
//   - (omitted):   No VRF, uses global routing table (for l3 without EVPN)
type ServiceSpec struct {
	Description string `json:"description"`
	ServiceType string `json:"service_type"` // l2, l3, irb

	// VPN references (names from ipvpn/macvpn sections)
	IPVPN   string `json:"ipvpn,omitempty"`    // Reference to ipvpn definition
	MACVPN  string `json:"macvpn,omitempty"`   // Reference to macvpn definition
	VRFType string `json:"vrf_type,omitempty"` // "interface" or "shared"

	// VLAN ID for L2/IRB services (local bridge domain)
	VLAN int `json:"vlan,omitempty"`

	// Routing protocol specification
	Routing *RoutingSpec `json:"routing,omitempty"`

	// Anycast gateway (for IRB services)
	AnycastGateway string `json:"anycast_gateway,omitempty"` // e.g., "10.1.100.1/24"
	AnycastMAC     string `json:"anycast_mac,omitempty"`     // e.g., "00:00:00:01:02:03"

	// Filters (references to filter_specs)
	IngressFilter string `json:"ingress_filter,omitempty"`
	EgressFilter  string `json:"egress_filter,omitempty"`

	// QoS
	QoSPolicy  string `json:"qos_policy,omitempty"`
	QoSProfile string `json:"qos_profile,omitempty"` // Legacy — kept for backward compat

	// Permissions (override global permissions for this service)
	Permissions map[string][]string `json:"permissions,omitempty"`
}

// RoutingSpec defines routing protocol specification for a service.
//
// For BGP services:
//   - Local AS is always from device profile (ResolvedProfile.ASNumber)
//   - Peer AS can be fixed (number), or "request" (provided at apply time)
//   - Peer IP is derived from interface IP for point-to-point links
type RoutingSpec struct {
	Protocol string `json:"protocol"` // "bgp", "static", or empty (none)

	// BGP-specific
	PeerAS       string `json:"peer_as,omitempty"`       // AS number, or "request"
	ImportPolicy string `json:"import_policy,omitempty"` // Route policy name → translated to ROUTE_MAP
	ExportPolicy string `json:"export_policy,omitempty"` // Route policy name → translated to ROUTE_MAP

	// Additional BGP filtering (compose as AND conditions with policies)
	ImportCommunity  string `json:"import_community,omitempty"`   // COMMUNITY_SET match in import route-map
	ExportCommunity  string `json:"export_community,omitempty"`   // COMMUNITY_SET set-action in export route-map
	ImportPrefixList string `json:"import_prefix_list,omitempty"` // PREFIX_SET match in import route-map
	ExportPrefixList string `json:"export_prefix_list,omitempty"` // PREFIX_SET match in export route-map
	Redistribute     *bool  `json:"redistribute,omitempty"`       // Override default redistribution
}

// ============================================================================
// Filter Definitions
// ============================================================================

// FilterSpec defines a reusable set of ACL rules.
// Referenced by services via ingress_filter/egress_filter fields.
type FilterSpec struct {
	Description string        `json:"description"`
	Type        string        `json:"type"` // L3, L3V6
	Rules       []*FilterRule `json:"rules"`
}

// FilterRule defines a single rule within a FilterSpec.
type FilterRule struct {
	Sequence      int    `json:"seq"`
	SrcPrefixList string `json:"src_prefix_list,omitempty"` // Reference to prefix_lists
	DstPrefixList string `json:"dst_prefix_list,omitempty"`
	SrcIP         string `json:"src_ip,omitempty"` // Direct CIDR
	DstIP         string `json:"dst_ip,omitempty"`
	Protocol      string `json:"protocol,omitempty"` // tcp, udp, icmp, or number
	SrcPort       string `json:"src_port,omitempty"` // Port or range "1024-65535"
	DstPort       string `json:"dst_port,omitempty"`
	DSCP          string `json:"dscp,omitempty"`
	Action        string `json:"action"` // permit, deny
	CoS           string `json:"cos,omitempty"`
	Policer       string `json:"policer,omitempty"`
	Log           bool   `json:"log,omitempty"` // TODO(v4): not consumed — implement ACL_RULE log action (requires SONiC logging infrastructure)
}

// PolicerSpec defines a rate limiter.
type PolicerSpec struct {
	Bandwidth string `json:"bandwidth"`        // e.g., "10m", "1g"
	Burst     string `json:"burst"`            // e.g., "1m"
	Action    string `json:"action,omitempty"` // TODO(v4): not consumed — implement policer exceed action (drop vs remark) in POLICER table
}

// ============================================================================
// Route Policy Definitions (BGP)
// ============================================================================

// RoutePolicy defines a BGP route policy for import/export filtering.
// Referenced by service routing via import_policy/export_policy.
//
type RoutePolicy struct {
	Description string             `json:"description,omitempty"`
	Rules       []*RoutePolicyRule `json:"rules"`
}

// RoutePolicyRule defines a single rule within a RoutePolicy.
type RoutePolicyRule struct {
	Sequence int    `json:"seq"`
	Action   string `json:"action"` // permit, deny

	// Match conditions (all conditions must match)
	PrefixList   string `json:"prefix_list,omitempty"`    // Reference to prefix_lists
	ASPathLength string `json:"as_path_length,omitempty"` // e.g., "> 10", "< 5"
	Community    string `json:"community,omitempty"`      // Match community

	// Set actions (for permit rules)
	Set *RoutePolicySet `json:"set,omitempty"`
}

// RoutePolicySet defines attributes to set on matching routes.
type RoutePolicySet struct {
	LocalPref int    `json:"local_pref,omitempty"` // Set LOCAL_PREF
	Community string `json:"community,omitempty"`  // Set/add community
	MED       int    `json:"med,omitempty"`        // Set MED
}

// ============================================================================
// Site Specification
// ============================================================================

// SiteSpecFile represents the site specification file (site.json).
// Sites define topology (which devices are route reflectors).
// Device details (loopback_ip, etc.) come from individual profiles.
type SiteSpecFile struct {
	Version string              `json:"version"`
	Sites   map[string]*SiteSpec `json:"sites"`
}

// SiteSpec contains site-specific topology specification.
type SiteSpec struct {
	Region          string   `json:"region"`                     // Region this site belongs to
	RouteReflectors []string `json:"route_reflectors,omitempty"` // Device names that are RRs
	ClusterID       string   `json:"cluster_id,omitempty"`       // BGP RR cluster ID; used by topology provisioner, falls back to loopback IP
}

// ============================================================================
// Platform Specification
// ============================================================================

// PlatformSpecFile represents the hardware platform specification file (platforms.json).
// This defines what types of switches are supported (HWSKU, ports, speeds).
type PlatformSpecFile struct {
	Version   string                  `json:"version"`
	Platforms map[string]*PlatformSpec `json:"platforms"`
}

// PlatformSpec defines a SONiC platform.
type PlatformSpec struct {
	HWSKU        string   `json:"hwsku"`
	Description  string   `json:"description,omitempty"`
	PortCount    int      `json:"port_count"`
	DefaultSpeed string   `json:"default_speed"`
	Breakouts    []string `json:"breakouts,omitempty"` // Supported breakout modes

	// newtlab VM fields
	VMImage              string         `json:"vm_image,omitempty"`
	VMMemory             int            `json:"vm_memory,omitempty"`
	VMCPUs               int            `json:"vm_cpus,omitempty"`
	VMNICDriver          string         `json:"vm_nic_driver,omitempty"`
	VMInterfaceMap       string         `json:"vm_interface_map,omitempty"`
	VMInterfaceMapCustom map[string]int `json:"vm_interface_map_custom,omitempty"` // SONiC name → QEMU NIC index (for "custom" map type)
	VMCPUFeatures        string         `json:"vm_cpu_features,omitempty"`
	VMCredentials        *VMCredentials `json:"vm_credentials,omitempty"`
	VMBootTimeout        int            `json:"vm_boot_timeout,omitempty"`
	Dataplane            string         `json:"dataplane,omitempty"`        // "vpp", "barefoot", "" (none/vs)
	VMImageRelease       string         `json:"vm_image_release,omitempty"` // e.g. "202405" — selects release-specific boot patches
	UnsupportedFeatures  []string       `json:"unsupported_features,omitempty"` // features this platform cannot handle (e.g. "acl")
}

// SupportsFeature returns true if the platform supports the named feature.
// A feature is unsupported only if explicitly listed in UnsupportedFeatures.
func (p *PlatformSpec) SupportsFeature(feature string) bool {
	for _, f := range p.UnsupportedFeatures {
		if f == feature {
			return false
		}
	}
	return true
}

// VMCredentials holds default SSH credentials for a VM platform.
type VMCredentials struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

// ============================================================================
// Device Profile
// ============================================================================

// DeviceProfile contains per-device specification.
// This is the minimal set of device-specific data; everything else
// is inherited from region/global or derived at runtime.
type DeviceProfile struct {
	// REQUIRED - must be specified
	MgmtIP     string `json:"mgmt_ip"`
	LoopbackIP string `json:"loopback_ip"`
	Site       string `json:"site"` // Site name - region is derived from site.json

	// OPTIONAL OVERRIDES - if set, override region/global values
	ASNumber         *int `json:"as_number,omitempty"`
	IsRouteReflector bool `json:"is_route_reflector,omitempty"`

	// OPTIONAL - device-specific
	MAC             string              `json:"mac,omitempty"`
	Platform        string              `json:"platform,omitempty"`
	VLANPortMapping map[int][]string    `json:"vlan_port_mapping,omitempty"` // TODO(v4): not consumed — implement VLAN-to-port mapping for pre-provisioned access ports
	GenericAlias    map[string]string   `json:"generic_alias,omitempty"`
	PrefixLists     map[string][]string `json:"prefix_lists,omitempty"`

	// OPTIONAL - SSH access for Redis tunnel
	SSHUser string `json:"ssh_user,omitempty"`
	SSHPass string `json:"ssh_pass,omitempty"`
	SSHPort int    `json:"ssh_port,omitempty"` // 0 means default (22)

	// OPTIONAL - newtlab per-device overrides
	ConsolePort int    `json:"console_port,omitempty"`
	VMMemory    int    `json:"vm_memory,omitempty"`
	VMCPUs      int    `json:"vm_cpus,omitempty"`
	VMImage     string `json:"vm_image,omitempty"`
	VMHost      string `json:"vm_host,omitempty"`

	// OPTIONAL - eBGP underlay ASN (unique per device)
	UnderlayASN int `json:"underlay_asn,omitempty"`
}

// ResolvedProfile contains fully resolved device values
// after applying inheritance (profile > region > global) and derivation.
type ResolvedProfile struct {
	// From profile
	DeviceName string
	MgmtIP     string
	LoopbackIP string
	Region     string
	Site       string
	Platform   string

	// Resolved from inheritance
	ASNumber         int
	IsRouteReflector bool

	// Derived at runtime
	RouterID     string   // = LoopbackIP
	VTEPSourceIP string   // = LoopbackIP
	BGPNeighbors []string // From site route_reflectors → lookup loopback IPs

	// From profile (optional)
	MAC string

	// Merged maps (profile > region > global)
	GenericAlias map[string]string
	PrefixLists  map[string][]string

	// SSH access (for Redis tunnel)
	SSHUser string
	SSHPass string
	SSHPort int // 0 means default (22)

	// newtlab runtime (written by newtlab, read by newtron)
	ConsolePort int

	// eBGP underlay ASN (unique per device; 0 means use ASNumber for iBGP-only)
	UnderlayASN int
}

// ============================================================================
// Constants
// ============================================================================

// ServiceType constants
const (
	ServiceTypeL2  = "l2"
	ServiceTypeL3  = "l3"
	ServiceTypeIRB = "irb"
)

// VRFType constants
const (
	VRFTypeInterface = "interface" // Per-interface VRF: {service}-{interface}
	VRFTypeShared    = "shared"    // Shared VRF: name = ipvpn definition name
)

// RoutingProtocol constants
const (
	RoutingProtocolBGP    = "bgp"
	RoutingProtocolStatic = "static"
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
	Version     string                     `json:"version"`
	Description string                     `json:"description,omitempty"`
	Devices     map[string]*TopologyDevice `json:"devices"`
	Links       []*TopologyLink            `json:"links,omitempty"`
	NewtLab     *NewtLabConfig             `json:"newtlab,omitempty"`
}

// ServerConfig defines a server in the newtlab server pool.
type ServerConfig struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	MaxNodes int    `json:"max_nodes,omitempty"` // 0 = unlimited
}

// NewtLabConfig holds newtlab orchestration settings from topology.json.
type NewtLabConfig struct {
	LinkPortBase    int               `json:"link_port_base,omitempty"`
	ConsolePortBase int               `json:"console_port_base,omitempty"`
	SSHPortBase     int               `json:"ssh_port_base,omitempty"`
	Hosts           map[string]string `json:"hosts,omitempty"`   // legacy: kept for backward compat
	Servers         []*ServerConfig   `json:"servers,omitempty"` // server pool for auto-placement
}

// TopologyDevice defines a device's configuration within a topology.
type TopologyDevice struct {
	DeviceConfig *TopologyDeviceConfig          `json:"device_config,omitempty"`
	Interfaces   map[string]*TopologyInterface  `json:"interfaces"`
}

// TopologyDeviceConfig defines device-level settings for topology provisioning.
// These are applied before interface-level services.
type TopologyDeviceConfig struct {
	RouteReflector bool `json:"route_reflector,omitempty"`
}

// TopologyInterface defines an interface's service binding within a topology.
// Provides all parameters that would normally be supplied by the user at CLI.
type TopologyInterface struct {
	Link    string            `json:"link,omitempty"`    // "device:interface" (documentation/validation)
	Service string            `json:"service"`           // service name from network.json
	IP      string            `json:"ip,omitempty"`      // IP address (e.g., "10.1.1.1/30")
	Params  map[string]string `json:"params,omitempty"`  // service-specific params (e.g., peer_as)
}

// TopologyLink defines a point-to-point connection between two interfaces.
// Used for validation (both ends defined) and topology visualization.
type TopologyLink struct {
	A string `json:"a"` // "device:interface"
	Z string `json:"z"` // "device:interface"
}

// HasDevice returns true if the topology contains a device with the given name.
func (t *TopologySpecFile) HasDevice(name string) bool {
	_, ok := t.Devices[name]
	return ok
}

// DeviceNames returns a sorted list of device names in the topology.
func (t *TopologySpecFile) DeviceNames() []string {
	names := make([]string, 0, len(t.Devices))
	for name := range t.Devices {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
