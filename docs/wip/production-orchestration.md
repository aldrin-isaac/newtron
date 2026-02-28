# Production Orchestration Requirements

Status: WIP — capturing requirements for evolving newtrun from lab orchestrator
to production-capable orchestrator.

## Context

newtron is a per-device semantic change driver. It understands every allowable
operation on a SONiC device, enforces preconditions, and provides reference-aware
reverse operations. newtrun is the orchestrator that composes newtron operations
across multiple devices.

Today newtrun is lab-grade: it deploys virtual topologies via newtlab, provisions
devices, runs test scenarios, and tears down. These are the structural gaps that
must be addressed for production use.

## Gap 1: Decoupled from Topology Lifecycle

newtrun assumes it owns the full lifecycle — deploy VMs, provision, test, destroy.
Production devices already exist. The orchestrator must work with existing
infrastructure without owning the device lifecycle.

**Requirements:**
- Connect to existing devices by profile/inventory without newtlab
- No assumption that the orchestrator deployed the devices
- Support heterogeneous environments (mix of physical and virtual, different
  SONiC versions, different platforms)
- Device discovery or inventory integration (static file at minimum, IPAM/DCIM
  integration eventually)

## Gap 2: Compensation on Failure (Rollback)

If a suite provisions switch1 successfully then fails on switch2, newtrun reports
failure and stops. It never calls reverse operations on switch1 to restore it.
A production orchestrator must compensate for partial failures.

**Requirements:**
- Track which operations succeeded on which devices during a suite run
- On failure, invoke newtron's domain-level reverse operations to compensate
  (not mechanical ChangeSet reversal — see DESIGN_PRINCIPLES.md Principle #23,
  "Shared Resources and Safe Reversal")
- Compensation must respect shared resources: RemoveService checks for remaining
  consumers before deleting VRFs, filters, etc.
- Configurable compensation policy: auto-rollback, pause-and-ask, continue-on-error
- Compensation log: record what was rolled back and what was left in place

## Gap 3: API Service

newtrun is CLI-only with local state.json on disk. A production orchestrator must
be a service.

**Requirements:**
- REST or gRPC API for triggering operations and querying status
- Authentication and authorization (API keys at minimum, OIDC/RBAC eventually)
- Persistent state store (database, not local JSON files)
- Webhook/event notifications for operation completion, failure, drift detection
- Integration surface for CI/CD pipelines, change management systems, and
  monitoring platforms

## Gap 4: Concurrent Device Operations

Within a step, newtrun provisions devices sequentially. Production fabrics have
hundreds of devices.

**Requirements:**
- Concurrent device operations with configurable parallelism (e.g., 10 devices
  at a time)
- Per-device failure isolation: one device failing does not block others
- Progress reporting at device granularity during concurrent operations
- Rate limiting to avoid overwhelming management network or SONiC daemons

## Gap 5: Dry-Run Orchestration

newtron supports per-device dry-run, but newtrun has no way to preview an entire
suite's changes across all devices before committing.

**Requirements:**
- Dry-run mode that collects ChangeSets from all devices without applying them
- Unified diff view: all changes across all devices in one report
- Change approval gate: human reviews the dry-run output before execution proceeds
- Change window enforcement: operations only execute during approved windows

## Priority

Gaps 1 and 2 are structural — they require architectural changes to newtrun.
Gaps 3-5 are features that can be added incrementally once the architecture
supports them.

Gap 2 (compensation) is the hardest. It requires the orchestrator to maintain
a per-device operation journal and know the correct reverse operation sequence,
respecting the reference-aware teardown constraints documented in
DESIGN_PRINCIPLES.md.
