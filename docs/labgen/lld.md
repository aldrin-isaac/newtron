# labgen -- Low-Level Design

> **v2 — Initial version.** No v1 predecessor. This document was created as part of the v2 documentation effort to provide complete Go type definitions, function signatures, and implementation details for all labgen packages.

## 1. Package Structure

```
cmd/labgen/
  main.go                   CLI entry point, flag parsing, generation sequence

pkg/labgen/
  types.go                  Top-level data structures (Topology, NodeDef, LinkDef, ...)
  parse.go                  YAML loading, validation, NodeInterfaces helper
  configdb_gen.go           config_db.json generation, fabric IP computation, interface mapping
  frr_gen.go                FRR config generation (leaf and spine templates)
  clab_gen.go               Containerlab YAML generation, kind resolution, sequential iface maps
  specs_gen.go              Newtron spec file generation (network, site, platform, profiles)

pkg/configlet/
  configlet.go              Configlet struct, LoadConfiglet, ListConfiglets
  resolve.go                Variable substitution, configlet resolution, deep merge
```

---

## 2. Data Structures

### 2.1 Topology (pkg/labgen/types.go)

```go
type Topology struct {
    Name         string                `yaml:"name"`
    Defaults     TopologyDefaults      `yaml:"defaults"`
    Network      TopologyNetwork       `yaml:"network"`
    Nodes        map[string]NodeDef    `yaml:"nodes"`
    Links        []LinkDef             `yaml:"links"`
    RoleDefaults map[string][]string   `yaml:"role_defaults"`
}
```

`Name` identifies the topology and is used as the containerlab topology name
and the base filename for the `.clab.yml` output.

`RoleDefaults` maps role names (e.g., `"spine"`, `"leaf"`) to ordered lists
of configlet names that are applied when a node does not specify its own
`configlets` list.

### 2.2 TopologyDefaults

```go
type TopologyDefaults struct {
    Image        string `yaml:"image"`
    Kind         string `yaml:"kind,omitempty"`
    Username     string `yaml:"username,omitempty"`
    Password     string `yaml:"password,omitempty"`
    Platform     string `yaml:"platform"`
    Site         string `yaml:"site"`
    HWSKU        string `yaml:"hwsku"`
    NTPServer1   string `yaml:"ntp_server_1"`
    NTPServer2   string `yaml:"ntp_server_2"`
    SyslogServer string `yaml:"syslog_server"`
}
```

- `Image`: Docker image for SONiC nodes. Required.
- `Kind`: Containerlab kind. If empty, auto-detected from `Image`.
- `Username`/`Password`: SSH credentials injected into vrnetlab `ENV`.
- `Platform`, `HWSKU`: SONiC platform identifiers used in config_db and specs.

### 2.3 TopologyNetwork

```go
type TopologyNetwork struct {
    ASNumber int    `yaml:"as_number"`
    Region   string `yaml:"region"`
}
```

Used by BGP configuration generation and specs (network.json region entry).

### 2.4 NodeDef

```go
type NodeDef struct {
    Role       string            `yaml:"role"`
    LoopbackIP string            `yaml:"loopback_ip,omitempty"`
    Image      string            `yaml:"image,omitempty"`
    Platform   string            `yaml:"platform,omitempty"`
    Cmd        string            `yaml:"cmd,omitempty"`
    Configlets []string          `yaml:"configlets,omitempty"`
    Variables  map[string]string `yaml:"variables,omitempty"`
}
```

- `Role`: One of `"spine"`, `"leaf"`, `"server"`. Validated by `validateTopology`.
- `LoopbackIP`: Required for spine and leaf nodes. Used as router-id and VTEP source.
- `Image`: Per-node image override. Falls back to `Defaults.Image`.
- `Configlets`: Per-node configlet override. Falls back to `RoleDefaults[node.Role]`.
- `Variables`: Per-node key-value pairs merged into the variable map for configlet resolution.

### 2.5 LinkDef

```go
type LinkDef struct {
    Endpoints []string `yaml:"endpoints"`
}
```

Each link has exactly two endpoints in `"nodeName:InterfaceName"` format.
Example: `["spine1:Ethernet0", "leaf1:Ethernet0"]`.

