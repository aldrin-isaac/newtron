# CONFIG_DB Discovery and Verification Guide

This guide documents the SONiC Redis database architecture, CONFIG_DB table
schemas, and the verification methodology used by newtron for managing SONiC
devices. It is the primary reference for anyone writing integration tests,
debugging configuration issues, or extending newtron's device management
capabilities.

---

## 1. Redis Database Architecture in SONiC

SONiC stores all switch state in a set of Redis databases running inside the
`database` container. Each database serves a distinct purpose:

| DB Index | Name         | Purpose                                              |
|----------|--------------|------------------------------------------------------|
| 0        | APPL_DB      | Application state written by daemons (orchagent input)|
| 1        | ASIC_DB      | ASIC programming state (SAI objects from syncd)      |
| 2        | COUNTERS_DB  | Port counters, flow counters, queue statistics       |
| 4        | CONFIG_DB    | **Switch configuration -- the primary database for newtron** |
| 6        | STATE_DB     | Operational state populated by kernel and daemons    |

### Key Format

All SONiC Redis keys use a **pipe separator** between the table name and the
entry key:

```
TABLE|entry_key
```

For entries with compound keys (e.g., an IP address on an interface), the pipe
separator is used again:

```
TABLE|parent_key|child_key
```

Examples:

```
PORT|Ethernet0
VLAN|Vlan700
INTERFACE|Ethernet4|10.0.0.1/31
VLAN_MEMBER|Vlan700|Ethernet8
BGP_NEIGHBOR_AF|10.0.0.1|l2vpn_evpn
```

### Values

All values are **Redis hashes** (dictionaries of field:value string pairs).
There are no nested structures; every field value is a flat string.

```bash
redis-cli -n 4 HGETALL 'PORT|Ethernet0'
# Returns:
# "speed" "100000"
# "mtu" "9100"
# "admin_status" "up"
# "lanes" "1,2,3,4"
```

### Empty Entries (NULL Sentinel)

Some tables use **presence-based semantics**: the mere existence of the key
carries meaning (e.g., "this IP is assigned to this interface"). These entries
still require at least one field in the Redis hash, so SONiC uses a sentinel:

```
"NULL": "NULL"
```

Example -- assigning 10.0.0.1/31 to Ethernet4:

```bash
redis-cli -n 4 HSET 'INTERFACE|Ethernet4|10.0.0.1/31' NULL NULL
redis-cli -n 4 HGETALL 'INTERFACE|Ethernet4|10.0.0.1/31'
# "NULL" "NULL"
```

The presence of the key `INTERFACE|Ethernet4|10.0.0.1/31` is what tells
orchagent to program the IP address. The `NULL:NULL` field is just a
placeholder to satisfy the Redis hash requirement.

---

## 2. CONFIG_DB Table Reference

This section documents every CONFIG_DB table that newtron interacts with.
For each table: purpose, key format, common fields, and example Redis commands.

---

### Core Tables

#### DEVICE_METADATA|localhost

Global device identity and platform information. There is exactly one entry
with key `localhost`.

| Field      | Description                        | Example Value          |
|------------|------------------------------------|------------------------|
| hostname   | Device hostname                    | `leaf1`                |
| hwsku      | Hardware SKU identifier            | `Force10-S6000`        |
| platform   | Platform string                    | `x86_64-kvm_x86_64-r0`|
| type       | Device role                        | `LeafRouter`           |
| bgp_asn    | BGP autonomous system number       | `65001`                |
| mac        | System MAC address                 | `52:54:00:aa:bb:01`    |

```bash
# Read device metadata
redis-cli -n 4 HGETALL 'DEVICE_METADATA|localhost'

# Get just the hostname
redis-cli -n 4 HGET 'DEVICE_METADATA|localhost' hostname

# Get the BGP ASN
redis-cli -n 4 HGET 'DEVICE_METADATA|localhost' bgp_asn
```

---

#### PORT|EthernetN

Physical port configuration. One entry per front-panel port.

**Key format:** `PORT|Ethernet<number>` (e.g., `PORT|Ethernet0`, `PORT|Ethernet48`)

| Field        | Description                    | Example Value        |
|--------------|--------------------------------|----------------------|
| speed        | Link speed in Mbps             | `100000` (100G)      |
| mtu          | Maximum transmission unit      | `9100`               |
| admin_status | Administrative state           | `up` or `down`       |
| description  | Human-readable label           | `to-spine1:Ethernet0`|
| lanes        | SerDes lane mapping            | `1,2,3,4`            |
| alias        | Platform-specific port name    | `fortyGigE0/0`       |
| fec          | Forward error correction mode  | `rs` or `none`       |

```bash
# List all configured ports
redis-cli -n 4 KEYS 'PORT|*'

# Read full port config
redis-cli -n 4 HGETALL 'PORT|Ethernet0'

# Change MTU
redis-cli -n 4 HSET 'PORT|Ethernet0' mtu 9216

# Shut down a port
redis-cli -n 4 HSET 'PORT|Ethernet0' admin_status down

# Count total ports
redis-cli -n 4 KEYS 'PORT|*' | wc -l
```

---

#### LOOPBACK_INTERFACE|Loopback0

Loopback interface definition and IP assignment. This uses two levels of keys:

1. `LOOPBACK_INTERFACE|Loopback0` -- creates the loopback interface (may have a `vrf_name` field, or `NULL:NULL`)
2. `LOOPBACK_INTERFACE|Loopback0|<ip>/<mask>` -- assigns an IP address (always `NULL:NULL`)

```bash
# Check loopback exists
redis-cli -n 4 HGETALL 'LOOPBACK_INTERFACE|Loopback0'

# List all loopback IPs
redis-cli -n 4 KEYS 'LOOPBACK_INTERFACE|Loopback0|*'

# Read a specific loopback IP entry
redis-cli -n 4 HGETALL 'LOOPBACK_INTERFACE|Loopback0|10.1.0.1/32'
# "NULL" "NULL"

# Assign a new loopback IP
redis-cli -n 4 HSET 'LOOPBACK_INTERFACE|Loopback0|10.1.0.1/32' NULL NULL
```

---

### L2 Tables

#### VLAN|VlanN

VLAN definition. One entry per VLAN.

**Key format:** `VLAN|Vlan<id>` (e.g., `VLAN|Vlan700`)

| Field        | Description             | Example Value |
|--------------|-------------------------|---------------|
| vlanid       | VLAN ID (numeric string)| `700`         |
| admin_status | Administrative state    | `up`          |
| description  | Human-readable label    | `tenant-A`    |

```bash
# List all VLANs
redis-cli -n 4 KEYS 'VLAN|*' | grep -v MEMBER | grep -v INTERFACE

# Create a VLAN
redis-cli -n 4 HSET 'VLAN|Vlan700' vlanid 700 admin_status up

# Read VLAN config
redis-cli -n 4 HGETALL 'VLAN|Vlan700'

# Delete a VLAN
redis-cli -n 4 DEL 'VLAN|Vlan700'
```

