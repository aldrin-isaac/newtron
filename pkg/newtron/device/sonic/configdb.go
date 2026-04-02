// Package device handles SONiC device connection and configuration via config_db/Redis.
package sonic

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

// ConfigDB mirrors SONiC's config_db.json structure
type ConfigDB struct {
	DeviceMetadata    map[string]map[string]string  `json:"DEVICE_METADATA,omitempty"`
	Port              map[string]PortEntry          `json:"PORT,omitempty"`
	VLAN              map[string]VLANEntry          `json:"VLAN,omitempty"`
	VLANMember        map[string]VLANMemberEntry    `json:"VLAN_MEMBER,omitempty"`
	VLANInterface     map[string]map[string]string  `json:"VLAN_INTERFACE,omitempty"`
	Interface         map[string]InterfaceEntry     `json:"INTERFACE,omitempty"`
	PortChannel       map[string]PortChannelEntry   `json:"PORTCHANNEL,omitempty"`
	PortChannelMember map[string]map[string]string  `json:"PORTCHANNEL_MEMBER,omitempty"`
	LoopbackInterface map[string]map[string]string  `json:"LOOPBACK_INTERFACE,omitempty"`
	VRF               map[string]VRFEntry           `json:"VRF,omitempty"`
	VXLANTunnel       map[string]VXLANTunnelEntry   `json:"VXLAN_TUNNEL,omitempty"`
	VXLANTunnelMap    map[string]VXLANMapEntry      `json:"VXLAN_TUNNEL_MAP,omitempty"`
	VXLANEVPNNVO      map[string]EVPNNVOEntry       `json:"VXLAN_EVPN_NVO,omitempty"`
	SuppressVLANNeigh map[string]map[string]string  `json:"SUPPRESS_VLAN_NEIGH,omitempty"`
	SAG               map[string]map[string]string  `json:"SAG,omitempty"`
	SAGGlobal         map[string]map[string]string  `json:"SAG_GLOBAL,omitempty"`
	BGPNeighbor       map[string]BGPNeighborEntry   `json:"BGP_NEIGHBOR,omitempty"`
	BGPNeighborAF     map[string]BGPNeighborAFEntry `json:"BGP_NEIGHBOR_AF,omitempty"`
	BGPGlobals        map[string]BGPGlobalsEntry    `json:"BGP_GLOBALS,omitempty"`
	BGPGlobalsAF      map[string]BGPGlobalsAFEntry  `json:"BGP_GLOBALS_AF,omitempty"`
	BGPEVPNVNI        map[string]BGPEVPNVNIEntry    `json:"BGP_EVPN_VNI,omitempty"`
	RouteTable        map[string]StaticRouteEntry   `json:"ROUTE_TABLE,omitempty"`
	ACLTable          map[string]ACLTableEntry      `json:"ACL_TABLE,omitempty"`
	ACLRule           map[string]ACLRuleEntry       `json:"ACL_RULE,omitempty"`
	Scheduler         map[string]SchedulerEntry     `json:"SCHEDULER,omitempty"`
	Queue             map[string]QueueEntry         `json:"QUEUE,omitempty"`
	WREDProfile       map[string]WREDProfileEntry   `json:"WRED_PROFILE,omitempty"`
	PortQoSMap        map[string]PortQoSMapEntry    `json:"PORT_QOS_MAP,omitempty"`
	DSCPToTCMap       map[string]map[string]string  `json:"DSCP_TO_TC_MAP,omitempty"`
	TCToQueueMap      map[string]map[string]string  `json:"TC_TO_QUEUE_MAP,omitempty"`
	// v3: BGP management framework (frrcfgd) tables
	RouteRedistribute  map[string]RouteRedistributeEntry  `json:"ROUTE_REDISTRIBUTE,omitempty"`
	RouteMap           map[string]RouteMapEntry           `json:"ROUTE_MAP,omitempty"`
	BGPPeerGroup       map[string]BGPPeerGroupEntry       `json:"BGP_PEER_GROUP,omitempty"`
	BGPPeerGroupAF     map[string]BGPPeerGroupAFEntry     `json:"BGP_PEER_GROUP_AF,omitempty"`
	BGPGlobalsEVPNRT   map[string]BGPGlobalsEVPNRTEntry   `json:"BGP_GLOBALS_EVPN_RT,omitempty"`
	PrefixSet          map[string]PrefixSetEntry          `json:"PREFIX_SET,omitempty"`
	CommunitySet       map[string]CommunitySetEntry       `json:"COMMUNITY_SET,omitempty"`

	StaticRoute map[string]map[string]string `json:"STATIC_ROUTE,omitempty"`

	// Newtron-specific table — unified intent model (§39)
	NewtronIntent map[string]map[string]string `json:"NEWTRON_INTENT,omitempty"`
}

// ============================================================================
// Unified Intent Model (§39)
// ============================================================================

// IntentState represents the lifecycle state of an intent.
type IntentState string

const (
	IntentUnrealized IntentState = "unrealized"
	IntentInFlight   IntentState = "in-flight"
	IntentActuated   IntentState = "actuated"
)

// Operation names — the 16 newtron operations (§19).
// Each constant matches the operation's URL segment and intent record value.
const (
	OpSetupDevice        = "setup-device"
	OpCreateVRF          = "create-vrf"
	OpBindIPVPN          = "bind-ipvpn"
	OpCreateVLAN         = "create-vlan"
	OpBindMACVPN         = "bind-macvpn"
	OpCreateACL          = "create-acl"
	OpAddBGPEVPNPeer = "add-bgp-evpn-peer"
	OpCreatePortChannel  = "create-portchannel"
	OpConfigureIRB       = "configure-irb"
	OpAddStaticRoute     = "add-static-route"
	OpSetProperty        = "set-property"
	OpClearProperty      = "clear-property"
	OpConfigureInterface = "configure-interface"
	OpAddBGPPeer         = "add-bgp-peer"
	OpApplyService       = "apply-service"
	OpBindACL            = "bind-acl"
	OpApplyQoS              = "apply-qos"
	OpAddACLRule            = "add-acl-rule"
	OpAddPortChannelMember  = "add-pc-member"
	OpInterfaceInit         = "interface-init"
	OpDeployService         = "deploy-service"
)

