# labgen -- High-Level Design

> **v2 — Initial version.** No v1 predecessor. This document was created as part of the v2 documentation effort to cover the labgen tool, which previously had no dedicated design documentation.

## 1. Purpose

labgen is a code-generation tool that transforms a declarative topology YAML file
into all the artifacts needed to stand up a virtual SONiC fabric using
containerlab. A single YAML file describing nodes, links, and roles produces:

- Per-node SONiC startup configurations (`config_db.json`)
- Per-node FRR routing configurations (`frr.conf`)
- A containerlab topology file (`<name>.clab.yml`)
- Newtron spec files consumed by the control plane (network, site, platform, profiles)

The tool exists so that the entire lab definition lives in one human-readable
file while the mechanical work of IP assignment, interface mapping, MAC
generation, configlet merging, and spec scaffolding is automated and
reproducible.

---

## 2. Scope

| In scope | Out of scope |
|---|---|
| Topology parsing and validation | Containerlab deployment (handled by `containerlab deploy`) |
| config_db.json generation via configlet system | Post-deploy lifecycle (MAC apply, FRR push, NIC bridging) |
| FRR config generation (BGP, EVPN) | Runtime device management |
| Containerlab YAML generation | SONiC image building |
| Newtron spec file generation | Redis seeding or test execution |
| Fabric link IP assignment (/31) | Multi-AS / eBGP topologies |
| Deterministic per-node MAC assignment | Physical lab provisioning |

---

## 3. Architecture Overview

### 3.1 Generation Pipeline

```
                         +-------------------+
                         | Topology YAML     |
                         | (spine-leaf.yml)  |
                         +---------+---------+
                                   |
                          LoadTopology(path)
                          validateTopology()
                                   |
                    +--------------+--------------+
                    |              |              |
                    v              v              v
          +--------+----+  +------+------+  +----+--------+
          | configdb_gen|  | frr_gen     |  | clab_gen    |
          | .go         |  | .go         |  | .go         |
          +--------+----+  +------+------+  +----+--------+
                    |              |              |
                    v              v              v
          node/config_db   node/frr.conf   <name>.clab.yml
               .json
                    |
                    +-------+
                            |
                    +-------v-------+
                    | specs_gen.go  |
                    +-------+-------+
                            |
              +-------------+-------------+
              |             |             |
              v             v             v
        network.json   site.json   profiles/*.json
        platforms.json
```

### 3.2 Four Generators

| Generator | Source file | Responsibility |
|---|---|---|
| **configdb_gen** | `pkg/labgen/configdb_gen.go` | Loads configlets, resolves variables, merges config_db tables, adds PORT and INTERFACE entries, assigns unique system MACs |
| **frr_gen** | `pkg/labgen/frr_gen.go` | Computes fabric link IPs, generates per-role FRR configs (leaf BGP + EVPN, spine route reflector) |
| **clab_gen** | `pkg/labgen/clab_gen.go` | Builds containerlab YAML with correct kind, interface mapping, QEMU tuning, health checks |
| **specs_gen** | `pkg/labgen/specs_gen.go` | Produces newtron network/site/platform/profile JSON specs for the control plane |

---

## 4. Input Format

The topology YAML has five top-level sections:

```yaml
name: spine-leaf

defaults:
  image: vrnetlab/cisco_sonic:ngdp-202411
  username: cisco
  password: cisco123
  platform: vs-platform
  site: lab-site
  hwsku: "Force10-S6000"
  ntp_server_1: "10.100.0.1"
  ntp_server_2: "10.100.0.2"
  syslog_server: "10.100.0.3"

network:
  as_number: 65000
  region: lab-region

nodes:
  spine1:
    role: spine
    loopback_ip: "10.0.0.1"
    variables:
      cluster_id: "10.0.0.1"
  leaf1:
    role: leaf
    loopback_ip: "10.0.0.11"
    variables:
      vtep_name: vtep1

links:
  - endpoints: ["spine1:Ethernet0", "leaf1:Ethernet0"]

role_defaults:
  spine:
    - sonic-baseline
    - sonic-evpn-spine
  leaf:
    - sonic-baseline
    - sonic-evpn-leaf
    - sonic-acl-copp
    - sonic-qos-8q
```

### 4.1 Node Roles

| Role | Meaning | Gets config_db | Gets frr.conf | Gets profile |
|---|---|---|---|---|
| `spine` | BGP route reflector, fabric backbone | Yes | Yes | Yes |
| `leaf` | ToR switch, EVPN VTEP | Yes | Yes | Yes |
| `server` | Linux container (netshoot) for traffic generation | No | No | No |

### 4.2 Configlet References

Each node gets configlets from either:
1. Its own `configlets` list (per-node override), or
2. The `role_defaults` map (looked up by `node.Role`)

