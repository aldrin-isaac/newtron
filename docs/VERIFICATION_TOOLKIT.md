# Verification Toolkit

Tooling architecture and verification patterns for any Linux-based network
system, including SONiC. These techniques were developed and validated during
newtron E2E test debugging.

## Table of Contents

- [1. Verification Layers](#1-verification-layers)
- [2. Layer 1: Configuration Verification](#2-layer-1-configuration-verification)
- [3. Layer 2: ASIC/Hardware Verification](#3-layer-2-asichardware-verification)
- [4. Layer 3: Kernel State Verification](#4-layer-3-kernel-state-verification)
- [5. Layer 4: Data-Plane Verification](#5-layer-4-data-plane-verification)
- [6. Layer 5: Protocol Verification](#6-layer-5-protocol-verification)
- [7. Debugging Decision Tree](#7-debugging-decision-tree)
- [8. Tool Reference](#8-tool-reference)
- [9. Automated Verification Patterns](#9-automated-verification-patterns)
- [10. CI/CD Integration](#10-cicd-integration)

---

## 1. Verification Layers

Network system verification operates in five layers. Each layer has
different tools, different trust levels, and different failure modes:

```
Layer 5: Protocol State      (BGP sessions, EVPN routes, LLDP)
Layer 4: Data-Plane          (ping, traceroute, iperf, packet capture)
Layer 3: Kernel State        (ip route, bridge fdb, tc filters, netfilter)
Layer 2: ASIC Programming    (ASIC_DB, SAI objects, forwarding tables)
Layer 1: Configuration       (CONFIG_DB, running-config, startup-config)
```

**Key insight from SONiC-VS debugging:** Layers 1 and 2 can be fully
programmed while Layers 3-5 don't work. Always verify layer by layer,
bottom-up.

### Trust Matrix

| Layer | SONiC-VS | SONiC HW | Generic Linux |
|---|---|---|---|
| Configuration | Full trust | Full trust | Full trust |
| ASIC | Partial (programmed but no forwarding) | Full trust | N/A |
| Kernel State | Partial (some fields lag) | Full trust | Full trust |
| Data-Plane | No trust (VXLAN/VRF broken) | Full trust | Full trust |
| Protocol | Full trust (FRR independent) | Full trust | Full trust |

---

## 2. Layer 1: Configuration Verification

### SONiC CONFIG_DB (Redis DB 4)

**Tools:** `redis-cli`, Go `redis` package, newtron `AssertConfigDBEntry`

```bash
# Verify entry exists
redis-cli -n 4 EXISTS 'VLAN|Vlan700'

# Verify field values
redis-cli -n 4 HGETALL 'VLAN|Vlan700'

# Verify multiple entries with pattern
redis-cli -n 4 KEYS 'VLAN_MEMBER|Vlan700|*'

# Count entries in a table
redis-cli -n 4 KEYS 'ACL_RULE|MY_TABLE|*' | wc -l

# Monitor writes in real-time
redis-cli -n 4 MONITOR
```

**Go verification:**
```go
// Exact field match
testutil.AssertConfigDBEntry(t, "leaf1", "VLAN", "Vlan700", map[string]string{
    "vlanid": "700",
    "admin_status": "up",
})

// Existence check
testutil.AssertConfigDBEntryExists(t, "leaf1", "VRF", "Vrf_e2e_irb")

// Absence check (after deletion)
testutil.AssertConfigDBEntryAbsent(t, "leaf1", "VLAN", "Vlan700")
```

### Generic Linux Configuration

```bash
# sysctl settings
sysctl net.ipv4.ip_forward
sysctl -a | grep -i forward

# Network interface config
ip -j link show eth0 | jq '.[0].mtu'
ip -j addr show eth0 | jq '.[0].addr_info'

# Routing table config
ip -j route show table main | jq '.'

# iptables/nftables rules
iptables -L -n -v
nft list ruleset

# systemd service state
systemctl is-active redis-server
```

---

## 3. Layer 2: ASIC/Hardware Verification

### SONiC ASIC_DB (Redis DB 1)

The ASIC_DB contains SAI (Switch Abstraction Interface) objects that
represent the ASIC's forwarding state.

```bash
# All ASIC objects
redis-cli -n 1 KEYS '*' | head -20

# VLAN objects
redis-cli -n 1 KEYS '*SAI_OBJECT_TYPE_VLAN*'

# Tunnel objects (VXLAN)
redis-cli -n 1 KEYS '*SAI_OBJECT_TYPE_TUNNEL*'

# Bridge port objects
redis-cli -n 1 KEYS '*SAI_OBJECT_TYPE_BRIDGE_PORT*'

# Router interface objects
redis-cli -n 1 KEYS '*SAI_OBJECT_TYPE_ROUTER_INTERFACE*'

# Read specific object fields
redis-cli -n 1 HGETALL 'ASIC_STATE:SAI_OBJECT_TYPE_VLAN:oid:...'
```

**Go verification:**
```go
func WaitForASICVLAN(ctx context.Context, t *testing.T, name string, vlanID int) error {
    client := testutil.LabRedisClient(t, name, 1)
    wantField := fmt.Sprintf("SAI_VLAN_ATTR_VLAN_ID")
    wantValue := fmt.Sprintf("%d", vlanID)

    for {
        select {
        case <-ctx.Done():
            return fmt.Errorf("ASIC_DB: VLAN %d not found on %s: %w", vlanID, name, ctx.Err())
        default:
        }
        keys, _ := client.Keys(ctx, "ASIC_STATE:SAI_OBJECT_TYPE_VLAN:*").Result()
        for _, key := range keys {
            val, _ := client.HGet(ctx, key, wantField).Result()
            if val == wantValue {
                return nil
            }
        }
        time.Sleep(500 * time.Millisecond)
    }
}
```

**Key insight:** On SONiC-VS, ASIC_DB objects ARE created (control-plane
programming works), but they don't result in actual data-plane forwarding.
ASIC_DB verification confirms orchagent processed the configuration, not
that packets will flow.

### Hardware Counters (Physical Switches Only)

```bash
# Port counters via COUNTERS_DB (DB 2)
redis-cli -n 2 HGETALL 'COUNTERS:oid:...'

# Via SONiC CLI
show interfaces counters

# Specific interface
show interfaces counters Ethernet0
```

---

## 4. Layer 3: Kernel State Verification

### Interface State

```bash
# Interface list with details
ip -d link show

# Specific interface
ip -j link show Ethernet0 | jq '.[0] | {mtu, operstate, flags}'

# Kernel counters (bypass ASIC)
cat /sys/class/net/Ethernet0/statistics/rx_packets
cat /sys/class/net/Ethernet0/statistics/tx_packets

# Bridge interfaces
bridge link show
bridge vlan show

# Bridge FDB (MAC address table)
bridge fdb show br Bridge

# VXLAN interfaces
ip -d link show type vxlan
bridge fdb show dev vtep1-700
```

### Routing State

```bash
# All routing tables
ip route show table all

# Specific VRF
ip route show vrf Vrf_e2e_irb

# ARP/neighbor table
ip neigh show

# VRF interfaces
ip link show type vrf
ip vrf show
```

### Traffic Control (tc)

```bash
# Show qdiscs
sudo /usr/sbin/tc qdisc show

# Show filters with statistics
sudo /usr/sbin/tc -s filter show dev eth3 ingress

# Show mirred redirect rules
sudo /usr/sbin/tc filter show dev eth3 ingress

# Verify packet counts on tc rules
sudo /usr/sbin/tc -s filter show dev swveth3 ingress
# Look for "Sent N bytes N pkt" to confirm packets matched
```

### Netfilter State

```bash
# iptables (IPv4)
sudo iptables -L -n -v --line-numbers

# ebtables (bridge-level filtering)
sudo ebtables -L --Lc

# Check for DROP rules that might block traffic
sudo ebtables -L | grep DROP
sudo iptables -L FORWARD -n -v | grep DROP

# Flush ebtables if blocking (careful in production)
sudo ebtables -F
```

### SONiC STATE_DB (Redis DB 6)

```bash
# Port operational state
redis-cli -n 6 HGETALL 'PORT_TABLE|Ethernet0'

# BGP neighbor state
redis-cli -n 6 HGETALL 'BGP_NEIGHBOR_TABLE|10.0.0.1'

# VXLAN tunnel state
redis-cli -n 6 HGETALL 'VXLAN_TUNNEL_TABLE|10.0.0.11'

# LAG state
redis-cli -n 6 HGETALL 'LAG_TABLE|PortChannel100'
```

**Go polling:**
```go
// Poll STATE_DB until value matches
err := testutil.PollStateDB(ctx, t, "leaf1",
    "BGP_NEIGHBOR_TABLE", "10.0.0.1", "state", "Established")
if err != nil {
    t.Skipf("BGP did not converge: %v", err)
}
```

---

## 5. Layer 4: Data-Plane Verification

### Ping (ICMP)

```bash
# Basic connectivity
ping -c 5 -W 2 10.70.0.2

# With source interface
ping -c 3 -I eth1 10.70.0.2

# With specific packet size (test MTU)
ping -c 3 -s 8000 -M do 10.70.0.2

# From a specific container
docker exec clab-spine-leaf-server1 ping -c 5 10.70.0.2
```

**Go implementation:**
```go
func ServerPing(t *testing.T, server, target string, count int) bool {
    output, err := ServerExec(t, server, "ping", "-c",
        fmt.Sprintf("%d", count), "-W", "2", target)
    if err != nil {
        t.Logf("ping %s→%s failed: %v\n%s", server, target, err, output)
        // Log diagnostics
        logServerDiagnostics(t, server)
        return false
    }
    return true
}

func logServerDiagnostics(t *testing.T, server string) {
    ifout, _ := ServerExec(t, server, "ip", "addr", "show", "eth1")
    t.Logf("%s interfaces:\n%s", server, ifout)
    rtout, _ := ServerExec(t, server, "ip", "route", "show")
    t.Logf("%s routes:\n%s", server, rtout)
    arpout, _ := ServerExec(t, server, "ip", "neigh", "show")
    t.Logf("%s ARP:\n%s", server, arpout)
}
```

### Packet Capture (tcpdump)

```bash
# Capture on specific interface inside SONiC VM
ssh cisco@<ip> "sudo tcpdump -i Ethernet2 -c 10 -nn"

# Capture VXLAN traffic
ssh cisco@<ip> "sudo tcpdump -i vtep1-700 -c 10 -nn"

# Capture ARP specifically
ssh cisco@<ip> "sudo tcpdump -i Ethernet2 -c 10 -nn arp"

# Capture on container interface
docker exec clab-spine-leaf-leaf1 tcpdump -i eth3 -c 10 -nn

# Save to file for analysis
ssh cisco@<ip> "sudo tcpdump -i Ethernet2 -c 100 -w /tmp/cap.pcap"
```

### Traceroute

```bash
# Trace path through VRF
docker exec clab-spine-leaf-server1 traceroute -n 10.90.2.2

# TCP traceroute (bypasses ICMP filtering)
docker exec clab-spine-leaf-server1 traceroute -T -n 10.90.2.2
```

### Performance Testing

```bash
# iperf3 server on one end
docker exec clab-spine-leaf-server2 iperf3 -s -D

# iperf3 client on other end
docker exec clab-spine-leaf-server1 iperf3 -c 10.70.0.2 -t 10 -J
```

---

## 6. Layer 5: Protocol Verification

### BGP

```bash
# Inside SONiC VM via FRR
ssh cisco@<ip> "vtysh -c 'show bgp summary'"
ssh cisco@<ip> "vtysh -c 'show bgp l2vpn evpn summary'"
ssh cisco@<ip> "vtysh -c 'show bgp l2vpn evpn route'"

# Specific neighbor
ssh cisco@<ip> "vtysh -c 'show bgp neighbor 10.0.0.1'"

# Advertised routes
ssh cisco@<ip> "vtysh -c 'show bgp l2vpn evpn route type 2'"
ssh cisco@<ip> "vtysh -c 'show bgp l2vpn evpn route type 5'"

# Route table
ssh cisco@<ip> "vtysh -c 'show ip route'"
ssh cisco@<ip> "vtysh -c 'show ip route vrf Vrf_e2e_irb'"
```

### EVPN/VXLAN

```bash
# VXLAN tunnel status (SONiC CLI)
ssh cisco@<ip> "show vxlan tunnel"

# VNI to VLAN mapping
ssh cisco@<ip> "show vxlan vlanvnimap"

# Remote VTEPs
ssh cisco@<ip> "show vxlan remotevtep"

# EVPN routes in FRR
ssh cisco@<ip> "vtysh -c 'show evpn vni'"
ssh cisco@<ip> "vtysh -c 'show evpn mac vni all'"
```

### LLDP

```bash
ssh cisco@<ip> "show lldp neighbors"
ssh cisco@<ip> "show lldp table"
```

---

## 7. Debugging Decision Tree

When a test fails, follow this decision tree:

```
Test Failed
│
├── Is it a CONFIG_DB assertion?
│   ├── YES → Check Redis: redis-cli -n 4 HGETALL 'TABLE|key'
│   │   ├── Key exists but wrong value → Bug in operation Execute()
│   │   ├── Key missing → Bug in operation Execute() or cleanup ran
│   │   └── Correct value but test fails → Bug in assertion helper
│   └── NO ↓
│
├── Is it an ASIC convergence timeout?
│   ├── YES → Check ASIC_DB: redis-cli -n 1 KEYS '*VLAN*'
│   │   ├── Object present → Orchagent processed it, may need more time
│   │   ├── Object missing → Orchagent didn't process (check syslog)
│   │   └── On VS: consider soft-fail (t.Skip)
│   └── NO ↓
│
├── Is it a STATE_DB assertion?
│   ├── YES → Compare CONFIG_DB vs STATE_DB
│   │   ├── CONFIG_DB correct, STATE_DB wrong → ASIC didn't apply (VS limitation)
│   │   ├── Both wrong → Operation didn't write correctly
│   │   └── STATE_DB correct but test fails → Timing issue (poll longer)
│   └── NO ↓
│
├── Is it a data-plane ping failure?
│   ├── YES → Check layer by layer:
│   │   1. Server IP configured? (ip addr show eth1)
│   │   2. ARP resolving? (ip neigh show)
│   │   3. Bridge forwarding? (bridge fdb show)
│   │   4. tc rules active? (tc -s filter show)
│   │   5. ASIC forwarding? (packet counters)
│   │   6. VXLAN encap? (tcpdump on vtep)
│   │   └── On VS: likely unsupported (t.Skip)
│   └── NO ↓
│
├── Is it a connection error?
│   ├── YES → Check:
│   │   1. Container running? (docker ps)
│   │   2. SSH accessible? (ssh cisco@<ip>)
│   │   3. Redis accessible? (redis-cli via SSH)
│   │   4. Tunnel alive? (check tunnel pool)
│   │   5. Profile IP correct? (cat profile.json)
│   └── NO → Read the actual error message carefully
```

---

## 8. Tool Reference

### Essential Tools

| Tool | Purpose | Install |
|---|---|---|
| `redis-cli` | Redis database inspection | `apt install redis-tools` |
| `sshpass` | Automated SSH authentication | `apt install sshpass` |
| `jq` | JSON parsing | `apt install jq` |
| `tcpdump` | Packet capture | Pre-installed in SONiC/netshoot |
| `ip` | Network interface/routing | `apt install iproute2` |
| `bridge` | Bridge state inspection | `apt install iproute2` |
| `tc` | Traffic control rules | Inside SONiC at `/usr/sbin/tc` |
| `iperf3` | Performance testing | `apt install iperf3` |
| `vtysh` | FRR CLI | Inside SONiC bgp container |

### Convenience Scripts

**redis-dump.sh** -- Dump all CONFIG_DB entries for a node:
```bash
#!/bin/bash
IP=$1; DB=${2:-4}
for key in $(sshpass -p cisco123 ssh -o StrictHostKeyChecking=no cisco@$IP \
    "redis-cli -n $DB KEYS '*'" 2>/dev/null); do
    echo "=== $key ==="
    sshpass -p cisco123 ssh -o StrictHostKeyChecking=no cisco@$IP \
        "redis-cli -n $DB HGETALL '$key'" 2>/dev/null
done
```

**check-connectivity.sh** -- Verify all layers for a node:
```bash
#!/bin/bash
IP=$1
echo "=== Layer 1: CONFIG_DB ==="
sshpass -p cisco123 ssh cisco@$IP "redis-cli -n 4 DBSIZE" 2>/dev/null

echo "=== Layer 2: ASIC_DB ==="
sshpass -p cisco123 ssh cisco@$IP "redis-cli -n 1 DBSIZE" 2>/dev/null

echo "=== Layer 3: Kernel ==="
sshpass -p cisco123 ssh cisco@$IP "ip link show | grep 'state UP'" 2>/dev/null

echo "=== Layer 5: BGP ==="
sshpass -p cisco123 ssh cisco@$IP "vtysh -c 'show bgp summary' 2>/dev/null | tail -5"
```

---

## 9. Automated Verification Patterns

### Pre-Test Health Check

Run before every test suite to catch infrastructure issues early:

```go
func verifyLabHealth(t *testing.T) {
    nodes := testutil.LabSonicNodes(t)
    for _, node := range nodes {
        // Layer 1: CONFIG_DB accessible
        client := testutil.LabRedisClient(t, node.Name, 4)
        if _, err := client.Ping(ctx).Result(); err != nil {
            t.Fatalf("%s: Redis unreachable: %v", node.Name, err)
        }

        // Layer 1: Startup config loaded
        testutil.AssertConfigDBEntryExists(t, node.Name, "DEVICE_METADATA", "localhost")

        // Layer 5: BGP config present (if expected)
        if isLeaf(node.Name) {
            testutil.AssertConfigDBEntryExists(t, node.Name, "BGP_GLOBALS", "default")
        }
    }
}
```

### Post-Test State Audit

Verify no stale state after test cleanup:

```go
func auditStaleState(t *testing.T) {
    stalePatterns := []string{
        "VLAN|Vlan5*", "VLAN|Vlan6*", "VLAN|Vlan7*", "VLAN|Vlan8*",
        "PORTCHANNEL|PortChannel2*",
        "VRF|Vrf_e2e_*",
        "ACL_TABLE|E2E_*",
    }
    nodes := testutil.LabSonicNodes(t)
    for _, node := range nodes {
        client := testutil.LabRedisClient(t, node.Name, 4)
        for _, pattern := range stalePatterns {
            keys, _ := client.Keys(ctx, pattern).Result()
            if len(keys) > 0 {
                t.Errorf("%s: stale keys matching %s: %v", node.Name, pattern, keys)
            }
        }
    }
}
```

### Continuous Verification

For long-running tests or soak tests:

```go
func monitorBGPState(ctx context.Context, t *testing.T, name string) {
    ticker := time.NewTicker(10 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            entry := testutil.LabStateDBEntry(t, name, "BGP_NEIGHBOR_TABLE", "10.0.0.1")
            if entry["state"] != "Established" {
                t.Logf("WARNING: BGP session on %s is %s (not Established)", name, entry["state"])
            }
        }
    }
}
```

---

## 10. CI/CD Integration

### Test Pipeline

```yaml
stages:
  - lint
  - unit
  - integration
  - e2e

unit:
  script:
    - go test ./...
  artifacts:
    - coverage.out

integration:
  script:
    - make test-integration
  services:
    - redis:7-alpine

e2e:
  script:
    - make test-e2e-full
  artifacts:
    - testlab/.generated/e2e-report.md
    - testlab/.generated/e2e-results.txt
  timeout: 30m
```

### Report Parsing

```bash
# Extract pass/fail counts from report
grep -E '^\| \*\*(Passed|Failed|Skipped)' testlab/.generated/e2e-report.md

# Check for any failures
if grep -q '| FAIL |' testlab/.generated/e2e-report.md; then
    echo "E2E tests have failures"
    exit 1
fi

# Count by status
echo "PASS: $(grep -c '| PASS |' testlab/.generated/e2e-report.md)"
echo "FAIL: $(grep -c '| FAIL |' testlab/.generated/e2e-report.md)"
echo "SKIP: $(grep -c '| SKIP |' testlab/.generated/e2e-report.md)"
```

### Notification on Failure

```bash
# If E2E tests fail, dump diagnostics
if [ $E2E_RC -ne 0 ]; then
    echo "=== E2E Report ==="
    cat testlab/.generated/e2e-report.md

    echo "=== Failed Tests ==="
    grep -B2 -A10 'FAIL' testlab/.generated/e2e-results.txt

    echo "=== Lab Status ==="
    make lab-status
fi
```
