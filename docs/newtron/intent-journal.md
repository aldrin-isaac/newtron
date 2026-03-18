# Persistent Intent Records and Drift Detection

## 1. Problem

newtron records in-flight operations via intent records in STATE_DB for
crash recovery. Two problems:

1. **STATE_DB is volatile.** Cleared on reboot. If a process crashes
   mid-apply and the device reboots before recovery, the zombie evidence
   is gone. The next operator connects to a device with orphaned partial
   state and no breadcrumb.

2. **No drift detection.** newtron can verify its own writes immediately
   after applying (ChangeSet verification), but has no way to detect
   drift over time — unauthorized CONFIG_DB edits, daemon-induced
   changes, or spec changes that haven't been refreshed onto devices.

## 2. Solution

Three changes, cleanly separated:

1. **Move the intent record to CONFIG_DB.** Same lifecycle as today
   (write before acting, delete on success), but persistent across
   reboot. The intent record is *current intent* — what the device is
   supposed to look like right now — not history.

2. **Rolling operation history for rollback.** Keep the last 10
   completed commits in CONFIG_DB. Fixed-size ring buffer — when the
   11th completes, the oldest is evicted. Enables "undo last N
   operations" without unbounded growth.

3. **Drift detection via abstract Node reconstruction.** Use
   `GenerateDeviceComposite()` with current specs + service bindings
   to produce the expected CONFIG_DB. Diff against actual CONFIG_DB.
   No journal needed — the specs, profiles, and bindings already encode
   the full expected state.

### What this is NOT

**Not a journal.** An append-only journal in CONFIG_DB would grow
indefinitely — thousands of entries after years of operation — bloating
`config save`, `config reload`, and every `KEYS *` scan. CONFIG_DB is
a configuration database: it should contain what the device should look
like, not what was done to it over the years. History belongs in logs or
an external audit system, not in the device's configuration store.

The rolling history (item 2) is bounded at 10 entries — a fixed-cost
undo buffer, not an ever-growing record.

---

## 3. Design Principles Applied

| Principle | Application |
|-----------|-------------|
| §5 Specs are intent; device is reality | Expected state derived from current specs. Device CONFIG_DB is actual state. Drift = diff(expected, actual). |
| §9 The ChangeSet is universal | Reconstruction produces ChangeSets through the same code path used for provisioning. |
| §12 Verify writes, observe the rest | Drift detection extends "verify your own writes" from immediate to historical. newtron reports drift; the orchestrator judges. |
| §13 Symmetric operations | Crash recovery intent survives reboot. Zombie detection structural proof preserved. |
| §23 Abstract Node | Reconstruction uses the same abstract Node that topology provisioning uses. One code path, different initialization. |
| §36 Reconstruct, don't record | See §8. |
| §37 On-device intent sufficiency | See §8. |
| §38 Bounded device footprint | See §8. |

---

## 4. Persistent Intent Record

### 4.1 Why CONFIG_DB

STATE_DB is cleared on reboot. CONFIG_DB survives reboot (via
`config save`). `NEWTRON_INTENT` already proves the pattern:
newtron-owned records in CONFIG_DB that SONiC daemons ignore (no YANG
model, no daemon subscription).

### 4.2 Table Schema

**Table:** `NEWTRON_INTENT`

**Key format:** `NEWTRON_INTENT|<device>`

Singular per device — the lock serializes operations, so at most one
intent exists per device at any time. Same key format as the current
STATE_DB record.

**Fields:** Identical to the current STATE_DB schema:

| Field | Type | Description |
|-------|------|-------------|
| `holder` | string | Same as lock holder (`user@hostname`) |
| `created` | RFC3339 | When the commit started |
| `phase` | string | `""` (applying) or `"rolling_back"` |
| `rollback_holder` | string | Who initiated rollback |
| `rollback_started` | RFC3339 | When rollback began |
| `operations` | JSON | Array of operation sub-records |

