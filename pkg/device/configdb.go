// Package device handles SONiC device connection and configuration via config_db/Redis.
package device

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
	ACLTableType      map[string]ACLTableTypeEntry  `json:"ACL_TABLE_TYPE,omitempty"`
	Scheduler         map[string]SchedulerEntry     `json:"SCHEDULER,omitempty"`
	Queue             map[string]QueueEntry         `json:"QUEUE,omitempty"`
	WREDProfile       map[string]WREDProfileEntry   `json:"WRED_PROFILE,omitempty"`
	PortQoSMap        map[string]PortQoSMapEntry    `json:"PORT_QOS_MAP,omitempty"`
	DSCPToTCMap       map[string]map[string]string  `json:"DSCP_TO_TC_MAP,omitempty"`
	TCToQueueMap      map[string]map[string]string  `json:"TC_TO_QUEUE_MAP,omitempty"`
	Policer           map[string]PolicerEntry       `json:"POLICER,omitempty"`

	// v3: BGP management framework (frrcfgd) tables
	RouteRedistribute  map[string]RouteRedistributeEntry  `json:"ROUTE_REDISTRIBUTE,omitempty"`
	RouteMap           map[string]RouteMapEntry           `json:"ROUTE_MAP,omitempty"`
	BGPPeerGroup       map[string]BGPPeerGroupEntry       `json:"BGP_PEER_GROUP,omitempty"`
	BGPPeerGroupAF     map[string]BGPPeerGroupAFEntry     `json:"BGP_PEER_GROUP_AF,omitempty"`
	BGPGlobalsAFNet    map[string]BGPGlobalsAFNetEntry    `json:"BGP_GLOBALS_AF_NETWORK,omitempty"`
	BGPGlobalsAFAgg    map[string]BGPGlobalsAFAggEntry    `json:"BGP_GLOBALS_AF_AGGREGATE_ADDR,omitempty"`
	PrefixSet          map[string]PrefixSetEntry          `json:"PREFIX_SET,omitempty"`
	CommunitySet       map[string]CommunitySetEntry       `json:"COMMUNITY_SET,omitempty"`
	ASPathSet          map[string]ASPathSetEntry           `json:"AS_PATH_SET,omitempty"`

	// Newtron-specific tables (custom, not standard SONiC)
	NewtronServiceBinding map[string]ServiceBindingEntry `json:"NEWTRON_SERVICE_BINDING,omitempty"`
}

// ServiceBindingEntry tracks service bindings applied by newtron.
// Key format: interface name (e.g., "Ethernet0", "PortChannel100", "Vlan100")
// This provides explicit tracking of what service was applied, enabling
// proper removal and refresh without relying on naming conventions.
type ServiceBindingEntry struct {
	ServiceName string `json:"service_name"`          // Service name from network.json
	IPAddress   string `json:"ip_address,omitempty"`  // IP assigned (for L3 services)
	VRFName     string `json:"vrf_name,omitempty"`    // VRF created/bound
	IPVPN       string `json:"ipvpn,omitempty"`       // IP-VPN name (for L3 EVPN)
	MACVPN      string `json:"macvpn,omitempty"`      // MAC-VPN name (for L2 EVPN)
	IngressACL  string `json:"ingress_acl,omitempty"` // Generated ingress ACL name
	EgressACL   string `json:"egress_acl,omitempty"`  // Generated egress ACL name
	AppliedAt   string `json:"applied_at,omitempty"`  // Timestamp when applied
	AppliedBy   string `json:"applied_by,omitempty"`  // User who applied
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
	MaxEBGPPaths string `json:"max_ebgp_paths,omitempty"`
	MaxIBGPPaths string `json:"max_ibgp_paths,omitempty"`
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
	PeerGroup    string `json:"peer_group,omitempty"`
	EBGPMultihop string `json:"ebgp_multihop,omitempty"`
	Password     string `json:"password,omitempty"`
}

