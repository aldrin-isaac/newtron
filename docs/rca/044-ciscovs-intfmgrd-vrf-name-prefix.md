# RCA-044: CiscoVS intfmgrd Requires Vrf_ Prefix for VRF Names

**Severity**: High
**Platform**: CiscoVS (SONiC 202505 / Silicon One NGDP)
**Status**: Fixed — use `Vrf_` prefix for all VRF names on CiscoVS

## Symptom

On CiscoVS (Palladium2 / Silicon One NGDP), intfmgrd silently ignores INTERFACE table
CONFIG_DB entries when the `vrf_name` field does not start with the `Vrf` prefix. VRF
names like `CUSTOMER` are processed correctly by vrfmgrd (kernel VRF device created),
vrforch/orchagent (SAI virtual router programmed), and APP_DB VRF_TABLE (entry written).
However, intfmgrd does not write the corresponding INTF_TABLE entries to APP_DB, so
orchagent never creates the SAI router interface or connected routes.

Observable failures:

- VRF created successfully: kernel VRF device exists, ASIC_DB has SAI_OBJECT_TYPE_VIRTUAL_ROUTER
- `INTERFACE|EthernetN` written to CONFIG_DB with `vrf_name=CUSTOMER` — entry persists in Redis
- intfmgrd produces zero log entries about the INTERFACE entry
- No INTF_TABLE entries appear in APP_DB (swss.rec shows no SET for the entry)
- Kernel interface has no master VRF and no IP address
- No SAI router interface or connected route in ASIC_DB
- Host dataplane connectivity fails (100% packet loss)

## Root Cause

intfmgrd on CiscoVS validates the VRF name format before processing INTERFACE entries.
VRF names must start with `Vrf` (SONiC convention from sonic-yang-models). Names that do
not match this pattern are silently dropped — no error log, no APP_DB write, no kernel
action.

vrfmgrd does NOT enforce this naming convention — it creates kernel VRF devices for any
name. This asymmetry means the VRF infrastructure is fully set up but interface bindings
are never processed.

## Evidence

On a fresh 2node CiscoVS deployment:

1. `VRF|CUSTOMER` created → vrfmgrd processes, kernel device exists, APP_DB VRF_TABLE:CUSTOMER written
2. `INTERFACE|Ethernet2 vrf_name=CUSTOMER` written → swss.rec shows NO INTF_TABLE:Ethernet2|SET
3. `VRF|Vrf_CUSTOMER` created → same vrfmgrd processing
4. `INTERFACE|Ethernet2 vrf_name=Vrf_CUSTOMER` written → swss.rec shows INTF_TABLE:Ethernet2|SET immediately

The only difference is the VRF name prefix.

## Fix

Use `Vrf_` prefix for all VRF names on CiscoVS. This matches the SONiC YANG model
convention (`sonic-vrf.yang` defines the VRF name pattern as starting with `Vrf`).

All newtron-created VRF names should use the `Vrf_` prefix. The newtron VRF creation API
should enforce this convention, or at minimum document it.

## Impact

- simple-vrf-host test suite: changed `CUSTOMER` → `Vrf_CUSTOMER` (all 4 scenarios pass)
- 2node-primitive routed scenario: already uses `Vrf_local` (unaffected)
- Service-created VRFs: already use `Vrf_` prefix via newtron naming convention (unaffected)

## Related

- RCA-037: intfmgrd VRF binding race at provision time (timing issue, different root cause)
- RCA-041: vrfmgrd writes VRF_OBJECT_TABLE on runtime notification but not VRF_TABLE
- sonic-yang-models `sonic-vrf.yang` — VRF name pattern constraint