---

#### VLAN_MEMBER|VlanN|EthernetN

Associates a port with a VLAN.

**Key format:** `VLAN_MEMBER|Vlan<id>|Ethernet<num>`

| Field        | Description                              | Example Value |
|--------------|------------------------------------------|---------------|
| tagging_mode | Whether frames are tagged or untagged    | `tagged` or `untagged` |

```bash
# List all members of Vlan700
redis-cli -n 4 KEYS 'VLAN_MEMBER|Vlan700|*'

# Add a tagged member
redis-cli -n 4 HSET 'VLAN_MEMBER|Vlan700|Ethernet8' tagging_mode tagged

# Add an untagged (access) member
redis-cli -n 4 HSET 'VLAN_MEMBER|Vlan700|Ethernet12' tagging_mode untagged

# Check tagging mode
redis-cli -n 4 HGET 'VLAN_MEMBER|Vlan700|Ethernet8' tagging_mode

# Remove port from VLAN
redis-cli -n 4 DEL 'VLAN_MEMBER|Vlan700|Ethernet8'
```

---

#### VLAN_INTERFACE|VlanN

SVI (Switched Virtual Interface) definition. Binds a VLAN to a VRF and
enables L3 routing on the VLAN.

**Key format (interface):** `VLAN_INTERFACE|Vlan<id>`
**Key format (IP address):** `VLAN_INTERFACE|Vlan<id>|<ip>/<mask>`

| Field    | Description            | Example Value   |
|----------|------------------------|-----------------|
| vrf_name | VRF the SVI belongs to | `Vrf_e2e_irb`  |

The IP assignment key uses `NULL:NULL` -- its presence means the IP is
assigned to the SVI:

```bash
# Create SVI bound to a VRF
redis-cli -n 4 HSET 'VLAN_INTERFACE|Vlan700' vrf_name Vrf_e2e_irb

# Assign IP to the SVI
redis-cli -n 4 HSET 'VLAN_INTERFACE|Vlan700|192.168.70.1/24' NULL NULL

# List all SVI IPs for Vlan700
redis-cli -n 4 KEYS 'VLAN_INTERFACE|Vlan700|*'

# Check which VRF a VLAN SVI is in
redis-cli -n 4 HGET 'VLAN_INTERFACE|Vlan700' vrf_name

# Remove IP from SVI
redis-cli -n 4 DEL 'VLAN_INTERFACE|Vlan700|192.168.70.1/24'

# Remove SVI entirely
redis-cli -n 4 DEL 'VLAN_INTERFACE|Vlan700'
```

---

### L3 Tables

#### INTERFACE|EthernetN

L3 routed interface configuration. Follows the same two-level pattern as
VLAN_INTERFACE and LOOPBACK_INTERFACE.

**Key format (interface):** `INTERFACE|Ethernet<num>`
**Key format (IP address):** `INTERFACE|Ethernet<num>|<ip>/<mask>`

| Field    | Description            | Example Value   |
|----------|------------------------|-----------------|
| vrf_name | VRF the interface is in| `Vrf_e2e_irb`  |

```bash
# Bind an interface to a VRF
redis-cli -n 4 HSET 'INTERFACE|Ethernet4' vrf_name Vrf_e2e_irb

# Assign IP
redis-cli -n 4 HSET 'INTERFACE|Ethernet4|10.0.0.1/31' NULL NULL

# List all L3 interfaces
redis-cli -n 4 KEYS 'INTERFACE|*' | grep -v '|.*|' | head -20

# List all IPs on Ethernet4
redis-cli -n 4 KEYS 'INTERFACE|Ethernet4|*'

# Remove IP
redis-cli -n 4 DEL 'INTERFACE|Ethernet4|10.0.0.1/31'
```

---

#### VRF|VrfName

VRF (Virtual Routing and Forwarding) instance definition.

**Key format:** `VRF|<name>` (e.g., `VRF|Vrf_e2e_irb`)

| Field         | Description                         | Example Value |
|---------------|-------------------------------------|---------------|
| vrf_reg_mask  | VRF registration mask (typically empty or absent) | `0x0` |

In practice, newtron creates VRFs with `NULL:NULL` to indicate presence:

```bash
# Create a VRF
redis-cli -n 4 HSET 'VRF|Vrf_e2e_irb' NULL NULL

# List all VRFs
redis-cli -n 4 KEYS 'VRF|*'

# Check if VRF exists
redis-cli -n 4 EXISTS 'VRF|Vrf_e2e_irb'

# Delete a VRF
redis-cli -n 4 DEL 'VRF|Vrf_e2e_irb'
```

---

### LAG Tables

#### PORTCHANNEL|PortChannelN

Link Aggregation Group (LAG / port-channel) definition.

**Key format:** `PORTCHANNEL|PortChannel<num>` (e.g., `PORTCHANNEL|PortChannel1`)

| Field        | Description                        | Example Value |
|--------------|------------------------------------|---------------|
| mtu          | MTU for the LAG                    | `9100`        |
| admin_status | Administrative state               | `up`          |
| min_links    | Minimum active links for LAG to be up | `1`        |

```bash
# Create a port-channel
redis-cli -n 4 HSET 'PORTCHANNEL|PortChannel1' mtu 9100 admin_status up min_links 1

# List all port-channels
redis-cli -n 4 KEYS 'PORTCHANNEL|*' | grep -v MEMBER

# Read port-channel config
redis-cli -n 4 HGETALL 'PORTCHANNEL|PortChannel1'
```

---

#### PORTCHANNEL_MEMBER|PortChannelN|EthernetN

Associates a physical port with a LAG. Uses presence-based semantics.

**Key format:** `PORTCHANNEL_MEMBER|PortChannel<num>|Ethernet<num>`

```bash
# Add member to LAG
redis-cli -n 4 HSET 'PORTCHANNEL_MEMBER|PortChannel1|Ethernet0' NULL NULL

# List members of PortChannel1
redis-cli -n 4 KEYS 'PORTCHANNEL_MEMBER|PortChannel1|*'

# Remove member
redis-cli -n 4 DEL 'PORTCHANNEL_MEMBER|PortChannel1|Ethernet0'
```

---

### EVPN/VXLAN Tables

These tables configure VXLAN overlay networking with EVPN control plane.

#### VXLAN_TUNNEL|vtep1

Defines the VXLAN tunnel endpoint (VTEP).

**Key format:** `VXLAN_TUNNEL|<name>` (conventionally `vtep1`)

| Field  | Description              | Example Value |
|--------|--------------------------|---------------|
| src_ip | VTEP source IP (loopback)| `10.1.0.1`   |