// BGPNeighborAFEntry represents per-neighbor address-family settings
// Key format: "neighbor_ip|address_family" (e.g., "10.0.0.2|l2vpn_evpn")
type BGPNeighborAFEntry struct {
	Activate             string `json:"activate,omitempty"`
	RouteReflectorClient string `json:"route_reflector_client,omitempty"`
	NextHopSelf          string `json:"next_hop_self,omitempty"`
	SoftReconfiguration  string `json:"soft_reconfiguration,omitempty"`

	// v3: frrcfgd extended fields
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

// ACLTableTypeEntry represents a custom ACL table type
type ACLTableTypeEntry struct {
	MatchFields   string `json:"matches,omitempty"`
	Actions       string `json:"actions,omitempty"`
	BindPointType string `json:"bind_point_type,omitempty"`
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

// PolicerEntry represents a rate limiter
type PolicerEntry struct {
	MeterType    string `json:"meter_type,omitempty"`
	Mode         string `json:"mode,omitempty"`
	CIR          string `json:"cir,omitempty"`
	CBS          string `json:"cbs,omitempty"`
	PIR          string `json:"pir,omitempty"`
	PBS          string `json:"pbs,omitempty"`
	GreenAction  string `json:"green_action,omitempty"`
	YellowAction string `json:"yellow_action,omitempty"`
	RedAction    string `json:"red_action,omitempty"`
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
	ASN         string `json:"asn,omitempty"`
	LocalAddr   string `json:"local_addr,omitempty"`
	AdminStatus string `json:"admin_status,omitempty"`
	HoldTime    string `json:"holdtime,omitempty"`
	Keepalive   string `json:"keepalive,omitempty"`
	Password    string `json:"password,omitempty"`
}

// BGPPeerGroupAFEntry represents per-AF settings for a peer group.
// Key format: "peer_group_name|address_family" (e.g., "SPINE_PEERS|ipv4_unicast")
type BGPPeerGroupAFEntry struct {
	Activate             string `json:"activate,omitempty"`
	RouteReflectorClient string `json:"route_reflector_client,omitempty"`
	NextHopSelf          string `json:"next_hop_self,omitempty"`
	RouteMapIn           string `json:"route_map_in,omitempty"`
	RouteMapOut          string `json:"route_map_out,omitempty"`
	SoftReconfiguration  string `json:"soft_reconfiguration,omitempty"`
}

// BGPGlobalsAFNetEntry represents a BGP network statement.
// Key format: "vrf|address_family|prefix" (e.g., "default|ipv4_unicast|10.0.0.0/24")
type BGPGlobalsAFNetEntry struct {
	Policy string `json:"policy,omitempty"` // Optional route-map
}

// BGPGlobalsAFAggEntry represents a BGP aggregate-address.
// Key format: "vrf|address_family|prefix" (e.g., "default|ipv4_unicast|10.0.0.0/8")
type BGPGlobalsAFAggEntry struct {
	AsSet       string `json:"as_set,omitempty"`
	SummaryOnly string `json:"summary_only,omitempty"`
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

// ASPathSetEntry represents an AS-path regex filter.
// Key format: set_name (e.g., "ASPATH_FILTER")
type ASPathSetEntry struct {
	ASPathMember string `json:"as_path_member,omitempty"` // Regex pattern
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
	// Get all keys
	keys, err := c.client.Keys(c.ctx, "*").Result()
	if err != nil {
		return nil, err
	}

	db := &ConfigDB{
		DeviceMetadata:        make(map[string]map[string]string),
		Port:                  make(map[string]PortEntry),
		VLAN:                  make(map[string]VLANEntry),
		VLANMember:            make(map[string]VLANMemberEntry),
		VLANInterface:         make(map[string]map[string]string),
		Interface:             make(map[string]InterfaceEntry),
		PortChannel:           make(map[string]PortChannelEntry),
		PortChannelMember:     make(map[string]map[string]string),
		LoopbackInterface:     make(map[string]map[string]string),
		VRF:                   make(map[string]VRFEntry),
		VXLANTunnel:           make(map[string]VXLANTunnelEntry),
		VXLANTunnelMap:        make(map[string]VXLANMapEntry),
		VXLANEVPNNVO:          make(map[string]EVPNNVOEntry),
		SuppressVLANNeigh:     make(map[string]map[string]string),
		SAG:                   make(map[string]map[string]string),
		SAGGlobal:             make(map[string]map[string]string),
		BGPNeighbor:           make(map[string]BGPNeighborEntry),
		BGPNeighborAF:         make(map[string]BGPNeighborAFEntry),
		BGPGlobals:            make(map[string]BGPGlobalsEntry),
		BGPGlobalsAF:          make(map[string]BGPGlobalsAFEntry),
		BGPEVPNVNI:            make(map[string]BGPEVPNVNIEntry),
		RouteTable:            make(map[string]StaticRouteEntry),
		ACLTable:              make(map[string]ACLTableEntry),
		ACLRule:               make(map[string]ACLRuleEntry),
		ACLTableType:          make(map[string]ACLTableTypeEntry),
		Scheduler:             make(map[string]SchedulerEntry),
		Queue:                 make(map[string]QueueEntry),
		WREDProfile:           make(map[string]WREDProfileEntry),
		PortQoSMap:            make(map[string]PortQoSMapEntry),
		DSCPToTCMap:           make(map[string]map[string]string),
		TCToQueueMap:          make(map[string]map[string]string),
		Policer:               make(map[string]PolicerEntry),
		RouteRedistribute:     make(map[string]RouteRedistributeEntry),
		RouteMap:              make(map[string]RouteMapEntry),
		BGPPeerGroup:          make(map[string]BGPPeerGroupEntry),
		BGPPeerGroupAF:        make(map[string]BGPPeerGroupAFEntry),
		BGPGlobalsAFNet:       make(map[string]BGPGlobalsAFNetEntry),
		BGPGlobalsAFAgg:       make(map[string]BGPGlobalsAFAggEntry),
		PrefixSet:             make(map[string]PrefixSetEntry),
		CommunitySet:          make(map[string]CommunitySetEntry),
		ASPathSet:             make(map[string]ASPathSetEntry),
		NewtronServiceBinding: make(map[string]ServiceBindingEntry),
	}

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
	switch table {
	case "PORT":
		db.Port[entry] = PortEntry{
			AdminStatus: vals["admin_status"],
			Alias:       vals["alias"],
			Description: vals["description"],
			FEC:         vals["fec"],
			Index:       vals["index"],
			Lanes:       vals["lanes"],
			MTU:         vals["mtu"],
			Speed:       vals["speed"],
			Autoneg:     vals["autoneg"],
		}
	case "VLAN":
		db.VLAN[entry] = VLANEntry{
			VLANID:      vals["vlanid"],
			Description: vals["description"],
			MTU:         vals["mtu"],
			AdminStatus: vals["admin_status"],
		}
	case "VLAN_MEMBER":
		db.VLANMember[entry] = VLANMemberEntry{
			TaggingMode: vals["tagging_mode"],
		}
	case "PORTCHANNEL":
		db.PortChannel[entry] = PortChannelEntry{
			AdminStatus: vals["admin_status"],
			MTU:         vals["mtu"],
			MinLinks:    vals["min_links"],
			Fallback:    vals["fallback"],
			FastRate:    vals["fast_rate"],
		}
	case "VRF":
		db.VRF[entry] = VRFEntry{
			VNI: vals["vni"],
		}
	case "VXLAN_TUNNEL":
		db.VXLANTunnel[entry] = VXLANTunnelEntry{
			SrcIP: vals["src_ip"],
		}
	case "VXLAN_TUNNEL_MAP":
		db.VXLANTunnelMap[entry] = VXLANMapEntry{
			VLAN: vals["vlan"],
			VRF:  vals["vrf"],
			VNI:  vals["vni"],
		}
	case "ACL_TABLE":
		db.ACLTable[entry] = ACLTableEntry{
			PolicyDesc: vals["policy_desc"],
			Type:       vals["type"],
			Stage:      vals["stage"],
			Ports:      vals["ports"],
			Services:   vals["services"],
		}
	case "ACL_RULE":
		db.ACLRule[entry] = ACLRuleEntry{
			Priority:       vals["PRIORITY"],
			PacketAction:   vals["PACKET_ACTION"],
			SrcIP:          vals["SRC_IP"],
			DstIP:          vals["DST_IP"],
			IPProtocol:     vals["IP_PROTOCOL"],
			L4SrcPort:      vals["L4_SRC_PORT"],
			L4DstPort:      vals["L4_DST_PORT"],
			L4SrcPortRange: vals["L4_SRC_PORT_RANGE"],
			L4DstPortRange: vals["L4_DST_PORT_RANGE"],
		}
	case "BGP_NEIGHBOR_AF":
		db.BGPNeighborAF[entry] = BGPNeighborAFEntry{
			Activate:             vals["activate"],
			RouteReflectorClient: vals["route_reflector_client"],
			NextHopSelf:          vals["next_hop_self"],
			SoftReconfiguration:  vals["soft_reconfiguration"],
			AllowASIn:            vals["allowas_in"],
			RouteMapIn:           vals["route_map_in"],
			RouteMapOut:          vals["route_map_out"],
			PrefixListIn:         vals["prefix_list_in"],
			PrefixListOut:        vals["prefix_list_out"],
			DefaultOriginate:     vals["default_originate"],
			AddpathTxAll:         vals["addpath_tx_all_paths"],
		}
	case "BGP_GLOBALS":
		db.BGPGlobals[entry] = BGPGlobalsEntry{
			RouterID:           vals["router_id"],
			LocalASN:           vals["local_asn"],
			ConfedID:           vals["confed_id"],
			ConfedPeers:        vals["confed_peers"],
			GracefulRestart:    vals["graceful_restart"],
			LoadBalanceMPRelax: vals["load_balance_mp_relax"],
			RRClusterID:        vals["rr_cluster_id"],
			EBGPRequiresPolicy: vals["ebgp_requires_policy"],
			DefaultIPv4Unicast: vals["default_ipv4_unicast"],
			LogNeighborChanges: vals["log_neighbor_changes"],
			SuppressFIBPending: vals["suppress_fib_pending"],
		}
	case "BGP_GLOBALS_AF":
		db.BGPGlobalsAF[entry] = BGPGlobalsAFEntry{
			AdvertiseAllVNI:    vals["advertise-all-vni"],
			AdvertiseDefaultGW: vals["advertise-default-gw"],
			AdvertiseSVIIP:     vals["advertise-svi-ip"],
			AdvertiseIPv4:      vals["advertise_ipv4_unicast"],
			AdvertiseIPv6:      vals["advertise_ipv6_unicast"],
			RD:                 vals["rd"],
			RTImport:           vals["rt_import"],
			RTExport:           vals["rt_export"],
			RTImportEVPN:       vals["route_target_import_evpn"],
			RTExportEVPN:       vals["route_target_export_evpn"],
			MaxEBGPPaths:       vals["max_ebgp_paths"],
			MaxIBGPPaths:       vals["max_ibgp_paths"],
		}
	case "BGP_EVPN_VNI":
		db.BGPEVPNVNI[entry] = BGPEVPNVNIEntry{
			RD:                 vals["rd"],
			RTImport:           vals["route_target_import"],
			RTExport:           vals["route_target_export"],
			AdvertiseDefaultGW: vals["advertise_default_gw"],
		}
	case "NEWTRON_SERVICE_BINDING":
		db.NewtronServiceBinding[entry] = ServiceBindingEntry{
			ServiceName: vals["service_name"],
			IPAddress:   vals["ip_address"],
			VRFName:     vals["vrf_name"],
			IPVPN:       vals["ipvpn"],
			MACVPN:      vals["macvpn"],
			IngressACL:  vals["ingress_acl"],
			EgressACL:   vals["egress_acl"],
			AppliedAt:   vals["applied_at"],
			AppliedBy:   vals["applied_by"],
		}
	case "INTERFACE":
		db.Interface[entry] = InterfaceEntry{
			VRFName:     vals["vrf_name"],
			NATZone:     vals["nat_zone"],
			ProxyArp:    vals["proxy_arp"],
			MPLSEnabled: vals["mpls"],
		}
	case "VLAN_INTERFACE":
		if db.VLANInterface[entry] == nil {
			db.VLANInterface[entry] = make(map[string]string)
		}
		for k, v := range vals {
			db.VLANInterface[entry][k] = v
		}
	case "BGP_NEIGHBOR":
		db.BGPNeighbor[entry] = BGPNeighborEntry{
			LocalAddr:     vals["local_addr"],
			Name:          vals["name"],
			ASN:           vals["asn"],
			HoldTime:      vals["holdtime"],
			KeepaliveTime: vals["keepalive"],
			AdminStatus:   vals["admin_status"],
			PeerGroup:     vals["peer_group"],
			EBGPMultihop:  vals["ebgp_multihop"],
			Password:      vals["password"],
		}
	case "LOOPBACK_INTERFACE":
		if db.LoopbackInterface[entry] == nil {
			db.LoopbackInterface[entry] = make(map[string]string)
		}
		for k, v := range vals {
			db.LoopbackInterface[entry][k] = v
		}
	case "SUPPRESS_VLAN_NEIGH":
		if db.SuppressVLANNeigh[entry] == nil {
			db.SuppressVLANNeigh[entry] = make(map[string]string)
		}
		for k, v := range vals {
			db.SuppressVLANNeigh[entry][k] = v
		}
	case "VXLAN_EVPN_NVO":
		db.VXLANEVPNNVO[entry] = EVPNNVOEntry{
			SourceVTEP: vals["source_vtep"],
		}
	case "PORTCHANNEL_MEMBER":
		if db.PortChannelMember[entry] == nil {
			db.PortChannelMember[entry] = make(map[string]string)
		}
		for k, v := range vals {
			db.PortChannelMember[entry][k] = v
		}
	case "DEVICE_METADATA":
		if db.DeviceMetadata[entry] == nil {
			db.DeviceMetadata[entry] = make(map[string]string)
		}
		for k, v := range vals {
			db.DeviceMetadata[entry][k] = v
		}

	// v3: frrcfgd tables
	case "ROUTE_REDISTRIBUTE":
		db.RouteRedistribute[entry] = RouteRedistributeEntry{
			RouteMap: vals["route_map"],
			Metric:   vals["metric"],
		}
	case "ROUTE_MAP":
		db.RouteMap[entry] = RouteMapEntry{
			Action:         vals["route_operation"],
			MatchPrefixSet: vals["match_prefix_set"],
			MatchCommunity: vals["match_community"],
			MatchASPath:    vals["match_as_path"],
			MatchNextHop:   vals["match_next_hop"],
			SetLocalPref:   vals["set_local_pref"],
			SetCommunity:   vals["set_community"],
			SetMED:         vals["set_med"],
			SetNextHop:     vals["set_next_hop"],
		}
	case "BGP_PEER_GROUP":
		db.BGPPeerGroup[entry] = BGPPeerGroupEntry{
			ASN:         vals["asn"],
			LocalAddr:   vals["local_addr"],
			AdminStatus: vals["admin_status"],
			HoldTime:    vals["holdtime"],
			Keepalive:   vals["keepalive"],
			Password:    vals["password"],
		}
	case "BGP_PEER_GROUP_AF":
		db.BGPPeerGroupAF[entry] = BGPPeerGroupAFEntry{
			Activate:             vals["activate"],
			RouteReflectorClient: vals["route_reflector_client"],
			NextHopSelf:          vals["next_hop_self"],
			RouteMapIn:           vals["route_map_in"],
			RouteMapOut:          vals["route_map_out"],
			SoftReconfiguration:  vals["soft_reconfiguration"],
		}
	case "BGP_GLOBALS_AF_NETWORK":
		db.BGPGlobalsAFNet[entry] = BGPGlobalsAFNetEntry{
			Policy: vals["policy"],
		}
	case "BGP_GLOBALS_AF_AGGREGATE_ADDR":
		db.BGPGlobalsAFAgg[entry] = BGPGlobalsAFAggEntry{
			AsSet:       vals["as_set"],
			SummaryOnly: vals["summary_only"],
		}
	case "PREFIX_SET":
		db.PrefixSet[entry] = PrefixSetEntry{
			IPPrefix:     vals["ip_prefix"],
			Action:       vals["action"],
			MaskLenRange: vals["masklength_range"],
		}
	case "COMMUNITY_SET":
		db.CommunitySet[entry] = CommunitySetEntry{
			SetType:         vals["set_type"],
			MatchAction:     vals["match_action"],
			CommunityMember: vals["community_member"],
		}
	case "AS_PATH_SET":
		db.ASPathSet[entry] = ASPathSetEntry{
			ASPathMember: vals["as_path_member"],
		}
	}
}

// Set writes a table entry. If fields is empty, a "NULL":"NULL" sentinel is
// written so the Redis key is actually created (SONiC convention for
// field-less entries like PORTCHANNEL_MEMBER or INTERFACE IP keys).
func (c *ConfigDBClient) Set(table, key string, fields map[string]string) error {
	redisKey := fmt.Sprintf("%s|%s", table, key)
	if len(fields) == 0 {
		return c.client.HSet(c.ctx, redisKey, "NULL", "NULL").Err()
	}
	for k, v := range fields {
		if err := c.client.HSet(c.ctx, redisKey, k, v).Err(); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes a table entry
func (c *ConfigDBClient) Delete(table, key string) error {
	redisKey := fmt.Sprintf("%s|%s", table, key)
	return c.client.Del(c.ctx, redisKey).Err()
}

// DeleteField removes a specific field from an entry
func (c *ConfigDBClient) DeleteField(table, key, field string) error {
	redisKey := fmt.Sprintf("%s|%s", table, key)
	return c.client.HDel(c.ctx, redisKey, field).Err()
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
	return c.client.Keys(c.ctx, pattern).Result()
}

// Exists checks if a key exists
func (c *ConfigDBClient) Exists(table, key string) (bool, error) {
	redisKey := fmt.Sprintf("%s|%s", table, key)
	n, err := c.client.Exists(c.ctx, redisKey).Result()
	return n > 0, err
}

// ToJSON exports config_db as JSON
func (db *ConfigDB) ToJSON() ([]byte, error) {
	return json.MarshalIndent(db, "", "  ")
}
