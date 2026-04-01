# Newtrun Test Suite Fixes — March 2026

Running log of all fixes applied during comprehensive test suite validation.
Each fix includes rationale, design conformance attestation, and reference to
the authoritative intent DAG hierarchy (`intents.md`).

**Governing documents**: `ai-instructions.md`, `intents.md`,
`DESIGN_PRINCIPLES.md`, `DESIGN_PRINCIPLES_NEWTRON.md`, `CLAUDE.md`

---

## Fix 1: 1node-vs-basic missing setup-device scenario

**Suite**: 1node-vs-basic
**Symptom**: All operations fail with `parent "device" does not exist`
**Root cause**: The suite went straight from boot-ssh to apply-service/create-vlan
without calling setup-device, which creates the `device` root intent (row 1).
**Fix**:
- Added `01-setup-device.yaml` with setup-device call and intent verification
- Renumbered existing scenarios: 01→02, 02→03, 03→04
- Updated `requires` in service-lifecycle and vlan-vrf from `[boot-ssh]` to `[setup-device]`
**Design conformance**: The `device` intent is the DAG root (intents.md row 1).
All resource intents declare `[device]` as parent. I4 (parent must exist on creation) requires
setup-device to run first. This is a test suite fix, not a code change.
**Result**: 1node-vs-basic 5/5 PASS

---

## Fix 2: interface-props, acl-lifecycle, qos-lifecycle — sub-resource ops on unconfigured interfaces

**Suite**: 2node-vs-primitive (scenarios 5, 6, 7) and 2node-ngdp-primitive (same)
**Symptom**:
- `set-mtu-9100`: `writeIntent "interface|Ethernet40|mtu": parent "interface|Ethernet40" does not exist`
- `bind-acl-ethernet10`: `writeIntent "interface|Ethernet40|acl|ingress": parent "interface|Ethernet40" does not exist`
- `apply-qos-ethernet11`: `writeIntent "interface|Ethernet44|qos": parent "interface|Ethernet44" does not exist`
**Root cause**: `ensureInterfaceIntent` was eliminated. Sub-resource operations (SetProperty,
BindACL, ApplyQoS) enforce I4 — the parent interface intent must exist before sub-resource
intents can be created. These scenarios operated on interfaces without prior ConfigureInterface.
**Fix**:
- Added `configure-interface` step at the start of each scenario (pure anchor, no IP/VRF)
- Added `unconfigure-interface` cleanup step at end of each scenario
- Added `clear-property` steps in interface-props before unconfigure (I5: children first)
- Changed `requires` from `[boot-ssh]` to `[setup-device]` (device intent must also exist)
- Applied to both `2node-vs-primitive` and `2node-ngdp-primitive` variants
**Design conformance**: Intent-dag-reference.md rows 8, 9, 10, 17 all have
`[interface|INTF]` as parent. I4 requires the parent to exist. ConfigureInterface (row 7)
with no params creates a pure anchor with parent `[device]`. This is NOT a workaround —
it is the architecturally correct sequence: configure the interface, then attach sub-resources.
**Rejected alternative**: Changing SetProperty parent to `[device]` — rejected because
intents.md row 10 explicitly documents `[interface|INTF]` as the parent.
Port properties are interface properties, not device properties.
**Result**: 2node-vs-primitive 21/21 PASS

---

## Fix 3: ExportEntries dropping field-less entries

**Suite**: 2node-vs-service
**Symptom**: `LOOPBACK_INTERFACE|Loopback0|10.0.0.1/32` and `INTERFACE|Ethernet0|10.1.0.0/31`
missing from delivered CONFIG_DB composite
**Root cause**: `ExportEntries` had `if len(fields) > 0` guards on both `appendTyped` and
`appendRaw` that skipped entries with no hash fields. SONiC uses field-less entries for IP
assignments (`LOOPBACK_INTERFACE|Loopback0|10.0.0.1/32`), portchannel members, etc. — the key
IS the data; the hash body is empty (NULL:NULL sentinel in Redis).
**Fix**: Removed `len(fields) > 0` guards from both `appendTyped` and `appendRaw` in
`configdb.go:ExportEntries`. Entries are now exported regardless of field count.
**Design conformance**: SONiC uses field-less hash entries as a standard pattern for IP
assignments and membership. The delivery layer (`pipeline.go:PipelineSet`) already handles
empty fields with NULL sentinels.
**File**: `pkg/newtron/device/sonic/configdb.go`

