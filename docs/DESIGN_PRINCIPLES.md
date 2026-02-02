# Design Principles for Robust SONiC Management

Lessons learned from building and debugging the newtron E2E test suite.
These principles guide the development of robust network management
software that navigates SONiC pitfalls by design.

## Table of Contents

- [1. Separation of Intent and State](#1-separation-of-intent-and-state)
- [2. Defensive Verification](#2-defensive-verification)
- [3. Platform Abstraction](#3-platform-abstraction)
- [4. Graceful Degradation](#4-graceful-degradation)
- [5. Connection Resilience](#5-connection-resilience)
- [6. Test Architecture](#6-test-architecture)
- [7. Operational Safety](#7-operational-safety)
- [8. Debugging by Design](#8-debugging-by-design)
- [9. Configuration Discovery](#9-configuration-discovery)
- [10. Anti-Patterns to Avoid](#10-anti-patterns-to-avoid)

---

## 1. Separation of Intent and State

### Principle

Distinguish between what the operator **wants** (CONFIG_DB) and what the
system **is** (STATE_DB). Never treat them as interchangeable.

### Why It Matters

On SONiC-VS (and sometimes on hardware during convergence), CONFIG_DB and
STATE_DB diverge:

| Scenario | CONFIG_DB | STATE_DB | Impact |
|---|---|---|---|
| MTU change | 9000 | 9100 | ASIC didn't apply to kernel |
| VLAN creation | Vlan800 exists | No VLAN entry | ASIC convergence pending |
| VXLAN tunnel | Configured | Shows "up" | Data plane doesn't forward |

### Implementation

```go
// GOOD: Separate methods for intent vs state
type Interface struct {
    configMTU int  // from CONFIG_DB
    operMTU   int  // from STATE_DB
}

func (i *Interface) DesiredMTU() int     { return i.configMTU }
func (i *Interface) OperationalMTU() int { return i.operMTU }

// BAD: Single method that hides the source
func (i *Interface) MTU() int {
    if i.operMTU != 0 {
        return i.operMTU  // STATE_DB overrides -- hides CONFIG_DB
    }
    return i.configMTU
}
```

### Design Pattern

- **For writes:** Write to CONFIG_DB, verify CONFIG_DB.
- **For reads:** Expose both desired (CONFIG_DB) and operational (STATE_DB)
  state separately.
- **For health checks:** Compare CONFIG_DB vs STATE_DB to detect drift.

---

## 2. Defensive Verification

### Principle

Verify at the right layer. Don't assume Layer N works because Layer N-1
does.

### Verification Hierarchy

```
1. Verify CONFIG_DB write succeeded    → redis HGETALL
2. Verify ASIC_DB received the config  → ASIC_DB key scan
3. Verify kernel state reflects config → ip link, bridge, tc
4. Verify data-plane forwarding works  → ping, tcpdump
5. Verify protocol state is correct    → BGP sessions, EVPN routes
```

### Implementation

```go
// After any configuration change:
func verifyConfigApplied(ctx context.Context, t *testing.T, d *Device, vlanID int) {
    // Layer 1: CONFIG_DB
    testutil.AssertConfigDBEntryExists(t, d.Name, "VLAN", fmt.Sprintf("Vlan%d", vlanID))

    // Layer 2: ASIC_DB (may soft-fail on VS)
    if err := testutil.WaitForASICVLAN(ctx, t, d.Name, vlanID); err != nil {
        if isVirtualSwitch(d) {
            t.Logf("ASIC convergence timeout (expected on VS): %v", err)
        } else {
            t.Fatalf("ASIC convergence failed: %v", err)
        }
    }
}
```

### Rule

- **Config operations:** Verify Layer 1 (CONFIG_DB) -- hard fail
- **Convergence tests:** Verify Layer 2 (ASIC_DB) -- soft fail on VS
- **Connectivity tests:** Verify Layer 4 (data-plane) -- soft fail on VS
- **Never skip Layer 1** -- if CONFIG_DB is wrong, everything else is wrong

---

## 3. Platform Abstraction

### Principle

Build a platform capability model that tests and operations query at
runtime to adapt their behavior.

### Implementation

```go
type PlatformCapabilities struct {
    SupportsVXLANDataPlane    bool
    SupportsASICConvergence   bool
    MTUAppliedToKernel        bool
    ARPSuppressionWorks       bool
    StateDBReliableForMTU     bool
    StateDBReliableForBGP     bool
}

func DetectCapabilities(d *Device) PlatformCapabilities {
    meta := d.ConfigDB.DeviceMetadata()
    isVS := meta.Platform == "x86_64-kvm_x86_64-r0"

    return PlatformCapabilities{
        SupportsVXLANDataPlane:  !isVS,
        SupportsASICConvergence: !isVS, // partial on VS
        MTUAppliedToKernel:      !isVS,
        ARPSuppressionWorks:     !isVS,
        StateDBReliableForMTU:   !isVS,
        StateDBReliableForBGP:   true, // FRR is independent
    }
}
```

### Usage in Tests

```go
func TestE2E_DataPlane_L2Bridged(t *testing.T) {
    // ... setup ...

    // Control-plane verification (always hard-fail)
    testutil.AssertConfigDBEntry(t, name, "VLAN", "Vlan700", ...)

    // Data-plane verification (platform-adaptive)
    if !testutil.ServerPing(t, "server1", "10.70.0.2", 5) {
        caps := DetectCapabilities(dev)
        if !caps.SupportsVXLANDataPlane {
            t.Skip("VXLAN data-plane not supported on this platform")
        }
        t.Fatal("ping failed on hardware platform (should work)")
    }
}
```

### Benefits

- Same test code runs on VS and hardware
- Failure messages explain WHY the test skipped
- New platforms can be added without changing test logic
- CI can run against VS (skip data-plane) and nightly against HW (full)

---

## 4. Graceful Degradation

### Principle

When a feature doesn't work on a platform, degrade gracefully: log
diagnostics, then skip. Never silently pass or cryptically fail.

### Three-Step Pattern

```go
// Step 1: Attempt the operation
if !testutil.ServerPing(t, "server1", "10.70.0.2", 5) {
    // Step 2: Log diagnostics (always, even before skip)
    t.Logf("Ping failed. Collecting diagnostics...")
    ifout, _ := testutil.ServerExec(t, "server1", "ip", "addr", "show", "eth1")
    t.Logf("server1 interfaces: %s", ifout)
    rtout, _ := testutil.ServerExec(t, "server1", "ip", "route", "show")
    t.Logf("server1 routes: %s", rtout)

    // Step 3: Skip with explanation
    t.Skip("VXLAN data-plane forwarding not supported on virtual switch")
}
```

### Why Diagnostics Before Skip

Even on platforms where failure is expected, diagnostic logs are valuable:
- They confirm the failure is the EXPECTED failure (not a new bug)
- They provide baseline data for future debugging
- They document what the system state looks like when the feature doesn't
  work

### Anti-Pattern

```go
// BAD: No diagnostics, no explanation
if !testutil.ServerPing(t, "server1", "10.70.0.2", 5) {
    t.Skip("skip")
}
```

---

## 5. Connection Resilience

### Principle

Network connections fail. Design for reconnection, not for permanent
connections.

### SSH Tunnel Design

```go
type SSHTunnel struct {
    localAddr string
    sshClient *ssh.Client
    listener  net.Listener
    done      chan struct{}
    wg        sync.WaitGroup
}

// Key design decisions:
// 1. Random local port (127.0.0.1:0) -- avoids port conflicts
// 2. Goroutine per accepted connection -- handles concurrent Redis clients
// 3. Bidirectional io.Copy -- no data loss
// 4. Clean shutdown via Close() -- stops listener, waits for goroutines
// 5. InsecureIgnoreHostKey for lab (production should verify)
```

### Connection Pool Pattern

```go
var (
    tunnelMu sync.Mutex
    tunnels  map[string]*SSHTunnel
)

func getTunnel(host, user, pass string) (*SSHTunnel, error) {
    tunnelMu.Lock()
    defer tunnelMu.Unlock()

    key := host
    if t, ok := tunnels[key]; ok {
        // Verify tunnel is still alive
        if t.IsAlive() {
            return t, nil
        }
        t.Close() // Dead tunnel, recreate
    }

    t, err := NewSSHTunnel(host, user, pass)
    if err != nil {
        return nil, err
    }
    tunnels[key] = t
    return t, nil
}
```

### Fresh Connection for Verification

After writing to CONFIG_DB, the device's in-memory state is stale. Always
create a new connection to verify:

```go
// GOOD: Fresh connection reads current CONFIG_DB
dev := testutil.LabLockedDevice(t, name)
op.Execute(ctx, dev)

verifyDev := testutil.LabConnectedDevice(t, name) // NEW connection
if !verifyDev.VLANExists(700) {
    t.Fatal("VLAN 700 not found")
}

// BAD: Same device has stale cache
dev := testutil.LabLockedDevice(t, name)
op.Execute(ctx, dev)
if !dev.VLANExists(700) { // Reads stale in-memory state
    t.Fatal("VLAN 700 not found") // False negative
}
```

---

## 6. Test Architecture

### Principle

Tests should be deterministic, isolated, and self-documenting.

### Test Structure

```go
func TestE2E_CreateVLAN(t *testing.T) {
    // 1. Guard (skip if infrastructure missing)
    testutil.SkipIfNoLab(t)
    testutil.Track(t, "VLAN", "leaf")

    // 2. Setup (get target, context, locked device)
    nodeName := leafNodeName(t)
    ctx := testutil.LabContext(t)
    dev := testutil.LabLockedDevice(t, nodeName)

    // 3. Execute (the operation under test)
    op := &operations.CreateVLANOp{ID: 500, Desc: "e2e-test"}
    if err := op.Validate(ctx, dev); err != nil {
        t.Fatalf("validate: %v", err)
    }
    if err := op.Execute(ctx, dev); err != nil {
        t.Fatalf("execute: %v", err)
    }

    // 4. Cleanup (BEFORE verification -- runs even if verify fails)
    t.Cleanup(func() {
        client := testutil.LabRedisClient(t, nodeName, 4)
        client.Del(context.Background(), "VLAN|Vlan500")
    })

    // 5. Verify (fresh connection, CONFIG_DB assertion)
    testutil.AssertConfigDBEntry(t, nodeName, "VLAN", "Vlan500",
        map[string]string{"vlanid": "500"})
}
```

### Cleanup Ordering

Register cleanup IMMEDIATELY after creation, in reverse dependency order:

```go
// Created: VRF → VLAN → VLAN_MEMBER → VNI_MAP → SVI
// Cleanup: SVI → VNI_MAP → VLAN_MEMBER → VLAN → VRF
// (Go t.Cleanup is LIFO, so register in creation order)

op1 := createVRF(...)
t.Cleanup(func() { deleteVRF(...) })       // runs LAST

op2 := createVLAN(...)
t.Cleanup(func() { deleteVLAN(...) })      // runs 4th

op3 := addMember(...)
t.Cleanup(func() { removeMember(...) })    // runs 3rd

op4 := mapVNI(...)
t.Cleanup(func() { unmapVNI(...) })        // runs 2nd

op5 := configureSVI(...)
t.Cleanup(func() { deleteSVI(...) })       // runs FIRST
```

### Resource Isolation

Each test uses unique resource IDs to prevent interference:

```go
// operations_test.go uses VLAN 500-509
// multidevice_test.go uses VLAN 600-609
// dataplane_test.go uses VLAN 700-809
```

---

## 7. Operational Safety

### Principle

Operations should be safe by default: validate before execute, lock before
write, audit after change.

### Validate-Execute Pattern

```go
// ALWAYS validate before execute
if err := op.Validate(ctx, dev); err != nil {
    return fmt.Errorf("precondition failed: %w", err)
}

// Validation checks (via PreconditionChecker):
// - Device is connected
// - Device is locked
// - Target interface/VLAN/VRF exists (or doesn't, for create)
// - No conflicting state (port not in LAG for VLAN add, etc.)
// - Required dependencies exist (VTEP for VNI mapping, etc.)

if err := op.Execute(ctx, dev); err != nil {
    return fmt.Errorf("execute failed: %w", err)
}
```

### Lock Discipline

```go
// GOOD: Lock, operate, unlock in deferred cleanup
dev := LabLockedDevice(t, name) // locks + registers cleanup
op.Execute(ctx, dev)
// Unlock happens automatically in t.Cleanup

// BAD: Manual lock/unlock with risk of leak
dev.Lock(ctx)
op.Execute(ctx, dev) // if this panics, lock is never released
dev.Unlock()
```

### Dry-Run by Default

The CLI defaults to dry-run mode. Changes only happen with `-x`:

```bash
newtron -d leaf1 -i Ethernet0 apply-service customer-l3   # Preview only
newtron -d leaf1 -i Ethernet0 apply-service customer-l3 -x # Execute
```

---

## 8. Debugging by Design

### Principle

Build observability into the system so debugging doesn't require special
tools.

### Structured Logging

```go
// Every operation logs its intent and outcome
log.WithFields(log.Fields{
    "device":    d.Name,
    "operation": "CreateVLAN",
    "vlan_id":   500,
}).Info("Creating VLAN")

// Error paths include context
log.WithFields(log.Fields{
    "device":    d.Name,
    "operation": "CreateVLAN",
    "vlan_id":   500,
    "error":     err,
}).Error("Failed to create VLAN")
```

### E2E Test Step Logging

```go
// Each step logs its purpose and outcome
t.Log("Step 1: Creating VLAN 700 on leaf1")
// ... operation ...
t.Log("Step 1: PASS - VLAN 700 created on leaf1")

// Failures log diagnostic context before asserting
t.Logf("Step 7: ASIC convergence failed on %s: %v", name, err)
t.Skip("ASIC convergence for IRB topology not supported on virtual switch")
```

### Test Report

The markdown report provides immediate visibility:

```markdown
| # | Test | Status | Duration | Node | Category |
|---|------|--------|----------|------|----------|
| 1 | ConnectAllNodes | PASS | 921ms | all | Connectivity |
| 6 | DataPlane_IRBSymmetric | SKIP | 16.2s | leaf1, leaf2 | Data Plane |
```

---

## 9. Configuration Discovery

### Principle

Automate the discovery of what tables and fields a feature uses, rather
than hardcoding assumptions.

### Discovery Pattern

```go
// Discover tables used by an operation by running it against a
// clean CONFIG_DB and diffing before/after:

func discoverTables(op Operation, dev *Device) []string {
    before := getAllKeys(dev, 4) // Snapshot
    op.Execute(ctx, dev)
    after := getAllKeys(dev, 4)  // Snapshot

    var newKeys []string
    for _, k := range after {
        if !contains(before, k) {
            newKeys = append(newKeys, k)
        }
    }
    return newKeys
}
```

### CONFIG_DB Monitoring

```bash
# Real-time monitoring during operation
redis-cli -n 4 MONITOR &
PID=$!

# Run the operation
newtron -d leaf1 -i Ethernet0 apply-service customer-l3 -x

# Stop monitoring
kill $PID

# Output shows every Redis command:
# "HSET" "VRF|Vrf-customer-l3-Ethernet0" "vrf_reg_mask" "0"
# "HSET" "INTERFACE|Ethernet0" "vrf_name" "Vrf-customer-l3-Ethernet0"
# ...
```

### Schema Validation

```go
// Validate CONFIG_DB entries match expected schema
func validateVLANEntry(t *testing.T, entry map[string]string) {
    required := []string{"vlanid"}
    optional := []string{"admin_status", "description", "mtu"}

    for _, field := range required {
        if _, ok := entry[field]; !ok {
            t.Errorf("missing required field: %s", field)
        }
    }
    for field := range entry {
        if !contains(append(required, optional...), field) {
            t.Logf("WARNING: unexpected field in VLAN entry: %s", field)
        }
    }
}
```

---

## 10. Anti-Patterns to Avoid

### 1. Assuming STATE_DB Reflects CONFIG_DB

```go
// BAD: reads STATE_DB which may not reflect CONFIG_DB on VS
mtu := dev.GetInterface("Ethernet0").MTU()
assert(mtu == 9000)

// GOOD: reads CONFIG_DB directly
entry := testutil.ReadEntry(t, addr, 4, "PORT", "Ethernet0")
assert(entry["mtu"] == "9000")
```

### 2. Suppressing Errors in Shell Scripts

```bash
# BAD: error is invisible
tc qdisc add dev eth1 clsact 2>/dev/null

# GOOD: capture and handle
if ! sudo /usr/sbin/tc qdisc add dev eth1 clsact 2>&1; then
    echo "WARNING: tc qdisc failed" >&2
fi
```

### 3. Assuming Binary Paths

```bash
# BAD: assumes tc is in PATH
tc filter add ...

# GOOD: explicit path, verified
TC_BIN="/usr/sbin/tc"
command -v "$TC_BIN" >/dev/null 2>&1 || { echo "tc not found at $TC_BIN"; exit 1; }
sudo "$TC_BIN" filter add ...
```

### 4. Using Same Device for Write and Verify

```go
// BAD: stale cache
dev.Execute(op)
dev.VLANExists(700) // reads in-memory cache, may be stale

// GOOD: fresh connection
dev.Execute(op)
fresh := testutil.LabConnectedDevice(t, name)
fresh.VLANExists(700) // reads current Redis state
```

### 5. Hardcoding Failure Modes

```go
// BAD: data-plane failure is always fatal
if !ping(server1, server2) {
    t.Fatal("ping failed")
}

// GOOD: platform-aware failure mode
if !ping(server1, server2) {
    t.Log("ping failed, collecting diagnostics...")
    logDiagnostics(t)
    t.Skip("data-plane not supported on virtual switch")
}
```

### 6. Not Cleaning Up on Failure

```go
// BAD: cleanup only happens on success path
createVLAN(700)
addMember(700, "Ethernet2")
mapVNI(700, 10700)
// cleanup here only runs if all three succeed

// GOOD: cleanup registered immediately after creation
createVLAN(700)
t.Cleanup(func() { deleteVLAN(700) })
addMember(700, "Ethernet2")
t.Cleanup(func() { removeMember(700, "Ethernet2") })
```

### 7. Assuming Port Forwarding

```go
// BAD: assumes Redis port is forwarded
addr = fmt.Sprintf("%s:6379", mgmtIP)

// GOOD: use SSH tunnel when available
if profile.SSHUser != "" {
    tunnel := NewSSHTunnel(mgmtIP, profile.SSHUser, profile.SSHPass)
    addr = tunnel.LocalAddr()
} else {
    addr = fmt.Sprintf("%s:6379", mgmtIP) // direct (integration tests)
}
```

### 8. Ignoring ASIC Convergence Time

```go
// BAD: verify immediately after write
mapVNI(700, 10700)
assert(asicHasVLAN(700)) // may not have converged yet

// GOOD: poll with timeout
mapVNI(700, 10700)
err := WaitForASICVLAN(ctx, t, name, 700) // polls every 500ms
```

---

## Summary of Principles

| # | Principle | One-Line Rule |
|---|---|---|
| 1 | Separation of Intent and State | CONFIG_DB is what you want; STATE_DB is what you have |
| 2 | Defensive Verification | Verify at the right layer; don't assume layers are linked |
| 3 | Platform Abstraction | Detect capabilities at runtime; adapt behavior |
| 4 | Graceful Degradation | Log diagnostics, then skip; never silently pass |
| 5 | Connection Resilience | Pool tunnels, reconnect on failure, fresh reads for verify |
| 6 | Test Architecture | Guard → Setup → Execute → Cleanup → Verify |
| 7 | Operational Safety | Validate → Lock → Execute → Audit |
| 8 | Debugging by Design | Structured logs, step annotations, markdown reports |
| 9 | Configuration Discovery | Monitor Redis, diff before/after, validate schema |
| 10 | Anti-Patterns | Don't trust STATE_DB, don't suppress errors, don't assume paths |
