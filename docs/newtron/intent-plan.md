# Universal Intent Recording — Detailed Implementation Plan

This plan supersedes the intent-related sections of the Unified API +
Topology Format plan.

## Naming Conventions Applied

This plan applies several naming refinements driven by the principle that
**operation names must use the vocabulary operators work with** (spec terms,
not implementation details):

- **Baseline consolidation**: `set-device-metadata`, `setup-loopback`,
  `setup-bgp`, `setup-vtep`, `setup-route-reflector` collapse into a single
  `setup-device` operation. These are one conceptual activity: "set up this
  switch for the fabric." One step, one intent record.
- **`add-overlay-peer` -> `add-bgp-multihop-peer`**: Standard BGP
  terminology. "Multihop" is the FRR/Cisco term for loopback-to-loopback
  peering across intermediate hops — says exactly what it is.
- **`add-bgp-peer` -> `add-bgp-peer`** (interface-level): Simpler.
  "Peer" and "neighbor" are synonymous in BGP; interface context implies
  direct.
- **`create-acl-table` -> `create-acl`**: ACL_TABLE is a CONFIG_DB
  implementation detail. Operators create ACLs.
- **`configure-svi` -> `configure-irb`**: SVI is a SONiC implementation
  term. Operators configure IRB (the spec vocabulary).
- **`map-l2vni` -> `bind-macvpn`** (node-level): L2VNI is an implementation
  term. Operators bind a MAC-VPN spec to a VLAN. The same name exists at
  interface level (bind a port to a MAC-VPN); URL path and resource key
  disambiguate.

Result: **16 operations** (down from 21).

## Problem Statement

Today, only `ApplyService` writes NEWTRON_INTENT records. The other
operations (`SetupDevice`, `CreateVRF`, `AddBGPMultihopPeer`, etc.)
write CONFIG_DB entries with no provenance record.
This means:

- **Drift detection is blind to most operations.** If someone manually
  changes the BGP ASN or deletes a VTEP entry, newtron cannot detect it.
- **Reconstruction is impossible.** Reading NEWTRON_INTENT from a device
  returns only service bindings. The baseline (loopback, BGP, VTEP, overlay
  peers) and standalone resources (VRFs, VLANs) are invisible.
- **The round-trip is broken.** `topology.json steps -> provision ->
  NEWTRON_INTENT -> Snapshot() -> topology.json steps` only survives for
  service operations. Everything else is lost.
- **Crash recovery covers only services.** A crash mid-operation leaves
  partial CONFIG_DB state with no breadcrumb for recovery.

The design principles (§19, §20, §21) are explicit: every operation must
write an intent record sufficient for both teardown and reconstruction. This
plan implements that directive.

## Design Thesis

**A topology step and an intent record are the same thing in different
serialization formats.**

- Step form: `{url, params}` — JSON, structured `map[string]any`
- Intent form: `{resource, operation, state, params}` — Redis hash, flat
  `map[string]string`

Provisioning reads steps and writes intents. Reconstruction reads intents
and produces expected state using the same code path. One dispatcher
(`ReplayStep`), one conversion function (`IntentToStep`). Two sides of
the same coin.

## Core Design Decisions

### 1. Flat intent map — no parent/child

ApplyService creates VRFs, VLANs, BGP neighbors, ACLs internally via
`generateServiceEntries`. These are infrastructure created BY the service,
not independently managed resources. The service intent record tracks them
via its resolved params (`vrf_name`, `l3vni`, `vlan_id`). RemoveService
uses those params for teardown, scanning other intents before deleting
shared resources.

A standalone `create-vrf` step creates a VRF as an independently managed
resource with its own intent record. The two mechanisms don't conflict:
ApplyService checks for existing VRFs (idempotent), and RemoveService
checks for remaining consumers (reference-aware).

No parent/child linking, no nesting, no hierarchy. Each intent is
self-contained. The shared resource protection mechanism (intent scanning)
handles all ownership questions.

### 2. Every operation writes NEWTRON_INTENT as the first ChangeSet entry

Same pattern as existing ApplyService: the intent entry is the first
addition to the ChangeSet (write-ahead manifest). If the process crashes
after the intent is written but before CONFIG_DB entries are complete,
the intent survives as a recovery breadcrumb.

Reverse operations delete NEWTRON_INTENT as the last entry in the
ChangeSet. This ensures partial teardown can be re-run (the intent
persists until cleanup is complete).

### 3. Baseline operation has no individual reverse

`setup-device` is a device-lifetime operation. You never tear down BGP
from a fabric switch. Its intent enables reconstruction and drift detection.
Its "reverse" is reprovision (CompositeOverwrite) per §21: "Drift
remediation is reprovision."

Standalone resource operations (CreateVRF, CreateVLAN, etc.) have existing
reverse operations that will also delete intents.

### 4. One dispatcher, one conversion function

`ReplayStep(ctx, n, step)` is the single dispatcher for both provisioning
(from topology.json) and reconstruction (from device intents). To reconstruct
from intents, convert each intent record to a topology step first:

```
IntentToStep(resource, intent) -> TopologyStep    // flat -> structured
ReplayStep(ctx, n, step)       -> error           // the one dispatcher
```

No second dispatcher. One code path. Two sides of the same coin.

### 5. Static priority for reconstruction ordering

Intent records in NEWTRON_INTENT are unordered (Redis hash keys).
Reconstruction needs correct ordering because operations have preconditions
(BGP requires DEVICE_METADATA). A fixed priority per operation type follows
the natural provisioning sequence:

| Priority | Operations |
|----------|------------|
| 1 | setup-device |
| 2 | create-portchannel |
| 3 | set-property |
| 4 | add-bgp-multihop-peer |
| 5 | create-vrf |
| 6 | bind-ipvpn |
| 7 | create-vlan |
| 8 | bind-macvpn (node-level: VLAN-to-VNI overlay) |
| 9 | configure-irb |
| 10 | create-acl |
| 11 | configure-interface, add-bgp-peer |
| 12 | apply-service |
| 13 | add-static-route, bind-acl, apply-qos |

No complex dependency resolution. The priority is a static map.

### 6. Snapshot exports ALL actuated intents

Current `Snapshot()` exports only actuated service intents. After this
change, it exports ALL actuated intents as topology steps, ordered by
priority. This completes the round-trip:

```
topology.json steps
    -> provision (ReplayStep on abstract Node)
    -> NEWTRON_INTENT on device
    -> Snapshot()
    -> topology.json steps
```

Snapshot emits user params (what the operator requested), not resolved
params. Resolved values are re-derived from current specs at replay time.

## Operation Verb Vocabulary

The leading verb in each operation name communicates its lifecycle:

| Verb | Meaning | Reverse | Examples |
|------|---------|---------|----------|
| `setup-*` | Device-lifetime initialization. Done once at provisioning. No individual reverse — remediation is reprovision. | reprovision | setup-device |
| `set-*` | Field assignment on existing entry. Per-resource. | reprovision | set-property |
| `create-*` | Constructs a new named resource. | `delete-*` | create-vrf, create-vlan, create-portchannel, create-acl |
| `add-*` | Adds an instance to a collection. | `remove-*` | add-bgp-multihop-peer, add-bgp-peer, add-static-route |
| `bind-*` | Establishes a relationship between resources. | `unbind-*` | bind-ipvpn, bind-acl, bind-macvpn |
| `apply-*` | Applies a composite (service, QoS policy). | `remove-*` | apply-service, apply-qos |
| `configure-*` | Configures an existing resource. | `unconfigure-*` | configure-interface, configure-irb |

The verb tells you whether a reverse operation exists without looking it up:
- `setup-*` and `set-*` = no individual reverse
- Everything else = reverse exists (delete, remove, unbind, etc.)

## Complete Operation List (16 operations)

### Node-level (10)

| # | Operation | Resource key | Reverse |
|---|-----------|-------------|---------|
| 1 | `setup-device` | `device` | reprovision |
| 2 | `create-vrf` | `vrf\|{name}` | `delete-vrf` |
| 3 | `bind-ipvpn` | `ipvpn\|{vrf}` | `unbind-ipvpn` |
| 4 | `create-vlan` | `vlan\|{id}` | `delete-vlan` |
| 5 | `bind-macvpn` | `macvpn\|{vlan_id}` | `unbind-macvpn` |
| 6 | `create-acl` | `acl\|{name}` | `delete-acl` |
| 7 | `add-bgp-multihop-peer` | `multihop-peer\|{ip}` | `remove-bgp-multihop-peer` |
| 8 | `create-portchannel` | `portchannel\|{name}` | `delete-portchannel` |
| 9 | `configure-irb` | `irb\|{vlan_id}` | `unconfigure-irb` |
| 10 | `add-static-route` | `route\|{vrf}\|{prefix}` | `remove-static-route` |

### Interface-level (6)

| # | Operation | Resource key | Reverse |
|---|-----------|-------------|---------|
| 11 | `set-property` | `{intf}\|port\|{prop}` | reprovision |
| 12 | `configure-interface` | `{intf}\|configure` | `unconfigure-interface` |
| 13 | `add-bgp-peer` | `{intf}\|bgp\|{ip}` | `remove-bgp-peer` |
| 14 | `apply-service` | `{intf}\|service` | `remove-service` |
| 15 | `bind-acl` | `{intf}\|acl\|{dir}` | `unbind-acl` |
| 16 | `apply-qos` | `{intf}\|qos` | `remove-qos` |

Note: interface-level `bind-macvpn` (`{intf}|macvpn`) is NOT a standalone
operation — it exists only within ApplyService's internal flow. The node-level
`bind-macvpn` IS a standalone operation (creates the VLAN-to-VNI overlay mapping).

## Intent Param Catalog

Each intent stores both **user params** (for Snapshot/reconstruction) and
**resolved params** (for teardown). The intent record is the union. Snapshot
extracts user params; teardown reads resolved params.

### setup-device (resource: `device`)

The single baseline operation. Records all device initialization state.

```
operation         = setup-device
state             = actuated
# User params (from topology step):
hostname          = leaf1
bgp_asn           = 65001
source_ip         = 10.0.0.1       # VTEP source (optional, absent if no EVPN)
# Resolved params (from profile):
loopback_ip       = 10.0.0.1
asn               = 65001
router_id         = 10.0.0.1
# Optional route-reflector params (present only for RR devices):
cluster_id        = 10.0.0.1
local_asn         = 65001
local_addr        = 10.0.0.1
client_ips        = 10.0.0.11,10.0.0.12   # comma-separated
client_asns       = 65011,65012            # comma-separated
peer_ips          = 10.0.0.2               # comma-separated
peer_asns         = 65002                  # comma-separated
```

ReplayStep for `setup-device` calls internal config functions, in order:
1. `setDeviceMetadataConfig(fields)`
2. `configureLoopbackConfig()`
3. `configureBGPConfig(opts)` (with options derived from params)
4. `setupVTEPConfig(sourceIP)` — only if `source_ip` present
5. `configureRouteReflectorConfig(opts)` — only if `cluster_id` present