// Intent param field names — shared across intent construction, teardown reads,
// and IntentToStep conversion. Using constants prevents typo-induced data loss.
const (
	FieldServiceName = "service_name"
	FieldServiceType = "service_type"
	FieldVRFName     = "vrf_name"
	FieldIPAddress   = "ip_address"
	FieldVLANID      = "vlan_id"
	FieldL3VNI       = "l3vni"
	FieldL3VNIVlan    = "l3vni_vlan"
	FieldRouteTargets = "route_targets"
	FieldName        = "name"
	FieldNeighborIP  = "neighbor_ip"
	FieldVRF         = "vrf"
	FieldPrefix      = "prefix"
	FieldNextHop     = "next_hop"
	FieldMetric      = "metric"
	FieldASN         = "asn"
	FieldEVPN        = "evpn"
	FieldDescription = "description"
	FieldProperty    = "property"
	FieldValue       = "value"
	FieldIntfIP      = "ip"
	FieldRemoteAS    = "remote_as"
	FieldQoSPolicy   = "policy"
	FieldACLName     = "acl_name"
	FieldDirection   = "direction"
	FieldMembers     = "members"
	FieldACLType     = "type"
	FieldStage       = "stage"
	FieldPorts       = "ports"
	FieldAnycastMAC  = "anycast_mac"
	FieldMACVPN      = "macvpn"
	FieldIPVPN       = "ipvpn"
	FieldVNI         = "vni"
	FieldSourceIP    = "source_ip"
	FieldBGPPeerAS   = "bgp_peer_as"
	FieldTagged          = "tagged"
	FieldARPSuppression  = "arp_suppression"
	FieldRules           = "rules"
)

// Intent is the internal domain model for a desired-state record bound to
// a device resource. See DESIGN_PRINCIPLES_NEWTRON §39 for the full model.
//
// This is the internal (node-accessible) type. The public API type
// (pkg/newtron.Intent) mirrors this with domain vocabulary for external
// consumers. Conversions happen at the API boundary.
type Intent struct {
	// Identity
	Resource  string `json:"resource"`            // binding point: "interface|Ethernet0", "vlan|100", "device"
	Operation string `json:"operation"`           // composite op: "apply-service", "create-vlan", "setup-device"
	Name      string `json:"name,omitempty"`      // spec reference: "transit", "" if none

	// DAG — structural dependencies between intent records
	Parents  []string `json:"parents,omitempty"`  // resource keys this intent depends on (_parents CSV)
	Children []string `json:"children,omitempty"` // resource keys that depend on this intent (_children CSV)

	// Lifecycle
	State     IntentState `json:"state"`
	Holder    string      `json:"holder,omitempty"`
	Created   time.Time   `json:"created,omitempty"`
	AppliedAt *time.Time  `json:"applied_at,omitempty"`
	AppliedBy string      `json:"applied_by,omitempty"`

	// Resolved parameters — self-sufficient for teardown + reconstruction (§37).
	Params map[string]string `json:"params,omitempty"`

	// Composite operations — expanded primitive list for crash recovery.
	Phase           string             `json:"phase,omitempty"`
	RollbackHolder  string             `json:"rollback_holder,omitempty"`
	RollbackStarted *time.Time         `json:"rollback_started,omitempty"`
	Operations      []IntentOperation  `json:"operations,omitempty"`
}

// IsService returns true if this intent represents a service binding.
func (i *Intent) IsService() bool {
	return i.Operation == OpApplyService
}

// IsInFlight returns true if this intent is currently being actuated.
func (i *Intent) IsInFlight() bool {
	return i.State == IntentInFlight
}

// IsActuated returns true if this intent has been fully realized.
func (i *Intent) IsActuated() bool {
	return i.State == IntentActuated
}

// intentIdentityFields are the fields that describe intent identity/lifecycle
// (as opposed to resolved parameters). These are stripped when extracting
// params from a flat CONFIG_DB record.
var intentIdentityFields = map[string]bool{
	"state": true, "operation": true, "name": true,
	"holder": true, "created": true,
	"applied_at": true, "applied_by": true,
	"phase": true, "rollback_holder": true, "rollback_started": true,
	"operations": true,
	"_parents": true, "_children": true,
}

// NewIntent constructs an Intent from a flat CONFIG_DB field map.
// This is the primary constructor — CONFIG_DB stores intents as flat hashes
// with identity fields (state, operation, name) alongside resolved params.
func NewIntent(resource string, fields map[string]string) *Intent {
	// Extract params: everything that isn't an identity field.
	params := make(map[string]string)
	for k, v := range fields {
		if !intentIdentityFields[k] && v != "" {
			params[k] = v
		}
	}

	var appliedAt *time.Time
	if s := fields["applied_at"]; s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			appliedAt = &t
		}
	}

	var created time.Time
	if s := fields["created"]; s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			created = t
		}
	}

	state := IntentState(fields["state"])
	if state == "" {
		state = IntentActuated // default for records without explicit state
	}

	return &Intent{
		Resource:  resource,
		Operation: fields["operation"],
		Name:      fields["name"],
		Parents:   parseCSV(fields["_parents"]),
		Children:  parseCSV(fields["_children"]),
		State:     state,
		Holder:    fields["holder"],
		Created:   created,
		AppliedAt: appliedAt,
		AppliedBy: fields["applied_by"],
		Params:    params,
		Phase:     fields["phase"],
	}
}

