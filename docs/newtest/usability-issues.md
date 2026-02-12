# CLI Usability Issues

Issues observed across newtron, newtest, and newtlab. Each should be resolved before the tools are considered production-ready.

Items from commit 33d68a1 ("Add live progress reporting, suite discovery, and CLI UX improvements") are marked with their completion status.

## COMPLETE

### UX-C1: Live progress reporting (33d68a1)

ConsoleProgress with real-time PASS/FAIL/SKIP per scenario, dot-padding, color, duration. Working as designed.

### UX-C2: Suite discovery command (33d68a1)

`newtest suites` command lists available test suites. Working as designed.

### UX-C3: Enriched `list` output (33d68a1)

Tabwriter, dependency ordering, `--dir` flag. Working as designed.

### UX-C4: Enriched `topologies` output (33d68a1)

Device/link counts and descriptions. Working as designed.

### UX-C5: Shared ANSI helpers (33d68a1)

`pkg/cli/format.go` (Green, Yellow, Red, Bold, Dim, DotPad) used by all three CLIs. Working as designed.

### UX-1: Spurious spine2 profile warning — RESOLVED

`deriveBGPNeighbors()` now checks `topo.HasDevice(rrName)` before loading a profile. Devices not in the current topology are silently skipped.

### UX-2: asic_db discovery warning on every connect — RESOLVED

Downgraded from `Warnf` to `Debugf`. Expected on VPP; visible with `-v`.

### UX-3: Failed steps show no error detail in progress output — RESOLVED

`executeStep()` now aggregates `Details[].Message` into `StepResult.Message` when executors only set per-device details. The suite summary also aggregates details as a fallback.

### UX-4: Logrus lines interleaved with progress table — RESOLVED

Log level defaults to `warn` unless `-v` is passed. Info-level operation logs no longer interleave with progress output.

### UX-5: `--no-deploy` requires a running lab but gives no guidance — RESOLVED

Connect failures in `--no-deploy` mode now include: "hint: no running lab; deploy first with: newtlab deploy -S <specDir>".

### UX-6: `--scenario` requires numbered filename prefix — RESOLVED

`resolveScenarioPath()` tries: exact match → `*-<name>.yaml` glob → scan `name:` field. `--scenario bgp-direct-neighbor` now finds `12-bgp-direct-neighbor.yaml`.

### UX-7: Suite summary omits per-device error messages — RESOLVED

`SuiteEnd()` failure section aggregates `Details[].Message` when `step.Message` is empty. Belt-and-suspenders with UX-3 fix.

### UX-8: Verbose mode shows no error detail on connect failure — RESOLVED

`ScenarioEnd()` now surfaces `result.DeployError` in verbose mode before the status line.

### UX-10: Cobra `completion` command clutters help — RESOLVED

`CompletionOptions{HiddenDefaultCmd: true}` set on all three root commands.

### UX-11: newtron `ExecuteByDefault` setting is dead — RESOLVED

Removed from `Settings` struct.

### UX-12: newtron `DefaultDevice` setting is dead — RESOLVED

Removed from `Settings` struct.

### UX-13: newtron `LastDevice` setting is half-built — RESOLVED

Removed from `Settings` struct.

### UX-14: newtron `interactive` command is premature — RESOLVED

Set `Hidden: true`. Still functional but not shown in help.

### UX-15: newtron `shell` command is premature — RESOLVED

Set `Hidden: true`. Still functional but not shown in help.

### UX-17: newtest `--topology` override has no validation — RESOLVED (partial)

`--topology` override is now validated: the topology specs directory must exist. `--platform` validation is deferred (requires platform registry lookup).

### UX-19: newtlab `--host` on deploy is undocumented — RESOLVED (partial)

Flag description improved to "deploy only nodes assigned to this host (multi-host mode)". Full docs deferred.

### UX-20: Root help structure is wildly inconsistent — RESOLVED

All three CLIs now share a consistent structure: concise Long description (2-3 sentences) + canonical usage pattern. No lengthy examples in root help.

### UX-21: Version output is broken across all three tools — RESOLVED (partial)

Dev builds now print `<tool> dev build (use 'make build' for version info)` instead of `dev (unknown)`. Full fix requires wiring ldflags into the build system.

### UX-22: Global flags leak onto irrelevant subcommands — RESOLVED

`-x`/`-s`/`--json` moved from `PersistentFlags` to local flags via `addWriteFlags()`/`addOutputFlags()` helpers. Context flags (`-n`/`-d`/`-i`/`-S`/`-v`) remain global.

### UX-23: `-v`/`--verbose` scope is inconsistent — RESOLVED

`-v` is now a `PersistentFlag` on all three root commands.

### UX-25: Errors dump full usage block — RESOLVED

`SilenceUsage: true` and `SilenceErrors: true` set on all three root commands.

### UX-26: `settings show` displays dead settings — RESOLVED

Dead settings (`DefaultDevice`, `LastDevice`, `ExecuteByDefault`) removed from struct. Only 5 active settings remain.

### UX-27: newtron help lists 40+ commands flat, ungrouped — RESOLVED

Commands grouped into 4 categories: Object Operations, Resource Management, Device Operations, Configuration & Meta. Noun-group aliases (`acl`, `bgp`, `vlan`, etc.) hidden but still functional.

---

## OPEN

### UX-9: Settings-based path resolution untested

3-tier resolution (flag > env > settings > default) for `LabSpecs`, `DefaultSuite`, `TopologiesDir` was added in 33d68a1 but never exercised in a real run. All test runs use explicit `--dir`. Unknown whether the fallback chain actually works.

### UX-16: newtest `TopologiesDir` setting is dead

Defined in `pkg/settings/settings.go` and settable via settings command, but `cmd/newtest/cmd_run.go` hardcodes `newtest/topologies` as the default and never reads the setting value.

### UX-18: newtlab `bridge` hidden command is vestigial

The bridge process now runs as standalone `newtlink <config.json>`. The hidden `newtlab bridge` command is leftover from before newtlink was extracted. Kept as fallback for `startBridgeProcess()` but should be removed once newtlink is universal.

### UX-24: `-S`/`--specs` scope is inconsistent

Global on newtron and newtlab, absent on newtest. newtest hardcodes `newtest/topologies` and uses `--dir` on `run` only. The spec/dir resolution pattern should be consistent: either all tools use `-S` globally, or each tool documents its own path resolution clearly.

### UX-28: newtest has no lifecycle management — RESOLVED

Replaced one-shot `run` with a lifecycle model:
- `newtest start` — deploy (if needed), run, leave topology up; resumes paused runs
- `newtest pause` — stop after current scenario, keep topology
- `newtest stop` — destroy topology, clean state
- `newtest status` — show suite/scenario progress without side effects

Suite-level locking via PID prevents concurrent `start` invocations on the same suite. State is persisted to `~/.newtron/newtest/<suite>/state.json`. The `run` command is kept as a hidden alias for backward compatibility.
