# SONiC YANG Constraints Reference

Source: `sonic-buildimage/src/sonic-yang-models/yang-models/` (master branch)
Last fetched: 2026-03-07

Only tables that newtron writes are included. Fields listed are the subset
newtron uses — the YANG models define additional fields not shown here.

## VLAN (sonic-vlan.yang)

**VLAN_LIST**
- Key: `name` — pattern `Vlan(409[0-5]|40[0-8][0-9]|[1-3][0-9]{3}|[1-9][0-9]{2}|[1-9][0-9]|[2-9])`
- `vlanid`: uint16, range 2..4094
- `description`: string, length 1..255
- `mtu`: uint16, range 1..9216
- `admin_status`: stypes:admin_status (up|down)

**VLAN_MEMBER_LIST**
- Key: `name|port`
- `tagging_mode`: stypes:vlan_tagging_mode (tagged|untagged) — **mandatory**

**VLAN_INTERFACE_LIST** (base entry)
- Key: `name`
- `vrf_name`: leafref to VRF
- `nat_zone`: uint8, range 0..3
- `mpls`: enum {enable, disable}
- `proxy_arp`: string pattern "enabled|disabled"
- `grat_arp`: string pattern "enabled|disabled"

**VLAN_INTERFACE_IPPREFIX_LIST** (IP sub-entry)
- Key: `name|ip-prefix`
- `scope`: enum {global, local}
- `family`: ip-family

## VRF (sonic-vrf.yang)

**VRF_LIST**
- Key: `name` — YANG pattern `Vrf[a-zA-Z0-9_-]+`, but SONiC CLI and vrfmgrd
  accept arbitrary names (e.g., `CUSTOMER`, `Vrf_IRB`). newtron schema uses
  `^[a-zA-Z][a-zA-Z0-9_-]*$`.
