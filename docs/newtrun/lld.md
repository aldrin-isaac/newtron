# newtrun Low-Level Design (LLD)

newtrun is an E2E test orchestrator for newtron and SONiC. It parses YAML scenario files, deploys VM topologies via newtlab, orchestrates device operations via an HTTP client that talks to newtron-server, and runs multi-step verification sequences. This document covers `pkg/newtrun/` and `cmd/newtrun/`.

For the high-level architecture, see [newtrun HLD](hld.md). For the device connection layer, see [Device Layer LLD](../newtron/device-lld.md). For the newtron-server HTTP API, see [newtron API](../newtron/api.md).

**Architectural note**: newtrun is a pure HTTP client. It does not import `pkg/newtron/network/`, `pkg/newtron/network/node/`, or `pkg/newtron/device/sonic/`. All SONiC device operations go through `r.Client` (`pkg/newtron/client.Client`), which talks to newtron-server over HTTP. The server handles device connections, CONFIG_DB access, spec resolution, and ChangeSet application.

---

## 1. Package Structure

```
newtron/
├── cmd/
│   └── newtrun/
│       ├── main.go               # Entry point, root command, exit code mapping
│       ├── helpers.go            # resolveDir, resolveSuite, suitesBaseDir, resolveTopologiesDir
│       ├── cmd_start.go          # start subcommand (+ deprecated run alias)
│       ├── cmd_pause.go          # pause subcommand
│       ├── cmd_stop.go           # stop subcommand
│       ├── cmd_status.go         # status subcommand
│       ├── cmd_list.go           # list subcommand (suites + scenarios)
│       ├── cmd_suites.go         # suites subcommand (hidden alias for list)
│       ├── cmd_topologies.go     # topologies subcommand
│       └── cmd_actions.go        # actions subcommand (action metadata + detail view)
├── pkg/
│   └── newtrun/
│       ├── scenario.go           # Scenario, Step, StepAction constants, ExpectBlock, PollBlock, BatchCall
│       ├── parser.go             # ParseScenario, stepValidations table, ValidateDependencyGraph
│       ├── runner.go             # Runner (with Client, ServerURL, NetworkID), RunOptions, Run
│       ├── steps.go              # stepExecutor interface, StepOutput, multi-device helpers, provision/wait/verify-provisioning executors
│       ├── steps_newtron.go      # newtronExecutor: URL expansion, jq evaluation, one-shot/polling/batch modes
│       ├── steps_host.go         # hostExecExecutor, shellQuote, runSSHCommand
│       ├── deploy.go             # DeployTopology, EnsureTopology, DestroyTopology
│       ├── state.go              # RunState, ScenarioState, SuiteStatus, persistence
│       ├── state_test.go         # Unit tests for state functions
│       ├── progress.go           # ProgressReporter, consoleProgress, StateReporter
│       ├── errors.go             # InfraError, StepError, PauseError
│       ├── report.go             # ScenarioResult, StepResult, StepStatus, ReportGenerator
│       └── newtrun_test.go       # Unit tests
└── newtrun/                      # E2E test assets
    ├── topologies/
    │   ├── 1node-vs/specs/            # Single-switch topology
    │   ├── 2node-ngdp/specs/          # 2-switch + 6-host topology
    │   ├── 2node-ngdp-service/specs/  # 2-switch + 8-host topology (service testing)
    │   ├── 2node-vs/specs/            # 2-switch + 6-host topology (sonic-vs)
    │   ├── 2node-vs-service/specs/    # 2-switch + 8-host topology (sonic-vs service testing)
    │   ├── 3node-ngdp/specs/          # 1-spine + 2-leaf + 2-host topology (EVPN dataplane)
    │   └── 4node-ngdp/specs/          # 4-node topology
    ├── suites/
    │   ├── 1node-vs-basic/            # Single-switch basics (4 scenarios)
    │   ├── 2node-ngdp-primitive/      # Disaggregated operation tests (21 scenarios)
    │   ├── 2node-ngdp-service/        # Service lifecycle tests (6 scenarios)
    │   ├── 2node-vs-primitive/        # Disaggregated operation tests, sonic-vs (21 scenarios)
    │   ├── 2node-vs-service/          # Service lifecycle tests, sonic-vs (6 scenarios)
    │   ├── 2node-vs-drift/            # Config drift detection tests (7 scenarios)
    │   ├── 2node-vs-zombie/           # Zombie intent detection tests (8 scenarios)
    │   ├── 3node-ngdp-dataplane/      # EVPN L2/L3 dataplane tests (8 scenarios)
    │   └── simple-vrf-host/           # Simple VRF with host verification (4 scenarios)
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
| `description` | yes | Human-readable description shown in `newtrun list` |
| `topology` | yes | Topology directory name under `newtrun/topologies/` |
| `platform` | yes | Platform name from `platforms.json` in the topology spec dir |
| `requires` | no | List of scenario names that must pass before this one runs (suite mode). A required scenario that fails or is skipped causes this scenario to be skipped. |
| `after` | no | List of scenario names that must run before this one (ordering only, no pass/fail gate). Both `requires` and `after` contribute to dependency ordering via topological sort. |
| `requires_features` | no | Platform features required (e.g., `["acl", "macvpn"]`). Scenarios are skipped if the deployed platform does not support a listed feature. |
| `repeat` | no | Run all steps N times; 0 or omitted means once. Fail-fast per iteration. |
| `steps` | yes | Ordered list of test steps |

### 2.2 Step

```go
type Step struct {
    Name    string         `yaml:"name"`
    Action  StepAction     `yaml:"action"`
    Devices deviceSelector `yaml:"devices,omitempty"`

    // wait
    Duration time.Duration `yaml:"duration,omitempty"`

    // host-exec, newtron (shared)
    Command string         `yaml:"command,omitempty"`
    Params  map[string]any `yaml:"params,omitempty"`

    // newtron (generic server action)
    Method string      `yaml:"method,omitempty"` // HTTP method: GET, POST, DELETE
    URL    string      `yaml:"url,omitempty"`    // URL template (e.g., /node/{{device}}/vlan)
    Poll   *PollBlock  `yaml:"poll,omitempty"`   // polling configuration
    Batch  []BatchCall `yaml:"batch,omitempty"`  // sequential batch of calls

    // All actions
    Expect        *ExpectBlock `yaml:"expect,omitempty"`
    ExpectFailure bool         `yaml:"expect_failure,omitempty"`
}
```

Step is intentionally a flat union — all action-specific fields live on one
struct. Validation of which fields are required for which action happens at
parse time via the declarative `stepValidations` table (see §3.2).

### 2.3 StepAction Constants

```go
type StepAction string

