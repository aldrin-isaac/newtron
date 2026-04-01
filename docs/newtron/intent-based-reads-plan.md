# Plan: Intent-Based Reads — Eliminate Projection Reads from Operational Logic

## Context

Architecture §1 ("Intent DB is the decision substrate") states:

> All operational logic reads the intent DB — not the projection. The projection
> exists for exactly two purposes: device delivery and drift detection. No
> operational decision reads the projection.

The precondition layer already obeys this — all `Require*` methods use
`GetIntent()`. But query methods (`GetVLAN`, `GetVRF`, etc.), reference
counting (`isQoSPolicyReferenced`, SAG_GLOBAL check), membership queries
(`InterfaceIsPortChannelMember`), config-time decisions (`BGPNeighborExists`,
`BGPPeerGroup` check), and health checks (`CheckBGPSessions`) still read
configDB typed tables (the projection).

This plan converts all remaining operational reads to use intent records,
completing the architectural principle. The result: SONiC CONFIG_DB
knowledge is contained to exactly two places — config generators (forward
path) and schema validation (render path).

## Design Approach

### Intent Query Helpers

Add helper methods to Node that scan the intent DB by prefix and by param.
These are the primitives that query/display and reference counting methods
will use:

```go
// IntentsByPrefix returns all intents whose resource starts with prefix.
// Example: IntentsByPrefix("vlan|") → all VLAN intents.
func (n *Node) IntentsByPrefix(prefix string) map[string]*IntentRecord

// IntentsByParam returns intents where params[key] == value.
// Example: IntentsByParam("vrf", "CUSTOMER") → intents in that VRF.
func (n *Node) IntentsByParam(key, value string) map[string]*IntentRecord

// IntentsByOp returns intents with the given operation type.
// Example: IntentsByOp("bind-macvpn") → all MAC-VPN bindings.
func (n *Node) IntentsByOp(op string) map[string]*IntentRecord
```

These scan `n.configDB.NewtronIntent` — the intent DB, not the projection
tables. They are O(n) over intents, which is small (dozens, not thousands).

### Intent Params Completeness

Every intent already stores the params needed for reconstruction (Intent
Round-Trip Completeness rule in CLAUDE.md). This means query methods can
build their response entirely from intent params:

| Query | Intent source | Params used |
|-------|--------------|-------------|
| GetVLAN(100) existence | `GetIntent("vlan\|100")` | `vlan_id` |
| GetVLAN(100) description | `GetIntent("vlan\|100")` | `description` |
| GetVLAN(100) members | `IntentsByParam("vlan_id","100")` filtered to service intents | interface names from resource key |
| GetVLAN(100) L2VNI | `GetIntent("macvpn\|100")` | `vni` |
| GetVLAN(100) ARP suppression | `GetIntent("macvpn\|100")` | `arp_suppression` |
| GetVLAN(100) IRB | `GetIntent("interface\|Vlan100")` | `ip_address`, `vrf` |
| GetVRF("CUSTOMER") L3VNI | `GetIntent("ipvpn\|CUSTOMER")` | `l3vni` |
| GetVRF("CUSTOMER") interfaces | `IntentsByParam("vrf","CUSTOMER")` | interface names |
| PortChannel members | `IntentsByPrefix("portchannel\|PC100\|")` | member names |
| Interface VRF | `GetIntent("interface\|Eth0")` | `vrf` |
| Interface IP | `GetIntent("interface\|Eth0")` | `intf_ip` or `ip_address` |
| QoS policy consumers | `IntentsByPrefix("interface\|")` + filter `\|qos` suffix | `qos_policy` |
| ACL port list | `IntentsByPrefix("interface\|")` + filter `\|acl\|` | `acl_name` → interface names |

### Categories of Change

**A. Domain queries → intent reads** (functions that build CLI/API responses):

