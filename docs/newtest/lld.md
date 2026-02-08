# newtest Low-Level Design (LLD)

newtest is an E2E test orchestrator for newtron and SONiC. It parses YAML scenario files, deploys VM topologies via vmlab, provisions devices via newtron, and runs multi-step verification sequences. This document covers `pkg/newtest/` and `cmd/newtest/`.

For the architectural principles behind newtron, vmlab, and newtest, see [Design Principles](../DESIGN_PRINCIPLES.md). For the high-level architecture, see [newtest HLD](hld.md). For the device connection layer, see [Device Layer LLD](../newtron/device-lld.md).

---

## 1. Package Structure

```
newtron/
├── cmd/
│   └── newtest/
│       ├── main.go               # Entry point, root command
│       ├── cmd_run.go            # run subcommand
│       ├── cmd_list.go           # list subcommand
│       └── cmd_topologies.go     # topologies subcommand
├── pkg/
│   └── newtest/
│       ├── scenario.go           # Scenario, Step, StepAction, ExpectBlock types
│       ├── parser.go             # ParseScenario, ValidateScenario
│       ├── runner.go             # Runner, RunOptions, RunScenario
│       ├── steps.go              # StepExecutor interface, all executor implementations
│       ├── deploy.go             # DeployTopology, DestroyTopology (vmlab wrapper)
│       ├── report.go             # ScenarioResult, StepResult, ReportGenerator
│       └── newtest_test.go       # Unit tests
└── newtest/                      # E2E test assets
    ├── topologies/
    │   ├── 2node/specs/          # 2-node topology spec dir
    │   └── 4node/specs/          # 4-node topology spec dir
    ├── scenarios/                # YAML scenario files
    └── .generated/               # Runtime output (gitignored)
```

---

## 2. Core Types (`pkg/newtest/scenario.go`)

### 2.1 Scenario — Parsed Test Definition

```go
// Scenario is a parsed test scenario from a YAML file.
// Each scenario targets a single topology and platform.
type Scenario struct {
    Name        string   `yaml:"name"`
    Description string   `yaml:"description"`
    Topology    string   `yaml:"topology"`    // "2node", "4node"
    Platform    string   `yaml:"platform"`    // "sonic-vpp", "sonic-vs"
    Steps       []Step   `yaml:"steps"`
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Unique scenario identifier (matches filename without `.yaml`) |
| `description` | yes | Human-readable description shown in `newtest list` |
| `topology` | yes | Topology directory name under `newtest/topologies/` |
| `platform` | yes | Platform name from `platforms.json` in the topology spec dir |
| `steps` | yes | Ordered list of test steps |

### 2.2 Step — Single Test Step

```go
// Step is a single action within a scenario.
// Fields are action-specific — the parser validates that only relevant
// fields are set for each action type.
//
// Design note: Step is intentionally a flat union — all action-specific fields
// live on one struct. This is a deliberate trade-off for clean YAML unmarshaling.
// Go types can't enforce "if action is verify-route then prefix is required" —
// that validation lives in ValidateScenario (§3.2). The alternative (per-action
// types with a YAML discriminator) adds unmarshaling complexity without
// meaningful safety, since the YAML is the source of truth.
type Step struct {
    Name      string         `yaml:"name"`
    Action    StepAction     `yaml:"action"`
    Devices   DeviceSelector `yaml:"devices,omitempty"`   // "all", ["leaf1"], or ["leaf1", "leaf2"]

    // wait
    Duration  time.Duration  `yaml:"duration,omitempty"`

    // verify-config-db, verify-state-db
    Table     string         `yaml:"table,omitempty"`
    Key       string         `yaml:"key,omitempty"`

    // verify-route (single-device: parser validates len(Devices) == 1)
    Prefix    string         `yaml:"prefix,omitempty"`
    VRF       string         `yaml:"vrf,omitempty"`

    // apply-service, remove-service
    Interface string         `yaml:"interface,omitempty"`
    Service   string         `yaml:"service,omitempty"`
    Params    map[string]any `yaml:"params,omitempty"`

    // apply-baseline
    Configlet string            `yaml:"configlet,omitempty"`
    Vars      map[string]string `yaml:"vars,omitempty"`

    // ssh-command
    Command   string         `yaml:"command,omitempty"`

    // verify-ping (single-device: parser validates len(Devices) == 1)
    Target    string         `yaml:"target,omitempty"`
    Count     int            `yaml:"count,omitempty"`

    // All verify-* actions, ssh-command
    Expect    *ExpectBlock   `yaml:"expect,omitempty"`
}
```

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
    ActionApplyBaseline      StepAction = "apply-baseline"
    ActionSSHCommand         StepAction = "ssh-command"
)
```

**Owner mapping** (see newtest HLD §5.1):

| Action | Implemented By | newtron Method |
|--------|----------------|----------------|
| `provision` | newtron | `TopologyProvisioner.ProvisionDevice()` |
| `verify-provisioning` | newtron | `Device.VerifyChangeSet()` |
| `verify-config-db` | newtron | `ConfigDBClient.Get*()` |
| `verify-state-db` | newtron | `StateDBClient.Get*()` |
| `verify-bgp` | newtron | `Device.RunHealthChecks("bgp")` |
| `verify-health` | newtron | `Device.RunHealthChecks()` |
| `verify-route` | newtron | `Device.GetRoute()` / `GetRouteASIC()` |
| `verify-ping` | **newtest native** | SSH ping |
| `apply-service` | newtron | `Interface.ApplyService()` |
| `remove-service` | newtron | `Interface.RemoveService()` |
| `apply-baseline` | newtron | `Device.ApplyBaseline()` |
| `ssh-command` | **newtest native** | SSH exec |
| `wait` | **newtest native** | `time.Sleep` |

### 2.4 DeviceSelector

```go
// DeviceSelector handles the two YAML forms for the "devices" field:
//   devices: all           → All: true
//   devices: [leaf1, leaf2] → Devices: ["leaf1", "leaf2"]
type DeviceSelector struct {
    All     bool
    Devices []string
}

// UnmarshalYAML implements yaml.Unmarshaler.
// Accepts a bare string "all" or a list of device names.
func (ds *DeviceSelector) UnmarshalYAML(unmarshal func(interface{}) error) error {
    // Try string first
    var s string
    if err := unmarshal(&s); err == nil {
        if s == "all" {
            ds.All = true
            return nil
        }
        return fmt.Errorf("invalid device selector string: %q (expected \"all\")", s)
    }
    // Try list
    return unmarshal(&ds.Devices)
}

// Resolve returns the list of device names to target, given the full
// set of devices in the topology. If All is true, returns all devices
// sorted by name for deterministic ordering.
func (ds *DeviceSelector) Resolve(allDevices []string) []string
```

### 2.5 ExpectBlock — Action-Specific Assertions