const (
    ActionProvision          StepAction = "provision"
    ActionWait               StepAction = "wait"
    ActionVerifyProvisioning StepAction = "verify-provisioning"
    ActionHostExec           StepAction = "host-exec"
    ActionNewtron            StepAction = "newtron"
)
```

Five action types. The `validActions` set is derived from the `executors` map
at init time, ensuring the two stay synchronized without manual maintenance.

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

### 2.5 PollBlock

```go
type PollBlock struct {
    Timeout  time.Duration `yaml:"timeout"`
    Interval time.Duration `yaml:"interval"`
}
```

Configures polling for the `newtron` action. The executor polls the URL at
`Interval` until the jq assertion passes or `Timeout` expires.

### 2.6 BatchCall

```go
type BatchCall struct {
    Method string         `yaml:"method"`
    URL    string         `yaml:"url"`
    Params map[string]any `yaml:"params,omitempty"`
}
```

A single HTTP call within a batch sequence. Batch calls execute sequentially
within a step, failing on the first error.

### 2.7 ExpectBlock

```go
type ExpectBlock struct {
    // Polling (used internally by newtronExecutor.executePoll to bridge poll: config to pollForDevices)
    Timeout      time.Duration `yaml:"timeout,omitempty"`
    PollInterval time.Duration `yaml:"poll_interval,omitempty"`

    // host-exec
    SuccessRate *float64 `yaml:"success_rate,omitempty"`
    Contains    string   `yaml:"contains,omitempty"`

    // newtron (generic server action) — jq expression evaluated against response body
    JQ string `yaml:"jq,omitempty"`
}
```

| Field | Used By | Description |
|-------|---------|-------------|
| `timeout` | `newtron` (internal) | Polling timeout; set by `newtronExecutor.executePoll` to bridge `step.Poll.Timeout` into `pollForDevices` |
| `poll_interval` | `newtron` (internal) | Polling interval; set by `newtronExecutor.executePoll` to bridge `step.Poll.Interval` into `pollForDevices` |
| `success_rate` | `host-exec` | Required ping success rate (0.0–1.0) |
| `contains` | `host-exec` | Substring match against command output |
| `jq` | `newtron` | jq expression evaluated against the JSON response body; must produce boolean `true` to pass |

---

## 3. Parser (`parser.go`)

### 3.1 ParseScenario

```go
func ParseScenario(path string) (*Scenario, error)
func ParseAllScenarios(dir string) ([]*Scenario, error)
func ValidateDependencyGraph(scenarios []*Scenario) ([]*Scenario, error)
```

**ParseScenario flow:**

1. Read file at `path`
2. `yaml.Unmarshal` into `Scenario`
3. `applyDefaults` — currently a no-op (empty function body). The five remaining actions require no default injection.
4. `validateStepFields` — declarative validation per step (see §3.2)
5. Return `*Scenario`

**ParseAllScenarios**: reads all `.yaml` files in `dir`, returns parsed scenarios.
Used when running all scenarios in a suite.

**ValidateDependencyGraph**: validates all `requires` and `after` references exist
and there are no cycles. Returns scenarios in topological order (Kahn's algorithm)
on success. Both `requires` and `after` contribute edges to the dependency graph;
`after` differs from `requires` only in that it does not gate on pass/fail.

### 3.2 validateStepFields

Declarative validation via the `stepValidations` table. Each action maps to a
`stepValidation` struct that declares device requirements, required step-level
fields, required params keys, and optional custom validation functions:

```go
type stepValidation struct {
    needsDevices  bool     // must have a device selector
    singleDevice  bool     // exactly one device required (implies needsDevices)
    fields        []string // required step-level fields: "command"
    params        []string // required params map keys
    custom        func(prefix string, step *Step) error
}
```

The `stepFieldGetter` table maps field names to accessor functions on `*Step`.
Currently it contains a single mapping: `"command"` → `s.Command`.

**Validation rules per action:**

| Action | Rule | Details |
|--------|------|---------|
| `provision` | `needsDevices: true` | |
| `wait` | custom | `duration` must be non-zero |
| `verify-provisioning` | `needsDevices: true` | |
| `host-exec` | `singleDevice: true`, `fields: ["command"]` | Exactly one device; `command` field required |
| `newtron` | custom | Either `url` or `batch` must be non-empty |

---

## 4. Runner (`runner.go`)

### 4.1 Runner

```go
type Runner struct {
    ScenariosDir  string
    TopologiesDir string
    ServerURL     string              // newtron-server HTTP address
    NetworkID     string              // network identifier for server operations
    Client        *client.Client      // HTTP client for all SONiC operations
    Lab           *newtlab.Lab
    Composites    map[string]string   // device name → composite handle UUID
    HostConns     map[string]*ssh.Client // host device name → SSH client
    Progress      ProgressReporter

    opts               RunOptions
    scenario           *Scenario
    discoveredPlatform string          // platform discovered from connected devices
}
```

| Field | Description |
|-------|-------------|
| `ScenariosDir` | Path to suite directory (e.g., `newtrun/suites/2node-ngdp-primitive`) |
| `TopologiesDir` | Path to `newtrun/topologies/` |
| `ServerURL` | newtron-server HTTP address (e.g., `http://localhost:8080`). Resolved from: `--server` flag → `NEWTRON_SERVER` env → settings → `newtron.DefaultServerURL`. |
| `NetworkID` | Network identifier passed to all server requests. Resolved from: `--network-id` flag → `NEWTRON_NETWORK_ID` env → settings → `newtron.DefaultNetworkID`. |
| `Client` | HTTP client (`pkg/newtron/client.Client`) created during `connectDevices`. All SONiC operations — provisioning, service lifecycle, CONFIG_DB queries, health checks, verification — go through this client. |
| `Lab` | newtlab Lab instance from deploy (nil when `--no-deploy`) |
| `Composites` | Last composite handle UUID per device name, accumulated from executor `StepOutput`. Last-write-wins: if multiple steps produce composite handles for the same device, only the latest is retained. Read by `verify-provisioning`. |
| `HostConns` | SSH client connections keyed by host device name. Used by `host-exec` executor to run commands inside host network namespaces. |
| `Progress` | Progress reporter for lifecycle callbacks. When set, receives events for suite/scenario/step start and end. |
| `discoveredPlatform` | Platform name discovered from the first non-host device profile after connection. Used as fallback for `hasDataplane()` and `checkPlatformFeatures()`. |

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
| `NoDeploy` | Skip deploy/destroy. Used with `--no-deploy` flag. |

