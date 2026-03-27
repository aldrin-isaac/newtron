package sonic

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/newtron-network/newtron/pkg/util"
)

// FieldType identifies the expected type of a CONFIG_DB field value.
type FieldType int

const (
	FieldString FieldType = iota // free-form string
	FieldInt                     // integer (parsed via strconv.Atoi)
	FieldEnum                    // one of a fixed set of values
	FieldIP                      // IPv4 address
	FieldCIDR                    // IPv4 CIDR notation (e.g., "10.0.0.0/24")
	FieldMAC                     // MAC address
	FieldBool                    // "true" or "false"
)

// FieldConstraint defines validation rules for a single CONFIG_DB field.
type FieldConstraint struct {
	Type       FieldType // value type
	Range      *[2]int   // min, max (for FieldInt)
	Pattern    string    // regex (optional, for FieldString)
	Enum       []string  // allowed values (for FieldEnum)
	AllowEmpty bool      // allow "" (SONiC convention for clearing a field)
}

// Check validates a field value against this constraint.
func (fc FieldConstraint) Check(value string) error {
	if value == "" && fc.AllowEmpty {
		return nil
	}
	switch fc.Type {
	case FieldInt:
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("must be integer, got %q", value)
		}
		if fc.Range != nil && (n < fc.Range[0] || n > fc.Range[1]) {
			return fmt.Errorf("must be %d–%d, got %d", fc.Range[0], fc.Range[1], n)
		}
	case FieldEnum:
		found := false
		for _, v := range fc.Enum {
			if v == value {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("must be one of %v, got %q", fc.Enum, value)
		}
	case FieldBool:
		if value != "true" && value != "false" {
			return fmt.Errorf("must be \"true\" or \"false\", got %q", value)
		}
	case FieldIP:
		if !util.IsValidIPv4(value) {
			return fmt.Errorf("must be valid IPv4 address, got %q", value)
		}
	case FieldCIDR:
		if !util.IsValidIPv4CIDR(value) {
			return fmt.Errorf("must be valid CIDR, got %q", value)
		}
	case FieldMAC:
		if _, err := net.ParseMAC(value); err != nil {
			return fmt.Errorf("must be valid MAC address, got %q", value)
		}
	case FieldString:
		// no constraint beyond being present
	}
	if fc.Pattern != "" {
		if matched, _ := regexp.MatchString(fc.Pattern, value); !matched {
			return fmt.Errorf("must match pattern %s, got %q", fc.Pattern, value)
		}
	}
	return nil
}

// TableSchema defines the key format and field constraints for a CONFIG_DB table.
type TableSchema struct {
	KeyPattern string                     // regex for key format validation
	Fields     map[string]FieldConstraint // field name → constraint
	AllowExtra bool                       // if true, unknown fields are accepted without validation
}

// ValidateEntry checks a key and field map against this schema.
// Returns all violations via ValidationBuilder.
func (ts TableSchema) ValidateEntry(table, key string, fields map[string]string) error {
	var vb util.ValidationBuilder

	if ts.KeyPattern != "" {
		if matched, _ := regexp.MatchString(ts.KeyPattern, key); !matched {
			vb.AddErrorf("%s|%s: invalid key format (must match %s)", table, key, ts.KeyPattern)
		}
	}

	for field, value := range fields {
		fc, ok := ts.Fields[field]
		if !ok {
			if !ts.AllowExtra {
				vb.AddErrorf("%s|%s: unknown field %q", table, key, field)
			}
			continue
		}
		if err := fc.Check(value); err != nil {
			vb.AddErrorf("%s|%s field %q: %s", table, key, field, err)
		}
	}

	return vb.Build()
}

// intRange is a convenience for creating [2]int range pointers.
func intRange(min, max int) *[2]int {
	return &[2]int{min, max}
}