### add-bgp-multihop-peer (resource: `multihop-peer|10.0.0.2`)
```
operation       = add-bgp-multihop-peer
state           = actuated
neighbor_ip     = 10.0.0.2       # user
asn             = 65002           # user
evpn            = true            # user
description     = spine1          # user (optional)
```

### create-vrf (resource: `vrf|Vrf_TRANSIT`)
```
operation       = create-vrf
state           = actuated
name            = Vrf_TRANSIT     # user
```

### bind-ipvpn (resource: `ipvpn|Vrf_TRANSIT`)
```
operation       = bind-ipvpn
state           = actuated
vrf             = Vrf_TRANSIT     # user
ipvpn           = TRANSIT         # user (spec name)
l3vni           = 1001            # resolved (from spec)
l3vni_vlan      = 3001            # resolved (from spec)
```

### create-vlan (resource: `vlan|100`)
```
operation       = create-vlan
state           = actuated
vlan_id         = 100             # user
```

### bind-macvpn — node-level (resource: `macvpn|100`)

Creates VXLAN_TUNNEL_MAP + BGP_EVPN_VNI + ARP suppression for a VLAN.

```
operation       = bind-macvpn
state           = actuated
vlan_id         = 100             # user
macvpn          = CUSTOMER_L2     # user (spec name)
vni             = 10100           # resolved (from spec)
```

### create-portchannel (resource: `portchannel|PortChannel1`)
```
operation       = create-portchannel
state           = actuated
name            = PortChannel1    # user
members         = Ethernet0,Ethernet4   # user (comma-separated)
```

### create-acl (resource: `acl|PROTECT_RE`)
```
operation       = create-acl
state           = actuated
name            = PROTECT_RE      # user
type            = L3              # user
stage           = INGRESS         # user
ports           = Ethernet0,Ethernet4   # user (comma-separated)
```

### configure-irb (resource: `irb|100`)

Creates VLAN_INTERFACE + SAG + IP address for IRB routing on a VLAN.

```
operation       = configure-irb
state           = actuated
vlan_id         = 100             # user
vrf             = Vrf_CUSTOMER    # user
ip_address      = 10.10.0.1/24   # user
anycast_mac     = 00:11:22:33:44:55  # user (optional)
```

### apply-service (resource: `Ethernet0|service`)
```
operation       = apply-service
state           = actuated
service_name    = transit         # user
ip_address      = 10.1.1.1/30    # user
peer_as         = 65002           # user (optional)
service_type    = routed          # resolved
vrf_name        = Vrf_TRANSIT     # resolved
l3vni           = 1001            # resolved
vlan_id         = 100             # resolved (for bridging types)
route_map_in    = RM_IN_A1B2C3D4 # resolved
route_map_out   = RM_OUT_E5F6G7H8  # resolved
... (existing fields unchanged)
```

### configure-interface (resource: `Ethernet0|configure`)
```
operation       = configure-interface
state           = actuated
vrf             = Vrf_TRANSIT     # user (optional)
ip              = 10.1.100.1/24  # user (optional)
```

### add-bgp-peer (resource: `Ethernet0|bgp|10.1.1.2`)
```
operation       = add-bgp-peer
state           = actuated
neighbor_ip     = 10.1.1.2       # user
remote_as       = 65002           # user
description     = underlay peer  # user (optional)
```

### set-property (resource: `Ethernet0|port|mtu`)
```
operation       = set-property
state           = actuated
property        = mtu             # user
value           = 1500            # user
```

### bind-acl (resource: `Ethernet0|acl|ingress`)
```
operation       = bind-acl
state           = actuated
acl_name        = PROTECT_RE      # user
direction       = ingress         # user
```

### apply-qos (resource: `Ethernet0|qos`)
```
operation       = apply-qos
state           = actuated
policy          = PREMIUM         # user (spec name)
```

### add-static-route (resource: `route|Vrf_TRANSIT|10.0.0.0/8`)
```
operation       = add-static-route
state           = actuated
vrf             = Vrf_TRANSIT     # user
prefix          = 10.0.0.0/8     # user
next_hop        = 10.1.1.2       # user
metric          = 100             # user (optional)
```

## IntentToStep Conversion

Each operation defines a mapping from flat intent params to structured
step params. The URL is derived from resource key + operation:

- Node-level: URL = `/{operation}`
- Interface operations: URL = `/interface/{interface}/{operation}`

### Conversion rules per operation

**setup-device**: Extract user params only. Reconstruct `fields` map
from hostname + bgp_asn. Include `source_ip` if present. Include
route-reflector params if present (expand comma-separated to arrays).
```
Intent: hostname=leaf1, bgp_asn=65001, source_ip=10.0.0.1, asn=65001, ...
Step:   {"url": "/setup-device", "params": {
          "fields": {"hostname": "leaf1", "bgp_asn": "65001"},
          "source_ip": "10.0.0.1"
        }}
```

**add-bgp-multihop-peer**: All params are user params.
```
Intent: neighbor_ip=10.0.0.2, asn=65002, evpn=true
Step:   {"url": "/add-bgp-multihop-peer", "params": {"neighbor_ip": "10.0.0.2", "asn": 65002, "evpn": true}}
```

**apply-service**: Extract user params only (service_name -> service,
ip_address, peer_as). Skip resolved params (vrf_name, l3vni, etc.).
```
Intent: service_name=transit, ip_address=10.1.1.1/30, vrf_name=Vrf_TRANSIT, l3vni=1001, ...
Step:   {"url": "/interface/Ethernet0/apply-service", "params": {"service": "transit", "ip_address": "10.1.1.1/30"}}
```