---

## 5. Output Artifacts

### 5.1 Per-Node Outputs

| File | Description |
|---|---|
| `<node>/config_db.json` | SONiC startup configuration (merged configlets + PORT + INTERFACE + DEVICE_METADATA MAC) |
| `<node>/frr.conf` | Full FRR configuration (BGP + EVPN, role-specific) |

### 5.2 Topology-Level Output

| File | Description |
|---|---|
| `<name>.clab.yml` | Containerlab topology with nodes, links, binds, env, health checks |

### 5.3 Spec Outputs

| File | Description |
|---|---|
| `specs/network.json` | Network definition (regions, AS, services, filters, VPNs) |
| `specs/site.json` | Site definition with route reflector list |
| `specs/platforms.json` | Platform definition (HWSKU, port count, default speed) |
| `specs/profiles/<node>.json` | Per-node profile (mgmt_ip=PLACEHOLDER, loopback, site, platform) |

---

## 6. Configlet System

### 6.1 Template Format

Configlets are JSON files that represent fragments of SONiC `config_db.json`.
They use `{{variable}}` placeholders that are resolved at generation time.

```json
{
  "name": "sonic-baseline",
  "description": "Base SONiC configuration for all switches",
  "version": "1.0",
  "variables": ["device_name", "hwsku", "platform", "loopback_ip"],
  "config_db": {
    "DEVICE_METADATA": {
      "localhost": {
        "hostname": "{{device_name}}",
        "hwsku": "{{hwsku}}",
        "platform": "{{platform}}"
      }
    },
    "LOOPBACK_INTERFACE": {
      "Loopback0|{{loopback_ip}}/32": {}
    }
  }
}
```

### 6.2 Role-Based Defaults

The `role_defaults` map assigns a list of configlets per role. For example,
leaf nodes get `sonic-baseline`, `sonic-evpn-leaf`, `sonic-acl-copp`, and
`sonic-qos-8q` by default.

### 6.3 Merge Semantics

When multiple configlets are applied to a node, they are deep-merged at the
`table -> key -> field` level. Fields in later configlets overwrite fields from
earlier ones for the same table and key.

### 6.4 Available Configlets

| Configlet | Purpose |
|---|---|
| `sonic-baseline` | DEVICE_METADATA, LOOPBACK, NTP, SYSLOG, FEATURE, MGMT_VRF |
| `sonic-evpn-leaf` | VXLAN_TUNNEL, VXLAN_EVPN_NVO, SAG_GLOBAL |
| `sonic-evpn-spine` | Empty config_db (spine BGP is entirely FRR-managed) |
| `sonic-acl-copp` | Control Plane Protection ACLs and policers |
| `sonic-qos-8q` | 8-queue QoS with DSCP mapping, scheduling, WRED profiles |
| `sonic-evpn` | Generic EVPN/VXLAN baseline (VXLAN_TUNNEL, VXLAN_EVPN_NVO) |

---

## 7. Container Images

| Kind | Image | Use case |
|---|---|---|
| `sonic-vm` | `vrnetlab/cisco_sonic:*` | Full SONiC in QEMU VM -- realistic ASIC simulation, requires 2 CPU + 6 GiB RAM per node |
| `sonic-vs` | Native SONiC-VS container | Lightweight SONiC virtual switch, lower resource needs |
| `linux` | `nicolaka/netshoot:latest` | Server containers for traffic endpoints, runs `sleep infinity` |

Kind is auto-detected from the image name. If the image contains `vrnetlab` or
`sonic-vm`, it is treated as `sonic-vm`; otherwise `sonic-vs`. The `kind` field
in `defaults` can override auto-detection.

---

## 8. FRR Configuration

### 8.1 Why FRR Is Generated Separately

SONiC uses `frr_split_config_enabled: true` with `docker_routing_config_mode: split`.
In this mode, bgpcfgd does not fully support the LeafRouter device type for
EVPN fabrics. labgen generates complete FRR configs that are pushed to nodes
via `vtysh -f` after deployment.

### 8.2 Spine: Route Reflector

- Peer group: `LEAF-PEERS` with `route-reflector-client`
- `bgp cluster-id` set from `node.Variables["cluster_id"]` or loopback IP
- L2VPN EVPN address family with `route-reflector-client` for leaf peers
- `next-hop-self` for leaf peers in IPv4 unicast
- `maximum-paths 64` / `maximum-paths ibgp 64`

### 8.3 Leaf: EVPN VTEP

- Peer group: `SPINE` with `remote-as` matching the network AS
- L2VPN EVPN address family with `advertise-all-vni`, `advertise-default-gw`, `advertise-svi-ip`
- Route-map `RM_SET_SRC` sets source IP to loopback on BGP-learned routes
- `maximum-paths 16` / `maximum-paths ibgp 16`

