# Test Run Fixes — 1node-vs-architecture Suite

Running document of all fixes applied during E2E test execution.
Each fix must be justified against the architecture document
(`docs/newtron/unified-pipeline-architecture.md`).

---

## Fix 1: RebuildProjection — clear unsavedIntents after replay

**File**: `pkg/newtron/network/node/node.go` (RebuildProjection)

**Symptom**: After topology-reconcile, `reload-config` triggered
`ensureActuatedIntent()` which refused because `unsavedIntents = true`.

**Root cause**: `RebuildProjection` calls `ReplayStep` → `writeIntent` →
`unsavedIntents = true`. But replay is reconstruction, not new CRUD mutations.
`unsavedIntents` was never cleared after replay.

**Fix**: Added `n.unsavedIntents = false` at end of `RebuildProjection`.

**Architecture justification**: Architecture §8 (RebuildProjection) says it
reconstructs the projection from intents. This matches `BuildAbstractNode`
(topology.go:87) and `InitFromDeviceIntent` (node.go:704), both of which set
`unsavedIntents = false` after replay. Replay is reconstruction, not mutation —
the flag must reflect that.

---

## Fix 2: RebuildProjection — suppress actuatedIntent during replay

**File**: `pkg/newtron/network/node/node.go` (RebuildProjection)

**Symptom**: `RebuildProjection` failed with "precondition failed: device must
be locked for changes" during intent replay on actuated nodes.

**Root cause**: `precondition()` (precondition.go:32-38) adds
`RequireConnected().RequireLocked()` when `n.actuatedIntent == true`. During
`InitFromDeviceIntent`, `actuatedIntent` is set AFTER replay (line 702). But in
`RebuildProjection`, the node already has `actuatedIntent = true` from previous
init, so replay fails the precondition.

**Fix**: Temporarily clear `actuatedIntent` before replay, restore after:
```go
wasActuated := n.actuatedIntent
n.actuatedIntent = false
// ... replay ...
n.actuatedIntent = wasActuated
```

**Architecture justification**: Architecture §8 says RebuildProjection replays
intents to reconstruct the projection. Replay is the same operation as initial
construction — `InitFromDeviceIntent` sets `actuatedIntent` AFTER replay
(node.go:702), which means replay executes without actuation enforcement. The
fix makes `RebuildProjection` structurally consistent with `InitFromDeviceIntent`.

---

## Fix 3: RebuildProjection — do not pre-populate intent DB before replay

**File**: `pkg/newtron/network/node/node.go` (RebuildProjection)

**Symptom**: After actuated CRUD (e.g., create-vlan), drift check showed VLAN
entries as "extra" — the projection was empty despite the intent existing.

**Root cause**: `RebuildProjection` set `n.configDB.NewtronIntent = intents`
BEFORE calling `ReplayStep`. Config methods check `GetIntent(resource)` at the
top for idempotency — if the intent already exists, they return an empty
ChangeSet without rendering. Pre-populating the intent DB caused all intents
to be seen as "already existing" during replay, so nothing was rendered into
the projection.

