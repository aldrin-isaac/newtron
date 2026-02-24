# BGP Troubleshooting Workflow

## Incremental Debugging Methodology

**Golden Rule**: Start from the most basic layer and work upward. Do not proceed until each layer is verified.

## Layer 1: Physical/Link Layer

### 1.1 Verify Link Status
```bash
# SSH to device
newtlab ssh leaf1

# Check interface status
show interface status Ethernet0

# Expected: Oper = up, Admin = up
```

**Stop here if link is down.** Debug:
- Check QEMU NIC attachments (`ps -ef | grep qemu`)
- Verify bridge worker connections (`newtlab status`)
- Check kernel interface state (`ip link show`)

### 1.2 Verify MAC Addresses
```bash
# Check interface MAC
ip link show Ethernet0

# Check system MAC
redis-cli -n 4 HGET 'DEVICE_METADATA|localhost' 'mac'
```

**Expected**: All interfaces on the same device should have the same MAC as the system MAC.

**If MACs differ between devices**: Good! Each device should have a unique system MAC.

**If two devices have identical MACs**: This will cause L2 forwarding issues. Check QEMU command for `mac=` parameters.

## Layer 2: IP Layer

### 2.1 Assign Test IPs
```bash
# On leaf1
sudo config interface ip add Ethernet0 10.1.0.0/31

# On leaf2
sudo config interface ip add Ethernet0 10.1.0.1/31
```

### 2.2 Test Ping
```bash
# From leaf1
ping -c 3 -I Ethernet0 10.1.0.1
```

**Stop here if ping fails.** Debug:
- Check ARP resolution: `ip neigh show`
- Check interface IP assignment: `ip addr show Ethernet0`
- Capture packets: `tcpdump -i Ethernet0 icmp`

**Expected**: 0% packet loss, RTT 20-80ms typical for QEMU virtio

## Layer 3: BGP Session Establishment

### 3.1 Check BGP Configuration
```bash
# View CONFIG_DB BGP settings
redis-cli -n 4 HGETALL 'DEVICE_METADATA|localhost'

# View BGP neighbor config
redis-cli -n 4 HGETALL 'BGP_NEIGHBOR|default|<peer-ip>'

# Expected fields:
# - asn: remote AS number
# - local_addr: local IP for session
# - admin_status: up
# - ebgp_multihop: true (for loopback peerings)
```

### 3.2 Verify Router BGP ASN
```bash
# Check running FRR config
docker exec bgp vtysh -c 'show running-config' | grep 'router bgp'

# Should match DEVICE_METADATA bgp_asn
# router bgp 65011
```

**Critical Check**: Router's ASN must match what peer expects.

### 3.3 Check BGP Summary
```bash
docker exec bgp vtysh -c 'show bgp summary'
```

**Interpret State Column**:
- `Established` → Good! Session is up.
- `Active` → TCP connection failed (routing issue, wrong peer IP)
- `Connect` → TCP handshake in progress
- `Idle` → Session rejected or reset (see 3.4)
- `OpenSent/OpenConfirm` → OPEN message exchange (should transition quickly)

**If stuck in Idle > 30s**: BGP OPEN message was rejected. Proceed to 3.4.

### 3.4 Check BGP Neighbor Details (Idle State)
```bash
docker exec bgp vtysh -c 'show bgp neighbor <peer-ip>'
```

**Key fields to check**:
```
BGP state = Idle
Last reset <time>, <reason>
Connections established X; dropped Y
```

**Common Reset Reasons**:

| Reason | Meaning | Fix |
|--------|---------|-----|
| `Notification sent (OPEN Message Error/Bad Peer AS)` | Remote AS in OPEN ≠ configured ASN | Fix CONFIG_DB `asn` field to match peer's actual AS |
| `Connection refused` | Peer not listening on port 179 | Check FRR is running, firewall rules |
| `Connection timeout` | No IP route to peer | Check underlay routing (see 3.5) |
| `Hold Timer Expired` | Keepalives stopped | Network issue or peer crash |

**Bad Peer AS Deep Dive**:
```bash
# The hex dump in "Message received" shows the OPEN packet
# Bytes at offset 0x04-0x05 are the AS number in network byte order

# Example:
# 00660104 FDF400B4 ...
# ^^^^
# FDF4 = 65012 in hex

# If this doesn't match the configured "asn" in BGP_NEIGHBOR, FRR rejects it.
```

**Resolution**: Ensure CONFIG_DB BGP_NEIGHBOR asn matches the peer's DEVICE_METADATA bgp_asn.

### 3.5 Verify Underlay Routing (for loopback peerings)
```bash
# Check route to peer loopback
docker exec bgp vtysh -c 'show ip route 10.0.0.12'

# Expected: Route via underlay BGP or connected interface
# If missing: underlay BGP not converged or route not redistributed
```

## Layer 4: BGP Route Exchange

### 4.1 Check Received Routes
```bash
docker exec bgp vtysh -c 'show bgp ipv4 unicast neighbors <peer-ip> routes'
```

### 4.2 Check Advertised Routes
```bash
docker exec bgp vtysh -c 'show bgp ipv4 unicast neighbors <peer-ip> advertised-routes'
```

