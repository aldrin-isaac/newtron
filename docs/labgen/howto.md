# labgen -- How-To Guide

> **v2 — Initial version.** No v1 predecessor. This document was created as part of the v2 documentation effort as a practical guide for using labgen, creating topologies, and working with configlets.

## 1. Quick Start

### 1.1 Direct Invocation

```bash
go run ./cmd/labgen \
  -topology testlab/topologies/spine-leaf.yml \
  -output testlab/.generated \
  -configlets configlets/
```

### 1.2 Via Makefile

```bash
# Default topology (spine-leaf)
make lab-start

# Specific topology
make lab-start TOPO=minimal

# Generate artifacts only (no deploy)
make labgen
```

The Makefile `lab-start` target builds labgen, generates artifacts, deploys
via containerlab, and runs all post-deploy steps (MAC apply, FRR push, NIC
bridging, profile patching).

### 1.3 Generated Output

After running labgen, the output directory contains:

```
testlab/.generated/
  spine-leaf.clab.yml         # containerlab topology
  spine1/
    config_db.json            # SONiC startup config
    frr.conf                  # FRR routing config
  spine2/
    config_db.json
    frr.conf
  leaf1/
    config_db.json
    frr.conf
  leaf2/
    config_db.json
    frr.conf
  specs/
    network.json              # newtron network definition
    site.json                 # site with route reflectors
    platforms.json            # platform/HWSKU definitions
    profiles/
      spine1.json             # per-node profile (mgmt_ip=PLACEHOLDER)
      spine2.json
      leaf1.json
      leaf2.json
```

Server nodes (e.g., `server1`, `server2`) do not get config_db.json, frr.conf,
or profile files.

---

## 2. Creating a Topology

### 2.1 Complete Example

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
  spine2:
    role: spine
    loopback_ip: "10.0.0.2"
    variables:
      cluster_id: "10.0.0.2"
  leaf1:
    role: leaf
    loopback_ip: "10.0.0.11"
    variables:
      vtep_name: vtep1
      spine1_ip: "10.0.0.1"
      spine2_ip: "10.0.0.2"
  leaf2:
    role: leaf
    loopback_ip: "10.0.0.12"
    variables:
      vtep_name: vtep1
      spine1_ip: "10.0.0.1"
      spine2_ip: "10.0.0.2"
  server1:
    role: server
    image: nicolaka/netshoot:latest
  server2:
    role: server
    image: nicolaka/netshoot:latest

links:
  - endpoints: ["spine1:Ethernet0", "leaf1:Ethernet0"]
  - endpoints: ["spine1:Ethernet1", "leaf2:Ethernet0"]
  - endpoints: ["spine2:Ethernet0", "leaf1:Ethernet1"]
  - endpoints: ["spine2:Ethernet1", "leaf2:Ethernet1"]
  - endpoints: ["leaf1:Ethernet2", "server1:eth1"]
  - endpoints: ["leaf2:Ethernet2", "server2:eth1"]

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

### 2.2 Field Reference

| Section | Field | Required | Description |
|---|---|---|---|
| *(top)* | `name` | Yes | Topology name, used for clab YAML filename |
| `defaults` | `image` | Yes | Default Docker image for SONiC nodes |
| `defaults` | `kind` | No | Containerlab kind override (`sonic-vm` or `sonic-vs`). Auto-detected from image if omitted |
| `defaults` | `username` | No | SSH username (injected into QEMU env for sonic-vm) |
| `defaults` | `password` | No | SSH password |
| `defaults` | `platform` | No | SONiC platform name (default: `vs-platform`) |
| `defaults` | `site` | No | Site name for specs (default: `<name>-site`) |
| `defaults` | `hwsku` | No | Hardware SKU (default: `Force10-S6000`) |
| `defaults` | `ntp_server_1` | No | NTP server address (configlet variable) |
| `defaults` | `ntp_server_2` | No | Second NTP server address |
| `defaults` | `syslog_server` | No | Syslog server address |
| `network` | `as_number` | Yes | BGP AS number for the fabric |
| `network` | `region` | Yes | Region name for specs |
| `nodes.<name>` | `role` | Yes | One of: `spine`, `leaf`, `server` |
| `nodes.<name>` | `loopback_ip` | Yes (spine/leaf) | Loopback IP, used as router-id |
| `nodes.<name>` | `image` | No | Per-node image override |
| `nodes.<name>` | `platform` | No | Per-node platform override |
| `nodes.<name>` | `cmd` | No | Container command (server nodes only, default: `sleep infinity`) |
| `nodes.<name>` | `configlets` | No | Per-node configlet list (overrides role_defaults) |
| `nodes.<name>` | `variables` | No | Per-node variable overrides for configlet substitution |
| `links[]` | `endpoints` | Yes | Array of exactly 2 strings in `"node:interface"` format |
| `role_defaults` | `<role>` | No | Ordered list of configlet names applied to all nodes of that role |

