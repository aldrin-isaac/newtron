# Cluster B — ChangeSet substrate

**Status:** scoped, pending implementation. Part of the
[newtcon-driven gaps batch](newtcon-driven-gaps.md); see the parent
doc for the unifying §46 principle, style invariants, and
cross-cluster phasing.

## Scope

Two issues, both instances of §46 applied to newtron's **ChangeSet
substrate** — the canonical typed delta vocabulary newtron uses
internally to drive Redis writes and verify them
(`DESIGN_PRINCIPLES_NEWTRON.md` §11):

- [newtron#11](https://github.com/aldrin-isaac/newtron/issues/11) — Structured `ChangeSet` in `WriteResult`
- [newtron#12](https://github.com/aldrin-isaac/newtron/issues/12) — Call-site provenance on ChangeSet entries (verbose mode)

## Shared load-bearing primitives

- **`sonic.ConfigChange`** (in `pkg/newtron/device/sonic/types.go`)
  — the canonical typed entry: Table, Key, Type (add/modify/delete),
  Fields. Already JSON-tagged. #11 exposes this type directly in
  HTTP responses; #12 adds a `Source` field to it (captured at
  emission time, serialized via verbose mode only).
- **`ChangeSet.Changes []Change`** (in
  `pkg/newtron/network/node/changeset.go`, aliased to
  `[]sonic.ConfigChange`) — the universal contract per §11. #11
  exposes it via `WriteResult.Changes`; #12 enriches each entry
  with generation provenance.
- **All `WriteResult`-construction call sites** — every handler that
  runs a ChangeSet builds a `WriteResult`. Both #11 and #12 touch
  this same set of sites. Implementing them sequentially in one
  PR pair minimizes call-site churn.

## Implementation order (within this cluster)

1. **#11 first** — trivial, additive. Adds `Changes []sonic.ConfigChange`
   field to `WriteResult`. Populated from `cs.Changes` at every
   construction site.
2. **#12 second** — moderate. Builds on #11 (verbose response shape
   is *Changes with Source*; default is Changes alone). Requires the
   operator decision on §33 reconciliation (verbose-mode opt-in) to
   be accepted before implementation.

---

## 1. `newtron#11` — Structured `ChangeSet` in `WriteResult`

### Principle check

**§46 (load-bearing):** `WriteResult.Preview` is a derivative string
rendering of `ChangeSet.Changes`. §46 requires the canonical form
(typed `Changes` array) alongside; this change adds it. The
summary `Preview` is retained for CLI rendering — exactly the
"additive evolution" model §46 prescribes.

**§11 supports:** ChangeSet is the Universal Contract; serializing
`Changes` directly gives consumers the same single-representation
substrate newtron uses internally for write and verify. No parallel
format invented; no internal type leaked.

### Implementation

**File:** `pkg/newtron/types.go` (existing home of `WriteResult`).

**Change:** add `Changes` field to existing `WriteResult`:

```go
// WriteResult wraps the outcome of a configuration write operation.
type WriteResult struct {
    Preview      string               `json:"preview,omitempty"`
    Changes      []sonic.ConfigChange `json:"changes,omitempty"` // NEW
    ChangeCount  int                  `json:"change_count"`
    Applied      bool                 `json:"applied"`
    Verified     bool                 `json:"verified"`
    Saved        bool                 `json:"saved"`
    Verification *VerificationResult  `json:"verification,omitempty"`
}
```

`sonic.ConfigChange` is in `pkg/newtron/device/sonic/types.go` and
is already JSON-tagged (`json:"table"`, `json:"key"`, `json:"type"`,
`json:"fields,omitempty"`). No new types needed.

**Callers:** every site that constructs a `WriteResult` after
running a `ChangeSet`. Search `WriteResult{` across
`pkg/newtron/api/` (and any non-API constructor sites). For each:

```go
result := WriteResult{
    Preview:     cs.Preview(),       // existing
    Changes:     cs.Changes,         // NEW — direct assignment; cs.Changes is []Change aliased to []sonic.ConfigChange
    ChangeCount: len(cs.Changes),    // existing
    // ... rest unchanged
}
```

**Tests:** extend `pkg/newtron/api/api_test.go` to assert `Changes`
is populated and matches the per-table-per-key-per-field shape on at
least one apply path (e.g., `POST /network/{n}/node/{d}/vlan`
create-vlan, which has well-known ChangeSet output).

**Estimated effort:** single PR. One field, one assignment per
construction site, one test.

---

## 2. `newtron#12` — Call-site provenance on ChangeSet entries (verbose mode)

### Principle check

**§46 (load-bearing, with caveat):** `Source` is the canonical
provenance substrate newtron captures at emission time but does not
expose. §46 says to expose canonical substrate; this endpoint does
so. **The caveat** is §33: exposing internal-package method names
in HTTP responses puts internal Go symbols in the public surface.
§46 and §33 must be reconciled here — they do so via opt-in
verbose mode.

