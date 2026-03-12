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
│       ├── scenario.go           # Scenario, Step, StepAction constants, ExpectBlock
│       ├── parser.go             # ParseScenario, stepValidations table, ValidateDependencyGraph
│       ├── runner.go             # Runner (with Client, ServerURL, NetworkID), RunOptions, Run
│       ├── steps.go              # stepExecutor interface, StepOutput, all executor implementations
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
    │   ├── 2node-ngdp/specs/          # 2-switch + 6-host topology
    │   ├── 2node-ngdp-service/specs/  # 2-switch + 8-host topology (service testing)
    │   ├── 3node-ngdp/specs/          # 3-switch + 6-host topology (EVPN dataplane)
    │   └── 4node-ngdp/specs/          # 4-node topology
    ├── suites/
    │   ├── 1node-vs-basic/           # Single-switch basics (4 scenarios)
    │   ├── 2node-ngdp-primitive/      # Disaggregated operation tests (20 scenarios)
    │   ├── 2node-ngdp-service/        # Service lifecycle tests (6 scenarios)
    │   ├── 3node-ngdp-dataplane/      # EVPN L2/L3 dataplane tests (8 scenarios)
    │   └── simple-vrf-host/      # Simple VRF with host verification
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
    Command   string         `yaml:"command,omitempty"`        // ssh-command, host-exec
    Target    string         `yaml:"target,omitempty"`         // verify-ping
    Count     int            `yaml:"count,omitempty"`          // verify-ping
    VLANID    int            `yaml:"vlan_id,omitempty"`        // create-vlan, delete-vlan, etc.
    Tagging   string         `yaml:"tagging,omitempty"`        // add-vlan-member ("tagged"/"untagged")
    Expect    *ExpectBlock   `yaml:"expect,omitempty"`         // verify-*, ssh-command, host-exec
}
```

Step is intentionally a flat union — all action-specific fields live on one
struct. Validation of which fields are required for which action happens at
parse time via the declarative `stepValidations` table (see §3.2).

**Step-Level Fields Are Canonical** — If the Step struct has a named field for
a concept (`interface`, `vrf`, `prefix`, `table`, `key`, `service`, `command`,
`target`, `vlan_id`), all actions MUST read from that field. `params:` contains
only action-specific arguments that have no step-level equivalent (`ipvpn`,
`macvpn`, `remote_asn`, `qos_policy`, `next_hop`, `name`, `member`, etc.).
This ensures parse-time validation via `stepFieldGetter` and a single canonical
location for each concept.

### 2.3 StepAction Constants

```go
type StepAction string

