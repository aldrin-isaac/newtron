# Newtron E2E Testing — HOWTO Guide v2

### What Changed in v2

| Area | Change |
|------|--------|
| **Redis Access** | Rewrote Section 6: replaced direct `redis-cli -h <ip>` with SSH-based access via `sshpass`; documented SSH tunnel pool in Go test code |
| **SSH Debugging** | Added subsection in Section 8: 4-step diagnostic (verify SSH, check CLOSE-WAIT, verify Redis inside VM, check patched profiles) |
| **ASIC Convergence** | Added subsection in Section 8: ASIC_DB inspection, WaitForASICVLAN usage, orchagent log checking, soft-fail policy |
| **Three-Tier Assertions** | Added to Section 5: assertion strategy table (CONFIG_DB hard fail, ASIC_DB soft, data-plane soft) |
| **LabSonicNodes** | Added to Section 5: LabSonicNodes vs LabNodes guidance — servers don't have Redis/SSH |
| **Config Persistence** | Added to Section 7: runtime-only warning, `config save -y`, ResetLabBaseline |
| **Common Pitfalls** | Added 4 new entries: connection refused on 6379, SSH tunnel timeout, stale CONFIG_DB, config lost after restart |
| **Configlet Verification** | Updated Section 10: configlet check uses SSH-based redis-cli instead of direct |
| **Related Docs** | Added references to e2e-hld.md, e2e-lld.md, labgen docs |

**Lines:** 836 (v1) → 948 (v2) | All v1 sections preserved; Redis access rewritten for SSH tunnels.

---

## Table of Contents