### 8.4 Intentional Omission: bgp suppress-fib-pending

`bgp suppress-fib-pending` is intentionally not included. When FRR config is
loaded dynamically via `vtysh -f` (rather than at FRR startup), the FIB
notification for pre-existing connected routes is never received, causing
`network` statement routes to be stuck as "Not advertised to any peer".

---

## 9. Network Addressing

### 9.1 Fabric Links

Each non-server link is assigned a /31 point-to-point IP pair from the
`10.1.0.0/24` range:

| Link index | Endpoint A | Endpoint B |
|---|---|---|
| 0 | `10.1.0.0/31` | `10.1.0.1/31` |
| 1 | `10.1.0.2/31` | `10.1.0.3/31` |
| 2 | `10.1.0.4/31` | `10.1.0.5/31` |
| ... | `10.1.0.(N*2)/31` | `10.1.0.(N*2+1)/31` |

Links involving server nodes are skipped (servers use plain Linux networking).

### 9.2 Loopback IPs

Loopback IPs are defined per node in the topology YAML. They are used as:
- BGP router-id
- VTEP source IP (on leaf nodes)
- Route-map source address (`RM_SET_SRC`)

### 9.3 System MACs

Each SONiC node receives a deterministic, locally-administered MAC address:

```
02:42:f0:ab:XX:XX
```

where `XX:XX` is derived from the node's sorted index. The `02:` prefix sets the
IEEE locally-administered bit. This is necessary because vrnetlab QEMU VMs all
share the same default QEMU MAC.

---

## 10. Integration Points

### 10.1 containerlab

labgen produces a `<name>.clab.yml` that is deployed via:

```bash
cd <output-dir> && sudo containerlab deploy -t <name>.clab.yml
```

Destruction uses `containerlab destroy -t <name>.clab.yml --cleanup`.

### 10.2 setup.sh Lab Lifecycle

`setup.sh lab-start` orchestrates the full sequence:

1. **labgen** -- Generate all artifacts
2. **containerlab deploy** -- Create containers and virtual links
3. **lab_wait_healthy** -- Wait for SONiC VMs to pass health checks
4. **lab_wait_redis** -- Wait for Redis inside each SONiC node
5. **lab_apply_macs** -- Restart swss to apply unique system MACs from config_db
6. **lab_push_frr** -- SCP frr.conf to each node, load via `vtysh -f`
7. **lab_bridge_nics** -- Bridge QEMU NICs (ethN) to ASIC simulator ports (swvethN) using tc mirred
8. **lab_patch_profiles** -- Replace PLACEHOLDER mgmt_ip in profiles with real Docker IPs

### 10.3 Newtron Specs Consumption

The generated specs under `specs/` are consumed by newtron's control plane.
`Network.NewNetwork` reads `network.json`, `site.json`, and `platforms.json`
to initialize the network model. Per-node profiles under `specs/profiles/`
are used by `ConnectDevice` to locate and authenticate to lab switches.

---

## Appendix: File Layout

```
cmd/labgen/
  main.go                 # CLI entry point

pkg/labgen/
  types.go                # Topology, NodeDef, LinkDef, TopologyDefaults
  parse.go                # LoadTopology, validateTopology, NodeInterfaces
  configdb_gen.go         # GenerateStartupConfigs, ComputeFabricLinkIPs, SonicIfaceToClabIface
  frr_gen.go              # GenerateFRRConfigs, generateLeafFRR, generateSpineFRR
  clab_gen.go             # GenerateClabTopology, buildSequentialIfaceMaps
  specs_gen.go            # GenerateLabSpecs, network/site/platform/profile generation

pkg/configlet/
  configlet.go            # LoadConfiglet, ListConfiglets, Configlet struct
  resolve.go              # ResolveVariables, ResolveConfiglet, MergeConfigDB

configlets/
  sonic-baseline.json     # Base SONiC config (DEVICE_METADATA, LOOPBACK, NTP, FEATURE)
  sonic-evpn-leaf.json    # EVPN leaf (VXLAN_TUNNEL, VXLAN_EVPN_NVO, SAG)
  sonic-evpn-spine.json   # EVPN spine (empty -- BGP via FRR only)
  sonic-evpn.json         # Generic EVPN (VXLAN_TUNNEL, VXLAN_EVPN_NVO)
  sonic-acl-copp.json     # CoPP ACLs and policers
  sonic-qos-8q.json       # 8-queue QoS with DSCP/TC/WRED

testlab/topologies/
  spine-leaf.yml          # 2-spine + 2-leaf + 2-server reference topology
  minimal.yml             # 1-spine + 1-leaf minimal topology
```