// ToFields converts an Intent to a flat CONFIG_DB field map.
// This is the inverse of NewIntent — identity fields and params
// are merged into a single flat hash for storage.
func (i *Intent) ToFields() map[string]string {
	result := make(map[string]string, len(i.Params)+8)
	// Copy params first.
	for k, v := range i.Params {
		result[k] = v
	}
	// Add identity fields (overwrite any param collision).
	result["state"] = string(i.State)
	if i.Operation != "" {
		result["operation"] = i.Operation
	}
	if i.Name != "" {
		result["name"] = i.Name
	}
	if i.Holder != "" {
		result["holder"] = i.Holder
	}
	if !i.Created.IsZero() {
		result["created"] = i.Created.UTC().Format(time.RFC3339)
	}
	if i.AppliedAt != nil {
		result["applied_at"] = i.AppliedAt.UTC().Format(time.RFC3339)
	}
	if i.AppliedBy != "" {
		result["applied_by"] = i.AppliedBy
	}
	if i.Phase != "" {
		result["phase"] = i.Phase
	}
	// Always include _parents and _children — even when empty — because Redis
	// HSET merges fields and won't delete a field that isn't in the update map.
	// Omitting _children when empty would leave the old value in Redis.
	result["_parents"] = strings.Join(i.Parents, ",")
	result["_children"] = strings.Join(i.Children, ",")
	return result
}

// parseCSV splits a comma-separated string into a slice, trimming whitespace
// and filtering empty strings. Returns nil for empty input.
func parseCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// AddToCSV appends an item to a CSV string if not already present.
func AddToCSV(csv, item string) string {
	items := parseCSV(csv)
	for _, existing := range items {
		if existing == item {
			return csv // already present
		}
	}
	if csv == "" {
		return item
	}
	return csv + "," + item
}

// RemoveFromCSV removes an item from a CSV string.
func RemoveFromCSV(csv, item string) string {
	items := parseCSV(csv)
	result := make([]string, 0, len(items))
	for _, existing := range items {
		if existing != item {
			result = append(result, existing)
		}
	}
	return strings.Join(result, ",")
}

// PortEntry represents a physical port configuration
type PortEntry struct {
	AdminStatus string `json:"admin_status,omitempty"`
	Alias       string `json:"alias,omitempty"`
	Description string `json:"description,omitempty"`
	FEC         string `json:"fec,omitempty"`
	Index       string `json:"index,omitempty"`
	Lanes       string `json:"lanes,omitempty"`
	MTU         string `json:"mtu,omitempty"`
	Speed       string `json:"speed,omitempty"`
	Autoneg     string `json:"autoneg,omitempty"`
}

// VLANEntry represents a VLAN configuration
type VLANEntry struct {
	VLANID      string `json:"vlanid"`
	Description string `json:"description,omitempty"`
	MTU         string `json:"mtu,omitempty"`
	AdminStatus string `json:"admin_status,omitempty"`
	DHCPServers string `json:"dhcp_servers,omitempty"`
}

// VLANMemberEntry represents VLAN membership
type VLANMemberEntry struct {
	TaggingMode string `json:"tagging_mode"` // tagged, untagged
}

// InterfaceEntry represents interface configuration
type InterfaceEntry struct {
	VRFName     string `json:"vrf_name,omitempty"`
	NATZone     string `json:"nat_zone,omitempty"`
	ProxyArp    string `json:"proxy_arp,omitempty"`
	MPLSEnabled string `json:"mpls,omitempty"`
}

// PortChannelEntry represents LAG configuration
type PortChannelEntry struct {
	AdminStatus string `json:"admin_status,omitempty"`
	MTU         string `json:"mtu,omitempty"`
	MinLinks    string `json:"min_links,omitempty"`
	Fallback    string `json:"fallback,omitempty"`
	FastRate    string `json:"fast_rate,omitempty"`
	LACPKey     string `json:"lacp_key,omitempty"`
	Description string `json:"description,omitempty"`
}

// VRFEntry represents VRF configuration
type VRFEntry struct {
	VNI      string `json:"vni,omitempty"`
	Fallback string `json:"fallback,omitempty"`
}

// VXLANTunnelEntry represents VTEP configuration
type VXLANTunnelEntry struct {
	SrcIP string `json:"src_ip"`
}

// VXLANMapEntry represents VNI to VLAN/VRF mapping
type VXLANMapEntry struct {
	VLAN string `json:"vlan,omitempty"`
	VRF  string `json:"vrf,omitempty"`
	VNI  string `json:"vni"`
}

// EVPNNVOEntry represents EVPN NVO configuration
type EVPNNVOEntry struct {
	SourceVTEP string `json:"source_vtep"`
}

// BGPGlobalsEntry represents global BGP settings for a VRF
type BGPGlobalsEntry struct {
	RouterID        string `json:"router_id,omitempty"`
	LocalASN        string `json:"local_asn,omitempty"`
	ConfedID        string `json:"confed_id,omitempty"`
	ConfedPeers     string `json:"confed_peers,omitempty"`
	GracefulRestart string `json:"graceful_restart,omitempty"`

	// v3: frrcfgd extended fields
	LoadBalanceMPRelax  string `json:"load_balance_mp_relax,omitempty"`
	RRClusterID         string `json:"rr_cluster_id,omitempty"`
	EBGPRequiresPolicy  string `json:"ebgp_requires_policy,omitempty"`
	DefaultIPv4Unicast  string `json:"default_ipv4_unicast,omitempty"`
	LogNeighborChanges  string `json:"log_neighbor_changes,omitempty"`
	SuppressFIBPending  string `json:"suppress_fib_pending,omitempty"`
}

