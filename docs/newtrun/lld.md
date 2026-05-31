# newtrun Low-Level Design

This document specifies newtrun's type definitions, package structure, and code mechanics — the *how* and *what fields*. For the architectural *what* and *why* (run lifecycle, server-shutdown semantics, SSE design, strict Option A), see the [HLD](hld.md). For HTTP-client authoring (endpoints, payloads, status codes), see the [API reference](api.md). For CLI operation, see the [HOWTO](howto.md).

**Audience:** Engineers reading or modifying newtrun's source code. Field-by-field type tables, package boundaries, the data flow through each handler.

**Mental model:** newtrun is split into a thin CLI client (`bin/newtrun`) and a long-lived server (`bin/newtrun-server`). The CLI is a stateless HTTP client. The server owns the run registry, executes scenarios in goroutines, and streams progress over Server-Sent Events. Everything below is organized around that split.

---

## Table of Contents

1. [Package Structure](#1-package-structure)
2. [Core Types](#2-core-types-scenariogo)
3. [Parser](#3-parser-parsergo)
4. [State Persistence](#4-state-persistence-statego)
5. [Runner Internals](#5-runner-internals-runnergo)
6. [Progress Reporting](#6-progress-reporting-progressgo)
7. [HTTP Server Package](#7-http-server-package-pkgnewtrunapi)
8. [HTTP Client Package](#8-http-client-package-pkgnewtrunclient)
9. [Step Executors](#9-step-executors)
10. [newtlab Integration](#10-newtlab-integration-deploygo)
11. [Results & Reporting](#11-results--reporting-reportgo)
12. [Error Handling](#12-error-handling-errorsgo)
13. [CLI Binary](#13-cli-binary-cmdnewtrun)
14. [Server Binary](#14-server-binary-cmdnewtrun-server)

---

## 1. Package Structure

Three Go packages compose newtrun, one each for engine / server / client. The CLI binary and the server binary each have a `cmd/` package that wires the pieces together.

```
pkg/newtrun/                  # Engine (HTTP-agnostic orchestration core)
  scenario.go                 # Scenario, Step, StepAction, ExpectBlock, BatchCall
  parser.go                   # ParseScenario, ParseScenarioBytes, ValidateDependencyGraph
  runner.go                   # Runner, RunOptions, Run(ctx, opts), iterateScenarios
  steps.go                    # stepExecutor interface, multi-device helpers
  steps_newtron.go            # ActionNewtron: URL expansion, jq, polling, batch
  steps_cli.go                # ActionNewtronCLI: subprocess execution
  steps_host.go               # ActionHostExec: SSH command execution
  deploy.go                   # Deploy/Ensure/Destroy via newtlab
  state.go                    # RunState, ScenarioState, StepState; SuiteStatusFromOutcome
  progress.go                 # ProgressReporter (7 callbacks), consoleProgress, StateReporter
  errors.go                   # InfraError, StepError, PauseError
  report.go                   # ScenarioResult, StepResult, ReportGenerator (markdown + JUnit)

pkg/newtrun/api/              # HTTP server package
  server.go                   # Server, Config, route registration, handleHealth, listSubdirs
  middleware.go               # withRequestID, withLogger, withRecovery
  runs.go                     # All run handlers + reconcileStaleStatus + finalizers + newRunID
  suites.go                   # /api/suites endpoints + list-scenarios + nameRE
  scenarios.go                # GET/PUT/DELETE per-scenario + atomic write + path resolution
  topologies.go               # GET /api/topologies
  registry.go                 # RunRegistry, RegistryEntry, AlreadyRunningError
  safety.go                   # InlineSafetyPolicy, SafetyViolation
  reporter.go                 # HTTPReporter (implements ProgressReporter, publishes to broker)
  broker.go                   # EventBroker (SSE multiplexer, drop-on-full)
  types.go                    # APIResponse, EventType, payload types, request shapes

pkg/newtrun/client/           # HTTP client (used by CLI and future browser-side adapter)
  client.go                   # Client struct, all endpoint methods, StreamEvents SSE parser

cmd/newtrun/                  # CLI binary (thin HTTP-client surface)
  main.go                     # Root command, --newtrun-server flag, --verbose
  clientutil.go               # newClient factory, requireServer probe
  helpers.go                  # resolveSuite, resolveTopologyFromState
  cmd_start.go                # POST /api/runs + SSE event renderer
  cmd_pause.go                # POST /api/runs/{suite}/pause
  cmd_stop.go                 # multi-step orchestration: stop + destroy + delete
  cmd_status.go               # GET-based status display, --monitor auto-refresh
  cmd_list.go                 # list suites and scenarios via GET /api/suites/...
  cmd_suites.go               # GET /api/suites
  cmd_scenario.go             # scenario CRUD subcommands + suite create/delete
  cmd_topologies.go           # GET /api/topologies
  cmd_actions.go              # static action vocabulary help
  scenario_e2e_test.go        # CLI→server E2E tests

cmd/newtrun-server/           # Server binary entry point
  main.go                     # --listen, --suites-base, --topologies-base
```

The split enforces one-way import direction: `cmd/newtrun → pkg/newtrun/client → pkg/newtrun/api → pkg/newtrun`. `pkg/newtrun/` is HTTP-agnostic — it knows nothing about the server. `pkg/newtrun/api/` adapts the engine to HTTP. `pkg/newtrun/client/` consumes the HTTP surface.

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
| `name` | yes | Unique scenario identifier; matches filename without `.yaml` and matches the URL `{name}` segment of scenario CRUD endpoints. |
| `description` | no | Human-readable description shown in `newtrun list` and on the SuiteStart event. |
| `topology` | no | Topology name; the Runner aborts if the server's loaded topology doesn't match. |
| `platform` | no | Platform name (e.g., `sonic-vs`); used by capability checks. |
| `requires` | no | Names of scenarios that must pass before this one runs. Hard dependency — failure of a required scenario marks this one SKIP. |
| `after` | no | Soft ordering — this scenario runs after the listed ones regardless of their status. Used for cleanup scenarios. |
| `requires_features` | no | Platform feature flags (e.g., `evpn-vxlan`). The Runner skips the scenario if the platform doesn't declare them. |
| `repeat` | no | Run the steps N times in sequence. Used for soak/stability tests. |
| `steps` | yes | The ordered list of [Step](#22-step) records. |

### 2.2 Step

```go
type Step struct {
    Name          string         `yaml:"name"`
    Action        StepAction     `yaml:"action"`
    Devices       deviceSelector `yaml:"devices,omitempty"`
    Command       string         `yaml:"command,omitempty"`
    URL           string         `yaml:"url,omitempty"`
    Method        string         `yaml:"method,omitempty"`
    Params        map[string]any `yaml:"params,omitempty"`
    Duration      time.Duration  `yaml:"duration,omitempty"`
    Expect        *ExpectBlock   `yaml:"expect,omitempty"`
    Poll          *PollBlock     `yaml:"poll,omitempty"`
    Batch         []BatchCall    `yaml:"batch,omitempty"`
    ExpectFailure bool           `yaml:"expect_failure,omitempty"`
}
```

| Field | Used by | Description |
|-------|---------|-------------|
| `name` | all | Step identifier for logs and reports. |
| `action` | all | Discriminator — `newtron`, `newtron-cli`, `host-exec`, `wait`, `topology-reconcile`, `verify-topology`. |
| `devices` | newtron, newtron-cli, host-exec | YAML accepts `all` or a list of device names. See [§2.3](#23-deviceselector). |
| `command` | newtron-cli, host-exec | Subprocess command line. `{{device}}` is replaced per device. |
| `url` | newtron | HTTP path on newtron-server. `{{device}}` is replaced per device. |
| `method` | newtron | HTTP method; defaults to GET. |
| `params` | newtron, batch | Request body (any JSON-serializable map). |
| `duration` | wait | Sleep duration. |
| `expect` | newtron, newtron-cli, host-exec | Response assertions. See [§2.4](#24-expectblock). |
| `poll` | newtron | Polling spec. See [§2.5](#25-pollblock). |
| `batch` | newtron | Multiple calls grouped per device. See [§2.6](#26-batchcall). |
| `expect_failure` | newtron | When true, inverts pass/fail — assert that the call fails. |

### 2.3 deviceSelector

A YAML-flexible type that accepts either the literal string `"all"` or a list of device names:

```yaml
devices: all
# or
devices: [switch1, switch2]
```

The selector resolves to a sorted device list at run time via `Resolve(allDeviceNames []string) []string`. `All=true` returns the sorted copy of all device names; explicit lists are returned as-is.

### 2.4 ExpectBlock

```go
type ExpectBlock struct {
    Timeout      time.Duration `yaml:"timeout,omitempty"`
    PollInterval time.Duration `yaml:"poll_interval,omitempty"`
    SuccessRate  *float64      `yaml:"success_rate,omitempty"`
    Contains     string        `yaml:"contains,omitempty"`
    JQ           string        `yaml:"jq,omitempty"`
}
```

| Action | Honors |
|--------|--------|
| `newtron` | `jq` (evaluated against response body) |
| `newtron-cli` | `jq` (parses stdout as JSON when `--json` is in the command), `contains` (substring of combined stdout+stderr) |
| `host-exec` | `success_rate` (parsed from ping output), `contains` (substring of combined stdout+stderr) |

`Timeout` and `PollInterval` are internal — `newtronExecutor.executePoll` bridges the YAML `poll:` block to a generic polling helper via this same struct.

A non-matching `expect` block fails the step with the assertion's message.

### 2.5 PollBlock

```go
type PollBlock struct {
    Timeout  time.Duration `yaml:"timeout"`
    Interval time.Duration `yaml:"interval"`
}
```

A step with `poll` repeats its action+expect every `Interval` until the expect succeeds or `Timeout` elapses. Used for convergence checks (BGP sessions, route propagation).

### 2.6 BatchCall

```go
type BatchCall struct {
    Method string         `yaml:"method"`
    URL    string         `yaml:"url"`
    Params map[string]any `yaml:"params,omitempty"`
}
```

The `newtron` action with `batch` runs N calls per device in sequence, collecting results before the expect assertion. Used when one operation needs multiple HTTP calls to set up its preconditions.

### 2.7 StepAction enumeration

| Constant | YAML value | Behavior |
|----------|------------|----------|
| `ActionNewtron` | `newtron` | HTTP call to newtron-server (the most common action). |
| `ActionNewtronCLI` | `newtron-cli` | Subprocess call to `bin/newtron`. Used for loopback testing. |
| `ActionHostExec` | `host-exec` | SSH command on a host VM. Used for ping, traffic generation. |
| `ActionWait` | `wait` | Sleep for `Duration`. |
| `ActionProvision` | `topology-reconcile` | Single `Client.Reconcile(name, "topology", ...)` call per device — the newtron-server performs ConfigReload, lock, ReplaceAll, and SaveConfig internally. High-impact; inline-runs require explicit opt-in. |
| `ActionVerifyProvisioning` | `verify-topology` | Compute drift between device CONFIG_DB and the topology projection. Zero drift = pass. |

---

## 3. Parser (`parser.go`)

### 3.1 ParseScenario

```go
func ParseScenario(path string) (*Scenario, error)
```

Reads the YAML file at `path`, unmarshals into a `Scenario`, validates required fields, and validates each step against its action's field requirements. Returns the parsed scenario or a wrapped error.

### 3.2 ParseScenarioBytes

```go
func ParseScenarioBytes(data []byte) (*Scenario, error)
```

The bytes-in variant. Used by `PUT /api/suites/{suite}/scenarios/{name}` to validate the request body before any disk write — the server is the single point that knows the accept set.

### 3.3 ParseAllScenarios

```go
func ParseAllScenarios(dir string) ([]*Scenario, error)
```

Reads every `*.yaml` file in `dir`, parses each, and returns the list. Used by `GET /api/suites/{suite}/scenarios` and by the Runner at startup.

### 3.4 Validation rules

Per-action requirements enforced by `validateStepFields` (the `stepValidations` table at `pkg/newtrun/parser.go:94`):

| Action | Enforced by validator | Notes |
|--------|----------------------|-------|
| `newtron` | `url` or `batch` (custom check; mutually exclusive with each other in practice) | Devices, method, body are unconstrained at parse time. |
| `newtron-cli` | — | Not in `stepValidations`; `command` is unchecked at parse time and fails at the executor if missing. |
| `host-exec` | `command`; exactly **one** device (`singleDevice: true`) | Multi-device steps are rejected with "host-exec requires exactly one device". |
| `wait` | `duration` (custom check) | |
| `topology-reconcile` | `devices` (`needsDevices: true`) | |
| `verify-topology` | `devices` (`needsDevices: true`) | |

Cross-step rules in `ValidateDependencyGraph`:
- Names in `requires` and `after` must reference scenarios that exist in the suite.
- No cycles.
- Returns the scenarios in topological order.

### 3.5 HasRequires + ComputeTargetChain

```go
func HasRequires(scenarios []*Scenario) bool
func ComputeTargetChain(scenarios []*Scenario, target string) ([]*Scenario, error)
```

`HasRequires` is a quick probe: do any scenarios in the suite declare `requires` or `after`? The Runner topologically sorts only when at least one does.

`ComputeTargetChain` returns the minimum dependency chain reaching `target` — used by `newtrun start --target <name>` to skip everything not on the path.

---

## 4. State Persistence (`state.go`)

### 4.1 RunState

```go
type RunState struct {
    Suite     string          `json:"suite"`
    SuiteDir  string          `json:"suite_dir"`
    Topology  string          `json:"topology"`
    SpecDir   string          `json:"spec_dir,omitempty"`
    Platform  string          `json:"platform"`
    Target    string          `json:"target,omitempty"`
    PID       int             `json:"pid"`
    Status    SuiteStatus     `json:"status"`
    Started   time.Time       `json:"started"`
    Updated   time.Time       `json:"updated"`
    Finished  time.Time       `json:"finished,omitempty"`
    Scenarios []ScenarioState `json:"scenarios"`
}
```

Persisted to `~/.newtron/newtrun/<key>/state.json` after every progress event. `<key>` is the suite name for file-backed runs or a UUID for inline runs (separate `_inline/<uuid>/` subdirectory keeps the namespaces clean).

### 4.2 SuiteStatus

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

| Status | Set by | Means |
|--------|--------|-------|
| `running` | `handleStartRun` (initial) | Runner goroutine is in flight. |
| `pausing` | `handlePauseRun` | Pause requested; Runner will exit at next scenario boundary. |
| `paused` | finalizer when Runner returns `PauseError` | A subsequent `start` resumes. |
| `complete` | finalizer when no errors and no scenario failed | Happy path. |
| `aborted` | finalizer when Runner returned `context.Canceled` or `context.DeadlineExceeded` | Server shut down, inline timeout, or stop endpoint. |
| `failed` | finalizer when any scenario was FAIL/ERROR or run returned a non-Pause/non-Canceled error | Genuine test failure. |

### 4.3 SuiteStatusFromOutcome

```go
func SuiteStatusFromOutcome(runErr error, results []*ScenarioResult) SuiteStatus
```

The single source of truth for terminal status. Used by both the Runner (which emits the wire `SuiteEnd` event) and the server-side finalizer (which writes `state.json`). Same input → same output so the SSE event and the persisted file never disagree.

Precedence:
1. `PauseError` → `paused`
2. `context.Canceled` or `context.DeadlineExceeded` → `aborted`
3. Any other non-nil error → `failed`
4. Otherwise inspect `results` for any FAIL/ERROR → `failed`
5. Else → `complete`

### 4.4 ScenarioState and StepState

```go
type ScenarioState struct {
    Name              string      `json:"name"`
    Description       string      `json:"description,omitempty"`
    Status            string      `json:"status"`
    Duration          string      `json:"duration"`
    CurrentStep       string      `json:"current_step,omitempty"`
    CurrentStepAction string      `json:"current_step_action,omitempty"`
    CurrentStepIndex  int         `json:"current_step_index,omitempty"`
    TotalSteps        int         `json:"total_steps,omitempty"`
    Requires          []string    `json:"requires,omitempty"`
    SkipReason        string      `json:"skip_reason,omitempty"`
    Steps             []StepState `json:"steps,omitempty"`
}

type StepState struct {
    Name      string           `json:"name"`
    Action    string           `json:"action"`
    Status    string           `json:"status"`
    Duration  string           `json:"duration"`
    Message   string           `json:"message,omitempty"`
    DeviceOps []sonic.DeviceOp `json:"device_ops,omitempty"`
}
```

`StepState.DeviceOps` captures per-device-operation events newtron emits during the step's execution. Empty when no producer fed events; populated by `StepProgress` callers (current: none in this repo; planned: the per-device-op SSE consumer in `steps_newtron.go`, gated on newtron Phase 2b).

### 4.5 State directory helpers

```go
func StateDir(suite string) (string, error)        // ~/.newtron/newtrun/<suite>
func InlineStateDir(id string) (string, error)     // ~/.newtron/newtrun/_inline/<id>
func SaveRunState(state *RunState) error           // suite namespace
func SaveInlineRunState(state *RunState) error     // inline namespace
func LoadRunState(suite string) (*RunState, error)
func LoadInlineRunState(id string) (*RunState, error)
func LoadAnyRunState(id string) (*RunState, error) // tries both; used by handleGetRun
func ListSuiteStates() ([]string, error)
```

`saveStateAt` is the shared marshal-and-write body — `SaveRunState` and `SaveInlineRunState` differ only in which directory they target.

### 4.6 CheckPausing

```go
func CheckPausing(suite string) bool
```

Reads `state.json` and returns true when `state.Status == SuiteStatusPausing`. The Runner calls this at each scenario boundary in `iterateScenarios` — that's how the pause signal propagates from `handlePauseRun` (which only writes the file) to the running goroutine.

---

## 5. Runner Internals (`runner.go`)

### 5.1 Runner

```go
type Runner struct {
    ScenariosDir string
    ServerURL    string         // newtron-server HTTP address
    NetworkID    string         // network identifier for server operations
    Client       *client.Client // HTTP client for all SONiC operations
    Lab          *newtlab.Lab
    HostConns    map[string]*ssh.Client
    Progress     ProgressReporter
    Topology     string         // topology name (from server)
    SpecDir      string         // spec directory (from server)

    discoveredPlatform string
    opts               RunOptions
    scenario           *Scenario
}
```

| Field | Set by | Used by |
|-------|--------|---------|
| `ScenariosDir` | `NewRunner(dir)` or `handleStartRun` | Parser to enumerate scenarios. |
| `ServerURL` | `handleStartRun` from `req.NewtronServer` or server default | `client.Client` constructor + steps_cli passes to subprocess via `--server`. |
| `NetworkID` | `handleStartRun` from `req.NetworkID` or server default | Network identifier in HTTP calls. |
| `Client` | `connectToServer` | Every `newtron` action HTTP call. |
| `Lab` | `deployTopology` (when `!opts.NoDeploy`) | Deploy + destroy. |
| `HostConns` | `connectDevices` | `host-exec` SSH calls. |
| `Progress` | `handleStartRun` (HTTPReporter → StateReporter chain) | Every lifecycle event. |
| `Topology` | `connectToServer` from `GET /network` | Verified against each scenario's declared topology. |
| `SpecDir` | `connectToServer` | Used by `Reconcile` action and by `cmd_stop` when destroying. |

### 5.2 RunOptions

```go
type RunOptions struct {
    Scenario  string
    Target    string
    All       bool
    Platform  string
    Keep      bool
    NoDeploy  bool
    Verbose   bool
    JUnitPath string

    Suite     string                 // lifecycle key; empty disables state tracking
    Resume    bool                   // true when resuming a paused run
    Completed map[string]StepStatus  // scenario → status from previous run
}
```

`Suite` is set when the run is being driven via lifecycle endpoints (start/pause/stop); empty when called from `Run()` directly. The `CheckPausing` probe in `iterateScenarios` is conditional on `Suite != ""` — direct `Run()` calls bypass it.

### 5.3 Run(ctx, opts)

The top-level entry. Resolves scenarios from `opts.All` / `opts.Target` / `opts.Scenario`, validates the dependency graph, connects to newtron-server, deploys the topology if needed, connects to host devices, then enters `iterateScenarios`. Always emits `SuiteEnd` before returning (even on error) so reporters carry a terminal event.

The terminal status passed to `SuiteEnd` is computed via `SuiteStatusFromOutcome(err, results)` — the wire and the persisted state get the same value.

### 5.4 iterateScenarios

```go
func (r *Runner) iterateScenarios(
    ctx context.Context,
    scenarios []*Scenario,
    opts RunOptions,
    deployedPlatform string,
    run scenarioRunner,
) ([]*ScenarioResult, error)
```

Per-iteration sequence:

1. **ctx-cancel check.** If the context is canceled, return early with `ctx.Err()`. This is what makes graceful server shutdown produce `status=aborted` instead of a flood of synthetic FAIL events ([HLD §9.3](hld.md)).
2. **Resume skip.** If `opts.Resume` and `opts.Completed[sc.Name] == StepStatusPassed`, emit `ScenarioEnd` with `status=SKIPPED` and `SkipReason="already passed (resumed)"`.
3. **Pause check.** If `opts.Suite != ""` and `CheckPausing(opts.Suite)`, return `PauseError{Completed: len(results)}`.
4. **`requires` check.** If any prerequisite scenario failed, mark this scenario SKIPPED.
5. **Feature-flag check.** If the platform doesn't support a scenario's `requires_features`, mark SKIPPED.
6. **Run.** Emit `ScenarioStart`, call the scenarioRunner callback, emit `ScenarioEnd`.

### 5.5 runScenarioSteps

```go
func (r *Runner) runScenarioSteps(
    ctx context.Context, sc *Scenario, opts RunOptions, result *ScenarioResult,
)
```

Executes the steps of a scenario, recording per-step results into `result.Steps`. Honors `sc.Repeat` (run the step list N times). A step's failure stops the scenario at that step — subsequent steps are not run. When `Repeat > 1`, `result.FailedIteration` is set to the iteration number that failed, and outer iterations are not run.

### 5.6 Dispatcher

A package-local map dispatches `step.Action` to a `stepExecutor`:

| Action | Executor |
|--------|----------|
| `ActionNewtron` | `newtronExecutor` ([§9.1](#91-newtronexecutor)) |
| `ActionNewtronCLI` | `newtronCLIExecutor` ([§9.2](#92-newtroncliexecutor)) |
| `ActionHostExec` | `hostExecExecutor` ([§9.3](#93-hostexecexecutor)) |
| `ActionWait` | `waitExecutor` (sleep) |
| `ActionProvision` | `provisionExecutor` ([§9.4](#94-provisionexecutor)) |
| `ActionVerifyProvisioning` | `verifyProvisioningExecutor` — drift check against the topology projection. |

### 5.7 connectToServer / connectDevices

```go
func (r *Runner) connectToServer() error
func (r *Runner) connectDevices() error
```

`connectToServer` constructs the newtron HTTP client (`client.New(r.ServerURL, r.NetworkID)`) and reads the server's loaded network spec to populate `r.Topology` and `r.SpecDir`. Fails fast if the server has no network registered.

`connectDevices` walks the topology's device list, identifies hosts via `Client.IsHostDevice(name)`, opens an SSH connection to each, and stores them in `r.HostConns`. Skipped when `opts.NoDeploy == true`.

---

## 6. Progress Reporting (`progress.go`)

### 6.1 ProgressReporter

```go
type ProgressReporter interface {
    SuiteStart(scenarios []*Scenario)
    ScenarioStart(name string, index, total int)
    ScenarioEnd(result *ScenarioResult, index, total int)
    StepStart(scenario string, step *Step, index, total int)
    StepProgress(scenario string, step *Step, op *sonic.DeviceOp, index int)
    StepEnd(scenario string, result *StepResult, index, total int)
    SuiteEnd(results []*ScenarioResult, status SuiteStatus, duration time.Duration)
}
```

Seven callbacks invoked by the Runner. Implementations:

| Implementation | Purpose |
|----------------|---------|
| `consoleProgress` | Terminal output for direct `Run()` invocations (no server). |
| `StateReporter` | Persists `RunState` to disk after every callback; chainable. |
| `HTTPReporter` ([§7.3](#73-httpreporter)) | Publishes events to the `EventBroker`; chainable. |

### 6.2 StateReporter

```go
type StateReporter struct {
    State *RunState
    Save  func(*RunState) error  // SaveRunState or SaveInlineRunState
    Inner ProgressReporter       // chain (e.g., HTTPReporter)
}
```

Mutates `r.State` on every callback and saves to disk. `Save` is injected so the same reporter logic works for both the suite and inline namespaces. `Inner` lets a downstream reporter (typically `HTTPReporter`) receive the same events.

### 6.3 StepProgress

The one callback with no current producer in the Runner. Reserved for the per-device-op streaming consumer in `steps_newtron.go` (gated on newtron Phase 2b — when the newtron-server's `WriteResult` stream lands, `steps_newtron.go` will forward each device op via `StepProgress`).

### 6.4 SuiteEnd carries status

`SuiteEnd` carries a `SuiteStatus` so the wire event distinguishes "the suite ran and N scenarios failed" from "the server died mid-run". All `ProgressReporter` implementations honor it; the `SuiteEndPayload` JSON field carries the same value.

---

## 7. HTTP Server Package (`pkg/newtrun/api/`)

### 7.1 Server

```go
type Server struct {
    cfg        Config
    logger     *log.Logger
    httpServer *http.Server
    broker     *EventBroker
    registry   *RunRegistry
}
```

| Method | Purpose |
|--------|---------|
| `NewServer(cfg) *Server` | Constructor; applies Config defaults (port, base dirs, newtron-server URL). |
| `(*Server).Start(addr) error` | Blocks listening on `addr`. |
| `(*Server).Stop(ctx) error` | Cancels every in-flight run, waits up to 5s, shuts down the HTTP listener. |
| `(*Server).Handler() http.Handler` | Exported handler for testing — external packages mount the real server into `httptest.Server`. |
| `(*Server).Broker() *EventBroker` | Accessor (no-arg getter per §32 exception). |
| `(*Server).Registry() *RunRegistry` | Accessor. |

### 7.2 Config defaults

`NewServer` fills empty fields:

| Field | Default |
|-------|---------|
| `SuitesBase` | `newtrun/suites` |
| `TopologiesBase` | `newtrun/topologies` |
| `NewtronServer` | `http://127.0.0.1:18080` |
| `NetworkID` | `default` |
| `InlineURLPrefix` | empty (no URL restriction enforced by default; see [§7.7](#77-inlinesafetypolicy)) |
| `Logger` | `log.Default()` |

### 7.3 HTTPReporter

```go
type HTTPReporter struct {
    Broker *EventBroker
    RunKey string
    Inner  newtrun.ProgressReporter
}
```

Implements `ProgressReporter`. Each callback constructs an `Event` and publishes it via `Broker.Publish(RunKey, ev)`. `Inner` is typically a `StateReporter`, so disk-persistence and SSE-publication happen on the same event.

### 7.4 EventBroker

```go
type EventBroker struct {
    mu          sync.RWMutex
    subscribers map[string]map[chan Event]struct{}
}
```

SSE multiplexer. The handler `handleRunEvents` calls `Subscribe(runKey)` which returns a buffered `chan Event` (capacity 64) and an unsubscribe func.

`Publish(runKey, ev)` fans out to every subscriber of that key. **Drop-on-full:** if a subscriber's buffer is full, the event is dropped for that subscriber only — other subscribers still receive it. SSE is best-effort.

### 7.5 RunRegistry

```go
type RunRegistry struct {
    mu      sync.Mutex
    entries map[string]*RegistryEntry
}

type RegistryEntry struct {
    Key     string
    Started time.Time
    Cancel  context.CancelFunc
    Done    chan struct{}
    Result  *RunResult
}
```

| Method | Purpose |
|--------|---------|
| `Acquire(key) (*RegistryEntry, error)` | Reserve the key. Returns `AlreadyRunningError` if held. |
| `Get(key) *RegistryEntry` | Lookup; nil if no entry. |
| `Release(key, *RunResult)` | Closes `Done`, removes the entry, stores the result. |
| `CancelAll(timeout)` | Server shutdown: cancel every entry's context, wait for `Done`. |

Same-suite re-runs collide on the key and return 409. Inline runs allocate UUIDs that never collide. Different suites run concurrently.

### 7.6 reconcileStaleStatus

```go
func (s *Server) reconcileStaleStatus(state *newtrun.RunState, runKey string)
```

The server-restart-honesty rule ([HLD §9.3](hld.md)). When `handleGetRun` or `handleListRuns` loads a `state.json` that claims `running` or `pausing` but the registry has no live entry, the in-memory copy is relabeled to `aborted` before serialization. The disk file is not rewritten — the next finalizer write applies the canonical status.

Called from both `handleGetRun` and `handleListRuns` via this helper (per `docs/ai-instructions.md` §7 — second instance of a pattern must consolidate).

### 7.7 InlineSafetyPolicy

```go
type InlineSafetyPolicy struct {
    AllowedActions     map[newtrun.StepAction]bool
    AllowedURLPrefixes []string
    AllowReconcile     bool
    WallTimeBudget     time.Duration
}
```

| Field | Default | Override |
|-------|---------|----------|
| `AllowedActions` | `{newtron, wait}` | Per-policy, not per-request. |
| `AllowedURLPrefixes` | `nil` (no URL restriction) — `handleStartInlineRun` overlays the server's configured `InlineURLPrefix` if set. | Per-policy. |
| `AllowReconcile` | `false` | Request body `allow_reconcile: true`. |
| `WallTimeBudget` | `60s` | Request body `timeout_seconds: N`. |

`Validate(scenario)` returns a `SafetyViolation` describing what tripped the policy. 400 Bad Request from `handleStartInlineRun` carries the violation as the error message.

### 7.8 finalizeRunState + finalizeInlineState

```go
func finalizeRunState(state *newtrun.RunState, results []*newtrun.ScenarioResult, runErr error)
func finalizeInlineState(state *newtrun.RunState, results []*newtrun.ScenarioResult, runErr error)
```

Run after the Runner goroutine returns. Both delegate to `SuiteStatusFromOutcome` for the status; they differ only in which persistence function they call (`SaveRunState` vs `SaveInlineRunState`).

### 7.9 Route registration

`buildHandler()` (in `server.go`) registers the HTTP routes against `http.ServeMux`. See [api.md](api.md) for the canonical list; the handler functions are spread across `runs.go` / `suites.go` / `scenarios.go` / `topologies.go` per `docs/DESIGN_PRINCIPLES.md` §28 (file-level feature cohesion).

---

## 8. HTTP Client Package (`pkg/newtrun/client/`)

### 8.1 Client

```go
type Client struct {
    baseURL      string
    httpClient   *http.Client  // for short request/response calls
    streamClient *http.Client  // for SSE (no timeout)
}
```

| Method | Endpoint |
|--------|----------|
| `Health(ctx)` | GET /api/health |
| `ListRuns(ctx)` | GET /api/runs |
| `GetRun(ctx, suite)` | GET /api/runs/{suite} |
| `StartRun(ctx, req)` | POST /api/runs |
| `PauseRun(ctx, suite)` | POST /api/runs/{suite}/pause |
| `StopRun(ctx, suite)` | POST /api/runs/{suite}/stop |
| `DeleteRun(ctx, suite)` | DELETE /api/runs/{suite} |
| `StreamEvents(ctx, suite, handle)` | GET /api/runs/{suite}/events (SSE) |
| `ListSuites(ctx)` | GET /api/suites |
| `ListSuiteScenarios(ctx, suite)` | GET /api/suites/{suite}/scenarios |
| `CreateSuite(ctx, name)` | POST /api/suites |
| `DeleteSuite(ctx, name)` | DELETE /api/suites/{suite} |
| `GetScenario(ctx, suite, name)` | GET /api/suites/{suite}/scenarios/{name} |
| `PutScenario(ctx, suite, name, body)` | PUT /api/suites/{suite}/scenarios/{name} |
| `DeleteScenario(ctx, suite, name)` | DELETE /api/suites/{suite}/scenarios/{name} |
| `ListTopologies(ctx)` | GET /api/topologies |

### 8.2 Transport helpers

```go
func (c *Client) get(ctx, path, out)  error              // JSON envelope read
func (c *Client) post(ctx, path, in, out) error          // JSON envelope write
func (c *Client) do(ctx, method, path, in, out) error    // shared body of get/post
func (c *Client) getRaw(ctx, path) ([]byte, error)       // non-envelope (YAML scenario body)
func (c *Client) putRaw(ctx, path, body) error           // non-envelope (YAML scenario PUT)
```

`do` is the shared body — marshal `in` to JSON, dispatch, parse envelope, unmarshal `data` into `out`. `getRaw`/`putRaw` exist because scenario YAML is not JSON-enveloped.

### 8.3 StreamEvents

```go
func (c *Client) StreamEvents(
    ctx context.Context, suite string, handle func(Event),
) error
```

Opens a long-running GET to `/api/runs/{suite}/events`, parses the SSE frame stream line-by-line, decodes each `data:` line as an `Event`, and calls `handle(ev)`. Returns when the context is canceled or the connection closes.

SSE comment lines (those starting with `:`) are silently skipped — they're heartbeats and subscription confirmations.

### 8.4 ServerError

```go
type ServerError struct {
    StatusCode int
    Message    string
}
func (e *ServerError) Error() string
```

Any 4xx/5xx response from a do/getRaw/putRaw call wraps the body in a `ServerError`. `errors.As` recovers the status code; the CLI's `notFoundIsNil` uses this to treat 404 as "absent" rather than failure.

---

## 9. Step Executors

Source files: `steps.go`, `steps_newtron.go`, `steps_cli.go`, `steps_host.go`.

### 9.1 newtronExecutor

Action: `newtron`. Dispatches one HTTP call per device (or one global call for non-device-scoped URLs). Per-device URL expansion replaces `{{device}}` in `step.URL` via `strings.ReplaceAll` in `expandURL` (`pkg/newtrun/steps_newtron.go:225`). Response is matched against `step.Expect` and optionally polled via `step.Poll`.

`batch`-mode runs N URLs per device in sequence before the expect; useful for setting up preconditions.

### 9.2 newtronCLIExecutor

Action: `newtron-cli`. Spawns `bin/newtron <device> <command...>` as a subprocess. Passes `--server <runner.ServerURL>` so the subprocess targets the same newtron-server the in-process client uses. Honors `step.Expect` against the subprocess exit code, stdout, and stderr.

If the command includes `--json` and `step.Expect.JQ` is set, the executor parses stdout as JSON and evaluates the jq expression.

### 9.3 hostExecExecutor

Action: `host-exec`. Runs `step.Command` over SSH on the named host devices. Per-device parallelism (each device's SSH call runs in its own goroutine; results collected and merged).

Honors `step.Expect.SuccessRate` for ping commands: parses the "N% packet loss" line and asserts the success rate is at least the configured threshold.

### 9.4 provisionExecutor

Action: `topology-reconcile`. Calls `POST /network/{netID}/node/{device}/intent/reconcile?mode=topology` once per device through `Client.Reconcile`. The reconcile is one call — the newtron-server handles ConfigReload, lock, ReplaceAll, and SaveConfig internally — not deploy+reconcile+verify on the client side. This is the high-impact action — it can replace an entire device's intent state. Inline runs require explicit opt-in (`allow_reconcile: true` in the request body).

### 9.5 Multi-device helpers

```go
func (r *Runner) resolveDevices(step *Step) []string
func (r *Runner) executeForDevices(step *Step, fn func(name string) (string, error)) *StepOutput
```

`resolveDevices` resolves `step.Devices` against the topology's device list. `executeForDevices` runs `fn` concurrently across devices, collects per-device results, and produces the merged `StepOutput`.

---

## 10. newtlab Integration (`deploy.go`)

```go
func DeployTopology(ctx context.Context, specDir string, force bool) (*newtlab.Lab, error)
func EnsureTopology(ctx context.Context, specDir string) (*newtlab.Lab, error)
func DestroyTopology(ctx context.Context, lab *newtlab.Lab) error
```

`EnsureTopology` reuses an existing lab if all nodes report `running`, otherwise deploys fresh. Used between iterations of the same suite (`newtrun start` → fail → fix → `newtrun start`) to avoid a full redeploy when the lab is healthy.

`DeployTopology` forces a fresh deployment. The Runner uses `EnsureTopology` unless `opts.Keep` is false.

`DestroyTopology` is called from `cmd_stop` (CLI side) and from defer cleanups when the Runner's deploy step succeeded.

---

## 11. Results & Reporting (`report.go`)

### 11.1 ScenarioResult and StepResult

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
    FailedIteration int  // which iteration failed (only set when Repeat > 1)
}

type StepResult struct {
    Name      string
    Action    StepAction
    Status    StepStatus
    Duration  time.Duration
    Message   string
    Details   []DeviceResult
    Iteration int
}
```

`StepStatus` values: `PASS`, `FAIL`, `SKIP`, `ERROR`.

### 11.2 ReportGenerator

```go
type ReportGenerator struct {
    Results []*ScenarioResult
}

func (g *ReportGenerator) WriteMarkdown(path string) error
func (g *ReportGenerator) WriteJUnit(path string) error
```

Produces the post-run summary report. The CLI calls `WriteMarkdown` unconditionally (to `newtrun/.generated/report.md`) and `WriteJUnit` when `--junit <path>` is set. Both consume `Results` reconstructed from `ScenarioEnd` SSE event payloads on the CLI side.

### 11.3 Output formats

| Format | Used by |
|--------|---------|
| Markdown table | Quick scrollback for ad-hoc runs. |
| JUnit XML | CI consumption (Jenkins, GitHub Actions, GitLab). |

---

## 12. Error Handling (`errors.go`)

```go
type InfraError struct {
    Op     string
    Device string
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

| Type | Returned by | Mapped to exit code |
|------|-------------|---------------------|
| `InfraError` | `connectToServer`, `connectDevices`, `deployTopology` | CLI exit 2 (infrastructure). |
| `StepError` | step executors when an assertion fails | CLI exit 1 (test failure) via `SuiteStatusFromOutcome → failed`. |
| `PauseError` | `iterateScenarios` when `CheckPausing` returns true | Not a CLI error — terminal status is `paused`, exit 0. |

The CLI also has two sentinel errors in `cmd/newtrun/main.go`: `errInfraError` and `errTestFailure`. The exit-code path maps `errInfraError → 2`, `errTestFailure → 1`, everything else → 1.

---

## 13. CLI Binary (`cmd/newtrun/`)

### 13.1 main.go

Root cobra command. Persistent flag `--newtrun-server <url>` (env: `NEWTRUN_SERVER`, default: `http://127.0.0.1:18081`). Subcommands:

| Command | Endpoint | Notes |
|---------|----------|-------|
| `start <suite>` | POST /api/runs + SSE | Streams events; exits on terminal SuiteEnd. |
| `pause <suite>` | POST /api/runs/{suite}/pause | Returns when pause signal lands. |
| `stop <suite>` | GET + POST /stop + newtlab.Destroy + DELETE | Multi-step orchestration. |
| `status [-s <pattern>]` | GET /api/runs + /api/runs/{suite} | Lists all suites; `-s/--suite <pattern>` filters by substring match. `--monitor` auto-refreshes. |
| `list [suite]` | GET /api/suites + /api/suites/{suite}/scenarios | Lists suites; with a suite name lists its scenarios. |
| `suites` | GET /api/suites | Hidden alias of `list`. |
| `suite create/delete <name>` | POST/DELETE /api/suites | Per [§7](#7-http-server-package-pkgnewtrunapi). |
| `scenario list/get/put/delete` | /api/suites/{suite}/scenarios* | Per [§7](#7-http-server-package-pkgnewtrunapi). |
| `topologies` | GET /api/topologies | List server-visible topologies. |
| `actions` | static | Help text describing the action vocabulary. |
| `version` | static | Build info. |

Exit codes:

| Code | Meaning |
|------|---------|
| 0 | All scenarios passed |
| 1 | At least one scenario failed |
| 2 | Infrastructure error (deploy / connection / server lost mid-run / run aborted) |

### 13.2 clientutil.go

```go
func newClient() *client.Client        // builds Client from --newtrun-server / env
func requireServer(ctx, c) error       // probes GET /api/health; clean error on connection refused
func notFoundIsNil(err) bool           // ServerError 404 → nil for absent-state reads
func fetchRunStateViaClient(suite) (*newtrun.RunState, error)
func listSuiteNamesViaClient() ([]string, error)
```

Every state-changing CLI command calls `requireServer` before its real work. Strict Option A: read commands also call `requireServer` — the CLI never reads `~/.newtron/newtrun/` directly.

### 13.3 cmd_start.go highlights

`cmd_start` is the most complex command — it subscribes to SSE, renders events to the terminal, tracks the terminal status, and exits with the right code:

| Behavior | Field tracked |
|----------|---------------|
| Any scenario FAIL → exit 1 | `hasFailure atomic.Bool` |
| Any scenario ERROR → exit 1 | `hasError atomic.Bool` |
| SuiteEnd ever arrived? | `suiteEndSeen atomic.Bool` |
| SuiteEnd.Status == aborted? | `suiteAborted atomic.Bool` |

Post-run logic:
- If stream ended without SuiteEnd → `errInfraError("connection lost mid-run")` → exit 2.
- If SuiteEnd.Status == aborted → `errInfraError("run was aborted")` → exit 2.
- Else if `hasFailure || hasError` → `errTestFailure` → exit 1.
- Else → nil → exit 0.

Markdown report is written to `newtrun/.generated/report.md` after every run; JUnit XML only when `--junit <path>` is set.

### 13.4 cmd_scenario.go

Per-scenario CRUD subcommands:

```
newtrun suite create <name>
newtrun suite delete <name>
newtrun scenario list <suite>
newtrun scenario get <suite> <name>
newtrun scenario put <suite> <name> [--file <path>]
newtrun scenario delete <suite> <name>
```

`put` defaults to stdin if `--file` is not given; the body is sent raw to `PUT /api/suites/{suite}/scenarios/{name}` and validated server-side.

---

## 14. Server Binary (`cmd/newtrun-server/`)

Three flags:

| Flag | Default | Meaning |
|------|---------|---------|
| `--listen` | `127.0.0.1:18081` | Bind address. Non-loopback values trigger a startup warning that there is no built-in authentication. |
| `--suites-base` | `newtrun/suites` | Directory containing suite subdirectories. |
| `--topologies-base` | `newtrun/topologies` | Directory containing topology subdirectories. |

The Config struct has `NewtronServer` and `NetworkID` fields with defaults (`http://127.0.0.1:18080` and `default`), but the current binary registers no CLI flag or env-var binding for either. The values can only be overridden per-request via the `newtron_server` and `network_id` fields on `POST /api/runs`. Adding a flag or env-var to the server binary is a small follow-on if operators need to point a whole instance at a non-default newtron-server.

The server installs a SIGTERM handler that calls `Stop(ctx)` — cancels every in-flight run, waits up to 5 seconds for them to drain, then shuts down the HTTP listener. The Runner's ctx-cancel check ([§5.4](#54-iteratescenarios)) is what makes the drain produce honest status (`aborted`) instead of synthetic FAIL events.

---

*Source-traced against `pkg/newtrun/`, `pkg/newtrun/api/`, `pkg/newtrun/client/`, `cmd/newtrun/`, and `cmd/newtrun-server/`. Type definitions are exact; method signatures are exact. If you find a discrepancy, the code is the authority — please open an issue or PR.*
