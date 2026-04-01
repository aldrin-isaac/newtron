# Unified Architecture Function Audit

Systematic audit of every function in `pkg/newtron/network/node/` against
the unified pipeline architecture (Intent → Replay → Render → [Deliver]).

Reference: `docs/newtron/unified-pipeline-architecture.md`

Date: 2026-03-29

---

## Categories

Each function belongs to exactly one category. The category defines what the
function does and the pattern it must follow. Violations are noted inline.

---

### CONSTRUCTION

**Purpose**: Create Node or Interface objects.

**Pattern**: Initialize struct fields. Never read a device. Never read
the projection for decisions. The object starts empty or with caller-
provided data.

| Function | Location | Conformance |
|----------|----------|-------------|
| `New` | node.go:83 | PASS |
| `NewAbstract` | node.go:104 | PASS |
| `NewTestNode` | node.go:1164 | PASS |
| `NewPreconditionChecker` | precondition.go:20 | PASS |
| `NewChangeSet` | changeset.go:49 | PASS |

---

### ACCESSOR

**Purpose**: Return a field value with no computation or I/O.

**Pattern**: One-liner returning a struct field. No side effects.

| Function | Location | Conformance |
|----------|----------|-------------|
| `HasActuatedIntent` | node.go:116 | PASS |
| `HasUnsavedIntents` | node.go:119 | PASS |
| `ClearUnsavedIntents` | node.go:122 | PASS |
| `Name` | node.go:484 | PASS |
| `Profile` | node.go:489 | PASS |
| `Resolved` | node.go:494 | PASS |
| `MgmtIP` | node.go:499 | PASS |
| `LoopbackIP` | node.go:504 | PASS |
| `ASNumber` | node.go:509 | PASS |
| `RouterID` | node.go:514 | PASS |
| `Zone` | node.go:519 | PASS |
| `BGPNeighbors` | node.go:524 | PASS |
| `ConfigDB` | node.go:529 | PASS |
| `Tunnel` | node.go:555 | PASS |
| `StateDBClient` | node.go:563 | PASS |
| `ConfigDBClient` | node.go:571 | PASS |
| `StateDB` | node.go:579 | PASS |
| `IsConnected` | node.go:730 | PASS |
| `IsLocked` | node.go:869 | PASS |
| `(*Interface).Node` | interface.go:45 | PASS |
| `(*Interface).Name` | interface.go:55 | PASS |
| `(*Interface).OperStatus` | interface.go:75 | PASS — reads StateDB, not projection |
| `(*VLANInfo).L2VNI` | vlan_ops.go:311 | PASS |
| `(*ChangeSet).IsEmpty` | changeset.go:118 | PASS |
| `(*PreconditionChecker).Errors` | precondition.go:194 | PASS |
| `(*PreconditionChecker).HasErrors` | precondition.go:199 | PASS |

---

### INTENT_READ

**Purpose**: Read the intent DB (`configDB.NewtronIntent`) to answer
questions about what has been configured.

**Pattern**: Access `GetIntent(resource)`, `IntentsByPrefix(pfx)`,
`IntentsByParam(k,v)`, `IntentsByOp(op)`, or scan `configDB.NewtronIntent`
directly. Never read typed configDB tables (VLAN, VRF, BGPNeighbor, etc.)
for the answer.

| Function | Location | Conformance |
|----------|----------|-------------|
| `GetIntent` | node.go:220 | PASS |
| `Intents` | node.go:234 | PASS |
| `ServiceIntents` | node.go:248 | PASS |
| `IntentsByPrefix` | node.go:267 | PASS |
| `IntentsByParam` | node.go:285 | PASS |
| `IntentsByOp` | node.go:304 | PASS |
| `SnapshotIntentDB` | node.go:127 | PASS |
| `(*Interface).binding` | interface.go:159 | PASS — reads NewtronIntent directly |
| `(*Interface).ServiceName` | interface.go:171 | PASS |
| `(*Interface).HasService` | interface.go:183 | PASS |
| `(*Interface).IngressACL` | interface.go:189 | PASS |
| `(*Interface).EgressACL` | interface.go:204 | PASS |
| `(*Interface).IsPortChannelMember` | interface.go:224 | PASS |
| `(*Interface).PortChannelParent` | interface.go:229 | PASS |
| `(*Interface).PortChannelMembers` | interface.go:256 | PASS |
| `(*Interface).VLANMembers` | interface.go:271 | PASS |
| `(*Interface).BGPNeighbors` | interface.go:295 | PASS |
| `(*Interface).VRF` | interface.go:128 | PASS |
| `(*Interface).IPAddresses` | interface.go:137 | PASS |
| `aclPortsFromIntents` | acl_ops.go:174 | PASS |
| `deleteRoutePoliciesFromIntent` | service_ops.go:951 | PASS |
| `ValidateIntentDAG` | intent_ops.go:182 | PASS |

---

### INTENT_WRITE

**Purpose**: Create, update, or delete NEWTRON_INTENT records.

**Pattern**: Modify `configDB.NewtronIntent` via `writeIntent` or
`deleteIntent`. Call `renderIntent` to update the projection's
NEWTRON_INTENT table so subsequent parent-lookup works within the
same operation. Never write typed configDB tables directly.

| Function | Location | Conformance |
|----------|----------|-------------|
| `writeIntent` | intent_ops.go:19 | PASS |
| `deleteIntent` | intent_ops.go:89 | PASS |
| `RestoreIntentDB` | node.go:142 | PASS |
| `ensureInterfaceIntent` | interface_ops.go:158 | PASS |

---

### PROJECTION_WRITE

**Purpose**: Update the typed configDB tables (the projection) from
a ChangeSet.

**Pattern**: Validate entries against the schema, then apply
adds/updates/deletes to the typed configDB tables. This is the ONE
mechanism that writes typed tables. Called by `render(cs)` and
`renderIntent(cs)`.

| Function | Location | Conformance |
|----------|----------|-------------|
| `render` | node.go:462 | PASS |
| `renderIntent` | intent_ops.go:124 | PASS |
| `RegisterPort` | node.go:416 | PASS — writes PORT (pre-intent bootstrap) |

---

### CONFIG_GENERATOR

**Purpose**: Pure functions that take parameters and return
`[]sonic.Entry` or a single `sonic.Entry`. No side effects.

**Pattern**: No `configDB` reads, no intent reads/writes, no device I/O.
Input → entries. These are the forward-path functions that translate
domain intent into CONFIG_DB wire format.

