# newtest Low-Level Design (LLD)

newtest is an E2E test orchestrator for newtron and SONiC. It parses YAML scenario files, deploys VM topologies via newtlab, provisions devices via newtron, and runs multi-step verification sequences. This document covers `pkg/newtest/` and `cmd/newtest/`.

For the high-level architecture, see [newtest HLD](hld.md). For the device connection layer, see [Device Layer LLD](../newtron/device-lld.md).

---

## 1. Package Structure

```
newtron/
├── cmd/
│   └── newtest/
│       ├── main.go               # Entry point, root command, exit code mapping
│       ├── helpers.go            # resolveDir, resolveSuite, suitesBaseDir
│       ├── cmd_start.go          # start subcommand (+ deprecated run alias)
│       ├── cmd_pause.go          # pause subcommand
│       ├── cmd_stop.go           # stop subcommand
│       ├── cmd_status.go         # status subcommand
│       ├── cmd_list.go           # list subcommand (suites + scenarios)
│       ├── cmd_suites.go         # suites subcommand (hidden alias for list)
│       └── cmd_topologies.go     # topologies subcommand
├── pkg/
│   └── newtest/
│       ├── scenario.go           # Scenario, Step, StepAction, ExpectBlock types
│       ├── parser.go             # ParseScenario, ValidateScenario, TopologicalSort
│       ├── runner.go             # Runner, RunOptions, Run, iterateScenarios
│       ├── steps.go              # StepExecutor interface, 42 executor implementations
│       ├── deploy.go             # DeployTopology, EnsureTopology, DestroyTopology
│       ├── state.go              # RunState, ScenarioState, SuiteStatus, persistence
│       ├── progress.go           # ProgressReporter, ConsoleProgress, StateReporter
│       ├── errors.go             # InfraError, StepError, PauseError
│       ├── report.go             # ScenarioResult, StepResult, StepStatus, ReportGenerator
│       └── newtest_test.go       # Unit tests
└── newtest/                      # E2E test assets
    ├── topologies/
    │   ├── 2node/specs/          # 2-node topology spec dir
    │   └── 4node/specs/          # 4-node topology spec dir
    ├── suites/                   # Test suites
    │   ├── 2node-standalone/     # Standalone scenario files
    │   └── 2node-incremental/    # 31 scenarios, dependency-ordered
    └── .generated/               # Runtime output (gitignored)
```

---

## 2. Core Types (`scenario.go`)

### 2.1 Scenario

```go
type Scenario struct {
    Name             string   `yaml:"name"`
    Description      string   `yaml:"description"`
    Topology         string   `yaml:"topology"`
    Platform         string   `yaml:"platform"`
    Requires         []string `yaml:"requires,omitempty"`
    After            []string `yaml:"after,omitempty"`
    RequiresFeatures []string `yaml:"requires_features,omitempty"`
    Repeat           int      `yaml:"repeat,omitempty"`
    Steps            []Step   `yaml:"steps"`
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Unique scenario identifier (matches filename without `.yaml`) |
| `description` | yes | Human-readable description shown in `newtest list` |
| `topology` | yes | Topology directory name under `newtest/topologies/` |
| `platform` | yes | Platform name from `platforms.json` in the topology spec dir |
| `requires` | no | List of scenario names that must pass before this one runs (suite mode) |
| `repeat` | no | Run all steps N times; 0 or omitted means once. Fail-fast per iteration. |
| `steps` | yes | Ordered list of test steps |

### 2.2 Step

```go
type Step struct {
    Name      string         `yaml:"name"`
    Action    StepAction     `yaml:"action"`
    Devices   deviceSelector `yaml:"devices,omitempty"`

    Duration  time.Duration  `yaml:"duration,omitempty"`      // wait
    Table     string         `yaml:"table,omitempty"`          // verify-config-db, verify-state-db
    Key       string         `yaml:"key,omitempty"`            // verify-config-db, verify-state-db
    Prefix    string         `yaml:"prefix,omitempty"`         // verify-route
    VRF       string         `yaml:"vrf,omitempty"`            // verify-route
    Interface string         `yaml:"interface,omitempty"`      // apply-service, remove-service, etc.
    Service   string         `yaml:"service,omitempty"`        // apply-service, restart-service
    Params    map[string]any `yaml:"params,omitempty"`         // action-specific parameters
    Command   string         `yaml:"command,omitempty"`        // ssh-command
    Target    string         `yaml:"target,omitempty"`         // verify-ping
    Count     int            `yaml:"count,omitempty"`          // verify-ping
    Expect    *ExpectBlock   `yaml:"expect,omitempty"`         // verify-*, ssh-command
}
```

Step is intentionally a flat union — all action-specific fields live on one
struct. Validation of which fields are required for which action happens
lazily via `validateStepFields` during execution (see §3.2).

### 2.3 StepAction Constants

```go
type StepAction string