### 2.3 Node Roles

- **spine**: BGP route reflector. Gets config_db.json, frr.conf (with cluster-id,
  route-reflector-client), and a profile with `is_route_reflector: true`.
- **leaf**: ToR switch and EVPN VTEP. Gets config_db.json, frr.conf (with
  advertise-all-vni), and a standard profile.
- **server**: Linux container. Gets no SONiC configuration. Uses `nicolaka/netshoot:latest`
  by default. Links use plain Linux interface names (e.g., `eth1`).

### 2.4 Link Format

Each link is an array of exactly two endpoints. Each endpoint is a string
in `"nodeName:InterfaceName"` format:

```yaml
links:
  # SONiC-to-SONiC link (uses SONiC Ethernet naming)
  - endpoints: ["spine1:Ethernet0", "leaf1:Ethernet0"]

  # SONiC-to-server link (server uses Linux naming)
  - endpoints: ["leaf1:Ethernet2", "server1:eth1"]
```

All node names in endpoints must be defined in the `nodes` section.

---

## 3. Configlet System

### 3.1 Creating a New Configlet

Create a JSON file in the configlets directory (e.g., `configlets/my-feature.json`):

```json
{
  "name": "my-feature",
  "description": "Description of the feature",
  "version": "1.0",
  "variables": ["device_name", "custom_var"],
  "config_db": {
    "MY_TABLE": {
      "{{device_name}}_entry": {
        "field1": "{{custom_var}}",
        "field2": "static-value"
      }
    }
  }
}
```

The `variables` array is documentation only -- it lists which `{{variables}}`
the configlet expects. All substitution is done by simple string replacement.

### 3.2 Available Variables

These variables are automatically populated from the topology:

| Variable | Source |
|---|---|
| `device_name` | Node name from the topology |
| `loopback_ip` | `node.LoopbackIP` |
| `router_id` | `node.LoopbackIP` (same as loopback_ip) |
| `as_number` | `topo.Network.ASNumber` (as string) |
| `hwsku` | `topo.Defaults.HWSKU` |
| `platform` | `topo.Defaults.Platform` |
| `ntp_server_1` | `topo.Defaults.NTPServer1` |
| `ntp_server_2` | `topo.Defaults.NTPServer2` |
| `syslog_server` | `topo.Defaults.SyslogServer` |

Custom variables can be added per node via the `variables` map:

```yaml
nodes:
  leaf1:
    role: leaf
    loopback_ip: "10.0.0.11"
    variables:
      vtep_name: vtep1
      my_custom_var: "custom-value"
```

Node-level variables override topology-level defaults.

### 3.3 Role Defaults vs. Per-Node Configlets

By default, configlets are selected from `role_defaults`:

```yaml
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

To override for a specific node, use the `configlets` field:

```yaml
nodes:
  leaf1:
    role: leaf
    loopback_ip: "10.0.0.11"
    configlets:
      - sonic-baseline
      - my-custom-leaf-config