| Function | Location | Conformance |
|----------|----------|-------------|
| `createVlanConfig` | vlan_ops.go:49 | PASS |
| `createVlanMemberConfig` | vlan_ops.go:71 | PASS |
| `createSviConfig` | vlan_ops.go:87 | PASS |
| `deleteSagGlobalConfig` | vlan_ops.go:119 | PASS |
| `deleteVlanMemberConfig` | vlan_ops.go:124 | PASS |
| `deleteSviIPConfig` | vlan_ops.go:129 | PASS |
| `deleteSviBaseConfig` | vlan_ops.go:134 | PASS |
| `deleteVlanConfig` | vlan_ops.go:140 | PASS |
| `destroyVlanConfig` | vlan_ops.go:180 | PASS |
| `createVrfConfig` | vrf_ops.go:23 | PASS |
| `createStaticRouteConfig` | vrf_ops.go:31 | PASS |
| `bindIpvpnConfig` | vrf_ops.go:54 | PASS |
| `destroyVrfConfig` | vrf_ops.go:119 | PASS |
| `unbindIpvpnConfig` | vrf_ops.go:270 | PASS |
| `CreateBGPNeighborConfig` | bgp_ops.go:36 | PASS |
| `DeleteBGPNeighborConfig` | bgp_ops.go:124 | PASS |
| `CreateBGPGlobalsConfig` | bgp_ops.go:149 | PASS |
| `CreateBGPGlobalsAFConfig` | bgp_ops.go:168 | PASS |
| `revertRedistributionConfig` | bgp_ops.go:179 | PASS |
| `CreateRouteRedistributeConfig` | bgp_ops.go:192 | PASS |
| `deleteBgpGlobalsConfig` | bgp_ops.go:205 | PASS |
| `deleteBgpGlobalsAFConfig` | bgp_ops.go:210 | PASS |
| `deleteRouteRedistributeConfig` | bgp_ops.go:215 | PASS |
| `updateDeviceMetadataConfig` | bgp_ops.go:220 | PASS |
| `createBgpNeighborAFConfig` | bgp_ops.go:225 | PASS |
| `CreateBGPPeerGroupConfig` | bgp_ops.go:254 | PASS |
| `UpdateBGPPeerGroupAF` | bgp_ops.go:271 | PASS |
| `DeleteBGPPeerGroupConfig` | bgp_ops.go:281 | PASS |
| `CreateEVPNPeerGroupConfig` | bgp_ops.go:295 | PASS |
| `DeleteEVPNPeerGroupConfig` | bgp_ops.go:319 | PASS |
| `CreateVTEPConfig` | evpn_ops.go:44 | PASS |
| `createVniMapConfig` | evpn_ops.go:54 | PASS |
| `enableArpSuppressionConfig` | evpn_ops.go:66 | PASS |
| `disableArpSuppressionConfig` | evpn_ops.go:75 | PASS |
| `deleteVniMapConfig` | evpn_ops.go:80 | PASS |
| `deleteVniMapByKeyConfig` | evpn_ops.go:86 | PASS |
| `deleteBgpEvpnVNIConfig` | evpn_ops.go:91 | PASS |
| `createAclTableConfig` | acl_ops.go:42 | PASS |
| `createAclRuleConfig` | acl_ops.go:57 | PASS |
| `buildAclRuleFields` | acl_ops.go:95 | PASS |
| `createAclRuleFromFilterConfig` | acl_ops.go:145 | PASS |
| `bindAclConfig` | acl_ops.go:155 | PASS |
| `updateAclPorts` | acl_ops.go:164 | PASS |
| `deleteAclTableConfig` | acl_ops.go:321 | PASS |
| `bindVrf` | interface_ops.go:37 | PASS |
| `enableIpRouting` | interface_ops.go:44 | PASS |
| `assignIpAddress` | interface_ops.go:50 | PASS |
| `destroyPortChannelConfig` | portchannel_ops.go:104 | PASS |
| `GenerateDeviceQoSConfig` | qos.go:28 | PASS |
| `bindQos` | qos.go:103 | PASS |
| `deleteDeviceQoSConfig` | qos.go:139 | PASS |
| `createRoutePolicy` | service_ops.go:727 | PASS |
| `createInlineRoutePolicy` | service_ops.go:845 | PASS |
| `createHashedPrefixSet` | service_ops.go:908 | PASS |

---

### CONFIG_METHOD

**Purpose**: Orchestrate a domain operation: check preconditions, generate
CONFIG_DB entries, write/delete intent records, render entries into the
projection.

**Pattern**: `precondition → generate entries → writeIntent/deleteIntent →
render(cs)`. May read intents for idempotency checks. Returns a ChangeSet
for the caller to Apply (or discard during replay).

Key invariant: by return, both the intent DB and the projection are updated.

| Function | Location | Conformance |
|----------|----------|-------------|
| `CreateVLAN` | vlan_ops.go:146 | PASS |
| `DeleteVLAN` | vlan_ops.go:187 | PASS |
| `ConfigureIRB` | vlan_ops.go:213 | PASS |
| `UnconfigureIRB` | vlan_ops.go:250 | PASS |
| `CreateVRF` | vrf_ops.go:147 | PASS |
| `DeleteVRF` | vrf_ops.go:170 | AUDIT MISSED — was missing `render(cs)` (Finding 7, fixed) |
| `AddVRFInterface` | vrf_ops.go:204 | PASS |
| `RemoveVRFInterface` | vrf_ops.go:218 | PASS |
| `BindIPVPN` | vrf_ops.go:233 | PASS |
| `UnbindIPVPN` | vrf_ops.go:306 | AUDIT MISSED — was missing `render(cs)` (Finding 8, fixed) |
| `AddStaticRoute` | vrf_ops.go:368 | PASS |
| `RemoveStaticRoute` | vrf_ops.go:403 | PASS |
| `ConfigureBGP` | bgp_ops.go:390 | PASS |
| `AddBGPEVPNPeer` | bgp_ops.go:447 | PASS |
| `RemoveBGPEVPNPeer` | bgp_ops.go:494 | PASS |
| `RemoveBGPGlobals` | bgp_ops.go:517 | AUDIT MISSED — was missing `render(cs)` (Finding 11, fixed) |
| `SetupVTEP` | evpn_ops.go:191 | PASS |
| `TeardownVTEP` | evpn_ops.go:275 | AUDIT MISSED — was missing `render(cs)` (Finding 10, fixed) |
| `ConfigureRouteReflector` | evpn_ops.go:329 | PASS |
| `BindMACVPN` | evpn_ops.go:100 | PASS |
| `UnbindMACVPN` | evpn_ops.go:155 | PASS |
| `CreateACL` | acl_ops.go:235 | PASS |
| `AddACLRule` | acl_ops.go:274 | PASS |
| `DeleteACLRule` | acl_ops.go:295 | PASS |
| `DeleteACL` | acl_ops.go:326 | PASS |
| `UnbindACLFromInterface` | acl_ops.go:342 | PASS |
| `SetIP` | interface_ops.go:60 | PASS — intentless sub-op, renders |
| `RemoveIP` | interface_ops.go:94 | AUDIT MISSED — was missing `render(cs)` (Finding 12, fixed) |
| `SetVRF` | interface_ops.go:124 | PASS |
| `ConfigureInterface` | interface_ops.go:173 | PASS |
| `UnconfigureInterface` | interface_ops.go:255 | PASS |
| `BindACL` | interface_ops.go:362 | PASS |
| `UnbindACL` | interface_ops.go:399 | PASS |
| `SetProperty` | interface_ops.go:446 | PASS |
| `ClearProperty` | interface_ops.go:516 | PASS |
| `AddBGPPeer` | interface_bgp_ops.go:35 | AUDIT MISSED — was missing `render(cs)` (Finding 9, fixed) |
| `RemoveBGPPeer` | interface_bgp_ops.go:110 | PASS |
| `CreatePortChannel` | portchannel_ops.go:27 | PASS |
| `DeletePortChannel` | portchannel_ops.go:109 | PASS |
| `AddPortChannelMember` | portchannel_ops.go:126 | PASS |
| `RemovePortChannelMember` | portchannel_ops.go:162 | PASS |
| `SetupDevice` | baseline_ops.go:30 | PASS |
| `ConfigureLoopback` | baseline_ops.go:134 | PASS |
| `RemoveLoopback` | baseline_ops.go:164 | PASS |
| `SetDeviceMetadata` | node.go:443 | PASS |
| `ApplyService` | service_ops.go:47 | AUDIT MISSED — missing QoS sub-intent (Finding 2, fixed) + inline ACL intent incomplete params (Finding 13, fixed) |
| `RemoveService` | service_ops.go:1172 | AUDIT MISSED — raw `ConfigDB().NewtronIntent` access instead of intent helpers (Finding 6, fixed) |
| `RefreshService` | service_ops.go:1444 | PASS |
| `addBGPRoutePolicies` | service_ops.go:599 | PASS |
| `addACLRulesFromFilterSpec` | service_ops.go:1012 | PASS |
| `removeSharedACL` | service_ops.go:1068 | PASS |
| `RemoveQoS` | qos_ops.go:82 | PASS |
| `ApplyQoS` | qos_ops.go:17 | PASS (fixed: added missing `render(cs)`) |
| `op` (generic helper) | changeset.go:147 | PASS |