// BGPGlobalsAFEntry represents BGP address-family settings
// Key format: "vrf_name|address_family" (e.g., "Vrf_CUST1|l2vpn_evpn")
type BGPGlobalsAFEntry struct {
	AdvertiseAllVNI    string `json:"advertise-all-vni,omitempty"`
	AdvertiseDefaultGW string `json:"advertise-default-gw,omitempty"`
	AdvertiseSVIIP     string `json:"advertise-svi-ip,omitempty"`
	AdvertiseIPv4      string `json:"advertise_ipv4_unicast,omitempty"`
	AdvertiseIPv6      string `json:"advertise_ipv6_unicast,omitempty"`
	RD                 string `json:"rd,omitempty"`
	RTImport           string `json:"rt_import,omitempty"` // Comma-separated list
	RTExport           string `json:"rt_export,omitempty"` // Comma-separated list
	RTImportEVPN       string `json:"route_target_import_evpn,omitempty"`
	RTExportEVPN       string `json:"route_target_export_evpn,omitempty"`

	// v3: frrcfgd extended fields
	MaxEBGPPaths           string `json:"max_ebgp_paths,omitempty"`
	MaxIBGPPaths           string `json:"max_ibgp_paths,omitempty"`
	RedistributeConnected  string `json:"redistribute_connected,omitempty"`
	RedistributeStatic     string `json:"redistribute_static,omitempty"`
}

// BGPGlobalsEVPNRTEntry represents a per-VRF EVPN route-target entry (frrcfgd managed).
// Key format: "vrf_name|L2VPN_EVPN|rt" (e.g., "Vrf_L3EVPN|L2VPN_EVPN|65001:50001")
type BGPGlobalsEVPNRTEntry struct {
	RouteTargetType string `json:"route-target-type,omitempty"` // "both", "import", "export"
}

// BGPEVPNVNIEntry represents per-VNI EVPN settings
// Key format: "vrf_name|vni" (e.g., "Vrf_CUST1|10001")
type BGPEVPNVNIEntry struct {
	RD                 string `json:"rd,omitempty"`
	RTImport           string `json:"route_target_import,omitempty"` // Comma-separated
	RTExport           string `json:"route_target_export,omitempty"` // Comma-separated
	AdvertiseDefaultGW string `json:"advertise_default_gw,omitempty"`
}

// BGPNeighborEntry represents a BGP neighbor
type BGPNeighborEntry struct {
	LocalAddr     string `json:"local_addr,omitempty"`
	Name          string `json:"name,omitempty"`
	ASN           string `json:"asn,omitempty"`
	HoldTime      string `json:"holdtime,omitempty"`
	KeepaliveTime string `json:"keepalive,omitempty"`
	AdminStatus   string `json:"admin_status,omitempty"`

	// v3: frrcfgd extended fields
	PeerGroup    string `json:"peer_group_name,omitempty"`
	EBGPMultihop string `json:"ebgp_multihop,omitempty"`
	Password     string `json:"password,omitempty"`
}

// BGPNeighborAFEntry represents per-neighbor address-family settings
// Key format: "neighbor_ip|address_family" (e.g., "10.0.0.2|l2vpn_evpn")
type BGPNeighborAFEntry struct {
	AdminStatus         string `json:"admin_status,omitempty"`
	RRClient            string `json:"rrclient,omitempty"`
	NHSelf              string `json:"nhself,omitempty"`
	NextHopUnchanged    string `json:"nexthop_unchanged,omitempty"`
	SoftReconfiguration string `json:"soft_reconfiguration,omitempty"`

	// frrcfgd extended fields
	AllowASIn        string `json:"allowas_in,omitempty"`
	RouteMapIn       string `json:"route_map_in,omitempty"`
	RouteMapOut      string `json:"route_map_out,omitempty"`
	PrefixListIn     string `json:"prefix_list_in,omitempty"`
	PrefixListOut    string `json:"prefix_list_out,omitempty"`
	DefaultOriginate string `json:"default_originate,omitempty"`
	AddpathTxAll     string `json:"addpath_tx_all_paths,omitempty"`
}

// StaticRouteEntry represents a static route in CONFIG_DB's ROUTE_TABLE.
type StaticRouteEntry struct {
	NextHop    string `json:"nexthop,omitempty"`
	Interface  string `json:"ifname,omitempty"`
	Distance   string `json:"distance,omitempty"`
	NextHopVRF string `json:"nexthop-vrf,omitempty"`
	Blackhole  string `json:"blackhole,omitempty"`
}

// ACLTableEntry represents an ACL table
type ACLTableEntry struct {
	PolicyDesc string `json:"policy_desc,omitempty"`
	Type       string `json:"type"`
	Stage      string `json:"stage,omitempty"`
	Ports      string `json:"ports,omitempty"` // Comma-separated
	Services   string `json:"services,omitempty"`
}

// ACLRuleEntry represents an ACL rule
type ACLRuleEntry struct {
	Priority       string `json:"PRIORITY,omitempty"`
	PacketAction   string `json:"PACKET_ACTION,omitempty"`
	SrcIP          string `json:"SRC_IP,omitempty"`
	DstIP          string `json:"DST_IP,omitempty"`
	IPProtocol     string `json:"IP_PROTOCOL,omitempty"`
	L4SrcPort      string `json:"L4_SRC_PORT,omitempty"`
	L4DstPort      string `json:"L4_DST_PORT,omitempty"`
	L4SrcPortRange string `json:"L4_SRC_PORT_RANGE,omitempty"`
	L4DstPortRange string `json:"L4_DST_PORT_RANGE,omitempty"`
	TCPFlags       string `json:"TCP_FLAGS,omitempty"`
	DSCP           string `json:"DSCP,omitempty"`
	ICMPType       string `json:"ICMP_TYPE,omitempty"`
	ICMPCode       string `json:"ICMP_CODE,omitempty"`
	EtherType      string `json:"ETHER_TYPE,omitempty"`
	InPorts        string `json:"IN_PORTS,omitempty"`
	RedirectPort   string `json:"REDIRECT_PORT,omitempty"`
}

// SchedulerEntry represents a QoS scheduler
type SchedulerEntry struct {
	Type   string `json:"type"`             // DWRR, STRICT
	Weight string `json:"weight,omitempty"` // For DWRR
}

// QueueEntry represents a queue configuration
type QueueEntry struct {
	Scheduler   string `json:"scheduler,omitempty"`
	WREDProfile string `json:"wred_profile,omitempty"`
}