| Function | File:Line | Current source | Intent replacement |
|----------|-----------|---------------|-------------------|
| `GetVLAN` | vlan_ops.go:330 | configDB.VLAN, VLANMember, VLANInterface, VXLANTunnelMap, SuppressVLANNeigh | GetIntent + IntentsByPrefix for members/macvpn/IRB |
| `ListVLANs` | vlan_ops.go:392 | configDB.VLAN iteration | IntentsByPrefix("vlan\|") |
| `GetVRF` | vrf_ops.go:437 | configDB.VRF, Interface, VLANInterface | GetIntent + IntentsByParam("vrf", name) |
| `ListVRFs` | vrf_ops.go:482 | configDB.VRF iteration | IntentsByPrefix("vrf\|") |
| `GetPortChannel` | portchannel_ops.go:206 | configDB.PortChannel, PortChannelMember | GetIntent + IntentsByPrefix("portchannel\|{name}\|") |
| `ListPortChannels` | portchannel_ops.go:236 | configDB.PortChannel iteration | IntentsByPrefix("portchannel\|") filtered to top-level |

**B. Interface property queries → intent reads** (intent-derived properties):

| Function | File:Line | Current source | Intent replacement |
|----------|-----------|---------------|-------------------|
| `Interface.VRF` | interface.go:126 | configDB.Interface | GetIntent("interface\|{name}") → params["vrf"] |
| `Interface.IPAddresses` | interface.go:138 | configDB.Interface scan | GetIntent("interface\|{name}") → params["intf_ip"] or params["ip_address"] |
| `Interface.PortChannelParent` | interface.go:241 | configDB.PortChannelMember scan | IntentsByPrefix("portchannel\|") scan for member match |
| `Interface.PortChannelMembers` | interface.go:271 | configDB.PortChannelMember scan | GetIntent("portchannel\|{name}") → params["members"] |
| `Interface.VLANMembers` | interface.go:291 | configDB.VLANMember scan | IntentsByParam("vlan_id", vlanID) filtered to service intents |
| `Interface.BGPNeighbors` | interface.go:310 | configDB.BGPNeighbor scan | IntentsByPrefix("interface\|{name}\|bgp-peer") or IntentsByPrefix("evpn-peer\|") |
| `Interface.IngressACL` | interface.go:189 | intent + configDB.ACLTable fallback | intent only — remove configDB fallback |
| `Interface.EgressACL` | interface.go:211 | intent + configDB.ACLTable fallback | intent only — remove configDB fallback |

**C. Reference counting → intent scans** (shared resource lifecycle):

| Function | File:Line | Current source | Intent replacement |
|----------|-----------|---------------|-------------------|
| SAG_GLOBAL in `UnconfigureIRB` | vlan_ops.go:276 | configDB.VLANInterface scan | IntentsByOp("configure-irb") filtered for anycast_mac param |
| `isQoSPolicyReferenced` | qos.go:163 | configDB.PortQoSMap scan | IntentsByPrefix("interface\|") with "\|qos" suffix, check qos_policy param |
| ACL port list in `generateServiceEntries` | service_ops.go:509,528 | configDB.ACLTable[name].Ports | Scan acl binding intents → collect interface names → build ports CSV |
| ACL port update in `UnbindACL` | interface_ops.go:411 | configDB.ACLTable[name].Ports | Same: scan acl binding intents → collect remaining interfaces |

**D. Config-time decisions → intent checks** (idempotency/existence):

| Function | File:Line | Current source | Intent replacement |
|----------|-----------|---------------|-------------------|
| BGPPeerGroup in `SetupVTEP` | evpn_ops.go:238 | configDB.BGPPeerGroup[key] | Remove check — make peer group creation unconditional (render upserts safely). SetupDevice guards idempotency via `GetIntent("device")`; SetupVTEP is its sub-operation and writes no "evpn" intent in production code. |
| BGPPeerGroup in `AddBGPEVPNPeer` | bgp_ops.go:450 | configDB.BGPPeerGroup[key] | `GetIntent("device") != nil` with `params["source_ip"] != ""` — SetupDevice with source_ip always creates the peer group via SetupVTEP. |
| `BGPNeighborExists` | bgp_ops.go:364 | configDB.HasBGPNeighbor | `GetIntent("evpn-peer\|" + ip) != nil` for overlay; scan `IntentsByPrefix("interface\|")` for `\|bgp-peer` children matching the IP for underlay |
| `BGPConfigured` | bgp_ops.go:331 | configDB.BGPConfigured() | GetIntent("device") — BGP globals created by SetupDevice |
| `VTEPSourceIP` | evpn_ops.go:33 | configDB.VXLANTunnel scan | GetIntent("device") → params["source_ip"], fallback to resolved.LoopbackIP (no "evpn" intent exists in production) |
| `DeleteACLRule` existence | acl_ops.go:267 | configDB.ACLRule check | GetIntent("acl\|{table}\|{rule}") |
| `removeSharedACL` existence | service_ops.go:1061 | configDB.ACLTable[aclName] | Redundant — line 1066 already does `GetIntent("acl\|" + aclName)`. Remove projection check. |
| `InterfaceExists` | interface_ops.go:14 | configDB.HasInterface (Port/PortChannel/VLAN) | Physical: `n.interfaces[name]` (RegisterPort map). PortChannel: `GetIntent("portchannel\|" + name)`. VLAN SVI: `GetIntent("vlan\|" + id)`. |