```

When `configlets` is specified on a node, `role_defaults` is **not** used for
that node.

### 3.4 Merge Behavior

Configlets are applied in order and deep-merged at the field level. Given:

```
Configlet A:  { "TABLE": { "key1": { "field_a": "1", "field_b": "2" } } }
Configlet B:  { "TABLE": { "key1": { "field_b": "OVERRIDE", "field_c": "3" } } }
```

Result after merging A then B:

```
{ "TABLE": { "key1": { "field_a": "1", "field_b": "OVERRIDE", "field_c": "3" } } }
```

---

## 4. Customizing Output

### 4.1 Adding Custom PORT Entries

labgen automatically creates PORT entries for all interfaces used in links,
plus Ethernet0 through Ethernet7 (minimum 8 ports). Default port attributes:

```json
{
  "admin_status": "up",
  "mtu": "9100",
  "speed": "40000"
}
```

To customize port settings, define them in a configlet:

```json
{
  "name": "my-port-config",
  "config_db": {
    "PORT": {
      "Ethernet0": {
        "admin_status": "up",
        "mtu": "9216",
        "speed": "100000",
        "fec": "rs"
      }
    }
  }
}
```

Configlet PORT entries take precedence -- labgen only adds PORT entries for
interfaces that do not already exist in the merged config_db.

### 4.2 Adding Custom Variables Per Node

```yaml
nodes:
  leaf1:
    role: leaf
    loopback_ip: "10.0.0.11"
    variables:
      vtep_name: vtep1
      vrf_name: "Vrf_customer"
      custom_vni: "10001"
```

These variables are available as `{{vtep_name}}`, `{{vrf_name}}`, `{{custom_vni}}`
in configlet templates.

### 4.3 Overriding Defaults Per Node

```yaml
nodes:
  special-leaf:
    role: leaf
    loopback_ip: "10.0.0.99"
    image: vrnetlab/cisco_sonic:special-build
    platform: special-platform
```

- `image`: Overrides `defaults.image` for this node. Containerlab kind is
  re-detected from the per-node image.
- `platform`: Overrides the platform in the generated profile.

---

## 5. Working with Generated Output

### 5.1 Directory Structure

```
<output-dir>/
  <name>.clab.yml                # Containerlab topology file
  <node>/
    config_db.json               # SONiC startup configuration
    frr.conf                     # FRR routing configuration
  specs/
    network.json                 # Network definition
    site.json                    # Site definition
    platforms.json               # Platform definition
    profiles/
      <node>.json                # Per-node profile
