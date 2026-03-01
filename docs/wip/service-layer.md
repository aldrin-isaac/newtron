# Service Layer (`pkg/newtron/service/`)

Status: WIP — implemented on `main` branch.

## Motivation

Before this change, the CLI (`cmd/newtron/`) interleaved three concerns:

1. **Connection lifecycle** — connect, lock, unlock, disconnect
2. **Permission checks** — auth.Checker calls before write operations
3. **Presentation** — flag parsing, output formatting, color codes

Every `cmd_*.go` write handler duplicated the same `withDeviceWrite` →
`checkExecutePermission` → `dev.SomeMethod()` → `executeAndSave` boilerplate.
This made it impossible to reuse the API surface from newtrun or a future HTTP
server without duplicating all the lifecycle and auth logic.

## What Changed

### New package: `pkg/newtron/service/`

A clean Go API that owns connection lifecycle and permission checks. The CLI
becomes a thin presentation layer that parses flags and formats output.

| File | Lines | Contents |
|------|-------|----------|
| `service.go` | 140 | Service struct, constructor, lifecycle helpers |
| `types.go` | 413 | Request/response types, WriteResult, ExecuteOpts |
| `device_read.go` | 248 | 15 device read operations |
| `device_write.go` | 854 | 42 device write operations (OnNode + Device pairs) |
| `spec_ops.go` | 400 | 26 spec CRUD operations |
| `provision.go` | 68 | Topology provisioning + composite generation |
| `errors.go` | 39 | Typed errors (NotFoundError, ValidationError, VerificationError) |
| **Total** | **~2200** | **~130 Service methods** |

### Core types

```go
type Service struct {
    net         *network.Network
    permChecker *auth.Checker
}

type ExecuteOpts struct {
    Execute bool  // true = apply; false = dry-run preview
    NoSave  bool  // skip config save after apply
}

type WriteResult struct {
    ChangeSet *node.ChangeSet
    Applied   bool
    Verified  bool
    Saved     bool
}
```

### Lifecycle helpers

The Service provides three internal lifecycle patterns:

- **`withDeviceRead`** — connect → fn(dev) → disconnect. For read operations.
- **`withDeviceWrite`** — connect → lock → fn → [apply → verify → save] → unlock → disconnect. For CLI write operations.
- **`withNodeWrite`** — same as above but on a pre-connected node. For callers that manage their own connections.

### OnNode pattern

Every device write operation has two methods:

```go
// OnNode: the operation itself (permission check + Node/Interface call).
// Returns a ChangeSet for callers who manage their own lifecycle (newtrun, future HTTP).
func (s *Service) CreateVLANOnNode(ctx context.Context, dev *node.Node,
    req *CreateVLANRequest) (*node.ChangeSet, error)

// Device: wraps OnNode with connect → lock → apply → verify → save → unlock → disconnect.
// Used by CLI where the service layer owns the full lifecycle.
func (s *Service) CreateVLAN(ctx context.Context,
    req *CreateVLANRequest, opts ExecuteOpts) (*WriteResult, error)
```

The Device method calls `withDeviceWrite` which calls the OnNode method internally.
This gives callers two entry points:

- **CLI/HTTP** call the Device method (`CreateVLAN`) — full lifecycle.
- **newtrun** calls the OnNode method (`CreateVLANOnNode`) inside `dev.ExecuteOp()` — caller owns lifecycle.

Some OnNode methods resolve specs internally (e.g., `BindIPVPNOnNode` looks up
the IPVPN spec, `ApplyQoSOnNode` looks up the QoS policy), so callers don't need
to pass resolved specs.

### CLI refactor (`cmd/newtron/`)

12 command files modified (620 insertions, 1017 deletions — net reduction of ~400 lines).

**Removed from `main.go`:**
- `withDeviceWrite` — moved to service layer
- `executeAndSave` — moved to service layer
- `checkExecutePermission` — moved to service layer

**Added to `main.go`:**
- `App.svc *service.Service` field, initialized in `PersistentPreRunE`
- `printWriteResult(result)` — shared output helper for all write commands

**Command handler pattern (before → after):**

Before:
```go
RunE: func(cmd *cobra.Command, args []string) error {
    return withDeviceWrite(func(ctx context.Context, dev *node.Node) (*node.ChangeSet, error) {
        authCtx := auth.NewContext().WithDevice(app.deviceName)
        if err := checkExecutePermission(auth.PermVLANCreate, authCtx); err != nil {
            return nil, err
        }
        return dev.CreateVLAN(ctx, vlanID, node.VLANConfig{Description: desc})
    })
}
```

After:
```go
RunE: func(cmd *cobra.Command, args []string) error {
    result, err := app.svc.CreateVLAN(cmd.Context(), &service.CreateVLANRequest{
        Device:      app.deviceName,
        VlanID:      vlanID,
        Description: desc,
    }, service.ExecuteOpts{Execute: app.executeMode, NoSave: app.noSave})
    if err != nil {
        return err
    }
    return printWriteResult(result)
}
```

### newtrun refactor (`pkg/newtrun/`)

Runner gains a `Service` field:

```go
type Runner struct {
    Service  *service.Service   // service layer API (owns Network + auth)
    Network  *network.Network   // convenience: Service.Network()
    // ... other fields unchanged
}
```

Initialized in `connectDevices` with no auth checker (test runner doesn't need
permission checks):

