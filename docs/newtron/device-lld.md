# Newtron Device Layer LLD

The device connection layer handles SSH tunnels, Redis client connections, and state access for SONiC devices. This document covers `pkg/newtron/device/sonic/` (SONiC-specific Redis implementation, including all shared types) — the low-level plumbing that connects newtron to a SONiC switch's Redis databases.

For the architectural principles behind newtron, newtlab, and newtrun, see [Design Principles](../DESIGN_PRINCIPLES.md). For network-level operations (service apply, topology provisioning, composites), see [newtron LLD](lld.md).

---

## 1. SSH Tunnel (`pkg/newtron/device/sonic/types.go`)

SONiC devices in the lab run inside QEMU VMs managed by newtlab. Redis listens on `127.0.0.1:6379` inside the VM, but QEMU SLiRP networking does not forward port 6379. The SSH tunnel solves this by forwarding a random local port through SSH to the in-VM Redis.

**Consumer note:** newtrun's `Runner.connectDevices()` calls `Node.Connect()` which delegates to `sonic.Device.Connect()` (§5.1), creating an SSH tunnel per device using the newtlab-allocated `SSHPort`. All Redis clients (CONFIG_DB, STATE_DB, APP_DB, ASIC_DB) then multiplex over this single tunnel.

### 1.1 When Tunnels Are Used

| Scenario | SSH Tunnel | Direct Redis |
|----------|-----------|--------------|
| Lab E2E tests (SONiC-VS in QEMU) | Yes - port 6379 not forwarded | No |
| Integration tests (standalone Redis) | No | Yes - Redis exposed directly |
| Production (if ever) | Would use proper auth | N/A |

The decision is made in `sonic.Device.Connect()` (§5.1) based on the presence of `SSHUser` and `SSHPass` in the resolved profile. When these fields are empty, a direct `<mgmt_ip>:6379` connection is used. This allows integration tests to run against a standalone Redis instance without SSH.

### 1.2 SSHTunnel Implementation

```go
// SSHTunnel forwards a local TCP port to a remote address through an SSH connection.
// Used to access Redis (127.0.0.1:6379) inside SONiC containers via SSH,
// since Redis has no authentication and port 6379 is not forwarded by QEMU.
type SSHTunnel struct {
    localAddr string         // "127.0.0.1:<port>"
    sshClient *ssh.Client
    listener  net.Listener
    done      chan struct{}
    wg        sync.WaitGroup
}

// NewSSHTunnel dials SSH on host:port and opens a local listener on a random port.
// Connections to the local port are forwarded to 127.0.0.1:6379 inside the SSH host.
// If port is 0, defaults to 22.
func NewSSHTunnel(host, user, pass string, port int) (*SSHTunnel, error)

// LocalAddr returns the local address (e.g. "127.0.0.1:54321") that forwards
// to Redis inside the SSH host.
func (t *SSHTunnel) LocalAddr() string

// Close stops the listener, closes the SSH connection, and waits for
// all forwarding goroutines to finish.
func (t *SSHTunnel) Close() error

// SSHClient returns the underlying ssh.Client for opening command sessions.
// Used by newtrun's verifyPingExecutor and sshCommandExecutor to run commands
// inside the device (e.g., "ping", "show interfaces status") via ssh.Session.
func (t *SSHTunnel) SSHClient() *ssh.Client { return t.sshClient }

// ExecCommand runs a command on the device via SSH session and returns the output.
// Used internally by SaveConfig().
// For arbitrary command execution, newtrun uses SSHClient() directly.
func (t *SSHTunnel) ExecCommand(cmd string) (string, error)
```

**How it works:**

1. `ssh.Dial("tcp", fmt.Sprintf("%s:%d", host, port), config)` establishes the SSH connection with password auth
2. `net.Listen("tcp", "127.0.0.1:0")` opens a local listener on a random available port
3. A background goroutine (`acceptLoop`) accepts incoming local connections
4. Each accepted connection is forwarded via `sshClient.Dial("tcp", "127.0.0.1:6379")`
5. Bidirectional `io.Copy` relays data between the local and remote connections
6. `Close()` signals the done channel, closes the listener, waits for goroutines, then closes SSH

**Security note:** `HostKeyCallback: ssh.InsecureIgnoreHostKey()` is used because this is a lab/test environment only. SONiC-VS VMs regenerate host keys on each boot.

**Timeouts:**
- Dial timeout: 30 seconds (`ssh.ClientConfig.Timeout`)

**Reconnection policy:** No automatic reconnection. If the SSH tunnel or any Redis client disconnects, the caller must call `Disconnect()` and then `Connect()` again. This is a deliberate simplicity choice — reconnection with state recovery adds complexity that isn't needed for lab/test workloads where a connection drop typically means the VM crashed.