### 2.6 ClabTopology / ClabNode / ClabLink / ClabHealthcheck (pkg/labgen/clab_gen.go)

```go
type ClabTopology struct {
    Name     string       `yaml:"name"`
    Topology ClabTopoSpec `yaml:"topology"`
}

type ClabTopoSpec struct {
    Nodes map[string]*ClabNode `yaml:"nodes"`
    Links []ClabLink           `yaml:"links"`
}

type ClabNode struct {
    Kind          string            `yaml:"kind"`
    Image         string            `yaml:"image"`
    Cmd           string            `yaml:"cmd,omitempty"`
    CPU           int               `yaml:"cpu,omitempty"`
    Memory        string            `yaml:"memory,omitempty"`
    Binds         []string          `yaml:"binds,omitempty"`
    StartupConfig string            `yaml:"startup-config,omitempty"`
    Env           map[string]string `yaml:"env,omitempty"`
    Healthcheck   *ClabHealthcheck  `yaml:"healthcheck,omitempty"`
}

type ClabHealthcheck struct {
    StartPeriod int `yaml:"start-period"`
    Interval    int `yaml:"interval"`
    Timeout     int `yaml:"timeout"`
    Retries     int `yaml:"retries"`
}

type ClabLink struct {
    Endpoints []string `yaml:"endpoints"`
}
```

For `sonic-vm` nodes:
- `StartupConfig` points to `<node>/config_db.json` (containerlab copies it into the VM).
- `CPU: 2`, `Memory: "6144mib"` for QEMU performance.
- `Healthcheck.StartPeriod: 600` (10-minute grace for VM boot).
- `Env` includes `QEMU_ADDITIONAL_ARGS: "-cpu host"` and optional `USERNAME`/`PASSWORD`.

For `sonic-vs` nodes:
- `Binds` mounts `<node>/config_db.json` into `/etc/sonic/config_db.json`.

For server nodes:
- `Kind: "linux"`, image defaults to `nicolaka/netshoot:latest`, cmd defaults to `sleep infinity`.

### 2.7 FabricLinkIP (pkg/labgen/configdb_gen.go)

```go
type FabricLinkIP struct {
    Node      string // node name
    Interface string // SONiC interface name (e.g. "Ethernet0")
    IP        string // IP address with prefix (e.g. "10.1.0.0/31")
    PeerNode  string // node on the other end
    PeerIP    string // peer's IP (without prefix, e.g. "10.1.0.1")
}
```

Used by both `configdb_gen` (to populate INTERFACE entries) and `frr_gen`
(to build BGP neighbor statements).

### 2.8 Configlet (pkg/configlet/configlet.go)

```go
type Configlet struct {
    Name        string                            `json:"name"`
    Description string                            `json:"description"`
    Version     string                            `json:"version"`
    ConfigDB    map[string]map[string]interface{} `json:"config_db"`
    Variables   []string                          `json:"variables"`
}
```

`ConfigDB` uses `interface{}` values because JSON field values may be strings
or nested objects. During resolution, all values are converted to
`map[string]map[string]map[string]string` (table -> key -> field -> value).

---

## 3. Generation Pipeline

### 3.1 Entry Point (cmd/labgen/main.go)

```go
func main() {
    topoFile  := flag.String("topology", "", "...")
    outputDir := flag.String("output", "", "...")
    configletDir := flag.String("configlets", "", "...")
    flag.Parse()

    topo, err := labgen.LoadTopology(*topoFile)
    // ...
    labgen.GenerateStartupConfigs(topo, *configletDir, *outputDir)
    linkIPs := labgen.ComputeFabricLinkIPs(topo)
    labgen.GenerateFRRConfigs(topo, linkIPs, *outputDir)
    labgen.GenerateClabTopology(topo, *outputDir)
    labgen.GenerateLabSpecs(topo, *outputDir)
}
```

The configlet directory defaults to `<topoDir>/../../configlets` if it exists,
otherwise `./configlets`.

Generation order matters: `ComputeFabricLinkIPs` is called once and shared
between `GenerateStartupConfigs` (via its own internal call) and
`GenerateFRRConfigs` (via the returned `linkIPs` map).

### 3.2 Step 1: LoadTopology (pkg/labgen/parse.go)

```go
func LoadTopology(path string) (*Topology, error)
```