---

### QUERY

**Purpose**: Build API/CLI responses describing the current state of
the node's configuration.

**Pattern**: Read intent DB only (`GetIntent`, `IntentsByPrefix`,
`IntentsByParam`). Never read typed configDB tables except PORT
(pre-intent infrastructure). Return domain-level response structs.

| Function | Location | Conformance |
|----------|----------|-------------|
| `GetVLAN` | vlan_ops.go:328 | PASS |
| `ListVLANs` | vlan_ops.go:386 | PASS |
| `GetVRF` | vrf_ops.go:437 | PASS |
| `ListVRFs` | vrf_ops.go:474 | PASS |
| `VTEPSourceIP` | evpn_ops.go:34 | PASS |
| `GetPortChannel` | portchannel_ops.go:198 | PASS |
| `ListPortChannels` | portchannel_ops.go:230 | PASS |
| `GetInterfacePortChannel` | portchannel_ops.go:266 | PASS |
| `GetInterface` | node.go:913 | PASS |
| `InterfaceHasService` | service_ops.go:16 | PASS |
| `GetServiceQoSPolicy` | qos.go:197 | PASS |
| `(*Interface).String` | interface.go:342 | PASS |
| `Tree` | node.go:323 | PASS — reads intent DB → topology steps |

---

### PRECONDITION

**Purpose**: Check prerequisites before an operation proceeds.

**Pattern**: Read intent DB via `GetIntent(resource)`. Never read typed
configDB tables. Return error if precondition is not met.

| Function | Location | Conformance |
|----------|----------|-------------|
| `precondition` | precondition.go:32 | PASS |
| `RequireConnected` | precondition.go:41 | PASS |
| `RequireLocked` | precondition.go:50 | PASS |
| `RequireInterfaceExists` | precondition.go:59 | PASS |
| `RequireInterfaceNotPortChannelMember` | precondition.go:68 | PASS |
| `RequireVLANExists` | precondition.go:79 | PASS |
| `RequireVLANNotExists` | precondition.go:89 | PASS |
| `RequireVRFExists` | precondition.go:99 | PASS |
| `RequireVRFNotExists` | precondition.go:109 | PASS |
| `RequirePortChannelExists` | precondition.go:119 | PASS |
| `RequirePortChannelNotExists` | precondition.go:129 | PASS |
| `RequireVTEPConfigured` | precondition.go:139 | PASS |
| `RequireACLTableExists` | precondition.go:149 | PASS |
| `RequireACLTableNotExists` | precondition.go:159 | PASS |
| `Check` | precondition.go:169 | PASS |
| `Result` | precondition.go:178 | PASS |
| `InterfaceExists` | interface_ops.go:15 | PASS — three-way: `n.interfaces` + intent |
| `InterfaceIsPortChannelMember` | portchannel_ops.go:251 | PASS |
| `BGPConfigured` | bgp_ops.go:332 | PASS |
| `BGPNeighborExists` | bgp_ops.go:366 | PASS |

---

### REFERENCE_COUNT

**Purpose**: Check if a shared resource has remaining consumers before
deciding whether to delete it.

**Pattern**: Scan intent DB (`IntentsByPrefix`, `IntentsByParam`,
`IntentsByOp`) for remaining references, excluding the current operation's
interface. Never read typed configDB tables.

| Function | Location | Conformance |
|----------|----------|-------------|
| `isQoSPolicyReferenced` | qos.go:165 | PASS |

Note: SAG_GLOBAL reference counting in `UnconfigureIRB` (vlan_ops.go:250)
uses `IntentsByOp("configure-irb")` — handled inline within the
CONFIG_METHOD, not a separate function.

---

### RECONSTRUCTION

**Purpose**: Convert intent records into topology steps and replay them
through config methods to rebuild the projection.

**Pattern**: Read intent records, topologically sort them, call config
methods. The config methods handle intent writing and rendering. After
replay, the projection matches what the intents declare.

| Function | Location | Conformance |
|----------|----------|-------------|
| `RebuildProjection` | node.go:158 | PASS |
| `InitFromDeviceIntent` | node.go:644 | PASS |
| `ReplayStep` | reconstruct.go:20 | PASS |
| `replayNodeStep` | reconstruct.go:38 | PASS |
| `replayInterfaceStep` | reconstruct.go:161 | PASS |
| `IntentToStep` | reconstruct.go:281 | PASS |
| `intentParamsToStepParams` | reconstruct.go:319 | PASS |
| `IntentsToSteps` | reconstruct.go:429 | PASS |
| `ReconstructExpected` | reconstruct.go:506 | PASS |

---

### CHANGESET

**Purpose**: Build, merge, and manipulate ChangeSets.

**Pattern**: Append/prepend changes, merge sets, format for display.
No configDB reads, no device I/O.

| Function | Location | Conformance |
|----------|----------|-------------|
| `(*ChangeSet).add` | changeset.go:59 | PASS |
| `(*ChangeSet).Add` | changeset.go:69 | PASS |
| `(*ChangeSet).Update` | changeset.go:74 | PASS |
| `(*ChangeSet).Delete` | changeset.go:79 | PASS |
| `(*ChangeSet).Adds` | changeset.go:84 | PASS |
| `(*ChangeSet).Updates` | changeset.go:91 | PASS |
| `(*ChangeSet).Deletes` | changeset.go:98 | PASS |
| `(*ChangeSet).Prepend` | changeset.go:107 | PASS |
| `(*ChangeSet).Merge` | changeset.go:113 | PASS |
| `(*ChangeSet).String` | changeset.go:175 | PASS |
| `(*ChangeSet).Preview` | changeset.go:203 | PASS |
| `(*ChangeSet).validate` | changeset.go:213 | PASS |
| `buildChangeSet` | changeset.go:125 | PASS |

---

### DEVICE_IO

**Purpose**: Read from or write to the device via Redis or SSH.
These are the I/O boundary — they operate on `n.conn` and never
modify the intent DB or projection.