// Schema maps CONFIG_DB table names to their schemas.
// Derived from SONiC YANG models (see device/sonic/yang/constraints.md)
// and cross-checked against newtron ops file usage.
// Only tables that newtron writes are included.
//
// YANG source: sonic-buildimage/src/sonic-yang-models/yang-models/
// Last cross-checked: 2026-03-07
var Schema = map[string]TableSchema{

	// ========================================================================
	// VLAN tables (vlan_ops.go)
	// YANG: sonic-vlan.yang
	// ========================================================================

	"VLAN": {
		// YANG: Vlan(409[0-5]|40[0-8][0-9]|[1-3][0-9]{3}|[1-9][0-9]{2}|[1-9][0-9]|[2-9])
		// We use 2-4094 (IEEE 802.1Q; VLAN 1 reserved, 4095 reserved)
		KeyPattern: `^Vlan([2-9]|[1-9]\d{1,2}|[1-3]\d{3}|40[0-8]\d|409[0-4])$`,
		Fields: map[string]FieldConstraint{
			"vlanid":      {Type: FieldInt, Range: intRange(2, 4094)},    // YANG: uint16, 2..4094
			"description": {Type: FieldString},                           // YANG: length 1..255
			"mtu":         {Type: FieldInt, Range: intRange(1, 9216)},    // YANG: uint16, 1..9216
		},
	},

	"VLAN_MEMBER": {
		KeyPattern: `^Vlan\d+\|.+$`,
		Fields: map[string]FieldConstraint{
			"tagging_mode": {Type: FieldEnum, Enum: []string{"tagged", "untagged"}}, // YANG: mandatory
		},
	},

	"VLAN_INTERFACE": {
		// YANG: sonic-vlan.yang — VLAN_INTERFACE_LIST + VLAN_INTERFACE_IPPREFIX_LIST
		// Key: "VlanN" (base) or "VlanN|IP/mask" (IP sub-entry)
		KeyPattern: `^Vlan\d+(\|.+)?$`,
		Fields: map[string]FieldConstraint{
			"vrf_name": {Type: FieldString}, // YANG: leafref to VRF
		},
	},

	"SAG_GLOBAL": {
		// No YANG model — SONiC community extension
		KeyPattern: `^IPv4$`,
		Fields: map[string]FieldConstraint{
			"gwmac": {Type: FieldMAC},
		},
	},

	"SUPPRESS_VLAN_NEIGH": {
		// Not in sonic-vxlan.yang — SONiC community extension
		KeyPattern: `^Vlan\d+$`,
		Fields: map[string]FieldConstraint{
			"suppress": {Type: FieldEnum, Enum: []string{"on", "off"}},
		},
	},

	// ========================================================================
	// VRF tables (vrf_ops.go)
	// YANG: sonic-vrf.yang
	// ========================================================================

	"VRF": {
		// YANG: pattern Vrf[a-zA-Z0-9_-]+, but SONiC CLI and vrfmgrd accept
		// arbitrary names (e.g., CUSTOMER, Vrf_IRB). Allow any non-empty
		// alphanumeric/underscore/hyphen string, plus "default".
		KeyPattern: `^[a-zA-Z][a-zA-Z0-9_-]*$`,
		Fields: map[string]FieldConstraint{
			"vni": {Type: FieldInt, Range: intRange(0, 16777215), AllowEmpty: true}, // YANG: uint32 0..16777215; "" clears L3VNI
		},
	},

	"STATIC_ROUTE": {
		// YANG: sonic-static-route.yang — key: vrf_name|prefix
		// Key: "prefix" or "VRF|prefix"
		KeyPattern: `^(([a-zA-Z][a-zA-Z0-9_-]*)\|)?\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}/\d{1,2}$`,
		Fields: map[string]FieldConstraint{
			"nexthop":  {Type: FieldIP},
			"distance": {Type: FieldInt, Range: intRange(0, 255)}, // YANG: 0..255
		},
	},

	// ========================================================================
	// BGP tables (bgp_ops.go)
	// YANG: sonic-bgp-global.yang, sonic-bgp-neighbor.yang, sonic-bgp-common.yang
	// ========================================================================

	"BGP_GLOBALS": {
		// YANG: key vrf_name — union("default" | leafref VRF)
		KeyPattern: `^[a-zA-Z][a-zA-Z0-9_-]*$`,
		Fields: map[string]FieldConstraint{
			"local_asn":              {Type: FieldInt, Range: intRange(1, 4294967295)}, // YANG: uint32 1..4294967295
			"router_id":             {Type: FieldIP},                                   // YANG: inet:ipv4-address
			"ebgp_requires_policy":  {Type: FieldBool},
			"suppress_fib_pending":  {Type: FieldBool},
			"log_neighbor_changes":  {Type: FieldBool},                                 // YANG: log_nbr_state_changes
		},
	},

	"BGP_NEIGHBOR": {
		// YANG: key vrf_name|neighbor
		KeyPattern: `^[^|]+\|.+$`,
		Fields: map[string]FieldConstraint{
			"asn":              {Type: FieldInt, Range: intRange(1, 4294967295)}, // YANG: uint32, refined >=1 in cmn-neigh
			"admin_status":     {Type: FieldEnum, Enum: []string{"up", "down"}},
			"local_addr":       {Type: FieldIP},                                  // YANG: union (IP, port, LAG, loopback, Vlan)
			"name":             {Type: FieldString},
			"ebgp_multihop":    {Type: FieldString},                              // YANG: boolean; newtron writes "true"/TTL
			"peer_group_name":  {Type: FieldString},                              // YANG: leafref → BGP_PEER_GROUP
		},
	},

	"BGP_NEIGHBOR_AF": {
		// YANG: key vrf_name|neighbor|afi_safi
		KeyPattern: `^[^|]+\|[^|]+\|(ipv4_unicast|ipv6_unicast|l2vpn_evpn)$`,
		Fields: map[string]FieldConstraint{
			"admin_status":       {Type: FieldBool},
			"rrclient":           {Type: FieldBool},     // YANG: boolean
			"nhself":             {Type: FieldBool},      // YANG: boolean
			"nexthop_unchanged":  {Type: FieldBool},      // YANG: unchanged_nexthop (boolean); newtron uses nexthop_unchanged
			"route_map_in":       {Type: FieldString},    // YANG: leafref list, max 1
			"route_map_out":      {Type: FieldString},    // YANG: leafref list, max 1
		},
	},

	"BGP_PEER_GROUP": {
		// YANG: sonic-bgp-peergroup, key vrf_name|peer_group_name
		KeyPattern: `^[^|]+\|[A-Z0-9_]+$`,
		Fields: map[string]FieldConstraint{
			"admin_status":  {Type: FieldEnum, Enum: []string{"up", "down"}},
			"local_addr":    {Type: FieldIP},     // YANG: union (IP, port, LAG, loopback, Vlan)
			"ebgp_multihop": {Type: FieldString},  // YANG: boolean; newtron writes "true"/TTL
		},
	},

	"BGP_PEER_GROUP_AF": {
		// YANG: sonic-bgp-peergroup, key vrf_name|peer_group_name|afi_safi
		KeyPattern: `^[^|]+\|[^|]+\|(ipv4_unicast|ipv6_unicast|l2vpn_evpn)$`,
		Fields: map[string]FieldConstraint{
			"admin_status":       {Type: FieldBool},
			"route_map_in":       {Type: FieldString},
			"route_map_out":      {Type: FieldString},
			"nexthop_unchanged":  {Type: FieldBool}, // YANG: boolean; used by EVPN peer group for eBGP overlay
		},
	},

	"BGP_GLOBALS_AF": {
		// YANG: key vrf_name|afi_safi
		KeyPattern: `^[^|]+\|(ipv4_unicast|ipv6_unicast|l2vpn_evpn)$`,
		Fields: map[string]FieldConstraint{
			"redistribute_connected":  {Type: FieldBool},
			"redistribute_static":     {Type: FieldBool},
			"advertise-ipv4-unicast":  {Type: FieldBool}, // YANG: boolean (hyphenated field name)
			"advertise-all-vni":       {Type: FieldBool}, // YANG: boolean
		},
	},

	"BGP_GLOBALS_EVPN_RT": {
		// Key: "VRF|L2VPN_EVPN|route-target"
		KeyPattern: `^[^|]+\|L2VPN_EVPN\|.+$`,
		Fields: map[string]FieldConstraint{
			"route-target-type": {Type: FieldEnum, Enum: []string{"both", "import", "export"}},
		},
	},

	"ROUTE_REDISTRIBUTE": {
		// Key: "VRF|protocol|bgp|AF"
		KeyPattern: `^[^|]+\|(connected|static)\|bgp\|(ipv4|ipv6)$`,
		Fields:     map[string]FieldConstraint{},
	},

	"DEVICE_METADATA": {
		// YANG: sonic-device_metadata.yang — many fields; listing those newtron writes
		KeyPattern: `^localhost$`,
		Fields: map[string]FieldConstraint{
			"bgp_asn":                       {Type: FieldInt, Range: intRange(1, 4294967295), AllowEmpty: true}, // YANG: inet:as-number; "" clears ASN
			"type":                          {Type: FieldString},                               // YANG: length 1..255
			"docker_routing_config_mode":    {Type: FieldString},                               // YANG: pattern separated|unified|split|split-unified
			"frr_mgmt_framework_config":     {Type: FieldBool},                                // YANG: boolean, default false
			"hostname":                      {Type: FieldString},                               // YANG: hostname type
			"mac":                           {Type: FieldMAC},                                  // YANG: yang:mac-address
			"hwsku":                         {Type: FieldString},                               // YANG: stypes:hwsku
		},
	},

	// ========================================================================
	// EVPN tables (evpn_ops.go)
	// YANG: sonic-vxlan.yang
	// ========================================================================

	"VXLAN_TUNNEL": {
		// YANG: max-elements 2
		KeyPattern: `^vtep\d+$`,
		Fields: map[string]FieldConstraint{
			"src_ip": {Type: FieldIP},
		},
	},

	"VXLAN_EVPN_NVO": {
		// YANG: max-elements 1; source_vtep is mandatory
		KeyPattern: `^nvo\d+$`,
		Fields: map[string]FieldConstraint{
			"source_vtep": {Type: FieldString}, // YANG: leafref to VXLAN_TUNNEL, mandatory
		},
	},

	"VXLAN_TUNNEL_MAP": {
		// YANG: key name|mapname; vlan and vni are mandatory
		KeyPattern: `^vtep\d+\|VNI\d+_.+$`,
		Fields: map[string]FieldConstraint{
			"vlan": {Type: FieldString, Pattern: `^Vlan\d+$`}, // YANG: Vlan pattern, mandatory
			"vrf":  {Type: FieldString},
			"vni":  {Type: FieldInt, Range: intRange(1, 16777215)}, // YANG: vnid_type, mandatory
		},
	},

	"BGP_EVPN_VNI": {
		// No YANG model — SONiC community extension for EVPN VNI config
		KeyPattern: `^[^|]+\|\d+$`,
		Fields:     map[string]FieldConstraint{},
	},

	// ========================================================================
	// Interface tables (interface_ops.go, baseline_ops.go)
	// YANG: sonic-interface.yang, sonic-loopback-interface.yang, sonic-port.yang
	// ========================================================================

	"INTERFACE": {
		// YANG: sonic-interface.yang — INTERFACE_LIST + INTERFACE_IPPREFIX_LIST
		// Key: "IntfName" or "IntfName|IP/mask"
		KeyPattern: `^(Ethernet\d+|PortChannel\d+|Vlan\d+|Loopback\d+)(\|.+)?$`,
		Fields: map[string]FieldConstraint{
			"vrf_name":     {Type: FieldString},                                                                     // YANG: leafref to VRF
			"mtu":          {Type: FieldInt, Range: intRange(68, 9216)},                                              // from PORT YANG
			"speed":        {Type: FieldEnum, Enum: []string{"1G", "10G", "25G", "40G", "50G", "100G", "200G", "400G"}}, // newtron convention
			"admin_status": {Type: FieldEnum, Enum: []string{"up", "down"}},
			"description":  {Type: FieldString},
		},
	},

	"LOOPBACK_INTERFACE": {
		// YANG: sonic-loopback-interface.yang
		KeyPattern: `^Loopback\d+(\|.+)?$`,
		Fields:     map[string]FieldConstraint{},
	},

	"PORT": {
		// YANG: sonic-port.yang — speed is uint32 1..1600000 (Mbps); newtron writes string enum
		KeyPattern: `^Ethernet\d+$`,
		Fields: map[string]FieldConstraint{
			"admin_status": {Type: FieldEnum, Enum: []string{"up", "down"}},                                            // YANG: default "down"
			"mtu":          {Type: FieldInt, Range: intRange(68, 9216)},                                                 // YANG: uint16 68..9216
			"speed":        {Type: FieldEnum, Enum: []string{"1G", "10G", "25G", "40G", "50G", "100G", "200G", "400G"}}, // YANG: uint32; newtron uses string
			"description":  {Type: FieldString},                                                                         // YANG: length 0..255
		},
	},

	// ========================================================================
	// PortChannel tables (portchannel_ops.go)
	// YANG: sonic-portchannel.yang
	// ========================================================================

	"PORTCHANNEL": {
		// YANG: pattern PortChannel[0-9]{1,4}
		KeyPattern: `^PortChannel\d{1,4}$`,
		Fields: map[string]FieldConstraint{
			"admin_status": {Type: FieldEnum, Enum: []string{"up", "down"}}, // YANG: mandatory
			"mtu":          {Type: FieldInt, Range: intRange(1, 9216)},       // YANG: uint16 1..9216
			"min_links":    {Type: FieldInt, Range: intRange(1, 1024)},       // YANG: uint16 1..1024
			"fallback":     {Type: FieldBool},                                // YANG: boolean_type
			"fast_rate":    {Type: FieldBool},                                // YANG: boolean_type
		},
	},

	"PORTCHANNEL_MEMBER": {
		// Key: "PortChannelN|EthernetN"
		KeyPattern: `^PortChannel\d+\|.+$`,
		Fields:     map[string]FieldConstraint{},
	},

	// ========================================================================
	// ACL tables (acl_ops.go)
	// No dedicated YANG file found — constraints from SONiC documentation
	// ========================================================================

	"ACL_TABLE": {
		KeyPattern: `^[a-zA-Z][a-zA-Z0-9_-]*$`,
		Fields: map[string]FieldConstraint{
			"type":        {Type: FieldEnum, Enum: []string{"L3", "L3V6", "MIRROR", "MIRRORV6"}},
			"stage":       {Type: FieldEnum, Enum: []string{"ingress", "egress"}},
			"ports":       {Type: FieldString},
			"policy_desc": {Type: FieldString},
		},
	},

	"ACL_RULE": {
		// Key: "ACLName|RuleName"
		KeyPattern: `^[^|]+\|.+$`,
		Fields: map[string]FieldConstraint{
			"PRIORITY":          {Type: FieldInt, Range: intRange(0, 65535)},
			"PACKET_ACTION":     {Type: FieldEnum, Enum: []string{"FORWARD", "DROP", "REDIRECT"}},
			"SRC_IP":            {Type: FieldCIDR},
			"DST_IP":            {Type: FieldCIDR},
			"IP_PROTOCOL":       {Type: FieldInt, Range: intRange(0, 255)},
			"L4_SRC_PORT":       {Type: FieldString, Pattern: `^\d+(-\d+)?$`},
			"L4_DST_PORT":       {Type: FieldString, Pattern: `^\d+(-\d+)?$`},
			"L4_SRC_PORT_RANGE": {Type: FieldString, Pattern: `^\d+-\d+$`},
			"L4_DST_PORT_RANGE": {Type: FieldString, Pattern: `^\d+-\d+$`},
			"TCP_FLAGS":         {Type: FieldString, Pattern: `^0x[0-9a-fA-F]+/0x[0-9a-fA-F]+$`},
			"ICMP_TYPE":         {Type: FieldInt, Range: intRange(0, 255)},
			"ICMP_CODE":         {Type: FieldInt, Range: intRange(0, 255)},
			"ETHER_TYPE":        {Type: FieldString, Pattern: `^(0x[0-9a-fA-F]+|\d+)$`},
			"DSCP":              {Type: FieldInt, Range: intRange(0, 63)},
			"TC":                {Type: FieldInt, Range: intRange(0, 7)},
			"IN_PORTS":          {Type: FieldString},
			"REDIRECT_PORT":     {Type: FieldString},
		},
	},

	// ========================================================================
	// QoS tables (qos_ops.go)
	// YANG: sonic-dscp-tc-map.yang, sonic-tc-queue-map.yang, sonic-scheduler.yang,
	//       sonic-wred-profile.yang, sonic-queue.yang, sonic-port-qos-map.yang
	// ========================================================================

	"PORT_QOS_MAP": {
		KeyPattern: `^(Ethernet|PortChannel)\d+$`,
		Fields: map[string]FieldConstraint{
			"dscp_to_tc_map":  {Type: FieldString},
			"tc_to_queue_map": {Type: FieldString},
		},
	},

	"QUEUE": {
		// Key: "IntfName|QueueID"
		KeyPattern: `^(Ethernet|PortChannel)\d+\|[0-7]$`,
		Fields: map[string]FieldConstraint{
			"scheduler":    {Type: FieldString},
			"wred_profile": {Type: FieldString},
		},
	},

	"DSCP_TO_TC_MAP": {
		Fields: dscpToTCMapFields(),
	},

	"TC_TO_QUEUE_MAP": {
		Fields: tcToQueueMapFields(),
	},

	"SCHEDULER": {
		// YANG: sonic-scheduler.yang
		Fields: map[string]FieldConstraint{
			"type":   {Type: FieldEnum, Enum: []string{"DWRR", "WRR", "STRICT"}}, // YANG: enum {DWRR, WRR, STRICT}
			"weight": {Type: FieldInt, Range: intRange(1, 100)},                   // YANG: uint8 1..100
		},
	},

	"WRED_PROFILE": {
		// YANG: sonic-wred-profile.yang — thresholds are uint64 (bytes), drop probability 0..100
		Fields: map[string]FieldConstraint{
			"ecn":                     {Type: FieldEnum, Enum: []string{"ecn_none", "ecn_green", "ecn_yellow", "ecn_red", "ecn_green_yellow", "ecn_green_red", "ecn_yellow_red", "ecn_all"}},
			"green_max_threshold":     {Type: FieldInt},                          // YANG: uint64 (bytes)
			"green_min_threshold":     {Type: FieldInt},                          // YANG: uint64 (bytes)
			"green_drop_probability":  {Type: FieldInt, Range: intRange(0, 100)}, // YANG: uint64 0..100
			"yellow_max_threshold":    {Type: FieldInt},                          // YANG: uint64 (bytes)
			"yellow_min_threshold":    {Type: FieldInt},                          // YANG: uint64 (bytes)
			"yellow_drop_probability": {Type: FieldInt, Range: intRange(0, 100)},
			"red_max_threshold":       {Type: FieldInt},                          // YANG: uint64 (bytes)
			"red_min_threshold":       {Type: FieldInt},                          // YANG: uint64 (bytes)
			"red_drop_probability":    {Type: FieldInt, Range: intRange(0, 100)},
		},
	},

	// ========================================================================
	// Newtron settings (per-device operational tuning)
	// No YANG model — newtron-owned table
	// ========================================================================

	"NEWTRON_SETTINGS": {
		// Key: "global" (singleton per device)
		KeyPattern: `^global$`,
		Fields: map[string]FieldConstraint{
			"max_history": {Type: FieldInt, Range: intRange(0, 100)},
		},
	},

	// ========================================================================
	// Newtron intent table (unified intent model §39)
	// No YANG model — newtron-owned table
	// Replaced both NEWTRON_SERVICE_BINDING and per-device crash-recovery intent
	// ========================================================================

	"NEWTRON_INTENT": {
		// Key: resource identifier — device name, interface name, VRF name,
		// composite keys like "CUSTOMER|10.0.0.0/8|10.10.1.0" for static routes.
		// Format varies by operation; AllowExtra permits operation-specific params.
		KeyPattern: `^[a-zA-Z0-9][a-zA-Z0-9_.|/-]*$`,
		AllowExtra: true, // params vary by operation — no fixed field set
		Fields: map[string]FieldConstraint{
			// Identity fields — present on every intent record
			"state": {Type: FieldEnum, Enum: []string{"actuated"}},
			"operation": {Type: FieldEnum, Enum: []string{
				OpSetupDevice, OpCreateVRF, OpBindIPVPN, OpCreateVLAN,
				OpBindMACVPN, OpCreateACL, OpAddBGPEVPNPeer,
				OpCreatePortChannel, OpConfigureIRB, OpAddStaticRoute,
				OpSetPortProperty, OpConfigureInterface, OpAddBGPPeer,
				OpApplyService, OpBindACL, OpApplyQoS,
				OpAddACLRule, OpAddPortChannelMember, OpInterfaceInit,
			}},
			// DAG metadata — structural dependencies between intent records
			"_parents":  {Type: FieldString, AllowEmpty: true},
			"_children": {Type: FieldString, AllowEmpty: true},
		},
	},

	"NEWTRON_HISTORY": {
		// Key: device|sequence (e.g., "leaf1|42"), max 10 entries per device
		KeyPattern: `^[a-zA-Z][a-zA-Z0-9_-]*\|\d+$`,
		Fields: map[string]FieldConstraint{
			"holder":     {Type: FieldString},
			"timestamp":  {Type: FieldString}, // RFC3339
			"operations": {Type: FieldString}, // JSON array
		},
	},

	// ========================================================================
	// Service tables (service_ops.go)
	// YANG: sonic-route-map.yang, sonic-routing-policy-sets.yang
	// ========================================================================

	"ROUTE_MAP": {
		// YANG: sonic-route-map.yang — key name|stmt_name (uint16 1..65535)
		KeyPattern: `^[^|]+\|\d+$`,
		Fields: map[string]FieldConstraint{
			"route_operation":  {Type: FieldEnum, Enum: []string{"permit", "deny"}}, // YANG: routing-policy-action-type
			"match_prefix_set": {Type: FieldString},                                  // YANG: leafref PREFIX_SET
			"match_community":  {Type: FieldString},                                  // YANG: leafref COMMUNITY_SET
			"set_local_pref":   {Type: FieldInt, Range: intRange(0, 4294967295)},     // YANG: uint32
			"set_community":    {Type: FieldString},
			"set_med":          {Type: FieldInt, Range: intRange(0, 4294967295)}, // YANG: uint32
		},
	},

	"PREFIX_SET": {
		// YANG: sonic-routing-policy-sets.yang — PREFIX_SET_LIST has nested prefix entries
		// newtron uses "name|sequence" keys with ip_prefix + action fields
		KeyPattern: `^[^|]+\|\d+$`,
		Fields: map[string]FieldConstraint{
			"ip_prefix": {Type: FieldCIDR},
			"action":    {Type: FieldEnum, Enum: []string{"permit", "deny"}},
		},
	},

	"COMMUNITY_SET": {
		// YANG: sonic-routing-policy-sets.yang — set_type is STANDARD/EXPANDED (uppercase)
		// newtron writes lowercase (standard/extended) — this matches SONiC CLI behavior
		KeyPattern: `^[A-Z0-9_]+$`, // newtron normalized name
		Fields: map[string]FieldConstraint{
			"set_type":         {Type: FieldEnum, Enum: []string{"standard", "extended"}},
			"match_action":     {Type: FieldEnum, Enum: []string{"any", "all"}},
			"community_member": {Type: FieldString},
		},
	},
}