const (
    ActionProvision          StepAction = "provision"
    ActionWait               StepAction = "wait"
    ActionVerifyProvisioning StepAction = "verify-provisioning"
    ActionVerifyConfigDB     StepAction = "verify-config-db"
    ActionVerifyStateDB      StepAction = "verify-state-db"
    ActionVerifyBGP          StepAction = "verify-bgp"
    ActionVerifyHealth       StepAction = "verify-health"
    ActionVerifyRoute        StepAction = "verify-route"
    ActionVerifyPing         StepAction = "verify-ping"
    ActionApplyService       StepAction = "apply-service"
    ActionRemoveService      StepAction = "remove-service"
    ActionConfigureLoopback  StepAction = "configure-loopback"
    ActionSSHCommand         StepAction = "ssh-command"
    ActionRestartService     StepAction = "restart-service"
    ActionApplyFRRDefaults   StepAction = "apply-frr-defaults"
    ActionSetInterface       StepAction = "set-interface"
    ActionCreateVLAN         StepAction = "create-vlan"
    ActionDeleteVLAN         StepAction = "delete-vlan"
    ActionAddVLANMember      StepAction = "add-vlan-member"
    ActionCreateVRF          StepAction = "create-vrf"
    ActionDeleteVRF          StepAction = "delete-vrf"
    ActionSetupEVPN          StepAction = "setup-evpn"
    ActionAddVRFInterface    StepAction = "add-vrf-interface"
    ActionRemoveVRFInterface StepAction = "remove-vrf-interface"
    ActionBindIPVPN          StepAction = "bind-ipvpn"
    ActionUnbindIPVPN        StepAction = "unbind-ipvpn"
    ActionBindMACVPN         StepAction = "bind-macvpn"
    ActionUnbindMACVPN       StepAction = "unbind-macvpn"
    ActionAddStaticRoute     StepAction = "add-static-route"
    ActionRemoveStaticRoute  StepAction = "remove-static-route"
    ActionRemoveVLANMember   StepAction = "remove-vlan-member"
    ActionApplyQoS           StepAction = "apply-qos"
    ActionRemoveQoS          StepAction = "remove-qos"
    ActionConfigureSVI       StepAction = "configure-svi"
    ActionBGPAddNeighbor     StepAction = "bgp-add-neighbor"
    ActionBGPRemoveNeighbor  StepAction = "bgp-remove-neighbor"
    ActionRefreshService     StepAction = "refresh-service"
    ActionCleanup            StepAction = "cleanup"
    ActionCreatePortChannel  StepAction = "create-portchannel"
    ActionDeletePortChannel  StepAction = "delete-portchannel"
    ActionAddPortChannelMember    StepAction = "add-portchannel-member"
    ActionRemovePortChannelMember StepAction = "remove-portchannel-member"
)
```

### 2.4 deviceSelector

```go
type deviceSelector struct {
    All     bool
    Devices []string
}