const (
    // Provisioning
    ActionProvision          StepAction = "provision"
    ActionConfigureLoopback  StepAction = "configure-loopback"
    ActionRemoveLoopback     StepAction = "remove-loopback"
    ActionApplyFRRDefaults   StepAction = "apply-frr-defaults"
    ActionConfigReload       StepAction = "config-reload"

    // Verification
    ActionVerifyProvisioning StepAction = "verify-provisioning"
    ActionVerifyConfigDB     StepAction = "verify-config-db"
    ActionVerifyStateDB      StepAction = "verify-state-db"
    ActionVerifyBGP          StepAction = "verify-bgp"
    ActionVerifyHealth       StepAction = "verify-health"
    ActionVerifyRoute        StepAction = "verify-route"
    ActionVerifyPing         StepAction = "verify-ping"

    // Service lifecycle
    ActionApplyService       StepAction = "apply-service"
    ActionRemoveService      StepAction = "remove-service"
    ActionRefreshService     StepAction = "refresh-service"

    // VLAN
    ActionCreateVLAN         StepAction = "create-vlan"
    ActionDeleteVLAN         StepAction = "delete-vlan"
    ActionAddVLANMember      StepAction = "add-vlan-member"
    ActionRemoveVLANMember   StepAction = "remove-vlan-member"
    ActionConfigureSVI       StepAction = "configure-svi"
    ActionRemoveSVI          StepAction = "remove-svi"

    // VRF
    ActionCreateVRF          StepAction = "create-vrf"
    ActionDeleteVRF          StepAction = "delete-vrf"
    ActionAddVRFInterface    StepAction = "add-vrf-interface"
    ActionRemoveVRFInterface StepAction = "remove-vrf-interface"

    // EVPN
    ActionSetupEVPN          StepAction = "setup-evpn"
    ActionTeardownEVPN       StepAction = "teardown-evpn"
    ActionBindIPVPN          StepAction = "bind-ipvpn"
    ActionUnbindIPVPN        StepAction = "unbind-ipvpn"
    ActionBindMACVPN         StepAction = "bind-macvpn"
    ActionUnbindMACVPN       StepAction = "unbind-macvpn"

    // BGP
    ActionConfigureBGP       StepAction = "configure-bgp"
    ActionRemoveBGPGlobals   StepAction = "remove-bgp-globals"
    ActionBGPAddNeighbor     StepAction = "bgp-add-neighbor"
    ActionBGPRemoveNeighbor  StepAction = "bgp-remove-neighbor"

    // ACL
    ActionCreateACLTable     StepAction = "create-acl-table"
    ActionDeleteACLTable     StepAction = "delete-acl-table"
    ActionAddACLRule         StepAction = "add-acl-rule"
    ActionDeleteACLRule      StepAction = "delete-acl-rule"
    ActionBindACL            StepAction = "bind-acl"
    ActionUnbindACL          StepAction = "unbind-acl"

    // QoS
    ActionApplyQoS           StepAction = "apply-qos"
    ActionRemoveQoS          StepAction = "remove-qos"

    // Interface
    ActionSetInterface       StepAction = "set-interface"
    ActionRemoveIP           StepAction = "remove-ip"

    // Routing
    ActionAddStaticRoute     StepAction = "add-static-route"
    ActionRemoveStaticRoute  StepAction = "remove-static-route"

    // PortChannel
    ActionCreatePortChannel       StepAction = "create-portchannel"
    ActionDeletePortChannel       StepAction = "delete-portchannel"
    ActionAddPortChannelMember    StepAction = "add-portchannel-member"
    ActionRemovePortChannelMember StepAction = "remove-portchannel-member"

    // Host
    ActionHostExec           StepAction = "host-exec"

    // Utility
    ActionWait               StepAction = "wait"
    ActionSSHCommand         StepAction = "ssh-command"
    ActionRestartService     StepAction = "restart-service"
    ActionCleanup            StepAction = "cleanup"
)
```

The `validActions` set is derived from the `executors` map at init time, ensuring
the two stay synchronized without manual maintenance.

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
| `verify-ping` | 30s† | — | Count: 5\*, SuccessRate: 1.0† |

\* **Unconditional default** — set on the step even without an `expect:` block
(Count is a step-level field, not an expect field).

† Requires an `expect:` block in YAML. If `expect:` is omitted, these defaults
are not applied. The executor itself still applies a fallback (e.g.,
`verifyPingExecutor` uses 100% success rate when `expect.SuccessRate` is nil).

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
3. `applyDefaults` — two-phase default injection:
   - **Phase 1 (unconditional):** `verify-ping` sets `Count = 5` even without an `expect:` block (Count is a step-level field, not inside ExpectBlock)
   - **Phase 2 (guarded):** if `step.Expect == nil`, remaining defaults are skipped; otherwise sets `Timeout`, `PollInterval`, `State`, `Source`, and `SuccessRate` per the §2.5 defaults table
4. Return `*Scenario`

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
    fields        []string // required step-level fields: "interface", "service", "table", etc.
    params        []string // required params map keys
    custom        func(prefix string, step *Step) error
}
```

The `stepFieldGetter` table maps field names (`"interface"`, `"service"`, `"table"`,
`"key"`, `"prefix"`, `"vrf"`, `"target"`, `"command"`, `"vlan_id"`) to accessor
functions on `*Step`, enabling generic validation of required step-level fields.

**Required fields per action** (actions not listed here — `configure-bgp`,
`create-portchannel`, `delete-portchannel`, `add-portchannel-member`,
`remove-portchannel-member` — have no validation rules in `stepValidations`):