// WREDProfileEntry represents a WRED drop profile
type WREDProfileEntry struct {
	GreenMinThreshold     string `json:"green_min_threshold,omitempty"`
	GreenMaxThreshold     string `json:"green_max_threshold,omitempty"`
	GreenDropProbability  string `json:"green_drop_probability,omitempty"`
	YellowMinThreshold    string `json:"yellow_min_threshold,omitempty"`
	YellowMaxThreshold    string `json:"yellow_max_threshold,omitempty"`
	YellowDropProbability string `json:"yellow_drop_probability,omitempty"`
	RedMinThreshold       string `json:"red_min_threshold,omitempty"`
	RedMaxThreshold       string `json:"red_max_threshold,omitempty"`
	RedDropProbability    string `json:"red_drop_probability,omitempty"`
	ECN                   string `json:"ecn,omitempty"`
}

// PortQoSMapEntry represents QoS map binding for a port
type PortQoSMapEntry struct {
	DSCPToTCMap  string `json:"dscp_to_tc_map,omitempty"`
	TCToQueueMap string `json:"tc_to_queue_map,omitempty"`
}


// ============================================================================
// v3: frrcfgd table entry types
// ============================================================================

// RouteRedistributeEntry represents route redistribution config.
// Key format: "vrf|src_protocol|address_family" (e.g., "default|connected|ipv4")
type RouteRedistributeEntry struct {
	RouteMap string `json:"route_map,omitempty"`
	Metric   string `json:"metric,omitempty"`
}

// RouteMapEntry represents a route-map rule.
// Key format: "map_name|seq" (e.g., "RM_IMPORT|10")
type RouteMapEntry struct {
	Action         string `json:"route_operation"`           // permit, deny
	MatchPrefixSet string `json:"match_prefix_set,omitempty"`
	MatchCommunity string `json:"match_community,omitempty"`
	MatchASPath    string `json:"match_as_path,omitempty"`
	MatchNextHop   string `json:"match_next_hop,omitempty"`
	SetLocalPref   string `json:"set_local_pref,omitempty"`
	SetCommunity   string `json:"set_community,omitempty"`
	SetMED         string `json:"set_med,omitempty"`
	SetNextHop     string `json:"set_next_hop,omitempty"`
}

// BGPPeerGroupEntry represents a BGP peer group template.
// Key format: peer_group_name (e.g., "SPINE_PEERS")
type BGPPeerGroupEntry struct {
	ASN          string `json:"asn,omitempty"`
	LocalAddr    string `json:"local_addr,omitempty"`
	AdminStatus  string `json:"admin_status,omitempty"`
	HoldTime     string `json:"holdtime,omitempty"`
	Keepalive    string `json:"keepalive,omitempty"`
	Password     string `json:"password,omitempty"`
	EBGPMultihop string `json:"ebgp_multihop,omitempty"`
}

// BGPPeerGroupAFEntry represents per-AF settings for a peer group.
// Key format: "peer_group_name|address_family" (e.g., "SPINE_PEERS|ipv4_unicast")
type BGPPeerGroupAFEntry struct {
	AdminStatus         string `json:"admin_status,omitempty"`
	RRClient            string `json:"rrclient,omitempty"`
	NHSelf              string `json:"nhself,omitempty"`
	NextHopUnchanged    string `json:"nexthop_unchanged,omitempty"`
	RouteMapIn          string `json:"route_map_in,omitempty"`
	RouteMapOut         string `json:"route_map_out,omitempty"`
	SoftReconfiguration string `json:"soft_reconfiguration,omitempty"`
}

// PrefixSetEntry represents an IP prefix list entry for route-map matching.
// Key format: "set_name|seq" (e.g., "PL_ALLOW|10")
type PrefixSetEntry struct {
	IPPrefix string `json:"ip_prefix"`
	Action   string `json:"action"`           // permit, deny
	MaskLenRange string `json:"masklength_range,omitempty"` // e.g., "24..32"
}

// CommunitySetEntry represents a BGP community list.
// Key format: set_name (e.g., "CUST_COMMUNITY")
type CommunitySetEntry struct {
	SetType     string `json:"set_type,omitempty"` // standard, expanded
	MatchAction string `json:"match_action,omitempty"`
	CommunityMember string `json:"community_member,omitempty"` // Comma-separated communities
}

// ============================================================================
// Projection Updates (for offline/abstract mode)
// ============================================================================

// ApplyEntries updates the ConfigDB's typed maps from a slice of entries.
// Used by abstract Node to keep the projection in sync as operations
// generate entries. Only tables needed for precondition checks and property
// accessors are handled — unrecognized tables are silently skipped (entries
// still accumulate in the abstract Node for composite export).
func (db *ConfigDB) ApplyEntries(entries []Entry) {
	for _, e := range entries {
		db.hydrateConfigTable(e.Table, e.Key, e.Fields)
	}
}

// hydrateConfigTable populates the ConfigDB from a flat field map using the
// unified configTableHydrators registry. This is the single code path for
// both physical nodes (Redis HGETALL → struct) and abstract nodes (Entry
// fields → projection struct).
func (db *ConfigDB) hydrateConfigTable(table, key string, fields map[string]string) {
	if hydrator, ok := configTableHydrators[table]; ok {
		hydrator(db, key, fields)
	}
}

