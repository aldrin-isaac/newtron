# RCA-052: VRF Name Exceeding IFNAMSIZ Silently Kills the Dataplane

**Severity**: High
**Platform**: All SONiC (Linux IFNAMSIZ — kernel constant, platform-independent). Surfaced on sonic-vs.
**Status**: Fixed — cap derived VRF names at 15 chars, fail-closed at author time and apply time (commit `19d85d8`)

## Symptom

A service whose derived VRF name is 16+ characters provisions with a clean
CONFIG_DB and clean drift, but the dataplane is dead. On a fresh 2node-vs
deployment the `prime-arp-svi` scenario (an SVI-sourced ping) failed with 100%
packet loss across three cold runs, while every CONFIG_DB-level check passed:

- `VRF|Vrf_SVC_EVPN_IRB` present in CONFIG_DB, correct fields
- `VLAN_INTERFACE|Vlan400 vrf_name=Vrf_SVC_EVPN_IRB` present, correct
- `intent drift` empty — all newtron writes actualized
- `intent snapshot` before/after clean — no orphaned intent
- The SVI has **no IPv4 address** in the kernel; the ping sources off the
  management IP instead and never reaches the fabric

The failure is invisible to CONFIG_DB, drift, and intent-snapshot because all
three operate at the CONFIG_DB layer, where the `VRF|...` row exists and matches.
Only a dataplane assertion caught it.

## Root Cause

Linux `IFNAMSIZ` is 16, which allows **15 usable characters** for an interface
or VRF device name (the 16th byte is the null terminator). `vrfmgrd` creates the
kernel VRF by running the equivalent of `ip link add "<name>" type vrf`; when
`<name>` is 16+ characters the kernel rejects it and no VRF device is created.
`intfmgrd` then has no master device to bind the SVI's IP onto, so it never
programs the address — the dataplane has no L3 interface in that VRF.

`Vrf_SVC_EVPN_IRB` is exactly 16 characters (`Vrf_` + `SVC_EVPN_IRB`), one over
the limit. The prior naming scheme derived VRF names from the IP-VPN
(`Vrf_<ipvpn>`, e.g. `Vrf_IRB` = 7) and happened to stay under; renaming VRFs
after the **service** (`Vrf_<service>`) pushed a long service name over the edge.
Nothing in newtron or SONiC rejected the over-length name — CONFIG_DB has no
length constraint on the `VRF` key, and `vrfmgrd`'s `ip link` failure produces no
CONFIG_DB or APP_DB signal that drift or the projection can see.

## Evidence

On the failing 2node-vs cold run:

1. `VRF|Vrf_SVC_EVPN_IRB` written to CONFIG_DB — persists in Redis, drift clean
2. `ip -d link show type vrf` on the device — **no** `Vrf_SVC_EVPN_IRB` device
   (kernel silently rejected the 16-char name)
3. `ip addr show Vlan400` — SVI up but carries no IPv4 (intfmgrd had no master VRF)
4. `prime-arp-svi` ping — sources off the mgmt IP, 100% loss to the peer

Shortening the service name so the VRF fit (`Vrf_EIRB` = 8) created the kernel
VRF, intfmgrd programmed the SVI IP, and the ping passed.

## Fix

The one truth is `util.VRFNameMaxLen = 15`; every check derives from it. A VRF
name is `Vrf_<service>` (shared) or `Vrf_<service>_<iface>` (interface).

- **`util.DeriveVRFName`** single-letters the interface component
  (`Ethernet12`→`E12`, `PortChannel1`→`P1`, `Vlan400`→`V400`) so the interface
  suffix costs the fewest characters.
- **Author-time cap** (`spec/validate.go`): a service's name must be
  ≤ `util.MaxServiceNameLen(vrf_type)` — **5 for interface** (`Vrf_`+name+`_`+≤5
  interface suffix), **11 for shared** (`Vrf_`+name). Both fall out of the
  15-char budget arithmetic, not magic numbers.
- **Apply-time fail-close** (`ApplyService`): `util.ValidateVRFNameLength` runs
  before any CONFIG_DB write and returns a 409 precondition error if the fully
  derived name (including the runtime interface, which the author cap cannot see
  for a long port or sub-interface) exceeds 15.
- **Schema** (`FieldMeta.MaxLength` + `MaxLengthWhen`): the service `name` field
  carries 11 with a `{vrf_type=interface → 5}` conditional, so a client (newtcon)
  enforces the cap live. `vrf_type` is immutable (it governs the cap).

## Impact

- Demo services renamed to a compact convention that fits even the ≤5 interface
  budget: `[E]`(EVPN) + `IRB`|`BRD`|`RTD`(type) + `[instance number]` —
  `EIRB1`/`EIRB2`, `EIRB`, `EBRD`, `BRD`, `IRB`, `RTD`, `ERTD1`. The
  human-readable name lives in the service `description`.
- `2node-vs-primitive` 25/25 and `2node-ngdp-primitive` 22/22 cold-green after
  the fix — the derived VRFs create real kernel devices (`VRFMGRD-ERRS: 0`).

## Lesson

Intent, drift, and intent-snapshot are all CONFIG_DB-level and blind to
kernel-programming failures below the row. Keep a real **dataplane assertion**
(an SVI-sourced ping) in every service suite — it caught what three layers of
CONFIG_DB checks could not. This extends the "monitor before blaming the
platform" discipline: the platform did exactly what it was told (`ip link add`
of a too-long name fails); the bug was ours, above the wire.

## Related

- RCA-044: intfmgrd requires the `Vrf_` prefix — the other end of the same VRF
  naming constraint (prefix required, length capped)
- RCA-030: CiscoVS VLAN SVI with no kernel interface — same "CONFIG_DB clean,
  kernel device missing, dataplane dead" failure shape from a different cause
- RCA-037 / RCA-041: intfmgrd/vrfmgrd binding races — timing variants of "VRF
  row present, interface never bound"
- Linux `IFNAMSIZ` (`include/uapi/linux/if.h`) — the 16-byte kernel name limit
