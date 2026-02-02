# E2E Testing HOWTO

End-to-end tests deploy a virtual SONiC fabric using containerlab and
exercise newtron operations against real SONiC devices running in
Docker/QEMU. Unlike integration tests (which use a plain Redis mock),
E2E tests verify the full stack: spec loading, device connection,
operations, and config_db writes on actual SONiC instances.

## Table of Contents

- [Architecture](#architecture)
- [Prerequisites](#prerequisites)
- [Quick Start](#quick-start)
- [Lab Lifecycle](#lab-lifecycle)
  - [Starting the Lab](#starting-the-lab)
  - [Checking Lab Status](#checking-lab-status)
  - [Stopping the Lab](#stopping-the-lab)
- [Running Tests](#running-tests)
  - [Run All E2E Tests](#run-all-e2e-tests)
  - [Run a Single Test](#run-a-single-test)
  - [Run a Category of Tests](#run-a-category-of-tests)
  - [Full Lifecycle (Start, Test, Stop)](#full-lifecycle)
- [Test Output and Reports](#test-output-and-reports)
  - [Real-Time Progress](#real-time-progress)
  - [Results File](#results-file)
  - [Verbose and Debug Output](#verbose-and-debug-output)
  - [JSON Output for CI](#json-output-for-ci)
- [Test Coverage](#test-coverage)
- [Data Plane Tests](#data-plane-tests)
- [Topology Definitions](#topology-definitions)
  - [spine-leaf (default)](#spine-leaf)
  - [minimal](#minimal)
  - [Writing a Custom Topology](#writing-a-custom-topology)
- [How It Works](#how-it-works)
  - [labgen: Artifact Generation](#labgen)
  - [Startup Sequence](#startup-sequence)
  - [SSH Tunnels (vrnetlab)](#ssh-tunnels)
  - [Management IP Patching](#management-ip-patching)
  - [Test Execution Flow](#test-execution-flow)
- [Test Helpers API](#test-helpers-api)
- [Writing New E2E Tests](#writing-new-e2e-tests)
- [Debugging Failures](#debugging-failures)
- [Environment Variables](#environment-variables)
- [Directory Layout](#directory-layout)

---

## Architecture

```
Topology YAML ──> labgen ──> containerlab YAML
(user-defined)      |         + per-node config_db.json
                    |         + newtron spec/profile files
                    v
              containerlab deploy
                    |
              SONiC containers (vrnetlab QEMU VMs)
                    |
              SSH tunnels expose Redis on :6379
                    |
              E2E tests connect via normal newtron path
                    |
              Results saved to testlab/.generated/e2e-results.txt
```

The `labgen` tool reads a newtron-native topology YAML, resolves configlet
templates using the existing `{{variable}}` pipeline, and generates all
artifacts needed for containerlab to deploy a working EVPN fabric.

Tests connect to each SONiC device through the standard `network.ConnectDevice`
path -- the same code path the CLI uses. Operations are executed via
`op.Validate()` followed by `op.Execute()`, and verification uses fresh
device connections to read back from Redis CONFIG_DB.

---

## Prerequisites

| Requirement | Purpose | Install |
|-------------|---------|---------|
| Docker | Run SONiC containers | [docs.docker.com](https://docs.docker.com/engine/install/) |
| containerlab | Orchestrate virtual topology | [containerlab.dev](https://containerlab.dev/install/) |
| redis-cli | Verify Redis connectivity | `apt install redis-tools` |
| python3 | Seed scripts, IP patching | Usually pre-installed |
| Go 1.21+ | Build labgen, run tests | [go.dev](https://go.dev/dl/) |
| sshpass | Automate SSH tunnel setup | `apt install sshpass` |
| SONiC image | The vrnetlab SONiC image | `vrnetlab/vr-sonic:202411` |

The SONiC vrnetlab image must be pre-built or pulled. The default topology
uses `vrnetlab/vr-sonic:202411`.

---

## Quick Start

```bash
# Start the lab, run all tests, stop the lab
make test-e2e-full

# Or step by step:
make lab-start          # deploy 2-spine + 2-leaf topology (~5 min)
make lab-status         # verify all nodes are up and Redis is reachable
make test-e2e           # run all 34 E2E tests (~7 min)
make lab-stop           # tear down
```

---

## Lab Lifecycle

### Starting the Lab

```bash
# Default topology (2-spine + 2-leaf)
make lab-start

# Specific topology
make lab-start TOPO=minimal
make lab-start TOPO=spine-leaf

# Direct script usage
./testlab/setup.sh lab-start spine-leaf
```

What happens during `lab-start`:

1. **Build labgen** -- compiles `cmd/labgen` to `testlab/.generated/labgen`
2. **Generate artifacts** -- creates per-node `config_db.json`, containerlab
   YAML, and newtron spec files in `testlab/.generated/`
3. **Deploy containers** -- runs `containerlab deploy` which starts Docker
   containers running SONiC inside QEMU VMs (vrnetlab)
4. **Wait for healthy** -- polls `containerlab inspect` until all containers
   leave "starting" state (up to 5 minutes)
5. **Set up SSH tunnels** -- for vrnetlab nodes, creates SSH port-forwards
   inside each container so Redis (port 6379) is reachable from the host
6. **Wait for Redis** -- pings CONFIG_DB on each node's management IP
   (up to 5 minutes, polling every 5 seconds)
7. **Patch profiles** -- discovers actual management IPs from
   `containerlab inspect` and writes them into the generated newtron
   profile files (replacing the `PLACEHOLDER` value)

The lab is ready when you see:

```
=== Lab spine-leaf is ready ===
```

### Checking Lab Status

```bash
make lab-status
```

Output shows:
- Which topology is running
- Container status from `containerlab inspect` (name, image, state, IPs)
- Redis connectivity per node (OK or UNREACHABLE)

Example output:
```
=== Containerlab Status ===
Topology: spine-leaf

+---+--------------------------+-----------+----------------------------+-------+---------+-------------------+
| # |          Name            |   Kind    |          Image             | State |  IPv4   |       IPv6        |
+---+--------------------------+-----------+----------------------------+-------+---------+-------------------+
| 1 | clab-spine-leaf-spine1   | sonic-vm  | vrnetlab/vr-sonic:202411   | running | 172.20.20.2/24 | ... |
| 2 | clab-spine-leaf-spine2   | sonic-vm  | vrnetlab/vr-sonic:202411   | running | 172.20.20.3/24 | ... |
| 3 | clab-spine-leaf-leaf1    | sonic-vm  | vrnetlab/vr-sonic:202411   | running | 172.20.20.4/24 | ... |
| 4 | clab-spine-leaf-leaf2    | sonic-vm  | vrnetlab/vr-sonic:202411   | running | 172.20.20.5/24 | ... |
+---+--------------------------+-----------+----------------------------+-------+---------+-------------------+

Redis connectivity:
  clab-spine-leaf-spine1: 172.20.20.2:6379 OK
  clab-spine-leaf-spine2: 172.20.20.3:6379 OK
  clab-spine-leaf-leaf1: 172.20.20.4:6379 OK
  clab-spine-leaf-leaf2: 172.20.20.5:6379 OK
```

### Stopping the Lab

```bash
make lab-stop

# Or directly:
./testlab/setup.sh lab-stop
```

This runs `containerlab destroy --cleanup` and removes the `.lab-state` file.

To also remove all generated artifacts:

```bash
make clean
```

---

## Running Tests

### Run All E2E Tests

```bash
# Requires a running lab (make lab-start)
make test-e2e
```

This runs all tests in `test/e2e/` with the `e2e` build tag. If no lab is
running, every test gracefully skips with:

```
--- SKIP: TestE2E_CreateVLAN (0.00s)
    lab.go:27: no lab topology running: run 'make lab-start' first
```

### Run a Single Test

```bash
go test -tags e2e -v -count=1 -timeout 10m ./test/e2e/ -run TestE2E_CreateVLAN
```

### Run a Category of Tests

```bash
# All VLAN tests
go test -tags e2e -v -count=1 -timeout 10m ./test/e2e/ -run 'TestE2E_.*VLAN'

# All ACL tests
go test -tags e2e -v -count=1 -timeout 10m ./test/e2e/ -run 'TestE2E_.*ACL'

# All LAG tests
go test -tags e2e -v -count=1 -timeout 10m ./test/e2e/ -run 'TestE2E_.*LAG'

# All EVPN tests (VRF, VTEP, VNI)
go test -tags e2e -v -count=1 -timeout 10m ./test/e2e/ -run 'TestE2E_.*(VRF|VTEP|VNI)'

# All connectivity tests
go test -tags e2e -v -count=1 -timeout 10m ./test/e2e/ -run 'TestE2E_(Connect|Verify|List)'

# All multi-device tests
go test -tags e2e -v -count=1 -timeout 10m ./test/e2e/ -run 'TestE2E_(BGP|VLANAcross|EVPNFabric)'

# All data plane tests
go test -tags e2e -v -count=1 -timeout 10m ./test/e2e/ -run 'TestE2E_DataPlane'
```

### Full Lifecycle

```bash
# Start lab, run tests, stop lab (even if tests fail)
make test-e2e-full

# With a specific topology
TOPO=minimal make test-e2e-full
```

---

## Test Output and Reports

### Real-Time Progress

The `-v` flag (included in all Makefile targets) shows each test as it runs:

```
=== RUN   TestE2E_ConnectAllNodes
=== RUN   TestE2E_ConnectAllNodes/spine1
=== RUN   TestE2E_ConnectAllNodes/leaf1
=== RUN   TestE2E_ConnectAllNodes/leaf2
--- PASS: TestE2E_ConnectAllNodes (4.21s)
    --- PASS: TestE2E_ConnectAllNodes/spine1 (1.23s)
    --- PASS: TestE2E_ConnectAllNodes/leaf1 (1.34s)
    --- PASS: TestE2E_ConnectAllNodes/leaf2 (1.41s)
=== RUN   TestE2E_CreateVLAN
--- PASS: TestE2E_CreateVLAN (12.45s)
...
```

Each test shows `RUN` when it starts and `PASS`/`FAIL`/`SKIP` when it
finishes, so you can monitor progress in real time.

### Results File

Both `make test-e2e` and `make test-e2e-full` save output to:

```
testlab/.generated/e2e-results.txt
```

This file contains the complete verbose test output. View it with:

```bash
cat testlab/.generated/e2e-results.txt

# Just the summary line
tail -3 testlab/.generated/e2e-results.txt

# Just failures
grep -A5 '--- FAIL' testlab/.generated/e2e-results.txt
```

### Markdown Report

A structured markdown report is generated at:

```
testlab/.generated/e2e-report.md
```

This contains a summary table (pass/fail/skip counts) and per-test results
with duration, target node, and category. Generated by `testutil.InitReport()`
and `testutil.WriteReport()` in `TestMain`.

```bash
cat testlab/.generated/e2e-report.md

# Just skips
grep '--- SKIP' testlab/.generated/e2e-results.txt

# Count pass/fail/skip
grep -c '--- PASS' testlab/.generated/e2e-results.txt
grep -c '--- FAIL' testlab/.generated/e2e-results.txt
grep -c '--- SKIP' testlab/.generated/e2e-results.txt
```

The results file is overwritten on each run. It is gitignored (inside
`.generated/`).

### Markdown Report

After every run, a structured markdown report is written to:

```
testlab/.generated/e2e-report.md
```

The report contains a summary table (pass/fail/skip/partial counts, topology,
duration) followed by a per-test results table with status, duration, node,
category, and comments. Tests with mixed subtest outcomes (e.g., some subtests
pass while others skip) are marked **PARTIAL** with the non-passing subtests
listed in the Comments column.

View it with:

```bash
cat testlab/.generated/e2e-report.md

# Or render it (if you have a markdown viewer)
glow testlab/.generated/e2e-report.md
```

The report is generated by `testutil.WriteReport()` called from `TestMain` in
`test/e2e/main_test.go`. Each test registers itself via `testutil.Track()`,
and outcomes are captured automatically through `t.Cleanup()` hooks.

### Verbose and Debug Output

**Default** -- `make test-e2e` already uses `-v` which shows test names and
logged messages. Many tests log intermediate state:

```
=== RUN   TestE2E_EVPNFabricHealth/leaf1
    multidevice_test.go:149: leaf1: BGP configured with 2 neighbors
    multidevice_test.go:157: leaf1: VTEP exists (source IP: 10.0.0.11)
    multidevice_test.go:165: leaf1: [pass] bgp_sessions: 2/2 neighbors Established
```

**Extra verbosity** -- For debugging a specific test, run it directly and
the test output goes straight to your terminal:

```bash
go test -tags e2e -v -count=1 -timeout 10m ./test/e2e/ -run TestE2E_CreateVRF
```

**Redis inspection** -- To see what's actually in CONFIG_DB on a node:

```bash
# Get the node's management IP
make lab-status

# Dump all keys
redis-cli -h 172.20.20.4 -n 4 KEYS '*'

# Read a specific entry
redis-cli -h 172.20.20.4 -n 4 HGETALL 'VLAN|Vlan500'

# Check if a key exists
redis-cli -h 172.20.20.4 -n 4 EXISTS 'VRF|Vrf_e2e_test'
```

**Container logs** -- To see SONiC boot logs or SSH tunnel status:

```bash
docker logs clab-spine-leaf-leaf1
docker logs clab-spine-leaf-leaf1 2>&1 | tail -50
```

### JSON Output for CI

For structured output suitable for CI parsing:

```bash
go test -tags e2e -v -count=1 -timeout 10m -json ./test/e2e/ > e2e-results.json
```

Each line is a JSON event:

```json
{"Time":"...","Action":"run","Package":"...","Test":"TestE2E_CreateVLAN"}
{"Time":"...","Action":"pass","Package":"...","Test":"TestE2E_CreateVLAN","Elapsed":12.45}
```

Tools like `gotestsum` can produce richer reports from this format:

```bash
gotestsum --format testname -- -tags e2e -count=1 -timeout 10m ./test/e2e/
```

---

## Test Coverage

### Summary: 34 tests across 4 files

| File | Tests | Category |
|------|-------|----------|
| `connectivity_test.go` | 4 | Basic connectivity and startup config |
| `operations_test.go` | 24 | All newtron operations |
| `multidevice_test.go` | 3 | Cross-device and fabric-wide checks |
| `dataplane_test.go` | 3 | Server-to-server data-plane connectivity |

### Connectivity Tests (`connectivity_test.go`)

| Test | What It Verifies |
|------|-----------------|
| `TestE2E_ConnectAllNodes` | Connects to every node, checks `IsConnected()` |
| `TestE2E_VerifyStartupConfig` | `DEVICE_METADATA` hostname matches node name |
| `TestE2E_VerifyLoopbackInterface` | `LOOPBACK_INTERFACE` has Loopback0 |
| `TestE2E_ListInterfaces` | Lists all interfaces, calls `GetInterface()` on each |

### Operations Tests (`operations_test.go`)

| Test | Operation | Target |
|------|-----------|--------|
| `TestE2E_CreateVLAN` | `CreateVLANOp` | leaf |
| `TestE2E_DeleteVLAN` | `DeleteVLANOp` | leaf |
| `TestE2E_AddVLANMember` | `AddVLANMemberOp` | leaf |
| `TestE2E_RemoveVLANMember` | `RemoveVLANMemberOp` | leaf |
| `TestE2E_ConfigureSVI` | `ConfigureSVIOp` | leaf |
| `TestE2E_CreateLAG` | `CreateLAGOp` | leaf |
| `TestE2E_DeleteLAG` | `DeleteLAGOp` | leaf |
| `TestE2E_AddLAGMember` | `AddLAGMemberOp` | leaf |
| `TestE2E_RemoveLAGMember` | `RemoveLAGMemberOp` | leaf |
| `TestE2E_ConfigureInterface` | `ConfigureInterfaceOp` | leaf |
| `TestE2E_SetInterfaceVRF` | `SetInterfaceVRFOp` | leaf |
| `TestE2E_SetInterfaceIP` | `SetInterfaceIPOp` | leaf |
| `TestE2E_BindACL` | `BindACLOp` | leaf |
| `TestE2E_CreateACLTable` | `CreateACLTableOp` | leaf |
| `TestE2E_AddACLRule` | `AddACLRuleOp` | leaf |
| `TestE2E_DeleteACLRule` | `DeleteACLRuleOp` | leaf |
| `TestE2E_DeleteACLTable` | `DeleteACLTableOp` | leaf |
| `TestE2E_CreateVRF` | `CreateVRFOp` | leaf |
| `TestE2E_DeleteVRF` | `DeleteVRFOp` | leaf |
| `TestE2E_CreateVTEP` | `CreateVTEPOp` | spine |
| `TestE2E_MapL2VNI` | `MapL2VNIOp` | leaf |
| `TestE2E_UnmapL2VNI` | `UnmapL2VNIOp` | leaf |
| `TestE2E_ApplyService` | `ApplyServiceOp` | leaf |
| `TestE2E_RemoveService` | `RemoveServiceOp` | leaf |

### Multi-Device Tests (`multidevice_test.go`)

| Test | What It Verifies |
|------|-----------------|
| `TestE2E_BGPNeighborState` | Polls STATE_DB for BGP sessions reaching Established |
| `TestE2E_VLANAcrossTwoLeaves` | Creates VLAN 600 on both leaves, verifies on both |
| `TestE2E_EVPNFabricHealth` | Runs health checks on all nodes, verifies none fail |

### Data Plane Tests (`dataplane_test.go`)

| Test | What It Verifies |
|------|-----------------|
| `TestE2E_DataPlane_L2Bridged` | L2 bridged EVPN/VXLAN: VLAN 700 + L2VNI 10700 across two leaves, ping between servers |
| `TestE2E_DataPlane_IRBSymmetric` | IRB symmetric EVPN: VRF + VLAN 800 + L2VNI + anycast gateway SVI, server-to-server routing |
| `TestE2E_DataPlane_L3Routed` | L3 routed EVPN: VRF + per-leaf /30 subnets on Ethernet8, inter-subnet routing |

---

## Data Plane Tests

Data-plane tests exercise server containers connected to leaf switches. They
configure L2, IRB, and L3 services on the SONiC leaves via the operations
API, then verify both control-plane state (CONFIG_DB entries) and data-plane
connectivity (ping between servers via `docker exec`).

### Topology

```
server1 (eth1) ─── leaf1:Ethernet8 ─── spine1/spine2 ─── leaf2:Ethernet8 ─── server2 (eth1)
```

Servers are `ubuntu:22.04` containers. Container names follow the
containerlab convention: `clab-<topoName>-server1`, `clab-<topoName>-server2`.

### SONiC-VS Data-Plane Limitation

SONiC-VS is a **control-plane-only** simulator. The ASIC emulator (`ngdpd`)
programs ASIC_DB but does NOT forward data-plane packets. Tests use a
three-tier assertion strategy:

- **CONFIG_DB** = hard fail (`t.Fatal`) -- writes and reads must always work
- **ASIC_DB convergence** = topology-dependent -- simple VLANs hard-fail,
  complex IRB topologies soft-fail (`t.Skip`) because orchagent may not fully
  process the dependency chain on VS
- **Data-plane ping** = soft fail (`t.Log` + `t.Skip`) -- ping never works
  on VS due to ngdpd not forwarding packets

See [NGDP_DEBUGGING.md](NGDP_DEBUGGING.md) for comprehensive ASIC emulator
debugging guidance and [SONIC_VS_PITFALLS.md](SONIC_VS_PITFALLS.md) for the
full catalog of VS limitations.

### Running Data Plane Tests

```bash
# All data plane tests
go test -tags e2e -v -count=1 -timeout 10m ./test/e2e/ -run 'TestE2E_DataPlane'

# Individual tests
go test -tags e2e -v -count=1 -timeout 10m ./test/e2e/ -run TestE2E_DataPlane_L2Bridged
go test -tags e2e -v -count=1 -timeout 10m ./test/e2e/ -run TestE2E_DataPlane_IRBSymmetric
go test -tags e2e -v -count=1 -timeout 10m ./test/e2e/ -run TestE2E_DataPlane_L3Routed
```

### Test Details

#### `TestE2E_DataPlane_L2Bridged`

Creates matching VLAN 700 + L2VNI 10700 on both leaf switches with
server-facing ports (Ethernet8) as untagged members. Verifies CONFIG_DB
entries, then tests ping between servers on the same subnet (10.70.0.0/24).

| Step | Operation | Fail Mode |
|------|-----------|-----------|
| 1 | CreateVLAN(700) on leaf1, leaf2 | Hard |
| 2 | AddVLANMember(Ethernet8, 700, untagged) on both | Hard |
| 3 | MapL2VNI(700 → 10700) on both | Hard |
| 4-6 | Verify CONFIG_DB entries (VLAN, VLAN_MEMBER, VXLAN_TUNNEL_MAP) | Hard |
| 7 | Wait for ASIC_DB convergence (VLAN 700 in ASIC_DB on both leaves, 30s timeout) | Hard |
| 8 | Configure server1=10.70.0.1/24, server2=10.70.0.2/24 | Hard |
| 9 | Ping between servers (5 packets) | Soft |

#### `TestE2E_DataPlane_IRBSymmetric`

Creates a VRF, VLAN 800 with L2VNI 10800, and an SVI with anycast gateway
(10.80.0.1/24) in the VRF on both leaves. Verifies servers can communicate
through the anycast gateway.

| Step | Operation | Fail Mode |
|------|-----------|-----------|
| 1 | CreateVRF("Vrf_e2e_irb") on both | Hard |
| 2 | CreateVLAN(800) on both | Hard |
| 3 | AddVLANMember(Ethernet8, 800, untagged) on both | Hard |
| 4 | MapL2VNI(800 → 10800) on both | Hard |
| 5 | ConfigureSVI(Vlan800, VRF, IP, AnycastGW) on both | Hard |
| 6-9 | Verify CONFIG_DB (VRF, VLAN_INTERFACE, VXLAN_TUNNEL_MAP) | Hard |
| 10 | Wait for ASIC_DB convergence (VLAN 800 in ASIC_DB, 30s timeout) | **Soft** |
| 11-12 | Configure server1=10.80.0.10/24, server2=10.80.0.20/24 (gw 10.80.0.1) | Hard |
| 13-14 | Ping gateway from each server (3 packets) | Soft |
| 15-16 | Ping between servers (5 packets) | Soft |

**Note on step 10:** IRB topologies (VRF + SVI + VNI) create a deep ASIC_DB
dependency chain that orchagent may not fully process on VS. The ASIC
convergence step uses soft-fail (`t.Skip`) instead of hard-fail. See
[NGDP_DEBUGGING.md](NGDP_DEBUGGING.md) Section 11 for convergence timeout
details by topology type.

#### `TestE2E_DataPlane_L3Routed`

Creates a VRF on both leaves, binds Ethernet8 directly to the VRF with
different /30 subnets, and verifies inter-subnet routing.

| Step | Operation | Fail Mode |
|------|-----------|-----------|
| 1 | CreateVRF("Vrf_e2e_l3") on both | Hard |
| 2 | SetInterfaceVRF(Ethernet8, Vrf_e2e_l3, 10.90.1.1/30) on leaf1 | Hard |
| 3 | SetInterfaceVRF(Ethernet8, Vrf_e2e_l3, 10.90.2.1/30) on leaf2 | Hard |
| 4-7 | Verify CONFIG_DB (VRF, INTERFACE entries) | Hard |
| 8-9 | Configure server1=10.90.1.2/30, server2=10.90.2.2/30 | Hard |
| 10-11 | Ping local gateway from each server (3 packets) | Soft |
| 12-13 | Ping between servers (5 packets) | Soft |

### Server Test Helpers

Server helper functions are in `internal/testutil/lab.go`:

| Function | Description |
|----------|-------------|
| `LabServerNode(t, name)` | Returns LabNode for a server container |
| `ServerExec(t, serverName, args...)` | Runs command on server via `docker exec` |
| `EnsureServerTools(t, serverName)` | Installs `iputils-ping` + `iproute2` (once per server) |
| `ServerConfigureInterface(t, serverName, iface, ipCIDR, gw)` | Configures IP and gateway, registers cleanup |
| `ServerPing(t, serverName, targetIP, count)` | Pings target, returns bool, logs diagnostics on failure |
| `ServerCleanupInterface(t, serverName, iface)` | Flushes IP addresses from interface |

---

## Topology Definitions

### spine-leaf

File: `testlab/topologies/spine-leaf.yml`

The default topology: 2 spines + 2 leaves forming a full EVPN mesh.

```
          spine1 -------- spine2
          /    \          /    \
       leaf1    leaf2  leaf1   leaf2
```

- 4 nodes: spine1, spine2, leaf1, leaf2
- 4 links: each spine connects to each leaf
- BGP AS 65000
- Spines: route reflectors with `sonic-baseline` + `sonic-evpn-spine` configlets
- Leaves: `sonic-baseline` + `sonic-evpn-leaf` + `sonic-acl-copp` + `sonic-qos-8q`
- Each leaf has a VTEP configured for VXLAN/EVPN

### minimal

File: `testlab/topologies/minimal.yml`

A 1-spine + 1-leaf topology for quick testing or development:

- 2 nodes: spine1, leaf1
- 1 link: spine1:Ethernet0 -- leaf1:Ethernet0
- Same configlets as spine-leaf

### Writing a Custom Topology

Create a YAML file in `testlab/topologies/`:

```yaml
name: my-topology                        # must match filename (without .yml)

defaults:
  image: vrnetlab/vr-sonic:202411        # SONiC Docker image
  username: cisco                        # SSH credentials (for vrnetlab)
  password: cisco123
  platform: vs-platform                  # newtron platform name
  site: lab-site                         # newtron site name
  hwsku: "Force10-S6000"                 # SONiC hardware SKU
  ntp_server_1: "10.100.0.1"
  ntp_server_2: "10.100.0.2"
  syslog_server: "10.100.0.3"

network:
  as_number: 65000                       # BGP AS for the fabric
  region: lab-region                     # newtron region name

nodes:
  spine1:
    role: spine                          # must be "spine" or "leaf"
    loopback_ip: "10.0.0.1"             # router ID (required)
    variables:                           # template variables for configlets
      cluster_id: "10.0.0.1"
  leaf1:
    role: leaf
    loopback_ip: "10.0.0.11"
    variables:
      vtep_name: vtep1
      spine1_ip: "10.0.0.1"

links:
  - endpoints: ["spine1:Ethernet0", "leaf1:Ethernet0"]  # SONiC interface names

role_defaults:                           # configlets applied per role
  spine:
    - sonic-baseline
    - sonic-evpn-spine
  leaf:
    - sonic-baseline
    - sonic-evpn-leaf
```

Then deploy it:

```bash
make lab-start TOPO=my-topology
```

**Validation rules**:
- `name` must match the YAML filename
- Every node needs `role` (spine/leaf) and `loopback_ip` (valid IPv4)
- Link endpoints must reference nodes defined in `nodes`
- Endpoint format: `"nodeName:EthernetN"` (SONiC naming)

---

## How It Works

### labgen

The `labgen` tool (`cmd/labgen/main.go`) generates three categories of
artifacts from a topology YAML:

1. **Per-node `config_db.json`** -- Merges configlets (from `configlets/`
   directory) with resolved template variables. Variables include
   `device_name`, `loopback_ip`, `router_id`, `as_number`, `hwsku`, plus
   any custom variables defined in the topology. Ensures at least 8
   Ethernet ports are defined for test flexibility.

2. **Containerlab YAML** -- Maps SONiC interface names to containerlab names
   (Ethernet0 -> eth1, Ethernet4 -> eth2, sequential). Sets `kind: sonic-vm`
   for vrnetlab images, includes SSH credentials as environment variables,
   and references the startup config.

3. **Newtron spec files** -- Generates `network.json` (with services, filters,
   VPN definitions), `site.json` (with spine nodes as route reflectors),
   `platforms.json`, and per-node profiles with `PLACEHOLDER` management IPs.

Build and run manually:

```bash
go build -o testlab/.generated/labgen ./cmd/labgen/
./testlab/.generated/labgen \
    -topology testlab/topologies/spine-leaf.yml \
    -output testlab/.generated \
    -configlets ./configlets
```

### Startup Sequence

The full `lab-start` sequence in `setup.sh`:

```
labgen -topology <file> -output <dir>
  |
  v
containerlab deploy -t <generated>.clab.yml --reconfigure
  |
  v
Poll containerlab inspect until no containers are "starting" (5 min timeout)
  |
  v
For each sonic-vm container:
  docker exec -d <name> sshpass -p <pass> ssh -N -L 0.0.0.0:6379:localhost:6379 <user>@127.0.0.1
  |
  v
For each node:
  Poll redis-cli -h <ip> -n 4 PING until PONG (5 min timeout)
  |
  v
For each node:
  Patch specs/profiles/<node>.json with actual management IP from containerlab inspect
  |
  v
Lab ready
```

### SSH Tunnels

vrnetlab runs SONiC inside a QEMU VM within a Docker container. QEMU uses
SLiRP user-mode networking, which only forwards explicitly listed TCP ports.
Port 6379 (Redis) is **not** forwarded; port 22 (SSH) **is** forwarded.
Therefore, Redis must be accessed through SSH tunnels.

**E2E test code** uses Go-native SSH tunnels (`pkg/device/tunnel.go`). Each
test helper that needs Redis calls `labTunnelAddr()`, which establishes an
SSH tunnel to the node and returns a `127.0.0.1:<random_port>` address.
Tunnels are pooled per node and reused across tests:

```
Test code  →  127.0.0.1:<random>  →  SSH tunnel  →  127.0.0.1:6379 inside VM
              (local listener)       (via port 22)    (Redis)
```

The tunnel pool is cleaned up by `CloseLabTunnels()` in `TestMain`.

**Setup script** (`setup.sh`) uses `sshpass + ssh` for ad-hoc Redis checks
during lab bring-up:

```bash
sshpass -p cisco123 ssh -o StrictHostKeyChecking=no \
    cisco@<mgmt_ip> "redis-cli -n 4 PING" < /dev/null
```

**Important:** SSH commands in shell scripts must use `< /dev/null` to prevent
consuming stdin from the calling script's read loop. Without this, subsequent
loop iterations silently lose their input.

### Management IP Patching

When labgen generates profile files, it doesn't know what IPs containerlab
will assign, so it writes `"mgmt_ip": "PLACEHOLDER"`. After deployment,
`lab_patch_profiles` in `setup.sh`:

1. Runs `containerlab inspect --format json` to get container names and IPs
2. Strips the `clab-<topo>-` prefix to get the node name
3. Updates each `specs/profiles/<node>.json` with the real IP

### Test Execution Flow

Each E2E test follows this pattern:

```
1. SkipIfNoLab(t)          -- skip if no topology running
2. LabContext(t)            -- 2-minute timeout context
3. LabLockedDevice(t, name) -- connect + lock for writes
4. op.Validate(ctx, dev)    -- precondition checks
5. op.Execute(ctx, dev)     -- apply changes to CONFIG_DB
6. Register cleanup         -- undo changes on test completion
7. LabConnectedDevice()     -- fresh connection for verification
8. Assert expected state    -- query device API or raw Redis
```

The fresh connection in step 7 is important: after `Execute()` writes to
Redis, the original device object has a stale in-memory cache. A new
connection reads the current CONFIG_DB state from Redis.

---

## Test Helpers API

All helpers are in `internal/testutil/lab.go` (build tag: `e2e`).

### Skip and Discovery

| Function | Returns | Description |
|----------|---------|-------------|
| `SkipIfNoLab(t)` | -- | Skip test if no topology is running |
| `LabTopologyName()` | `string` | Running topology name (from env or `.lab-state`) |
| `LabNodes(t)` | `[]LabNode` | All nodes with names and IPs |
| `LabNodeNames(t)` | `[]string` | Just the node names |
| `LabNodeIP(t, name)` | `string` | Management IP for a named node |
| `LabSpecsDir(t)` | `string` | Path to generated specs directory |

### Device Connection

| Function | Returns | Description |
|----------|---------|-------------|
| `LabNetwork(t)` | `*network.Network` | Network loaded from generated specs |
| `LabConnectedDevice(t, name)` | `*network.Device` | Connected device (fatal on error, auto-disconnect) |
| `TryLabConnectedDevice(t, name)` | `(*network.Device, error)` | Connected device (returns error, doesn't fatal) |
| `LabLockedDevice(t, name)` | `*network.Device` | Connected + locked device (auto-unlock) |
| `LabContext(t)` | `context.Context` | 2-minute timeout context (auto-cancel) |

### Redis and Assertions

| Function | Returns | Description |
|----------|---------|-------------|
| `LabRedisClient(t, name, db)` | `*redis.Client` | Raw Redis client for any DB (auto-close, uses SSH tunnel) |
| `AssertConfigDBEntry(t, name, table, key, fields)` | -- | Verify hash fields match expected values |
| `AssertConfigDBEntryExists(t, name, table, key)` | -- | Verify key exists in CONFIG_DB |
| `AssertConfigDBEntryAbsent(t, name, table, key)` | -- | Verify key does NOT exist in CONFIG_DB |
| `LabStateDBEntry(t, name, table, key)` | `map[string]string` | Read a STATE_DB hash |
| `PollStateDB(ctx, t, name, table, key, field, want)` | `error` | Poll STATE_DB until field matches value |
| `WaitForLabRedis(t, timeout)` | -- | Wait for Redis on all SONiC nodes (via SSH tunnel) |

### ASIC Convergence

| Function | Returns | Description |
|----------|---------|-------------|
| `WaitForASICVLAN(ctx, t, name, vlanID)` | `error` | Poll ASIC_DB (DB 1) until `SAI_OBJECT_TYPE_VLAN` with matching `SAI_VLAN_ATTR_VLAN_ID` appears. Use 30s timeout for simple VLANs (hard fail), 30s with soft fail for IRB topologies. |

### Cleanup and Lifecycle

| Function | Description |
|----------|-------------|
| `LabCleanupChanges(t, name, fn)` | Register cleanup that creates a fresh locked device and applies a changeset to undo test changes |
| `ResetLabBaseline()` | Delete stale CONFIG_DB keys from all SONiC nodes before the test suite runs (called from `TestMain`) |
| `CloseLabTunnels()` | Close all shared SSH tunnels after the test suite completes (called from `TestMain`) |

---

## Writing New E2E Tests

### Template

```go
//go:build e2e

package e2e_test

import (
    "context"
    "testing"

    "github.com/newtron-network/newtron/internal/testutil"
    "github.com/newtron-network/newtron/pkg/network"
    "github.com/newtron-network/newtron/pkg/operations"
)

func TestE2E_MyNewOperation(t *testing.T) {
    testutil.SkipIfNoLab(t)

    nodeName := leafNodeName(t) // or spineNodeName(t)
    ctx := testutil.LabContext(t)

    // Step 1: Get a locked device for writing
    dev := testutil.LabLockedDevice(t, nodeName)

    // Step 2: Execute the operation
    op := &operations.MyOp{/* ... */}
    if err := op.Validate(ctx, dev); err != nil {
        t.Fatalf("validate: %v", err)
    }
    if err := op.Execute(ctx, dev); err != nil {
        t.Fatalf("execute: %v", err)
    }

    // Step 3: Register cleanup (runs even on failure)
    testutil.LabCleanupChanges(t, nodeName, func(ctx context.Context, d *network.Device) (*network.ChangeSet, error) {
        return d.UndoMyOp(ctx /* ... */)
    })

    // Step 4: Verify with a fresh connection
    verifyDev := testutil.LabConnectedDevice(t, nodeName)
    if !verifyDev.CheckSomething() {
        t.Fatal("expected state not found")
    }
}
```

### Guidelines

- Always use the `e2e` build tag on the first line
- Start with `testutil.SkipIfNoLab(t)` so tests skip gracefully without a lab
- Use `LabLockedDevice` for operations that write to CONFIG_DB
- Use `LabConnectedDevice` (fresh connection) for verification reads
- Register cleanup with `LabCleanupChanges` or `t.Cleanup` with raw Redis
- For tests iterating over nodes, use `TryLabConnectedDevice` and `t.Skipf`
  to skip unreachable nodes rather than failing the whole test
- Use unique resource names/IDs per test to avoid conflicts (e.g., VLAN 500
  in CreateVLAN, 501 in DeleteVLAN, 502 in AddVLANMember)

### Cleanup Patterns

**Via device API** (preferred for simple operations):

```go
testutil.LabCleanupChanges(t, nodeName, func(ctx context.Context, d *network.Device) (*network.ChangeSet, error) {
    return d.DeleteVLAN(ctx, 500)
})
```

**Via raw Redis** (for complex multi-table cleanup):

```go
t.Cleanup(func() {
    client := testutil.LabRedisClient(t, nodeName, 4)
    c := context.Background()
    client.Del(c, "VLAN_INTERFACE|Vlan504|10.99.1.1/24")
    client.Del(c, "VLAN_INTERFACE|Vlan504")
    client.Del(c, "VRF|Vrf_e2e_svi")
    client.Del(c, "VLAN|Vlan504")
})
```

Delete in reverse dependency order: IP entries first, then parent table
entries, then VRFs, then VLANs.

---

## Debugging Failures

For comprehensive debugging guidance, see:
- [NGDP_DEBUGGING.md](NGDP_DEBUGGING.md) -- ASIC emulator debugging
- [SONIC_VS_PITFALLS.md](SONIC_VS_PITFALLS.md) -- Virtual switch pitfalls
- [LEARNINGS.md](LEARNINGS.md) -- Systematized debugging methodology

### "no lab topology running"

All tests skip. The lab is not started.

```bash
make lab-start
# Or check the state file:
cat testlab/.generated/.lab-state
```

### Connection refused on a node

Redis is not reachable. Port 6379 is NOT forwarded by QEMU -- you must
access Redis via SSH:

```bash
# Check if container is running
docker ps | grep clab-spine-leaf

# Test Redis via SSH (correct approach)
sshpass -p cisco123 ssh -o StrictHostKeyChecking=no cisco@<node-ip> \
    "redis-cli -n 4 PING" < /dev/null

# Check if SSH is reachable
ssh -o ConnectTimeout=5 cisco@<node-ip> "echo OK" < /dev/null
```

**Note:** Direct `redis-cli -h <node-ip> -n 4 PING` will fail because
port 6379 is not in the QEMU SLiRP hostfwd list. Always use SSH.

### ASIC_DB convergence timeout

If a test reports `timeout waiting for VLAN N in ASIC_DB`:

```bash
# Quick triage via SSH
sshpass -p cisco123 ssh cisco@<ip> "
  echo '=== orchagent ===' && docker exec swss supervisorctl status orchagent
  echo '=== CONFIG_DB ===' && redis-cli -n 4 KEYS '*VLAN*'
  echo '=== ASIC_DB ===' && redis-cli -n 1 KEYS '*SAI_OBJECT_TYPE_VLAN*'
  echo '=== orchagent errors ===' && docker exec swss cat /var/log/swss/orchagent.log | grep -i error | tail -10
" < /dev/null
```

For complex topologies (IRB = VRF + SVI + VNI), convergence may never
complete on VS -- this is expected behavior. These tests use soft-fail.

### Data-plane ping fails

Ping between servers will **always** fail on SONiC-VS. This is a known
limitation of the ASIC emulator (ngdpd). All data-plane tests use soft-fail
(`t.Skip`). See [NGDP_DEBUGGING.md](NGDP_DEBUGGING.md) Section 5 for details.

### Operation fails but cleanup succeeds

The operation wrote to Redis but didn't produce the expected state.
Inspect CONFIG_DB directly:

```bash
sshpass -p cisco123 ssh cisco@<node-ip> \
    "redis-cli -n 4 HGETALL 'VLAN|Vlan500'" < /dev/null
```

### Stale state from previous test run

Stale CONFIG_DB entries can cause orchagent crashes or test failures.
The test suite runs `ResetLabBaseline()` in `TestMain` to clean up known
keys, but if you see unexpected behavior:

```bash
# Check for stale entries
sshpass -p cisco123 ssh cisco@<ip> "redis-cli -n 4 KEYS '*Vlan700*'" < /dev/null

# Manual cleanup
sshpass -p cisco123 ssh cisco@<ip> "
  redis-cli -n 4 DEL 'VXLAN_TUNNEL_MAP|vtep1|map_10700_Vlan700'
  redis-cli -n 4 DEL 'VLAN_MEMBER|Vlan700|Ethernet2'
  redis-cli -n 4 DEL 'VLAN|Vlan700'
" < /dev/null
```

### Stale cache issues

If a test creates something but verification says it's not there, the
verification device may have a stale cache. Always use a **fresh**
`LabConnectedDevice` for verification, not the same device that executed
the operation.

### BGP test timeout

`TestE2E_BGPNeighborState` polls STATE_DB for up to 3 minutes waiting for
BGP sessions to reach Established. In virtual environments, BGP may not
converge. The test skips (not fails) if sessions don't converge:

```
--- SKIP: TestE2E_BGPNeighborState/leaf1/10.0.0.1
    BGP session did not reach Established (expected in some VS environments)
```

### Container won't start

```bash
# Check container logs
docker logs clab-spine-leaf-spine2

# Check if image exists
docker images | grep sonic

# Try manual deployment with verbose output
cd testlab/.generated
containerlab deploy -t spine-leaf.clab.yml --reconfigure 2>&1
```

---

## Environment Variables

| Variable | Purpose | Default |
|----------|---------|---------|
| `NEWTRON_LAB_TOPOLOGY` | Override running topology name | Read from `testlab/.generated/.lab-state` |
| `TOPO` | Topology for `make lab-start` | `spine-leaf` |

---

## Directory Layout

```
testlab/
  setup.sh                            # Lab orchestration script
  docker-compose.yml                  # Redis container (for integration tests)
  .gitignore                          # Ignores .generated/
  topologies/
    spine-leaf.yml                    # 2-spine + 2-leaf topology
    minimal.yml                       # 1-spine + 1-leaf topology
  seed/
    configdb.json                     # CONFIG_DB seed (integration tests)
    statedb.json                      # STATE_DB seed (integration tests)
  specs/                              # Static specs (integration tests)
    network.json
    site.json
    platforms.json
    profiles/
  .generated/                         # All generated artifacts (gitignored)
    .lab-state                        # Current topology name
    labgen                            # Compiled labgen binary
    spine-leaf.clab.yml               # Generated containerlab topology
    e2e-results.txt                   # Test output from last run
    e2e-report.md                     # Markdown test report (pass/fail/skip summary)
    spine1/config_db.json             # Per-node startup configs
    spine2/config_db.json
    leaf1/config_db.json
    leaf2/config_db.json
    specs/                            # Generated newtron specs
      network.json
      site.json
      platforms.json
      profiles/
        spine1.json                   # Patched with real mgmt_ip
        spine2.json
        leaf1.json
        leaf2.json

internal/testutil/
  lab.go                              # E2E test helpers (build tag: e2e)
  testutil.go                         # Redis discovery, skip helpers
  redis.go                            # Seed/flush/read Redis
  fixtures.go                         # Pre-built Device/Network fixtures

test/e2e/
  main_test.go                        # TestMain: baseline reset, report generation, tunnel cleanup
  connectivity_test.go                # 4 connectivity tests
  operations_test.go                  # 24 operation tests
  multidevice_test.go                 # 3 multi-device tests
  dataplane_test.go                   # 3 data-plane tests (L2, IRB, L3)

cmd/labgen/
  main.go                             # labgen CLI entry point

pkg/labgen/
  types.go                            # Topology YAML types
  parse.go                            # Topology parser + validation
  configdb_gen.go                     # config_db.json generator
  clab_gen.go                         # containerlab YAML generator
  specs_gen.go                        # newtron specs generator
```