### 4.3 Run

```go
func NewRunner(scenariosDir, topologiesDir string) *Runner
func (r *Runner) Run(opts RunOptions) ([]*ScenarioResult, error)
```

**Run** determines execution mode based on options:

1. **Single scenario** (`opts.Scenario` set): parse one scenario, run via `runIndependent`
2. **All scenarios** (`opts.All` set): parse all from `ScenariosDir`, validate dependency graph if any scenario has `Requires` or `After`, then:
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
3. **Requires check**: if any dependency failed/skipped → mark as `SKIP` with reason
4. **Feature check**: if platform doesn't support required features → mark as `SKIP`
5. **Execute**: call the `run` callback
6. **Report**: emit progress events via `r.Progress`

### 4.5 Shared vs Independent Mode

**runShared**: deploys once via `deployTopology`, connects once via `connectDevices`,
then iterates all scenarios. Each scenario reuses the same `Runner.Client` and
`Runner.Lab`. The `deployedPlatform` parameter is passed to feature checks so
all scenarios are evaluated against the actually deployed platform.

**runIndependent**: iterates scenarios, calling `runScenario` for each. Each
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

Creates the HTTP client and registers the network with newtron-server:

1. Create `client.Client` from `r.ServerURL` and `r.NetworkID`
2. Unregister first (`r.Client.UnregisterNetwork()`) to ensure fresh specs — different suites may use the same network ID with different spec dirs
3. Call `r.Client.RegisterNetwork(specDir)` — the server loads specs, topology, and platform definitions from the spec directory
4. Query `r.Client.TopologyDeviceNames()` to discover all devices in the topology
5. For each device, check `r.Client.IsHostDevice(name)`:
   - **Host devices**: establish a direct SSH connection via `connectHostSSH` and store in `r.HostConns`
   - **SONiC devices**: no pre-connection — the server connects on demand when an operation targets the device
6. Discover platform from the first non-host device's profile via `r.Client.DeviceInfo(name)` → store in `r.discoveredPlatform`

`connectHostSSH` resolves the host's SSH credentials (user, password, port, management IP) from the device profile via `r.Client.GetHostProfile(name)`, then dials SSH directly.

