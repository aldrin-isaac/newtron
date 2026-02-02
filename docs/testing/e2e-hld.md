# Newtron E2E Testing -- High-Level Design (HLD) v2

### What Changed in v2

| Area | Change |
|------|--------|
| **SSH Tunnel Pool** | Added tunnel pool architecture (labTunnels map, per-node reuse, CloseLabTunnels in TestMain) |
| **Redis Access Path** | Added complete packet path: test → SSH tunnel → 127.0.0.1:6379 inside QEMU VM; port 6379 NOT forwarded |
| **Three-Tier Assertions** | Added five test categories: control-plane (hard fail), ASIC convergence (topology-dependent), state-plane (soft), data-plane (soft), health-check (hard) |
| **ASIC Convergence** | Added WaitForASICVLAN polling mechanism with convergence behavior table by topology complexity |
| **Baseline Reset** | Added ResetLabBaseline mechanism: SSH to each node, redis-cli DEL of stale keys, orchagent settling |
| **LabSonicNodes** | Added LabSonicNodes vs LabNodes distinction — servers don't have Redis/SSH |
| **Design Decisions** | Added 10 numbered decisions with rationale (build tags, no test framework, Validate→Execute, fresh connections, cleanup, assertions, SSH tunnel, baseline reset, locking, LabSonicNodes) |
| **Network Addressing** | Added test subnet ranges (10.70.x for L2, 10.80.x for IRB, 10.90.x for L3) |
| **Failure Modes** | Added SSH tunnel failure, stale VXLAN/VRF key crashes, ASIC_DB non-convergence |

**Lines:** ~350 (v1) → 514 (v2) | All v1 sections preserved and expanded.

---

## 1. Purpose

This document describes the architecture and design of Newtron's end-to-end
(E2E) testing system. E2E tests validate that Newtron operations behave
correctly against live SONiC network devices running inside a containerlab
topology. The system covers control-plane configuration (CONFIG_DB writes),
ASIC convergence verification (ASIC_DB polling), state verification
(STATE_DB reads), multi-device orchestration, and data-plane connectivity.

## 2. Scope

| In scope | Out of scope |
|---|---|
| E2E tests against virtual SONiC devices | Unit tests (no build tag) |
| Lab lifecycle (start / stop / status) | Integration tests (`-tags integration`) |
| Lab artifact generation (labgen) | Production deployment procedures |
| Test utility library (`internal/testutil`) | CI/CD pipeline design |
| Data-plane verification via server containers | Performance / scale testing |
| SSH-tunneled Redis access | Hardware ASIC testing |
| Baseline reset and device locking | SONiC image builds |

## 3. System Context

```
+-----------------------------------------------------------------+
|                      Developer Workstation                       |
|                                                                  |
|  +----------+    +----------+    +---------------------------+   |
|  | make     |--->| labgen   |--->| testlab/.generated/       |   |
|  | lab-start|    | (Go CLI) |    |  +- spine-leaf.clab.yml   |   |
|  +----------+    +----------+    |  +- specs/profiles/*.json |   |
|       |                          |  +- */config_db.json      |   |
|       v                          +---------------------------+   |
|  +----------------------------------------------------------+    |
|  |                     containerlab                          |    |
|  |                                                           |    |
|  |  +--------------+  +--------------+  +---------+          |    |
|  |  | spine1       |  | spine2       |  | ...     | SONiC    |    |
|  |  | (vrnetlab)   |  | (vrnetlab)   |  |         | VMs      |    |
|  |  | QEMU+Redis   |  | QEMU+Redis   |  |         |          |    |
|  |  | SSH on :22   |  | SSH on :22   |  |         |          |    |
|  |  +--------------+  +--------------+  +---------+          |    |
|  |  +--------------+  +--------------+                       |    |
|  |  | leaf1        |  | leaf2        |  SONiC VMs            |    |
|  |  | (vrnetlab)   |  | (vrnetlab)   |  Redis on VM:6379     |    |
|  |  | SSH on :22   |  | SSH on :22   |  (NOT port-forwarded) |    |
|  |  +------+-------+  +------+-------+                       |    |
|  |         |                  |                               |    |
|  |  +------+-------+  +------+-------+                       |    |
|  |  | server1      |  | server2      |  netshoot containers  |    |
|  |  | (linux)      |  | (linux)      |                       |    |
|  |  +--------------+  +--------------+                       |    |
|  +----------------------------------------------------------+    |
|       |                                                          |
|       v                                                          |
|  +----------+                                                    |
|  | make     |  go test -tags e2e -v ./...                        |
|  | test-e2e |                                                    |
|  +----------+                                                    |
|       |                                                          |
|       v                                                          |
|  +----------------------------------------------------------+    |
|  | Test Process                                              |    |
|  |                                                           |    |
|  |  LabRedisClient("leaf1", 4)                               |    |
|  |     |                                                     |    |
|  |     v                                                     |    |
|  |  SSH tunnel pool (pkg/device/tunnel.go)                   |    |
|  |     | SSH to container_mgmt_IP:22                         |    |
|  |     | forward local_random_port -> VM 127.0.0.1:6379      |    |
|  |     v                                                     |    |
|  |  redis.NewClient(Addr: "127.0.0.1:<random>")             |    |
|  +----------------------------------------------------------+    |
+-----------------------------------------------------------------+
```