func (ds *deviceSelector) UnmarshalYAML(unmarshal func(any) error) error
func (ds *deviceSelector) Resolve(allDevices []string) []string
```

Handles two YAML forms: `devices: all` sets `All: true`; `devices: [leaf1, leaf2]`
populates `Devices`. `Resolve` returns the concrete device list — if `All` is true,
returns all devices sorted by name for deterministic ordering.

### 2.5 ExpectBlock

```go
type ExpectBlock struct {
    MinEntries   *int              `yaml:"min_entries,omitempty"`
    Exists       *bool             `yaml:"exists,omitempty"`
    Fields       map[string]string `yaml:"fields,omitempty"`
    Timeout      time.Duration     `yaml:"timeout,omitempty"`
    PollInterval time.Duration     `yaml:"poll_interval,omitempty"`
    State        string            `yaml:"state,omitempty"`
    Overall      string            `yaml:"overall,omitempty"`
    Protocol     string            `yaml:"protocol,omitempty"`
    NextHopIP    string            `yaml:"nexthop_ip,omitempty"`
    Source       string            `yaml:"source,omitempty"`
    SuccessRate  *float64          `yaml:"success_rate,omitempty"`
    Contains     string            `yaml:"contains,omitempty"`
}
```

**Default values per action:**

| Action | Timeout | PollInterval | Other Defaults |
|--------|---------|--------------|----------------|
| `verify-state-db` | 120s | 5s | — |
| `verify-bgp` | 120s | 5s | State: `"Established"` |
| `verify-route` | 60s | 5s | Source: `"app_db"` |
| `verify-ping` | 30s | — | Count: 5, SuccessRate: 1.0 |

---

## 3. Parser (`parser.go`)

### 3.1 ParseScenario

```go
func ParseScenario(path string) (*Scenario, error)
func ParseAllScenarios(dir string) ([]*Scenario, error)
func ValidateDependencyGraph(scenarios []*Scenario) ([]*Scenario, error)
func topologicalSort(scenarios []*Scenario) ([]*Scenario, error)
```

**ParseScenario flow:**

1. Read file at `path`
2. `yaml.Unmarshal` into `Scenario`
3. Apply defaults to steps (timeout, poll_interval, count)
4. Return `*Scenario`

**ParseAllScenarios**: reads all `.yaml` files in `dir`, returns parsed scenarios.
Used when running all scenarios in a suite and when resolving a specific scenario.

**ValidateDependencyGraph**: validates all `requires` references exist and there are
no cycles. Returns scenarios in topological order (Kahn's algorithm) on success.

**topologicalSort**: returns scenarios sorted in dependency order. Scenarios with
no dependencies come first.

### 3.2 validateStepFields

Checks that a scenario is well-formed: name is non-empty, topology directory
exists, platform exists in `platforms.json`, each step has a valid action, and
required fields are present per action type. Validation happens lazily during
execution rather than upfront.

**Required fields per action:**

| Action | Required Fields |
|--------|----------------|
| `provision` | `devices` |
| `wait` | `duration` |
| `verify-provisioning` | `devices` |
| `verify-config-db` | `devices`, `table`, `expect` (one of `min_entries`, `exists`, `fields`) |
| `verify-state-db` | `devices`, `table`, `key`, `expect.fields` |
| `verify-bgp` | `devices` |
| `verify-health` | `devices` |
| `verify-route` | `devices` (single), `prefix`, `vrf` |
| `verify-ping` | `devices` (single), `target` |
| `apply-service` | `devices`, `interface`, `service` |
| `remove-service` | `devices`, `interface` |
| `configure-loopback` | `devices` |
| `ssh-command` | `devices`, `command` |
| `restart-service` | `devices`, `service` |
| `apply-frr-defaults` | `devices` |
| `set-interface` | `devices`, `interface`, `params.property` |
| `create-vlan` | `devices`, `vlan_id` |
| `delete-vlan` | `devices`, `vlan_id` |
| `add-vlan-member` | `devices`, `vlan_id`, `params.interface` |
| `create-vrf` | `devices`, `params.vrf` |
| `delete-vrf` | `devices`, `params.vrf` |
| `setup-evpn` | `devices`, `params.source_ip` |
| `add-vrf-interface` | `devices`, `params.vrf`, `params.interface` |
| `remove-vrf-interface` | `devices`, `params.vrf`, `params.interface` |
| `bind-ipvpn` | `devices`, `params.vrf`, `params.ipvpn` |
| `unbind-ipvpn` | `devices`, `params.vrf` |
| `bind-macvpn` | `devices`, `vlan_id`, `params.macvpn` |
| `unbind-macvpn` | `devices`, `vlan_id` |
| `add-static-route` | `devices`, `params.vrf`, `params.prefix`, `params.next_hop` |
| `remove-static-route` | `devices`, `params.vrf`, `params.prefix` |
| `apply-qos` | `devices`, `params.interface`, `params.qos_policy` |
| `remove-qos` | `devices`, `params.interface` |
| `configure-svi` | `devices`, `vlan_id` |
| `bgp-add-neighbor` | `devices`, `params.remote_asn` |
| `bgp-remove-neighbor` | `devices`, `params.neighbor_ip` |
| `refresh-service` | `devices`, `interface` |
| `cleanup` | `devices` |
| `remove-vlan-member` | `devices`, `vlan_id`, `params.interface` |
| `create-portchannel` | `devices`, `params.name`, `params.members` (list) |
| `delete-portchannel` | `devices`, `params.name` |
| `add-portchannel-member` | `devices`, `params.name`, `params.member` |
| `remove-portchannel-member` | `devices`, `params.name`, `params.member` |

---

## 4. Runner (`runner.go`)

### 4.1 Runner

```go
type Runner struct {
    ScenariosDir  string
    TopologiesDir string
    Network       *network.Network
    Lab           *newtlab.Lab
    ChangeSets    map[string]*node.ChangeSet
    HostConns     map[string]*ssh.Client
    Progress      ProgressReporter

    opts     RunOptions
    scenario *Scenario
}
```

| Field | Description |
|-------|-------------|
| `ScenariosDir` | Path to suite directory (e.g., `newtest/suites/2node-incremental`) |
| `TopologiesDir` | Path to `newtest/topologies/` |
| `Network` | Top-level `network.Network` object (owns nodes, specs). Nodes accessed via `r.Network.GetNode(name)`, platforms via `r.Network.GetPlatform()`. |
| `Lab` | newtlab Lab instance from deploy (nil when `--no-deploy`) |
| `ChangeSets` | Last ChangeSet per device name, accumulated from executor `StepOutput`. Last-write-wins: if multiple steps produce ChangeSets for the same device, only the latest is retained. Read by `verify-provisioning`. |
| `HostConns` | SSH client connections keyed by host name. Used by `host-exec` executor to run commands inside host network namespaces. |
| `Progress` | Progress reporter for lifecycle callbacks. When set, receives events for suite/scenario/step start and end. |

### 4.2 RunOptions

```go
type RunOptions struct {
    Scenario  string
    All       bool
    Topology  string
    Platform  string
    Keep      bool
    NoDeploy  bool
    Verbose   bool
    JUnitPath string

    // Lifecycle fields (set by `start` command, not by `run`)
    Suite     string                // suite name for state tracking; empty disables lifecycle
    Resume    bool                  // true when resuming a paused run
    Completed map[string]StepStatus // scenario → status from previous run (resume)
}
```

| Field | Description |
|-------|-------------|
| `Suite` | Suite name for state tracking. When non-empty, enables lifecycle mode: `EnsureTopology` for deploy, `CheckPausing` for pause, state persistence. |
| `Resume` | True when resuming a paused suite. Already-passed scenarios are skipped. |
| `Completed` | Status map from the previous run's state. Seeds `scenarioStatus` in `iterateScenarios` so resume knows which scenarios already passed. |
| `Keep` | Implicitly true in lifecycle mode (`start`). When true, topology is not destroyed after completion. |
| `NoDeploy` | Skip deploy/destroy. Used with `--no-deploy` flag (deprecated `run` command). |

### 4.3 Run

```go
func NewRunner(scenariosDir, topologiesDir string) *Runner
func (r *Runner) Run(opts RunOptions) ([]*ScenarioResult, error)
```

**Run** determines execution mode based on options:

1. **Single scenario** (`opts.Scenario` set): parse one scenario, run via `runIndependent`
2. **All scenarios** (`opts.All` set): parse all from `ScenariosDir`, validate dependency graph if any scenario has `Requires`, then:
   - If all scenarios share the same topology → `runShared` (deploy once)
   - Otherwise → `runIndependent` (per-scenario deploy)

### 4.4 iterateScenarios

```go
type scenarioRunner func(ctx context.Context, sc *Scenario, topology, platform string) (*ScenarioResult, error)