**E. Membership checks → intent scans** (used by preconditions):

| Function | File:Line | Current source | Intent replacement |
|----------|-----------|---------------|-------------------|
| `InterfaceIsPortChannelMember` | portchannel_ops.go:250 | configDB.PortChannelMember scan | Scan portchannel member intents for this interface |
| `GetInterfacePortChannel` | portchannel_ops.go:262 | configDB.PortChannelMember scan | Same scan, return PortChannel name |

**F. Health checks → intent-based expected set:**

| Function | File:Line | Current source | Intent replacement |
|----------|-----------|---------------|-------------------|
| `CheckBGPSessions` | health_ops.go:42 | configDB.BGPNeighbor scan | IntentsByPrefix("evpn-peer\|") + IntentsByPrefix("interface\|") with bgp-peer children |

**G. Cleanup/teardown → deterministic from intents + specs:**

| Function | File:Line | Current source | Intent replacement |
|----------|-----------|---------------|-------------------|
| `unbindQos` | qos_ops.go:52 | configDB.Queue/PortQoSMap scan | Deterministic: policy spec defines queue count → generate QUEUE keys from interface name + indices; PORT_QOS_MAP key = interface name |
| `deleteDeviceQoSConfig` | qos.go:139 | configDB.Scheduler/WREDProfile scan | Deterministic: policy spec defines queues → generate SCHEDULER keys from policy name + indices; WRED key = policyName + "_ECN" |
| `RemoveLoopback` | baseline_ops.go:176 | configDB.LoopbackInterface scan | Deterministic: device intent params["source_ip"] → key = "Loopback0\|{ip}/32" + base key "Loopback0" |

**H. KEEP AS-IS (legitimate projection/infrastructure reads):**

| Function | File:Line | Reason |
|----------|-----------|--------|
| `Interface.AdminStatus` | interface.go:58 | Reads PORT table — pre-intent infrastructure via RegisterPort |
| `Interface.Speed` | interface.go:87 | Reads PORT table — pre-intent infrastructure |
| `Interface.MTU` | interface.go:103 | Reads PORT table — pre-intent infrastructure |
| `Interface.Description` | interface.go:256 | Reads PORT table — pre-intent infrastructure |
| `ListInterfaces` | node.go:886 | Reads PORT (pre-intent) + PortChannel (projection). PortChannel portion should use `IntentsByPrefix("portchannel\|")`, but low priority — display-only method, not an operational decision. |
| `RemoveLegacyBGPEntries` | bgp_ops.go:337 | Pre-intent bootstrap (newtron init) — reads actual CONFIG_DB |
| `scanRoutePoliciesByPrefix` | service_ops.go:970 | Blue-green migration only — content hashes not in intent params. `RemoveService` should use `deleteRoutePoliciesFromIntent()` (service_ops.go:938) which already reads `route_policy_keys` from the intent. `scanRoutePoliciesByPrefix` only needed for `RefreshService` blue-green migration (finding stale hash-named objects). |
| `IsUnifiedConfigMode` | node.go:487 | Infrastructure state check (frrcfgd mode) |
| All render/export/drift | various | Delivery and drift — projection's intended purpose |

### Resolved Concerns