```go
r.Service = service.New(net, nil)
r.Network = r.Service.Network()
```

All 42 step executors migrated to use `r.Service.XxxOnNode()` inside their
existing `dev.ExecuteOp()` callbacks. newtrun still manages its own connection
lifecycle (connect once at suite start, reuse for entire suite), but the
operations themselves go through the service layer.

**Executor pattern (before → after):**

Before:
```go
func (e *createVlanExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
    return executeForDevices(step, func(dev *node.Node, name string) (*node.ChangeSet, string, error) {
        cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
            return dev.CreateVLAN(ctx, step.VlanID, node.VLANConfig{Description: step.Description})
        })
        return cs, msg, err
    })
}
```

After:
```go
func (e *createVlanExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
    return executeForDevices(step, func(dev *node.Node, name string) (*node.ChangeSet, string, error) {
        cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
            return r.Service.CreateVLANOnNode(ctx, dev, &service.CreateVLANRequest{
                Device:      name,
                VlanID:      step.VlanID,
                Description: step.Description,
            })
        })
        return cs, msg, err
    })
}
```

## Architecture

```
                    ┌──────────────┐
                    │  CLI (cobra) │  ← parse flags, format output
                    └──────┬───────┘
                           │
   ┌──────────────┐        │         ┌────────────────┐
   │   newtrun    │────────┼────────▶│ service.Service │ ← lifecycle + auth
   │ (orchestrator)│       │         └──────┬──────────┘
   └──────────────┘        │                │
                           │         ┌──────┴──────────┐
                           │         │ network.Network  │ ← specs + device factory
                           │         │ node.Node        │ ← device operations
                           │         │ node.Interface   │ ← service binding point
                           │         └─────────────────┘
```

## Operation Catalog

### Device reads (15 methods)

Connect, read, disconnect. Return structured data.

ShowDevice, ListInterfaces, ShowInterface, GetInterfaceProperty,
ListVLANs, ShowVLAN, ListVRFs, ShowVRF, GetOrphanedACLs,
ListLAGs, ShowLAG, GetServiceBinding, VTEPExists, HealthCheck

### Device writes (42 operations × 2 methods = 84 methods)

Each operation has an `OnNode` method (the operation itself) and a `Device` method
(lifecycle wrapper). The Device method follows:
check permission → connect → lock → execute → apply → verify → save → unlock → disconnect.
Device methods return `(*WriteResult, error)`. OnNode methods return `(*node.ChangeSet, error)`.

- **Service**: ApplyService, RemoveService, RefreshService
- **VLAN**: CreateVLAN, DeleteVLAN, AddVLANMember, RemoveVLANMember,
  ConfigureSVI, RemoveSVI, BindMACVPN, UnbindMACVPN
- **VRF**: CreateVRF, DeleteVRF, AddVRFInterface, RemoveVRFInterface,
  BindIPVPN, UnbindIPVPN, AddBGPNeighbor, RemoveBGPNeighbor,
  AddStaticRoute, RemoveStaticRoute
- **EVPN**: SetupEVPN, TeardownEVPN
- **ACL**: CreateACL, DeleteACL, AddACLRule, RemoveACLRule, BindACL, UnbindACL
- **QoS**: ApplyQoS, RemoveQoS
- **Interface**: SetInterfaceProperty, RemoveIP
- **LAG**: CreateLAG, DeleteLAG, AddLAGMember, RemoveLAGMember
- **BGP**: ConfigureBGP, RemoveBGPGlobals
- **Loopback**: ConfigureLoopback, RemoveLoopback
- **Device**: Cleanup

### Spec operations (26 methods)

No device connection. Read/write spec files.

- **Services**: ListServices, ShowService, CreateService, DeleteService
- **IP-VPNs**: ListIPVPNs, ShowIPVPN, CreateIPVPN, DeleteIPVPN
- **MAC-VPNs**: ListMACVPNs, ShowMACVPN, CreateMACVPN, DeleteMACVPN
- **QoS**: ListQoSPolicies, ShowQoSPolicy, CreateQoSPolicy, DeleteQoSPolicy,
  AddQoSQueue, RemoveQoSQueue
- **Filters**: ListFilters, ShowFilter, CreateFilter, DeleteFilter,
  AddFilterRule, RemoveFilterRule
- **Platforms**: Platforms (returns all)

### Provision (2 methods)

- **Provision** — generates composite + delivers to devices
- **GenerateDeviceComposite** — dry-run composite generation

## Error Types

```go
type NotFoundError struct{ Resource, Name string }
type ValidationError struct{ Field, Message string }
type VerificationError struct{ Device, Message string; Passed, Failed, Total int }
```

Future HTTP layer maps these to status codes: 404, 400, 500.

## What Did NOT Change

- **Node/Interface API** — unchanged. Service layer delegates to it.
- **ChangeSet mechanics** — unchanged. Service layer uses Apply/Verify/SaveConfig.
- **Spec file format** — unchanged.
- **CLI behavior** — identical flags, output, and dry-run semantics.
- **newtrun connection lifecycle** — still connects once per device, reuses for entire suite.
- **newtrun `dev.ExecuteOp()` pattern** — executors still use ExecuteOp for lock/apply/unlock; operations inside go through service layer OnNode methods.

## Next Steps

- HTTP server that uses `service.Service` as its backend
- Production orchestration (see `docs/wip/production-orchestration.md`)
