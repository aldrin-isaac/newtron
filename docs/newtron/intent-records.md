# Operation Intent Records

Operation intent records are newtron's crash recovery mechanism for
multi-entry CONFIG_DB writes. If a process dies mid-apply, the intent
record tells the next operator exactly what happened and enables
automated rollback.

---

## The Problem

newtron's write path has an asymmetry. Service operations write
`NEWTRON_INTENT` as a write-ahead record — if the process
crashes mid-apply, `RemoveService` can read the intent record and clean up
partial state. But non-service multi-entry operations (`CreateVLAN`,
`SetupEVPN`, `ConfigureBGP`, `BindIPVPN`, `ApplyQoS`) have no such
protection. If `Apply()` fails midway and the process crashes, the
in-memory ChangeSet is lost and CONFIG_DB has orphaned entries with
no recovery path.

## The Solution

Record your intent on the device before acting, so recovery is
possible across process boundaries. The intent record generalizes the
`NEWTRON_INTENT` pattern to all operations.

**STATE_DB key:** `NEWTRON_INTENT|<device>` — singular, because the
lock serializes operations per device. Mirrors `NEWTRON_LOCK|<device>`.

**Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `holder` | string | Same as lock holder (`user@hostname`) |
| `created` | RFC3339 | When the commit started |
| `phase` | string | `""` (applying) or `"rolling_back"` |
| `rollback_holder` | string | Who initiated rollback |
| `rollback_started` | RFC3339 | When rollback began |
| `operations` | JSON | Array of operation sub-records |

**Operation sub-record:**

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Operation name (`device.create-vlan`) |
| `params` | map | Parameters for reversal (`{"vlan_id": "100"}`) |
| `reverse_op` | string | Reverse operation name (`device.delete-vlan`) |
| `started` | RFC3339 | When Apply() began |
| `completed` | RFC3339 | When Apply() succeeded |
| `reversed` | RFC3339 | When rollback reversed this op |

---

## Lifecycle

The intent record lives inside `Execute()`, between Lock and Unlock.
Lock happens first — it acquires the device lock, refreshes CONFIG_DB,
and checks for existing intents (zombie detection). Only then does
`WriteIntent` record what the operation is about to do. On success,
`DeleteIntent` removes the record before Unlock.

This ordering is what makes zombie detection a structural proof.
When a process crashes, it leaves both its lock and its intent in
STATE_DB. The lock has a Redis EXPIRE of 1 hour; the intent has no
EXPIRE — it persists until explicitly deleted. After the lock expires,
a new process can acquire it. At that point, Lock() checks for an
existing intent. If one exists, the previous holder wrote it (they
had the lock) but never deleted it (they crashed). The new process
holds the lock — proving the previous holder is gone. No staleness
heuristic needed — lock acquisition is the proof.

### Normal Commit (Happy Path)

Two STATE_DB writes per operation (mark started, mark completed).
For a typical 3-operation commit: 8 STATE_DB round-trips total
(create intent, 3×started, 3×completed, delete intent).

```
┌─────────────────────────────┐
│          Execute()          │
└─────────────────────────────┘
  │
  ▼
┌─────────────────────────────┐
│         Lock device         │
└─────────────────────────────┘
  │
  ▼
┌─────────────────────────────┐
│           fn(ctx)           │
│       builds pending        │
│         ChangeSets          │
└─────────────────────────────┘
  │
  ▼
┌─────────────────────────────┐
│         WriteIntent         │
│     holder, ops[], TTL      │
└─────────────────────────────┘
  │
  ▼
┌─────────────────────────────┐
│           per op:           │
│ Started > Apply > Completed │
│  (2 STATE_DB writes each)   │
└─────────────────────────────┘
  │
  ▼
┌─────────────────────────────┐
│         Verify all          │
│         ChangeSets          │
└─────────────────────────────┘
  │
  ▼
┌─────────────────────────────┐
│        DeleteIntent         │
└─────────────────────────────┘
  │
  ▼
┌─────────────────────────────┐
│        Save + Unlock        │
└─────────────────────────────┘
```

The initial `WriteIntent` is **fatal** — if the safety net cannot be
written, the operation does not proceed. Progress updates
(`Started`/`Completed` timestamps) within the apply loop are non-fatal
— they refine the recovery information but the intent already exists.

Intent is **skipped** when: dry-run mode (no writes), abstract/offline
mode (no device), or no pending changes.

---

### Crash during Apply

If the process crashes mid-apply, the intent record survives in
STATE_DB. Its timestamps tell the next operator exactly which
operations completed, which was in progress, and which never started.

```
┌───────────────────────┐
│      WriteIntent      │
└───────────────────────┘
  │ ok
  ▼
┌───────────────────────┐
│         op[0]         │
│  Started + Completed  │
└───────────────────────┘
  │ ok
  ▼
┌───────────────────────┐     ┌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌┐
│         op[1]         │     ╎     op[2]     ╎
│     Started only      │     ╎ no timestamps ╎
└───────────────────────┘     └╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌┘
  ╵ Apply fails
  ▼
┌───────────────────────┐
│         CRASH         │
└───────────────────────┘
  ╵ intent survives
  ▼
┌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌┐
╎ NEWTRON_INTENT|device  ╎
╎  remains in STATE_DB   ╎
└╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌┘
```