// dscpToTCMapFields generates field constraints for DSCP_TO_TC_MAP.
// Fields are "0" through "63", each mapping to TC 0-7.
// YANG: sonic-dscp-tc-map.yang — key is DSCP value as string.
func dscpToTCMapFields() map[string]FieldConstraint {
	fields := make(map[string]FieldConstraint, 64)
	for i := 0; i < 64; i++ {
		fields[fmt.Sprintf("%d", i)] = FieldConstraint{
			Type:  FieldInt,
			Range: intRange(0, 7),
		}
	}
	return fields
}

// tcToQueueMapFields generates field constraints for TC_TO_QUEUE_MAP.
// Fields are "0" through "7", each mapping to queue 0-7.
// YANG: sonic-tc-queue-map.yang — key is TC value as string.
func tcToQueueMapFields() map[string]FieldConstraint {
	fields := make(map[string]FieldConstraint, 8)
	for i := 0; i < 8; i++ {
		fields[fmt.Sprintf("%d", i)] = FieldConstraint{
			Type:  FieldInt,
			Range: intRange(0, 7),
		}
	}
	return fields
}

// ValidateChange checks a single ConfigChange against the schema.
// Delete operations validate key format only. Add/Modify validate key + fields.
// Returns nil if the table is not in the schema (unknown tables are rejected
// by ValidateChangeSet, not here).
func ValidateChange(c ConfigChange) error {
	schema, ok := Schema[c.Table]
	if !ok {
		return nil
	}

	if c.Type == ChangeTypeDelete {
		// Deletes only need key format validation
		if schema.KeyPattern != "" {
			if matched, _ := regexp.MatchString(schema.KeyPattern, c.Key); !matched {
				return fmt.Errorf("%s|%s: invalid key format (must match %s)", c.Table, c.Key, schema.KeyPattern)
			}
		}
		return nil
	}

	return schema.ValidateEntry(c.Table, c.Key, c.Fields)
}