### 4.9 Runner Helpers

```go
func (r *Runner) allDeviceNames() []string
func (r *Runner) resolveDevices(step *Step) []string
func (r *Runner) hasDataplane() bool
func (r *Runner) resolvePlatform() string
func HasRequires(scenarios []*Scenario) bool
```

`allDeviceNames` queries `r.Client.TopologyDeviceNames()` for the current device list.

`hasDataplane` calls `r.Client.ShowPlatform(platformName)` and checks if
`PlatformSpec.Dataplane` is non-empty.

`resolvePlatform` returns the platform name from CLI override → scenario YAML → device discovery (in that priority order).

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

Persisted to `~/.newtron/newtrun/<suite>/state.json` via `SaveRunState`.

### 5.3 ScenarioState

```go
type ScenarioState struct {
    Name              string      `json:"name"`
    Description       string      `json:"description,omitempty"`
    Status            string      `json:"status"`                        // "PASS","FAIL","SKIP","ERROR","running","" (pending)
    Duration          string      `json:"duration"`
    CurrentStep       string      `json:"current_step,omitempty"`        // step name while in-progress
    CurrentStepAction string      `json:"current_step_action,omitempty"` // step action while in-progress
    CurrentStepIndex  int         `json:"current_step_index,omitempty"`  // 0-based step index
    TotalSteps        int         `json:"total_steps,omitempty"`
    Requires          []string    `json:"requires,omitempty"`
    SkipReason        string      `json:"skip_reason,omitempty"`
    Steps             []StepState `json:"steps,omitempty"`
}
```

`CurrentStepAction` enables the `status --detail` view to show which action type
is currently executing (e.g., "provision", "newtron"), not just the step name.

### 5.4 StepState

```go
type StepState struct {
    Name     string `json:"name"`
    Action   string `json:"action"`
    Status   string `json:"status"`   // "PASS","FAIL","SKIP","ERROR"
    Duration string `json:"duration"` // e.g. "2s", "<1s"
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
| `StateDir` | Returns `~/.newtron/newtrun/<suite>/` |
| `SuiteName` | Extracts suite name from directory path via `filepath.Base` |
| `SaveRunState` | Writes state to `state.json`, updating `Updated` timestamp |
| `LoadRunState` | Reads state from `state.json`. Returns `nil, nil` if not found. |
| `RemoveRunState` | Deletes the entire suite state directory |
| `ListSuiteStates` | Returns names of all suites with state directories. Only returns suites that have actual suite directories in the suites base directory. |
| `AcquireLock` | Checks for live PID in existing state; sets `state.PID = os.Getpid()` |
| `ReleaseLock` | Clears PID and saves state |
| `CheckPausing` | Returns true if the suite's status is `"pausing"` |
| `IsProcessAlive` | Checks if PID exists via `syscall.Kill(pid, 0)` |

### 5.6 Pause Flow

1. User runs `newtrun pause` → reads state, sets `Status = SuiteStatusPausing`, saves
2. Running `iterateScenarios` checks `CheckPausing(suite)` before each scenario
3. When pausing detected, returns `PauseError{Completed: len(results)}`
4. `cmd_start.go` catches `PauseError`, sets `Status = SuiteStatusPaused`, saves

### 5.7 Resume Flow

1. User runs `newtrun start <suite>` → `LoadRunState` finds paused state
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

- **SuiteStart**: initializes `ScenarioState` entries with metadata (name, description, total steps, requires)
- **ScenarioStart**: sets scenario status to `"running"`
- **ScenarioEnd**: records final status, duration, skip reason; clears `CurrentStep`/`CurrentStepAction`
- **StepStart**: records current step name, action, and index (enables progress display in `status --detail`)
- **StepEnd**: appends `StepState` with result to the current scenario (enables incremental detail view)
- **SuiteEnd**: final state save

All callbacks delegate to `Inner` after saving state. Save failures are logged
as warnings but do not abort execution.

---

## 7. Step Executors (`steps.go`, `steps_newtron.go`, `steps_host.go`)

### 7.1 Executor Interface

```go
type stepExecutor interface {
    Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
}

type StepOutput struct {
    Result     *StepResult
    Composites map[string]string  // device name → composite handle UUID
}
```

All executors are stateless — mutable state lives in the Runner. Executors
read from Runner (e.g., `r.Client`, `r.Composites`, `r.HostConns`) and return
`StepOutput` rather than writing directly. The Runner merges Composites after
each step.

### 7.2 Executor Dispatch

```go
var executors = map[StepAction]stepExecutor{
    ActionProvision:          &provisionExecutor{},
    ActionWait:               &waitExecutor{},
    ActionVerifyProvisioning: &verifyProvisioningExecutor{},
    ActionHostExec:           &hostExecExecutor{},
    ActionNewtron:            &newtronExecutor{},
}