// DeleteEntry removes an entry from the ConfigDB by table and key.
// Used by render to keep the projection consistent when processing deletes.
func (db *ConfigDB) DeleteEntry(table, key string) {
	switch table {
	case "PORT":
		delete(db.Port, key)
	case "PORTCHANNEL":
		delete(db.PortChannel, key)
	case "PORTCHANNEL_MEMBER":
		delete(db.PortChannelMember, key)
	case "VLAN":
		delete(db.VLAN, key)
	case "VLAN_MEMBER":
		delete(db.VLANMember, key)
	case "VRF":
		delete(db.VRF, key)
	case "INTERFACE":
		delete(db.Interface, key)
	case "VLAN_INTERFACE":
		delete(db.VLANInterface, key)
	case "VXLAN_TUNNEL":
		delete(db.VXLANTunnel, key)
	case "VXLAN_TUNNEL_MAP":
		delete(db.VXLANTunnelMap, key)
	case "VXLAN_EVPN_NVO":
		delete(db.VXLANEVPNNVO, key)
	case "BGP_GLOBALS":
		delete(db.BGPGlobals, key)
	case "BGP_NEIGHBOR":
		delete(db.BGPNeighbor, key)
	case "BGP_NEIGHBOR_AF":
		delete(db.BGPNeighborAF, key)
	case "BGP_GLOBALS_AF":
		delete(db.BGPGlobalsAF, key)
	case "DEVICE_METADATA":
		delete(db.DeviceMetadata, key)
	case "NEWTRON_INTENT":
		delete(db.NewtronIntent, key)
	case "SUPPRESS_VLAN_NEIGH":
		delete(db.SuppressVLANNeigh, key)
	case "ACL_TABLE":
		delete(db.ACLTable, key)
	case "ACL_RULE":
		delete(db.ACLRule, key)
	case "LOOPBACK_INTERFACE":
		delete(db.LoopbackInterface, key)
	case "ROUTE_REDISTRIBUTE":
		delete(db.RouteRedistribute, key)
	case "SAG_GLOBAL":
		delete(db.SAGGlobal, key)
	case "ROUTE_MAP":
		delete(db.RouteMap, key)
	case "PREFIX_SET":
		delete(db.PrefixSet, key)
	case "COMMUNITY_SET":
		delete(db.CommunitySet, key)
	case "BGP_PEER_GROUP":
		delete(db.BGPPeerGroup, key)
	case "BGP_PEER_GROUP_AF":
		delete(db.BGPPeerGroupAF, key)
	case "BGP_EVPN_VNI":
		delete(db.BGPEVPNVNI, key)
	case "BGP_GLOBALS_EVPN_RT":
		delete(db.BGPGlobalsEVPNRT, key)
	case "STATIC_ROUTE":
		delete(db.StaticRoute, key)
	case "ROUTE_TABLE":
		delete(db.RouteTable, key)
	case "SAG":
		delete(db.SAG, key)
	case "PORT_QOS_MAP":
		delete(db.PortQoSMap, key)
	case "QUEUE":
		delete(db.Queue, key)
	case "DSCP_TO_TC_MAP":
		delete(db.DSCPToTCMap, key)
	case "TC_TO_QUEUE_MAP":
		delete(db.TCToQueueMap, key)
	case "SCHEDULER":
		delete(db.Scheduler, key)
	case "WRED_PROFILE":
		delete(db.WREDProfile, key)
	}
}

// structToFields converts a typed struct to map[string]string using json tags.
// Zero-value (empty string) fields are omitted.
func structToFields(v any) map[string]string {
	val := reflect.ValueOf(v)
	typ := val.Type()
	fields := make(map[string]string)
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if s := val.Field(i).String(); s != "" {
			fields[name] = s
		}
	}
	return fields
}

// ExportEntries is the inverse of ApplyEntries. It returns all non-empty entries
// from all tables in the ConfigDB.
func (db *ConfigDB) ExportEntries() []Entry {
	var entries []Entry

	appendTyped := func(table string, key string, v any) {
		fields := structToFields(v)
		// Export even with empty fields — SONiC uses field-less entries for
		// IP assignments (INTERFACE|Eth0|10.0.0.1/31), portchannel members,
		// etc. The delivery layer writes NULL:NULL sentinels for these.
		entries = append(entries, Entry{Table: table, Key: key, Fields: fields})
	}

	appendRaw := func(table string, key string, fields map[string]string) {
		entries = append(entries, Entry{Table: table, Key: key, Fields: copyFields(fields)})
	}

	// Raw maps
	for k, v := range db.DeviceMetadata {
		appendRaw("DEVICE_METADATA", k, v)
	}
	for k, v := range db.VLANInterface {
		appendRaw("VLAN_INTERFACE", k, v)
	}
	for k, v := range db.PortChannelMember {
		appendRaw("PORTCHANNEL_MEMBER", k, v)
	}
	for k, v := range db.LoopbackInterface {
		appendRaw("LOOPBACK_INTERFACE", k, v)
	}
	for k, v := range db.SuppressVLANNeigh {
		appendRaw("SUPPRESS_VLAN_NEIGH", k, v)
	}
	for k, v := range db.SAG {
		appendRaw("SAG", k, v)
	}
	for k, v := range db.SAGGlobal {
		appendRaw("SAG_GLOBAL", k, v)
	}
	for k, v := range db.DSCPToTCMap {
		appendRaw("DSCP_TO_TC_MAP", k, v)
	}
	for k, v := range db.TCToQueueMap {
		appendRaw("TC_TO_QUEUE_MAP", k, v)
	}
	for k, v := range db.StaticRoute {
		appendRaw("STATIC_ROUTE", k, v)
	}
	for k, v := range db.NewtronIntent {
		appendRaw("NEWTRON_INTENT", k, v)
	}

	// Typed maps
	for k, v := range db.Port {
		appendTyped("PORT", k, v)
	}
	for k, v := range db.VLAN {
		appendTyped("VLAN", k, v)
	}
	for k, v := range db.VLANMember {
		appendTyped("VLAN_MEMBER", k, v)
	}
	for k, v := range db.Interface {
		appendTyped("INTERFACE", k, v)
	}
	for k, v := range db.PortChannel {
		appendTyped("PORTCHANNEL", k, v)
	}
	for k, v := range db.VRF {
		appendTyped("VRF", k, v)
	}
	for k, v := range db.VXLANTunnel {
		appendTyped("VXLAN_TUNNEL", k, v)
	}
	for k, v := range db.VXLANTunnelMap {
		appendTyped("VXLAN_TUNNEL_MAP", k, v)
	}
	for k, v := range db.VXLANEVPNNVO {
		appendTyped("VXLAN_EVPN_NVO", k, v)
	}
	for k, v := range db.BGPNeighbor {
		appendTyped("BGP_NEIGHBOR", k, v)
	}
	for k, v := range db.BGPNeighborAF {
		appendTyped("BGP_NEIGHBOR_AF", k, v)
	}
	for k, v := range db.BGPGlobals {
		appendTyped("BGP_GLOBALS", k, v)
	}
	for k, v := range db.BGPGlobalsAF {
		appendTyped("BGP_GLOBALS_AF", k, v)
	}
	for k, v := range db.BGPEVPNVNI {
		appendTyped("BGP_EVPN_VNI", k, v)
	}
	for k, v := range db.RouteTable {
		appendTyped("ROUTE_TABLE", k, v)
	}
	for k, v := range db.ACLTable {
		appendTyped("ACL_TABLE", k, v)
	}
	for k, v := range db.ACLRule {
		appendTyped("ACL_RULE", k, v)
	}
	for k, v := range db.Scheduler {
		appendTyped("SCHEDULER", k, v)
	}
	for k, v := range db.Queue {
		appendTyped("QUEUE", k, v)
	}
	for k, v := range db.WREDProfile {
		appendTyped("WRED_PROFILE", k, v)
	}
	for k, v := range db.PortQoSMap {
		appendTyped("PORT_QOS_MAP", k, v)
	}
	for k, v := range db.RouteRedistribute {
		appendTyped("ROUTE_REDISTRIBUTE", k, v)
	}
	for k, v := range db.RouteMap {
		appendTyped("ROUTE_MAP", k, v)
	}
	for k, v := range db.BGPPeerGroup {
		appendTyped("BGP_PEER_GROUP", k, v)
	}
	for k, v := range db.BGPPeerGroupAF {
		appendTyped("BGP_PEER_GROUP_AF", k, v)
	}
	for k, v := range db.BGPGlobalsEVPNRT {
		appendTyped("BGP_GLOBALS_EVPN_RT", k, v)
	}
	for k, v := range db.PrefixSet {
		appendTyped("PREFIX_SET", k, v)
	}
	for k, v := range db.CommunitySet {
		appendTyped("COMMUNITY_SET", k, v)
	}

	return entries
}

