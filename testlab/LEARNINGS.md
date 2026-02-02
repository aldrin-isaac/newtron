# SONiC Lab Integration Learnings

Reference document for working with SONiC CONFIG_DB, the FRR management
framework, and containerlab-based lab environments.

## General Principles

1. **Balance learning approaches.** Combine trial-and-error with searching
   online (SONiC GitHub issues, HLDs, YANG models) and asking the user.
   Avoid pure trial-and-error when authoritative sources exist.

2. **Mirror the SONiC management framework.** When determining CONFIG_DB
   field names, values, and actuation behavior, always check how the SONiC
   management framework (sonic-mgmt-framework, translib transformers) does
   it. Newtron's CONFIG_DB entries should match what the management CLI
   would produce.

3. **Verify values against SONiC YANG models.** The YANG models in
   `sonic-buildimage/src/sonic-yang-models/yang-models/` are the
   authoritative schema for CONFIG_DB tables. Check field types, allowed
   values, and constraints before writing entries.

4. **Read SONiC HLD/LLD design documents.** Before implementing a feature
   that touches SONiC internals, check for High-Level Design and Low-Level
   Design documents in `sonic-net/SONiC/doc/`. Key documents:
   - `doc/mgmt/SONiC_Design_Doc_Unified_FRR_Mgmt_Interface.md`
   - `doc/BGP/BGP-router-id.md`
   - `doc/ztp/SONiC-config-setup.md`

5. **Verify the full dependency chain.** For CONFIG_DB entries, trace the
   path from Redis → daemon (frrcfgd/intfmgrd/etc.) → running state.
   For running states, verify: CONFIG_DB entry exists → daemon processed
   it → Linux/FRR state reflects it.

6. **Config should be correct before reboot.** Apply all configuration,
   save, then reboot for a clean start. Do not tweak config after reboot;
   only troubleshoot functional failures.

7. **Persist all learnings.** Always update this document when discovering
   new behavior, fixing a bug, or learning a SONiC convention. This
   prevents repeating mistakes after context compaction. Every significant
   finding should be recorded here before moving on.

## CONFIG_DB Initialization

### Boot Sequence

SONiC loads CONFIG_DB in this order at boot:

1. `init_cfg.json` (platform defaults) -- CRM, FEATURE, FLEX_COUNTER_TABLE,
   etc.
2. `config_db.json` (user config) -- merged on top via `sonic-cfggen`
3. `CONFIG_DB_INITIALIZED` flag set to `"1"` (simple Redis string key)

Source: `sonic-buildimage/files/scripts/configdb-load.sh`

### Never FlushDB

**Do not use `FlushDB` on CONFIG_DB.** It destroys platform defaults from
`init_cfg.json` (FEATURE, CRM, FLEX_COUNTER_TABLE, BGP_DEVICE_GLOBAL,
AUTO_TECHSUPPORT, SYSLOG_CONFIG, PASSW_HARDENING, SYSTEM_DEFAULTS, KDUMP,
NTP) and removes `CONFIG_DB_INITIALIZED`.

Instead, selectively delete only the tables being replaced. This preserves
platform defaults loaded at boot.

### CONFIG_DB_INITIALIZED

- Redis data type: simple string (`SET CONFIG_DB_INITIALIZED "1"`)
- Required by: `sonic-cfggen -d` (blocks indefinitely without it),
  `hostcfgd`, and other daemons
- `config save -y` hangs if this flag is missing because it runs
  `sonic-cfggen -d --print-data`

## CONFIG_DB Entry Guidelines

### Platform and HWSKU

The hwsku determines port naming and lane mapping. Using the wrong hwsku
means syncd/orchagent creates ports that don't match CONFIG_DB PORT entries,
so EthernetN interfaces never appear in the Linux kernel.

- Cisco NGDP images default to `cisco-8101-p4-32x100-vs` (sequential:
  Ethernet0, Ethernet1, Ethernet2...)
- Force10-S6000 uses stepped naming (Ethernet0, Ethernet4, Ethernet8...)
- Check `/usr/share/sonic/device/<platform>/default_sku` for the platform
  default
- The `port_config.ini` in the hwsku directory defines the port-to-lane
  mapping

The hwsku in `DEVICE_METADATA|localhost` must match the platform's actual
port layout. If in doubt, do not override the platform default.

### INTERFACE Table

SONiC `intfmgrd` requires a **base INTERFACE entry** before processing IP
entries:

```
INTERFACE|Ethernet0          {}          # base entry (required)
INTERFACE|Ethernet0|10.1.0.1/31  {}     # IP entry
```

Without the base entry, `intfmgrd` ignores the IP entry and the address is
never assigned to the Linux interface. For VRF-bound interfaces, the base
entry carries the `vrf_name` field.

### BGP_NEIGHBOR Table

Key format: `BGP_NEIGHBOR|<vrf>|<neighbor_ip>` (VRF-prefixed, per Unified FRR Mgmt schema)

Example: `BGP_NEIGHBOR|default|10.1.0.1`

**Critical:** frrcfgd skips entries without the VRF prefix. In the init
replay code, it splits the key on `|` and checks `len(key_list) == 1` —
if there's no VRF prefix, the entry is silently bypassed as "non-compatible."

Required fields:
- `asn` -- remote AS number (string)
- `admin_status` -- `"up"` or `"down"` (NOT `"true"`/`"false"`)
- `local_addr` -- local source IP address (string)

Optional but important:
- `local_asn` -- overrides BGP_GLOBALS local_asn for this neighbor
- `name` -- peer description

### BGP_NEIGHBOR_AF Table

Key format: `BGP_NEIGHBOR_AF|<vrf>|<neighbor_ip>|<afi_safi>`