## 4. Key Components

### 4.1 Lab Generation (`cmd/labgen`)

The `labgen` CLI tool reads a topology YAML file and produces all artifacts
needed to deploy a containerlab topology:

- **Startup configs** -- per-node `config_db.json` files built by applying
  configlets with variable substitution.
- **Containerlab topology** -- `<name>.clab.yml` with node definitions, links,
  images, credentials, healthchecks, and volume mounts.
- **Newtron specifications** -- `specs/network.json`, `specs/site.json`,
  `specs/platforms.json`, and per-node `specs/profiles/<node>.json`.
- **FRR configs** -- per-node `frr.conf` files for BGP configuration.

### 4.2 Topology Definitions (`testlab/topologies/`)

Topologies are YAML files that declare nodes, roles, links, variables, and
which configlets to apply per role.

| Topology | Nodes | Links | Use Case |
|---|---|---|---|
| `spine-leaf` | 2 spines, 2 leaves, 2 servers | 6 (full mesh + server ports) | Full E2E suite |
| `minimal` | 1 spine, 1 leaf | 1 | Smoke tests, fast iteration |

### 4.3 Configlets (`configlets/`)

JSON templates merged into each node's `config_db.json` during generation.
Variables like `{{hostname}}`, `{{loopback_ip}}`, and `{{vtep_name}}` are
substituted from the topology file.

| Configlet | Purpose |
|---|---|
| `sonic-baseline` | Hostname, hwsku, platform, loopback, NTP, syslog, features |
| `sonic-evpn-spine` | BGP globals, address families, route reflector config |
| `sonic-evpn-leaf` | VXLAN tunnel, EVPN NVO, BGP neighbors, SAG MAC |
| `sonic-acl-copp` | ACL tables and CoPP policer rules |
| `sonic-qos-8q` | QoS queue profiles (8-queue model) |

### 4.4 Container Images

| Image | Type | Purpose |
|---|---|---|
| `vrnetlab/cisco_sonic:ngdp-202411` | vrnetlab (QEMU) | SONiC NG-DP virtual switch |
| `nicolaka/netshoot:latest` | Linux container | End-host simulation for data-plane tests |
| `redis:7-alpine` | Container | Standalone Redis for integration tests |

### 4.5 Lab Management (`testlab/setup.sh`)

Shell script providing the lab lifecycle. Called through Makefile targets.

