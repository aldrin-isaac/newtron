# SONiC-VS Pitfalls and Tripping Hazards

This document catalogs every known pitfall, limitation, and tripping hazard when
developing and testing against SONiC Virtual Switch (VS) images. It is written
from hard-won experience building newtron's test infrastructure against
CONFIG_DB over Redis.

---

## 1. ASIC Simulator (ngdpd) Capabilities and Limitations

The SONiC-VS image ships with **ngdpd** (also called `vssyncd` in some builds)
as a stand-in for a real ASIC SDK. It is a **control-plane-only** simulator.

### What ngdpd Does

- Programs **ASIC_DB** (Redis DB 1) in response to orchagent requests.
- Accepts SAI object creation calls (e.g., `SAI_OBJECT_TYPE_VLAN`,
  `SAI_OBJECT_TYPE_BRIDGE_PORT`, `SAI_OBJECT_TYPE_TUNNEL`) and returns valid
  object IDs.
- Allows orchagent's full CONFIG_DB -> APP_DB -> ASIC_DB pipeline to execute
  without errors.

### What ngdpd Does NOT Do

- **Does not forward data-plane packets.** There is no packet path from one
  Ethernet TAP interface to another through the simulated ASIC.
- **VXLAN encap/decap is absent.** ASIC_DB will contain tunnel objects, but no
  kernel datapath performs the actual UDP encapsulation.
- **L2 bridging across VNIs does not function.** Even though ASIC_DB shows
  bridge ports bound to VLANs and VNIs, frames injected on one port never
  egress another.
- **L3 routing through VRFs is control-plane only.** FRR installs routes into
  the kernel, but VRF-aware forwarding through the ASIC path does not work.
- **MTU changes are not applied to kernel TAP interfaces.** Writing MTU 9000 to
  CONFIG_DB succeeds, orchagent processes it, but the kernel interface retains
  its default MTU:

```bash
# Write MTU 9000 to CONFIG_DB
redis-cli -n 4 HSET "PORT|Ethernet0" mtu 9000

# Check STATE_DB -- still shows 9100 (default)
redis-cli -n 6 HGET "PORT_TABLE|Ethernet0" mtu
# "9100"

# Check kernel -- also unchanged
ip link show Ethernet0 | grep mtu
# ... mtu 9100 ...
```

- **Interface admin status** changes (`admin_status: up/down`) may or may not
  propagate to the kernel. On some VS builds, `ip link set EthernetN down` is
  never called even when CONFIG_DB says `admin_status: down`.

### The Packet Path (What Actually Exists)

The VS wiring uses veth pairs and tc redirect rules:

```
Container ethN  <-->  tc mirred redirect  <-->  swvethN  <-->  vethN  -->  ngdpd
```

- `ethN` is the container-side network interface.
- `swvethN` / `vethN` is a veth pair created by the VS startup scripts.
- tc redirect rules shuttle packets between `ethN` and `swvethN`.
- ngdpd listens on the `vethN` side but **does not forward packets back out**
  to `EthernetN` TAP interfaces.

You can verify this wiring:

```bash
# List veth pairs
ip link show type veth

# Check tc redirect rules
sudo /usr/sbin/tc -s filter show dev eth0 ingress
# filter protocol all pref 1 flower chain 0
#   action order 1: mirred (Egress Redirect to device swveth0) ...
#   Sent 1234 bytes 5 pkt (act_hit 5 ...)
```

Packets are counted as "act_hit" but they vanish inside ngdpd.

---

## 2. CONFIG_DB vs STATE_DB Divergence

This is the single most important thing to understand for writing correct tests.

### The Three Databases

| Database   | Redis DB | Contents                        | Reliability on VS          |
|------------|----------|---------------------------------|----------------------------|
| CONFIG_DB  | 4        | Desired configuration           | **Always reliable**        |
| STATE_DB   | 6        | Kernel/operational state        | **Unreliable for ASIC features** |
| ASIC_DB    | 1        | ASIC programming state          | **Useful for convergence checks** |

### CONFIG_DB (DB 4) -- Always Reliable

CONFIG_DB is a write-through store. Whatever you write is what you read back.
There is no kernel or ASIC dependency.

