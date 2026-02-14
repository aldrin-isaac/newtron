# Usability Improvements — Future Work

Remaining usability improvements identified during the cross-tool CLI audit.
These are larger items that require more design work or have wider blast radius.

## U-1: Bridge visibility in newtlab status

**Tool**: newtlab
**Effort**: Medium

The `newtlab status` output shows nodes and links but not bridge processes.
In multi-host deployments, users can't tell which host runs each link's bridge
worker, or whether bridge processes are alive.

**Proposed changes:**
- Add a "BRIDGES" section to `newtlab status` detail view showing:
  - Host IP, PID, stats address, alive status per bridge
- Add a `worker_host` column to the link table
- In the `--json` output, bridge state is already included via `LabState.Bridges`

**Files**: `cmd/newtlab/cmd_status.go`

---

## U-2: Estimated completion time in newtest status

**Tool**: newtest
**Effort**: Medium

The `newtest status` display shows progress (e.g., "3/31 passed") but no time
estimate. Users can't tell if a suite will finish in 5 minutes or 2 hours.

**Proposed changes:**
- Track per-scenario duration in `RunState.Scenarios` (already stored as string)
- Compute average duration of completed scenarios
- Extrapolate remaining time: `avg_duration * remaining_count`
- Display as "est. remaining: ~Xm" in status output
- Only show estimate after at least 2 scenarios have completed

**Files**: `cmd/newtest/cmd_status.go`

---

## U-3: Node discovery for newtlab ssh

**Tool**: newtlab
**Effort**: Low-Medium

`newtlab ssh <node>` requires knowing node names. Users must run
`newtlab status` first to discover available names.

**Proposed changes:**
- Add shell completion for node names (cobra's `ValidArgsFunction`)
- On "node not found" error, list available nodes:
  `"node 'foo' not found; available: leaf1, spine1"`
- Consider prefix matching: `newtlab ssh leaf` matches `leaf1` if unambiguous

**Files**: `cmd/newtlab/cmd_ssh.go`, `cmd/newtlab/cmd_stop.go`, `cmd/newtlab/cmd_console.go`