```
lab-start [TOPO]
  +-> labgen (generate artifacts)
  +-> containerlab deploy
  +-> lab_wait_healthy (poll container status)
  +-> lab_wait_redis (SSH to each node, run redis-cli PING)
  +-> lab_apply_macs (restart swss for unique system MACs)
  +-> lab_push_frr (push FRR config via SCP + vtysh)
  +-> lab_bridge_nics (tc mirred redirect: ethN <-> swvethN)
  +-> lab_patch_profiles (inject mgmt IPs + SSH creds into profiles)

lab-stop
  +-> containerlab destroy --cleanup

lab-status
  +-> containerlab inspect + SSH-based Redis PING per node
```

**Redis wait uses SSH** -- The `lab_wait_redis` function does NOT connect
directly to port 6379. Instead it runs:

```bash
sshpass -p "$ssh_pass" ssh ... "$ssh_user@$ip" "redis-cli -n 4 PING"
```

This is because port 6379 is not forwarded by QEMU SLiRP networking
(see Section 9.7 below).

### 4.6 Test Utilities (`internal/testutil/`)

A Go library providing helpers for E2E and integration tests, organized
into source files separated by build tag:

| File | Build Tag | Responsibility |
|---|---|---|
| `testutil.go` | `integration \|\| e2e` | Redis connection, paths, contexts, skip guards |
| `redis.go` | `integration \|\| e2e` | Seed/flush/read/write Redis entries from JSON |
| `fixtures.go` | `integration \|\| e2e` | Device and network fixtures, assertion helpers |
| `lab.go` | `e2e` | Lab discovery, SSH tunnel pool, device locking, CONFIG_DB/ASIC_DB assertions, baseline reset, server helpers |
| `report.go` | `e2e` | E2E test report generation (pass/fail/skip/partial) |

#### 4.6.1 LabSonicNodes vs LabNodes

The test utilities distinguish between two node types:

- **`LabNodes(t)`** -- Returns ALL nodes from the containerlab topology,
  including server containers (netshoot). Used when you need to reference
  server containers for data-plane tests.

- **`LabSonicNodes(t)`** -- Returns only SONiC nodes that have a
  corresponding profile file in `specs/profiles/<name>.json`. Server
  containers are excluded because they do not run Redis and cannot be
  accessed via SSH tunnels. Used for all control-plane and state-plane tests.

The filtering is done by checking for the existence of the profile file:

```go
profilePath := filepath.Join(profilesDir, n.Name+".json")
if _, err := os.Stat(profilePath); err == nil {
    sonicNodes = append(sonicNodes, n)
}
```

### 4.7 E2E Test Suite (`test/e2e/`)

| File | Tests | Focus |
|---|---|---|
| `main_test.go` | 1 (TestMain) | Baseline reset, tunnel cleanup, report generation |
| `connectivity_test.go` | 4 | Basic reachability, startup config, loopback, interface enumeration |
| `operations_test.go` | ~20 | VLAN, LAG, Interface, ACL, EVPN/VRF, Service CRUD operations |
| `multidevice_test.go` | 3 | BGP state, cross-leaf VLAN, EVPN fabric health |
| `dataplane_test.go` | 3 | L2 bridged, IRB symmetric, L3 routed ping tests |

## 5. Test Taxonomy

Tests are classified by what they validate and how failures are reported.
This three-tier assertion strategy ensures that tests produce meaningful
results even when running on SONiC-VS, which has limited ASIC emulation.

### 5.1 Control-Plane Tests (hard fail)

Validate that operations write the correct entries to CONFIG_DB. A failure
means the operation produced wrong state. These tests always `t.Fatal` on
failure.

**Assertion:** `AssertConfigDBEntry()`, `AssertConfigDBEntryExists()`,
`AssertConfigDBEntryAbsent()` -- all read from CONFIG_DB (DB 4) via SSH
tunnel.

**Examples:** CreateVLAN, DeleteVLAN, AddVLANMember, CreateLAG, CreateVRF,
MapL2VNI, CreateACLTable, ApplyService.

### 5.2 ASIC Convergence Tests (topology-dependent)

Poll ASIC_DB (DB 1) for evidence that orchagent has programmed the
configuration into the simulated ASIC. Simple topologies (VLAN-only)
converge reliably on VS; complex topologies (IRB with VRF + SVI + VNI)
may not.

