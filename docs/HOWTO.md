# Newtron HOWTO Guide (v4)

### What Changed in v2

| Area | Change |
|------|--------|
| **Connection Architecture** | Added new Section 2 explaining SSH tunnels — why they are needed (port 6379 not forwarded), SSH credentials in profiles, Connect() flow |
| **StateDB Queries** | Added new Section 13 for operational state access via STATE_DB (DB 6) |
| **Config Persistence** | Added new Section 16 warning that Redis changes are runtime-only; `config save -y` required |
| **Lab Environment** | Added new Section 17 with labgen build, lab-start lifecycle, topology switching |
| **Device Profiles** | Added `ssh_user`/`ssh_pass` optional fields to profile documentation |
| **Service Management** | Added NEWTRON_SERVICE_BINDING tracking step to ApplyService flow |
| **Build Lab Tools** | Added Section 1.3 with `make labgen` and `make build` instructions |
| **Host Key Verification** | Added InsecureIgnoreHostKey note for lab environments |

**Lines:** 1699 (v1) → 2599 (v2) | All v1 sections preserved and renumbered.

### What Changed in v3

| Area | Change |
|------|--------|
| **BGP Management** | BGP now managed via frrcfgd (CONFIG_DB-managed) instead of direct FRR manipulation |
| **Route Reflector Setup** | Added SetupRouteReflector (replaces SetupBGPEVPN) for BGP route reflector configuration |
| **Composite Mode** | Added composite (formerly spool) mode for offline config generation without device connection |
| **Port Creation** | Port creation now validates against platform.json for hardware compatibility |
| **Extended Routing Spec** | Routing spec extended with community, prefix-list, and redistribute support |

### What Changed in v4

