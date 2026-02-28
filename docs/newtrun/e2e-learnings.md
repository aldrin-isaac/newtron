# SONiC-VS Reference and E2E Learnings

Captured from the legacy `test/e2e/`, `internal/testutil/`, and debugging docs
before removal. These patterns inform newtrun scenario design and newtlab-based
testing.

---

## 1. SONiC Daemon Behavior

| Daemon | Watches | Writes | Crash Risk |
|--------|---------|--------|------------|
| orchagent | CONFIG_DB → ASIC_DB | ASIC_DB (DB 1) | Low |
| bgpcfgd | BGP_* in CONFIG_DB → FRR | — | Low |
| fpmsyncd | FRR routes → APP_DB | ROUTE_TABLE in APP_DB (DB 0) | Low |
| vxlanmgrd | VXLAN_* in CONFIG_DB | — | **High** — crashes on stale/orphaned entries |
| intfmgrd | INTERFACE/VLAN_INTERFACE | — | Low |

**Critical**: vxlanmgrd crashes if CONFIG_DB has a `VXLAN_TUNNEL_MAP` entry without
a corresponding `VLAN`. Always clean up VXLAN artifacts in reverse dependency order.

## 2. Virtual Switch (VS) Platform Quirks

- **Data plane**: VXLAN tunneling not supported. Ping tests must soft-fail (`t.Skip`), not `t.Fatal`.
- **ASIC_DB**: Updates lag hardware; use polling (30s timeout) to verify convergence.
- **BGP convergence**: May be slow or fail in VS; gracefully skip on failure.
- **Timing**: VS is slower than hardware — use 30–60s for ASIC, 3min for BGP.

## 3. Convergence Ordering

**VXLAN/EVPN sequence** (each step must complete before the next):

1. Create VLAN (CONFIG_DB)
2. Add VLAN_MEMBER (CONFIG_DB)
3. Map L2VNI (VXLAN_TUNNEL_MAP + SUPPRESS_VLAN_NEIGH)
4. Poll ASIC_DB until VLAN appears (30s timeout)
5. Configure servers, test data plane

If steps run without waiting, vxlanmgrd may see incomplete entries and crash.

## 4. Cleanup Ordering

Always reverse dependency order:

```
VXLAN_TUNNEL_MAP  →  (unmap VNI first)
SUPPRESS_VLAN_NEIGH
VLAN_MEMBER       →  (remove port from VLAN)
VLAN_INTERFACE    →  (IP binding, then base entry)
VLAN              →  (delete VLAN last)
VRF               →  (if L3VNI was created)
```

For multi-device tests, clean both devices but don't fail if one device can't connect
during cleanup.

## 5. Timeout Strategy

| Operation | Timeout | Notes |
|-----------|---------|-------|
| Device operations (SSH + Redis) | 2 min | SONiC-VS is slow |
| ASIC convergence (VLAN in DB 1) | 30 sec | orchagent is relatively fast |
| BGP neighbor Established | 3 min | BGP can be slow in lab |
| Cleanup operations | 30 sec | Should be quick |

## 6. CONFIG_DB Key Patterns

```
VLAN|Vlan500                              → empty hash (NULL:NULL convention)
VLAN_MEMBER|Vlan100|Ethernet2             → tagging_mode: "untagged" or "tagged"
VLAN_INTERFACE|Vlan100                    → vrf_name (base entry)
VLAN_INTERFACE|Vlan100|10.1.100.1/24      → IP binding (empty hash)
INTERFACE|Ethernet2                       → vrf_name (base entry)
INTERFACE|Ethernet2|10.99.2.1/30          → IP binding (empty hash)
VRF|customer-l3-Eth0                      → vni (empty if no L3VNI)
VXLAN_TUNNEL_MAP|vtep1|map_10700_Vlan700  → vlan: Vlan700, vni: 10700
SUPPRESS_VLAN_NEIGH|Vlan700               → suppress: on
NEWTRON_SERVICE_BINDING|Ethernet2         → service_name: l2-extend
SAG_GLOBAL|IPv4                           → gwmac: 00:00:00:01:02:03
ACL_TABLE|name                            → type, stage, ports (comma-separated, no spaces)
ACL_RULE|name|RULE_200                    → priority, packet_action, src_ip, ...
BGP_GLOBALS|default                       → local_asn, router_id
BGP_NEIGHBOR|10.0.0.1                     → asn, ...
BGP_NEIGHBOR_AF|10.0.0.1|ipv4_unicast    → route_reflector_client, ...
BGP_GLOBALS_AF|default|ipv4_unicast       → ...
BGP_GLOBALS_AF_NETWORK|default|ipv4_unicast|10.99.0.0/24  → ...
ROUTE_REDISTRIBUTE|default|static|bgp|ipv4 → ...
ROUTE_MAP|name|10                         → route_operation: permit, set_local_pref: 200
```