```go
// ExpectBlock is a union of all action-specific expectation fields.
// Each action uses a subset of fields; the parser validates this.
type ExpectBlock struct {
    // verify-config-db: three assertion modes
    MinEntries *int              `yaml:"min_entries,omitempty"` // mode 1: count keys ≥ min
    Exists     *bool             `yaml:"exists,omitempty"`      // mode 2: key exists or not
    Fields     map[string]string `yaml:"fields,omitempty"`      // mode 3: field value match

    // Polling (verify-state-db, verify-bgp, verify-route, verify-ping)
    Timeout      time.Duration   `yaml:"timeout,omitempty"`       // max wait (default varies)
    PollInterval time.Duration   `yaml:"poll_interval,omitempty"` // poll interval (default 5s)

    // verify-bgp
    State string `yaml:"state,omitempty"` // "Established"

    // verify-health
    Overall string `yaml:"overall,omitempty"` // "ok"

    // verify-route
    Protocol  string `yaml:"protocol,omitempty"`   // "bgp", "connected"
    NextHopIP string `yaml:"nexthop_ip,omitempty"` // "10.0.0.1"
    Source    string `yaml:"source,omitempty"`      // "app_db" or "asic_db"

    // verify-ping
    SuccessRate *float64 `yaml:"success_rate,omitempty"` // 0.0-1.0 (default 1.0)

    // ssh-command
    Contains string `yaml:"contains,omitempty"` // substring match
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

## 3. Scenario Parser (`pkg/newtest/parser.go`)

### 3.1 ParseScenario

```go
// ParseScenario reads a YAML scenario file and returns a validated Scenario.
// Returns an error if the file cannot be read, parsed, or fails validation.
func ParseScenario(path string) (*Scenario, error)
```

**Flow:**

1. Read file at `path`
2. `yaml.Unmarshal` into `Scenario`
3. Apply defaults to steps (timeout, poll_interval, count)
4. Call `ValidateScenario`
5. Return `*Scenario`

```go
// ParseAllScenarios reads all .yaml files in dir and returns parsed scenarios.
// Stops on first parse error.
func ParseAllScenarios(dir string) ([]*Scenario, error)
```

### 3.2 ValidateScenario

```go
// ValidateScenario checks that a scenario is well-formed:
//   - Name is non-empty
//   - Topology directory exists under topologyDir
//   - Platform exists in platforms.json
//   - Each step has a valid action
//   - Required fields are present for each action type
//   - Device names (when explicit) exist in topology.json
func ValidateScenario(s *Scenario, topologiesDir string) error
```

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
| `apply-baseline` | `devices`, `configlet` |
| `ssh-command` | `devices`, `command` |

### 3.3 YAML Field Mapping

Maps YAML keys to Go struct fields for each action:

| YAML Key | Go Field | Actions |
|----------|----------|---------|
| `name` | `Step.Name` | all |
| `action` | `Step.Action` | all |
| `devices` | `Step.Devices` | all actions (single-device actions validate len == 1) |
| `duration` | `Step.Duration` | wait |
| `table` | `Step.Table` | verify-config-db, verify-state-db |
| `key` | `Step.Key` | verify-config-db, verify-state-db |
| `prefix` | `Step.Prefix` | verify-route |
| `vrf` | `Step.VRF` | verify-route |
| `interface` | `Step.Interface` | apply-service, remove-service |
| `service` | `Step.Service` | apply-service |
| `params` | `Step.Params` | apply-service |
| `configlet` | `Step.Configlet` | apply-baseline |
| `vars` | `Step.Vars` | apply-baseline |
| `command` | `Step.Command` | ssh-command |
| `target` | `Step.Target` | verify-ping |
| `count` | `Step.Count` | verify-ping |
| `expect` | `Step.Expect` | verify-*, ssh-command |

---

## 4. Runner (`pkg/newtest/runner.go`)

### 4.1 Runner — Top-Level Orchestrator

```go
// Runner is the top-level newtest orchestrator. It loads scenarios, deploys
// topologies via vmlab, provisions devices via newtron, runs test steps,
// and collects results.
type Runner struct {
    ScenariosDir  string                        // newtest/scenarios/
    TopologiesDir string                        // newtest/topologies/
    Network       *network.Network              // OO hierarchy (owns devices, specs)
    Lab           *vmlab.Lab                    // vmlab Lab (nil if --no-deploy)
    ChangeSets    map[string]*network.ChangeSet // last ChangeSet per device
    Verbose       bool

    // Set per-scenario in RunScenario before executing steps.
    // Executors access these via r.opts and r.scenario.
    opts     RunOptions
    scenario *Scenario
}
```

| Field | Description |
|-------|-------------|
| `ScenariosDir` | Path to `newtest/scenarios/` |
| `TopologiesDir` | Path to `newtest/topologies/` |
| `Network` | Top-level `network.Network` object (owns devices, specs, OO hierarchy). Replaces the previous `Devices` + `Platforms` fields — devices are accessed via `r.Network.GetDevice(name)`, platforms via `r.Network.GetPlatform()`. |
| `Lab` | vmlab Lab instance from `DeployTopology()` (nil when `--no-deploy`) |
| `ChangeSets` | Accumulated by the runner from executor `StepOutput`; maps device name to last ChangeSet from `provision` or `apply-*` steps; read by `verify-provisioning`. **Last-write-wins**: if multiple steps produce ChangeSets for the same device, only the latest is retained. |
| `Verbose` | Enables detailed output during step execution |

### 4.2 RunOptions

```go
// RunOptions controls Runner behavior from CLI flags.
type RunOptions struct {
    Scenario  string // scenario name (or "" for --all)
    All       bool   // run all scenarios
    Topology  string // override topology (default: from scenario)
    Platform  string // override platform (default: from scenario)
    Keep      bool   // don't destroy after tests
    NoDeploy  bool   // skip deploy/destroy
    Parallel  int    // parallel provisioning count (default 1)
    Verbose   bool
    JUnitPath string // JUnit XML output path (empty = no JUnit)
}
```

### 4.3 RunScenario Flow

```go
func NewRunner(scenariosDir, topologiesDir string) *Runner

// Run executes one or all scenarios and returns results.
// When opts.All is true, runs all scenarios in ParseAllScenarios order.
// When opts.Scenario is set, runs only that scenario.
func (r *Runner) Run(opts RunOptions) ([]*ScenarioResult, error)