---

## Fix 4: Systematic applyEntry field mapping gaps (17 tables)

**Suite**: 2node-vs-service
**Symptom**: BGP_GLOBALS|default in delivered CONFIG_DB only had 2 of 11 fields
(`local_asn`, `router_id`), causing `(Policy)` route filtering due to missing
`ebgp_requires_policy: false`
**Root cause**: `applyEntry` in `configdb.go` was systematically incomplete compared to the
authoritative `tableParsers` in `configdb_parsers.go`. The abstract node round-trip
(`applyShadow → applyEntry → ExportEntries → structToFields`) silently dropped every
field that `applyEntry` didn't map.
**Fix**: Updated all 17 typed table cases in `applyEntry` to match the complete field
mappings in `configdb_parsers.go`. Tables fixed: PORT (2 fields), PORTCHANNEL (2),
VLAN (3), VRF (1), INTERFACE (3), BGP_GLOBALS (9), BGP_NEIGHBOR (4), BGP_NEIGHBOR_AF (6),
BGP_GLOBALS_AF (10), ACL_TABLE (2), ACL_RULE (9), ROUTE_REDISTRIBUTE (1), ROUTE_MAP (3),
BGP_PEER_GROUP (4), BGP_PEER_GROUP_AF (4), PREFIX_SET (1), WRED_PROFILE (1).
**Design conformance**: `configdb_parsers.go` is the authoritative field mapping (used for
Redis reads on physical nodes). `applyEntry` must mirror it exactly for the abstract node
path to produce identical CONFIG_DB state.
**File**: `pkg/newtron/device/sonic/configdb.go`

---

## Fix 5: topology.go ProvisionDevice missing SaveConfig

**Suite**: 2node-vs-service
**Symptom**: After provisioning, `config reload` reverted to factory defaults because
CONFIG_DB changes were only in Redis, not persisted to `/etc/sonic/config_db.json`
**Root cause**: `ProvisionDevice` in `topology.go` delivered the composite to Redis but
did not call `dev.SaveConfig(ctx)`. The newtrun HTTP path (`steps.go:provisionExecutor`)
already had SaveConfig, but the internal Go API path did not.
**Fix**: Added `dev.SaveConfig(ctx)` after `DeliverComposite` in `topology.go:ProvisionDevice`.
**Design conformance**: `config reload` reads from disk (`/etc/sonic/config_db.json`), not
from Redis. Any operation that modifies CONFIG_DB and expects it to survive a config reload
must save to disk first.
**File**: `pkg/newtron/network/topology.go`

---

## Fix 6: SetupVTEP stomping BGP_GLOBALS|default

**Suite**: 2node-vs-service
**Symptom**: BGP_GLOBALS|default had only `local_asn` and `router_id` despite
`ConfigureBGP` writing 5 fields including `ebgp_requires_policy: false`
**Root cause**: `SetupDevice` calls `ConfigureBGP` (writes 5 fields) then `SetupVTEP`.
`SetupVTEP` unconditionally called `CreateBGPGlobalsConfig("default", ..., nil)` which
wrote BGP_GLOBALS with only 2 fields. Since `applyEntry` does full struct replacement
(not field merge), the later write stomped the earlier richer entry.
**Fix**: Added existence check in `SetupVTEP`: `if _, exists := n.configDB.BGPGlobals["default"]; !exists`
before writing BGP_GLOBALS. If `ConfigureBGP` already wrote it, skip the redundant write.
**Design conformance**: Single-Owner Principle — `ConfigureBGP` is the authoritative writer
for BGP_GLOBALS|default. Other operations should check existence, not overwrite.
**Fragile pattern**: Documented in `fragile-patterns.md` FP-2 for subsequent resolution.
**File**: `pkg/newtron/network/node/evpn_ops.go`

---

## Fix 7: BGP_PEER_GROUP missing ebgp_multihop, BGP_PEER_GROUP_AF missing nexthop_unchanged