```bash
# Write
redis-cli -n 4 HSET "VLAN|Vlan700" vlanid 700

# Read -- always returns exactly what was written
redis-cli -n 4 HGETALL "VLAN|Vlan700"
# 1) "vlanid"
# 2) "700"
```

**Rule: All configuration correctness tests MUST verify CONFIG_DB.**

### STATE_DB (DB 6) -- Unreliable for ASIC-Dependent Features

STATE_DB reflects what the kernel and ASIC drivers report. On VS, ASIC-dependent
features never fully converge.

```bash
# CONFIG_DB says MTU 9000
redis-cli -n 4 HGET "PORT|Ethernet0" mtu
# "9000"

# STATE_DB still says 9100
redis-cli -n 6 HGET "PORT_TABLE|Ethernet0" mtu
# "9100"
```

Specific divergence cases:

- **MTU**: CONFIG_DB value is ignored by the kernel; STATE_DB shows default.
- **VLAN membership operational state**: STATE_DB may not show members for
  VLANs that have complex VXLAN/VNI bindings.
- **VXLAN tunnel state**: `VXLAN_TUNNEL_TABLE` in STATE_DB may remain empty
  even when CONFIG_DB has a fully defined tunnel.
- **Interface counters**: `COUNTERS_DB` (DB 2) shows zero for all packet
  counters since the ASIC never forwards.

### STATE_DB -- What IS Reliable

Features that converge independently of the ASIC work fine:

- **BGP session state** (populated by FRR via bgpcfgd, not the ASIC).
- **Interface existence** (TAP interfaces are created by VS startup scripts).
- **LLDP neighbor info** (lldpd runs in the kernel, not via the ASIC).

### ASIC_DB (DB 1) -- Useful for Convergence Checks

Even on VS, ASIC_DB is populated by orchagent. This lets you verify that the
CONFIG_DB -> orchagent -> ASIC_DB pipeline completed:

```bash
# Check if VLAN 700 reached ASIC_DB
redis-cli -n 1 KEYS "*SAI_OBJECT_TYPE_VLAN*"
# Returns OIDs if orchagent processed the VLAN

# Check VXLAN tunnel objects
redis-cli -n 1 KEYS "*SAI_OBJECT_TYPE_TUNNEL*"
```

**Rule: Use ASIC_DB convergence checks as a proxy for "orchagent processed this
correctly." Do NOT assume ASIC_DB presence means the data plane works.**

---

## 3. Binary Path Issues

### The `/usr/sbin` Problem

SONiC-VS containers do not include `/usr/sbin` in the default `$PATH` for
non-root users or for scripts executed via SSH.

```bash
# This fails silently or with "command not found"
tc filter show dev eth0 ingress
# bash: tc: command not found

# This works
sudo /usr/sbin/tc filter show dev eth0 ingress
```

### Affected Binaries

| Binary     | Path                    | Notes                            |
|------------|-------------------------|----------------------------------|
| `tc`       | `/usr/sbin/tc`          | Required for all traffic control |
| `ebtables` | `/usr/sbin/ebtables`    | Bridge-level packet filtering    |
| `brctl`    | `/usr/sbin/brctl`       | Legacy bridge management         |
| `ip`       | `/usr/sbin/ip` or `/sbin/ip` | Usually in PATH, but verify |
| `ethtool`  | `/usr/sbin/ethtool`     | Interface diagnostics            |

### Defensive Scripting

Always verify binary availability before use and never suppress stderr
blindly:

```bash
# BAD: hides the real error
tc filter show dev eth0 ingress 2>/dev/null

# BAD: "command not found" is silently eaten
result=$(tc filter show dev eth0 ingress 2>/dev/null)

# GOOD: use absolute path with sudo
result=$(sudo /usr/sbin/tc filter show dev eth0 ingress 2>&1)
if [ $? -ne 0 ]; then
    echo "tc failed: $result" >&2
    exit 1
fi
```

In Go test code (newtron context):

```go
// BAD
out, err := sshClient.Run("tc filter show dev eth0 ingress")

// GOOD
out, err := sshClient.Run("sudo /usr/sbin/tc filter show dev eth0 ingress")
if err != nil {
    t.Fatalf("tc command failed (is /usr/sbin/tc available?): %v, output: %s", err, out)
}
```

