# Cluster C — Topology spec substrate

**Status:** scoped, pending implementation. Part of the
[newtcon-driven gaps batch](newtcon-driven-gaps.md); see the parent
doc for the unifying §46 principle, style invariants, and
cross-cluster phasing.

## Scope

Three issues, all instances of §46 applied to newtron's **topology
spec substrate** — the `topology.json` contents that define the
network's structural shape (devices and links). Per
`DESIGN_PRINCIPLES_NEWTRON.md` §7, topology is a network-scoped
definition newtron owns.

- [newtron#14](https://github.com/aldrin-isaac/newtron/issues/14) — Full topology read endpoint (`GET /network/{netID}/topology`)
- [newtron#15](https://github.com/aldrin-isaac/newtron/issues/15) — Topology node CRUD (`create-node`, `delete-node`, `update-node`)
- [newtron#16](https://github.com/aldrin-isaac/newtron/issues/16) — Topology link CRUD (`create-link`, `delete-link`)

## Shared load-bearing primitives

All three issues share the same internal primitives — most
already present in newtron:

- **`spec.TopologySpecFile`** (in `pkg/newtron/spec/types.go`) — the
  canonical top-level type: Version, Platform, Description, Devices
  map, Links slice, NewtLab config. Fully JSON-tagged. #14 exposes
  it directly; #15/#16 mutate its `Devices` map and `Links` slice.
- **`spec.TopologyDevice`** and **`spec.TopologyLink`** — nested
  types. #15 takes `TopologyDevice` as body; #16 takes
  `TopologyLink`.
- **`spec.Loader.SaveTopology(spec *TopologySpecFile) error`** —
  **already exists** with atomic temp-file + rename semantics
  (`pkg/newtron/spec/loader.go` line 484). #15 and #16 share this
  exact call site for persistence; #14 doesn't need it (read-only).
- **`spec.Loader.validateTopology()`** — already exists; validates
  profile references and link endpoints. #15 and #16 reuse this for
  pre-persist validation; #14 doesn't need it.
- **`Network.GetTopology()`** — already exists and returns the
  typed substrate. #14 wraps it; #15/#16 mutate via new sibling
  methods.

**Verdict on the underlying primitives:** the spec-layer write
machinery already exists. The gap is purely the Network-method
layer (new `AddTopologyDevice`, `DeleteTopologyDevice`,
`UpdateTopologyDevice`, `AddTopologyLink`, `DeleteTopologyLink`)
plus the HTTP handlers and routes.

## Implementation order (within this cluster)

1. **#14 first** — trivial. One handler, one route. Standalone.
   Logically precedes write CRUD (operators want to read what they
   write), though mechanically independent.
2. **#15 + #16 together** — moderate; one PR. They share the
   `Loader.SaveTopology` call site, the same Network method-layer
   pattern, and the same validation hook. Splitting them across two
   PRs would create churn in identical files.

## Infrastructure consequence (informational, not in scope here)

Topology edits have effects outside newtron's spec layer:

- In **lab mode**, newtlab must re-deploy the affected device(s)
  after a `topology.json` change (start/stop VMs, reconfigure
  bridges).
- In **production**, the operator performs the physical
  rack-and-cable step (or removes a switch from service).

newtron does not invoke newtlab and does not perform physical
operations. Topology CRUD endpoints persist the spec and rebuild
in-memory Network state; downstream coordination is the consumer's
concern (newtcon will surface the "run `newtlab deploy` to reflect
this change" guidance to the operator).

---

## 1. `newtron#14` — Full topology read endpoint

_Landed on branch `impl/phase-1-newtron-substrate-gaps` (Phase 1 batch)._

### Principle check

**§46 (load-bearing):** the `TopologySpecFile` is canonical
substrate that the spec loader already builds in memory. Today's
`GET /topology/node` returns device names only (`[]string`) — the
"summary instead of canonical" pattern §46 explicitly rejects.
Exposing the full typed substrate directly is the resolution.

**§7 supports:** topology is a network-scoped definition newtron
owns; a typed-read endpoint is the minimum substrate-visibility for
that definition.

### Implementation

**Network method** — `Network.GetTopology()` already exists and
returns `*spec.TopologySpecFile`. No new method needed.

**HTTP handler** in `pkg/newtron/api/handler_network.go`, alongside
the existing `handleTopologyDeviceNames`:

```go
func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
    na := s.requireNetwork(w, r)
    if na == nil {
        return
    }
    val, err := na.do(r.Context(), func() (any, error) {
        topo := na.net.GetTopology()
        if topo == nil {
            return nil, &newtron.NotFoundError{Resource: "topology", Name: ""}
        }
        return topo, nil
    })
    if err != nil {
        writeError(w, err)
        return
    }
    writeJSON(w, http.StatusOK, val)
}
```

Returns `*spec.TopologySpecFile` with existing JSON tags (devices,
links, metadata).

**Route** in `pkg/newtron/api/handler.go`, alongside the existing
`/topology/node` route:

```go
mux.HandleFunc("GET /network/{netID}/topology", s.handleTopology)
```

**Tests:** assert round-trip JSON shape matches `TopologySpecFile`;
404 with `Error.kind="not_found"` when `HasTopology()` is false.

**Estimated effort:** trivial. One handler, one route. Reuses
existing `GetTopology()`.

---

## 2. `newtron#15` — Topology node CRUD

### Principle check

**§46 (load-bearing):** the typed `TopologyDevice` is canonical
substrate. Today, mutating it requires a YAML hand-edit + `reload`
— the "no typed verb for an existing substrate" pattern §46
rejects via rule 1 ("canonical first").

**§7 supports:** topology nodes are network-scoped definitions; the
existing verb pattern (`create-service`, `delete-profile`) extends
naturally to topology.

**§16 (verb vocabulary):** `create-node`, `delete-node`,
`update-node` fit the existing `verb-noun` form newtron uses
throughout.

### Implementation

**New Network methods** in `pkg/newtron/network/network.go`,
mirroring the existing `SaveProfile`/`SaveZone`/`SaveService`
pattern:

```go
// AddTopologyDevice creates a device entry in the topology spec.
// Returns ConflictError if a device with this name already exists.
// Validates against existing validateTopology rules (profile ref).
// Persists atomically via spec.Loader.SaveTopology.
func (n *Network) AddTopologyDevice(name string, device *spec.TopologyDevice) error

// DeleteTopologyDevice removes a device from the topology spec.
// Returns NotFoundError if no device with this name exists.
// Returns ConflictError if any link still references the device
// (operator must delete the referring links first, or call with
// force=true to cascade — see open-question note below).
func (n *Network) DeleteTopologyDevice(name string, force bool) error

// UpdateTopologyDevice replaces a device entry. Same validation
// as Add; same persistence path.
func (n *Network) UpdateTopologyDevice(name string, device *spec.TopologyDevice) error
```

Each calls `loader.SaveTopology(spec)` after mutation; failure
unwinds the in-memory mutation before returning.

**HTTP handlers** in `pkg/newtron/api/handler_network.go`,
mirroring the `handleCreateService` / `handleDeleteService` /
etc. patterns:

```go
func (s *Server) handleCreateTopologyNode(w http.ResponseWriter, r *http.Request) {
    // parse netID, decode body { name, device },
    // call na.net.AddTopologyDevice, return device or error
}
func (s *Server) handleDeleteTopologyNode(w http.ResponseWriter, r *http.Request) {
    // parse netID, name, force query param,
    // call na.net.DeleteTopologyDevice, return {"deleted": name}
}
func (s *Server) handleUpdateTopologyNode(w http.ResponseWriter, r *http.Request) {
    // parse netID, name, decode body,
    // call na.net.UpdateTopologyDevice, return device
}
```

**Routes** in `pkg/newtron/api/handler.go`:

```go
mux.HandleFunc("POST /network/{netID}/topology/create-node", s.handleCreateTopologyNode)
mux.HandleFunc("DELETE /network/{netID}/topology/node/{name}", s.handleDeleteTopologyNode)
mux.HandleFunc("PUT /network/{netID}/topology/node/{name}", s.handleUpdateTopologyNode)
```

**Request types** in `pkg/newtron/api/types.go`:

```go
type TopologyNodeCreateRequest struct {
    Name   string                `json:"name"`
    Device *spec.TopologyDevice  `json:"device"`
}
```

`Update` takes a `*spec.TopologyDevice` body directly (the name is
in the URL path).

**Tests:** round-trip create+read; duplicate-name → 409; deletion
of name referenced by a link → 409 (unless `?force=true`);
validation failure (unknown profile reference) → 400 with
substrate-level rejection reason; in-memory state updated post-CRUD
without requiring `reload`.

**Estimated effort:** moderate. Persistence (`SaveTopology`) and
validation (`validateTopology`) already exist. Gap is the
Network-method layer + handlers. Implement together with #16
(same PR).

**Open question:** `?force=true` on `delete-node` to cascade-delete
referring links — defer to Architect during implementation; not
filing a separate gap for it.

---

## 3. `newtron#16` — Topology link CRUD

### Principle check

Same as #15: §46 (canonical `TopologyLink` substrate exposed
directly), §7 (network-scoped definition), §16 (verb vocabulary).

### Implementation

**New Network methods**, mirroring #15:

```go
// AddTopologyLink adds a link to the topology spec.
// Returns ConflictError if an equivalent link (unordered {A,Z})
// already exists. Validates endpoints (both devices must exist;
// both interfaces must be declared on their respective devices).
func (n *Network) AddTopologyLink(link *spec.TopologyLink) error

// DeleteTopologyLink removes a link from the topology spec. Match
// is unordered: {a:X, z:Y} matches {a:Y, z:X}. Returns
// NotFoundError if no matching link.
func (n *Network) DeleteTopologyLink(link *spec.TopologyLink) error
```

Both invoke `loader.SaveTopology(spec)` after mutation.

**HTTP handlers** in `handler_network.go`:

```go
func (s *Server) handleCreateTopologyLink(w http.ResponseWriter, r *http.Request) {
    // decode body as *spec.TopologyLink, call AddTopologyLink, return link
}
func (s *Server) handleDeleteTopologyLink(w http.ResponseWriter, r *http.Request) {
    // decode body as *spec.TopologyLink, call DeleteTopologyLink, return {"deleted": link}
}
```

**Routes:**

```go
mux.HandleFunc("POST /network/{netID}/topology/create-link", s.handleCreateTopologyLink)
mux.HandleFunc("DELETE /network/{netID}/topology/link", s.handleDeleteTopologyLink)
```

`DELETE` takes the body convention (avoids URL-escaping
`device:interface` strings). Alternative path-param form is noted
in the issue body as an open question for the Architect.

**Tests:** create + read; duplicate detection on A/Z swap; deletion
matches unordered pair; validation failures with substrate-level
rejection reasons (unknown device, undeclared interface).

**Estimated effort:** moderate. Same effort profile as #15; one PR
for both.