| Concern | Resolution |
|---------|-----------|
| **O(n) intent scans** | Intent DB is small (dozens of records, not thousands). Linear scan is negligible compared to SSH/Redis latency. No index needed. |
| **Intent params completeness** | Intent Round-Trip Completeness rule already guarantees every param affecting CONFIG_DB is stored. If a query needs a value, it's in the intent. |
| **Port properties not in intents** | PORT table entries are pre-intent infrastructure (RegisterPort). AdminStatus, Speed, MTU, Description are physical port properties, not intent-derived. Reading Port/PortChannel tables is correct. |
| **Content-hashed cleanup** | `scanRoutePoliciesByPrefix` needs to find old-hash objects during blue-green migration. Hashes aren't in intent params. Projection scan by prefix is acceptable — this is delivery mechanics (finding what CONFIG_DB keys to delete), not domain logic. |
| **ACL port list as shared resource** | Derived from intent scans: collect all `interface\|*\|acl\|{dir}` intents with matching ACL name → interface names = ports CSV. No projection read needed. |
| **unbindQos/deleteDeviceQoSConfig** | QoS entries are deterministic from policy spec + interface name. Re-reading the spec at removal time is correct because the QoS intent stores the policy name, and the spec defines the structure. |
| **BGPNeighbors query** | Underlay peers: from `interface\|{name}\|bgp-peer` intents. Overlay peers: from `evpn-peer\|{ip}` intents. Both store the neighbor IP in intent params. |
| **No "evpn" intent in production** | `SetupVTEP` checks `GetIntent("evpn")` at line 212 and tests manually set `NewtronIntent["evpn"]`, but no production `writeIntent` call creates it. `SetupVTEP` is a sub-operation of `SetupDevice` (baseline_ops.go:77) and doesn't produce its own intent. The BGPPeerGroup check at line 238 is an intra-composite optimization. Fix: make peer group creation unconditional (render handles upserts), rely on `GetIntent("device")` for cross-execution idempotency. |
| **InterfaceExists for PortChannel/VLAN SVI** | `InterfaceExists` checks Port, PortChannel, VLAN tables. Physical ports are pre-intent (RegisterPort → `n.interfaces` map). PortChannels and VLAN SVIs are intent-managed. Fix: check `n.interfaces[name]` for physical ports, `GetIntent("portchannel\|...")` for PCs, `GetIntent("vlan\|...")` for SVIs. |
| **IPAddresses() single-IP per intent** | Each interface intent stores one IP (`intf_ip` or `ip_address`). `SetIP`/`RemoveIP` are sub-operations that don't produce intents. In the intent-first model, IPs not tracked by intents are projection-only mutations. Callers that count remaining IPs (RemoveIP:92, SetVRF:311) will work correctly — one intent = one IP. |
| **Route policy cleanup already intent-based** | `deleteRoutePoliciesFromIntent()` (service_ops.go:938) already reads `route_policy_keys` from the intent record. `RemoveService` should use this path. `scanRoutePoliciesByPrefix` is only needed for `RefreshService` blue-green migration. |

## Execution Order

### Phase 0: Intent Query Helpers

#### 0.1. Add `IntentsByPrefix` to `node/node.go`
- [x] `func (n *Node) IntentsByPrefix(prefix string) map[string]*sonic.Intent`
- [x] Scans `n.configDB.NewtronIntent` for keys starting with prefix
- [x] Returns map of resource → Intent

#### 0.2. Add `IntentsByParam` to `node/node.go`
- [x] `func (n *Node) IntentsByParam(key, value string) map[string]*sonic.Intent`
- [x] Scans `n.configDB.NewtronIntent` for entries where params[key] == value

#### 0.3. Add `IntentsByOp` to `node/node.go`
- [x] `func (n *Node) IntentsByOp(op string) map[string]*sonic.Intent`
- [x] Scans `n.configDB.NewtronIntent` for entries where op field matches

#### 0.4. Add unit tests for intent query helpers
- [x] Test prefix matching, param matching, op matching
- [x] Test empty results, multiple matches

#### 0.5. Build + test
- [x] `go build ./...` passes
- [x] `go test ./pkg/newtron/network/node/... -count=1` passes

### Phase 1: Query/Display Methods

#### 1.1. Rewrite `ListVLANs` — vlan_ops.go:392
- [x] `IntentsByPrefix("vlan|")` → extract VLAN IDs from resource keys
- [x] Delete configDB.VLAN iteration