**Suite**: 2node-vs-service
**Symptom**: Overlay BGP sessions stuck in `Active` state — loopback-to-loopback peers
couldn't connect due to missing `ebgp_multihop`, and once connected, EVPN routes would
be dropped due to missing `nexthop_unchanged`
**Root cause**: `BGPPeerGroupEntry` struct was missing `EBGPMultihop` field.
`BGPPeerGroupAFEntry` struct was missing `NextHopUnchanged` field. Both `applyEntry` and
`tableParsers` didn't map these fields. `CreateEVPNPeerGroupConfig` correctly set them,
but they were silently dropped through the configDB round-trip. Same class of bug as Fix 4.
**Fix**: Added `EBGPMultihop` to `BGPPeerGroupEntry`, `NextHopUnchanged` to
`BGPPeerGroupAFEntry`. Updated `applyEntry` and `tableParsers` for both tables.
**Design conformance**: These fields are in `schema.go` already (YANG-derived). The struct
and parsers must be complete for the abstract node path to produce correct CONFIG_DB.
**Fragile pattern**: Documented in `fragile-patterns.md` FP-1, FP-3, FP-4.
**Files**: `pkg/newtron/device/sonic/configdb.go`, `pkg/newtron/device/sonic/configdb_parsers.go`

---

## Fix 8: ApplyService ARP suppression skipped during intent reconstruction

**Suite**: 2node-vs-drift
**Symptom**: `verify-clean` drift detection reports SUPPRESS_VLAN_NEIGH|Vlan300 and
SUPPRESS_VLAN_NEIGH|Vlan400 as "extra" entries — exist on device but not in
reconstructed expected state.
**Root cause**: `ApplyService` gated ARP suppression on `!vlanCS.IsEmpty()`. During
intent reconstruction, `IntentsToSteps` replays the `vlan|VlanN` intent first
(via `replayNodeStep("create-vlan")`), which calls `CreateVLAN`. When `ApplyService`
replays for the interface and calls `CreateVLAN` again, the intent-idempotent guard
returns an empty ChangeSet. The `!vlanCS.IsEmpty()` check evaluated false, so
`enableArpSuppressionConfig` was never called, and SUPPRESS_VLAN_NEIGH was absent
from the reconstructed expected state.
**Fix**: Removed `!vlanCS.IsEmpty()` gate from ARP suppression in `ApplyService`.
The entry is idempotent (SUPPRESS_VLAN_NEIGH is a merge-parser table; writing
`{"suppress":"on"}` again is safe). ARP suppression is now added whenever the
MAC-VPN spec has `arp_suppression: true`, regardless of whether CreateVLAN returned
a non-empty ChangeSet.
**Design conformance**: SUPPRESS_VLAN_NEIGH is a per-VLAN property derived from the
MAC-VPN spec. It must appear in the reconstructed expected state whenever the service
references a MAC-VPN with ARP suppression enabled. The intent-idempotent guard on
CreateVLAN correctly prevents double VLAN creation, but must not prevent unrelated
table entries from being added to the ApplyService ChangeSet.
**File**: `pkg/newtron/network/node/service_ops.go`
**Result**: 2node-vs-drift 7/7 PASS

---

## Fix 9: bgpcfgd empty description crash

**Suite**: 2node-ngdp-service
**Symptom**: bgpcfgd (SONiC 202505) generates `neighbor X description ` with empty value.
FRR rejects "% Command incomplete" and the entire config commit fails — custom peer
groups, neighbor assignments, everything lost.
**Root cause**: `CreateBGPNeighborConfig` only set `name` field when `Description` was
non-empty. bgpcfgd unconditionally generates `neighbor X description {name}` even when
the `name` field is absent from the CONFIG_DB hash.
**Fix**: Always set `fields["name"]` to `neighborIP` when no explicit Description provided.
**Design conformance**: bgpcfgd template requires the `name` field. This is a platform
compatibility fix — the CONFIG_DB entry is correct with or without the field, but the
daemon crashes without it.
**File**: `pkg/newtron/network/node/bgp_ops.go`

---