func (r *Runner) iterateScenarios(ctx context.Context, scenarios []*Scenario, opts RunOptions, deployedPlatform string, run scenarioRunner) ([]*ScenarioResult, error)
```

Encapsulates the common scenario iteration loop used by both `runShared` and
`runIndependent`. For each scenario:

1. **Resume**: skip already-passed scenarios from `opts.Completed`
2. **Pause check**: if `CheckPausing(opts.Suite)` returns true, return `PauseError`
3. **Requires check**: if any dependency failed/skipped → mark as `SKIP`
4. **Execute**: call the `run` callback
5. **Report**: emit progress events via `r.Progress`

### 4.5 Shared vs Independent Mode

**runShared**: deploys once via `deployTopology`, connects once, then iterates
all scenarios. Each scenario reuses the same `Runner.Network` and `Runner.Lab`.

**runIndependent**: iterates scenarios, calling the internal runner for each. Each
scenario gets its own deploy/connect cycle.

### 4.6 deployTopology

```go
func (r *Runner) deployTopology(ctx context.Context, specDir string, opts RunOptions) (cleanup func(), err error)
```

Dual behavior:
- **Lifecycle mode** (`opts.Suite != ""`): calls `EnsureTopology` — reuses running topology or deploys fresh. Returns nil cleanup (topology stays up; `stop` command handles teardown).
- **Legacy mode** (`opts.Suite == ""`): calls `DeployTopology` — always deploys fresh. Returns a deferred `DestroyTopology` cleanup unless `opts.Keep` is true.

### 4.7 runScenarioSteps

```go
func (r *Runner) runScenarioSteps(ctx context.Context, scenario *Scenario, opts RunOptions, result *ScenarioResult)
```

Executes steps within a scenario. When `scenario.Repeat > 1`, all steps execute
in a loop for N iterations. Each iteration is fail-fast. The
`ScenarioResult.FailedIteration` field records which iteration failed (1-based).

### 4.8 Device Connection

```go
func (r *Runner) connectDevices(ctx context.Context, specDir string) error
```

Builds the `network.Network` from the spec directory and connects all devices
via SSH. Uses `network.NewNetwork(specDir)` to load specs, then `dev.Connect(ctx)`
for each device. Connection failures are wrapped in `InfraError`.

### 4.9 Runner Helpers

```go
func (r *Runner) allDeviceNames() []string
func (r *Runner) resolveDevices(step *Step) []string
func (r *Runner) hasDataplane() bool
func HasRequires(scenarios []*Scenario) bool
```

`hasDataplane` checks if the scenario platform supports data plane forwarding.
Reads `PlatformSpec.Dataplane` — returns true when non-empty.

---

## 5. Lifecycle (`state.go`)

### 5.1 SuiteStatus

```go
type SuiteStatus string

const (
    SuiteStatusRunning  SuiteStatus = "running"
    SuiteStatusPausing  SuiteStatus = "pausing"
    SuiteStatusPaused   SuiteStatus = "paused"
    SuiteStatusComplete SuiteStatus = "complete"
    SuiteStatusAborted  SuiteStatus = "aborted"
    SuiteStatusFailed   SuiteStatus = "failed"
)
```

### 5.2 RunState

```go
type RunState struct {
    Suite     string          `json:"suite"`
    SuiteDir  string          `json:"suite_dir"`
    Topology  string          `json:"topology"`
    Platform  string          `json:"platform"`
    PID       int             `json:"pid"`
    Status    SuiteStatus     `json:"status"`
    Started   time.Time       `json:"started"`
    Updated   time.Time       `json:"updated"`
    Finished  time.Time       `json:"finished,omitempty"`
    Scenarios []ScenarioState `json:"scenarios"`
}
```

Persisted to `~/.newtron/newtest/<suite>/state.json` via `SaveRunState`.

### 5.3 ScenarioState

```go
type ScenarioState struct {
    Name             string      `json:"name"`
    Description      string      `json:"description,omitempty"`
    Status           string      `json:"status"`                       // "PASS","FAIL","SKIP","ERROR","running","" (pending)
    Duration         string      `json:"duration"`                     // e.g. "2s", "15s"
    CurrentStep      string      `json:"current_step,omitempty"`       // step name while in-progress
    CurrentStepIndex int         `json:"current_step_index,omitempty"` // 0-based step index
    TotalSteps       int         `json:"total_steps,omitempty"`        // total steps in scenario
    Requires         []string    `json:"requires,omitempty"`           // dependency scenario names
    SkipReason       string      `json:"skip_reason,omitempty"`        // reason for skip
    Steps            []StepState `json:"steps,omitempty"`
}
```

### 5.4 StepState

```go
type StepState struct {
    Name     string `json:"name"`
    Action   string `json:"action"`
    Status   string `json:"status"`
    Duration string `json:"duration"`
    Message  string `json:"message,omitempty"`
}
```

### 5.5 State Functions

```go
func StateDir(suite string) (string, error)
func SuiteName(dir string) string
func SaveRunState(state *RunState) error
func LoadRunState(suite string) (*RunState, error)
func RemoveRunState(suite string) error
func ListSuiteStates() ([]string, error)
func AcquireLock(state *RunState) error
func ReleaseLock(state *RunState) error
func CheckPausing(suite string) bool
func IsProcessAlive(pid int) bool
```

| Function | Description |
|----------|-------------|
| `StateDir` | Returns `~/.newtron/newtest/<suite>/` |
| `SuiteName` | Extracts suite name from directory path via `filepath.Base` |
| `SaveRunState` | Writes state to `state.json`, updating `Updated` timestamp |
| `LoadRunState` | Reads state from `state.json`. Returns `nil, nil` if not found. |
| `RemoveRunState` | Deletes the entire suite state directory |
| `ListSuiteStates` | Returns names of all suites with state directories |
| `AcquireLock` | Checks for live PID in existing state; sets `state.PID = os.Getpid()` |
| `ReleaseLock` | Clears PID and saves state |
| `CheckPausing` | Returns true if the suite's status is `"pausing"` |
| `IsProcessAlive` | Checks if PID exists via `syscall.Kill(pid, 0)` |

### 5.5 Pause Flow

1. User runs `newtest pause` → reads state, sets `Status = SuiteStatusPausing`, saves
2. Running `iterateScenarios` checks `CheckPausing(suite)` before each scenario
3. When pausing detected, returns `PauseError{Completed: len(results)}`
4. `cmd_start.go` catches `PauseError`, sets `Status = SuiteStatusPaused`, saves

### 5.6 Resume Flow

1. User runs `newtest start <suite>` → `LoadRunState` finds paused state
2. CLI builds `RunOptions{Resume: true, Completed: map[name]status}` from saved state
3. `iterateScenarios` skips scenarios that already passed in `opts.Completed`
4. Execution continues from first non-passed scenario

---

## 6. Progress Reporting (`progress.go`)

### 6.1 ProgressReporter Interface

```go
type ProgressReporter interface {
    SuiteStart(scenarios []*Scenario)
    ScenarioStart(name string, index, total int)
    ScenarioEnd(result *ScenarioResult, index, total int)
    StepStart(scenario string, step *Step, index, total int)
    StepEnd(scenario string, result *StepResult, index, total int)
    SuiteEnd(results []*ScenarioResult, duration time.Duration)
}
```

The Runner calls these via `r.progress(func(p) { p.Method(...) })`, which
no-ops if `r.Progress` is nil.

### 6.2 consoleProgress

```go
type consoleProgress struct {
    W       io.Writer
    Verbose bool
    suiteName string
    dotWidth  int
}

