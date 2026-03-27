# Eliminate `accumulated` — Export from ConfigDB

Implementation plan for completing the unified pipeline by removing the parallel
`accumulated` bookkeeping mechanism from Node.

**Prerequisite**: Unified Pipeline + Intent-Idempotent Primitives (completed).
See `docs/newtron/unified-pipeline-tracker.md`.

---

## Context

The unified pipeline achieved "same code path, different initialization"
for config entries. But a structural misalignment remains: the Node maintains
**two parallel bookkeeping mechanisms** for the same state:

1. **`configDB`** — typed ConfigDB struct, always updated by every operation
2. **`accumulated`** — `[]sonic.Entry` slice, appended to by 4 separate code paths

`accumulated` is a redundant shadow of configDB. Every code path that updates
configDB must ALSO append to `accumulated`, and the two mechanisms don't stay
synchronized:

| Accumulation site | configDB update | Accumulates | Handles deletes |
|---|---|---|---|
| `applyShadow(cs)` | yes | yes | yes |
| `applyIntentToShadow(entry)` | yes | yes | no |
| `RegisterPort(name, fields)` | yes | yes | no |
| `AddEntries(entries)` | yes | yes | no |
| `deleteIntent` (own record) | yes | **no** | yes |

When composites call primitives, both the primitive's accumulation AND the
composite's `applyShadow(mergedCS)` fire — double-accumulation. This works
only because `CompositeBuilder.AddEntry` is last-write-wins. But it's two
bookkeeping systems that must stay synchronized, and they already don't:
`deleteIntent`'s own-record path updates configDB but never accumulates.

**Root cause**: `accumulated` exists because `BuildComposite` needs `[]Entry`
but ConfigDB stores typed structs. The fix is to give ConfigDB an export method.

## Principle

**The configDB IS the accumulated state.** In abstract mode, configDB starts
empty and operations build it up. At any point, configDB reflects the complete
desired state. `accumulated` is a redundant copy. Delete it.

## Design

### Add `ConfigDB.ExportEntries()` — the inverse of `ApplyEntries`

`ApplyEntries` deserializes `[]Entry` → typed struct fields.
`ExportEntries` serializes typed struct fields → `[]Entry`.

All 28 typed entry structs have only `string` fields with `json:"field_name"`
tags. A single reflection-based helper handles all of them:

```go
// structToFields converts a typed struct to map[string]string using json tags.
// Zero-value fields are omitted — matching the behavior of config functions
// that only set fields they care about.
func structToFields(v any) map[string]string {
    val := reflect.ValueOf(v)
    typ := val.Type()
    fields := make(map[string]string)
    for i := 0; i < typ.NumField(); i++ {
        tag := typ.Field(i).Tag.Get("json")
        if tag == "" || tag == "-" {
            continue
        }
        name, _, _ := strings.Cut(tag, ",")
        if s := val.Field(i).String(); s != "" {
            fields[name] = s
        }
    }
    return fields
}
```

`ExportEntries` uses a switch mirroring `applyEntry`, iterating each typed
map and calling `structToFields` on each value. Raw `map[string]string` tables
(DEVICE_METADATA, NEWTRON_INTENT, etc.) are exported directly.

### Update `BuildComposite` to read from configDB

```go
func (n *Node) BuildComposite() *CompositeConfig {
    cb := NewCompositeBuilder(n.name, CompositeOverwrite).
        SetGeneratedBy("abstract-node")
    cb.AddEntries(n.configDB.ExportEntries())
    return cb.Build()
}
```

### Delete `accumulated` and all accumulation logic

Remove the field from Node. Remove the `if n.offline { n.accumulated = ... }`
blocks from every site.

## `applyEntry` field coverage — not a problem

`applyEntry` stores a subset of each typed struct's fields (e.g., BGP_GLOBALS
stores 2 of 11 fields). This looks lossy, but for the abstract node path it's
safe: the only entries that enter the abstract configDB come from newtron's own
config functions, which only set the fields that `applyEntry` handles. The
round-trip is: config function produces `{local_asn, router_id}` → `applyEntry`
stores `{local_asn, router_id}` → `ExportEntries` reads `{local_asn, router_id}`.
No field loss.