// ExportPorts returns the PORT table as raw map[string]map[string]string.
// Used by drift detection to seed ReconstructExpected with port metadata.
func (db *ConfigDB) ExportPorts() map[string]map[string]string {
	result := make(map[string]map[string]string, len(db.Port))
	for name, entry := range db.Port {
		result[name] = structToFields(entry)
	}
	return result
}

// ExportIntentEntries returns only the NEWTRON_INTENT entries from the ConfigDB.
// Used by delta Reconcile to deliver intent records separately (intents are
// excluded from DiffConfigDB and thus from ApplyDrift).
func (db *ConfigDB) ExportIntentEntries() []Entry {
	var entries []Entry
	for k, v := range db.NewtronIntent {
		entries = append(entries, Entry{Table: "NEWTRON_INTENT", Key: k, Fields: v})
	}
	return entries
}

// ExportRaw converts the ConfigDB to a RawConfigDB for drift detection.
// Equivalent to the former CompositeConfig.Tables — same data, built from ExportEntries.
func (db *ConfigDB) ExportRaw() RawConfigDB {
	raw := make(RawConfigDB)
	for _, e := range db.ExportEntries() {
		if raw[e.Table] == nil {
			raw[e.Table] = make(map[string]map[string]string)
		}
		raw[e.Table][e.Key] = e.Fields
	}
	return raw
}

// copyFields returns a shallow copy of the map (avoids aliasing caller's map).
func copyFields(fields map[string]string) map[string]string {
	if fields == nil {
		return map[string]string{}
	}
	cp := make(map[string]string, len(fields))
	for k, v := range fields {
		cp[k] = v
	}
	return cp
}

// ConfigDBClient wraps Redis client for config_db access
type ConfigDBClient struct {
	client *redis.Client
	ctx    context.Context
}

// NewConfigDBClient creates a new config_db client
func NewConfigDBClient(addr string) *ConfigDBClient {
	return &ConfigDBClient{
		client: redis.NewClient(&redis.Options{
			Addr: addr,
			DB:   4, // CONFIG_DB
		}),
		ctx: context.Background(),
	}
}

// Connect tests the connection
func (c *ConfigDBClient) Connect() error {
	return c.client.Ping(c.ctx).Err()
}

// Close closes the connection
func (c *ConfigDBClient) Close() error {
	return c.client.Close()
}

// GetAll reads the entire config_db
func (c *ConfigDBClient) GetAll() (*ConfigDB, error) {
	// Get all keys using cursor-based SCAN (non-blocking, unlike KEYS *)
	keys, err := scanKeys(c.ctx, c.client, "*", 100)
	if err != nil {
		return nil, err
	}

	db := newConfigDB()

	for _, key := range keys {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) < 2 {
			continue
		}
		table := parts[0]
		entry := parts[1]

		// Get hash values
		vals, err := c.client.HGetAll(c.ctx, key).Result()
		if err != nil {
			continue
		}

		c.parseEntry(db, table, entry, vals)
	}

	return db, nil
}

func (c *ConfigDBClient) parseEntry(db *ConfigDB, table, entry string, vals map[string]string) {
	db.hydrateConfigTable(table, entry, vals)
}

// Set writes a table entry. If fields is empty, a "NULL":"NULL" sentinel is
// written so the Redis key is actually created (SONiC convention for
// field-less entries like PORTCHANNEL_MEMBER or INTERFACE IP keys).
func (c *ConfigDBClient) Set(table, key string, fields map[string]string) error {
	redisKey := fmt.Sprintf("%s|%s", table, key)
	if len(fields) == 0 {
		return c.client.HSet(c.ctx, redisKey, "NULL", "NULL").Err()
	}
	// Write all fields in a single HSET command to fire exactly ONE keyspace
	// notification. Writing one field at a time fires N notifications, causing
	// bgpcfgd to receive partial state and attempt BGP neighbor programming
	// before all fields (asn, local_addr, admin_status) are present.
	args := make([]interface{}, 0, len(fields)*2)
	for k, v := range fields {
		args = append(args, k, v)
	}
	return c.client.HSet(c.ctx, redisKey, args...).Err()
}