func (r *Runner) executeStep(ctx context.Context, step *Step, index, total int, opts RunOptions) *StepOutput
```

`executeStep` looks up the executor, calls `Execute`, sets duration and name
on the result, and aggregates per-device error details into the `Message` field
when executors only set `Details`.

When `step.ExpectFailure` is true, `applyExpectFailure` inverts the result:
FAIL/ERROR becomes PASS (expected failure), PASS becomes FAIL (unexpected
success). If `expect.contains` is set, the error message must contain that
substring for the inversion to apply.

### 7.3 Multi-Device Helpers

```go
func (r *Runner) executeForDevices(step *Step, fn func(name string) (string, error)) *StepOutput
func (r *Runner) checkForDevices(step *Step, fn func(name string) (StepStatus, string)) *StepOutput
func (r *Runner) pollForDevices(ctx context.Context, step *Step, fn func(name string) (done bool, msg string, err error)) *StepOutput
```

Three patterns used by executors. Note the callback signatures — they receive
only the device `name` string. The callback body uses `r.Client` to perform
all operations against the server:

- **executeForDevices**: for mutating actions and one-shot newtron calls. Calls `fn` for each device in parallel.
- **checkForDevices**: for single-shot verification (verify-provisioning). Returns per-device status.
- **pollForDevices**: for polling verification (newtron polling mode). Polls until `step.Expect.Timeout` using `step.Expect.PollInterval`.

| Helper | Used By |
|--------|---------|
| `executeForDevices` | `provision`, `newtron` (one-shot mode, batch mode) |
| `checkForDevices` | `verify-provisioning` |
| `pollForDevices` | `newtron` (polling mode) |

**Host-skip behavior:** all three helpers check `r.HostConns[name]` before invoking the callback. If the device is a host, it is skipped with status `SKIP` and message "host device (SONiC operation/verification not applicable)". This means `devices: all` in a mixed switch+host topology automatically restricts SONiC operations to switches only. The only executor that targets hosts is `hostExecExecutor`, which uses `r.HostConns` directly.

### 7.4 pollUntil

```go
func pollUntil(ctx context.Context, timeout, interval time.Duration, fn func() (done bool, err error)) error
```

Low-level polling primitive used by `pollForDevices`. Calls `fn` at `interval`
until `fn` returns `done=true`, timeout expires, or `ctx` is cancelled.

### 7.5 Executor Summary

| # | Executor | Action | File | Client Call |
|---|----------|--------|------|-------------|
| 1 | `provisionExecutor` | `provision` | `steps.go` | `GenerateComposite` → `ConfigReload` → `RefreshWithRetry` → `DeliverComposite` → `Refresh` → `SaveConfig` |
| 2 | `waitExecutor` | `wait` | `steps.go` | — (context-aware `time.After`) |
| 3 | `verifyProvisioningExecutor` | `verify-provisioning` | `steps.go` | `VerifyComposite(name, handle)` |
| 4 | `hostExecExecutor` | `host-exec` | `steps_host.go` | — (direct SSH via `r.HostConns`) |
| 5 | `newtronExecutor` | `newtron` | `steps_newtron.go` | `RawRequest(method, path, body)` |

### 7.6 Provision Executor Detail

The `provisionExecutor` performs a multi-step sequence entirely via `r.Client`:

1. `r.Client.GenerateComposite(name)` → returns handle with UUID
2. `r.Client.ConfigReload(name)` — best-effort baseline restore (failure is non-fatal)
3. `r.Client.RefreshWithRetry(name, 60s)` — wait for SwSS readiness after reload
4. `r.Client.DeliverComposite(name, handle, "overwrite")` — server handles lock → deliver → unlock
5. `r.Client.Refresh(name)` — refresh cached CONFIG_DB and interface list
6. `r.Client.SaveConfig(name)` — persist to config_db.json for future config-reload steps

The handle UUID is stored in `StepOutput.Composites[name]` for later use by `verify-provisioning`.

### 7.7 Host Exec Executor (`steps_host.go`)

The `hostExecExecutor` is unique — it does not use `r.Client` at all. It runs
commands directly via the SSH connection stored in `r.HostConns`:

1. Resolve single device name from step
2. Look up `*ssh.Client` in `r.HostConns`
3. Wrap command in `ip netns exec <device> sh -c <quoted-cmd>` — the namespace
   name matches the device name, and `sh -c` ensures compound commands (pipes,
   semicolons) execute entirely inside the namespace
4. Execute via `runSSHCommand` (creates SSH session, runs `CombinedOutput`)
5. Check `expect.SuccessRate` (ping) or `expect.Contains` (string match) or bare exit code

`shellQuote` wraps strings in single quotes, escaping embedded single quotes.

### 7.8 newtronExecutor Detail (`steps_newtron.go`)

The `newtronExecutor` implements the generic `newtron` action — a single step type
that makes arbitrary HTTP calls to newtron-server. It replaces the 60+ dedicated
executors from the previous design with URL templates, jq-based assertions, and
three execution modes.

**URL template expansion** (`expandURL`):

```go
func expandURL(urlTemplate, networkID, device string) string
```

Two substitutions:
1. `{{device}}` → `url.PathEscape(device)` — per-device expansion
2. Implicit `/network/<networkID>` prefix — URLs in YAML are relative to the
   network (e.g., `/node/{{device}}/vlan` expands to
   `/network/mynet/node/switch1/vlan`)

**Three execution modes:**

1. **One-shot mode** (no `poll`, no `batch`):
   - If URL contains `{{device}}`: `executeForDevices` runs the call in parallel per device
   - If URL has no `{{device}}`: single network-scoped call (no device parallelism)

2. **Polling mode** (`poll:` block present):
   - If URL contains `{{device}}`: `pollForDevices` polls each device in parallel
   - If no `{{device}}`: `pollUntil` polls a network-scoped endpoint
   - Polls until the jq assertion passes or timeout expires
   - Errors during polling mean "not ready yet" — the poll continues

3. **Batch mode** (`batch:` list present):
   - Executes a sequence of HTTP calls within a single step
   - If any batch URL contains `{{device}}`: `executeForDevices` runs the full
     batch sequence per device (each device runs all calls in order)
   - If no `{{device}}`: batch runs once with no device scoping
   - Fails on the first error within the batch

**jq evaluation** (`evalJQ`):

```go
func evalJQ(expr string, data json.RawMessage, method, path string) (string, error)
```

Uses the `gojq` library to compile and run a jq expression against the JSON
response body. The expression must produce a single boolean `true` to pass.
Any other value (including `false`, `null`, a non-boolean) is a failure. When
no jq assertion is specified, a non-error HTTP response is sufficient to pass.

**doCall flow:**

```go
func (e *newtronExecutor) doCall(r *Runner, method, urlTemplate string, params map[string]any, device string, expect *ExpectBlock) (string, error)
```

1. `expandURL(urlTemplate, r.Client.NetworkID(), device)` — substitute `{{device}}` and prepend network prefix
2. Build request body from `params` for POST/PUT/DELETE methods
3. `r.Client.RawRequest(method, path, body)` — HTTP call to newtron-server
4. If `expect.JQ` is set: `evalJQ(expr, data, method, path)` — evaluate jq assertion
5. If no jq: return success message (`"GET /network/.../node/switch1/vlan: ok"`)

### 7.9 Executor Parameter Reference

| Action | Step Fields | Expect Fields | Notes |
|--------|-------------|---------------|-------|
| `provision` | — | — | Multi-step sequence (see §7.6); populates `StepOutput.Composites` |
| `wait` | **duration** | — | Context-aware sleep; no device interaction |
| `verify-provisioning` | — | — | Reads `r.Composites[device]` (UUID handle from prior `provision` step) |
| `host-exec` | **command** | `success_rate`, `contains` | Direct SSH via `r.HostConns`; wraps in `ip netns exec <device> sh -c` |
| `newtron` | **url** or **batch**, method, params | `jq` | Generic HTTP action; `poll:` block for polling; `batch:` for sequential calls |

### 7.10 YAML Examples

Representative YAML patterns covering the major test categories. All examples
are type-valid and can be copy-pasted into scenario files.

**Provisioning + verification** — the most common suite preamble:

```yaml
steps:
  - name: provision-switches
    action: provision
    devices: [switch1, switch2]

  - name: wait-convergence
    action: wait
    duration: 45s

  - name: verify-provisioning
    action: verify-provisioning
    devices: [switch1, switch2]