// RunScenario executes a single scenario end-to-end.
// ctx is context.Background() from Run(); wrapped with signal.NotifyContext internally.
func (r *Runner) RunScenario(ctx context.Context, scenario *Scenario, opts RunOptions) (*ScenarioResult, error)
```

**RunScenario pseudocode:**

```go
func (r *Runner) RunScenario(ctx context.Context, scenario *Scenario, opts RunOptions) (*ScenarioResult, error) {
    // Store opts and scenario on Runner so executors can access them.
    r.opts = opts
    r.scenario = scenario

    result := &ScenarioResult{
        Name:     scenario.Name,
        Topology: scenario.Topology,
        Platform: scenario.Platform,
    }
    start := time.Now()

    // 1. Resolve topology spec dir
    topology := opts.Topology
    if topology == "" {
        topology = scenario.Topology
    }
    specDir := filepath.Join(r.TopologiesDir, topology, "specs")

    // 2. Deploy topology (unless --no-deploy)
    if !opts.NoDeploy {
        lab, err := DeployTopology(specDir)
        if err != nil {
            result.DeployError = err
            result.Status = StatusError
            result.Duration = time.Since(start)
            return result, nil
        }
        r.Lab = lab

        // VM leak prevention: register deferred destroy on successful deploy.
        // This ensures VMs are cleaned up even if the runner panics or returns early.
        // The --keep flag and normal destroy path both skip if already destroyed.
        if !opts.Keep {
            defer DestroyTopology(r.Lab)
        }
    }

    // 2a. SIGINT handling: signal.NotifyContext ensures Ctrl-C triggers
    // deferred destroy rather than leaving orphaned VMs.
    ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
    defer cancel()

    // 3. Connect to all devices via newtron (builds Network OO hierarchy, connects)
    if err := r.connectDevices(ctx, specDir); err != nil {
        result.DeployError = err
        result.Status = StatusError
        result.Duration = time.Since(start)
        return result, nil
    }

    // 5. Execute steps sequentially, accumulate ChangeSets from output
    r.ChangeSets = make(map[string]*network.ChangeSet)
    for i, step := range scenario.Steps {
        output := r.executeStep(ctx, &step, i, len(scenario.Steps), opts)

        // Merge any ChangeSets returned by the executor.
        // Last-write-wins: if a later step produces a ChangeSet for the same
        // device, it replaces the earlier one. This is correct because
        // verify-provisioning should verify the most recent ChangeSet
        // (the one matching the current CONFIG_DB state).
        for name, cs := range output.ChangeSets {
            r.ChangeSets[name] = cs
        }

        result.Steps = append(result.Steps, *output.Result)

        // Stop on first failure (fail-fast)
        if output.Result.Status == StatusFailed || output.Result.Status == StatusError {
            break
        }
    }

    // 6. Compute overall status
    result.Status = computeOverallStatus(result.Steps)
    result.Duration = time.Since(start)

    // 7. Destroy topology
    // Handled by defer above. The deferred DestroyTopology runs on:
    //   - Normal completion
    //   - Early return (deploy error, connect error)
    //   - SIGINT (via signal.NotifyContext)
    //   - Panic
    // Skipped when --keep or --no-deploy (defer not registered).

    return result, nil
}
```

### 4.4 Step Dispatch

```go
// executors maps each StepAction to its executor implementation.
var executors = map[StepAction]StepExecutor{
    ActionProvision:          &provisionExecutor{},
    ActionWait:               &waitExecutor{},
    ActionVerifyProvisioning: &verifyProvisioningExecutor{},
    ActionVerifyConfigDB:     &verifyConfigDBExecutor{},
    ActionVerifyStateDB:      &verifyStateDBExecutor{},
    ActionVerifyBGP:          &verifyBGPExecutor{},
    ActionVerifyHealth:       &verifyHealthExecutor{},
    ActionVerifyRoute:        &verifyRouteExecutor{},
    ActionVerifyPing:         &verifyPingExecutor{},
    ActionApplyService:       &applyServiceExecutor{},
    ActionRemoveService:      &removeServiceExecutor{},
    ActionApplyBaseline:      &applyBaselineExecutor{},
    ActionSSHCommand:         &sshCommandExecutor{},
}

// executeStep dispatches a step to its executor and wraps the result
// with step index formatting for console output.
func (r *Runner) executeStep(
    ctx context.Context,
    step *Step,
    index, total int,
    opts RunOptions,
) *StepOutput {
    executor, ok := executors[step.Action]
    if !ok {
        return &StepOutput{
            Result: &StepResult{
                Name:    step.Name,
                Action:  step.Action,
                Status:  StatusError,
                Message: fmt.Sprintf("unknown action: %s", step.Action),
            },
        }
    }
    return executor.Execute(ctx, r, step)
}
```

### 4.5 Device Connection — newtron Integration

```go
// connectDevices builds the Network OO hierarchy from the topology spec dir
// and connects all devices via SSH. Locking is not done here — newtron
// operations acquire/release the device lock per-operation (lock → apply →
// verify → unlock). See newtron LLD §5 Execution Model.
//
// Uses the same connection path as `newtron provision` — reuses
// pkg/network/, pkg/device/, and pkg/spec/.
func (r *Runner) connectDevices(ctx context.Context, specDir string) error {
    // 1. Build Network from spec dir (loads all specs, creates device hierarchy)
    net, err := network.NewNetwork(specDir)
    if err != nil {
        return fmt.Errorf("loading specs: %w", err)
    }
    r.Network = net

    // 2. Connect each device in topology (no locking — operations lock internally)
    //    Profile has ssh_port, mgmt_ip from vmlab patching (see vmlab LLD §10)
    for _, name := range net.DeviceNames() {
        dev, err := net.GetDevice(name)
        if err != nil {
            return fmt.Errorf("getting device %s: %w", name, err)
        }
        if err := dev.Connect(ctx); err != nil {
            return fmt.Errorf("connecting to %s: %w", name, err)
        }
    }

    return nil
}
```

**Connection flow** (see device LLD §5.1):

1. `device.NewDevice(name, profile)` — creates Device from resolved profile
2. `dev.Connect(ctx)` — opens SSH tunnel using `profile.SSHPort` (vmlab-allocated),
   connects to CONFIG_DB (DB 4), STATE_DB (DB 6), APP_DB (DB 0), ASIC_DB (DB 1)
3. All Redis clients share the same SSH tunnel
4. STATE_DB, APP_DB, ASIC_DB failure is non-fatal (verification-only clients)

---

## 5. Step Executors (`pkg/newtest/steps.go`)

> **Convention:** Throughout executor code, `dev` refers to `*network.Device` obtained via `r.Network.GetDevice(name)`. To access device-layer methods (ConfigDBClient, GetRoute, VerifyChangeSet), use `dev.Underlying()` which returns `*device.Device`. Methods on `network.Device` that delegate to the device layer (e.g., `RunHealthChecks`, `GetRoute`, `GetRouteASIC`, `VerifyChangeSet`) are called directly on `dev`.

### 5.1 Executor Interface

```go
// StepExecutor executes a single step and returns output.
// Each executor is stateless — all mutable state lives in the Runner.
// Executors read from Runner (e.g. Devices, ChangeSets) but never write
// to it directly; they return StepOutput and the Runner merges state.
type StepExecutor interface {
    Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
}

// --- Runner helpers available to executors ---

// allDeviceNames returns sorted names of all topology devices from r.Network.
// Used by DeviceSelector.Resolve() when step.Devices.All is true.
func (r *Runner) allDeviceNames() []string {
    return r.Network.DeviceNames()
}

// resolveDevices resolves step.Devices to concrete device names.
func (r *Runner) resolveDevices(step *Step) []string {
    return step.Devices.Resolve(r.allDeviceNames())
}

// hasDataplane checks if the scenario platform supports dataplane forwarding.
// Used by verifyPingExecutor to skip on platforms without dataplane (e.g., sonic-vs).
func (r *Runner) hasDataplane() bool {
    platformName := r.scenario.Platform
    if r.opts.Platform != "" {
        platformName = r.opts.Platform
    }
    p, err := r.Network.GetPlatform(platformName)
    if err != nil {
        return false
    }
    return p.Dataplane != ""
}