### stderr Suppression Anti-Pattern

A common trap: a script uses `2>/dev/null` to suppress expected warnings, but
this also hides "command not found" errors. The script appears to succeed but
produces empty output:

```bash
# Looks like no filters exist, but actually tc was never found
filters=$(tc -s filter show dev eth0 ingress 2>/dev/null)
echo "Filters: '$filters'"
# Filters: ''   <-- misleading, looks like zero filters
```

Always separate expected stderr from unexpected errors:

```bash
filters=$(sudo /usr/sbin/tc -s filter show dev eth0 ingress 2>&1) || {
    echo "FATAL: tc command failed: $filters" >&2
    exit 1
}
```

---

## 4. VXLAN Data-Plane Limitations (Proven)

This section documents what was tested and proven not to work. These are not
theoretical limitations -- every approach below was attempted.

### tc mirred Redirect Does Match

Packets injected into `ethN` via tc do get redirected to `swvethN`. This is
confirmed by tc statistics:

```bash
sudo /usr/sbin/tc -s filter show dev eth0 ingress
# filter protocol all pref 1 flower chain 0
#   action order 1: mirred (Egress Redirect to device swveth0) ...
#   Sent 4560 bytes 10 pkt (dropped 0, overlimits 0 requeues 0)
```

The packets are counted. They reach `swvethN`. **But they go no further.**

### Kernel Bridge Does NOT Forward tc-Injected Packets

Even when adding interfaces directly to the kernel bridge with the correct
VLAN:

```bash
# Add eth0 directly to Bridge as an access port in VLAN 700
sudo ip link set eth0 master Bridge
sudo bridge vlan add vid 700 dev eth0 pvid untagged

# Check bridge membership
bridge vlan show
# eth0     700 PVID Egress Untagged

# Check for vtep interface
ip link show vtep1-700
# vtep1-700 exists (created by orchagent)

# Inject a packet on eth0
# ... packet appears in tcpdump on eth0 ...
# ... packet does NOT appear on vtep1-700 ...
```

### ASIC_DB Has the Objects, Data Plane Does Not

```bash
# VXLAN tunnel object exists
redis-cli -n 1 KEYS "*TUNNEL*"
# 1) "ASIC_STATE:SAI_OBJECT_TYPE_TUNNEL:oid:0x..."
# 2) "ASIC_STATE:SAI_OBJECT_TYPE_TUNNEL_MAP:oid:0x..."
# 3) "ASIC_STATE:SAI_OBJECT_TYPE_TUNNEL_MAP_ENTRY:oid:0x..."

# Tunnel termination exists
redis-cli -n 1 KEYS "*TUNNEL_TERM*"
# 1) "ASIC_STATE:SAI_OBJECT_TYPE_TUNNEL_TERM_TABLE_ENTRY:oid:0x..."
```

These objects prove that orchagent processed the configuration correctly. But
ngdpd does not implement a data-plane forwarding engine behind these objects.

### Summary

```
CONFIG_DB write  -->  orchagent processes  -->  ASIC_DB populated  -->  STOP
                                                                       |
                                                           ngdpd does NOT
                                                           forward packets
```

**Documentation confirms: "SONiC-VS is a control-plane simulator."** Any test
that depends on packets traversing the ASIC path (VXLAN, inter-VLAN routing,
L2 bridging across ports) must be skipped on VS.

---

## 5. ebtables and Packet Filtering

### Default Rules May Block Traffic

Some SONiC-VS images ship with default ebtables rules that drop ARP, broadcast,
or multicast traffic. This is a prerequisite to check before any data-plane
debugging (even though data-plane ultimately does not work on VS, clearing
ebtables removes one variable).

### Inspection

```bash
# Check all chains
sudo /usr/sbin/ebtables -L

# Example output showing restrictive rules:
# Bridge chain: FORWARD, entries: 2, policy: ACCEPT
# -p ARP -j DROP
# -d Broadcast -j DROP
```

### Clearing Rules

```bash
# Flush all chains
sudo /usr/sbin/ebtables -F FORWARD
sudo /usr/sbin/ebtables -F INPUT
sudo /usr/sbin/ebtables -F OUTPUT

# Verify
sudo /usr/sbin/ebtables -L
# Should show empty chains with ACCEPT policy
```