Fixing `applyEntry` to store all struct fields (by delegating to `tableParsers`)
is a valid separate cleanup — it would close the latent bug for physical nodes —
but it is out of scope here.

## Files Changed

| File | Change |
|------|--------|
| `pkg/newtron/device/sonic/configdb.go` | Add `ExportEntries()`, add `structToFields()` |
| `pkg/newtron/device/sonic/configdb_test.go` | Add `TestExportEntries_RoundTrip` |
| `pkg/newtron/network/node/node.go` | Delete `accumulated` field; update `BuildComposite`; remove accumulation in `applyShadow`, `RegisterPort`; delete `AddEntries` |
| `pkg/newtron/network/node/intent_ops.go` | Simplify `applyIntentToShadow` (just configDB update) |

## Implementation

### S1. `structToFields` helper (`configdb.go`)

Reflection-based, using json tags. Handles all 28 typed structs with one function.
Zero-value fields omitted — matches config function behavior.

### S2. `ExportEntries` method (`configdb.go`)

Switch statement mirroring `applyEntry` — one case per table. Each case iterates
the typed map, calls `structToFields`, and appends an Entry. Raw tables iterate
the map directly and copy fields.

```go
func (db *ConfigDB) ExportEntries() []Entry {
    var entries []Entry

    // Typed tables
    for key, val := range db.Port {
        if f := structToFields(val); len(f) > 0 {
            entries = append(entries, Entry{Table: "PORT", Key: key, Fields: f})
        }
    }
    for key, val := range db.VLAN {
        if f := structToFields(val); len(f) > 0 {
            entries = append(entries, Entry{Table: "VLAN", Key: key, Fields: f})
        }
    }
    // ... all 28 typed tables ...

    // Raw tables
    for key, fields := range db.DeviceMetadata {
        if len(fields) > 0 {
            entries = append(entries, Entry{Table: "DEVICE_METADATA", Key: key, Fields: copyFields(fields)})
        }
    }
    // ... all raw tables ...

    return entries
}
```

### S3. Update `BuildComposite` (`node.go`)

Replace `n.accumulated` iteration with `n.configDB.ExportEntries()`.

### S4. Delete `accumulated` (`node.go`)

- Remove `accumulated []sonic.Entry` from Node struct
- Remove `if n.offline { n.accumulated = append(...) }` from `applyShadow`
- Remove `if n.offline { n.accumulated = append(...) }` from `RegisterPort`
- Delete `AddEntries` method (zero external callers)

### S5. Simplify `applyIntentToShadow` (`intent_ops.go`)

```go
func (n *Node) applyIntentToShadow(entry sonic.Entry) {
    n.configDB.ApplyEntries([]sonic.Entry{entry})
}
```

### S6. Tests (`configdb_test.go` or `configdb_parsers_test.go`)

**`TestExportEntries_RoundTrip`**: For each table, create entries via config
functions, apply via `ApplyEntries`, export via `ExportEntries`, verify fields
match. This is the critical correctness test — ensures no field is lost or
invented in the round-trip.

**`TestBuildComposite_NoAccumulated`**: Create an abstract Node, run operations,
verify `BuildComposite` output contains expected tables/keys.

## Verification

1. `go build ./... && go vet ./...`
2. `go test ./... -count=1` — all previously passing tests still pass
3. `grep -rn 'accumulated' pkg/newtron/network/node/` — zero hits
4. `grep -rn 'applyIntentToShadow' pkg/newtron/network/node/intent_ops.go` — single-line body

## Resolved Concerns

| Concern | Resolution |
|---------|------------|
| `applyEntry` field loss | Not a problem: abstract nodes only contain fields that config functions produce, which are the same fields `applyEntry` stores. |
| `ExportEntries` on physical nodes | Out of scope — `ExportEntries` is only called from `BuildComposite` on abstract nodes. |
| Delete entries in composite | Abstract nodes build desired state from empty — no deletes in the normal provisioning flow. |
| Map iteration order | `CompositeBuilder.AddEntry` uses last-write-wins merging; order doesn't matter. |
| `AddEntries` callers | Zero external callers (verified by grep). Safe to delete. |