// StepOutput is the return value from every executor.
// Result is always set. ChangeSets is non-nil only for mutating actions
// (provision, apply-service, remove-service, apply-baseline) — the Runner
// accumulates these for later use by verify-provisioning.
type StepOutput struct {
    Result     *StepResult
    ChangeSets map[string]*network.ChangeSet // per-device, nil for read-only steps
}
```

All executors follow the same pattern:

1. Resolve target devices from `step.Devices`
2. Call the appropriate newtron method on each device
3. Interpret results against `step.Expect`
4. Return `*StepOutput` with result status and optional ChangeSets

For multi-device steps, per-device results are collected in `StepResult.Details`.
The step status is `StatusPassed` only if all devices pass.

### 5.2 provisionExecutor

```go
type provisionExecutor struct{}

func (e *provisionExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
```

**Wraps:** `network.TopologyProvisioner.ProvisionDevice()` (see newtron LLD §5.4)

**Flow:**

1. Resolve devices from `step.Devices`
2. Create `TopologyProvisioner` from `r.Network`: `network.NewTopologyProvisioner(r.Network)`
3. For each device (respecting `r.opts.Parallel`):
   - Call `provisioner.ProvisionDevice(ctx, deviceName)`
   - Extract `ChangeSet` from returned `CompositeDeliveryResult.ChangeSet`
   - Store into `StepOutput.ChangeSets[deviceName]`
4. Return per-device results with ChangeSets

**Parallel provisioning:** When `r.opts.Parallel > 1`, devices are provisioned
concurrently using a semaphore (buffered channel) to limit concurrency.
Errors are collected via a `sync.Mutex`-guarded slice; on completion, all
per-device errors are joined into a single `StepResult` with `StatusFailed`
if any device failed. Successful devices still have their ChangeSets stored
in `StepOutput.ChangeSets` — partial success is visible to subsequent
verify-provisioning steps.

### 5.3 waitExecutor

```go
type waitExecutor struct{}

func (e *waitExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
```

**Wraps:** `time.Sleep(step.Duration)`

No newtron integration. Returns `StatusPassed` after the duration elapses.

### 5.4 verifyProvisioningExecutor

```go
type verifyProvisioningExecutor struct{}

func (e *verifyProvisioningExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
```

**Wraps:** `Device.VerifyChangeSet(cs)` (see device LLD §5.7, newtron LLD §5.2)

**Flow:**

1. Resolve devices from `step.Devices`
2. For each device:
   - Read `r.ChangeSets[deviceName]` — error if no ChangeSet accumulated
   - Call `dev.VerifyChangeSet(ctx, cs)`
   - Returns `VerificationResult` (see newtron LLD §3.6A):
     - `result.Failed == 0` → `StatusPassed`
     - `result.Failed > 0` → `StatusFailed` with error details
3. Message format: `"24/24 CONFIG_DB entries verified"` or
   `"22/24 CONFIG_DB entries verified (2 failed)"`

### 5.5 verifyConfigDBExecutor

```go
type verifyConfigDBExecutor struct{}

func (e *verifyConfigDBExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
```

**Wraps:** `device.ConfigDBClient.Get*()` — direct Redis read (see newtron LLD §3.5)

**Three assertion modes** (see newtest HLD §5.2):

**Mode 1: `expect.MinEntries`** — count keys matching table

```go
keys := dev.Underlying().ConfigDB.GetTableKeys(step.Table)
if len(keys) >= *step.Expect.MinEntries {
    // StatusPassed
} else {
    // StatusFailed: "BGP_NEIGHBOR: 1 entries (expected ≥ 2)"
}
```

**Mode 2: `expect.Exists`** — check if table/key exists

```go
entry := dev.Underlying().ConfigDB.GetEntry(step.Table, step.Key)
exists := entry != nil
if exists == *step.Expect.Exists {
    // StatusPassed
} else {
    // StatusFailed: "LOOPBACK_INTERFACE|Loopback0|10.0.0.11/32: expected exists=true, got false"
}
```

**Mode 3: `expect.Fields`** — read table/key, compare each field value

```go
entry := dev.Underlying().ConfigDB.GetEntry(step.Table, step.Key)
for field, expected := range step.Expect.Fields {
    actual := entry[field]
    if actual != expected {
        // StatusFailed: "DEVICE_METADATA|localhost: field hostname: expected leaf1, got leaf2"
    }
}
```

### 5.6 verifyStateDBExecutor

```go
type verifyStateDBExecutor struct{}

func (e *verifyStateDBExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
```

**Wraps:** `device.StateDBClient.GetEntry()` — generic STATE_DB read with polling

**Flow:**

1. Resolve devices via `r.resolveDevices(step)`
2. For each device, poll until timeout:
   - Call `dev.Underlying().StateDBClient().GetEntry(step.Table, step.Key)`
     (returns `map[string]string` — the generic accessor added to
     StateDBClient in device LLD §2.3)
   - Compare each field in `step.Expect.Fields` against the returned map
   - If all fields match → `StatusPassed`
   - Sleep `step.Expect.PollInterval` (default 5s) and retry
3. On timeout → `StatusFailed`

### 5.7 verifyBGPExecutor

```go
type verifyBGPExecutor struct{}

func (e *verifyBGPExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
```

**Wraps:** `Device.RunHealthChecks("bgp")` (see newtron LLD §5.2)

**Flow:**

1. Resolve devices
2. For each device, poll until `step.Expect.Timeout` (default 120s):
   - **Health check first (fast fail):** Call `dev.RunHealthChecks(ctx, "bgp")`.
     Returns `[]HealthCheckResult` (see newtron LLD §5.2).
     Check if all BGP check results have `Status == "pass"`.
   - **State check second (if configured):** When `step.Expect.State` is set
     (e.g. `"Established"`), additionally reads STATE_DB per-neighbor state via
     `dev.Underlying().StateDBClient().GetBGPNeighborState(vrf, neighborIP)` for each
     BGP_NEIGHBOR key in CONFIG_DB. Compares the `State` field (e.g.,
     `"Established"`, `"Active"`) against `step.Expect.State`. VRF is extracted
     from the BGP_NEIGHBOR key prefix (e.g., `"default|10.0.0.2"` → vrf=`"default"`).
   - **Both must pass.** Health check runs first because it's faster and catches
     gross failures (daemon not running, no neighbors). State check runs second
     for per-neighbor granularity.
   - If all pass → `StatusPassed`
   - Sleep `step.Expect.PollInterval` (default 5s) and retry
3. On timeout → `StatusFailed`

### 5.8 verifyHealthExecutor

```go
type verifyHealthExecutor struct{}

func (e *verifyHealthExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
```

**Wraps:** `Device.RunHealthChecks()` — no filter, runs all checks (see newtron LLD §5.2)

**Flow:**

1. Resolve devices
2. For each device:
   - Call `dev.RunHealthChecks(ctx)` — runs all 5 check types
     (interfaces, BGP, EVPN, LAG, VXLAN)
   - Returns `[]HealthCheckResult`
   - When `step.Expect.Overall == "ok"`: all checks must have `Status == "pass"`
   - Count pass/fail, report `"overall ok (5 checks passed)"`
3. Any check with `Status == "fail"` → `StatusFailed`

**Single-shot semantics:** `verify-health` performs a single read — it does not poll with timeout. Health checks read STATE_DB and CONFIG_DB which reflect the current device state. If convergence time is needed (e.g., waiting for BGP sessions to establish), use a `wait` step before `verify-health`. This is intentional: `verify-bgp` and `verify-state-db` poll because they wait for asynchronous state changes, but `verify-health` is a point-in-time snapshot.

### 5.9 verifyRouteExecutor

```go
type verifyRouteExecutor struct{}

func (e *verifyRouteExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
```

**Wraps:** `Device.GetRoute()` or `Device.GetRouteASIC()` (see device LLD §5.7)

Single-device action: parser validates `len(step.Devices) == 1`.

**Polling flow:**

```go
func (e *verifyRouteExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
    deviceName := step.Devices.Resolve(r.allDeviceNames())[0] // validated single by parser
    dev, err := r.Network.GetDevice(deviceName)
    if err != nil {
        return &StepOutput{Result: &StepResult{
            Status: StatusError, Device: deviceName, Message: err.Error(),
        }}
    }
    timeout := step.Expect.Timeout       // default 60s
    interval := step.Expect.PollInterval // default 5s
    deadline := time.Now().Add(timeout)
    polls := 0

    for time.Now().Before(deadline) {
        polls++
        var entry *device.RouteEntry
        var err error

        if step.Expect.Source == "asic_db" {
            entry, err = dev.GetRouteASIC(ctx, step.VRF, step.Prefix)
        } else {
            entry, err = dev.GetRoute(ctx, step.VRF, step.Prefix)
        }

        if err != nil {
            // Infrastructure error — don't retry
            return &StepOutput{Result: &StepResult{
                Status: StatusError, Device: deviceName, Message: err.Error(),
            }}
        }

        if entry != nil && matchRoute(entry, step.Expect) {
            source := "APP_DB"
            if step.Expect.Source == "asic_db" {
                source = "ASIC_DB"
            }
            return &StepOutput{Result: &StepResult{
                Status:  StatusPassed,
                Device:  deviceName,
                Message: fmt.Sprintf("%s via %s (%s, %s, polled %d times)",
                    step.Prefix, step.Expect.NextHopIP, step.Expect.Protocol, source, polls),
            }}
        }

        time.Sleep(interval)
    }

    return &StepOutput{Result: &StepResult{
        Status:  StatusFailed,
        Device:  deviceName,
        Message: fmt.Sprintf("%s not found after %s (%d polls)", step.Prefix, timeout, polls),
    }}
}
```

**matchRoute** checks RouteEntry against ExpectBlock:

```go
// matchRoute returns true if the RouteEntry matches all non-empty expect fields.
func matchRoute(entry *device.RouteEntry, expect *ExpectBlock) bool {
    if expect.Protocol != "" && entry.Protocol != expect.Protocol {
        return false
    }
    if expect.NextHopIP != "" {
        found := false
        for _, nh := range entry.NextHops {
            if nh.IP == expect.NextHopIP {
                found = true
                break
            }
        }
        if !found {
            return false
        }
    }
    return true
}
```

**RouteEntry fields** (from newtron LLD §3.6A):

| Field | Type | Description |
|-------|------|-------------|
| `Prefix` | string | `"10.1.0.0/31"` |
| `VRF` | string | `"default"`, `"Vrf-customer"` |
| `Protocol` | string | `"bgp"`, `"connected"`, `"static"` |
| `NextHops` | []NextHop | IP + interface pairs |
| `Source` | RouteSource | `"APP_DB"` or `"ASIC_DB"` |

### 5.10 verifyPingExecutor

```go
type verifyPingExecutor struct{}

func (e *verifyPingExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
```

**newtest native** — not backed by a newtron method.

Single-device action: parser validates `len(step.Devices) == 1`.

**Flow:**

1. Check platform capability: `r.hasDataplane()`
   - If `false` → return `StatusSkipped` with message
     `"platform sonic-vs has dataplane: false"`
2. Resolve source device: `r.resolveDevices(step)[0]`
3. Resolve target IP from `step.Target`:
   - If `step.Target` is a valid IP, use it directly
   - Otherwise, treat it as a device name and resolve to its loopback IP:
     ```go
     targetDev, err := r.Network.GetDevice(step.Target)
     if err != nil {
         return &StepOutput{Result: &StepResult{
             Status: StatusError, Message: fmt.Sprintf("target device %q: %s", step.Target, err),
         }}
     }
     targetIP := targetDev.Underlying().Profile.LoopbackIP
     ```
4. SSH to source device using the separate command SSH session (not the Redis
   tunnel). Opens a new `ssh.Session` on the device's existing tunnel SSH
   client (`dev.Underlying().SSHClient()`) and runs:
   ```go
   session, err := dev.Underlying().SSHClient().NewSession()
   if err != nil {
       return &StepOutput{Result: &StepResult{
           Status: StatusError, Device: deviceName,
           Message: fmt.Sprintf("SSH session: %s", err),
       }}
   }
   defer session.Close()

   output, err := session.CombinedOutput(fmt.Sprintf("ping -c %d -W 5 %s", step.Count, targetIP))
   if err != nil {
       // ping returns exit code 1 on partial loss — check output, not just error
       if len(output) == 0 {
           return &StepOutput{Result: &StepResult{
               Status: StatusError, Device: deviceName,
               Message: fmt.Sprintf("ping command failed: %s", err),
           }}
       }
   }
   ```
5. Parse ping output: match `(\d+)% packet loss` from stdout and compute
   success rate as `1.0 - (loss / 100.0)`
6. Compare against `step.Expect.SuccessRate` (default 1.0)
   - If actual ≥ expected → `StatusPassed`
   - Otherwise → `StatusFailed`

### 5.11 applyServiceExecutor

```go
type applyServiceExecutor struct{}

func (e *applyServiceExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
```

**Wraps:** `Interface.ApplyService()` (see newtron LLD §5.1)

**Flow:**

1. Resolve devices
2. For each device:
   - Get interface from `r.Network.GetDevice(name).GetInterface(step.Interface)`
   - Extract `ipAddr` from `step.Params["ip"]` if present
   - Call `iface.ApplyService(ctx, step.Service, ipAddr, false)` (execute mode):
     ```go
     cs, err := iface.ApplyService(ctx, step.Service, ipAddr, false)
     if err != nil {
         return &StepOutput{Result: &StepResult{
             Status: StatusError, Device: name,
             Message: fmt.Sprintf("apply-service %s: %s", step.Service, err),
         }}
     }
     ```
   - Collect returned ChangeSet into `StepOutput.ChangeSets[deviceName]`
3. Return per-device results with ChangeSets

### 5.12 removeServiceExecutor

```go
type removeServiceExecutor struct{}

func (e *removeServiceExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
```

**Wraps:** `Interface.RemoveService()` (see newtron LLD §5.1)

**Flow:**

1. Resolve devices
2. For each device:
   - Get interface from `r.Network.GetDevice(name).GetInterface(step.Interface)`
   - Call `iface.RemoveService(ctx, false)` (execute mode):
     ```go
     cs, err := iface.RemoveService(ctx, false)
     if err != nil {
         return &StepOutput{Result: &StepResult{
             Status: StatusError, Device: name,
             Message: fmt.Sprintf("remove-service: %s", err),
         }}
     }
     ```
   - Collect returned ChangeSet into `StepOutput.ChangeSets[deviceName]`
3. Return per-device results with ChangeSets

### 5.13 applyBaselineExecutor

```go
type applyBaselineExecutor struct{}

func (e *applyBaselineExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
```

**Wraps:** `Device.ApplyBaseline()` (see newtron LLD §5.2)

**Flow:**

1. Resolve devices
2. For each device:
   - Convert `step.Vars` (map) to `[]string` (`"key=value"` format)
   - Call `dev.ApplyBaseline(ctx, step.Configlet, vars)`:
     ```go
     cs, err := dev.ApplyBaseline(ctx, step.Configlet, vars, false)
     if err != nil {
         return &StepOutput{Result: &StepResult{
             Status: StatusError, Device: name,
             Message: fmt.Sprintf("apply-baseline %s: %s", step.Configlet, err),
         }}
     }
     ```
   - Collect returned ChangeSet into `StepOutput.ChangeSets[deviceName]`
3. Return per-device results with ChangeSets

### 5.14 sshCommandExecutor

```go
type sshCommandExecutor struct{}

func (e *sshCommandExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
```

**newtest native** — uses SSH exec directly.

**Flow:**

1. Resolve devices
2. For each device:
   - Open SSH session using device profile credentials (SSHUser, SSHPass, SSHPort)
   - Run `step.Command` via `session.CombinedOutput()`
   - If `step.Expect.Contains` is set: check if output contains substring
     - Contains → `StatusPassed`
     - Not found → `StatusFailed` with output snippet
   - If no expect: pass if exit code is 0

---

## 6. vmlab Integration (`pkg/newtest/deploy.go`)

### 6.1 DeployTopology

```go
// DeployTopology deploys a VM topology using vmlab.
// Creates a vmlab.Lab from the spec directory, calls Lab.Deploy(),
// and returns the Lab for later destroy.
//
// See vmlab LLD §4.1 (Lab type), §12 (deploy flow).
func DeployTopology(specDir string) (*vmlab.Lab, error) {
    lab, err := vmlab.NewLab(specDir)
    if err != nil {
        return nil, fmt.Errorf("newtest: load topology: %w", err)
    }
    if err := lab.Deploy(); err != nil {
        return nil, fmt.Errorf("newtest: deploy topology: %w", err)
    }
    return lab, nil
}
```

After `Deploy()` returns:

- All VMs are running and SSH-reachable
- Device profiles have been patched with `ssh_port`, `console_port`, `mgmt_ip`
  (see vmlab LLD §10)
- newtest can now connect to devices using the patched profiles

### 6.2 DestroyTopology

```go
// DestroyTopology tears down a deployed topology.
// Kills QEMU processes, removes overlay disks, restores profiles.
//
// See vmlab LLD §4.1 (Lab type), §13 (destroy flow).
func DestroyTopology(lab *vmlab.Lab) error {
    if lab == nil {
        return nil
    }
    if err := lab.Destroy(); err != nil {
        return fmt.Errorf("newtest: destroy topology: %w", err)
    }
    return nil
}
```

### 6.3 Platform Capability Check

`hasDataplane()` is defined in §5.1. It takes no arguments and reads the scenario's platform from `r.scenario.Platform` (overridden by `r.opts.Platform` if set). Returns true when `PlatformSpec.Dataplane != ""`.

**Platform dataplane values** (from `platforms.json`):

| Platform | Dataplane | verify-ping |
|----------|-----------|-------------|
| `sonic-vpp` | `"vpp"` | Executes |
| `sonic-vs` | `""` | Skipped |

---

## 7. Results and Reporting (`pkg/newtest/report.go`)

### 7.1 ScenarioResult

```go
// ScenarioResult holds the result of a single scenario execution.
type ScenarioResult struct {
    Name        string       // scenario name
    Topology    string       // topology name
    Platform    string       // platform name
    Status      Status       // overall: PASS if all passed, FAIL if any failed
    Duration    time.Duration
    Steps       []StepResult
    DeployError error        // non-nil if vmlab deploy failed
}
```

**Overall status computation:**

```go
func computeOverallStatus(steps []StepResult) Status {
    hasError := false
    for _, s := range steps {
        if s.Status == StatusError {
            hasError = true
        }
        if s.Status == StatusFailed {
            return StatusFailed
        }
    }
    if hasError {
        return StatusError // infrastructure errors propagate, not downgraded to FAIL
    }
    return StatusPassed
}
```

Skipped steps do not affect overall status — a scenario with all steps
passed or skipped is `PASS`. `ERROR` takes priority over `PASS` but not
over `FAIL`, since a test failure is a definitive signal while an
infrastructure error means "unknown".

### 7.2 StepResult

```go
// StepResult holds the result of a single step execution.
type StepResult struct {
    Name     string         // from Step.Name
    Action   StepAction     // from Step.Action
    Status   Status
    Duration time.Duration
    Message  string         // human-readable detail
    Device   string         // which device (single-device steps)
    Details  []DeviceResult // per-device results (multi-device steps)
}

// DeviceResult holds the result for a single device within a multi-device step.
type DeviceResult struct {
    Device  string
    Status  Status
    Message string
}
```

### 7.3 Status

```go
type Status string

const (
    StatusPassed  Status = "PASS"
    StatusFailed  Status = "FAIL"
    StatusSkipped Status = "SKIP"
    StatusError   Status = "ERROR" // infrastructure error (not test failure)
)
```

| Status | Meaning | Causes |
|--------|---------|--------|
| `PASS` | Step/scenario succeeded | Assertions matched |
| `FAIL` | Assertion failed | Expected value mismatch, timeout |
| `SKIP` | Skipped | Platform lacks dataplane |
| `ERROR` | Infrastructure error | SSH connection failure, Redis timeout |

`FAIL` vs `ERROR`: A `FAIL` means the test ran but the assertion didn't hold.
An `ERROR` means the test could not run due to infrastructure problems.
At the scenario level, `FAIL` takes priority over `ERROR` (see §7.1).

### 7.4 ReportGenerator

```go
// ReportGenerator produces test reports from scenario results.
type ReportGenerator struct {
    Results []*ScenarioResult
}

// WriteMarkdown writes a markdown report to the given path.
// Format matches newtest HLD §9.
func (g *ReportGenerator) WriteMarkdown(path string) error

// WriteJUnit writes a JUnit XML report for CI integration.
func (g *ReportGenerator) WriteJUnit(path string) error

// PrintConsole writes human-readable output to w.
// Format matches newtest HLD §8.
func (g *ReportGenerator) PrintConsole(w io.Writer)
```

### 7.5 Console Output Format

Matches newtest HLD §8:

```
newtest: bgp-underlay (4node topology, sonic-vpp)

Deploying topology...
  ✓ spine1 (SSH :40000)
  ✓ spine2 (SSH :40001)
  ✓ leaf1 (SSH :40002)
  ✓ leaf2 (SSH :40003)

Running steps...
  [1/5] provision-all
    ✓ spine1 provisioned (3.2s)
    ✓ spine2 provisioned (2.9s)
    ✓ leaf1 provisioned (3.1s)
    ✓ leaf2 provisioned (3.0s)

  [2/5] wait-convergence
    ✓ 30s elapsed

  [3/5] verify-provisioning
    ✓ spine1: 24/24 CONFIG_DB entries verified
    ✓ spine2: 24/24 CONFIG_DB entries verified
    ✓ leaf1: 31/31 CONFIG_DB entries verified
    ✓ leaf2: 31/31 CONFIG_DB entries verified

  [4/5] verify-underlay-route
    ✓ spine1: 10.1.0.0/31 via 10.1.0.1 (bgp, APP_DB, polled 3 times)

  [5/5] verify-health
    ✓ spine1: overall ok (5 checks passed)
    ✓ spine2: overall ok (5 checks passed)
    ✓ leaf1: overall ok (5 checks passed)
    ✓ leaf2: overall ok (5 checks passed)

Destroying topology...
  ✓ All VMs stopped

PASS: bgp-underlay (5/5 steps passed, 68s)
```

**Status symbols:**

| Symbol | Status |
|--------|--------|
| `✓` | PASS |
| `✗` | FAIL |
| `⊘` | SKIP |
| `!` | ERROR |

### 7.6 Markdown Report Format

Written to `newtest/.generated/report.md`. Format matches newtest HLD §9:

```markdown
# newtest Report — 2026-02-05 10:30:00

| Scenario | Topology | Platform | Result | Duration |
|----------|----------|----------|--------|----------|
| bgp-underlay | 4node | sonic-vpp | PASS | 68s |
| full-fabric | 4node | sonic-vpp | FAIL | 140s |

## Failures

### full-fabric
Step 7 (verify-ping): leaf1 → leaf2 ping failed
  Expected: 5/5 packets received
  Got: 0/5 packets received

## Skipped

### full-fabric (when run with --platform sonic-vs)
Step 7 (verify-ping): SKIP — platform sonic-vs has dataplane: false
```

### 7.7 JUnit XML Format

For CI systems that parse JUnit XML:

```go
// junitTestSuites represents the top-level JUnit XML structure.
type junitTestSuites struct {
    XMLName  xml.Name         `xml:"testsuites"`
    Suites   []junitTestSuite `xml:"testsuite"`
}

// junitTestSuite maps to a single scenario.
type junitTestSuite struct {
    Name     string          `xml:"name,attr"`
    Tests    int             `xml:"tests,attr"`
    Failures int             `xml:"failures,attr"`
    Errors   int             `xml:"errors,attr"`
    Skipped  int             `xml:"skipped,attr"`
    Time     float64         `xml:"time,attr"`
    Cases    []junitTestCase `xml:"testcase"`
}

// junitTestCase maps to a single step.
type junitTestCase struct {
    Name      string        `xml:"name,attr"`
    ClassName string        `xml:"classname,attr"` // scenario name
    Time      float64       `xml:"time,attr"`
    Failure   *junitFailure `xml:"failure,omitempty"`
    Skipped   *junitSkipped `xml:"skipped,omitempty"`
    Error     *junitError   `xml:"error,omitempty"`
}
```

Each `ScenarioResult` maps to a `junitTestSuite`. Each `StepResult` maps to
a `junitTestCase`. The `classname` attribute is set to the scenario name for
grouping in CI dashboards.

---

## 8. CLI Implementation (`cmd/newtest/`)

### 8.1 Command Tree

Same Cobra pattern as `cmd/newtron/` and `cmd/vmlab/`:

```go
// main.go
func main() {
    rootCmd := &cobra.Command{
        Use:   "newtest",
        Short: "E2E testing for newtron",
    }

    rootCmd.AddCommand(
        newRunCmd(),
        newListCmd(),
        newTopologiesCmd(),
    )

    if err := rootCmd.Execute(); err != nil {
        os.Exit(1)
    }
}
```

### 8.2 run Command

```go
// cmd_run.go
func newRunCmd() *cobra.Command {
    var opts RunOptions

    cmd := &cobra.Command{
        Use:   "run",
        Short: "Run test scenarios",
        RunE: func(cmd *cobra.Command, args []string) error {
            runner := NewRunner("newtest/scenarios", "newtest/topologies")
            results, err := runner.Run(opts)
            if err != nil {
                return err
            }

            // Print console output
            gen := &ReportGenerator{Results: results}
            gen.PrintConsole(os.Stdout)

            // Write markdown report
            gen.WriteMarkdown("newtest/.generated/report.md")

            // Write JUnit if requested
            if opts.JUnitPath != "" {
                gen.WriteJUnit(opts.JUnitPath)
            }

            // Exit code based on results (infra errors take priority)
            hasFailure, hasInfraError := false, false
            for _, r := range results {
                if r.DeployError != nil || r.Status == StatusError {
                    hasInfraError = true
                }
                if r.Status == StatusFailed {
                    hasFailure = true
                }
            }
            if hasInfraError {
                os.Exit(2)
            }
            if hasFailure {
                os.Exit(1)
            }
            return nil
        },
    }

    cmd.Flags().StringVar(&opts.Scenario, "scenario", "", "run specific scenario")
    cmd.Flags().BoolVar(&opts.All, "all", false, "run all scenarios")
    cmd.Flags().StringVar(&opts.Topology, "topology", "", "override topology")
    cmd.Flags().StringVar(&opts.Platform, "platform", "", "override platform")
    cmd.Flags().BoolVar(&opts.Keep, "keep", false, "don't destroy topology after tests")
    cmd.Flags().BoolVar(&opts.NoDeploy, "no-deploy", false, "skip deploy/destroy")
    cmd.Flags().IntVar(&opts.Parallel, "parallel", 1, "parallel provisioning count")
    cmd.Flags().BoolVarP(&opts.Verbose, "verbose", "v", false, "verbose output")
    cmd.Flags().StringVar(&opts.JUnitPath, "junit", "", "JUnit XML output path")

    return cmd
}
```

**Flag to RunOptions mapping:**

| Flag | RunOptions Field | Default |
|------|-----------------|---------|
| `--scenario <name>` | `Scenario` | `""` |
| `--all` | `All` | `false` |
| `--topology <name>` | `Topology` | `""` (from scenario) |
| `--platform <name>` | `Platform` | `""` (from scenario) |
| `--keep` | `Keep` | `false` |
| `--no-deploy` | `NoDeploy` | `false` |
| `--parallel <n>` | `Parallel` | `1` |
| `--junit <path>` | `JUnitPath` | `""` |
| `-v, --verbose` | `Verbose` | `false` |

### 8.3 list Command

```go
// cmd_list.go
func newListCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "list",
        Short: "List available scenarios",
        RunE: func(cmd *cobra.Command, args []string) error {
            scenarios, err := ParseAllScenarios("newtest/scenarios")
            if err != nil {
                return err
            }
            fmt.Println("Available scenarios:")
            for _, s := range scenarios {
                fmt.Printf("  %-16s %s (%s, %s)\n",
                    s.Name, s.Description, s.Topology, s.Platform)
            }
            return nil
        },
    }
}
```

**Output format:**

```
Available scenarios:
  bgp-underlay     Verify eBGP underlay sessions establish (4node, sonic-vpp)
  bgp-overlay      Verify iBGP overlay sessions (4node, sonic-vpp)
  service-l3       L3 service apply/remove (2node, sonic-vpp)
  health           Health checks after provisioning (2node, sonic-vpp)
```

### 8.4 topologies Command

```go
// cmd_topologies.go
func newTopologiesCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "topologies",
        Short: "List available topologies",
        RunE: func(cmd *cobra.Command, args []string) error {
            // Read subdirectories of newtest/topologies/
            entries, err := os.ReadDir("newtest/topologies")
            if err != nil {
                return err
            }
            fmt.Println("Available topologies:")
            for _, e := range entries {
                if e.IsDir() {
                    fmt.Printf("  %s\n", e.Name())
                }
            }
            return nil
        },
    }
}
```

---

## 9. Error Handling

### 9.1 Error Types

```go
// InfraError indicates an infrastructure failure (VM, SSH, Redis)
// that prevented a test from running.
type InfraError struct {
    Op      string // "deploy", "connect", "ssh"
    Device  string // device name (or "" for topology-level)
    Err     error
}

func (e *InfraError) Error() string {
    if e.Device != "" {
        return fmt.Sprintf("newtest: %s %s: %s", e.Op, e.Device, e.Err)
    }
    return fmt.Sprintf("newtest: %s: %s", e.Op, e.Err)
}

func (e *InfraError) Unwrap() error { return e.Err }

// StepError indicates a step execution failure (not an assertion failure).
type StepError struct {
    Step   string
    Action StepAction
    Err    error
}

func (e *StepError) Error() string {
    return fmt.Sprintf("newtest: step %s (%s): %s", e.Step, e.Action, e.Err)
}

func (e *StepError) Unwrap() error { return e.Err }
```

All errors use `fmt.Errorf` with `%w` wrapping for context, following the
same pattern as vmlab (see vmlab LLD §15.1):

```go
return fmt.Errorf("newtest: deploy topology: %w", err)
return fmt.Errorf("newtest: connect to %s: %w", deviceName, err)
return fmt.Errorf("newtest: step %s: no ChangeSet for device %s", step.Name, deviceName)
```

### 9.2 Exit Codes

| Exit Code | Meaning | Trigger |
|-----------|---------|---------|
| 0 | All scenarios passed | All steps PASS or SKIP |
| 1 | One or more scenarios failed | Any step FAIL |
| 2 | Infrastructure error | VM boot failure, SSH connection failure, deploy error |

Exit codes are set in the `run` command (§8.2) based on `ScenarioResult.Status` and `ScenarioResult.DeployError`.

### 9.3 Scenario Execution Order

**Scenarios are strictly sequential.** When `--all` is used, scenarios run one at a time in `ParseAllScenarios` order (alphabetical by filename). Each scenario gets its own vmlab deploy/destroy cycle. There is no parallel scenario execution — this is intentional because:

1. vmlab port allocations (SSH, console, link ports) would conflict between concurrent topologies
2. Resource consumption (QEMU VMs) makes parallelism impractical on a single host
3. Test isolation is simpler to reason about with sequential execution

**Steps within a scenario** are also strictly sequential. The fail-fast model (§4.3) means a failing step stops the scenario immediately. Step-level parallelism only applies to `provision` (multi-device provisioning within a single step, controlled by `--parallel`).

---

## Cross-References

### References to newtron LLD

| newtron LLD Section | How newtest Uses It |
|----------------------|---------------------|
| §3.1 `spec.NewLoader()` | Loading topology specs in `Runner.connectDevices()` |
| §3.1A Spec type ownership | Shows which fields newtest reads (e.g., `PlatformSpec.Dataplane`) |
| §3.6 `ChangeSet` | Stored per-device in `Runner.ChangeSets` for `verify-provisioning` |
| §3.6A `VerificationResult`, `RouteEntry` | Step result formatting in verifyProvisioningExecutor and verifyRouteExecutor |
| §5.1 `Interface.ApplyService()`, `RemoveService()` | applyServiceExecutor, removeServiceExecutor |
| §5.2 `Device.VerifyChangeSet()` | verifyProvisioningExecutor |
| §5.2 `Device.GetRoute()` | verifyRouteExecutor (source: `app_db`) |
| §5.2 `Device.GetRouteASIC()` | verifyRouteExecutor (source: `asic_db`) |
| §5.2 `Device.RunHealthChecks()` | verifyHealthExecutor, verifyBGPExecutor |
| §5.2 `Device.ApplyBaseline()` | applyBaselineExecutor |
| §5.3 `HealthCheckResult` (within Operation Configuration Types) | Health check status interpretation |
| §5.4 `TopologyProvisioner.ProvisionDevice()` | provisionExecutor |

### References to Device Layer LLD

| Device LLD Section | How newtest Uses It |
|--------------------|---------------------|
| §5.1 `Device.Connect()` | `Runner.connectDevices()` — SSH tunnel, multi-DB connection |
| §5.7 Verification methods | `GetRoute()` / `GetRouteASIC()` called by verifyRouteExecutor |
| §3 APP_DB | Route reads for verify-route (source: `app_db`) |
| §4 ASIC_DB | Route reads for verify-route (source: `asic_db`) |

### References to vmlab LLD

| vmlab LLD Section | How newtest Uses It |
|--------------------|---------------------|
| §1.1 `PlatformSpec.Dataplane` | `Runner.hasDataplane()` for verify-ping skip logic |
| §1.2 `DeviceProfile.SSHPort` | Used by `Device.Connect()` after vmlab profile patching |
| §4.1 `vmlab.Lab` | `Runner.Lab` field, `DeployTopology()` return type |
| §4.1 `vmlab.NewLab(specDir)` | Called in `DeployTopology()` |
| §4.1 `vmlab.Lab.Deploy()` | Called in `DeployTopology()` |
| §4.1 `vmlab.Lab.Destroy()` | Called in `DestroyTopology()` |
| §10 Profile patching | newtest relies on vmlab having patched `ssh_port`/`mgmt_ip` into profiles before connecting devices |

### References to newtest HLD

| newtest HLD Section | LLD Section |
|---------------------|-------------|
| §5 Step actions | §2.3 StepAction constants, §5 Step executors |
| §6 Verification tiers | §5 executor-to-newtron method mapping |
| §8 Output format | §7.5 Console output format |
| §9 Report format | §7.6 Markdown report format |
| §10 CLI | §8 CLI implementation |
| §12 Implementation phases | All sections (phased delivery) |

---

## Appendix A: Changelog

#### v6

| Area | Change |
|------|--------|
| **Runner.Network** | Replaced `Devices map[string]*device.Device` + `Platforms` with `Network *network.Network` (§4.1) |
| **connectDevices()** | Rewritten: builds `network.Network` OO hierarchy, connects all devices. No session-level locking — newtron operations lock per-operation (§4.5) |
| **RouteEntry** | Fixed type to `*device.RouteEntry` (not `*network.RouteEntry`) — lives in `pkg/device/verify.go` |
| **VM Leak Prevention** | Added `defer DestroyTopology(r.Lab)` after successful deploy (§4.3) |
| **SIGINT Handling** | Added `signal.NotifyContext` for clean shutdown on Ctrl-C (§4.3) |
| **verify-health** | Documented single-shot semantics — no polling, use `wait` for convergence (§5.8) |
| **ChangeSet Accumulation** | Documented last-write-wins explicitly (§4.1) |
| **Parallel Scenarios** | Documented strictly sequential execution with rationale (§9.3) |
| **Executor Updates** | applyServiceExecutor and removeServiceExecutor use `r.Network.GetDevice().GetInterface()` (§5.11, §5.12) |