| Area | Change |
|------|--------|
| **Spool to Composite Rename** | Spool renamed to composite throughout the codebase and APIs |
| **Topology Provisioning** | Added topology provisioning from topology.json (new Section 21) |
| **Provisioning Modes** | Two provisioning modes: full device (CompositeOverwrite) and per-interface (ApplyService) |
| **Type Naming Cleanup** | Type naming cleanup with Spec suffixes for consistency |

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
9. [EVPN/VXLAN Configuration](#9-evpnvxlan-configuration)
10. [ACL Management](#10-acl-management)
11. [BGP Direct Neighbors](#11-bgp-direct-neighbors)
12. [Health Checks](#12-health-checks)
13. [State DB Queries](#13-state-db-queries)
14. [Baseline Configuration](#14-baseline-configuration)
15. [Cleanup Operations](#15-cleanup-operations)
16. [Config Persistence](#16-config-persistence)
17. [Lab Environment](#17-lab-environment)
18. [Troubleshooting](#18-troubleshooting)
19. [Go API Usage](#19-go-api-usage)
20. [Quick Reference](#20-quick-reference)
21. [Topology Provisioning](#21-topology-provisioning)
22. [Composite Configuration](#22-composite-configuration)
23. [Related Documentation](#23-related-documentation)

---

## 1. Installation

### 1.1 Build from Source

```bash
# Clone the repository
git clone https://github.com/newtron-network/newtron.git
cd newtron

# Build the binary
go build -o newtron ./cmd/newtron

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

If you plan to use the containerlab-based test environment:

```bash
# Build the labgen tool (generates containerlab topologies from YAML)
make labgen

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

Redis inside SONiC listens on `127.0.0.1:6379` and has no authentication. In containerlab environments using QEMU/SLiRP networking (SONiC-VS / vrnetlab), **port 6379 is not forwarded** by the QEMU user-mode networking stack. Only port 22 (SSH) is accessible from the management network.

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
  "site": "dc1",
  "platform": "accton-as7726-32x",
  "ssh_user": "cisco",
  "ssh_pass": "cisco123"
}
```

When both `ssh_user` and `ssh_pass` are set, `Device.Connect()` creates an SSH tunnel automatically. When either is empty, it connects directly to `<mgmt_ip>:6379`.

### 2.4 Connection Flow (Code Reference)

From `pkg/device/device.go`, the `Connect()` method implements this logic:

```go
func (d *Device) Connect(ctx context.Context) error {
    var addr string
    if d.Profile.SSHUser != "" && d.Profile.SSHPass != "" {
        // SSH tunnel path: connect SSH to device:22, forward to 127.0.0.1:6379
        tun, err := NewSSHTunnel(d.Profile.MgmtIP, d.Profile.SSHUser, d.Profile.SSHPass)
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

The SSH tunnel currently uses `InsecureIgnoreHostKey()` for the host key callback. This is appropriate for lab and test environments but **must be replaced with proper host key verification for production deployments**. The relevant code is in `pkg/device/tunnel.go`:

```go
config := &ssh.ClientConfig{
    User: user,
    Auth: []ssh.AuthMethod{ssh.Password(pass)},
    // Lab/test environment -- production would verify host keys.
    HostKeyCallback: ssh.InsecureIgnoreHostKey(),
}
```

### 2.6 Dual Database Access

On connection, newtron opens two Redis clients through the same tunnel (or direct address):

| Database | Redis DB Number | Purpose |
|----------|----------------|---------|
| CONFIG_DB | 4 | Configuration state (read/write) |
| STATE_DB | 6 | Operational state (read-only, non-fatal if unavailable) |

If STATE_DB connection fails, newtron logs a warning and continues. Configuration operations work without STATE_DB; only operational state queries require it.

---

## 3. Specification Setup

Newtron uses **specification files** (declarative intent) to define what you want. These specs are translated to device **configuration** (imperative state) at runtime.

### 3.1 Directory Structure

Create the specification directory:

```bash
sudo mkdir -p /etc/newtron/profiles
sudo mkdir -p /etc/newtron/configlets
```

```
/etc/newtron/
    ├── network.json        # Service definitions, VPNs, filters, policies
    ├── site.json           # Site definitions and route reflectors
    ├── platforms.json      # Hardware platform definitions
    ├── topology.json       # (optional) Topology for automated provisioning
    ├── profiles/
    │   ├── leaf1-dc1.json
    │   └── spine1-dc1.json
    └── configlets/
        └── sonic-baseline.json
```

For lab environments, specs are auto-generated at `testlab/.generated/specs/` (see [Section 17: Lab Environment](#17-lab-environment)).

### 3.2 Network Specification (`network.json`)

The main specification file defines VPN parameters, services, filters, and permissions.

```bash
sudo vim /etc/newtron/network.json
```

```json
{
  "version": "1.0",
  "lock_dir": "/var/run/newtron",
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
  "regions": {
    "datacenter-east": {
      "as_number": 65001,
      "as_name": "dc-east"
    }
  },
  "prefix_lists": {
    "rfc1918": ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"],
    "bogons": ["0.0.0.0/8", "127.0.0.0/8", "224.0.0.0/4"]
  },
  "filter_specs": {
    "customer-ingress": {
      "description": "Ingress filter for customer interfaces",
      "type": "L3",
      "rules": [
        {"seq": 100, "src_prefix_list": "bogons", "action": "deny", "log": true},
        {"seq": 9999, "action": "permit"}
      ]
    }
  },
  "policers": {
    "rate-10m": {
      "bandwidth": "10m",
      "burst": "1m",
      "action": "drop"
    }
  },
  "qos_profiles": {
    "customer-qos": {
      "dscp_to_tc_map": "DSCP_TO_TC_MAP",
      "tc_to_queue_map": "TC_TO_QUEUE_MAP"
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

  "ipvpn": {
    "customer-vpn": {
      "description": "Customer L3VPN",
      "l3_vni": 10001,
      "import_rt": ["65001:1"],
      "export_rt": ["65001:1"]
    },
    "server-vpn": {
      "description": "Server/datacenter shared VRF",
      "l3_vni": 20001,
      "import_rt": ["65001:100"],
      "export_rt": ["65001:100"]
    }
  },

  "macvpn": {
    "servers-vlan100": {
      "description": "Server VLAN 100",
      "vlan": 100,
      "l2_vni": 10100,
      "arp_suppression": true
    }
  },

  "services": {
    "customer-l3": {
      "description": "L3 routed customer interface",
      "service_type": "l3",
      "vrf_type": "interface",
      "ipvpn": "customer-vpn",
      "ingress_filter": "customer-ingress",
      "qos_profile": "customer-qos",
      "routing": {
        "protocol": "bgp",
        "peer_as": "request",
        "import_policy": "customer-import"
      }
    },
    "server-irb": {
      "description": "Server VLAN with shared VRF",
      "service_type": "irb",
      "vrf_type": "shared",
      "ipvpn": "server-vpn",
      "macvpn": "servers-vlan100",
      "anycast_gateway": "10.1.100.1/24",
      "anycast_mac": "00:00:00:01:02:03"
    },
    "transit": {
      "description": "Transit (global table, no VPN)",
      "service_type": "l3",
      "ingress_filter": "transit-protect"
    }
  }
}
```

**Key Sections:**

| Section | Purpose |
|---------|---------|
| `ipvpn` | IP-VPN definitions (L3VNI, route targets) for L3 routing |
| `macvpn` | MAC-VPN definitions (VLAN, L2VNI) for L2 bridging |
| `services` | Service templates referencing ipvpn/macvpn by name |
| `filter_specs` | Reusable ACL rule templates |
| `policers` | Rate limiter definitions referenced by filter rules |
| `qos_profiles` | QoS map references for DSCP/TC mapping |
| `route_policies` | BGP import/export policies |
| `prefix_lists` | Reusable IP prefix lists (expanded in filter rules and policies) |

**Service Types:**

| Type | Description | Requires |
|------|-------------|----------|
| `l3` | L3 routed interface | `ipvpn` (optional), IP address at apply time |
| `l2` | L2 bridged interface | `macvpn` |
| `irb` | Integrated routing and bridging | Both `ipvpn` and `macvpn` |

**VRF Instantiation (`vrf_type`):**

| Value | Behavior |
|-------|----------|
| `"interface"` | Per-interface VRF: `{service}-{interface}` |
| `"shared"` | Shared VRF: name = ipvpn definition name |
| (omitted) | Global routing table (no VPN) |

### 3.3 Site Specification (`site.json`)

Define sites and their route reflectors (by device name only -- IPs come from profiles):

```bash
sudo vim /etc/newtron/site.json
```

```json
{
  "version": "1.0",
  "sites": {
    "dc1": {
      "region": "datacenter-east",
      "route_reflectors": ["spine1-dc1", "spine2-dc1"]
    },
    "dc2": {
      "region": "datacenter-west",
      "route_reflectors": ["spine1-dc2", "spine2-dc2"]
    }
  }
}
```

**Note:** `route_reflectors` contains device names only. Their loopback IPs are looked up from each device's profile at runtime.

### 3.4 Platform Specification (`platforms.json`)

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

### 3.5 Device Profiles

Create a profile for each device. The profile is the **single source of truth** for device-specific data (IPs, platform, SSH credentials):

```bash
sudo vim /etc/newtron/profiles/leaf1-dc1.json
```

```json
{
  "mgmt_ip": "192.168.1.10",
  "loopback_ip": "10.0.0.10",
  "site": "dc1",
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
| `site` | Site name (region is derived from site.json) |

**Optional Fields:**

| Field | Description |
|-------|-------------|
| `platform` | Platform name (maps to platforms.json for HWSKU) |
| `ssh_user` | SSH username for Redis tunnel access |
| `ssh_pass` | SSH password for Redis tunnel access |
| `as_number` | Override region AS number for this device |
| `is_route_reflector` | Mark device as route reflector |
| `is_border_router` | Mark as border router |
| `vlan_port_mapping` | Device-specific VLAN-to-port mappings |
| `generic_alias` | Device-specific aliases (override region/global) |
| `prefix_lists` | Device-specific prefix lists (merged with region/global) |

**Note:** The `region` is derived from `site.json` (site "dc1" maps to region "datacenter-east"). No need to specify it in the profile.

Create profiles for route reflectors too:

```bash
sudo vim /etc/newtron/profiles/spine1-dc1.json
```

```json
{
  "mgmt_ip": "192.168.1.1",
  "loopback_ip": "10.0.0.1",
  "site": "dc1",
  "platform": "accton-as7726-32x",
  "is_route_reflector": true,
  "ssh_user": "admin",
  "ssh_pass": "YourSonicPassword"
}
```

### 3.6 Profile Resolution and Inheritance

When a device is loaded, newtron resolves its profile through a three-level inheritance chain:

```
Device Profile  >  Region defaults  >  Global defaults
```

The `ResolvedProfile` includes:

- **From profile:** device name, mgmt_ip, loopback_ip, site, platform, SSH credentials
- **From inheritance:** AS number, affinity, router/bridge flags
- **Derived at runtime:** router-id (= loopback_ip), VTEP source IP (= loopback_ip), BGP EVPN neighbors (from site route_reflectors, resolved to loopback IPs)
- **Merged maps:** generic_alias and prefix_lists are merged (profile > region > global)

### 3.7 Topology Specification (Optional)

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

### 4.1 Command Structure (Object-Oriented)

The CLI follows an object-oriented design where **context flags select the object** and **commands are methods on that object**:

```
newtron -n <network> -d <device> -i <interface> <verb> [args] [-x]
         |                                     |   |         |
         +----- Object Selection --------------+   +- Method-+
```

**Context Flags (Object Selectors):**

| Flag | Description | Object Level |
|------|-------------|--------------|
| `-n, --network` | Network configuration name | Network object |
| `-d, --device` | Target device name | Device object |
| `-i, --interface` | Target interface/LAG/VLAN | Interface object |

**Interface Name Formats:**

Both short and full interface names are accepted. Short names are automatically expanded:

| Short | Full (SONiC) | Example Usage |
|-------|--------------|---------------|
| `Eth0` | `Ethernet0` | `-i Eth0` |
| `Po100` | `PortChannel100` | `-i Po100` |
| `Vl100` | `Vlan100` | `-i Vl100` |
| `Lo0` | `Loopback0` | `-i Lo0` |

Case-insensitive: `eth0`, `Eth0`, `ETH0` all work.

**Command Verbs (Symmetric Read/Write Operations):**

| Write Verb | Read Verb | Description |
|------------|-----------|-------------|
| `set <prop> <val>` | `get <prop>` | Property access |
| `create <type>` | `show` / `list <type>s` | Object lifecycle |
| `delete <type>` | - | Object deletion |
| `add-member` / `remove-member` | `list-members` | Collection membership |
| `apply-service` / `remove-service` / `refresh-service` | `get-service` | Service binding |
| `bind-acl` / `unbind-acl` | `list-acls` | ACL binding |
| `bind-macvpn` / `unbind-macvpn` | `get-macvpn` | MAC-VPN binding |
| `add-bgp-neighbor` | `list-bgp-neighbors` | BGP neighbors |

**Operation Flags:**

- `-s, --specs` - Specification directory (default: `/etc/newtron`)
- `-x, --execute` - Execute changes (default: dry-run)
- `-v, --verbose` - Verbose output
- `--json` - JSON output format

### 4.2 Persistent Settings

Store default values to avoid repeating flags:

```bash
# Set default device
newtron settings set device leaf1-dc1

# Set default network
newtron settings set network production

# View current settings
newtron settings show

# Clear all settings
newtron settings clear
```

Settings are stored in `~/.newtron/settings.json`.

### 4.3 Dry-Run Mode (Default)

By default, all commands run in dry-run mode, showing what changes would be made without applying them:

```bash
# Using short interface name (Eth0 expands to Ethernet0)
newtron -d leaf1-dc1 -i Eth0 apply-service customer-l3 --ip 10.1.1.1/30

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
newtron -d leaf1-dc1 -i Eth0 apply-service customer-l3 --ip 10.1.1.1/30 -x

# Output:
# Changes to be applied:
#   [ADD] VRF|customer-l3-Ethernet0
#   ...
#
# Service applied successfully.
```

### 4.5 View Device/Interface Status

View device status:

```bash
newtron -d leaf1-dc1 show

# Output:
# Device: leaf1-dc1
# Management IP: 192.168.1.10
# Loopback IP: 10.0.0.10
# Platform: accton-as7726-32x (Accton-AS7726-32X)
#
# Derived Values (from spec -> device config):
#   BGP Local AS: 65001 (from region: datacenter-east)
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

View interface details:

```bash
# Short name Eth0 expands to Ethernet0
newtron -d leaf1-dc1 -i Eth0 show

# Output:
# Interface: Ethernet0
# Admin Status: up
# Oper Status: up
# MTU: 9100
# Speed: 100G
# Service: customer-l3
# VRF: customer-l3-Ethernet0
# IP Address: 10.1.1.1/30
# ACLs:
#   Ingress: customer-l3-in
#   Egress: customer-l3-out
```

### 4.6 Interactive Mode

Enter interactive menu mode:

```bash
# Without device (select later)
newtron interactive

# With device
newtron -d leaf1-dc1 interactive
```

```
=== Newtron Interactive Mode ===
Device: leaf1-dc1

Main Menu:
  1. Service Management
  2. Interface Configuration
  3. Link Aggregation (LAG)
  4. VLANs
  5. ACL/Filters
  6. EVPN/VXLAN
  7. BGP
  8. Health Checks
  9. Baseline Configuration
  q. Quit

Select option:
```

---

## 5. Service Management

Services are the primary way to configure interfaces with bundled VPN, filter, QoS, and routing settings.

### 5.1 List Available Services

```bash
newtron list services

# Output:
# Available Services:
#
# Name           Type   Description
# ----           ----   -----------
# customer-l3    l3     L3 routed customer interface
# server-irb     irb    Server VLAN with IRB routing
# transit        l3     Transit peering interface
```

### 5.2 Show Service Details

```bash
newtron show service customer-l3

# Output:
# Service: customer-l3
# Description: L3 routed customer interface
# Type: l3
#
# EVPN Configuration:
#   L3 VNI: 10001
#   VRF Type: interface (per-interface VRF)
#   Import RT: [65001:1]
#   Export RT: [65001:1]
#
# Filters:
#   Ingress: customer-ingress
#   Egress: customer-egress
#
# QoS Profile: customer-qos
#
# Routing:
#   Protocol: bgp
#   Peer AS: request (provided at apply time)
#   Import Policy: customer-import
#
# Permissions:
#   service.apply: [neteng, netops]
```

### 5.3 Apply a Service

Apply a service to an interface (OO style: method on interface object):

```bash
# Dry-run first (select device and interface, then call apply-service method)
newtron -d leaf1-dc1 -i Ethernet0 apply-service customer-l3 --ip 10.1.1.1/30

# Execute
newtron -d leaf1-dc1 -i Ethernet0 apply-service customer-l3 --ip 10.1.1.1/30 -x
```

**What happens (in order):**

1. Creates VRF `customer-l3-Ethernet0` with L3VNI 10001 (for `vrf_type: interface`)
2. Creates VXLAN_TUNNEL_MAP for the L3VNI
3. Configures BGP EVPN route targets for the VRF from the `ipvpn` definition
4. Binds Ethernet0 to the VRF
5. Configures IP 10.1.1.1/30 on interface
6. Creates or updates ACL from `customer-ingress` filter spec (shared across all interfaces using this service)
7. Binds ACL to interface (adds interface to ports list)
8. Applies QoS profile mappings (DSCP-to-TC, TC-to-queue) if specified
9. Configures BGP neighbor if service has routing spec
10. Records service binding in `NEWTRON_SERVICE_BINDING` table for tracking

**Preconditions checked:**

- Device must be connected and locked
- Interface must not be a LAG member (configure the LAG instead)
- Interface must not already have a service bound
- L3 service requires an IP address
- L2/IRB service requires a `macvpn` reference with a `vlan` field
- EVPN services require VTEP and BGP to be configured
- Shared VRF must already exist (for `vrf_type: shared`)
- All referenced filter_specs, QoS profiles, and route policies must exist in the spec

### 5.4 Apply Service to PortChannel

```bash
newtron -d leaf1-dc1 -i PortChannel100 apply-service customer-l3 --ip 10.2.1.1/30 -x
```

### 5.5 Remove a Service

```bash
# Preview removal
newtron -d leaf1-dc1 -i Ethernet0 remove-service

# Execute removal
newtron -d leaf1-dc1 -i Ethernet0 remove-service -x
```

**What happens:**

1. Removes QoS mapping from interface (always)
2. Removes IP addresses from interface (always)
3. Handles shared ACLs: removes interface from ACL ports list, or deletes ACL entirely if this was the last user
4. Unbinds interface from VRF; for per-interface VRFs (`vrf_type: interface`), deletes the VRF and all associated EVPN config (BGP_EVPN_VNI, BGP_GLOBALS_AF, VXLAN_TUNNEL_MAP)
5. For L2/IRB services: removes VLAN membership; if last member, removes all VLAN-related config (SVI, ARP suppression, L2VNI mapping, VLAN itself)
6. Deletes `NEWTRON_SERVICE_BINDING` entry

**Dependency-aware cleanup:** Shared resources (ACLs, VLANs, VRFs) are only deleted when the interface being cleaned up is the last user. This is determined by a `DependencyChecker` that counts remaining users while excluding the current interface.

### 5.6 Refresh a Service

When a service definition changes in `network.json` (e.g., updated filter-spec, QoS profile, or route policy), use `refresh-service` to synchronize the interface:

```bash
# Preview what would change
newtron -d leaf1-dc1 -i Ethernet0 refresh-service

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
newtron -d leaf1-dc1 -i Ethernet0 refresh-service -x
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

**Use cases:**

- Filter-spec rules were added/removed/modified in `network.json`
- QoS profile was updated
- Route policy was changed
- Service RT/VNI settings changed

**Example -- refresh after filter change:**

```bash
# 1. Edit network.json to add a new rule to the customer-ingress filter-spec
# 2. Refresh all interfaces using that service
newtron -d leaf1-dc1 -i Ethernet0 refresh-service -x
newtron -d leaf1-dc1 -i Ethernet4 refresh-service -x
```

---

## 6. Interface Configuration

### 6.1 List Interfaces

```bash
newtron -d leaf1-dc1 list interfaces

# Output:
# Interface     Status   Speed    MTU    Description        Service
# ---------     ------   -----    ---    -----------        -------
# Ethernet0     up       100G     9100   Uplink to spine1   customer-l3
# Ethernet4     up       100G     9100   Uplink to spine2   -
# Ethernet8     down     100G     9100   -                  -
# PortChannel100 up      200G     9100   Server bond        server-irb
```

### 6.2 Show Interface Details

```bash
newtron -d leaf1-dc1 -i Ethernet0 show

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

Use `get` method to read individual properties:

```bash
# Get MTU
newtron -d leaf1-dc1 -i Ethernet0 get mtu
# Output: mtu: 9100

# Get admin status
newtron -d leaf1-dc1 -i Ethernet0 get admin-status
# Output: admin-status: up

# Get description
newtron -d leaf1-dc1 -i Ethernet0 get description
# Output: description: Uplink to spine1

# Get bound service
newtron -d leaf1-dc1 -i Ethernet0 get-service
# Output: Service: customer-l3

# Get with JSON output
newtron -d leaf1-dc1 -i Ethernet0 get mtu --json
# Output: {"mtu": "9100"}
```

### 6.4 Configure Interface Properties

Use `set` method on the interface object. The `Set()` method determines the correct Redis table based on interface type (`PORT` for physical, `PORTCHANNEL` for LAGs):

```bash
# Set description
newtron -d leaf1-dc1 -i Ethernet4 set description "Link to customer-A" -x

# Set MTU
newtron -d leaf1-dc1 -i Ethernet4 set mtu 9000 -x

# Admin down
newtron -d leaf1-dc1 -i Ethernet8 set admin-status down -x

# Multiple settings (multiple set commands)
newtron -d leaf1-dc1 -i Ethernet4 set description "Customer link" -x
newtron -d leaf1-dc1 -i Ethernet4 set mtu 9000 -x
newtron -d leaf1-dc1 -i Ethernet4 set admin-status up -x
```

**Supported properties:**

| Property | Valid Values | Notes |
|----------|-------------|-------|
| `mtu` | Integer | Validated by `util.ValidateMTU()` |
| `speed` | `1G`, `10G`, `25G`, `40G`, `50G`, `100G`, `200G`, `400G` | Must match platform capabilities |
| `admin-status` | `up`, `down` | |
| `description` | Any string | |

**Preconditions:** LAG members cannot be configured directly; configure the parent LAG instead.

---

## 7. Link Aggregation (LAG)

### 7.1 List LAGs

```bash
newtron -d leaf1-dc1 list lags

# Output:
# LAG            Status   Members              Mode     MTU
# ---            ------   -------              ----     ---
# PortChannel100 up       Ethernet12,Ethernet16 active   9100
# PortChannel200 up       Ethernet20,Ethernet24 active   9100
```

### 7.2 Create a LAG

Create at device level (LAG does not exist yet):

```bash
# Create LAG with members
newtron -d leaf1-dc1 create lag PortChannel300 \
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

### 7.3 List LAG Members

Use `list-members` to see current membership:

```bash
newtron -d leaf1-dc1 -i PortChannel300 list-members

# Output:
# Ethernet28
# Ethernet32
```

### 7.4 Add Member to LAG

Select the LAG with `-i`, then use `add-member` method:

```bash
# Short names: Po300 = PortChannel300, Eth36 = Ethernet36
newtron -d leaf1-dc1 -i Po300 add-member Eth36 -x
```

**Preconditions checked:**

- Interface must exist
- Interface must be a physical interface
- Interface must not be in another LAG
- Interface must not have a service bound

### 7.5 Remove Member from LAG

```bash
newtron -d leaf1-dc1 -i Po300 remove-member Eth36 -x
```

**Preconditions checked:**

- Interface must be a member of this LAG

### 7.6 Delete a LAG

```bash
# Remove service first if bound
newtron -d leaf1-dc1 -i PortChannel300 remove-service -x

# Then delete LAG (device-level operation, members are removed automatically)
newtron -d leaf1-dc1 delete lag PortChannel300 -x
```

---

## 8. VLAN Management

### 8.1 List VLANs

```bash
newtron -d leaf1-dc1 list vlans

# Output:
# VLAN  NAME        MAC-VPN           L2VNI  ARP-SUPP
# ----  ----        -------           -----  --------
# 100   Servers     servers-vlan100   10100  on
# 200   Storage     storage-vlan200   10200  on
# 300   Management  -                 -      -
```

### 8.2 Create a VLAN

Create at device level:

```bash
newtron -d leaf1-dc1 create vlan 400 --name "NewVLAN" --description "Test VLAN" -x
```

**Preconditions checked:**

- VLAN ID must be between 1 and 4094
- VLAN must not already exist

### 8.3 List VLAN Members

Use `list-members` to see current VLAN membership:

```bash
# Vl100 = Vlan100
newtron -d leaf1-dc1 -i Vl100 list-members

# Output:
# Ethernet0 (untagged)
# Ethernet4 (tagged)
# PortChannel100 (untagged)
```

### 8.4 Add Port to VLAN

Select the VLAN with `-i Vl<id>` or `-i Vlan<id>`, then use `add-member` method:

```bash
# Add as untagged (access port) - short names
newtron -d leaf1-dc1 -i Vl400 add-member Eth40 -x

# Add as tagged (trunk port)
newtron -d leaf1-dc1 -i Vlan400 add-member Ethernet44 --tagged -x
```

**Preconditions checked:**

- Port must not have IP addresses (L3 config)
- Port must not be bound to a VRF
- L2 and L3 are mutually exclusive

### 8.5 Remove Port from VLAN

```bash
newtron -d leaf1-dc1 -i Vlan400 remove-member Ethernet40 -x
```

### 8.6 Configure VLAN SVI

Select the VLAN interface and use `set` methods:

```bash
newtron -d leaf1-dc1 -i Vlan400 set ip 10.1.40.1/24 -x
newtron -d leaf1-dc1 -i Vlan400 set vrf Vrf_TENANT1 -x
```

### 8.7 Delete a VLAN

```bash
# Remove all members first
newtron -d leaf1-dc1 -i Vlan400 remove-member Ethernet40 -x
newtron -d leaf1-dc1 -i Vlan400 remove-member Ethernet44 -x

# Remove MAC-VPN binding if present
newtron -d leaf1-dc1 -i Vlan400 unbind-macvpn -x

# Delete VLAN (device-level operation, also removes VNI mappings and members)
newtron -d leaf1-dc1 delete vlan 400 -x
```

The `DeleteVLAN` operation automatically removes VLAN members and VNI mappings as part of the changeset.

---

## 9. EVPN/VXLAN Configuration

### 9.1 Prerequisites

Before configuring EVPN services:

1. **VTEP must be configured**
2. **BGP EVPN must be configured**

### 9.2 Create VTEP

Device-level operation:

```bash
newtron -d leaf1-dc1 create vtep --source-ip 10.0.0.10 -x

# Source IP is typically the loopback IP (auto-derived from profile)
```

This creates two entries:

- `VXLAN_TUNNEL|vtep1` with `src_ip` set to the source IP
- `VXLAN_EVPN_NVO|nvo1` with `source_vtep` pointing to `vtep1`

### 9.3 View EVPN Status

```bash
newtron -d leaf1-dc1 list evpn

# Output:
# VTEP: vtep1
# Source IP: 10.0.0.10
#
# IP-VPN (L3):
#   Vrf_CUST1     L3VNI: 10001  RT: 65001:1
#   Vrf_TENANT1   L3VNI: 10002  RT: 65001:2
#
# MAC-VPN (L2):
#   Vlan100       servers-vlan100   L2VNI: 10100  ARP Suppress: on
#   Vlan200       storage-vlan200   L2VNI: 10200  ARP Suppress: on
```

### 9.4 Create VRF with L3VNI

Device-level operation:

```bash
newtron -d leaf1-dc1 create vrf Vrf_NEWCUST --l3vni 10003 -x
```

This creates:

- `VRF|Vrf_NEWCUST` with `vni` field
- `VXLAN_TUNNEL_MAP|vtep1|map_10003_Vrf_NEWCUST` mapping

**Preconditions checked:**

- VTEP must exist (for L3VNI mapping)
- VRF must not already exist
- VNI must be valid (1-16777215)

### 9.5 Map L2VNI and L3VNI

Direct VNI mapping operations (lower-level than service apply):

```bash
# Map VLAN to L2VNI
newtron -d leaf1-dc1 map-l2vni --vlan 100 --vni 10100 -x

# Map VRF to L3VNI
newtron -d leaf1-dc1 map-l3vni --vrf Vrf_CUST1 --vni 10001 -x

# Unmap a VNI
newtron -d leaf1-dc1 unmap-vni --vni 10100 -x
```

### 9.6 Get MAC-VPN Binding

Check the current MAC-VPN binding for a VLAN:

```bash
newtron -d leaf1-dc1 -i Vlan100 get-macvpn

# Output:
# MAC-VPN: servers-vlan100
#   L2VNI: 10100
#   ARP Suppression: true

# If no binding exists:
# (no MAC-VPN bound)
```

### 9.7 Bind VLAN to MAC-VPN

Bind a VLAN to a MAC-VPN definition from network.json.
Select the VLAN with `-i`, then use `bind-macvpn` method:

```bash
newtron -d leaf1-dc1 -i Vlan300 bind-macvpn tenant-vlan300 -x
```

The MAC-VPN definition in network.json specifies the L2VNI and ARP suppression:

```json
{
  "macvpn": {
    "tenant-vlan300": {
      "vlan": 300,
      "l2_vni": 10300,
      "arp_suppression": true
    }
  }
}
```

**Preconditions checked:**

- Interface must be a VLAN interface
- VTEP must exist
- MAC-VPN definition must exist in network.json

### 9.8 Unbind MAC-VPN

```bash
newtron -d leaf1-dc1 -i Vlan300 unbind-macvpn -x
```

This removes the L2VNI mapping (VXLAN_TUNNEL_MAP) and ARP suppression (SUPPRESS_VLAN_NEIGH) settings.

### 9.9 Delete VRF

```bash
# Remove all interface bindings first
newtron -d leaf1-dc1 -i Ethernet0 set vrf default -x

# Delete VRF (device-level operation, removes VNI mappings)
newtron -d leaf1-dc1 delete vrf Vrf_NEWCUST -x
```

**Preconditions checked:**

- VRF must exist
- VRF must have no interfaces bound to it

---

## 10. ACL Management

### 10.1 List ACL Tables

```bash
newtron -d leaf1-dc1 list acls

# Output:
# ACL Table                   Type   Stage    Ports
# ---------                   ----   -----    -----
# customer-l3-in              L3     ingress  Ethernet0,Ethernet4
# customer-l3-out             L3     egress   Ethernet0,Ethernet4
# DATAACL                     L3     ingress  Ethernet8
```

Note: ACLs created by services are shared -- the same ACL table is bound to multiple interfaces via the `ports` field.

### 10.2 Show ACL Rules

```bash
newtron -d leaf1-dc1 show acl customer-l3-in

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
# 101    src: 127.0.0.0/8                DENY     0
# 102    src: 224.0.0.0/4                DENY     567
# 9999   any                             PERMIT   892456
```

### 10.3 Create Custom ACL Table

Device-level operation:

```bash
newtron -d leaf1-dc1 create acl CUSTOM-ACL \
  --type L3 \
  --stage ingress \
  --description "Custom access control" \
  -x
```

### 10.4 Add ACL Rule

Device-level operation (ACL rules are part of the ACL table):

```bash
# Permit specific subnet
newtron -d leaf1-dc1 add-acl-rule CUSTOM-ACL RULE_100 \
  --priority 9000 \
  --src-ip 10.100.0.0/16 \
  --action permit \
  -x

# Deny SSH from external
newtron -d leaf1-dc1 add-acl-rule CUSTOM-ACL RULE_200 \
  --priority 8000 \
  --protocol tcp \
  --dst-port 22 \
  --action deny \
  -x

# Permit all else
newtron -d leaf1-dc1 add-acl-rule CUSTOM-ACL RULE_9999 \
  --priority 1 \
  --action permit \
  -x
```

### 10.5 Delete ACL Rule

```bash
newtron -d leaf1-dc1 delete-acl-rule CUSTOM-ACL RULE_200 -x
```

### 10.6 List ACLs Bound to Interface

Use `list-acls` to see ACLs bound to an interface:

```bash
newtron -d leaf1-dc1 -i Ethernet0 list-acls

# Output:
# Direction  ACL Name
# ---------  --------
# ingress    customer-l3-in
# egress     customer-l3-out
```

### 10.7 Bind ACL to Interface

Select the interface, then use `bind-acl` method:

```bash
newtron -d leaf1-dc1 -i Ethernet4 bind-acl CUSTOM-ACL --direction ingress -x
```

This adds the interface to the ACL's `ports` list (ACLs are shared across interfaces).

### 10.8 Unbind ACL from Interface

```bash
newtron -d leaf1-dc1 -i Ethernet4 unbind-acl CUSTOM-ACL -x
```

If this is the last interface bound to the ACL, the ACL table and all its rules are deleted. Otherwise, the interface is removed from the ACL's ports list.

### 10.9 Delete ACL Table

```bash
# Unbind from all interfaces first
newtron -d leaf1-dc1 -i Ethernet4 unbind-acl CUSTOM-ACL -x

# Delete table (device-level operation, rules are deleted automatically)
newtron -d leaf1-dc1 delete acl CUSTOM-ACL -x
```

### 10.10 How Filter-Specs Become ACLs

When a service with a filter_spec is applied, newtron:

1. Derives the ACL name from the service name (e.g., `customer-l3-in`, `customer-l3-out`)
2. Checks if the ACL already exists (shared across interfaces using the same service)
3. If the ACL exists: adds the new interface to the `ports` list
4. If the ACL does not exist: creates the ACL table and expands all rules

Rule expansion handles:

- **Prefix list references:** The `src_prefix_list`/`dst_prefix_list` fields reference entries in `prefix_lists`. Each prefix becomes a separate ACL rule (Cartesian product for src x dst).
- **Policer references:** The `policer` field adds a `POLICER` field to the ACL rule.
- **CoS/TC marking:** The `cos` field maps to traffic class values (e.g., `ef` -> TC 5).
- **Priority calculation:** ACL priority is computed as `10000 - sequence_number`.

---

## 11. BGP Direct Neighbors

Configure BGP neighbors on interfaces. These are direct eBGP peers using the interface IP as update-source.

### 11.1 Add BGP Neighbor (Auto-Derived IP)

For /30 and /31 subnets, the neighbor IP is automatically computed:

```bash
# Interface has IP 10.1.1.1/30, neighbor IP auto-derived as 10.1.1.2
newtron -d leaf1-dc1 -i Ethernet0 add-bgp-neighbor 65100 -x

# Interface has IP 10.1.1.0/31, neighbor IP auto-derived as 10.1.1.1
newtron -d leaf1-dc1 -i Ethernet4 add-bgp-neighbor 65200 -x
```

### 11.2 Add BGP Neighbor (Explicit IP)

For /29 or larger subnets, you must specify the neighbor IP:

```bash
# Interface has IP 10.1.1.1/29
newtron -d leaf1-dc1 -i Ethernet8 add-bgp-neighbor 65300 --neighbor-ip 10.1.1.5 -x
```

**Validation:**

- The neighbor IP must be on the same subnet as the interface
- If `--neighbor-ip` is omitted for /29 or larger, the command fails with an error
- The interface must have an IP address configured

### 11.3 Add BGP Neighbor (Passive Mode)

For customer-facing interfaces where the customer initiates the BGP session:

```bash
newtron -d leaf1-dc1 -i Ethernet0 add-bgp-neighbor 65100 --passive -x
```

**Notes:**

- `--passive` and `--neighbor-ip` are mutually exclusive
- Passive mode waits for the peer to initiate the connection

### 11.4 List BGP Neighbors

```bash
# List all BGP neighbors on device
newtron -d leaf1-dc1 list-bgp-neighbors

# List BGP neighbors on specific interface
newtron -d leaf1-dc1 -i Ethernet0 list-bgp-neighbors

# Output:
# Neighbor IP    Remote AS   State
# -----------    ---------   -----
# 10.1.1.2       65100       Established
```

### 11.5 Remove BGP Neighbor

```bash
# Remove BGP neighbor from interface
newtron -d leaf1-dc1 -i Ethernet0 remove-bgp-neighbor -x

# Remove specific neighbor IP (if multiple neighbors on interface)
newtron -d leaf1-dc1 -i Ethernet0 remove-bgp-neighbor --neighbor-ip 10.1.1.2 -x
```

The remove operation deletes all address-family entries (ipv4_unicast, ipv6_unicast, l2vpn_evpn) and the neighbor entry.

### 11.6 Security Constraints

All direct BGP neighbors have these non-negotiable security constraints:

| Constraint | Value | Rationale |
|------------|-------|-----------|
| TTL | 1 | Prevents BGP hijacking via multi-hop attacks (GTSM) |
| Subnet validation | Required | Neighbor IP must be on interface subnet |
| Update source | Interface IP | Uses directly connected IP, not loopback |

---

## 12. Health Checks

### 12.1 Run All Health Checks

Device-level operation:

```bash
newtron -d leaf1-dc1 health-check

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

### 12.2 Check Specific Component

The `RunHealthChecks` method accepts a `checkType` parameter to run specific checks:

```bash
# BGP health
newtron -d leaf1-dc1 health-check --check bgp

# Interface health
newtron -d leaf1-dc1 health-check --check interfaces

# EVPN health
newtron -d leaf1-dc1 health-check --check evpn

# LAG health
newtron -d leaf1-dc1 health-check --check lag
```

**Available check types:**

| Check | What it verifies |
|-------|-----------------|
| `bgp` | BGP neighbor count (warns if zero) |
| `interfaces` | Counts admin-down interfaces |
| `evpn` | VTEP existence and VNI mapping count |
| `lag` | LAG count and total member count |

### 12.3 JSON Output

```bash
newtron -d leaf1-dc1 health-check --json

# Useful for monitoring integration
```

The JSON output uses `HealthCheckResult` objects with `check`, `status`, and `message` fields.

---

## 13. State DB Queries

Newtron reads operational state from SONiC's **STATE_DB** (Redis database 6). This provides real-time information that is separate from configuration state.

### 13.1 Available State Tables

| Table | Information |
|-------|-------------|
| `PORT_TABLE` | Interface oper_status, admin_status, speed, MTU |
| `LAG_TABLE` | LAG oper_status, speed, MTU |
| `LAG_MEMBER_TABLE` | LAG member status, selected state, LACP port numbers |
| `VLAN_TABLE` | VLAN oper_status |
| `VRF_TABLE` | VRF state |
| `VXLAN_TUNNEL_TABLE` | VTEP oper_status, discovered remote VTEPs |
| `BGP_NEIGHBOR_TABLE` | Neighbor state, prefixes received/sent, uptime |
| `INTERFACE_TABLE` | Interface VRF binding, proxy ARP |
| `NEIGH_TABLE` | ARP/NDP neighbor entries |
| `FDB_TABLE` | MAC forwarding entries (port, type, VNI, remote VTEP) |
| `ROUTE_TABLE` | Routes (nexthop, interface, protocol) |
| `TRANSCEIVER_INFO` | Optics vendor, model, serial number |
| `TRANSCEIVER_STATUS` | Optics temperature, voltage, TX/RX power |

### 13.2 Query BGP Neighbor State

```bash
newtron -d leaf1-dc1 state bgp-neighbors

# Output:
# BGP Neighbors (from STATE_DB):
#
# Neighbor         State         AS     Pfx Rcvd  Pfx Sent  Uptime
# --------         -----         --     --------  --------  ------
# 10.0.0.1         Established   65001  12        8         02:15:30
# 10.0.0.2         Established   65001  12        8         02:15:28
# 10.1.1.2         Established   65100  4         6         01:45:12
```

### 13.3 Query Interface Operational State

```bash
newtron -d leaf1-dc1 state interfaces

# Output:
# Interface State (from STATE_DB):
#
# Interface     Admin    Oper     Speed    MTU
# ---------     -----    ----     -----    ---
# Ethernet0     up       up       100000   9100
# Ethernet4     up       up       100000   9100
# Ethernet8     down     down     100000   9100
```

### 13.4 Query EVPN State

```bash
newtron -d leaf1-dc1 state evpn

# Output:
# EVPN State (from STATE_DB):
#
# VTEP:
#   vtep1: oper_status=active, src_ip=10.0.0.10
#
# Remote VTEPs:
#   10.0.0.11 (leaf2-dc1)
#   10.0.0.12 (leaf3-dc1)
#
# VNI Count: 5
```

### 13.5 Query Transceiver Info

```bash
newtron -d leaf1-dc1 state transceiver Ethernet0

# Output:
# Transceiver: Ethernet0
#   Vendor: Finisar
#   Model: FTLX8574D3BCL
#   Serial: ABC12345
#   Type: QSFP28
#   Present: true
#   Temperature: 32.5C
#   TX Power: -1.2 dBm
#   RX Power: -2.1 dBm
```

### 13.6 State DB vs Config DB

It is important to understand the distinction between these two databases:

| Aspect | CONFIG_DB (DB 4) | STATE_DB (DB 6) |
|--------|-----------------|-----------------|
| Content | Desired configuration | Operational state |
| Modified by | Newtron, config reload, CLI | SONiC daemons (orchagent, bgpcfgd, etc.) |
| Persistence | Saved to /etc/sonic/config_db.json | Runtime only (lost on reboot) |
| Example | `PORT|Ethernet0` with `admin_status=up` | `PORT_TABLE|Ethernet0` with `oper_status=up` |

---

## 14. Baseline Configuration

### 14.1 List Available Configlets

Network-level operation (no device needed):

```bash
newtron list baselines

# Output:
# Available Configlets:
#
# Name              Version   Description
# ----              -------   -----------
# sonic-baseline    1.0       Base SONiC configuration
# sonic-evpn        1.0       EVPN/VXLAN baseline
# sonic-qos-8q      1.0       8-queue QoS configuration
```

### 14.2 Show Configlet Details

```bash
newtron show baseline sonic-baseline

# Output:
# Configlet: sonic-baseline
# Version: 1.0
# Description: Base SONiC configuration for all switches
#
# Variables Required:
#   - device_name (from profile)
#   - hwsku (from platform)
#   - platform (from profile)
#   - loopback_ip (from profile)
#   - ntp_server_1
#   - ntp_server_2
#   - syslog_server
#
# Tables Modified:
#   - DEVICE_METADATA
#   - LOOPBACK_INTERFACE
#   - NTP_SERVER
#   - SYSLOG_SERVER
#   - FEATURE
#   - MGMT_VRF_CONFIG
```

### 14.3 Apply Baseline Configuration

Device-level operation. The `ApplyBaseline` method automatically provides default variables from the device's resolved profile (device_name, loopback_ip):

```bash
# Preview
newtron -d leaf1-dc1 apply-baseline sonic-baseline

# Execute
newtron -d leaf1-dc1 apply-baseline sonic-baseline -x

# Apply multiple configlets
newtron -d leaf1-dc1 apply-baseline sonic-baseline sonic-evpn -x
```

### 14.4 Provide Missing Variables

```bash
newtron -d leaf1-dc1 apply-baseline sonic-baseline \
  --var ntp_server_1=10.100.1.1 \
  --var ntp_server_2=10.100.1.2 \
  --var syslog_server=10.100.2.1 \
  -x
```

### 14.5 Built-in Configlets

| Configlet | Tables Modified | Description |
|-----------|----------------|-------------|
| `sonic-baseline` | `DEVICE_METADATA`, `LOOPBACK_INTERFACE` | Sets hostname and loopback IP |
| `sonic-evpn` | `VXLAN_TUNNEL`, `VXLAN_EVPN_NVO` | Creates VTEP with loopback as source |

---

## 15. Cleanup Operations

Newtron's cleanup operation identifies and removes orphaned configurations -- resources that are no longer in use.

### 15.1 Preview All Cleanup

```bash
newtron -d leaf1-dc1 cleanup

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

### 15.2 Execute Cleanup

```bash
# Clean up all orphaned resources
newtron -d leaf1-dc1 cleanup -x
```

### 15.3 Cleanup Specific Types

```bash
# Only orphaned ACLs (ACL tables with empty ports list)
newtron -d leaf1-dc1 cleanup --type acls -x

# Only orphaned VRFs (VRFs with no interface bindings, excluding "default")
newtron -d leaf1-dc1 cleanup --type vrfs -x

# Only orphaned VNI mappings (VNI maps pointing to deleted VLANs/VRFs)
newtron -d leaf1-dc1 cleanup --type vnis -x
```

### 15.4 What Gets Cleaned Up

| Type | Detection Criteria | What Gets Deleted |
|------|--------------------|-------------------|
| `acl` | ACL table with empty `ports` field | All ACL_RULE entries, then ACL_TABLE |
| `vrf` | VRF with no interface `vrf_name` references (skip "default") | VRF entry |
| `vni` | VXLAN_TUNNEL_MAP pointing to nonexistent VRF or VLAN | VXLAN_TUNNEL_MAP entry |

### 15.5 Orphaned Resource Detection Details

**ACLs:** The cleanup scans all `ACL_TABLE` entries. If `acl.Ports` is empty, the ACL has no interfaces bound and is considered orphaned. All corresponding `ACL_RULE` entries (matching the `aclName|` prefix) are deleted first, then the table itself.

**VRFs:** The cleanup scans all `VRF` entries. For each VRF (excluding "default"), it checks if any `INTERFACE` entry has `vrf_name` matching the VRF. If no interfaces reference the VRF, it is orphaned.

**VNIs:** The cleanup scans all `VXLAN_TUNNEL_MAP` entries. For each mapping, it checks whether the referenced VRF or VLAN still exists. If the referenced resource is gone, the mapping is orphaned.

### 15.6 When to Run Cleanup

- After removing services from multiple interfaces
- After deleting VLANs or VRFs
- Periodically as maintenance
- When investigating unexpected config state
- After test runs leave stale configuration

**Philosophy:** Only active configurations should exist on the device. Cleanup prevents unbounded growth of orphaned config settings over time.

---

## 16. Config Persistence

### 16.1 Runtime vs Persistent Configuration

Newtron writes configuration changes to **Redis CONFIG_DB** (database 4). These changes take effect immediately at runtime but are **ephemeral** -- they exist only in Redis memory and are lost on device reboot.

```
newtron apply-service --> Redis CONFIG_DB (runtime) --> SONiC daemons process changes
                          NOT saved to disk yet
```

### 16.2 Persisting Changes

To persist changes across reboots, you must save the running configuration to disk on the SONiC device:

```bash
# SSH to the device
ssh admin@192.168.1.10

# Save running config to /etc/sonic/config_db.json
sudo config save -y
```

Or as a one-liner:

```bash
ssh admin@192.168.1.10 'sudo config save -y'
```

With the lab SSH credentials:

```bash
ssh cisco@<mgmt_ip> 'sudo config save -y'
```

### 16.3 What Happens on Reboot Without Save

If the device reboots before `config save -y`:

1. SONiC loads `/etc/sonic/config_db.json` from disk into Redis
2. All changes made since the last `config save` are gone
3. The device returns to its last persisted state

### 16.4 When to Save

Save after completing a set of related changes:

```bash
# Apply services
newtron -d leaf1-dc1 -i Ethernet0 apply-service customer-l3 --ip 10.1.1.1/30 -x
newtron -d leaf1-dc1 -i Ethernet4 apply-service customer-l3 --ip 10.1.2.1/30 -x

# Verify everything looks correct
newtron -d leaf1-dc1 health-check

# Then persist
ssh admin@192.168.1.10 'sudo config save -y'
```

### 16.5 Config Reload

To revert to the persisted configuration (discard runtime changes):

```bash
ssh admin@192.168.1.10 'sudo config reload -y'
```

This reloads `/etc/sonic/config_db.json` into Redis, effectively undoing any unsaved changes.

---

## 17. Lab Environment

Newtron includes a containerlab-based lab environment for testing and development.

### 17.1 Overview

The lab uses:

- **containerlab** to orchestrate SONiC-VS (virtual SONiC) containers
- **labgen** (`cmd/labgen/`) to generate topology files, startup configs, and newtron specs
- **setup.sh** (`testlab/setup.sh`) to manage the lab lifecycle

### 17.2 Start the Lab

```bash
# Build labgen and start the default spine-leaf topology
make lab-start

# Start a specific topology
make lab-start TOPO=spine-leaf
```

The `lab-start` process:

1. Builds the `labgen` binary
2. Generates lab artifacts in `testlab/.generated/`:
   - Per-node `config_db.json` startup configs
   - Per-node `frr.conf` FRR/BGP configs
   - `<topo>.clab.yml` containerlab topology
   - `specs/` directory with newtron specifications
3. Deploys the containerlab topology
4. Waits for SONiC containers to become healthy
5. Waits for Redis to be ready on all nodes (via SSH)
6. Applies unique system MACs (restarts swss)
7. Pushes FRR configuration to all nodes
8. Bridges QEMU NICs to ASIC simulator ports
9. **Patches profiles** with real management IPs and SSH credentials

### 17.3 Stop and Check Lab Status

```bash
# Stop the running lab
make lab-stop

# Check lab status (shows nodes, IPs, Redis connectivity)
make lab-status
```

### 17.4 Profile Patching

After containerlab deploys the topology, the `lab_patch_profiles` function updates each device profile with:

1. **Real management IP:** The Docker container IP assigned by containerlab
2. **SSH credentials:** Username and password from the containerlab YAML `env` section

This is necessary because the profile `mgmt_ip` in the generated specs initially contains a placeholder. The patching step replaces it with the actual container IP.

Example of a patched profile at `testlab/.generated/specs/profiles/leaf1.json`:

```json
{
  "mgmt_ip": "172.20.20.3",
  "loopback_ip": "10.0.0.1",
  "site": "dc1",
  "platform": "accton-as7726-32x",
  "ssh_user": "cisco",
  "ssh_pass": "cisco123"
}
```

### 17.5 Generated Spec Directory

All lab specifications are generated at:

```
testlab/.generated/specs/
  network.json
  site.json
  platforms.json
  profiles/
    leaf1.json
    leaf2.json
    spine1.json
    ...
```

When running newtron against the lab, point specs to this directory:

```bash
newtron -s testlab/.generated/specs -d leaf1 -i Eth0 show
```

### 17.6 Redis Access in the Lab

In the containerlab environment, Redis port 6379 is **not forwarded** by QEMU SLiRP networking. All Redis access goes through SSH tunnels:

```
newtron --> SSH to container:22 --> forward to 127.0.0.1:6379 (inside container)
```

The `lab_status` command verifies Redis connectivity via SSH for all SONiC nodes.

### 17.7 Running Tests Against the Lab

```bash
# Run E2E tests (requires running lab)
make test-e2e

# Full lifecycle: start lab, run tests, stop lab
make test-e2e-full

# Run unit tests (no lab required)
make test

# Run integration tests (uses local Docker Redis container)
make test-integration
```

### 17.8 Integration Test Redis (No Lab Required)

For integration tests that do not need a full containerlab topology, newtron provides a simpler Redis setup:

```bash
# Start a standalone Redis container
make redis-start

# Seed it with test data
make redis-seed

# Run integration tests
make test-integration

# Stop Redis
make redis-stop
```

The seed data lives in `testlab/seed/configdb.json` and `testlab/seed/statedb.json`.

---

## 18. Troubleshooting

### 18.1 Connection Issues

**Problem:** Cannot connect to device -- Redis connection refused

```bash
newtron -d leaf1-dc1 show
# Error: redis connection failed: dial tcp 192.168.1.10:6379: connect: connection refused
```

**Diagnosis:** Port 6379 is not accessible. In containerlab/QEMU environments, Redis port is NOT forwarded by SLiRP networking. Only SSH (port 22) is forwarded.

**Solutions:**

1. Ensure `ssh_user` and `ssh_pass` are set in the device profile -- this enables the SSH tunnel
2. Verify management IP in profile is correct
3. Check network connectivity: `ping 192.168.1.10`
4. For direct Redis (non-QEMU), verify Redis is running: `ssh admin@192.168.1.10 'redis-cli ping'`
5. For production, check firewall rules allow port 6379

### 18.2 SSH Tunnel Failed

**Problem:** SSH tunnel cannot be established

```bash
newtron -d leaf1-dc1 show
# Error: SSH tunnel to 172.20.20.3: SSH dial 172.20.20.3: dial tcp 172.20.20.3:22: connect: connection refused
```

**Solutions:**

1. Verify SSH service is running on the device: `ssh cisco@<mgmt_ip>` manually
2. Check SSH credentials in the profile (`ssh_user` and `ssh_pass`)
3. Verify port 22 is accessible from the newtron host
4. In lab: ensure `make lab-start` completed successfully (profile patching sets SSH creds)
5. In lab: check that the container is healthy: `make lab-status`

### 18.3 Changes Lost After Reboot

**Problem:** Configuration changes disappear after device reboot

**Cause:** Newtron writes to Redis CONFIG_DB at runtime. Changes are ephemeral until explicitly saved.

**Solution:** After applying changes, save the config on the device:

```bash
ssh admin@192.168.1.10 'sudo config save -y'
```

See [Section 16: Config Persistence](#16-config-persistence) for details.

### 18.4 Stale State After Test

**Problem:** Tests fail because leftover configuration from a previous test run causes crashes or unexpected behavior in SONiC daemons (vxlanmgrd, orchagent).

**Solutions:**

1. Use the `ResetLabBaseline()` function in test `TestMain` to clean stale entries before tests
2. Use the `Cleanup` device operation to remove orphaned resources
3. As a last resort, reload config on the device: `ssh admin@<ip> 'sudo config reload -y'`

In E2E tests, the `ResetLabBaseline()` helper connects to each SONiC node via SSH and deletes known stale CONFIG_DB keys in parallel.

### 18.5 Precondition Failures

**Problem:** Operation fails precondition check

```bash
newtron -d leaf1-dc1 -i Ethernet0 apply-service customer-l3 --ip 10.1.1.1/30
# Error: apply-service on Ethernet0: VTEP configured required - VTEP not configured - create VTEP first
```

**Solution:** Follow the dependency chain:

```bash
# 1. Create VTEP first
newtron -d leaf1-dc1 create vtep --source-ip 10.0.0.10 -x

# 2. Verify BGP is configured (manual or via baseline)

# 3. Then apply service
newtron -d leaf1-dc1 -i Ethernet0 apply-service customer-l3 --ip 10.1.1.1/30 -x
```

### 18.6 Interface in Use

**Problem:** Cannot modify interface that has configuration

```bash
newtron -d leaf1-dc1 -i PortChannel100 add-member Ethernet0
# Error: add-member on Ethernet0: interface must not have service - interface has service 'customer-l3' bound
```

**Solution:** Remove the service first:

```bash
newtron -d leaf1-dc1 -i Ethernet0 remove-service -x
newtron -d leaf1-dc1 -i PortChannel100 add-member Ethernet0 -x
```

### 18.7 Verbose Mode

Enable verbose output for debugging:

```bash
newtron -v -d leaf1-dc1 -i Ethernet0 apply-service customer-l3 --ip 10.1.1.1/30

# Shows detailed logs including:
# - Configuration loading
# - SSH tunnel establishment
# - Device state queries
# - Precondition checks
# - Change generation
```

### 18.8 View Audit Logs

Check what changes were made:

```bash
newtron -d leaf1-dc1 list audit --last 24h

# Output:
# Timestamp            User    Operation       Interface   Status
# ---------            ----    ---------       ---------   ------
# 2024-01-15 10:30:00  alice   apply-service   Ethernet0   success
# 2024-01-15 10:25:00  alice   create lag      PC100       success
# 2024-01-15 10:20:00  bob     apply-service   Ethernet4   failed
```

Show specific audit event:

```bash
newtron show audit <event-id>
```

### 18.9 Common Error Messages

| Error | Cause | Solution |
|-------|-------|----------|
| "device required" | Missing -d flag | Add `-d <device>` to command |
| "interface required" | Missing -i flag | Add `-i <interface>` to command |
| "interface does not exist" | Typo in interface name | Check `newtron -d <device> list interfaces` |
| "VRF does not exist" | Service uses shared VRF not created | Create VRF first or use `vrf_type: interface` |
| "VTEP not configured" | EVPN service without VTEP | Create VTEP with `newtron -d <device> create vtep` |
| "BGP not configured" | EVPN service without BGP | Configure BGP (manual or baseline) |
| "interface is a member of LAG" | Trying to configure LAG member directly | Configure the LAG instead |
| "L2 and L3 are mutually exclusive" | Adding VLAN member to routed port | Remove IP/VRF first |
| "redis connection failed" | Port 6379 not forwarded | Add ssh_user/ssh_pass to profile for SSH tunnel |
| "SSH dial ... connection refused" | Port 22 not reachable | Check SSH service, network, container health |
| "device not locked" | Forgot to Lock() before write ops (Go API) | Call `dev.Lock(ctx)` before operations |
| "interface already has service" | Apply-service to an interface that already has one | Remove existing service first |

### 18.10 Reset to Clean State

If configuration is in a bad state:

```bash
# Option 1: Remove services and start fresh
newtron -d leaf1-dc1 -i Ethernet0 remove-service -x

# Option 2: Run cleanup to remove all orphaned resources
newtron -d leaf1-dc1 cleanup -x

# Option 3: Use SONiC's config reload (on switch, reverts to saved config)
ssh admin@192.168.1.10 'sudo config reload -y'
```

---

## 19. Go API Usage

The newtron package provides an object-oriented API for programmatic access. The design uses parent references so child objects can access their parent's configuration.

### 19.1 Object Hierarchy

```
Network (top-level: loads specs, owns device profiles)
    |
    +-- Device (has parent reference to Network)
            |
            +-- Interface (has parent reference to Device)
```

Each object level can reach its parent:

- `Interface.Device()` returns the owning Device
- `Interface.Network()` returns the Network (via Device parent)
- `Device.Network()` returns the Network that created it

This design means operations on an Interface can access the full specification (services, filter-specs, prefix-lists) without needing specs passed as parameters.

### 19.2 Creating a Network and Connecting to a Device

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/newtron-network/newtron/pkg/network"
)

func main() {
    ctx := context.Background()

    // Create Network -- loads all specifications (declarative intent)
    net, err := network.NewNetwork("/etc/newtron")
    if err != nil {
        log.Fatal(err)
    }

    // Connect to a device (Device is created in Network's context)
    // If the profile has ssh_user/ssh_pass, an SSH tunnel is established
    // automatically for Redis access
    dev, err := net.ConnectDevice(ctx, "leaf1-dc1")
    if err != nil {
        log.Fatal(err)
    }
    defer dev.Disconnect()

    // Device has access to Network configuration through parent reference
    fmt.Printf("Device: %s\n", dev.Name())
    fmt.Printf("AS Number: %d\n", dev.ASNumber())
    fmt.Printf("BGP Neighbors: %v\n", dev.BGPNeighbors())
}
```

### 19.3 Connecting with SSH Tunnel (Explicit)

The SSH tunnel is established automatically by `Device.Connect()` when the device profile contains `ssh_user` and `ssh_pass`. You do not need to create the tunnel manually. However, understanding the flow is useful for debugging:

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

### 19.4 Accessing Specifications Through Parent References

```go
// Get an interface (Interface is created in Device's context)
intf, err := dev.GetInterface("Ethernet0")
if err != nil {
    log.Fatal(err)
}

// Interface can access Device properties
fmt.Printf("Device AS: %d\n", intf.Device().ASNumber())

// Interface can access Network configuration (via Device parent)
svc, err := intf.Network().GetService("customer-l3")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Service type: %s\n", svc.ServiceType)

// Get filter spec from Network
filter, err := intf.Network().GetFilterSpec("customer-edge-in")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Filter rules: %d\n", len(filter.Rules))
```

### 19.5 Checking Device State

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
if intf.IsLAGMember() {
    fmt.Printf("Interface is member of: %s\n", intf.LAGParent())
}
```

### 19.6 Listing Network-Level Specifications

```go
// List all services
for _, name := range net.ListServices() {
    svc, _ := net.GetService(name)
    fmt.Printf("Service %s: %s (%s)\n", name, svc.Description, svc.ServiceType)
}

// Get prefix list
prefixes, _ := net.GetPrefixList("rfc1918")
fmt.Printf("RFC1918 prefixes: %v\n", prefixes)

// Get region
region, _ := net.GetRegion("amer-wan")
fmt.Printf("Region AS: %d\n", region.ASNumber)
```

### 19.7 Performing Operations (OO Style)

Operations are methods on the objects they operate on:

```go
// Lock device for changes
if err := dev.Lock(ctx); err != nil {
    log.Fatal(err)
}
defer dev.Unlock()

// Interface operations -- methods on Interface
intf, _ := dev.GetInterface("Ethernet0")

changeSet, err := intf.ApplyService(ctx, "customer-l3", network.ApplyServiceOpts{
    IPAddress: "10.1.1.1/30",
    PeerAS:    65100, // For services with routing.peer_as="request"
})
if err != nil {
    log.Fatal(err)
}

// Preview changes (dry-run)
fmt.Println("Changes to be applied:")
fmt.Print(changeSet.String())

// Apply changes
if err := changeSet.Apply(dev); err != nil {
    log.Fatal(err)
}
```

**Device operations -- methods on Device:**

```go
// Create a VLAN
changeSet, err := dev.CreateVLAN(ctx, 100, network.VLANConfig{
    Description: "Server VLAN",
    L2VNI:       10100,
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

// Add member to PortChannel
changeSet, err := dev.AddPortChannelMember(ctx, "PortChannel100", "Ethernet8")
if err != nil {
    log.Fatal(err)
}
changeSet.Apply(dev)

// Create VRF with L3VNI
changeSet, err := dev.CreateVRF(ctx, "Vrf_CUST1", network.VRFConfig{
    L3VNI:    10001,
    ImportRT: []string{"65001:1"},
    ExportRT: []string{"65001:1"},
})
if err != nil {
    log.Fatal(err)
}
changeSet.Apply(dev)

// Run health checks
results, err := dev.RunHealthChecks(ctx, "") // "" = all checks
if err != nil {
    log.Fatal(err)
}
for _, r := range results {
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

### 19.8 Disconnect and Tunnel Cleanup

```go
// Disconnect closes Redis clients, releases lock, and closes SSH tunnel
dev.Disconnect()
```

For E2E tests using shared SSH tunnels, use the test utility:

```go
// In TestMain:
func TestMain(m *testing.M) {
    // Reset baseline (clean stale CONFIG_DB entries)
    if err := testutil.ResetLabBaseline(); err != nil {
        fmt.Fprintf(os.Stderr, "baseline reset: %v\n", err)
    }

    code := m.Run()

    // Close all shared SSH tunnels
    testutil.CloseLabTunnels()
    os.Exit(code)
}
```

The `CloseLabTunnels()` function iterates over all cached SSH tunnels and calls `Close()` on each, which stops the local listener, closes the SSH connection, and waits for forwarding goroutines to finish.

### 19.9 Design Benefits

The parent reference design provides:

1. **Natural Access**: Interface can access `Device.Network().GetService()` naturally
2. **No Spec Passing**: Operations do not need specs passed separately -- specs are reached via parent references
3. **Encapsulation**: Specs are owned by the right object level (Network owns services, Device owns state)
4. **Single Source of Truth**: Specs loaded once at Network level, translated to config at runtime
5. **True OO**: Operations are methods on objects (e.g., `intf.ApplyService()`, `dev.CreateVLAN()`), not standalone functions

---

## 20. Quick Reference

### 20.1 OO CLI Pattern

```
newtron -d <device> -i <interface> <verb> [args] [-x]
         |              |            |
         +-- Object Selection --+    +-- Method Call
```

### 20.2 Command Cheat Sheet

```bash
# Network-level (no device needed)
newtron list services                              # List available services
newtron list devices                               # List known devices
newtron show service <name>                        # Show service details
newtron settings set device <device>               # Set default device

# Device-level (select device with -d)
newtron -d <device> show                           # Show device status
newtron -d <device> list interfaces                # List interfaces
newtron -d <device> list vlans                     # List VLANs
newtron -d <device> list lags                      # List LAGs
newtron -d <device> list vrfs                      # List VRFs
newtron -d <device> list acls                      # List ACLs
newtron -d <device> create vlan <id> [options]     # Create VLAN
newtron -d <device> create lag <name> [options]    # Create LAG
newtron -d <device> create vrf <name> [options]    # Create VRF
newtron -d <device> create vtep [options]          # Create VTEP
newtron -d <device> create acl <name> [options]    # Create ACL
newtron -d <device> delete vlan <id>               # Delete VLAN
newtron -d <device> delete lag <name>              # Delete LAG
newtron -d <device> delete vrf <name>              # Delete VRF
newtron -d <device> delete acl <name>              # Delete ACL
newtron -d <device> health-check                   # Run health checks
newtron -d <device> apply-baseline <configlet>     # Apply baseline
newtron -d <device> cleanup                        # Remove orphaned configs
newtron -d <device> cleanup --type acls            # Cleanup only ACLs

# State DB queries
newtron -d <device> state bgp-neighbors            # BGP neighbor operational state
newtron -d <device> state interfaces               # Interface oper_status
newtron -d <device> state evpn                     # VTEP and remote VTEPs
newtron -d <device> state transceiver <port>       # Optics info

# Interface-level (select device and interface with -d -i)
newtron -d <device> -i <intf> show                 # Show interface details
newtron -d <device> -i <intf> get mtu              # Get MTU value
newtron -d <device> -i <intf> get admin-status     # Get admin status
newtron -d <device> -i <intf> set mtu <value>      # Set MTU
newtron -d <device> -i <intf> set admin-status up  # Set admin status
newtron -d <device> -i <intf> set description "x"  # Set description
newtron -d <device> -i <intf> get-service          # Get bound service name
newtron -d <device> -i <intf> apply-service <svc> --ip <cidr>  # Apply service
newtron -d <device> -i <intf> remove-service       # Remove service
newtron -d <device> -i <intf> refresh-service      # Sync to current service def
newtron -d <device> -i <intf> list-acls            # List bound ACLs
newtron -d <device> -i <intf> bind-acl <acl>       # Bind ACL
newtron -d <device> -i <intf> unbind-acl <acl>     # Unbind ACL

# LAG/VLAN operations (interface is LAG or VLAN)
newtron -d <device> -i PortChannel100 list-members             # List LAG members
newtron -d <device> -i PortChannel100 add-member Ethernet0     # Add LAG member
newtron -d <device> -i PortChannel100 remove-member Ethernet0  # Remove LAG member
newtron -d <device> -i Vlan100 list-members                    # List VLAN members
newtron -d <device> -i Vlan100 add-member Ethernet4 --tagged   # Add VLAN member
newtron -d <device> -i Vlan100 remove-member Ethernet4         # Remove VLAN member
newtron -d <device> -i Vlan100 get-macvpn                      # Get MAC-VPN binding
newtron -d <device> -i Vlan100 bind-macvpn servers-vlan100     # Bind MAC-VPN
newtron -d <device> -i Vlan100 unbind-macvpn                   # Unbind MAC-VPN

# BGP operations (direct neighbors, TTL=1 enforced)
newtron -d <device> -i <intf> list-bgp-neighbors               # List BGP neighbors
newtron -d <device> -i <intf> add-bgp-neighbor <asn>           # Auto-derive IP (/30, /31)
newtron -d <device> -i <intf> add-bgp-neighbor <asn> --neighbor-ip <ip>  # Explicit IP (/29+)
newtron -d <device> -i <intf> add-bgp-neighbor <asn> --passive # Passive mode (customer)
newtron -d <device> -i <intf> remove-bgp-neighbor              # Remove BGP neighbor
```

### 20.3 Dependency Order for New Device

1. Configure baseline: `newtron -d <device> apply-baseline sonic-baseline -x`
2. Create VTEP: `newtron -d <device> create vtep --source-ip <loopback> -x`
3. Configure BGP (manual or baseline)
4. Create LAGs if needed: `newtron -d <device> create lag ...`
5. Create VLANs if needed: `newtron -d <device> create vlan ...`
6. Apply services: `newtron -d <device> -i <interface> apply-service ...`
7. Persist: `ssh admin@<ip> 'sudo config save -y'`

### 20.4 Lab Quick Start

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

## 21. Topology Provisioning

Topology provisioning automates device configuration from a `topology.json` spec file. Instead of manually running service-apply commands for each interface, the topology declares the complete desired state.

### 21.1 Creating a Topology Spec

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

### 21.2 Full Device Provisioning (Go API)

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
- Connects only for delivery (CompositeOverwrite)
- Replaces entire CONFIG_DB

### 21.3 Per-Interface Provisioning (Go API)

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

### 21.4 Inspecting Generated Config

You can generate the composite config without delivering it:

```go
composite, err := tp.GenerateDeviceComposite("leaf1-dc1")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Generated %d entries\n", composite.EntryCount())
```

### 21.5 When to Use Each Mode

| Mode | Use Case |
|------|----------|
| **ProvisionDevice** | Initial device setup, lab provisioning, disaster recovery |
| **ProvisionInterface** | Adding a service to one interface on a running device |

---

## 22. Composite Configuration

Composite mode generates a complete CONFIG_DB configuration offline (without a device connection), then delivers it atomically.

### 22.1 Building a Composite Config (Go API)

```go
cb := network.NewCompositeBuilder("leaf1-dc1", network.CompositeOverwrite).
    SetGeneratedBy("my-tool").
    SetDescription("Initial provisioning")

// Add device-level entries
cb.AddBGPGlobals("default", map[string]string{
    "local_asn": "13908",
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

### 22.2 Delivering a Composite Config

```go
// Connect to device
dev, err := net.ConnectDevice(ctx, "leaf1-dc1")
if err != nil {
    log.Fatal(err)
}
defer dev.Disconnect()

if err := dev.Lock(ctx); err != nil {
    log.Fatal(err)
}
defer dev.Unlock()

// Deliver with overwrite (replaces entire CONFIG_DB)
result, err := dev.DeliverComposite(composite, network.CompositeOverwrite)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Applied: %d, Skipped: %d\n", result.Applied, result.Skipped)
```

### 22.3 Delivery Modes

| Mode | Behavior | Use Case |
|------|----------|----------|
| **CompositeOverwrite** | Replace entire CONFIG_DB | Initial provisioning, lab setup |
| **CompositeMerge** | Add entries to existing config | Incremental service deployment |

**Merge restrictions**: Only supported for interface-level service configuration, and only if the target interface has no existing service binding.

---

## 23. Related Documentation

The following documents provide additional detail on specific topics:

| Document | Description |
|----------|-------------|
| [HLD.md](HLD.md) | High-Level Design -- architecture overview, design decisions, system context |
| [LLD.md](LLD.md) | Low-Level Design -- package structure, data flow, object model details |
| [DESIGN_PRINCIPLES.md](DESIGN_PRINCIPLES.md) | Core design principles and philosophy |
| [CONFIGDB_GUIDE.md](CONFIGDB_GUIDE.md) | SONiC CONFIG_DB table reference -- every table, field, and key format used by newtron |
| [SONIC_VS_PITFALLS.md](SONIC_VS_PITFALLS.md) | Known issues and workarounds for SONiC Virtual Switch (VS) in containerlab |
| [CONTAINERLAB_HOWTO.md](CONTAINERLAB_HOWTO.md) | Detailed guide for setting up and using the containerlab test environment |
| [NGDP_DEBUGGING.md](NGDP_DEBUGGING.md) | Debugging the NGDP ASIC simulator (data plane bridging, NIC issues) |
| [LEARNINGS.md](LEARNINGS.md) | Lessons learned during development (SONiC quirks, testing strategies, design evolution) |
| [TESTING.md](TESTING.md) | Test strategy -- unit tests, integration tests, test helpers |
| [E2E_TESTING.md](E2E_TESTING.md) | End-to-end testing guide -- lab setup, test patterns, data plane verification |
| [VERIFICATION_TOOLKIT.md](VERIFICATION_TOOLKIT.md) | Tools and techniques for verifying configuration correctness |
