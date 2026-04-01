# Newtron HOWTO Guide

Newtron is a network automation tool for SONiC switches. It reads and writes SONiC's Redis databases (CONFIG_DB, APP_DB, ASIC_DB, STATE_DB) through SSH-tunneled Redis connections — no CLI scraping, no screen parsing.

All interaction goes through an HTTP API server (`newtron-server`). The CLI and newtrun are HTTP clients — they never connect to devices directly. This means you can run the server close to your devices and operate from anywhere.

```
┌─────────────┐     ┌────────────────┐     ┌────────────┐     ┌──────────────────┐
│             │     │                │     │            │     │                  │
│ newtron CLI │     │ newtron-server │     │ SSH tunnel │     │   SONiC Redis    │
│             │     │  (HTTP :8080)  │     │            │     │ DB 4 (CONFIG_DB) │
│             │ ──▶ │                │ ──▶ │            │ ──▶ │                  │
└─────────────┘     └────────────────┘     └────────────┘     └──────────────────┘
                      ▲
                      │
                      │
                    ┌────────────────┐
                    │                │
                    │    newtrun     │
                    │                │
                    └────────────────┘
```

All write commands default to **dry-run** (preview only). Add `-x` to execute. This applies to both device operations and spec edits.

For the architectural principles behind this design, see the [HLD](hld.md). For type definitions and API reference, see the [LLD](lld.md).

---

## Table of Contents