1. Reads the YAML file with `os.ReadFile`.
2. Unmarshals into `Topology` with `gopkg.in/yaml.v3`.
3. Calls `validateTopology`.

**validateTopology checks:**

| Check | Error condition |
|---|---|
| `topo.Name == ""` | Topology name is required |
| `len(topo.Nodes) == 0` | At least one node is required |
| `topo.Defaults.Image == ""` | defaults.image is required |
| `topo.Network.ASNumber == 0` | network.as_number is required |
| `topo.Network.Region == ""` | network.region is required |
| `node.Role == ""` | Node role is required |
| `node.Role not in {spine, leaf, server}` | Invalid role |
| Non-server node with `LoopbackIP == ""` | loopback_ip required for spine/leaf |
| Non-server node with unparseable `LoopbackIP` | Invalid loopback_ip (checked with `net.ParseIP`) |
| `len(link.Endpoints) != 2` | Link must have exactly 2 endpoints |
| Endpoint not in `"node:interface"` format | Invalid endpoint format |
| Endpoint references undefined node | Node not found in `topo.Nodes` |

**NodeInterfaces helper:**

```go
func NodeInterfaces(topo *Topology, nodeName string) []string
```

Iterates all links and collects unique interface names used by `nodeName`.
Returns a deduplicated slice (insertion order).

### 3.3 Step 2: GenerateStartupConfigs (pkg/labgen/configdb_gen.go)

```go
func GenerateStartupConfigs(topo *Topology, configletDir, outputDir string) error
```

For each non-server node (sorted alphabetically for deterministic MAC assignment):

1. **buildNodeConfigDB** -- Determine configlet list, build variable map, load/resolve/merge configlets.
2. **addPortEntries** -- Add PORT table entries for interfaces appearing in links, plus ensure Ethernet0-7 exist.
3. **addInterfaceEntries** -- Add INTERFACE table entries for fabric link IPs.
4. **Set DEVICE_METADATA MAC** -- `merged["DEVICE_METADATA"]["localhost"]["mac"] = nodeMAC(nodeIndex)`.
5. **Write** -- Marshal to JSON, write to `<outputDir>/<node>/config_db.json`.

**buildNodeConfigDB:**

```go
func buildNodeConfigDB(topo *Topology, nodeName string, node NodeDef, configletDir string) (map[string]map[string]map[string]string, error)
```

1. Picks configlet names from `node.Configlets` or `topo.RoleDefaults[node.Role]`.
2. Builds variable map via `buildVarsMap`.
3. For each configlet name: `configlet.LoadConfiglet` -> `configlet.ResolveConfiglet` -> `configlet.MergeConfigDB`.

**buildVarsMap:**

```go
func buildVarsMap(topo *Topology, nodeName string, node NodeDef) map[string]string
```

Builds the variable map from topology defaults and node overrides:

| Variable | Source |
|---|---|
| `device_name` | `nodeName` |
| `loopback_ip` | `node.LoopbackIP` |
| `router_id` | `node.LoopbackIP` |
| `as_number` | `topo.Network.ASNumber` (formatted as string) |
| `hwsku` | `topo.Defaults.HWSKU` |
| `platform` | `topo.Defaults.Platform` |
| `ntp_server_1` | `topo.Defaults.NTPServer1` |
| `ntp_server_2` | `topo.Defaults.NTPServer2` |
| `syslog_server` | `topo.Defaults.SyslogServer` |
| *(any)* | `node.Variables[k]` (overrides all above) |

**addPortEntries:**

```go
func addPortEntries(topo *Topology, nodeName string, configDB map[string]map[string]map[string]string)
```

- Adds PORT entries for all interfaces used in links (from `NodeInterfaces`).
- Ensures Ethernet0 through Ethernet7 exist (minimum 8 ports for E2E tests).
- Default port attributes: `admin_status: "up"`, `mtu: "9100"`, `speed: "40000"`.
- Existing PORT entries from configlets are not overwritten.

**addInterfaceEntries:**

```go
func addInterfaceEntries(linkIPs map[string]FabricLinkIP, configDB map[string]map[string]map[string]string)
```