## Fix 10: DEVICE_METADATA stomping by ConfigureBGP (FP-2 class)

**Suite**: 2node-ngdp-service
**Symptom**: After provisioning + config reload, frrcfgd falls back to standard Jinja2
templates (PEER_V4/PEER_V6) instead of DB templates. Overlay BGP broken.
**Root cause**: Two issues compounding:
1. `applyEntry` for DEVICE_METADATA did full struct replacement (same class as FP-2).
   `SetupDevice` calls `SetDeviceMetadata` (writes all fields including
   `docker_routing_config_mode=unified`) then `ConfigureBGP`. `ConfigureBGP` wrote
   DEVICE_METADATA with only `bgp_asn` and `type`, stomping the frrcfgd fields.
2. `ConfigureBGP` hardcoded `type: "LeafRouter"` — violating single-owner principle
   (device type is owned by `SetDeviceMetadata`, not `ConfigureBGP`).
**Fix** (two parts):
1. Changed `applyEntry` for DEVICE_METADATA to do field-level merge instead of
   full replacement, matching Redis HSET behavior.
2. Removed `type` from `ConfigureBGP`'s DEVICE_METADATA write — `ConfigureBGP` now
   only writes `bgp_asn` (the field it genuinely owns). `type` is written once by
   `SetDeviceMetadata`.
**Design conformance**: Single-owner principle — each DEVICE_METADATA field has one
writer. `bgp_asn` owned by `ConfigureBGP` (derives from profile). `type`, `hostname`,
`docker_routing_config_mode`, `frr_mgmt_framework_config` owned by `SetDeviceMetadata`
(passes through from caller). Field-level merge in `applyEntry` matches Redis HSET
behavior for consistency between abstract and physical node paths.
**Files**: `pkg/newtron/device/sonic/configdb.go`, `pkg/newtron/network/node/bgp_ops.go`
**Result**: 2node-ngdp-service 6/6 PASS, 3node-ngdp-dataplane 8/8 PASS

---

## Fix 11: simple-vrf-host missing setup-device scenario

**Suite**: simple-vrf-host
**Symptom**: `create-customer-vrf` fails with `parent "device" does not exist`
**Root cause**: Same as Fix 1 — the suite went straight from boot-ssh to create-vrf
without calling setup-device, which creates the `device` root intent (row 1).
**Fix**:
- Added `01-setup-device.yaml` with setup-device call and intent verification
- Updated `requires` in create-vrf from `[boot-ssh]` to `[setup-device]`
**Design conformance**: The `device` intent is the DAG root (intents.md
row 1). I4 (parent must exist on creation) requires setup-device first.
This is a test suite fix, not a code change.
**Result**: simple-vrf-host 5/5 PASS

---

## Suites Completed

| Suite | Result | Notes |
|-------|--------|-------|
| 1node-vs-basic | 5/5 PASS | Fix 1 applied |
| 2node-vs-primitive | 21/21 PASS | Fix 2 applied |
| 2node-vs-service | 6/6 PASS | Fixes 3-7 applied |
| 2node-vs-drift | 7/7 PASS | Fix 8 applied |
| 2node-vs-zombie | 8/8 PASS | No code changes needed |
| 2node-ngdp-primitive | 21/21 PASS | Fix 2 applied |
| 2node-ngdp-service | 6/6 PASS | Fixes 9-10 applied |
| 3node-ngdp-dataplane | 8/8 PASS | No code changes needed |
| simple-vrf-host | 5/5 PASS | Fix 11 applied (test suite fix) |

---

## Fragile Patterns (documented for subsequent resolution)

See `fragile-patterns.md` for full details:
- **FP-1**: applyEntry / parsers / struct field triplication — no compile-time check
- **FP-2**: BGP_GLOBALS stomping by multiple writers — applyEntry does full replacement
- **FP-3**: schema.go knows fields that structs don't — false validation confidence
- **FP-4**: BGP_PEER_GROUP_AF missing fields vs BGP_NEIGHBOR_AF — inheritance gap
- **FP-5**: DEVICE_METADATA stomping — same class as FP-2, resolved with field merge + single-owner cleanup

---

## Proposed Deviations (for discussion at end)

(none yet)