#### 1.2. Rewrite `GetVLAN` — vlan_ops.go:330
- [x] Existence: `GetIntent("vlan|{id}")`
- [x] Description: intent params `description`
- [x] Members: scan `IntentsByParam("vlan_id", strconv.Itoa(id))` for service/configure-interface intents → extract interface names
- [x] IRB: `GetIntent("interface|Vlan{id}")`
- [x] L2VNI: `GetIntent("macvpn|{id}")` → params `vni`
- [x] ARP suppression: `GetIntent("macvpn|{id}")` → params `arp_suppression`
- [x] MAC-VPN name: `GetIntent("macvpn|{id}")` → params `macvpn`
- [x] Delete all configDB.VLAN, VLANMember, VLANInterface, VXLANTunnelMap, SuppressVLANNeigh reads

#### 1.3. Rewrite `ListVRFs` — vrf_ops.go:482
- [x] `IntentsByPrefix("vrf|")` → extract VRF names from resource keys

#### 1.4. Rewrite `GetVRF` — vrf_ops.go:437
- [x] Existence: `GetIntent("vrf|{name}")`
- [x] L3VNI: `GetIntent("ipvpn|{name}")` → params `l3vni`
- [x] Interfaces: `IntentsByParam("vrf", name)` → extract interface names from resource keys
- [x] Delete configDB.VRF, Interface, VLANInterface reads

#### 1.5. Rewrite `ListPortChannels` — portchannel_ops.go:236
- [x] `IntentsByPrefix("portchannel|")` → filter to top-level (one `|`)

#### 1.6. Rewrite `GetPortChannel` — portchannel_ops.go:206
- [x] Existence: `GetIntent("portchannel|{name}")`
- [x] Members: `IntentsByPrefix("portchannel|{name}|")` for member sub-intents
- [x] AdminStatus: hardcoded "up" (always created with admin_status: up)
- [x] Delete configDB.PortChannel, PortChannelMember reads

#### 1.7. Build + test
- [x] `go build ./...` passes
- [x] `go test ./pkg/newtron/network/node/... -count=1` passes

### Phase 2: Interface Property Queries

#### 2.1. Rewrite `Interface.VRF` — interface.go:126
- [x] `i.node.GetIntent("interface|" + i.name)` → params `vrf`
- [x] Works for both regular interfaces and IRBs (same key pattern)
- [x] Delete configDB.Interface read

#### 2.2. Rewrite `Interface.IPAddresses` — interface.go:138
- [x] `i.node.GetIntent("interface|" + i.name)` → params `intf_ip` (configure-interface) or `ip_address` (configure-irb)
- [x] Return as single-element slice (intent stores one IP per interface)
- [x] Delete configDB.Interface scan

#### 2.3. Rewrite `Interface.PortChannelParent` — interface.go:241
- [x] Scan `IntentsByPrefix("portchannel|")` for member sub-intents where parts[2] == i.name
- [x] Delete configDB.PortChannelMember scan

#### 2.4. Rewrite `Interface.PortChannelMembers` — interface.go:271
- [x] `IntentsByPrefix("portchannel|" + i.name + "|")` → extract member names from FieldName param
- [x] Delete configDB.PortChannelMember scan

#### 2.5. Rewrite `Interface.VLANMembers` — interface.go:291
- [x] Extract VLAN ID from i.name (e.g., "Vlan100" → "100")
- [x] `IntentsByParam("vlan_id", vlanID)` filtered to interface intents, skip IRBs
- [x] Delete configDB.VLANMember scan

#### 2.6. Rewrite `Interface.BGPNeighbors` — interface.go:310
- [x] `IntentsByPrefix("interface|" + i.name + "|bgp-peer")` → extract neighbor_ip from params
- [x] Delete configDB.BGPNeighbor scan and IPAddresses dependency

#### 2.7. Simplify `Interface.IngressACL` — interface.go:189
- [x] Keep service binding intent read (ingress_acl param)
- [x] Add standalone ACL binding intent check (interface|{name}|acl|ingress → acl_name param)
- [x] Remove configDB.ACLTable fallback scan

#### 2.8. Simplify `Interface.EgressACL` — interface.go:211
- [x] Keep service binding intent read (egress_acl param)
- [x] Add standalone ACL binding intent check (interface|{name}|acl|egress → acl_name param)
- [x] Remove configDB.ACLTable fallback scan

#### 2.9. Build + test
- [x] `go build ./...` passes
- [x] `go test ./pkg/newtron/network/node/... -count=1` passes

### Phase 3: Reference Counting + Membership