### In Test Code

```go
func clearEbtables(t *testing.T, client *ssh.Client) {
    t.Helper()
    for _, chain := range []string{"FORWARD", "INPUT", "OUTPUT"} {
        out, err := client.Run(fmt.Sprintf("sudo /usr/sbin/ebtables -F %s", chain))
        if err != nil {
            t.Logf("Warning: ebtables flush %s failed: %v (output: %s)", chain, err, out)
            // Non-fatal: ebtables may not be installed
        }
    }
}
```

### Important Caveat

Clearing ebtables is **necessary but not sufficient** for data-plane
forwarding. Even with all ebtables rules flushed, the ASIC simulator (ngdpd)
remains the real bottleneck. Packets still do not traverse the simulated ASIC.

---

## 6. FRR Configuration

### FRR is Independent of the ASIC Simulator

FRR (Free Range Routing) runs inside the `bgp` Docker container and operates
entirely in the kernel/control plane. It does **not** depend on ngdpd for BGP
session establishment or route exchange.

### What Works

- **BGP sessions establish** between SONiC-VS nodes (or between SONiC-VS and
  external FRR/GoBGP peers).
- **EVPN routes are exchanged** (Type-2 MAC/IP, Type-5 IP prefix).
- **Route installation into kernel** via zebra works.
- **VRF-aware BGP** sessions and route leaking work at the control-plane level.

### FRR Config vs config_db.json

FRR configuration and `config_db.json` (CONFIG_DB) are **separate systems**:

| Aspect              | config_db.json              | FRR config (frr.conf)         |
|---------------------|-----------------------------|-------------------------------|
| Managed by          | newtron via Redis           | SCP + vtysh or bgpcfgd        |
| Persistence         | Redis RDB on disk           | /etc/frr/frr.conf in bgp container |
| Reload mechanism    | `config reload`             | `vtysh -f /etc/frr/frr.conf`  |
| Scope               | Interfaces, VLANs, VXLAN,  | BGP, OSPF, route-maps,        |
|                     | ACLs, etc.                  | prefix-lists, EVPN            |

### Pushing FRR Config

```bash
# Copy config into the bgp container
docker cp frr.conf bgp:/etc/frr/frr.conf

# Apply without restarting
docker exec bgp vtysh -f /etc/frr/frr.conf

# Verify
docker exec bgp vtysh -c "show running-config"
```

Via SSH from a test harness:

```bash
# SCP the config file to the switch
scp -P 2222 frr.conf admin@localhost:/tmp/frr.conf

# Then on the switch
sudo docker cp /tmp/frr.conf bgp:/etc/frr/frr.conf
sudo docker exec bgp vtysh -f /etc/frr/frr.conf
```

### FRR Config Persistence Gotcha

FRR config does **not** survive a container restart unless you write it to the
correct path inside the container:

```bash
# This persists across bgp container restarts
docker exec bgp vtysh -c "write memory"

# Verify the saved config
docker exec bgp cat /etc/frr/frr.conf
```

If you only push via `vtysh -f` without `write memory`, a container restart
wipes your config back to whatever was baked into the image.

---

## 7. tc mirred redirect Details

### Purpose

The tc mirred redirect rules are the glue between the container's network
interfaces (`ethN`) and the ASIC simulator's virtual switch ports (`swvethN`).
Without these rules, packets on `ethN` never reach ngdpd at all.

### Setup Sequence

```bash
# Step 1: Create clsact qdisc on ethN (required before adding filters)
sudo /usr/sbin/tc qdisc add dev eth0 clsact

# Step 2: Add ingress redirect from eth0 to swveth0
sudo /usr/sbin/tc filter add dev eth0 ingress \
    protocol all flower \
    action mirred egress redirect dev swveth0

# Step 3: Create clsact qdisc on swveth0
sudo /usr/sbin/tc qdisc add dev swveth0 clsact

# Step 4: Add ingress redirect from swveth0 back to eth0
sudo /usr/sbin/tc filter add dev swveth0 ingress \
    protocol all flower \
    action mirred egress redirect dev eth0
```

**Both directions are required.** Without the return path (swvethN -> ethN),
any response packets from ngdpd (if they existed) would never reach the
container interface.

