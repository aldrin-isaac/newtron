# NGDP ASIC Emulator Debugging Guide

This document is a comprehensive guide to understanding, debugging, and working
with the NGDP ASIC emulator (`ngdpd`) that ships inside SONiC Virtual Switch
(VS) images. It covers architecture, internal wiring, debugging techniques,
common failure modes, and proven workarounds -- all from direct experience
building newtron's E2E test infrastructure.

---

## Table of Contents

1. [What is ngdpd?](#1-what-is-ngdpd)
2. [Architecture: From CONFIG_DB to ASIC](#2-architecture-from-config_db-to-asic)
3. [Internal Packet Wiring](#3-internal-packet-wiring)
4. [ASIC_DB Deep Dive](#4-asic_db-deep-dive)
5. [What ngdpd Can and Cannot Do](#5-what-ngdpd-can-and-cannot-do)
6. [Debugging ASIC_DB Convergence](#6-debugging-asic_db-convergence)
7. [Debugging the Packet Path](#7-debugging-the-packet-path)
8. [Process Hierarchy and Service Health](#8-process-hierarchy-and-service-health)
9. [Common Failure Modes](#9-common-failure-modes)
10. [Debugging Recipes](#10-debugging-recipes)
11. [ASIC Convergence Timeouts by Topology](#11-asic-convergence-timeouts-by-topology)
12. [Writing ASIC-Aware Tests](#12-writing-asic-aware-tests)
13. [Diagnostic Commands Reference](#13-diagnostic-commands-reference)

---

## 1. What is ngdpd?

`ngdpd` (also referred to as `vssyncd` in some SONiC builds) is the ASIC
simulator daemon in SONiC-VS images. It replaces the real ASIC SDK (e.g.,
Memory Access SDK, memory access lib, or vendor-specific SDKs) with a
**control-plane-only** stub.

In production SONiC, `syncd` communicates with real ASIC hardware via SAI
(Switch Abstraction Interface). In SONiC-VS, `ngdpd` implements the SAI API
surface but:

- **Accepts** all SAI object creation, deletion, and attribute-set calls
- **Returns** valid Object IDs (OIDs) for every operation
- **Programs** ASIC_DB (Redis DB 1) so the control-plane pipeline completes
- **Does NOT** implement any data-plane forwarding engine

### Where ngdpd Runs

```
Host machine
 └─ Docker (containerlab)
     └─ vrnetlab container (cisco_sonic)
         └─ QEMU VM (SONiC-VS)
             └─ systemd
                 ├─ redis-server (all DBs)
                 ├─ orchagent  ← reads CONFIG_DB, programs ASIC via SAI
                 ├─ ngdpd      ← ASIC simulator (SAI backend)
                 ├─ bgp (FRR)
                 ├─ teamd
                 └─ other SONiC services
```

### Image Identification

The newtron testlab uses the `ngdp-202411` build:

```yaml
# testlab/topologies/spine-leaf.yml
defaults:
  image: vrnetlab/cisco_sonic:ngdp-202411
```

Built from `sonic-vs-ngdp-202411.qcow2` via the Makefile in
`testlab/images/sonic-ngdp/`.

---

## 2. Architecture: From CONFIG_DB to ASIC

The SONiC control-plane pipeline operates in three stages. Understanding this
pipeline is essential for debugging ASIC convergence issues.

### Stage 1: CONFIG_DB Write

The operator (newtron, `redis-cli`, or `config_db.json` at boot) writes to
CONFIG_DB (Redis DB 4).

```
newtron → Redis HSET "VLAN|Vlan700" vlanid 700
```

### Stage 2: orchagent Processing

`orchagent` subscribes to CONFIG_DB changes via Redis keyspace notifications.
When it sees a new VLAN entry, it:

1. Reads the CONFIG_DB entry
2. Validates the configuration
3. Writes to APP_DB (DB 0) with internal state
4. Calls SAI APIs to program the ASIC

```
CONFIG_DB (DB 4)  →  orchagent  →  APP_DB (DB 0)  →  SAI API call
```

### Stage 3: ASIC Programming (ngdpd)

The SAI call reaches `ngdpd`, which:

1. Allocates an OID for the object
2. Writes the object to ASIC_DB (DB 1)
3. Returns success to orchagent

```
SAI create_vlan(vlan_id=700)  →  ngdpd  →  ASIC_DB write
                                          oid:0x260000000005a2
                                          SAI_VLAN_ATTR_VLAN_ID = 700
```

### The Complete Pipeline

```
CONFIG_DB write                     ← newtron / redis-cli
    │
    ▼
orchagent processes                 ← reads CONFIG_DB, calls SAI
    │
    ▼
APP_DB updated                      ← internal orchagent state
    │
    ▼
ngdpd receives SAI call            ← ASIC simulator
    │
    ▼
ASIC_DB populated                  ← objects with OIDs appear
    │
    ▼
STOP                               ← no data-plane forwarding
```

On real hardware, after ASIC_DB, the ASIC would begin forwarding packets.
On VS, the pipeline ends at ASIC_DB.

---

## 3. Internal Packet Wiring

Understanding the packet wiring inside the vrnetlab container is critical for
debugging data-plane (non-)behavior.

### Layer 1: Containerlab to Container

```
server1 eth1  ←→  veth pair  ←→  clab-spine-leaf-leaf1 eth1
```

Containerlab creates veth pairs connecting containers. The server's `eth1` is
directly wired to the SONiC container's `eth1`.

### Layer 2: Container to QEMU VM

Inside the vrnetlab container, QEMU runs the SONiC VM. The container's `ethN`
interfaces are connected to QEMU VM TAP interfaces via tc mirred redirect rules:

```
Container ethN  ←→  tc mirred redirect  ←→  tapN  ←→  QEMU NIC (EthernetN)
```

The tc rules are set up by `/etc/tc-tap-ifup` (created by `vrnetlab.py`):

```bash
# From vrnetlab.py create_tc_tap_ifup():
tc qdisc add dev eth$INDEX clsact
tc filter add dev eth$INDEX ingress flower action mirred egress redirect dev tap$INDEX

tc qdisc add dev tap$INDEX clsact
tc filter add dev tap$INDEX ingress flower action mirred egress redirect dev eth$INDEX
```

### Layer 3: QEMU VM Internal (ASIC Simulator)

Inside the SONiC VM, another layer of veth pairs and tc rules connects
the QEMU NIC interfaces to the ASIC simulator:

```
EthernetN (TAP)  ←→  vethN / swvethN (veth pair)  ←→  ngdpd
```

The `swvethN ↔ ethN` bridging is done by the `lab_bridge_nics` function in
`setup.sh`:

```bash
# From setup.sh lab_bridge_nics():
sudo /usr/sbin/tc qdisc add dev swveth$i clsact
sudo /usr/sbin/tc filter add dev swveth$i ingress flower action mirred egress redirect dev eth$i

sudo /usr/sbin/tc qdisc add dev eth$i clsact
sudo /usr/sbin/tc filter add dev eth$i ingress flower action mirred egress redirect dev swveth$i
```

### Complete Packet Path (End to End)

```
server1 eth1
  ↓ (veth pair, containerlab)
leaf1 container eth1
  ↓ (tc mirred redirect, vrnetlab)
tap1 (QEMU TAP)
  ↓ (QEMU virtio NIC)
Ethernet0 inside SONiC VM
  ↓ (tc mirred redirect, lab_bridge_nics)
swveth0 / veth0 (veth pair)
  ↓
ngdpd (ASIC simulator)
  ↓
NOTHING — ngdpd does not forward packets back out
```

### Management Interface Path

The management interface (`eth0` in the container) has a slightly different
wiring. QEMU uses SLiRP user-mode networking for the management plane, with
explicit TCP port forwarding (hostfwd):

```
Container eth0  ←→  tc mirred redirect  ←→  tap0  ←→  QEMU SLiRP
                                                           ↓
                                                    hostfwd port 22 (SSH)
                                                    hostfwd port 80, 443, 830...
                                                    NO hostfwd for port 6379 (Redis)
```

This is why Redis must be accessed via SSH tunnel, not directly.

Forwarded ports (from `vrnetlab.py`):

```python
self.mgmt_tcp_ports = [80, 443, 830, 6030, 8080, 9339, 32767, 50051, 57400]
# Note: 6379 is NOT in this list
```

---

## 4. ASIC_DB Deep Dive

ASIC_DB (Redis DB 1) contains SAI objects that represent the ASIC programming
state. Understanding its schema is essential for convergence debugging.

### Key Format

```
ASIC_STATE:SAI_OBJECT_TYPE_<TYPE>:oid:<HEX_OID>
```

Examples:

```
ASIC_STATE:SAI_OBJECT_TYPE_VLAN:oid:0x260000000005a2
ASIC_STATE:SAI_OBJECT_TYPE_BRIDGE_PORT:oid:0x3a000000001234
ASIC_STATE:SAI_OBJECT_TYPE_TUNNEL:oid:0x2a000000000abc
ASIC_STATE:SAI_OBJECT_TYPE_VIRTUAL_ROUTER:oid:0x30000000000def
```

### Common SAI Object Types

| SAI Object Type | Corresponds To | CONFIG_DB Source |
|---|---|---|
| `SAI_OBJECT_TYPE_VLAN` | VLAN | `VLAN\|Vlan<id>` |
| `SAI_OBJECT_TYPE_BRIDGE_PORT` | Bridge port member | `VLAN_MEMBER\|...` |
| `SAI_OBJECT_TYPE_TUNNEL` | VXLAN tunnel | `VXLAN_TUNNEL\|...` |
| `SAI_OBJECT_TYPE_TUNNEL_MAP` | VNI mapping | `VXLAN_TUNNEL_MAP\|...` |
| `SAI_OBJECT_TYPE_TUNNEL_MAP_ENTRY` | VNI-VLAN binding | `VXLAN_TUNNEL_MAP\|...` |
| `SAI_OBJECT_TYPE_TUNNEL_TERM_TABLE_ENTRY` | Tunnel termination | auto (orchagent) |
| `SAI_OBJECT_TYPE_ROUTER_INTERFACE` | L3 interface / SVI | `VLAN_INTERFACE\|...` |
| `SAI_OBJECT_TYPE_VIRTUAL_ROUTER` | VRF | `VRF\|...` |
| `SAI_OBJECT_TYPE_ROUTE_ENTRY` | IP route | FRR / orchagent |
| `SAI_OBJECT_TYPE_NEXT_HOP` | Next hop | FRR / orchagent |

### Reading ASIC_DB

```bash
# List all objects of a type
redis-cli -n 1 KEYS "ASIC_STATE:SAI_OBJECT_TYPE_VLAN:*"

# Get all attributes of an object
redis-cli -n 1 HGETALL "ASIC_STATE:SAI_OBJECT_TYPE_VLAN:oid:0x260000000005a2"
# 1) "SAI_VLAN_ATTR_VLAN_ID"
# 2) "700"

# Count objects by type
redis-cli -n 1 KEYS "ASIC_STATE:SAI_OBJECT_TYPE_TUNNEL:*" | wc -l

# Search for tunnel-related objects
redis-cli -n 1 KEYS "*TUNNEL*"
```

### ASIC_DB Object Dependencies

Objects in ASIC_DB have dependencies. When debugging convergence failures, check
the dependency chain:

```
VRF (VIRTUAL_ROUTER)
  └── ROUTER_INTERFACE (SVI)
       └── VLAN
            ├── BRIDGE_PORT (members)
            └── TUNNEL_MAP_ENTRY (VNI binding)
                 └── TUNNEL_MAP
                      └── TUNNEL
```

If a parent object is missing, child objects may not be created. For example,
if `SAI_OBJECT_TYPE_VIRTUAL_ROUTER` (VRF) is not in ASIC_DB, the SVI
`ROUTER_INTERFACE` won't be created either.

---

## 5. What ngdpd Can and Cannot Do

### Capabilities (Control Plane)

| Feature | Works? | Evidence |
|---|---|---|
| CONFIG_DB writes for any table | Yes | All HSET operations succeed |
| orchagent processing (CONFIG_DB → ASIC_DB) | Yes | Objects appear in DB 1 |
| Simple VLAN objects in ASIC_DB | Yes | Converges in < 5s |
| VRF objects in ASIC_DB | Yes | Converges in < 5s |
| VXLAN tunnel objects in ASIC_DB | Yes | Objects present (< 30s) |
| Bridge port objects in ASIC_DB | Yes | Created with VLAN members |
| SAI OID allocation | Yes | Valid OIDs returned |
| BGP sessions via FRR | Yes | FRR independent of ASIC |
| EVPN route exchange (BGP) | Yes | BGP EVPN address family works |

### Limitations (Data Plane)

| Feature | Works? | Details |
|---|---|---|
| L2 bridging (packet forwarding) | No | Frames don't egress between ports |
| VXLAN encap/decap | No | No UDP/VXLAN wrapping occurs |
| L3 routing through ASIC | No | Kernel routes exist but ASIC doesn't forward |
| MTU application to kernel TAP | No | CONFIG_DB=9000, kernel stays 9100 |
| Interface admin status propagation | Partial | Some builds apply, some don't |
| ARP suppression | No | Requires ASIC interception |
| ACL hardware offload | No | Rules accepted but not enforced in data plane |
| QoS scheduling | No | Queue configuration accepted but not active |

### The Fundamental Rule

```
CONFIG_DB write  →  orchagent processes  →  ASIC_DB populated  →  STOP
                                                                   │
                                                       ngdpd does NOT
                                                       forward packets
```

**Test CONFIG_DB and ASIC_DB for correctness. Never expect data-plane
forwarding.**

---

## 6. Debugging ASIC_DB Convergence

ASIC_DB convergence means: an object written to CONFIG_DB has been processed
by orchagent and programmed into ASIC_DB via ngdpd.

### Why Convergence Matters

Even though ngdpd doesn't forward packets, ASIC_DB convergence proves the
entire control-plane pipeline works:

1. CONFIG_DB entry was well-formed
2. orchagent parsed it correctly
3. SAI API call was valid
4. ngdpd accepted and persisted the object

This is the strongest verification possible on VS without real hardware.

### The WaitForASICVLAN Pattern

The standard convergence check polls ASIC_DB for a specific SAI object:

```go
// From internal/testutil/lab.go:508-537
func WaitForASICVLAN(ctx context.Context, t *testing.T, name string, vlanID int) error {
    client := LabRedisClient(t, name, 1) // ASIC_DB = Redis DB 1
    want := fmt.Sprintf("%d", vlanID)

    for {
        select {
        case <-ctx.Done():
            return fmt.Errorf("timeout waiting for VLAN %d in ASIC_DB on %s", vlanID, name)
        default:
        }

        keys, _ := client.Keys(ctx, "ASIC_STATE:SAI_OBJECT_TYPE_VLAN:*").Result()
        for _, key := range keys {
            vid, _ := client.HGet(ctx, key, "SAI_VLAN_ATTR_VLAN_ID").Result()
            if vid == want {
                return nil  // Converged
            }
        }
        time.Sleep(1 * time.Second)
    }
}
```

### Extending to Other Object Types

The same pattern works for any SAI object type. Adjust the key pattern and
attribute name:

```bash
# Wait for tunnel object
KEYS "ASIC_STATE:SAI_OBJECT_TYPE_TUNNEL:*"
# Check: SAI_TUNNEL_ATTR_TYPE == "SAI_TUNNEL_TYPE_VXLAN"

# Wait for VRF object
KEYS "ASIC_STATE:SAI_OBJECT_TYPE_VIRTUAL_ROUTER:*"
# Check: presence is sufficient (VRF has no distinguishing attr besides OID)

# Wait for router interface (SVI)
KEYS "ASIC_STATE:SAI_OBJECT_TYPE_ROUTER_INTERFACE:*"
# Check: SAI_ROUTER_INTERFACE_ATTR_TYPE == "SAI_ROUTER_INTERFACE_TYPE_VLAN"
```

### When Convergence Fails

If `WaitForASICVLAN` times out, check these in order:

**1. Is orchagent running?**

```bash
ssh cisco@<ip> "docker exec swss supervisorctl status orchagent"
# orchagent  RUNNING  pid 123, uptime 0:30:00
```

If orchagent has crashed or restarted, check its logs:

```bash
ssh cisco@<ip> "docker exec swss cat /var/log/swss/orchagent.log | tail -50"
```

**2. Is the CONFIG_DB entry correct?**

```bash
ssh cisco@<ip> "redis-cli -n 4 HGETALL 'VLAN|Vlan700'"
# 1) "vlanid"
# 2) "700"
```

If the entry is malformed or missing fields, orchagent will ignore it.

**3. Are there errors in orchagent's log?**

```bash
ssh cisco@<ip> "docker exec swss cat /var/log/swss/orchagent.log | grep -i error"
ssh cisco@<ip> "docker exec swss cat /var/log/swss/orchagent.log | grep -i 'Vlan700'"
```

Look for:
- `Failed to create` messages
- `SAI_STATUS_FAILURE` returns
- Dependency errors (e.g., creating VLAN member before VLAN)

**4. Is there a stale entry blocking creation?**

Stale ASIC_DB entries from a previous test run can prevent new objects from
being created:

```bash
# Check for stale entries
ssh cisco@<ip> "redis-cli -n 1 KEYS '*VLAN*'"

# Check for stale CONFIG_DB entries
ssh cisco@<ip> "redis-cli -n 4 KEYS '*VLAN*'"
```

The newtron E2E suite handles this with `ResetLabBaseline()` which deletes
known stale keys before the test suite runs (see `internal/testutil/lab.go`,
`staleE2EKeys` variable).

**5. Is it a complex topology that simply won't converge on VS?**

IRB topologies (VRF + SVI + VXLAN VNI mapping) may never converge on VS.
See [Section 11](#11-asic-convergence-timeouts-by-topology).

---

## 7. Debugging the Packet Path

When debugging why packets aren't flowing (they won't on VS, but understanding
*where* they stop is valuable), trace each segment.

### Segment 1: Server to Container

```bash
# On server1, send a ping
docker exec clab-spine-leaf-server1 ping -c 1 10.70.0.2

# On the SONiC container, check if packets arrive on eth1
docker exec clab-spine-leaf-leaf1 tcpdump -i eth1 -c 5 icmp
```

### Segment 2: Container eth to TAP (tc redirect)

```bash
# Inside the SONiC VM, check tc stats on the interface
ssh cisco@<ip> "sudo /usr/sbin/tc -s filter show dev eth1 ingress"
# filter protocol all pref 1 flower chain 0
#   action order 1: mirred (Egress Redirect to device tap1) ...
#   Sent 4560 bytes 10 pkt (dropped 0, overlimits 0 requeues 0)
```

If `Sent X bytes Y pkt` is non-zero and increasing, packets are being
redirected. **This is working correctly.**

If `Sent 0 bytes 0 pkt`, the tc rule isn't matching. Check:

```bash
# Is the qdisc attached?
ssh cisco@<ip> "sudo /usr/sbin/tc qdisc show dev eth1"
# Should show "clsact"

# Are both directions configured?
ssh cisco@<ip> "sudo /usr/sbin/tc filter show dev eth1 ingress"
ssh cisco@<ip> "sudo /usr/sbin/tc filter show dev tap1 ingress"
```

### Segment 3: TAP to ASIC (swveth/veth)

```bash
# Inside the VM, check the swveth/veth pairs exist
ssh cisco@<ip> "ip link show type veth"
# Should show swveth0, veth0, swveth1, veth1, etc.

# Check tc stats on swveth
ssh cisco@<ip> "sudo /usr/sbin/tc -s filter show dev swveth1 ingress"
```

### Segment 4: ASIC to Egress (ngdpd)

This is where packets stop. ngdpd receives them but does not forward:

```bash
# Capture on the ASIC-side veth interface
ssh cisco@<ip> "tcpdump -i veth1 -c 5"
# (may show incoming packets)

# Capture on a different port's veth interface
ssh cisco@<ip> "tcpdump -i veth2 -c 5"
# (will show nothing -- ngdpd doesn't forward)
```

### Segment 5: VXLAN (Never Works on VS)

```bash
# Check for VXLAN UDP packets
ssh cisco@<ip> "tcpdump -i any udp port 4789 -c 1"
# (silence -- no VXLAN encapsulation occurs)
```

### ebtables Check

Before debugging packet path, always clear ebtables rules that may be
dropping traffic:

```bash
# Check current rules
ssh cisco@<ip> "sudo /usr/sbin/ebtables -L"

# If restrictive rules exist, flush them
ssh cisco@<ip> "sudo /usr/sbin/ebtables -F FORWARD"
ssh cisco@<ip> "sudo /usr/sbin/ebtables -F INPUT"
ssh cisco@<ip> "sudo /usr/sbin/ebtables -F OUTPUT"
```

---

## 8. Process Hierarchy and Service Health

### Checking Service Status

SONiC runs services in Docker containers inside the QEMU VM. The primary
container for ASIC-related services is `swss`:

```bash
# List all SONiC Docker containers
ssh cisco@<ip> "docker ps --format '{{.Names}}: {{.Status}}'"
# swss: Up 30 minutes
# syncd: Up 30 minutes
# bgp: Up 30 minutes
# teamd: Up 30 minutes
# database: Up 30 minutes

# Check processes inside swss container
ssh cisco@<ip> "docker exec swss supervisorctl status"
# orchagent            RUNNING   pid 45, uptime 0:30:00
# ...

# Check ngdpd / syncd process
ssh cisco@<ip> "docker exec syncd supervisorctl status"
# syncd                RUNNING   pid 67, uptime 0:30:00
```

### Important Logs

```bash
# orchagent logs (most useful for convergence debugging)
ssh cisco@<ip> "docker exec swss cat /var/log/swss/orchagent.log | tail -100"

# syncd/ngdpd logs
ssh cisco@<ip> "docker exec syncd cat /var/log/syncd/syncd.log | tail -100"

# System logs
ssh cisco@<ip> "tail -100 /var/log/syslog"
```

### Service Restart (Use with Caution)

If orchagent or syncd appear stuck, restarting can help -- but it also
resets all ASIC_DB state:

```bash
# Restart swss (includes orchagent)
ssh cisco@<ip> "sudo systemctl restart swss"

# Wait for services to come back
sleep 30

# Verify
ssh cisco@<ip> "docker exec swss supervisorctl status orchagent"
```

After restart, all CONFIG_DB entries will be re-processed by orchagent,
re-populating ASIC_DB from scratch. This can take 30-60 seconds.

---

## 9. Common Failure Modes

### Failure 1: ASIC_DB Convergence Timeout (Simple VLAN)

**Symptom:** `WaitForASICVLAN` times out for a simple VLAN that should work.

**Causes:**
1. orchagent crashed (check `docker exec swss supervisorctl status`)
2. Stale ASIC_DB entry from previous test (check `redis-cli -n 1 KEYS "*VLAN*"`)
3. CONFIG_DB entry malformed (check `redis-cli -n 4 HGETALL "VLAN|Vlan700"`)
4. Redis connectivity issue (check SSH tunnel)

**Debug:**
```bash
# Quick triage
ssh cisco@<ip> "
  echo '=== orchagent ===' && docker exec swss supervisorctl status orchagent
  echo '=== CONFIG_DB ===' && redis-cli -n 4 KEYS '*VLAN*'
  echo '=== ASIC_DB ===' && redis-cli -n 1 KEYS '*SAI_OBJECT_TYPE_VLAN*'
"
```

### Failure 2: ASIC_DB Convergence Timeout (IRB Topology)

**Symptom:** `WaitForASICVLAN` times out after configuring VRF + SVI + VNI.

**Cause:** This is expected on VS. The IRB topology creates a complex chain
of SAI objects (VRF → router interface → VLAN → bridge port → tunnel map
entry → tunnel). orchagent may not fully process this chain on VS.

**Resolution:** Soft-fail with `t.Skip`:

```go
if err := testutil.WaitForASICVLAN(asicCtx, t, name, 800); err != nil {
    t.Logf("ASIC convergence failed on %s: %v (expected on VS)", name, err)
    t.Skip("ASIC convergence for IRB topology not supported on virtual switch")
}
```

### Failure 3: orchagent Crash on Stale State

**Symptom:** orchagent crashes immediately when the test suite starts, or
ASIC_DB shows inconsistent state.

**Cause:** Stale CONFIG_DB entries from a previous test run can cause
orchagent to attempt operations that conflict with existing ASIC_DB state.
The most common trigger is VXLAN-related entries:

```
# orchagent.log:
# ERR syncd#syncd: brcm_sai_create_vxlan_tunnel_map: ... already exists
```

**Resolution:** Clean up before tests:

```go
// From internal/testutil/lab.go -- ResetLabBaseline()
var staleE2EKeys = []string{
    "VXLAN_TUNNEL_MAP|vtep1|map_10700_Vlan700",
    "VLAN_MEMBER|Vlan700|Ethernet2",
    "VLAN|Vlan700",
    "VLAN_INTERFACE|Vlan800|10.80.0.1/24",
    "VLAN_INTERFACE|Vlan800",
    // ... all keys that tests may create
}
```

The baseline reset deletes these keys in reverse dependency order before
any tests run.

### Failure 4: Ping Fails Despite Correct ASIC_DB

**Symptom:** All CONFIG_DB and ASIC_DB checks pass, but `ping` between
servers fails.

**Cause:** This is the fundamental ngdpd limitation. ASIC_DB objects exist
but ngdpd doesn't implement a forwarding engine.

**Resolution:** Always use soft-fail for ping tests:

```go
if !testutil.ServerPing(t, "server1", "10.70.0.2", 5) {
    t.Log("ping failed (expected on virtual switch)")
    t.Skip("VXLAN data-plane forwarding not supported on virtual switch")
}
```

### Failure 5: tc Command Fails

**Symptom:** `tc` commands return "command not found" or fail silently.

**Cause:** `/usr/sbin/tc` is not in the regular PATH inside the SONiC VM.

**Resolution:** Always use the full path:

```bash
# Wrong:
tc -s filter show dev eth0 ingress

# Correct:
sudo /usr/sbin/tc -s filter show dev eth0 ingress
```

### Failure 6: Redis Connection Refused

**Symptom:** `redis-cli -h <mgmt_ip> -n 4 PING` fails with connection
refused.

**Cause:** Port 6379 is not forwarded by QEMU SLiRP networking. Only
explicitly listed TCP ports are forwarded (see `vrnetlab.py`
`mgmt_tcp_ports`).

**Resolution:** Use SSH tunnel:

```bash
# Via SSH command execution (for ad-hoc debugging):
sshpass -p cisco123 ssh cisco@<ip> "redis-cli -n 4 PING"

# Via SSH tunnel (for programmatic access):
# See pkg/device/tunnel.go -- NewSSHTunnel()
```

---

## 10. Debugging Recipes

### Recipe 1: Full ASIC_DB Dump

Get a complete picture of what the ASIC simulator has programmed:

```bash
ssh cisco@<ip> "
echo '=== VLAN objects ==='
for key in \$(redis-cli -n 1 KEYS 'ASIC_STATE:SAI_OBJECT_TYPE_VLAN:*'); do
    echo \"  \$key\"
    redis-cli -n 1 HGETALL \"\$key\" | paste - -
done

echo '=== Tunnel objects ==='
for key in \$(redis-cli -n 1 KEYS '*TUNNEL*'); do
    echo \"  \$key\"
done

echo '=== VRF objects ==='
redis-cli -n 1 KEYS '*VIRTUAL_ROUTER*'

echo '=== Router interfaces ==='
redis-cli -n 1 KEYS '*ROUTER_INTERFACE*'

echo '=== Bridge ports ==='
redis-cli -n 1 KEYS '*BRIDGE_PORT*' | wc -l
echo 'bridge port count'
" < /dev/null
```

### Recipe 2: CONFIG_DB to ASIC_DB Correlation

Verify that a CONFIG_DB entry was programmed into ASIC_DB:

```bash
ssh cisco@<ip> "
echo '=== CONFIG_DB: VLAN 700 ==='
redis-cli -n 4 HGETALL 'VLAN|Vlan700'

echo '=== ASIC_DB: VLAN objects ==='
for key in \$(redis-cli -n 1 KEYS 'ASIC_STATE:SAI_OBJECT_TYPE_VLAN:*'); do
    vid=\$(redis-cli -n 1 HGET \"\$key\" SAI_VLAN_ATTR_VLAN_ID)
    echo \"  OID: \$key  VID: \$vid\"
done

echo '=== CONFIG_DB: VXLAN Tunnel ==='
redis-cli -n 4 HGETALL 'VXLAN_TUNNEL|vtep1'

echo '=== ASIC_DB: Tunnel objects ==='
redis-cli -n 1 KEYS '*SAI_OBJECT_TYPE_TUNNEL:*'
" < /dev/null
```

### Recipe 3: Convergence Timeline

Monitor ASIC_DB in real-time while writing to CONFIG_DB:

```bash
# Terminal 1: Watch ASIC_DB for new VLAN objects
ssh cisco@<ip> "while true; do
    count=\$(redis-cli -n 1 KEYS 'ASIC_STATE:SAI_OBJECT_TYPE_VLAN:*' | wc -l)
    echo \"\$(date +%H:%M:%S) VLAN count: \$count\"
    sleep 1
done" < /dev/null &

# Terminal 2: Write CONFIG_DB entry
ssh cisco@<ip> "redis-cli -n 4 HSET 'VLAN|Vlan700' vlanid 700"

# Watch Terminal 1 -- VLAN count should increment within 5 seconds
```

### Recipe 4: orchagent Log Analysis

Extract orchagent's processing of a specific VLAN:

```bash
ssh cisco@<ip> "
docker exec swss cat /var/log/swss/orchagent.log | grep -i 'vlan.*700' | tail -20
docker exec swss cat /var/log/swss/orchagent.log | grep -i 'error\|fail\|warn' | tail -20
" < /dev/null
```

### Recipe 5: Full System Health Check

Run before debugging to establish baseline:

```bash
ssh cisco@<ip> "
echo '=== Services ==='
docker ps --format '{{.Names}}: {{.Status}}'

echo '=== orchagent ==='
docker exec swss supervisorctl status orchagent 2>/dev/null || echo 'swss container not running'

echo '=== Redis ==='
redis-cli PING

echo '=== CONFIG_DB key count ==='
redis-cli -n 4 DBSIZE

echo '=== ASIC_DB key count ==='
redis-cli -n 1 DBSIZE

echo '=== Interfaces ==='
ip link show | grep 'Ethernet\|swveth\|veth' | head -20

echo '=== ebtables ==='
sudo /usr/sbin/ebtables -L FORWARD 2>/dev/null || echo 'ebtables not available'
" < /dev/null
```

---

## 11. ASIC Convergence Timeouts by Topology

Different topologies have different convergence characteristics on VS:

| Topology | ASIC_DB Converges? | Typical Time | Recommended Timeout | Failure Mode |
|---|---|---|---|---|
| Simple VLAN | Yes | < 5s | 30s | Hard fail (`t.Fatal`) |
| VLAN + members | Yes | < 10s | 30s | Hard fail |
| VRF | Yes | < 5s | 30s | Hard fail |
| VXLAN tunnel | Usually | < 30s | 60s | Soft fail |
| L2VNI (VLAN + tunnel map) | Usually | < 30s | 60s | Soft fail |
| IRB (VRF + SVI + VNI) | Often not | May never | 30s | Soft fail (`t.Skip`) |
| EVPN Type-2 routes | Depends on FRR | 30-60s | 90s | Separate check |

### Why IRB Fails

The IRB topology requires a deep dependency chain in ASIC_DB:

```
CONFIG_DB writes:
  VRF|Vrf_tenant1
  VLAN|Vlan800
  VLAN_MEMBER|Vlan800|Ethernet2
  VLAN_INTERFACE|Vlan800 (VRF binding)
  VLAN_INTERFACE|Vlan800|10.80.0.1/24 (anycast gateway)
  VXLAN_TUNNEL_MAP|vtep1|map_10800_Vlan800

ASIC_DB dependency chain:
  VIRTUAL_ROUTER (VRF) ← must exist first
    └── ROUTER_INTERFACE (SVI for Vlan800) ← depends on VRF + VLAN
         └── VLAN ← must exist for SVI
              ├── BRIDGE_PORT (Ethernet2 member)
              └── TUNNEL_MAP_ENTRY (VNI 10800)
                   └── TUNNEL_MAP
                        └── TUNNEL (vtep1)
```

If orchagent processes these out of order or hits a timing issue with the VS
simulator, the chain may not complete. On real hardware, the ASIC SDK handles
these dependencies atomically; on VS, ngdpd's simplified implementation may
not.

---

## 12. Writing ASIC-Aware Tests

### Pattern: Three-Tier Verification

Every E2E test should follow this pattern:

```go
func TestFeature(t *testing.T) {
    // Tier 1: CONFIG_DB (must always work) — hard fail
    err := op.Execute(ctx)
    if err != nil {
        t.Fatalf("CONFIG_DB operation failed: %v", err)
    }

    // Verify CONFIG_DB entry
    testutil.AssertConfigDBEntry(t, nodeName, "VLAN", "Vlan700",
        map[string]string{"vlanid": "700"})

    // Tier 2: ASIC_DB convergence (may not work for complex topologies) — soft fail
    asicCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()
    if err := testutil.WaitForASICVLAN(asicCtx, t, nodeName, 700); err != nil {
        t.Logf("ASIC convergence failed: %v", err)
        t.Logf("ASIC_DB keys: %v", dumpASICKeys(t, nodeName))
        t.Skip("ASIC convergence not supported on virtual switch for this topology")
    }

    // Tier 3: Data plane (never works on VS) — soft fail
    if !testutil.ServerPing(t, "server1", "10.70.0.2", 5) {
        t.Log("Data-plane ping failed (expected on virtual switch)")
        t.Skip("Data-plane forwarding not supported on virtual switch")
    }
}
```

### Pattern: Always Log Before Skip

Never write a bare `t.Skip()`. Always capture diagnostic state first:

```go
// BAD
if err != nil {
    t.Skip("doesn't work on VS")
}

// GOOD
if err != nil {
    t.Logf("ASIC convergence error: %v", err)
    t.Logf("CONFIG_DB state: %s", dumpConfigDB(t, nodeName))
    t.Logf("ASIC_DB keys: %s", dumpASICKeys(t, nodeName))
    t.Skipf("ASIC convergence failed (expected on VS): %v", err)
}
```

### Pattern: Cleanup in Reverse Dependency Order

ASIC_DB objects have dependencies. Cleanup must delete in reverse order to
avoid orchagent errors:

```go
t.Cleanup(func() {
    // Reverse of creation order:
    // 1. Remove IP addresses (most specific)
    // 2. Remove VNI mapping
    // 3. Remove VLAN members
    // 4. Remove SVI / VLAN_INTERFACE
    // 5. Remove VLAN
    // 6. Remove VRF (least specific)

    // Use fresh device connection for cleanup (stale cache)
    dev := testutil.LabConnectedDevice(t, nodeName)
    dev.RemoveVLANInterfaceIP(ctx, 800, "10.80.0.1/24")
    dev.RemoveVLANInterface(ctx, 800)
    dev.UnmapL2VNI(ctx, "vtep1", "Vlan800")
    dev.RemoveVLANMember(ctx, 800, "Ethernet2")
    dev.DeleteVLAN(ctx, 800)
    dev.DeleteVRF(ctx, "Vrf_tenant1")
})
```

### Pattern: Baseline Reset

Before the test suite runs, clean up stale state from previous runs:

```go
// In TestMain or test setup:
func TestMain(m *testing.M) {
    testutil.ResetLabBaseline()  // Deletes known stale keys
    code := m.Run()
    testutil.CloseLabTunnels()
    os.Exit(code)
}
```

---

## 13. Diagnostic Commands Reference

### Quick Reference Card

| What to Check | Command |
|---|---|
| Is orchagent running? | `docker exec swss supervisorctl status orchagent` |
| Is syncd/ngdpd running? | `docker exec syncd supervisorctl status` |
| Redis responsive? | `redis-cli PING` |
| CONFIG_DB entry | `redis-cli -n 4 HGETALL "TABLE\|Key"` |
| ASIC_DB objects by type | `redis-cli -n 1 KEYS "*SAI_OBJECT_TYPE_VLAN*"` |
| ASIC_DB object attributes | `redis-cli -n 1 HGETALL "ASIC_STATE:SAI_...:oid:0x..."` |
| STATE_DB entry | `redis-cli -n 6 HGETALL "TABLE\|Key"` |
| orchagent errors | `docker exec swss cat /var/log/swss/orchagent.log \| grep error` |
| syncd errors | `docker exec syncd cat /var/log/syncd/syncd.log \| grep error` |
| Interface list | `ip link show` |
| veth pairs | `ip link show type veth` |
| tc redirect stats | `sudo /usr/sbin/tc -s filter show dev ethN ingress` |
| Bridge state | `bridge fdb show && bridge vlan show` |
| ebtables rules | `sudo /usr/sbin/ebtables -L` |
| Kernel routes | `ip route show` / `ip route show vrf Vrf_name` |
| VXLAN packets | `tcpdump -i any udp port 4789` |

### All Commands Require SSH

All commands above must be run inside the SONiC VM via SSH:

```bash
# Ad-hoc:
sshpass -p cisco123 ssh -o StrictHostKeyChecking=no cisco@<mgmt_ip> "<command>"

# From testlab setup.sh:
# Already uses this pattern with < /dev/null to prevent stdin consumption
```

### Redis Database Numbers

| DB | Name | Contents |
|---|---|---|
| 0 | APPL_DB | Application state (orchagent internal) |
| 1 | ASIC_DB | ASIC programming state (SAI objects) |
| 2 | COUNTERS_DB | Port/flow counters |
| 4 | CONFIG_DB | Desired configuration (primary for newtron) |
| 6 | STATE_DB | Operational/kernel state |

### Binary Paths Inside SONiC VM

| Binary | Path | Notes |
|---|---|---|
| `tc` | `/usr/sbin/tc` | NOT in user PATH; always use full path |
| `ip` | `/sbin/ip` | Usually in PATH |
| `redis-cli` | `/usr/bin/redis-cli` | In PATH |
| `bridge` | `/sbin/bridge` | Usually in PATH |
| `vtysh` | `/usr/bin/vtysh` | FRR CLI (inside bgp container) |
| `ebtables` | `/usr/sbin/ebtables` | May need sudo |
| `tcpdump` | `/usr/bin/tcpdump` | May need sudo |
