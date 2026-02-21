# Refactor Items

Tracked items for future cleanup — none are blocking, all are correctness-preserving.

## ~~1. Move ConfigureSVI / RemoveSVI to vlan_ops.go~~ DONE

Moved SVIConfig, ConfigureSVI, RemoveSVI from evpn_ops.go to vlan_ops.go.

## ~~2. Parser validation table uses `params` for step-level fields~~ DONE

Moved `vlan_id` and `interface` from `params` to `fields` validation for all
actions where executors read step-level fields (`step.VLANID`, `step.Interface`).
Added `vlan_id` to `stepFieldGetter` map. Updated tests to match.

## ~~3. Add RemoveBGPGlobals primitive~~ DONE

`RemoveBGPGlobals` implemented in `bgp_ops.go`, uses `BGPGlobalsConfig` internally.

## ~~4. Missing `remove-svi` VLAN_ID as step-level field~~ DONE

Changed `removeSVIExecutor` to read `step.VLANID` instead of `intParam(step.Params, "vlan_id")`.
Validation updated to `fields: []string{"vlan_id"}` for consistency with other VLAN operations.

## ~~5. Single-owner principle for CONFIG_DB tables (DRY)~~ DONE

Implemented in three phases:

1. **Config functions**: Each owning `*_ops.go` file exports pure config functions
   returning `[]CompositeEntry` (e.g., `BGPNeighborConfig`, `VTEPConfig`, `vlanConfig`).
2. **Key helpers**: Exported key functions encapsulate CONFIG_DB key formats
   (`VLANName`, `VNIMapKey`, `BGPGlobalsAFKey`, `BGPNeighborAFKey`, etc.).
3. **Schema leakage eliminated**: `composite.go` has no CONFIG_DB knowledge (typed
   helpers deleted, `AddEntries()` accepts config function output). `topology.go`
   calls config functions exclusively. `service_ops.go` teardowns use key helpers.

Target ownership map in CLAUDE.md "Single-Owner Principle" section.
Separation of Concerns principle documented in CLAUDE.md.