**Fix**: Removed `n.configDB.NewtronIntent = intents`. `IntentsToSteps(intents)`
takes intents as a parameter (doesn't need configDB populated). `writeIntent`
populates the intent DB during replay, and `render(cs)` updates the projection.

**Architecture justification**: Architecture §10 (Interactive trace) shows
`GetIntent("vlan|100") → nil (not yet created)` during writes — the intent DB
should not contain the intent before the config method runs. Both
`BuildAbstractNode` and `InitFromDeviceIntent` start with an empty intent DB
before replay and let `writeIntent` populate it during replay. The fix makes
`RebuildProjection` consistent with these two paths.

**Note**: The architecture doc's pseudocode at §8 line 693 shows
`configDB.NewtronIntent = intents` before replay. This pseudocode is incorrect
— it contradicts the implementation pattern established by `BuildAbstractNode`
and `InitFromDeviceIntent`, and the interactive trace at §10. The pseudocode
should be updated to remove the pre-population line.

---

## Fix 4: Verify transport guard — no-op without device connection

**File**: `pkg/newtron/network/node/changeset.go` (Verify)

**Symptom**: Topology-mode CRUD (create-vlan with `?mode=topology`) returned
HTTP 500 with "verify failed: device not connected".

**Root cause**: `ChangeSet.Verify()` required `IsConnected()` and returned
`ErrNotConnected` when no device transport existed. In topology offline mode,
there is no device connection — but the `Execute()` path in `pkg/newtron/node.go`
calls `Commit()` which calls `Verify()`, causing the failure.

**Fix**: Changed `Verify()` to use the transport guard pattern:
```go
if n.conn == nil {
    return nil
}
```

**Architecture justification**: Architecture §8 line 626-630: "Lock, Apply, and
Unlock are no-ops when `n.conn == nil` (no transport connection exists). This is
not a dual code path — it is the I/O boundary respecting the absence of a wire."
`Verify` is device I/O (re-reads from Redis per §7 table) — it must follow the
same transport guard pattern. `Apply` already has this guard (changeset.go:221).

---

## Fix 5: SaveConfig transport guard — no-op without device connection

**File**: `pkg/newtron/network/node/node.go` (SaveConfig)

**Symptom**: Would have failed for topology-mode writes via `Execute()` →
`Commit()` → `Save()` path (caught during Fix 4 investigation).

**Root cause**: `SaveConfig()` returned `ErrNotConnected` when `!n.connected`.
In topology offline mode, no device connection exists, so `config save` via SSH
is impossible.

**Fix**: Changed to transport guard pattern:
```go
if n.conn == nil {
    return nil
}
```

**Architecture justification**: Same as Fix 4 — architecture §8 line 626-630.
`SaveConfig` writes to the device filesystem via SSH. Without transport, the I/O
boundary returns nil. Same pattern as Lock, Apply, Unlock, and now Verify.

---

## Fix 6: Reorder drift-guard scenario steps

**File**: `newtrun/suites/1node-vs-architecture/06-drift-guard.yaml`

**Symptom**: Step 4 (verify-vlan-not-on-device) failed with HTTP 500 because
`ensureActuatedIntent()` refused to switch from topology mode — the topology
node had unsaved intents from step 3 (topology-mode create-vlan).

**Root cause**: The test created a VLAN in topology mode (step 3) then
immediately tried to read device configdb in actuated mode (step 4). The
unsaved intent guard (architecture §5: "ensureActuatedIntent refuses to destroy
a topology node with unsaved intents") correctly blocked the mode switch.

**Fix**: Reordered steps: verify-in-tree (topology mode) → reload (clears
unsaved intents) → verify-not-on-device (actuated mode). Moved the actuated-mode
device read after the topology reload.

**Architecture justification**: Architecture §5 (Unsaved topology intent guard):
"Switching from topology to actuated mode destroys the topology node. If the user
created intents via --topology CRUD but didn't save, those intents are lost."
The guard is correct. The test was wrong — it tried to switch modes without
clearing unsaved state first. The fix respects the guard by reloading before
switching modes.

---

## Fix 7: RebuildProjection — save/restore unsavedIntents across replay

**File**: `pkg/newtron/network/node/node.go` (RebuildProjection)

**Symptom**: Two issues:
1. (Run 7) Mode-switching test "mode-switch-blocked-by-unsaved" expected failure
   but succeeded — unsavedIntents was cleared by RebuildProjection.
2. (Run 8) After initial topology-reconcile, the node had unsavedIntents = true
   because RebuildProjection replay set it via writeIntent, and the conditional
   clear (`if n.conn != nil`) didn't trigger in offline mode.

**Root cause**: `writeIntent` unconditionally sets `n.unsavedIntents = true`,
including during replay (reconstruction). Both `BuildAbstractNode`
(topology.go:87) and `InitFromDeviceIntent` (node.go:725) clear the flag AFTER
replay because replay is not mutation. `RebuildProjection` must do the same, but
it also must preserve the flag when CRUD mutations happened before the rebuild.

**Fix**: Save `unsavedIntents` before replay, restore after:
```go
wasUnsaved := n.unsavedIntents
// ... replay ...
n.unsavedIntents = wasUnsaved
```

This preserves pre-rebuild semantics:
- After initial construction (cleared by BuildAbstractNode/InitFromDeviceIntent):
  stays false — topology replay is not a mutation.
- After CRUD (user created VLAN without saving): stays true — unsaved intent
  guard must still block mode switching.
- After device re-read (actuated rebuild): stays false — device intents are
  persisted, not unsaved.

**Architecture justification**: Architecture §5 (Unsaved topology intent guard):
"Node tracks unsavedIntents — set by writeIntent/deleteIntent, left false during
ReplayStep, cleared after Save."

The key phrase: "left false during ReplayStep" — replay should NOT change the
unsavedIntents flag. Both `BuildAbstractNode` and `InitFromDeviceIntent` enforce
this by clearing after replay. `RebuildProjection` enforces it by
save/restore — the flag reflects only real CRUD operations, never replay.

---

## Fix 8: Reorder intent-reload scenario steps

**File**: `newtrun/suites/1node-vs-architecture/09-intent-reload.yaml`

**Symptom**: Step "verify-vlan500-not-on-device" (actuated mode device read)
failed with "topology node has unsaved intents" — same pattern as Fix 6.

**Root cause**: The test created VLAN 500 in topology mode (step 1), then tried
to read device configdb in actuated mode (step 3) before reloading. The unsaved
intent guard correctly blocked the mode switch.

**Fix**: Moved `verify-vlan500-not-on-device` after the `reload-topology` step.
The reload clears unsaved intents, unblocking the mode switch to actuated.

**Architecture justification**: Same as Fix 6 — the unsaved intent guard is
correct behavior per architecture §5. The test was wrong to attempt actuated
mode access while topology intents are unsaved. Moving the device check after
reload verifies the same behavior (topology offline doesn't write to Redis)
without fighting the guard.

---

## Fix 9: ReplaceAll — clean all owned tables, not just those with entries

**File**: `pkg/newtron/device/sonic/pipeline.go` (ReplaceAll)

**Symptom**: After Clear + Reconcile (`clear-reconcile` test scenario),
`BGP_GLOBALS|default` still existed on the device. The test expected all
newtron-owned tables to be wiped.

**Root cause**: `ReplaceAll` only collected tables from the entries being
delivered. When the projection was empty (only PORT entries after Clear),
tables like BGP_GLOBALS, VLAN, VRF were never scanned or cleaned. The
config reload step (step 2 of Reconcile) restored factory CONFIG_DB
entries, and `ReplaceAll` never touched those tables.

**Fix**: Added `ownedTables []string` parameter to `ReplaceAll`. The
method now seeds its table set from owned tables (via `OwnedTables()`)
PLUS any tables in the delivery entries. This ensures tables with zero
entries in the projection are still scanned and cleaned.

```go
func (c *ConfigDBClient) ReplaceAll(changes []Entry, ownedTables []string) error {
    tables := make(map[string]bool)
    for _, table := range ownedTables {
        if !platformMergeTables[table] {
            tables[table] = true
        }
    }
    // ... rest unchanged
}
```

Caller updated: `n.conn.Client().ReplaceAll(entries, sonic.OwnedTables())`

**Architecture justification**: Architecture §6 (Clear): "Clear + Reconcile:
Pushes an empty projection to the device — `ReplaceAll` with only PORT
entries clears all other owned tables." The architecture explicitly states
that `ReplaceAll` should clear owned tables even when only PORT entries
are in the delivery set. The fix makes the implementation match this
specification.

Also consistent with architecture §6 (Reconcile): "Factory fields (mac,
platform, hwsku) survive because: ConfigReload restores them from
`/etc/sonic/config_db.json`; `ReplaceAll` only DELs keys for tables the
Node manages." The key phrase is "tables the Node manages" — this is the
owned tables set, not just the tables present in entries.

---

## Fix 10: ReplaceAll delivery must include NEWTRON_INTENT table

**File**: `pkg/newtron/network/node/node.go` (Reconcile)

**Symptom**: After Clear + Reconcile, `NEWTRON_INTENT|device` still existed
on the device. The test expected all newtron-managed entries wiped.

**Root cause**: `OwnedTables()` derives from the Schema but filters out
tables in `excludedFromDrift` — which includes `NEWTRON_INTENT`. This is
correct for drift detection (NEWTRON_INTENT is primary state, not derived
projection — comparing it for drift is wrong). But Reconcile passed
`OwnedTables()` to `ReplaceAll`, meaning NEWTRON_INTENT was never scanned
or cleaned during delivery.

`ExportEntries()` DOES export NEWTRON_INTENT records (configdb.go:801-803).
During interactive writes, intent records are prepended to the ChangeSet and
written to Redis. During Reconcile, the same records flow through
`ExportEntries` → `ReplaceAll`. But when the intent DB is empty (after
Clear), no NEWTRON_INTENT entries are exported, and without
NEWTRON_INTENT in the delivery table set, `ReplaceAll` never cleans
stale intent keys.

Config reload (step 1 of Reconcile) restores `config_db.json` which
includes previously saved NEWTRON_INTENT records. `ReplaceAll` must
delete these stale records.

**Fix**: Append `"NEWTRON_INTENT"` to the delivery table list:
```go
deliveryTables := append(sonic.OwnedTables(), "NEWTRON_INTENT")
n.conn.Client().ReplaceAll(entries, deliveryTables)
```

**Architecture justification**: Architecture §4: "In Redis delivery order,
intent records arrive BEFORE config entries (because Prepend). This means
even the wire order is intent-first." Intent records are part of the
delivered state. `ExportEntries` includes them. `ReplaceAll` must manage
the NEWTRON_INTENT table for delivery to be complete.

The separation is correct: `OwnedTables()` excludes NEWTRON_INTENT for
drift (architecture §7: drift = projection ≠ device — intent records are
not projection). Reconcile explicitly adds it because delivery includes
the full state (intents + projection), not just the projection.

---

## Fix 11: BuildTopologyNode — handle zero steps after clear+save

**File**: `pkg/newtron/network.go` (BuildTopologyNode)

**Symptom**: After clear + save, `intent/reload?mode=topology` returned
HTTP 500: "device 'switch1' has no provisioning steps in topology.json".

**Root cause**: `BuildTopologyNode` called `BuildAbstractNode`, which
requires at least one step (topology.go:50). After clear + save,
topology.json has zero steps for the device — this is a valid state,
not an error. The check was correct for the initial provisioning path
(you shouldn't provision a device with no steps) but too strict for
reload after clear+save.

**Fix**: Check `topoDev.Steps` length before calling `BuildAbstractNode`.
If zero, delegate to `BuildEmptyTopologyNode` — creates a node with
ports registered from topology.json but no intents.

```go
topoDev, err := net.internal.GetTopologyDevice(device)
if err != nil { return nil, err }
if len(topoDev.Steps) == 0 {
    return net.BuildEmptyTopologyNode(device)
}
dev, err := tp.BuildAbstractNode(device)
```

**Architecture justification**: Architecture §6 (Reload): "Destroys the
current node and rebuilds from topology.json — same as initial
topology-mode construction." An empty topology.json entry (zero steps)
is a valid state — it's what clear+save produces. Reloading from it
should produce the same empty node that `Clear` produces: ports
registered, no intents, projection contains only PORT entries.

`BuildEmptyAbstractNode` (topology.go:92) does exactly this —
constructs a Node with the device's profile and resolved specs,
registers ports, and returns. This is structurally identical to
`BuildAbstractNode` minus the step replay loop.

---

## Fix 12: Drift guard — skip when no intents exist (fresh device)

**File**: `pkg/newtron/network/node/node.go` (Lock)

**Symptom**: `setup-device` on a fresh device (no prior intents) returned
HTTP 500: "device drifted from intents (1 entries) — reconcile first".

**Root cause**: The drift guard in Lock compares the projection (from
intent replay) against actual CONFIG_DB. On a fresh device with no
NEWTRON_INTENT records, `InitFromDeviceIntent` builds an empty projection
and sets `actuatedIntent = true`. The drift guard then detects factory
CONFIG_DB entries (e.g., BGP_GLOBALS|default from factory config) as
"drift" from the empty projection.

But this is not drift — it's the initial state before any intents exist.
Factory CONFIG_DB entries are pre-intent infrastructure, not drift from
intents that haven't been written yet.

**Fix**: Skip the drift guard when the intent DB is empty:
```go
if n.actuatedIntent && len(n.configDB.NewtronIntent) > 0 {
    // drift guard logic...
}
```

**Architecture justification**: Architecture §8 (Drift Guard): "the drift
guard ensures new intents are never applied on a drifted foundation." With
zero intents, there IS no foundation to drift from. The drift guard's
purpose is to detect external CONFIG_DB modifications after intents are
established — not to block the first write on a fresh device.

The crash recovery table at §8 enumerates crash scenarios, all of which
assume intents exist on the device. A fresh device (zero intents) is not
a crash scenario — it's the initial state. The first `setup-device` call
establishes the intent foundation; subsequent writes are protected by
the drift guard.

---

## Fix 13: ACL Rule Intent Round-Trip Completeness + skipInReconstruct

**Files**:
- `pkg/newtron/network/node/acl_ops.go` (AddACLRule)
- `pkg/newtron/network/node/reconstruct.go` (skipInReconstruct, ReplayStep)

**Symptom**: ACL rules created via standalone `AddACLRule` were lost during
reconstruction (RebuildProjection). After reconnection, the projection had
the ACL table but no rules.

**Root cause** (two bugs, same pattern):

1. `skipInReconstruct` included `OpAddACLRule`, assuming ACL rules are always
   re-created by their parent `CreateACL` during replay. This is wrong:
   `CreateACL` only creates rules passed in its initial `opts.Rules` parameter.
   Standalone `AddACLRule` creates its own intent (`acl|{table}|{rule}`) that
   is NOT re-created by replaying the parent `CreateACL`.

2. `writeIntent` in `AddACLRule` stored only `{name: ruleName}` — missing all
   config params (priority, action, src_ip, dst_ip, protocol, src_port,
   dst_port). Even if the intent were replayed, the rule would have no
   meaningful content.

**Fix**:

1. Removed `OpAddACLRule` from `skipInReconstruct`. The intent now participates
   in `IntentsToSteps` and gets replayed independently.

2. Stored full rule config in intent params:
   ```go
   intentParams := map[string]string{
       sonic.FieldName: ruleName,
       "acl":           tableName,
   }
   if opts.Priority > 0 { intentParams["priority"] = strconv.Itoa(opts.Priority) }
   if opts.Action != "" { intentParams["action"] = opts.Action }
   // ... src_ip, dst_ip, protocol, src_port, dst_port
   ```

3. Added `add-acl-rule` case to `replayNodeStep` in `reconstruct.go`.

**Architecture justification**: CLAUDE.md "Intent Round-Trip Completeness":
"Every param that affects CONFIG_DB output must complete the full round-trip:
writeIntent stores it, intentParamsToStepParams exports it, ReplayStep
passes it back." The ACL rule violated all three parts.

Architecture §1 "Intent DB is primary state": each resource MUST have
exactly one canonical representation in the intent DAG. `skipInReconstruct`
violated this by assuming the parent would recreate the child — creating a
dual-representation path (parent-recreated vs standalone) for the same
resource. Removing from skipInReconstruct ensures ONE path: the intent IS
the resource's representation, always.

**Related**: See RCA-046 for the uniform pattern analysis.

---

## Fix 14: PortChannel Member Intent Round-Trip Completeness + skipInReconstruct

**Files**:
- `pkg/newtron/network/node/portchannel_ops.go` (AddPortChannelMember)
- `pkg/newtron/network/node/reconstruct.go` (skipInReconstruct, ReplayStep)

**Symptom**: PortChannel members added via standalone `AddPortChannelMember`
were lost during reconstruction. Same pattern as Fix 13.

**Root cause** (same two-bug pattern as Fix 13):

1. `skipInReconstruct` included `OpAddPortChannelMember`, assuming members are
   always re-created by `CreatePortChannel`. Wrong: `CreatePortChannel` only
   creates members passed in its initial `opts.Members`. Standalone
   `AddPortChannelMember` creates its own intent
   (`portchannel|{pcName}|{member}`) that is NOT re-created by replaying
   `CreatePortChannel`.

2. `writeIntent` stored only `{name: member}` — missing the `portchannel`
   param needed for replay.

**Fix**:

1. Removed `OpAddPortChannelMember` from `skipInReconstruct`.

2. Stored portchannel name in intent params:
   ```go
   map[string]string{
       sonic.FieldName: member,
       "portchannel":   pcName,
   }
   ```

3. Added `add-pc-member` case to `replayNodeStep` in `reconstruct.go`.

**Architecture justification**: Same as Fix 13. CLAUDE.md Intent Round-Trip
Completeness + Architecture §1 single-representation principle.

**Pattern note**: Fixes 13 and 14 are the SAME underlying pattern: standalone
operations whose intents were incorrectly skipped during reconstruction.
After these fixes, `skipInReconstruct` contains only `OpInterfaceInit`
(which IS always auto-created by sub-resource operations like SetProperty,
BindACL, ApplyQoS — this is correct because InterfaceInit has no standalone
API). See `feedback_uniform_patterns.md` for the general principle.

---

## Fix 15: setup-device YAML restructure — pre/post reconcile

**File**: `newtrun/suites/2node-vs-primitive/02-setup-device.yaml`

**Symptom**: `setup-device` on a device with stale CONFIG_DB from previous
test runs hit the drift guard ("device drifted from intents — reconcile
first").

**Root cause**: `setup-device` uses incremental `cs.Apply()` (HSET only),
which does NOT clean stale entries. On a device that previously had full
provisioning (VLANs, VRFs, ACLs, etc.), the first `setup-device` creates
a minimal projection (just device baseline) while the device CONFIG_DB still
has entries from the previous run. The drift guard (Fix 12: skipped when
zero intents) allows the first write, but subsequent writes see drift.

**Fix**: Added reconcile steps in the scenario:
1. Reconcile BEFORE setup-device — aligns device CONFIG_DB with any existing
   intents (cleans stale entries from previous runs when combined with the
   config reload in Reconcile).
2. Setup-device — creates device baseline intents.
3. Reconcile AFTER setup-device — delivers the new projection to the device,
   ensuring device CONFIG_DB matches the full projection including device
   intent infrastructure.

**Architecture justification**: Architecture §6 (Reconcile): "Deliver the
full projection to the device, eliminating drift." The pre-setup reconcile
handles the transition from a previously provisioned device to a clean
baseline. The post-setup reconcile delivers the new projection. This is
the correct E2E test pattern: reconcile to clean → create intents →
reconcile to deliver.

---

## Fix 16: Remove Redundant RWMutex from Node — Actor Provides Serialization

**Files**:
- `pkg/newtron/network/node/node.go` (removed `mu sync.RWMutex` and all 20 lock sites)
- `pkg/newtron/network/node/interface_ops.go` (removed lock from `InterfaceExists`)
- `pkg/newtron/network/node/service_ops.go` (removed lock from `InterfaceHasService`)

**Symptom**: `POST /node/switch1/interface/PortChannel1/configure-interface`
hung for 5 minutes then returned 504 Gateway Timeout. All subsequent
requests for the same device also hung (actor blocked).

**Root cause**: Go's `sync.RWMutex` is NOT reentrant. `GetInterface` acquired
`n.mu.Lock()` (write lock), then called `InterfaceExists` → `GetIntent` →
`n.mu.RLock()`. A goroutine holding a write lock cannot acquire a read lock
on the same mutex — deadlock. The actor goroutine was permanently stuck.

The deeper issue: **the mutex was redundant**. The `NodeActor` in
`api/actors.go` already guarantees single-goroutine access to the Node via
a channel-based actor pattern. `NodeActor.run()` processes one request at
a time — only the actor goroutine ever touches the Node. The mutex added
no concurrency protection, only complexity and a deadlock hazard.

Evidence: `InitFromDeviceIntent` had to perform lock gymnastics — acquire
write lock for steps 2-5, release it before calling `ReplayStep` (which
re-acquired locks), then re-acquire for steps 7-8. This contortion was
needed because the mutex was fighting the actor's serialization, not
complementing it.

**Fix**: Removed the `sync.RWMutex` field and all 22 lock/unlock call
sites entirely. The actor pattern is the serialization mechanism. The
Node is a single-threaded object owned by its actor goroutine.

Specific cleanups:
- `InitFromDeviceIntent`: removed anonymous function wrapper and separate
  lock/unlock block — all code runs inline
- `GetInterface`: calls `InterfaceExists` directly (no lock concern)
- `ListInterfaces`: calls `IntentsByPrefix` directly
- Removed `getIntent()`, `intentsByPrefix()`, `interfaceExists()` lockless
  variants — they were bandaids on the mutex, unnecessary without it

**Architecture justification**: The actor pattern (`api/actors.go`) is
the concurrency model. Architecture §8 (Lock): "Actor serialization
ensures one writer per device." The distributed Redis lock in `Lock()`
provides inter-process exclusion. The actor channel provides intra-process
serialization. The `sync.RWMutex` added a third, redundant layer that
conflicted with the actor's guarantees.

**Principle**: Don't add synchronization primitives to objects that are
already protected by a higher-level serialization mechanism. The actor
pattern IS the synchronization — adding a mutex inside the actor-owned
object is redundant and, as demonstrated, actively harmful.

---

## Fix 17: UnbindIPVPN — Missing BGP_GLOBALS Deletion (Operational Symmetry)

**File**: `pkg/newtron/network/node/vrf_ops.go` (unbindIpvpnConfig)

**Symptom**: `teardown-overlay` step `delete-vrf-irb-sw1` failed with
"device drifted from intents (1 entries) — reconcile first". The drift
entry was `BGP_GLOBALS|Vrf_IRB` (type: "extra") — present on the device
but not in the projection.

**Root cause**: `bindIpvpnConfig` (line 72-73) creates `BGP_GLOBALS|{vrf}`
with `local_asn` and `router_id` via `CreateBGPGlobalsConfig`. But
`unbindIpvpnConfig` only deleted `BGP_GLOBALS_AF` entries and
`ROUTE_REDISTRIBUTE` — it never deleted `BGP_GLOBALS|{vrf}`. This is an
**operational symmetry violation**: the forward path creates an entry that
the reverse path doesn't clean up.

After `unbind-ipvpn`, the intent (`ipvpn|Vrf_IRB`) is deleted, so the
projection no longer includes `BGP_GLOBALS|Vrf_IRB`. But the entry
persists in Redis. The next write (`delete-vrf`) triggers the drift guard,
which correctly detects the stale `BGP_GLOBALS|Vrf_IRB` as drift.

Note: `destroyVrfConfig` (cascading VRF deletion) had a separate
`deleteBgpGlobalsConfig` call because it knew `unbindIpvpnConfig` was
incomplete. With the fix, `unbindIpvpnConfig` is now self-contained, so
the duplicate in `destroyVrfConfig` was removed.

**Fix**: Added `deleteBgpGlobalsConfig(vrfName)` to `unbindIpvpnConfig`,
after the child entries (BGP_GLOBALS_AF, ROUTE_REDISTRIBUTE). Removed the
now-redundant standalone call in `destroyVrfConfig`.

**Architecture justification**: CLAUDE.md "Operational Symmetry": "For
every forward action there MUST be an equal and opposite reverse action."
`bindIpvpnConfig` creates BGP_GLOBALS, BGP_GLOBALS_AF (x2),
ROUTE_REDISTRIBUTE, and BGP_GLOBALS_EVPN_RT. `unbindIpvpnConfig` must
delete all of them. The BGP_GLOBALS deletion was missing.

Also: CLAUDE.md "CONFIG_DB Write Ordering": "Reverse operations must
delete in the opposite order (children before parents)." BGP_GLOBALS_AF
depends on BGP_GLOBALS. The deletion order is: BGP_GLOBALS_AF first,
then BGP_GLOBALS — correct per the ordering principle.

---

## Fix 18: ARP Suppression Missing After Reconstruction

**File**: `pkg/newtron/network/node/service_ops.go` (ApplyService)

**Symptom**: `service-lifecycle` step `refresh-svc-sw1` fails with drift
guard error: `SUPPRESS_VLAN_NEIGH|Vlan400` extra on device. The entry
exists in Redis from the original `ApplyService`, but after
`RebuildProjection` it's missing from the projection.

**Root cause**: The ARP suppression guard at line 409 was:
```go
if !vlanCS.IsEmpty() && macvpnDef != nil && macvpnDef.ARPSuppression {
```

During reconstruction (`RebuildProjection` → `IntentsToSteps` →
`ReplayStep`), intents replay in topological order: `vlan|400` replays
BEFORE `interface|Ethernet12`. When `ApplyService` replays for
Ethernet12, `CreateVLAN(400)` returns an empty ChangeSet (vlan intent
already exists — intent-idempotent). The `!vlanCS.IsEmpty()` guard
evaluates to false, so `SUPPRESS_VLAN_NEIGH|Vlan400` is NOT added to
the projection. The device still has the entry from the original apply,
so the drift guard detects it as extra.

This is an instance of a broader pattern bug: entries conditionally
gated on sub-operation freshness (`!subCS.IsEmpty()`) will be omitted
during reconstruction because the sub-operation's intent was already
replayed in a prior step. Searched for all `IsEmpty()` guards in the
node package — this is the only instance.

**Fix**: Removed the `!vlanCS.IsEmpty()` guard. ARP suppression is now
added unconditionally when `macvpnDef != nil && macvpnDef.ARPSuppression`.
`render(cs)` handles upserts safely — duplicate `SUPPRESS_VLAN_NEIGH`
entries are harmless (same table/key/fields → no-op in the projection).

**Architecture justification**: Architecture §2 "One Pipeline": every
config method renders entries into the projection via `render(cs)`.
The projection must contain ALL entries the device should have. Coupling
entry generation to sub-operation freshness breaks the invariant during
reconstruction, where intents replay in dependency order and earlier
intents have already populated the projection.

Architecture §4: "render(cs) updates the projection" — it's an upsert,
not an insert. Adding ARP suppression when the VLAN already exists
is correct and idempotent.

---

## Fix 19: CheckBGPSessions — missing overlay peers from SetupVTEP

**File**: `pkg/newtron/network/node/health_ops.go` (CheckBGPSessions)

**Symptom**: `verify-evpn-bgp-sessions` in service-lifecycle failed — bgp/check
returned only the underlay peer (10.1.0.1), not the overlay peer (10.0.0.2).
Overlay BGP sessions were Established on the device but CheckBGPSessions didn't
know to check them.

**Root cause**: `CheckBGPSessions` builds its expected peer set from intent DB
scans: `IntentsByPrefix("evpn-peer|")` for overlay and
`IntentsByPrefix("interface|...|bgp-peer")` for underlay. But overlay peers
created by `SetupVTEP` (a sub-operation of `SetupDevice`) don't have individual
`evpn-peer|{ip}` intent records — they're part of the `device` intent's
rendering, derived from `resolved.BGPNeighbors`.

This is a regression from the intent-based reads conversion: the old code read
`configDB.BGPNeighbor` (the projection), which contained all neighbors. The new
code reads only explicit `evpn-peer|{ip}` intents, missing SetupVTEP-created
peers.

**Fix**: Added a third source to CheckBGPSessions: if the `device` intent
exists with `source_ip` (meaning SetupVTEP ran), derive overlay peers from
`resolved.BGPNeighbors`. This is architecturally correct — the device intent
IS the intent for SetupVTEP's sub-operations, and `resolved.BGPNeighbors` is
deterministically derived from the profile.

Three sources of expected peers:
1. SetupVTEP overlay peers: `GetIntent("device")` + `resolved.BGPNeighbors`
2. Standalone overlay peers: `IntentsByPrefix("evpn-peer|")` (AddBGPEVPNPeer)
3. Underlay peers: `IntentsByPrefix("interface|...|bgp-peer")`

Deduplication via `seenOverlay` map prevents double-counting peers that appear
in both source 1 and source 2.

**Test fix**: Removed the `restart-bgp-for-service` step and 90s convergence
wait from service-lifecycle. These were bandaids for the missing overlay peer
issue — the restart caused systemd rate-limiting failures ("Start request
repeated too quickly") and added ~100s to the test. With the root cause fixed,
overlay BGP config survives teardown-overlay (only VLANs/VRFs/MAC-VPN removed,
not VTEP/BGP infrastructure), so no restart is needed.

**Architecture justification**: Architecture §1 "Intent DB is the decision
substrate": operational logic reads intent DB, not projection. But "intent"
includes the composite device intent and its implied sub-operations. The device
intent + resolved profile together fully determine which overlay peers exist.
CheckBGPSessions reading both is analogous to how preconditions check
`GetIntent("device")` + profile params rather than looking for sub-intents.

**Bug category**: 3 (Intent Round-Trip Incompleteness) — variant. The CONFIG_DB
entries reconstruct correctly (SetupVTEP re-derives them from profile during
replay). But the intent-based query missed them because the query assumed
explicit `evpn-peer|{ip}` intents exist for all overlay peers.

---

## Fix 20: SetupVTEP overwrites BGP_GLOBALS|default — strips ebgp_requires_policy

**File**: `pkg/newtron/network/node/evpn_ops.go` (SetupVTEP, line 214)

**Suite**: 2node-vs-service

**Scenario**: provision (verify-bgp)

**Symptom**: BGP sessions never establish — underlay peers show "peerState: Policy"
(0 prefixes exchanged), overlay peers stuck in "Connect".

**Root cause**: `SetupVTEP` called `CreateBGPGlobalsConfig("default", ..., nil)`
which wrote `BGP_GLOBALS|default` with only `local_asn` and `router_id`.
`ConfigureBGP` (called earlier in `SetupDevice`) had already written the same
key with additional fields: `ebgp_requires_policy: false`,
`suppress_fib_pending: false`, `log_neighbor_changes: true`.

The `configTableHydrators` registry replaces the entire typed struct on each
write — the later write with nil extra fields stripped the earlier write's
fields. Without `ebgp_requires_policy: false`, FRR enforces route-map
requirements on all eBGP sessions, blocking prefix exchange.

**Fix**: Removed the redundant `BGP_GLOBALS|default` write from `SetupVTEP`.
`ConfigureBGP` already creates it with the correct fields. `SetupVTEP` only
needs the l2vpn_evpn address-family entry (`BGP_GLOBALS_AF|default|l2vpn_evpn`)
and the peer group — not a second `BGP_GLOBALS|default` entry.

**Architecture justification**: Single-Owner Principle — `BGP_GLOBALS|default`
is owned by `ConfigureBGP`. Sub-operations like `SetupVTEP` must not re-write
entries already created by the owning function.

**Bug category**: 11 (Hydrator Replace Semantics) — the hydrator replaces the
entire typed struct on every write. Any two writes to the same table|key where
the second has fewer fields will silently strip fields from the first write.

---

## Fix 21: BGP_GLOBALS_AF struct missing redistribute_connected and redistribute_static fields

**Files**: `pkg/newtron/device/sonic/configdb.go`, `pkg/newtron/device/sonic/configdb_parsers.go`

**Suite**: 2node-vs-service (latent — would cause false drift on nodes with redistribute overrides)

**Symptom**: After `RebuildProjection`, `BGP_GLOBALS_AF` entries lose
`redistribute_connected` and `redistribute_static` fields. The hydrator reads
from the field map into the struct, so missing struct fields are silently
dropped.

**Root cause**: `addBGPRoutePolicies` (`service_ops.go:711`) writes
`redistribute_connected: true` and `redistribute_static: true` to
`BGP_GLOBALS_AF` entries. The schema (`schema.go:276-277`) validates these
fields. But the `BGPGlobalsAFEntry` struct (`configdb.go:422-437`) and the
`BGP_GLOBALS_AF` hydrator (`configdb_parsers.go:144-158`) didn't include them.

During intent replay (`RebuildProjection`), the hydrator reads the field values
from the ChangeSet into the struct — fields without struct counterparts are
discarded. `ExportEntries` then exports only struct fields, missing the
redistribute fields. The reconstructed projection is missing these entries →
false drift on a correctly-configured device.

**Fix**: Added `RedistributeConnected` and `RedistributeStatic` fields to the
`BGPGlobalsAFEntry` struct and the `BGP_GLOBALS_AF` hydrator.

**Architecture justification**: Every field written to CONFIG_DB by any config
function must have a corresponding field in the typed struct AND the hydrator.
The intent round-trip requires that `ExportEntries` produces the same entries
that the original operation wrote — struct fields are the mechanism by which
projection state survives `RebuildProjection`.

**Bug category**: 11 (Hydrator Replace Semantics) — sub-pattern: a struct
missing fields causes those fields to be silently dropped during hydration, even
when the schema accepts them.

---

## Fix 22: save-roundtrip test assertion used API param name instead of intent param name

**File**: `newtrun/suites/1node-vs-architecture/26-save-roundtrip.yaml`

**Test**: `verify-vlan-params-survived`

**Symptom**: The `verify-vlan-params-survived` step failed — the jq assertion
`.params.id == "123"` evaluated to false even though the VLAN 123 step was
present in topology.json.

**Root cause**: The assertion checked `.params.id` but the intent stores the
VLAN ID under the key `vlan_id`, not `id`. The key `id` is the API request
field name (used in `VLANCreateRequest.ID`); the intent param key is `vlan_id`
(used in `writeIntent` and `intentParamsToStepParams`).

**Fix**: Changed assertion from `params.id == "123"` to `params.vlan_id == "123"`.

**Architecture justification**: Intent params use domain-level keys (`vlan_id`),
not API request field names (`id`). The intent round-trip stores what the
operation needs for replay — values are keyed by the name used in `writeIntent`
and exported by `intentParamsToStepParams`, which are the replay-facing names,
not the HTTP handler field names.

**Bug category**: Test assertion error — API param name vs intent param name mismatch.

**Note**: The first run failure also revealed that topology.json is a shared file
that persists across test runs. When `save-roundtrip` failed mid-test, the saved
VLAN 123 step remained in topology.json, contaminating subsequent fresh runs. The
fix was to restore topology.json to its clean state (just the setup-device step)
before rerunning.