// ValidateChanges checks a slice of ConfigChanges against the schema.
// Returns all violations as a single ValidationError, or nil if valid.
// Unknown tables cause a validation error. Unknown fields cause a validation error.
// Empty field maps on Add/Modify are allowed (key-only entries like INTERFACE|Ethernet0).
func ValidateChanges(changes []ConfigChange) error {
	var vb util.ValidationBuilder

	for _, c := range changes {
		if c.Type == ChangeTypeDelete {
			// Validate key format for deletes
			if schema, ok := Schema[c.Table]; ok && schema.KeyPattern != "" {
				if matched, _ := regexp.MatchString(schema.KeyPattern, c.Key); !matched {
					vb.AddErrorf("%s|%s: invalid key format (must match %s)", c.Table, c.Key, schema.KeyPattern)
				}
			}
			continue
		}

		schema, ok := Schema[c.Table]
		if !ok {
			vb.AddErrorf("%s|%s: unknown table (no schema defined)", c.Table, c.Key)
			continue
		}

		// Key format validation
		if schema.KeyPattern != "" {
			if matched, _ := regexp.MatchString(schema.KeyPattern, c.Key); !matched {
				vb.AddErrorf("%s|%s: invalid key format (must match %s)", c.Table, c.Key, schema.KeyPattern)
			}
		}

		// Field validation
		for field, value := range c.Fields {
			fc, ok := schema.Fields[field]
			if !ok {
				if !schema.AllowExtra {
					vb.AddErrorf("%s|%s: unknown field %q", c.Table, c.Key, field)
				}
				continue
			}
			if err := fc.Check(value); err != nil {
				vb.AddErrorf("%s|%s field %q: %s", c.Table, c.Key, field, err)
			}
		}
	}

	return vb.Build()
}