**bind-ipvpn**: Extract user params (vrf, ipvpn spec name).
Skip resolved params (l3vni, l3vni_vlan).
```
Intent: vrf=Vrf_TRANSIT, ipvpn=TRANSIT, l3vni=1001, l3vni_vlan=3001
Step:   {"url": "/bind-ipvpn", "params": {"vrf": "Vrf_TRANSIT", "ipvpn": "TRANSIT"}}
```

**bind-macvpn** (node-level): Extract user params (vlan_id, macvpn spec name).
Skip resolved params (vni).
```
Intent: vlan_id=100, macvpn=CUSTOMER_L2, vni=10100
Step:   {"url": "/bind-macvpn", "params": {"vlan_id": 100, "macvpn": "CUSTOMER_L2"}}
```

**All other operations**: All params are user params — map directly (strings
and ints). No filtering needed.

## Shared Resource Protection

Reverse operations that delete shared CONFIG_DB resources must scan
NEWTRON_INTENT for remaining consumers before deletion.

### Current scan points

- **RemoveService** (service_ops.go): Scans for other service intents
  using the same VRF/VLAN before deleting shared infrastructure.

### Scan points to add

- **DeleteVRF**: Scan NEWTRON_INTENT for intents referencing the VRF name
  in their params. Refuses if consumers exist (service intents, bind-ipvpn,
  configure-interface with VRF binding, configure-irb with VRF binding).

- **DeleteVLAN**: Scan for service intents and standalone intents
  referencing the VLAN ID (bind-macvpn, configure-irb).

- **RemoveService** extension: Must also check standalone intents
  (`vrf|{name}`, `vlan|{id}`, `ipvpn|{vrf}`, `irb|{id}`, `macvpn|{id}`)
  before deleting shared resources.

- **RemoveQoS device-wide tables**: Scan for other intents using the same
  QoS policy before deleting DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER,
  WRED_PROFILE entries.

The scan mechanism is the same: iterate `n.configDB.NewtronIntent`,
check each record's params for the resource identifier. If any other
actuated intent references it, refuse deletion.

## Scenario Verification

### Scenario A: Two services share a VRF

1. `ApplyService(Ethernet0, transit)` -> intent `Ethernet0|service`,
   creates Vrf_TRANSIT
2. `ApplyService(Ethernet4, transit)` -> intent `Ethernet4|service`,
   Vrf_TRANSIT exists (idempotent skip)
3. `RemoveService(Ethernet0)` -> scans intents, `Ethernet4|service` still
   has `vrf_name=Vrf_TRANSIT` -> keep VRF
4. `RemoveService(Ethernet4)` -> scans intents, no consumers -> delete VRF

### Scenario B: Standalone VRF + service overlap

1. `/create-vrf Vrf_TRANSIT` -> intent `vrf|Vrf_TRANSIT`
2. `ApplyService(Ethernet0, transit)` -> intent `Ethernet0|service`, VRF
   exists (idempotent skip)
3. `RemoveService(Ethernet0)` -> scans intents, `vrf|Vrf_TRANSIT` exists
   -> keep VRF

### Scenario C: Drift detection on baseline config

1. Device provisioned with setup-device + service steps -> N NEWTRON_INTENT records
2. Someone manually changes BGP ASN via redis-cli
3. Reconstruction: replay intents on abstract Node -> expected CONFIG_DB
4. Diff against actual: DEVICE_METADATA bgp_asn mismatch -> drift detected

### Scenario D: Crash mid-setup-device

1. ChangeSet applying: NEWTRON_INTENT `device` written (first entry),
   BGP_GLOBALS partially written... crash
2. Next operator acquires lock, OperationIntent detected (zombie)
3. NEWTRON_INTENT `device` exists -> knows setup-device was attempted
4. Recovery: reprovision (CompositeOverwrite) restores correct state

### Scenario E: Snapshot round-trip

1. Provision device with topology steps
2. `Snapshot()` reads all NEWTRON_INTENT records
3. Converts each to topology step (user params only)
4. New device provisioned from snapshot -> identical CONFIG_DB

### Scenario F: Spec change after provisioning

1. Device has `apply-service transit` intent with `ip_address=10.1.1.1/30`
2. Transit spec updated: new ACL filter added
3. Reconstruction: replay intent with current spec -> expected CONFIG_DB
   includes new ACL entries
4. Diff: new ACL entries missing from actual -> drift detected
5. Remediation: reprovision or RefreshService

## Implementation Steps

### Phase 0: DRY Cleanup (prerequisite)

Universal intent recording adds more operations to the intent pipeline.
If we build on the current code, every existing DRY violation multiplies.
Fix first, then extend.

#### 0a. Eliminate dual data store (`n.intents` vs `configDB.NewtronIntent`)

**Problem**: Intent data exists in two in-memory representations: the
structured `n.intents` map (`map[string]*sonic.Intent`) and the flat
`n.configDB.NewtronIntent` (`map[string]map[string]string`). After
`applyShadow` writes to ConfigDB, `n.intents` is stale. Interface methods
read ConfigDB directly; Node methods read `n.intents`. Real consistency
bug in offline mode.

**Fix**: Delete `n.intents`. All intent accessors read from
`configDB.NewtronIntent` and construct `sonic.Intent` on demand:

```go
func (n *Node) GetIntent(resource string) *sonic.Intent {
    fields, ok := n.configDB.NewtronIntent[resource]
    if !ok { return nil }
    return sonic.NewIntent(resource, fields)
}
```

Delete `SetIntent`, `RemoveIntent`, `LoadIntents`. Intent state is
ConfigDB state — one source, one code path. `applyShadow` already
updates ConfigDB, so writes are automatically visible to reads.