**Assertion:** `WaitForASICVLAN()` -- polls for `SAI_OBJECT_TYPE_VLAN`
entries with matching `SAI_VLAN_ATTR_VLAN_ID`.

| Topology | Converges on VS? | Recommended handling |
|---|---|---|
| Simple VLAN | Yes (< 5s) | Hard fail on timeout |
| VLAN + members | Yes (< 10s) | Hard fail on timeout |
| VRF + SVI + VNI (IRB) | Often not | Soft fail (`t.Skip`) |

### 5.3 State-Plane Tests (soft fail)

Poll STATE_DB for operational convergence (e.g., BGP session reaching
`Established`). Since SONiC-VS may not fully converge, failures produce
`t.Skip` rather than `t.Fatal`.

**Assertion:** `PollStateDB()` -- polls STATE_DB (DB 6) for a field
reaching an expected value.

**Examples:** BGPNeighborState.

### 5.4 Data-Plane Tests (soft fail)

Configure server containers with IP addresses and verify end-to-end ping
through the EVPN/VXLAN fabric. SONiC-VS with NGDP may or may not forward
VXLAN traffic depending on NIC bridging and ASIC convergence, so ping
failures produce `t.Skip` with diagnostic output.

**Examples:** L2Bridged, IRBSymmetric, L3Routed.

### 5.5 Health-Check Tests (hard fail)

Run the device health-check API and verify all checks pass.

**Examples:** EVPNFabricHealth.

## 6. Data Flow

### 6.1 TestMain Setup Flow

```
TestMain(m)
  |
  +- InitReport()              <- initialize test report tracking
  +- ResetLabBaseline()        <- delete stale CONFIG_DB keys from all nodes
  |    +- containerlab inspect <- discover nodes and IPs
  |    +- for each SONiC node:
  |         sshpass ssh <user>@<ip> "redis-cli -n 4 DEL '<key>' && ..."
  |    +- sleep 5s             <- allow orchagent to process deletions
  |
  +- m.Run()                   <- execute all test functions
  |
  +- CloseLabTunnels()         <- close all shared SSH tunnels
  +- WriteReport(path)         <- generate e2e-report.md
  +- os.Exit(code)
```

### 6.2 Test Execution Flow

```
go test -tags e2e
  |
  +- SkipIfNoLab()           <- verify lab is running
  +- Track(t, category, node) <- register test for report
  +- LabSonicNodes()         <- discover SONiC nodes (excludes servers)
  +- LabLockedDevice()       <- connect + acquire file lock
  |    +- LabConnectedDevice()
  |    |    +- LabNetwork()  <- load specs from .generated/specs/
  |    |    +- net.ConnectDevice(ctx, name)
  |    |         +- SSH tunnel for Redis (pkg/device/tunnel.go)
  |    +- dev.Lock(ctx)      <- file-system lock
  |
  +- op.Validate(ctx, dev)   <- pre-flight checks
  +- op.Execute(ctx, dev)    <- write CONFIG_DB entries (via SSH tunnel)
  |
  +- LabConnectedDevice()    <- fresh connection for verification
  |    +- dev.VLANExists()   <- read back from CONFIG_DB
  |
  +- AssertConfigDBEntry()   <- direct Redis assertion (via SSH tunnel)
  |    +- LabRedisClient()   <- get Redis client through SSH tunnel pool
  |
  +- t.Cleanup()             <- reverse changes via Redis DEL
```

### 6.3 Redis Access Path

Port 6379 is NOT forwarded by QEMU SLiRP networking. All Redis access
from the test process goes through SSH tunnels on port 22.