**Pattern**: Use `n.conn.Client()` for Redis, `n.conn.Session()` for SSH.
Return data to caller. No projection writes.

| Function | Location | Conformance |
|----------|----------|-------------|
| `Drift` | node.go:339 | PASS — exports projection for comparison (legitimate) |
| `Reconcile` | node.go:365 | PASS |
| `Ping` | node.go:738 | PASS |
| `PingWithRetry` | node.go:747 | PASS |
| `SaveConfig` | node.go:995 | PASS |
| `EnsureUnifiedConfigMode` | node.go:1014 | PASS |
| `ConfigReload` | node.go:1066 | PASS |
| `RestartService` | node.go:1103 | PASS |
| `ApplyFRRDefaults` | node.go:1121 | PASS |
| `(*ChangeSet).Apply` | changeset.go:218 | PASS |
| `(*ChangeSet).Verify` | changeset.go:258 | PASS |
| `verifyConfigChanges` | changeset.go:277 | PASS |
| `GetRoute` | vrf_ops.go:497 | PASS |
| `GetRouteASIC` | vrf_ops.go:510 | PASS |
| `GetNeighbor` | vrf_ops.go:522 | PASS |
| `CheckBGPSessions` | health_ops.go:32 | PASS |
| `checkBGPFromStateDB` | health_ops.go:73 | PASS |
| `checkBGPFromVtysh` | health_ops.go:111 | PASS |
| `CheckInterfaceOper` | health_ops.go:209 | PASS |
| `RemoveLegacyBGPEntries` | bgp_ops.go:338 | PASS — documented pre-intent bootstrap exception |

---

### LIFECYCLE

**Purpose**: Manage connection state, locks, and execution flow.

**Pattern**: Establish/tear down SSH+Redis, acquire/release locks,
orchestrate Lock→fn→Apply→Unlock.

| Function | Location | Conformance |
|----------|----------|-------------|
| `ConnectForSetup` | node.go:593 | PASS — pre-intent bootstrap |
| `ConnectTransport` | node.go:620 | PASS |
| `DisconnectTransport` | node.go:198 | PASS |
| `Disconnect` | node.go:712 | PASS |
| `Lock` | node.go:778 | PASS |
| `Unlock` | node.go:849 | PASS |
| `ExecuteOp` | node.go:888 | AUDIT MISSED — dead code, wrong pattern (Finding 16, deleted) |

---

### PROJECTION_READ_ALLOWED

**Purpose**: Read typed configDB tables for legitimate reasons. These
are the documented exceptions where projection reads are correct:

1. **PORT/PORTCHANNEL table**: Pre-intent infrastructure populated by
   `RegisterPort`. Physical port properties (admin status, speed, MTU,
   description) are not intent-managed.
2. **Render/export/drift**: The projection's intended purpose — device
   delivery and drift detection.
3. **Pre-intent bootstrap**: `ConnectForSetup` loads actual CONFIG_DB
   before any intents exist.
4. **Blue-green migration**: `scanRoutePoliciesByPrefix` finds stale
   content-hashed objects during `RefreshService`.
5. **Infrastructure state**: `IsUnifiedConfigMode` checks frrcfgd mode.

| Function | Location | Reason |
|----------|----------|--------|
| `(*Interface).AdminStatus` | interface.go:60 | AUDIT MISSED — was reading `configDB.PortChannel` for intent-managed data (Finding 4, fixed) |
| `(*Interface).Speed` | interface.go:91 | PORT pre-intent (PortChannel has no Speed) |
| `(*Interface).MTU` | interface.go:107 | AUDIT MISSED — was reading `configDB.PortChannel` for intent-managed data (Finding 4, fixed) |
| `(*Interface).Description` | interface.go:241 | AUDIT MISSED — was reading `configDB.PortChannel` for intent-managed data (Finding 4, fixed) |
| `WiredInterfaces` | node.go:427 | PORT table — pre-intent |
| `ListInterfaces` | node.go:942 | AUDIT MISSED — was reading `configDB.PortChannel` for intent-managed data (Finding 3, fixed) |
| `loadInterfaces` | node.go:970 | PORT + PortChannel — ConnectForSetup only |
| `IsUnifiedConfigMode` | node.go:536 | DeviceMetadata — infrastructure state |
| `scanRoutePoliciesByPrefix` | service_ops.go:983 | Blue-green migration |
| `scanExistingRoutePolicies` | service_ops.go:944 | Delegates to above |

---

### KEY_HELPER

**Purpose**: Construct CONFIG_DB key strings from domain parameters.

**Pattern**: Pure string manipulation. No configDB reads, no I/O.

| Function | Location | Conformance |
|----------|----------|-------------|
| `VLANName` | vlan_ops.go:14 | PASS |
| `VLANMemberKey` | vlan_ops.go:17 | PASS |
| `IRBIPKey` | vlan_ops.go:22 | PASS |
| `vlanResource` | vlan_ops.go:27 | PASS |
| `BGPGlobalsAFKey` | bgp_ops.go:163 | PASS |
| `RouteRedistributeKey` | bgp_ops.go:187 | PASS |
| `BGPNeighborAFKey` | bgp_ops.go:200 | PASS |
| `BGPPeerGroupKey` | bgp_ops.go:235 | PASS |
| `BGPPeerGroupAFKey` | bgp_ops.go:244 | PASS |
| `VNIMapKey` | evpn_ops.go:18 | PASS |
| `BGPEVPNVNIKey` | evpn_ops.go:23 | PASS |
| `splitKey` | node.go:985 | DELETED — dead code, zero callers (Finding 34) |
| `intentKind` | intent_ops.go:164 | PASS |
| `stepURL` | reconstruct.go:251 | PASS |
| `parseStepURL` | reconstruct.go:262 | PASS |
| `intentInterface` | reconstruct.go:301 | PASS |
| `parsePolicyName` | qos.go:186 | PASS |

---

### UTILITY

**Purpose**: Miscellaneous helpers that don't fit other categories.

| Function | Location | Conformance |
|----------|----------|-------------|
| `BuildLockHolder` | node.go:835 | PASS |
| `mapFilterType` | acl_ops.go:27 | PASS |
| `computeFilterHash` | acl_ops.go:203 | PASS |
| `serializeRROpts` | baseline_ops.go:98 | PASS |
| `(*Interface).IsPortChannel` | interface.go:312 | PASS |
| `(*Interface).IsVLAN` | interface.go:317 | PASS |
| `parseServiceFromACL` | interface.go:327 | DELETED — dead code, zero callers (Finding 35) |
| `bindingInt` | service_ops.go:40 | PASS |
| `expandPrefixList` | service_ops.go:1049 | PASS |
| `slicesEqual` | intent_ops.go:129 | PASS |
| `appendUnique` | intent_ops.go:142 | PASS |
| `removeItem` | intent_ops.go:152 | PASS |
| `paramString` | reconstruct.go:530 | PASS |
| `paramInt` | reconstruct.go:545 | PASS |
| `paramBool` | reconstruct.go:566 | PASS |
| `paramStringMap` | reconstruct.go:583 | PASS |
| `paramStringSlice` | reconstruct.go:601 | PASS |
| `parseRouteReflectorOpts` | reconstruct.go:622 | PASS |
| `deserializeRRPeers` | reconstruct.go:659 | PASS |
| `skipInReconstruct` | reconstruct.go:419 | PASS |
| `interfaceExistsInConfigDB` | node.go:965 | AUDIT MISSED — dead indirection with misleading name (Finding 5, deleted) |
| `HealthCheckResult` | health_ops.go:22 | PASS — type definition |
| `DAGViolation` | intent_ops.go:175 | PASS — type definition |
| `routeMapRule` | service_ops.go:717 | PASS — type definition |

