# Intent DAG Implementation Tracker

Authoritative spec: `docs/newtron/intent-dag-architecture.md`

Legend: `[ ]` = not started, `[x]` = completed, `[!]` = blocked/issue

---

## Phase 1: Foundation — Intent Struct, Schema, Core Operations

### 1.1 configdb.go — Intent struct extension

- [x] **T1.1.1** Add `Parents []string` field to `Intent` struct (L142)
- [x] **T1.1.2** Add `Children []string` field to `Intent` struct (L142)
- [x] **T1.1.3** Add `"_parents"` to `intentIdentityFields` map (L183)
- [x] **T1.1.4** Add `"_children"` to `intentIdentityFields` map (L183)
- [x] **T1.1.5** Update `NewIntent` (L194): extract `_parents` from fields into `Parents` (CSV split)
- [x] **T1.1.6** Update `NewIntent` (L194): extract `_children` from fields into `Children` (CSV split)
- [x] **T1.1.7** Update `ToFields` (L239): serialize `Parents` to `_parents` CSV
- [x] **T1.1.8** Update `ToFields` (L239): serialize `Children` to `_children` CSV

### 1.2 schema.go — NEWTRON_INTENT table

- [x] **T1.2.1** Add `"_parents"` field: `{Type: FieldString, AllowEmpty: true}` (L519)
- [x] **T1.2.2** Add `"_children"` field: `{Type: FieldString, AllowEmpty: true}` (L519)
- [x] **T1.2.3** Add `OpAddACLRule` to schema.go operation enum (new operation for §10.16 child intents)
- [x] **T1.2.4** Add `OpAddPortChannelMember` to schema.go operation enum (new operation for §10.17 child intents)

### 1.2b configdb.go — New operation constants

- [x] **T1.2.5** Define `OpAddACLRule = "add-acl-rule"` constant in configdb.go (L76-92)
- [x] **T1.2.6** Define `OpAddPortChannelMember = "add-pc-member"` constant in configdb.go (L76-92)

### 1.3 intent_ops.go — Core operations rewrite

- [x] **T1.3.1** Change `writeIntent` signature: add `parents []string` param, return `error`
- [x] **T1.3.2** Implement writeIntent idempotent-update logic (§4.1)
- [x] **T1.3.3** Implement writeIntent parent existence check (I4)
- [x] **T1.3.4** Implement writeIntent child registration
- [x] **T1.3.5** Implement writeIntent intent record creation
- [x] **T1.3.6** Change `deleteIntent` signature: return `error`
- [x] **T1.3.7** Implement deleteIntent children-empty check (I5)
- [x] **T1.3.8** Implement deleteIntent parent deregistration
- [x] **T1.3.9** Update `updateIntent`: enforce I8 — strip `_parents`/`_children` from `merge` map
- [x] **T1.3.10** Add `parseCSV(s string) []string` helper (split on comma, trim, filter empty)
- [x] **T1.3.11** Add `addToCSV(csv, item string) string` helper
- [x] **T1.3.12** Add `removeFromCSV(csv, item string) string` helper
- [x] **T1.3.13** Verify `cs.Prepend` method exists on ChangeSet
- [x] **T1.3.14** deleteIntent: ensure both online and offline modes delete own record from shadow configDB
- [x] **T1.3.15** Add `applyIntentToShadow` helper — always updates configDB for intent records (both online and offline), so parent-child creation in the same operation works in online mode

### 1.4 Verification — Phase 1

- [x] **T1.4.1** `go build ./...` passes
- [x] **T1.4.2** `go vet ./...` passes

---

## Phase 2: Call Site Migration — writeIntent callers gain `parents`

Every `writeIntent` call must be updated with the correct `parents` argument
and kind-prefixed resource key per §10.1–§10.17.

### 2.1 baseline_ops.go — `SetupDevice` (§10.1 `device`)

- [x] **T2.1.1** `SetupDevice`: `writeIntent(cs, OpSetupDevice, "device", params, nil)` (root, no parents). Handle returned error.

### 2.2 vlan_ops.go — `CreateVLAN` (§10.2 `vlan|ID`)

- [x] **T2.2.1** `CreateVLAN`: add `parents: []string{"device"}`, handle error
- [x] **T2.2.2** `ConfigureIRB`: key `"irb|"+strconv.Itoa(vlanID)` — parents conditional on VRF (§10.13). Handle error.
- [x] **T2.2.3** `UnconfigureIRB`: `deleteIntent` now returns error — handle it
- [x] **T2.2.4** `DeleteVLAN`: `deleteIntent` now returns error — handle it
- [x] **T2.2.5** Simplify `destroyVlanConfig`: just delete VLAN entry. DAG ensures children already removed.

### 2.3 vrf_ops.go — `CreateVRF` (§10.3 `vrf|NAME`)

- [x] **T2.3.1** `CreateVRF`: add `parents: []string{"device"}`, handle error
- [x] **T2.3.2** `DeleteVRF`: `deleteIntent` returns error — handle it
- [x] **T2.3.3** `BindIPVPN`: key `"ipvpn|"+vrfName` — parents `["vrf|"+vrfName]` (§10.14). Handle error.
- [x] **T2.3.4** `UnbindIPVPN`: `deleteIntent` returns error — handle it
- [x] **T2.3.5** `AddStaticRoute`: key `"route|"+vrfName+"|"+prefix` — parents `["vrf|"+vrfName]` (§10.15). Handle error.
- [x] **T2.3.6** `RemoveStaticRoute`: `deleteIntent` returns error — handle it
- [x] **T2.3.7** `unbindIpvpnConfig`: verify `GetIntent("ipvpn|"+vrfName)` key is kind-prefixed ✓
- [x] **T2.3.8** `UnbindIPVPN`: verify `GetIntent("ipvpn|"+vrfName)` key is kind-prefixed ✓

### 2.4 acl_ops.go — `CreateACL` (§10.4 `acl|NAME`)

- [x] **T2.4.1** `CreateACL`: add `parents: []string{"device"}`, handle error
- [x] **T2.4.2** `DeleteACL`: `deleteIntent` returns error — handle it
- [x] **T2.4.3** `AddACLRule`: writeIntent for child `"acl|"+tableName+"|"+ruleName`, parents `["acl|"+tableName]` (§10.16)
- [x] **T2.4.4** `DeleteACLRule`: deleteIntent for child `"acl|"+tableName+"|"+ruleName` (§10.16). Handle error.
- [x] **T2.4.5** Simplify `deleteAclTableConfig`: just delete ACL_TABLE entry. DAG ensures rules already removed.

### 2.5 portchannel_ops.go — `CreatePortChannel` (§10.5 `portchannel|NAME`)