Files: `node.go` (delete intent map + accessors), `interface.go`
(already reads ConfigDB, verify consistency), `intent_test.go` (update).

#### 0b. All intent construction goes through `sonic.NewIntent` + `ToFields`

**Problem**: ApplyService hand-assembles `bindingFields` as a raw
`map[string]string` with bare string literals (`"state"`, `"operation"`,
`"service_name"`). This bypasses `sonic.Intent.ToFields()`, producing
records with different structure (no timestamps, no nil-guarding).
RemoveService reads the raw flat map directly, bypassing `sonic.NewIntent`.

**Fix**: ApplyService constructs a `sonic.Intent` struct, calls
`ToFields()` to produce the flat map, and adds that to the ChangeSet.
RemoveService reads via `sonic.NewIntent(resource, fields)` to get the
structured type. One constructor, one serializer — all paths use them.

```go
intent := &sonic.Intent{
    Resource:  i.name + "|service",
    Operation: sonic.OpApplyService,
    State:     sonic.IntentActuated,
    Params:    bindingParams,  // includes service_name, ip_address, resolved fields
}
cs.Add("NEWTRON_INTENT", i.name+"|service", intent.ToFields())
```

Files: `service_ops.go` (ApplyService, RemoveService, RefreshService),
`sonic/configdb.go` (verify ToFields covers all fields).

#### 0c. Define constants for operation names and intent field names

**Problem**: Operation names (`"apply-service"`, `"configure-bgp"`) and
intent field names (`"service_name"`, `"vrf_name"`, `"ip_address"`) are
bare string literals scattered across 10+ files. A typo causes silent
data loss. Renaming requires global search-and-replace with no compiler
assistance.

**Fix**: Define constants in `sonic/configdb.go`:

```go
// Operation names (16 operations)
const (
    OpSetupDevice         = "setup-device"
    OpCreateVRF           = "create-vrf"
    OpBindIPVPN           = "bind-ipvpn"
    OpCreateVLAN          = "create-vlan"
    OpBindMACVPN          = "bind-macvpn"
    OpCreateACL           = "create-acl"
    OpAddBGPMultihopPeer  = "add-bgp-multihop-peer"
    OpCreatePortChannel   = "create-portchannel"
    OpSetProperty         = "set-property"
    OpConfigureIRB        = "configure-irb"
    OpConfigureInterface  = "configure-interface"
    OpAddBGPPeer      = "add-bgp-peer"
    OpApplyService        = "apply-service"
    OpAddStaticRoute      = "add-static-route"
    OpBindACL             = "bind-acl"
    OpApplyQoS            = "apply-qos"
)

// Intent param field names
const (
    FieldServiceName = "service_name"
    FieldServiceType = "service_type"
    FieldVRFName     = "vrf_name"
    FieldIPAddress   = "ip_address"
    FieldVLANID      = "vlan_id"
    FieldL3VNI       = "l3vni"
    FieldName        = "name"
    FieldNeighborIP  = "neighbor_ip"
    FieldVRF         = "vrf"
    FieldPrefix      = "prefix"
    // ... all shared fields
)
```

Replace all bare literals with constants. `IsService()` becomes
`i.Operation == OpApplyService`. Schema validation, ReplayStep dispatch,
intent construction, and teardown reads all reference the same constants.

Files: `sonic/configdb.go` (define constants), then all files that
reference operation or field names.

#### 0d. Shared URL construction for step-intent duality

**Problem**: `Snapshot()` constructs step URLs inline
(`"/interface/" + resource + "/apply-service"`). `parseStepURL()`
reverse-engineers them. The encode and decode halves don't share code.

**Fix**: Add `stepURL(op, interfaceName string) string` that both
Snapshot and IntentToStep use. `parseStepURL` remains the decoder.

```go
func stepURL(op, interfaceName string) string {
    if interfaceName != "" {
        return "/interface/" + interfaceName + "/" + op
    }
    return "/" + op
}
```

Files: `reconstruct.go` (add stepURL, use in IntentToStep),
`node.go` (Snapshot uses stepURL).

#### 0e. Clean up public API boundary dead code

**Problem**: `newtron.Intent` has phantom fields (`Phase`,
`RollbackHolder`, `RollbackStarted`, `Operations`) that
`intentFromSonic()` never populates. `newtron.IntentOperation` is dead
code. `IsService()`/`IsActuated()` are duplicated at both layers with
bare strings. `sonic.Intent.Name` is an ApplyService-specific field
that has no meaning for the other 15 operations — it duplicates
`Params["service_name"]`.

**Fix**: Remove phantom fields from public type. Remove
`newtron.IntentOperation` if unused by any consumer. Remove
`sonic.Intent.Name` — ApplyService stores the service name in
`Params[FieldServiceName]` (already present). `ToFields()` drops the
`name` field; `NewIntent()` drops it from parsing. Public `IsService()`
and `IsActuated()` should delegate to the constants defined in 0c (via
the raw cast to `sonic.IntentState`, which is already how the conversion
works).

Files: `types.go`, `node.go` (intentFromSonic), `sonic/configdb.go`
(Intent struct, ToFields, NewIntent).

#### 0f. Verify NEWTRON_INTENT schema handles variable params

**Problem**: Each of the 16 operations writes different param fields to
NEWTRON_INTENT. Schema validation (§13) is fail-closed — unknown fields
are errors. The schema must either enumerate all valid fields or use a
permissive pattern for intent params.

**Fix**: Permissive schema for NEWTRON_INTENT. Validate `operation`
(enum of 16 operation constants) and `state` (enum: `actuated`). Allow
all other fields without validation — params vary by operation type and
enumerating every valid field per operation would create a parallel
definition of the intent catalog. This is consistent with how the
existing schema already treats NEWTRON_INTENT in practice.