**No Redis EXPIRE.** Intent records persist until explicitly deleted
by `DeleteIntent` (success), `rollback`, or `clear`. Silent expiration
would destroy crash evidence — the next operator would see no zombie,
proceed on a device with orphaned partial state, and compound the
problem. If nobody resolves the intent, it stays as a permanent marker
that this device has unresolved partial state.

---

## Zombie Detection and Resolution

The term "zombie" is borrowed from Unix: the operation's process died,
but its state remains on the device, waiting to be reaped.

When a process crashes, its lock expires after 1 hour (Redis EXPIRE).
Its intent survives indefinitely (no EXPIRE). When a new process
acquires the lock and finds an existing intent, it is a zombie — the
lock acquisition proves the previous holder is gone. The device is in
an unknown partial state and new operations are blocked until the
zombie is resolved.

```
                          ┌──────────────────────────┐
                          │       New process        │
                          │       calls Lock()       │
                          └──────────────────────────┘
                            │
                            ▼
                          ┌──────────────────────────┐
                          │        ReadIntent        │
                          │      from STATE_DB       │
                          └──────────────────────────┘
                            │
                            ▼
┌──────────────┐          ┌──────────────────────────┐
│    Normal    │  no      │     intent exists?       │
│  operation   │ ◀─────── │                          │
└──────────────┘          └──────────────────────────┘
                            │ yes
                            ▼
                          ┌──────────────────────────┐
                          │    Execute() returns     │
                          │ ErrDeviceZombieIntent │
                          └──────────────────────────┘
                    ╱              │              ╲
                   ╱               │               ╲
                  ▼                ▼                ▼
┌──────────────────┐  ┌────────────────────┐  ┌──────────────────┐
│  device zombie   │  │ zombie rollback -x │  │   zombie clear   │
│    (inspect)     │  │   (fresh lock)     │  │   (fresh lock)   │
└──────────────────┘  └────────────────────┘  └──────────────────┘
```

**Three resolution paths:**

- **`device zombie`** — read-only inspection. No lock required.
  Displays holder, timestamps, and per-operation status.
- **`zombie rollback -x`** — reverses completed operations using
  domain-level reverse operations. Acquires a fresh lock. Supports
  dry-run preview without `-x`.
- **`zombie clear`** — deletes the intent record without reversing
  anything. For cases where the operator has manually cleaned up or
  the partial state is acceptable. Acquires a fresh lock.

No automatic recovery. The operator (or orchestrator) explicitly
chooses the resolution path.

---

## Rollback (Idempotent Reversal)

Rollback transitions the intent to `rolling_back` phase and reverses
each operation in reverse order. Each successfully reversed operation
is marked with a `Reversed` timestamp. If rollback itself crashes,
retry skips already-reversed operations and continues where it left
off.

```
                                   ┌──────────────────────────────┐
                                   │                              │
                                   │        RollbackZombie        │
                                   │                              │
                                   └──────────────────────────────┘
                                     │
                                     │
                                     ▼
                                   ┌──────────────────────────────┐
                                   │                              │
                                   │     Phase = rolling_back     │
                                   │      set RollbackHolder      │
                                   │     set RollbackStarted      │
                                   │                              │
                                   └──────────────────────────────┘
                                     │
                                     │
                                     ▼
┌─────────────────────┐            ┌──────────────────────────────┐
│                     │            │                              │
│  all ops processed  │            │ iterate ops in reverse order │
│   → DeleteIntent    │  done      │                              │  next op
│                     │ ◀───────── │                              │ ◀╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴┐
└─────────────────────┘            └──────────────────────────────┘                                    ╵
                                     │                                                                 ╵
                                     │                                                                 ╵
                                     ▼                                                                 ╵
┌─────────────────────┐            ┌──────────────────────────────┐            ┌────────────────────┐  ╵
│                     │            │                              │            │                    │  ╵
│   Apply ChangeSet   │            │       dispatchReverse        │            │ PreconditionError  │  ╵
│  mark op.Reversed   │            │    call domain reverse op    │            │ → skip (not found) │  ╵
│ persist to STATE_DB │  success   │                              │            │                    │  ╵
│                     │ ◀───────── │                              │ ─────────▶ │                    │  ╵
└─────────────────────┘            └──────────────────────────────┘            └────────────────────┘  ╵
  ╵                                  │                                                                 ╵
  ╵                                  │                                                                 ╵
  ╵                                  ▼                                                                 ╵
  ╵                                ┌──────────────────────────────┐                                    ╵
  ╵                                │                              │                                    ╵
  ╵                                │         other error          │                                    ╵
  ╵                                │       → abort rollback       │                                    ╵
  ╵                                │                              │                                    ╵
  ╵                                └──────────────────────────────┘                                    ╵
  ╵                                                                                                    ╵
  ╵╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴┘
```

The iteration skips two categories before reaching `dispatchReverse`:
- **`op.Reversed != nil`** — already reversed in a prior rollback attempt
  (idempotent retry after rollback crash)