func NewConsoleProgress(verbose bool) ProgressReporter
```

Append-only terminal progress reporter. Never uses ANSI cursor rewriting,
so output is safe for pipes, CI, and scrollback buffers.

- **SuiteStart**: prints scenario roster table (index, name, step count)
- **ScenarioEnd**: one dot-padded line per scenario with PASS/FAIL/SKIP/ERROR
- **StepEnd**: only shown in verbose mode; includes per-device failure details
- **SuiteEnd**: summary line with pass/fail/skip/error counts and total duration

### 6.3 StateReporter

```go
type StateReporter struct {
    Inner ProgressReporter
    State *RunState
    scenarioIndex int
}
```

Wraps a `ProgressReporter` and persists `RunState` after each lifecycle event:

- **SuiteStart**: initializes `ScenarioState` entries with metadata (name, total steps, requires)
- **ScenarioStart**: sets scenario status to `"running"`
- **ScenarioEnd**: records final status, duration, skip reason
- **StepStart**: records current step name and index (enables progress display in `status`)
- **SuiteEnd**: final state save

All callbacks delegate to `Inner` after saving state. Save failures are logged
as warnings but do not abort execution.

---

## 7. Step Executors (`steps.go`)

### 7.1 Executor Interface

```go
type stepExecutor interface {
    Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
}

type StepOutput struct {
    Result     *StepResult
    ChangeSets map[string]*node.ChangeSet
}
```

All executors are stateless — mutable state lives in the Runner. Executors
read from Runner (e.g., Network, ChangeSets) but return `StepOutput` rather
than writing directly. The Runner merges ChangeSets after each step.

### 7.2 Executor Dispatch

```go
var executors = map[StepAction]stepExecutor{ ... } // 42 entries