## 7. Empty Hash Convention

SONiC uses `NULL: NULL` as a sentinel for empty CONFIG_DB entries:

```go
// These are semantically equivalent:
fields == map[string]string{"NULL": "NULL"}  // stored form
fields == map[string]string{}                 // after filtering
```

Entries like VRF, VLAN (no fields), IP bindings, and PORTCHANNEL_MEMBER use this.

## 8. VXLAN Tunnel Naming

- **Leaf L2VNI**: `VXLAN_TUNNEL_MAP|vtep1|map_<VNI>_Vlan<ID>`
- **Leaf L3VNI** (VRF): `VXLAN_TUNNEL_MAP|vtep1|map_<L3VNI>_<VRF>`
- **NVO**: Always `VXLAN_EVPN_NVO|nvo1` with `source_vtep=vtep1`
- SONiC-VS supports only one VXLAN_TUNNEL per device

## 9. BGP State Polling

Poll `BGP_NEIGHBOR_TABLE|<ip>` in STATE_DB (DB 6), field `state`:
- Values: Active → Connect → OpenSent → OpenConfirm → **Established**
- Poll interval: 2 seconds
- On VS, sessions may never reach Established — skip gracefully

## 10. Verification Patterns

- **CONFIG_DB**: Direct Redis read after Apply; check key exists + field values match.
- **Fresh connections**: After modifying state, create a new device connection (cached
  ConfigDB becomes stale after Apply).
- **ASIC convergence**: Poll ASIC_DB for SAI objects (VLAN, routes) — confirms orchagent processed.
- **Data plane**: Run `ping` from server containers; soft-fail on VS.
- **Health checks**: Tolerate transient state — don't use them to verify immediate persistence.

## 11. ACL Port Binding

The `ports` field in `ACL_TABLE` is comma-separated with **no spaces**:
`"Ethernet1,Ethernet2"` not `"Ethernet1, Ethernet2"`.

## 12. Infrastructure Snapshot/Restore

Always snapshot infrastructure tables (DEVICE_METADATA, BGP_GLOBALS, LOOPBACK_INTERFACE)
before modifying, and restore in `t.Cleanup`. Corrupting these breaks subsequent tests.

## 13. Server Container Interaction

Servers are Linux containers (nicolaka/netshoot). Configure via SSH or `docker exec`:
```
ip addr flush dev eth1 && ip addr add 10.70.0.1/24 dev eth1 && ip link set eth1 up
```

On failure, capture diagnostics: `ip addr`, `ip route`, `arp -n`.

---

## 14. ASIC Simulator (ngdpd) Capabilities

SONiC-VS ships with `ngdpd` (also called `vssyncd`), a **control-plane-only** ASIC stub.

| Capability | Works on VS? | Notes |
|---|---|---|
| CONFIG_DB writes (any table) | Yes | All HSET operations succeed |
| CONFIG_DB → ASIC_DB pipeline | Yes | orchagent processes, objects appear in DB 1 |
| VXLAN tunnel objects in ASIC_DB | Yes | Objects present, no data-plane encap |
| BGP sessions via FRR | Yes | FRR runs independently of ASIC |
| EVPN route exchange (BGP) | Yes | BGP EVPN address family works |
| L2 bridging (packet forwarding) | **No** | Frames don't egress between ports |
| VXLAN encap/decap | **No** | No UDP/VXLAN wrapping occurs |
| L3 VRF routing through ASIC | **No** | Kernel routes exist, ASIC doesn't forward |
| MTU application to kernel TAP | **No** | CONFIG_DB=9000, kernel stays 9100 |
| ARP suppression | **No** | Requires ASIC interception |

**Fundamental rule**: `CONFIG_DB write → orchagent → ASIC_DB populated → STOP`.
ngdpd does NOT forward packets. Test CONFIG_DB and ASIC_DB for correctness;
never expect data-plane forwarding.

## 15. Binary Paths Inside SONiC VM

`/usr/sbin` is **not in the default PATH** for non-root SSH users.

| Binary | Path | Notes |
|---|---|---|
| `tc` | `/usr/sbin/tc` | Always use full path + sudo |
| `ebtables` | `/usr/sbin/ebtables` | May need sudo |
| `ip` | `/sbin/ip` | Usually in PATH |
| `redis-cli` | `/usr/bin/redis-cli` | In PATH |
| `bridge` | `/sbin/bridge` | Usually in PATH |
| `vtysh` | `/usr/bin/vtysh` | FRR CLI (inside bgp container) |