### Verifying Rules Exist

```bash
# Check ingress filters on eth0
sudo /usr/sbin/tc -s filter show dev eth0 ingress

# Expected output:
# filter protocol all pref 49152 flower chain 0
#   filter protocol all pref 49152 flower chain 0 handle 0x1
#     not_in_hw
#     action order 1: mirred (Egress Redirect to device swveth0) stolen
#     index 1 ref 1 bind 1 installed 3600 sec used 1 sec
#     Action statistics:
#     Sent 12345 bytes 42 pkt (dropped 0, overlimits 0 requeues 0)
#     backlog 0b 0p requeues 0
```

### Common Failures

**Missing clsact qdisc:**

```bash
sudo /usr/sbin/tc filter add dev eth0 ingress flower action mirred egress redirect dev swveth0
# Error: Cannot find specified qdisc on specified device.
# Fix: add clsact first
sudo /usr/sbin/tc qdisc add dev eth0 clsact
```

**Interface not found:**

```bash
sudo /usr/sbin/tc filter add dev eth0 ingress flower action mirred egress redirect dev swveth0
# Error: Cannot find device "swveth0"
# Fix: VS startup may not have created the veth pair yet. Wait or restart syncd.
```

### Interpreting tc Stats

```bash
sudo /usr/sbin/tc -s filter show dev eth0 ingress
# Sent 4560 bytes 10 pkt (dropped 0, overlimits 0 requeues 0)
```

- **`Sent X bytes Y pkt`**: Packets that matched the filter and were redirected.
- **`dropped 0`**: No packets dropped by the tc action itself.
- **This does NOT mean the packets were forwarded by the ASIC.** It only means
  tc successfully redirected them to `swvethN`. After that, ngdpd receives them
  but does not forward.

---

## 8. Testing Strategy for VS

### Three Failure Modes

Tests against SONiC-VS must distinguish between three categories of failure:

#### Hard Fail: `t.Fatal` / `t.Fatalf`

Use for CONFIG_DB operations that must always work on VS:

```go
func TestVLANCreate(t *testing.T) {
    // Write to CONFIG_DB
    err := client.CreateVLAN(ctx, 700)
    if err != nil {
        t.Fatalf("CONFIG_DB VLAN creation must work on VS: %v", err)
    }

    // Read back from CONFIG_DB
    vlan, err := client.GetVLAN(ctx, 700)
    if err != nil {
        t.Fatalf("CONFIG_DB VLAN read must work on VS: %v", err)
    }
    if vlan.VlanID != 700 {
        t.Fatalf("CONFIG_DB VLAN ID mismatch: got %d, want 700", vlan.VlanID)
    }
}
```

#### Soft Fail: `t.Skip` / `t.Skipf`

Use for data-plane or ASIC convergence that is known not to work on VS:

```go
func TestVXLANDataPlane(t *testing.T) {
    // Setup CONFIG_DB (this part must work)
    err := client.CreateVXLANTunnel(ctx, tunnel)
    if err != nil {
        t.Fatalf("CONFIG_DB tunnel creation failed: %v", err)
    }

    // Wait for ASIC convergence (may not happen on VS)
    err = WaitForASICTunnel(ctx, client, tunnel.Name, 30*time.Second)
    if err != nil {
        t.Logf("ASIC tunnel convergence failed (expected on VS): %v", err)
        t.Logf("ASIC_DB keys: %v", dumpASICKeys(ctx, client, "TUNNEL"))
        t.Skip("Skipping data-plane test: ASIC tunnel did not converge (SONiC-VS limitation)")
    }

    // Data-plane test would go here (never reached on VS)
}
```

#### Diagnostic: `t.Log` / `t.Logf`

Use to capture useful debugging information regardless of pass/fail:

```go
func TestIRBConvergence(t *testing.T) {
    // ... setup ...

    // Always log diagnostic state
    t.Logf("CONFIG_DB state: %+v", dumpConfigDB(ctx, client))
    t.Logf("ASIC_DB VLAN keys: %v", dumpASICKeys(ctx, client, "VLAN"))
    t.Logf("ASIC_DB TUNNEL keys: %v", dumpASICKeys(ctx, client, "TUNNEL"))
    t.Logf("STATE_DB port state: %v", dumpStateDB(ctx, client, "PORT_TABLE"))

    // ... assertions ...
}
```