func (r *Runner) executeStep(ctx context.Context, step *Step, index, total int, opts RunOptions) *StepOutput
```

`executeStep` looks up the executor, calls `Execute`, sets duration and name
on the result, and aggregates per-device error details into the `Message` field
when executors only set `Details`.

### 7.3 Multi-Device Helpers

```go
func (r *Runner) executeForDevices(step *Step, fn func(dev *node.Node, name string) (*node.ChangeSet, string, error)) *StepOutput
func (r *Runner) checkForDevices(step *Step, fn func(dev *node.Node, name string) (StepStatus, string)) *StepOutput
func (r *Runner) pollForDevices(ctx context.Context, step *Step, fn func(dev *node.Node, name string) (done bool, msg string, err error)) *StepOutput
```

Three patterns used by executors:

- **executeForDevices**: for mutating actions (provision, apply-service, create-vlan, etc.). Collects ChangeSets per device.
- **checkForDevices**: for single-shot verification (verify-health, verify-config-db). Returns per-device status.
- **pollForDevices**: for polling verification (verify-bgp, verify-state-db, verify-route). Polls until timeout.

### 7.4 Param Helpers

```go
func strParam(params map[string]any, key string) string
func intParam(params map[string]any, key string) int
func boolParam(params map[string]any, key string) bool
```

Extract typed values from `step.Params`. Used by all operation executors to
read action-specific parameters from YAML. `intParam` handles int, float64
(from YAML), and string (via strconv).

### 7.5 Executor Summary

| # | Executor | Action | Wraps |
|---|----------|--------|-------|
| 1 | `provisionExecutor` | `provision` | `TopologyProvisioner.ProvisionDevice()` |
| 2 | `waitExecutor` | `wait` | `time.Sleep` |
| 3 | `verifyProvisioningExecutor` | `verify-provisioning` | `Node.VerifyChangeSet()` |
| 4 | `verifyConfigDBExecutor` | `verify-config-db` | `ConfigDBClient.Get*()` |
| 5 | `verifyStateDBExecutor` | `verify-state-db` | `StateDBClient.GetEntry()` with polling |
| 6 | `verifyBGPExecutor` | `verify-bgp` | `RunHealthChecks("bgp")` with polling |
| 7 | `verifyHealthExecutor` | `verify-health` | `RunHealthChecks()` (single-shot) |
| 8 | `verifyRouteExecutor` | `verify-route` | `GetRoute()` / `GetRouteASIC()` with polling |
| 9 | `verifyPingExecutor` | `verify-ping` | SSH ping (newtest native) |
| 10 | `applyServiceExecutor` | `apply-service` | `Interface.ApplyService()` |
| 11 | `removeServiceExecutor` | `remove-service` | `Interface.RemoveService()` |
| 12 | `configureLoopbackExecutor` | `configure-loopback` | `Node.ConfigureLoopback()` |
| 13 | `sshCommandExecutor` | `ssh-command` | SSH exec (newtest native) |
| 14 | `restartServiceExecutor` | `restart-service` | `Node.RestartService()` |
| 15 | `applyFRRDefaultsExecutor` | `apply-frr-defaults` | `Node.ApplyFRRDefaults()` |
| 16 | `setInterfaceExecutor` | `set-interface` | `Interface.Set/SetIP/SetVRF()` |
| 17 | `createVLANExecutor` | `create-vlan` | `Node.CreateVLAN()` |
| 18 | `deleteVLANExecutor` | `delete-vlan` | `Node.DeleteVLAN()` |
| 19 | `addVLANMemberExecutor` | `add-vlan-member` | `Node.AddVLANMember()` |
| 20 | `createVRFExecutor` | `create-vrf` | `Node.CreateVRF()` |
| 21 | `deleteVRFExecutor` | `delete-vrf` | `Node.DeleteVRF()` |
| 22 | `setupEVPNExecutor` | `setup-evpn` | `Node.SetupEVPN()` |
| 23 | `addVRFInterfaceExecutor` | `add-vrf-interface` | `Node.AddVRFInterface()` |
| 24 | `removeVRFInterfaceExecutor` | `remove-vrf-interface` | `Node.RemoveVRFInterface()` |
| 25 | `bindIPVPNExecutor` | `bind-ipvpn` | `Node.BindIPVPN()` |
| 26 | `unbindIPVPNExecutor` | `unbind-ipvpn` | `Node.UnbindIPVPN()` |
| 27 | `bindMACVPNExecutor` | `bind-macvpn` | `Node.BindMACVPN()` |
| 28 | `unbindMACVPNExecutor` | `unbind-macvpn` | `Node.UnbindMACVPN()` |
| 29 | `addStaticRouteExecutor` | `add-static-route` | `Node.AddStaticRoute()` |
| 30 | `removeStaticRouteExecutor` | `remove-static-route` | `Node.RemoveStaticRoute()` |
| 31 | `removeVLANMemberExecutor` | `remove-vlan-member` | `Node.RemoveVLANMember()` |
| 32 | `applyQoSExecutor` | `apply-qos` | `Node.ApplyQoS()` |
| 33 | `removeQoSExecutor` | `remove-qos` | `Node.RemoveQoS()` |
| 34 | `configureSVIExecutor` | `configure-svi` | `Node.ConfigureSVI()` |
| 35 | `bgpAddNeighborExecutor` | `bgp-add-neighbor` | `Interface.AddBGPNeighbor` / `Node.AddLoopbackBGPNeighbor` |
| 36 | `bgpRemoveNeighborExecutor` | `bgp-remove-neighbor` | `Interface.RemoveBGPNeighbor` / `Node.RemoveBGPNeighbor` |
| 37 | `refreshServiceExecutor` | `refresh-service` | `Interface.RefreshService()` |
| 38 | `cleanupExecutor` | `cleanup` | `Node.Cleanup()` |
| 39 | `createPortChannelExecutor` | `create-portchannel` | `Node.CreatePortChannel()` |
| 40 | `deletePortChannelExecutor` | `delete-portchannel` | `Node.DeletePortChannel()` |
| 41 | `addPortChannelMemberExecutor` | `add-portchannel-member` | `Node.AddPortChannelMember()` |
| 42 | `removePortChannelMemberExecutor` | `remove-portchannel-member` | `Node.RemovePortChannelMember()` |
| 43 | `hostExecExecutor` | `host-exec` | SSH exec in host namespace (newtest native) |
| 44 | `configureBGPExecutor` | `configure-bgp` | `Node.ConfigureBGP()` |
| 45 | `createACLTableExecutor` | `create-acl-table` | `Node.CreateACLTable()` |
| 46 | `addACLRuleExecutor` | `add-acl-rule` | `Node.AddACLRule()` |
| 47 | `deleteACLRuleExecutor` | `delete-acl-rule` | `Node.DeleteACLRule()` |
| 48 | `deleteACLTableExecutor` | `delete-acl-table` | `Node.DeleteACLTable()` |
| 49 | `bindACLExecutor` | `bind-acl` | `Interface.BindACL()` |
| 50 | `unbindACLExecutor` | `unbind-acl` | `Node.UnbindACLFromInterface()` |
| 51 | `removeSVIExecutor` | `remove-svi` | `Node.RemoveSVI()` |
| 52 | `removeIPExecutor` | `remove-ip` | `Interface.RemoveIP()` |
| 53 | `teardownEVPNExecutor` | `teardown-evpn` | `Node.TeardownEVPN()` |
| 54 | `removeBGPGlobalsExecutor` | `remove-bgp-globals` | `Node.RemoveBGPGlobals()` |

### 7.6 Verification Executor Detail

**verifyConfigDBExecutor** — three assertion modes:
1. `expect.MinEntries`: count keys in table, pass if `≥ min`
2. `expect.Exists`: check if table/key exists or not
3. `expect.Fields`: read table/key, compare each field value

**verifyStateDBExecutor** — polls `StateDBClient.GetEntry(table, key)` until all
`expect.Fields` match or timeout is reached.

**verifyBGPExecutor** — polls `RunHealthChecks("bgp")` and optionally checks
per-neighbor state via STATE_DB. Health check runs first (fast fail); state
check runs second (per-neighbor granularity).

**verifyHealthExecutor** — single-shot read. Does **not** poll. Use a `wait`
step before `verify-health` if convergence time is needed.

**verifyRouteExecutor** — polls `GetRoute` (APP_DB) or `GetRouteASIC` (ASIC_DB)
until the route matches expected protocol, next-hop, and source.

**verifyPingExecutor** — checks `hasDataplane()` first; skips on platforms
without data plane. Resolves target to IP (device name → loopback IP), runs
`ping -c N -W 5 <target>` via SSH, parses packet loss from output.

### 7.7 BGP Neighbor Executor Dispatch

`bgpAddNeighborExecutor` dispatches based on `step.Interface`:
- **Interface set** → direct BGP neighbor via `Interface.AddBGPNeighbor`
- **Interface empty** → loopback BGP neighbor via `Node.AddLoopbackBGPNeighbor`

Same pattern for `bgpRemoveNeighborExecutor`.

---

## 8. newtlab Integration (`deploy.go`)

### 8.1 DeployTopology

```go
func DeployTopology(ctx context.Context, specDir string) (*newtlab.Lab, error)
```

Creates a `newtlab.Lab` from the spec directory, sets `lab.Force = true`,
calls `lab.Deploy(ctx)`, and returns the lab. After deploy, all VMs are
running and SSH-reachable with patched profiles.

### 8.2 EnsureTopology

```go
func EnsureTopology(ctx context.Context, specDir string) (*newtlab.Lab, error)
```

Reuses an existing lab if all nodes are running, otherwise deploys fresh.

**Flow:**
1. Create `newtlab.Lab` from spec dir
2. Check `lab.Status()` — if all nodes are `"running"`, return the existing lab
3. Otherwise, set `lab.Force = true`, call `lab.Deploy(ctx)`, and return the lab

Used by `start` command via `deployTopology()` when `opts.Suite` is set.

### 8.3 DestroyTopology

```go
func DestroyTopology(ctx context.Context, lab *newtlab.Lab) error
```

Calls `lab.Destroy(ctx)`. Returns nil if `lab` is nil. Used by the `stop`
command and by deferred cleanup in legacy (`run`) mode.

---

## 9. Results & Reporting (`report.go`)

### 9.1 StepStatus

```go
type StepStatus string

