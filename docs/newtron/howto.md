# Newtron HOWTO Guide

newtron interacts with SONiC devices through Redis, not CLI commands. Every operation in this guide reads or writes CONFIG_DB, APP_DB, ASIC_DB, or STATE_DB through an SSH-tunneled Redis client. All write operations default to dry-run (preview only) — add `-x` to execute. For the architectural principles behind this design, see [Design Principles](../DESIGN_PRINCIPLES.md).

---

## Table of Contents

1. [Installation](#1-installation)
2. [Connection Architecture](#2-connection-architecture)
3. [Specification Setup](#3-specification-setup)
4. [Basic Usage](#4-basic-usage)
5. [Service Management](#5-service-management)
6. [Interface Configuration](#6-interface-configuration)
7. [Link Aggregation (LAG)](#7-link-aggregation-lag)
8. [VLAN Management](#8-vlan-management)
9. [VRF Management](#9-vrf-management)
10. [EVPN/VXLAN Configuration](#10-evpnvxlan-configuration)
11. [ACL Management](#11-acl-management)
12. [BGP Visibility](#12-bgp-visibility)
13. [QoS Management](#13-qos-management)
14. [Filter Management](#14-filter-management)
15. [Health Checks](#15-health-checks)
16. [Baseline Configuration](#16-baseline-configuration)
17. [Cleanup Operations](#17-cleanup-operations)
18. [Config Persistence](#18-config-persistence)
19. [Lab Environment](#19-lab-environment)
20. [Troubleshooting](#20-troubleshooting)
21. [Go API Usage](#21-go-api-usage)
22. [Quick Reference](#22-quick-reference)
23. [Topology Provisioning](#23-topology-provisioning)
24. [Composite Configuration](#24-composite-configuration)
25. [End-to-End Workflows](#25-end-to-end-workflows)
26. [CONFIG_DB Cache Behavior](#26-config_db-cache-behavior)
27. [Related Documentation](#27-related-documentation)

---

## 1. Installation

### 1.1 Build from Source

```bash
# Clone the repository
git clone https://github.com/newtron-network/newtron.git
cd newtron

# Build the binary
go build -o bin/newtron ./cmd/newtron

# Install to PATH
sudo mv newtron /usr/local/bin/
```

### 1.2 Verify Installation

```bash
newtron version
# Output: newtron version 1.0.0

newtron --help
```

### 1.3 Build Lab Tools

If you plan to use lab environments:

```bash
# Build everything
make build
```

---

## 2. Connection Architecture

Newtron configures SONiC switches by reading and writing to Redis databases inside each device. Understanding the connection path is essential for both production deployment and lab environments.

### 2.1 How Newtron Talks to Devices

SONiC stores all configuration in **CONFIG_DB** (Redis database 4) and exposes operational state in **STATE_DB** (Redis database 6). Newtron connects to these Redis instances to read current state and apply configuration changes.

The connection path depends on whether SSH credentials are present in the device profile:

```
With SSH credentials (production / lab):
  newtron CLI --> SSH to device:22 --> forward to 127.0.0.1:6379 (Redis)

Without SSH credentials (integration testing with direct Redis):
  newtron CLI --> direct TCP to device:6379 (Redis)
```

### 2.2 Why SSH Tunnels?

Redis inside SONiC listens on `127.0.0.1:6379` and has no authentication. In QEMU/SLiRP networking environments (SONiC-VS), **port 6379 is not forwarded** by the QEMU user-mode networking stack. Only port 22 (SSH) is accessible from the management network.

The SSH tunnel solves this by:

1. Connecting to the device via SSH on port 22
2. Opening a local listener on a random port (e.g., `127.0.0.1:54321`)
3. Forwarding connections from that local port through SSH to `127.0.0.1:6379` inside the device

### 2.3 SSH Credentials in Device Profile

SSH credentials are specified in the device profile JSON with the `ssh_user` and `ssh_pass` fields:

```json
{
  "mgmt_ip": "172.20.20.2",
  "loopback_ip": "10.0.0.10",
  "zone": "datacenter-east",
  "platform": "accton-as7726-32x",
  "ssh_user": "cisco",
  "ssh_pass": "cisco123"
}
```

When both `ssh_user` and `ssh_pass` are set, `Node.Connect()` creates an SSH tunnel automatically. When either is empty, it connects directly to `<mgmt_ip>:6379`.

### 2.4 Connection Flow (Code Reference)

From `pkg/newtron/device/sonic/device.go`, the `Connect()` method implements this logic:

```go
func (d *Device) Connect(ctx context.Context) error {
    var addr string
    if d.Profile.SSHUser != "" && d.Profile.SSHPass != "" {
        // SSH tunnel path: connect SSH to device:22, forward to 127.0.0.1:6379
        tun, err := NewSSHTunnel(d.Profile.MgmtIP, d.Profile.SSHUser, d.Profile.SSHPass, d.Profile.SSHPort)
        if err != nil {
            return fmt.Errorf("SSH tunnel to %s: %w", d.Name, err)
        }
        d.tunnel = tun
        addr = tun.LocalAddr() // e.g., "127.0.0.1:54321"
    } else {
        // Direct path: connect to device Redis directly
        addr = fmt.Sprintf("%s:6379", d.Profile.MgmtIP)
    }

    // Connect to CONFIG_DB (DB 4) and STATE_DB (DB 6) via the resolved address
    d.client = NewConfigDBClient(addr)
    // ...
}
```

### 2.5 Host Key Verification

The SSH tunnel currently uses `InsecureIgnoreHostKey()` for the host key callback. This is appropriate for lab and test environments but **must be replaced with proper host key verification for production deployments**. The relevant code is in `pkg/newtron/device/sonic/types.go`:

```go
config := &ssh.ClientConfig{
    User: user,
    Auth: []ssh.AuthMethod{ssh.Password(pass)},
    // Lab/test environment -- production would verify host keys.
    HostKeyCallback: ssh.InsecureIgnoreHostKey(),
}
```

### 2.6 Four-Database Access

On connection, newtron opens four Redis clients through the same tunnel (or direct address):

| Database | Redis DB Number | Purpose | Fatal on failure? |
|----------|----------------|---------|-------------------|
| CONFIG_DB | 4 | Configuration state (read/write) | Yes |
| STATE_DB | 6 | Operational state (read-only) | No |
| APP_DB | 0 | Route verification — routes from FRR/fpmsyncd | No |
| ASIC_DB | 1 | ASIC-level verification — SAI objects from orchagent | No |

Only CONFIG_DB connection failure is fatal. STATE_DB, APP_DB, and ASIC_DB failures are logged as warnings and the system continues. Configuration operations work without them; only routing state queries (`GetRoute`, `GetRouteASIC`) and operational state queries (`bgp status`, health checks) require the non-CONFIG_DB clients.

---

## 3. Specification Setup

Newtron uses **specification files** (declarative intent) to define what you want. These specs are translated to device **configuration** (imperative state) at runtime.

### 3.1 Directory Structure

Create the specification directory:

```bash
sudo mkdir -p /etc/newtron/profiles
```

```
/etc/newtron/
    ├── network.json        # Service definitions, VPNs, filters, policies
    ├── platforms.json      # Hardware platform definitions
    ├── topology.json       # (optional) Topology for automated provisioning
    └── profiles/
        ├── leaf1-dc1.json
        └── spine1-dc1.json
```

For lab environments, newtlab generates specs per topology (see `docs/newtlab/`).

### 3.2 Network Specification (`network.json`)

The main specification file defines VPN parameters, services, filters, and permissions.

```bash
sudo vim /etc/newtron/network.json
```

```json
{
  "version": "1.0",
  "lock_ttl": 3600,
  "super_users": ["admin"],
  "user_groups": {
    "neteng": ["alice", "bob"],
    "netops": ["charlie", "diana"]
  },
  "permissions": {
    "service.apply": ["neteng", "netops"],
    "service.remove": ["neteng"],
    "lag.create": ["neteng"],
    "acl.modify": ["neteng"],
    "all": ["neteng"]
  },
  "zones": {
    "datacenter-east": {
      "prefix_lists": {
        "dc-east-aggregates": ["10.100.0.0/16"]
      }
    }
  },
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
        { "name": "best-effort",  "type": "dwrr", "weight": 40, "dscp": [0] },
        { "name": "business",     "type": "dwrr", "weight": 30, "dscp": [10, 18, 20] },
        { "name": "voice",        "type": "strict",             "dscp": [46] },
        { "name": "network-ctrl", "type": "strict",             "dscp": [48, 56] }
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
    },
    "server-vpn": {
      "description": "Server/datacenter shared VRF",
      "vrf": "Vrf_server",
      "l3vni": 20001,
      "route_targets": ["65001:100"]
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
      "description": "L3 routed customer interface",
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
      "description": "Server VLAN with shared VRF",
      "service_type": "evpn-irb",
      "vrf_type": "shared",
      "ipvpn": "server-vpn",
      "macvpn": "servers-vlan100"
    },
    "transit": {
      "description": "Transit (global table, no VPN)",
      "service_type": "routed",
      "ingress_filter": "transit-protect"
    },
    "access-vlan": {
      "description": "Campus access VLAN with edge filtering",
      "service_type": "bridged",
      "ingress_filter": "campus-edge-in",
      "qos_policy": "campus-4q"
    },
    "local-irb": {
      "description": "Management VLAN with local routing",
      "service_type": "irb",
      "vrf_type": "shared",
      "ingress_filter": "mgmt-protect",
      "egress_filter": "mgmt-egress",
      "qos_policy": "mgmt-2q"
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

**Service Types:**

| Type            | Description                            | Requires                              |
|-----------------|----------------------------------------|---------------------------------------|
| `routed`        | L3 routed interface (local)            | IP address at apply time              |
| `bridged`       | L2 bridged interface (local)           | VLAN at apply time                    |
| `irb`           | Integrated routing and bridging (local)| VLAN + IP at apply time               |
| `evpn-routed`   | L3 routed with EVPN overlay            | `ipvpn` reference                     |
| `evpn-bridged`  | L2 bridged with EVPN overlay           | `macvpn` reference                    |
| `evpn-irb`      | IRB with EVPN overlay + anycast GW     | Both `ipvpn` and `macvpn` references  |

**VRF Instantiation (`vrf_type`):**

| Value | Behavior |
|-------|----------|
| `"interface"` | Per-interface VRF: `{service}-{interface}` |
| `"shared"` | Shared VRF: name = ipvpn definition name |
| (omitted) | Global routing table (no VPN) |

**QoS Policies:**

QoS policies define declarative queue configurations. Each policy contains 1-8 queues, where array position = queue index = traffic class. Services reference policies by name via `qos_policy`.

Queue types:
- `dwrr` -- Deficit Weighted Round Robin, requires `weight` (percentage)
- `strict` -- Strict priority, must not have `weight`

Optional: `ecn: true` on a queue creates a shared WRED profile with ECN marking.

All 64 DSCP values are mapped: explicitly listed values go to their queue, unmapped values default to queue 0.

### 3.3 Platform Specification (`platforms.json`)

Define supported hardware platforms:

```bash
sudo vim /etc/newtron/platforms.json
```

```json
{
  "version": "1.0",
  "platforms": {
    "accton-as7726-32x": {
      "hwsku": "Accton-AS7726-32X",
      "description": "Accton AS7726-32X 100G switch",
      "port_count": 32,
      "default_speed": "100G"
    },
    "dell-s5248f-on": {
      "hwsku": "DellEMC-S5248f-P-25G-DPB",
      "description": "Dell S5248F-ON 25G switch",
      "port_count": 48,
      "default_speed": "25G"
    }
  }
}
```

### 3.4 Device Profiles

Create a profile for each device. The profile is the **single source of truth** for device-specific data (IPs, platform, SSH credentials):

```bash
sudo vim /etc/newtron/profiles/leaf1-dc1.json
```

```json
{
  "mgmt_ip": "192.168.1.10",
  "loopback_ip": "10.0.0.10",
  "zone": "datacenter-east",
  "platform": "accton-as7726-32x",
  "ssh_user": "admin",
  "ssh_pass": "YourSonicPassword"
}
```

**Required Fields:**

| Field | Description |
|-------|-------------|
| `mgmt_ip` | Management IP address (for SSH/Redis connection) |
| `loopback_ip` | Loopback IP (used as router-id, VTEP source, BGP neighbor) |
| `zone` | Zone name (must exist in network.json zones) |

**Optional Fields:**

| Field | Description |
|-------|-------------|
| `platform` | Platform name (maps to platforms.json for HWSKU) |
| `ssh_user` | SSH username for Redis tunnel access |
| `ssh_pass` | SSH password for Redis tunnel access |
| `underlay_asn` | eBGP underlay AS number (unique per device) |
| `evpn` | EVPN overlay peering configuration (see below) |
| `vlan_port_mapping` | Device-specific VLAN-to-port mappings |
| `prefix_lists` | Device-specific prefix lists (merged with zone/global) |

**EVPN Configuration Fields:**

The optional `evpn` object configures EVPN overlay peering:

| Field | Description |
|-------|-------------|
| `peers` | List of loopback IPs for EVPN iBGP peers (explicit peering list) |
| `route_reflector` | Boolean — mark device as EVPN route reflector |
| `cluster_id` | Route reflector cluster ID (required if `route_reflector` is true) |

Create profiles for route reflectors too:

```bash
sudo vim /etc/newtron/profiles/spine1-dc1.json
```

```json
{
  "mgmt_ip": "192.168.1.1",
  "loopback_ip": "10.0.0.1",
  "zone": "datacenter-east",
  "platform": "accton-as7726-32x",
  "evpn": {
    "route_reflector": true,
    "cluster_id": "10.0.0.1"
  },
  "ssh_user": "admin",
  "ssh_pass": "YourSonicPassword"
}
```

### 3.5 Profile Resolution and Inheritance

When a device is loaded, newtron resolves its profile through a three-level inheritance chain:

```
Device Profile  >  Zone defaults  >  Global defaults
```

The `ResolvedProfile` includes:

- **From profile:** device name, mgmt_ip, loopback_ip, zone, platform, SSH credentials, underlay_asn, EVPN peering config
- **Derived at runtime:** router-id (= loopback_ip), VTEP source IP (= loopback_ip), BGP EVPN neighbors (from profile EVPN peers), BGP neighbor ASNs
- **Merged maps:** prefix_lists, filters, QoS policies are merged (profile > zone > global)

### 3.6 Topology Specification (Optional)

```json
{
  "version": "1.0",
  "description": "DC1 lab topology",
  "devices": {
    "leaf1-dc1": {
      "interfaces": {
        "Ethernet0": {
          "service": "fabric-underlay",
          "ip": "10.10.1.1/31"
        },
        "Ethernet4": {
          "service": "customer-l3",
          "ip": "10.1.1.1/30",
          "params": { "peer_as": "65100" }
        }
      }
    }
  }
}
```

Topology spec is optional. When present, it enables automated provisioning via `TopologyProvisioner`.

---

## 4. Basic Usage

### 4.1 Command Structure (Noun-Group CLI)

The CLI follows a **noun-group** design where commands are organized by resource type. The first positional argument is the device name unless it matches a known command:

```
newtron <device> <noun> <action> [args] [-x]
         |         |       |              |
         +- scope -+       +-- operation -+
```

Examples:

```bash
newtron leaf1 interface list                # device-required
newtron leaf1 vlan create 100 -x            # write with execute
newtron service list                        # no device needed
newtron evpn ipvpn list                     # no device needed
```

**Implicit device detection:** If the first argument does not match a known command name, it is treated as the device name. This means `newtron leaf1 vlan list` is equivalent to `newtron -d leaf1 vlan list`.

**Two command scopes:**

| Scope | Description | Examples |
|-------|-------------|----------|
| Device-required | Reads/writes CONFIG_DB on a specific device | `interface list`, `vlan create`, `vrf add-neighbor` |
| No-device | Reads/writes network.json specs | `service list`, `evpn ipvpn create`, `qos create`, `filter list` |

**Interface Name Formats:**

Both short and full interface names are accepted. Short names are automatically expanded:

| Short | Full (SONiC) | Example Usage |
|-------|--------------|---------------|
| `Eth0` | `Ethernet0` | `interface show Eth0` |
| `Po100` | `PortChannel100` | `lag show Po100` |
| `Vl100` | `Vlan100` | `vlan show 100` |
| `Lo0` | `Loopback0` | `interface show Lo0` |

Case-insensitive: `eth0`, `Eth0`, `ETH0` all work.

**Flags:**

| Flag | Description |
|------|-------------|
| `-d, --device` | Target device name (optional if implicit device used) |
| `-n, --network` | Network configuration name |
| `-S, --specs` | Specification directory (default: `/etc/newtron`) |
| `-x, --execute` | Execute changes (default: dry-run) |
| `--no-save` | Skip config save after execute (save is default with `-x`) |
| `-v, --verbose` | Verbose output |
| `--json` | JSON output format |

### 4.2 Persistent Settings

Store default values to avoid repeating flags:

```bash
# Set default network
newtron settings set network production

# Set spec directory
newtron settings set specs /etc/newtron

# Set default test suite
newtron settings set suite newtest/suites/2node-incremental

# View current settings
newtron settings show

# Clear all settings
newtron settings clear
```

Settings are stored in `~/.newtron/settings.json`.

### 4.3 Dry-Run Mode (Default)

By default, all write commands run in dry-run mode, showing what changes would be made without applying them:

```bash
newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30

# Output:
# Changes to be applied:
#   [ADD] VRF|customer-l3-Ethernet0
#   [ADD] INTERFACE|Ethernet0 -> {"vrf_name": "customer-l3-Ethernet0"}
#   [ADD] INTERFACE|Ethernet0|10.1.1.1/30 -> {}
#   [ADD] ACL_TABLE|customer-l3-in -> {...}
#   ...
#
# DRY-RUN: No changes applied. Use -x to execute.
```

### 4.4 Execute Mode

Add the `-x` flag to actually apply changes:

```bash
newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x

# Output:
# Changes to be applied:
#   [ADD] VRF|customer-l3-Ethernet0
#   ...
#
# Changes applied successfully.
```

Execute mode (`-x`) saves automatically by default. Use `--no-save` to skip:

```bash
newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x             # execute + save
newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x --no-save   # execute without saving
```

### 4.5 View Device Status

```bash
newtron leaf1 show

# Output:
# Device: leaf1-dc1
# Management IP: 192.168.1.10
# Loopback IP: 10.0.0.10
# Platform: accton-as7726-32x (Accton-AS7726-32X)
#
# Derived Values (from spec -> device config):
#   BGP Local AS: 65001 (from profile: underlay_asn)
#   BGP Router ID: 10.0.0.10
#   BGP EVPN Neighbors: [10.0.0.1, 10.0.0.2]
#   VTEP Source: 10.0.0.10 via Loopback0
#
# State:
#   Interfaces: 32 (28 up, 4 down)
#   VLANs: 5
#   VRFs: 3
#   PortChannels: 2
```

### 4.6 Interactive Shell

Start an interactive REPL shell with a persistent device connection:

```bash
# Start shell for a device
newtron leaf1 shell

# Alias
newtron leaf1 sh
```

```
newtron(leaf1-dc1)> interface list
newtron(leaf1-dc1)> vrf show Vrf-customer
newtron(leaf1-dc1)> service apply Ethernet0 l3-transit --ip 10.1.0.0/31 --peer-as 65001 -x
newtron(leaf1-dc1)> exit
```

The shell maintains a persistent SSH tunnel and Redis connection, avoiding reconnection overhead for each command. All noun-group commands are available.

---

## 5. Service Management

Services are the primary way to configure interfaces with bundled VPN, filter, QoS, and routing settings.

### 5.1 List Available Services

No device needed -- reads from network.json:

```bash
newtron service list

# Output:
# NAME           TYPE          DESCRIPTION
# ----           ----          -----------
# customer-l3    evpn-routed   L3 routed customer interface
# server-irb     evpn-irb      Server VLAN with IRB routing
# transit        routed        Transit peering interface
# access-vlan    bridged       Campus access VLAN with edge filtering
# local-irb      irb           Management VLAN with local routing
```

### 5.2 Show Service Details

```bash
newtron service show customer-l3

# Output:
# Service: customer-l3
# Description: L3 routed customer interface
# Type: evpn-routed
#
# EVPN Configuration:
#   L3 VNI: 10001
#   VRF Type: interface (per-interface VRF)
#   Route Targets: [65001:1]
#
# Filters:
#   Ingress: customer-ingress
#   Egress: customer-egress
#
# QoS Policy: customer-4q
#
# Routing:
#   Protocol: bgp
#   Peer AS: request (provided at apply time)
#   Import Policy: customer-import
#
# Permissions:
#   service.apply: [neteng, netops]
```

### 5.3 Create a Service Definition

Create a new service definition in network.json (no device needed):

```bash
newtron service create my-service --type evpn-routed --ipvpn customer-vpn --vrf-type interface -x

newtron service create my-l2svc --type evpn-bridged --macvpn servers-vlan100 -x

newtron service create my-irb --type evpn-irb --ipvpn server-vpn --macvpn servers-vlan100 \
  --qos-policy customer-4q --ingress-filter customer-ingress --description "IRB service" -x

# Local service types (no EVPN overlay):
newtron service create my-transit --type routed --ingress-filter transit-protect -x

newtron service create my-vlan --type bridged --ingress-filter campus-edge-in \
  --qos-policy campus-4q --description "Campus access VLAN" -x

newtron service create my-local-irb --type irb --vrf-type shared \
  --ingress-filter mgmt-protect --egress-filter mgmt-egress \
  --qos-policy mgmt-2q --description "Management VLAN with local routing" -x
```

### 5.4 Delete a Service Definition

```bash
newtron service delete my-service -x
```

### 5.5 Apply a Service

Apply a service to an interface on a device:

```bash
# Dry-run first
newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30

# Execute
newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x

# With eBGP peer AS (for services with routing.peer_as: "request")
newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 --peer-as 65100 -x
```

**What happens (in order):**

1. Creates VRF `customer-l3-Ethernet0` with L3VNI 10001 (for `vrf_type: interface`)
2. Creates VXLAN_TUNNEL_MAP for the L3VNI
3. Configures BGP EVPN route targets for the VRF from the `ipvpn` definition
4. Binds Ethernet0 to the VRF
5. Configures IP 10.1.1.1/30 on interface
6. Creates or updates ACL from `customer-ingress` filter spec (shared across all interfaces using this service)
7. Binds ACL to interface (adds interface to ACL binding list)
8. Applies QoS profile mappings (DSCP-to-TC, TC-to-queue) if specified
9. Configures BGP neighbor if service has routing spec
10. Records service binding in `NEWTRON_SERVICE_BINDING` table for tracking

**Preconditions checked:**

- Device must be connected (lock is acquired automatically in execute mode)
- Interface must not be a LAG member (configure the LAG instead)
- Interface must not already have a service bound
- L3 service requires an IP address
- L2/IRB service requires a `macvpn` reference
- EVPN services require VTEP and BGP to be configured
- Shared VRF must already exist (for `vrf_type: shared`)
- All referenced filters, QoS policies, and route policies must exist in the spec

### 5.6 Apply Service to PortChannel

```bash
newtron leaf1 service apply PortChannel100 customer-l3 --ip 10.2.1.1/30 -x
```

### 5.7 Remove a Service

```bash
# Preview removal
newtron leaf1 service remove Ethernet0

# Execute removal
newtron leaf1 service remove Ethernet0 -x
```

**What happens:**

1. Removes QoS mapping from interface (always)
2. Removes IP addresses from interface (always)
3. Handles shared ACLs: removes interface from ACL binding list, or deletes ACL entirely if this was the last user
4. Unbinds interface from VRF; for per-interface VRFs (`vrf_type: interface`), deletes the VRF and all associated EVPN config (BGP_EVPN_VNI, BGP_GLOBALS_AF, VXLAN_TUNNEL_MAP)
5. For L2/IRB services: removes VLAN membership; if last member, removes all VLAN-related config (SVI, ARP suppression, L2VNI mapping, VLAN itself)
6. Deletes `NEWTRON_SERVICE_BINDING` entry

**Dependency-aware cleanup:** Shared resources (ACLs, VLANs, VRFs) are only deleted when the interface being cleaned up is the last user. This is determined by a `DependencyChecker` that counts remaining users while excluding the current interface.

### 5.8 Refresh a Service

When a service definition changes in `network.json` (e.g., updated filter-spec, QoS profile, or route policy), use `refresh` to synchronize the interface:

```bash
# Preview what would change
newtron leaf1 service refresh Ethernet0

# Output:
# Changes to synchronize service:
#   [DELETE] ACL_RULE|customer-l3-in|RULE_100
#   [DELETE] ACL_RULE|customer-l3-in|RULE_9999
#   [DELETE] ACL_TABLE|customer-l3-in
#   [DELETE] NEWTRON_SERVICE_BINDING|Ethernet0
#   [ADD] VRF|customer-l3-Ethernet0
#   [ADD] ACL_TABLE|customer-l3-in -> {...}
#   [ADD] ACL_RULE|customer-l3-in|RULE_100 -> {...}
#   [ADD] NEWTRON_SERVICE_BINDING|Ethernet0 -> {...}
#   ...
#
# DRY-RUN: No changes applied. Use -x to execute.

# Execute the refresh
newtron leaf1 service refresh Ethernet0 -x
```

**How RefreshService works internally:**

RefreshService performs a full remove-then-reapply cycle. It:

1. Saves the current service name and IP address from the interface binding
2. Calls `RemoveService()` to generate a changeset that tears down the current config
3. Calls `ApplyService()` with the saved parameters to generate a changeset that applies the current service definition
4. Merges both changesets into a single atomic changeset

This approach ensures all changes in the service definition are picked up, including:

- Filter-spec rules added, removed, or modified
- QoS profile changes
- Route policy changes
- VPN parameter changes (route targets, VNI)

**Example -- refresh after filter change:**

```bash
# 1. Edit network.json to add a new rule to the customer-ingress filter-spec
# 2. Refresh all interfaces using that service
newtron leaf1 service refresh Ethernet0 -x
newtron leaf1 service refresh Ethernet4 -x
```

### 5.9 Get Service Binding

```bash
newtron leaf1 service get Ethernet0

# Output:
# Service: customer-l3
# IP: 10.1.1.1/30
# VRF: customer-l3-Ethernet0
```

---

## 6. Interface Configuration

### 6.1 List Interfaces

```bash
newtron leaf1 interface list

# Output:
# INTERFACE      ADMIN  OPER  IP ADDRESS    VRF                   SERVICE
# ---------      -----  ----  ----------    ---                   -------
# Ethernet0      up     up    10.1.1.1/30   customer-l3-Ethernet0 customer-l3
# Ethernet4      up     up    -             -                      -
# Ethernet8      down   down  -             -                      -
# PortChannel100 up     up    10.2.1.1/30   -                      server-irb
```

### 6.2 Show Interface Details

```bash
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
#
# Counters:
#   RX: 1.2TB (packets: 892M)
#   TX: 1.1TB (packets: 845M)
#   Errors: 0
```

### 6.3 Get Interface Properties

```bash
# Get MTU
newtron leaf1 interface get Ethernet0 mtu
# Output: mtu: 9100

# Get admin status
newtron leaf1 interface get Ethernet0 admin-status
# Output: admin-status: up

# Get description
newtron leaf1 interface get Ethernet0 description
# Output: description: Uplink to spine1

# Get with JSON output
newtron leaf1 interface get Ethernet0 mtu --json
# Output: {"mtu": "9100"}
```

### 6.4 Configure Interface Properties

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

**Preconditions:** LAG members cannot be configured directly; configure the parent LAG instead.

### 6.5 List ACLs on Interface

```bash
newtron leaf1 interface list-acls Ethernet0

# Output:
# Direction  ACL Name
# ---------  --------
# ingress    customer-l3-in
# egress     customer-l3-out
```

### 6.6 List VLAN Members on Interface

```bash
newtron leaf1 interface list-members Ethernet0

# Output:
# VLAN 100 (untagged)
```

---

## 7. Link Aggregation (LAG)

### 7.1 List LAGs

```bash
newtron leaf1 lag list

# Output:
# NAME             STATUS   MEMBERS                ACTIVE
# ----             ------   -------                ------
# PortChannel100   up       Ethernet12,Ethernet16  2
# PortChannel200   up       Ethernet20,Ethernet24  2
```

### 7.2 Show LAG Details

```bash
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
```

### 7.3 LAG Status

```bash
newtron leaf1 lag status

# Output:
# LAG operational summary from STATE_DB:
#
# NAME             OPER    ACTIVE/TOTAL  MIN-LINKS
# ----             ----    ------------  ---------
# PortChannel100   up      2/2           1
# PortChannel200   up      2/2           1
```

### 7.4 Create a LAG

```bash
newtron leaf1 lag create PortChannel300 \
  --members Ethernet28,Ethernet32 \
  --mode active \
  --min-links 1 \
  --fast-rate \
  -x
```

**Parameters:**

- `--members` - Comma-separated list of member interfaces
- `--mode` - LACP mode: `active`, `passive`, or `on` (static)
- `--min-links` - Minimum links for LAG to be up
- `--fast-rate` - LACP fast rate (1s timeout vs 30s)
- `--mtu` - MTU (default: 9100)

**Preconditions checked:**

- PortChannel must not already exist
- All member interfaces must exist
- No member may already be in another LAG

### 7.5 Add Interface to LAG

```bash
newtron leaf1 lag add-interface PortChannel300 Ethernet36 -x
```

**Preconditions checked:**

- Interface must exist
- Interface must be a physical interface
- Interface must not be in another LAG
- Interface must not have a service bound

### 7.6 Remove Interface from LAG

```bash
newtron leaf1 lag remove-interface PortChannel300 Ethernet36 -x
```

**Preconditions checked:**

- Interface must be a member of this LAG

### 7.7 Delete a LAG

```bash
# Remove service first if bound
newtron leaf1 service remove PortChannel300 -x

# Then delete LAG (members are removed automatically)
newtron leaf1 lag delete PortChannel300 -x
```

---

## 8. VLAN Management

### 8.1 List VLANs

```bash
newtron leaf1 vlan list

# Output:
# VLAN ID  L2VNI  SVI       MEMBERS
# -------  -----  ---       -------
# 100      10100  up        Ethernet0,Ethernet4,PortChannel100
# 200      10200  up        Ethernet8
# 300      -      -         Ethernet12
```

### 8.2 Show VLAN Details

```bash
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
```

### 8.3 VLAN Status

```bash
newtron leaf1 vlan status

# Output:
# VLAN operational summary from STATE_DB:
#
# VLAN  OPER   L2VNI  MEMBERS  MAC-VPN
# ----  ----   -----  -------  -------
# 100   up     10100  3        servers-vlan100
# 200   up     10200  1        storage-vlan200
# 300   up     -      1        -
```

### 8.4 Create a VLAN

```bash
newtron leaf1 vlan create 400 --name "NewVLAN" --description "Test VLAN" -x
```

**Preconditions checked:**

- VLAN ID must be between 1 and 4094
- VLAN must not already exist

### 8.5 Add Interface to VLAN

```bash
# Add as untagged (access)
newtron leaf1 vlan add-interface 400 Ethernet40 -x

# Add as tagged (trunk)
newtron leaf1 vlan add-interface 400 Ethernet44 --tagged -x
```

**Preconditions checked:**

- Interface must not have IP addresses (L3 config)
- Interface must not be bound to a VRF
- L2 and L3 are mutually exclusive

### 8.6 Remove Interface from VLAN

```bash
newtron leaf1 vlan remove-interface 400 Ethernet40 -x
```

### 8.7 Configure VLAN SVI

Configure an SVI (Switched Virtual Interface) on a VLAN:

```bash
newtron leaf1 vlan configure-svi 400 --ip 10.1.40.1/24 --vrf Vrf_TENANT1 --anycast-gw -x
```

### 8.8 Bind VLAN to MAC-VPN

Bind a VLAN to a MAC-VPN definition from network.json for VXLAN extension:

```bash
newtron leaf1 vlan bind-macvpn 100 servers-vlan100 -x
```

The MAC-VPN definition in network.json specifies the L2VNI and ARP suppression:

```json
{
  "macvpns": {
    "servers-vlan100": {
      "description": "Server VLAN 100",
      "vlan_id": 100,
      "vni": 10100,
      "arp_suppression": true
    }
  }
}
```

**Preconditions checked:**

- VLAN must exist on the device
- VTEP must exist
- MAC-VPN definition must exist in network.json

### 8.9 Unbind MAC-VPN

```bash
newtron leaf1 vlan unbind-macvpn 100 -x
```

This removes the L2VNI mapping (VXLAN_TUNNEL_MAP) and ARP suppression (SUPPRESS_VLAN_NEIGH) settings.

### 8.10 Delete a VLAN

```bash
# Remove all members first
newtron leaf1 vlan remove-interface 400 Ethernet40 -x
newtron leaf1 vlan remove-interface 400 Ethernet44 -x

# Unbind MAC-VPN if present
newtron leaf1 vlan unbind-macvpn 400 -x

# Delete VLAN (also removes VNI mappings and members)
newtron leaf1 vlan delete 400 -x
```

The `DeleteVLAN` operation automatically removes VLAN members and VNI mappings as part of the changeset.

---

## 9. VRF Management

VRFs (Virtual Routing and Forwarding instances) are first-class routing contexts that own interfaces, BGP neighbors, IP-VPN bindings, and static routes.

### 9.1 List VRFs

```bash
newtron leaf1 vrf list

# Output:
# NAME               L3VNI  INTERFACES
# ----               -----  ----------
# default            -      Ethernet0,Ethernet4
# Vrf_CUST1          10001  Ethernet8,Ethernet12
# Vrf_SERVER         20001  Vlan100
```

### 9.2 Show VRF Details

```bash
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
```

### 9.3 VRF Status

```bash
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

### 9.4 Create a VRF

```bash
newtron leaf1 vrf create Vrf_CUST1 -x
```

**Preconditions checked:**

- VRF must not already exist

### 9.5 Delete a VRF

```bash
newtron leaf1 vrf delete Vrf_CUST1 -x
```

**Preconditions checked:**

- VRF must exist
- VRF must have no interfaces bound to it
- VRF must have no BGP neighbors configured
- VRF must not have an IP-VPN bound

### 9.6 Add Interface to VRF

```bash
newtron leaf1 vrf add-interface Vrf_CUST1 Ethernet8 -x
```

### 9.7 Remove Interface from VRF

```bash
newtron leaf1 vrf remove-interface Vrf_CUST1 Ethernet8 -x
```

### 9.8 Bind IP-VPN

Bind a VRF to an IP-VPN definition from network.json. This configures L3VNI, route targets, and VXLAN tunnel mapping:

```bash
newtron leaf1 vrf bind-ipvpn Vrf_CUST1 customer-vpn -x
```

### 9.9 Unbind IP-VPN

```bash
newtron leaf1 vrf unbind-ipvpn Vrf_CUST1 -x
```

### 9.10 Add BGP Neighbor

Add a BGP neighbor to a VRF. The neighbor is associated with a specific interface and remote AS:

```bash
# Auto-derived neighbor IP for /30 subnets (interface has 10.1.1.1/30, derives 10.1.1.2)
newtron leaf1 vrf add-neighbor default Ethernet0 65100 -x

# Auto-derived neighbor IP for /31 subnets (interface has 10.1.1.0/31, derives 10.1.1.1)
newtron leaf1 vrf add-neighbor default Ethernet4 65200 -x

# Explicit neighbor IP (for /29 or larger, or loopback-based peering)
newtron leaf1 vrf add-neighbor Vrf_CUST1 Loopback0 64512 --neighbor 10.0.0.1 -x

# With description
newtron leaf1 vrf add-neighbor default Ethernet0 65100 --description "Customer A uplink" -x
```

**Auto-derivation rules:**

- `/30` subnet: the other usable IP on the /30 is used (e.g., .1 derives .2, .2 derives .1)
- `/31` subnet: the other IP on the /31 is used (e.g., .0 derives .1, .1 derives .0)
- `/29` or larger: `--neighbor` flag is required (too many candidates to auto-derive)

**Validation:**

- The neighbor IP must be on the same subnet as the interface (unless loopback-based)
- The interface must have an IP address configured (unless `--neighbor` is explicit)

### 9.11 Remove BGP Neighbor

```bash
# Remove by interface
newtron leaf1 vrf remove-neighbor default Ethernet0 -x

# Remove by explicit neighbor IP
newtron leaf1 vrf remove-neighbor Vrf_CUST1 10.0.0.1 -x
```

The remove operation deletes all address-family entries (ipv4_unicast, ipv6_unicast, l2vpn_evpn) and the neighbor entry.

### 9.12 Add Static Route

```bash
newtron leaf1 vrf add-route Vrf_CUST1 10.99.0.0/16 10.1.1.2 -x

# With metric
newtron leaf1 vrf add-route Vrf_CUST1 10.99.0.0/16 10.1.1.2 --metric 100 -x
```

### 9.13 Remove Static Route

```bash
newtron leaf1 vrf remove-route Vrf_CUST1 10.99.0.0/16 -x
```

### 9.14 VRF Workflow

Typical workflow for setting up a customer VRF:

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

## 10. EVPN/VXLAN Configuration

The EVPN subsystem manages the overlay transport layer: VTEP, NVO, and BGP EVPN sessions.

### 10.1 Setup EVPN Overlay

The `setup` command is an idempotent composite that configures the full EVPN stack in one shot:

```bash
# Uses loopback IP from device profile as source IP
newtron leaf1 evpn setup -x

# Explicit source IP
newtron leaf1 evpn setup --source-ip 10.0.0.10 -x
```

**What setup creates (skips any that already exist):**

1. `VXLAN_TUNNEL|vtep1` with `src_ip` set to the loopback IP
2. `VXLAN_EVPN_NVO|nvo1` with `source_vtep` pointing to `vtep1`
3. BGP EVPN sessions with peers from profile EVPN config (explicit peers or route reflector settings)

### 10.2 EVPN Status

View combined config and operational state:

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

### 10.3 IP-VPN Management (Spec Authoring)

IP-VPN definitions are spec-level objects in network.json. No device needed:

```bash
# List all IP-VPN definitions
newtron evpn ipvpn list

# Show details
newtron evpn ipvpn show customer-vpn

# Create a new IP-VPN definition
newtron evpn ipvpn create my-vpn --l3vni 10001 --route-targets 65001:1 \
  --description "Customer VPN" -x

# Delete
newtron evpn ipvpn delete my-vpn -x
```

### 10.4 MAC-VPN Management (Spec Authoring)

MAC-VPN definitions are also spec-level objects. No device needed:

```bash
# List all MAC-VPN definitions
newtron evpn macvpn list

# Show details
newtron evpn macvpn show servers-vlan100

# Create a new MAC-VPN definition
newtron evpn macvpn create my-l2vpn --vni 10100 --vlan-id 100 --arp-suppress \
  --description "Server L2 extension" -x

# Delete
newtron evpn macvpn delete my-l2vpn -x
```

### 10.5 EVPN Workflow

Set up a complete EVPN overlay from scratch:

```bash
# 1. Create VPN definitions (no device needed)
newtron evpn ipvpn create customer-vpn --l3vni 10001 --route-targets 65001:1 -x
newtron evpn macvpn create servers-vlan100 --vni 10100 --vlan-id 100 --arp-suppress -x

# 2. Set up overlay on device
newtron leaf1 evpn setup -x

# 3. Create VRF and bind to IP-VPN
newtron leaf1 vrf create Vrf_CUST1 -x
newtron leaf1 vrf bind-ipvpn Vrf_CUST1 customer-vpn -x

# 4. Create VLAN and bind to MAC-VPN
newtron leaf1 vlan create 100 -x
newtron leaf1 vlan bind-macvpn 100 servers-vlan100 -x
```

---

## 11. ACL Management

### 11.1 List ACL Tables

```bash
newtron leaf1 acl list

# Output:
# NAME                 TYPE   STAGE    INTERFACES              RULES
# ----                 ----   -----    ----------              -----
# customer-l3-in       L3     ingress  Ethernet0,Ethernet4     4
# customer-l3-out      L3     egress   Ethernet0,Ethernet4     2
# DATAACL              L3     ingress  Ethernet8               3
```

Note: ACLs created by services are shared -- the same ACL table is bound to multiple interfaces via the `ports` field.

### 11.2 Show ACL Rules

```bash
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

### 11.3 Create Custom ACL Table

```bash
newtron leaf1 acl create CUSTOM-ACL \
  --type ipv4 \
  --stage ingress \
  --interfaces Ethernet0 \
  --description "Custom access control" \
  -x
```

### 11.4 Add ACL Rule

```bash
# Permit specific subnet
newtron leaf1 acl add-rule CUSTOM-ACL RULE_100 \
  --priority 9000 \
  --src-ip 10.100.0.0/16 \
  --action permit \
  -x

# Deny SSH from external
newtron leaf1 acl add-rule CUSTOM-ACL RULE_200 \
  --priority 8000 \
  --protocol tcp \
  --dst-port 22 \
  --action deny \
  -x

# Permit all else
newtron leaf1 acl add-rule CUSTOM-ACL RULE_9999 \
  --priority 1 \
  --action permit \
  -x
```

### 11.5 Delete ACL Rule

```bash
newtron leaf1 acl delete-rule CUSTOM-ACL RULE_200 -x
```

### 11.6 Bind ACL to Interface

```bash
newtron leaf1 acl bind CUSTOM-ACL Ethernet4 --direction ingress -x
```

This adds the interface to the ACL's binding list (ACLs are shared across interfaces).

### 11.7 Unbind ACL from Interface

```bash
newtron leaf1 acl unbind CUSTOM-ACL Ethernet4 -x
```

If this is the last interface bound to the ACL, the ACL table and all its rules are deleted. Otherwise, the interface is removed from the ACL's binding list.

### 11.8 Delete ACL Table

```bash
# Unbind from all interfaces first
newtron leaf1 acl unbind CUSTOM-ACL Ethernet4 -x

# Delete table (rules are deleted automatically)
newtron leaf1 acl delete CUSTOM-ACL -x
```

### 11.9 How Filter-Specs Become ACLs

When a service with a filter_spec is applied, newtron:

1. Derives the ACL name from the service name (e.g., `customer-l3-in`, `customer-l3-out`)
2. Checks if the ACL already exists (shared across interfaces using the same service)
3. If the ACL exists: adds the new interface to the binding list
4. If the ACL does not exist: creates the ACL table and expands all rules

Rule expansion handles:

- **Prefix list references:** The `src_prefix_list`/`dst_prefix_list` fields reference entries in `prefix_lists`. Each prefix becomes a separate ACL rule (Cartesian product for src x dst).
- **CoS/TC marking:** The `cos` field maps to traffic class values (e.g., `ef` -> TC 5).
- **Priority calculation:** ACL priority is computed as `10000 - sequence_number`.

---

## 12. BGP Visibility

BGP is **visibility-only** in the noun-group CLI. It has a single `status` subcommand that shows a unified view of BGP configuration and operational state. All peer management lives in the `vrf` noun group (`vrf add-neighbor` / `vrf remove-neighbor`), and EVPN overlay sessions are managed by `evpn setup`.

### 12.1 View BGP Status

```bash
newtron leaf1 bgp status

# Output:
# BGP Status for leaf1-dc1
# ========================
#
# Identity:
#   Local AS: 65001
#   Router ID: 10.0.0.10
#   Loopback: 10.0.0.10
#
# Configured Neighbors (CONFIG_DB):
#   IP             AS      Type       VRF             Interface
#   --             --      ----       ---             ---------
#   10.0.0.1       65001   overlay    default         Loopback0
#   10.0.0.2       65001   overlay    default         Loopback0
#   10.1.1.2       65100   underlay   default         Ethernet0
#   10.1.2.2       65200   underlay   Vrf_CUST1       Ethernet8
#
# Operational State (STATE_DB):
#   Neighbor       State         Pfx Rcvd  Pfx Sent  Uptime
#   --------       -----         --------  --------  ------
#   10.0.0.1       Established   12        8         02:15:30
#   10.0.0.2       Established   12        8         02:15:28
#   10.1.1.2       Established   4         6         01:45:12
#   10.1.2.2       Established   3         5         01:30:05
```

### 12.2 Managing BGP Neighbors

All BGP peer management uses the `vrf` noun group:

```bash
# Add eBGP neighbor (auto-derived IP for /30)
newtron leaf1 vrf add-neighbor default Ethernet0 65100 -x

# Add iBGP neighbor (explicit IP for loopback-based peering)
newtron leaf1 vrf add-neighbor Vrf_CUST1 Loopback0 64512 --neighbor 10.0.0.1 -x

# Remove a neighbor
newtron leaf1 vrf remove-neighbor default Ethernet0 -x
```

See [Section 9: VRF Management](#9-vrf-management) for the full set of neighbor operations.

### 12.3 Security Constraints

All direct BGP neighbors have these non-negotiable security constraints:

| Constraint | Value | Rationale |
|------------|-------|-----------|
| TTL | 1 | Prevents BGP hijacking via multi-hop attacks (GTSM) |
| Subnet validation | Required | Neighbor IP must be on interface subnet |
| Update source | Interface IP | Uses directly connected IP, not loopback |

---

## 13. QoS Management

QoS policies define declarative queue configurations. The QoS noun has two scopes: spec authoring (no device needed) and device application (requires device).

### 13.1 List QoS Policies

No device needed:

```bash
newtron qos list

# Output:
# NAME             QUEUES  DESCRIPTION
# ----             ------  -----------
# customer-4q      4       4-queue customer-edge policy
# 8q-datacenter    8       8-queue datacenter policy
```

### 13.2 Show QoS Policy

```bash
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
```

### 13.3 Create a QoS Policy

```bash
newtron qos create my-policy --description "Custom QoS" -x
```

### 13.4 Add Queues to a Policy

```bash
newtron qos add-queue my-policy 0 --type dwrr --weight 40 --dscp 0 --name best-effort -x
newtron qos add-queue my-policy 1 --type dwrr --weight 30 --dscp 10,18,20 --name business -x
newtron qos add-queue my-policy 2 --type strict --dscp 46 --name voice -x
newtron qos add-queue my-policy 3 --type strict --dscp 48,56 --name network-ctrl --ecn -x
```

**Queue parameters:**

| Parameter | Description |
|-----------|-------------|
| `--type` | `dwrr` (weighted) or `strict` (priority) |
| `--weight` | DWRR weight percentage (required for `dwrr`, forbidden for `strict`) |
| `--dscp` | Comma-separated DSCP values (0-63) mapped to this queue |
| `--name` | Human-readable queue name |
| `--ecn` | Enable ECN marking (creates WRED profile) |

**Constraints:** 1-8 queues per policy. DSCP values 0-63, no duplicates across queues. DWRR weights sum to 100%.

### 13.5 Remove a Queue

```bash
newtron qos remove-queue my-policy 3 -x
```

### 13.6 Delete a QoS Policy

```bash
newtron qos delete my-policy -x
```

### 13.7 Apply QoS to Interface

Apply a QoS policy to a device interface (requires device):

```bash
newtron leaf1 qos apply Ethernet0 8q-datacenter -x
```

This creates DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, and PORT_QOS_MAP entries in CONFIG_DB.

### 13.8 Remove QoS from Interface

```bash
newtron leaf1 qos remove Ethernet0 -x
```

### 13.9 QoS Override Workflow

Override service-managed QoS on a specific interface:

```bash
# Apply custom QoS (overrides service-managed policy)
newtron leaf1 qos apply Ethernet0 8q-datacenter -x

# Revert to service-managed QoS (refresh re-applies the service definition)
newtron leaf1 qos remove Ethernet0 -x
newtron leaf1 service refresh Ethernet0 -x
```

---

## 14. Filter Management

Filters are reusable ACL rule templates in network.json (spec authoring). When a service is applied, its `ingress_filter` and `egress_filter` references are instantiated as device-level ACLs.

**Key distinction:** A `filter` is a template definition in the spec; an `acl` is a device-level instance. Services bridge the two -- applying a service with a filter creates the corresponding ACL.

### 14.1 List Filters

No device needed:

```bash
newtron filter list

# Output:
# NAME               TYPE  RULES  DESCRIPTION
# ----               ----  -----  -----------
# customer-ingress   L3    2      Ingress filter for customer interfaces
# transit-protect    L3    5      Transit peering protection
```

### 14.2 Show Filter Details

```bash
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
```

### 14.3 Create a Filter

```bash
newtron filter create my-filter --type ipv4 --description "Custom filter" -x
```

### 14.4 Add Rules to a Filter

```bash
# Deny a source prefix
newtron filter add-rule my-filter --priority 100 --action deny --src-ip 10.0.0.0/8 -x

# Deny specific protocol
newtron filter add-rule my-filter --priority 200 --action deny --protocol icmp -x

# Permit all else (default deny-all if omitted)
newtron filter add-rule my-filter --priority 9999 --action permit -x
```

### 14.5 Remove a Rule

```bash
newtron filter remove-rule my-filter 200 -x
```

### 14.6 Delete a Filter

```bash
newtron filter delete my-filter -x
```

### 14.7 Filter Lifecycle

Filters live in network.json and are instantiated on devices when services reference them:

```bash
# 1. Create filter template
newtron filter create my-filter --type ipv4 -x
newtron filter add-rule my-filter --priority 100 --action deny --src-ip 10.0.0.0/8 -x
newtron filter add-rule my-filter --priority 9999 --action permit -x

# 2. Create service referencing the filter
newtron service create my-svc --type routed --ingress-filter my-filter -x

# 3. Apply service to device interface (filter becomes ACL on device)
newtron leaf1 service apply Ethernet0 my-svc --ip 10.1.1.1/30 -x

# 4. Update filter rules
newtron filter add-rule my-filter --priority 150 --action deny --src-ip 172.16.0.0/12 -x

# 5. Refresh service to pick up filter changes
newtron leaf1 service refresh Ethernet0 -x
```

---

## 15. Health Checks

### 15.1 Run All Health Checks

```bash
newtron leaf1 health check

# Output:
# Health Check Results for leaf1-dc1
# ==================================
#
# [PASS] BGP IPv4 Unicast: 2 peers established
# [PASS] BGP EVPN: 2 peers established
# [PASS] Interfaces: 28/32 up
# [WARN] Interface Ethernet8: admin down
# [PASS] PortChannels: 2/2 up, all members active
# [PASS] VXLAN Tunnel: vtep1 operational
# [PASS] VRFs: 3 configured, all operational
#
# Summary: 6 passed, 1 warning, 0 failed
```

### 15.2 Check Specific Component

```bash
# BGP health
newtron leaf1 health check --check bgp

# Interface health
newtron leaf1 health check --check interfaces

# EVPN health
newtron leaf1 health check --check evpn

# LAG health
newtron leaf1 health check --check lag
```

**Available check types:**

| Check | What it verifies |
|-------|-----------------|
| `bgp` | BGP neighbor count (warns if zero) |
| `interfaces` | Counts admin-down interfaces |
| `evpn` | VTEP existence and VNI mapping count |
| `lag` | LAG count and total member count |

### 15.3 JSON Output

```bash
newtron leaf1 health check --json

# Useful for monitoring integration
```

The JSON output uses `HealthCheckResult` objects with `check`, `status`, and `message` fields.

---

## 16. Baseline Configuration

The loopback configuration functionality in newtron is implemented through the `ConfigureLoopback()` method on `node.Node`. This method applies loopback interface configuration directly from the device profile.

### 16.1 How Baseline Works

The baseline operation:
- Sets the device hostname from the device profile
- Configures the loopback interface with the IP from the device profile
- Uses inline logic in `pkg/newtron/network/node/baseline_ops.go`

This is an internal operation typically called during topology provisioning, not directly exposed as a standalone CLI command.

### 16.2 Integration with Provisioning

Baseline configuration is applied automatically during:
- Topology provisioning via `newtron provision topology`
- Individual device provisioning flows

The baseline logic is embedded in the provisioner and uses the device's resolved profile to derive all configuration values.

---

## 17. Cleanup Operations

Newtron's cleanup operation identifies and removes orphaned configurations -- resources that are no longer in use.

### 17.1 Preview All Cleanup

```bash
newtron leaf1 device cleanup

# Output:
# Orphaned configurations to remove:
#   [DELETE] ACL_RULE|old-acl-Ethernet0-in|RULE_100
#   [DELETE] ACL_RULE|old-acl-Ethernet0-in|RULE_9999
#   [DELETE] ACL_TABLE|old-acl-Ethernet0-in
#   [DELETE] VRF|Vrf_UNUSED
#   [DELETE] VXLAN_TUNNEL_MAP|vtep1|map_10099_Vrf_UNUSED
#
# DRY-RUN: No changes applied. Use -x to execute.
```

### 17.2 Execute Cleanup

```bash
newtron leaf1 device cleanup -x
```

### 17.3 Cleanup Specific Types

```bash
# Only orphaned ACLs (ACL tables with no interfaces bound)
newtron leaf1 device cleanup --type acls -x

# Only orphaned VRFs (VRFs with no interface bindings, excluding "default")
newtron leaf1 device cleanup --type vrfs -x

# Only orphaned VNI mappings (VNI maps pointing to deleted VLANs/VRFs)
newtron leaf1 device cleanup --type vnis -x
```

### 17.4 What Gets Cleaned Up

| Type | Detection Criteria | What Gets Deleted |
|------|--------------------|-------------------|
| `acl` | ACL table with empty `ports` field | All ACL_RULE entries, then ACL_TABLE |
| `vrf` | VRF with no interface `vrf_name` references (skip "default") | VRF entry |
| `vni` | VXLAN_TUNNEL_MAP pointing to nonexistent VRF or VLAN | VXLAN_TUNNEL_MAP entry |

### 17.5 Orphaned Resource Detection Details

**ACLs:** The cleanup scans all `ACL_TABLE` entries. If `acl.Ports` is empty, the ACL has no interfaces bound and is considered orphaned. All corresponding `ACL_RULE` entries (matching the `aclName|` prefix) are deleted first, then the table itself.

**VRFs:** The cleanup scans all `VRF` entries. For each VRF (excluding "default"), it checks if any `INTERFACE` entry has `vrf_name` matching the VRF. If no interfaces reference the VRF, it is orphaned.

**VNIs:** The cleanup scans all `VXLAN_TUNNEL_MAP` entries. For each mapping, it checks whether the referenced VRF or VLAN still exists. If the referenced resource is gone, the mapping is orphaned.

### 17.6 When to Run Cleanup

- After removing services from multiple interfaces
- After deleting VLANs or VRFs
- Periodically as maintenance
- When investigating unexpected config state
- After test runs leave stale configuration

**Philosophy:** Only active configurations should exist on the device. Cleanup prevents unbounded growth of orphaned config settings over time.

---

## 18. Config Persistence

### 18.1 Runtime vs Persistent Configuration

Newtron writes configuration changes to **Redis CONFIG_DB** (database 4). These changes take effect immediately at runtime but are **ephemeral** -- they exist only in Redis memory and are lost on device reboot.

```
newtron service apply --> Redis CONFIG_DB (runtime) --> SONiC daemons process changes
                          NOT saved to disk yet
```

### 18.2 Persisting Changes

To persist changes across reboots, you must save the running configuration to disk on the SONiC device:

```bash
# SSH to the device
ssh admin@192.168.1.10

# Save running config to /etc/sonic/config_db.json
sudo config save -y
```

Newtron saves automatically after executing (`-x`). To skip the save step:

```bash
newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x --no-save
```

Or as a one-liner via SSH:

```bash
ssh admin@192.168.1.10 'sudo config save -y'
```

### 18.3 What Happens on Reboot Without Save

If the device reboots before `config save -y`:

1. SONiC loads `/etc/sonic/config_db.json` from disk into Redis
2. All changes made since the last `config save` are gone
3. The device returns to its last persisted state

### 18.4 When to Save

Save after completing a set of related changes:

```bash
# Apply services
newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x
newtron leaf1 service apply Ethernet4 customer-l3 --ip 10.1.2.1/30 -x

# Verify everything looks correct
newtron leaf1 health check

# Then persist
ssh admin@192.168.1.10 'sudo config save -y'
```

### 18.5 Config Reload

To revert to the persisted configuration (discard runtime changes):

```bash
ssh admin@192.168.1.10 'sudo config reload -y'
```

This reloads `/etc/sonic/config_db.json` into Redis, effectively undoing any unsaved changes.

---

## 19. Lab Environment

Lab environments for newtron use **newtlab** (see `docs/newtlab/`). newtlab orchestrates
SONiC-VS QEMU VMs without requiring root or Docker. E2E testing uses the **newtest**
framework (see `docs/newtest/`).

### 19.1 Running Tests

```bash
# Run unit tests (no lab required)
make test
```

### 19.2 Redis Access

Redis inside SONiC listens on `127.0.0.1:6379` and is not directly accessible.
newtron uses SSH tunnels when `ssh_user`/`ssh_pass` are set in the device profile:

```
newtron --> SSH to device:22 --> forward to 127.0.0.1:6379 (inside device)
```

---

## 20. Troubleshooting

### 20.1 Connection Issues

**Problem:** Cannot connect to device -- Redis connection refused

```bash
newtron leaf1 show
# Error: redis connection failed: dial tcp 192.168.1.10:6379: connect: connection refused
```

**Diagnosis:** Port 6379 is not accessible. In QEMU environments, Redis port is NOT forwarded by SLiRP networking. Only SSH (port 22) is forwarded.

**Solutions:**

1. Ensure `ssh_user` and `ssh_pass` are set in the device profile -- this enables the SSH tunnel
2. Verify management IP in profile is correct
3. Check network connectivity: `ping 192.168.1.10`
4. For direct Redis (non-QEMU), verify Redis is running: `ssh admin@192.168.1.10 'redis-cli ping'`
5. For production, check firewall rules allow port 6379

### 20.2 SSH Tunnel Failed

**Problem:** SSH tunnel cannot be established

```bash
newtron leaf1 show
# Error: SSH tunnel to 172.20.20.3: SSH dial 172.20.20.3: dial tcp 172.20.20.3:22: connect: connection refused
```

**Solutions:**

1. Verify SSH service is running on the device: `ssh cisco@<mgmt_ip>` manually
2. Check SSH credentials in the profile (`ssh_user` and `ssh_pass`)
3. Verify port 22 is accessible from the newtron host
4. In lab: ensure `newtlab deploy` completed successfully (profile patching sets SSH creds)
5. In lab: check that the VM is healthy: `newtlab status`

### 20.3 Changes Lost After Reboot

**Problem:** Configuration changes disappear after device reboot

**Cause:** Newtron writes to Redis CONFIG_DB at runtime. Changes are ephemeral until explicitly saved.

**Solution:** After applying changes, save the config on the device:

```bash
ssh admin@192.168.1.10 'sudo config save -y'
```

Note: `-x` saves by default. Use `--no-save` to skip persistence.

See [Section 18: Config Persistence](#18-config-persistence) for details.

### 20.4 Stale State After Test

**Problem:** Tests fail because leftover configuration from a previous test run causes crashes or unexpected behavior in SONiC daemons (vxlanmgrd, orchagent).

**Solutions:**

1. Use the `ResetLabBaseline()` function in test `TestMain` to clean stale entries before tests
2. Use `device cleanup` to remove orphaned resources
3. As a last resort, reload config on the device: `ssh admin@<ip> 'sudo config reload -y'`

In E2E tests, the `ResetLabBaseline()` helper connects to each SONiC node via SSH and deletes known stale CONFIG_DB keys in parallel.

### 20.5 Precondition Failures

**Problem:** Operation fails precondition check

```bash
newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30
# Error: service apply on Ethernet0: VTEP configured required - VTEP not configured - create VTEP first
```

**Solution:** Follow the dependency chain:

```bash
# 1. Set up EVPN overlay first (creates VTEP + NVO + BGP EVPN)
newtron leaf1 evpn setup -x

# 2. Then apply service
newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x
```

**VRF precondition failures:**

```bash
newtron leaf1 vrf delete Vrf_CUST1
# Error: vrf delete: VRF has interfaces bound - remove interfaces first
```

```bash
# Remove interfaces before deleting VRF
newtron leaf1 vrf remove-interface Vrf_CUST1 Ethernet8 -x
newtron leaf1 vrf remove-interface Vrf_CUST1 Ethernet12 -x
newtron leaf1 vrf delete Vrf_CUST1 -x
```

### 20.6 Interface in Use

**Problem:** Cannot modify interface that has configuration

```bash
newtron leaf1 lag add-interface PortChannel100 Ethernet0
# Error: add-interface on Ethernet0: interface must not have service - interface has service 'customer-l3' bound
```

**Solution:** Remove the service first:

```bash
newtron leaf1 service remove Ethernet0 -x
newtron leaf1 lag add-interface PortChannel100 Ethernet0 -x
```

### 20.7 Verbose Mode

Enable verbose output for debugging:

```bash
newtron -v leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30

# Shows detailed logs including:
# - Configuration loading
# - SSH tunnel establishment
# - Device state queries
# - Precondition checks
# - Change generation
```

### 20.8 View Audit Logs

Check what changes were made:

```bash
newtron audit list --last 24h

# Output:
# Timestamp            User    Operation          Device      Status
# ---------            ----    ---------          ------      ------
# 2024-01-15 10:30:00  alice   service apply      leaf1-dc1   success
# 2024-01-15 10:25:00  alice   lag create         leaf1-dc1   success
# 2024-01-15 10:20:00  bob     service apply      leaf1-dc1   failed
```

### 20.9 Common Error Messages

| Error | Cause | Solution |
|-------|-------|----------|
| "device required" | Missing device name | Add `-d <device>` or use implicit device as first arg |
| "interface does not exist" | Typo in interface name | Check `newtron <device> interface list` |
| "VRF does not exist" | Service uses shared VRF not created | Create VRF first or use `vrf_type: interface` |
| "VRF has interfaces bound" | Trying to delete VRF with interfaces | Remove interfaces first with `vrf remove-interface` |
| "VTEP not configured" | EVPN service without VTEP | Set up EVPN with `newtron <device> evpn setup -x` |
| "BGP not configured" | EVPN service without BGP | Configure BGP (manual or baseline) |
| "interface is a member of LAG" | Trying to configure LAG member directly | Configure the LAG instead |
| "L2 and L3 are mutually exclusive" | Adding VLAN member to routed port | Remove IP/VRF first |
| "redis connection failed" | Port 6379 not forwarded | Add ssh_user/ssh_pass to profile for SSH tunnel |
| "SSH dial ... connection refused" | Port 22 not reachable | Check SSH service, network, VM health |
| "device is locked by another process" | Another newtron instance holds the device lock | Wait for other operation to finish, or check `LockHolder()` for stale locks (TTL auto-expires) |
| "interface already has service" | Apply service to an interface that already has one | Remove existing service first |

### 20.10 Reset to Clean State

If configuration is in a bad state:

```bash
# Option 1: Remove services and start fresh
newtron leaf1 service remove Ethernet0 -x

# Option 2: Run cleanup to remove all orphaned resources
newtron leaf1 device cleanup -x

# Option 3: Use SONiC's config reload (on switch, reverts to saved config)
ssh admin@192.168.1.10 'sudo config reload -y'
```

---

## 21. Go API Usage

The newtron package provides an object-oriented API for programmatic access. The design uses parent references so child objects can access their parent's configuration.

### 21.1 Object Hierarchy

```
Network (top-level: loads specs, owns device profiles)
    |
    +-- Node (has SpecProvider from Network)
            |
            +-- Interface (has parent reference to Node)
```

Each object level can reach its parent:

- `Interface.Node()` returns the owning Node
- Node embeds `SpecProvider` (implemented by Network) — `node.GetService()` just works
- Node exposes accessors: `Tunnel()`, `StateDBClient()`, `ConfigDBClient()`, `StateDB()`, `ConfigDB()`

This design means operations on an Interface can access the full specification (services, filter-specs, prefix-lists) through the Node's embedded SpecProvider without needing specs passed as parameters.

### 21.2 Creating a Network and Connecting to a Device

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/newtron-network/newtron/pkg/newtron/network"
)

func main() {
    ctx := context.Background()

    // Create Network -- loads all specifications (declarative intent)
    net, err := network.NewNetwork("/etc/newtron")
    if err != nil {
        log.Fatal(err)
    }

    // Connect to a node (Node is created in Network's context)
    // If the profile has ssh_user/ssh_pass, an SSH tunnel is established
    // automatically for Redis access
    dev, err := net.ConnectNode(ctx, "leaf1-dc1")
    if err != nil {
        log.Fatal(err)
    }
    defer dev.Disconnect()

    // Node has access to Network configuration through parent reference
    fmt.Printf("Device: %s\n", dev.Name())
    fmt.Printf("AS Number: %d\n", dev.ASNumber())
    fmt.Printf("BGP Neighbors: %v\n", dev.BGPNeighbors())
}
```

### 21.3 Connecting with SSH Tunnel (Explicit)

The SSH tunnel is established automatically by `Node.Connect()` when the device profile contains `ssh_user` and `ssh_pass`. You do not need to create the tunnel manually. However, understanding the flow is useful for debugging:

```go
// The profile determines the connection method:
//
// Profile with SSH credentials:
//   {
//     "mgmt_ip": "172.20.20.3",
//     "ssh_user": "cisco",
//     "ssh_pass": "cisco123",
//     ...
//   }
//
// Connect() will:
//   1. SSH to 172.20.20.3:22 with cisco/cisco123
//   2. Open local listener on random port (e.g., 127.0.0.1:54321)
//   3. Forward connections to 127.0.0.1:6379 inside the device
//   4. Connect Redis client to 127.0.0.1:54321
//
// Profile without SSH credentials:
//   {
//     "mgmt_ip": "172.20.20.3",
//     ...
//   }
//
// Connect() will:
//   1. Connect Redis client directly to 172.20.20.3:6379
```

### 21.4 Accessing Specifications Through Parent References

```go
// Get an interface (Interface is created in Device's context)
intf, err := dev.GetInterface("Ethernet0")
if err != nil {
    log.Fatal(err)
}

// Interface can access Node properties
fmt.Printf("Device AS: %d\n", intf.Node().ASNumber())

// Interface can access Network configuration (via Node's embedded SpecProvider)
svc, err := intf.Node().GetService("customer-l3")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Service type: %s\n", svc.ServiceType)

// Get filter spec from Network (via Node)
filter, err := intf.Node().GetFilter("customer-edge-in")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Filter rules: %d\n", len(filter.Rules))
```

### 21.5 Checking Device State

```go
// List all interfaces
for _, name := range dev.ListInterfaces() {
    intf, _ := dev.GetInterface(name)
    fmt.Printf("%s: %s\n", name, intf)
}

// Check existence
if dev.VLANExists(100) {
    fmt.Println("VLAN 100 exists")
}

if dev.VTEPExists() {
    fmt.Println("VTEP is configured")
}

// Check interface state
intf, _ := dev.GetInterface("Ethernet0")
if intf.HasService() {
    fmt.Printf("Interface has service: %s\n", intf.ServiceName())
}
if intf.IsPortChannelMember() {
    fmt.Printf("Interface is member of: %s\n", intf.PortChannelParent())
}
```

### 21.6 Listing Network-Level Specifications

```go
// List all services
for _, name := range net.ListServices() {
    svc, _ := net.GetService(name)
    fmt.Printf("Service %s: %s (%s)\n", name, svc.Description, svc.ServiceType)
}

// List IP-VPN definitions
for _, name := range net.ListIPVPNs() {
    ipvpn, _ := net.GetIPVPN(name)
    fmt.Printf("IP-VPN %s: L3VNI %d\n", name, ipvpn.L3VNI)
}

// Get prefix list
prefixes, _ := net.GetPrefixList("rfc1918")
fmt.Printf("RFC1918 prefixes: %v\n", prefixes)

// List zones (from network spec)
for name := range net.Spec().Zones {
    fmt.Printf("Zone: %s\n", name)
}
```

### 21.7 Performing Operations

Operations are methods on the objects they operate on:

```go
// Interface operations
intf, _ := dev.GetInterface("Ethernet0")

changeSet, err := intf.ApplyService(ctx, "customer-l3", node.ApplyServiceOpts{IPAddress: "10.1.1.1/30"})
if err != nil {
    log.Fatal(err) // includes ErrDeviceLocked if another process has the lock
}

// Preview changes (dry-run)
fmt.Println("Changes to be applied:")
fmt.Print(changeSet.String())

// Apply changes
if err := changeSet.Apply(dev); err != nil {
    log.Fatal(err)
}
```

**Device operations:**

```go
// Create a VLAN
changeSet, err := dev.CreateVLAN(ctx, 100, network.VLANConfig{
    Description: "Server VLAN",
})
if err != nil {
    log.Fatal(err)
}
changeSet.Apply(dev)

// Create a PortChannel (LAG)
changeSet, err := dev.CreatePortChannel(ctx, "PortChannel100", network.PortChannelConfig{
    Members:  []string{"Ethernet0", "Ethernet4"},
    MinLinks: 1,
    FastRate: true,
})
if err != nil {
    log.Fatal(err)
}
changeSet.Apply(dev)

// Create VRF
changeSet, err := dev.CreateVRF(ctx, "Vrf_CUST1")
if err != nil {
    log.Fatal(err)
}
changeSet.Apply(dev)

// Add interface to VRF
changeSet, err := dev.AddVRFInterface(ctx, "Vrf_CUST1", "Ethernet8")
if err != nil {
    log.Fatal(err)
}
changeSet.Apply(dev)

// Bind IP-VPN to VRF
changeSet, err := dev.BindIPVPN(ctx, "Vrf_CUST1", "customer-vpn")
if err != nil {
    log.Fatal(err)
}
changeSet.Apply(dev)

// Add static route to VRF
changeSet, err := dev.AddStaticRoute(ctx, "Vrf_CUST1", "10.99.0.0/16", "10.1.1.2")
if err != nil {
    log.Fatal(err)
}
changeSet.Apply(dev)

// Set up EVPN overlay (idempotent composite)
changeSet, err := dev.SetupEVPN(ctx, "") // empty = use loopback IP
if err != nil {
    log.Fatal(err)
}
changeSet.Apply(dev)

// Run health checks
report, err := provisioner.VerifyDeviceHealth(ctx, deviceName)
if err != nil {
    log.Fatal(err)
}
for _, r := range report.Results {
    fmt.Printf("[%s] %s: %s\n", r.Status, r.Check, r.Message)
}

// Cleanup orphaned resources
changeSet, summary, err := dev.Cleanup(ctx, "") // "" = all types
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Orphaned ACLs: %v\n", summary.OrphanedACLs)
fmt.Printf("Orphaned VRFs: %v\n", summary.OrphanedVRFs)
if !changeSet.IsEmpty() {
    changeSet.Apply(dev)
}
```

**Spec authoring (network-level, no device):**

```go
// Save a new IP-VPN definition
err := net.SaveIPVPN("my-vpn", spec.IPVPNSpec{
    Description:  "Customer VPN",
    L3VNI:        10001,
    RouteTargets: []string{"65001:1"},
})

// Delete an IP-VPN definition
err := net.DeleteIPVPN("my-vpn")

// Save a new MAC-VPN definition
err := net.SaveMACVPN("my-l2vpn", spec.MACVPNSpec{
    Description:    "Server L2 extension",
    VNI:            10100,
    VlanID:         100,
    ARPSuppression: true,
})
```

### 21.8 Disconnect and Tunnel Cleanup

```go
// Disconnect closes Redis clients, releases lock, and closes SSH tunnel
dev.Disconnect()
```

E2E testing uses the newtest framework. See `docs/newtest/e2e-learnings.md` for
SONiC-specific patterns (convergence timing, cleanup ordering, vxlanmgrd pitfalls).

### 21.9 Design Benefits

The parent reference design provides:

1. **Natural Access**: Interface can access `node.GetService()` naturally via embedded SpecProvider
2. **No Spec Passing**: Operations do not need specs passed separately — specs are reached via the Node's SpecProvider
3. **Encapsulation**: Specs are owned by the right object level (Network owns services, Node owns state and connection)
4. **Single Source of Truth**: Specs loaded once at Network level, translated to config at runtime
5. **True OO**: Operations are methods on objects (e.g., `intf.ApplyService()`, `dev.CreateVLAN()`), not standalone functions

---

## 22. Quick Reference

### 22.1 CLI Pattern

```
newtron <device> <noun> <action> [args] [-x]
```

The first argument is the device name unless it matches a known command. Write commands preview changes by default; use `-x` to execute.

### 22.2 Command Tree

```
Resource Commands
├── interface
│   ├── list
│   ├── show <interface>
│   ├── get <interface> <property>
│   ├── set <interface> <property> <value>
│   ├── list-acls <interface>
│   └── list-members <interface>
├── vlan
│   ├── list
│   ├── show <vlan-id>
│   ├── status
│   ├── create <vlan-id> [--name] [--description]
│   ├── delete <vlan-id>
│   ├── add-interface <vlan-id> <interface> [--tagged]
│   ├── remove-interface <vlan-id> <interface>
│   ├── configure-svi <vlan-id> [--vrf] [--ip] [--anycast-gw]
│   ├── bind-macvpn <vlan-id> <macvpn-name>
│   └── unbind-macvpn <vlan-id>
├── vrf
│   ├── list
│   ├── show <vrf-name>
│   ├── status
│   ├── create <vrf-name>
│   ├── delete <vrf-name>
│   ├── add-interface <vrf-name> <interface>
│   ├── remove-interface <vrf-name> <interface>
│   ├── bind-ipvpn <vrf-name> <ipvpn-name>
│   ├── unbind-ipvpn <vrf-name>
│   ├── add-neighbor <vrf-name> <interface> <remote-asn> [--neighbor] [--description]
│   ├── remove-neighbor <vrf-name> <interface|ip>
│   ├── add-route <vrf-name> <prefix> <next-hop> [--metric]
│   └── remove-route <vrf-name> <prefix>
├── lag
│   ├── list
│   ├── show <lag-name>
│   ├── status
│   ├── create <lag-name> --members <...> [--min-links] [--mode] [--fast-rate] [--mtu]
│   ├── delete <lag-name>
│   ├── add-interface <lag-name> <interface>
│   └── remove-interface <lag-name> <interface>
├── bgp
│   └── status
├── evpn
│   ├── setup [--source-ip]
│   ├── status
│   ├── ipvpn
│   │   ├── list
│   │   ├── show <name>
│   │   ├── create <name> --l3vni <vni> --vrf <vrf-name> [--route-targets] [--description]
│   │   └── delete <name>
│   └── macvpn
│       ├── list
│       ├── show <name>
│       ├── create <name> --vni <vni> --vlan-id <id> [--anycast-ip] [--anycast-mac] [--route-targets] [--arp-suppress] [--description]
│       └── delete <name>
├── qos
│   ├── list
│   ├── show <policy-name>
│   ├── create <policy-name> [--description]
│   ├── delete <policy-name>
│   ├── add-queue <policy-name> <queue-id> --type <dwrr|strict> [--weight] [--dscp] [--name] [--ecn]
│   ├── remove-queue <policy-name> <queue-id>
│   ├── apply <interface> <policy-name>
│   └── remove <interface>
├── filter
│   ├── list
│   ├── show <filter-name>
│   ├── create <filter-name> --type <ipv4|ipv6> [--description]
│   ├── delete <filter-name>
│   ├── add-rule <filter-name> --priority <N> --action <permit|deny> [match flags...]
│   └── remove-rule <filter-name> <priority>
├── acl
│   ├── list
│   ├── show <acl-name>
│   ├── create <acl-name> --type <ipv4|ipv6> --stage <ingress|egress> [--interfaces] [--description]
│   ├── delete <acl-name>
│   ├── add-rule <acl-name> <rule-name> --priority <N> [match flags...] --action <permit|deny>
│   ├── delete-rule <acl-name> <rule-name>
│   ├── bind <acl-name> <interface> --direction <ingress|egress>
│   └── unbind <acl-name> <interface>
├── service
│   ├── list
│   ├── show <service-name>
│   ├── create <service-name> --type <routed|bridged|irb|evpn-routed|evpn-bridged|evpn-irb> [--ipvpn] [--macvpn] [--vrf-type]
│   │   [--qos-policy] [--ingress-filter] [--egress-filter] [--description]
│   ├── delete <service-name>
│   ├── apply <interface> <service> [--ip] [--peer-as]
│   ├── remove <interface>
│   ├── get <interface>
│   └── refresh <interface>

Device Operations
├── show
├── provision [-d <device>] [-x] [--no-save]
├── health check [--check <name>]
├── shell (alias: sh)
└── device
    └── cleanup [--type]

Configuration & Meta
├── settings
│   ├── show
│   ├── set <key> <value>
│   └── clear
├── audit
│   └── list [--last <duration>] [--device] [--user] [--failures]
└── version
```

### 22.3 Common Flag Reference

| Flag | Short | Description |
|------|-------|-------------|
| `--device` | `-d` | Target device name |
| `--network` | `-n` | Network configuration name |
| `--specs` | `-S` | Specification directory |
| `--execute` | `-x` | Execute changes (default: dry-run) |
| `--no-save` | | Skip config save after execute (save is default with `-x`) |
| `--verbose` | `-v` | Verbose output |
| `--json` | | JSON output format |

### 22.4 Dependency Order for New Device

1. Set up overlay: `newtron leaf1 evpn setup -x`
2. Provision from topology: `newtron provision -d leaf1 -x`
3. Or apply services individually: `newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x`
4. Verify: `newtron leaf1 health check`
5. Persist: automatic with `-x` (or `ssh admin@<ip> 'sudo config save -y'` if `--no-save` was used)

### 22.5 Lab Quick Start

```bash
# Start lab
make lab-start

# Run E2E tests
make test-e2e

# Check status
make lab-status

# Stop lab
make lab-stop
```

---

## 23. Topology Provisioning

Topology provisioning automates device configuration from a `topology.json` spec file. Instead of manually running service apply commands for each interface, the topology declares the complete desired state.

### 23.1 Creating a Topology Spec

Create `specs/topology.json`:

```json
{
  "version": "1.0",
  "description": "DC1 lab topology",
  "devices": {
    "spine1-dc1": {
      "device_config": { "route_reflector": true },
      "interfaces": {
        "Ethernet0": {
          "link": "leaf1-dc1:Ethernet48",
          "service": "fabric-underlay",
          "ip": "10.10.1.0/31"
        }
      }
    },
    "leaf1-dc1": {
      "interfaces": {
        "Ethernet0": {
          "service": "customer-l3",
          "ip": "10.1.1.1/30",
          "params": { "peer_as": "65100" }
        },
        "Ethernet48": {
          "link": "spine1-dc1:Ethernet0",
          "service": "fabric-underlay",
          "ip": "10.10.1.1/31"
        }
      }
    }
  },
  "links": [
    {"a": "spine1-dc1:Ethernet0", "z": "leaf1-dc1:Ethernet48"}
  ]
}
```

**Key points:**
- Device names must match profiles in `profiles/`
- Service names must exist in `network.json`
- `params` provides values for services that require them (e.g., `peer_as: "request"`)
- `links` section is optional, used for validation

### 23.2 Provisioning via CLI

```bash
# Dry-run all devices
newtron provision

# Dry-run specific device
newtron provision -d leaf1

# Execute all devices
newtron provision -x

# Execute + save for one device (save is default)
newtron provision -d leaf1 -x
```

### 23.3 Full Device Provisioning (Go API)

Full device provisioning generates the complete CONFIG_DB offline and delivers it atomically:

```go
// Load network with topology
net, err := network.NewNetwork("/etc/newtron/specs")
if err != nil {
    log.Fatal(err)
}

// Create provisioner
tp, err := network.NewTopologyProvisioner(net)
if err != nil {
    log.Fatal(err)
}

// Validate before provisioning
if err := tp.ValidateTopologyDevice("leaf1-dc1"); err != nil {
    log.Fatal(err)
}

// Provision (connects, delivers CompositeOverwrite, disconnects)
result, err := tp.ProvisionDevice(ctx, "leaf1-dc1")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Applied %d entries\n", result.Applied)
```

This mode:
- Does NOT read existing device config
- Generates all CONFIG_DB entries from specs + topology
- Performs best-effort `config reload -y` to restore CONFIG_DB baseline before delivery
- Connects and delivers via CompositeOverwrite (merges composite on top of existing CONFIG_DB, preserving factory defaults; only stale keys are removed)
- Runs `config save -y` after delivery to persist provisioned config

### 23.4 Per-Interface Provisioning (Go API)

Per-interface provisioning connects to the device and applies a single service:

```go
tp, _ := network.NewTopologyProvisioner(net)

// Provision one interface (connects, interrogates, applies service)
cs, err := tp.ProvisionInterface(ctx, "leaf1-dc1", "Ethernet0")
if err != nil {
    log.Fatal(err)
}
fmt.Println(cs.Preview())

// Apply the changeset
if err := cs.Apply(dev); err != nil {
    log.Fatal(err)
}
```

This mode uses the standard `ApplyService()` path with full precondition checking.

### 23.5 Inspecting Generated Config

You can generate the composite config without delivering it:

```go
composite, err := tp.GenerateDeviceComposite("leaf1-dc1")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Generated %d entries\n", composite.EntryCount())
```

### 23.6 When to Use Each Mode

| Mode | Use Case |
|------|----------|
| **ProvisionDevice** | Initial device setup, lab provisioning, disaster recovery |
| **ProvisionInterface** | Adding a service to one interface on a running device |

---

## 24. Composite Configuration

Composite mode generates a complete CONFIG_DB configuration offline (without a device connection), then delivers it atomically.

### 24.1 Building a Composite Config (Go API)

```go
cb := node.NewCompositeBuilder("leaf1-dc1", node.CompositeOverwrite).
    SetGeneratedBy("my-tool").
    SetDescription("Initial provisioning")

// Add device-level entries
cb.AddBGPGlobals("default", map[string]string{
    "local_asn": "64512",
    "router_id": "10.0.0.11",
})

// Add port configuration
cb.AddPortConfig("Ethernet0", map[string]string{
    "admin_status": "up",
    "mtu":          "9100",
})

// Add service binding
cb.AddService("Ethernet0", "customer-l3", map[string]string{
    "ip_address": "10.1.1.1/30",
})

// Build the composite
composite := cb.Build()
fmt.Printf("Composite has %d entries\n", composite.EntryCount())
```

### 24.2 Delivering a Composite Config

```go
// Connect to device
dev, err := net.ConnectNode(ctx, "leaf1-dc1")
if err != nil {
    log.Fatal(err)
}
defer dev.Disconnect()

// Lock is acquired/released automatically by DeliverComposite
// Deliver with overwrite (merges on top of CONFIG_DB, removes stale keys)
result, err := dev.DeliverComposite(composite, node.CompositeOverwrite)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Applied: %d, Skipped: %d\n", result.Applied, result.Skipped)
```

### 24.3 Delivery Modes

| Mode | Behavior | Use Case |
|------|----------|----------|
| **CompositeOverwrite** | Merge composite on top of CONFIG_DB (stale keys removed, factory defaults preserved) | Initial provisioning, lab setup |
| **CompositeMerge** | Add entries to existing config | Incremental service deployment |

**Merge restrictions**: Only supported for interface-level service configuration, and only if the target interface has no existing service binding.

---

## 25. End-to-End Workflows

These workflows show how newtron primitives compose for common operational tasks.

### 25.1 Day-1: New Device Provisioning

```bash
# 1. Set up overlay infrastructure
newtron leaf1 evpn setup --source-ip 10.0.0.2 -x

# 2. Provision from topology
newtron provision -d leaf1 -x

# 3. Verify BGP sessions
newtron leaf1 bgp status

# 4. Run health checks
newtron leaf1 health check

# 5. Persist
newtron leaf1 evpn setup -x     # execute + save (save is default)
```

### 25.2 Add L3 Customer Interface

```bash
# 1. Apply customer service
newtron leaf1 service apply Ethernet4 customer-l3 --ip 10.1.1.1/30 --peer-as 65100 -x

# 2. Verify service binding
newtron leaf1 service get Ethernet4

# 3. Verify BGP neighbor came up
newtron leaf1 bgp status
```

### 25.3 Add L2 VLAN Extension

```bash
# 1. Create VLAN
newtron leaf1 vlan create 100 --name Servers -x

# 2. Add interface to VLAN
newtron leaf1 vlan add-interface 100 Ethernet5 -x

# 3. Bind to MAC-VPN for VXLAN extension
newtron leaf1 vlan bind-macvpn 100 servers-vlan100 -x
```

### 25.4 Set Up EVPN Overlay from Scratch

```bash
# 1. Create VPN definitions (no device needed)
newtron evpn ipvpn create my-vpn --l3vni 10001 --route-targets 65100:1 -x
newtron evpn macvpn create my-l2vpn --vni 10100 --vlan-id 100 --arp-suppress -x

# 2. Set up overlay on device
newtron leaf1 evpn setup --source-ip 10.0.0.2 -x

# 3. Create VRF and bind to IP-VPN
newtron leaf1 vrf create Vrf_CUST1 -x
newtron leaf1 vrf bind-ipvpn Vrf_CUST1 my-vpn -x

# 4. Create VLAN and bind to MAC-VPN
newtron leaf1 vlan create 100 -x
newtron leaf1 vlan bind-macvpn 100 my-l2vpn -x
```

### 25.5 Manual Surgical QoS Override

```bash
# Override service-managed QoS on a specific interface
newtron leaf1 qos apply Ethernet0 8q-datacenter -x

# Revert to service-managed QoS
newtron leaf1 qos remove Ethernet0 -x
newtron leaf1 service refresh Ethernet0 -x
```

### 25.6 Add Customer VRF with Full Connectivity

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

### 25.7 Decommission a Customer VRF

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

# 6. Clean up any orphaned resources
newtron leaf1 device cleanup -x
```

---

## 26. CONFIG_DB Cache Behavior

newtron caches CONFIG_DB (Redis DB 4) in memory to avoid per-check Redis round-trips during precondition validation. This section explains how the cache is managed and when it refreshes.

### 26.1 Shared-Device Context

A SONiC device's CONFIG_DB can be modified by newtron, other automation tools (Ansible, Salt), admins running `redis-cli`, and SONiC daemons (frrcfgd, orchagent). newtron's distributed lock (STATE_DB) only coordinates newtron instances -- it does not prevent external writes. The cache is an optimization, not transactional isolation.

### 26.2 The Episode Model

Every self-contained unit of work that reads the cache must start with a fresh snapshot. These units are called **episodes**:

- **Write operations** (via `ExecuteOp`): `Lock()` automatically refreshes the cache after acquiring the distributed lock. Precondition checks within the operation read from this fresh snapshot. No action needed by the caller.

- **Read-only code** (health checks, CLI show commands): Call `Refresh()` before reading from the cache, or use `ConfigDBClient.Get()`/`Exists()` to read from Redis directly (as `verify-config-db` does in newtest).

- **Composite provisioning path**: Call `Refresh()` after `DeliverComposite()` to reload the cache with the newly written config.

### 26.3 Precondition Checks

Precondition checks (`VRFExists`, `VLANExists`, `VTEPExists`, etc.) read from the cached snapshot. They are **advisory safety nets** -- they catch common mistakes like creating a duplicate VRF or adding a member to a non-existent VLAN. They cannot prevent all race conditions with external actors modifying CONFIG_DB concurrently.

### 26.4 When to Call Refresh()

| Situation | Action |
|-----------|--------|
| Before a write operation | Not needed -- `ExecuteOp` calls `Lock()` which refreshes |
| After composite delivery | Call `Refresh()` |
| Before health checks | Not needed -- `CheckBGPSessions()` calls `Refresh()` internally |
| Before reading cache in custom code | Call `Refresh()` |
| For one-off Redis reads | Use `ConfigDBClient.Get()` directly (bypasses cache) |

---

## 27. Zone-Level and Node-Level Spec Overrides

### Zone-Level Specs

Add specs directly under a zone in `network.json` to make them available only to devices in that zone:

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

### Node-Level Specs

Add specs to a device profile (`profiles/<device>.json`) for device-specific overrides:

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

### Resolution Order

When a node looks up a spec by name, the merged result follows **lower-level-wins**:

1. **Node (profile)** — highest priority
2. **Zone** — middle priority
3. **Network** — lowest priority (fallback)

All specs from all levels are visible. If the same name exists at multiple levels, the most specific level wins.

---

## 28. Related Documentation

The following documents provide additional detail on specific topics:

| Document | Description |
|----------|-------------|
| [hld.md](hld.md) | High-Level Design -- architecture overview, design decisions, system context |
| [lld.md](lld.md) | Low-Level Design -- package structure, data flow, object model details |
| [device-lld.md](device-lld.md) | Device Layer LLD -- SSH tunnels, Redis clients, state access |
| [DESIGN_PRINCIPLES.md](../DESIGN_PRINCIPLES.md) | Core design principles and philosophy |
| [e2e-learnings.md](../newtest/e2e-learnings.md) | SONiC-VS reference and E2E testing patterns |