### Always Log Before Skip

**Never write a bare `t.Skip`.** Always log diagnostic information first so that
if the VS behavior changes in a future image, you have data to understand what
happened:

```go
// BAD
if err != nil {
    t.Skip("doesn't work on VS")
}

// GOOD
if err != nil {
    t.Logf("ASIC convergence error: %v", err)
    t.Logf("CONFIG_DB state: %s", configDump)
    t.Logf("ASIC_DB keys: %s", asicDump)
    t.Skipf("ASIC convergence failed (expected on VS for IRB topology): %v", err)
}
```

### ASIC Convergence Timeouts

The `WaitForASICVLAN` pattern (polling ASIC_DB until an object appears) works
differently for simple vs. complex topologies:

| Topology            | ASIC_DB Convergence on VS | Recommended Action       |
|---------------------|---------------------------|--------------------------|
| Simple VLAN         | Usually works (< 5s)      | Hard fail if timeout     |
| VLAN + members      | Usually works (< 10s)     | Hard fail if timeout     |
| VRF                 | Usually works (< 5s)      | Hard fail if timeout     |
| VXLAN tunnel        | May work (< 30s)          | Soft fail if timeout     |
| IRB (VRF+SVI+VNI)  | Often does not converge   | Soft fail if timeout     |
| EVPN Type-2 routes  | Depends on FRR, not ASIC  | Separate FRR check       |

```go
func WaitForASICVLAN(ctx context.Context, client RedisClient, vlanID int, timeout time.Duration) error {
    deadline := time.Now().Add(timeout)
    pattern := fmt.Sprintf("ASIC_STATE:SAI_OBJECT_TYPE_VLAN:*")

    for time.Now().Before(deadline) {
        keys, err := client.Keys(ctx, 1, pattern) // DB 1 = ASIC_DB
        if err != nil {
            return fmt.Errorf("redis keys query failed: %w", err)
        }
        for _, key := range keys {
            attrs, _ := client.HGetAll(ctx, 1, key)
            if attrs["SAI_VLAN_ATTR_VLAN_ID"] == strconv.Itoa(vlanID) {
                return nil // Converged
            }
        }
        time.Sleep(500 * time.Millisecond)
    }
    return fmt.Errorf("ASIC_DB VLAN %d did not appear within %v", vlanID, timeout)
}
```

---

## 9. What Works Reliably on VS

These features are safe to test with hard failures (`t.Fatal`):

### CONFIG_DB CRUD Operations

All CONFIG_DB writes and reads work perfectly. The Redis layer has no ASIC
dependency.

```go
// All of these are safe to t.Fatal on VS:
client.CreateVLAN(ctx, 700)
client.CreateVLANMember(ctx, 700, "Ethernet0", "untagged")
client.CreateLAG(ctx, "PortChannel1")
client.CreateLAGMember(ctx, "PortChannel1", "Ethernet0")
client.CreateVRF(ctx, "Vrf_tenant1")
client.CreateVXLANTunnel(ctx, "vtep1", "10.0.0.1")
client.CreateVXLANMap(ctx, "vtep1", "Vlan700", 10700)
client.CreateACLTable(ctx, "DATAACL", "L3", []string{"Ethernet0"})
client.CreateACLRule(ctx, "DATAACL", "RULE_1", rule)
```

### BGP Sessions via FRR

FRR operates independently. BGP sessions establish and routes are exchanged:

```bash
# Verify BGP session state
docker exec bgp vtysh -c "show bgp summary"
# Neighbor     V   AS   MsgRcvd  MsgSent  Up/Down  State/PfxRcd
# 10.0.0.2     4   65002   100      100   01:00:00  5

# Verify EVPN routes
docker exec bgp vtysh -c "show bgp l2vpn evpn"
# Route Distinguisher: 10.0.0.1:2
# *> [2]:[0]:[48]:[aa:bb:cc:dd:ee:ff]  10.0.0.1  ...
# *> [5]:[0]:[24]:[192.168.1.0]         10.0.0.1  ...
```