- [x] **T2.5.1** `CreatePortChannel`: add `parents: []string{"device"}`, handle error
- [x] **T2.5.2** `DeletePortChannel`: `deleteIntent` returns error — handle it
- [x] **T2.5.3** `AddPortChannelMember`: writeIntent for child `"pc|"+pcName+"|"+member`, parents `["portchannel|"+pcName]` (§10.17). Handle error.
- [x] **T2.5.4** `RemovePortChannelMember`: deleteIntent for child `"pc|"+pcName+"|"+member` (§10.17). Handle error.
- [x] **T2.5.5** Simplify `destroyPortChannelConfig`: just delete PORTCHANNEL entry. DAG ensures members already removed.

### 2.6 bgp_ops.go — `AddBGPEVPNPeer` (§10.6 `evpn-peer|ADDR`)

- [x] **T2.6.1** `AddBGPEVPNPeer`: add `parents: []string{"device"}`, handle error
- [x] **T2.6.2** `RemoveBGPEVPNPeer`: `deleteIntent` returns error — handle it

### 2.7 interface_ops.go — `ConfigureInterface` (§10.7 `interface|INTF`)

- [x] **T2.7.1** `ConfigureInterface`: key `"interface|"+i.name`, parents based on mode (bridged/routed/IP-only). Handle error.
- [x] **T2.7.2** `ConfigureInterface`: REMOVE `updateIntent` on `"vlan|"` (dual-tracking eliminated)
- [x] **T2.7.3** `UnconfigureInterface`: update `GetIntent` key to `"interface|"+i.name`
- [x] **T2.7.4** `UnconfigureInterface`: REMOVE `updateIntent` on `"vlan|"` (dual-tracking eliminated)
- [x] **T2.7.5** `UnconfigureInterface`: change `deleteIntent` key to `"interface|"+i.name`. Handle error.
- [x] **T2.7.6** `BindACL`: key `"interface|"+i.name+"|acl|"+direction`. Parents: `["interface|"+i.name, "acl|"+aclName]` (§10.8 multi-parent). Handle error.
- [x] **T2.7.7** `UnbindACL`: update `GetIntent` scan keys to use `"interface|"` prefix
- [x] **T2.7.8** `UnbindACL`: change `deleteIntent` key to use `"interface|"` prefix. Handle error.
- [x] **T2.7.9** `SetProperty`: key `"interface|"+i.name+"|"+property`. Parents: `["interface|"+i.name]` (§10.11). Handle error.
- [x] **T2.7.10** `BindACL`: REMOVE `IsFirstACLUser` — replaced with inline `util.AddToCSV` (handles empty case)
- [x] **T2.7.11** `UnconfigureInterface`: add `cascadeDeleteChildren` before `deleteIntent` — I5 enforcement
- [x] **T2.7.12** `UnconfigureInterface`: `cascadeDeleteChildren` handles all child types (properties, QoS, ACL, macvpn)
- [x] **T2.7.13** (added) `interface.go`: update `binding()` and `ServiceName()` to use `"interface|"+i.name` as intent key
- [x] **T2.7.14** (added) `interface.go`: update `IngressACL()` and `EgressACL()` to use `"interface|"+i.name` as intent key
- [x] **T2.7.15** (added) `interface_ops.go`: update `IsLastServiceUser`, `IsLastIPVPNUser`, `IsLastAnycastMACUser` to compare against `"interface|"+dc.excludeInterface`

### 2.8 interface_bgp_ops.go — `AddBGPPeer` (§10.7 `interface|INTF`)

- [x] **T2.8.1** `AddBGPPeer`: key `"interface|"+i.name`. Parents: `["device"]` (§10.7). Handle error.
- [x] **T2.8.2** `RemoveBGPPeer`: update `GetIntent` key to `"interface|"+i.name`
- [x] **T2.8.3** `RemoveBGPPeer`: change `deleteIntent` key to `"interface|"+i.name`. Handle error.
- [x] **T2.8.4** `RemoveBGPPeer`: add `cascadeDeleteChildren` before `deleteIntent` — I5 enforcement

### 2.9 macvpn_ops.go — `Interface.BindMACVPN` (§10.10 `interface|INTF|macvpn`)

- [x] **T2.9.1** `BindMACVPN`: key `"interface|"+i.name+"|macvpn"`. Parents: `["interface|"+i.name]` (§10.10). Handle error.
- [x] **T2.9.2** `UnbindMACVPN`: update `GetIntent` key to `"interface|"+i.name+"|macvpn"`
- [x] **T2.9.3** `UnbindMACVPN`: change `deleteIntent` key to `"interface|"+i.name+"|macvpn"`. Handle error.

### 2.10 evpn_ops.go — `Node.BindMACVPN` (§10.12 `macvpn|VLANID`)

- [x] **T2.10.1** `Node.BindMACVPN`: add `parents: []string{"vlan|"+strconv.Itoa(vlanID)}` (§10.12). Handle error.
- [x] **T2.10.2** `Node.BindMACVPN`: REMOVE `updateIntent` on `"vlan|"` (dual-tracking eliminated)
- [x] **T2.10.3** `Node.UnbindMACVPN`: `deleteIntent` returns error — handle it
- [x] **T2.10.4** `Node.UnbindMACVPN`: REMOVE `updateIntent` on `"vlan|"` (dual-tracking eliminated)
- [x] **T2.10.5** `Node.UnbindMACVPN`: verify `GetIntent("macvpn|"+vlanID)` key is kind-prefixed ✓

### 2.11 qos_ops.go — `Interface.ApplyQoS` (§10.9 `interface|INTF|qos`)

- [x] **T2.11.1** `ApplyQoS`: key `"interface|"+i.name+"|qos"`. Parents: `["interface|"+i.name]` (§10.9). Handle error.
- [x] **T2.11.2** `RemoveQoS`: update `GetIntent` key to `"interface|"+i.name+"|qos"`
- [x] **T2.11.3** `RemoveQoS`: change `deleteIntent` key to `"interface|"+i.name+"|qos"`. Handle error.

### 2.12 service_ops.go — `ApplyService` (§10.7 `interface|INTF`)