// compiledPatterns caches compiled regexps for key patterns.
// Built at init time to avoid repeated compilation.
var compiledPatterns map[string]*regexp.Regexp

func init() {
	compiledPatterns = make(map[string]*regexp.Regexp, len(Schema))
	for table, schema := range Schema {
		if schema.KeyPattern != "" {
			compiledPatterns[table] = regexp.MustCompile(schema.KeyPattern)
		}
	}
	// Also compile field-level patterns
	for _, schema := range Schema {
		for _, fc := range schema.Fields {
			if fc.Pattern != "" {
				// Pre-validate that patterns compile (panic at init if invalid)
				regexp.MustCompile(fc.Pattern)
			}
		}
	}
}

// IsKnownTable returns true if the table has a schema entry.
func IsKnownTable(table string) bool {
	_, ok := Schema[table]
	return ok
}

// KnownTables returns all table names with schema entries, sorted.
func KnownTables() []string {
	tables := make([]string, 0, len(Schema))
	for t := range Schema {
		tables = append(tables, t)
	}
	// Simple sort
	for i := 0; i < len(tables); i++ {
		for j := i + 1; j < len(tables); j++ {
			if strings.Compare(tables[i], tables[j]) > 0 {
				tables[i], tables[j] = tables[j], tables[i]
			}
		}
	}
	return tables
}