```bash
# Create VTEP
redis-cli -n 4 HSET 'VXLAN_TUNNEL|vtep1' src_ip 10.1.0.1

# Read VTEP config
redis-cli -n 4 HGETALL 'VXLAN_TUNNEL|vtep1'
```

---

#### VXLAN_EVPN_NVO|nvo1

Network Virtualization Overlay (NVO) object. Links EVPN to the VTEP.

**Key format:** `VXLAN_EVPN_NVO|<name>` (conventionally `nvo1`)

| Field       | Description            | Example Value |
|-------------|------------------------|---------------|
| source_vtep | Name of the VTEP entry | `vtep1`       |

```bash
# Create NVO
redis-cli -n 4 HSET 'VXLAN_EVPN_NVO|nvo1' source_vtep vtep1

# Read NVO config
redis-cli -n 4 HGETALL 'VXLAN_EVPN_NVO|nvo1'
```

---

#### VXLAN_TUNNEL_MAP|vtep1|map_VVVVV_VlanNNN

Maps a VLAN to a VNI (VXLAN Network Identifier) on the VTEP.

**Key format:** `VXLAN_TUNNEL_MAP|<vtep>|map_<VNI>_Vlan<id>`

The key name itself encodes both the VNI and the VLAN. The fields mirror
this information:

| Field | Description | Example Value |
|-------|-------------|---------------|
| vlan  | VLAN name   | `Vlan700`     |
| vni   | VNI number  | `70000`       |

```bash
# Create VLAN-to-VNI mapping
redis-cli -n 4 HSET 'VXLAN_TUNNEL_MAP|vtep1|map_70000_Vlan700' vlan Vlan700 vni 70000

# List all VNI mappings
redis-cli -n 4 KEYS 'VXLAN_TUNNEL_MAP|*'

# Read specific mapping
redis-cli -n 4 HGETALL 'VXLAN_TUNNEL_MAP|vtep1|map_70000_Vlan700'

# Delete mapping
redis-cli -n 4 DEL 'VXLAN_TUNNEL_MAP|vtep1|map_70000_Vlan700'
```

---

#### SUPPRESS_VLAN_NEIGH|VlanN

Enables ARP/ND suppression on a VLAN. This tells the switch to respond
to ARP requests locally using information from the EVPN control plane
instead of flooding them.

**Key format:** `SUPPRESS_VLAN_NEIGH|Vlan<id>`

| Field    | Description                | Example Value |
|----------|----------------------------|---------------|
| suppress | Enable ARP/ND suppression  | `on`          |

```bash
# Enable ARP suppression on Vlan700
redis-cli -n 4 HSET 'SUPPRESS_VLAN_NEIGH|Vlan700' suppress on

# Check suppression status
redis-cli -n 4 HGET 'SUPPRESS_VLAN_NEIGH|Vlan700' suppress

# List all VLANs with ARP suppression
redis-cli -n 4 KEYS 'SUPPRESS_VLAN_NEIGH|*'
```

---

### BGP Tables

#### BGP_GLOBALS|default

Global BGP configuration for the default VRF.

**Key format:** `BGP_GLOBALS|default`

| Field                       | Description                          | Example Value |
|-----------------------------|--------------------------------------|---------------|
| local_asn                   | Local autonomous system number       | `65001`       |
| router_id                   | BGP router identifier                | `10.1.0.1`    |
| rr_clnt_to_clnt_reflection | Route-reflector client-to-client     | `true`        |

```bash
# Read BGP globals
redis-cli -n 4 HGETALL 'BGP_GLOBALS|default'

# Get local ASN
redis-cli -n 4 HGET 'BGP_GLOBALS|default' local_asn

# Set router ID
redis-cli -n 4 HSET 'BGP_GLOBALS|default' router_id 10.1.0.1
```

---

#### BGP_NEIGHBOR|ip

Per-neighbor BGP session configuration.

**Key format:** `BGP_NEIGHBOR|<ip>` (e.g., `BGP_NEIGHBOR|10.0.0.1`)

| Field        | Description                      | Example Value   |
|--------------|----------------------------------|-----------------|
| asn          | Remote AS number                 | `65100`         |
| name         | Neighbor description/name        | `spine1`        |
| admin_status | Administrative state             | `true`          |
| local_addr   | Local address for the session    | `10.0.0.0`      |
| keepalive    | Keepalive interval (seconds)     | `3`             |
| holdtime     | Hold timer (seconds)             | `9`             |

```bash
# List all BGP neighbors
redis-cli -n 4 KEYS 'BGP_NEIGHBOR|*' | grep -v '_AF'

# Read neighbor config
redis-cli -n 4 HGETALL 'BGP_NEIGHBOR|10.0.0.1'

# Create a neighbor
redis-cli -n 4 HSET 'BGP_NEIGHBOR|10.0.0.1' asn 65100 name spine1 admin_status true local_addr 10.0.0.0

# Delete a neighbor
redis-cli -n 4 DEL 'BGP_NEIGHBOR|10.0.0.1'
```

---

#### BGP_NEIGHBOR_AF|ip|address_family

Per-neighbor, per-address-family activation. Used to enable EVPN on a
BGP session.

**Key format:** `BGP_NEIGHBOR_AF|<ip>|<af>` (e.g., `BGP_NEIGHBOR_AF|10.0.0.1|l2vpn_evpn`)

| Field        | Description                    | Example Value |
|--------------|--------------------------------|---------------|
| admin_status | Enable this AF on the neighbor | `true`        |

```bash
# Enable L2VPN EVPN on a neighbor
redis-cli -n 4 HSET 'BGP_NEIGHBOR_AF|10.0.0.1|l2vpn_evpn' admin_status true

# List all EVPN-enabled neighbors
redis-cli -n 4 KEYS 'BGP_NEIGHBOR_AF|*|l2vpn_evpn'

# Check if EVPN is enabled for a neighbor
redis-cli -n 4 HGET 'BGP_NEIGHBOR_AF|10.0.0.1|l2vpn_evpn' admin_status
```

---

#### BGP_GLOBALS_AF|default|address_family

Global BGP address-family configuration (multipath settings, etc.).

**Key format:** `BGP_GLOBALS_AF|default|<af>` (e.g., `BGP_GLOBALS_AF|default|ipv4_unicast`)

| Field          | Description                           | Example Value |
|----------------|---------------------------------------|---------------|
| max_ebgp_paths | Maximum ECMP paths for eBGP           | `2`           |
| max_ibgp_paths | Maximum ECMP paths for iBGP           | `2`           |

```bash
# Read IPv4 unicast globals
redis-cli -n 4 HGETALL 'BGP_GLOBALS_AF|default|ipv4_unicast'

# Set ECMP path counts
redis-cli -n 4 HSET 'BGP_GLOBALS_AF|default|ipv4_unicast' max_ebgp_paths 2 max_ibgp_paths 2

# Check L2VPN EVPN globals
redis-cli -n 4 HGETALL 'BGP_GLOBALS_AF|default|l2vpn_evpn'
```