const (
    StepStatusPassed  StepStatus = "PASS"
    StepStatusFailed  StepStatus = "FAIL"
    StepStatusSkipped StepStatus = "SKIP"
    StepStatusError   StepStatus = "ERROR"
)
```

| Status | Meaning |
|--------|---------|
| `PASS` | Assertion matched |
| `FAIL` | Assertion failed (expected value mismatch, timeout) |
| `SKIP` | Skipped (platform lacks data plane, dependency failed) |
| `ERROR` | Infrastructure error (SSH failure, Redis timeout) |

`FAIL` vs `ERROR`: `FAIL` means the test ran but the assertion didn't hold.
`ERROR` means the test could not run. At the scenario level, `FAIL` takes
priority over `ERROR`.

### 9.2 ScenarioResult

```go
type ScenarioResult struct {
    Name            string
    Topology        string
    Platform        string
    Status          StepStatus
    Duration        time.Duration
    Steps           []StepResult
    DeployError     error
    SkipReason      string
    Repeat          int  // total iterations requested (0 = no repeat)
    FailedIteration int  // which iteration failed (0 = none; 1-based when Repeat > 1)
}
```

**Overall status** via `computeOverallStatus`:
- Any step `FAIL` → scenario `FAIL`
- Any step `ERROR` (no `FAIL`) → scenario `ERROR`
- All `PASS`/`SKIP` → scenario `PASS`

### 9.3 StepResult

```go
type StepResult struct {
    Name      string
    Action    StepAction
    Status    StepStatus
    Duration  time.Duration
    Message   string
    Device    string
    Details   []DeviceResult
    Iteration int  // 1-based iteration number (0 = no repeat)
}

type DeviceResult struct {
    Device  string
    Status  StepStatus
    Message string
}
```

Single-device steps set `Device` and `Message`. Multi-device steps populate
`Details` with per-device results.

### 9.4 ReportGenerator

```go
type ReportGenerator struct {
    Results []*ScenarioResult
}