Example: `BGP_NEIGHBOR_AF|default|10.1.0.1|ipv4_unicast`

Where `afi_safi` is `ipv4_unicast`, `ipv6_unicast`, or `l2vpn_evpn`.

- `admin_status` -- `"up"` or `"down"` for activation
- `activate` -- `"true"` or `"false"` (some frrcfgd versions)

### admin_status Values

The YANG typedef (`sonic-types.yang.j2`) defines:
```yang
typedef admin_status {
    type enumeration { enum up; enum down; }
}
```

Use `"up"`/`"down"`. The `"true"`/`"false"` format is legacy.

## FRR Config Daemon (frrcfgd)

### Activation

Enabled when `DEVICE_METADATA|localhost` has:
```
frr_mgmt_framework_config = "true"
docker_routing_config_mode = "unified"
```

### Behavior

- Does initial bulk sync from CONFIG_DB on startup
- Subscribes to keyspace events for ongoing changes
- Translates CONFIG_DB entries to vtysh CLI commands via key maps
- Does NOT check local interface existence (unlike legacy bgpcfgd)
- Communicates with FRR via Unix domain sockets

### ROUTE_REDISTRIBUTE Table

Key format: `ROUTE_REDISTRIBUTE|<vrf>|<src_protocol>|<dst_protocol>|<addr_family>`

Per the [Unified FRR Mgmt HLD](https://github.com/sonic-net/SONiC/blob/master/doc/mgmt/SONiC_Design_Doc_Unified_FRR_Mgmt_Interface.md),
the key has **4 segments** (not 3). The `dst_protocol` is always `"bgp"`.

```
ROUTE_REDISTRIBUTE|default|connected|bgp|ipv4   {}
ROUTE_REDISTRIBUTE|default|connected|bgp|ipv6   {}
ROUTE_REDISTRIBUTE|default|static|bgp|ipv4      {"route_map": "MY_MAP"}
```

**Critical:** If the key format is wrong (e.g., missing `bgp` segment),
frrcfgd crashes with `ValueError: not enough values to unpack` during its
initial CONFIG_DB bulk sync. This prevents ALL subsequent table processing
(including BGP_NEIGHBOR), so no BGP neighbors get rendered into FRR config.

### PORT Table (Platform-Managed)

PORT entries are created at boot by `portsyncd` reading `port_config.ini`
for the configured HWSKU. These entries contain platform-derived fields:
`lanes`, `speed`, `alias`, `index`.

**Never delete and recreate PORT entries.** Newtron should only merge its
fields (`admin_status`, `mtu`) into existing PORT entries. Deleting PORT
entries removes lane mappings that orchagent needs to create SAI ports and
Linux netdevs.

In `pipeline.go`, PORT is designated as a merge-only table via
`platformMergeTables` — `ReplaceAll` overlays fields without deleting
existing entries.

### Known Issues

- admin_status type mismatch (Issue #18865, fixed PR #21697)
- BGP neighbor AF activation (Issue #20663, fixed PR #21697)
- Route-map string iteration bug in unified mode (Issue #20019)
- psubscribe API mismatch during init (Issue #13109, fixed PR #13836)

## Lab Workflow

### Standard Lab Bring-Up Procedure

1. **Boot devices** with minimal startup config (`containerlab deploy`)
2. **Wait** for SONiC to be healthy and Redis ready
3. **Apply all config** via newtron (`provision -x`) -- topology, BGP, MACs
4. **Save config** on all nodes (`config save -y`)
5. **Reboot** all nodes for clean startup with complete saved config
6. **Verify** BGP sessions, interface IPs, routing tables

### Containerlab + vrnetlab

- vrnetlab handles `ethN <-> tapN` bridging automatically via `tc mirred`
  rules in the tap ifup script (`connection_mode=tc`)
- Container `ethN` maps to SONiC `EthernetN` inside the QEMU VM
- No manual NIC bridging is required

### eBGP Underlay + iBGP Overlay (RFC 7938)

- Spines share a single underlay ASN (e.g., 65100)
- Each leaf gets a unique underlay ASN (65101, 65102, ...)
- Overlay uses a shared ASN (e.g., 65000) with loopback-to-loopback iBGP
- Spines are route reflectors for the overlay
- `BGP_GLOBALS.local_asn` = underlay ASN (device default)
- iBGP overlay neighbors override with `local_asn` = overlay ASN

## References

- [SONiC Configuration Wiki](https://github.com/sonic-net/SONiC/wiki/Configuration)
- [Unified FRR Management Interface](https://github.com/sonic-net/SONiC/blob/master/doc/mgmt/SONiC_Design_Doc_Unified_FRR_Mgmt_Interface.md)
- [BGP Router-ID Design](https://github.com/sonic-net/SONiC/blob/master/doc/BGP/BGP-router-id.md)
- [frrcfgd source](https://github.com/sonic-net/sonic-buildimage/blob/master/src/sonic-frr-mgmt-framework/frrcfgd/frrcfgd.py)
- [sonic-bgp-neighbor.yang](https://github.com/sonic-net/sonic-buildimage/blob/master/src/sonic-yang-models/yang-models/sonic-bgp-neighbor.yang)
- [sonic-bgp-common.yang](https://github.com/sonic-net/sonic-buildimage/blob/master/src/sonic-yang-models/yang-models/sonic-bgp-common.yang)
- [configdb-load.sh](https://github.com/sonic-net/sonic-buildimage/blob/master/files/scripts/configdb-load.sh)
- [init_cfg.json template](https://github.com/sonic-net/sonic-buildimage/blob/master/files/build_templates/init_cfg.json.j2)
