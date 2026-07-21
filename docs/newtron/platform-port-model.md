# Platform Port Model

**Status: implemented** (phases 1–3 — PRs #306, #307, and this change). This note
is the design rationale: the *why* behind the explicit `PlatformSpec.Ports`
inventory that replaced `vm_interface_map`. Its "today" passages describe the
*pre-change* baseline (the stride schemes) and are retained as the historical
contrast that motivated the design — they are **not** current behavior. For how
NIC resolution works now, see `docs/newtlab/lld.md` §5.3; the phase-by-phase
record is in `platform-port-model-impl.md`.

## 1. Purpose

A platform's **port model** answers two questions newtlab must resolve *before
any device boots*:

1. **Which ports does this platform have, and what are they named?**
   (`Ethernet0`, `Ethernet4`, … on a stride-4 SONiC box; `ge-0/0/0`,
   `ge-0/0/1`, `fxp0` on a vJunos box.)
2. **Which QEMU NIC slot backs each port?** (`Ethernet0` → data NIC 1.)

Today neither is stored. Newtlab reconstructs (2) from a compact *stride rule*
(`vm_interface_map`) plus the port names the operator wrote into the topology,
and (1) is never modeled at all — it lives inside the SONiC image's
`port_config.ini`, which is unavailable until the VM is running. This note
proposes storing an explicit, **generated** per-port table on `PlatformSpec` so
both questions are answerable before boot, for SONiC and non-SONiC platforms
alike.

## 2. The Problem

### 2.1 The bootstrapping inversion

The authoritative SONiC port table (`<hwsku>/port_config.ini`) is sealed inside
the VM disk image. Newtlab needs to know a platform's ports to build the VM and
to validate the topology that references them — but that information lives inside
the very node that has not been spun up yet. The data needed to *start* the
device is locked inside the *started* device.

Newtlab gets away with this today only because it sidesteps the port table
entirely (§3). The cost is paid in three quieter failures rather than one loud
one.

### 2.2 Three degradations

1. **No validation.** A topology link naming `Ethernet200` on a 32-port
   platform, or `Ethernet5` on a stride-4 platform, is not caught at deploy
   time. `ResolveNICIndex` either returns a NIC slot that maps to nothing or
   fails with an arithmetic error far from the cause.
2. **No discoverability.** Nothing can answer "what ports can I wire on this
   platform?" before boot — there is no list to read.
3. **Non-stride naming is inexpressible.** The stride schemes
   (`sequential`, `stride-4`) are formulas over `EthernetN`. They cannot
   represent `ge-0/0/0`, `xe-1/0/0`, `fxp0`, `ae0`, or any name that is not
   `Ethernet` + a strided integer. The moment a `platform.json` must describe a
   vJunos box for newtlab to deploy it, the stride model has nothing to say.

(3) is the forcing function: it is not a missing optimization but a hard wall.
The sibling Junos automation framework `netconf.pl` already models its
platforms as **chassis + cards** (`spec/platforms.yaml`: `ge-0/0/N` data ports,
`fxp0` management, `ae`/`irb` virtual) precisely because no stride formula
covers Junos port naming.

## 3. Current State (today)

### 3.1 What `PlatformSpec` carries

`PlatformSpec` (pkg/newtron/spec/types.go) describes ports only as a **summary**:

- `PortCount int` — how many data ports exist.
- `DefaultSpeed string` — the headline per-port speed.
- `Breakouts []string` — the union of breakout-mode strings.
- `VMInterfaceMap string` — the NIC ordering *scheme*: `sequential`,
  `stride-4`, `linux`, or `custom`.

There is no per-port detail. The generators that build a `PlatformSpec` from
SONiC inputs (`FromPortConfigINI`, `FromSONiCPlatformJSON`) deliberately discard
per-port lanes/alias/index — a documented non-goal (#185). The authoritative
per-port shape stays in the image.

### 3.2 How newtlab allocates NICs

NIC allocation is **purely topology-driven**. `PortCount` is read **nowhere** in
newtlab (grep-confirmed). The path:

- `AllocateLinks` (pkg/newtlab/link.go) walks the topology's links. Each wired
  endpoint — `"r1:Ethernet0"`, split by `splitLinkEndpoint` into device +
  interface — produces exactly one `NICConfig`. Unwired ports get no NIC.
- The NIC slot comes from `ResolveNICIndex(ports, ifaceName)`
  (pkg/newtlab/iface_map.go), a lookup in the platform's explicit port inventory
  (`PlatformSpec.Ports`) — the single authority for the device-native name → NIC
  mapping, for every platform, switch and host alike. There is no naming formula
  (Ethernet stride-4, vJunos `ge-0/0/0`, host `eth0` all coexist), so the mapping
  is always tabled. An unresolvable name fails the deploy with an actionable
  error rather than producing a VM whose NIC maps to nothing.
- **Hosts differ only in coalescing.** newtlab packs many small host VMs into one
  shared QEMU process, so a host's `nic_index` is a per-host ordinal, not an
  absolute slot. `coalesceHostVMs` gives each host a `NICBase` in the shared VM,
  sized from the highest ordinal that host actually wires (the `ports[]`
  authority — not a link count), so non-contiguous usage (eth0 and eth3) reserves
  the full span. `AllocateLinks` then resolves a host interface as
  `NICBase + (ResolveNICIndex(ports, name) - 1)`. A switch (a dedicated VM) needs
  no offset — its `nic_index` is the absolute slot.
- `qemu.go` emits the management NIC (slot 0) plus the data NICs sorted by
  index, relying on `kernel ethN == NIC index N` (QEMU PCI enumeration order)
  for TC-mirred bridging. A gap in the wired indices breaks this invariant —
  the "no gaps, wire contiguously from Ethernet0" rule (RCA-020).
- Separately, `buildPatchVars` (pkg/newtlab/patch.go) derives `PortStride`
  (`stride-4` → 4, otherwise → 1) to drive the VPP boot patch's port *renaming*
  (RCA-013) — the one place a platform's port *names* are generated rather than
  taken from the image.

So the platform-derived port input newtlab uses is the `ports[]` inventory: the
topology's explicit names resolve against it, and for coalesced hosts the
per-host ordinal is offset by the shared VM's `NICBase`.

## 4. The Resolving Insight: a Pre-Boot Projection

The natural objection to itemizing ports on `PlatformSpec` is §27 (Single Owner)
/ "device is source of reality": the image's `port_config.ini` is the runtime
truth, and a second hand-authored copy would drift.

That objection applies only to **hand-authoring**. It dissolves if the port
table is **generated, not authored** — a *pre-boot projection* of the port
inventory, captured at platform-onboarding time:

- **SONiC:** the generators (`FromPortConfigINI`, `FromSONiCPlatformJSON`)
  already read the authoritative source; they would *populate* the table
  instead of discarding it. The image remains the runtime source of reality;
  `platform.json` holds a captured snapshot for the phase *before* the image
  exists. Re-running the generator refreshes it. This is the same shape as
  newtron's own projection — a derived replay of an authority, never a competing
  writer.
- **vJunos:** there is no `port_config.ini`. The port inventory is a property of
  the chassis/card model — exactly what `netconf.pl`'s `platforms.yaml` already
  encodes. Newtron's `platform.json` becomes the SONiC-side analog, generated or
  authored from that chassis definition. (Honest asymmetry: for Junos the
  "projection" has no machine-readable in-image source the way SONiC does; its
  authority is the chassis spec. This is noted as an open question in §10.)

In both cases the stored table is a snapshot of an upstream authority, refreshed
by regeneration — not a source of truth that competes with the device.

## 5. Schema (as designed — now shipped)

Sections 5–8 are the original design, preserved as the rationale record; they
read in the proposing tense but describe **shipped** behavior. `PlatformSpec.Ports`
and `PortSpec` are in `pkg/newtron/spec/types.go` today; §10's open questions are
struck through as each was resolved.

A `Ports []PortSpec` on `PlatformSpec`:

```go
// PortSpec is one front-panel port in a platform's pre-boot port model.
// Generated from the platform's port authority (SONiC port_config.ini /
// platform.json, or a Junos chassis definition), not hand-authored.
type PortSpec struct {
    Name     string `json:"name"`             // device-native interface name: "Ethernet0", "ge-0/0/0"
    NICIndex int    `json:"nic_index"`        // QEMU data-NIC slot (1-based; NIC 0 is management)
    Speed    string `json:"speed,omitempty"`  // canonical, e.g. "40G" (defaults to PlatformSpec.DefaultSpeed)
    Lanes    []int  `json:"lanes,omitempty"`  // serdes lanes, when known (SONiC); informational
}
```

```jsonc
// SONiC stride-4 (Force10-S6000_vs) — generated from port_config.ini.
"ports": [
  { "name": "Ethernet0",  "nic_index": 1, "speed": "40G", "lanes": [1,2,3,4] },
  { "name": "Ethernet4",  "nic_index": 2, "speed": "40G", "lanes": [5,6,7,8] },
  { "name": "Ethernet8",  "nic_index": 3, "speed": "40G", "lanes": [9,10,11,12] }
  // … 32 rows
]
```

```jsonc
// vJunos-router — generated/authored from the chassis model.
"ports": [
  { "name": "ge-0/0/0", "nic_index": 1 },
  { "name": "ge-0/0/1", "nic_index": 2 },
  { "name": "ge-0/0/2", "nic_index": 3 }
]
```

The table is the single representation of name↔NIC mapping. The
`nic_index` column makes the mapping *explicit* and *total*, so it works for any
naming, strided or not.

## 6. Generator Changes

The stride schemes stop being runtime behavior and become **generator inputs**:

- `FromPortConfigINI` reads the `name` column it already parses and assigns
  `nic_index` by row order (the `port_config.ini` row order is the authoritative
  NIC ordering — §5.3 of newtlab/lld.md), emitting an explicit `ports` table.
  The `sequential` vs `stride-4` distinction collapses: both produce
  monotonically increasing `nic_index` over the rows; the table records the
  result, not the formula.
- `FromSONiCPlatformJSON` does the same from the `interfaces` map (ordered by
  the index field).
- A Junos generator (future; could live alongside `netconf.pl` integration)
  emits the `ge-/xe-/fxp0` table from the chassis/card definition.

`DefaultSpeed`, `PortCount`, and `Breakouts` remain as the human-facing summary;
`PortCount` becomes `len(Ports)` by construction (validated at load).

## 7. newtlab Consumption

- **`ResolveNICIndex` becomes a table lookup.** Given the node's platform and a
  port name, return the matching `PortSpec.NICIndex`. The four formula branches
  (`sequential`/`stride-4`/`linux`/`custom`) collapse into one lookup; the dead
  `custom` branch (§3.2) is deleted rather than wired up — the explicit table
  *is* the general case `custom` was reaching for.
- **Topology validation.** `AllocateLinks` rejects a link whose port name is not
  in the platform's `Ports` table, with a clear deploy-time error ("port
  `Ethernet200` not on platform `Force10-S6000_vs` (32 ports: Ethernet0…124)")
  instead of a downstream NIC that maps to nothing. This is the answer to "should
  newtlab look in platform.json to know its ports?" — yes, as a name→NIC lookup
  and a validator.
- **VPP boot-patch naming.** `buildPatchVars` derives the renamed port set from
  the `Ports` table directly (the table already lists the final names and their
  NIC slots), retiring the `PortStride` formula. RCA-013's concern — that
  `vm_interface_map` is a deployment property, not a function of the source port
  names — is preserved: the *table* is the deployment artifact, generated per
  variant (the VPP variant's table lists its post-rename `Ethernet0,1,2,…`).
- **No-gaps (RCA-020) becomes explicit, not implicit.** With every port's
  `nic_index` recorded, contiguity is a property newtlab can *check* (and, if a
  future decision warrants, *relax* by dense-filling NICs up to the highest used
  index). Relaxing no-gaps is out of scope for this note; the explicit table is
  the prerequisite that makes it a decision rather than a constraint.

## 8. Migration

Greenfield (§40 — delete, don't deprecate):

- Regenerate every in-tree `platform.json` through the updated generators so each
  gains its `ports` table. The SONiC files (`Force10-S6000_vs`,
  `Force10-S6000_vpp`, `cisco-p200-32x100-vs`) regenerate from their
  `port_config.ini` — note this is where the §5 example comes from: stride-4
  *names* (`Ethernet0,4,8,…`, from the HWSKU) with sequential *NIC assignment*
  (`nic_index 1,2,3,…`, by row order), which the explicit table captures without
  needing the `vm_interface_map="sequential"` scheme string those files carry
  today. The host/router platforms (`alpine-host`, `vjunos-router`) get
  authored/generated tables.
- `VMInterfaceMap` is **removed** from `PlatformSpec` once `Ports` is the stored
  truth — both its consumers (`ResolveNICIndex`, `PortStride`) read the table
  instead. (Open question §10: whether to retain it transiently as a generator
  convenience knob, or move that knob into the generator CLI only.)
- The `custom` scheme and its unreachable `customMap` plumbing are deleted.
- `node.go`'s `InterfaceMap` field and its `"sequential"` fallback are removed;
  the node carries its platform's `Ports` table instead.

## 9. Principles Adherence

- **§27 Single Owner / device-is-reality.** The table is a generated projection
  of the port authority (image `port_config.ini` / chassis spec), refreshed by
  regeneration — not a second author (§4).
- **§7 Definition is network-scoped; execution is device-scoped.** Wiring stays
  in `topology.json` (network-scoped); the port *inventory* is platform identity
  (global), correctly on `PlatformSpec`.
- **§13 Same Concept = Same Name.** One `Ports` table replaces four parallel
  name→NIC schemes; `nic_index` is the single mapping concept.
- **§40 Greenfield.** `vm_interface_map`, the `custom` scheme, and the
  `PortStride` formula are deleted, not aliased.
- **editing-guidelines §11.** This document is marked unimplemented; §3
  describes current behavior with file references.

## 10. Open Questions

1. **Junos projection source.** ~~SONiC's table regenerates from an in-image
   file; Junos has no equivalent. Is the chassis/card model in `netconf.pl`'s
   `platforms.yaml` the authority newtron generates from, or does newtron author
   the Junos `ports` table directly?~~ **Resolved (Phase 4): authored directly.**
   `vjunos-router` carries a hand-authored `ge-0/0/0..9` table (mirroring
   netconf.pl's `vJunos-Router-PFE` card) — self-contained, no cross-repo
   coupling. A `FromJunosChassis` generator that reads `platforms.yaml` remains a
   future option if a real newtron↔netconf.pl integration is built.
2. **`vm_interface_map`'s fate.** ~~Fully removed, or retained as a
   generator-only input?~~ **Resolved (Phase 3): fully removed.**
3. **Breakouts × ports.** A port broken out 4× becomes four logical sub-ports
   (`Ethernet0/1`…). Does the `ports` table enumerate the base ports only (and
   breakouts stay a separate dimension), or the active logical ports after a
   chosen breakout? The former keeps the table a stable platform property; the
   latter couples it to a per-deployment breakout choice.
4. **Relaxing no-gaps (RCA-020).** Out of scope here, but the explicit table
   enables it. Worth a separate decision once the table exists.