---

## Findings

### Finding 1: `ApplyQoS` missing `render(cs)` call

**Location**: `qos_ops.go:17`

**Issue**: `ApplyQoS` builds a ChangeSet with QoS CONFIG_DB entries
(DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER, WRED_PROFILE, PORT_QOS_MAP,
QUEUE) but never calls `render(cs)`. The intent is written via
`writeIntent` (which calls `renderIntent` for the NEWTRON_INTENT entry),
but the CONFIG_DB entries are never validated against the schema or applied
to the projection.

**Impact**:
- During intent replay (topology mode or `InitFromDeviceIntent`), QoS
  CONFIG_DB entries are not rendered into the projection. The projection
  is incomplete — it has the QoS intent but not the QoS CONFIG_DB tables.
- During interactive use via `Execute`, `cs.Apply(n)` writes to Redis
  successfully, but the local projection diverges from the device.
- Schema validation is skipped for QoS entries on the forward path.

**Contrast**: `RemoveQoS` (qos_ops.go:107) correctly calls `render(cs)`.
`ApplyService` (service_ops.go:580) calls `render(cs)` which covers
inline QoS entries added at lines 552-555. Only the standalone
`ApplyQoS` path is affected.

**Fix**: Add `if err := n.render(cs); err != nil { return nil, err }`
before the return statement at line 47. **Fixed** — render call added.

---

## Critical Analysis: Does This Optimally Serve the Architecture?

The conformance audit (now 299/299 PASS after fixing Finding 1) asks "does this follow the rules?"
This section asks the harder question: "does this serve what the
architecture demands in the most optimal manner?"

### Finding 2: Service-Applied QoS Has No Intent — Broken Operational Symmetry

**Severity**: High — creates orphaned CONFIG_DB entries in the incremental path.

**Issue**: `ApplyService` generates QoS entries inline (lines 552-555)
by calling `i.bindQos()` and `GenerateDeviceQoSConfig()` directly,
without writing a dedicated QoS intent (`interface|{name}|qos`). But
`RemoveService` at line 1229 calls `i.unbindQos()`, which reads
`GetIntent("interface|{name}|qos")` — a key that was never written.

Result: `unbindQos()` returns empty. The per-interface QoS entries
(PORT_QOS_MAP, QUEUE) are never deleted by RemoveService's ChangeSet.

```
ApplyService:
  writes intent "interface|Eth0" with params { qos_policy: "POLICY" }
  generates PORT_QOS_MAP|Eth0, QUEUE|Eth0|0..N entries (no QoS intent)

RemoveService:
  i.unbindQos() → GetIntent("interface|Eth0|qos") → nil → returns []
  ❌ PORT_QOS_MAP|Eth0 and QUEUE|Eth0|* are orphaned on device
```

Device-wide QoS entries (DSCP_TO_TC_MAP, etc.) are cleaned up via the
binding's `qos_policy` param (lines 1232-1236). But per-interface QoS
entries are not.

**Why the tests don't catch it**: Test suites use Reconcile (full
projection delivery via ReplaceAll), which clears all owned tables.
The incremental removal path is untested.

**Impact**: After incremental `RemoveService`, drift shows orphaned
PORT_QOS_MAP and QUEUE entries. Reconcile fixes it. But the architecture
demands that incremental operations be self-sufficient — Apply delivers
what render produced, and the ChangeSet must be complete.

**Root cause**: Two representations for the same concept. Standalone
`ApplyQoS` writes a `interface|{name}|qos` intent. Service-inline QoS
does not. `unbindQos` only handles the intent-backed path.

**Fix options**:
1. Have `ApplyService` write a QoS sub-intent (`interface|{name}|qos`)
   so `unbindQos` works uniformly. This is the architecturally correct
   fix — every applied concept has an intent.
2. Have `RemoveService` generate QoS delete entries from the binding's
   `qos_policy` param instead of calling `unbindQos()`. This works but
   duplicates cleanup logic.

Option 1 aligns with the architecture: the intent DB should contain
every concept that was configured. If QoS was applied, there should be
a QoS intent.

### Finding 3: `ListInterfaces` Reads Projection for PortChannel Names

**Severity**: Low — correctness is fine, but violates the architecture's
universal rule.

**Issue**: `ListInterfaces` (node.go:942) iterates `configDB.PortChannel`
to collect PortChannel names. PortChannel is intent-managed (created by
`CreatePortChannel` which writes a portchannel intent). Reading
`configDB.PortChannel` for a display query violates §1: "Query/display
methods build responses from intent records and params, not from typed
configDB structs."

**What it should do**: Use `IntentsByPrefix("portchannel|")` for
PortChannel names, plus `configDB.Port` for physical ports (which IS
pre-intent infrastructure via RegisterPort).

**Why it matters**: Coupling display code to the PORTCHANNEL table
structure means a change in how PortChannels are rendered would break
display. Reading intents keeps the display layer independent of SONiC
table structure.

### Finding 4: PortChannel Property Reads From Projection

**Severity**: Low — principled issue, not a correctness issue.

**Issue**: `AdminStatus` (interface.go:68), `MTU` (interface.go:119), and
`Description` (interface.go:249) read `configDB.PortChannel[name]` for
PortChannel interfaces. The audit classified these as PROJECTION_READ_ALLOWED
("pre-intent infrastructure"), but PortChannel is NOT pre-intent — it is
created by `CreatePortChannel` via intent.

**Nuance**: `admin_status` is not stored in the portchannel intent params —
it's a constant default ("up") in the config generator. So there's no intent
param to read. The projection is the only place this value exists.

**The deeper issue**: If a value affects CONFIG_DB but isn't in the intent,
it can't be read from the intent. The Intent Round-Trip Completeness rule
says "every param that affects CONFIG_DB output must be stored." But
`admin_status: "up"` is a default, not a caller-provided param. The rule
is about reconstruction (does replay produce the same CONFIG_DB?), not about
display queries.