Files: `sonic/schema.go`, `sonic/schema_test.go`.

#### 0g. Rename methods to match operation names

**Problem**: Three method names diverge from their operation names.
The same-coin principle demands that `add-bgp-multihop-peer` maps to
`AddBGPMultihopPeer` and its reverse `remove-bgp-multihop-peer` maps to
`RemoveBGPMultihopPeer`. Currently the method names don't match:

| Operation | Expected method | Current method |
|-----------|----------------|----------------|
| `set-property` | `i.SetProperty()` | `i.Set()` |
| `remove-bgp-multihop-peer` | `n.RemoveBGPMultihopPeer()` | `n.RemoveBGPPeer()` |
| `unconfigure-irb` | `n.UnconfigureIRB()` | ~~`n.RemoveIRB()`~~ (renamed) |

**Fix**: Rename all three methods and update all call sites (CLI commands,
client methods, API handlers, tests). Mechanical — one rename per method,
no logic changes.

Files: `interface_ops.go` (`Set` → `SetProperty`), `bgp_ops.go`
(`RemoveBGPPeer` → `RemoveBGPMultihopPeer`), `vlan_ops.go`
(`UnconfigureIRB` — already renamed), plus all callers in `cmd/newtron/`,
`pkg/newtron/client/`, `pkg/newtron/api/`, and tests.

### Phase 1: Universal intent recording

With Phase 0 complete, intent construction follows a single pattern:
create `sonic.Intent`, call `ToFields()`, add to ChangeSet. All field
names are constants. All reads go through `configDB.NewtronIntent`.

#### Step 1: Resource key helpers

Add a function that derives the NEWTRON_INTENT key for each operation.

File: `pkg/newtron/network/node/reconstruct.go`

```go
func resourceKey(op string, params map[string]string) string {
    switch op {
    case sonic.OpSetupDevice:        return "device"
    case sonic.OpCreateVRF:          return "vrf|" + params[sonic.FieldName]
    case sonic.OpCreateVLAN:         return "vlan|" + params[sonic.FieldVLANID]
    case sonic.OpCreatePortChannel:  return "portchannel|" + params[sonic.FieldName]
    case sonic.OpCreateACL:          return "acl|" + params[sonic.FieldName]
    case sonic.OpConfigureIRB:       return "irb|" + params[sonic.FieldVLANID]
    case sonic.OpBindMACVPN:         return "macvpn|" + params[sonic.FieldVLANID]
    case sonic.OpBindIPVPN:          return "ipvpn|" + params[sonic.FieldVRF]
    case sonic.OpAddBGPMultihopPeer: return "multihop-peer|" + params[sonic.FieldNeighborIP]
    case sonic.OpAddStaticRoute:     return "route|" + params[sonic.FieldVRF] + "|" + params[sonic.FieldPrefix]
    }
    return ""
}
```

Interface operations derive keys in each method from the interface name.

#### Step 2: Implement setup-device as a consolidated operation

Create a new `SetupDevice` method on Node that calls the underlying
config functions directly — NOT the intent-recording Node methods.
`SetupDevice` writes a single NEWTRON_INTENT entry for the whole
composite; the sub-operations (SetDeviceMetadata, ConfigureLoopback,
ConfigureBGP, SetupVTEP, ConfigureRouteReflector) do NOT write their
own intent entries. This avoids the double-intent problem where
SetupDevice would produce 6 intent records instead of 1.

The sub-operations become unexported config functions
(`setDeviceMetadataConfig`, `configureLoopbackConfig`,
`configureBGPConfig`, `setupVTEPConfig`,
`configureRouteReflectorConfig`). They are not public methods and
have no API endpoints — `SetupDevice` is the only entry point.
A switch with BGP but no loopback is broken; these are not independent
operations.

```go
func (n *Node) SetupDevice(ctx context.Context, opts SetupDeviceOpts) (*ChangeSet, error) {
    // 1. Write NEWTRON_INTENT "device" (first entry, write-ahead)
    // 2. Call config functions directly (not intent-recording methods):
    //    - setDeviceMetadataConfig(opts.Fields)
    //    - configureLoopbackConfig()
    //    - configureBGPConfig(opts.BGPOpts)
    //    - setupVTEPConfig(opts.SourceIP) — if opts.SourceIP != ""
    //    - configureRouteReflectorConfig(opts.RR) — if opts.RR != nil
    // 3. Merge all entries into one ChangeSet, return
}
```

Files:
- `baseline_ops.go`: `SetupDevice` (new), `setDeviceMetadataConfig`,
  `configureLoopbackConfig`, `configureBGPConfig`, `setupVTEPConfig`,
  `configureRouteReflectorConfig` (demoted from public methods)
- `node.go`: Remove public `SetDeviceMetadata`, `ConfigureLoopback`,
  `ConfigureBGP`, `SetupVTEP`, `ConfigureRouteReflector`
- `pkg/newtron/node.go`: Remove public API wrappers for the 5 methods;
  add `SetupDevice` wrapper
- `pkg/newtron/api/handler_node.go`: Remove 5 handlers; add
  `handleSetupDevice`
- `pkg/newtron/api/handler.go`: Remove 5 route registrations; add
  `POST .../setup-device`
- `pkg/newtron/client/node.go`: Remove 5 client methods; add
  `SetupDevice`
- `cmd/newtron/`: Update CLI to route through SetupDevice

#### Step 3: Add intent recording to each remaining operation