1. [Prerequisites](#1-prerequisites)
2. [Quick Start](#2-quick-start)
3. [Lab Lifecycle](#3-lab-lifecycle)
4. [Running Tests](#4-running-tests)
5. [Writing New E2E Tests](#5-writing-new-e2e-tests)
6. [Working with Redis](#6-working-with-redis)
7. [Working with Server Containers](#7-working-with-server-containers)
8. [Debugging Failures](#8-debugging-failures)
9. [Adding a New Topology](#9-adding-a-new-topology)
10. [Adding a New Configlet](#10-adding-a-new-configlet)
11. [Common Pitfalls](#11-common-pitfalls)
12. [Reference](#12-reference)

---

## 1. Prerequisites

### Required Software

| Tool | Minimum Version | Install |
|---|---|---|
| Go | 1.21 | https://go.dev/dl/ |
| Docker | 20.x | https://docs.docker.com/engine/install/ |
| containerlab | latest | https://containerlab.dev/install/ |
| redis-cli | any | `apt install redis-tools` or `brew install redis` |
| python3 | 3.8 | Usually pre-installed |
| sshpass | any | `apt install sshpass` |

### Docker Images

Pull the required images before first use:

```bash
docker pull redis:7-alpine
docker pull nicolaka/netshoot:latest
```

The SONiC vrnetlab image (`vrnetlab/cisco_sonic:ngdp-202411`) must be built
locally. See `testlab/images/sonic-ngdp/` for build instructions:

```bash
cd testlab/images/sonic-ngdp && make docker-image
```

### System Resources

The `spine-leaf` topology (4 SONiC VMs + 2 servers) requires:

- CPU: 8+ cores recommended (each QEMU VM uses 1-2 cores)
- RAM: 16 GB minimum (each SONiC VM uses ~2 GB)
- Disk: 20 GB free (VM images + Docker layers)

The `minimal` topology (2 SONiC VMs) is lighter and works with 8 GB RAM.

---

## 2. Quick Start

### One-Command Full Lifecycle

```bash
make test-e2e-full
```

This runs the complete cycle: build labgen, generate artifacts, deploy
containerlab, wait for VMs and Redis, run E2E tests, then tear down the lab.

### Step-by-Step

```bash
# 1. Start the lab (default: spine-leaf topology)
make lab-start

# 2. Check that everything is ready
make lab-status

# 3. Run E2E tests
make test-e2e

# 4. Tear down when done
make lab-stop
```

### Using a Different Topology

```bash
make lab-start TOPO=minimal
make test-e2e
make lab-stop
```

---

## 3. Lab Lifecycle

### Starting the Lab

```bash
make lab-start                # Uses spine-leaf topology
make lab-start TOPO=minimal   # Uses minimal topology
```

What happens behind the scenes:

1. `labgen` is compiled from `cmd/labgen/`
2. Artifacts are generated into `testlab/.generated/`
3. `containerlab deploy` creates containers and networks
4. Script waits for all SONiC containers to reach "healthy" status (up to
   5 minutes)
5. Script waits for Redis on each SONiC node to respond to PING (up to
   5 minutes)
6. Management IPs from Docker are patched into `specs/profiles/*.json`

### Checking Lab Status

```bash
make lab-status
```

Output shows:
- Running topology name
- containerlab node table (IPs, status, image)
- Redis connectivity per SONiC node (OK / UNREACHABLE)

### Stopping the Lab

```bash
make lab-stop
```

Runs `containerlab destroy --cleanup` to remove all containers, networks,
and generated runtime state.

### Restarting After Changes

If you modify configlets, topology files, or vrnetlab code:

```bash
make lab-stop
make lab-start
```

If you only modified Go test code, no restart is needed -- just re-run
`make test-e2e`.

---

## 4. Running Tests

### E2E Tests Only (Lab Required)

```bash
make test-e2e
```

Equivalent to:
```bash
go test -tags e2e -v -count=1 -timeout 10m -p 1 ./...
```

Results are saved to `testlab/.generated/e2e-results.txt`.
A markdown summary report is also generated at `testlab/.generated/e2e-report.md`.

### Run a Specific Test

```bash
go test -tags e2e -v -count=1 -timeout 10m -run TestE2E_CreateVLAN ./test/e2e/
```

### Run a Category of Tests

```bash
# All VLAN tests
go test -tags e2e -v -count=1 -timeout 10m -run 'TestE2E_.*VLAN' ./test/e2e/

# All data-plane tests
go test -tags e2e -v -count=1 -timeout 10m -run 'TestE2E_DataPlane' ./test/e2e/

# All multi-device tests
go test -tags e2e -v -count=1 -timeout 10m -run 'TestE2E_BGP|TestE2E_VLAN.*Two|TestE2E_EVPN' ./test/e2e/
```

### Unit Tests (No Lab Needed)

```bash
make test
```

### Integration Tests (Redis Only, No Lab)

```bash
make test-integration
```

This starts a standalone Redis container, seeds it, runs integration tests,
and stops Redis.

### All Non-E2E Tests

```bash
make test-all    # unit + integration
```

---

## 5. Writing New E2E Tests

### Step 1: Create the Test File

E2E tests live in `test/e2e/`. Either add to an existing file or create a
new one. Every file must have the build tag:

```go
//go:build e2e

package e2e_test
```

### Step 2: Import Test Utilities

```go
import (
    "context"
    "testing"

    "github.com/newtron-network/newtron/internal/testutil"
    "github.com/newtron-network/newtron/pkg/operations"
)
```

### Step 3: Write the Test

Follow this standard pattern:

```go
func TestE2E_YourTestName(t *testing.T) {
    // 1. Guard: skip if no lab is running
    testutil.SkipIfNoLab(t)

    // 2. Get a target node
    nodeName := leafNodeName(t)  // or spineNodeName(t)
    ctx := testutil.LabContext(t) // 2-minute timeout

    // 3. Acquire a locked device for modifications
    dev := testutil.LabLockedDevice(t, nodeName)

    // 4. Create and execute an operation
    op := &operations.CreateVLANOp{
        ID:   550,
        Desc: "my-test-vlan",
    }
    if err := op.Validate(ctx, dev); err != nil {
        t.Fatalf("validate: %v", err)
    }
    if err := op.Execute(ctx, dev); err != nil {
        t.Fatalf("execute: %v", err)
    }

    // 5. Register cleanup (runs after test, in LIFO order)
    testutil.LabCleanupChanges(t, nodeName,
        func(ctx context.Context, d *network.Device) (*network.ChangeSet, error) {
            return d.DeleteVLAN(ctx, 550)
        })

    // 6. Verify with a fresh connection
    verifyDev := testutil.LabConnectedDevice(t, nodeName)
    if !verifyDev.VLANExists(550) {
        t.Fatal("VLAN 550 should exist after creation")
    }

    // 7. Or verify via direct Redis assertion
    testutil.AssertConfigDBEntry(t, nodeName, "VLAN", "Vlan550", map[string]string{
        "vlanid": "550",
    })
}
```

### Step 4: Choose Resource IDs

Pick VLAN IDs, PortChannel numbers, VRF names, and subnets that don't
conflict with existing tests. Check the LLD document's "Test ID Allocation"
section. Convention:

- VLAN IDs: use 550-599 range if the 500s aren't full
- PortChannels: use 210+ range
- VRF names: prefix with `Vrf_e2e_`
- Subnets: use `10.x.x.x` ranges not already allocated

### Step 5: Pick the Right Failure Mode

| Scenario | Use |
|---|---|
| Operation validate/execute error | `t.Fatalf(...)` -- hard fail |
| Missing CONFIG_DB entry | `t.Fatal(...)` -- hard fail |
| BGP not converging | `t.Skip(...)` -- soft fail |
| Data-plane ping fails | `t.Skip(...)` -- soft fail |
| Device connection fails | `t.Skipf(...)` -- allows graceful degradation |

### Three-Tier Assertion Strategy

| Tier | What | Failure Mode | Example |
|------|------|-------------|---------|
| CONFIG_DB | Config written correctly | `t.Fatal` (hard fail) | `AssertConfigDBEntry(t, ...)` |
| ASIC_DB | ASIC programmed correctly | `t.Skip` (soft fail) | `WaitForASICVLAN(t, ...)` |
| Data-plane | Traffic forwarding | `t.Skip` (soft fail) | `ServerPing(t, ...)` |

CONFIG_DB assertions always hard-fail -- if we wrote the config, it must be there.
ASIC_DB and data-plane assertions soft-fail because SONiC-VS doesn't reliably program ASIC or forward packets.

### LabSonicNodes vs LabNodes

- `testutil.LabNodes(t)` -- returns ALL nodes (SONiC + servers)
- `testutil.LabSonicNodes(t)` -- returns only SONiC nodes (no servers)

Use `LabSonicNodes` when iterating over nodes for Redis/SSH operations. Servers don't have Redis or SSH.

### Multi-Device Test Pattern

```go
func TestE2E_MultiDeviceExample(t *testing.T) {
    testutil.SkipIfNoLab(t)

    nodes := testutil.LabSonicNodes(t)
    ctx := testutil.LabContext(t)

    // Iterate over all (or specific) nodes
    for _, node := range nodes {
        t.Run(node.Name, func(t *testing.T) {
            dev := testutil.LabConnectedDevice(t, node.Name)
            // ... verify something on each node
        })
    }
}
```

### Data-Plane Test Pattern

```go
func TestE2E_DataPlaneExample(t *testing.T) {
    testutil.SkipIfNoLab(t)
    testutil.SkipIfNoServers(t, "server1", "server2")

    // ... set up VLAN/VRF/VNI on leaves ...

    // Configure server interfaces
    testutil.ServerConfigureInterface(t, "server1", "eth1", "10.70.0.1/24", "")
    testutil.ServerConfigureInterface(t, "server2", "eth1", "10.70.0.2/24", "")

    // Test connectivity (soft fail for SONiC-VS)
    if !testutil.ServerPing(t, "server1", "10.70.0.2", 5) {
        t.Skip("Data-plane ping failed (expected in SONiC-VS)")
    }
}
```

---

## 6. Working with Redis

### Accessing a Node's Redis from the Host

Port 6379 is **NOT** forwarded by QEMU SLiRP. You cannot connect to Redis
directly from the host via `redis-cli -h <mgmt_ip>`. Instead, access Redis
through SSH:

```bash
# From the host -- SSH into the node, then use redis-cli locally
sshpass -p cisco123 ssh -o StrictHostKeyChecking=no cisco@<mgmt_ip> \
  "redis-cli -n 4 KEYS '*'"

# Or read a specific entry
sshpass -p cisco123 ssh -o StrictHostKeyChecking=no cisco@<mgmt_ip> \
  "redis-cli -n 4 HGETALL 'VLAN|Vlan100'"

# Check STATE_DB (DB 6)
sshpass -p cisco123 ssh -o StrictHostKeyChecking=no cisco@<mgmt_ip> \
  "redis-cli -n 6 HGETALL 'BGP_NEIGHBOR_TABLE|10.0.0.1'"
```

The Go test code uses SSH tunnels from `pkg/device/tunnel.go` -- it creates
a local listener on `127.0.0.1:<random_port>` that forwards to
`127.0.0.1:6379` inside the VM via SSH. The `testutil.LabRedisClient()`
function handles this transparently through a shared tunnel pool.

### Using the Standalone Redis (Integration Tests)

```bash
make redis-start    # Start container
make redis-seed     # Load seed data
make redis-ip       # Show IP address

redis-cli -h $(make redis-ip) -n 4 KEYS '*'

make redis-stop     # Clean up
```

### Redis Key Format

All keys follow the pattern `TABLE|KEY`. For compound keys:
`TABLE|KEY|SUBKEY`.

```
VLAN|Vlan100                          -> hash {vlanid: "100", ...}
VLAN_MEMBER|Vlan100|Ethernet0         -> hash {tagging_mode: "tagged"}
INTERFACE|Ethernet0|10.0.0.1/30       -> hash {} (empty)
ACL_RULE|MY_ACL|RULE_100              -> hash {priority: "100", ...}
VXLAN_TUNNEL_MAP|vtep1|map_10100_Vlan100 -> hash {vlan: "Vlan100", vni: "10100"}
```

### In-Test Redis Operations

```go
// Get a Redis client for a lab node
client := testutil.LabRedisClient(t, "leaf1", 4)

// Read an entry
val, err := client.HGetAll(ctx, "VLAN|Vlan100").Result()

// Write an entry (for test setup)
client.HSet(ctx, "VLAN|Vlan999", map[string]interface{}{
    "vlanid":       "999",
    "admin_status": "up",
})

// Delete an entry (for cleanup)
client.Del(ctx, "VLAN|Vlan999")
```

---

## 7. Working with Server Containers

Server containers (`nicolaka/netshoot:latest`) simulate end-hosts connected
to leaf switches via Ethernet8.

### Running Commands on Servers

```go
output, err := testutil.ServerExec(t, "server1", "ip", "addr", "show", "eth1")
```

From the host CLI:
```bash
docker exec clab-spine-leaf-server1 ip addr show eth1
docker exec clab-spine-leaf-server1 ping -c 3 10.70.0.2
```

### Configuring Server Interfaces

```go
// Set IP and optional default gateway
testutil.ServerConfigureInterface(t, "server1", "eth1", "10.70.0.1/24", "10.70.0.1")

// No gateway (L2-only)
testutil.ServerConfigureInterface(t, "server1", "eth1", "10.70.0.1/24", "")

// Clean up interface
testutil.ServerCleanupInterface(t, "server1", "eth1")
```

### Testing Connectivity

```go
// ServerPing returns true if any ICMP reply is received
ok := testutil.ServerPing(t, "server1", "10.70.0.2", 5)
if !ok {
    // Logs full diagnostics: ping output, ip addr, ip route, arp table
    t.Skip("ping failed")
}
```

### Server Network Topology

```
leaf1:Ethernet8 <-> server1:eth1
leaf2:Ethernet8 <-> server2:eth1
```

Both servers have `eth1` connected to their respective leaf's Ethernet8
port. The `eth0` interface is the Docker management network.

### Config Persistence Warning

CONFIG_DB changes made via the Newtron API (or redis-cli) are **runtime only**.
They survive container restarts but NOT VM reboots. To persist:

```bash
# Inside the SONiC VM
sudo config save -y
```

E2E tests intentionally use ephemeral config. TestMain calls `ResetLabBaseline()`
which deletes any stale test entries from CONFIG_DB.

---

## 8. Debugging Failures

### Test Log Output

Check the saved results:
```bash
cat testlab/.generated/e2e-results.txt
```

Or view the markdown summary report:
```bash
cat testlab/.generated/e2e-report.md
```

Or filter for failures:
```bash
grep -E '(FAIL|FATAL|ERROR)' testlab/.generated/e2e-results.txt
```

### Inspecting Lab State

```bash
# Full containerlab status
make lab-status

# Direct containerlab inspect
cd testlab/.generated && containerlab inspect -t spine-leaf.clab.yml

# Check container health
docker inspect --format '{{.State.Health.Status}}' clab-spine-leaf-leaf1

# Check container logs
docker logs clab-spine-leaf-leaf1

# Shell into a vrnetlab container
docker exec -it clab-spine-leaf-leaf1 bash
```

### Inspecting Redis State

```bash
# SSH into the SONiC VM and inspect Redis
sshpass -p cisco123 ssh cisco@<mgmt_ip> "redis-cli -n 4 KEYS '*'" | sort

# Check specific table
sshpass -p cisco123 ssh cisco@<mgmt_ip> "redis-cli -n 4 KEYS 'VLAN*'"

# Read a specific entry
sshpass -p cisco123 ssh cisco@<mgmt_ip> "redis-cli -n 4 HGETALL 'VLAN|Vlan500'"
```

### SSH into the SONiC VM

```bash
# From inside the container (the VM is at 10.0.0.15)
docker exec -it clab-spine-leaf-leaf1 ssh cisco@10.0.0.15
# Password: cisco123

# Or via the container's management IP
ssh cisco@172.17.0.3
# Password: cisco123
```

### Common Debug Commands Inside SONiC VM

```bash
# Show running config
show runningconfiguration all

# Check BGP
show ip bgp summary
vtysh -c "show bgp summary"

# Check interfaces
show interfaces status
show ip interface

# Check VXLAN
show vxlan tunnel
show vxlan vlanvnimap

# Check Redis directly inside VM
redis-cli -n 4 KEYS '*'
```

### Checking for CLOSE-WAIT Issues

```bash
# Check TCP state inside a container
docker exec clab-spine-leaf-spine1 ss -tnp | grep CLOSE-WAIT

# No CLOSE-WAIT entries should appear on port 22
```

### Verifying SSH Tunnel Access

```bash
# Verify Redis is NOT directly accessible (port 6379 not forwarded)
redis-cli -h <mgmt_ip> -n 4 PING
# Should FAIL with "Connection refused"

# Verify SSH access works for Redis
sshpass -p cisco123 ssh -o StrictHostKeyChecking=no cisco@<mgmt_ip> \
  "redis-cli -n 4 PING"
# Should return: PONG
```

### SSH Tunnel Debugging

If tests fail with "SSH tunnel" errors:

1. **Verify SSH is accessible:**
   ```bash
   sshpass -p cisco123 ssh -o StrictHostKeyChecking=no cisco@<mgmt_ip> echo OK
   ```

2. **Check for CLOSE-WAIT connections:**
   ```bash
   docker exec clab-spine-leaf-leaf1 ss -tnp | grep 22
   ```
   Excessive CLOSE-WAIT on port 22 indicates tunnel cleanup issues.

3. **Verify Redis responds inside the VM:**
   ```bash
   sshpass -p cisco123 ssh cisco@<mgmt_ip> "redis-cli -n 4 PING"
   ```

4. **Check the SSH credentials in patched profiles:**
   ```bash
   cat testlab/.generated/specs/profiles/leaf1.json | jq '.ssh_user, .ssh_pass'
   ```
   Should show "cisco" and "cisco123".

### ASIC Convergence Issues

ASIC_DB assertions may fail if the SONiC orchagent hasn't processed CONFIG_DB changes yet.

1. **Check ASIC_DB directly:**
   ```bash
   sshpass -p cisco123 ssh cisco@<mgmt_ip> \
     "redis-cli -n 1 KEYS 'ASIC_STATE:SAI_OBJECT_TYPE_VLAN*'"
   ```

2. **Wait for orchagent:** The test utilities include `WaitForASICVLAN()` which polls
   ASIC_DB until the VLAN appears (up to 30s timeout). If it consistently times out,
   check orchagent logs inside the VM:
   ```bash
   sshpass -p cisco123 ssh cisco@<mgmt_ip> "tail -20 /var/log/syslog | grep orchagent"
   ```

3. **ASIC assertions are soft-fail:** Tests skip (not fail) when ASIC convergence
   times out, because SONiC-VS timing is unpredictable.

---

## 9. Adding a New Topology

### Step 1: Create the Topology File

Create `testlab/topologies/<name>.yml`:

```yaml
name: my-topology

defaults:
  image: vrnetlab/cisco_sonic:ngdp-202411
  username: cisco
  password: cisco123
  platform: vs-platform
  site: lab-site
  hwsku: "Force10-S6000"
  ntp_server_1: "10.100.0.1"
  ntp_server_2: "10.100.0.2"
  syslog_server: "10.100.0.3"

network:
  as_number: 65000
  region: lab-region

nodes:
  spine1:
    role: spine
    loopback_ip: "10.0.0.1"
    variables:
      cluster_id: "10.0.0.1"
  leaf1:
    role: leaf
    loopback_ip: "10.0.0.11"
    variables:
      vtep_name: vtep1
      spine1_ip: "10.0.0.1"

links:
  - endpoints: ["spine1:Ethernet0", "leaf1:Ethernet0"]

role_defaults:
  spine:
    - sonic-baseline
    - sonic-evpn-spine
  leaf:
    - sonic-baseline
    - sonic-evpn-leaf
```

### Step 2: Deploy and Test

```bash
make lab-start TOPO=my-topology
make lab-status
make test-e2e
make lab-stop
```

### Notes

- Node names must be unique across the topology.
- Server nodes (`role: server`) use `nicolaka/netshoot:latest` by default
  and don't get configlets.
- Links use SONiC interface naming (`Ethernet0`, `Ethernet4`, `Ethernet8`,
  etc., in multiples of 4).
- Variables in the `variables:` block are available for configlet template
  substitution.

---

## 10. Adding a New Configlet

### Step 1: Create the JSON Template

Create `configlets/<name>.json`:

```json
{
  "MY_TABLE": {
    "entry1": {
      "field1": "{{my_variable}}",
      "field2": "static_value"
    }
  }
}
```

Variables use `{{variable_name}}` syntax and are resolved from:
1. Node-level `variables:` in the topology
2. Node-level fields (`loopback_ip`, etc.)
3. Topology `defaults:`

### Step 2: Reference It in a Topology

Add the configlet name to `role_defaults:` in the topology file:

```yaml
role_defaults:
  leaf:
    - sonic-baseline
    - sonic-evpn-leaf
    - my-new-configlet    # Added
```

### Step 3: Regenerate and Redeploy

```bash
make lab-stop
make lab-start
```

Verify the configlet was applied:
```bash
sshpass -p cisco123 ssh -o StrictHostKeyChecking=no cisco@<mgmt_ip> \
  "redis-cli -n 4 HGETALL 'MY_TABLE|entry1'"
```

---

## 11. Common Pitfalls

### "no lab is running" / Tests Skip Everything

**Cause:** Lab is not started, or `.lab-state` file is missing.

**Fix:**
```bash
make lab-start
```

### Tests Fail with "Redis unreachable"

**Cause:** SONiC VMs haven't fully booted, or SSH tunnel could not be established.

**Fix:** Wait and check:
```bash
make lab-status
# If Redis shows UNREACHABLE, wait a minute and try again.
# If persistent, restart the lab.
```

### "validate: VLAN 500 already exists"

**Cause:** Previous test run didn't clean up properly.

**Fix:** Either restart the lab or manually clean Redis:
```bash
sshpass -p cisco123 ssh cisco@<mgmt_ip> "redis-cli -n 4 DEL 'VLAN|Vlan500'"
```

### Tests Pass Locally But Skip Data-Plane

**Expected behavior.** SONiC-VS does not support VXLAN data-plane
forwarding. Data-plane tests log diagnostics and skip. This is not a
failure.

### Lab Start Times Out Waiting for Healthy

**Cause:** QEMU VMs are slow to boot, or the host lacks resources.

**Fix:**
- Check `docker logs clab-spine-leaf-<node>` for boot progress.
- Ensure host has sufficient CPU and RAM.
- Try `minimal` topology for faster startup.

### "device lock timeout" or "could not acquire lock"

**Cause:** A previous test process crashed while holding a device lock.

**Fix:**
```bash
rm -f /tmp/newtron-test-locks/*
```

### Stale Generated Artifacts

If tests behave unexpectedly after code changes:

```bash
make clean
make lab-stop
make lab-start
```

`make clean` removes `testlab/.generated/` and clears the Go test cache.

### Tests Fail with Connection Refused on Port 6379

**Cause:** Port 6379 is not forwarded by QEMU. This is by design.

**Fix:** Access Redis through SSH tunnel. The test code handles this
automatically when SSH credentials are in the device profile. From the
command line, use `sshpass` with SSH as shown in Section 6.

### SSH Tunnel Timeout During WaitForLabRedis

**Cause:** SONiC VM SSH server not ready yet.

**Fix:** The wait loop retries SSH connections. If persistent, check VM boot
progress with `docker logs clab-spine-leaf-<node>`.

### Stale CONFIG_DB Entries from Previous Test Run

**Cause:** Tests didn't clean up or ResetLabBaseline didn't run.

**Fix:** TestMain calls `ResetLabBaseline()` which deletes stale VLAN, VRF,
ACL, VXLAN, and NEWTRON_SERVICE_BINDING entries. Restart the lab for a clean
slate.

### Config Lost After VM Restart

**Cause:** Redis changes are runtime-only. `config save -y` was not run.

**Fix:** For E2E tests this is expected -- tests use ephemeral config. For
persistent changes, SSH into the VM and run `sudo config save -y`.

---

## 12. Reference

### Environment Variables

| Variable | Default | Description |
|---|---|---|
| `REDIS_ADDR` | (auto-detected) | Override Redis address for integration tests |
| `NEWTRON_TEST_REDIS` | (auto-detected) | Alternative Redis address override |

### File Paths

| Path | Description |
|---|---|
| `testlab/.generated/.lab-state` | Contains current topology name |
| `testlab/.generated/e2e-results.txt` | Test output log |
| `testlab/.generated/e2e-report.md` | Markdown summary report (pass/fail/skip/partial) |
| `testlab/.generated/specs/profiles/<node>.json` | Patched device profiles |
| `testlab/.generated/<node>/config_db.json` | Generated startup configs |
| `/tmp/newtron-test-locks/` | Device lock files |

### Timeouts

| Operation | Timeout | Location |
|---|---|---|
| Lab container healthy | 5 min | `setup.sh:lab_wait_healthy` |
| Lab Redis ready | 5 min | `setup.sh:lab_wait_redis` |
| `LabContext()` | 2 min | `testutil/lab.go` |
| `Context()` | 30 sec | `testutil/testutil.go` |
| `go test -timeout` | 10 min | Makefile `test-e2e` |
| BGP convergence poll | 3 min | `multidevice_test.go` |
| SSH wait (backup.sh) | 60 sec (30 retries x 2s) | `backup.sh:wait_for_ssh` |

### Port Reference

| Port | Service | Access Method |
|---|---|---|
| 22 | SSH | QEMU hostfwd -> VM SSH |
| 6379 | Redis | **NOT forwarded** -- access via SSH tunnel only |
| 80 | HTTP | QEMU hostfwd |
| 443 | HTTPS | QEMU hostfwd |
| 830 | NETCONF | QEMU hostfwd |
| 8080 | gNMI/HTTP API | QEMU hostfwd |
| 9339 | gNMI (IANA) | QEMU hostfwd |

**Important:** Port 6379 is NOT in the QEMU SLiRP hostfwd list. Redis must
be accessed via SSH tunnels (Go code: `pkg/device/tunnel.go`, shell:
`sshpass + ssh` with redis-cli). See [NGDP_DEBUGGING.md](../NGDP_DEBUGGING.md)
Section 3 for the complete packet path explanation.

### SONiC VM Credentials

| Topology | Username | Password |
|---|---|---|
| `spine-leaf` | `cisco` | `cisco123` |
| `minimal` | `cisco` | `cisco123` |

---

## Related Documentation

For deeper dives into specific topics, see:

- [e2e-hld.md](e2e-hld.md) -- E2E Testing High-Level Design
- [e2e-lld.md](e2e-lld.md) -- E2E Testing Low-Level Design
- [labgen HLD](../labgen/hld.md) -- Lab Generator High-Level Design
- [labgen LLD](../labgen/lld.md) -- Lab Generator Low-Level Design
- [labgen HOWTO](../labgen/howto.md) -- Lab Generator HOWTO Guide
- [LEARNINGS.md](../LEARNINGS.md) -- Systematized debugging learnings
- [SONIC_VS_PITFALLS.md](../SONIC_VS_PITFALLS.md) -- SONiC-VS pitfalls catalog
- [NGDP_DEBUGGING.md](../NGDP_DEBUGGING.md) -- ASIC emulator debugging
- [CONFIGDB_GUIDE.md](../CONFIGDB_GUIDE.md) -- CONFIG_DB schema discovery
- [CONTAINERLAB_HOWTO.md](../CONTAINERLAB_HOWTO.md) -- Containerlab setup guide
- [VERIFICATION_TOOLKIT.md](../VERIFICATION_TOOLKIT.md) -- Verification tooling
- [DESIGN_PRINCIPLES.md](../DESIGN_PRINCIPLES.md) -- Design principles
- [test-plan.md](test-plan.md) -- Formal test plan with success criteria