---

### ACL Tables

#### ACL_TABLE|name

Access Control List table definition. Binds an ACL to ports and specifies
its type and direction.

**Key format:** `ACL_TABLE|<name>` (e.g., `ACL_TABLE|INGRESS_FILTER`)

| Field       | Description                          | Example Value              |
|-------------|--------------------------------------|----------------------------|
| type        | ACL type                             | `L3` or `L3V6`            |
| stage       | Processing direction                 | `ingress` or `egress`      |
| policy_desc | Human-readable description           | `Ingress filter for tenant`|
| ports       | Comma-separated list of bound ports  | `Ethernet0,Ethernet4`     |

```bash
# List all ACL tables
redis-cli -n 4 KEYS 'ACL_TABLE|*'

# Create an ACL table
redis-cli -n 4 HSET 'ACL_TABLE|INGRESS_FILTER' type L3 stage ingress policy_desc "Ingress filter" ports "Ethernet0,Ethernet4"

# Read ACL table config
redis-cli -n 4 HGETALL 'ACL_TABLE|INGRESS_FILTER'

# Check which ports an ACL is bound to
redis-cli -n 4 HGET 'ACL_TABLE|INGRESS_FILTER' ports
```

---

#### ACL_RULE|table|rule

Individual ACL rule within a table.

**Key format:** `ACL_RULE|<table_name>|<rule_name>` (e.g., `ACL_RULE|INGRESS_FILTER|RULE_10`)

| Field         | Description                    | Example Value       |
|---------------|--------------------------------|---------------------|
| priority      | Rule priority (higher = first) | `100`               |
| packet_action | What to do with matched traffic| `FORWARD` or `DROP` |
| src_ip        | Source IP match (CIDR)         | `10.0.0.0/8`       |
| dst_ip        | Destination IP match (CIDR)    | `192.168.0.0/16`   |
| ip_protocol   | IP protocol number             | `6` (TCP)          |
| l4_src_port   | L4 source port                 | `80`               |
| l4_dst_port   | L4 destination port            | `443`              |

```bash
# Create an ACL rule
redis-cli -n 4 HSET 'ACL_RULE|INGRESS_FILTER|RULE_10' \
    priority 100 \
    packet_action FORWARD \
    src_ip 10.0.0.0/8 \
    dst_ip 192.168.0.0/16 \
    ip_protocol 6 \
    l4_dst_port 443

# List all rules in a table
redis-cli -n 4 KEYS 'ACL_RULE|INGRESS_FILTER|*'

# Read a specific rule
redis-cli -n 4 HGETALL 'ACL_RULE|INGRESS_FILTER|RULE_10'

# Delete a rule
redis-cli -n 4 DEL 'ACL_RULE|INGRESS_FILTER|RULE_10'
```

---

### Service Tables

#### NEWTRON_SERVICE_BINDING|EthernetN

Newtron-specific table that binds a service definition to a physical port.
This is a custom table used by the newtron orchestrator, not part of
upstream SONiC.

**Key format:** `NEWTRON_SERVICE_BINDING|Ethernet<num>`

| Field       | Description                          | Example Value        |
|-------------|--------------------------------------|----------------------|
| service     | Service template name                | `enterprise-L3`     |
| ip          | IP address for the interface         | `10.100.0.1/24`     |
| vrf         | VRF name                             | `Vrf_e2e_irb`       |
| ipvpn       | IP VPN identifier / route target     | `65000:100`          |
| macvpn      | MAC VPN identifier / route target    | `65000:200`          |
| ingress_acl | Name of ingress ACL table            | `INGRESS_FILTER`     |
| egress_acl  | Name of egress ACL table             | `EGRESS_FILTER`      |

```bash
# List all service bindings
redis-cli -n 4 KEYS 'NEWTRON_SERVICE_BINDING|*'

# Read a binding
redis-cli -n 4 HGETALL 'NEWTRON_SERVICE_BINDING|Ethernet8'

# Create a binding
redis-cli -n 4 HSET 'NEWTRON_SERVICE_BINDING|Ethernet8' \
    service enterprise-L3 \
    ip "10.100.0.1/24" \
    vrf Vrf_e2e_irb \
    ipvpn "65000:100" \
    macvpn "65000:200" \
    ingress_acl INGRESS_FILTER \
    egress_acl EGRESS_FILTER

# Delete a binding
redis-cli -n 4 DEL 'NEWTRON_SERVICE_BINDING|Ethernet8'
```

---

## 3. STATE_DB Table Reference

STATE_DB (DB 6) contains **operational state** populated by SONiC daemons
and the Linux kernel. It reflects what is actually running, not what was
configured.

> **Important:** STATE_DB reliability varies by feature and platform.
> See Section 5 for guidance on when to trust STATE_DB vs CONFIG_DB.

---

#### PORT_TABLE|EthernetN

Operational port state from the kernel and syncd.

**Key format:** `PORT_TABLE|Ethernet<num>` (in STATE_DB, DB 6)

| Field        | Description                        | Example Value |
|--------------|------------------------------------|---------------|
| admin_status | Admin state (mirrors CONFIG_DB)    | `up`          |
| oper_status  | Operational link state             | `up` or `down`|
| speed        | Negotiated speed                   | `100000`      |
| mtu          | Operational MTU                    | `9100`        |

```bash
# Check if a port is operationally up
redis-cli -n 6 HGET 'PORT_TABLE|Ethernet0' oper_status

# Get full operational state
redis-cli -n 6 HGETALL 'PORT_TABLE|Ethernet0'

# List all ports and their oper_status
for port in $(redis-cli -n 6 KEYS 'PORT_TABLE|*'); do
    echo "$port: $(redis-cli -n 6 HGET $port oper_status)"
done
```

---

#### LAG_TABLE|PortChannelN

Operational LAG state.

**Key format:** `LAG_TABLE|PortChannel<num>` (in STATE_DB, DB 6)

| Field          | Description                    | Example Value     |
|----------------|--------------------------------|-------------------|
| oper_status    | LAG operational state          | `up` or `down`    |
| active_members | List of active member ports    | `Ethernet0,Ethernet4` |

```bash
# Check LAG operational status
redis-cli -n 6 HGET 'LAG_TABLE|PortChannel1' oper_status

# See active members
redis-cli -n 6 HGET 'LAG_TABLE|PortChannel1' active_members
```

---

#### LAG_MEMBER_TABLE|PortChannelN|EthernetN

Per-member LACP state.

**Key format:** `LAG_MEMBER_TABLE|PortChannel<num>|Ethernet<num>` (in STATE_DB, DB 6)

| Field  | Description          | Example Value                |
|--------|----------------------|------------------------------|
| status | LACP member state    | `collecting` or `distributing` |