**Fix**: For PortChannel, `admin_status` should be stored in the intent
params (it's always "up" at creation, but `SetProperty` can change it).
Then `AdminStatus()` can read from intents consistently:
1. Check `GetIntent("interface|{name}|property|admin_status")` for explicit
   override
2. Check `GetIntent("portchannel|{name}")` params for creation-time value
3. Fall back to PORT table for physical ports (pre-intent)

### Finding 5: `interfaceExistsInConfigDB` — Dead Name, Live Indirection

**Severity**: Trivial — misleading name, unnecessary wrapper.

**Issue**: `interfaceExistsInConfigDB` (node.go:965) delegates to
`InterfaceExists()` which performs intent-based checks. The name says
"configDB" but the implementation reads intents. The function is a
single-line wrapper with no callers that couldn't call `InterfaceExists`
directly.

**Fix**: Rename to `interfaceExists` or inline at call sites.

### Finding 6: Intent DB Access via `ConfigDB().NewtronIntent`

**Severity**: Style — functionally correct, visually ambiguous.

**Issue**: `RemoveService` (service_ops.go:1196, 1323, 1375) accesses
the intent DB via `n.ConfigDB().NewtronIntent` instead of using
`IntentsByParam`/`IntentsByPrefix`. The expression `n.ConfigDB().X` looks
identical whether X is `NewtronIntent` (intent DB) or `VLAN` (projection).
A reader scanning for projection reads cannot distinguish them without
reading the field name.

**Fix**: Use `n.IntentsByParam(sonic.FieldServiceName, serviceName)` at
line 1196, and the appropriate intent query helper at lines 1323 and 1375.
This makes the intent-vs-projection distinction syntactically clear.

### Finding 7: `DeleteVRF` missing `render(cs)` — Config Method Contract Violation

**Severity**: High — called from API/CLI. Projection retains deleted VRF entries.

**Location**: `vrf_ops.go:170`

**Issue**: Builds ChangeSet with VRF and BGP_GLOBALS delete entries, calls
`deleteIntent`, but never calls `render(cs)`. Within the same episode
(before `RebuildProjection`), subsequent operations see stale VRF state
in the projection.

**Fix**: Added `render(cs)` after `deleteIntent`. **Fixed.**

### Finding 8: `UnbindIPVPN` missing `render(cs)` — Config Method Contract Violation

**Severity**: High — called from API/CLI. Projection retains IP-VPN entries.

**Location**: `vrf_ops.go:306`

**Issue**: Builds large ChangeSet (VRF vni clear, IP-VPN entries, transit
VLAN delete), calls `deleteIntent`, but never calls `render(cs)`.

**Fix**: Added `render(cs)` after `deleteIntent`. **Fixed.**

### Finding 9: `AddBGPPeer` missing `render(cs)` — Config Method Contract Violation

**Severity**: High — called from API, CLI, and ReplayStep. BGP_NEIGHBOR
entries never rendered into projection.

**Location**: `interface_bgp_ops.go:35`

**Issue**: Builds ChangeSet with BGP_NEIGHBOR entries via `buildChangeSet`,
calls `writeIntent`, but never calls `render(cs)`. During reconstruction
the projection is incomplete — BGP neighbor CONFIG_DB entries are missing
from the projection, causing drift on correctly-configured devices.

**Fix**: Added `render(cs)` after `writeIntent`. **Fixed.**

### Finding 10: `TeardownVTEP` missing `render(cs)`

**Severity**: Medium — currently unused (defined but never called outside
node package).

**Location**: `evpn_ops.go:275`

**Fix**: Added `render(cs)` before return. **Fixed.**

### Finding 11: `RemoveBGPGlobals` missing `render(cs)`

**Severity**: Medium — currently unused.

**Location**: `bgp_ops.go:517`

**Fix**: Added `render(cs)` before return. **Fixed.**

### Finding 12: `RemoveIP` missing `render(cs)`

**Severity**: Low — dead code (defined but no callers).

**Location**: `interface_ops.go:94`

**Fix**: Added `render(cs)` before return. **Fixed.**

### Finding 13: Inline `create-acl` intent in `ApplyService` — incomplete params

**Severity**: High — breaks reconstruction (`InitFromDeviceIntent`) for
any device with service-generated ACLs.

**Location**: `service_ops.go:521-524` (ingress), `service_ops.go:542-544` (egress)

**Issue**: When `ApplyService` auto-creates an ACL table for a filter-spec,
the `writeIntent` call stores only `{rules: "..."}`. The standalone
`CreateACL` stores `name`, `type`, `stage`, `ports`, `description`.
During reconstruction, `IntentsToSteps` processes this intent (not in
`skipInReconstruct`), and `ReplayStep`'s `create-acl` case requires
`name` — fails with `"create-acl: missing 'name' param"`.

This is an Intent Round-Trip Completeness violation: the params stored
in the intent are insufficient for reconstruction to reproduce the
same CONFIG_DB output.

**Fix**: Added `name`, `type`, `stage`, `ports`, `description` to both
inline `writeIntent` calls. **Fixed.**

### Finding 14: Phantom "evpn" intent — three sites check an intent no production code writes

**Severity**: Medium — operational correctness risk. If the phantom intent
is absent (always, in production), the code takes the wrong branch.

**Location**: `evpn_ops.go:207` (SetupVTEP), `precondition.go:142`
(RequireVTEPConfigured), `service_ops.go:139` (ApplyService EVPN guard)

**Issue**: Three code sites call `GetIntent("evpn")` but zero production
`writeIntent` calls create an "evpn" intent. `SetupVTEP` is a sub-operation
of `SetupDevice` and writes no intent of its own. Tests manually inject
`NewtronIntent["evpn"]` to make checks pass.

The reliable proxy for "VTEP is configured" is the "device" intent's
`source_ip` param — `SetupDevice` always stores it (baseline_ops.go:43),
and `SetupDevice` with `source_ip` always calls `SetupVTEP`.

**Fix**:
- `evpn_ops.go:207`: Removed conditional — VTEP creation is now
  unconditional (render handles upserts safely).
- `precondition.go:142`: Changed to check `GetIntent("device")` with
  `params["source_ip"] != ""`.
- `service_ops.go:139`: Changed to check `GetIntent("device")` with
  `params["source_ip"] != ""`.
- Three test files updated to inject "device" intent instead of "evpn".
**Fixed.**

### Finding 15: Dead `RefreshService` intent deletion using wrong key

**Severity**: Low — dead code (no-op due to wrong key, but misleading).

**Location**: `service_ops.go:1489-1493`

**Issue**: `RefreshService` contained:
```go
configDB := n.ConfigDB()
delete(configDB.NewtronIntent, i.name)
```
This uses `i.name` (e.g., "Ethernet0") but intent keys are prefixed
(`"interface|Ethernet0"`). The delete was always a no-op. Additionally,
it bypasses the intent API by directly mutating `configDB.NewtronIntent`.

**Fix**: Deleted the dead code block. **Fixed.**

### Finding 16: `ExecuteOp` — dead code with wrong architecture pattern

**Severity**: Medium — architectural violation, zero callers.

**Location**: `node.go:875-904`

**Issue**: `ExecuteOp` implements Lock→fn→Apply→Unlock without the
snapshot/restore/dry-run mechanism that the real entry point `Execute`
(pkg/newtron/node.go:287) provides. It also lacks `RebuildProjection`
which `execute()` in actors.go calls before every operation. Zero
callers — completely dead code.

**Fix**: Deleted the function (30 lines). Updated architecture doc
references from `ExecuteOp` to `Execute`. **Fixed.**

### Finding 17: `ApplyService` route policy backfill uses fragile `cs.Changes[0]`

**Severity**: Low — correct today but fragile.

**Location**: `service_ops.go:585-594`

**Issue**: After `writeIntent` (which calls `cs.Prepend`, placing the
intent entry at `cs.Changes[0]`), route policy backfill mutates
`cs.Changes[0].Fields[...]` directly. This is correct because `Prepend`
guarantees position 0, but the raw index is fragile — any future change
to `Prepend` semantics or ChangeSet structure would silently corrupt
the wrong entry.

**Fix**: Added `intentEntry := &cs.Changes[0]` immediately after
`writeIntent`, then replaced `cs.Changes[0].Fields[...]` references
with `intentEntry.Fields[...]`. The named variable makes the intent
explicit and localizes the position assumption. **Fixed.**

### Finding 18: Dead `handler_provisioning.go` — entire file

**Severity**: Critical — dead code that broke the build.

**Location**: `api/handler_provisioning.go` (entire file)

**Issue**: Every symbol this file references (`GenerateDeviceComposite`,
`storeComposite`, `ProvisioningHandleResponse`, `getComposite`,
`removeComposite`, `connectAndLocked`) had been deleted in prior work.
The file was dead end-to-end and prevented `go build ./...` from passing.

**Fix**: Deleted the file. **Fixed.**

### Finding 19: Dead `connectAndLocked` in `actors.go`

**Severity**: Medium — dead code, only caller was in deleted F18 file.

**Location**: `api/actors.go:321-333`

**Issue**: `connectAndLocked()` (lock → fn → unlock pattern) had zero
callers after F18 deletion. The unified architecture uses `Execute()`
for all write operations.

**Fix**: Deleted the method. Updated stale comment reference. **Fixed.**

### Finding 20: `BindACL` missing `render(cs)`

**Severity**: High — ACL binding not applied to projection.

**Location**: `interface_ops.go:366-399`

**Issue**: `BindACL` builds a ChangeSet with `cs.Update(...)` but never
calls `render(cs)`. The ACL_TABLE binding update is not rendered into
the projection — subsequent operations see stale state.

**Fix**: Added `n.render(cs)` before the return. **Fixed.**

### Finding 21: `UnbindACL` missing `render(cs)`

**Severity**: High — ACL unbinding not applied to projection.

**Location**: `interface_ops.go:402-441`

**Issue**: Same as F20 — builds ChangeSet but never renders.

**Fix**: Added `n.render(cs)` before the return. **Fixed.**

### Finding 22: `SetProperty` missing `render(cs)`

**Severity**: Medium — PORT/PORTCHANNEL update not applied to projection.

**Location**: `interface_ops.go:447-514`

**Issue**: `SetProperty` builds PORT or PORTCHANNEL update entries but
never calls `render(cs)`. The property change is not rendered into the
projection.

**Fix**: Added `n.render(cs)` before the return. **Fixed.**

### Finding 23: `RemoveLoopback` missing `render(cs)`

**Severity**: Medium — loopback deletion not applied to projection.

**Location**: `baseline_ops.go:164-190`

**Issue**: `RemoveLoopback` builds `cs.Delete(...)` entries for
LOOPBACK_INTERFACE but never calls `render(cs)`.

**Fix**: Added `n.render(cs)` before the return. **Fixed.**

### Finding 24: Dead `VerifyCommitted()` client method

**Severity**: Low — dead code, no server route exists.

**Location**: `client/node.go:428-435`

**Issue**: POSTs to `/verify-committed` which has no server route.

**Fix**: Deleted. **Fixed.**

### Finding 25: Dead `ListIntents()` client method + CLI command

**Severity**: Low — dead code, no server route exists.

**Location**: `client/node.go:472-478`, `cmd/newtron/cmd_intent.go:25-61`

**Issue**: `ListIntents()` GETs `/intents` which has no server route.
The `intentListCmd` CLI command calls this dead method.

**Fix**: Deleted client method and CLI command. **Fixed.**

### Finding 27: Dead `cmd_preferences.go`

**Severity**: Low — dead code, never registered in main.go.

**Location**: `cmd/newtron/cmd_preferences.go` (entire file)

**Issue**: `preferencesCmd` was never added to the root command.
Duplicate of `cmd_settings.go`.

**Fix**: Deleted the file. **Fixed.**

### Finding 32: Misleading comment in `scanRoutePoliciesByPrefix`

**Severity**: Low — comment claims "ground truth from device" but reads projection.

**Location**: `service_ops.go:997-1000`

**Issue**: Comment said "what actually exists on the device (ground truth for
diff)" but the function scans the projection, not the device.