// Delete removes a table entry
func (c *ConfigDBClient) Delete(table, key string) error {
	redisKey := fmt.Sprintf("%s|%s", table, key)
	return c.client.Del(c.ctx, redisKey).Err()
}


// Get reads a table entry
func (c *ConfigDBClient) Get(table, key string) (map[string]string, error) {
	redisKey := fmt.Sprintf("%s|%s", table, key)
	return c.client.HGetAll(c.ctx, redisKey).Result()
}

// TableKeys returns all Redis keys matching the given table prefix.
// Useful for counting entries or iterating a table without loading all values.
func (c *ConfigDBClient) TableKeys(table string) ([]string, error) {
	pattern := fmt.Sprintf("%s|*", table)
	return scanKeys(c.ctx, c.client, pattern, 100)
}

// Exists checks if a key exists
func (c *ConfigDBClient) Exists(table, key string) (bool, error) {
	redisKey := fmt.Sprintf("%s|%s", table, key)
	n, err := c.client.Exists(c.ctx, redisKey).Result()
	return n > 0, err
}


// ============================================================================
// Projection query methods — used by loopback mode to read from the in-memory
// projection instead of Redis. Same interface as ConfigDBClient.Get/Exists/TableKeys.
// ============================================================================

// Get returns the fields for a table|key entry from the projection.
// Returns an empty map (not error) if the entry does not exist.
func (db *ConfigDB) Get(table, key string) map[string]string {
	raw := db.ExportRaw()
	if t, ok := raw[table]; ok {
		if fields, ok := t[key]; ok {
			return fields
		}
	}
	return map[string]string{}
}

// Exists returns true if a table|key entry exists in the projection.
func (db *ConfigDB) Exists(table, key string) bool {
	raw := db.ExportRaw()
	if t, ok := raw[table]; ok {
		_, ok := t[key]
		return ok
	}
	return false
}

// TableKeys returns all keys in a table from the projection.
func (db *ConfigDB) TableKeys(table string) []string {
	raw := db.ExportRaw()
	t, ok := raw[table]
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	return keys
}

// ============================================================================
// Nil-safe query methods — called by network.Device to avoid nil-check boilerplate
// ============================================================================

// HasVLAN reports whether the given VLAN ID exists in the VLAN table.
func (db *ConfigDB) HasVLAN(id int) bool {
	if db == nil {
		return false
	}
	_, ok := db.VLAN[fmt.Sprintf("Vlan%d", id)]
	return ok
}

// HasVRF reports whether the named VRF exists.
func (db *ConfigDB) HasVRF(name string) bool {
	if db == nil {
		return false
	}
	_, ok := db.VRF[name]
	return ok
}

// HasPortChannel reports whether the named PortChannel exists.
func (db *ConfigDB) HasPortChannel(name string) bool {
	if db == nil {
		return false
	}
	_, ok := db.PortChannel[name]
	return ok
}

// HasACLTable reports whether the named ACL table exists.
func (db *ConfigDB) HasACLTable(name string) bool {
	if db == nil {
		return false
	}
	_, ok := db.ACLTable[name]
	return ok
}

// HasVTEP reports whether any VXLAN tunnel (VTEP) is configured.
func (db *ConfigDB) HasVTEP() bool {
	if db == nil {
		return false
	}
	return len(db.VXLANTunnel) > 0
}

// HasBGPNeighbor reports whether the given BGP neighbor key exists.
// Key format: "vrf|ip" (e.g., "default|10.0.0.2").
func (db *ConfigDB) HasBGPNeighbor(key string) bool {
	if db == nil {
		return false
	}
	_, ok := db.BGPNeighbor[key]
	return ok
}

// HasInterface reports whether the named interface exists in the Port,
// PortChannel, or VLAN table (for SVI interfaces like Vlan100).
func (db *ConfigDB) HasInterface(name string) bool {
	if db == nil {
		return false
	}
	if _, ok := db.Port[name]; ok {
		return true
	}
	if _, ok := db.PortChannel[name]; ok {
		return true
	}
	// VLAN SVI interfaces (Vlan100, Vlan200) live in the VLAN table
	if strings.HasPrefix(name, "Vlan") {
		id := 0
		if n, _ := fmt.Sscanf(name[4:], "%d", &id); n == 1 && id > 0 {
			return db.HasVLAN(id)
		}
	}
	return false
}

// BGPConfigured reports whether BGP_GLOBALS|default exists — the frrcfgd-managed
// BGP instance that frrcfgd uses to create the `router bgp` process. Without this
// entry, frrcfgd silently ignores BGP_NEIGHBOR entries.
//
// This intentionally does NOT check DEVICE_METADATA.bgp_asn or BGP_NEIGHBOR count.
// Factory SONiC images may have bgp_asn set (bgpcfgd era) and/or legacy BGP_NEIGHBOR
// entries without a BGP_GLOBALS entry — those do not constitute a working frrcfgd
// BGP instance.
func (db *ConfigDB) BGPConfigured() bool {
	if db == nil {
		return false
	}
	_, ok := db.BGPGlobals["default"]
	return ok
}

// scanKeys iterates Redis keys matching the given pattern using cursor-based
// SCAN instead of the blocking O(N) KEYS command. The count hint controls
// how many keys Redis returns per iteration (not an exact limit).
func scanKeys(ctx context.Context, client *redis.Client, pattern string, countHint int64) ([]string, error) {
	var cursor uint64
	var keys []string
	for {
		batch, nextCursor, err := client.Scan(ctx, cursor, pattern, countHint).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return keys, nil
}