Each operation calls centralized helpers in `intent_ops.go` to write
NEWTRON_INTENT as the first ChangeSet entry. This satisfies §27
(single-owner): `intent_ops.go` is the sole owner of NEWTRON_INTENT
construction. No `*_ops.go` file touches the table directly.

```go
// intent_ops.go — sole owner of NEWTRON_INTENT writes (§27)
func (n *Node) writeIntent(cs *ChangeSet, op, resource string, params map[string]string) {
    intent := &sonic.Intent{
        Resource:  resource,
        Operation: op,
        State:     sonic.IntentActuated,
        Params:    params,
    }
    cs.Add("NEWTRON_INTENT", resource, intent.ToFields())
}

func (n *Node) deleteIntent(cs *ChangeSet, resource string) {
    cs.Delete("NEWTRON_INTENT", resource)
}
```

Each operation calls the helper:

```go
// vrf_ops.go
func (n *Node) CreateVRF(ctx context.Context, name string) (*ChangeSet, error) {
    // ... existing precondition checks ...
    cs := NewChangeSet()
    n.writeIntent(cs, sonic.OpCreateVRF, "vrf|"+name, map[string]string{"name": name})
    // ... existing CONFIG_DB entries ...
    return cs, n.op(ctx, cs, ...)
}
```

**Signature changes required** (§33: operations accept names, resolve specs internally):

- `BindIPVPN(ctx, vrfName string, ipvpnDef *spec.IPVPNSpec)` →
  `BindIPVPN(ctx, vrfName, ipvpnName string)`. Resolves `ipvpnDef` internally
  via `n.GetIPVPN(ipvpnName)`. Enables writing user param `ipvpn = <name>` to
  the intent record (§22 dual-purpose: name for Snapshot, resolved L3VNI/RTs
  for teardown).

- `BindMACVPN(ctx, vlanID, vni int)` →
  `BindMACVPN(ctx, vlanID int, macvpnName string)`. Resolves VNI internally
  via `n.GetMACVPN(macvpnName)`. Enables writing user param `macvpn = <name>`
  to the intent record. Callers (ApplyService, topology steps) already know the
  spec name — passing pre-resolved VNI was an abstraction leak.

Files modified (all 16 operations):
- `intent_ops.go`: `writeIntent`, `deleteIntent` helpers (new)
- `baseline_ops.go`: SetupDevice (migrate inline NEWTRON_INTENT write to `writeIntent`)
- `vrf_ops.go`: CreateVRF, BindIPVPN (+ signature change), AddStaticRoute
- `vlan_ops.go`: CreateVLAN, ConfigureIRB
- `evpn_ops.go`: (SetupVTEP absorbed into setup-device)
- `macvpn_ops.go`: BindMACVPN (node-level, + signature change)
- `portchannel_ops.go`: CreatePortChannel
- `acl_ops.go`: CreateACL, BindACL (interface-level)
- `interface_ops.go`: ConfigureInterface, SetProperty
- `interface_bgp_ops.go`: AddBGPPeer
- `bgp_ops.go`: AddBGPMultihopPeer
- `qos_ops.go`: ApplyQoS
- `service_ops.go`: ApplyService (migrate from direct `cs.Add` to `writeIntent`)

#### Step 4: Add intent deletion to reverse operations

Each reverse operation calls `deleteIntent` as the last ChangeSet entry:

```go
func (n *Node) DeleteVRF(ctx context.Context, name string) (*ChangeSet, error) {
    // ... existing scan + deletion logic ...
    n.deleteIntent(cs, "vrf|"+name)
    return cs, n.op(ctx, cs, ...)
}
```

Files modified:
- `vrf_ops.go`: DeleteVRF, UnbindIPVPN, RemoveStaticRoute
- `vlan_ops.go`: DeleteVLAN, UnconfigureIRB
- `macvpn_ops.go`: UnbindMACVPN
- `portchannel_ops.go`: DeletePortChannel
- `acl_ops.go`: DeleteACL, UnbindACL (interface-level)
- `interface_ops.go`: UnconfigureInterface
- `interface_bgp_ops.go`: RemoveBGPPeer
- `bgp_ops.go`: RemoveBGPMultihopPeer
- `qos_ops.go`: RemoveQoS
- `service_ops.go`: RemoveService (already deletes — migrate to constants)

`setup-device` has no individual reverse. Its intent is replaced during
reprovision (CompositeOverwrite).

#### Step 5: IntentToStep conversion

Convert flat intent record to structured topology step. Uses `stepURL()`
from Phase 0d and constants from Phase 0c.

File: `pkg/newtron/network/node/reconstruct.go`

```go
func IntentToStep(resource string, fields map[string]string) spec.TopologyStep {
    intent := sonic.NewIntent(resource, fields)
    op := intent.Operation
    iface, _ := splitInterfaceResource(resource)
    step := spec.TopologyStep{
        URL:    stepURL(op, iface),
        Params: intentParamsToStepParams(op, intent.Params),
    }
    return step
}
```

`intentParamsToStepParams` is per-operation: extracts user params,
converts types (string->int, comma-separated->array).

#### Step 6: Extend Snapshot to export all intents

`Snapshot()` reads `configDB.NewtronIntent` (the single source from
Phase 0a), converts each actuated intent via `IntentToStep`, sorts by
static priority.

File: `pkg/newtron/network/node/node.go`

#### Step 7: Reconstruction function

```go
func ReconstructExpected(ctx context.Context, sp SpecProvider,
    name string, profile *spec.DeviceProfile,
    resolved *spec.ResolvedProfile,
    intents map[string]map[string]string,
    ports map[string]map[string]string) (*Node, error) {

    n := NewAbstract(sp, name, profile, resolved)
    for portName, fields := range ports {
        n.RegisterPort(portName, fields)
    }
    steps := intentsToSteps(intents) // IntentToStep + sort by priority
    for _, step := range steps {
        if err := ReplayStep(ctx, n, step); err != nil {
            return nil, fmt.Errorf("reconstruct %s: %w", step.URL, err)
        }
    }
    return n, nil
}
```