### 4.3 Check Route Redistribution
```bash
# Verify connected routes are being redistributed
docker exec bgp vtysh -c 'show running-config' | grep -A5 redistribute

# Expected:
# router bgp <asn>
#  redistribute connected
```

## Layer 5: FRR Logs

```bash
# Check for FRR errors
docker exec bgp cat /var/log/frr/frr.log | tail -50

# If file doesn't exist, logging may not be enabled
# Check journal logs
docker logs bgp | tail -50
```

**Common errors**:
- `Cannot have local-as same as BGP AS number` → Remove local-as from neighbor config
- `ebgp-multihop and ttl-security cannot be configured together` → Remove ttl-security
- `Bad Peer AS` → ASN mismatch (see 3.4)

## Common Issue Patterns

### Pattern 1: Direct Link Works, Loopback Peering Fails

**Symptoms**:
- Underlay BGP (10.1.0.0 ↔ 10.1.0.1): Established
- Overlay BGP (10.0.0.11 ↔ 10.0.0.12): Idle

**Root Cause**: CONFIG_DB has wrong peer AS for overlay neighbor.

**Fix**: Use peer's `underlay_asn` from its device profile. Both underlay and overlay use all-eBGP (see RCA-026).

**Example**:
- leaf1 runs AS 65011 (underlay_asn)
- leaf2 runs AS 65012 (underlay_asn)
- Underlay peering: 65011 ↔ 65012 (interface IPs)
- Overlay peering: 65011 ↔ 65012 (loopback IPs, same ASNs)

### Pattern 2: BGP Container Crash Loop

**Symptoms**:
- `docker ps` shows bgp container constantly restarting
- `docker logs bgp` shows FRR errors

**Root Cause**: Invalid FRR configuration rendered by frrcfgd.

**Debug**:
```bash
# Check CONFIG_DB for invalid BGP entries
redis-cli -n 4 KEYS 'BGP*'
redis-cli -n 4 HGETALL 'BGP_NEIGHBOR|default|<peer>'

# Check frrcfgd template rendering
docker exec bgp cat /etc/frr/frr.conf
```

### Pattern 3: System MAC Collision

**Symptoms**:
- Ping works initially, then fails intermittently
- ARP table shows MAC flapping

**Root Cause**: Multiple devices with same system MAC.

**Debug**:
```bash
# Check each device's MAC
newtlab ssh leaf1 "redis-cli -n 4 HGET 'DEVICE_METADATA|localhost' 'mac'"
newtlab ssh leaf2 "redis-cli -n 4 HGET 'DEVICE_METADATA|localhost' 'mac'"

# Check QEMU MACs
ps -ef | grep qemu | grep -o 'mac=[^ ]*'
```

**Fix**: Ensure each QEMU VM has unique MACs via `GenerateMAC()` function.

## Newtron Verification Methods

newtron provides structured verification primitives for automated checks:

```go
// Get route from APP_DB (FRR → SONiC)
route, err := node.GetRoute(ctx, "default", "10.0.0.11/32")

// Get route from ASIC_DB (SONiC → SAI)
asicRoute, err := node.GetRouteASIC(ctx, "default", "10.0.0.11/32")

// Verify a ChangeSet was applied correctly
err := cs.Verify(node)
if cs.Verification.Failed > 0 {
    // handle failed verification
}

// Check BGP neighbor existence
if node.BGPNeighborExists("10.0.0.12") {
    // neighbor is configured
}
```

## SONiC Redis Databases

- **DB 0 (APP_DB)**: Routes from FRR via fpmsyncd. Use `GetRoute()`.
- **DB 1 (ASIC_DB)**: SAI objects from orchagent. Use `GetRouteASIC()`.
- **DB 4 (CONFIG_DB)**: Device configuration. Use `GetEntry()` / `SetEntry()`.
- **DB 6 (STATE_DB)**: Operational state. Use for health checks.

## Timeouts and Expectations

| Operation | Expected Time | Red Flag |
|-----------|---------------|----------|
| Link up | < 5s | > 10s → hardware/driver issue |
| ARP resolution | < 1s | > 5s → L2 forwarding broken |
| BGP TCP connect | < 2s | > 10s → routing or firewall |
| BGP session establish | < 30s | > 2m → OPEN rejection or config error |
| Route installation (APP_DB) | < 5s | > 30s → fpmsyncd or orchagent stuck |
| Route installation (ASIC_DB) | < 10s | > 60s → SAI driver issue |

## When to Escalate

1. **Logs are clean, config is correct, but session won't establish** → Likely FRR bug or platform limitation
2. **ASIC_DB never updates despite APP_DB changes** → orchagent/syncd issue (check `docs/rca/`)
3. **BGP flaps continuously (Established → Idle → Established)** → Timer mismatch or keepalive loss

## Related RCAs

- `docs/rca/019-*`: BGP local-as conflicts with router ASN
- `docs/rca/020-*`: SONiC VPP port count must match NIC count
- `docs/rca/021-*`: SetIP requires both base and IP entries
- `docs/rca/022-*`: CiscoVS build issues and orchagent timeouts