```bash
# Check member LACP state
redis-cli -n 6 HGETALL 'LAG_MEMBER_TABLE|PortChannel1|Ethernet0'
```

---

#### BGP_NEIGHBOR_TABLE|ip

BGP session operational state from FRR (Free Range Routing).

**Key format:** `BGP_NEIGHBOR_TABLE|<ip>` (in STATE_DB, DB 6)

| Field         | Description                    | Example Value   |
|---------------|--------------------------------|-----------------|
| state         | BGP session state              | `Established`, `Active`, `Connect`, `Idle` |
| peerGroupName | BGP peer group (if configured) | `SPINE_PEERS`   |

```bash
# Check if BGP session is established
redis-cli -n 6 HGET 'BGP_NEIGHBOR_TABLE|10.0.0.1' state

# List all BGP neighbor states
redis-cli -n 6 KEYS 'BGP_NEIGHBOR_TABLE|*'

# Get full neighbor operational state
redis-cli -n 6 HGETALL 'BGP_NEIGHBOR_TABLE|10.0.0.1'
```

---

#### VXLAN_TUNNEL_TABLE|ip

VXLAN tunnel operational state.

**Key format:** `VXLAN_TUNNEL_TABLE|<remote_vtep_ip>` (in STATE_DB, DB 6)

| Field | Description        | Example Value |
|-------|--------------------|---------------|
| state | Tunnel state       | `up` or `down`|

```bash
# Check VXLAN tunnel to a remote VTEP
redis-cli -n 6 HGET 'VXLAN_TUNNEL_TABLE|10.1.0.2' state

# List all VXLAN tunnels
redis-cli -n 6 KEYS 'VXLAN_TUNNEL_TABLE|*'
```

---

#### INTERFACE_TABLE|EthernetN

Interface operational state including the operational VRF assignment.

**Key format:** `INTERFACE_TABLE|Ethernet<num>` (in STATE_DB, DB 6)

| Field | Description          | Example Value   |
|-------|----------------------|-----------------|
| vrf   | Operational VRF name | `Vrf_e2e_irb`  |

```bash
# Check which VRF an interface is operationally in
redis-cli -n 6 HGET 'INTERFACE_TABLE|Ethernet4' vrf
```

---

## 4. ASIC_DB (DB 1) Key Patterns

ASIC_DB stores SAI (Switch Abstraction Interface) objects programmed into
the switching ASIC by syncd. These keys confirm that orchagent has
processed the CONFIG_DB changes and pushed them to the hardware.

> **Note:** ASIC_DB keys use OID (Object ID) suffixes that are
> auto-generated. You typically search by type prefix, not exact key.

### Key SAI Object Types

| SAI Object Type                    | Purpose                          |
|------------------------------------|----------------------------------|
| `ASIC_STATE:SAI_OBJECT_TYPE_VLAN`  | VLAN entries in the ASIC         |
| `ASIC_STATE:SAI_OBJECT_TYPE_BRIDGE_PORT` | Bridge port objects         |
| `ASIC_STATE:SAI_OBJECT_TYPE_TUNNEL`| VXLAN tunnel objects             |
| `ASIC_STATE:SAI_OBJECT_TYPE_ROUTER_INTERFACE` | L3 interface objects |
| `ASIC_STATE:SAI_OBJECT_TYPE_ROUTE_ENTRY` | Programmed routes          |
| `ASIC_STATE:SAI_OBJECT_TYPE_NEXT_HOP` | Next-hop entries              |

### Querying ASIC_DB

```bash
# List all VLAN objects in ASIC
redis-cli -n 1 KEYS 'ASIC_STATE:SAI_OBJECT_TYPE_VLAN:*'

# Read a specific VLAN ASIC entry (use a key from the previous command)
redis-cli -n 1 HGETALL 'ASIC_STATE:SAI_OBJECT_TYPE_VLAN:oid:0x2600000000001a'

# Count VLAN entries
redis-cli -n 1 KEYS 'ASIC_STATE:SAI_OBJECT_TYPE_VLAN:*' | wc -l

# List all tunnel objects (VXLAN)
redis-cli -n 1 KEYS 'ASIC_STATE:SAI_OBJECT_TYPE_TUNNEL:*'

# List all router interfaces
redis-cli -n 1 KEYS 'ASIC_STATE:SAI_OBJECT_TYPE_ROUTER_INTERFACE:*'

# Check all bridge ports
redis-cli -n 1 KEYS 'ASIC_STATE:SAI_OBJECT_TYPE_BRIDGE_PORT:*'

# Search for a specific VLAN ID in ASIC entries
for key in $(redis-cli -n 1 KEYS 'ASIC_STATE:SAI_OBJECT_TYPE_VLAN:*'); do
    vlanid=$(redis-cli -n 1 HGET "$key" SAI_VLAN_ATTR_VLAN_ID)
    if [ "$vlanid" = "700" ]; then
        echo "Found VLAN 700: $key"
        redis-cli -n 1 HGETALL "$key"
    fi
done
```

### Why ASIC_DB Matters

ASIC_DB is useful for verifying **convergence without a data plane**. When
running tests on a virtual switch (VS), you cannot send real traffic, but
you can verify that orchagent processed the configuration by checking
ASIC_DB:

- VLAN created in CONFIG_DB -> SAI_OBJECT_TYPE_VLAN appears in ASIC_DB
- Route programmed by FRR -> SAI_OBJECT_TYPE_ROUTE_ENTRY appears in ASIC_DB
- VXLAN tunnel configured -> SAI_OBJECT_TYPE_TUNNEL appears in ASIC_DB

This is as close to "data-plane verification" as you can get on a VS
platform without actual traffic.

---

## 5. Discovery Methodology

### How to Find What Table a Feature Uses

When implementing a new feature or debugging an existing one, use this
process to discover the relevant CONFIG_DB tables:

**Step 1: List all tables in CONFIG_DB**

```bash
# Get unique table names
redis-cli -n 4 KEYS '*' | sed 's/|.*//' | sort -u
```

**Step 2: Search for tables related to your feature**

```bash
# Find VLAN-related keys
redis-cli -n 4 KEYS '*VLAN*'

# Find BGP-related keys
redis-cli -n 4 KEYS '*BGP*'

# Find VXLAN-related keys
redis-cli -n 4 KEYS '*VXLAN*' '*TUNNEL*' '*NVO*'

# Find interface-related keys
redis-cli -n 4 KEYS '*INTERFACE*'
```

**Step 3: Examine table contents**

```bash
# Pick a key and inspect its fields
redis-cli -n 4 HGETALL 'VLAN|Vlan700'
```

**Step 4: Cross-reference with source code**

Look at the orchagent source code in the SONiC repos, or the newtron source
code, for table name constants:

```bash
# In the newtron codebase
grep -r 'CONFIG_DB\|HSET\|HGETALL\|"VLAN"\|"PORT"\|"INTERFACE"' pkg/
```