1. [Installation](#1-installation)
2. [Server Setup](#2-server-setup)
3. [Specification Files](#3-specification-files)
4. [CLI Basics](#4-cli-basics)
5. [Service Management](#5-service-management)
6. [Health Checks](#6-health-checks)
7. [Interface Configuration](#7-interface-configuration)
8. [VLAN Management](#8-vlan-management)
9. [VRF Management](#9-vrf-management)
10. [EVPN/VXLAN Overlay](#10-evpnvxlan-overlay)
11. [Link Aggregation (LAG)](#11-link-aggregation-lag)
12. [ACL Management](#12-acl-management)
13. [BGP Visibility](#13-bgp-visibility)
14. [QoS Management](#14-qos-management)
15. [Filter and Policy Management](#15-filter-and-policy-management)
16. [Device Operations](#16-device-operations)
17. [Topology Provisioning](#17-topology-provisioning)
18. [Troubleshooting](#18-troubleshooting)
19. [Go API Usage](#19-go-api-usage)
20. [End-to-End Workflows](#20-end-to-end-workflows)
21. [Quick Reference](#21-quick-reference)
22. [Related Documentation](#22-related-documentation)

---

## 1. Installation

### 1.1 Build from Source

```bash
git clone https://github.com/newtron-network/newtron.git
cd newtron

# Build the CLI and server
go build -o bin/newtron ./cmd/newtron
go build -o bin/newtron-server ./cmd/newtron-server

# Optional: build lab tools (newtlab, newtrun)
make build
```

### 1.2 Verify

```bash
bin/newtron version
bin/newtron --help
```

---

## 2. Server Setup

Why a server? The CLI and newtrun need a running `newtron-server` to operate. The server manages SSH connections to SONiC devices, caches them across requests, and serializes operations per-device to prevent CONFIG_DB corruption from concurrent writes.

### 2.1 Start the Server

```bash
# Minimal — serve a single network from /etc/newtron
newtron-server -spec-dir /etc/newtron

# With explicit listen address and network ID
newtron-server -addr :9090 -spec-dir /etc/newtron -net-id production

# Disable SSH connection caching (reconnect per request)
newtron-server -spec-dir /etc/newtron -idle-timeout -1s
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `:8080` | Listen address |
| `-spec-dir` | (none) | Spec directory to auto-register as a network |
| `-net-id` | `default` | Network ID for the auto-registered spec directory |
| `-idle-timeout` | `0` (5 min) | SSH idle timeout. `0` = default 5m. Negative = no caching. |

The server can manage multiple networks simultaneously. Each network has its own set of spec files and devices. The CLI registers its network on startup via the HTTP API.

### 2.2 Connection Architecture

The server connects to SONiC devices via SSH tunnels to reach their Redis instances:

```
newtron-server ──SSH──→ SONiC device:22 ──forward──→ 127.0.0.1:6379 (Redis)
```

Redis inside SONiC listens only on localhost and has no authentication. In QEMU/SLiRP lab environments, only port 22 (SSH) is forwarded — Redis port 6379 is not directly accessible. The SSH tunnel solves this.

When SSH credentials (`ssh_user`, `ssh_pass`) are absent from a device profile, the server connects to Redis directly at `<mgmt_ip>:6379`. This is useful for integration tests with mock Redis instances.

### 2.3 SSH Connection Caching and NodeActor Serialization

The server caches SSH connections per-device (one NodeActor per device). After `idle-timeout` (default 5 minutes) with no requests, the connection is closed. The next request reconnects automatically.

All operations for a device are serialized through its NodeActor — no concurrent CONFIG_DB access, no locking races. This eliminates the entire class of bugs where two CLI invocations interleave writes to the same device.

**External actors are not coordinated.** The NodeActor serializes newtron operations, but it cannot prevent external tools (Ansible, `redis-cli`, SONiC CLI) from writing to CONFIG_DB concurrently. If external tools modify CONFIG_DB while newtron is operating, newtron's precondition checks (which read from a cached snapshot) may miss the external changes. In practice this is rare — newtron is usually the sole CONFIG_DB writer in a deployment.

### 2.4 Four-Database Access

On connection, the server opens four Redis clients through the same tunnel:

| Database | Redis DB | Purpose | Fatal on failure? |
|----------|----------|---------|-------------------|
| CONFIG_DB | 4 | Configuration state (read/write) | Yes |
| STATE_DB | 6 | Operational state (read-only) | No |
| APP_DB | 0 | Route verification — routes from FRR/fpmsyncd | No |
| ASIC_DB | 1 | ASIC-level verification — SAI objects from orchagent | No |

Only CONFIG_DB connection failure is fatal. STATE_DB, APP_DB, and ASIC_DB failures are logged as warnings and the system continues. Configuration operations work without them; only routing state queries (`GetRoute`, `GetRouteASIC`) and operational state queries (`bgp status`, health checks) require the non-CONFIG_DB clients.

### 2.5 Configuring the CLI to Use the Server

The CLI resolves the server URL from (highest priority first):

1. `--server` flag
2. `NEWTRON_SERVER` environment variable
3. `settings server` value
4. Default: `http://localhost:8080`

The network ID follows the same pattern:

1. `--network-id` flag
2. `NEWTRON_NETWORK_ID` environment variable
3. `settings network_id` value
4. Default: `default`

```bash
# Explicit server on each command
newtron --server http://10.0.0.1:8080 leaf1 show

# Set once in settings
newtron settings set server http://10.0.0.1:8080
newtron settings set network_id production

# Or via environment
export NEWTRON_SERVER=http://10.0.0.1:8080
export NEWTRON_NETWORK_ID=production
```

On every CLI invocation (except `settings`, `version`, and `help`), the CLI calls `RegisterNetwork` on the server with its spec directory. This is idempotent — if the network is already registered, the server returns 409 and the CLI continues.

### 2.7 Reloading Specs

If you modify spec files on disk (manually or via another tool) while the server is running, the server won't see the changes — specs are loaded into memory at registration time.

To pick up changes without restarting the server:

```bash
curl -X POST http://localhost:8080/network/default/reload
```

Or via the Go client:

```go
c := client.New("http://localhost:8080", "default")
c.ReloadNetwork()
```

This stops all SSH connections, reloads specs from disk, and creates a fresh internal state. SSH connections reconnect lazily on the next request. API-authored spec changes (service create, filter add-rule, etc.) are safe — they write to disk immediately, so reload picks them up.

### 2.6 Host Key Verification

The SSH tunnel currently uses `InsecureIgnoreHostKey()` for the host key callback. This is appropriate for lab and test environments but **must be replaced with proper host key verification for production deployments**.

---

## 3. Specification Files

Why specs? Newtron separates **intent** (what services, VPNs, and policies you want) from **execution** (the CONFIG_DB entries that implement them). Specs declare intent; the server translates them to device config at runtime using each device's profile (IPs, AS numbers, platform).

### 3.1 Directory Structure

```
/etc/newtron/
├── network.json        # Services, VPNs, filters, QoS policies, prefix lists
├── platforms.json      # Hardware platform definitions
├── topology.json       # (optional) Topology for automated provisioning
└── profiles/
    ├── leaf1.json      # Per-device: management IP, credentials, zone
    └── spine1.json
```

Flat layout (all files in one directory) or nested layout (`specs/` subdirectory containing `network.json`) are both supported. The CLI auto-detects which.

For lab environments, newtlab generates spec files per topology under `newtrun/topologies/<name>/specs/`.

### 3.2 Network Specification (`network.json`)

The main spec file defines all reusable network objects. Every object is referenced by name from services or device operations.

```json
{
  "version": "1.0",
  "prefix_lists": {
    "rfc1918": ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"],
    "bogons": ["0.0.0.0/8", "127.0.0.0/8", "224.0.0.0/4"]
  },
  "filters": {
    "customer-ingress": {
      "description": "Ingress filter for customer interfaces",
      "type": "ipv4",
      "rules": [
        {"seq": 100, "src_prefix_list": "bogons", "action": "deny", "log": true},
        {"seq": 9999, "action": "permit"}
      ]
    }
  },
  "qos_policies": {
    "customer-4q": {
      "description": "4-queue customer-edge policy",
      "queues": [
        {"name": "best-effort",  "type": "dwrr", "weight": 40, "dscp": [0]},
        {"name": "business",     "type": "dwrr", "weight": 30, "dscp": [10, 18, 20]},
        {"name": "voice",        "type": "strict",             "dscp": [46]},
        {"name": "network-ctrl", "type": "strict",             "dscp": [48, 56]}
      ]
    }
  },
  "route_policies": {
    "customer-import": {
      "description": "Import policy for customer VRFs",
      "rules": [
        {"seq": 10, "action": "permit", "prefix_list": "rfc1918"},
        {"seq": 9999, "action": "deny"}
      ]
    }
  },
  "ipvpns": {
    "customer-vpn": {
      "description": "Customer L3VPN",
      "vrf": "Vrf_customer",
      "l3vni": 10001,
      "route_targets": ["65001:1"]
    }
  },
  "macvpns": {
    "servers-vlan100": {
      "description": "Server VLAN 100",
      "vlan_id": 100,
      "vni": 10100,
      "anycast_ip": "10.1.100.1/24",
      "anycast_mac": "00:00:00:01:02:03",
      "arp_suppression": true
    }
  },
  "services": {
    "customer-l3": {
      "description": "L3 routed customer interface with EVPN",
      "service_type": "evpn-routed",
      "vrf_type": "interface",
      "ipvpn": "customer-vpn",
      "ingress_filter": "customer-ingress",
      "qos_policy": "customer-4q",
      "routing": {
        "protocol": "bgp",
        "peer_as": "request",
        "import_policy": "customer-import"
      }
    },
    "server-irb": {
      "description": "Server VLAN with shared VRF and EVPN",
      "service_type": "evpn-irb",
      "vrf_type": "shared",
      "ipvpn": "customer-vpn",
      "macvpn": "servers-vlan100"
    },
    "transit": {
      "description": "Transit peering (global table, no VPN)",
      "service_type": "routed"
    }
  }
}
```

**Key Sections:**

| Section | Purpose |
|---------|---------|
| `ipvpns` | IP-VPN definitions (L3VNI, route targets) for L3 routing |
| `macvpns` | MAC-VPN definitions (L2VNI) for L2 bridging |
| `services` | Service templates referencing ipvpn/macvpn by name |
| `filters` | Reusable ACL rule templates |
| `qos_policies` | Declarative queue definitions (DSCP mapping, scheduling, ECN) |
| `route_policies` | BGP import/export policies |
| `prefix_lists` | Reusable IP prefix lists (expanded in filter rules and policies) |

**Service Types** — each type determines what CONFIG_DB entries are generated:

| Type | What It Does | Requires |
|------|-------------|----------|
| `routed` | L3 interface in global or VRF table | IP address at apply time |
| `bridged` | L2 VLAN member, no routing | VLAN at apply time |
| `irb` | L2 + L3 on same interface (local) | VLAN + IP at apply time |
| `evpn-routed` | L3 with VXLAN overlay | `ipvpn` reference |
| `evpn-bridged` | L2 with VXLAN overlay | `macvpn` reference |
| `evpn-irb` | L2+L3 with VXLAN, anycast gateway | Both `ipvpn` and `macvpn` |

**VRF Instantiation** (`vrf_type`):

| Value | Behavior |
|-------|----------|
| `"interface"` | Per-interface VRF: `{service}-{interface}` |
| `"shared"` | Named VRF from IP-VPN definition |
| (omitted) | Global routing table |

**QoS Policies:**

QoS policies define declarative queue configurations. Each policy contains 1-8 queues, where array position = queue index = traffic class. Services reference policies by name via `qos_policy`.

Queue types:
- `dwrr` — Deficit Weighted Round Robin, requires `weight` (percentage)
- `strict` — Strict priority, must not have `weight`

Optional: `ecn: true` on a queue creates a shared WRED profile with ECN marking.

All 64 DSCP values are mapped: explicitly listed values go to their queue, unmapped values default to queue 0.

### 3.3 Device Profiles

Each device needs a profile JSON file in `profiles/`. The profile is the source of reality for device-specific data:

```json
{
  "mgmt_ip": "172.20.20.2",
  "loopback_ip": "10.0.0.1",
  "zone": "datacenter-east",
  "platform": "accton-as7726-32x",
  "underlay_asn": 65001,
  "ssh_user": "admin",
  "ssh_pass": "YourPassword",
  "evpn": {
    "peers": ["10.0.0.10", "10.0.0.11"]
  }
}
```

**Required fields:** `mgmt_ip`, `loopback_ip`, `zone`

Profiles can also be managed via CLI or API (instead of editing JSON files directly):

```bash
# Create a profile
newtron profile create switch3 \
  --mgmt-ip 172.20.20.4 --loopback-ip 10.0.0.3 \
  --zone datacenter-east --platform accton-as7726-32x \
  --underlay-asn 65003 -x

# List all profiles
newtron profile list

# Show a profile
newtron profile show switch3

# Delete a profile
newtron profile delete switch3 -x
```

**Optional fields:**

| Field | Description |
|-------|-------------|
| `platform` | Maps to `platforms.json` for HWSKU |
| `ssh_user`, `ssh_pass` | SSH credentials (enables SSH tunnel to Redis) |
| `ssh_port` | SSH port override (default: 22) |
| `underlay_asn` | eBGP AS number (per-profile assignment) |
| `evpn.peers` | EVPN overlay peer loopback IPs |
| `evpn.route_reflector` | Mark as EVPN route reflector |
| `evpn.cluster_id` | Route reflector cluster ID |
| `vlan_port_mapping` | Device-specific VLAN-to-port mappings |
| `prefix_lists` | Device-level prefix lists (merged with zone/global) |

### 3.4 Spec Inheritance

Specs resolve through a three-level chain where lower levels win:

```
Device Profile  >  Zone defaults  >  Global (network.json)
```

Seven maps participate in merge: services, filters, ipvpns, macvpns, qos_policies, route_policies, prefix_lists. A device-level prefix list overrides a zone-level one of the same name, which overrides a global one.

Zones can be managed via CLI or API:

```bash
# List zones
newtron zone list

# Create a zone
newtron zone create datacenter-west -x

# Delete a zone (fails if profiles reference it)
newtron zone delete datacenter-west -x
```

**Zone-level overrides** — add specs under a zone in `network.json` to scope them to devices in that zone:

```json
{
  "zones": {
    "amer": {
      "services": {
        "amer-transit": {
          "description": "AMER-specific transit service",
          "service_type": "routed",
          "ingress_filter": "transit-protect"
        }
      },
      "prefix_lists": {
        "amer-internal": ["10.10.0.0/16", "10.20.0.0/16"]
      }
    }
  }
}
```

Zone-level specs can reference network-level specs (e.g., the `amer-transit` service references the network-level `transit-protect` filter).

**Node-level overrides** — add specs to a device profile (`profiles/<device>.json`) for device-specific overrides:

```json
{
  "mgmt_ip": "192.168.1.10",
  "loopback_ip": "10.0.0.10",
  "zone": "amer",
  "services": {
    "customer-l3": {
      "description": "Customer L3 with device-specific QoS",
      "service_type": "evpn-routed",
      "ipvpn": "customer-vpn",
      "vrf_type": "interface",
      "qos_policy": "special-4q"
    }
  }
}
```

**Resolution order** — when a node looks up a spec by name:

1. **Node (profile)** — highest priority
2. **Zone** — middle priority
3. **Network** — lowest priority (fallback)

All specs from all levels are visible. If the same name exists at multiple levels, the most specific level wins.

**Common pitfall:** a device profile overrides a network-level service with the same name but different fields. The override replaces the entire spec — fields not repeated in the override are lost, not merged. If you want to change one field of a network-level service for a specific device, you must repeat all fields.

### 3.5 Platform Specification (`platforms.json`)

```json
{
  "version": "1.0",
  "platforms": {
    "accton-as7726-32x": {
      "hwsku": "Accton-AS7726-32X",
      "description": "Accton AS7726-32X 100G switch",
      "port_count": 32,
      "default_speed": "100G"
    }
  }
}
```

### 3.6 Topology Specification (Optional)

When present, enables automated provisioning via `intent reconcile --topology`:

```json
{
  "version": "1.0",
  "description": "Lab topology",
  "devices": {
    "leaf1": {
      "interfaces": {
        "Ethernet0": {
          "service": "transit",
          "ip": "10.10.1.0/31"
        },
        "Ethernet4": {
          "service": "customer-l3",
          "ip": "10.1.1.1/30",
          "params": {"peer_as": "65100"}
        }
      }
    }
  }
}
```

---

## 4. CLI Basics

### 4.1 Command Structure

The CLI uses a **noun-group** pattern. Commands are organized by resource type, with the device name as the first argument (or via `-D`):

```
newtron <device> <noun> <action> [args] [-x]
```

Examples:

```bash
newtron leaf1 interface list              # device-scoped read
newtron leaf1 vlan create 100 -x          # device-scoped write
newtron service list                      # spec-level (no device)
newtron evpn ipvpn list                   # spec-level (no device)
```

**Implicit device detection:** If the first argument doesn't match a known command or start with `-`, it's treated as the device name. So `newtron leaf1 vlan list` is equivalent to `newtron -D leaf1 vlan list`.

### 4.2 Two Command Scopes

| Scope | What It Does | Examples |
|-------|-------------|----------|
| Device-scoped | Reads/writes CONFIG_DB on a device via the server | `interface list`, `vlan create`, `service apply` |
| Spec-level | Reads/writes `network.json` on the server | `service list`, `evpn ipvpn create`, `filter add-rule` |

Device-scoped commands require a device name. Spec-level commands do not.

### 4.3 Global Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--device` | `-D` | Device name |
| `--specs` | `-S` | Spec directory (default: from settings or `/etc/newtron`) |
| `--server` | | Server URL (default: settings or `http://localhost:8080`) |
| `--network-id` | `-N` | Network identifier (default: settings or `default`) |
| `--verbose` | `-v` | Verbose output |

### 4.4 Write and Output Flags

Write flags (available on all noun-group commands):

| Flag | Short | Description |
|------|-------|-------------|
| `--execute` | `-x` | Execute changes (default: dry-run preview) |
| `--no-save` | | Skip `config save` after execute (requires `-x`) |

Output flag:

| Flag | Description |
|------|-------------|
| `--json` | JSON output |

### 4.5 Dry-Run Mode (Default)

All write commands preview changes without applying them:

```bash
newtron leaf1 vlan create 100

# Output:
# Changes to be applied:
#   [ADD] VLAN|Vlan100
#
# DRY-RUN: No changes applied. Use -x to execute.
```

### 4.6 Execute Mode

Add `-x` to apply. By default, `-x` also saves the config to disk on the device. Use `--no-save` to skip persistence:

```bash
newtron leaf1 vlan create 100 -x               # execute + save
newtron leaf1 vlan create 100 -x --no-save      # execute without save
```

### 4.7 Interface Name Shortcuts

Both short and full SONiC interface names are accepted (case-insensitive):

| Short | Full | Example |
|-------|------|---------|
| `Eth0` | `Ethernet0` | `interface show Eth0` |
| `Po100` | `PortChannel100` | `lag show Po100` |
| `Vl100` | `Vlan100` | `vlan show 100` |
| `Lo0` | `Loopback0` | `interface show Lo0` |

### 4.8 Persistent Settings

Store defaults to avoid repeating flags:

```bash
newtron settings set specs /etc/newtron
newtron settings set server http://10.0.0.1:8080
newtron settings set network_id production

newtron settings show           # view all
newtron settings get server     # view one
newtron settings clear          # reset all
newtron settings path           # show settings file path
```

Settings are stored in `~/.newtron/settings.json`.

**Available settings:** `network`, `specs` (or `spec_dir`), `suite` (or `default_suite`), `topologies_dir`, `server` (or `server_url`), `network_id`

### 4.9 Show Device Status

```bash
newtron leaf1 show

# Output:
# Device: leaf1
# Management IP: 172.20.20.2
# Loopback IP: 10.0.0.1
# Platform: accton-as7726-32x
# Zone: datacenter-east
#
# Derived Configuration:
#   BGP Local AS: 65001
#   BGP Router ID: 10.0.0.1
#   VTEP Source: 10.0.0.1
#   BGP EVPN Neighbors: [10.0.0.10, 10.0.0.11]
#
# State:
#   Interfaces: 32
#   PortChannels: 2
#   VLANs: 5
#   VRFs: 3
```

---

## 5. Service Management

Why services? Services bundle VPN, filter, QoS, and routing settings into a single named template. Applying a service to an interface generates all the CONFIG_DB entries needed — VRF, VLAN, ACL, QoS, BGP neighbor — in one atomic operation.

### 5.1 Spec-Level Operations (No Device)

```bash
# List all service definitions
newtron service list

# Output:
# NAME           TYPE          DESCRIPTION
# ----           ----          -----------
# customer-l3    evpn-routed   L3 routed customer interface with EVPN
# server-irb     evpn-irb      Server VLAN with shared VRF and EVPN
# transit        routed        Transit peering (global table, no VPN)

# Show details
newtron service show customer-l3

# Output:
# Service: customer-l3
# Description: L3 routed customer interface with EVPN
# Type: evpn-routed
#
# EVPN Configuration:
#   L3 VNI: 10001
#   VRF Type: interface (per-interface VRF)
#   Route Targets: [65001:1]
#
# Filters:
#   Ingress: customer-ingress
#
# QoS Policy: customer-4q
#
# Routing:
#   Protocol: bgp
#   Peer AS: request (provided at apply time)
#   Import Policy: customer-import

# Create a new service
newtron service create my-service --type evpn-routed --ipvpn customer-vpn \
  --vrf-type interface --ingress-filter customer-ingress --qos-policy customer-4q \
  --description "Custom L3 service" -x

# Delete a service definition (does NOT remove from devices — use service remove first)
newtron service delete my-service -x
```

**Create flags:**

| Flag | Required | Description |
|------|----------|-------------|
| `--type` | Yes | `routed`, `bridged`, `irb`, `evpn-routed`, `evpn-bridged`, `evpn-irb` |
| `--ipvpn` | No | IP-VPN reference (required for evpn-routed, evpn-irb) |
| `--macvpn` | No | MAC-VPN reference (required for evpn-bridged, evpn-irb) |
| `--vrf-type` | No | `interface` (per-interface VRF) or `shared` |
| `--qos-policy` | No | QoS policy name |
| `--ingress-filter` | No | Ingress filter name |
| `--egress-filter` | No | Egress filter name |
| `--description` | No | Description |

### 5.2 Apply a Service

Applying a service to an interface generates and applies all CONFIG_DB entries:

```bash
# L3 service with IP address
newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x

# With BGP peer AS (when service has routing.peer_as: "request")
newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 --peer-as 65100 -x

# L2 service (no IP needed)
newtron leaf1 service apply Ethernet4 server-irb -x

# Apply to a PortChannel
newtron leaf1 service apply PortChannel100 customer-l3 --ip 10.2.1.1/30 -x
```

**What happens when applying `customer-l3` (evpn-routed, vrf_type=interface):**

1. Creates VRF `customer-l3-Ethernet0` with L3VNI from the ipvpn definition
2. Creates VXLAN_TUNNEL_MAP for the L3VNI
3. Configures BGP EVPN route targets for the VRF
4. Binds Ethernet0 to the VRF
5. Configures IP address on the interface
6. Creates or reuses ACL from the ingress filter (shared across same-service interfaces)
7. Binds ACL to interface (adds interface to ACL binding list)
8. Applies QoS profile mappings (DSCP-to-TC, TC-to-queue)
9. Configures BGP neighbor if service has routing spec
10. Records intent in `NEWTRON_INTENT` table

**Preconditions checked:**

- Device must be reachable via the server
- Interface must not be a LAG member (configure the LAG instead)
- Interface must not already have a service bound
- L3 service requires an IP address (`--ip`)
- L2/IRB service requires a `macvpn` reference in the service definition
- EVPN services require VTEP and BGP to be configured (`evpn setup`)
- Shared VRF must already exist (for `vrf_type: shared`)
- All referenced filters, QoS policies, and route policies must exist in the spec

### 5.3 Remove a Service

```bash
# Preview removal
newtron leaf1 service remove Ethernet0

# Execute removal
newtron leaf1 service remove Ethernet0 -x
```

**What happens (in order):**

1. Removes QoS mapping from interface
2. Removes IP addresses from interface
3. Handles shared policy objects (ACLs, route maps, prefix sets, BGP peer groups): removes interface from binding list, or deletes the object entirely if this was the last user (see [§15.5](#155-shared-policy-objects))
4. Unbinds interface from VRF; for per-interface VRFs (`vrf_type: interface`), deletes the VRF and all associated EVPN config (BGP_EVPN_VNI, BGP_GLOBALS_AF, VXLAN_TUNNEL_MAP)
5. For L2/IRB services: removes VLAN membership; if last member, removes all VLAN-related config (SVI, ARP suppression, MAC-VPN binding, VLAN itself)
6. Deletes `NEWTRON_INTENT` entry

**Dependency-aware cleanup:** Shared resources (ACLs, route maps, prefix sets, peer groups, VLANs, VRFs) are only deleted when the interface being cleaned up is the last user. This is determined by scanning CONFIG_DB for remaining consumers while excluding the current interface.

### 5.4 Refresh a Service

When a service definition changes in `network.json` (updated filter rules, QoS profile, route targets), refresh synchronizes the interface:

```bash
# Preview what would change
newtron leaf1 service refresh Ethernet0

# Output:
# Changes to synchronize service:
#   [DELETE] ACL_RULE|customer-l3-in|RULE_100
#   [DELETE] ACL_RULE|customer-l3-in|RULE_9999
#   [DELETE] ACL_TABLE|customer-l3-in
#   [DELETE] NEWTRON_INTENT|Ethernet0
#   [ADD] VRF|customer-l3-Ethernet0
#   [ADD] ACL_TABLE|customer-l3-in -> {...}
#   [ADD] ACL_RULE|customer-l3-in|RULE_100 -> {...}
#   [ADD] NEWTRON_INTENT|Ethernet0 -> {...}
#   ...
#
# DRY-RUN: No changes applied. Use -x to execute.

# Execute the refresh
newtron leaf1 service refresh Ethernet0 -x
```

**How RefreshService works internally:**

RefreshService performs a full remove-then-reapply cycle. Why not a targeted diff? Because services touch many CONFIG_DB tables with cross-dependencies (ACLs, VRFs, route maps, peer groups). A targeted diff would need table-specific merge logic for every combination. The remove+reapply approach guarantees that the final state matches the current spec, regardless of what changed:

1. Saves the current service name, IP address, and peer AS from the interface binding
2. Calls `RemoveService()` to generate a changeset that tears down the current config
3. Calls `ApplyService()` with the saved parameters to generate a changeset that applies the current service definition
4. Merges both changesets into a single atomic changeset

This approach ensures all changes in the service definition are picked up, including:

- Filter-spec rules added, removed, or modified
- QoS profile changes
- Route policy changes
- VPN parameter changes (route targets, VNI)

If nothing changed, it reports "already in sync."

**Example — refresh after filter change:**

```bash
# 1. Edit network.json to add a new rule to the customer-ingress filter-spec
# 2. Refresh all interfaces using that service
newtron leaf1 service refresh Ethernet0 -x
newtron leaf1 service refresh Ethernet4 -x
```

### 5.5 Get Service Binding

```bash
newtron leaf1 service get Ethernet0

# Output:
# Service: customer-l3
# IP: 10.1.1.1/30
# VRF: customer-l3-Ethernet0
```

---

## 6. Health Checks

Why health checks? After provisioning or applying changes, you want to verify that CONFIG_DB intent matches actual device state and that operational components (BGP, interfaces, EVPN) are healthy.

### 6.1 Run All Health Checks

```bash
newtron leaf1 health check

# Output:
# Health Check Results for leaf1
# ==============================
#
# Config Intent:
#   [PASS] 247 entries verified
#
# Operational Checks:
#   [PASS] BGP IPv4 Unicast: 2 peers established
#   [PASS] BGP EVPN: 2 peers established
#   [PASS] Interfaces: 28/32 up
#   [WARN] Interface Ethernet8: admin down
#   [PASS] PortChannels: 2/2 up, all members active
#   [PASS] VXLAN Tunnel: vtep1 operational
#   [PASS] VRFs: 3 configured, all operational
#
# Overall: PASS (6 passed, 1 warning, 0 failed)
```

Health checks require the device to have intent records (NEWTRON_INTENT). The intent projection provides the baseline for CONFIG_DB verification.

### 6.2 Check Specific Component

```bash
newtron leaf1 health check --check bgp
newtron leaf1 health check --check interfaces
newtron leaf1 health check --check evpn
newtron leaf1 health check --check lag
```

**Available check types:**

| Check | What It Verifies |
|-------|-----------------|
| `bgp` | BGP neighbor count and session state (warns if zero neighbors, or any not Established) |
| `interfaces` | Counts admin-down interfaces, reports each one as a warning |
| `evpn` | VTEP existence, NVO configuration, VNI mapping count |
| `lag` | LAG count, total member count, min-links compliance |

### 6.3 JSON Output

```bash
newtron leaf1 health check --json
```

The JSON output uses `HealthCheckResult` objects with `check`, `status`, and `message` fields. Useful for monitoring integration.

---

## 7. Interface Configuration

Interfaces are the unit of service delivery — every service, VLAN membership, VRF binding, and ACL attaches to an interface. These commands let you inspect and configure interface properties directly, outside of service management.

### 7.1 List and Show

```bash
newtron leaf1 interface list

# Output:
# INTERFACE      ADMIN  OPER  IP ADDRESS    VRF                    SERVICE
# Ethernet0      up     up    10.1.1.1/30   customer-l3-Ethernet0  customer-l3
# Ethernet4      up     up    -             -                       -
# Ethernet8      down   down  -             -                       -
# PortChannel100 up     up    10.2.1.1/30   -                       server-irb

newtron leaf1 interface show Ethernet0

# Output:
# Interface: Ethernet0
# Admin Status: up
# Oper Status: up
# Speed: 100G
# MTU: 9100
# Description: Uplink to spine1
#
# VRF: customer-l3-Ethernet0
# IP Addresses:
#   - 10.1.1.1/30
#
# Service: customer-l3
#
# ACLs:
#   Ingress: customer-l3-in
#   Egress: customer-l3-out
```

### 7.2 Get and Set Properties

```bash
# Get MTU
newtron leaf1 interface get Ethernet0 mtu
# Output: mtu: 9100

# Get admin status
newtron leaf1 interface get Ethernet0 admin-status
# Output: admin-status: up

# Get with JSON output
newtron leaf1 interface get Ethernet0 mtu --json
# Output: {"mtu": "9100"}
```

The `set` action determines the correct Redis table based on interface type (`PORT` for physical, `PORTCHANNEL` for LAGs):

```bash
# Set description
newtron leaf1 interface set Ethernet4 description "Link to customer-A" -x

# Set MTU
newtron leaf1 interface set Ethernet4 mtu 9000 -x

# Admin down
newtron leaf1 interface set Ethernet8 admin-status down -x
```

**Supported properties:**

| Property | Valid Values | Notes |
|----------|-------------|-------|
| `mtu` | Integer | Validated by `util.ValidateMTU()` |
| `speed` | `1G`, `10G`, `25G`, `40G`, `50G`, `100G`, `200G`, `400G` | Must match platform capabilities |
| `admin-status` | `up`, `down` | |
| `description` | Any string | |

**Constraint:** LAG members cannot be configured directly — configure the parent LAG instead.

### 7.3 List ACLs and VLAN Members

```bash
newtron leaf1 interface list-acls Ethernet0

# Output:
# Direction  ACL Name
# ---------  --------
# ingress    customer-l3-in
# egress     customer-l3-out

newtron leaf1 interface list-members PortChannel100
```

---

## 8. VLAN Management

VLANs are typically created automatically by services (`evpn-bridged`, `evpn-irb`, `irb`, `bridged`). Use these commands when you need manual VLAN control — creating VLANs before applying services, managing members independently, or binding VLANs to MAC-VPNs for VXLAN extension.

### 8.1 Read Operations

```bash
newtron leaf1 vlan list

# Output:
# VLAN ID  L2VNI  SVI       MEMBERS
# -------  -----  ---       -------
# 100      10100  up        Ethernet0,Ethernet4,PortChannel100
# 200      10200  up        Ethernet8
# 300      -      -         Ethernet12

newtron leaf1 vlan show 100

# Output:
# VLAN: 100
# Name: Servers
# L2VNI: 10100
# ARP Suppression: on
# MAC-VPN: servers-vlan100
# SVI: Vlan100 (10.1.100.1/24, VRF: Vrf_SERVER)
# Members:
#   Ethernet0 (untagged)
#   Ethernet4 (tagged)
#   PortChannel100 (untagged)

newtron leaf1 vlan status              # operational summary from STATE_DB
```

### 8.2 Create and Delete

```bash
newtron leaf1 vlan create 100 --description "Server VLAN" -x
newtron leaf1 vlan delete 100 -x
```

**Create preconditions:** VLAN ID must be 1–4094, VLAN must not already exist.

**Delete ordering** — when manually cleaning up a VLAN, follow this sequence:

1. Remove all VLAN members (`vlan remove-interface`)
2. Unbind MAC-VPN if present (`vlan unbind-macvpn`)
3. Delete the VLAN (`vlan delete`)

The `vlan delete` command automatically removes members and VNI mappings as part of the changeset, but explicit cleanup gives you a chance to verify each step.

```bash
# Explicit cleanup sequence
newtron leaf1 vlan remove-interface 100 Ethernet0 -x
newtron leaf1 vlan remove-interface 100 Ethernet4 -x
newtron leaf1 vlan unbind-macvpn 100 -x
newtron leaf1 vlan delete 100 -x
```

### 8.3 Manage Members

```bash
newtron leaf1 vlan add-interface 100 Ethernet0 -x           # untagged (access)
newtron leaf1 vlan add-interface 100 Ethernet4 --tagged -x   # tagged (trunk)
newtron leaf1 vlan remove-interface 100 Ethernet0 -x
```

**Preconditions:** Interface must not have IP addresses (L3 config), must not be bound to a VRF. L2 and L3 are mutually exclusive on an interface.

### 8.4 Configure SVI

Create a routed VLAN interface (SVI) with optional VRF and anycast gateway:

```bash
newtron leaf1 vlan configure-svi 100 --ip 10.1.100.1/24 --vrf Vrf_SERVER --anycast-gw 00:00:00:01:02:03 -x
```

### 8.5 MAC-VPN Binding

Bind a VLAN to a MAC-VPN definition for VXLAN extension:

```bash
newtron leaf1 vlan bind-macvpn 100 servers-vlan100 -x
newtron leaf1 vlan unbind-macvpn 100 -x
```

The MAC-VPN definition in `network.json` specifies the L2VNI and ARP suppression settings:

```json
{
  "macvpns": {
    "servers-vlan100": {
      "description": "Server VLAN 100",
      "vlan_id": 100,
      "vni": 10100,
      "anycast_ip": "10.1.100.1/24",
      "anycast_mac": "00:00:00:01:02:03",
      "arp_suppression": true
    }
  }
}
```

Binding creates the VXLAN_TUNNEL_MAP entry and enables ARP suppression. Unbinding removes the L2VNI mapping (VXLAN_TUNNEL_MAP) and ARP suppression (SUPPRESS_VLAN_NEIGH) settings.

**bind-macvpn preconditions:**

| Precondition | Why |
|-------------|-----|
| VLAN must exist on the device | Cannot bind VNI to a non-existent VLAN |
| VTEP must be configured (`evpn setup`) | VXLAN_TUNNEL_MAP references the VTEP |
| MAC-VPN definition must exist in the spec | Provides VNI, ARP suppression settings |

---

## 9. VRF Management

VRFs are first-class routing contexts that own interfaces, BGP neighbors, IP-VPN bindings, and static routes. The `vrf` noun group handles all of these.

### 9.1 Read Operations

```bash
newtron leaf1 vrf list

# Output:
# NAME               L3VNI  INTERFACES
# ----               -----  ----------
# default            -      Ethernet0,Ethernet4
# Vrf_CUST1          10001  Ethernet8,Ethernet12
# Vrf_SERVER         20001  Vlan100

newtron leaf1 vrf show Vrf_CUST1

# Output:
# VRF: Vrf_CUST1
# L3VNI: 10001
# IP-VPN: customer-vpn
# Import RT: 65001:1
# Export RT: 65001:1
#
# Interfaces:
#   Ethernet8 (10.1.1.1/30)
#   Ethernet12 (10.1.2.1/30)
#
# BGP Neighbors:
#   10.1.1.2  AS 65100  via Ethernet8
#   10.1.2.2  AS 65200  via Ethernet12
#
# Static Routes:
#   10.99.0.0/16 -> 10.1.1.2

newtron leaf1 vrf status

# Output:
# VRF operational summary from STATE_DB:
#
# NAME           STATE    INTERFACES  NEIGHBORS  L3VNI
# ----           -----    ----------  ---------  -----
# default        up       2           4          -
# Vrf_CUST1      up       2           2          10001
# Vrf_SERVER     up       1           0          20001
```

### 9.2 Create and Delete

```bash
newtron leaf1 vrf create Vrf_CUST1 -x
newtron leaf1 vrf delete Vrf_CUST1 -x
```

**Create preconditions:** VRF must not already exist.

**Delete preconditions:**

| Precondition | Why |
|-------------|-----|
| VRF must exist | Cannot delete what doesn't exist |
| No interfaces bound | Remove interfaces first (`vrf remove-interface`) |
| No BGP neighbors configured | Remove neighbors first (`vrf remove-neighbor`) |
| No IP-VPN bound | Unbind IP-VPN first (`vrf unbind-ipvpn`) |

### 9.3 Interface Membership

```bash
newtron leaf1 vrf add-interface Vrf_CUST1 Ethernet8 -x
newtron leaf1 vrf remove-interface Vrf_CUST1 Ethernet8 -x
```

### 9.4 IP-VPN Binding

Bind a VRF to an IP-VPN definition to configure L3VNI, route targets, and VXLAN tunnel mapping:

```bash
newtron leaf1 vrf bind-ipvpn Vrf_CUST1 customer-vpn -x
newtron leaf1 vrf unbind-ipvpn Vrf_CUST1 -x
```

### 9.5 BGP Neighbors

All BGP peer management lives here, not under `bgp`:

```bash
# Auto-derived neighbor IP for /30 subnets (10.1.1.1/30 → peer 10.1.1.2)
newtron leaf1 vrf add-neighbor default Ethernet0 65100 -x

# Auto-derived for /31 (10.1.1.0/31 → peer 10.1.1.1)
newtron leaf1 vrf add-neighbor default Ethernet4 65200 -x

# Explicit neighbor IP (for /29+ or loopback-based peering)
newtron leaf1 vrf add-neighbor Vrf_CUST1 Loopback0 64512 --neighbor 10.0.0.1 -x

# With description
newtron leaf1 vrf add-neighbor default Ethernet0 65100 --description "Customer A" -x

# Remove by interface
newtron leaf1 vrf remove-neighbor default Ethernet0 -x

# Remove by explicit IP
newtron leaf1 vrf remove-neighbor Vrf_CUST1 10.0.0.1 -x
```

**Auto-derivation rules:**

- `/30` subnet: the other usable IP on the /30 is used (e.g., .1 derives .2, .2 derives .1)
- `/31` subnet: the other IP on the /31 is used (e.g., .0 derives .1, .1 derives .0)
- `/29` or larger: `--neighbor` flag is required (too many candidates to auto-derive)

**Validation:**

- The neighbor IP must be on the same subnet as the interface (unless loopback-based peering with `--neighbor`)
- The interface must have an IP address configured (unless `--neighbor` is explicit)
- The `--neighbor` flag is required when the interface subnet is larger than /30

**remove-neighbor cleanup:** The remove operation deletes all address-family entries (ipv4_unicast, ipv6_unicast, l2vpn_evpn) for the neighbor, then the BGP_NEIGHBOR entry itself. This ensures no orphaned AF entries remain.

### 9.6 Static Routes

```bash
newtron leaf1 vrf add-route Vrf_CUST1 10.99.0.0/16 10.1.1.2 -x
newtron leaf1 vrf add-route Vrf_CUST1 10.99.0.0/16 10.1.1.2 --metric 100 -x
newtron leaf1 vrf remove-route Vrf_CUST1 10.99.0.0/16 -x
```

### 9.7 VRF Setup Workflow

Typical customer VRF from scratch:

```bash
# 1. Create VRF
newtron leaf1 vrf create Vrf_CUST1 -x

# 2. Add interfaces
newtron leaf1 vrf add-interface Vrf_CUST1 Ethernet8 -x
newtron leaf1 vrf add-interface Vrf_CUST1 Ethernet12 -x

# 3. Add BGP neighbors
newtron leaf1 vrf add-neighbor Vrf_CUST1 Ethernet8 65100 -x
newtron leaf1 vrf add-neighbor Vrf_CUST1 Ethernet12 65200 -x

# 4. Bind to IP-VPN for EVPN extension
newtron leaf1 vrf bind-ipvpn Vrf_CUST1 customer-vpn -x

# 5. Verify
newtron leaf1 vrf show Vrf_CUST1
```

---

## 10. EVPN/VXLAN Overlay

Why a dedicated noun? EVPN configuration has two distinct layers: the device-level overlay infrastructure (VTEP, NVO, BGP EVPN sessions) and the spec-level VPN definitions (IP-VPNs for L3, MAC-VPNs for L2). The `evpn` noun handles both.

### 10.1 Device-Level: Setup and Status

```bash
# Idempotent — creates VTEP, NVO, and BGP EVPN sessions (skips existing)
newtron leaf1 evpn setup -x

# Explicit VTEP source IP (default: loopback IP from profile)
newtron leaf1 evpn setup --source-ip 10.0.0.10 -x

# View combined config and operational state
newtron leaf1 evpn status
```

`evpn setup` creates:
1. `VXLAN_TUNNEL|vtep1` with `src_ip` from profile loopback
2. `VXLAN_EVPN_NVO|nvo1` pointing to `vtep1`
3. BGP EVPN sessions with peers from `profile.evpn.peers`

**`evpn status` output:**

```bash
newtron leaf1 evpn status

# Output:
# EVPN Overlay Status
# ===================
#
# VTEP: vtep1
# Source IP: 10.0.0.10
# NVO: nvo1
#
# IP-VPN (L3) Bindings:
#   Vrf_CUST1     L3VNI: 10001  RT: 65001:1
#   Vrf_SERVER    L3VNI: 20001  RT: 65001:100
#
# MAC-VPN (L2) Bindings:
#   Vlan100       servers-vlan100   L2VNI: 10100  ARP Suppress: on
#   Vlan200       storage-vlan200   L2VNI: 10200  ARP Suppress: on
#
# BGP EVPN Neighbors:
#   10.0.0.1   Established  (spine1-dc1)
#   10.0.0.2   Established  (spine2-dc1)
```

### 10.2 Spec-Level: IP-VPN Management

IP-VPNs define L3VNI and route targets for EVPN L3 routing. No device needed:

```bash
newtron evpn ipvpn list
newtron evpn ipvpn show customer-vpn

newtron evpn ipvpn create my-vpn \
  --l3vni 10001 --route-targets 65001:1 --vrf Vrf_customer \
  --description "Customer VPN" -x

newtron evpn ipvpn delete my-vpn -x
```

### 10.3 Spec-Level: MAC-VPN Management

MAC-VPNs define L2VNI for EVPN L2 bridging. No device needed:

```bash
newtron evpn macvpn list
newtron evpn macvpn show servers-vlan100

newtron evpn macvpn create my-l2vpn \
  --vni 10100 --vlan-id 100 --arp-suppress \
  --anycast-ip 10.1.100.1/24 --anycast-mac 00:00:00:01:02:03 \
  --description "Server L2 extension" -x

newtron evpn macvpn delete my-l2vpn -x
```

### 10.4 EVPN Overlay Workflow

Set up a complete EVPN overlay from scratch:

```bash
# 1. Create VPN definitions (spec-level, no device)
newtron evpn ipvpn create customer-vpn --l3vni 10001 --route-targets 65001:1 -x
newtron evpn macvpn create servers-vlan100 --vni 10100 --vlan-id 100 --arp-suppress -x

# 2. Set up overlay infrastructure on device
newtron leaf1 evpn setup -x

# 3. Bind VRF to IP-VPN
newtron leaf1 vrf create Vrf_CUST1 -x
newtron leaf1 vrf bind-ipvpn Vrf_CUST1 customer-vpn -x

# 4. Bind VLAN to MAC-VPN
newtron leaf1 vlan create 100 -x
newtron leaf1 vlan bind-macvpn 100 servers-vlan100 -x
```

---

## 11. Link Aggregation (LAG)

LAGs bundle physical interfaces into a single logical interface for bandwidth aggregation and redundancy. Create the LAG first, then apply services to the LAG name (e.g., `PortChannel100`) instead of individual member interfaces.

### 11.1 Read Operations

```bash
newtron leaf1 lag list

# Output:
# NAME             STATUS   MEMBERS                ACTIVE
# ----             ------   -------                ------
# PortChannel100   up       Ethernet12,Ethernet16  2
# PortChannel200   up       Ethernet20,Ethernet24  2

newtron leaf1 lag show PortChannel100

# Output:
# LAG: PortChannel100
# Admin Status: up
# MTU: 9100
# Mode: active
# Min Links: 1
# Members:
#   Ethernet12 (active)
#   Ethernet16 (active)

newtron leaf1 lag status

# Output:
# LAG operational summary from STATE_DB:
#
# NAME             OPER    ACTIVE/TOTAL  MIN-LINKS
# ----             ----    ------------  ---------
# PortChannel100   up      2/2           1
# PortChannel200   up      2/2           1
```

### 11.2 Create and Delete

```bash
newtron leaf1 lag create PortChannel100 \
  --members Ethernet12,Ethernet16 \
  --min-links 1 \
  --fast-rate \
  --mtu 9100 \
  -x

newtron leaf1 lag delete PortChannel100 -x   # members removed automatically
```

**Create flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--members` | (required) | Comma-separated member interfaces |
| `--mode` | `active` | LACP mode: `active`, `passive`, or `on` (static) |
| `--min-links` | `1` | Minimum active links for LAG to be up |
| `--fast-rate` | `true` | LACP fast rate (1s timeout vs 30s) |
| `--mtu` | `9100` | MTU |

**Preconditions:** PortChannel must not exist, members must exist, no member in another LAG.

### 11.3 Manage Members

```bash
newtron leaf1 lag add-interface PortChannel100 Ethernet20 -x
newtron leaf1 lag remove-interface PortChannel100 Ethernet20 -x
```

**add-interface preconditions:** Interface must exist, must be a physical interface, must not be in another LAG, must not have a service bound.

### 11.4 Delete a LAG

```bash
# Remove service first if bound
newtron leaf1 service remove PortChannel100 -x

# Then delete LAG (members are removed automatically)
newtron leaf1 lag delete PortChannel100 -x
```

---

## 12. ACL Management

ACLs are device-level CONFIG_DB objects. Services create ACLs automatically from filter specs — this section covers direct ACL management for cases where you need manual control.

### 12.1 Read Operations

```bash
newtron leaf1 acl list

# Output:
# NAME                 TYPE   STAGE    INTERFACES              RULES
# ----                 ----   -----    ----------              -----
# customer-l3-in       L3     ingress  Ethernet0,Ethernet4     4
# customer-l3-out      L3     egress   Ethernet0,Ethernet4     2
# DATAACL              L3     ingress  Ethernet8               3

newtron leaf1 acl show customer-l3-in

# Output:
# ACL Table: customer-l3-in
# Type: L3
# Stage: ingress
# Bound to: Ethernet0,Ethernet4
#
# Rules:
# Seq    Match                           Action   Counter
# ---    -----                           ------   -------
# 100    src: 0.0.0.0/8                  DENY     1234
# 101    src: 127.0.0.0/8               DENY     0
# 102    src: 224.0.0.0/4               DENY     567
# 9999   any                             PERMIT   892456
```

Note: ACLs created by services are shared — the same ACL table is bound to multiple interfaces via the `ports` field.

### 12.2 Create, Modify, Delete

```bash
# Create ACL table
newtron leaf1 acl create CUSTOM-ACL --type L3 --stage ingress --interfaces Ethernet0 -x

# Add rules
newtron leaf1 acl add-rule CUSTOM-ACL RULE_100 \
  --priority 9000 --src-ip 10.100.0.0/16 --action permit -x

newtron leaf1 acl add-rule CUSTOM-ACL RULE_200 \
  --priority 8000 --protocol tcp --dst-port 22 --action deny -x

newtron leaf1 acl add-rule CUSTOM-ACL RULE_9999 \
  --priority 1 --action permit -x

# Delete a rule
newtron leaf1 acl delete-rule CUSTOM-ACL RULE_200 -x

# Delete entire ACL (rules deleted automatically)
newtron leaf1 acl delete CUSTOM-ACL -x
```

**Rule flags:** `--priority`, `--action` (permit/deny, required), `--src-ip`, `--dst-ip`, `--protocol` (tcp/udp/icmp/number), `--src-port`, `--dst-port`

### 12.3 Bind and Unbind

```bash
newtron leaf1 acl bind CUSTOM-ACL Ethernet4 --direction ingress -x
newtron leaf1 acl unbind CUSTOM-ACL Ethernet4 -x
```

ACLs are shared — multiple interfaces can be bound to the same ACL table. Unbinding the last interface deletes the ACL.

**Cleanup sequence** — to fully remove a manually created ACL:

```bash
# Step 1: Unbind from all interfaces
newtron leaf1 acl unbind CUSTOM-ACL Ethernet0 -x
newtron leaf1 acl unbind CUSTOM-ACL Ethernet4 -x

# Step 2: Delete the ACL table (rules deleted automatically)
newtron leaf1 acl delete CUSTOM-ACL -x
```

If the ACL is bound to only one interface, unbinding it will automatically delete the ACL and all its rules (no separate delete step needed).

### 12.4 How Services Create ACLs

When a service with an `ingress_filter` is applied, newtron:

1. Derives a content-hashed ACL name from the filter spec and direction
   (e.g., `PROTECT_RE_IN_1ED5F2C7` — see [§15.5 Shared Policy Objects](#155-shared-policy-objects))
2. If the ACL exists: adds the interface to its binding list
3. If not: creates the ACL table and expands all rules from the filter spec

Rule expansion handles `src_prefix_list`/`dst_prefix_list` references — each
prefix becomes a separate ACL rule. Priority is computed as
`10000 - sequence_number`.

---

## 13. BGP Visibility

BGP is **read-only** in the CLI. The `bgp` noun has a single `status` subcommand. All peer management lives under `vrf` (`vrf add-neighbor`, `vrf remove-neighbor`). EVPN overlay sessions are managed by `evpn setup`.

```bash
newtron leaf1 bgp status

# Output:
# BGP Status for leaf1
# ====================
#
# Identity:
#   Local AS: 65001
#   Router ID: 10.0.0.1
#
# Configured Neighbors (CONFIG_DB):
#   IP           AS     Type      VRF        Interface
#   10.0.0.10    65010  overlay   default    Loopback0
#   10.1.1.2     65100  underlay  default    Ethernet0
#
# Operational State (STATE_DB):
#   Neighbor     State         Pfx Rcvd  Pfx Sent  Uptime
#   10.0.0.10    Established   12        8         02:15:30
#   10.1.1.2     Established   4         6         01:45:12
```

### 13.1 Managing BGP Neighbors

All BGP peer management uses the `vrf` noun group:

```bash
# Add eBGP neighbor (auto-derived IP for /30)
newtron leaf1 vrf add-neighbor default Ethernet0 65100 -x

# Add neighbor with explicit IP (loopback-based peering)
newtron leaf1 vrf add-neighbor Vrf_CUST1 Loopback0 64512 --neighbor 10.0.0.1 -x

# Remove a neighbor
newtron leaf1 vrf remove-neighbor default Ethernet0 -x
```

See [§9 VRF Management](#9-vrf-management) for the full set of neighbor operations, auto-derivation rules, and validation.

### 13.2 Security Constraints

**Security constraints** on all direct BGP neighbors:

| Constraint | Value | Rationale |
|------------|-------|-----------|
| TTL | 1 | Prevents BGP hijacking via multi-hop attacks (GTSM) |
| Subnet validation | Required | Neighbor IP must be on interface subnet |
| Update source | Interface IP | Uses directly connected IP, not loopback |

---

## 14. QoS Management

QoS has two scopes: spec authoring (no device) and device application (requires device). Policies define declarative queue configurations that map DSCP values to traffic classes with scheduling weights.

### 14.1 Spec-Level Operations

```bash
newtron qos list

# Output:
# NAME             QUEUES  DESCRIPTION
# ----             ------  -----------
# customer-4q      4       4-queue customer-edge policy
# 8q-datacenter    8       8-queue datacenter policy

newtron qos show customer-4q

# Output:
# Policy: customer-4q
# Description: 4-queue customer-edge policy
# Queues: 4
#
# Queue  Name           Type    Weight  DSCP         ECN
# -----  ----           ----    ------  ----         ---
# 0      best-effort    dwrr    40      0            -
# 1      business       dwrr    30      10,18,20     -
# 2      voice          strict  -       46           -
# 3      network-ctrl   strict  -       48,56        -

# Create empty policy, then add queues
newtron qos create my-policy --description "Custom QoS" -x

newtron qos add-queue my-policy 0 --type dwrr --weight 40 --dscp 0 --name best-effort -x
newtron qos add-queue my-policy 1 --type dwrr --weight 30 --dscp 10,18,20 --name business -x
newtron qos add-queue my-policy 2 --type strict --dscp 46 --name voice -x
newtron qos add-queue my-policy 3 --type strict --dscp 48,56 --name network-ctrl --ecn -x

newtron qos remove-queue my-policy 3 -x
newtron qos delete my-policy -x
```

**Queue parameters:**

| Parameter | Description |
|-----------|-------------|
| `--type` | `dwrr` (weighted round-robin) or `strict` (priority) |
| `--weight` | DWRR weight percentage (required for `dwrr`, forbidden for `strict`) |
| `--dscp` | Comma-separated DSCP values (0–63) mapped to this queue |
| `--name` | Human-readable queue name |
| `--ecn` | Enable ECN marking (creates a shared WRED profile) |

**Constraints:** 1–8 queues per policy. DSCP values 0–63, no duplicates across queues. DWRR weights must sum to 100%.

### 14.2 Device Application

```bash
newtron leaf1 qos apply Ethernet0 customer-4q -x
newtron leaf1 qos remove Ethernet0 -x
```

Creates DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER, and PORT_QOS_MAP entries in CONFIG_DB.

### 14.3 QoS Override Workflow

Services can include QoS policies. To temporarily override with a different policy:

```bash
newtron leaf1 qos apply Ethernet0 8q-datacenter -x     # override
newtron leaf1 qos remove Ethernet0 -x                   # remove override
newtron leaf1 service refresh Ethernet0 -x               # restore service-managed QoS
```

---

## 15. Filter and Policy Management

Three spec-level policy objects control traffic handling: **filters** (ACL
templates), **route policies** (BGP import/export rules), and **prefix lists**
(reusable IP prefix sets referenced by filters and route policies). All three
live in `network.json` and are instantiated as CONFIG_DB objects when services
reference them.

Why both filters and ACLs? A **filter** is a reusable template in `network.json`.
An **ACL** is a device-level CONFIG_DB object. Services bridge the two — applying
a service with a filter creates the corresponding ACL. Filter management is
spec-level; ACL management is device-level.

### 15.1 Spec-Level Operations

```bash
newtron filter list

# Output:
# NAME               TYPE  RULES  DESCRIPTION
# ----               ----  -----  -----------
# customer-ingress   L3    2      Ingress filter for customer interfaces
# transit-protect    L3    5      Transit peering protection

newtron filter show customer-ingress

# Output:
# Filter: customer-ingress
# Type: L3
# Description: Ingress filter for customer interfaces
#
# Rules:
# Seq    Match                    Action   Log
# ---    -----                    ------   ---
# 100    src_prefix_list: bogons  deny     yes
# 9999   any                      permit   -

# Create empty filter, then add rules
newtron filter create my-filter --type ipv4 --description "Custom filter" -x

newtron filter add-rule my-filter --priority 100 --action deny --src-ip 10.0.0.0/8 -x
newtron filter add-rule my-filter --priority 200 --action deny --protocol icmp -x
newtron filter add-rule my-filter --priority 9999 --action permit -x

newtron filter remove-rule my-filter 200 -x
newtron filter delete my-filter -x
```

**Rule flags:** `--priority` (required, sequence number), `--action` (permit/deny, required), `--src-ip`, `--dst-ip`, `--protocol`, `--src-port`, `--dst-port`, `--dscp`, `--src-prefix-list`, `--dst-prefix-list`

Delete fails if any service references the filter.

### 15.2 Filter Lifecycle

Filters live in network.json and are instantiated on devices when services reference them:

```bash
# 1. Create filter template
newtron filter create my-filter --type ipv4 -x
newtron filter add-rule my-filter --priority 100 --action deny --src-ip 10.0.0.0/8 -x
newtron filter add-rule my-filter --priority 9999 --action permit -x

# 2. Create service referencing the filter
newtron service create my-svc --type routed --ingress-filter my-filter -x

# 3. Apply service (filter becomes ACL on device)
newtron leaf1 service apply Ethernet0 my-svc --ip 10.1.1.1/30 -x

# 4. Update filter rules later
newtron filter add-rule my-filter --priority 150 --action deny --src-ip 172.16.0.0/12 -x

# 5. Refresh to pick up filter changes
newtron leaf1 service refresh Ethernet0 -x
```

### 15.3 Route Policy Management

Route policies define match-action rules for BGP route filtering. A service's
`routing.import_policy` or `routing.export_policy` references a route policy by
name. When the service is applied, newtron translates the route policy into
`ROUTE_MAP` and (if rules reference prefix lists) `PREFIX_SET` entries in
CONFIG_DB.

Route policies are managed via the HTTP API. No CLI noun exists yet — use the
Go client or newtrun step actions (`create-route-policy`, `delete-route-policy`,
`add-route-policy-rule`, `remove-route-policy-rule`).

**Spec structure** in `network.json`:

```json
{
  "route_policies": {
    "customer-import": {
      "description": "Accept customer routes with local-pref 200",
      "rules": [
        {
          "sequence": 10,
          "action": "permit",
          "prefix_list": "rfc1918",
          "set": { "local_pref": 200 }
        },
        {
          "sequence": 20,
          "action": "deny"
        }
      ]
    }
  }
}
```

**Rule fields:** `sequence` (ordering), `action` (permit/deny), `prefix_list`
(optional — matches routes against this prefix list), `community` (optional —
match on community), `set` (optional — modify attributes: `local_pref`,
`community`, `med`).

**Go client usage:**

```go
client.CreateRoutePolicy(ctx, newtron.CreateRoutePolicyRequest{
    Name:        "customer-import",
    Description: "Accept customer routes with local-pref 200",
})
client.AddRoutePolicyRule(ctx, newtron.AddRoutePolicyRuleRequest{
    Policy:     "customer-import",
    Sequence:   10,
    Action:     "permit",
    PrefixList: "rfc1918",
    Set:        &newtron.RoutePolicySetSpec{LocalPref: 200},
})
```

Delete fails if any service references the route policy.

### 15.4 Prefix List Management

Prefix lists are named collections of IP prefixes. They are referenced in two
places: filter rules (`src_prefix_list` / `dst_prefix_list` — each prefix
expands to a separate ACL rule) and route policy rules (`prefix_list` — becomes
a `PREFIX_SET` match condition in `ROUTE_MAP`).

Prefix lists are managed via the HTTP API. No CLI noun exists yet — use the
Go client or newtrun step actions (`create-prefix-list`, `delete-prefix-list`,
`add-prefix-entry`, `remove-prefix-entry`).

**Spec structure** in `network.json`:

```json
{
  "prefix_lists": {
    "rfc1918": ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"],
    "bogons": ["0.0.0.0/8", "127.0.0.0/8", "224.0.0.0/4"]
  }
}
```

**Go client usage:**

```go
client.CreatePrefixList(ctx, newtron.CreatePrefixListRequest{
    Name:     "rfc1918",
    Prefixes: []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
})
client.AddPrefixListEntry(ctx, newtron.AddPrefixListEntryRequest{
    PrefixList: "rfc1918",
    Prefix:     "100.64.0.0/10",
})
client.RemovePrefixListEntry(ctx, "rfc1918", "100.64.0.0/10")
```

Delete fails if any filter rule or route policy rule references the prefix list.

### 15.5 Shared Policy Objects

When services reference filters, route policies, or prefix lists, newtron
creates CONFIG_DB objects that may be **shared** across multiple interfaces.
Three mechanisms manage this sharing:

**Content-hashed naming.** ACL_TABLE, ROUTE_MAP, PREFIX_SET, and COMMUNITY_SET
entries include an 8-character SHA256 hash of their generated CONFIG_DB fields
in the key name (e.g., `PROTECT_RE_IN_1ED5F2C7`). The hash ensures that
identical specs produce identical names, and changed specs produce different
names.

**Blue-green migration.** When a spec changes (filter rule added, route policy
modified), the content hash changes. On `service refresh`, newtron creates the
new object alongside the old one, migrates the interface to the new object, and
deletes the old object only if no other interface still references it. This
avoids a window where the device has no policy applied.

**Reference-aware cleanup.** Shared policy objects are created on first reference
and deleted when the last consumer is removed. `RemoveService` scans CONFIG_DB
for remaining consumers of each shared object before deciding whether to delete
it. This prevents one interface's removal from breaking another interface's
policy.

**BGP peer groups.** When multiple interfaces use the same service with BGP
routing, newtron creates a `BGP_PEER_GROUP` named after the service. Neighbors
reference the peer group; shared attributes (route maps, admin status) live on
`BGP_PEER_GROUP_AF`. Peer groups are created on first `ApplyService` for a
service with BGP routing and deleted when the last interface using that service
is removed.

---

## 16. Device Operations

Operations that act on the device as a whole rather than on a specific resource. Intent operations inspect and reconcile the device's configuration state against its intent records. Initialization prepares a device for newtron management. Platform and audit provide visibility.

### 16.1 Intent Operations

Every write operation (service apply, VRF create, EVPN setup) records an intent in the NEWTRON_INTENT table. The projection — the expected CONFIG_DB state — is derived by replaying all intents. Six intent commands let you inspect, compare, and reconcile this state.

#### 16.1.1 Intent Tree

View the intent DAG as a tree rooted at the device or scoped to a resource:

```bash
# Full tree from device root
newtron leaf1 intent tree

# Scope to a resource kind
newtron leaf1 intent tree vlan

# Scope to a specific resource
newtron leaf1 intent tree vlan:100

# Scope to an interface subtree
newtron leaf1 intent tree interface:Ethernet0

# Include path from resource to root
newtron leaf1 intent tree vlan:100 --ancestors

# JSON output
newtron leaf1 intent tree --json
```

The tree shows every intent record with its operation type and parameters. Use it to understand what newtron has configured and the dependency structure between resources.

**Resource kind values:** `vlan`, `vrf`, `interface`, `bgp`, `evpn`, `acl`, `qos`, `service`, `portchannel`

#### 16.1.2 Drift Detection

Compare the expected CONFIG_DB (derived from intent replay) against the actual CONFIG_DB:

```bash
newtron leaf1 intent drift

# Output (clean):
# Drift Report for leaf1: CLEAN
# No drift detected in newtron-owned tables.

# Output (drifted):
# Drift Report for leaf1: DRIFTED
#
# Missing entries (2):
# TABLE          KEY              EXPECTED FIELDS
# VLAN           Vlan100          [vlanid=100]
# VLAN_MEMBER    Vlan100|Eth0     [tagging_mode=untagged]
#
# Extra entries (1):
# TABLE          KEY              ACTUAL FIELDS
# STATIC_ROUTE   default|0.0.0.0  [nexthop=10.0.0.1]
#
# Modified entries (1):
# TABLE          KEY              FIELD         EXPECTED  ACTUAL
# BGP_NEIGHBOR   10.1.1.2         admin_status  up        down
```

Three drift categories:

| Category | Meaning |
|----------|---------|
| Missing | Intent says it should exist; device doesn't have it |
| Extra | Device has it in a newtron-owned table; intent doesn't account for it |
| Modified | Both have it; field values differ |

**Topology mode** — reconstructs expected state from `topology.json` steps instead of device NEWTRON_INTENT records:

```bash
newtron leaf1 --topology intent drift
```

Use topology mode to detect drift from the topology's intended state on devices that haven't been provisioned yet, or to compare a live device against the topology definition.

**When to run:**
- After suspecting external CONFIG_DB edits (Ansible, redis-cli, SONiC CLI)
- After a config reload to verify the saved config matches intent
- As a periodic health check to detect configuration decay
- Before reconcile, to preview what would change

#### 16.1.3 Reconcile

Deliver the expected CONFIG_DB to the device, eliminating all drift:

```bash
# Preview what would change (dry-run)
newtron leaf1 intent reconcile

# Execute — replaces device CONFIG_DB with projection
newtron leaf1 intent reconcile -x

# Topology mode — reconcile from topology.json
newtron leaf1 --topology intent reconcile -x
```

Reconcile reconstructs the projection from intents, computes the diff against the device, and applies it. This is the primary mechanism for:

- **Fixing drift** — external edits are overwritten to match the projection
- **Re-provisioning** — after a config reload, reconcile restores all newtron-managed state
- **Topology provisioning** — with `--topology`, delivers the full topology-defined state (see [§17](#17-topology-provisioning))

**What reconcile does NOT do:** It does not add new intents or change the intent DAG. It delivers the existing projection. To change what's configured, use the normal write operations (service apply, VRF create, etc.), then reconcile if the device has drifted.

#### 16.1.4 Intent Save

Persist the device's current intent records back to `topology.json`:

```bash
newtron leaf1 intent save

# Output:
# Saved 12 steps for leaf1

# With topology format
newtron leaf1 --topology intent save
```

Use this after manually configuring a device (service apply, VRF create, etc.) to capture its intent state for later reprovisioning or topology-based drift detection. The saved steps can be delivered to a fresh device via `intent reload` + `intent reconcile --topology`.

#### 16.1.5 Intent Reload (Topology Mode Only)

Rebuild the node's intent DAG from `topology.json` steps:

```bash
newtron leaf1 intent reload

# Output:
# Reloaded 12 steps for leaf1
```

Implicitly uses topology mode. Use this when `topology.json` has been updated externally (edited by hand, saved from a different device) and you want the server's in-memory state to reflect the changes before reconciling.

#### 16.1.6 Intent Clear (Topology Mode Only)

Reset the node's intent DAG to an empty state with ports only:

```bash
newtron leaf1 intent clear

# Output:
# Cleared intent DAG for leaf1 (2 steps remain)
```

Implicitly uses topology mode. The remaining steps are port registrations — the minimal state for an empty node. Use this before rebuilding a topology node from scratch via service apply/VRF create/etc.

#### 16.1.7 Intent Workflow

Typical workflow for detecting and fixing drift:

```bash
# 1. Check for drift
newtron leaf1 intent drift

# 2. If drifted, inspect the intent tree to understand expected state
newtron leaf1 intent tree

# 3. Preview reconcile
newtron leaf1 intent reconcile

# 4. Execute reconcile to fix drift
newtron leaf1 intent reconcile -x

# 5. Verify drift is resolved
newtron leaf1 intent drift
```

### 16.2 Platform Information

```bash
newtron platform list
newtron platform show accton-as7726-32x
```

Shows HWSKU, port count, default speed, supported/unsupported features, and dependency impact.

### 16.3 Audit Logs

Every write operation (execute mode) emits an audit event:

```bash
newtron audit list --last 24h
newtron audit list --device leaf1 --failures
newtron audit list --limit 50 --json

# Output:
# Timestamp            User    Operation          Device      Status
# ---------            ----    ---------          ------      ------
# 2024-01-15 10:30:00  alice   service apply      leaf1       success
# 2024-01-15 10:25:00  alice   lag create         leaf1       success
# 2024-01-15 10:20:00  bob     service apply      leaf1       failed
```

**Flags:** `--device`, `--user`, `--last` (duration: `24h`, `7d`), `--limit` (default 100), `--failures`

### 16.4 Device Initialization

Before newtron can manage a device, it must be initialized. Initialization enables **unified config mode** (frrcfgd) so that all CONFIG_DB writes — BGP neighbors, VRFs, EVPN tunnels — are processed by FRR. Without it, SONiC's default bgpcfgd silently ignores dynamic CONFIG_DB entries.

**Preconditions:**

| Requirement | Why |
|-------------|-----|
| Device profile exists | `newtron` must resolve SSH credentials and management IP |
| Device is reachable via SSH | Init writes DEVICE_METADATA and restarts the bgp container |
| bgp container is running | The restart command targets the bgp systemd service |

**Initialize a fresh device:**

```bash
newtron switch1 init
```

```
Initializing switch1 for newtron management...
Initialized.
  DEVICE_METADATA: docker_routing_config_mode=unified
  bgp container restarted, frrcfgd running
  Config saved to persist across reboots
```

**Already initialized (idempotent):**

```bash
newtron switch1 init
```

```
Initializing switch1 for newtron management...
switch1 is already initialized (frrcfgd enabled).
```

**Device with active BGP sessions (safety check):**

If the device has existing BGP neighbors in CONFIG_DB, init refuses to proceed. Switching to unified mode restarts the bgp container (dropping all BGP sessions) and replaces `frr.conf` with frrcfgd-generated config from CONFIG_DB. Any FRR routing configuration done via vtysh that was never written to CONFIG_DB is permanently lost:

```bash
newtron leaf1 init
```

```
Error: leaf1 has 4 active BGP neighbor(s) — device has active BGP
configuration; use --force to proceed (this will restart bgp, drop all
sessions, and replace frr.conf — any vtysh-only config not in CONFIG_DB
will be lost)
```

Use `--force` only if you understand the impact (session drops + vtysh config loss):

```bash
newtron leaf1 init --force
```

**When to run:**

- Before using `newtron` on a new device for the first time (standalone, no topology)
- Not needed after topology reconcile — provisioning includes init automatically
- Not needed in newtlab — boot patches handle it during `newtlab deploy`

**What it does:**

1. Writes `docker_routing_config_mode=unified` and `frr_mgmt_framework_config=true` to DEVICE_METADATA
2. Restarts the bgp container so frrcfgd takes over from bgpcfgd
3. Polls until frrcfgd is confirmed running (up to 120 seconds)
4. Saves config to persist the change across reboots

**What can go wrong:**

| Symptom | Cause | Fix |
|---------|-------|-----|
| `frrcfgd not enabled` on `Connect()` | Device was never initialized | Run `newtron <device> init` |
| `device has active BGP configuration` | Device has BGP neighbors; init drops sessions and replaces frr.conf | Back up `frr.conf` first, then use `--force` if config loss is acceptable |
| Init times out waiting for frrcfgd | bgp container failed to start | SSH to device, check `docker ps`, `journalctl -u bgp` |
| `ensuring unified config mode requires SSH connection` | Profile missing `ssh_user`/`ssh_pass` | Add SSH credentials to device profile |

### 16.5 Config Persistence

Newtron writes to Redis CONFIG_DB — changes take effect immediately but are **ephemeral** until saved to disk.

```
┌────────────┐     ┌──────────────────────┐     ┌───────────────────────────┐
│            │     │                      │     │                           │
│            │     │   Redis CONFIG_DB    │     │        config save        │
│ newtron -x │     │ (runtime, immediate) │     │ /etc/sonic/config_db.json │
│            │     │                      │     │       (persistent)        │
│            │ ──▶ │                      │ ──▶ │                           │
└────────────┘     └──────────────────────┘     └───────────────────────────┘
```

With `-x`, newtron saves automatically. With `-x --no-save`, it doesn't.

**What happens on reboot without save:**

1. SONiC loads `/etc/sonic/config_db.json` from disk into Redis
2. All changes made since the last `config save` are gone
3. The device returns to its last persisted state

**When to save** — save after completing a set of related changes and verifying they look correct:

```bash
# Apply services
newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x
newtron leaf1 service apply Ethernet4 customer-l3 --ip 10.1.2.1/30 -x

# Verify everything looks correct
newtron leaf1 health check

# At this point, both service applies have already saved (default with -x).
# If you used --no-save, explicitly save via SSH:
ssh admin@<mgmt_ip> 'sudo config save -y'
```

**Config reload** — to revert to the persisted configuration (discard all runtime changes):

```bash
# Via SSH to the device:
ssh admin@<mgmt_ip> 'sudo config reload -y'
```

This reloads `/etc/sonic/config_db.json` into Redis, effectively undoing any unsaved changes.

### 16.6 Crash Recovery

If newtron crashes during a write operation (killed, SSH drop, OOM), the device may have partial state. Recovery is structural — NEWTRON_INTENT records are the persistent state, and the projection is derived from them:

1. **Check drift** — see what the device has vs. what intents expect:
   ```bash
   newtron leaf1 intent drift
   ```

2. **Reconcile** — deliver the projection to fix any inconsistency:
   ```bash
   newtron leaf1 intent reconcile -x
   ```

If the crash happened mid-write, some intents may have been recorded while others weren't. In that case, the projection reflects only the successfully recorded intents. Reconcile delivers that consistent subset. To add the missing operations, re-run them (they're idempotent) and reconcile again.

**No separate crash-recovery mechanism.** The intent DAG + drift detection + reconcile is the universal recovery path. There are no zombie markers, no rollback breadcrumbs — just "what does the intent say?" vs "what does the device have?"

---

## 17. Topology Provisioning

Why provisioning? Instead of manually creating VLANs, VRFs, BGP neighbors, and services one by one, provisioning generates the complete CONFIG_DB for a device from its topology spec and delivers it in one operation.

Topology provisioning uses the same intent pipeline as manual operations. The server builds an abstract Node from `topology.json`, replays the topology steps (setup-device, apply-service, add-neighbor, etc.) to build the projection, then delivers it to the device via reconcile.

```bash
# Preview what would change
newtron leaf1 --topology intent reconcile

# Provision a device from topology
newtron leaf1 --topology intent reconcile -x
```

**Provisioning flow (per device):**

1. **Intent Replay** — the server reads `topology.json` steps for the device and replays them through the abstract Node. Each step calls the same code path as the corresponding manual operation (`SetupDevice`, `ApplyService`, `AddNeighbor`, etc.). The result is a projection — the expected CONFIG_DB.
2. **Drift Comparison** — the projection is compared against the device's actual CONFIG_DB, producing a list of missing, extra, and modified entries.
3. **Delivery** — the diff is applied to the device, bringing it into alignment with the topology definition.
4. **Config Save** — the device config is persisted to survive reboots.

This is the same reconcile mechanism described in [§16.1.3](#1613-reconcile), but with `--topology` to source intents from `topology.json` instead of device NEWTRON_INTENT records.

---

## 18. Troubleshooting

### 18.1 Connection Issues

**"redis connection failed"** — the server can't reach the device's Redis.

1. Check that `ssh_user` and `ssh_pass` are set in the device profile (enables SSH tunnel)
2. Verify management IP: `ping <mgmt_ip>`
3. Verify SSH: `ssh <ssh_user>@<mgmt_ip>` manually
4. In lab: check VM health with `newtlab status`

**"SSH dial ... connection refused"** — SSH port not reachable.

1. Verify SSH service is running on the device: `ssh <ssh_user>@<mgmt_ip>` manually
2. Check SSH credentials in the profile (`ssh_user` and `ssh_pass`)
3. In lab: ensure `newtlab deploy` completed successfully
4. Check `ssh_port` in profile matches actual SSH port
5. In lab: check that the VM is healthy: `newtlab status`

**"server not reachable"** — the CLI can't reach `newtron-server`.

1. Verify the server is running: `curl http://localhost:8080/health`
2. Check the `--server` flag or `NEWTRON_SERVER` environment variable
3. If remote: check firewall allows the server port

### 18.2 Changes Lost After Reboot

**Cause:** CONFIG_DB is Redis (in-memory). Changes are ephemeral until saved.

**Solution:** Newtron saves automatically with `-x`. If you used `--no-save`, explicitly save:

```bash
ssh admin@<mgmt_ip> 'sudo config save -y'
```

See [§16.4 Config Persistence](#164-config-persistence) for details.

### 18.3 Precondition Failures

Operations have preconditions that prevent invalid state. Follow the dependency chain:

```bash
# "VTEP not configured" → set up EVPN first
newtron leaf1 evpn setup -x
newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x

# "VRF has interfaces bound" → remove interfaces first
newtron leaf1 vrf remove-interface Vrf_CUST1 Ethernet8 -x
newtron leaf1 vrf delete Vrf_CUST1 -x

# "interface already has service" → remove existing service first
newtron leaf1 service remove Ethernet0 -x
newtron leaf1 service apply Ethernet0 new-service --ip 10.1.1.1/30 -x

# "interface is a member of LAG" → configure the LAG, not the member
newtron leaf1 service apply PortChannel100 customer-l3 --ip 10.1.1.1/30 -x
```

### 18.4 Stale State After Tests

```bash
# Check what's different from expected state
newtron leaf1 intent drift

# Reconcile to restore intent-defined state
newtron leaf1 intent reconcile -x

# Or reload persisted config (discards all runtime changes)
ssh admin@<mgmt_ip> 'sudo config reload -y'
```

### 18.5 Verbose Mode

```bash
newtron -v leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30

# Shows detailed logs including:
# - Configuration loading
# - Server HTTP requests
# - Precondition checks
# - Change generation
```

### 18.6 Audit Logs

Check what changes were made:

```bash
newtron audit list --last 24h

# Output:
# Timestamp            User    Operation          Device      Status
# ---------            ----    ---------          ------      ------
# 2024-01-15 10:30:00  alice   service apply      leaf1       success
# 2024-01-15 10:25:00  alice   lag create         leaf1       success
# 2024-01-15 10:20:00  bob     service apply      leaf1       failed
```

### 18.7 Reset to Clean State

If configuration is in a bad state, use escalating options:

```bash
# Option 1: Remove services from specific interfaces and start fresh
newtron leaf1 service remove Ethernet0 -x

# Option 2: Reconcile to restore intent-defined state
newtron leaf1 intent reconcile -x

# Option 3: Reload persisted config (on switch, reverts to last saved state)
ssh admin@<mgmt_ip> 'sudo config reload -y'
```

### 18.8 Common Error Reference

| Error | Cause | Fix |
|-------|-------|-----|
| `device required` | Missing device name | Add device as first arg or `-D` |
| `interface does not exist` | Typo in interface name | Check `interface list` |
| `VRF does not exist` | Shared VRF not created | Create VRF first or use `vrf_type: interface` |
| `VRF has interfaces bound` | Trying to delete VRF with interfaces | Remove interfaces first with `vrf remove-interface` |
| `VTEP not configured` | EVPN service without overlay | Run `evpn setup -x` |
| `BGP not configured` | EVPN service without BGP | Configure BGP (manual or via provisioning) |
| `interface is a member of LAG` | Configuring LAG member directly | Configure the parent LAG |
| `L2 and L3 are mutually exclusive` | Adding VLAN member to routed port | Remove IP/VRF first |
| `interface already has service` | Service already bound | Remove existing service first |
| `redis connection failed` | Port 6379 not forwarded | Add ssh_user/ssh_pass to profile for SSH tunnel |
| `SSH dial...connection refused` | Port 22 not reachable | Check SSH service, network, VM health |
| `device is locked` | Another operation in progress | Wait (lock has TTL auto-expiry) |

---

## 19. Go API Usage

Why use the Go API? The Go API is for building automation on top of newtron — test frameworks, provisioners, custom tooling. The CLI uses it internally (via the HTTP client), but you can also use the `pkg/newtron` types and client package directly.

### 19.1 HTTP Client (Recommended)

The `pkg/newtron/client` package is the standard way to talk to `newtron-server`:

```go
package main

import (
    "fmt"
    "log"

    "github.com/newtron-network/newtron/pkg/newtron"
    "github.com/newtron-network/newtron/pkg/newtron/client"
)

func main() {
    c := client.New("http://localhost:8080", "default")
    if err := c.RegisterNetwork("/etc/newtron"); err != nil {
        log.Fatal(err)
    }

    // Spec-level reads (no device)
    services, _ := c.ListServices()
    for _, name := range services {
        svc, _ := c.ShowService(name)
        fmt.Printf("%s: %s (%s)\n", name, svc.Description, svc.ServiceType)
    }

    // Device reads
    info, _ := c.DeviceInfo("leaf1")
    fmt.Printf("Device: %s, AS: %d\n", info.Name, info.BGPAS)

    interfaces, _ := c.ListInterfaces("leaf1")
    for _, iface := range interfaces {
        fmt.Printf("%s: %s %s\n", iface.Name, iface.AdminStatus, iface.Service)
    }

    // Device writes (dry-run by default)
    opts := newtron.ExecOpts{Execute: true}
    result, err := c.CreateVLAN("leaf1", 100, "Server VLAN", opts)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Applied: %v, Changes: %d\n", result.Applied, result.ChangeCount)
}
```

### 19.2 Client Method Patterns

**Spec reads** return typed detail structs:

```go
svc, err := c.ShowService("customer-l3")        // *newtron.ServiceDetail
ipvpn, err := c.ShowIPVPN("customer-vpn")        // *newtron.IPVPNDetail
policy, err := c.ShowQoSPolicy("customer-4q")    // *newtron.QoSPolicyDetail
```

**Device reads** take a device name and return typed views:

```go
vlans, err := c.ListVLANs("leaf1")               // []newtron.VLANStatusEntry
vrf, err := c.ShowVRF("leaf1", "Vrf_CUST1")      // *newtron.VRFDetail
bgp, err := c.BGPStatus("leaf1")                  // *newtron.BGPStatusResult
```

**Device writes** take a device name, parameters, and `ExecOpts`, returning `*WriteResult`:

```go
opts := newtron.ExecOpts{Execute: true, NoSave: false}
result, err := c.SetupEVPN("leaf1", "", opts)
result, err = c.CreateVRF("leaf1", "Vrf_CUST1", opts)
result, err = c.BindIPVPN("leaf1", "Vrf_CUST1", "customer-vpn", opts)
```

**Interface writes** take device, interface, and parameters:

```go
serviceOpts := newtron.ApplyServiceOpts{IPAddress: "10.1.1.1/30", PeerAS: 65100}
result, err := c.ApplyService("leaf1", "Ethernet0", "customer-l3", serviceOpts, opts)
result, err = c.RemoveService("leaf1", "Ethernet0", opts)
```

**Intent operations:**

```go
// View intent tree
tree, err := c.IntentTree("leaf1", "", "", false)

// Detect drift
entries, err := c.IntentDrift("leaf1", "")

// Reconcile (topology mode)
result, err := c.Reconcile("leaf1", "topology", newtron.ExecOpts{Execute: true})
fmt.Printf("Reconciled: %d entries applied\n", result.Applied)

// Save intents to topology.json
snap, err := c.IntentSave("leaf1", "")
fmt.Printf("Saved %d steps\n", len(snap.Steps))
```

### 19.3 Error Handling

The client wraps server errors in `*client.ServerError`:

```go
result, err := c.CreateVLAN("leaf1", 100, "Test", opts)
if err != nil {
    var serverErr *client.ServerError
    if errors.As(err, &serverErr) {
        switch serverErr.StatusCode {
        case 404:
            fmt.Println("Device not found")
        case 400:
            fmt.Println("Validation error:", serverErr.Message)
        case 409:
            fmt.Println("Conflict:", serverErr.Message)
        }
    }
}
```

### 19.4 Direct Library Usage

The HTTP client is the recommended integration path. Direct library usage (`pkg/newtron/network/`, `pkg/newtron/network/node/`) is internal and subject to change. All external consumers — CLI, newtrun, custom tooling — should use the HTTP client against `newtron-server`.

---

## 20. End-to-End Workflows

These workflows show how newtron primitives compose for common operational tasks.

### 20.1 Day-1: New Device Provisioning

Starting from a freshly deployed SONiC device with SSH access:

```bash
# 1. Start the server (if not already running)
newtron-server -spec-dir /etc/newtron &

# 2. Initialize the device (enables frrcfgd unified config mode)
newtron leaf1 init

# 3. Provision from topology (reconcile delivers the full projection)
newtron leaf1 --topology intent reconcile -x

# 4. Verify BGP sessions
newtron leaf1 bgp status

# 5. Run health checks
newtron leaf1 health check
```

### 20.2 Add L3 Customer Interface

Starting from a provisioned switch with EVPN overlay already set up:

```bash
# 1. Apply customer service
newtron leaf1 service apply Ethernet4 customer-l3 --ip 10.1.1.1/30 --peer-as 65100 -x

# 2. Verify service binding
newtron leaf1 service get Ethernet4

# 3. Verify BGP neighbor came up
newtron leaf1 bgp status
```

### 20.3 Add L2 VLAN Extension

```bash
# 1. Create VLAN
newtron leaf1 vlan create 100 --description Servers -x

# 2. Add interface to VLAN
newtron leaf1 vlan add-interface 100 Ethernet5 -x

# 3. Bind to MAC-VPN for VXLAN extension
newtron leaf1 vlan bind-macvpn 100 servers-vlan100 -x
```

### 20.4 New Customer Onboarding (EVPN L3)

Starting from a provisioned switch with EVPN overlay already set up:

```bash
# 1. Define the VPN (if not already defined)
newtron evpn ipvpn create customer-vpn --l3vni 10001 --route-targets 65001:1 -x

# 2. Define the service
newtron service create customer-l3 --type evpn-routed --ipvpn customer-vpn \
  --vrf-type interface --ingress-filter customer-ingress --qos-policy customer-4q -x

# 3. Apply to customer-facing interfaces on each switch
newtron leaf1 service apply Ethernet4 customer-l3 --ip 10.1.1.1/30 --peer-as 65100 -x
newtron leaf2 service apply Ethernet4 customer-l3 --ip 10.1.2.1/30 --peer-as 65200 -x

# 4. Verify
newtron leaf1 service get Ethernet4
newtron leaf1 bgp status
newtron leaf1 health check
```

### 20.5 Add Customer VRF with Full Connectivity

```bash
# 1. Create VRF
newtron leaf1 vrf create Vrf_CUST1 -x

# 2. Add customer-facing interfaces
newtron leaf1 vrf add-interface Vrf_CUST1 Ethernet8 -x
newtron leaf1 vrf add-interface Vrf_CUST1 Ethernet12 -x

# 3. Add eBGP neighbors
newtron leaf1 vrf add-neighbor Vrf_CUST1 Ethernet8 65100 -x
newtron leaf1 vrf add-neighbor Vrf_CUST1 Ethernet12 65200 -x

# 4. Bind to IP-VPN for EVPN extension
newtron leaf1 vrf bind-ipvpn Vrf_CUST1 customer-vpn -x

# 5. Add a static route for a non-BGP destination
newtron leaf1 vrf add-route Vrf_CUST1 10.99.0.0/16 10.1.1.2 -x

# 6. Verify
newtron leaf1 vrf show Vrf_CUST1
newtron leaf1 bgp status
```

### 20.6 Decommission a Customer VRF

The reverse of the setup workflow — remove in dependency order:

```bash
# 1. Remove static routes
newtron leaf1 vrf remove-route Vrf_CUST1 10.99.0.0/16 -x

# 2. Remove BGP neighbors
newtron leaf1 vrf remove-neighbor Vrf_CUST1 Ethernet8 -x
newtron leaf1 vrf remove-neighbor Vrf_CUST1 Ethernet12 -x

# 3. Unbind IP-VPN
newtron leaf1 vrf unbind-ipvpn Vrf_CUST1 -x

# 4. Remove interfaces
newtron leaf1 vrf remove-interface Vrf_CUST1 Ethernet8 -x
newtron leaf1 vrf remove-interface Vrf_CUST1 Ethernet12 -x

# 5. Delete VRF
newtron leaf1 vrf delete Vrf_CUST1 -x

# 6. Verify no drift
newtron leaf1 intent drift
```

### 20.7 Service Lifecycle: Apply, Modify, Remove

```bash
# Apply
newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x

# Modify the filter (add a rule to block a prefix)
newtron filter add-rule customer-ingress --priority 150 --action deny \
  --src-ip 172.16.0.0/12 -x

# Refresh to pick up changes
newtron leaf1 service refresh Ethernet0 -x

# Eventually remove
newtron leaf1 service remove Ethernet0 -x

# Verify no drift
newtron leaf1 intent drift
```

### 20.8 Lab Deployment and Testing

```bash
# Build all tools
make build

# Deploy topology (uses newtlab)
bin/newtlab deploy newtrun/topologies/2node-ngdp

# Start server pointing at lab specs
bin/newtron-server -spec-dir newtrun/topologies/2node-ngdp/specs &

# Provision switches from topology
bin/newtron -S newtrun/topologies/2node-ngdp/specs -D switch1 --topology intent reconcile -x
bin/newtron -S newtrun/topologies/2node-ngdp/specs -D switch2 --topology intent reconcile -x

# Verify health
bin/newtron -S newtrun/topologies/2node-ngdp/specs -D switch1 health check
bin/newtron -S newtrun/topologies/2node-ngdp/specs -D switch2 health check

# Run E2E test suite (uses newtrun)
bin/newtrun start newtrun/suites/2node-ngdp-primitive
```

---

## 21. Quick Reference

### 21.1 Command Tree

```
newtron <device> <noun> <action> [args] [-x]

Spec-Level (no device needed)
├── service    list | show | create | delete
├── evpn
│   ├── ipvpn    list | show | create | delete
│   └── macvpn   list | show | create | delete
├── filter     list | show | create | delete | add-rule | remove-rule
├── qos        list | show | create | delete | add-queue | remove-queue
├── profile    list | show | create | delete
├── zone       list | create | delete
├── platform   list | show
├── settings   show | set | get | clear | path
├── audit      list
└── version

Device-Scoped (requires device name)
├── show
├── init
├── interface  list | show | get | set | list-acls | list-members
├── service    apply | remove | refresh | get
├── vlan       list | show | status | create | delete
│              add-interface | remove-interface | configure-svi
│              bind-macvpn | unbind-macvpn
├── vrf        list | show | status | create | delete
│              add-interface | remove-interface
│              bind-ipvpn | unbind-ipvpn
│              add-neighbor | remove-neighbor
│              add-route | remove-route
├── evpn       setup | status
├── lag        list | show | status | create | delete
│              add-interface | remove-interface
├── acl        list | show | create | delete
│              add-rule | delete-rule | bind | unbind
├── bgp        status
├── qos        apply | remove
├── health     check
└── intent     tree | drift | reconcile | save | reload | clear
```

### 21.2 Key Patterns

| Pattern | Example |
|---------|---------|
| Preview before executing | `newtron leaf1 vlan create 100` (dry-run) |
| Execute | `newtron leaf1 vlan create 100 -x` |
| Execute without saving | `newtron leaf1 vlan create 100 -x --no-save` |
| JSON output | `newtron leaf1 interface list --json` |
| Verbose | `newtron -v leaf1 service apply ...` |
| Spec edit | `newtron service create my-svc --type routed -x` |

### 21.3 Dependency Order for New Device

1. Start server: `newtron-server -spec-dir /etc/newtron`
2. Initialize device: `newtron leaf1 init`
3. Provision from topology: `newtron leaf1 --topology intent reconcile -x`
4. Or apply services individually: `newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x`
5. Verify: `newtron leaf1 health check`
6. Check drift: `newtron leaf1 intent drift`
7. Persist: automatic with `-x` (or `ssh admin@<ip> 'sudo config save -y'` if `--no-save` was used)

---

## 22. Related Documentation

| Document | What It Covers |
|----------|---------------|
| [HLD](hld.md) | Architecture, design decisions, verification model |
| [LLD](lld.md) | Type definitions, HTTP API routes, CONFIG_DB tables |
| [Device LLD](device-lld.md) | SSH tunnels, Redis clients, state access |
| [API Reference](api.md) | HTTP API endpoints, request/response types |
| [newtrun HLD](../newtrun/hld.md) | E2E test framework design |
| [newtrun HOWTO](../newtrun/howto.md) | Writing and running test suites |
| [newtlab HOWTO](../newtlab/howto.md) | Deploying lab topologies |
| [RCA Index](../rca/) | Root-cause analyses for SONiC pitfalls |