**Anti-pattern**: Never suppress stderr with `2>/dev/null` for critical commands.
A missing binary's "command not found" gets swallowed, producing empty output that
looks like "no results" instead of an error.

## 16. CONFIG_DB vs STATE_DB Divergence

| Feature | CONFIG_DB (DB 4) | STATE_DB (DB 6) | Why |
|---|---|---|---|
| MTU | 9000 (as written) | 9100 (default) | ASIC doesn't apply to kernel TAP |
| VLAN membership | Present | May be absent | Complex VXLAN bindings don't converge |
| VXLAN tunnel state | Fully defined | Empty | No kernel datapath for VXLAN |
| Interface counters (DB 2) | N/A | Zero | ASIC never forwards packets |
| BGP neighbor state | Config present | **Reliable** | FRR is independent of ASIC |
| Interface existence | Port defined | **Reliable** | TAPs created by VS startup |

**Rule**: All configuration tests MUST verify CONFIG_DB. Use STATE_DB only for
things that converge independently of the ASIC (BGP sessions, interface existence).

## 17. Packet Wiring (Container → ASIC)

```
Container ethN
  ↓ (tc mirred redirect)
tapN (QEMU TAP)
  ↓ (QEMU virtio NIC)
EthernetN inside SONiC VM
  ↓ (tc mirred redirect)
swvethN / vethN (veth pair)
  ↓
ngdpd (ASIC simulator) → STOP (no forwarding)
```

Management interface uses QEMU SLiRP with explicit TCP port forwarding.
**Port 6379 (Redis) is NOT forwarded** — only SSH (port 22) is reliable.
Redis must be accessed via SSH tunnel.

## 18. ASIC_DB Convergence by Topology

| Topology | Converges on VS? | Timeout | Failure Mode |
|---|---|---|---|
| Simple VLAN | Yes (< 5s) | 30s | Hard fail |
| VLAN + members | Yes (< 10s) | 30s | Hard fail |
| VRF | Yes (< 5s) | 30s | Hard fail |
| VXLAN tunnel | Usually (< 30s) | 60s | Soft fail |
| IRB (VRF + SVI + VNI) | Often not | 30s | Soft fail |
| EVPN Type-2 routes | Depends on FRR | 90s | Separate check |

IRB fails because orchagent must create a deep SAI dependency chain
(VRF → router interface → VLAN → bridge port → tunnel map), and timing
issues in the VS simulator can prevent completion.

## 19. Process Hierarchy Inside SONiC VM

```
systemd
├── redis-server (all DBs: 0-15)
├── orchagent (CONFIG_DB → ASIC_DB via SAI)
├── syncd/ngdpd (ASIC simulator)
├── bgp (FRR container — independent of ASIC)
├── teamd (LAG management)
├── lldpd
└── various SONiC services
```

Key logs: `docker exec swss cat /var/log/swss/orchagent.log`,
`docker exec syncd cat /var/log/syncd/syncd.log`.

## 20. FRR Config vs CONFIG_DB

FRR configuration and CONFIG_DB are **separate systems**:

| Aspect | CONFIG_DB | FRR (frr.conf) |
|---|---|---|
| Managed by | newtron via Redis | SCP + vtysh or bgpcfgd |
| Scope | Interfaces, VLANs, VXLAN, ACLs | BGP, route-maps, prefix-lists, EVPN |
| Persistence | Redis RDB / `config save -y` | `vtysh -c "write memory"` |

FRR config does NOT survive a container restart unless saved via `write memory`.

## 21. Redis Database Numbers

| DB | Name | Contents |
|---|---|---|
| 0 | APPL_DB | Application state (orchagent internal) |
| 1 | ASIC_DB | ASIC programming state (SAI objects) |
| 2 | COUNTERS_DB | Port/flow counters |
| 4 | CONFIG_DB | Desired configuration (primary for newtron) |
| 6 | STATE_DB | Operational/kernel state |

## 22. Debugging Methodology

When a test fails, follow this sequence:

1. **CONFIG_DB** — Is the write correct? (`redis-cli -n 4 HGETALL 'TABLE|key'`)
2. **ASIC_DB** — Did orchagent program it? (`redis-cli -n 1 KEYS '*VLAN*'`)
3. **STATE_DB** — Does the kernel reflect it? (unreliable on VS for ASIC features)
4. **Packet counters** — Are packets flowing? (tc stats, interface counters)
5. **Binary paths** — Are tools actually running? (check `/usr/sbin/tc`)
6. **Documentation** — Is this a known VS limitation?