- **`op.Started == nil`** — never started, nothing to reverse

Rollback calls the **same domain-level reverse operations** used
interactively (`DeleteVLAN`, `RemoveService`, `UnbindIPVPN`, etc.).
No separate registry or parallel logic. Each reverse operation is
existence-checking and reference-aware — safe to call on partial
state.

---

## Error Classification

Reverse operations fail for two fundamentally different reasons,
and rollback responds differently to each:

```
                          ┌──────────────────────────┐
                          │        reverse op        │
                          │      returns error       │
                          └──────────────────────────┘
                     ╱                                  ╲
                    ╱                                    ╲
                   ▼                                      ▼
┌──────────────────────────┐          ┌────────────────────────┐
│    PreconditionError     │          │      domain error      │
│ (resource doesn't exist) │          │ (has active consumers) │
└──────────────────────────┘          └────────────────────────┘
  │                                     │
  ▼                                     ▼
┌──────────────────────────┐          ┌────────────────────────┐
│      rollback: SKIP      │          │    rollback: ABORT     │
│      forward never       │          │   unexpected state,    │
│        created it        │          │ operator investigates  │
└──────────────────────────┘          └────────────────────────┘
```

- **"Doesn't exist"** → `PreconditionError`. The forward operation
  never created the resource (partial apply) or it was already cleaned
  up. Rollback skips it — nothing to undo.

- **"Has active consumers"** → plain error. The resource exists but
  has dependencies from outside this commit (another service bound to
  the VRF, interfaces still attached). Something unexpected happened.
  Rollback aborts; the operator investigates.

This classification is enforced at the source — the precondition
checker, `GetInterface`, and every "resource not found" check return
`PreconditionError`. Domain safety checks ("VRF has bound interfaces")
return plain errors. Rollback doesn't classify errors; it responds to
the classification the operations already provide.

---

## Interaction with Existing Mechanisms

**NEWTRON_INTENT (service records):** The unified intent model merges
service binding and crash recovery into a single record per resource.
The intent record serves both purposes: it is the domain-level teardown
manifest (persistent, part of service lifecycle) and the operation-level
crash recovery record. `ApplyService` writes a single `NEWTRON_INTENT`
entry that both protects against process crash during the multi-entry
write and enables future `RemoveService`.

During recovery, the intent record
(`operation: "apply-service"`, `resource: "Ethernet0"`) gives the
recovery tool enough information to call `RemoveService` on the right
interface. `RemoveService` reads the same intent record to discover what
infrastructure was applied and tears it down.

**Composite delivery (MULTI/EXEC):** No intent needed. PipelineSet is
atomic. If EXEC fails, CONFIG_DB is unchanged.

**NEWTRON_LOCK:** Both live in STATE_DB. The lock has a Redis EXPIRE
of 1 hour; the intent has no EXPIRE — it persists until explicitly
deleted. When a process crashes, the lock expires first, allowing a
new process to acquire it. The intent is still there when the new
process checks for zombies — guaranteed, because it never expires
silently.

---

## CLI

```
newtron -D leaf1 device zombie                # inspect (no lock)
newtron -D leaf1 device zombie rollback       # preview rollback (dry-run)
newtron -D leaf1 device zombie rollback -x    # execute rollback (fresh lock)
newtron -D leaf1 device zombie clear          # dismiss without rollback
```

---

## Forward ↔ Reverse Operation Map

Every forward operation declares its reverse and captures the
parameters needed for dispatch:

| Forward | Reverse | Params |
|---------|---------|--------|
| `CreateVLAN` | `device.delete-vlan` | `vlan_id` |
| `AddVLANMember` | `device.remove-vlan-member` | `vlan_id`, `interface` |
| `ConfigureSVI` | `device.remove-svi` | `vlan_id` |
| `CreateVRF` | `device.delete-vrf` | `vrf` |
| `BindIPVPN` | `device.unbind-ipvpn` | `vrf` |
| `AddStaticRoute` | `device.remove-static-route` | `vrf`, `prefix` |
| `ConfigureBGP` | `device.remove-bgp-globals` | — |
| `SetupEVPN` | `device.teardown-evpn` | — |
| `MapL2VNI` | `device.unmap-l2vni` | `vlan_id` |
| `CreateACLTable` | `device.delete-acl-table` | `name` |
| `AddACLRule` | `device.delete-acl-rule` | `table_name`, `rule_name` |
| `CreatePortChannel` | `device.delete-portchannel` | `name` |
| `AddPortChannelMember` | `device.remove-portchannel-member` | `name`, `member` |
| `AddBGPNeighbor` | `device.remove-bgp-neighbor` | `neighbor_ip` |
| `ConfigureLoopback` | `device.remove-loopback` | — |
| `ApplyService` | `interface.remove-service` | `interface` |
| `ApplyQoS` | `interface.remove-qos` | `interface` |
| `BindACL` | `interface.unbind-acl` | `interface`, `acl_name` |
| `BindMACVPN` | `interface.unbind-macvpn` | `interface` |

Operations with no params (`—`) tear down by scanning CONFIG_DB —
the reverse operation finds everything it needs from device reality.