### Redis Connectivity via SSH Tunnels

The standard newtron pattern of SSH tunnel -> Redis works reliably:

```go
// Establish SSH tunnel to Redis
tunnel, err := ssh.NewTunnel(sshConfig, "localhost:6379")
if err != nil {
    t.Fatalf("SSH tunnel failed: %v", err)
}

// Connect Redis client through tunnel
rdb := redis.NewClient(&redis.Options{
    Addr: tunnel.LocalAddr(),
})

// All Redis operations work
err = rdb.Ping(ctx).Err()
if err != nil {
    t.Fatalf("Redis ping through SSH tunnel failed: %v", err)
}
```

### Interface Enumeration

Listing and inspecting interfaces via CONFIG_DB or the kernel works:

```bash
# CONFIG_DB: list all ports
redis-cli -n 4 KEYS "PORT|*"

# Kernel: list interfaces
ip link show

# Both are consistent for interface names (Ethernet0, Ethernet4, ...)
```

### ASIC_DB Programming (Simple Objects)

For simple objects (VLANs, bridge ports), ASIC_DB converges reliably:

```bash
# After creating VLAN 700 in CONFIG_DB, ASIC_DB shows:
redis-cli -n 1 KEYS "*SAI_OBJECT_TYPE_VLAN*"
# 1) "ASIC_STATE:SAI_OBJECT_TYPE_VLAN:oid:0x260000000005a2"

redis-cli -n 1 HGETALL "ASIC_STATE:SAI_OBJECT_TYPE_VLAN:oid:0x260000000005a2"
# 1) "SAI_VLAN_ATTR_VLAN_ID"
# 2) "700"
```

---

## 10. What Does NOT Work on VS

These features must use soft failures (`t.Skip`) or be excluded from VS test
runs entirely.

### VXLAN Encap/Decap

No packet encapsulation or decapsulation occurs. ASIC_DB has tunnel objects but
there is no kernel datapath to perform the UDP/VXLAN wrapping:

```bash
# Tunnel object exists in ASIC_DB
redis-cli -n 1 KEYS "*TUNNEL*"
# (objects present)

# But no VXLAN interface actually encapsulates
tcpdump -i any udp port 4789
# (silence -- no VXLAN packets)
```

### L2 Bridging Across VNIs

Frames sent on one VLAN-bound port do not egress on another port in the same
VLAN, because the bridge forwarding depends on the ASIC:

```bash
# On port Ethernet0 (VLAN 700):
tcpdump -i Ethernet0 -c 1 ether dst aa:bb:cc:dd:ee:ff
# (inject frame on Ethernet4, also VLAN 700)
# tcpdump never captures the frame on Ethernet0
```

### L3 Routing Through VRFs (Data Plane)

Kernel routing tables show VRF routes (installed by FRR/zebra), but packets
are not forwarded through the ASIC path:

```bash
# Routes exist in kernel VRF table
ip route show vrf Vrf_tenant1
# 192.168.1.0/24 dev Vlan700 proto bgp ...

# But ping through the VRF does not work
ip vrf exec Vrf_tenant1 ping -c 1 192.168.1.1
# (timeout -- no ASIC forwarding)
```

### MTU Application to Kernel TAP Interfaces

```bash
# Set MTU in CONFIG_DB
redis-cli -n 4 HSET "PORT|Ethernet0" mtu 9000

# Kernel interface unchanged
ip link show Ethernet0 | grep mtu
# mtu 9100  <-- still default
```

### ARP Suppression on VLAN Interfaces

ARP suppression requires the ASIC to intercept ARP requests and respond with
cached entries. On VS, ARP requests are not intercepted:

```bash
# Even with suppress_arp enabled in CONFIG_DB
redis-cli -n 4 HSET "SUPPRESS_VLAN_NEIGH|Vlan700" "" ""

# ARP requests still flood normally (no ASIC interception)
```

### Full STATE_DB Convergence for ASIC-Dependent Features

STATE_DB entries that depend on ASIC feedback will not converge:

```bash
# These STATE_DB entries may be missing or stale on VS:
redis-cli -n 6 HGETALL "VXLAN_TUNNEL_TABLE|vtep1"       # empty
redis-cli -n 6 HGETALL "VLAN_MEMBER_TABLE|Vlan700|Ethernet0"  # may be stale
```