```

**SSH reachability check with polling** — verify device is SSH-reachable:

```yaml
steps:
  - name: ssh-echo-switch1
    action: newtron
    devices: [switch1]
    method: POST
    url: /node/{{device}}/ssh-command
    params: {command: "echo ok"}
    poll:
      timeout: 120s
      interval: 5s
    expect:
      jq: '.output | contains("ok")'
```

**VLAN creation and verification** — create L2 domain, verify in CONFIG_DB:

```yaml
steps:
  - name: create-vlan100
    action: newtron
    devices: [switch1]
    method: POST
    url: /node/{{device}}/vlan
    params: {id: 100}

  - name: add-member-untagged
    action: newtron
    devices: [switch1]
    method: POST
    url: /node/{{device}}/vlan/100/member
    params: {interface: Ethernet4, tagged: false}

  - name: verify-vlan100-exists
    action: newtron
    devices: [switch1]
    url: /node/{{device}}/configdb/VLAN/Vlan100/exists
    expect:
      jq: '.exists == true'

  - name: verify-member-untagged
    action: newtron
    devices: [switch1]
    url: /node/{{device}}/configdb/VLAN_MEMBER/Vlan100%7CEthernet4
    expect:
      jq: '.tagging_mode == "untagged"'
```

**Service apply via newtron action** — apply service to an interface:

```yaml
steps:
  - name: apply-transit
    action: newtron
    devices: [switch1]
    method: POST
    url: /node/{{device}}/interface/Ethernet2/apply-service
    params:
      service: transit
      ip_address: "10.10.1.1/31"
```

**BGP verification with polling** — poll until sessions are established:

```yaml
steps:
  - name: verify-bgp
    action: newtron
    devices: [switch1, switch2]
    url: /node/{{device}}/bgp/sessions
    poll:
      timeout: 120s
      interval: 5s
    expect:
      jq: '[.[] | select(.state != "Established")] | length == 0'