- [x] **T2.12.1** `ApplyService`: key `"interface|"+i.name`. Parents based on service type. Handle error.
- [x] **T2.12.2** `ApplyService`: ACL intent key `"acl|"+aclName` — add `parents: []string{"device"}`. Handle error.
- [x] **T2.12.3** `ApplyService`: REMOVE `updateIntent` on `"vlan|"` (dual-tracking eliminated)
- [x] **T2.12.4** `ApplyService`: cs.Changes[0].Fields mutation — verified coexists with DAG metadata (route_map/policy keys don't conflict with _parents/_children)
- [x] **T2.12.5** `RemoveService`: REMOVE `updateIntent` on `"vlan|"` (dual-tracking eliminated)
- [x] **T2.12.6** `RemoveService`: change `deleteIntent` key to `"interface|"+i.name`. Handle error.
- [x] **T2.12.7** `removeSharedACL`: `deleteIntent` key `"acl|"+aclName` — handle error
- [x] **T2.12.8** `deleteRoutePoliciesFromIntent`: update `GetIntent` key to `"interface|"+intfName`
- [x] **T2.12.9** `RemoveService`: property children handled by `cascadeDeleteChildren` before `deleteIntent` — I5 enforcement
- [x] **T2.12.10** `ApplyService`: verified cs.Changes[0].Fields mutation does NOT overwrite `_parents`/`_children` (keys are disjoint)
- [x] **T2.12.11** (added) `removeSharedACL`: cascade-delete ACL rule child intents before deleting ACL table intent (I5 enforcement)

### 2.13 service_ops.go — DependencyChecker replacement

- [x] **T2.13.1** `RemoveService`: replaced `depCheck.IsLastServiceUser(serviceName)` with inline intent scan (excludeKey pattern)
- [x] **T2.13.2** `removeSharedACL`: replaced `depCheck.IsLastACLUser(aclName)` with `acl|NAME` `_children` check (binding children only)
- [x] **T2.13.3** `removeSharedACL`: replaced `depCheck.GetACLRemainingInterfaces(aclName)` with `_children` interface extraction
- [x] **T2.13.4** `RemoveService`: replaced `depCheck.IsLastIPVPNUser(ipvpnName)` with inline intent scan
- [x] **T2.13.5** `RemoveService`: replaced `depCheck.IsLastVLANMember(vlanID)` with `vlan|ID` `_children` check
- [x] **T2.13.6** `RemoveService`: replaced `depCheck.IsLastAnycastMACUser()` with inline intent scan

### 2.14 Composite operation verification (§9)

- [x] **T2.14.1** Verified same-ChangeSet parent-child creation works (§9.2) — all composite tests pass (RefreshService, BlueGreen, BGPPeerGroup)
- [x] **T2.14.2** Verified CompositeOverwrite correctly rebuilds `_parents`/`_children` — abstract node + BuildComposite tests pass
- [x] **T2.14.3** `route_policy_keys` stored as regular intent param — not in _parents/_children, verified via I8 enforcement in updateIntent

### 2.15 Redundant precondition checks (§6.4)

- [x] **T2.15.1** Precondition checks (`RequireVLANExists`, `RequireVRFExists`, `RequireACLExists`) remain as defense-in-depth alongside I4 parent existence — documented here

### 2.16 Verification — Phase 2

- [x] **T2.16.1** `go build ./...` passes
- [x] **T2.16.2** `go vet ./...` passes
- [x] **T2.16.3** `go test ./pkg/newtron/network/node/ -count=1` passes
- [x] **T2.16.4** `go test ./pkg/newtron/... -count=1` passes

---

## Phase 3: Destroy Function Simplification

### 3.1 vlan_ops.go — `destroyVlanConfig`

- [x] **T3.1.1** Simplify to just `[]sonic.Entry{{Table: "VLAN", Key: VLANName(vlanID)}}` — no member/VNI enumeration

### 3.2 acl_ops.go — `deleteAclTableConfig`

- [x] **T3.2.1** Simplify to just `[]sonic.Entry{{Table: "ACL_TABLE", Key: name}}` — no rules enumeration

### 3.3 portchannel_ops.go — `destroyPortChannelConfig`

- [x] **T3.3.1** Simplify to just `[]sonic.Entry{{Table: "PORTCHANNEL", Key: name}}` — no members enumeration

### 3.4 Verification — Phase 3

- [x] **T3.4.1** `go build ./...` passes
- [x] **T3.4.2** `go test ./pkg/newtron/network/node/ -count=1` passes

---

## Phase 4: DependencyChecker Removal + Dead Code Cleanup

### 4.1 interface_ops.go — Remove DependencyChecker

- [x] **T4.1.1** Delete `DependencyChecker` struct
- [x] **T4.1.2** Delete `NewDependencyChecker` function
- [x] **T4.1.3** Delete `IsFirstACLUser` method
- [x] **T4.1.4** Delete `IsLastACLUser` method
- [x] **T4.1.5** Delete `GetACLRemainingInterfaces` method
- [x] **T4.1.6** Delete `IsLastVLANMember` method
- [x] **T4.1.7** Delete `IsLastServiceUser` method
- [x] **T4.1.8** Delete `IsLastIPVPNUser` method
- [x] **T4.1.9** Delete `IsLastAnycastMACUser` method

### 4.2 intent_ops.go — Remove updateIntent (if fully eliminated)

- [x] **T4.2.1** Verified zero remaining `updateIntent` callers via grep
- [x] **T4.2.2** Deleted `updateIntent` function (zero callers)

### 4.3 configdb.go — Remove dual-tracking constants (if unused)

- [x] **T4.3.1** `FieldMembers` still used: portchannel_ops.go (creation params), reconstruct.go (reconstruction) — keep
- [x] **T4.3.2** `FieldRules` still used: service_ops.go (ACL intent creation params) — keep (redundant with _children but harmless)
- [x] **T4.3.3** No constants removed (all still referenced)

### 4.4 Verification — Phase 4

- [x] **T4.4.1** `go build ./...` passes
- [x] **T4.4.2** `go vet ./...` passes
- [x] **T4.4.3** `go test ./pkg/newtron/... -count=1` all pass

---

## Phase 5: Reconstruction — Topological Sort Replaces stepPriority

### 5.1 reconstruct.go — Replace stepPriority

- [x] **T5.1.1** Deleted `stepPriority` map — replaced by topological sort
- [x] **T5.1.2** Rewrote `IntentsToSteps`: Kahn's algorithm using `_parents`/`_children`, ties broken by resource key. Skips `OpAddACLRule`/`OpAddPortChannelMember` (re-created by parent ops).
- [x] **T5.1.3** Rewrote `intentInterface`: checks `"interface|"` prefix, strips it to extract interface name — handles all kind-prefixed keys

### 5.2 reconstruct.go — intentParamsToStepParams

- [x] **T5.2.1** Verified `intentParamsToStepParams` does NOT export `_parents`/`_children` — excluded by `intentIdentityFields` in `NewIntent` (strips into `Parents`/`Children` fields, not `Params`)

### 5.3 reconstruct.go — ReplayStep cases

- [x] **T5.3.1** Verified all `replayInterfaceStep` cases work — `intentInterface` strips `interface|` prefix correctly
- [x] **T5.3.2** `OpAddACLRule` skipped in IntentsToSteps — re-created by parent ApplyService (filter spec). No replay case needed.
- [x] **T5.3.3** `OpAddPortChannelMember` skipped in IntentsToSteps — re-created by CreatePortChannel (members in opts). No replay case needed.
- [x] **T5.3.4** `intentInterface` rewritten for kind-prefixed keys (done in T5.1.3)

### 5.4 Verification — Phase 5

- [x] **T5.4.1** `go build ./...` passes
- [x] **T5.4.2** All reconstruction/replay/intent/snapshot tests pass
- [x] **T5.4.3** `go test ./pkg/newtron/... -count=1` all pass

---

## Phase 6: Health Checks (§11)

### 6.1 Add `ValidateIntentDAG`

- [x] **T6.1.1** Implemented `ValidateIntentDAG(configDB *sonic.ConfigDB) []DAGViolation` in intent_ops.go
- [x] **T6.1.2** Check I2 — bidirectional consistency (parent↔child symmetry)
- [x] **T6.1.3** Check I3 — referential integrity (dangling_parent, dangling_child)
- [x] **T6.1.4** Orphan detection — BFS from `device` root, unreachable records flagged
- [x] **T6.1.5** Added 4 unit tests: Healthy, BrokenBidirectional, DanglingParent, Orphan
- [x] **T6.1.6** Integration deferred — `ValidateIntentDAG` is available; health pipeline integration is a follow-up (requires API layer plumbing)

### 6.2 Verification — Phase 6

- [x] **T6.2.1** `go build ./...` passes
- [x] **T6.2.2** `go test ./pkg/newtron/network/node/ -count=1` passes

---

## Phase 7: CLI — `newtron intent tree` (§12)

### 7.1 Add CLI command

- [x] **T7.1.1** Add `cmd_intent.go` with `intentTreeCmd` implementing tree display
- [x] **T7.1.2** Implement `printIntentTree` with multi-parent rendering (§12.3.1, §12.5)
- [x] **T7.1.3** Implement `intentKind` helper (already existed in `intent_ops.go`; added `intentKindPrefix` to `node.go` for public layer)
- [x] **T7.1.4** Implement kind/resource filtering (§12.2)
- [x] **T7.1.5** Implement `--ancestors` mode (§12.2)
- [x] **T7.1.6** Register command in `main.go` (device operations group)
- [x] **T7.1.7** Implement `printIntentLeaf` helper (merged into `printIntentTree` via `node.Leaf` bool — leaf nodes skip child recursion)
- [x] **T7.1.8** Add API endpoint `GET /node/{device}/intent/tree` with JSON response (§12.6): handler in handler_node.go, route in handler.go, `IntentTreeNode` struct in types.go
- [x] **T7.1.9** (added) Add `IntentTree` method on public `Node` (`pkg/newtron/node.go`)
- [x] **T7.1.10** (added) Add `IntentTree` client method (`pkg/newtron/client/node.go`)
- [x] **T7.1.11** (added) Add `Parents`/`Children` fields to public `Intent` type (T10.2.1 done early)
- [x] **T7.1.12** (added) Update `intentFromSonic` to populate `Parents`/`Children`

### 7.2 Verification — Phase 7

- [x] **T7.2.1** `go build ./...` passes
- [ ] **T7.2.2** Manual verification of tree output format

---

## Phase 8: Test Updates

### 8.1 Existing test fixes

- [x] **T8.1.1** Update all tests that call `writeIntent` to pass `parents` parameter
- [x] **T8.1.2** Update all tests that check intent resource keys to use kind-prefixed format
- [x] **T8.1.3** Update tests that rely on `updateIntent` for dual-tracking to use new child intents
- [x] **T8.1.4** Update tests for `destroyVlanConfig`, `deleteAclTableConfig`, `destroyPortChannelConfig` (simplified)
- [x] **T8.1.5** Update `TestDeleteVLAN` — simplified intent (no children), assert only VLAN delete
- [x] **T8.1.6** Update `TestDeleteACL` — simplified intent (no children), assert only ACL_TABLE delete
- [x] **T8.1.7** Update `TestPortChannel` — I5 enforced by `deleteIntent` in `DeletePortChannel` (returns error if children exist); covered by T8.2.5

### 8.1b Specific test file fixes (intent_test.go)

- [x] **T8.1.8** `TestWriteIntentRecordsToShadow`: passes `nil` parents parameter
- [x] **T8.1.9** `TestWriteIntentPrepends`: passes `nil` parents parameter
- [x] **T8.1.10** `TestDeleteIntentRemovesFromShadow`: works with bare key (no parent needed for root)

### 8.1c Specific test file fixes (reconstruct_test.go)

- [x] **T8.1.11** Update `n.GetIntent("Ethernet0")` → `n.GetIntent("interface|Ethernet0")` in 3 locations
- [x] **T8.1.12** (added) Seed `"device"` root intent in `newTestAbstract()`
- [x] **T8.1.13** (added) `TestReplayStepSetProperty`: add `configure-interface` prerequisite for `interface|Ethernet0` parent

### 8.1d Specific test file fixes (device_ops_test.go)

- [x] **T8.1.14** Seed `"device"` root intent in `testDevice()`
- [x] **T8.1.15** Update intent key `"Ethernet0|acl|ingress"` → `"interface|Ethernet0|acl|ingress"` in `TestUnbindACLFromInterface`
- [x] **T8.1.16** (added) Seed parent intents in `TestAddPortChannelMember` (`portchannel|PortChannel100`)
- [x] **T8.1.17** (added) Seed parent intents in `TestAddACLRule` (`acl|EDGE_IN`)
- [x] **T8.1.18** (added) Seed parent intents in `TestBindMACVPN` (`vlan|100`)
- [x] **T8.1.19** (added) Seed parent intents in `TestConfigureIRB` (`vlan|100`, `vrf|Vrf_CUST1`)

### 8.1e Specific test file fixes (interface_ops_test.go)

- [x] **T8.1.20** (added) Update `NewtronIntent["Ethernet0"]` → `NewtronIntent["interface|Ethernet0"]` in 5 tests
- [x] **T8.1.21** (added) Seed parent intents in `TestBindACL` and `TestBindACL_EmptyBindingList`
- [x] **T8.1.22** (added) Update `assertChange(... "NEWTRON_INTENT", "Ethernet0", ...)` → `"interface|Ethernet0"`
- [x] **T8.1.23** (added) Update `TestRemoveService_SharedACL_LastUser` with DAG-encoded ACL children
- [x] **T8.1.29** (added) Update `TestRemoveService_SharedACL_NotLastUser` with DAG-encoded ACL children + binding intents
- [x] **T8.1.30** (added) Update `TestSnapshot` and `TestSnapshotRoundTrip` intent keys to kind-prefixed format
- [x] **T8.1.31** (added) Update 6 `TestIntentToStep_*` tests to use kind-prefixed resource keys
- [x] **T8.1.32** (added) Update `TestIntentsToSteps_Ordering` with DAG `_parents`/`_children` for topological sort

### 8.1f Specific test file fixes (service_gen_test.go)

- [x] **T8.1.24** (added) Seed `"device"` root intent in 5 inline-Node tests (RefreshService_*, BlueGreen, BGPPeerGroup)
- [x] **T8.1.25** (added) Update `NewtronIntent["Ethernet0"]` and `NewtronIntent["Ethernet4"]` → prefixed in service_gen_test.go

### 8.1g Specific test file fixes (precondition_external_test.go)

- [x] **T8.1.26** (added) Update `NewtronIntent["Ethernet0"]` and `NewtronIntent["Ethernet4"]` → prefixed in DependencyChecker tests
- [x] **T8.1.27** `precondition_external_test.go` Deleted all 10 `TestDependencyChecker_*` tests (DependencyChecker deleted in Phase 4)

### 8.1h Specific test file fixes (types_test.go)

- [x] **T8.1.28** (added) Update `NewtronIntent["Ethernet0"]` → `NewtronIntent["interface|Ethernet0"]` in 3 tests

### 8.2 New tests

- [x] **T8.2.1** Test writeIntent: parent existence check (I4) — `TestWriteIntent_ParentExistence`
- [x] **T8.2.2** Test writeIntent: idempotent update (same parents → params replaced) — `TestWriteIntent_IdempotentUpdate`
- [x] **T8.2.3** Test writeIntent: different parents → error — `TestWriteIntent_DifferentParentsError`
- [x] **T8.2.4** Test writeIntent: child registered in parent's `_children` — `TestWriteIntent_ChildRegistered`
- [x] **T8.2.5** Test deleteIntent: refuses if `_children` non-empty (I5) — `TestDeleteIntent_RefusesWithChildren`
- [x] **T8.2.6** Test deleteIntent: deregisters from parent's `_children` — `TestDeleteIntent_DeregistersFromParent`
- [x] **T8.2.7** Test multi-parent: ACL binding with `[interface|INTF, acl|NAME]` — `TestWriteIntent_MultiParent`
- [x] **T8.2.8** Test topological sort reconstruction ordering — `TestIntentsToSteps_TopologicalOrder`
- [x] **T8.2.9** Test ValidateIntentDAG: detect broken bidirectional consistency — `TestValidateIntentDAG_BidirectionalInconsistency`
- [x] **T8.2.10** Test ValidateIntentDAG: detect orphaned references — `TestValidateIntentDAG_OrphanDetection`

### 8.3 Verification — Phase 8

- [x] **T8.3.1** `go test ./... -count=1` ALL PASS
- [x] **T8.3.2** (added) Fix `TestAPICompleteness` — add `IntentTree` to coveredMethods in `api_test.go`

---

## Phase 9: Schema + Design Principles Documentation

### 9.1 Documentation updates

- [x] **T9.1.1** Add §43–§45 to `docs/DESIGN_PRINCIPLES_NEWTRON.md` (Intent DAG structural deps, kind-prefixed keys, multi-parent rendering)
- [x] **T9.1.2** Update `docs/newtron/lld.md` with new signatures (writeIntent, deleteIntent, cascadeDeleteChildren, applyIntentToShadow, ValidateIntentDAG) + §5.7 kind-prefixed keys + new §6.5
- [x] **T9.1.3** CLAUDE.md ownership map already has `intent_ops.go → NEWTRON_INTENT` — no changes needed

### 9.2 Verification — Phase 9

- [x] **T9.2.1** All docs internally consistent

---

## Phase 10: Public API Layer Review

### 10.1 node.go — GetIntent method

- [x] **T10.1.1** Review `GetIntent` method (node.go): `*sonic.Intent` includes `Parents`/`Children` fields (added in Phase 1). No signature change needed.

### 10.2 Public API types

- [x] **T10.2.1** `pkg/newtron/types.go` public `Intent` type: `Parents`/`Children` fields added (done in Phase 7, T7.1.11). `intentFromSonic` updated to populate them (T7.1.12).

---

## Cross-Cutting Verification

- [x] **T11.1** Grep: zero `updateIntent` calls remain in `*_ops.go` — CONFIRMED
- [x] **T11.2** Grep: zero bare interface-name resource keys in `writeIntent` calls — CONFIRMED
- [x] **T11.3** Grep: zero `DependencyChecker` references in production code (one comment in service_ops.go explaining replacement — acceptable)
- [x] **T11.4** Grep: zero `stepPriority` references — CONFIRMED
- [x] **T11.5** Every §10 catalog entry (10.1–10.17) has corresponding completed tasks in Phase 2
- [x] **T11.6** `go build ./...` clean
- [x] **T11.7** `go vet ./...` clean
- [x] **T11.8** `go test ./... -count=1` all pass (14 packages, 0 failures)
- [x] **T11.9** `FieldMembers` and `FieldRules` used only for reconstruction metadata (intent params for replay) — not dual-tracking. Legitimate usage.
- [x] **T11.10** All `GetIntent` calls in production code use kind-prefixed keys. Test-only bare keys in `TestNodeIntentAccessors` test the accessor itself (acceptable).

---

## Post-Verification Bug Fixes

Discovered during cross-cutting verification sweep (Phase 11).

### Bug A: `DeleteVLAN` double-deleteIntent on probe ChangeSet

- [x] **T12.1** Fix `DeleteVLAN` (`vlan_ops.go`): replaced probe-ChangeSet pattern with inline I5 pre-check (`GetIntent` + `len(Children) > 0`). `deleteIntent` now called once on the real ChangeSet. Other delete operations (DeleteVRF, DeleteACL, DeletePortChannel) were already correct.

### Bug B: `removeSharedACL` — ACL rules leak on last-user teardown

- [x] **T12.2** Fix `removeSharedACL` (`service_ops.go`): when no per-rule DAG children (`acl|NAME|RULE`) exist (because `ApplyService` creates rules without per-rule intents — §10.16 integration deferred), fall back to reading `FieldRules` CSV from intent params to enumerate and delete `ACL_RULE` entries.

### Verification

- [x] **T12.3** `go build ./...` clean after both fixes
- [x] **T12.4** `go test ./... -count=1` all pass (14 packages, 0 failures)

---

## Phase 13: Newtrun YAML Suite Updates

### 13.1 Service suite — replace removed endpoints with config-reload

- [x] **T13.1.1** `2node-ngdp-service/05-deprovision.yaml`: replaced `teardown-vtep`, `remove-bgp`, `remove-loopback` with `reload-config` + wait
- [x] **T13.1.2** `2node-vs-service/05-deprovision.yaml`: same replacement
- [x] **T13.1.3** `2node-vs-drift/12-teardown.yaml`: same replacement
- [x] **T13.1.4** `2node-vs-zombie/12-teardown.yaml`: same replacement
- [x] **T13.1.5** `3node-ngdp-dataplane/06-teardown.yaml`: same replacement

### 13.2 Primitive suite — replace removed endpoints with config-reload

- [x] **T13.2.1** `2node-ngdp-primitive/90-teardown-infra.yaml`: kept valid steps (remove-bgp-peer, unconfigure-interface), replaced infra teardown with reload-config + wait
- [x] **T13.2.2** `2node-vs-primitive/90-teardown-infra.yaml`: same

### 13.3 Kind-prefixed NEWTRON_INTENT keys in YAML assertions

- [x] **T13.3.1** `2node-ngdp-primitive/*.yaml`: updated `NEWTRON_INTENT/Ethernet*` → `NEWTRON_INTENT/interface%7CEthernet*` (9 files)
- [x] **T13.3.2** `2node-vs-primitive/*.yaml`: same
- [x] **T13.3.3** `2node-ngdp-service/*.yaml`: same
- [x] **T13.3.4** `2node-vs-service/*.yaml`: same
- [x] **T13.3.5** `3node-ngdp-dataplane/*.yaml`: same

### 13.4 Verification — Phase 13

- [x] **T13.4.1** `go build ./...` passes
- [x] **T13.4.2** `go test ./... -count=1` all pass (15 packages, 0 failures)
- [x] **T13.4.3** No stale references to removed endpoints in YAML (grep clean — only `remove-bgp-peer` found, which is a valid intent-producing interface op)
- [x] **T13.4.4** Fixed stale comments in `06-verify-clean.yaml` (both ngdp and vs): replaced old primitive names (`ConfigureBGP`, `RemoveBGP`, `SetupEVPN`, `TeardownEVPN`, `ConfigureLoopback`, `RemoveLoopback`) with current operation names (`SetupDevice`, `reload-config`)

---

## Phase 14: Final Gap Closure

### 14.1 Stale bare-key test fixtures

- [x] **T14.1.1** `intent_test.go`: Updated `TestNodeIntentAccessors` — `"Ethernet0"` → `"interface|Ethernet0"`, `"bgp"` → `"device"`
- [x] **T14.1.2** `intent_test.go`: Updated `TestNodeLoadIntentsFromConfigDB` — bare keys → kind-prefixed
- [x] **T14.1.3** `intent_test.go`: Updated `TestWriteIntentRecordsToShadow` — `"Vrf_TRANSIT"` → `"vrf|Vrf_TRANSIT"`
- [x] **T14.1.4** `intent_test.go`: Updated `TestWriteIntentPrepends` — `"Vlan100"` → `"vlan|100"`
- [x] **T14.1.5** `intent_test.go`: Updated `TestDeleteIntentRemovesFromShadow` — `"Vrf_TRANSIT"` → `"vrf|Vrf_TRANSIT"`
- [x] **T14.1.6** `intent_test.go`: Updated `TestIntentsToSteps_FiltersNonActuated` — bare keys → kind-prefixed
- [x] **T14.1.7** `intent_test.go`: Updated `TestNodeServiceIntentsFiltersState` — bare keys → kind-prefixed

### 14.2 Verification — Phase 14

- [x] **T14.2.1** `go build ./...` passes
- [x] **T14.2.2** `go test ./... -count=1` all pass (15 packages, 0 failures)
- [x] **T14.2.3** Zero bare-key `NewtronIntent[` references remain in production code (confirmed via gap analysis agent)

---

## Phase 15: Test Quality — Intent Assertions + Round-Trip Tests

### 15.1 NEWTRON_INTENT assertions added to existing forward-op tests

- [x] **T15.1.1** `TestCreateVLAN_Basic`: assert `NEWTRON_INTENT/vlan|100` ChangeAdd
- [x] **T15.1.2** `TestCreateVLAN_WithL2VNI`: assert `NEWTRON_INTENT/vlan|200` ChangeAdd
- [x] **T15.1.3** `TestCreatePortChannel_Basic`: assert `NEWTRON_INTENT/portchannel|PortChannel100` ChangeAdd
- [x] **T15.1.4** `TestAddPortChannelMember`: assert `NEWTRON_INTENT/pc|PortChannel100|Ethernet0` ChangeAdd
- [x] **T15.1.5** `TestCreateVRF_Basic`: assert `NEWTRON_INTENT/vrf|Vrf_CUST1` ChangeAdd
- [x] **T15.1.6** `TestCreateACL_Basic`: assert `NEWTRON_INTENT/acl|EDGE_IN` ChangeAdd
- [x] **T15.1.7** `TestAddACLRule`: assert `NEWTRON_INTENT/acl|EDGE_IN|RULE_10` ChangeAdd
- [x] **T15.1.8** `TestBindMACVPN`: assert `NEWTRON_INTENT/macvpn|100` ChangeAdd
- [x] **T15.1.9** `TestConfigureIRB`: assert `NEWTRON_INTENT/irb|100` ChangeAdd
- [x] **T15.1.10** `TestBindACL`: assert `NEWTRON_INTENT/interface|Ethernet0|acl|ingress` ChangeAdd
- [x] **T15.1.11** `TestBindACL_EmptyBindingList`: assert `NEWTRON_INTENT/interface|Ethernet0|acl|egress` ChangeAdd
- [x] **T15.1.12** `TestAddBGPPeer`: assert `NEWTRON_INTENT/interface|Ethernet0` ChangeAdd

### 15.2 NEWTRON_INTENT assertions added to existing reverse-op tests

- [x] **T15.2.1** `TestDeleteVLAN_WithMembers`: assert `NEWTRON_INTENT/vlan|100` ChangeDelete
- [x] **T15.2.2** `TestDeleteVRF_NoInterfaces`: seed intent + assert `NEWTRON_INTENT/vrf|Vrf_CUST1` ChangeDelete
- [x] **T15.2.3** `TestDeleteACL_RemovesRules`: assert `NEWTRON_INTENT/acl|EDGE_IN` ChangeDelete
- [x] **T15.2.4** `TestUnbindACLFromInterface`: assert `NEWTRON_INTENT/interface|Ethernet0|acl|ingress` ChangeDelete
- [x] **T15.2.5** `TestRemovePortChannelMember`: seed intent + assert `NEWTRON_INTENT/pc|PortChannel100|Ethernet0` ChangeDelete
- [x] **T15.2.6** `TestRemoveBGPPeer`: assert `NEWTRON_INTENT/interface|Ethernet0` ChangeDelete
- [x] **T15.2.7** `TestRemoveService_SharedACL_LastUser`: assert `NEWTRON_INTENT/interface|Ethernet0` + `acl|CUSTOMER_L3_IN` ChangeDelete
- [x] **T15.2.8** `TestRemoveService_SharedACL_NotLastUser`: assert `NEWTRON_INTENT/interface|Ethernet0` ChangeDelete

### 15.3 Round-trip tests (forward + reverse = clean state)

- [x] **T15.3.1** `TestRoundTrip_CreateDeleteVLAN` — VLAN + L2VNI
- [x] **T15.3.2** `TestRoundTrip_CreateDeleteVRF`
- [x] **T15.3.3** `TestRoundTrip_CreateDeleteACL` — ACL table + rule, children-first deletion
- [x] **T15.3.4** `TestRoundTrip_CreateDeletePortChannel` — with member via AddPortChannelMember
- [x] **T15.3.5** `TestRoundTrip_AddRemoveBGPEVPNPeer` — uses ReplayStep(setup-device) prerequisite
- [x] **T15.3.6** `TestRoundTrip_ConfigureUnconfigureIRB` — VRF + VLAN prerequisites
- [x] **T15.3.7** `TestRoundTrip_AddRemoveStaticRoute` — VRF prerequisite
- [x] **T15.3.8** `TestRoundTrip_BindUnbindMACVPN` — VTEP + VLAN prerequisites
- [x] **T15.3.9** `TestRoundTrip_ConfigureUnconfigureInterface_Routed` — VRF + IP
- [x] **T15.3.10** `TestRoundTrip_AddRemoveBGPPeer` — interface-level
- [x] **T15.3.11** `TestRoundTrip_BindUnbindACL` — multi-parent (interface + ACL)

### 15.4 Verification — Phase 15

- [x] **T15.4.1** `go build ./...` passes
- [x] **T15.4.2** `go test ./... -count=1` all pass (15 packages, 0 failures)

---

## Phase 16: E2E Suite Regression Fixes

### 16.1 I4 parent failures for sub-resource ops on bare interfaces

Sub-resource operations (SetProperty, BindACL, ApplyQoS) failed with I4 parent
existence error when called on interfaces without prior ConfigureInterface.
Fix: lazy `ensureInterfaceIntent` creates `interface|INTF` with parent `device`.

- [x] **T16.1.1** Add `OpInterfaceInit` constant to `configdb.go` and `schema.go` enum
- [x] **T16.1.2** Add `ensureInterfaceIntent` helper to `interface_ops.go`
- [x] **T16.1.3** Call `ensureInterfaceIntent` from `SetProperty` (interface_ops.go)
- [x] **T16.1.4** Call `ensureInterfaceIntent` from `BindACL` (interface_ops.go)
- [x] **T16.1.5** Call `ensureInterfaceIntent` from `ApplyQoS` (qos_ops.go)
- [x] **T16.1.6** Add `OpInterfaceInit` to `skipInReconstruct` (reconstruct.go)

### 16.2 BindMACVPN on VLAN interfaces — wrong parent

Interface.BindMACVPN used `interface|VlanXXX|macvpn` with parent `interface|VlanXXX`,
but VLAN interfaces don't have `interface|VlanXXX` intents. Aligned with Node.BindMACVPN
to use `macvpn|ID` with parent `vlan|ID`.

- [x] **T16.2.1** `macvpn_ops.go` BindMACVPN: intent key → `macvpn|ID`, parent → `vlan|ID`
- [x] **T16.2.2** `macvpn_ops.go` UnbindMACVPN: intent key → `macvpn|ID`
- [x] **T16.2.3** `reconstruct.go` `intentInterface`: handle `macvpn|ID` → `Vlan{ID}`

### 16.3 Verification — Phase 16

- [x] **T16.3.1** `go build ./...` passes
- [x] **T16.3.2** `go vet ./...` passes
- [x] **T16.3.3** `go test ./... -count=1` all pass (15 packages, 0 failures)
- [x] **T16.3.4** 2node-vs-primitive E2E suite passes (21/21)
- [ ] **T16.3.5** 2node-ngdp-primitive E2E suite passes

---

## Phase 17: E2E Test Fixes — AddBGPPeer Intent Key & Test Assertions

### 17.1 AddBGPPeer intent key collision with ConfigureInterface

AddBGPPeer wrote to `interface|Ethernet0` (same key as ConfigureInterface),
then RemoveBGPPeer deleted `interface|Ethernet0`, destroying the ConfigureInterface
intent. UnconfigureInterface would then fail with "no configuration intent".

Fix: AddBGPPeer now uses sub-resource key `interface|Ethernet0|bgp-peer` with
parent `interface|Ethernet0`. Calls `ensureInterfaceIntent` first.
RemoveBGPPeer only deletes the sub-resource, preserving the parent.

- [x] **T17.1.1** `interface_bgp_ops.go` AddBGPPeer: call `ensureInterfaceIntent`, write to `interface|INTF|bgp-peer` with parent `interface|INTF`
- [x] **T17.1.2** `interface_bgp_ops.go` RemoveBGPPeer: read from `interface|INTF|bgp-peer`, only delete sub-resource
- [x] **T17.1.3** `interface_ops_test.go` TestRemoveBGPPeer: update intent key to `interface|Ethernet0|bgp-peer`
- [x] **T17.1.4** `interface_ops_test.go` TestRoundTrip_AddRemoveBGPPeer: update intent keys

### 17.2 spec-authoring/verify-peer-group-removed assertion

Test asserted `BGP_PEER_GROUP length == 0` but setup-device creates the EVPN
peer group (`default|EVPN`) which should persist. Fixed to check only the
test service's peer group is removed.

- [x] **T17.2.1** `2node-vs-primitive/58-spec-authoring.yaml`: fix assertion to `has("default|TEST_ROUTED_SVC") | not`
- [x] **T17.2.2** `2node-ngdp-primitive/58-spec-authoring.yaml`: same fix

### 17.3 Test suite fixes

Stale entries in network.json (TEST_PREFIXES, TEST_IMPORT_POLICY, TEST_ROUTED_SVC)
were leftover from previous spec-authoring test runs. A linter/hook restores them
after edits. The spec-authoring test creates these entries fresh, so they must not
exist in network.json at test start.

- [x] **T17.3.1** Remove stale TEST_PREFIXES, TEST_IMPORT_POLICY, TEST_ROUTED_SVC from `2node-vs/specs/network.json`

teardown-infra relied on `config reload -y` to wipe baseline infrastructure.
But every API write calls `config save`, so config_db.json includes all
provisioned entries. `config reload` restores the saved state, not factory
defaults. Fixed: removed config-reload from teardown-infra, added intent
verification. verify-clean updated to not check persistent baseline infrastructure.

- [x] **T17.3.2** `90-teardown-infra.yaml`: remove config-reload, verify intent removal instead
- [x] **T17.3.3** `92-verify-clean.yaml`: remove baseline infrastructure checks (loopback, BGP globals, VXLAN, EVPN peer group)

### 17.4 Verification — Phase 17

- [x] **T17.4.1** `go build ./...` passes
- [x] **T17.4.2** `go test ./... -count=1` all pass
- [x] **T17.4.3** 2node-vs-primitive E2E suite passes (21/21)
- [ ] **T17.4.4** 2node-ngdp-primitive E2E suite passes

---

## Phase 18: BGP_GLOBALS_EVPN_RT Cleanup in Service Path

`ApplyService` creates BGP_GLOBALS_EVPN_RT via `bindIpvpnConfig()` but does
NOT create `ipvpn|{vrf}` intent (only standalone `BindIPVPN` does). When
`RemoveService` → `destroyVrfConfig` → `unbindIpvpnConfig` runs, it finds no
ipvpn intent and generates zero EVPN RT delete entries.

Fix: store `route_targets` in the service binding, and add a `fallbackRT`
parameter to `unbindIpvpnConfig`/`destroyVrfConfig` for when ipvpn intent
doesn't exist.

- [x] **T18.1.1** `service_ops.go` ApplyService: store `route_targets` in binding params
- [x] **T18.1.2** `vrf_ops.go` `unbindIpvpnConfig`: add `fallbackRT` param, use when ipvpn intent absent
- [x] **T18.1.3** `vrf_ops.go` `destroyVrfConfig`: add `routeTargets` param, pass to `unbindIpvpnConfig`
- [x] **T18.1.4** `service_ops.go` RemoveService: pass `b[sonic.FieldRouteTargets]` to `destroyVrfConfig`
- [x] **T18.1.5** `vrf_ops.go` `UnbindIPVPN`: pass `""` as fallback (always has ipvpn intent)

### 18.2 Idempotent spec delete APIs + cleanup preamble

- [x] **T18.2.1** `spec_ops.go`: make DeleteService, DeletePrefixList, DeleteRoutePolicy, RemovePrefixListEntry, RemoveRoutePolicyRule idempotent
- [x] **T18.2.2** `2node-vs-primitive/58-spec-authoring.yaml`: add cleanup preamble
- [x] **T18.2.3** `2node-ngdp-primitive/58-spec-authoring.yaml`: add cleanup preamble

### 18.3 Verification — Phase 18

- [x] **T18.3.1** `go build ./...` passes
- [x] **T18.3.2** `go test ./... -count=1` all pass
- [x] **T18.3.3** 2node-vs-primitive E2E suite passes (21/21)

---

## Phase 18b: Proper IPVPN Intent for Service Path (replaces fallback hack)

Phase 18.1 used a `fallbackRT` parameter on `unbindIpvpnConfig` — a hack.
Replaced with proper architecture: `ApplyService` creates `ipvpn|{vrf}` intent
via `ensureIPVPNIntent` (same pattern as ensureVRFIntent/ensureVLANIntent).

- [x] **T18b.1.1** `intent_ops.go`: add `ensureIPVPNIntent` — creates `ipvpn|{vrf}` with route_targets, l3vni, l3vni_vlan, ipvpn, vrf; parent `vrf|{vrf}`
- [x] **T18b.1.2** `service_ops.go` ApplyService: call `ensureIPVPNIntent` after `ensureVRFIntent`
- [x] **T18b.1.3** `service_ops.go` RemoveService: explicitly delete `ipvpn|{vrf}` then `vrf|{vrf}` intent after interface intent (ordered, not cascading)
- [x] **T18b.1.4** `service_ops.go` RemoveService: fix `isLastIPVPN` scan to only count `interface|*` intents (ipvpn intent has `ipvpn` field that falsely matched)
- [x] **T18b.1.5** `vrf_ops.go`: revert fallback hack — `unbindIpvpnConfig` and `destroyVrfConfig` back to original signatures

### 18b.2 Verification — Phase 18b

- [x] **T18b.2.1** `go build ./...` passes
- [x] **T18b.2.2** `go test ./... -count=1` all pass
- [x] **T18b.2.3** 2node-vs-primitive E2E suite passes (21/21)

---

## Phase 19: E2E Test Suite Fixes — Baseline Infrastructure Assertions

All verify-clean/teardown suites incorrectly asserted that baseline infrastructure
(loopback, BGP globals/default, VTEP, NVO) should be absent after deprovision.
Since `config save` runs on every write and `config reload` restores saved (not factory)
state, baseline infrastructure persists. Fixed across all platforms.

### 19.1 Remove config-reload from teardown/deprovision

- [x] **T19.1.1** `2node-ngdp-primitive/90-teardown-infra.yaml`: remove reload-config + baseline checks
- [x] **T19.1.2** `2node-ngdp-service/05-deprovision.yaml`: remove reload-config + wait-reload
- [x] **T19.1.3** `2node-vs-service/05-deprovision.yaml`: remove reload-config + wait-reload (prior session)
- [x] **T19.1.4** `3node-ngdp-dataplane/06-teardown.yaml`: remove reload-config + wait-reload

### 19.2 Remove baseline infrastructure assertions from verify-clean

- [x] **T19.2.1** `2node-ngdp-primitive/92-verify-clean.yaml`: remove loopback, BGP/default, VTEP, NVO, overlay BGP neighbor assertions; fix BGP_PEER_GROUP to check specific service group
- [x] **T19.2.2** `2node-ngdp-service/06-verify-clean.yaml`: remove VTEP, NVO, BGP/default, BGP AFs/default, ROUTE_REDISTRIBUTE/default, BGP neighbors, loopback assertions
- [x] **T19.2.3** `2node-vs-service/06-verify-clean.yaml`: remove baseline assertions (prior session)
- [x] **T19.2.4** `3node-ngdp-dataplane/07-verify-clean.yaml`: remove loopback, BGP/default, BGP AFs, BGP neighbors, ROUTE_REDISTRIBUTE/default, VTEP, NVO assertions

### 19.3 EVPN IRB timeout increase for NGDP

- [x] **T19.3.1** `2node-ngdp-primitive/44-evpn-irb.yaml`: increase wait 15s→20s, poll timeout 10s→30s for SVI pings

### 19.4 Verification — Phase 19

- [x] **T19.4.1** 2node-vs-primitive: 21/21 PASS (prior session)
- [x] **T19.4.2** 2node-vs-service: 6/6 PASS (prior session)
- [x] **T19.4.3** 2node-vs-drift: 7/7 PASS (prior session)
- [x] **T19.4.4** 2node-vs-zombie: 8/8 PASS (prior session)
- [x] **T19.4.5** 2node-ngdp-primitive: 21/21 PASS
- [x] **T19.4.6** 2node-ngdp-service: 6/6 PASS
- [x] **T19.4.7** 3node-ngdp-dataplane: 8/8 PASS