#### 3.1. Rewrite SAG_GLOBAL check in `UnconfigureIRB` — vlan_ops.go
- [x] Replace `configDB.VLANInterface` scan with `IntentsByOp(sonic.OpConfigureIRB)`
- [x] Filter for intents with `anycast_mac` param, excluding the current VLAN
- [x] If any remain → other SVI uses SAG_GLOBAL → don't delete

#### 3.2. Rewrite `isQoSPolicyReferenced` — qos.go
- [x] Replace `configDB.PortQoSMap` scan with intent scan for both standalone QoS intents and service intents with qos_policy param
- [x] Exclude the current interface

#### 3.3. Rewrite ACL port list in `generateServiceEntries` — service_ops.go
- [x] Replace `configDB.ACLTable[aclName]` read with `GetIntent("acl|"+aclName)` for existence
- [x] Added `aclPortsFromIntents` helper to acl_ops.go — scans both standalone ACL binding intents and service intents
- [x] Collect interface names → sorted ports CSV

#### 3.3b. Rewrite ACL port list in `BindACL` — interface_ops.go
- [x] Replace `configDB.ACLTable[aclName].Ports` with `aclPortsFromIntents` (binding intent already written → current interface included)

#### 3.4. Rewrite ACL port update in `UnbindACL` — interface_ops.go
- [x] Use `aclPortsFromIntents` then `RemoveFromCSV(allPorts, i.name)` (binding intent not yet deleted → explicit exclusion)

#### 3.5. Rewrite `InterfaceIsPortChannelMember` — portchannel_ops.go
- [x] Scan `IntentsByPrefix("portchannel|")` for member intents where parts[2] == name

#### 3.6. Rewrite `GetInterfacePortChannel` — portchannel_ops.go
- [x] Same scan, return parts[1] (PortChannel name)

#### 3.7. Build + test
- [x] `go build ./...` passes
- [x] `go test ./pkg/newtron/network/node/... -count=1` passes

### Phase 4: Config-Time Decisions

#### 4.1. Rewrite BGPPeerGroup check in `SetupVTEP` — evpn_ops.go:238
- [x] Remove the `configDB.BGPPeerGroup[pgKey]` conditional — make peer group creation unconditional
- [x] `render(cs)` handles upserts safely; duplicate entries in the ChangeSet are harmless
- [x] `SetupDevice` guards cross-execution idempotency via `GetIntent("device")`; `SetupVTEP` is always called from `SetupDevice`
- [x] Note: no "evpn" intent exists in production code — `SetupVTEP` is a sub-operation of `SetupDevice`, not an intent-producing entry point

#### 4.1b. Rewrite BGPPeerGroup check in `AddBGPEVPNPeer` — bgp_ops.go:450
- [x] Replace `configDB.BGPPeerGroup[pgKey]` with `GetIntent("device") != nil` + check `params["source_ip"] != ""`
- [x] If device intent exists with source_ip, SetupDevice ran SetupVTEP which created the peer group
- [x] Error message: "EVPN peer group does not exist; run setup-device with source_ip first"

#### 4.2. Rewrite `BGPNeighborExists` — bgp_ops.go:364
- [x] Check `GetIntent("evpn-peer|" + neighborIP)` for overlay peers
- [x] Check `IntentsByPrefix("interface|")` for underlay peers with `add-bgp-peer` op matching the IP

#### 4.3. Rewrite `BGPConfigured` — bgp_ops.go:331
- [x] Replace `configDB.BGPConfigured()` with `GetIntent("device") != nil`
- [x] BGP globals are created by SetupDevice — if device intent exists, BGP is configured

#### 4.4. Rewrite `VTEPSourceIP` — evpn_ops.go:33
- [x] Check `GetIntent("device")` → params `source_ip` (no "evpn" intent exists in production)
- [x] Fallback to `resolved.LoopbackIP`
- [x] Delete configDB.VXLANTunnel scan

#### 4.5. Rewrite `DeleteACLRule` existence check — acl_ops.go:267
- [x] Replace `configDB.ACLRule[ruleKey]` with `GetIntent("acl|{tableName}|{ruleName}")`

#### 4.5a. Remove redundant ACL existence check in `removeSharedACL` — service_ops.go:1061
- [x] Remove `configDB.ACLTable[aclName]` check — existing intent check handles the nil case