**Fix**: Rewrote comment to accurately describe projection scan for
blue-green migration. **Fixed.**

### Finding 33: Direct `configDB.NewtronIntent` access in `UnbindIPVPN`

**Severity**: Low — bypasses intent API.

**Location**: `vrf_ops.go:315-326`

**Issue**: `UnbindIPVPN` iterates `n.configDB.NewtronIntent` directly
instead of using `IntentsByParam`. This bypasses the intent query API
and couples to the raw map structure.

**Fix**: Replaced with `n.IntentsByParam(sonic.FieldVRFName, vrfName)`. **Fixed.**

### Finding 34: Dead `splitKey` function

**Severity**: Low — dead code, zero production callers.

**Location**: `node.go:950-958`

**Issue**: Utility function with zero callers. Tests existed for it.

**Fix**: Deleted function and tests. **Fixed.**

### Finding 35: Dead `parseServiceFromACL` function

**Severity**: Low — dead code, zero production callers.

**Location**: `interface.go:343-353`

**Issue**: Utility function using stale naming convention, zero callers.

**Fix**: Deleted function and tests. **Fixed.**

### Finding 36: Dead `doPut()` client method

**Severity**: Low — dead code, zero callers, no PUT routes exist.

**Location**: `client/client.go:150-171`

**Issue**: HTTP PUT helper with zero callers. No server route accepts PUT.

**Fix**: Deleted. **Fixed.**

### Finding 37: Missing `CLI-WORKAROUND` tags on `ApplyFRRDefaults`

**Severity**: Low — violates Redis-First Interaction Principle tagging.

**Location**: `node.go:1075-1112`

**Issue**: Uses vtysh CLI commands without the required `CLI-WORKAROUND`
tag per CLAUDE.md "Redis-First Interaction Principle."

**Fix**: Added `CLI-WORKAROUND(frr-defaults)` tag with gap and resolution. **Fixed.**

### Findings Not Applicable (Investigated and Dismissed)

| ID | Description | Reason for dismissal |
|----|-------------|---------------------|
| F26 | `SetDeviceMetadata` lacks intent | Sub-operation of `SetupDevice` (which writes its own intent) and pre-intent `InitDevice`. Correct by design. |
| F28 | IntentTree client/server shape mismatch | Deferred — needs deeper analysis of filter params. |
| F29 | Duplicate `handleListNeighbors` | Documented alias for `/bgp/check` per `api.md:105`. Not dead. |
| F30 | `ConfigureInterfaceRequest` API boundary | Broader pattern (all `api.*Request` types in client). Deferred. |
| F31 | `OperationIntent` legacy types | Used by Lock()'s legacy STATE_DB migration. Still needed. |
| F38 | `audit/event.go` internal import | Internal package importing internal package — fine. |
| F39 | Unimplemented `--json` flag | Known TODO placeholder, not a bug. |

### Resolution Status

All applicable findings from all four audit passes have been resolved:

