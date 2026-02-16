// Package spec handles loading and validating JSON specification files.
package spec

import (
	"sort"
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

// QoSProfile maps interface types to scheduler configurations (legacy).
type QoSProfile struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	SchedulerMap string `json:"scheduler_map"` // e.g., "8q" or "4q"
	DSCPToTCMap  string `json:"dscp_to_tc_map,omitempty"`
	TCToQueueMap string `json:"tc_to_queue_map,omitempty"`
}

// OverridableSpecs holds spec maps that participate in hierarchical resolution
// (network → zone → node). Embedded by NetworkSpecFile, ZoneSpec, and DeviceProfile.
// Resolution is a union with lower-level-wins: node > zone > network.
type OverridableSpecs struct {
	PrefixLists   map[string][]string      `json:"prefix_lists,omitempty"`
	Filters       map[string]*FilterSpec   `json:"filters,omitempty"`
	QoSPolicies   map[string]*QoSPolicy    `json:"qos_policies,omitempty"`
	QoSProfiles   map[string]*QoSProfile   `json:"qos_profiles,omitempty"`
	RoutePolicies map[string]*RoutePolicy  `json:"route_policies,omitempty"`
	IPVPNs        map[string]*IPVPNSpec    `json:"ipvpns,omitempty"`
	MACVPNs       map[string]*MACVPNSpec   `json:"macvpns,omitempty"`
	Services      map[string]*ServiceSpec  `json:"services,omitempty"`
}

// NetworkSpecFile represents the global network specification file (network.json).
type NetworkSpecFile struct {
	Version     string              `json:"version"`
	SuperUsers  []string            `json:"super_users"`
	UserGroups  map[string][]string `json:"user_groups"`  // Group name → user list
	Permissions map[string][]string `json:"permissions"`  // Action → allowed groups
	Zones       map[string]*ZoneSpec `json:"zones"`

	OverridableSpecs // Embedded — all 8 overridable spec maps
}

// ZoneSpec defines zone settings (AS number, defaults).
type ZoneSpec struct {
	ASNumber int `json:"as_number"`

	OverridableSpecs // Embedded — zone-level overrides
}

// ============================================================================
// VPN Definitions
// ============================================================================

// IPVPNSpec defines IP-VPN parameters for L3 routing (EVPN Type-5 routes).
// Referenced by services via the "ipvpn" field.
//
// VRF is the explicit SONiC VRF name used on-device.
// For vrf_type "shared": VRF name = IPVPNSpec.VRF
// For vrf_type "interface": VRF name = derived from service + interface
type IPVPNSpec struct {
	Description  string   `json:"description,omitempty"`
	VRF          string   `json:"vrf"`
	L3VNI        int      `json:"l3vni"`
	RouteTargets []string `json:"route_targets"`
}