#### 4.5b. Rewrite `InterfaceExists` — interface_ops.go:14
- [x] Replace `configDB.HasInterface(name)` with three-way check:
  - Physical ports: `_, ok := n.interfaces[name]` (populated by RegisterPort)
  - PortChannels: `strings.HasPrefix(name, "PortChannel") && n.GetIntent("portchannel|"+name) != nil`
  - VLAN SVIs: `strings.HasPrefix(name, "Vlan") && n.GetIntent("vlan|"+strconv.Itoa(id)) != nil`
- [x] Also update `interfaceExistsInConfigDB` (node.go) — delegates to `InterfaceExists`
- [x] Test fixture `testDevice()` updated to populate `n.interfaces` from Port entries

#### 4.6. Build + test
- [x] `go build ./...` passes
- [x] `go test ./pkg/newtron/network/node/... -count=1` passes

### Phase 5: Health Check + Cleanup Scans

#### 5.1. Rewrite `CheckBGPSessions` expected set — health_ops.go:42
- [x] Replace `configDB.BGPNeighbor` scan with intent scan
- [x] Overlay peers: `IntentsByPrefix("evpn-peer|")` → extract VRF + IP from params
- [x] Underlay peers: `IntentsByPrefix("interface|")` with `|bgp-peer` suffix → extract VRF + IP from params
- [x] Build expected map from intent data

#### 5.2. Rewrite `unbindQos` — qos_ops.go:52
- [x] Read QoS intent: `GetIntent("interface|{name}|qos")` → policy name
- [x] Resolve QoS spec from policy name via `GetQoSPolicy`
- [x] Generate QUEUE delete entries deterministically: `{intfName}|{0..len(queues)-1}`
- [x] Generate PORT_QOS_MAP delete entry: key = interface name
- [x] Delete configDB.Queue/PortQoSMap scans

#### 5.3. Rewrite `deleteDeviceQoSConfig` — qos.go:139
- [x] Generate SCHEDULER delete entries deterministically: `{policyName}_Q{0..len(queues)-1}`
- [x] Generate WRED_PROFILE delete entry: `{policyName}_ECN` (if spec has ECN)
- [x] Resolve spec from SpecProvider via `GetQoSPolicy`
- [x] Delete configDB.Scheduler/WREDProfile scans

#### 5.4. Rewrite `RemoveLoopback` — baseline_ops.go:176
- [x] Read device intent: `GetIntent("device")` → params `source_ip`
- [x] Generate delete entries deterministically: `Loopback0|{ip}/32` + `Loopback0`
- [x] Delete configDB.LoopbackInterface scan

#### 5.5. Build + test
- [x] `go build ./...` passes
- [x] `go test ./pkg/newtron/network/node/... -count=1` passes

### Phase 6: Dead Code Removal

#### 6.1. Delete typed configDB accessors no longer called
- [x] `BGPConfigured()` on sonic.ConfigDB — KEEP: still tested in configdb_parsers_test.go (sonic package utility)
- [x] `HasBGPNeighbor()` on sonic.ConfigDB — KEEP: still tested in configdb_parsers_test.go (sonic package utility)
- [x] `HasInterface()` on sonic.ConfigDB — KEEP: still tested in configdb_parsers_test.go (sonic package utility)
- [x] No callers remain in node/ package (verified via grep). Methods retained for sonic package internal use.
- [x] Test fixture `testDevice()` updated: populates `n.interfaces` from Port entries, cleaned up stale VXLANTunnel seeding

#### 6.2. Build + test
- [x] `go build ./...` passes
- [x] `go test ./pkg/newtron/network/node/... -count=1` passes

### Phase 7: Verification + Conformance Audit