Note: `intents` parameter is `map[string]map[string]string` — raw
ConfigDB data. No `sonic.Intent` objects passed across boundaries.
`IntentToStep` handles the conversion internally.

#### Step 8: Tests

Each operation needs a test verifying:
1. The operation writes a NEWTRON_INTENT entry with correct resource key
2. The reverse operation (if any) deletes the NEWTRON_INTENT entry
3. `IntentToStep` round-trip: intent -> step -> replay -> same CONFIG_DB

Round-trip integration test: provision an abstract node with topology
steps, `Snapshot()`, provision a second abstract node from the snapshot,
compare CONFIG_DB tables (all tables, including NEWTRON_INTENT itself).

#### Step 9: Shared resource scan updates

Extend intent scanning in reverse operations to account for all intent
types. All scans read `configDB.NewtronIntent` (the single source).

File: `vrf_ops.go`, `vlan_ops.go`, `qos_ops.go`

## Verification Criteria

1. `go build ./... && go vet ./... && go test ./...` passes after each step
2. `n.intents` map no longer exists — single source is `configDB.NewtronIntent`
3. Zero bare string literals for operation names or intent field names
4. All intent construction goes through `sonic.Intent` + `ToFields()`
5. All intent reads go through `sonic.NewIntent(resource, fields)`
6. Every operation writes exactly one NEWTRON_INTENT entry
7. Every reverse operation deletes its NEWTRON_INTENT entry
8. `Snapshot()` on a fully provisioned abstract node returns steps that,
   when replayed on a fresh abstract node, produce identical CONFIG_DB
9. Shared resource deletion scans all intent types
10. Every method name matches its operation name (§16, §32)

## What This Plan Does NOT Do

- **No new reverse operations for baselines.** Remediation is reprovision.
- **No parent/child intent linking.** Flat map, self-contained intents.
- **No param conversion layer.** `IntentToStep` is per-operation, not a
  generic flattener.
- **No changes to OperationIntent** (crash recovery mechanism). It remains
  a separate mechanism from NEWTRON_INTENT. OperationIntent detects
  crashes (via lock acquisition); NEWTRON_INTENT identifies what was
  being modified.
- **No changes to the ReplayStep dispatcher pattern.** The switch
  statement handles both provisioning and reconstruction. Cases updated
  to match the 16 operations (setup-device replaces 5 former cases).
- **ApplyService's entry generation is unchanged.** It continues to call
  config functions directly via `generateServiceEntries`. It adopts the
  centralized `writeIntent` helper and `{intf}|service` resource key
  (Phase 0b + Phase 1), but the service-to-entries flow is not modified.
- **Intent entries participate in the ChangeSet pipeline by construction**
  — schema validation (§13), dry-run preview (§12), verification (§14),
  and offline shadow updates all apply automatically.
- **Offline mode writes intents to shadow ConfigDB** via `applyShadow`,
  which already processes all ChangeSet entries including NEWTRON_INTENT.
  `BuildComposite` exports them. No special handling needed.

## ReplayStep Dispatcher Update

The ReplayStep switch statement updates to match the 16 operations:

```go
switch op {
case "setup-device":
    return n.SetupDevice(ctx, parseSetupDeviceOpts(p))
case "add-bgp-multihop-peer":
    return n.AddBGPMultihopPeer(ctx, ...)
case "create-vrf":
    return n.CreateVRF(ctx, paramString(p, "name"))
case "bind-ipvpn":
    return n.BindIPVPN(ctx, ...)
case "create-vlan":
    return n.CreateVLAN(ctx, paramInt(p, "vlan_id"))
case "bind-macvpn":
    // Node-level only: VLAN-to-VNI overlay mapping.
    // Interface-level bind-macvpn is not a standalone operation —
    // it exists only within ApplyService's internal flow.
    return n.BindMACVPN(ctx, paramInt(p, "vlan_id"), paramString(p, "macvpn"))
case "create-portchannel":
    return n.CreatePortChannel(ctx, ...)
case "create-acl":
    return n.CreateACL(ctx, ...)
case "configure-irb":
    return n.ConfigureIRB(ctx, ...)
case "set-property":
    iface, err := n.GetInterface(ifaceName)
    if err != nil { return err }
    return iface.SetProperty(ctx, ...)
case "configure-interface":
    iface, err := n.GetInterface(ifaceName)
    if err != nil { return err }
    return iface.ConfigureInterface(ctx, ...)
case "add-bgp-peer":
    iface, err := n.GetInterface(ifaceName)
    if err != nil { return err }
    return iface.AddBGPPeer(ctx, ...)
case "apply-service":
    iface, err := n.GetInterface(ifaceName)
    if err != nil { return err }
    return iface.ApplyService(ctx, ...)
case "add-static-route":
    return n.AddStaticRoute(ctx, ...)
case "bind-acl":
    iface, err := n.GetInterface(ifaceName)
    if err != nil { return err }
    return iface.BindACL(ctx, ...)
case "apply-qos":
    iface, err := n.GetInterface(ifaceName)
    if err != nil { return err }
    return iface.ApplyQoS(ctx, ...)
default:
    return fmt.Errorf("unknown operation: %s", op)
}
```

Note: `bind-macvpn` is node-level only. Interface-level MAC-VPN binding
is an internal detail of ApplyService, not a standalone step or intent.