| Action | Required Fields |
|--------|----------------|
| `provision` | `devices` |
| `wait` | `duration` (custom) |
| `verify-provisioning` | `devices` |
| `verify-config-db` | `devices`, `table`, `expect` (custom: one of `min_entries`, `exists`, `fields`) |
| `verify-state-db` | `devices`, `table`, `key`, `expect.fields` (custom) |
| `verify-bgp` | `devices` |
| `verify-health` | `devices` |
| `verify-route` | `devices` (single), `prefix`, `vrf` |
| `verify-ping` | `devices` (single), `target` |
| `apply-service` | `devices`, `interface`, `service` |
| `remove-service` | `devices`, `interface` |
| `configure-loopback` | `devices` |
| `remove-loopback` | `devices` |
| `ssh-command` | `devices`, `command` |
| `restart-service` | `devices`, `service` |
| `config-reload` | `devices` |
| `apply-frr-defaults` | `devices` |
| `cleanup` | `devices` |
| `set-interface` | `devices`, `interface`, `params.property` |
| `create-vlan` | `devices`, `vlan_id` |
| `delete-vlan` | `devices`, `vlan_id` |
| `add-vlan-member` | `devices`, `vlan_id`, `interface` |
| `remove-vlan-member` | `devices`, `vlan_id`, `interface` |
| `configure-svi` | `devices`, `vlan_id` |
| `remove-svi` | `devices`, `vlan_id` |
| `create-vrf` | `devices`, `vrf` |
| `delete-vrf` | `devices`, `vrf` |
| `setup-evpn` | `devices`, `params.source_ip` |
| `teardown-evpn` | `devices` |
| `add-vrf-interface` | `devices`, `vrf`, `interface` |
| `remove-vrf-interface` | `devices`, `vrf`, `interface` |
| `bind-ipvpn` | `devices`, `vrf`, `params.ipvpn` |
| `unbind-ipvpn` | `devices`, `vrf` |
| `bind-macvpn` | `devices`, `vlan_id`, `params.macvpn` |
| `unbind-macvpn` | `devices`, `vlan_id` |
| `add-static-route` | `devices`, `vrf`, `prefix`, `params.next_hop` |
| `remove-static-route` | `devices`, `vrf`, `prefix` |
| `apply-qos` | `devices`, `interface`, `params.qos_policy` |
| `remove-qos` | `devices`, `interface` |
| `bgp-add-neighbor` | `devices`, `params.remote_asn` |
| `bgp-remove-neighbor` | `devices`, `params.neighbor_ip` |
| `remove-bgp-globals` | `devices` |
| `refresh-service` | `devices`, `interface` |
| `host-exec` | `devices` (single), `command` |
| `create-acl-table` | `devices`, `params.name` |
| `add-acl-rule` | `devices`, `params.name`, `params.rule`, `params.action` |
| `delete-acl-rule` | `devices`, `params.name`, `params.rule` |
| `delete-acl-table` | `devices`, `params.name` |
| `bind-acl` | `devices`, `interface`, `params.name`, `params.direction` |
| `unbind-acl` | `devices`, `interface`, `params.name` |
| `remove-ip` | `devices`, `interface`, `params.ip` |

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

    opts     RunOptions
    scenario *Scenario
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
2. Call `r.Client.RegisterNetwork(specDir)` — the server loads specs, topology, and platform definitions from the spec directory
3. Query `r.Client.TopologyDeviceNames()` to discover all devices in the topology
4. For each device, check `r.Client.IsHostDevice(name)`:
   - **Host devices**: establish a direct SSH connection via `connectHostSSH` and store in `r.HostConns`
   - **SONiC devices**: no pre-connection — the server connects on demand when an operation targets the device

`connectHostSSH` resolves the host's SSH credentials (user, password, port, management IP) from the device profile via `r.Client.GetHostProfile(name)`, then dials SSH directly.

### 4.9 Runner Helpers

```go
func (r *Runner) allDeviceNames() []string
func (r *Runner) resolveDevices(step *Step) []string
func (r *Runner) hasDataplane() bool
func HasRequires(scenarios []*Scenario) bool
```

`allDeviceNames` queries `r.Client.TopologyDeviceNames()` for the current device list.

`hasDataplane` calls `r.Client.ShowPlatform(platformName)` and checks if
`PlatformSpec.Dataplane` is non-empty.

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
is currently executing (e.g., "provision", "verify-bgp"), not just the step name.

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

## 7. Step Executors (`steps.go`, `steps_host.go`)

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
var executors = map[StepAction]stepExecutor{ ... } // see scenario.go for full list

