# Containerlab + vrnetlab Guide for Newtron

This document covers the internals of how newtron uses containerlab and vrnetlab
to stand up virtual SONiC fabrics for end-to-end testing. It is written for
developers who need to understand the plumbing, diagnose failures, or extend
the test lab infrastructure.

---

## Table of Contents

- [1. Architecture Overview](#1-architecture-overview)
- [2. QEMU Port Forwarding](#2-qemu-port-forwarding)
- [3. Tripping Hazards](#3-tripping-hazards)
  - [3.1 Management IP Unknown Until Deploy](#31-management-ip-unknown-until-deploy)
  - [3.2 Container NIC to QEMU VM Mapping](#32-container-nic-to-qemu-vm-mapping)
  - [3.3 SSH Tunnel for Redis](#33-ssh-tunnel-for-redis)
  - [3.4 Container Health States](#34-container-health-states)
  - [3.5 config_db.json Loading](#35-config_dbjson-loading)
  - [3.6 Server Containers](#36-server-containers)
- [4. Lab Lifecycle](#4-lab-lifecycle)
  - [4.1 Starting (lab-start)](#41-starting-lab-start)
  - [4.2 Stopping (lab-stop)](#42-stopping-lab-stop)
  - [4.3 Status Checking (lab-status)](#43-status-checking-lab-status)
- [5. Topology File Format](#5-topology-file-format)
- [6. Debugging containerlab Issues](#6-debugging-containerlab-issues)
- [7. Resource Requirements](#7-resource-requirements)
- [8. Common Commands Reference](#8-common-commands-reference)

---

## 1. Architecture Overview

The newtron E2E test lab is a layered stack of three technologies:

```
+--------------------------------------------------------------+
|  Newtron E2E Test Code (Go)                                  |
|  Connects to devices via network.ConnectDevice(), uses       |
|  SSH tunnels for Redis access (port 6379 inside VM)          |
+--------------------------------------------------------------+
        |                            |
        | SSH tunnel (Go)            | containerlab inspect
        |                            |
+--------------------------------------------------------------+
|  containerlab                                                |
|  Orchestrates Docker containers with virtual network links   |
|  Docker bridge management network (172.20.20.0/24)           |
+--------------------------------------------------------------+
        |                            |
        | Docker containers          | veth pairs (point-to-point)
        |                            |
+--------------------------------------------------------------+
|  vrnetlab (per SONiC container)                              |
|  Wraps QEMU VM inside Docker container                       |
|  SLiRP user-mode networking for management plane             |
|  tc mirred redirect rules bridge ethN <-> tapN for data      |
+--------------------------------------------------------------+
        |
        | QEMU virtio NICs
        |
+--------------------------------------------------------------+
|  SONiC VM (inside QEMU)                                      |
|  Full SONiC OS: Redis, swss, bgp (FRR), syncd, etc.         |
|  Redis listens on localhost:6379 (no auth, no PAM)           |
+--------------------------------------------------------------+
```

**containerlab** orchestrates Docker containers with point-to-point virtual
Ethernet links between them. Each link becomes a veth pair connecting the
containers, which appear as `ethN` interfaces inside each container.

**vrnetlab** wraps a QEMU virtual machine inside a Docker container. The
Docker image (`vrnetlab/cisco_sonic:ngdp-202411`) contains a Debian base with
QEMU, plus the SONiC `.qcow2` disk image. When the container starts,
`launch.py` boots QEMU with the disk image, waits for the console login
prompt, performs bootstrap configuration, and then optionally loads a startup
`config_db.json` via SCP.

**SONiC** runs as a full virtual switch inside the QEMU VM. It has its own
Redis instance (CONFIG_DB on DB 4, STATE_DB on DB 6, ASIC_DB on DB 1),
the swss orchestration agent, FRR for BGP/routing, and the syncd process
(virtual switch variant for SONiC-VS).

**Management network**: containerlab creates a Docker bridge network (default
subnet `172.20.20.0/24`). Each container gets an IP from this pool on `eth0`.
For vrnetlab containers, QEMU's SLiRP user-mode networking forwards selected
TCP ports from the container's management IP to the VM's internal IP
(`10.0.0.15`). Management IPs are dynamically assigned by Docker and are not
known until after `containerlab deploy` completes.

**Data-plane links**: containerlab creates veth pairs for each link defined in
the topology. These appear as `eth1`, `eth2`, ... inside each container. For
vrnetlab containers, these must be bridged to the QEMU VM's tap interfaces
using `tc mirred redirect` rules (see section 3.2). For server containers
(kind: linux), the `ethN` interfaces are directly usable.

---

## 2. QEMU Port Forwarding

vrnetlab uses QEMU's SLiRP user-mode networking stack for the management
interface. SLiRP creates an internal NAT network (`10.0.0.0/24`) between the
container and the VM. The VM gets `10.0.0.15` as its management address.

Only **explicitly listed TCP ports** are forwarded from the container's
external interface to the VM. The forwarding is set up in `vrnetlab.py` at
container boot time:

```python
# From testlab/images/common/vrnetlab.py -- VM.__init__()
self.mgmt_tcp_ports = [80, 443, 830, 6030, 8080, 9339, 32767, 50051, 57400]
```

The gen_mgmt() method builds the QEMU `-netdev user` argument:

```
hostfwd=tcp:0.0.0.0:22-10.0.0.15:22,       # SSH (always added)
hostfwd=udp:0.0.0.0:161-10.0.0.15:161,     # SNMP (always added)
hostfwd=tcp:0.0.0.0:80-10.0.0.15:80,       # HTTP
hostfwd=tcp:0.0.0.0:443-10.0.0.15:443,     # HTTPS
hostfwd=tcp:0.0.0.0:830-10.0.0.15:830,     # NETCONF
hostfwd=tcp:0.0.0.0:6030-10.0.0.15:6030,   # gNMI/gNOI (Arista)
hostfwd=tcp:0.0.0.0:8080-10.0.0.15:8080,   # gNMI/gNOI (SONiC), HTTP APIs
hostfwd=tcp:0.0.0.0:9339-10.0.0.15:9339,   # gNMI/gNOI (IANA)
hostfwd=tcp:0.0.0.0:32767-10.0.0.15:32767, # gNMI/gNOI (Juniper)
hostfwd=tcp:0.0.0.0:50051-10.0.0.15:50051, # gNMI/gNOI (Cisco NX-OS)
hostfwd=tcp:0.0.0.0:57400-10.0.0.15:57400  # gNMI/gNOI (Nokia)
```

### Port 6379 (Redis) is NOT forwarded

This is intentional. Redis inside SONiC has **no authentication** -- no PAM,
no password, no TLS. Forwarding it directly would expose an unauthenticated
database on the management network. SSH provides the security layer.

To reach Redis, you must go through SSH:

```bash
# From the host, via shell:
sshpass -p cisco123 ssh -o StrictHostKeyChecking=no \
    cisco@172.20.20.4 "redis-cli -n 4 PING"

# From the host, one-shot query:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "redis-cli -n 4 HGETALL 'VLAN|Vlan100'"
```

### Port 22 is the only reliable access method

SSH (port 22) is the only port guaranteed to be forwarded and reachable from
outside the container. All lab automation -- config loading, Redis access,
FRR config push, tc rule application -- goes through SSH.

---

## 3. Tripping Hazards

These are the non-obvious things that will silently break your lab if you are
not aware of them.

### 3.1 Management IP Unknown Until Deploy

containerlab assigns management IPs from the Docker bridge pool
(`172.20.20.0/24`). The actual IP for each container depends on the order
Docker assigns addresses, which is not deterministic across deploys.

**Consequence**: The `labgen` tool cannot know the management IP at artifact
generation time. Profile files are generated with a `PLACEHOLDER` value:

```json
{
  "name": "leaf1",
  "mgmt_ip": "PLACEHOLDER",
  "platform": "vs-platform",
  "site": "lab-site"
}
```

After `containerlab deploy` completes, `lab_patch_profiles()` in `setup.sh`
reads the actual IPs and patches the profile JSON files:

```bash
# setup.sh -- lab_patch_profiles() logic:
# 1. Run containerlab inspect or docker inspect to get container IPs
# 2. For each SONiC node:
#    - Strip the "clab-<topo>-" prefix to get node name
#    - Update specs/profiles/<node>.json with real IP
#    - Also inject ssh_user and ssh_pass from clab YAML env vars
```

The Go test code reads patched profiles via `network.NewNetwork(specsDir)`.
If profiles still contain `PLACEHOLDER`, device connections fail with address
resolution errors.

**To verify patching worked:**

```bash
cat testlab/.generated/specs/profiles/leaf1.json | python3 -m json.tool
# Should show "mgmt_ip": "172.20.20.X" (not PLACEHOLDER)
# Should show "ssh_user": "cisco", "ssh_pass": "cisco123"
```

### 3.2 Container NIC to QEMU VM Mapping

This is the single most confusing part of the vrnetlab stack. Container `ethN`
interfaces are **NOT** directly connected to the QEMU VM's SONiC interfaces.
There are two levels of indirection involved.

**Level 1: Container ethN to QEMU tapN (vrnetlab)**

vrnetlab's `launch.py` boots QEMU with tap interfaces. The `tc` datapath mode
(used by the SONiC image) creates `tc mirred redirect` rules that bridge
container `ethN` to QEMU `tapN`:

```
Container eth1  <--tc mirred-->  tap1  <--QEMU virtio-->  VM eth1
Container eth2  <--tc mirred-->  tap2  <--QEMU virtio-->  VM eth2
Container eth3  <--tc mirred-->  tap3  <--QEMU virtio-->  VM eth3
```

This is set up by vrnetlab's `create_tc_tap_ifup()` script, which runs when
QEMU creates each tap device.

**Level 2: VM ethN to ASIC simulator swvethN (NGDP SONiC)**

Inside the SONiC VM, the NGDP ASIC simulator uses veth pairs. The simulated
switch ports are `vethN`, and the other ends of the veth pairs are `swvethN`.
Traffic must be bridged between the VM's `ethN` (QEMU NIC) and `swvethN`
(ASIC port) using additional `tc mirred redirect` rules:

```
VM eth1  <--tc mirred-->  swveth1  <---veth-pair--->  veth1 (ASIC port)
VM eth2  <--tc mirred-->  swveth2  <---veth-pair--->  veth2 (ASIC port)
VM eth3  <--tc mirred-->  swveth3  <---veth-pair--->  veth3 (ASIC port)
```

These rules are applied by `lab_bridge_nics()` in `setup.sh`, which SSHes
into each SONiC node and runs:

```bash
# Applied by lab_bridge_nics() via SSH into each SONiC VM:
for i in $(seq 1 64); do
  if ip link show eth$i && ip link show swveth$i; then
    sudo /usr/sbin/tc qdisc add dev swveth$i clsact
    sudo /usr/sbin/tc filter add dev swveth$i ingress flower \
        action mirred egress redirect dev eth$i
    sudo /usr/sbin/tc qdisc add dev eth$i clsact
    sudo /usr/sbin/tc filter add dev eth$i ingress flower \
        action mirred egress redirect dev swveth$i
  else
    break
  fi
done
```

**Critical detail**: The `tc` binary is at `/usr/sbin/tc`, which is NOT in
the default PATH inside the SONiC VM. You must use `sudo /usr/sbin/tc`.

**If tc rules are not applied**: Data-plane links will appear "up" at layer 2
but no traffic will flow. Ping between connected interfaces will fail silently.
This is one of the most common causes of "everything looks right but nothing
works" scenarios.

**Sequential NIC mapping**: containerlab assigns container interfaces
sequentially (eth1, eth2, eth3...) regardless of the SONiC interface numbering.
The `labgen` tool's `buildSequentialIfaceMaps()` function handles this
translation:

```
Container eth1 = first SONiC interface in sorted order
Container eth2 = second SONiC interface in sorted order
Container eth3 = third SONiC interface in sorted order
```

For the spine-leaf topology:

```
spine1: Ethernet0 -> eth1, Ethernet1 -> eth2
spine2: Ethernet0 -> eth1, Ethernet1 -> eth2
leaf1:  Ethernet0 -> eth1, Ethernet1 -> eth2, Ethernet2 -> eth3
leaf2:  Ethernet0 -> eth1, Ethernet1 -> eth2, Ethernet2 -> eth3
```

**Verify tc rules are applied:**

```bash
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "sudo /usr/sbin/tc filter show dev eth1 ingress"
# Should show:
# filter protocol all pref 49152 flower chain 0
#   action order 1: mirred (Egress Redirect to device swveth1) ...

sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "sudo /usr/sbin/tc filter show dev swveth1 ingress"
# Should show:
# filter protocol all pref 49152 flower chain 0
#   action order 1: mirred (Egress Redirect to device eth1) ...
```

### 3.3 SSH Tunnel for Redis

Redis inside the SONiC VM listens on `127.0.0.1:6379` with **no
authentication**. It has no password, no PAM, no TLS. SSH provides the
security layer.

**Go code approach** (used by E2E tests):

The `pkg/device/tunnel.go` SSHTunnel creates a local TCP listener on a random
port and forwards connections through SSH to `127.0.0.1:6379` inside the VM:

```go
// From pkg/device/tunnel.go:
func NewSSHTunnel(host, user, pass string) (*SSHTunnel, error) {
    config := &ssh.ClientConfig{
        User: user,
        Auth: []ssh.AuthMethod{ssh.Password(pass)},
        HostKeyCallback: ssh.InsecureIgnoreHostKey(),
    }

    sshClient, err := ssh.Dial("tcp", host+":22", config)
    // ...
    listener, err := net.Listen("tcp", "127.0.0.1:0")  // random port
    // ...
}

// Each accepted connection is forwarded:
func (t *SSHTunnel) forward(local net.Conn) {
    remote, err := t.sshClient.Dial("tcp", "127.0.0.1:6379")
    // bidirectional io.Copy between local and remote
}
```

The test harness in `internal/testutil/lab.go` maintains a shared tunnel pool
(`labTunnels` map) so multiple tests can share tunnels to the same node. The
`labTunnelAddr()` function returns the local address
(`127.0.0.1:<random_port>`) that Redis clients should connect to.

**Shell approach** (used for manual debugging):

```bash
# One-shot Redis command via SSH:
sshpass -p cisco123 ssh -o StrictHostKeyChecking=no \
    cisco@172.20.20.4 "redis-cli -n 4 PING"

# Interactive Redis session via SSH:
sshpass -p cisco123 ssh -o StrictHostKeyChecking=no \
    cisco@172.20.20.4 "redis-cli -n 4"

# Dump all VLAN keys:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "redis-cli -n 4 KEYS '*VLAN*'"

# Read a specific entry:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "redis-cli -n 4 HGETALL 'VLAN|Vlan100'"

# Check BGP state in STATE_DB:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "redis-cli -n 6 KEYS 'NEIGH_STATE_TABLE*'"
```

**Never expose Redis directly on the management network.** Even in lab
environments, keeping Redis behind SSH prevents accidental state corruption
from stray connections.

### 3.4 Container Health States

vrnetlab containers use a health check mechanism based on the `/run/health`
file inside the container. The `healthcheck.py` script reads this file and
reports the health status:

```python
# From testlab/images/common/healthcheck.py:
health_file = open("/run/health", "r")
health = health_file.read()
exit_status, message = health.strip().split(" ", 1)
sys.exit(int(exit_status))
```

The `vrnetlab.py` VR class updates this file as the VM progresses:

| Health File Content | Docker Status | Meaning |
|---------------------|---------------|---------|
| `1 starting` | `starting` | QEMU VM is booting (takes 3-5 minutes) |
| `0 running` | `healthy` | VM booted, login succeeded, bootstrap done |
| `1 VM failed - restarting` | `unhealthy` | VM crashed or boot timed out |

**Waiting for healthy containers:**

The `lab_wait_healthy()` function in `setup.sh` polls `containerlab inspect`
until no SONiC containers are in "starting" state (5-minute timeout). It
**excludes linux containers** (servers) because those are always immediately
ready -- they run a simple `sleep infinity` command.

```bash
# Check container health states manually:
docker inspect --format '{{.State.Health.Status}}' clab-spine-leaf-leaf1
# Returns: starting, healthy, or unhealthy

# Check all containers:
docker ps --format "table {{.Names}}\t{{.Status}}" | grep clab-spine-leaf
```

The containerlab YAML generated by `labgen` sets generous health check
parameters for SONiC-VM nodes:

```yaml
healthcheck:
  start-period: 600   # 10 min grace period for VM boot
  interval: 30        # check every 30s
  timeout: 10         # each check has 10s to complete
  retries: 3          # mark unhealthy after 3 consecutive failures
```

### 3.5 config_db.json Loading

The startup configuration flow for vrnetlab SONiC containers has multiple
stages:

**Stage 1: Placement** -- `labgen` generates a per-node `config_db.json` in
`testlab/.generated/<node>/config_db.json`. The containerlab YAML references
this as `startup-config`:

```yaml
nodes:
  leaf1:
    kind: sonic-vm
    image: vrnetlab/cisco_sonic:ngdp-202411
    startup-config: leaf1/config_db.json
```

containerlab copies this file into the container at `/config/config_db.json`
before the container starts.

**Stage 2: SCP into VM** -- When the QEMU VM boots and SSH becomes available,
the `backup.sh` restore script SCPs the config into the VM:

```bash
# From testlab/images/sonic-ngdp/docker/backup.sh -- restore():
# 1. Wait for SSH to be available (up to 30 retries)
# 2. SCP /config/config_db.json to the VM at /tmp/config_db.json
# 3. Run: sudo config load -y /tmp/config_db.json && sudo config save -y
```

This happens during `launch.py`'s `startup_config()` method, which calls
`backup.sh -u cisco -p cisco123 restore` after the VM login completes.

**Stage 3: SONiC services load config** -- The `config load` command writes
the JSON into Redis CONFIG_DB. SONiC's orchestration agent (orchagent) reads
CONFIG_DB and programs the ASIC. FRR reads its own configuration separately.

**FRR config is separate:**

`config_db.json` does not contain FRR/BGP configuration. FRR config is pushed
separately by `lab_push_frr()` in `setup.sh`:

```bash
# 1. SCP frr.conf to the VM at /tmp/frr.conf
# 2. docker cp /tmp/frr.conf bgp:/etc/frr/lab_frr.conf
# 3. docker exec bgp vtysh -f /etc/frr/lab_frr.conf
# 4. docker exec bgp vtysh -c 'write memory'
```

**Verify config was loaded:**

```bash
# Check CONFIG_DB has the expected hostname:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "redis-cli -n 4 HGETALL 'DEVICE_METADATA|localhost'"

# Check LOOPBACK_INTERFACE:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "redis-cli -n 4 KEYS 'LOOPBACK_INTERFACE*'"

# Check FRR config inside the VM:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "sudo docker exec bgp vtysh -c 'show running-config'"
```

### 3.6 Server Containers

Server containers are Linux containers (not vrnetlab VMs). They use the
`nicolaka/netshoot:latest` image, which comes pre-installed with networking
tools.

**Key differences from SONiC containers:**

| Property | SONiC Container | Server Container |
|----------|-----------------|------------------|
| kind | `sonic-vm` | `linux` |
| Image | `vrnetlab/cisco_sonic:ngdp-202411` | `nicolaka/netshoot:latest` |
| Command | `launch.py` (QEMU) | `sleep infinity` |
| Has QEMU | Yes | No |
| Has Redis | Yes | No |
| Has SSH | Yes (port 22) | No (use docker exec) |
| ethN usage | Bridged to QEMU tap via tc | Directly usable |
| Boot time | 3-5 minutes | Seconds |

**Accessing server containers:**

```bash
# Run a command:
docker exec clab-spine-leaf-server1 ping -c 3 10.70.0.2

# Configure an IP address:
docker exec clab-spine-leaf-server1 \
    ip addr add 10.70.0.1/24 dev eth1

# Run tcpdump:
docker exec clab-spine-leaf-server1 \
    tcpdump -i eth1 -c 10 -nn

# Check interfaces:
docker exec clab-spine-leaf-server1 ip addr show

# Check ARP table:
docker exec clab-spine-leaf-server1 arp -n

# Interactive shell:
docker exec -it clab-spine-leaf-server1 bash
```

**Interface mapping for servers:**
- `eth0` = Docker management network (172.20.20.x) -- do not configure
- `eth1` = First data-plane link (connected to a leaf switch port)

In the topology YAML, server interfaces pass through as-is:

```yaml
links:
  - endpoints: ["leaf1:Ethernet2", "server1:eth1"]
  - endpoints: ["leaf2:Ethernet2", "server2:eth1"]
```

**Pre-installed tools** (nicolaka/netshoot):
ping, ip, arp, tcpdump, traceroute, mtr, nmap, curl, iperf3, netcat,
bridge, ss, nslookup, dig, and many more.

---

## 4. Lab Lifecycle

### 4.1 Starting (lab-start)

```bash
make lab-start              # default: spine-leaf topology
make lab-start TOPO=minimal # minimal topology
```

The full startup sequence in `setup.sh lab_start()`:

**Step 1: Build labgen**

```bash
go build -o testlab/.generated/labgen ./cmd/labgen/
```

**Step 2: Generate artifacts**

```bash
testlab/.generated/labgen \
    -topology testlab/topologies/spine-leaf.yml \
    -output testlab/.generated \
    -configlets ./configlets
```

This generates:
- `testlab/.generated/spine-leaf.clab.yml` -- containerlab topology
- `testlab/.generated/spine1/config_db.json` -- per-node startup configs
- `testlab/.generated/spine1/frr.conf` -- per-node FRR configs
- `testlab/.generated/specs/` -- newtron spec files (network.json, site.json,
  platforms.json, profiles/\*.json)

**Step 3: containerlab deploy**

```bash
cd testlab/.generated
containerlab deploy -t spine-leaf.clab.yml --reconfigure
```

This creates Docker containers, sets up veth links, copies startup configs.
The `--reconfigure` flag ensures a clean deploy even if containers from a
previous run exist.

**Step 4: Wait for SONiC containers to boot (5-minute timeout)**

Polls `containerlab inspect --format json` every 10 seconds. Excludes
`kind: linux` containers (servers) from the health check. Waits until no
SONiC container has "starting" in its status field.

**Step 5: Wait for Redis via SSH (5-minute timeout)**

For each SONiC node, polls `redis-cli -n 4 PING` via SSH every 5 seconds
until it returns `PONG`. This confirms the VM is fully booted and Redis is
accepting connections.

**Step 6: Apply unique system MACs (lab_apply_macs)**

All QEMU VMs boot with the same default MAC address. Each node's
`config_db.json` specifies a unique MAC in `DEVICE_METADATA`, but it only
takes effect after swss restarts. This step:

1. Disables warm restart: `redis-cli -n 4 HSET 'WARM_RESTART|swss' 'enable' 'false'`
2. Saves config: `sudo config save -y`
3. Cold-restarts swss: `sudo systemctl restart swss`
4. Waits 30 seconds for swss to reinitialize
5. Verifies MACs via `ip link show Ethernet0`

Without this step, all nodes share the same MAC, causing L2 loops and ARP
confusion.

**Step 7: Push FRR configuration (lab_push_frr)**

For each SONiC node that has a `frr.conf`:

```bash
# SCP config to VM:
sshpass -p cisco123 scp frr.conf cisco@<ip>:/tmp/frr.conf

# Copy into FRR container and load:
sshpass -p cisco123 ssh cisco@<ip> \
    "sudo docker cp /tmp/frr.conf bgp:/etc/frr/lab_frr.conf && \
     sudo docker exec bgp vtysh -f /etc/frr/lab_frr.conf && \
     sudo docker exec bgp vtysh -c 'write memory'"
```

**Step 8: Bridge container NICs to ASIC ports (lab_bridge_nics)**

Applies tc mirred redirect rules inside each SONiC VM to bridge `ethN` to
`swvethN` (see section 3.2). Without this, data-plane traffic does not flow.

**Step 9: Patch profiles with management IPs (lab_patch_profiles)**

Reads actual IPs from Docker inspect, updates profile JSON files:

```bash
# Before patching:
{"name": "leaf1", "mgmt_ip": "PLACEHOLDER", ...}

# After patching:
{"name": "leaf1", "mgmt_ip": "172.20.20.4", "ssh_user": "cisco", "ssh_pass": "cisco123", ...}
```

**Step 10: Record topology name**

```bash
echo "spine-leaf" > testlab/.generated/.lab-state
```

The `.lab-state` file is read by E2E tests to discover which topology is
running.

### Lab Startup Order Is Critical

The steps above MUST execute in this exact order. Getting the order wrong
causes hard-to-diagnose failures:

| Wrong Order | Symptom |
|---|---|
| Bridge NICs before MAC apply | swss restart removes tc rules; must bridge AFTER MACs |
| Push FRR before Redis ready | FRR config loaded before BGP container is up |
| Patch profiles before containers healthy | Docker inspect returns stale/no IPs |
| Run E2E tests before NIC bridging | ASIC simulator never sees packets; `WaitForASICVLAN` may behave differently |
| Skip MAC apply step | All nodes share the same MAC; L2 loops, ARP confusion, BGP flapping |

If the lab seems broken after startup, the first thing to check is whether
all steps completed successfully. Look at the `make lab-start` output for
any `FAILED` lines.

### 4.2 Stopping (lab-stop)

```bash
make lab-stop
```

This performs:

```bash
# Read topology name from state file
topo_name=$(cat testlab/.generated/.lab-state)

# Destroy containerlab topology and clean up
cd testlab/.generated
containerlab destroy -t ${topo_name}.clab.yml --cleanup

# Remove state file
rm -f testlab/.generated/.lab-state
```

The `--cleanup` flag removes the containerlab state directory and any
persistent data. To also remove all generated artifacts:

```bash
make clean
# Removes testlab/.generated/ entirely
```

### 4.3 Status Checking (lab-status)

```bash
make lab-status
```

Output:

```
=== Containerlab Status ===
Topology: spine-leaf

+---+--------------------------+-----------+-----------------------------------+-------+----------------+
| # |          Name            |   Kind    |              Image                | State |    IPv4        |
+---+--------------------------+-----------+-----------------------------------+-------+----------------+
| 1 | clab-spine-leaf-spine1   | sonic-vm  | vrnetlab/cisco_sonic:ngdp-202411  | running | 172.20.20.2/24 |
| 2 | clab-spine-leaf-spine2   | sonic-vm  | vrnetlab/cisco_sonic:ngdp-202411  | running | 172.20.20.3/24 |
| 3 | clab-spine-leaf-leaf1    | sonic-vm  | vrnetlab/cisco_sonic:ngdp-202411  | running | 172.20.20.4/24 |
| 4 | clab-spine-leaf-leaf2    | sonic-vm  | vrnetlab/cisco_sonic:ngdp-202411  | running | 172.20.20.5/24 |
| 5 | clab-spine-leaf-server1  | linux     | nicolaka/netshoot:latest          | running | 172.20.20.6/24 |
| 6 | clab-spine-leaf-server2  | linux     | nicolaka/netshoot:latest          | running | 172.20.20.7/24 |
+---+--------------------------+-----------+-----------------------------------+-------+----------------+

Redis connectivity (SONiC nodes only, via SSH):
  clab-spine-leaf-spine1: 172.20.20.2 (SSH->Redis) OK
  clab-spine-leaf-spine2: 172.20.20.3 (SSH->Redis) OK
  clab-spine-leaf-leaf1: 172.20.20.4 (SSH->Redis) OK
  clab-spine-leaf-leaf2: 172.20.20.5 (SSH->Redis) OK
```

The Redis connectivity check uses SSH, not direct port access. For each SONiC
node, it runs:

```bash
sshpass -p cisco123 ssh -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
    cisco@<ip> "redis-cli -n 4 PING"
```

If the response contains `PONG`, the node is marked as `OK`.

---

## 5. Topology File Format

Newtron uses its own topology YAML format (stored in `testlab/topologies/`),
which is translated into containerlab YAML by `labgen`.

### Full Example: spine-leaf.yml

```yaml
name: spine-leaf

defaults:
  image: vrnetlab/cisco_sonic:ngdp-202411    # Docker image for SONiC nodes
  username: cisco                             # SSH credentials (for vrnetlab)
  password: cisco123
  platform: vs-platform                       # newtron platform name
  site: lab-site                              # newtron site name
  hwsku: "Force10-S6000"                      # SONiC hardware SKU
  ntp_server_1: "10.100.0.1"
  ntp_server_2: "10.100.0.2"
  syslog_server: "10.100.0.3"

network:
  as_number: 65000                            # BGP AS for the fabric
  region: lab-region                          # newtron region name

nodes:
  spine1:
    role: spine
    loopback_ip: "10.0.0.1"                  # Router ID
    variables:
      cluster_id: "10.0.0.1"                 # BGP route reflector cluster ID
  spine2:
    role: spine
    loopback_ip: "10.0.0.2"
    image: vrnetlab/cisco_sonic:ngdp-202411   # Per-node image override
    variables:
      cluster_id: "10.0.0.2"
  leaf1:
    role: leaf
    loopback_ip: "10.0.0.11"
    variables:
      vtep_name: vtep1                        # VXLAN tunnel name
      spine1_ip: "10.0.0.1"
      spine2_ip: "10.0.0.2"
  leaf2:
    role: leaf
    loopback_ip: "10.0.0.12"
    image: vrnetlab/cisco_sonic:ngdp-202411
    variables:
      vtep_name: vtep1
      spine1_ip: "10.0.0.1"
      spine2_ip: "10.0.0.2"
  server1:
    role: server                              # Linux container, not SONiC
    image: nicolaka/netshoot:latest
  server2:
    role: server
    image: nicolaka/netshoot:latest

links:
  - endpoints: ["spine1:Ethernet0", "leaf1:Ethernet0"]
  - endpoints: ["spine1:Ethernet1", "leaf2:Ethernet0"]
  - endpoints: ["spine2:Ethernet0", "leaf1:Ethernet1"]
  - endpoints: ["spine2:Ethernet1", "leaf2:Ethernet1"]
  - endpoints: ["leaf1:Ethernet2", "server1:eth1"]
  - endpoints: ["leaf2:Ethernet2", "server2:eth1"]

role_defaults:                                # Configlets applied per role
  spine:
    - sonic-baseline
    - sonic-evpn-spine
  leaf:
    - sonic-baseline
    - sonic-evpn-leaf
    - sonic-acl-copp
    - sonic-qos-8q
```

### Sections Explained

**`defaults`**: Default values applied to all SONiC nodes. Per-node overrides
(like `image`) take precedence. The `username`/`password` become environment
variables (`USERNAME`/`PASSWORD`) in the generated containerlab YAML, used by
vrnetlab for SSH access to the VM.

**`network`**: Fabric-wide settings used in configlet template resolution and
newtron spec generation.

**`nodes`**: Each entry defines a node. Required fields:
- `role`: `spine`, `leaf`, or `server`
- `loopback_ip`: Router ID (required for spine/leaf, not for server)

Optional fields:
- `image`: Override the default Docker image
- `cmd`: Override the container command (servers only)
- `variables`: Key-value pairs passed to configlet templates

**`links`**: Point-to-point connections. Each link has exactly two endpoints
in the format `"nodeName:InterfaceName"`. SONiC interface names (e.g.,
`Ethernet0`) are translated to containerlab names (e.g., `eth1`) by labgen.
Server interface names (e.g., `eth1`) pass through as-is.

**`role_defaults`**: Lists of configlet names applied to all nodes of a given
role. Configlets are JSON templates in the `configlets/` directory (e.g.,
`configlets/sonic-baseline.json`). They are merged by labgen to produce each
node's `config_db.json`.

### How labgen Generates Artifacts

```
topology YAML + configlets/*.json
        |
        v
    labgen parse + validate
        |
        +---> Per-node config_db.json (configlets merged + variables resolved)
        |
        +---> Per-node frr.conf (FRR/BGP config)
        |
        +---> <topo>.clab.yml (containerlab topology with translated interfaces)
        |
        +---> specs/network.json (services, filters, VPN definitions)
        |
        +---> specs/site.json (spine nodes as route reflectors)
        |
        +---> specs/platforms.json (hardware platform definitions)
        |
        +---> specs/profiles/<node>.json (per-node profile with PLACEHOLDER mgmt_ip)
```

The interface translation for sonic-vm nodes works by sorting all interfaces
used by a node (from the links section) by their Ethernet number, then
assigning sequential `ethN` names:

```go
// From pkg/labgen/clab_gen.go -- buildSequentialIfaceMaps():
// Sort interfaces by Ethernet number
// Ethernet0 -> eth1
// Ethernet1 -> eth2
// Ethernet2 -> eth3
// (sequential, not based on SONiC port numbering)
```

---

## 6. Debugging containerlab Issues

### Container won't start

```bash
# Check if the Docker image exists:
docker images | grep sonic
# Expected: vrnetlab/cisco_sonic   ngdp-202411   ...

# Check container logs for QEMU errors:
docker logs clab-spine-leaf-leaf1 2>&1 | head -100

# Check if resources are exhausted:
docker stats --no-stream

# Check Docker events:
docker events --since 5m --filter container=clab-spine-leaf-leaf1

# Try a manual deploy with verbose output:
cd testlab/.generated
containerlab deploy -t spine-leaf.clab.yml --reconfigure 2>&1

# Check for port conflicts:
ss -tlnp | grep -E ':(22|80|443|6379|8080)'
```

### Redis unreachable via SSH

```bash
# Step 1: Can you SSH into the VM at all?
sshpass -p cisco123 ssh -o StrictHostKeyChecking=no \
    cisco@172.20.20.4 "echo SSH_OK"

# Step 2: Is Redis running inside the VM?
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "ps aux | grep redis"

# Step 3: Can Redis respond inside the VM?
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "redis-cli PING"

# Step 4: Is CONFIG_DB (DB 4) accessible?
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "redis-cli -n 4 DBSIZE"

# Step 5: Check if QEMU VM is still booting:
docker inspect --format '{{.State.Health.Status}}' \
    clab-spine-leaf-leaf1
# "starting" means VM is still booting -- wait longer
# "unhealthy" means VM boot failed -- check docker logs
```

### Data-plane links not working

```bash
# Step 1: Verify tc rules are applied inside the VM:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "sudo /usr/sbin/tc filter show dev eth1 ingress"
# Should show mirred redirect to swveth1

sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "sudo /usr/sbin/tc filter show dev swveth1 ingress"
# Should show mirred redirect to eth1

# Step 2: If no rules, re-apply them:
sshpass -p cisco123 ssh cisco@172.20.20.4 "
for i in \$(seq 1 8); do
  if ip link show eth\$i 2>/dev/null && ip link show swveth\$i 2>/dev/null; then
    sudo /usr/sbin/tc qdisc add dev swveth\$i clsact 2>/dev/null || \
        sudo /usr/sbin/tc qdisc replace dev swveth\$i clsact
    sudo /usr/sbin/tc filter add dev swveth\$i ingress flower \
        action mirred egress redirect dev eth\$i 2>/dev/null
    sudo /usr/sbin/tc qdisc add dev eth\$i clsact 2>/dev/null || \
        sudo /usr/sbin/tc qdisc replace dev eth\$i clsact
    sudo /usr/sbin/tc filter add dev eth\$i ingress flower \
        action mirred egress redirect dev swveth\$i 2>/dev/null
    echo eth\$i OK
  fi
done"

# Step 3: Check interface status inside the VM:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "show interfaces status"

# Step 4: Check container-side interfaces:
docker exec clab-spine-leaf-leaf1 ip link show
```

### IP address wrong in profile

```bash
# Check what containerlab assigned:
containerlab inspect -t testlab/.generated/spine-leaf.clab.yml

# Check what's in the profile:
cat testlab/.generated/specs/profiles/leaf1.json | python3 -m json.tool

# If they don't match, re-run patching manually:
# (extract from setup.sh -- lab_patch_profiles logic)
docker inspect --format \
    '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' \
    clab-spine-leaf-leaf1
```

### BGP not converging

```bash
# Check if FRR config was pushed:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "sudo docker exec bgp vtysh -c 'show running-config'"

# Check BGP summary:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "sudo docker exec bgp vtysh -c 'show bgp summary'"

# Check BGP neighbors in detail:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "sudo docker exec bgp vtysh -c 'show bgp neighbors'"

# Check interface IPs (BGP peering requires correct IPs):
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "show ip interface"

# Check STATE_DB for BGP session state:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "redis-cli -n 6 KEYS 'NEIGH_STATE_TABLE*'"
```

### Stale state after failed test run

E2E tests register cleanup functions, but if a test crashes or the process is
killed, stale CONFIG_DB entries may remain. The test harness calls
`ResetLabBaseline()` from `TestMain` before running tests, which deletes known
stale keys. For manual cleanup:

```bash
# Delete specific stale entries:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "redis-cli -n 4 DEL 'VLAN|Vlan500'"

# Nuclear option: reload config from saved config_db.json:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "sudo config load -y /etc/sonic/config_db.json && sudo config save -y"

# Or just restart the lab:
make lab-stop && make lab-start
```

---

## 7. Resource Requirements

### Hardware Recommendations

| Topology | VMs | RAM (minimum) | CPU Cores (minimum) | Boot Time |
|----------|-----|---------------|---------------------|-----------|
| minimal (1 spine + 1 leaf) | 2 | 8 GB | 4 | ~3 min |
| spine-leaf (2 spine + 2 leaf + 2 server) | 4 VMs + 2 containers | 16 GB | 8 | ~5 min |

### Per-VM Resource Allocation

Each QEMU VM is configured by `labgen` with:

```yaml
# From pkg/labgen/clab_gen.go:
cpu: 2              # 2 vCPU cores
memory: "6144mib"   # 6 GB RAM
```

The QEMU launch in `launch.py` adds:

```python
# From testlab/images/sonic-ngdp/docker/launch.py:
self.qemu_args.extend(["-smp", "2"])  # 2 SMP cores
# ram=4096 (from superclass init, overridden by clab memory setting)
```

With KVM acceleration (`/dev/kvm` must exist), QEMU uses hardware
virtualization. Without KVM, VMs run in emulation mode and are significantly
slower.

### Storage

- SONiC qcow2 image: ~2 GB
- Docker image (with Debian + QEMU): ~3 GB
- Per-VM overlay disk: ~100 MB
- Generated artifacts: ~1 MB

### Network

containerlab creates:
- 1 Docker bridge network (management, 172.20.20.0/24)
- N veth pairs (one per link in the topology)
- No external network access required during operation

### Checking Available Resources

```bash
# Available RAM:
free -h

# Available CPU cores:
nproc

# KVM support:
ls -la /dev/kvm
# If missing, VMs will run without hardware acceleration (very slow)

# Docker disk usage:
docker system df

# Running containers and their resource usage:
docker stats --no-stream
```

---

## 8. Common Commands Reference

### Lab Management

```bash
# Start the default (spine-leaf) topology:
make lab-start

# Start a specific topology:
make lab-start TOPO=minimal

# Check lab status:
make lab-status

# Stop the lab:
make lab-stop

# Full lifecycle: start, test, stop:
make test-e2e-full

# Clean all generated artifacts:
make clean

# Direct script usage (bypasses Makefile):
./testlab/setup.sh lab-start spine-leaf
./testlab/setup.sh lab-status
./testlab/setup.sh lab-stop
```

### SSH into SONiC VM

```bash
# Interactive SSH session:
sshpass -p cisco123 ssh -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null \
    cisco@172.20.20.4

# Run a single command:
sshpass -p cisco123 ssh cisco@172.20.20.4 "show interfaces status"

# Become root:
sshpass -p cisco123 ssh cisco@172.20.20.4 "sudo -i"
```

### Redis via SSH

```bash
# Ping CONFIG_DB:
sshpass -p cisco123 ssh cisco@172.20.20.4 "redis-cli -n 4 PING"

# List all keys:
sshpass -p cisco123 ssh cisco@172.20.20.4 "redis-cli -n 4 KEYS '*'"

# List VLAN keys:
sshpass -p cisco123 ssh cisco@172.20.20.4 "redis-cli -n 4 KEYS '*VLAN*'"

# Read a specific entry:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "redis-cli -n 4 HGETALL 'DEVICE_METADATA|localhost'"

# Read VRF entries:
sshpass -p cisco123 ssh cisco@172.20.20.4 "redis-cli -n 4 KEYS 'VRF*'"

# Check DB sizes:
sshpass -p cisco123 ssh cisco@172.20.20.4 "redis-cli -n 4 DBSIZE"

# Delete a key (cleanup):
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "redis-cli -n 4 DEL 'VLAN|Vlan500'"

# STATE_DB (DB 6) - BGP state:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "redis-cli -n 6 KEYS 'NEIGH_STATE_TABLE*'"

# ASIC_DB (DB 1) - programmed ASIC state:
sshpass -p cisco123 ssh cisco@172.20.20.4 \
    "redis-cli -n 1 KEYS 'ASIC_STATE:SAI_OBJECT_TYPE_VLAN*'"
```

### Server Container Commands

```bash
# Ping from server1 to an IP:
docker exec clab-spine-leaf-server1 ping -c 3 10.70.0.2

# Configure an IP on server's data interface:
docker exec clab-spine-leaf-server1 \
    ip addr add 10.70.0.1/24 dev eth1

# Set default route:
docker exec clab-spine-leaf-server1 \
    ip route add default via 10.70.0.254 dev eth1

# Check interfaces:
docker exec clab-spine-leaf-server1 ip addr show

# Check routing table:
docker exec clab-spine-leaf-server1 ip route show

# Run tcpdump on data interface:
docker exec clab-spine-leaf-server1 tcpdump -i eth1 -c 20 -nn

# Check ARP table:
docker exec clab-spine-leaf-server1 arp -n

# Interactive shell:
docker exec -it clab-spine-leaf-server1 bash
```

### Container Logs and Inspection

```bash
# View container logs (QEMU boot, vrnetlab output):
docker logs clab-spine-leaf-leaf1

# Follow logs in real time:
docker logs -f clab-spine-leaf-leaf1

# Last 50 lines:
docker logs clab-spine-leaf-leaf1 2>&1 | tail -50

# Check container health:
docker inspect --format '{{.State.Health.Status}}' clab-spine-leaf-leaf1

# Check container IP:
docker inspect --format \
    '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' \
    clab-spine-leaf-leaf1

# containerlab inspect (all nodes):
containerlab inspect -t testlab/.generated/spine-leaf.clab.yml

# containerlab inspect as JSON:
containerlab inspect -t testlab/.generated/spine-leaf.clab.yml --format json
```

### SONiC Commands Inside the VM

After SSHing into a SONiC VM (`sshpass -p cisco123 ssh cisco@<ip>`):

```bash
# Interface status:
show interfaces status

# IP interfaces:
show ip interface

# BGP summary (via vtysh in the bgp container):
sudo docker exec bgp vtysh -c "show bgp summary"

# BGP neighbors:
sudo docker exec bgp vtysh -c "show bgp neighbors"

# BGP IPv4 routes:
sudo docker exec bgp vtysh -c "show bgp ipv4 unicast"

# EVPN routes:
sudo docker exec bgp vtysh -c "show bgp l2vpn evpn"

# VXLAN tunnel status:
show vxlan tunnel

# VXLAN VNI mapping:
show vxlan vlanvnimap

# VLAN info:
show vlan brief

# Port channel (LAG):
show interfaces portchannel

# ACL tables:
show acl table

# ACL rules:
show acl rule

# System MAC:
show platform summary

# Running configuration (saved config_db):
show runningconfiguration all

# Reload config from file:
sudo config load -y /etc/sonic/config_db.json
sudo config save -y

# Restart a SONiC service:
sudo systemctl restart swss
sudo systemctl restart bgp
```

### E2E Test Commands

```bash
# Run all E2E tests:
make test-e2e

# Run a single test:
go test -tags e2e -v -count=1 -timeout 10m ./test/e2e/ \
    -run TestE2E_CreateVLAN

# Run all VLAN tests:
go test -tags e2e -v -count=1 -timeout 10m ./test/e2e/ \
    -run 'TestE2E_.*VLAN'

# Run data plane tests:
go test -tags e2e -v -count=1 -timeout 10m ./test/e2e/ \
    -run 'TestE2E_DataPlane'

# Run with JSON output for CI:
go test -tags e2e -v -count=1 -timeout 10m -json ./test/e2e/

# View last test results:
cat testlab/.generated/e2e-results.txt

# View test report:
cat testlab/.generated/e2e-report.md
```

### Build the vrnetlab Docker Image

If the SONiC vrnetlab image needs to be rebuilt:

```bash
cd testlab/images/sonic-ngdp
# Place the qcow2 image: sonic-vs-ngdp-202411.qcow2
make
# Builds: vrnetlab/cisco_sonic:ngdp-202411
```

The Makefile in `testlab/images/sonic-ngdp/` determines the version tag from
the qcow2 filename (`sonic-vs-ngdp-202411.qcow2` yields tag `ngdp-202411`).