```

### 5.2 How config_db.json Is Used

For **sonic-vm** (vrnetlab): containerlab uses the `startup-config` field to
copy config_db.json into the QEMU VM during boot. The VM reads it as its
initial CONFIG_DB.

For **sonic-vs**: the config_db.json is bind-mounted at `/etc/sonic/config_db.json`
inside the container.

### 5.3 How frr.conf Is Pushed

FRR configs are **not** loaded at container start time. They are pushed
post-deploy by `setup.sh lab_push_frr`:

1. SCP `frr.conf` to `/tmp/frr.conf` on the SONiC node.
2. `docker cp /tmp/frr.conf bgp:/etc/frr/lab_frr.conf` -- copy into the BGP container.
3. `docker exec bgp vtysh -f /etc/frr/lab_frr.conf` -- load the config into FRR.
4. `docker exec bgp vtysh -c 'write memory'` -- persist to running config.

This approach is necessary because the BGP container starts after the main
SONiC container, and the FRR split-mode configuration needs to be loaded
into the already-running FRR instance.

### 5.4 How Specs Are Consumed

The specs under `specs/` are consumed by newtron's control plane:
- `Network.NewNetwork` reads `network.json`, `site.json`, and `platforms.json`.
- `ConnectDevice` uses per-node profiles from `specs/profiles/` to locate
  devices by management IP and authenticate via SSH.

The `mgmt_ip: "PLACEHOLDER"` in profiles is replaced with real Docker IPs
by `lab_patch_profiles` after containerlab deploy.

---

## 6. Adding a New Node Type

### 6.1 Adding a New Role

Currently labgen supports three roles: `spine`, `leaf`, and `server`. To add
behavior for a new role, you would need to modify:

1. **parse.go** -- Add the new role to the validation check:
   ```go
   if node.Role != "spine" && node.Role != "leaf" && node.Role != "server" && node.Role != "newrole" {
   ```

2. **frr_gen.go** -- Add a new `generateNewRoleFRR` function and wire it into
   `GenerateFRRConfigs`:
   ```go
   case "newrole":
       conf = generateNewRoleFRR(topo, nodeName, node, linkIPs)
   ```

3. **specs_gen.go** -- Update `generateProfiles` if the new role needs special
   profile fields (similar to `is_route_reflector` for spines).

### 6.2 Creating Configlets for a New Role

1. Create configlet JSON files in the `configlets/` directory.
2. Add the role to `role_defaults` in the topology YAML:

```yaml
role_defaults:
  newrole:
    - sonic-baseline
    - my-newrole-config
```

### 6.3 Adding to a Topology

```yaml
nodes:
  mynode:
    role: newrole
    loopback_ip: "10.0.0.50"
    variables:
      special_var: "value"
```

---

## 7. Troubleshooting

### 7.1 "configlet not found"

```
Error: node leaf1: loading configlet sonic-baseline: reading configlet sonic-baseline:
  open configlets/sonic-baseline.json: no such file or directory
```

**Cause**: The `-configlets` path is wrong or the configlet file does not exist.

**Fix**: Verify the configlets directory path. The default resolution order is:
1. Explicit `-configlets` flag value.
2. `<topology-dir>/../../configlets` (relative to topology file).
3. `./configlets` (current working directory).

Check that the file `<configlets-dir>/<name>.json` exists for each configlet
referenced in `role_defaults` or node `configlets`.

### 7.2 "interface mapping mismatch"

If containerlab links do not match the expected SONiC interfaces, check:

1. Link endpoint interface names match the actual SONiC Ethernet naming.
2. For `sonic-vm`: interfaces are mapped sequentially. If a node uses Ethernet0
   and Ethernet4, they map to eth1 and eth2 (not eth1 and eth5).
3. Server node interfaces must use Linux names (e.g., `eth1`), not SONiC names.

### 7.3 "PLACEHOLDER in profile"

```json
{
  "mgmt_ip": "PLACEHOLDER",
  ...
}
```

**Cause**: Profiles are generated with `mgmt_ip: "PLACEHOLDER"` because the
management IP is assigned by containerlab at deploy time.

**Fix**: Run `lab_patch_profiles` after containerlab deploy. This is done
automatically by `setup.sh lab-start`. If running manually:

```bash
./testlab/setup.sh lab-start spine-leaf
```

The patching step replaces `"PLACEHOLDER"` with the real Docker-assigned IP
and adds `ssh_user`/`ssh_pass` from the clab YAML.

### 7.4 "bgp suppress-fib-pending"

Do **not** add `bgp suppress-fib-pending` to the FRR configuration. It is
intentionally omitted because it causes route advertisement failures when FRR
config is loaded dynamically via `vtysh -f`. The symptom is BGP routes stuck
in "Not advertised to any peer" state.

See the code comment in `pkg/labgen/frr_gen.go`:

```go
// NOTE: bgp suppress-fib-pending is intentionally omitted. It blocks
// advertisement of routes whose FIB installation hasn't been confirmed
// by zebra. When config is loaded dynamically via "vtysh -f" (rather
// than read at FRR startup), the FIB notification for pre-existing
// connected routes (like loopbacks) is never received, causing
// "network" statement routes to be stuck as "Not advertised to any peer".
```

### 7.5 "node validation failed"

```
Error: validating topology: node leaf1: loopback_ip is required
```

**Cause**: A spine or leaf node is missing the `loopback_ip` field.

**Fix**: Add a valid IPv4 address as the loopback_ip for all spine and leaf
nodes. Server nodes do not require a loopback_ip.

### 7.6 "link references undefined node"

```
Error: validating topology: link 3: endpoint "badnode:Ethernet0" references undefined node "badnode"
```

**Cause**: A link endpoint references a node name that is not defined in the
`nodes` section.

**Fix**: Verify that all node names in link endpoints exactly match the keys
in the `nodes` map. Node names are case-sensitive.

### 7.7 swss Warm Restart Freeze

After MAC apply, if `show ip route` or `show vlan` commands hang, the likely
cause is orchagent frozen for warm restart. The `lab_apply_macs` step in
setup.sh explicitly disables warm restart before restarting swss:

```bash
redis-cli -n 4 HSET 'WARM_RESTART|swss' 'enable' 'false'
sudo config save -y
sudo systemctl restart swss
```

If this was skipped, manually run these commands on the affected node.

---

## 8. Integration with Lab Lifecycle

### 8.1 Full Lifecycle Sequence

labgen is step 1 of the complete lab lifecycle managed by `setup.sh`:

```
labgen            Generate config_db.json, frr.conf, clab.yml, specs
    |
    v
containerlab      Create containers and virtual network links
deploy
    |
    v
wait_healthy      Poll containers until all SONiC VMs pass health checks
                   (up to 5 min, skip linux containers)
    |
    v
wait_redis        SSH into each SONiC node, poll redis-cli PING until PONG
                   (up to 5 min per node)
    |
    v
apply_macs        Disable warm restart, save config, cold-restart swss
                   to apply unique DEVICE_METADATA MAC per node
    |
    v
push_frr          SCP frr.conf to each node, load via vtysh -f,
                   write memory to persist
    |
    v
bridge_nics       Bridge QEMU NICs (ethN) to ASIC simulator ports (swvethN)
                   using tc mirred redirect (up to 64 ports per node)
    |
    v
patch_profiles    Replace PLACEHOLDER mgmt_ip in specs/profiles/*.json
                   with real Docker-assigned IPs, add ssh_user/ssh_pass
    |
    v
Lab Ready         BGP sessions establish, EVPN converges, E2E tests can run
```

### 8.2 Running the Full Lifecycle

```bash
# Start lab with default topology
make lab-start

# Start lab with specific topology
make lab-start TOPO=minimal

# Check lab status
make lab-status

# Stop and clean up
make lab-stop
```

### 8.3 Running E2E Tests

```bash
# Run E2E tests against a running lab
make test-e2e

# Full lifecycle: start lab, run tests, stop lab
make test-e2e-full
```

---

## 9. Related Documentation

| Document | Path | Description |
|---|---|---|
| labgen HLD | [docs/labgen/hld.md](hld.md) | High-level architecture, input/output format, configlet system overview |
| labgen LLD | [docs/labgen/lld.md](lld.md) | Detailed data structures, function signatures, generation pipeline internals |
| vmlab LLD | [docs/vmlab/lld.md](../vmlab/lld.md) | VM orchestration, QEMU lifecycle, port management |

### Topology Files

| File | Description |
|---|---|
| `testlab/topologies/spine-leaf.yml` | 2-spine + 2-leaf + 2-server reference topology |
| `testlab/topologies/minimal.yml` | 1-spine + 1-leaf minimal topology for quick testing |

### Configlet Files

| File | Description |
|---|---|
| `configlets/sonic-baseline.json` | Base SONiC config (DEVICE_METADATA, LOOPBACK, NTP, SYSLOG, FEATURE, MGMT_VRF) |
| `configlets/sonic-evpn-leaf.json` | EVPN leaf (VXLAN_TUNNEL, VXLAN_EVPN_NVO, SAG_GLOBAL) |
| `configlets/sonic-evpn-spine.json` | EVPN spine (empty config_db; BGP managed via FRR only) |
| `configlets/sonic-evpn.json` | Generic EVPN (VXLAN_TUNNEL, VXLAN_EVPN_NVO) |
| `configlets/sonic-acl-copp.json` | Control Plane Protection ACLs and policers |
| `configlets/sonic-qos-8q.json` | 8-queue QoS with DSCP-to-TC mapping, scheduling, WRED |