func (r *Runner) executeStep(ctx context.Context, step *Step, index, total int, opts RunOptions) *StepOutput
```

`executeStep` looks up the executor, calls `Execute`, sets duration and name
on the result, and aggregates per-device error details into the `Message` field
when executors only set `Details`.

### 7.3 Multi-Device Helpers

```go
func (r *Runner) executeForDevices(step *Step, fn func(name string) (string, error)) *StepOutput
func (r *Runner) checkForDevices(step *Step, fn func(name string) (StepStatus, string)) *StepOutput
func (r *Runner) pollForDevices(ctx context.Context, step *Step, fn func(name string) (done bool, msg string, err error)) *StepOutput
```

Three patterns used by executors. Note the callback signatures — they receive
only the device `name` string. The callback body uses `r.Client` to perform
all operations against the server:

- **executeForDevices**: for mutating actions (provision, apply-service, create-vlan, etc.). Calls `r.Client.*()` for each device.
- **checkForDevices**: for single-shot verification (verify-health, verify-config-db). Returns per-device status.
- **pollForDevices**: for polling verification (verify-bgp, verify-state-db, verify-route). Polls until `step.Expect.Timeout` using `step.Expect.PollInterval`.

**Host-skip behavior:** all three helpers check `r.HostConns[name]` before invoking the callback. If the device is a host, it is skipped with status `SKIP` and message "host device (SONiC operation/verification not applicable)". This means `devices: all` in a mixed switch+host topology automatically restricts SONiC operations to switches only. The `provisionExecutor` implements the same host-skip inline (it does not use `executeForDevices`). The only executor that targets hosts is `hostExecExecutor`, which uses `r.HostConns` directly.

### 7.4 Param Helpers

```go
func strParam(params map[string]any, key string) string
func intParam(params map[string]any, key string) int
func boolParam(params map[string]any, key string) bool
func strSliceParam(params map[string]any, key string) []string
```

Extract typed values from `step.Params`. Used by all operation executors to
read action-specific parameters from YAML. `intParam` handles int, float64
(from YAML), and string (via strconv). `strSliceParam` handles `[]any` (YAML
default for lists) by converting each element to a string.

### 7.5 Executor Summary

All SONiC operations go through `r.Client.*()` HTTP client methods. The "Client Call" column shows the primary method invoked by each executor.

| # | Executor | Action | Client Call |
|---|----------|--------|-------------|
| 1 | `provisionExecutor` | `provision` | `GenerateComposite` → `ConfigReload` → `RefreshWithRetry` → `DeliverComposite` → `Refresh` → `SaveConfig` |
| 2 | `waitExecutor` | `wait` | — (context-aware time.After) |
| 3 | `verifyProvisioningExecutor` | `verify-provisioning` | `VerifyComposite(name, handle)` |
| 4 | `verifyConfigDBExecutor` | `verify-config-db` | `ConfigDBTableKeys` / `QueryConfigDB` / `ConfigDBEntryExists` |
| 5 | `verifyStateDBExecutor` | `verify-state-db` | `QueryStateDB` (polling) |
| 6 | `verifyBGPExecutor` | `verify-bgp` | `CheckBGPSessions` (polling) |
| 7 | `verifyHealthExecutor` | `verify-health` | `HealthCheck` |
| 8 | `verifyRouteExecutor` | `verify-route` | `GetRoute` / `GetRouteASIC` (polling) |
| 9 | `verifyPingExecutor` | `verify-ping` | `ShowPlatform` + `DeviceInfo` + `SSHCommand("ping ...")` |
| 10 | `applyServiceExecutor` | `apply-service` | `ApplyService` |
| 11 | `removeServiceExecutor` | `remove-service` | `RemoveService` |
| 12 | `configureLoopbackExecutor` | `configure-loopback` | `ConfigureLoopback` |
| 13 | `sshCommandExecutor` | `ssh-command` | `SSHCommand` |
| 14 | `restartServiceExecutor` | `restart-service` | `RestartService` |
| 15 | `configReloadExecutor` | `config-reload` | `ConfigReload` |
| 16 | `applyFRRDefaultsExecutor` | `apply-frr-defaults` | `ApplyFRRDefaults` |
| 17 | `setInterfaceExecutor` | `set-interface` | `SetIP` / `SetVRF` / `InterfaceSet` (by property) |
| 18 | `createVLANExecutor` | `create-vlan` | `CreateVLAN` |
| 19 | `deleteVLANExecutor` | `delete-vlan` | `DeleteVLAN` |
| 20 | `addVLANMemberExecutor` | `add-vlan-member` | `AddVLANMember` |
| 21 | `removeVLANMemberExecutor` | `remove-vlan-member` | `RemoveVLANMember` |
| 22 | `configureSVIExecutor` | `configure-svi` | `ConfigureSVI` |
| 23 | `removeSVIExecutor` | `remove-svi` | `RemoveSVI` |
| 24 | `createVRFExecutor` | `create-vrf` | `CreateVRF` |
| 25 | `deleteVRFExecutor` | `delete-vrf` | `DeleteVRF` |
| 26 | `addVRFInterfaceExecutor` | `add-vrf-interface` | `AddVRFInterface` |
| 27 | `removeVRFInterfaceExecutor` | `remove-vrf-interface` | `RemoveVRFInterface` |
| 28 | `setupEVPNExecutor` | `setup-evpn` | `SetupEVPN` |
| 29 | `teardownEVPNExecutor` | `teardown-evpn` | `TeardownEVPN` |
| 30 | `bindIPVPNExecutor` | `bind-ipvpn` | `BindIPVPN` |
| 31 | `unbindIPVPNExecutor` | `unbind-ipvpn` | `UnbindIPVPN` |
| 32 | `bindMACVPNExecutor` | `bind-macvpn` | `BindMACVPN` |
| 33 | `unbindMACVPNExecutor` | `unbind-macvpn` | `UnbindMACVPN` |
| 34 | `configureBGPExecutor` | `configure-bgp` | `ConfigureBGP` |
| 35 | `removeBGPGlobalsExecutor` | `remove-bgp-globals` | `RemoveBGPGlobals` |
| 36 | `bgpAddNeighborExecutor` | `bgp-add-neighbor` | `InterfaceAddBGPNeighbor` / `AddBGPNeighbor` |
| 37 | `bgpRemoveNeighborExecutor` | `bgp-remove-neighbor` | `InterfaceRemoveBGPNeighbor` / `RemoveBGPNeighbor` |
| 38 | `createACLTableExecutor` | `create-acl-table` | `CreateACLTable` |
| 39 | `deleteACLTableExecutor` | `delete-acl-table` | `DeleteACLTable` |
| 40 | `addACLRuleExecutor` | `add-acl-rule` | `AddACLRule` |
| 41 | `deleteACLRuleExecutor` | `delete-acl-rule` | `RemoveACLRule` |
| 42 | `bindACLExecutor` | `bind-acl` | `BindACL` |
| 43 | `unbindACLExecutor` | `unbind-acl` | `UnbindACL` |
| 44 | `applyQoSExecutor` | `apply-qos` | `ApplyQoS` |
| 45 | `removeQoSExecutor` | `remove-qos` | `RemoveQoS` |
| 46 | `addStaticRouteExecutor` | `add-static-route` | `AddStaticRoute` |
| 47 | `removeStaticRouteExecutor` | `remove-static-route` | `RemoveStaticRoute` |
| 48 | `createPortChannelExecutor` | `create-portchannel` | `CreatePortChannel` |
| 49 | `deletePortChannelExecutor` | `delete-portchannel` | `DeletePortChannel` |
| 50 | `addPortChannelMemberExecutor` | `add-portchannel-member` | `AddPortChannelMember` |
| 51 | `removePortChannelMemberExecutor` | `remove-portchannel-member` | `RemovePortChannelMember` |
| 52 | `removeIPExecutor` | `remove-ip` | `RemoveIP` |
| 53 | `removeLoopbackExecutor` | `remove-loopback` | `RemoveLoopback` |
| 54 | `refreshServiceExecutor` | `refresh-service` | `RefreshService` |
| 55 | `cleanupExecutor` | `cleanup` | `Cleanup` |
| 56 | `hostExecExecutor` | `host-exec` | — (direct SSH via `r.HostConns`) |
| 57 | `createPrefixListExecutor` | `create-prefix-list` | `CreatePrefixList` |
| 58 | `deletePrefixListExecutor` | `delete-prefix-list` | `DeletePrefixList` |
| 59 | `addPrefixEntryExecutor` | `add-prefix-entry` | `AddPrefixEntry` |
| 60 | `removePrefixEntryExecutor` | `remove-prefix-entry` | `RemovePrefixEntry` |
| 61 | `createRoutePolicyExecutor` | `create-route-policy` | `CreateRoutePolicy` |
| 62 | `deleteRoutePolicyExecutor` | `delete-route-policy` | `DeleteRoutePolicy` |
| 63 | `addRoutePolicyRuleExecutor` | `add-route-policy-rule` | `AddRoutePolicyRule` |
| 64 | `removeRoutePolicyRuleExecutor` | `remove-route-policy-rule` | `RemoveRoutePolicyRule` |
| 65 | `createServiceExecutor` | `create-service` | `CreateService` |
| 66 | `deleteServiceExecutor` | `delete-service` | `DeleteService` |

Executors 57–66 are **network-level spec authoring** actions. They operate at
network scope (no `devices:` field) and call `r.Client.*` directly. These
actions create or modify specs that services reference — they never touch
device CONFIG_DB.

### 7.6 Verification Executor Detail

**verifyConfigDBExecutor** — three assertion modes via `r.Client`:
1. `expect.MinEntries`: calls `ConfigDBTableKeys`/`QueryConfigDB` to count entries, pass if `≥ min`
2. `expect.Exists`: calls `ConfigDBEntryExists`, checks boolean match
3. `expect.Fields`: calls `QueryConfigDB`, compares each field value

**verifyStateDBExecutor** — polls `r.Client.QueryStateDB(name, table, key)` until
all `expect.Fields` match or timeout is reached.

**verifyBGPExecutor** — polls `r.Client.CheckBGPSessions(name)`. Checks all
results have `status: "pass"`. For non-default expected states, additionally
verifies message strings contain the expected state.

**verifyHealthExecutor** — single-shot call to `r.Client.HealthCheck(name)`.
Returns structured report with config verification counts and operational check
results. Does **not** poll. Use a `wait` step before `verify-health` if
convergence time is needed.

**verifyRouteExecutor** — polls `r.Client.GetRoute` (APP_DB) or
`r.Client.GetRouteASIC` (ASIC_DB) depending on `expect.source`. Matches
protocol and next-hop IP against the returned `RouteEntry`.

**verifyPingExecutor** — checks `r.Client.ShowPlatform` for dataplane support
first; skips on platforms without data plane. Resolves target to IP via
`r.Client.DeviceInfo` (device name → loopback IP), runs
`r.Client.SSHCommand(device, "ping -c N -W 5 <target>")`, parses packet loss
from output.

### 7.7 Provision Executor Detail

The `provisionExecutor` performs a multi-step sequence entirely via `r.Client`:

1. `r.Client.GenerateComposite(name)` → returns handle with UUID
2. `r.Client.ConfigReload(name)` — best-effort baseline restore (failure is non-fatal)
3. `r.Client.RefreshWithRetry(name, 60s)` — wait for SwSS readiness after reload
4. `r.Client.DeliverComposite(name, handle, "overwrite")` — server handles lock → deliver → unlock
5. `r.Client.Refresh(name)` — refresh cached CONFIG_DB and interface list
6. `r.Client.SaveConfig(name)` — persist to config_db.json for future config-reload steps

The handle UUID is stored in `r.Composites[name]` for later use by `verify-provisioning`.

### 7.8 BGP Neighbor Executor Dispatch

`bgpAddNeighborExecutor` dispatches based on `step.Interface`:
- **Interface set** → `r.Client.InterfaceAddBGPNeighbor(device, interface, config, opts)`
- **Interface empty** → `r.Client.AddBGPNeighbor(device, config, opts)` (loopback neighbor)

Same pattern for `bgpRemoveNeighborExecutor`.

### 7.9 Host Exec Executor (`steps_host.go`)

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

### 7.10 Executor Parameter Reference

Most executors follow a common pattern: extract step fields and params, call
one of the three multi-device helpers (§7.3), invoke an `r.Client` method inside
the callback, and return `StepOutput`. The table below lists every step-level
field and `params:` key consumed by each executor. **Bold** = required (enforced
by `stepValidations`); regular = optional. `—` = none consumed.

| Action | Step Fields | Params | Notes |
|--------|-------------|--------|-------|
| ***Provisioning*** | | | |
| `provision` | — | — | Inline host-skip; multi-step sequence (see §7.7); populates `StepOutput.Composites` |
| `configure-loopback` | — | — | |
| `remove-loopback` | — | — | |
| `apply-frr-defaults` | — | — | |
| `config-reload` | — | — | |
| ***Verification*** | | | |
| `verify-provisioning` | — | — | Reads `r.Composites[device]` (UUID handle from prior `provision` step) |
| `verify-config-db` | **table**, key | — | Three assertion modes via `expect:`: `min_entries` (count), `exists` (boolean), `fields` (value match) |
| `verify-state-db` | **table**, **key** | — | Polls until `expect.fields` all match or timeout |
| `verify-bgp` | — | — | Polls; reads `expect.state` (default: `"Established"`) |
| `verify-health` | — | — | Single-shot check; does not poll |
| `verify-route` | **prefix**, **vrf** | — | Polls; `expect.source`: `"asic_db"` → `GetRouteASIC`, else → `GetRoute`; matches `expect.protocol` and `expect.nexthop_ip` |
| `verify-ping` | **target**, count | — | Skips on platforms without dataplane; resolves target name → loopback IP via `DeviceInfo`; parses `% packet loss` from output |
| ***Service*** | | | |
| `apply-service` | **interface**, **service** | ip | Reads `params["ip"]` via direct map lookup (not `strParam`) |
| `remove-service` | **interface** | — | |
| `refresh-service` | **interface** | — | |
| ***VLAN*** | | | |
| `create-vlan` | **vlan_id** | — | |
| `delete-vlan` | **vlan_id** | — | |
| `add-vlan-member` | **vlan_id**, **interface**, tagging | — | `tagging: "tagged"` → tagged member; absent/`"untagged"` → untagged |
| `remove-vlan-member` | **vlan_id**, **interface** | — | |
| `configure-svi` | **vlan_id** | vrf, ip | Sends `SVIConfigureRequest{VlanID, VRF, IPAddress}` |
| `remove-svi` | **vlan_id** | — | |
| ***VRF*** | | | |
| `create-vrf` | **vrf** | — | |
| `delete-vrf` | **vrf** | — | |
| `add-vrf-interface` | **vrf**, **interface** | — | |
| `remove-vrf-interface` | **vrf**, **interface** | — | |
| ***EVPN*** | | | |
| `setup-evpn` | — | **source_ip** | |
| `teardown-evpn` | — | — | |
| `bind-ipvpn` | **vrf** | **ipvpn** | |
| `unbind-ipvpn` | **vrf** | — | |
| `bind-macvpn` | **vlan_id** | **macvpn** | Constructs `Vlan<id>` from `step.VLANID` for the `BindMACVPN` call |
| `unbind-macvpn` | **vlan_id** | — | Constructs `Vlan<id>` from `step.VLANID` |
| ***BGP*** | | | |
| `configure-bgp` | — | — | |
| `remove-bgp-globals` | — | — | |
| `bgp-add-neighbor` | interface | **remote_asn**, neighbor_ip | Dispatch: interface set → `InterfaceAddBGPNeighbor`, absent → `AddBGPNeighbor` |
| `bgp-remove-neighbor` | interface | **neighbor_ip** | Same dispatch: interface set → `InterfaceRemoveBGPNeighbor`, absent → `RemoveBGPNeighbor` |
| ***ACL*** | | | |
| `create-acl-table` | — | **name**, type, stage, description | type default: L3; stage default: ingress |
| `delete-acl-table` | — | **name** | Deletes ACL_TABLE + all ACL_RULE entries |
| `add-acl-rule` | — | **name**, **rule**, **action**, priority, src_ip, dst_ip, protocol, src_port, dst_port | action = FORWARD/DROP; 9 params total |
| `delete-acl-rule` | — | **name**, **rule** | |
| `bind-acl` | **interface** | **name**, **direction** | direction = ingress/egress |
| `unbind-acl` | **interface** | **name** | |
| ***QoS*** | | | |
| `apply-qos` | **interface** | **qos_policy** | |
| `remove-qos` | **interface** | — | |
| ***Interface*** | | | |
| `set-interface` | **interface** | **property**, value | Dispatch on property: `ip` → `SetIP`, `vrf` → `SetVRF`, else → `InterfaceSet` |
| `remove-ip` | **interface** | **ip** | |
| ***Routing*** | | | |
| `add-static-route` | **vrf**, **prefix** | **next_hop**, metric | metric via `intParam` (default 0) |
| `remove-static-route` | **vrf**, **prefix** | — | |
| ***PortChannel*** | | | |
| `create-portchannel` | — | name, members, mtu, min_links, fallback, fast_rate | No `stepValidations` entry; members via `strSliceParam`; fallback, fast_rate via `boolParam` |
| `delete-portchannel` | — | name | No `stepValidations` entry |
| `add-portchannel-member` | — | name, member | No `stepValidations` entry |
| `remove-portchannel-member` | — | name, member | No `stepValidations` entry |
| ***Host*** | | | |
| `host-exec` | **command** | — | Direct SSH via `r.HostConns`; wraps in `ip netns exec <device> sh -c`; checks `expect.success_rate` or `expect.contains` |
| ***Utility*** | | | |
| `wait` | **duration** | — | Context-aware sleep; no device interaction |
| `ssh-command` | **command** | — | Checks `expect.contains` for output matching |
| `restart-service` | **service** | — | |
| `cleanup` | — | type | Empty type → all cleanup categories (acl, vrf, vni) |

### 7.11 YAML Examples

Representative YAML patterns covering the major test categories. All examples
are type-valid and can be copy-pasted into scenario files.

**Provisioning + verification** — the most common suite preamble:

```yaml
steps:
  - name: provision-switches
    action: provision
    devices: [switch1, switch2]

  - name: config-reload
    action: config-reload
    devices: [switch1, switch2]

  - name: wait-convergence
    action: wait
    duration: 45s

  - name: verify-bgp
    action: verify-bgp
    devices: [switch1, switch2]
    expect:
      state: Established
      timeout: 120s
      poll_interval: 5s

  - name: verify-provisioning
    action: verify-provisioning
    devices: [switch1, switch2]