For each fabric link IP assignment:
- Creates an interface-level entry: `INTERFACE[<iface>] = {}` (required by SONiC).
- Creates an IP-level entry: `INTERFACE[<iface>|<ip>/<prefix>] = {}`.

**nodeMAC:**

```go
func nodeMAC(index int) string
```

Returns `fmt.Sprintf("02:42:f0:ab:%02x:%02x", (index>>8)&0xff, index&0xff)`.
The `02:` prefix sets the locally-administered bit (IEEE LAA).

**ComputeFabricLinkIPs:**

```go
func ComputeFabricLinkIPs(topo *Topology) map[string]map[string]FabricLinkIP
```

Returns a nested map: `node -> interface -> FabricLinkIP`.
Iterates links sequentially, skipping server endpoints. Each link gets a /31
pair: `10.1.0.(linkIdx*2)` and `10.1.0.(linkIdx*2+1)`.

**SonicIfaceToClabIface:**

```go
func SonicIfaceToClabIface(sonicName string) string
```

Converts `"Ethernet0"` to `"eth1"` (0-based SONiC to 1-based containerlab).
Non-Ethernet names pass through unchanged.

### 3.4 Step 3: GenerateFRRConfigs (pkg/labgen/frr_gen.go)

```go
func GenerateFRRConfigs(topo *Topology, linkIPs map[string]map[string]FabricLinkIP, outputDir string) error
```

For each non-server node (sorted alphabetically):
- Leaf nodes: call `generateLeafFRR`.
- Spine nodes: call `generateSpineFRR`.
- Write to `<outputDir>/<node>/frr.conf`.

**generateLeafFRR:**

```go
func generateLeafFRR(topo *Topology, nodeName string, node NodeDef, linkIPs map[string]map[string]FabricLinkIP) string
```

Produces the following FRR configuration:

```
frr version 10.0.1
frr defaults traditional
hostname <nodeName>
log syslog informational
no zebra nexthop kernel enable
fpm address 127.0.0.1
no fpm use-next-hop-groups
no service integrated-vtysh-config
!
route-map RM_SET_SRC permit 10
 set src <loopback_ip>
exit
!
ip protocol bgp route-map RM_SET_SRC
!
ip nht resolve-via-default
ipv6 nht resolve-via-default
!
router bgp <as_number>
 bgp router-id <loopback_ip>
 bgp log-neighbor-changes
 bgp bestpath as-path multipath-relax
 no bgp default ipv4-unicast
 no bgp ebgp-requires-policy
 network <loopback_ip>/32
 neighbor SPINE peer-group
 neighbor SPINE remote-as <as_number>
 neighbor <spine_peer_ip> peer-group SPINE
 neighbor <spine_peer_ip> update-source <local_ip>
 !
 address-family ipv4 unicast
  redistribute connected
  neighbor SPINE activate
  maximum-paths 16
  maximum-paths ibgp 16
 exit-address-family
 !
 address-family l2vpn evpn
  neighbor SPINE activate
  advertise-all-vni
  advertise-default-gw
  advertise-svi-ip
 exit-address-family
exit
!
end
```

Spine peers are discovered by iterating the node's `linkIPs` and selecting
entries where the peer node has `role == "spine"`. Peers are sorted by name
for deterministic output.

**generateSpineFRR:**

```go
func generateSpineFRR(topo *Topology, nodeName string, node NodeDef, linkIPs map[string]map[string]FabricLinkIP) string
```