### ASIC_DB Convergence for Complex IRB Topologies

When VRF + SVI + VXLAN VNI mapping are all configured together (IRB), orchagent
may not fully process all objects on VS:

```bash
# Some ASIC_DB objects may be missing for IRB:
redis-cli -n 1 KEYS "*SAI_OBJECT_TYPE_ROUTER_INTERFACE*"
# May not show the SVI (Vlan interface) router interface

redis-cli -n 1 KEYS "*SAI_OBJECT_TYPE_VIRTUAL_ROUTER*"
# VRF object may or may not appear
```

**Recommendation**: Use generous timeouts (30-60s) and soft-fail:

```go
err := WaitForASICVLAN(ctx, client, 700, 60*time.Second)
if err != nil {
    t.Logf("IRB ASIC convergence timed out (expected on VS): %v", err)
    t.Logf("ASIC_DB dump: %v", dumpAllASICKeys(ctx, client))
    t.Skip("Skipping: IRB ASIC convergence not supported on SONiC-VS")
}
```

---

## LabNodes vs LabSonicNodes

A subtle but important distinction in test helpers:

- **`LabNodes(t)`** returns ALL nodes: SONiC switches AND server containers
- **`LabSonicNodes(t)`** returns only SONiC nodes (excludes `kind: linux`)

**Gotcha:** Any helper that accesses Redis or SSH must use `LabSonicNodes`.
Server containers (e.g., `nicolaka/netshoot`) do not run Redis or sshd.
Using `LabNodes` for Redis operations will fail on server nodes with
connection refused / SSH timeout errors.

```go
// WRONG: will try to SSH-tunnel into server1, server2
for _, node := range testutil.LabNodes(t) {
    client := testutil.LabRedisClient(t, node.Name, 4)
    // FAILS on servers
}

// CORRECT: only SONiC nodes
for _, node := range testutil.LabSonicNodes(t) {
    client := testutil.LabRedisClient(t, node.Name, 4)
    // Works
}
```

This was a real bug in `WaitForLabRedis` -- it originally used `LabNodes`
and timed out trying to open SSH tunnels to server containers.

---

## Quick Reference: Test Decision Tree

```
Is the test verifying CONFIG_DB content?
  YES -> t.Fatal on failure (always works on VS)
  NO  -> continue

Is the test verifying BGP/FRR behavior?
  YES -> t.Fatal on failure (FRR is independent of ASIC)
  NO  -> continue

Is the test verifying ASIC_DB convergence?
  Simple object (VLAN, port)? -> t.Fatal with 10s timeout
  Complex object (IRB, VXLAN)? -> t.Skip with 60s timeout
  NO  -> continue

Is the test verifying data-plane forwarding?
  YES -> t.Skip with diagnostic logging (never works on VS)
  NO  -> continue

Is the test verifying STATE_DB?
  ASIC-dependent feature? -> t.Skip (unreliable on VS)
  Kernel-only feature?    -> t.Fatal (works on VS)
```

---

## Appendix: Useful Diagnostic Commands

```bash
# Dump all CONFIG_DB keys
redis-cli -n 4 KEYS "*"

# Dump all ASIC_DB keys (can be large)
redis-cli -n 1 KEYS "*" | head -50

# Check orchagent logs for errors
docker exec swss cat /var/log/swss/orchagent.log | tail -50

# Check syncd (ngdpd) logs
docker exec syncd cat /var/log/syncd/syncd.log | tail -50

# Verify all containers are running
docker ps --format "table {{.Names}}\t{{.Status}}"

# Check veth pair wiring
ip link show type veth | grep -E "(swveth|veth)" | head -20

# Check tc redirect rules on all interfaces
for i in 0 1 2 3; do
    echo "=== eth$i ==="
    sudo /usr/sbin/tc -s filter show dev eth$i ingress 2>/dev/null
done

# Check FRR BGP state
docker exec bgp vtysh -c "show bgp summary"
docker exec bgp vtysh -c "show bgp l2vpn evpn"

# Check kernel bridge state
bridge fdb show
bridge vlan show

# Check ebtables rules
sudo /usr/sbin/ebtables -L --Lc
```