```

**Host dataplane verification** — configure host IP, ping gateway via `host-exec`:

```yaml
steps:
  - name: configure-host-ip
    action: host-exec
    devices: [host1]
    command: "ip addr add 192.168.1.10/24 dev eth0"

  - name: wait-arp
    action: wait
    duration: 3s

  - name: ping-gateway
    action: host-exec
    devices: [host1]
    command: "ping -c 5 -W 2 192.168.1.1"
    expect:
      success_rate: 0.80
```

**Expected failure** — verify that an operation fails with zombie intent:

```yaml
steps:
  - name: create-vlan-blocked-by-zombie
    action: newtron
    devices: [switch1]
    method: POST
    url: /node/{{device}}/vlan
    params: {id: 999}
    expect_failure: true
```

### 7.11 Worked Example: create-vlan via newtron

Tracing a single `newtron` step from YAML through every layer.

**1. YAML definition**

```yaml
- name: create-vlan100
  action: newtron
  devices: [switch1]
  method: POST
  url: /node/{{device}}/vlan
  params: {id: 100}
```

**2. Parser — `applyDefaults`** (`parser.go`). No-op — the `newtron` action
has no defaults to inject.

**3. Parser — `validateStepFields`** (`parser.go`). Looks up
`stepValidations[ActionNewtron]`:

```go
ActionNewtron: {custom: func(prefix string, step *Step) error {
    if step.URL == "" && len(step.Batch) == 0 {
        return fmt.Errorf("%s: newtron requires url or batch", prefix)
    }
    return nil
}}
```

`step.URL` is `"/node/{{device}}/vlan"` (non-empty) → validation passes. No
device or field validation is enforced by the parser — the `newtron` action
delegates device handling to the executor.

**4. Runner — `executeStep`** (`runner.go`). Looks up
`executors[ActionNewtron]` → `&newtronExecutor{}`. Calls
`executor.Execute(ctx, r, step)`.

**5. Executor — `newtronExecutor.Execute`** (`steps_newtron.go`). No `batch`,
no `poll`. `method` defaults to `"POST"` (from YAML). URL contains `{{device}}`
→ enters one-shot per-device mode: calls `r.executeForDevices(step, fn)`.

**6. Per-device callback — `doCall`**. For device `"switch1"`:

1. `expandURL("/node/{{device}}/vlan", "mynet", "switch1")` → `"/network/mynet/node/switch1/vlan"`
2. `params` is `{"id": 100}`, method is `POST` → `body = {"id": 100}`
3. `r.Client.RawRequest("POST", "/network/mynet/node/switch1/vlan", body)` → HTTP POST to newtron-server
4. No `expect.JQ` set → returns `"POST /network/mynet/node/switch1/vlan: ok"`

**7. Server** (`api/handler_node.go`). The handler resolves the node, calls
`n.CreateVLAN(ctx, 100)`. Returns JSON response.

**8. Result** — `executeForDevices` collects the per-device message into
`DeviceResult{Device: "switch1", Status: PASS, Message: "POST .../vlan: ok"}`.
Returns `StepOutput{Result: &StepResult{Status: PASS, Details: [...]}}`.

**9. Runner** — `executeStep` sets `result.Duration` and `result.Name`. Reports
progress via `r.Progress.StepEnd(...)`. The Runner advances to the next step.

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
| `SKIP` | Skipped (platform lacks data plane, dependency failed, feature unsupported) |
| `ERROR` | Infrastructure error (SSH failure, HTTP error, Redis timeout) |

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
    Details   []DeviceResult
    Iteration int  // 1-based iteration number (0 = no repeat)
}

type DeviceResult struct {
    Device  string
    Status  StepStatus
    Message string
}
```

Multi-device steps populate `Details` with per-device results. The Runner
aggregates error details into `Message` when executors only set `Details`.

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
repeated scenarios. Skipped scenarios with a skip reason produce a single
`<testcase>` with a `<skipped>` element.

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
| `InfraError` | Deploy fails, network registration fails, SSH connect fails | `"newtrun: deploy: <err>"` or `"newtrun: connect leaf1: <err>"` |
| `StepError` | Unknown action, internal executor error | `"newtrun: step provision-all (provision): <err>"` |
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

## 11. CLI (`cmd/newtrun/`)

### 11.1 Command Tree

```
newtrun
├── start [suite]          # deploy topology, run all scenarios
├── pause                  # stop after current scenario
├── stop                   # tear down topology and clean state
├── status                 # check progress
├── list [suite]           # show available suites / scenarios in a suite
├── suites                 # hidden alias for list
├── topologies             # show available topologies
├── actions [action]       # list all actions or show action details
├── run [suite]            # hidden, deprecated alias for start
└── version                # print version information
```

Global flag: `--verbose` / `-v` (all commands).

### 11.2 start Command

```
newtrun start [suite]
```

**Flags:** `--dir`, `--scenario`, `--topology`, `--platform`, `--junit`, `--server`, `--network-id`

**Server URL resolution chain:** `--server` flag → `NEWTRON_SERVER` env → `settings.GetServerURL()` → `newtron.DefaultServerURL`

**Network ID resolution chain:** `--network-id` flag → `NEWTRON_NETWORK_ID` env → `settings.GetNetworkID()` → `newtron.DefaultNetworkID`

