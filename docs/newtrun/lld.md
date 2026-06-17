# newtrun Low-Level Design

This document specifies newtrun's type definitions, package structure, and code mechanics — the *how* and *what fields*. For the architectural *what* and *why* (run lifecycle, server-shutdown semantics, SSE design, strict Option A), see the [HLD](hld.md). For HTTP-client authoring (endpoints, payloads, status codes), see the [API reference](api.md). For CLI operation, see the [HOWTO](howto.md).

**Audience:** Engineers reading or modifying newtrun's source code. Field-by-field type tables, package boundaries, the data flow through each handler.

**Mental model:** newtrun is a thin CLI client (`bin/newtrun`) plus an engine that lives inside `bin/newt-server`. The CLI is a stateless HTTP client. The engine owns the run registry, executes scenarios in goroutines, and streams progress over Server-Sent Events. `bin/newt-server` mounts the engine's HTTP routes (`/newtrun/v1/...`) alongside the newtron and newtlab engines on a single port. Everything below is organized around that split.

---

## Table of Contents

1. [Package Structure](#1-package-structure)
2. [Core Types](#2-core-types)
3. [Suite Loader and Template Engine](#3-suite-loader-and-template-engine)
4. [Parser](#4-parser-parsergo)
5. [State Persistence](#5-state-persistence-statego)
6. [Runner Internals](#6-runner-internals-runnergo)
7. [Progress Reporting](#7-progress-reporting-progressgo)
8. [HTTP Server Package](#8-http-server-package-pkgnewtrunapi)
9. [HTTP Client Package](#9-http-client-package-pkgnewtrunclient)
10. [Step Executors](#10-step-executors)
11. [newtlab Integration](#11-newtlab-integration-deploygo)
12. [Results & Reporting](#12-results--reporting-reportgo)
13. [Error Handling](#13-error-handling-errorsgo)
14. [CLI Binary](#14-cli-binary-cmdnewtrun)
15. [Engine Mount Point](#15-engine-mount-point-cmdnewt-server)

---

## 1. Package Structure

Three Go packages compose newtrun, one each for engine / server / client. The CLI binary and the server binary each have a `cmd/` package that wires the pieces together.

```
pkg/newtrun/                  # Engine (HTTP-agnostic orchestration core)
  scenario.go                 # Scenario, Step, StepAction, ExpectBlock, BatchCall
  suite.go                    # Suite, ParameterSpec, LoadSuite, EffectiveTargets/Parameters,
                              #   target-value whitelist, ScenarioIsParameterized
  template.go                 # ExpandStep, CollectTemplateReferences, context-aware
                              #   substitution (URL/Shell/JQ/Raw), typed full-token preservation
  parser.go                   # ParseScenario, ParseScenarioBytes, ValidateDependencyGraph
  runner.go                   # Runner, RunOptions, Run(ctx, opts), iterateScenarios
  steps.go                    # stepExecutor interface, multi-device helpers
  steps_newtron.go            # ActionNewtron: URL expansion, jq, polling, batch
  steps_cli.go                # ActionNewtronCLI: subprocess execution
  steps_host.go               # ActionHostExec: SSH command execution
  steps_run_suite.go          # ActionRunSuite: child Runner + depth-counter context
  deploy.go                   # Deploy/Ensure/Destroy via newtlab
  state.go                    # RunState, ScenarioState, StepState; SuiteStatusFromOutcome
  progress.go                 # ProgressReporter (7 callbacks), StateReporter
  errors.go                   # InfraError, StepError, PauseError
  report.go                   # ScenarioResult, StepResult, ReportGenerator (markdown + JUnit)

pkg/newtrun/newtrun/v1/              # HTTP server package
  server.go                   # Server, Config, route registration, handleHealth, listSubdirs
  middleware.go               # withRequestID, withLogger, withRecovery
  runs.go                     # All run handlers + reconcileStaleStatus + finalizers + newRunID
  suites.go                   # /newtrun/v1/suites endpoints + list-scenarios + nameRE
  scenarios.go                # GET/PUT/DELETE per-scenario + atomic write + path resolution
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
  cmd_start.go                # POST /newtrun/v1/runs + SSE event renderer
  cmd_pause.go                # POST /newtrun/v1/runs/{suite}/pause
  cmd_stop.go                 # multi-step orchestration: stop + destroy + delete
  cmd_status.go               # GET-based status display, --monitor auto-refresh
  cmd_list.go                 # list suites and scenarios via GET /newtrun/v1/suites/...
  cmd_suites.go               # GET /newtrun/v1/suites
  cmd_scenario.go             # scenario CRUD subcommands + suite create/delete
  cmd_topologies.go           # delegates to newtron: GET/POST /newtron/v1/networks
  cmd_actions.go              # static action vocabulary help
  scenario_e2e_test.go        # CLI→server E2E tests

cmd/newt-server/              # Composed server that hosts all three engines
  main.go                     # --listen, --spec-dir, --auth-pam-service,
                              # --tls-cert/--tls-key/--tls-ca, --secret-store
```

The split enforces one-way import direction: `cmd/newtrun → pkg/newtrun/client → pkg/newtrun/api → pkg/newtrun`. `pkg/newtrun/` is HTTP-agnostic — it knows nothing about the server. `pkg/newtrun/api/` adapts the engine to HTTP (mounted by `cmd/newt-server` alongside the newtron and newtlab engines). `pkg/newtrun/client/` consumes the HTTP surface.

---

## 2. Core Types

### 2.1 Suite (`suite.go`)

```go
type Suite struct {
    Name        string                   `yaml:"name"`
    Description string                   `yaml:"description"`
    Topology    string                   `yaml:"topology"`
    Platform    string                   `yaml:"platform"`
    Targets     map[string][]string      `yaml:"targets,omitempty"`
    Parameters  map[string]ParameterSpec `yaml:"parameters,omitempty"`
    Scenarios   []*Scenario              `yaml:"-"`
}
```

A Suite is loaded from a directory containing `suite.yaml` and zero or more scenario YAMLs (see [§3.1 LoadSuite](#31-loadsuite)). It owns the topology + platform declaration that scenarios inherit; `Targets` and `Parameters` form the catalog parameterized scenarios reference via templates.

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Suite identifier; matches the directory name. |
| `description` | no | Human-readable description shown on `GET /newtrun/v1/suites/{name}/scenarios` and in `newtrun list`. |
| `topology` | yes | Topology name; the Runner aborts at startup if the server's loaded topology doesn't match. |
| `platform` | no | Default platform for capability checks; overridden by CLI `--platform`. |
| `targets` | no | Map of plural dimension key → list of values. Recognized dimensions: `devices`, `interfaces` (see [§2.4 singularize](#24-singularize)). Values must satisfy the target-value whitelist (`^[A-Za-z0-9_-]+$`). |
| `parameters` | no | Map of name → `ParameterSpec`. Catalog of typed values parameterized scenarios may reference via `{{param.X}}`. |
| `Scenarios` | n/a | Populated by `LoadSuite` from the dependency-ordered scenario list; not part of the wire format. |

Key methods:

```go
func (s *Suite) IsParameterized() bool
func (s *Suite) TargetIterations() []map[string]string
func (s *Suite) EffectiveTargets(overrides map[string][]string) (map[string][]string, error)
func (s *Suite) EffectiveParameters(overrides map[string]any) (map[string]any, error)
```

- `IsParameterized` reports whether any `Targets` or `Parameters` are declared. Scenarios within a parameterized suite may still be embedded-target — the parameterized vs embedded decision is per-scenario via `ScenarioIsParameterized` (see [§2.3](#23-scenario-scenariogo)).
- `TargetIterations` returns the cross-product expansion with singular keys (`devices` → `device`). Empty `Targets` returns a single `nil` binding so callers iterate once.
- `EffectiveTargets` merges per-key overrides with the suite defaults. Unknown dimensions, empty overrides, and identifier-whitelist violations return HTTP-400-class errors.
- `EffectiveParameters` merges per-name overrides with declared defaults. Each override goes through `ParameterSpec.Coerce`; unknown names, type mismatches, and constraint violations all error out.

### 2.2 ParameterSpec (`suite.go`)

```go
type ParameterSpec struct {
    Type     ParameterType `yaml:"type,omitempty"`
    Default  any           `yaml:"default,omitempty"`
    Values   []string      `yaml:"values,omitempty"` // for enum
    Min      *int          `yaml:"min,omitempty"`    // for int
    Max      *int          `yaml:"max,omitempty"`    // for int
    Required bool          `yaml:"required,omitempty"`
}

type ParameterType string
const (
    ParameterTypeString ParameterType = "string"
    ParameterTypeInt    ParameterType = "int"
    ParameterTypeBool   ParameterType = "bool"
    ParameterTypeEnum   ParameterType = "enum"
    ParameterTypeIPv4   ParameterType = "ipv4"
    ParameterTypeCIDR   ParameterType = "cidr"
)
```

`UnmarshalYAML` accepts two forms:

- **Shorthand scalar** — `admin_status: up` decodes to `{Type: string, Default: "up"}`; integer / bool YAML scalars infer the matching type.
- **Verbose map** — explicit `type:` plus type-specific fields. Verbose form without `type:` defaults to `string`.

Methods:

```go
func (p *ParameterSpec) ValidateDeclaration() error
func (p *ParameterSpec) Coerce(v any) (any, error)
```

- `ValidateDeclaration` runs at suite-load time: rejects unknown types, enums without `values`, and defaults that violate their own constraints.
- `Coerce` runs at request-override time: validates the JSON-typed input against the spec and returns the typed Go value to substitute into templates. Integers tolerate `int`, `int64`, and integer-valued `float64` (JSON numbers decode as float64).

### 2.3 Scenario (`scenario.go`)

```go
type Scenario struct {
    Name             string   `yaml:"name"`
    Description      string   `yaml:"description"`
    Topology         string   `yaml:"topology,omitempty"`        // rejected by LoadSuite
    Platform         string   `yaml:"platform,omitempty"`        // rejected by LoadSuite
    Requires         []string `yaml:"requires,omitempty"`
    After            []string `yaml:"after,omitempty"`
    RequiresFeatures []string `yaml:"requires_features,omitempty"`
    Repeat           int      `yaml:"repeat,omitempty"`
    Steps            []Step   `yaml:"steps"`
}
```

A scenario carries the step list plus dependency metadata. Topology and platform are suite-level (`suite.yaml`); LoadSuite rejects any scenario that sets either field. The `Topology`/`Platform` fields remain on the Go struct only so `ParseScenarioBytes` can detect and report the violation with the file path.

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Unique scenario identifier; matches filename without `.yaml` and matches the URL `{name}` segment of scenario CRUD endpoints. |
| `description` | no | Human-readable description shown in `newtrun list` and on the SuiteStart event. |
| `topology` | no | Suite-level field. Rejected by `LoadSuite` — declared on the suite, not on scenarios. The field exists on the Go struct so unmarshaling can detect the misplaced declaration and surface a descriptive error. |
| `platform` | no | Suite-level field. Rejected by `LoadSuite` — declared on the suite (or via CLI override), not on scenarios. Field exists on the Go struct for the same detection reason. |
| `requires` | no | Names of scenarios that must pass before this one runs. Hard dependency — failure of a required scenario marks this one SKIP. |
| `after` | no | Soft ordering — this scenario runs after the listed ones regardless of their status. Used for cleanup scenarios. |
| `requires_features` | no | Platform feature flags (e.g., `evpn-vxlan`). The Runner skips the scenario if the platform doesn't declare them. |
| `repeat` | no | Run the steps N times in sequence. Used for soak/stability tests. |
| `steps` | yes | The ordered list of [Step](#25-step) records. |

```go
func ScenarioIsParameterized(sc *Scenario) bool
```

Reports whether any step in the scenario uses `{{target.X}}` or `{{param.X}}` tokens. Decided per-scenario by scanning `CollectTemplateReferences` over every step; embedded-target scenarios coexist with parameterized scenarios in the same suite, and the runner takes the suite's target cross-product only for the latter.

### 2.4 singularize (`suite.go`)

```go
var singularizeMap = map[string]string{
    "devices":    "device",
    "interfaces": "interface",
}
func singularize(plural string) (string, bool)
```

Suite `targets:` keys are plural (`devices`, `interfaces`); template references are singular (`{{target.device}}`). The map is the closed catalog of recognized dimensions — new dimensions must be added here, and any plural not in the map is rejected at parse time. Keeps the substitution surface reviewable.

### 2.5 Step

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
| `devices` | newtron, newtron-cli, host-exec | YAML accepts `all` or a list of device names. See [§2.6](#26-deviceselector). Forbidden on parameterized steps. |
| `command` | newtron-cli, host-exec | Subprocess command line. Embedded-target: `{{device}}` is replaced per device. Parameterized: `{{target.X}}` / `{{param.X}}` substituted under shell-quoting context (see [§3.3](#33-context-aware-substitution)). |
| `url` | newtron | HTTP path on newtron-server. Embedded-target: `{{device}}` is replaced per device. Parameterized: `{{target.X}}` / `{{param.X}}` substituted under URL-path-escape context. |
| `method` | newtron | HTTP method; defaults to GET. |
| `params` | newtron, batch | Request body (any JSON-serializable map). Template tokens in string values are substituted; a value that is ENTIRELY one token preserves the parameter's typed Go value (int stays an int through `json.Marshal`). |
| `duration` | wait | Sleep duration. |
| `expect` | newtron, newtron-cli, host-exec | Response assertions. See [§2.7](#27-expectblock). |
| `poll` | newtron | Polling spec. See [§2.8](#28-pollblock). |
| `batch` | newtron | Multiple calls grouped per device. See [§2.9](#29-batchcall). |
| `expect_failure` | newtron | When true, inverts pass/fail — assert that the call fails. |

### 2.6 deviceSelector

A YAML-flexible type that accepts either the literal string `"all"` or a list of device names:

```yaml
devices: all
# or
devices: [switch1, switch2]
```

The selector resolves to a sorted device list at run time via `Resolve(allDeviceNames []string) []string`. `All=true` returns the sorted copy of all device names; explicit lists are returned as-is.

### 2.7 ExpectBlock

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

### 2.8 PollBlock

```go
type PollBlock struct {
    Timeout  time.Duration `yaml:"timeout"`
    Interval time.Duration `yaml:"interval"`
}
```

A step with `poll` repeats its action+expect every `Interval` until the expect succeeds or `Timeout` elapses. Used for convergence checks (BGP sessions, route propagation).

### 2.9 BatchCall

```go
type BatchCall struct {
    Method string         `yaml:"method"`
    URL    string         `yaml:"url"`
    Params map[string]any `yaml:"params,omitempty"`
}
```

The `newtron` action with `batch` runs N calls per device in sequence, collecting results before the expect assertion. Used when one operation needs multiple HTTP calls to set up its preconditions.

### 2.10 StepAction enumeration

| Constant | YAML value | Behavior |
|----------|------------|----------|
| `ActionNewtron` | `newtron` | HTTP call to newtron-server (the most common action). |
| `ActionNewtronCLI` | `newtron-cli` | Subprocess call to `bin/newtron`. Used for loopback testing. |
| `ActionHostExec` | `host-exec` | SSH command on a host VM. Used for ping, traffic generation. |
| `ActionWait` | `wait` | Sleep for `Duration`. |
| `ActionProvision` | `topology-reconcile` | Single `Client.Reconcile(name, "topology", ...)` call per device — the newtron-server performs ConfigReload, lock, ReplaceAll, and SaveConfig internally. High-impact; inline-runs require explicit opt-in. |
| `ActionVerifyProvisioning` | `verify-topology` | Compute drift between device CONFIG_DB and the topology projection. Zero drift = pass. |
| `ActionRunSuite` | `run-suite` | Composition primitive: invoke another sibling suite (resolved under `Runner.TopologiesBase`) as a single step. Child runs in-process with `NoDeploy=true`; depth-counter context bounds recursion to `MaxRunSuiteDepth` (default 5). Excluded from the default inline-allowed list — file-backed suites only. |

---

## 3. Suite Loader and Template Engine

### 3.1 LoadSuite

```go
func LoadSuite(dir string) (*Suite, error)
```

Reads the suite manifest at `<dir>/suite.yaml`, parses every other `*.yaml` file in the directory as a scenario, and runs cross-cutting validation. Returns the populated `Suite` (with `Scenarios` set in dependency order) or a wrapped error.

Validation steps, in order:

1. `<dir>/suite.yaml` exists; `name:` and `topology:` are non-empty.
2. `validateSuiteDeclaration`: every `targets.<key>` is a recognized plural (in `singularizeMap`); every target list is non-empty; every target value matches `^[A-Za-z0-9_-]+$`; every `ParameterSpec.ValidateDeclaration` passes.
3. For each `*.yaml` file other than `suite.yaml`: `ParseScenario` parses the file; the scenario must not set `topology:` or `platform:` (those are suite-level).
4. `validateScenarioAgainstSuite`: for each step that uses `{{target.X}}` or `{{param.X}}` — the scenario opts into parameterization — the references must resolve to declared dimensions/parameters, and the step must not also use `{{device}}` or set `devices:`.
5. `ValidateDependencyGraph` topologically sorts the scenarios on `requires` / `after`.

Failures at any step return descriptive errors; callers map them to HTTP 400 / 404 as appropriate.

### 3.2 Template engine (`template.go`)

```go
func ExpandStep(step Step, target map[string]string, params map[string]any) (Step, error)
func CollectTemplateReferences(step Step) (targets, params []string, hasDevice bool)
```

`ExpandStep` returns a fresh `Step` copy with every templated string substituted. The runner calls it once per (target-binding × repeat-iteration) so concurrent or repeated expansions never mutate the source step. Each step field is expanded under the substitution context appropriate to its downstream consumer (see [§3.3](#33-context-aware-substitution)):

| Step field | Context |
|---|---|
| `URL`, `Batch[].URL` | URL (path-escape) |
| `Command` | Shell (single-quote wrap) |
| `Params`, `Batch[].Params` | JSON (typed full-token preserve, otherwise inline) |
| `Expect.JQ` | JQ (JSON-quote strings, literal numerics) |
| `Expect.Contains` | Raw (no escape) |

Undefined references (`{{target.foo}}` where `foo` isn't in `target`) abort with a descriptive error. The error path is defensive — `LoadSuite` should have rejected unresolved references at parse time.

`CollectTemplateReferences` walks every templated surface (URL, Command, Params, Batch fields, Expect fields) and returns the distinct set of referenced targets/params plus whether the literal `{{device}}` token appears anywhere. Used by `ScenarioIsParameterized` and `validateScenarioAgainstSuite`.

### 3.3 Context-aware substitution

`applyTemplate(s, target, params, ctx)` is the single substitution primitive. The `ctx` selects how each substituted value is encoded as it lands in `s`:

| Context | Constant | Encoding |
|---|---|---|
| URL path component | `ctxURL` | `url.PathEscape` on every value |
| URL query-string value | `ctxURLQuery` | `url.QueryEscape` on every value — escapes `&`, `=`, `+` that PathEscape leaves alone, so a string parameter dropped into a query position can't smuggle extra parameters |
| Shell argument | `ctxShell` | single-quote wrap; embedded `'` becomes `'\''` (POSIX idiom) |
| JQ expression | `ctxJQ` | `int` / `int64` / `float64` / `bool` emitted as JQ literal; strings emitted via `jsonQuote` (the user writes `{{param.X}}` WITHOUT surrounding quotes — the engine adds them) |
| Free-form text | `ctxRaw` | no encoding |

URL substitution is dispatched via `applyTemplateURL`, which splits the input at the first `?` and applies `ctxURL` to the path portion and `ctxURLQuery` to the query portion. Both step.URL and BatchCall.URL route through this helper.

Target values pass the `^[A-Za-z0-9_-]+$` whitelist before reaching substitution, so they need no further escape beyond what each context demands defensively. Parameter values may be free-form strings — context-aware encoding is the only defense against injection at substitution time.

`expandMapAny` handles the JSON-params case specially: a string value matching `fullTokenRe` (the entire value is one `{{token}}`) is replaced by the parameter's typed Go value (preserving int / bool / etc. through `json.Marshal`); other strings go through `applyTemplate` with `ctxRaw` (Go's JSON encoder handles JSON escaping at marshal time).

## 4. Parser (`parser.go`)

### 4.1 ParseScenario

```go
func ParseScenario(path string) (*Scenario, error)
```

Reads the YAML file at `path`, unmarshals into a `Scenario`, validates required fields, and validates each step against its action's field requirements. Returns the parsed scenario or a wrapped error. Does not perform suite-level cross-checks — that's [`LoadSuite`'s](#31-loadsuite) job.

### 4.2 ParseScenarioBytes

```go
func ParseScenarioBytes(data []byte) (*Scenario, error)
```

The bytes-in variant. Used by `PUT /newtrun/v1/suites/{suite}/scenarios/{name}` to validate the request body before any disk write — the server is the single point that knows the accept set.

### 4.3 Validation rules

Per-action requirements enforced by `validateStepFields` (the `stepValidations` table in `parser.go`):

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

### 4.4 HasRequires + ComputeTargetChain

```go
func HasRequires(scenarios []*Scenario) bool
func ComputeTargetChain(scenarios []*Scenario, target string) ([]*Scenario, error)
```

`HasRequires` is a quick probe: do any scenarios in the suite declare `requires` or `after`? The Runner topologically sorts only when at least one does.

`ComputeTargetChain` returns the minimum dependency chain reaching `target` — used by `newtrun start --target <name>` to skip everything not on the path.

---

## 5. State Persistence (`state.go`)

### 5.1 RunState

```go
type RunState struct {
    Suite     string          `json:"suite"`
    Topology  string          `json:"topology"`
    Platform  string          `json:"platform"`
    Target    string          `json:"target,omitempty"`
    Status    SuiteStatus     `json:"status"`
    Started   time.Time       `json:"started"`
    Updated   time.Time       `json:"updated"`
    Finished  time.Time       `json:"finished,omitempty"`
    Scenarios []ScenarioState `json:"scenarios"`
}
```

Persisted to `~/.newtron/newtrun/<key>/state.json` after every progress event. `<key>` is the suite name for file-backed runs or a UUID for inline runs (separate `_inline/<uuid>/` subdirectory keeps the namespaces clean).

The fields exposed here are the abstract run identity — name (Suite), topology, platform, lifecycle status, and per-scenario progress. Storage internals (where `suite.yaml` lives on disk, which spec directory the runner used) are deliberately absent: clients address suites by *name*; resolving a name to bytes is server-internal and must not leak through the wire (§33 Public API Boundary). The legacy CLI-process PID lock retired when the runner became a goroutine under the registry — the AcquireLock / ReleaseLock helpers and the `pid` field were deleted together.

### 5.2 SuiteStatus

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

### 5.3 SuiteStatusFromOutcome

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

### 5.4 ScenarioState and StepState

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

### 5.5 State directory helpers

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

### 5.6 CheckPausing

```go
func CheckPausing(suite string) bool
```

Reads `state.json` and returns true when `state.Status == SuiteStatusPausing`. The Runner calls this at each scenario boundary in `iterateScenarios` — that's how the pause signal propagates from `handlePauseRun` (which only writes the file) to the running goroutine.

---

## 6. Runner Internals (`runner.go`)

### 6.1 Runner

```go
type Runner struct {
    SuiteDir       string         // the suite this runner executes (holds suite.yaml + scenario YAMLs)
    TopologiesBase string         // root for resolving sibling suites called by run-suite steps
    ServerURL      string         // newtron-server HTTP address
    NetworkID      string         // network identifier for server operations
    Client         *client.Client // HTTP client for all SONiC operations
    NewtlabURL     string         // newtlab-server HTTP address
    NewtlabClient  LabClient      // deploy / destroy / status via HTTP (§27)
    HostConns      map[string]*ssh.Client
    Progress       ProgressReporter
    Topology       string         // topology name (from server)
    SpecDir        string         // spec directory (from server)

    discoveredPlatform string
    opts               RunOptions
    scenario           *Scenario
}
```

| Field | Set by | Used by |
|-------|--------|---------|
| `SuiteDir` | `NewRunner(dir)` or `handleStartRun` (via `ResolveSuiteDir` glob) | `LoadSuite` to read `suite.yaml`; parser to enumerate scenarios. |
| `TopologiesBase` | `handleStartRun` from `Config.TopologiesBase` | `runSuiteExecutor` to resolve child suites named in `run-suite` steps. |
| `ServerURL` | `handleStartRun` from `req.NewtronServer` or server default | `client.Client` constructor + steps_cli passes to subprocess via `--server`. |
| `NetworkID` | `handleStartRun` via `resolveNetworkID` — three-level fallback `req.NetworkID` → `suite.Topology` → `Config.NetworkID` (#116). Inline runs skip the suite step. | Network identifier in HTTP calls. |
| `Client` | `connectToServer` | Every `newtron` action HTTP call. |
| `NewtlabClient` | `handleStartRun` from `Config.NewtlabClient` (composed at the entry point from `--newtlab-server`) | `deployTopology` → `Deploy` / `Destroy` / `LabStatus`. Per §27, newtlab owns LabState; newtrun reaches it via HTTP, never in-process via `newtlab.NewLab`. |
| `HostConns` | `connectDevices` | `host-exec` SSH calls. |
| `Progress` | `handleStartRun` (HTTPReporter → StateReporter chain) | Every lifecycle event. |
| `Topology` | `connectToServer` from `GET /network` | Verified against `Suite.Topology` (declared once in `suite.yaml`). |
| `SpecDir` | `connectToServer` | Used by `Reconcile` action and by `cmd_stop` when destroying. |

### 6.2 RunOptions

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

### 6.3 Run(ctx, opts)

The top-level entry. Resolves scenarios from `opts.All` / `opts.Target` / `opts.Scenario`, validates the dependency graph, connects to newtron-server, deploys the topology if needed, connects to host devices, then enters `iterateScenarios`. Always emits `SuiteEnd` before returning (even on error) so reporters carry a terminal event.

The terminal status passed to `SuiteEnd` is computed via `SuiteStatusFromOutcome(err, results)` — the wire and the persisted state get the same value.

### 6.4 iterateScenarios

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

### 6.5 runScenarioSteps

```go
func (r *Runner) runScenarioSteps(
    ctx context.Context, sc *Scenario, opts RunOptions, result *ScenarioResult,
)
```

Executes the steps of a scenario, recording per-step results into `result.Steps`. Honors `sc.Repeat` (run the step list N times). A step's failure stops the scenario at that step — subsequent steps are not run. When `Repeat > 1`, `result.FailedIteration` is set to the iteration number that failed, and outer iterations are not run.

### 6.6 Dispatcher

A package-local map dispatches `step.Action` to a `stepExecutor`:

| Action | Executor |
|--------|----------|
| `ActionNewtron` | `newtronExecutor` ([§10.1](#101-newtronexecutor)) |
| `ActionNewtronCLI` | `newtronCLIExecutor` ([§10.2](#102-newtroncliexecutor)) |
| `ActionHostExec` | `hostExecExecutor` ([§10.3](#103-hostexecexecutor)) |
| `ActionWait` | `waitExecutor` (sleep) |
| `ActionProvision` | `provisionExecutor` ([§10.4](#104-provisionexecutor)) |
| `ActionVerifyProvisioning` | `verifyProvisioningExecutor` — drift check against the topology projection. |

### 6.7 connectToServer / connectDevices

```go
func (r *Runner) connectToServer() error
func (r *Runner) connectDevices() error
```

`connectToServer` constructs the newtron HTTP client (`client.New(r.ServerURL, r.NetworkID)`) and reads the server's loaded network spec to populate `r.Topology` and `r.SpecDir`. Fails fast if the server has no network registered.

`connectDevices` walks the topology's device list, identifies hosts via `Client.IsHostDevice(name)`, opens an SSH connection to each, and stores them in `r.HostConns`. Skipped when `opts.NoDeploy == true`.

---

## 7. Progress Reporting (`progress.go`)

### 7.1 ProgressReporter

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
| `StateReporter` | Persists `RunState` to disk after every callback; chainable. |
| `HTTPReporter` ([§8.3](#83-httpreporter)) | Publishes events to the `EventBroker`; chainable. |

Terminal output for `newtrun start` lives client-side in
`cmd/newtrun/cmd_start.go`'s `renderEvent`, which prints one line per
SSE event from the server's `HTTPReporter` — there is no in-process
terminal reporter in the `pkg/newtrun` library.

### 7.2 StateReporter

```go
type StateReporter struct {
    State *RunState
    Save  func(*RunState) error  // SaveRunState or SaveInlineRunState
    Inner ProgressReporter       // chain (e.g., HTTPReporter)
}
```

Mutates `r.State` on every callback and saves to disk. `Save` is injected so the same reporter logic works for both the suite and inline namespaces. `Inner` lets a downstream reporter (typically `HTTPReporter`) receive the same events.

### 7.3 StepProgress

The one callback with no current producer in the Runner. Reserved for the per-device-op streaming consumer in `steps_newtron.go` (gated on newtron Phase 2b — when the newtron-server's `WriteResult` stream lands, `steps_newtron.go` will forward each device op via `StepProgress`).

### 7.4 SuiteEnd carries status

`SuiteEnd` carries a `SuiteStatus` so the wire event distinguishes "the suite ran and N scenarios failed" from "the server died mid-run". All `ProgressReporter` implementations honor it; the `SuiteEndPayload` JSON field carries the same value.

---

## 8. HTTP Server Package (`pkg/newtrun/newtrun/v1/`)

### 8.1 Server

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

### 8.2 Config defaults

`NewServer` fills empty fields:

| Field | Default |
|-------|---------|
| `TopologiesBase` | `newtrun/topologies` |
| `NewtronServer` | `http://127.0.0.1:18080` |
| `NetworkID` | `default` (final fallback only — file-backed runs default to `suite.Topology` first; see `resolveNetworkID` in [§8 handleStartRun](#8-http-server-package-pkgnewtrunapi)) |
| `InlineURLPrefix` | empty (no URL restriction enforced by default; see [§8.7](#87-inlinesafetypolicy)) |
| `Logger` | `log.Default()` |

### 8.3 HTTPReporter

```go
type HTTPReporter struct {
    Broker *EventBroker
    RunKey string
    Inner  newtrun.ProgressReporter
}
```

Implements `ProgressReporter`. Each callback constructs an `Event` and publishes it via `Broker.Publish(RunKey, ev)`. `Inner` is typically a `StateReporter`, so disk-persistence and SSE-publication happen on the same event.

### 8.4 EventBroker

```go
type EventBroker struct {
    mu          sync.RWMutex
    subscribers map[string]map[chan Event]struct{}
}
```

SSE multiplexer. The handler `handleRunEvents` calls `Subscribe(runKey)` which returns a buffered `chan Event` (capacity 64) and an unsubscribe func.

`Publish(runKey, ev)` fans out to every subscriber of that key. **Drop-on-full:** if a subscriber's buffer is full, the event is dropped for that subscriber only — other subscribers still receive it. SSE is best-effort.

### 8.5 RunRegistry

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

### 8.6 reconcileStaleStatus

```go
func (s *Server) reconcileStaleStatus(state *newtrun.RunState, runKey string)
```

The server-restart-honesty rule ([HLD §9.3](hld.md)). When `handleGetRun` or `handleListRuns` loads a `state.json` that claims `running` or `pausing` but the registry has no live entry, the in-memory copy is relabeled to `aborted` before serialization. The disk file is not rewritten — the next finalizer write applies the canonical status.

Called from both `handleGetRun` and `handleListRuns` via this helper (per `docs/ai-instructions.md` §7 — second instance of a pattern must consolidate).

### 8.7 InlineSafetyPolicy

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

### 8.8 finalizeRunState + finalizeInlineState

```go
func finalizeRunState(state *newtrun.RunState, results []*newtrun.ScenarioResult, runErr error)
func finalizeInlineState(state *newtrun.RunState, results []*newtrun.ScenarioResult, runErr error)
```

Run after the Runner goroutine returns. Both delegate to `SuiteStatusFromOutcome` for the status; they differ only in which persistence function they call (`SaveRunState` vs `SaveInlineRunState`).

### 8.9 Route registration

`buildHandler()` (in `server.go`) registers the HTTP routes against `http.ServeMux`. See [api.md](api.md) for the canonical list; the handler functions are spread across `runs.go` / `suites.go` / `scenarios.go` per `docs/DESIGN_PRINCIPLES.md` §28 (file-level feature cohesion).

---

## 9. HTTP Client Package (`pkg/newtrun/client/`)

### 9.1 Client

```go
type Client struct {
    baseURL      string
    httpClient   *http.Client  // for short request/response calls
    streamClient *http.Client  // for SSE (no timeout)
}
```

| Method | Endpoint |
|--------|----------|
| `Health(ctx)` | GET /newtrun/v1/health |
| `ListRuns(ctx)` | GET /newtrun/v1/runs |
| `GetRun(ctx, suite)` | GET /newtrun/v1/runs/{suite} |
| `StartRun(ctx, req)` | POST /newtrun/v1/runs |
| `PauseRun(ctx, suite)` | POST /newtrun/v1/runs/{suite}/pause |
| `StopRun(ctx, suite)` | POST /newtrun/v1/runs/{suite}/stop |
| `DeleteRun(ctx, suite)` | DELETE /newtrun/v1/runs/{suite} |
| `StreamEvents(ctx, suite, handle)` | GET /newtrun/v1/runs/{suite}/events (SSE) |
| `ListSuites(ctx)` | GET /newtrun/v1/suites |
| `ListSuiteScenarios(ctx, suite)` | GET /newtrun/v1/suites/{suite}/scenarios |
| `CreateSuite(ctx, name)` | POST /newtrun/v1/suites |
| `DeleteSuite(ctx, name)` | DELETE /newtrun/v1/suites/{suite} |
| `GetScenario(ctx, suite, name)` | GET /newtrun/v1/suites/{suite}/scenarios/{name} |
| `PutScenario(ctx, suite, name, body)` | PUT /newtrun/v1/suites/{suite}/scenarios/{name} |
| `DeleteScenario(ctx, suite, name)` | DELETE /newtrun/v1/suites/{suite}/scenarios/{name} |

### 9.2 Transport helpers

```go
func (c *Client) get(ctx, path, out)  error              // JSON envelope read
func (c *Client) post(ctx, path, in, out) error          // JSON envelope write
func (c *Client) do(ctx, method, path, in, out) error    // shared body of get/post
func (c *Client) getRaw(ctx, path) ([]byte, error)       // non-envelope (YAML scenario body)
func (c *Client) putRaw(ctx, path, body) error           // non-envelope (YAML scenario PUT)
```

`do` is the shared body — marshal `in` to JSON, dispatch, parse envelope, unmarshal `data` into `out`. `getRaw`/`putRaw` exist because scenario YAML is not JSON-enveloped.

### 9.3 StreamEvents

```go
func (c *Client) StreamEvents(
    ctx context.Context, suite string, handle func(Event),
) error
```

Opens a long-running GET to `/newtrun/v1/runs/{suite}/events`, parses the SSE frame stream line-by-line, decodes each `data:` line as an `Event`, and calls `handle(ev)`. Returns when the context is canceled or the connection closes.

SSE comment lines (those starting with `:`) are silently skipped — they're heartbeats and subscription confirmations.

### 9.4 ServerError

```go
type ServerError struct {
    StatusCode int
    Message    string
}
func (e *ServerError) Error() string
```

Any 4xx/5xx response from a do/getRaw/putRaw call wraps the body in a `ServerError`. `errors.As` recovers the status code; the CLI's `notFoundIsNil` uses this to treat 404 as "absent" rather than failure.

---

## 10. Step Executors

Source files: `steps.go`, `steps_newtron.go`, `steps_cli.go`, `steps_host.go`.

### 10.1 newtronExecutor

Action: `newtron`. Dispatches one HTTP call per device (or one global call for non-device-scoped URLs). Per-device URL expansion replaces `{{device}}` in `step.URL` via `strings.ReplaceAll` in `expandURL` (`pkg/newtrun/steps_newtron.go:225`). Response is matched against `step.Expect` and optionally polled via `step.Poll`.

`batch`-mode runs N URLs per device in sequence before the expect; useful for setting up preconditions.

### 10.2 newtronCLIExecutor

Action: `newtron-cli`. Spawns `bin/newtron <device> <command...>` as a subprocess. Passes `--server <runner.ServerURL>` so the subprocess targets the same newtron-server the in-process client uses. Honors `step.Expect` against the subprocess exit code, stdout, and stderr.

If the command includes `--json` and `step.Expect.JQ` is set, the executor parses stdout as JSON and evaluates the jq expression.

### 10.3 hostExecExecutor

Action: `host-exec`. Runs `step.Command` over SSH on the named host devices. Per-device parallelism (each device's SSH call runs in its own goroutine; results collected and merged).

Honors `step.Expect.SuccessRate` for ping commands: parses the "N% packet loss" line and asserts the success rate is at least the configured threshold.

### 10.4 provisionExecutor

Action: `topology-reconcile`. Calls `POST /networks/{netID}/nodes/{device}/intent/reconcile?mode=topology` once per device through `Client.Reconcile`. The reconcile is one call — the newtron-server handles ConfigReload, lock, ReplaceAll, and SaveConfig internally — not deploy+reconcile+verify on the client side. This is the high-impact action — it can replace an entire device's intent state. Inline runs require explicit opt-in (`allow_reconcile: true` in the request body).

### 10.5 Multi-device helpers

```go
func (r *Runner) resolveDevices(step *Step) []string
func (r *Runner) executeForDevices(step *Step, fn func(name string) (string, error)) *StepOutput
```

`resolveDevices` resolves `step.Devices` against the topology's device list. `executeForDevices` runs `fn` concurrently across devices, collects per-device results, and produces the merged `StepOutput`.

---

## 11. newtlab Integration (`deploy.go`)

```go
// LabClient is the subset of pkg/newtlab/client.Client newtrun uses.
// Production code passes *newtlabclient.Client directly — the
// interface exists for unit-testing without a live newtlab-server.
type LabClient interface {
    LabStatus(ctx context.Context, topology string) (*newtlab.LabState, error)
    Deploy(ctx context.Context, topology string, opts api.DeployRequest) error
    Destroy(ctx context.Context, topology string) error
}

func DeployTopology(ctx context.Context, client LabClient, topology string) error
func EnsureTopology(ctx context.Context, client LabClient, topology string) error
func DestroyTopology(ctx context.Context, client LabClient, topology string) error
```

Per §27 (Single Owner): newtlab owns LabState; newtrun reaches it through newtlab-server's HTTP surface, never in-process via `newtlab.NewLab`. The composed `newt-server` binary instantiates the client against its own loopback listener; standalone `newtrun-server` instantiates it from the `--newtlab-server` flag. Either way only newtlab-server writes `~/.newtlab/labs/<name>/state.json`.

`EnsureTopology` reuses an existing lab if all nodes report `running`, otherwise calls `Deploy` (forced). Used between iterations of the same suite (`newtrun start` → fail → fix → `newtrun start`) to avoid a full redeploy when the lab is healthy.

`DeployTopology` forces a fresh deployment. The Runner uses `EnsureTopology` in lifecycle mode (`opts.Suite != ""`) and `DeployTopology` in standalone mode; in the latter case a defer destroys the lab when `opts.Keep` is false.

`DestroyTopology` is also called from `cmd_stop` (CLI side) — the CLI constructs its own `*newtlabclient.Client` against the URL resolved from `--newtlab-server` / `NEWTLAB_SERVER` / the default `http://127.0.0.1:18080`.

`Deploy` is synchronous from the caller's perspective: it POSTs `/deploy` (which returns 202 Accepted immediately), then subscribes to the per-topology SSE stream and blocks until a terminal `complete` or `error` event arrives — matching the in-process `Lab.Deploy` semantics the HTTP path replaced.

---

## 12. Results & Reporting (`report.go`)

### 12.1 ScenarioResult and StepResult

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

### 12.2 ReportGenerator

```go
type ReportGenerator struct {
    Results []*ScenarioResult
}

func (g *ReportGenerator) WriteMarkdown(path string) error
func (g *ReportGenerator) WriteJUnit(path string) error
```

Produces the post-run summary report. The CLI calls `WriteMarkdown` unconditionally (to `newtrun/.generated/report.md`) and `WriteJUnit` when `--junit <path>` is set. Both consume `Results` reconstructed from `ScenarioEnd` SSE event payloads on the CLI side.

### 12.3 Output formats

| Format | Used by |
|--------|---------|
| Markdown table | Quick scrollback for ad-hoc runs. |
| JUnit XML | CI consumption (Jenkins, GitHub Actions, GitLab). |

---

## 13. Error Handling (`errors.go`)

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

## 14. CLI Binary (`cmd/newtrun/`)

### 14.1 main.go

Root cobra command. Persistent flag `--newtrun-server <url>` (env: `NEWTRUN_SERVER`, default: `http://127.0.0.1:18080` (newt-server front)). Subcommands:

| Command | Endpoint | Notes |
|---------|----------|-------|
| `start <suite>` | POST /newtrun/v1/runs + SSE | Streams events; exits on terminal SuiteEnd. |
| `pause <suite>` | POST /newtrun/v1/runs/{suite}/pause | Returns when pause signal lands. |
| `stop <suite>` | GET + POST /stop + newtlab.Destroy + DELETE | Multi-step orchestration. |
| `status [-s <pattern>]` | GET /newtrun/v1/runs + /newtrun/v1/runs/{suite} | Lists all suites; `-s/--suite <pattern>` filters by substring match. `--monitor` auto-refreshes. |
| `list [suite]` | GET /newtrun/v1/suites + /newtrun/v1/suites/{suite}/scenarios | Lists suites; with a suite name lists its scenarios. |
| `suites` | GET /newtrun/v1/suites | Hidden alias of `list`. |
| `suite create/delete <name>` | POST/DELETE /newtrun/v1/suites | Per [§8](#8-http-server-package-pkgnewtrunapi). |
| `scenario list/get/put/delete` | /newtrun/v1/suites/{suite}/scenarios* | Per [§8](#8-http-server-package-pkgnewtrunapi). |
| `topologies` | GET /newtron/v1/networks | List newtron-registered networks (delegated). |
| `topology create <name>` | POST /newtron/v1/networks with `scaffold=true` | Scaffold an empty spec layout and register it with newtron in one call. |
| `actions` | static | Help text describing the action vocabulary. |
| `version` | static | Build info. |

Exit codes:

| Code | Meaning |
|------|---------|
| 0 | All scenarios passed |
| 1 | At least one scenario failed |
| 2 | Infrastructure error (deploy / connection / server lost mid-run / run aborted) |

### 14.2 clientutil.go

```go
func newClient() *client.Client        // builds Client from --newtrun-server / env
func requireServer(ctx, c) error       // probes GET /newtrun/v1/health; clean error on connection refused
func notFoundIsNil(err) bool           // ServerError 404 → nil for absent-state reads
func fetchRunStateViaClient(suite) (*newtrun.RunState, error)
func listSuiteNamesViaClient() ([]string, error)
```

Every state-changing CLI command calls `requireServer` before its real work. Strict Option A: read commands also call `requireServer` — the CLI never reads `~/.newtron/newtrun/` directly.

### 14.3 cmd_start.go highlights

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

### 14.4 cmd_scenario.go

Per-scenario CRUD subcommands:

```
newtrun suite create <name>
newtrun suite delete <name>
newtrun scenario list <suite>
newtrun scenario get <suite> <name>
newtrun scenario put <suite> <name> [--file <path>]
newtrun scenario delete <suite> <name>
```

`put` defaults to stdin if `--file` is not given; the body is sent raw to `PUT /newtrun/v1/suites/{suite}/scenarios/{name}` and validated server-side.

---

## 15. Engine Mount Point (`cmd/newt-server/`)

The newtrun engine has no main package of its own. `cmd/newt-server/main.go` instantiates the engine — calling `pkg/newtrun/api.NewServer(cfg)` — and mounts its routes on the composed mux alongside the newtron and newtlab engines. Engine-relevant flags on `cmd/newt-server`:

| Flag | Default | Meaning |
|------|---------|---------|
| `--listen` | `127.0.0.1:18080` | Bind address for the composed listener; non-loopback values require `--auth-pam-service` plus `--tls-cert/--tls-key/--tls-ca` to be set. |
| `--spec-dir` | (required) | Spec directory passed to the newtron engine's `RegisterNetwork`. The newtrun engine discovers suites by globbing `<topologies-base>/*/suites/<name>/`; override the base with `--topologies-base` if needed. |
| `--topologies-base` | `newtrun/topologies` | Root of the topologies tree. The newtrun engine resolves suite names by globbing `<base>/*/suites/<name>/`. |

The Config struct backing the newtrun engine has `NewtronServer` and `NetworkID` fields with defaults (`http://127.0.0.1:18080` and `default`), inherited from the composed boundary. Per-request overrides via the `newtron_server` and `network_id` fields on `POST /newtrun/v1/runs` are the supported way to point a run at a non-default newtron-server.

`cmd/newt-server` installs a SIGTERM handler that calls each engine's `Stop(ctx)` — for newtrun, that cancels every in-flight run, waits up to 5 seconds for them to drain, then shuts down the HTTP listener. The Runner's ctx-cancel check ([§6.4](#64-iteratescenarios)) is what makes the drain produce honest status (`aborted`) instead of synthetic FAIL events.

---

*Source-traced against `pkg/newtrun/`, `pkg/newtrun/api/`, `pkg/newtrun/client/`, `cmd/newtrun/`, and `cmd/newt-server/`. Type definitions are exact; method signatures are exact. If you find a discrepancy, the code is the authority — please open an issue or PR.*