```go
func NewSSHTunnel(host, user, pass string, port int) (*SSHTunnel, error) {
    if port == 0 {
        port = 22
    }
    config := &ssh.ClientConfig{
        User: user,
        Auth: []ssh.AuthMethod{
            ssh.Password(pass),
        },
        HostKeyCallback: ssh.InsecureIgnoreHostKey(),
        Timeout:         30 * time.Second, // dial timeout
    }

    sshClient, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", host, port), config)
    if err != nil {
        return nil, fmt.Errorf("SSH dial %s: %w", host, err)
    }

    listener, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        sshClient.Close()
        return nil, fmt.Errorf("local listen: %w", err)
    }

    t := &SSHTunnel{
        localAddr: listener.Addr().String(),
        sshClient: sshClient,
        listener:  listener,
        done:      make(chan struct{}),
    }

    t.wg.Add(1)
    go t.acceptLoop()

    return t, nil
}

func (t *SSHTunnel) forward(local net.Conn) {
    defer t.wg.Done()
    defer local.Close()

    remote, err := t.sshClient.Dial("tcp", "127.0.0.1:6379")
    if err != nil {
        return
    }
    defer remote.Close()

    done := make(chan struct{}, 2)
    go func() { io.Copy(remote, local); done <- struct{}{} }()
    go func() { io.Copy(local, remote); done <- struct{}{} }()
    <-done
}
```

---

## 2. StateDB (`pkg/newtron/device/sonic/statedb.go`)

STATE_DB (Redis DB 6) contains the operational/runtime state of the device, separate from configuration. Where CONFIG_DB represents what you asked for, STATE_DB represents what the system is actually doing.

**Consumer note:** newtrun's `verifyStateDBExecutor` reads STATE_DB tables via `StateDBClient.Get*()` methods, polling with timeout until expected values appear. `verifyBGPExecutor` reads `BGPNeighborTable` from STATE_DB to check BGP session state.

### 2.1 StateDB Struct

```go
// StateDB mirrors SONiC's state_db structure (Redis DB 6)
type StateDB struct {
    PortTable         map[string]PortStateEntry         `json:"PORT_TABLE,omitempty"`
    LAGTable          map[string]LAGStateEntry          `json:"LAG_TABLE,omitempty"`
    LAGMemberTable    map[string]LAGMemberStateEntry    `json:"LAG_MEMBER_TABLE,omitempty"`
    VLANTable         map[string]VLANStateEntry         `json:"VLAN_TABLE,omitempty"`
    VRFTable          map[string]VRFStateEntry          `json:"VRF_TABLE,omitempty"`
    VXLANTunnelTable  map[string]VXLANTunnelStateEntry  `json:"VXLAN_TUNNEL_TABLE,omitempty"`
    BGPNeighborTable  map[string]BGPNeighborStateEntry  `json:"BGP_NEIGHBOR_TABLE,omitempty"`
    InterfaceTable    map[string]InterfaceStateEntry    `json:"INTERFACE_TABLE,omitempty"`
    NeighTable        map[string]NeighStateEntry        `json:"NEIGH_TABLE,omitempty"`
    FDBTable          map[string]FDBStateEntry          `json:"FDB_TABLE,omitempty"`
    RouteTable        map[string]RouteStateEntry        `json:"ROUTE_TABLE,omitempty"`
    TransceiverInfo   map[string]TransceiverInfoEntry   `json:"TRANSCEIVER_INFO,omitempty"`
    TransceiverStatus map[string]TransceiverStatusEntry `json:"TRANSCEIVER_STATUS,omitempty"`
}
```

**STATE_DB key formats:**

| Table | Key Pattern | Example |
|-------|------------|---------|
| `PORT_TABLE` | `PORT_TABLE\|<port>` | `PORT_TABLE\|Ethernet0` |
| `LAG_TABLE` | `LAG_TABLE\|<lag>` | `LAG_TABLE\|PortChannel100` |
| `LAG_MEMBER_TABLE` | `LAG_MEMBER_TABLE\|<lag>\|<member>` | `LAG_MEMBER_TABLE\|PortChannel100\|Ethernet0` |
| `VLAN_TABLE` | `VLAN_TABLE\|<vlan>` | `VLAN_TABLE\|Vlan100` |
| `VRF_TABLE` | `VRF_TABLE\|<vrf>` | `VRF_TABLE\|Vrf_CUST1` |
| `VXLAN_TUNNEL_TABLE` | `VXLAN_TUNNEL_TABLE\|<name>` | `VXLAN_TUNNEL_TABLE\|vtep1` |
| `BGP_NEIGHBOR_TABLE` | `BGP_NEIGHBOR_TABLE\|<vrf>\|<neighbor>` | `BGP_NEIGHBOR_TABLE\|default\|10.0.0.2` |
| `INTERFACE_TABLE` | `INTERFACE_TABLE\|<intf>` | `INTERFACE_TABLE\|Ethernet0` |
| `NEIGH_TABLE` | `NEIGH_TABLE\|<intf>\|<ip>` | `NEIGH_TABLE\|Ethernet0\|10.0.0.2` |
| `FDB_TABLE` | `FDB_TABLE\|<vlan>\|<mac>` | `FDB_TABLE\|Vlan100\|00:11:22:33:44:55` |
| `ROUTE_TABLE` | `ROUTE_TABLE\|<vrf>\|<prefix>` | `ROUTE_TABLE\|default\|10.1.0.0/31` |
| `TRANSCEIVER_INFO` | `TRANSCEIVER_INFO\|<port>` | `TRANSCEIVER_INFO\|Ethernet0` |
| `TRANSCEIVER_STATUS` | `TRANSCEIVER_STATUS\|<port>` | `TRANSCEIVER_STATUS\|Ethernet0` |
| `NEWTRON_LOCK` | `NEWTRON_LOCK\|<device>` | `NEWTRON_LOCK\|spine1` |