**Operation sub-record:** Unchanged.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Operation name (`device.create-vlan`) |
| `params` | map | Parameters for reversal (`{"vlan_id": "100"}`) |
| `reverse_op` | string | Reverse operation name |
| `started` | RFC3339 | When Apply() began |
| `completed` | RFC3339 | When Apply() succeeded |
| `reversed` | RFC3339 | When rollback reversed this op |

### 4.3 Lifecycle (Unchanged)

The intent lifecycle is identical to today:

```
Lock → fn(ctx) → WriteIntent → per-op Apply → Verify → DeleteIntent → Unlock
```

- **WriteIntent** writes to CONFIG_DB instead of STATE_DB
- **DeleteIntent** deletes from CONFIG_DB instead of STATE_DB
- **Zombie detection** at Lock() reads CONFIG_DB instead of STATE_DB
- **No Redis EXPIRE** — same as today. Intent persists until explicitly
  deleted. Now it also persists across reboot.

The structural proof is preserved unchanged: if you hold the lock and
an intent exists, the previous holder is dead.

### 4.4 What Survives Reboot

After reboot + `config reload`, CONFIG_DB is restored from
`/etc/sonic/config_db.json`. The intent record survives because:

1. `Commit()` calls `SaveConfig()` after successful apply (already does
   this today)
2. If the process crashes *before* `SaveConfig()`, the intent is in
   Redis but not on disk. On reboot, it's gone — but so is the partial
   CONFIG_DB state (config reload restores the last saved state). No
   orphans, no zombie needed.
3. If the process crashes *after* `SaveConfig()` but before
   `DeleteIntent`, the intent is on disk. On reboot, it's restored.
   The zombie mechanism detects it. This is the scenario that STATE_DB
   couldn't handle.

### 4.5 Schema Registration

Add `NEWTRON_INTENT` to `schema.go`. No YANG model (newtron-owned
table). Constraints derived from usage patterns. Same approach as
`NEWTRON_INTENT`.

### 4.6 Rolling Operation History