// MACVPNSpec defines MAC-VPN parameters for L2 bridging (EVPN Type-2 routes).
// Referenced by services via the "macvpn" field.
//
// VlanID is the local bridge domain ID, identical on all devices where
// this MAC-VPN is instantiated (opinionated choice for simplicity).
// AnycastIP is the shared gateway IP configured on all leafs (EVPN
// symmetric IRB anycast gateway). Omit for pure L2 (no routing).
type MACVPNSpec struct {
	Description    string   `json:"description,omitempty"`
	VlanID         int      `json:"vlan_id"`
	VNI            int      `json:"vni"`
	AnycastIP      string   `json:"anycast_ip,omitempty"`
	AnycastMAC     string   `json:"anycast_mac,omitempty"`
	RouteTargets   []string `json:"route_targets,omitempty"`
	ARPSuppression bool     `json:"arp_suppression,omitempty"`
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
	Description string `json:"description"`
	ServiceType string `json:"service_type"` // evpn-irb, evpn-bridged, evpn-routed, irb, bridged, routed

	// VPN references (names from ipvpn/macvpn sections)
	IPVPN   string `json:"ipvpn,omitempty"`    // Reference to ipvpn definition
	MACVPN  string `json:"macvpn,omitempty"`   // Reference to macvpn definition
	VRFType string `json:"vrf_type,omitempty"` // "interface" or "shared"

	// Routing protocol specification
	Routing *RoutingSpec `json:"routing,omitempty"`

	// Filters (references to filters)
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
	Type        string        `json:"type"` // ipv4, ipv6 (translated to L3, L3V6 for CONFIG_DB)
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
	Log           bool   `json:"log,omitempty"` // TODO(v4): not consumed — implement ACL_RULE log action (requires SONiC logging infrastructure)
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
// Platform Specification
// ============================================================================

// PlatformSpecFile represents the hardware platform specification file (platforms.json).
// This defines what types of switches are supported (HWSKU, ports, speeds).
type PlatformSpecFile struct {
	Version   string                  `json:"version"`
	Platforms map[string]*PlatformSpec `json:"platforms"`
}

// PlatformSpec defines a SONiC platform or host device type.
type PlatformSpec struct {
	HWSKU        string   `json:"hwsku"`
	Description  string   `json:"description,omitempty"`
	DeviceType   string   `json:"device_type,omitempty"` // "switch" (default) or "host"
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

// IsHost returns true if the platform is a host device (not a network switch).
func (p *PlatformSpec) IsHost() bool {
	return p.DeviceType == "host"
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

// EVPNConfig defines EVPN overlay peering for a device profile.
type EVPNConfig struct {
	Peers          []string `json:"peers,omitempty"`
	RouteReflector bool     `json:"route_reflector,omitempty"`
	ClusterID      string   `json:"cluster_id,omitempty"`
}

// DeviceProfile contains per-device specification.
// This is the minimal set of device-specific data; everything else
// is inherited from region/global or derived at runtime.
type DeviceProfile struct {
	// REQUIRED - must be specified
	MgmtIP     string `json:"mgmt_ip"`
	LoopbackIP string `json:"loopback_ip"`
	Zone     string `json:"zone"` // Zone name (must exist in network.json zones)

	// OPTIONAL - EVPN overlay peering
	EVPN *EVPNConfig `json:"evpn,omitempty"`

	// OPTIONAL OVERRIDES - if set, override region/global values
	ASNumber *int `json:"as_number,omitempty"`

	// OPTIONAL - device-specific
	MAC             string           `json:"mac,omitempty"`
	Platform        string           `json:"platform,omitempty"`
	VLANPortMapping map[int][]string `json:"vlan_port_mapping,omitempty"` // TODO(v4): not consumed — implement VLAN-to-port mapping for pre-provisioned access ports

	OverridableSpecs // Embedded — node-level overrides

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

	// OPTIONAL - virtual host IP assignment (newtlab auto-derives if omitted)
	HostIP      string `json:"host_ip,omitempty"`      // data-plane IP (e.g., "10.1.100.10/24")
	HostGateway string `json:"host_gateway,omitempty"` // default gateway
}

// ResolvedProfile contains fully resolved device values
// after applying inheritance (profile > region > global) and derivation.
type ResolvedProfile struct {
	// From profile
	DeviceName string
	MgmtIP     string
	LoopbackIP string
	Zone     string
	Platform   string

	// Resolved from inheritance
	ASNumber         int
	IsRouteReflector bool
	ClusterID        string // RR cluster ID; from profile EVPN config or defaults to loopback IP

	// Derived at runtime
	RouterID     string   // = LoopbackIP
	VTEPSourceIP string   // = LoopbackIP
	BGPNeighbors []string // From profile EVPN peers → lookup loopback IPs

	// From profile (optional)
	MAC string

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
	ServiceTypeEVPNIRB     = "evpn-irb"     // L2+L3 overlay: requires ipvpn + macvpn
	ServiceTypeEVPNBridged = "evpn-bridged"  // L2 overlay: requires macvpn
	ServiceTypeEVPNRouted  = "evpn-routed"   // L3 overlay: requires ipvpn
	ServiceTypeIRB         = "irb"           // Local L2+L3: vlan + ip at apply time
	ServiceTypeBridged     = "bridged"       // Local L2: vlan at apply time
	ServiceTypeRouted      = "routed"        // Local L3: ip at apply time
)

// VRFType constants
const (
	VRFTypeInterface = "interface" // Per-interface VRF: {service}-{interface}
	VRFTypeShared    = "shared"    // Shared VRF: name from ipvpn.vrf field
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