```
Test Process
  |
  +- labTunnelAddr(t, "leaf1", "172.17.0.3")
  |    +- labSSHConfig(t, "leaf1")         <- read ssh_user/ssh_pass from profile
  |    +- device.NewSSHTunnel("172.17.0.3", "cisco", "cisco123")
  |         +- ssh.Dial("tcp", "172.17.0.3:22", config)
  |         +- net.Listen("tcp", "127.0.0.1:0")   <- random local port
  |         +- go acceptLoop()                     <- forward connections
  |              +- sshClient.Dial("tcp", "127.0.0.1:6379")
  |
  +- redis.NewClient(Addr: "127.0.0.1:54321")     <- local tunnel endpoint
       |
       v
   SSH tunnel (port 22)
       |
       v
   Container mgmt IP:22
       |
       v
   QEMU hostfwd -> VM SSH (10.0.0.15:22)
       |
       v
   VM 127.0.0.1:6379 (redis-server inside SONiC)
```

**Shell script equivalent** (used by `setup.sh`):

```bash
sshpass -p "$ssh_pass" ssh -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
    "$ssh_user@$ip" "redis-cli -n 4 PING"
```

### 6.4 SSH Tunnel Pool

Tunnels are created on demand and shared across all tests for a given node.
The pool is protected by a mutex and stored in a package-level map:

```go
var (
    labTunnelsMu sync.Mutex
    labTunnels   map[string]*device.SSHTunnel  // node name -> tunnel
)
```

- First call to `labTunnelAddr("leaf1", ip)` creates the tunnel.
- Subsequent calls reuse the existing tunnel.
- `CloseLabTunnels()` is called from `TestMain` after `m.Run()` to close
  all tunnels cleanly.

## 7. Network Addressing

| Resource | Address Range |
|---|---|
| Loopback IPs (spines) | `10.0.0.1/32`, `10.0.0.2/32` |
| Loopback IPs (leaves) | `10.0.0.11/32`, `10.0.0.12/32` |
| Management IPs | Assigned by Docker bridge (172.x.x.x) |
| Test subnets (L2 bridged) | `10.70.0.0/24` |
| Test subnets (IRB) | `10.80.0.0/24` |
| Test subnets (L3 routed) | `10.90.1.0/30`, `10.90.2.0/30` |
| BGP AS number | 65000 |

## 8. Failure Modes and Recovery

| Failure | Impact | Recovery |
|---|---|---|
| VM fails to boot | `lab_wait_healthy` times out (5 min) | `make lab-stop && make lab-start` |
| Redis unreachable via SSH | `lab_wait_redis` times out (5 min) | Check VM health, restart lab |
| SSH tunnel creation fails | `LabRedisClient()` fatals the test | Verify SSH is up: `ssh cisco@<ip>` |
| Test leaves stale CONFIG_DB state | Subsequent tests may fail validation | `ResetLabBaseline()` runs in TestMain; worst case: restart lab |
| Stale VXLAN/VRF keys crash orchagent | vxlanmgrd/orchagent process crash | `ResetLabBaseline()` cleans known keys before test run |
| BGP does not converge | State-plane tests skip | Expected on SONiC-VS; not a test failure |
| VXLAN ping fails | Data-plane tests skip | Expected on SONiC-VS; not a test failure |
| ASIC_DB does not converge for IRB | ASIC convergence tests skip | Expected for complex topologies on VS |

## 9. Design Decisions

### 9.1 Go build tags over separate directories

E2E tests use `//go:build e2e` so they are excluded from `go test ./...`
by default. This prevents accidental execution without a lab.

### 9.2 No external test framework

Tests use Go's standard `testing` package with custom helpers in
`internal/testutil`. No dependency on testify or similar libraries.

### 9.3 Operations pattern (Validate -> Execute)

Every operation validates preconditions before modifying state. Tests
follow this two-step pattern consistently.

### 9.4 Fresh connections for verification

After executing an operation, tests create a new device connection to
read back state. This ensures verification is not reading cached data.

### 9.5 Cleanup via raw Redis

`t.Cleanup` functions delete CONFIG_DB entries directly via
`redis.Client.Del()` rather than using reverse operations. This is more
reliable and avoids cascading failures. Cleanup ordering follows reverse
dependency order (IP -> SVI -> VNI -> member -> VLAN -> VRF) to prevent
orchagent errors.

### 9.6 Three-tier assertion strategy

