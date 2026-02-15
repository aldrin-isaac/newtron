# Usability Improvements — Backlog

Open items from CLI usability audits. Resolved items have been removed.

---

## 1. Bridge visibility in multi-host status

**Tool**: newtlab
**Effort**: Low

`newtlab status --bridge-stats` shows per-link traffic, but in multi-host
deployments users can't see which host runs each bridge worker or whether
bridge processes are alive.

**Remaining work:**
- Add `worker_host` column to the link table
- Add a BRIDGES section to the detail view: host IP, PID, alive status
- JSON output already includes bridge state via `LabState.Bridges`

**Files**: `cmd/newtlab/cmd_status.go`

---

## 2. Node discovery for newtlab ssh

**Tool**: newtlab
**Effort**: Low

`newtlab ssh <node>` requires knowing exact node names. On "node not found",
the error gives no hints.

**Changes:**
- Add `ValidArgsFunction` for shell completion of node names
- On "not found", list available nodes: `"node 'foo' not found; available: leaf1, spine1"`
- Prefix matching: `newtlab ssh leaf` matches `leaf1` if unambiguous

**Files**: `cmd/newtlab/cmd_ssh.go`, `cmd/newtlab/cmd_stop.go`, `cmd/newtlab/cmd_console.go`

---

## 3. Remove vestigial newtlab bridge command

**Tool**: newtlab
**Effort**: Low

The hidden `newtlab bridge` subcommand is a leftover from before newtlink
was extracted as a standalone binary. `startBridgeProcess()` in
`pkg/newtlab/bridge.go` still falls back to it if the `newtlink` binary
is not found. Remove the command and the fallback once newtlink is the
sole bridge mechanism.

**Files**: `cmd/newtlab/cmd_bridge.go`, `pkg/newtlab/bridge.go`

---

## 4. Consistent -S/--specs flag across tools

**Tool**: newtest
**Effort**: Low

newtron and newtlab both have a global `-S`/`--specs` flag. newtest does not —
it uses `--dir` on individual subcommands and hardcodes `newtest/topologies`
as the default. Either add `-S` globally to newtest or document why the
resolution differs.

**Files**: `cmd/newtest/main.go`, `cmd/newtest/helpers.go`