**Step 5: Check SONiC schema files**

The SONiC Yang models and JSON schema files define the valid fields for
each table. These are the authoritative reference for field names and
allowed values.

---

### How to Verify a Configuration Was Applied

Follow this four-step verification ladder, from most basic to most
thorough:

#### Step 1: Write to CONFIG_DB

Use newtron or direct redis-cli to write the configuration:

```bash
redis-cli -n 4 HSET 'VLAN|Vlan700' vlanid 700 admin_status up
```

#### Step 2: Read back from CONFIG_DB (verify write)

Immediately verify the write succeeded:

```bash
redis-cli -n 4 HGETALL 'VLAN|Vlan700'
# Expect: "vlanid" "700" "admin_status" "up"
```

This confirms the data was persisted. If this fails, check key format
(pipe separator, correct table name, correct case).

#### Step 3: Check ASIC_DB for convergence

Wait for orchagent to process the change (typically < 1 second) and verify
it reached the ASIC:

```bash
# Wait briefly, then check
sleep 2
redis-cli -n 1 KEYS 'ASIC_STATE:SAI_OBJECT_TYPE_VLAN:*'
```

If the VLAN count increased, orchagent processed the CONFIG_DB entry. This
confirms the configuration was not just written, but was also **accepted**
by the SONiC stack.

#### Step 4: Check STATE_DB for operational state

For features backed by FRR (BGP), check the operational state:

```bash
redis-cli -n 6 HGET 'BGP_NEIGHBOR_TABLE|10.0.0.1' state
# Expect: "Established"
```

> **Caveat:** STATE_DB is only reliable for FRR-backed features on VS.
> See the trust matrix below.

---

### CONFIG_DB vs STATE_DB: Which to Trust

Not all STATE_DB entries are reliable on all platforms, especially on the
Virtual Switch (VS) used for testing. Use this trust matrix:

| Feature            | CONFIG_DB | STATE_DB    | ASIC_DB     | Notes                              |
|--------------------|-----------|-------------|-------------|------------------------------------|
| Port admin_status  | Trust     | Trust       | Trust       | Consistent everywhere              |
| Port MTU           | Trust     | **Unreliable on VS** | Trust | VS may not reflect MTU changes in STATE_DB |
| BGP session state  | N/A (config only) | **Trust** | N/A   | FRR-backed, reliably populated     |
| VLAN existence     | Trust     | Partial     | Trust       | ASIC_DB is the best convergence check |
| VXLAN tunnel state | Trust     | **Unreliable on VS** | Trust | VS may not report tunnel state correctly |
| ARP suppression    | Trust     | **Unreliable on VS** | Trust | VS may not reflect suppress state  |
| VRF existence      | Trust     | Partial     | Trust       | Check CONFIG_DB for configuration  |
| ACL rules          | Trust     | N/A         | Trust       | No STATE_DB representation         |

**General rule of thumb:**

- **Always trust CONFIG_DB** for verifying that the desired configuration
  was written.
- **Trust STATE_DB for BGP** -- it is backed by FRR and reliably reflects
  session state.
- **Trust STATE_DB for port admin_status** -- kernel-backed, reliable.
- **Do NOT trust STATE_DB for MTU, VXLAN, or ARP suppression on VS** --
  these may show stale or incorrect values.
- **Trust ASIC_DB for convergence** -- it confirms orchagent processed the
  config, but does not guarantee data-plane forwarding.

---

## 6. Common Redis Commands

### Exploration Commands

```bash
# List all table names in CONFIG_DB
redis-cli -n 4 KEYS '*' | sed 's/|.*//' | sort -u

# Count entries per table (useful for understanding DB size)
redis-cli -n 4 KEYS '*' | sed 's/|.*//' | sort | uniq -c | sort -rn

# List all keys in a specific table
redis-cli -n 4 KEYS 'VLAN|*'

# Count keys in a table
redis-cli -n 4 KEYS 'VLAN|*' | wc -l
```

### Read Commands

```bash
# Read all fields in an entry
redis-cli -n 4 HGETALL 'VLAN|Vlan700'

# Read a specific field
redis-cli -n 4 HGET 'VLAN|Vlan700' vlanid

# Check if a key exists (returns 1 or 0)
redis-cli -n 4 EXISTS 'VRF|Vrf_e2e_irb'

# Get all field names (without values)
redis-cli -n 4 HKEYS 'PORT|Ethernet0'

# Get all field values (without names)
redis-cli -n 4 HVALS 'PORT|Ethernet0'

# Get the number of fields in a hash
redis-cli -n 4 HLEN 'PORT|Ethernet0'
```

### Write Commands

```bash
# Set one or more fields (creates key if it does not exist)
redis-cli -n 4 HSET 'VLAN|Vlan999' vlanid 999 admin_status up

# Set a single field
redis-cli -n 4 HSET 'PORT|Ethernet0' admin_status down

# Set only if key does not already exist
redis-cli -n 4 HSETNX 'VLAN|Vlan999' vlanid 999

# Create a presence-only entry
redis-cli -n 4 HSET 'INTERFACE|Ethernet4|10.0.0.1/31' NULL NULL
```

### Delete Commands

```bash
# Delete an entire key
redis-cli -n 4 DEL 'VLAN|Vlan999'

# Delete a specific field from a hash
redis-cli -n 4 HDEL 'PORT|Ethernet0' description

# Delete multiple keys matching a pattern (use with caution!)
redis-cli -n 4 KEYS 'VLAN_MEMBER|Vlan999|*' | xargs -r redis-cli -n 4 DEL
```

### Monitoring Commands

```bash
# Watch all CONFIG_DB changes in real-time
redis-cli -n 4 MONITOR

# Watch STATE_DB changes (useful for convergence debugging)
redis-cli -n 6 MONITOR

# Watch ASIC_DB changes
redis-cli -n 1 MONITOR

# Get database info
redis-cli -n 4 INFO keyspace
```

### Cross-Database Verification Script

```bash
#!/bin/bash
# verify_vlan.sh -- verify VLAN 700 across all databases
VLAN_ID=700
VLAN_NAME="Vlan${VLAN_ID}"

echo "=== CONFIG_DB ==="
redis-cli -n 4 HGETALL "VLAN|${VLAN_NAME}"

echo ""
echo "=== VLAN Members ==="
redis-cli -n 4 KEYS "VLAN_MEMBER|${VLAN_NAME}|*"

echo ""
echo "=== VLAN Interface (SVI) ==="
redis-cli -n 4 HGETALL "VLAN_INTERFACE|${VLAN_NAME}"
redis-cli -n 4 KEYS "VLAN_INTERFACE|${VLAN_NAME}|*"

echo ""
echo "=== ARP Suppression ==="
redis-cli -n 4 HGETALL "SUPPRESS_VLAN_NEIGH|${VLAN_NAME}"

echo ""
echo "=== VXLAN Tunnel Map ==="
redis-cli -n 4 KEYS "VXLAN_TUNNEL_MAP|*${VLAN_NAME}*"

echo ""
echo "=== ASIC_DB (VLAN objects) ==="
for key in $(redis-cli -n 1 KEYS 'ASIC_STATE:SAI_OBJECT_TYPE_VLAN:*'); do
    vid=$(redis-cli -n 1 HGET "$key" SAI_VLAN_ATTR_VLAN_ID)
    if [ "$vid" = "${VLAN_ID}" ]; then
        echo "Found: $key"
    fi
done
```