### 2.2 State Entry Types

```go
type PortStateEntry struct {
    AdminStatus  string `json:"admin_status,omitempty"`
    OperStatus   string `json:"oper_status,omitempty"`
    Speed        string `json:"speed,omitempty"`
    MTU          string `json:"mtu,omitempty"`
    LinkTraining string `json:"link_training,omitempty"`
}

type LAGStateEntry struct {
    OperStatus string `json:"oper_status,omitempty"`
    Speed      string `json:"speed,omitempty"`
    MTU        string `json:"mtu,omitempty"`
}

type LAGMemberStateEntry struct {
    OperStatus     string `json:"oper_status,omitempty"`
    CollectingDist string `json:"collecting_distributing,omitempty"`
    Selected       string `json:"selected,omitempty"`
    ActorPortNum   string `json:"actor_port_num,omitempty"`
    PartnerPortNum string `json:"partner_port_num,omitempty"`
}

type BGPNeighborStateEntry struct {
    State           string `json:"state,omitempty"`
    RemoteAS        string `json:"remote_asn,omitempty"`
    LocalAS         string `json:"local_asn,omitempty"`
    PeerGroup       string `json:"peer_group,omitempty"`
    PfxRcvd         string `json:"prefixes_received,omitempty"`
    PfxSent         string `json:"prefixes_sent,omitempty"`
    MsgRcvd         string `json:"msg_rcvd,omitempty"`
    MsgSent         string `json:"msg_sent,omitempty"`
    Uptime          string `json:"uptime,omitempty"`
    HoldTime        string `json:"holdtime,omitempty"`
    KeepaliveTime   string `json:"keepalive,omitempty"`
    ConnectRetry    string `json:"connect_retry,omitempty"`
    LastResetReason string `json:"last_reset_reason,omitempty"`
}

type VXLANTunnelStateEntry struct {
    SrcIP      string `json:"src_ip,omitempty"`
    OperStatus string `json:"operstatus,omitempty"`
}

type FDBStateEntry struct {
    Port       string `json:"port,omitempty"`
    Type       string `json:"type,omitempty"`
    VNI        string `json:"vni,omitempty"`
    RemoteVTEP string `json:"remote_vtep,omitempty"`
}

type RouteStateEntry struct {
    NextHop   string `json:"nexthop,omitempty"`
    Interface string `json:"ifname,omitempty"`
    Protocol  string `json:"protocol,omitempty"`
}

type TransceiverInfoEntry struct {
    Vendor          string `json:"vendor_name,omitempty"`
    Model           string `json:"model,omitempty"`
    SerialNum       string `json:"serial_num,omitempty"`
    HardwareVersion string `json:"hardware_version,omitempty"`
    Type            string `json:"type,omitempty"`
    MediaInterface  string `json:"media_interface,omitempty"`
}

type VLANStateEntry struct {
    OperStatus string `json:"oper_status,omitempty"`
    State      string `json:"state,omitempty"`
}

type VRFStateEntry struct {
    State string `json:"state,omitempty"`
}

type InterfaceStateEntry struct {
    VRF      string `json:"vrf,omitempty"`
    ProxyArp string `json:"proxy_arp,omitempty"`
}

type NeighStateEntry struct {
    Family string `json:"family,omitempty"`
    MAC    string `json:"neigh,omitempty"`
    State  string `json:"state,omitempty"`
}

type TransceiverStatusEntry struct {
    Present     string `json:"present,omitempty"`
    Temperature string `json:"temperature,omitempty"`
    Voltage     string `json:"voltage,omitempty"`
    TxPower     string `json:"tx_power,omitempty"`
    RxPower     string `json:"rx_power,omitempty"`
}
```

### 2.3 StateDBClient