**Flow:**
1. Resolve suite directory from positional arg or `--dir` (via `resolveDir`)
2. Check for paused state → set `Resume: true` and populate `Completed` map
3. Build `RunState`, `AcquireLock`
4. Create `StateReporter` wrapping `ConsoleProgress`
5. Resolve server URL and network ID via resolution chains
6. Set `runner.ServerURL` and `runner.NetworkID`
7. Call `runner.Run(opts)`
8. Handle `PauseError` → save paused state
9. Determine final status (complete/failed), save state
10. Write markdown report and JUnit (if requested)
11. Return sentinel error for exit code mapping

**Lifecycle integration:**
- Always sets `Keep: true` (topology stays up)
- Always sets `NoDeploy: false` (EnsureTopology handles reuse)
- State is persisted at every lifecycle boundary

### 11.3 pause Command

```
newtrun pause
```

**Flags:** `--dir`

Sets `state.Status = SuiteStatusPausing` and saves. The running `iterateScenarios`
loop detects this via `CheckPausing` and stops after the current scenario.

Validates: suite must exist, must be `running`, runner PID must be alive.

### 11.4 stop Command

```
newtrun stop
```

**Flags:** `--dir`

1. Load state, refuse if runner PID is alive (use `pause` first)
2. Resolve topology from state, destroy via `lab.Destroy(ctx)`
3. Remove state directory via `RemoveRunState`

### 11.5 status Command

```
newtrun status
```

**Flags:** `--dir`, `--json`, `--suite`/`-s`, `--detail`/`-d`, `--monitor`/`-m`

| Flag | Description |
|------|-------------|
| `--suite`/`-s` | Filter suites by substring match (case-insensitive) |
| `--detail`/`-d` | Show per-step timing and status for each scenario |
| `--monitor`/`-m` | Auto-refresh every 2s until suite finishes (implies `--detail`) |
| `--json` | Machine-readable JSON output |
| `--dir` | Specific suite by directory path |

Without `--dir`: lists all suites from `ListSuiteStates()`, optionally filtered by `--suite`. With `--dir`: shows detailed status for one suite including topology liveness check, per-scenario table with progress/step info, and summary counts.

**Detail view** (`--detail`): expands each scenario to show per-step results with action, status, duration, and truncated message. Running scenarios show the currently executing step (name + action) at the bottom.

**Monitor mode** (`--monitor`): clears screen and reprints status every 2 seconds until the suite is no longer `running` or `pausing`.

JSON mode outputs `RunState` (single suite) or `[]RunState` (all suites).

### 11.6 list Command

```
newtrun list [suite]
```

**Flags:** `--dir`

- No args: `listSuites()` — table with suite name, scenario count, topology, devices, links
- With suite name: `listScenarios(dir)` — table with index, scenario name, steps, topology, requires

### 11.7 topologies Command

Lists directories under `newtrun/topologies/`.

### 11.8 actions Command

```
newtrun actions [action]
```

- No args: lists all actions grouped by category (Provisioning, Verification, VLAN, VRF, EVPN, Service, QoS, BGP, ACL, PortChannel, Interface, Routing, Host, Utility)
- With action name: shows detailed information including category, description, prerequisites, required/optional parameters, devices requirement, and YAML example

The `ActionMetadata` struct and `getActionMetadata()` function provide structured
metadata for each action, manually maintained in sync with `stepValidations` in
`parser.go`.

**Note:** The `actions` command still lists all metadata entries from
`getActionMetadata()` in `cmd_actions.go`, which includes entries for the
former dedicated actions (create-vlan, verify-bgp, etc.) as reference
documentation. These metadata entries describe the server API endpoints that
the `newtron` action calls — the same operations are still available, just
accessed via URL templates rather than dedicated step actions.

### 11.9 version Command

Prints version and git commit from `pkg/version/`.

### 11.10 Helpers (`helpers.go`)

```go
func resolveDir(cmd *cobra.Command, flagVal string, args ...string) string
func resolveSuiteName(name string) string
func suitesBaseDir() string
func resolveTopologiesDir() string
func resolveSuite(cmd *cobra.Command, dir string, filter func(SuiteStatus) bool) (string, error)
func resolveTopologyFromState(state *RunState) string
```

**Suite directory resolution** (`resolveDir`): positional arg → `--dir` flag → `NEWTRUN_SUITE` env → `settings.DefaultSuite` → `"newtrun/suites/2node-ngdp-standalone"`. Bare names (no `/`) are resolved under `suitesBaseDir()`.

**Suites base directory** (`suitesBaseDir`): `NEWTRUN_SUITES_BASE` env → `"newtrun/suites"`.

**Topologies directory** (`resolveTopologiesDir`): `NEWTRUN_TOPOLOGIES` env → `settings.TopologiesDir` → `"newtrun/topologies"`.

`resolveSuite` auto-detects the active suite when `--dir` is omitted by
scanning `ListSuiteStates()` and filtering by status.

`resolveTopologyFromState` infers the topology name from suite state, falling
back to parsing scenario files if `state.Topology` is empty.