Tests distinguish between three tiers:

1. **CONFIG_DB** (always works) -- Hard fail. If an operation fails to
   write the expected CONFIG_DB entries, the test fails unconditionally.

2. **ASIC_DB** (topology-dependent) -- Soft fail for complex topologies.
   Simple VLAN operations converge in ASIC_DB on VS; complex IRB
   topologies (VRF + SVI + VNI) may not. Tests use `WaitForASICVLAN()`
   with appropriate timeout handling.

3. **Data-plane** (VS-limited) -- Always soft fail/skip. VXLAN forwarding
   depends on both ASIC convergence and NIC bridging. Ping failures log
   full diagnostics (ip addr, ip route, arp) and skip.

See [NGDP_DEBUGGING.md](../NGDP_DEBUGGING.md) for ASIC convergence
details by topology type.

### 9.7 SSH-tunneled Redis access

Port 6379 is NOT in the QEMU SLiRP hostfwd list. The vrnetlab base
class (`testlab/images/common/vrnetlab.py`) configures:

```python
self.mgmt_tcp_ports = [80, 443, 830, 6030, 8080, 9339, 32767, 50051, 57400]
```

Note: 6379 is absent. Port 22 (SSH) is handled separately by vrnetlab's
base configuration and IS forwarded.

All Redis access therefore uses SSH tunnels:

- **Go code:** `pkg/device/tunnel.go` creates an `SSHTunnel` that dials
  SSH on port 22 and forwards a random local TCP port to
  `127.0.0.1:6379` inside the SONiC VM.
- **Shell scripts:** `sshpass + ssh` to run `redis-cli` commands directly
  inside the VM over SSH.
- **Tunnels are pooled** per node and shared across tests via the
  `labTunnels` map in `lab.go`.

### 9.8 Baseline reset

`TestMain` calls `ResetLabBaseline()` before running any tests. This
function:

1. Discovers all SONiC nodes via `containerlab inspect`.
2. Reads SSH credentials from each node's patched profile.
3. SSH-es into each node in parallel.
4. Runs `redis-cli -n 4 DEL '<key>'` for every key in the `staleE2EKeys`
   list (VLAN, VRF, VXLAN_TUNNEL_MAP, ACL, LAG entries from all tests).
5. Sleeps 5 seconds to let orchagent/vxlanmgrd process the deletions.

This prevents orchagent crashes from conflicting ASIC_DB state left by
a previous test run.

### 9.9 Device locking

`LabLockedDevice()` acquires a file-system lock before modifying a
device. This prevents concurrent test processes from conflicting, though
`go test -p 1` serializes execution anyway. The lock is released via
`t.Cleanup`.

```go
dev := LabConnectedDevice(t, name)
ctx := LabContext(t)
if err := dev.Lock(ctx); err != nil {
    t.Fatalf("locking lab device %s: %v", name, err)
}
t.Cleanup(func() { dev.Unlock() })
```

### 9.10 LabSonicNodes vs LabNodes

Two discovery functions exist because the topology contains both SONiC
switches and Linux server containers:

- `LabNodes(t)` returns all nodes (including servers).
- `LabSonicNodes(t)` returns only nodes with profile files (SONiC
  switches).

Server containers do not run Redis, SSH, or any SONiC services. They are
only used for data-plane tests via `docker exec` commands.

## 10. Related Documentation

- [e2e-lld.md](e2e-lld.md) -- Low-Level Design (implementation details)
- [e2e-howto.md](e2e-howto.md) -- Practical guide for running and writing tests
- [NGDP_DEBUGGING.md](../NGDP_DEBUGGING.md) -- ASIC emulator internals
- [SONIC_VS_PITFALLS.md](../SONIC_VS_PITFALLS.md) -- VS limitations catalog
- [CONFIGDB_GUIDE.md](../CONFIGDB_GUIDE.md) -- CONFIG_DB schema reference
- [VERIFICATION_TOOLKIT.md](../VERIFICATION_TOOLKIT.md) -- Verification tools