```go
// StateDBClient wraps Redis client for state_db access (DB 6)
type StateDBClient struct {
    client *redis.Client
    ctx    context.Context
}

func NewStateDBClient(addr string) *StateDBClient
func (c *StateDBClient) Connect() error
func (c *StateDBClient) Close() error
func (c *StateDBClient) GetAll() (*StateDB, error)
func (c *StateDBClient) GetPortState(name string) (*PortStateEntry, error)
func (c *StateDBClient) GetLAGState(name string) (*LAGStateEntry, error)
func (c *StateDBClient) GetLAGMemberState(lag, member string) (*LAGMemberStateEntry, error)
func (c *StateDBClient) GetBGPNeighborState(vrf, neighbor string) (*BGPNeighborStateEntry, error)
func (c *StateDBClient) GetVXLANTunnelState(name string) (*VXLANTunnelStateEntry, error)
func (c *StateDBClient) GetRemoteVTEPs() ([]string, error)
func (c *StateDBClient) GetRouteCount(vrf string) (int, error)
func (c *StateDBClient) GetFDBCount(vlan int) (int, error)
func (c *StateDBClient) GetTransceiverInfo(port string) (*TransceiverInfoEntry, error)
func (c *StateDBClient) GetTransceiverStatus(port string) (*TransceiverStatusEntry, error)

// GetEntry reads a single STATE_DB entry as raw map[string]string.
// Returns (nil, nil) if the entry does not exist.
// Used by newtrun's verifyStateDBExecutor for generic table/key/field assertions.
func (c *StateDBClient) GetEntry(table, key string) (map[string]string, error)

// AcquireLock atomically sets NEWTRON_LOCK|<device> in STATE_DB.
// Returns ErrDeviceLocked (with current holder) if the key already exists.
//
// Uses a Lua script for atomic check-and-set with expiry:
//
//   -- Acquire lock atomically: set fields only if key does not exist
//   if redis.call("EXISTS", KEYS[1]) == 0 then
//       redis.call("HSET", KEYS[1], "holder", ARGV[1], "acquired", ARGV[2], "ttl", ARGV[3])
//       redis.call("EXPIRE", KEYS[1], tonumber(ARGV[3]))
//       return 1
//   else
//       return 0
//   end
//
// EXPIRE provides automatic stale lock cleanup if the client crashes.
func (c *StateDBClient) AcquireLock(device, holder string, ttlSeconds int) error

// ReleaseLock deletes NEWTRON_LOCK|<device> from STATE_DB.
// Only deletes if the current holder matches (atomic via Lua script
// to prevent releasing another holder's lock).
//
//   if redis.call("HGET", KEYS[1], "holder") == ARGV[1] then
//       return redis.call("DEL", KEYS[1])
//   else
//       return 0
//   end
func (c *StateDBClient) ReleaseLock(device, holder string) error

// GetLockHolder reads NEWTRON_LOCK|<device> from STATE_DB.
// Returns holder string and acquired time, or ("", zero) if no lock exists.
func (c *StateDBClient) GetLockHolder(device string) (holder string, acquired time.Time, err error)
```

**NEWTRON_LOCK entry format** (Redis hash in STATE_DB):

```
NEWTRON_LOCK|spine1
  holder:   "aldrin@workstation1"
  acquired: "2026-02-07T16:00:00Z"
  ttl:      "3600"
```

The key has a Redis EXPIRE set to `ttl` seconds, so it auto-deletes if the holder crashes.

**Lock delegation from sonic.Device (newtron LLD §3.3):**

`sonic.Device.Lock(holder, ttl)` stores the holder string in `d.lockHolder` and calls `d.stateClient.AcquireLock(d.Name, holder, ttl)`. `sonic.Device.Unlock()` reads `d.lockHolder` and calls `d.stateClient.ReleaseLock(d.Name, d.lockHolder)` — the caller does not need to pass the holder string again. The `node.Node.Lock()` wrapper (newtron LLD §3.2) constructs the holder string as `"user@hostname"` and delegates to `n.conn.Lock(holder, ttl)`.

**CONFIG_DB cache refresh on Lock (HLD §4.10):**

`node.Node.Lock()` refreshes the CONFIG_DB cache immediately after acquiring the distributed lock. This starts a **write episode**: precondition checks within the subsequent `ExecuteOp` `fn()` read from this fresh snapshot. If `GetAll()` fails after lock acquisition, the lock is released and an error is returned — operating with a stale cache under lock is worse than failing the operation.

**ExecuteOp write episode lifecycle:**

`Lock()` (refresh) → `fn()` (precondition reads from cache) → `Apply()` (writes to Redis, no reload) → `Unlock()` (episode ends). No post-Apply refresh — the next episode will refresh itself.

Note: `ExecuteOp` does not call `Verify()` — it is a low-level building block. The CLI's `executeAndSave` wrapper adds Verify between Apply and Save. The newtrun framework has its own explicit `verify-provisioning` step that calls `cs.Verify()` separately.

**RunHealthChecks read-only episode:**

`RunHealthChecks()` calls `Refresh()` at entry to start a fresh read-only episode before reading from the cache for `checkBGP`, `checkInterfaces`, etc.

**Refresh():**

`Refresh()` calls `GetAll()` + rebuilds the interface list. It starts a read-only episode. Used after composite delivery, before health checks, and any other read-only code that needs current CONFIG_DB state.

**Why STATE_DB (DB 6):**
- Locks are operational state, not configuration — they should not persist across `config save -y` or device reboots
- STATE_DB is ephemeral: cleared on reboot, which is correct — a rebooted device has no active sessions
- SONiC daemons do not subscribe to `NEWTRON_LOCK` entries, so no unintended side effects