Key differences from leaf:
- Uses `bgp cluster-id <cluster_id>` (from `node.Variables["cluster_id"]`, fallback to router-id).
- Peer group name is `LEAF-PEERS` instead of `SPINE`.
- IPv4 unicast: adds `route-reflector-client` and `next-hop-self` for LEAF-PEERS.
- L2VPN EVPN: adds `route-reflector-client` for LEAF-PEERS but no `advertise-all-vni`.
- `maximum-paths 64` / `maximum-paths ibgp 64` (wider than leaf's 16).

### 3.5 Step 4: GenerateClabTopology (pkg/labgen/clab_gen.go)

```go
func GenerateClabTopology(topo *Topology, outputDir string) error
```

1. **resolveKind** -- Determines containerlab kind (`sonic-vm`, `sonic-vs`).
2. **buildSequentialIfaceMaps** -- For `sonic-vm`, builds per-node interface remapping.
3. **Node generation** -- Iterates sorted node names, creates `ClabNode` per node.
4. **Link generation** -- Translates SONiC interface names to containerlab names.
5. **Write** -- Marshals to YAML, writes to `<outputDir>/<name>.clab.yml`.

**resolveKind:**

```go
func resolveKind(topo *Topology) string
```

If `topo.Defaults.Kind` is set, returns it directly. Otherwise calls `kindFromImage`.

**kindFromImage:**

```go
func kindFromImage(image string) string
```

Returns `"sonic-vm"` if the lowercase image name contains `"vrnetlab"` or `"sonic-vm"`.
Otherwise returns `"sonic-vs"`.

**buildSequentialIfaceMaps:**

```go
func buildSequentialIfaceMaps(topo *Topology) map[string]map[string]string
```

For `sonic-vm` (QEMU), VM NICs are assigned sequentially regardless of SONiC
interface numbering. This function:

1. For each node, calls `NodeInterfaces` to get all used interfaces.
2. Sorts interfaces by numeric index (`sonicIfaceNum` extracts the number after `"Ethernet"`).
3. Maps them sequentially: `Ethernet0 -> eth1`, `Ethernet4 -> eth2`, etc.

This is critical because QEMU assigns NICs as eth1, eth2, eth3, ... and
containerlab must use these sequential names in link definitions.

Example mapping for a node using Ethernet0, Ethernet1, Ethernet4:

| SONiC interface | Sorted index | Clab interface |
|---|---|---|
| Ethernet0 | 0 | eth1 |
| Ethernet1 | 1 | eth2 |
| Ethernet4 | 2 | eth3 |

**Node generation logic by kind:**

| Kind | StartupConfig | Binds | CPU | Memory | Healthcheck | Env |
|---|---|---|---|---|---|---|
| `sonic-vm` | `<node>/config_db.json` | (none) | 2 | `6144mib` | start-period=600, interval=30, timeout=10, retries=3 | QEMU_ADDITIONAL_ARGS, USERNAME, PASSWORD |
| `sonic-vs` | (none) | `<node>/config_db.json:/etc/sonic/config_db.json:rw` | (none) | (none) | (none) | (none) |
| `linux` (server) | (none) | (none) | (none) | (none) | (none) | (none) |

**Link translation:**

For each link endpoint:
1. Server node endpoints pass through unchanged (Linux interface names like `eth1`).
2. For `sonic-vm`, uses the sequential iface map if available.
3. Fallback: `SonicIfaceToClabIface` (Ethernet0 -> eth1, simple +1 offset).

### 3.6 Step 5: GenerateLabSpecs (pkg/labgen/specs_gen.go)

```go
func GenerateLabSpecs(topo *Topology, outputDir string) error
```

Creates `specs/` and `specs/profiles/` directories, then generates four types
of spec files.

**generateNetworkSpec:**

Produces `specs/network.json` with:

| Field | Value |
|---|---|
| `version` | `"1.0"` |
| `lock_dir` | `"/tmp/newtron-lab-locks"` |
| `super_users` | `["labuser"]` |
| `user_groups` | `{"neteng": ["labuser"]}` |
| `permissions` | `{"all": ["neteng"]}` |
| `regions.<region>` | `{"as_number": <as>, "as_name": <region>}` |
| `filter_specs` | Pre-defined `customer-l3-in` and `customer-l3-out` |
| `ipvpn.customer-vpn` | `l3_vni: 10001`, RT = `<as>:10001` |
| `services.customer-l3` | L3 routed customer interface service |

**generateSiteSpec:**

Produces `specs/site.json`:

```go
spec := map[string]interface{}{
    "version": "1.0",
    "sites": map[string]interface{}{
        siteName: map[string]interface{}{
            "region":           topo.Network.Region,
            "route_reflectors": rrs,  // spine node names
        },
    },
}
```

`siteName` defaults to `topo.Defaults.Site`, falling back to `topo.Name + "-site"`.
Route reflectors are all nodes with `role == "spine"`, sorted alphabetically.

**generatePlatformsSpec:**

Produces `specs/platforms.json`:

```go
spec := map[string]interface{}{
    "version": "1.0",
    "platforms": map[string]interface{}{
        platformName: map[string]interface{}{
            "hwsku":         hwsku,           // default: "Force10-S6000"
            "description":   "Virtual SONiC platform for containerlab",
            "port_count":    32,
            "default_speed": "40000",
        },
    },
}
```

`platformName` defaults to `topo.Defaults.Platform` or `"vs-platform"`.

**generateProfiles:**

For each non-server node, produces `specs/profiles/<node>.json`:

```go
profile := map[string]interface{}{
    "mgmt_ip":     "PLACEHOLDER",
    "loopback_ip": node.LoopbackIP,
    "site":        siteName,
    "platform":    platform,
}
if node.Role == "spine" {
    profile["is_route_reflector"] = true
}
```

`mgmt_ip` is set to `"PLACEHOLDER"` because the management IP is not known
until containerlab assigns it at deploy time. The `lab_patch_profiles` step in
setup.sh replaces this with the real Docker-assigned IP.

---

## 4. Configlet System Details

### 4.1 LoadConfiglet (pkg/configlet/configlet.go)

```go
func LoadConfiglet(dir, name string) (*Configlet, error)
```

Reads `<dir>/<name>.json` and unmarshals into `Configlet`. The file must
be a valid JSON object with `name`, `config_db`, and optionally `description`,
`version`, `variables`, and `notes` fields.

### 4.2 ResolveConfiglet (pkg/configlet/resolve.go)

```go
func ResolveConfiglet(c *Configlet, vars map[string]string) map[string]map[string]map[string]string
```

Iterates every table, key, and field in `c.ConfigDB`:
- Both keys and values have `{{variable}}` placeholders replaced.
- The intermediate `map[string]interface{}` from JSON is converted to `map[string]string`.
- Returns a fully resolved 3-level map: `table -> key -> field -> value`.

**ResolveVariables:**

```go
func ResolveVariables(s string, vars map[string]string) string
```

Simple string replacement: for each `k -> v` in vars, replaces all occurrences
of `"{{k}}"` with `v`. No escaping, no nesting, no error on unresolved variables.

### 4.3 MergeConfigDB (pkg/configlet/resolve.go)

```go
func MergeConfigDB(base, overlay map[string]map[string]map[string]string) map[string]map[string]map[string]string
```

Three-level deep merge:

1. For each table in `overlay`, ensure the table exists in `base`.
2. For each key in the table, ensure the key exists in `base[table]`.
3. For each field, set `base[table][key][field] = overlay[table][key][field]`.

Fields from later configlets overwrite earlier ones. Keys and tables are
additive (never removed). This means a configlet can override specific fields
without losing other fields set by an earlier configlet.

---

## 5. Interface Mapping Details

### 5.1 SonicIfaceToClabIface

```go
func SonicIfaceToClabIface(sonicName string) string
```

Direct offset conversion:
- Input: `"Ethernet0"` -> extracts `0` -> returns `"eth1"` (0 + 1)
- Input: `"Ethernet4"` -> extracts `4` -> returns `"eth5"` (4 + 1)
- Input: `"eth1"` -> not prefixed with "Ethernet" -> returns `"eth1"` unchanged

This is the **fallback** mapping used for `sonic-vs` and when sequential
mapping is not applicable.

### 5.2 buildSequentialIfaceMaps

For `sonic-vm` (QEMU), the mapping is different. QEMU assigns NICs sequentially
as eth1, eth2, eth3, ... regardless of the SONiC Ethernet numbering. The
function:

1. Calls `NodeInterfaces(topo, nodeName)` to get all interfaces used by the node.
2. Sorts them numerically via `sonicIfaceNum` (extracts integer after "Ethernet").
3. Assigns `eth1` to the first, `eth2` to the second, etc.

**sonicIfaceNum:**

```go
func sonicIfaceNum(name string) int
```

Returns the integer parsed from the substring after `"Ethernet"`. Returns 0
for non-Ethernet names.

### 5.3 When Each Mapping Is Used

| Kind | Mapping | Reason |
|---|---|---|
| `sonic-vm` | `buildSequentialIfaceMaps` | QEMU assigns NICs sequentially; Ethernet0 and Ethernet4 both map to eth1 and eth2 if those are the only two ports used |
| `sonic-vs` | `SonicIfaceToClabIface` | Native container maps Ethernet N directly to eth(N+1) |
| `linux` (server) | None (passthrough) | Server interfaces use Linux names (e.g., `eth1`) directly |

---

## 6. FRR Configuration Details

### 6.1 Common Preamble (Both Roles)

Both leaf and spine FRR configs share:

```
frr version 10.0.1
frr defaults traditional
hostname <nodeName>
log syslog informational
no zebra nexthop kernel enable
fpm address 127.0.0.1
no fpm use-next-hop-groups
no service integrated-vtysh-config
!
route-map RM_SET_SRC permit 10
 set src <loopback_ip>
exit
!
ip protocol bgp route-map RM_SET_SRC
!
ip nht resolve-via-default
ipv6 nht resolve-via-default
```

- `fpm address 127.0.0.1`: Enables FPM for route installation to SONiC fpmsyncd.
- `no fpm use-next-hop-groups`: Disables next-hop groups in FPM (SONiC compatibility).
- `RM_SET_SRC`: Sets the source IP on BGP-learned routes to the loopback, ensuring
  return traffic uses the loopback as source.

### 6.2 Leaf BGP Configuration

```
router bgp <as_number>
 bgp router-id <loopback_ip>
 bgp log-neighbor-changes
 bgp bestpath as-path multipath-relax
 no bgp default ipv4-unicast
 no bgp ebgp-requires-policy
 network <loopback_ip>/32
 neighbor SPINE peer-group
 neighbor SPINE remote-as <as_number>
 neighbor <peer_ip> peer-group SPINE
 neighbor <peer_ip> update-source <local_ip>
 !
 address-family ipv4 unicast
  redistribute connected
  neighbor SPINE activate
  maximum-paths 16
  maximum-paths ibgp 16
 exit-address-family
 !
 address-family l2vpn evpn
  neighbor SPINE activate
  advertise-all-vni
  advertise-default-gw
  advertise-svi-ip
 exit-address-family
exit
```

Spine peers are discovered from `linkIPs[nodeName]` by checking
`topo.Nodes[lip.PeerNode].Role == "spine"`. The `update-source` is the local
end of the fabric /31 link.

### 6.3 Spine BGP Configuration

```
router bgp <as_number>
 bgp router-id <loopback_ip>
 bgp cluster-id <cluster_id>
 bgp log-neighbor-changes
 bgp bestpath as-path multipath-relax
 no bgp default ipv4-unicast
 no bgp ebgp-requires-policy
 network <loopback_ip>/32
 neighbor LEAF-PEERS peer-group
 neighbor LEAF-PEERS remote-as <as_number>
 neighbor <peer_ip> peer-group LEAF-PEERS
 neighbor <peer_ip> update-source <local_ip>
 !
 address-family ipv4 unicast
  redistribute connected
  neighbor LEAF-PEERS activate
  neighbor LEAF-PEERS route-reflector-client
  neighbor LEAF-PEERS next-hop-self
  maximum-paths 64
  maximum-paths ibgp 64
 exit-address-family
 !
 address-family l2vpn evpn
  neighbor LEAF-PEERS activate
  neighbor LEAF-PEERS route-reflector-client
 exit-address-family
exit
```

`cluster-id` defaults to `node.Variables["cluster_id"]` if set, otherwise
falls back to the router-id (loopback IP).

### 6.4 bgp suppress-fib-pending

Both `generateLeafFRR` and `generateSpineFRR` contain an explicit comment
explaining why `bgp suppress-fib-pending` is omitted:

> When config is loaded dynamically via "vtysh -f" (rather than read at FRR
> startup), the FIB notification for pre-existing connected routes (like
> loopbacks) is never received, causing "network" statement routes to be stuck
> as "Not advertised to any peer".

This is a known interaction between FRR's FIB notification mechanism and
dynamic config loading, specific to the containerlab use case.

---

## 7. Specs Generation Details

### 7.1 Profile Generation

```go
profile := map[string]interface{}{
    "mgmt_ip":     "PLACEHOLDER",
    "loopback_ip": node.LoopbackIP,
    "site":        siteName,
    "platform":    platform,
}
```

- `mgmt_ip` is always `"PLACEHOLDER"`. The real IP is patched by `lab_patch_profiles`
  in setup.sh after containerlab assigns Docker management IPs.
- `ssh_user` and `ssh_pass` are added by `lab_patch_profiles` from the clab YAML env.
- Spine nodes get `"is_route_reflector": true`.

### 7.2 Network Spec

Contains scaffolding for a complete newtron network definition:
- Region with AS number
- Pre-defined filter specs (`customer-l3-in`, `customer-l3-out`)
- Pre-defined IPVPN (`customer-vpn` with L3 VNI 10001)
- Pre-defined service (`customer-l3` as L3 routed interface)
- Empty policers, macvpn, prefix_lists (populated by tests or deployment)

### 7.3 Site Spec

- Site name from `topo.Defaults.Site` or `topo.Name + "-site"`.
- Route reflectors populated from spine node names (sorted).

### 7.4 Platform Spec

- Platform name from `topo.Defaults.Platform` or `"vs-platform"`.
- HWSKU from `topo.Defaults.HWSKU` or `"Force10-S6000"`.
- Fixed: `port_count: 32`, `default_speed: "40000"`.

---

## 8. Validation Rules Summary

The following table lists all validation rules in `validateTopology`:

| # | Field | Rule | Error message |
|---|---|---|---|
| 1 | `topo.Name` | Must be non-empty | `"topology name is required"` |
| 2 | `topo.Nodes` | Must have at least one entry | `"at least one node is required"` |
| 3 | `topo.Defaults.Image` | Must be non-empty | `"defaults.image is required"` |
| 4 | `topo.Network.ASNumber` | Must be non-zero | `"network.as_number is required"` |
| 5 | `topo.Network.Region` | Must be non-empty | `"network.region is required"` |
| 6 | `node.Role` | Must be non-empty | `"node <name>: role is required"` |
| 7 | `node.Role` | Must be `spine`, `leaf`, or `server` | `"node <name>: role must be 'spine', 'leaf', or 'server'"` |
| 8 | `node.LoopbackIP` (non-server) | Must be non-empty | `"node <name>: loopback_ip is required"` |
| 9 | `node.LoopbackIP` (non-server) | Must parse as valid IP | `"node <name>: invalid loopback_ip"` |
| 10 | `link.Endpoints` | Must have exactly 2 entries | `"link <i>: must have exactly 2 endpoints"` |
| 11 | Each endpoint | Must be in `"node:interface"` format | `"link <i>: endpoint must be in 'node:interface' format"` |
| 12 | Each endpoint | Node name must exist in `topo.Nodes` | `"link <i>: endpoint references undefined node"` |

---

## 9. Error Handling

All generators follow the same error propagation pattern:

1. Each generator function returns `error`.
2. Errors are wrapped with `fmt.Errorf("context: %w", err)` for chain-of-cause tracing.
3. `main.go` prints the error to stderr and exits with code 1.

Error wrapping chain example:

```
Error generating startup configs: node leaf1: loading configlet sonic-baseline: reading configlet sonic-baseline: open configlets/sonic-baseline.json: no such file or directory
```

Each layer adds context:
- `main.go`: "Error generating startup configs"
- `GenerateStartupConfigs`: "node leaf1"
- `buildNodeConfigDB`: "loading configlet sonic-baseline"
- `configlet.LoadConfiglet`: "reading configlet sonic-baseline"
- `os.ReadFile`: "open configlets/sonic-baseline.json: no such file or directory"

No panics are used. All error paths return structured errors that can be
traced back to the root cause.

---

## 10. Determinism Guarantees

labgen produces identical output for identical input:

| Mechanism | Where |
|---|---|
| Node names sorted alphabetically | `GenerateStartupConfigs`, `GenerateFRRConfigs`, `GenerateClabTopology`, `generateProfiles` |
| Links processed in definition order | `ComputeFabricLinkIPs`, link generation in `GenerateClabTopology` |
| Peer lists sorted by name | `generateLeafFRR`, `generateSpineFRR` |
| Interface lists sorted numerically | `buildSequentialIfaceMaps` |
| MAC assignment by sorted node index | `GenerateStartupConfigs` |

This is important for CI reproducibility: the same topology YAML always
produces byte-identical output files, making diffs meaningful and
artifact caching reliable.
