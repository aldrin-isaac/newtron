# Fragile Patterns — Queued for Resolution

Patterns in the codebase that are architecturally correct but structurally fragile.
Each pattern works today but could silently break with routine changes.

---

## FP-1: struct field triplication — RESOLVED

**What**: Every CONFIG_DB typed struct field previously had to exist in three places:
1. The struct definition (`BGPGlobalsEntry`, `BGPPeerGroupEntry`, etc.)
2. The `applyEntry` case in `configdb.go` (shadow ConfigDB path for abstract nodes)
3. The `tableParsers` entry in `configdb_parsers.go` (Redis read path for physical nodes)

**How it broke**: BGP_GLOBALS had 11 struct fields but `applyEntry` only mapped 2.
BGP_PEER_GROUP was missing `ebgp_multihop`, BGP_PEER_GROUP_AF was missing
`nexthop_unchanged`. Fields were silently dropped through the abstract node round-trip.

**Resolution**: `applyEntry` was eliminated. Both paths (physical node Redis reads
and abstract node shadow writes) now use a single `configTableHydrators` registry
in `configdb_parsers.go`. The triplication is reduced to a duplication: struct
definition + one hydrator entry. Reflection-based tests in
`configdb_consistency_test.go` verify that every struct field survives the
hydrate → ExportEntries round-trip:
- `TestHydrateExportRoundTrip_AllTypedTables` — all 28 typed tables
- `TestConfigTableHydrators_CoversAllTypedTables` — registry completeness
- `TestDeleteEntry_CoversAllHydratedTables` — delete coverage

---

## FP-2: BGP_GLOBALS stomping by multiple writers

**What**: Multiple operations write to `BGP_GLOBALS|default`:
- `ConfigureBGP` (bgp_ops.go) — writes with extra fields (ebgp_requires_policy, etc.)
- `SetupVTEP` (evpn_ops.go) — was writing with nil extra fields, stomping the richer entry
- `ConfigureRouteReflector` (evpn_ops.go) — writes with its own extra fields
- `ApplyService` auto-ensure (service_ops.go) — writes with extra fields

Since the hydrator does full struct replacement (not field merge), the last writer
wins. If a later writer passes fewer fields, the earlier writer's fields are lost.

**How it broke**: `SetupDevice` calls `ConfigureBGP` (5 fields) then `SetupVTEP`
(2 fields). SetupVTEP's `CreateBGPGlobalsConfig("default", ..., nil)` replaced
the entry, losing `ebgp_requires_policy`, `suppress_fib_pending`, `log_neighbor_changes`.

**Current fix**: `SetupVTEP` now checks `if _, exists := n.configDB.BGPGlobals["default"]`
before writing. This prevents the stomp in the normal flow.

**Risk**: Any new operation that writes BGP_GLOBALS|default without checking first
will re-introduce the bug. The pattern of "guard before write" is ad-hoc and easy to forget.

**Resolution options**:
1. Make the typed table hydrator do field-level merge instead of full replacement
   (matching how Redis HSET works). This is the principled fix — shadow ConfigDB
   should behave like Redis. Already done for DEVICE_METADATA.
2. Make `CreateBGPGlobalsConfig` always include the standard extra fields, so every
   caller writes the complete set. This is simpler but couples all callers.
3. Single-owner enforcement: only `ConfigureBGP` writes BGP_GLOBALS|default.
   Other operations check existence and skip. (This is the current ad-hoc approach.)

---

## FP-3: Schema knows fields that structs/hydrators don't — RESOLVED

**What**: `schema.go` defines validation constraints for fields that the typed structs
and hydrators may not map. Schema validation passes (the field name is known), but the
field is silently dropped when it flows through hydrate → ExportEntries.

**Resolution**: The reflection tests from FP-1 now verify that every struct field
round-trips through the hydrator. The remaining gap (schema fields not in the struct)
is caught by `TestHydrators_AllTablesRegistered` which ensures every ConfigDB struct
field has a hydrator. Adding a schema field without a struct field is now a two-step
fix: add to struct, hydrator picks it up automatically.

---

## FP-4: BGP_PEER_GROUP_AF missing fields vs BGP_NEIGHBOR_AF

**What**: `BGP_PEER_GROUP_AF` and `BGP_NEIGHBOR_AF` have similar field sets (both
are per-AF BGP configuration), but the peer group AF struct had fewer fields.
`nexthop_unchanged` was in BGP_NEIGHBOR_AF but not in BGP_PEER_GROUP_AF.

**Risk**: When SONiC inherits peer group attributes to neighbors, the inherited
fields need to exist in both structs. Missing a field on the peer group side means
it works for per-neighbor config but silently disappears for peer-group config.

**Resolution**: Audit all paired table types (NEIGHBOR/PEER_GROUP, TABLE/RULE) to
ensure their field sets are consistent where SONiC expects inheritance.