**Why Lua scripts (not plain SET NX):**
- The lock entry is a Redis hash (multiple fields: holder, acquired, ttl) — `SET NX` only works on string values
- Lua scripts execute atomically on the Redis server — no race window between EXISTS check and HSET
- EXPIRE is set in the same atomic script as HSET, ensuring TTL is always applied
- Release also uses Lua to atomically verify holder before DEL (prevents releasing someone else's lock)

### 2.4 Redis Serialization

Redis hashes store field names and values as strings. SONiC uses these hash field names directly (e.g., `admin_status`, `oper_status`). The Go structs use `json` tags but these map to Redis hash field names via table-driven parsers in `statedb_parsers.go` (15 registered table parsers).

The serialization path is:
1. `HGETALL <table>|<key>` returns `map[string]string` from Redis
2. Each field is assigned to the corresponding struct field via a registered parser function
3. This is done via `statedb_parsers.go` — a registry-driven approach matching the `configdb_parsers.go` pattern

**Why not json.Unmarshal:** Redis hashes are flat `map[string]string`, not JSON objects. There's no JSON to unmarshal. The `json` tags on structs serve double duty: they define both the Redis hash field name and the JSON serialization format (for when structs are marshaled to JSON for display/logging).

---

## 3. APP_DB (`pkg/newtron/device/sonic/appldb.go`)

APP_DB (Redis DB 0) contains application-level state written by SONiC daemons. For route verification, newtron reads `ROUTE_TABLE` entries written by `fpmsyncd` (the FPM-to-Redis daemon that syncs FRR's RIB into APP_DB).

**Consumer note:** newtrun's `verifyRouteExecutor` calls `Device.GetRoute()` (§5.8) to observe routes from APP_DB; it interprets the returned `RouteEntry` (newtron LLD §3.6A) to assert protocol, next-hop, and presence.

### 3.1 AppDB Struct

```go
// AppDBRouteEntry represents a route in APP_DB's ROUTE_TABLE.
// Multi-path (ECMP) routes use comma-separated values in nexthop and ifname.
type AppDBRouteEntry struct {
    NextHop   string `json:"nexthop"`   // "10.0.0.1" or "10.0.0.1,10.0.0.3" (ECMP)
    Interface string `json:"ifname"`    // "Ethernet0" or "Ethernet0,Ethernet4" (ECMP)
    Protocol  string `json:"protocol"`  // "bgp", "connected", "static"
}
```

Key format: `ROUTE_TABLE:<vrf>:<prefix>` — for example:
- `ROUTE_TABLE:default:10.1.0.0/31` — route in the default VRF
- `ROUTE_TABLE:Vrf-customer:192.168.1.0/24` — route in a named VRF

**ECMP convention:** For multi-path routes, `nexthop` and `ifname` are comma-separated with positional correspondence:
```
nexthop:  "10.0.0.1,10.0.0.3"
ifname:   "Ethernet0,Ethernet4"
-> NextHop{IP: "10.0.0.1", Interface: "Ethernet0"}
-> NextHop{IP: "10.0.0.3", Interface: "Ethernet4"}
```

### 3.2 AppDBClient

```go
// AppDBClient wraps Redis client for APP_DB access (DB 0).
type AppDBClient struct {
    client *redis.Client
    ctx    context.Context
}

func NewAppDBClient(addr string) *AppDBClient
func (c *AppDBClient) Connect() error
func (c *AppDBClient) Close() error

// GetRoute reads a single route from ROUTE_TABLE by VRF and prefix.
// Returns nil (not error) if the prefix does not exist.
// Parses comma-separated nexthop/ifname into []NextHop.
// For /32 host routes, retries without the mask if the initial lookup fails.
func (c *AppDBClient) GetRoute(vrf, prefix string) (*RouteEntry, error)
```

---

## 4. ASIC_DB (`pkg/newtron/device/sonic/asicdb.go`)

ASIC_DB (Redis DB 1) contains SAI (Switch Abstraction Interface) objects that represent what is actually programmed in hardware. Reading routes from ASIC_DB confirms that the data plane is programmed, not just the control plane.

**Consumer note:** newtrun's `verifyRouteExecutor` calls `Device.GetRouteASIC()` (§5.8) when `expect.source == "asic_db"` to confirm data-plane programming. See newtrun LLD §7.6.

### 4.1 SAI Object Chain

Unlike APP_DB's flat key-value routes, ASIC_DB stores routes as a chain of SAI objects that must be resolved:

```
Step 1: ASIC_STATE:SAI_OBJECT_TYPE_ROUTE_ENTRY:{"dest":"10.1.0.0/31","vr":"oid:0x3..."}
        -> SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID = "oid:0x5000..."

Step 2: ASIC_STATE:SAI_OBJECT_TYPE_NEXT_HOP_GROUP:oid:0x5000...
        -> SAI_NEXT_HOP_GROUP_MEMBER_LIST (one OID per ECMP path)

Step 3: ASIC_STATE:SAI_OBJECT_TYPE_NEXT_HOP:oid:0x4000...
        -> SAI_NEXT_HOP_ATTR_IP = "10.0.0.1"
        -> SAI_NEXT_HOP_ATTR_ROUTER_INTERFACE_ID = "oid:0x6000..."
```

For single-path routes, step 1 points directly to a `SAI_OBJECT_TYPE_NEXT_HOP` (skipping the group). The client handles both cases via `resolveNextHops()`.

**ASIC_DB key format:** Route entry keys are JSON-encoded with canonical formatting (sorted keys, no whitespace):

```
ASIC_STATE:SAI_OBJECT_TYPE_ROUTE_ENTRY:{"dest":"10.1.0.0/31","switch_id":"oid:0x21...","vr":"oid:0x3..."}
```

**switch_id OID resolution:** The `switch_id` is the same for all routes on a device. On connect, `AsicDBClient` discovers it by scanning for the single `ASIC_STATE:SAI_OBJECT_TYPE_SWITCH:oid:0x...` key and caching the OID. This is set once and reused for all route lookups.

**VR OID resolution:** The `vr` field identifies the Virtual Router (VRF). The resolution algorithm:

1. **Default VRF**: Scan `ASIC_STATE:SAI_OBJECT_TYPE_VIRTUAL_ROUTER:oid:*` keys. The default VR is the one referenced by the switch object's `SAI_SWITCH_ATTR_DEFAULT_VIRTUAL_ROUTER_ID`. Cache this OID on connect.
2. **Named VRF** (e.g., "Vrf_CUST1"): SONiC creates a VR OID for each VRF. To find it:
   - Read the VRF's connected loopback/interface prefix from CONFIG_DB (available via the `ConfigDB` snapshot on `sonic.Device` — e.g., the first IP entry in `INTERFACE|<vrf-member>|<ip>`)
   - Scan `ASIC_STATE:SAI_OBJECT_TYPE_ROUTE_ENTRY:*` keys for a JSON key whose `dest` matches the known connected prefix
   - Extract the `vr` OID from the matching JSON key — this is the VR OID for the named VRF
   - This avoids depending on COUNTERS_DB (DB 2) which newtron does not connect to
3. Cache all discovered VRF → VR OID mappings in `AsicDBClient.vrfOIDs` to avoid repeated scans.

**Note:** APP_DB uses colon (`:`) as the table-key separator (e.g., `ROUTE_TABLE:default:10.1.0.0/31`) while CONFIG_DB and STATE_DB use pipe (`|`). This is a SONiC convention — APP_DB entries are written by producer daemons that follow a different key format from CONFIG_DB's `sonic-cfggen` format.

**Key canonicalization:** When constructing the JSON key for lookup, fields must be sorted alphabetically (`dest`, `switch_id`, `vr`) with no whitespace. This matches SONiC's `syncd` key format.

### 4.2 AsicDBClient

```go
// AsicDBClient wraps Redis client for ASIC_DB access (DB 1).
// More complex than AppDBClient due to SAI OID chain resolution.
type AsicDBClient struct {
    client   *redis.Client
    ctx      context.Context
    switchOID string            // cached switch OID (discovered on Connect)
    defaultVR string            // cached default Virtual Router OID
    vrfOIDs   map[string]string // VRF name → VR OID (populated on demand, cached)
}

func NewAsicDBClient(addr string) *AsicDBClient
func (c *AsicDBClient) Close() error

// Connect establishes the Redis connection and discovers the switch and
// default VR OIDs that are required for all subsequent route lookups.
func (c *AsicDBClient) Connect() error

// ResolveVROID returns the VR OID for a given VRF name. Returns the cached
// value if available; otherwise performs the CONFIG_DB-based discovery described
// in §4.1 (scan ASIC_DB route entries for a known connected prefix in the VRF).
// The configDB parameter provides the CONFIG_DB snapshot for finding a connected
// prefix belonging to the VRF.
func (c *AsicDBClient) ResolveVROID(vrfName string, configDB *ConfigDB) (string, error)

// GetRouteASIC reads a route from ASIC_DB by resolving the SAI object chain:
// SAI_ROUTE_ENTRY -> SAI_NEXT_HOP_GROUP -> SAI_NEXT_HOP.
// Returns nil (not error) if the route is not programmed in ASIC.
// Returns RouteEntry with Source: RouteSourceAsicDB.
// The configDB parameter is needed for VR OID resolution of named VRFs.
func (c *AsicDBClient) GetRouteASIC(vrf, prefix string, configDB *ConfigDB) (*RouteEntry, error)
```

Key scanning uses cursor-based `SCAN` (via `scanKeys()`) to avoid O(N) `KEYS` commands.

---

## 5. Redis Integration

### 5.1 Connection (`pkg/newtron/device/sonic/device.go`)

The connection logic uses SSH tunnels when `SSHUser` and `SSHPass` are present in the resolved profile. When these are absent (e.g., integration tests with standalone Redis), a direct connection is made.

**Consumer note:** newtlab writes `ssh_port` and `mgmt_ip` into device profiles during deployment (newtlab LLD §10). `sonic.Device.Connect()` reads these fields from the resolved profile, so the SSH tunnel targets the correct newtlab-allocated port. newtrun calls `Node.Connect()` (which delegates to `sonic.Device.Connect()`) in `Runner.connectDevices()` after newtlab deploy — see newtrun LLD §4.5.

```go
func (d *Device) Connect(ctx context.Context) error {
    d.mu.Lock()
    defer d.mu.Unlock()

    if d.connected {
        return nil
    }

    var addr string
    if d.Profile.SSHUser != "" && d.Profile.SSHPass != "" {
        tun, err := NewSSHTunnel(d.Profile.MgmtIP, d.Profile.SSHUser, d.Profile.SSHPass, d.Profile.SSHPort)
        if err != nil {
            return fmt.Errorf("SSH tunnel to %s: %w", d.Name, err)
        }
        d.tunnel = tun
        addr = tun.LocalAddr()
    } else {
        addr = fmt.Sprintf("%s:6379", d.Profile.MgmtIP)
    }

    // Connect to CONFIG_DB (DB 4)
    d.client = NewConfigDBClient(addr)
    if err := d.client.Connect(); err != nil {
        return fmt.Errorf("connecting to config_db on %s: %w", d.Name, err)
    }

    // Load config_db
    var err error
    d.ConfigDB, err = d.client.GetAll()
    if err != nil {
        d.client.Close()
        return fmt.Errorf("loading config_db from %s: %w", d.Name, err)
    }

    // Connect to STATE_DB (DB 6)
    d.stateClient = NewStateDBClient(addr)
    if err := d.stateClient.Connect(); err != nil {
        util.WithDevice(d.Name).Warnf("Failed to connect to state_db: %v", err)
    } else {
        d.StateDB, err = d.stateClient.GetAll()
        if err != nil {
            util.WithDevice(d.Name).Warnf("Failed to load state_db: %v", err)
        }
    }

    // Connect APP_DB and ASIC_DB clients for verification
    d.applClient = NewAppDBClient(addr)
    if err := d.applClient.Connect(); err != nil {
        d.applClient = nil
    }

    d.asicClient = NewAsicDBClient(addr)
    if err := d.asicClient.Connect(); err != nil {
        d.asicClient = nil
    }

    d.connected = true
    return nil
}
```

**Key points:**
- All Redis clients share the same address (same tunnel)
- STATE_DB, APP_DB, and ASIC_DB failure is non-fatal: the device remains usable for config operations
- The `ConfigDB` and `StateDB` snapshots are loaded in full on connect
- APP_DB and ASIC_DB clients connect but do not bulk-load — routes are read on demand via `GetRoute`/`GetRouteASIC`
- A single SSH tunnel multiplexes DB 0, DB 1, DB 4, and DB 6 connections

### 5.2 Writing Changes

`ApplyChanges` is a **pure write** — it writes entries to Redis and returns. It does not reload the CONFIG_DB cache afterward. Cache refresh is the caller's responsibility: `Lock()` refreshes at the start of each write episode, and `Refresh()` is available for read-only episodes. See HLD §4.10 for the episode model.

```go
// ApplyChanges writes a set of changes to config_db via Redis.
// Pure write — does not reload CONFIG_DB cache. Cache refresh is the
// caller's responsibility via Lock() or Refresh().
func (d *Device) ApplyChanges(changes []ConfigChange) error
```

The method requires the device to be connected and locked. It iterates over changes, calling `client.Set()` for add/modify and `client.Delete()` for delete operations.

### 5.3 Disconnect with Tunnel Cleanup

```go
func (d *Device) Disconnect() error
```

Disconnect tears down in order:
1. Release device lock if held (via private `unlock()` to avoid mutex deadlock)
2. Close ConfigDBClient
3. Close StateDBClient
4. Close AppDBClient
5. Close AsicDBClient
6. Close SSHTunnel (if present): stops accept loop, waits for goroutines, closes SSH

### 5.4 Config Persistence

The `SaveConfig` method executes shell commands over the SSH tunnel to persist the running CONFIG_DB to disk.

```go
// SaveConfig persists the running CONFIG_DB to disk by running:
//   sudo config save -y
// Used after incremental changes to ensure they survive a reboot.
func (d *Device) SaveConfig(ctx context.Context) error
```

This method uses `SSHTunnel.ExecCommand()` internally.

### 5.5 SONiC Redis Database Layout

SONiC uses multiple Redis databases within a single Redis instance:

| DB | Name | Purpose | Newtron Access |
|----|------|---------|----------------|
| 0 | APPL_DB | Application state (routes, neighbors) | **Read** (GetRoute — routing state observation) |
| 1 | ASIC_DB | ASIC-programmed state (SAI objects) | **Read** (GetRouteASIC — ASIC route verification) |
| 2 | COUNTERS_DB | Interface/port counters | Not used |
| 3 | LOGLEVEL_DB | Logging configuration | Not used |
| 4 | CONFIG_DB | Configuration (ports, VLANs, BGP, etc.) | **Read/Write** |
| 5 | FLEX_COUNTER_DB | Flexible counters | Not used |
| 6 | STATE_DB | Operational state (oper_status, BGP state) | **Read** + distributed lock |

### 5.6 Verification Methods

These methods expose DB 0 and DB 1 reads at the `Device` level. They are observation primitives — they return structured data, not pass/fail verdicts. Orchestrators (newtrun) decide correctness.

**Single-shot reads:** Both `GetRoute` and `GetRouteASIC` perform a single Redis read and return immediately. They do not poll or retry. If the caller needs to wait for route convergence (e.g., waiting for BGP to install a route), the caller must implement its own polling loop with timeout. newtrun's `verifyRouteExecutor` (newtrun LLD §7.6) does exactly this.

```go
// GetRoute reads a route from APP_DB (Redis DB 0) via the AppDBClient.
// Parses the comma-separated nexthop/ifname fields into []NextHop.
// Returns nil RouteEntry (not error) if the prefix is not present.
func (d *Device) GetRoute(ctx context.Context, vrf, prefix string) (*RouteEntry, error)

// GetRouteASIC reads a route from ASIC_DB (Redis DB 1) by resolving the SAI
// object chain: SAI_ROUTE_ENTRY -> SAI_NEXT_HOP_GROUP -> SAI_NEXT_HOP.
// Returns nil RouteEntry (not error) if not programmed in ASIC.
func (d *Device) GetRouteASIC(ctx context.Context, vrf, prefix string) (*RouteEntry, error)

// VerifyChangeSet re-reads CONFIG_DB through a fresh ConfigDBClient connection
// and confirms every entry in the ChangeSet was applied correctly.
// Uses superset matching for ADD/MODIFY (actual may have more fields than expected)
// and absence checking for DELETE.
// Returns VerificationResult with pass count, fail count, and per-entry errors.
func (d *Device) VerifyChangeSet(ctx context.Context, changes []ConfigChange) (*VerificationResult, error)
```

### 5.7 Pipeline Operations

Composite delivery and bulk operations use Redis pipelines for atomicity and performance.

**Redis MULTI/EXEC semantics:**

```
MULTI                           -- start transaction
HSET BGP_GLOBALS|default router_id 10.0.0.1 local_asn 65000
HSET BGP_NEIGHBOR|10.0.0.2 asn 65000 local_addr 10.0.0.1
HSET BGP_NEIGHBOR_AF|10.0.0.2|ipv4_unicast activate true
DEL ROUTE_MAP|OLD_MAP|10
EXEC                            -- execute atomically
```

**Why pipelines:**
- **Atomicity**: Either all changes apply or none do. Prevents partial config states that could cause SONiC daemon issues.
- **Performance**: Single round-trip vs one per entry. A composite with 200 entries takes 1 round-trip instead of 200.
- **Consistency**: SONiC daemons see the complete change set at once via keyspace notifications, rather than processing entries one at a time.

**Error handling:**
- If any command in the pipeline fails, the entire MULTI/EXEC transaction is discarded
- The pipeline returns per-command results; the wrapper checks all results and returns the first error
- On pipeline failure, CONFIG_DB is not reloaded (no changes were applied)

**ReplaceAll for overwrite mode:**
```go
// ReplaceAll merges composite entries on top of existing CONFIG_DB, preserving
// factory defaults. Only stale keys (present in DB but absent from composite)
// are deleted from affected tables. PORT table entries are always merged (never
// deleted). Used by composite overwrite mode.
func (c *ConfigDBClient) ReplaceAll(changes []Entry) error
```

---

## 6. Config Persistence

Redis changes made by newtron are **runtime only**. They take effect immediately because SONiC daemons subscribe to CONFIG_DB changes, but they do not survive a device reboot.

To persist configuration across reboots, the SONiC command `config save -y` must be run inside the VM. This writes the current CONFIG_DB contents to `/etc/sonic/config_db.json`, which is loaded at boot.

**Implications for testing:**

| Test Type | Persistence | Cleanup Strategy |
|-----------|------------|------------------|
| Unit tests | N/A (no Redis) | N/A |
| Integration tests | Ephemeral (standalone Redis) | Fresh Redis per test |
| E2E lab tests | Runtime only (SONiC-VS) | `ResetLabBaseline()` deletes stale keys |

E2E tests rely on ephemeral configuration. The `ResetLabBaseline()` function cleans known stale keys before each test suite run.

**Provisioning flow**: `ProvisionDevice()` performs a best-effort `config reload -y` before composite delivery to restore CONFIG_DB to the saved baseline (clean slate for merge-based ReplaceAll). After delivery, `config save -y` persists the provisioned config so subsequent `config reload` steps re-read it rather than reverting to factory defaults. `ConfigReload()` retries every 5s for up to 90s when SwSS is not ready (common on fresh CiscoVS boot).