```

**Service lifecycle** — apply with IP param, verify route propagation, remove:

```yaml
steps:
  - name: apply-customer-service
    action: apply-service
    devices: [switch1]
    interface: Ethernet10
    service: customer-l3
    params:
      ip: 10.1.1.1/30

  - name: verify-route-propagation
    action: verify-route
    devices: [switch2]
    prefix: 10.1.1.0/30
    vrf: default
    expect:
      timeout: 60s
      protocol: bgp

  - name: remove-customer-service
    action: remove-service
    devices: [switch1]
    interface: Ethernet10
```

**VLAN + EVPN overlay** — create L2 domain and bind to MAC-VPN:

```yaml
steps:
  - name: create-vlan-300
    action: create-vlan
    devices: [switch1, switch2]
    vlan_id: 300

  - name: add-member-vlan-300
    action: add-vlan-member
    devices: [switch1]
    vlan_id: 300
    interface: Ethernet3
    tagging: untagged

  - name: bind-l2-overlay
    action: bind-macvpn
    devices: [switch1, switch2]
    vlan_id: 300
    params:
      macvpn: servers-vlan300
```

**ACL management** — create table, add rule, bind to interface:

```yaml
steps:
  - name: create-acl
    action: create-acl-table
    devices: [switch1]
    params:
      name: CUSTOMER_ACL
      type: L3
      stage: ingress

  - name: add-deny-rule
    action: add-acl-rule
    devices: [switch1]
    params:
      name: CUSTOMER_ACL
      rule: RULE_10
      action: DROP
      priority: 10
      src_ip: "10.0.0.0/8"

  - name: bind-acl-to-port
    action: bind-acl
    devices: [switch1]
    interface: Ethernet10
    params:
      name: CUSTOMER_ACL
      direction: ingress
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