#### 7.1. Grep verification — zero configDB reads in operational logic
- [x] `grep -rn 'n\.configDB\.VLAN\[' pkg/newtron/network/node/` → zero
- [x] `grep -rn 'n\.configDB\.VRF\[' pkg/newtron/network/node/` → zero
- [x] `grep -rn 'n\.configDB\.BGPNeighbor\[' pkg/newtron/network/node/` → zero
- [x] `grep -rn 'n\.configDB\.BGPPeerGroup\[' pkg/newtron/network/node/` → zero (test files only)
- [x] `grep -rn 'n\.configDB\.VLANMember' pkg/newtron/network/node/` → zero
- [x] `grep -rn 'n\.configDB\.VLANInterface' pkg/newtron/network/node/` → zero
- [x] `grep -rn 'n\.configDB\.VXLANTunnel' pkg/newtron/network/node/` → zero
- [x] `grep -rn 'n\.configDB\.VXLANTunnelMap' pkg/newtron/network/node/` → zero
- [x] `grep -rn 'n\.configDB\.SuppressVLANNeigh' pkg/newtron/network/node/` → zero
- [x] `grep -rn 'n\.configDB\.ACLTable\[' pkg/newtron/network/node/` → zero
- [x] `grep -rn 'n\.configDB\.ACLRule\[' pkg/newtron/network/node/` → zero
- [x] `grep -rn 'n\.configDB\.PortChannelMember' pkg/newtron/network/node/` → zero
- [x] `grep -rn 'n\.configDB\.Queue\[' pkg/newtron/network/node/` → zero
- [x] `grep -rn 'n\.configDB\.PortQoSMap\[' pkg/newtron/network/node/` → zero
- [x] `grep -rn 'n\.configDB\.Scheduler\[' pkg/newtron/network/node/` → zero
- [x] `grep -rn 'n\.configDB\.WREDProfile\[' pkg/newtron/network/node/` → zero
- [x] `grep -rn 'n\.configDB\.Interface\[' pkg/newtron/network/node/` → zero
- [x] `grep -rn 'configDB\.BGPNeighbor\[' pkg/newtron/network/node/` → zero (test files only; RemoveLegacyBGPEntries uses `n.configDB.BGPNeighbor` — allowed)
- [x] `grep -rn 'configDB\.ACLTable\[' pkg/newtron/network/node/` → zero (test files only)
- [x] `grep -rn 'configDB\.HasInterface\|configDB\.HasBGPNeighbor\|configDB\.BGPConfigured' pkg/newtron/network/node/` → zero
- [x] `grep -rn 'configDB\.PortChannel\[' pkg/newtron/network/node/` → interface.go only: AdminStatus/MTU/Description fallbacks for PortChannel interfaces (pre-intent infrastructure — same category as PORT table reads)
- [x] `grep -rn 'configDB\.LoopbackInterface' pkg/newtron/network/node/` → zero

#### 7.2. Remaining configDB reads are all in allowed categories
- [x] PORT table reads: AdminStatus, Speed, MTU, Description (pre-intent infrastructure)
- [x] PortChannel table reads in interface.go: AdminStatus, MTU, Description (pre-intent infrastructure — PortChannel properties like PORT properties)
- [x] RemoveLegacyBGPEntries: pre-intent bootstrap (reads actual CONFIG_DB directly)
- [x] scanRoutePoliciesByPrefix: RefreshService blue-green migration only (acceptable exception)
- [x] render/export/drift: projection's intended purpose
- [x] NewtronIntent reads: intent DB access (correct)
- [x] `n.interfaces[name]` map reads: pre-intent infrastructure (RegisterPort populates, not a projection read)

#### 7.3. All tests pass
- [x] `go build ./pkg/newtron/network/node/...` passes
- [x] `go test ./pkg/newtron/network/node/... -count=1` passes
- [x] Existing precondition tests still pass (they use GetIntent, unaffected)

#### 7.4. Post-implementation conformance audit (ai-instructions.md #9)
- [x] Architecture §1 "Intent DB is the decision substrate" — ALL operational reads use intents
- [x] Architecture §4 "op() internals" — preconditions and all operational decisions use intent DB
- [x] CLAUDE.md "Intent DB is primary state and the decision substrate" — implementation matches
- [x] No function bypasses intent DB for a decision that could be answered by intents
- [x] Port/PortChannel properties (AdminStatus, Speed, MTU, Description) correctly read PORT/PORTCHANNEL tables (pre-intent infrastructure)
- [x] scanRoutePoliciesByPrefix documented as acceptable exception (delivery mechanics)
- [x] Audit found and fixed 2 additional violations:
  - `service_ops.go:663` — `BGPPeerGroup` projection read in `addBGPRoutePolicies` → replaced with intent scan (check existing service intents for peer group existence)
  - `node.go:1133` — `DeviceMetadata`/`BGPGlobals` projection read in `ApplyFRRDefaults` → replaced with `n.resolved.UnderlayASN`
- [x] IsUnifiedConfigMode reads DeviceMetadata["localhost"] — infrastructure state, not intent-managed. Allowed.