func (g *ReportGenerator) WriteMarkdown(path string) error
func (g *ReportGenerator) WriteJUnit(path string) error
```

**WriteMarkdown**: summary table with scenario/topology/platform/result/duration/note
columns, followed by a failures section with per-step details.

**WriteJUnit**: JUnit XML. Each `ScenarioResult` → `<testsuite>`, each
`StepResult` → `<testcase>`. Iteration number is prepended to step names for
repeated scenarios.

---

## 10. Error Handling (`errors.go`)

### 10.1 Error Types

```go
type InfraError struct {
    Op     string // "deploy", "connect", "ssh"
    Device string // device name (or "" for topology-level)
    Err    error
}

type StepError struct {
    Step   string
    Action StepAction
    Err    error
}

type PauseError struct {
    Completed int
}
```

| Type | When | Message Format |
|------|------|---------------|
| `InfraError` | Deploy fails, SSH connect fails | `"newtest: deploy: <err>"` or `"newtest: connect leaf1: <err>"` |
| `StepError` | Unknown action, internal executor error | `"newtest: step provision-all (provision): <err>"` |
| `PauseError` | Suite paused between scenarios | `"paused after N scenarios"` |

All error types implement `error`. `InfraError` and `StepError` implement
`Unwrap() error` for `errors.Is`/`errors.As` compatibility.

### 10.2 Exit Codes

| Code | Trigger | Sentinel |
|------|---------|----------|
| 0 | All passed | `nil` |
| 1 | Test failure or unknown error | `errTestFailure` |
| 2 | Infrastructure error | `errInfraError` |

The `start` command's `RunE` returns sentinel errors (`errTestFailure`,
`errInfraError`) which `main()` maps to exit codes. Deferred cleanup
(lock release) runs before exit.

---

## 11. CLI (`cmd/newtest/`)

### 11.1 Command Tree

```go
func main() {
    rootCmd := &cobra.Command{Use: "newtest", ...}
    rootCmd.PersistentFlags().BoolVarP(&verboseFlag, "verbose", "v", false, "Verbose output")

    rootCmd.AddCommand(
        newStartCmd(),     // start [suite]
        newPauseCmd(),     // pause
        newStopCmd(),      // stop
        newStatusCmd(),    // status
        &runCmd,           // run [suite] (hidden, deprecated alias for start)
        newListCmd(),      // list [suite]
        newSuitesCmd(),    // suites (hidden alias for list)
        newTopologiesCmd(),// topologies
        versionCmd,        // version
    )
}
```

### 11.2 start Command

```go
func newStartCmd() *cobra.Command
```

**Flags:** `--dir`, `--scenario`, `--topology`, `--platform`, `--junit`

**Flow:**
1. Resolve suite directory from positional arg or `--dir`
2. Check for paused state → set `Resume: true` and populate `Completed` map
3. Build `RunState`, `AcquireLock`
4. Create `StateReporter` wrapping `ConsoleProgress`
5. Call `runner.Run(opts)`
6. Handle `PauseError` → save paused state
7. Determine final status (complete/failed), save state
8. Write markdown report and JUnit (if requested)
9. Return sentinel error for exit code mapping

**Lifecycle integration:**
- Always sets `Keep: true` (topology stays up)
- Always sets `NoDeploy: false` (EnsureTopology handles reuse)
- State is persisted at every lifecycle boundary

### 11.3 pause Command

```go
func newPauseCmd() *cobra.Command
```

**Flags:** `--dir`

Sets `state.Status = SuiteStatusPausing` and saves. The running `iterateScenarios`
loop detects this via `CheckPausing` and stops after the current scenario.

Validates: suite must exist, must be `running`, runner PID must be alive.

### 11.4 stop Command

```go
func newStopCmd() *cobra.Command
```

**Flags:** `--dir`

1. Load state, refuse if runner PID is alive (use `pause` first)
2. Resolve topology from state, destroy via `lab.Destroy(ctx)`
3. Remove state directory via `RemoveRunState`

### 11.5 status Command

```go
func newStatusCmd() *cobra.Command
```

**Flags:** `--dir`, `--json`

Without `--dir`: lists all suites from `ListSuiteStates()`. With `--dir`: shows
detailed status for one suite including topology liveness check, per-scenario
table with progress/step info, and summary counts.

JSON mode outputs `RunState` (single suite) or `[]RunState` (all suites).

### 11.6 list Command

```go
func newListCmd() *cobra.Command
```

**Flags:** `--dir`

- No args: `listSuites()` — tabwriter table with suite name, scenario count, topology, devices, links
- With suite name: `listScenarios(dir)` — tabwriter table with index, scenario name, steps, topology, requires

### 11.7 topologies Command

Lists directories under `newtest/topologies/`.

### 11.8 version Command

Prints version and git commit from `pkg/version/`.

### 11.9 Helpers (`helpers.go`)

```go
func resolveDir(cmd *cobra.Command, flagVal string, args ...string) string
func resolveSuiteName(name string) string
func suitesBaseDir() string
func resolveTopologiesDir() string
func resolveSuite(cmd *cobra.Command, dir string, filter func(SuiteStatus) bool) (string, error)
func resolveTopologyFromState(state *RunState) string
```

`resolveDir` handles positional arg → `--dir` override → default. Suite names
are resolved under `newtest/suites/` or used as paths directly.

`resolveSuite` auto-detects the active suite when `--dir` is omitted by
scanning `ListSuiteStates()` and filtering by status.