| Finding | Status | What was done |
|---------|--------|---------------|
| 1. ApplyQoS missing render | FIXED | Added `n.render(cs)` before return in `qos_ops.go` |
| 2. Service QoS no intent | FIXED | `ApplyService` now writes QoS sub-intent (`interface\|{name}\|qos`); `RemoveService` deletes it before parent intent |
| 3. ListInterfaces reads projection | FIXED | Replaced `configDB.PortChannel` iteration with `IntentsByPrefix("portchannel\|")` in `node.go` |
| 4. PortChannel property reads | FIXED | `AdminStatus` reads property sub-intent → creation default; `MTU` reads property sub-intent → portchannel intent param → PORT table; `Description` reads property sub-intent → PORT table |
| 5. interfaceExistsInConfigDB | FIXED | Deleted wrapper; caller uses `n.InterfaceExists(name)` directly |
| 6. ConfigDB().NewtronIntent | FIXED | Three sites in `RemoveService` replaced with `IntentsByParam`/`IntentsByOp` calls |
| 7. DeleteVRF missing render | FIXED | Added `n.render(cs)` in `vrf_ops.go` |
| 8. UnbindIPVPN missing render | FIXED | Added `n.render(cs)` in `vrf_ops.go` |
| 9. AddBGPPeer missing render | FIXED | Added `n.render(cs)` in `interface_bgp_ops.go` |
| 10. TeardownVTEP missing render | FIXED | Added `n.render(cs)` in `evpn_ops.go` |
| 11. RemoveBGPGlobals missing render | FIXED | Added `n.render(cs)` in `bgp_ops.go` |
| 12. RemoveIP missing render | FIXED | Added `n.render(cs)` in `interface_ops.go` |
| 13. Inline create-acl incomplete params | FIXED | Added `name`, `type`, `stage`, `ports`, `description` to inline `writeIntent` calls in `service_ops.go` |
| 14. Phantom "evpn" intent | FIXED | Three sites changed to check `GetIntent("device")` with `source_ip`; three test files updated |
| 15. Dead RefreshService intent deletion | FIXED | Deleted dead no-op code in `service_ops.go` |
| 16. ExecuteOp dead code | FIXED | Deleted 30-line function from `node.go`; updated doc references |
| 17. ApplyService fragile cs.Changes[0] | FIXED | Named `intentEntry` variable localizes position assumption |
| 18. Dead `handler_provisioning.go` | FIXED | Deleted entire file (build-breaking dead code) |
| 19. Dead `connectAndLocked` | FIXED | Deleted method from `actors.go`; fixed stale comment |
| 20. BindACL missing render | FIXED | Added `n.render(cs)` in `interface_ops.go` |
| 21. UnbindACL missing render | FIXED | Added `n.render(cs)` in `interface_ops.go` |
| 22. SetProperty missing render | FIXED | Added `n.render(cs)` in `interface_ops.go` |
| 23. RemoveLoopback missing render | FIXED | Added `n.render(cs)` in `baseline_ops.go` |
| 24. Dead `VerifyCommitted` | FIXED | Deleted client method (no server route) |
| 25. Dead `ListIntents` + CLI | FIXED | Deleted client method and `intentListCmd` (no server route) |
| 27. Dead `cmd_preferences.go` | FIXED | Deleted file (never registered in main.go) |
| 32. Misleading comment | FIXED | Rewrote `scanRoutePoliciesByPrefix` comment |
| 33. Direct configDB.NewtronIntent | FIXED | Replaced with `IntentsByParam` in `UnbindIPVPN` |
| 34. Dead `splitKey` | FIXED | Deleted function and tests |
| 35. Dead `parseServiceFromACL` | FIXED | Deleted function and tests |
| 36. Dead `doPut` | FIXED | Deleted client method |
| 37. Missing CLI-WORKAROUND tags | FIXED | Added `CLI-WORKAROUND(frr-defaults)` to `ApplyFRRDefaults` |

### Architectural Verdict

The codebase is **fully conformant** with the unified pipeline architecture
after resolving all 30 findings across four audit passes. The classes of
violation found:

1. **Missing `render(cs)` (Findings 1, 7-12, 20-23)**: Eleven config methods
   built ChangeSets but never updated the projection. Four high severity
   (BindACL, UnbindACL, and two from earlier passes), five medium, two low.

2. **Missing intent records (Finding 2)**: Service-applied QoS had no
   dedicated intent, breaking operational symmetry on teardown.

3. **Incomplete intent params (Finding 13)**: Inline ACL intents in
   `ApplyService` stored insufficient params for reconstruction.

4. **Phantom intent checks (Finding 14)**: Three sites checked an intent
   no production code writes — the reliable proxy is the "device" intent's
   `source_ip` param.

5. **Dead code (Findings 15, 16, 18, 19, 24, 25, 27, 34, 35, 36)**: Ten
   dead code items — files, functions, methods, and CLI commands with zero
   callers. Includes build-breaking `handler_provisioning.go`.

6. **Fragile internal references (Finding 17)**: Raw `cs.Changes[0]` index
   for intent backfill — named variable makes the contract explicit.

7. **Intent API bypass (Finding 33)**: Direct `configDB.NewtronIntent`
   iteration instead of using `IntentsByParam`.

8. **Documentation gaps (Findings 32, 37)**: Misleading comment and missing
   `CLI-WORKAROUND` tags.

The remaining findings (3-6) were principled purity improvements.

The architecture's core invariants now hold across all remaining functions:
- "by return, both the intent DB and the projection are updated" (§2)
- "intent DB is the decision substrate" (§1)
- "every param that affects CONFIG_DB output must be stored" (§4)

---

## Summary

| Category | Count | PASS | FAIL |
|----------|-------|------|------|
| CONSTRUCTION | 5 | 5 | 0 |
| ACCESSOR | 25 | 25 | 0 |
| INTENT_READ | 22 | 22 | 0 |
| INTENT_WRITE | 4 | 4 | 0 |
| PROJECTION_WRITE | 3 | 3 | 0 |
| CONFIG_GENERATOR | 54 | 54 | 0 |
| CONFIG_METHOD | 53 | 53 | 0 |
| QUERY | 13 | 13 | 0 |
| PRECONDITION | 20 | 20 | 0 |
| REFERENCE_COUNT | 1 | 1 | 0 |
| RECONSTRUCTION | 9 | 9 | 0 |
| CHANGESET | 13 | 13 | 0 |
| DEVICE_IO | 20 | 20 | 0 |
| LIFECYCLE | 6 | 6 | 0 |
| PROJECTION_READ_ALLOWED | 10 | 10 | 0 |
| KEY_HELPER | 17 | 17 | 0 |
| UTILITY | 23 | 23 | 0 |
| **Total** | **291** | **291** | **0** |

All functions now conform to the unified pipeline architecture (291
remaining after deleting dead code across four audit passes: `ExecuteOp`,
`splitKey`, `parseServiceFromACL`, `connectAndLocked`, `handler_provisioning.go`,
`doPut`, `VerifyCommitted`, `ListIntents`/`intentListCmd`, `cmd_preferences.go`).

The initial audit incorrectly marked 12 functions as PASS (plus the 1 it
caught as FAIL). A second-pass critical analysis found 12 additional
violations. A third-pass optimality audit found 4 more (Findings 14-17).
A fourth-pass comprehensive audit found 15 more (Findings 18-37):
dead files/functions, missing render calls, intent API bypasses, and
documentation gaps.
Entries marked `AUDIT MISSED` in the tables above are functions the
initial audit wrongly passed.