- `vni`: uint32, range 0..16777215, default 0. AllowEmpty: `""` clears L3VNI
  (vrfmgrd's `stoul("")` throws exception and skips the entry).
- `fallback`: boolean, default false

## BGP_GLOBALS (sonic-bgp-global.yang)

**BGP_GLOBALS_LIST**
- Key: `vrf_name` — union of "default" or leafref to VRF
- `local_asn`: uint32, range 1..4294967295
- `router_id`: inet:ipv4-address
- `log_nbr_state_changes`: boolean
- (many more optional boolean/int fields)

## BGP_GLOBALS_AF (sonic-bgp-global.yang)

**BGP_GLOBALS_AF_LIST**
- Key: `vrf_name|afi_safi`
- `max_ebgp_paths`: uint16, range 1..256
- `max_ibgp_paths`: uint16, range 1..256
- `advertise-all-vni`: boolean
- `advertise-svi-ip`: boolean
- `autort`: enum {rfc8365-compatible}

## BGP_GLOBALS_EVPN_RT

Not defined in `sonic-bgp-global.yang`. This table appears to be a
SONiC-specific extension for EVPN route targets, not covered by the
standard YANG model. Constraints are derived from newtron usage patterns.

## BGP_NEIGHBOR (sonic-bgp-neighbor.yang + sonic-bgp-common.yang)

**BGP_NEIGHBOR_LIST**
- Key: `vrf_name|neighbor`
- Uses `sonic-bgp-cmn` grouping:
  - `asn`: uint32, range 1..4294967295 (refined from 0..max in cmn-neigh)
  - `admin_status`: admin_status type (up|down)
  - `local_addr`: union (IP address, port leafref, LAG leafref, loopback leafref, Vlan pattern)
  - `name`: string (description)
  - `ebgp_multihop`: boolean
  - `ebgp_multihop_ttl`: uint8, range 1..255
  - `local_asn`: uint32, range 1..4294967295
  - `keepalive`: uint16
  - `holdtime`: uint16
  - `conn_retry`: uint16, range 1..65535
  - `passive_mode`: boolean
  - (many more optional fields)

## BGP_NEIGHBOR_AF (sonic-bgp-neighbor.yang + sonic-bgp-common.yang)

**BGP_NEIGHBOR_AF_LIST**
- Key: `vrf_name|neighbor|afi_safi`
- Uses `sonic-bgp-cmn-af` grouping:
  - `admin_status`: admin_status type
  - `rrclient`: boolean
  - `nhself`: boolean
  - `unchanged_nexthop`: boolean (note: YANG uses `unchanged_nexthop`, newtron uses `nexthop_unchanged`)
  - `route_map_in`: leafref list, max 1 element
  - `route_map_out`: leafref list, max 1 element
  - `send_community`: enum {standard, extended, both, large, all, none}
  - `weight`: uint16, range 0..65535
  - `as_override`: boolean
  - (many more optional fields)

## DEVICE_METADATA (sonic-device_metadata.yang)

**DEVICE_METADATA_LIST**
- Key: `localhost` (singleton)
- `bgp_asn`: inet:as-number (uint32). AllowEmpty: `""` clears ASN.
- `type`: string, length 1..255, pattern matching ~24 device types
- `docker_routing_config_mode`: string pattern {separated|unified|split|split-unified}, default "unified"
- `frr_mgmt_framework_config`: boolean, default false
- `hostname`: string
- `mac`: yang:mac-address
- `platform`: string, length 1..255
- (many more optional fields)

## INTERFACE (sonic-interface.yang)

**INTERFACE_LIST** (base entry)
- Key: `name` — leafref to PORT
- `vrf_name`: leafref to VRF
- `nat_zone`: uint8, range 0..3
- `mpls`: enum {enable, disable}
- `mac_addr`: yang:mac-address

**INTERFACE_IPPREFIX_LIST** (IP sub-entry)
- Key: `name|ip-prefix`
- `scope`: enum {global, local}
- `family`: ip-family

## LOOPBACK_INTERFACE (sonic-loopback-interface.yang)

**LOOPBACK_INTERFACE_LIST** (base entry)
- Key: `name`
- `vrf_name`: leafref to VRF
- `nat_zone`: uint8, range 0..3
- `admin_status`: admin_status, default "up"

**LOOPBACK_INTERFACE_IPPREFIX_LIST** (IP sub-entry)
- Key: `name|ip-prefix`
- `scope`: enum {global, local}
- `family`: ip-family

## PORT (sonic-port.yang)

**PORT_LIST**
- Key: `name` — string, length 1..128
- `admin_status`: admin_status (up|down), default "down"
- `mtu`: uint16, range 68..9216
- `speed`: uint32, range 1..1600000 (note: numeric, not string enum)
- `description`: string, length 0..255
- `fec`: string {rs|fc|none|auto}
- `autoneg`: string {on|off}
- `lanes`: string, length 1..128 (conditionally mandatory)
- `alias`: string, length 1..128

## PORTCHANNEL (sonic-portchannel.yang)

**PORTCHANNEL_LIST**
- Key: `name` — pattern `PortChannel[0-9]{1,4}`
- `admin_status`: admin_status — **mandatory**
- `mtu`: uint16, range 1..9216
- `min_links`: uint16, range 1..1024 (note: not 1..64 or 1..8)
- `description`: string, length 1..255
- `fallback`: boolean_type
- `fast_rate`: boolean_type
- `lacp_key`: union {"auto" | uint16 1..65535}
- `mode`: switchport_mode {routed|access|trunk}

**PORTCHANNEL_MEMBER_LIST**
- Key: `name|port`
- Keys only (leafrefs to PORTCHANNEL_LIST and PORT_LIST)

## VXLAN (sonic-vxlan.yang)

**VXLAN_TUNNEL_LIST**
- Key: `name` — string
- Max elements: 2
- `src_ip`: inet:ip-address
- `dst_ip`: inet:ip-address
- `ttl_mode`: string pattern "uniform|pipe"

**VXLAN_TUNNEL_MAP_LIST**
- Key: `name|mapname`
- `vlan`: string — pattern `Vlan(...)` (VLAN range pattern)
- `vni`: vnid_type (uint32, presumably 1..16777215)
- Both `vlan` and `vni` are **mandatory**

**VXLAN_EVPN_NVO_LIST**
- Key: `name` — string
- Max elements: 1
- `source_vtep`: leafref to VXLAN_TUNNEL — **mandatory**

## SUPPRESS_VLAN_NEIGH

Not defined in `sonic-vxlan.yang`. This table is a SONiC-specific extension.
Constraints derived from newtron usage: key is VLAN name, field `suppress` is "on"|"off".

## ACL (sonic-acl.yang)

ACL schemas derived from newtron usage and SONiC documentation.

**ACL_TABLE**
- Key: `name`
- `type`: enum {L3, L3V6, MIRROR, MIRRORV6}
- `stage`: enum {ingress, egress}
- `ports`: string (comma-separated port list)
- `policy_desc`: string

**ACL_RULE**
- Key: `name|rule_name`
- `PRIORITY`: uint16, range 0..65535
- `PACKET_ACTION`: enum {FORWARD, DROP, REDIRECT}
- `SRC_IP`: CIDR
- `DST_IP`: CIDR
- `IP_PROTOCOL`: uint8, range 0..255
- `L4_SRC_PORT`: string, pattern `^\d+(-\d+)?$` (single port or range, e.g., "179" or "3784-3785")
- `L4_DST_PORT`: string, pattern `^\d+(-\d+)?$` (single port or range)
- `L4_SRC_PORT_RANGE`: string, pattern `^\d+-\d+$` (explicit range field)
- `L4_DST_PORT_RANGE`: string, pattern `^\d+-\d+$`
- `TCP_FLAGS`: string, pattern `^0x[0-9a-fA-F]+/0x[0-9a-fA-F]+$` (value/mask)
- `ICMP_TYPE`: uint8, range 0..255
- `ICMP_CODE`: uint8, range 0..255
- `ETHER_TYPE`: string, pattern `^(0x[0-9a-fA-F]+|\d+)$`
- `DSCP`: uint8, range 0..63
- `TC`: uint8, range 0..7
- `IN_PORTS`: string
- `REDIRECT_PORT`: string

## STATIC_ROUTE (sonic-static-route.yang)

**STATIC_ROUTE_LIST**
- Key: `vrf_name|prefix`
- `vrf_name`: union {"default"|"mgmt"|VRF name}
- `prefix`: inet:ip-prefix
- `nexthop`: string (comma-separated for ECMP)
- `distance`: string (comma-separated), values 0..255
- `ifname`: string
- `advertise`: string pattern "true|false" (comma-separated)
- `blackhole`: string pattern "true|false" (comma-separated)
- `nexthop-vrf`: string (comma-separated VRF names)

## ROUTE_MAP (sonic-route-map.yang)

**ROUTE_MAP_LIST**
- Key: `name|stmt_name`
- `stmt_name`: uint16, range 1..65535 (sequence number)
- `route_operation`: routing-policy-action-type (permit|deny)
- `match_prefix_set`: leafref to PREFIX_SET
- `match_community`: leafref to COMMUNITY_SET
- `set_local_pref`: uint32
- `set_med`: uint32
- `set_community_inline`: leaf-list of community strings
- `set_community_ref`: leafref to COMMUNITY_SET
- (many more match/set fields)

## PREFIX_SET (sonic-routing-policy-sets.yang)

**PREFIX_SET_LIST**
- Key: `name`
- `mode`: enum {IPv4, IPv6}, default IPv4

Note: The YANG model defines PREFIX_SET as a named container with a mode.
The per-prefix entries (with sequence numbers, ip_prefix, action) appear
to be in a nested list not directly visible in this extraction. newtron
uses `name|sequence` as the key with `ip_prefix` and `action` fields.

## COMMUNITY_SET (sonic-routing-policy-sets.yang)

**COMMUNITY_SET_LIST**
- Key: `name`
- `set_type`: enum {STANDARD, EXPANDED} (note: uppercase in YANG)
- `match_action`: enum {ANY, ALL} (note: uppercase in YANG)
- `community_member`: leaf-list of strings
- `action`: routing-policy-action-type (permit|deny)

## QoS Tables

### DSCP_TO_TC_MAP (sonic-dscp-tc-map.yang)

**DSCP_TO_TC_MAP_LIST**
- Key: `name` — string, pattern `[a-zA-Z0-9]{1}([-a-zA-Z0-9_]{0,31})`, length 1..32
- Nested list keyed by DSCP value (0..63) → TC value
- Note: YANG uses a keyed-list model, not positional `dscp_0`..`dscp_63` fields.
  The CONFIG_DB representation flattens this to hash fields.

### TC_TO_QUEUE_MAP

Similar pattern to DSCP_TO_TC_MAP. TC values 0..7, queue values 0..7.

### SCHEDULER (sonic-scheduler.yang)

**SCHEDULER_LIST**
- Key: `name`
- `type`: enum {DWRR, WRR, STRICT}, default WRR
- `weight`: uint8, range 1..100, default 1
- `priority`: uint8, range 0..9
- `meter_type`: enum {packets, bytes}, default bytes
- `cir`, `pir`: uint64
- `cbs`, `pbs`: uint32

### WRED_PROFILE (sonic-wred-profile.yang)

**WRED_PROFILE_LIST**
- Key: `name` — pattern `[a-zA-Z0-9]{1}([-a-zA-Z0-9_]{0,31})`, length 1..32
- `green_min_threshold`, `green_max_threshold`: uint64 (bytes)
- `yellow_min_threshold`, `yellow_max_threshold`: uint64 (bytes)
- `red_min_threshold`, `red_max_threshold`: uint64 (bytes)
- `green_drop_probability`: uint64, range 0..100
- `yellow_drop_probability`: uint64, range 0..100
- `red_drop_probability`: uint64, range 0..100
- `ecn`: enum {ecn_none, ecn_green, ecn_yellow, ecn_red, ecn_green_yellow, ecn_green_red, ecn_yellow_red, ecn_all}
- `wred_green_enable`, `wred_yellow_enable`, `wred_red_enable`: boolean

### QUEUE (sonic-queue.yang)

**QUEUE_LIST**
- Key: `ifname|qindex`
- `qindex`: string (single index "3" or range "3-4")
- `scheduler`: leafref to SCHEDULER
- `wred_profile`: leafref to WRED_PROFILE

### PORT_QOS_MAP

Not found in a dedicated YANG file. newtron uses `dscp_to_tc_map` and
`tc_to_queue_map` fields (bracket-ref strings like `[DSCP_TO_TC_MAP|mapName]`).

## ROUTE_REDISTRIBUTE

Not found as a dedicated YANG table. Appears to be defined within
`sonic-bgp-global.yang` or as part of the BGP globals address family
configuration. newtron uses key format `VRF|protocol|bgp|AF`.

## BGP_EVPN_VNI

Not found in the standard YANG models. newtron uses key format `VRF|VNI`
with empty fields (presence-only entry).

## NEWTRON_SERVICE_BINDING

newtron-specific table — no SONiC YANG model. Constraints derived from
newtron usage patterns.