### 7.12 Worked Example: apply-service

Tracing a single `apply-service` step from YAML through every layer
(editing guideline #10: "trace one concrete operation through every layer").

**1. YAML definition**

```yaml
- name: apply-transit
  action: apply-service
  devices: [switch1]
  interface: Ethernet2
  service: transit
  params:
    ip: 10.10.1.1/31
```

**2. Parser — `applyDefaults`** (`parser.go:318`). `apply-service` has no
defaults to inject (`verify-*` actions only). The step passes through unchanged.

**3. Parser — `validateStepFields`** (`parser.go:184`). Looks up
`stepValidations[ActionApplyService]`:

```go
ActionApplyService: {needsDevices: true, fields: []string{"interface", "service"}}
```

Checks: `devices` non-empty ✓, `step.Interface` ("Ethernet2") non-empty ✓,
`step.Service` ("transit") non-empty ✓. Validation passes. Note that `params.ip`
is not validated — it is optional and only checked by the executor.

**4. Runner — `executeStep`** (`runner.go`). Looks up
`executors[ActionApplyService]` → `&applyServiceExecutor{}`. Calls
`executor.Execute(ctx, r, step)`.

**5. Executor — `applyServiceExecutor.Execute`** (`steps.go:821`). Reads
`step.Params["ip"]` via direct map lookup → `opts.IPAddress = "10.10.1.1/31"`.
Calls `r.executeForDevices(step, fn)`. The helper resolves `devices: [switch1]`,
checks `r.HostConns["switch1"]` (not a host — no skip), and invokes the callback
with `name = "switch1"`.

**6. Client — `r.Client.ApplyService`** (`client/client.go`). Sends HTTP POST:

```
POST /networks/{id}/nodes/switch1/interfaces/Ethernet2/apply-service
Body: {"service": "transit", "ip_address": "10.10.1.1/31", "exec_opts": {"execute": true}}
```

**7. Server** (`api/handler_interface.go`). The handler resolves the node, calls
`node.GetInterface("Ethernet2")` → `iface.ApplyService(ctx, "transit", opts)`.
The internal ApplyService resolves the "transit" ServiceSpec from the node's
SpecProvider, generates CONFIG_DB entries (INTERFACE VRF binding, IP address, BGP
neighbor, route policies), builds a ChangeSet, and applies it to Redis via
`DEL + HSET` sequences (§CONFIG_DB Replace Semantics). Returns
`WriteResult{ChangeCount: N}`.

**8. Response** — server serializes `WriteResult` as JSON. Client deserializes it.
Executor formats message: `"applied service transit on Ethernet2 (N changes)"`.
Returns `StepOutput{Result: &StepResult{Status: PASS, Message: ...}}`.

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

- No args: lists all actions grouped by category (Provisioning, Verification, VLAN, VRF, EVPN, Service, QoS, BGP, ACL, PortChannel, Interface, Routing, Host, Spec Authoring, Utility)
- With action name: shows detailed information including category, description, prerequisites, required/optional parameters, devices requirement, and YAML example

The `ActionMetadata` struct and `getActionMetadata()` function provide structured
metadata for each action, manually maintained in sync with `stepValidations` in
`parser.go`.

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