**§33 reconciliation:** the default response shape does NOT include
`Source` (preserving §33's internal/public boundary). Verbose
responses do (honoring §46's substrate exposure). The `json:"-"`
tag on `ConfigChange.Source` is the load-bearing line for the
reconciliation; the separate `VerboseConfigChange` type is the
explicit opt-in surface.

This reconciliation is consistent with both principles **if and
only if** the verbose-mode opt-in is preserved. A future change
that promotes `Source` into default responses would re-violate §33
and must be rejected at that time.

**§1, §20, §27, §13, §14, §15 unaffected:** `Source` is
generation-time metadata about the emitting Go method, not a new
representation alongside intent and reality, not a CONFIG_DB write,
not part of YANG schema, not stored on the device.

This issue requires explicit operator acceptance of the verbose-mode
resolution before implementation.

### Implementation

**New file** `pkg/newtron/network/node/source.go`:

```go
package node

import (
    "fmt"
    "runtime"
    "strings"
)

// callerSite returns "pkg/newtron/.../file.go:line FuncName" for the
// call site at the given depth. Used at ChangeSet.add() time to attach
// generation provenance to each ConfigChange. The captured Source is
// exposed via verbose-mode HTTP responses only — see api.md §verbose.
func callerSite(skip int) string {
    pc, file, line, ok := runtime.Caller(skip)
    if !ok {
        return ""
    }
    file = trimModulePath(file)
    name := ""
    if fn := runtime.FuncForPC(pc); fn != nil {
        name = trimModulePath(fn.Name())
    }
    return fmt.Sprintf("%s:%d %s", file, line, name)
}

const modulePath = "github.com/aldrin-isaac/newtron/"

func trimModulePath(s string) string {
    return strings.TrimPrefix(s, modulePath)
}
```

**Field on `sonic.ConfigChange`** in
`pkg/newtron/device/sonic/types.go`:

```go
type ConfigChange struct {
    Table  string            `json:"table"`
    Key    string            `json:"key"`
    Type   ChangeType        `json:"type"`
    Fields map[string]string `json:"fields,omitempty"`
    Source string            `json:"-"`  // captured at emission; serialized via verbose-mode view only
}
```

The `json:"-"` tag is the boundary keeper — default JSON output
omits `Source` regardless of value. This is the load-bearing line
for principle consistency; preserve it.

**Capture site** in `pkg/newtron/network/node/changeset.go`:

```go
// add appends a change of any type (internal use by buildChangeSet, op).
// Captures the call site at depth 3 (callerSite + add + Add/Update/Delete),
// so Source lands at the *_ops.go method that emitted the change.
func (cs *ChangeSet) add(table, key string, changeType sonic.ChangeType, fields map[string]string) {
    cs.Changes = append(cs.Changes, Change{
        Table:  table,
        Key:    key,
        Type:   changeType,
        Fields: fields,
        Source: callerSite(3),
    })
}
```

**Verbose view type** in `pkg/newtron/types.go`:

```go
// VerboseConfigChange is a ConfigChange with Source serialized. Returned
// only when the caller has explicitly requested verbose mode
// (?verbose=true). The default response shape (sonic.ConfigChange with
// Source tagged json:"-") preserves the public/internal API boundary;
// the verbose shape opts into exposing newtron-internal call sites for
// operator debugging and PR-quality bug reports.
type VerboseConfigChange struct {
    Table  string            `json:"table"`
    Key    string            `json:"key"`
    Type   sonic.ChangeType  `json:"type"`
    Fields map[string]string `json:"fields,omitempty"`
    Source string            `json:"source,omitempty"`
}

func toVerboseChanges(in []sonic.ConfigChange) []VerboseConfigChange {
    out := make([]VerboseConfigChange, len(in))
    for i, c := range in {
        out[i] = VerboseConfigChange{
            Table: c.Table, Key: c.Key, Type: c.Type, Fields: c.Fields, Source: c.Source,
        }
    }
    return out
}
```

**Handler integration:** every handler that emits
`WriteResult.Changes` checks `r.URL.Query().Get("verbose") == "true"`
and either returns the default shape or substitutes
`[]VerboseConfigChange` for the `Changes` field. Cleaner
alternative: a wrapper `VerboseWriteResult` returned only on
verbose request. Pick during implementation.

**Tests:** verify default response (`?verbose=false` or absent)
does NOT contain `"source"` in the JSON body (grep-based assertion);
verify `?verbose=true` does; verify the captured `Source` points at
the emitting `_ops.go` file (e.g.,
`pkg/newtron/network/node/vlan_ops.go` for a VLAN-create operation).

**Estimated effort:** single PR. Capture mechanism is ~30 lines;
default-vs-verbose response shaping touches every handler that
returns `WriteResult.Changes` (the same set updated by #11).