---

## 7. Go Test Verification Patterns

Newtron provides test utilities for verifying CONFIG_DB, STATE_DB, and
ASIC_DB state in integration tests. These are the standard patterns.

### CONFIG_DB Assertions

```go
// Verify a CONFIG_DB entry exists with specific field values.
// This is the most common assertion: "did newtron write the correct config?"
testutil.AssertConfigDBEntry(t, "leaf1", "VLAN", "Vlan700", map[string]string{
    "vlanid":       "700",
    "admin_status": "up",
})

// Verify VLAN member with tagging mode
testutil.AssertConfigDBEntry(t, "leaf1", "VLAN_MEMBER", "Vlan700|Ethernet8", map[string]string{
    "tagging_mode": "tagged",
})

// Verify VXLAN tunnel map
testutil.AssertConfigDBEntry(t, "leaf1", "VXLAN_TUNNEL_MAP", "vtep1|map_70000_Vlan700", map[string]string{
    "vlan": "Vlan700",
    "vni":  "70000",
})

// Verify SVI is bound to correct VRF
testutil.AssertConfigDBEntry(t, "leaf1", "VLAN_INTERFACE", "Vlan700", map[string]string{
    "vrf_name": "Vrf_e2e_irb",
})

// Verify a service binding
testutil.AssertConfigDBEntry(t, "leaf1", "NEWTRON_SERVICE_BINDING", "Ethernet8", map[string]string{
    "service":     "enterprise-L3",
    "vrf":         "Vrf_e2e_irb",
    "ingress_acl": "INGRESS_FILTER",
})
```

### Existence and Absence Checks

```go
// Verify an entry exists (don't care about specific field values).
// Useful for presence-based entries like VRFs and IP assignments.
testutil.AssertConfigDBEntryExists(t, "leaf1", "VRF", "Vrf_e2e_irb")

// Verify an IP is assigned (presence-based, NULL:NULL entry)
testutil.AssertConfigDBEntryExists(t, "leaf1", "VLAN_INTERFACE", "Vlan700|192.168.70.1/24")

// Verify a port-channel member exists
testutil.AssertConfigDBEntryExists(t, "leaf1", "PORTCHANNEL_MEMBER", "PortChannel1|Ethernet0")

// Verify an entry was deleted (critical for teardown tests).
// Fails the test if the key still exists in CONFIG_DB.
testutil.AssertConfigDBEntryAbsent(t, "leaf1", "VLAN", "Vlan700")

// Verify all related entries are cleaned up
testutil.AssertConfigDBEntryAbsent(t, "leaf1", "VLAN_MEMBER", "Vlan700|Ethernet8")
testutil.AssertConfigDBEntryAbsent(t, "leaf1", "VLAN_INTERFACE", "Vlan700")
testutil.AssertConfigDBEntryAbsent(t, "leaf1", "VLAN_INTERFACE", "Vlan700|192.168.70.1/24")
testutil.AssertConfigDBEntryAbsent(t, "leaf1", "SUPPRESS_VLAN_NEIGH", "Vlan700")
testutil.AssertConfigDBEntryAbsent(t, "leaf1", "VXLAN_TUNNEL_MAP", "vtep1|map_70000_Vlan700")
```

### STATE_DB Polling (Convergence Waits)

```go
// Poll STATE_DB until a BGP neighbor reaches Established state.
// This blocks until the condition is met or the context deadline expires.
// Use for FRR-backed features where STATE_DB is reliable.
testutil.PollStateDB(ctx, t, "leaf1", "BGP_NEIGHBOR_TABLE", "10.0.0.1", "state", "Established")

// Poll for port operational status
testutil.PollStateDB(ctx, t, "leaf1", "PORT_TABLE", "Ethernet0", "oper_status", "up")

// Poll for LAG operational status
testutil.PollStateDB(ctx, t, "leaf1", "LAG_TABLE", "PortChannel1", "oper_status", "up")

// Poll for VXLAN tunnel state
testutil.PollStateDB(ctx, t, "leaf1", "VXLAN_TUNNEL_TABLE", "10.1.0.2", "state", "up")
```

### ASIC_DB Verification

```go
// Wait for a VLAN to appear in ASIC_DB.
// This confirms orchagent processed the CONFIG_DB write.
testutil.WaitForASICVLAN(ctx, t, "leaf1", 700)

// Wait for a VLAN to be removed from ASIC_DB (teardown verification)
testutil.WaitForASICVLANAbsent(ctx, t, "leaf1", 700)
```

### Direct Redis Access

For cases where the test utilities are insufficient, use the Redis client
directly:

```go
// Get a Redis client for a specific device and database
client := testutil.LabRedisClient(t, "leaf1", 4) // DB 4 = CONFIG_DB

// Read all fields of a hash
val, err := client.HGetAll(ctx, "PORT|Ethernet2").Result()
require.NoError(t, err)
assert.Equal(t, "9100", val["mtu"])
assert.Equal(t, "up", val["admin_status"])

// Check if a key exists
exists, err := client.Exists(ctx, "VRF|Vrf_e2e_irb").Result()
require.NoError(t, err)
assert.Equal(t, int64(1), exists)

// Read a single field
speed, err := client.HGet(ctx, "PORT|Ethernet0", "speed").Result()
require.NoError(t, err)
assert.Equal(t, "100000", speed)

// List keys matching a pattern
keys, err := client.Keys(ctx, "VLAN_MEMBER|Vlan700|*").Result()
require.NoError(t, err)
assert.Len(t, keys, 2) // expect 2 members

// Write a test entry
err = client.HSet(ctx, "VLAN|Vlan999", map[string]interface{}{
    "vlanid":       "999",
    "admin_status": "up",
}).Err()
require.NoError(t, err)

// Clean up after test
defer client.Del(ctx, "VLAN|Vlan999")
```

### Accessing STATE_DB and ASIC_DB Directly

```go
// STATE_DB client (DB 6)
stateClient := testutil.LabRedisClient(t, "leaf1", 6)

bgpState, err := stateClient.HGet(ctx, "BGP_NEIGHBOR_TABLE|10.0.0.1", "state").Result()
require.NoError(t, err)
assert.Equal(t, "Established", bgpState)

// ASIC_DB client (DB 1)
asicClient := testutil.LabRedisClient(t, "leaf1", 1)

vlanKeys, err := asicClient.Keys(ctx, "ASIC_STATE:SAI_OBJECT_TYPE_VLAN:*").Result()
require.NoError(t, err)
t.Logf("Found %d VLAN objects in ASIC_DB", len(vlanKeys))
```