After a successful commit, the completed operations are archived into
a rolling history buffer before `DeleteIntent`. This enables rollback
of recent operations — not crash recovery (that's the zombie path), but
intentional undo.

**Table:** `NEWTRON_HISTORY`

**Key format:** `NEWTRON_HISTORY|<device>|<sequence>`

Sequence is monotonically increasing per device. Maximum 10 entries
per device. When the 11th commit completes, the oldest entry is deleted.

**Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `holder` | string | Who performed the commit |
| `timestamp` | RFC3339 | When the commit completed |
| `operations` | JSON | Array of operation sub-records (same schema as intent) |

The operations array is identical to the intent record's — same `name`,
`params`, `reverse_op` fields. The `started`/`completed` timestamps are
preserved for auditability. `reversed` is initially nil.

**Lifecycle:**

```
Lock → WriteIntent → Apply → Verify → ArchiveToHistory → DeleteIntent → Unlock
```

`ArchiveToHistory` is a new step between verify and delete:

1. Copy the intent's operations array into a new `NEWTRON_HISTORY` entry
2. Increment the device's sequence counter
3. If count > 10, delete the oldest entry (`DEL NEWTRON_HISTORY|device|oldest_seq`)
4. Then `DeleteIntent` removes the in-flight intent as before

**Why 10?** Large enough to cover a reasonable undo window (an operator
who made a mistake in the last few operations). Small enough that
CONFIG_DB overhead is negligible — 10 hash entries with a JSON field
each. The limit is a constant, not configurable, to keep the design
simple.

**Rollback from history:**

Rollback of a history entry uses the same `dispatchReverse` mechanism
as zombie rollback:

1. Read the history entry
2. Iterate operations in reverse order
3. For each: call the domain-level reverse operation
4. Mark each operation as `reversed` (persist to history entry)
5. On completion, the history entry remains (with all ops marked
   reversed) — it is not deleted, so the operator can see what was
   undone

Rollback is always most-recent-first. You cannot roll back entry N
without first rolling back entries N+1 through newest — operations
may depend on each other. The API enforces this ordering.

**Rollback does not enter history.** Rollback operations are
*consumption* of history, not new history. If rollbacks were archived,
they would push out the original operations — after 10 rollbacks, the
undo buffer would contain only undo records. The `ArchiveToHistory`
step is skipped when the commit is a rollback (same mechanism as
`bypassZombieCheck` — a flag on the Execute path).

This means:
- Rollback 1 reverses the most recent un-reversed entry, marks it
  reversed. No new history entry.
- Rollback 1 again reverses the *next* un-reversed entry. No
  undo-undo cycle.
- After rolling back all 10 entries, the history is fully reversed
  and further rollback returns "nothing to roll back."

**Interaction with zombie recovery:** History entries are always
`completed` commits. Zombie entries are `started` but never completed.
The two mechanisms are orthogonal:

- Zombie: "a commit crashed mid-apply — reverse the partial work"
- History rollback: "a commit succeeded but I want to undo it"

### 4.7 Migration

On first connect after upgrade, if STATE_DB contains
`NEWTRON_INTENT|<device>`, move it to CONFIG_DB and delete the
STATE_DB record. One-time, no backwards compatibility (§32).

---

## 5. Drift Detection

### 5.1 The Insight

newtron already knows how to compute the expected CONFIG_DB state for a
device — `GenerateDeviceComposite()` does exactly this. It creates an
abstract Node, calls the same operations that provisioning uses, and
produces a complete set of CONFIG_DB entries. Health verification
(`VerifyDeviceHealth`) already uses this to check config intent.

For post-provision operations (incremental apply-service, create-vlan,
etc.), the expected state is:

- **Provisioned baseline:** `GenerateDeviceComposite()` with current
  specs and topology
- **Plus incremental operations:** Derived from `NEWTRON_INTENT`
  records on the device (which services are applied to which interfaces)

The service bindings are already in CONFIG_DB. The specs and profiles
are on disk. No journal needed — all inputs to reconstruction are
already persistent.

### 5.2 Reconstruction Algorithm

```
1. Load current specs, profile, and topology for the device
2. Call GenerateDeviceComposite() → expected CONFIG_DB after provisioning
3. Read NEWTRON_INTENT records from actual CONFIG_DB
4. For each binding that is NOT in the topology (post-provision ops):
   a. Create abstract Node with expected state as shadow
   b. Replay the operation (ApplyService, BindACL, etc.)
   c. Shadow accumulates the entries
5. Shadow ConfigDB = full expected state
6. Load actual CONFIG_DB from device
7. Diff(expected, actual) = drift
```

**Step 4 handles the gap between "what provisioning creates" and "what
the device should have now."** Provisioning creates the topology-defined
services. Post-provision operations add services that aren't in the
topology. The bindings record what was added — and since bindings are
self-sufficient (§13, CLAUDE.md), they contain all parameters needed
to reconstruct the operation.

### 5.3 Why Not a Journal

A journal would record every operation chronologically and replay them
to reconstruct state. This seems natural but has problems:

- **Unbounded growth.** After years, the journal dominates CONFIG_DB.
  CONFIG_DB is the device's configuration — it powers `config save`,
  `config reload`, and daemon startup. Bloating it with history degrades
  device operations.
- **Redundant with existing data.** The specs + topology + bindings
  already encode the expected state. The journal would be a second,
  less authoritative copy.
- **Replay fragility.** Replaying thousands of operations through
  abstract Node methods is slower and more fragile than generating the
  expected state directly from current specs.
- **Not CONFIG_DB's role.** CONFIG_DB should contain intent — what the
  device should look like — not history. The intent record (in-flight
  operation) is intent. A completed-operation journal is history.

If audit history is needed, it belongs in structured logging or an
external store — not in the device's configuration database.

### 5.4 The Diff

Drift = diff(expected ConfigDB, actual ConfigDB).

Both are `*sonic.ConfigDB` instances. The diff produces three
categories:

| Category | Meaning |
|----------|---------|
| **Missing** | Expected entry absent from device. Deleted by someone, lost in a daemon restart, or never applied. |
| **Extra** | Device entry not in expected state. Added outside newtron. |
| **Modified** | Entry exists in both but fields differ. Changed on device or spec changed since last apply. |

### 5.5 Scope

The diff only covers tables in newtron's ownership map (§18). Tables
outside the map are excluded — newtron has no opinion about NTP, SNMP,
TACACS, or other entries it didn't create.

Excluded from diff:
- `NEWTRON_INTENT` — ephemeral crash recovery, not configuration
- `NEWTRON_LOCK` — STATE_DB, not in CONFIG_DB
- Tables without newtron ownership

### 5.6 Drift Categories

All differences are reported uniformly. The operator (or orchestrator)
decides which require action:

- **Unauthorized modification:** An entry newtron created was changed
  by someone else (manual redis-cli, SONiC CLI, another tool).
- **Missing entry:** An entry newtron expects is gone.
- **Spec drift:** Current spec produces different entries than what's on
  the device. The device needs a refresh.
- **Extra entry:** An entry exists that newtron never created. Not
  necessarily wrong, but flagged.

newtron reports; the orchestrator judges (§12).

---

## 6. API

### 6.1 Drift Detection

```
GET  /network/{netID}/node/{device}/drift
```

Performs reconstruction + diff. Returns:

```json
{
  "device": "leaf1",
  "status": "drifted",
  "missing": [
    {"table": "VLAN", "key": "Vlan100", "fields": {"vlanid": "100"}}
  ],
  "extra": [
    {"table": "VLAN", "key": "Vlan999", "fields": {"vlanid": "999"}}
  ],
  "modified": [
    {
      "table": "BGP_GLOBALS",
      "key": "default",
      "expected": {"local_asn": "65001"},
      "actual": {"local_asn": "65099"}
    }
  ]
}
```

If no differences: `"status": "clean"` with empty arrays.

### 6.2 Network-Level Drift Summary

```
GET  /network/{netID}/drift
```

Returns per-device drift status without full diff detail. Useful for
dashboards:

```json
{
  "devices": [
    {"device": "leaf1", "status": "clean"},
    {"device": "leaf2", "status": "drifted", "missing": 1, "extra": 0, "modified": 2}
  ]
}
```

### 6.3 Operation History and Rollback

```
GET  /network/{netID}/node/{device}/history
POST /network/{netID}/node/{device}/history/rollback
```

**GET** — returns the rolling history (up to 10 entries), newest first:

```json
{
  "device": "leaf1",
  "entries": [
    {
      "sequence": 42,
      "holder": "admin@mgmt",
      "timestamp": "2026-03-15T10:30:00Z",
      "operations": [
        {
          "name": "interface.apply-service",
          "params": {"interface": "Ethernet4", "service": "transit"},
          "reverse_op": "interface.remove-service",
          "reversed": null
        }
      ]
    },
    {
      "sequence": 41,
      "holder": "admin@mgmt",
      "timestamp": "2026-03-15T10:25:00Z",
      "operations": [
        {
          "name": "device.create-vlan",
          "params": {"vlan_id": "100"},
          "reverse_op": "device.delete-vlan",
          "reversed": null
        }
      ]
    }
  ]
}
```

**POST rollback** — rolls back the most recent un-reversed history
entry. Supports `dry_run=true` for preview. Acquires lock.

```json
{
  "rolled_back": {
    "sequence": 42,
    "operations_reversed": 1
  }
}
```

To roll back multiple operations, call rollback repeatedly — each call
reverses the next most-recent entry. The API enforces newest-first
ordering; callers cannot skip entries.

CLI:
```
newtron -D leaf1 device history              → GET  .../history
newtron -D leaf1 device history rollback      → POST .../history/rollback (dry-run)
newtron -D leaf1 device history rollback -x   → POST .../history/rollback (execute)
```

### 6.4 Zombie Endpoints (Relocated)

```
GET    /network/{netID}/node/{device}/intents/zombies
POST   /network/{netID}/node/{device}/intents/zombies/rollback
DELETE /network/{netID}/node/{device}/intents/zombies
```

Replaces the existing `/zombie` endpoints. Semantics unchanged:

- **GET** — read intent record (now from CONFIG_DB). No lock required.
- **POST rollback** — reverse completed operations. Acquires lock.
  Supports `dry_run=true`.
- **DELETE** — delete intent without reversing (operator accepts partial
  state). Acquires lock.

CLI commands map directly:
```
newtron -D leaf1 device zombie           → GET  .../intents/zombies
newtron -D leaf1 device zombie rollback  → POST .../intents/zombies/rollback
newtron -D leaf1 device zombie clear     → DELETE .../intents/zombies
```

---

## 7. Reconciliation

### 7.1 Surgical Reconciliation

The orchestrator uses drift results to call specific newtron operations:

```
1. GET /network/{netID}/node/{device}/drift
2. For each "missing" entry:
   → Call the forward operation (create-vlan, apply-service, etc.)
3. For each "extra" entry in newtron-owned tables:
   → Call the reverse operation (delete-vlan, remove-service, etc.)
4. For each "modified" entry:
   → Call refresh or re-apply to restore expected state
5. GET /network/{netID}/node/{device}/drift → verify "clean"
```

### 7.2 Full Reprovision

The nuclear option — regenerate and re-deliver the full composite:

```
1. POST /network/{netID}/composite/{device}  → generate
2. POST /network/{netID}/node/{device}/composite/deliver  → overwrite
3. GET  /network/{netID}/node/{device}/drift  → verify "clean"
```

Both strategies are orchestrator concerns. newtron provides the
primitives (drift detection, forward ops, reverse ops, provisioning);
the orchestrator decides the strategy (§8 scope boundaries).

---

## 8. New Design Principles

### §36 Reconstruct, Don't Record

> **Derive expected state from authoritative sources; don't maintain a
> parallel record of it.** The expected CONFIG_DB state for a device is
> fully determined by three persistent inputs: specs (on disk), device
> profile (on disk), and service bindings (in CONFIG_DB). Reconstructing
> expected state from these inputs — using the same abstract Node code
> path that provisioning uses — is cheaper, simpler, and more correct
> than maintaining a chronological journal and replaying it.
>
> A journal is a second copy of information that already exists in a more
> authoritative form. Specs change; the journal doesn't know. Profiles
> change; the journal doesn't know. The reconstruction approach uses
> current specs by definition — there is no stale copy to diverge.
>
> This principle extends §12 (verify writes, observe the rest) from
> immediate to historical: newtron can verify the cumulative effect of
> all its writes against current intent at any time, not just immediately
> after each operation.
>
> CONFIG_DB contains intent — what the device should look like — not
> history. The in-flight intent record (§13) is intent: "I am currently
> doing X." Completed operation history is not intent. It belongs in
> logs, not in the configuration database.

### §37 On-Device Intent Is Sufficient for Reconstruction

> **The device carries enough intent to reconstruct its expected state.**
> `NEWTRON_INTENT` records which services are applied to which
> interfaces, with all parameters needed for both teardown and
> reconstruction. Combined with current specs, the intent record tells you
> exactly what CONFIG_DB entries should exist. No external history, no
> journal replay, no off-device state needed.
>
> This closes the gap between provisioning (topology-defined) and the
> evolved device (post-provision operations). The topology gives you the
> baseline. The intent records give you everything that happened since.
> Together with current specs, you can reconstruct expected state at any
> time.
>
> The existing principle "intent records must be self-sufficient for reverse
> operations" (§13) extends here: **intent records must be self-sufficient for
> reconstruction of expected state.** Teardown is one consumer;
> drift detection is another. Same data, different purpose. If a future
> forward operation creates infrastructure that the intent record can't
> reconstruct, drift detection breaks silently — and there is no test
> that catches it until someone asks "why doesn't drift show this entry?"
>
> This principle makes a specific demand on intent record design: every field
> needed to regenerate the expected CONFIG_DB entries must be stored in
> the intent record. When adding a new forward operation, ask not only "can
> the reverse find everything it needs?" but also "can reconstruction
> regenerate the expected entries from the intent record alone?"

### §38 Bounded Device Footprint

> **Every newtron-owned record in CONFIG_DB must have a cost proportional
> to the device's physical infrastructure — or bounded by a fixed
> constant — never proportional to the number of operations performed
> over time.** CONFIG_DB is operational infrastructure: `config save`
> serializes it, `config reload` deserializes it, daemons scan it at
> startup. Unbounded growth degrades device operations for data that no
> daemon will ever consume.
>
> The intent record is O(1) per device — at most one in-flight operation.
> The rollback history is O(1) per device — capped at 10 entries, oldest
> evicted. Service bindings are O(interfaces) — proportional to physical
> ports, not to time. None grow with the number of operations performed
> over the device's lifetime.
>
> This principle killed the append-only journal: after seven years of
> operations, CONFIG_DB would be dominated by thousands of history
> entries that no SONiC daemon reads, slowing every `config save`,
> `config reload`, and `KEYS *` scan. The fix was not to add compaction
> or archival — it was to recognize that history does not belong in the
> configuration database at all. Audit history belongs in structured
> logging or an external store. CONFIG_DB is for intent.
>
> When adding a new newtron-owned CONFIG_DB table, ask: "does the entry
> count grow with time or with infrastructure?" If the answer is time,
> either cap it (rolling history) or move it out of CONFIG_DB entirely.

**Summary table entries:**

| # | Principle | One-Line Rule | |
|---|-----------|---------------|-|
| 36 | Reconstruct, don't record | Derive expected state from authoritative sources (specs + bindings); CONFIG_DB is for intent, not history | C |
| 37 | On-device intent sufficiency | The device carries enough intent (bindings) to reconstruct expected state; binding design must serve both teardown and reconstruction | C |
| 38 | Bounded device footprint | CONFIG_DB cost must be proportional to infrastructure or bounded by a constant, never proportional to operations over time | C |

---

## 9. What Does NOT Change

- **NEWTRON_INTENT (service records):** Unchanged. Intent records
  capture what infrastructure was applied per interface — the input for both
  `RemoveService` and drift reconstruction. They are intent (what
  should exist), not history.

- **ChangeSet:** Unchanged. Immediate verification after each
  operation.

- **Composite delivery (MULTI/EXEC):** Atomic as before.

- **Lock mechanism:** Unchanged. Lock serializes per device.

- **Abstract Node internals:** `NewAbstract`, `applyShadow`,
  `BuildComposite` unchanged. Reconstruction *uses* the abstract
  Node; it does not modify it.

- **`VerifyDeviceHealth()`:** Already uses `GenerateDeviceComposite()`
  for config intent verification. Drift detection generalizes this
  to include post-provision operations.

---

## 10. Implementation Sequence

1. **Intent to CONFIG_DB:** Change `WriteIntent` / `ReadIntent` /
   `DeleteIntent` to use CONFIG_DB instead of STATE_DB. Add
   `NEWTRON_INTENT` to `schema.go`. Update `Lock()` zombie detection.

2. **Rolling history:** Add `NEWTRON_HISTORY` to `schema.go`. Add
   `ArchiveToHistory` / `ReadHistory` / `RollbackHistory` to device
   and node layers. Update `Commit()` to archive before delete.

3. **Migration:** On first connect, move any STATE_DB intent to
   CONFIG_DB. One-time.

4. **ConfigDB diff:** Add `DiffConfigDB(expected, actual *ConfigDB)`
   to the device layer. Returns missing/extra/modified entries,
   filtered to newtron-owned tables.

5. **Reconstruction:** Add `ReconstructExpectedState(device)` that:
   - Calls `GenerateDeviceComposite()` for provisioned baseline
   - Reads `NEWTRON_INTENT` records for post-provision ops
   - Replays incremental operations on abstract Node
   - Returns expected `*ConfigDB`

6. **Drift detection:** Add `DetectDrift(device)` that calls
   reconstruction, loads actual ConfigDB, diffs.

7. **API:** Add drift endpoints (§6.1, §6.2), history endpoints
   (§6.3). Migrate zombie endpoints to new paths (§6.4).

8. **CLI:** Add `device drift` and `device history` commands.
   Update zombie commands.

9. **Documentation:** Update HLD, LLD, device-LLD, HOWTO, and
   DESIGN_PRINCIPLES_NEWTRON.
