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

## 6. Retire `pkg/newtron/health/` package

The standalone `health` package (`pkg/newtron/health/checker.go`) is a legacy stub with
placeholder implementations (BGP/VXLAN/EVPN checks return dummy "healthy" results without
querying actual state). The real health checks live in `pkg/newtron/network/node/health_ops.go`
(used by newtest). The `newtron health check` CLI command (`cmd/newtron/cmd_health.go`)
should be updated to use `Node.RunHealthChecks()` instead, and the `health/` package deleted.

## 7. Stream step results in `newtest status --detail`

Currently `StateReporter.ScenarioEnd` saves all step results in bulk when a scenario
finishes. To show up-to-the-moment step progress, `StateReporter.StepEnd` should
append each step's `StepState` to `ScenarioState.Steps` and call `SaveRunState()`
incrementally. This allows `newtest status --detail` to show which steps have completed
(with timing) even while a scenario is still running.

## 8. Show total scenarios and steps in `newtest status` summary

The summary section at the top of `newtest status` output should include the total
number of scenarios and total number of steps (sum of all scenario step counts).
Example: `  scenarios: 21 (210 steps total)`

## 9. Scenario descriptions in `newtest status --detail`

Each scenario YAML should have a `description` field with a detailed explanation of
the scenario's intent (what it tests and why). The parser should read this into
`Scenario.Description`. `ScenarioState` should carry the description through to
`state.json`, and `printDetailView` should display it as a header line under each
scenario name in the `--detail` output. This gives operators immediate context about
what each scenario is verifying without reading the YAML source.