### Full Integration Test Example

```go
func TestCreateVLANService(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
    defer cancel()

    // Step 1: Apply configuration via newtron
    lab := testutil.NewLab(t)
    leaf1 := lab.Device("leaf1")
    err := leaf1.ApplyServiceBinding("Ethernet8", ServiceConfig{
        VLAN:    700,
        VNI:     70000,
        VRF:     "Vrf_e2e_irb",
        SVI_IP:  "192.168.70.1/24",
    })
    require.NoError(t, err)

    // Step 2: Verify CONFIG_DB writes
    testutil.AssertConfigDBEntry(t, "leaf1", "VLAN", "Vlan700", map[string]string{
        "vlanid":       "700",
        "admin_status": "up",
    })
    testutil.AssertConfigDBEntryExists(t, "leaf1", "VRF", "Vrf_e2e_irb")
    testutil.AssertConfigDBEntry(t, "leaf1", "VLAN_INTERFACE", "Vlan700", map[string]string{
        "vrf_name": "Vrf_e2e_irb",
    })
    testutil.AssertConfigDBEntryExists(t, "leaf1", "VLAN_INTERFACE", "Vlan700|192.168.70.1/24")
    testutil.AssertConfigDBEntry(t, "leaf1", "VXLAN_TUNNEL_MAP", "vtep1|map_70000_Vlan700", map[string]string{
        "vlan": "Vlan700",
        "vni":  "70000",
    })

    // Step 3: Wait for ASIC convergence
    testutil.WaitForASICVLAN(ctx, t, "leaf1", 700)

    // Step 4: Verify BGP convergence (if applicable)
    testutil.PollStateDB(ctx, t, "leaf1", "BGP_NEIGHBOR_TABLE", "10.0.0.1", "state", "Established")

    // Step 5: Teardown and verify cleanup
    err = leaf1.RemoveServiceBinding("Ethernet8")
    require.NoError(t, err)

    testutil.AssertConfigDBEntryAbsent(t, "leaf1", "VLAN", "Vlan700")
    testutil.AssertConfigDBEntryAbsent(t, "leaf1", "VXLAN_TUNNEL_MAP", "vtep1|map_70000_Vlan700")
    testutil.WaitForASICVLANAbsent(ctx, t, "leaf1", 700)
}
```

---

## 8. Troubleshooting CONFIG_DB Issues

### Entry Missing After Write

**Symptom:** You wrote to CONFIG_DB but the key does not exist.

**Causes and fixes:**

- **Wrong separator.** SONiC uses `|` (pipe), not `/` or `:`.
  - Wrong: `VLAN/Vlan700`, `VLAN:Vlan700`
  - Correct: `VLAN|Vlan700`

- **Wrong case.** Table names are UPPER_CASE. Key components follow
  SONiC naming conventions (e.g., `Vlan700` not `vlan700`).
  - Wrong: `vlan|vlan700`
  - Correct: `VLAN|Vlan700`

- **Quoting issues in shell.** The pipe character is special in bash.
  Always quote the key:
  - Wrong: `redis-cli -n 4 HGETALL VLAN|Vlan700` (pipe interpreted by shell)
  - Correct: `redis-cli -n 4 HGETALL 'VLAN|Vlan700'`

### Field Values Are Wrong

**Symptom:** Fields exist but have unexpected values.

**Causes and fixes:**

- **All Redis values are strings.** Even numeric values like `vlanid` and
  `mtu` are stored as strings. Do not compare with integers in Go tests:
  - Wrong: `assert.Equal(t, 700, val["vlanid"])`
  - Correct: `assert.Equal(t, "700", val["vlanid"])`

- **Boolean values.** Some fields use `"true"/"false"`, others use
  `"up"/"down"`, and still others use `"on"/"off"`. Check the table
  reference above for the correct format.

### Stale Data in Tests

**Symptom:** Test reads old data from a previous test run.

**Causes and fixes:**

- **Use fresh connections.** Always use `testutil.LabConnectedDevice()`
  or `testutil.LabRedisClient()` for each test function. Do not reuse
  clients across tests.

- **Clean up after tests.** Delete test entries in a `defer` block or
  teardown function. Verify deletion with `AssertConfigDBEntryAbsent`.

- **Parallel test interference.** If tests run in parallel and share
  the same device, use unique VLAN IDs, VRF names, and IP addresses
  to avoid collisions.

### ASIC_DB Not Converging

**Symptom:** CONFIG_DB entry exists but no corresponding ASIC_DB entry
appears.

**Causes and fixes:**

- **VS limitation.** Some complex features (multi-VRF VXLAN, certain
  tunnel configurations) may not fully converge on VS. Check if the
  feature is supported on VS before debugging further.

- **orchagent crash.** Check the orchagent logs:
  ```bash
  docker logs swss 2>&1 | grep -i error | tail -20
  ```

- **Dependency ordering.** Some ASIC objects depend on others. For
  example, a VLAN member requires the VLAN to exist first, and a VXLAN
  tunnel map requires the tunnel to exist. Check that prerequisites
  are in CONFIG_DB.

- **Timeout too short.** On VS, orchagent can be slow under load.
  Increase the context timeout in tests:
  ```go
  ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
  ```

### STATE_DB Contradicts CONFIG_DB

**Symptom:** CONFIG_DB shows one value, STATE_DB shows another.

**This is normal on VS for certain features.** STATE_DB is populated by
daemons that may not fully function on VS. Use the trust matrix in
Section 5 to determine which database to check.

**Recommended approach:**

1. Always verify CONFIG_DB first -- this confirms your write was correct.
2. For BGP: trust STATE_DB (FRR-backed).
3. For port admin_status: trust STATE_DB (kernel-backed).
4. For everything else on VS: verify CONFIG_DB, then optionally check
   ASIC_DB for convergence. Do not rely on STATE_DB.

### Common Error Patterns and Resolutions

| Error | Root Cause | Resolution |
|-------|------------|------------|
| `WRONGTYPE Operation against a key holding the wrong kind of value` | Key exists but is not a hash (e.g., it is a string or list) | Delete the key and recreate as a hash with HSET |
| `(nil)` from HGET | Field does not exist in the hash | Check field name spelling and case |
| `(integer) 0` from EXISTS | Key does not exist | Check key format (table name, separator, entry key) |
| Test timeout waiting for STATE_DB | Feature not backed by STATE_DB on VS | Switch to CONFIG_DB or ASIC_DB verification |
| Duplicate ASIC_DB entries | Stale entries from previous test | Restart swss container or wait for garbage collection |
