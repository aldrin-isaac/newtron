# Newtron E2E Testing -- Low-Level Design (LLD) v2

### What Changed in v2

| Area | Change |
|------|--------|
| **SSH Tunnel** | Added Section 7: SSHTunnel struct, NewSSHTunnel, forward(), tunnel pool (labTunnelAddr, labSSHConfig, CloseLabTunnels) |
| **QEMU Port Forwarding** | Added Section 6: vrnetlab.py mgmt_tcp_ports list showing port 6379 NOT forwarded; port access method table |
| **ASIC Convergence** | Added Section 12: WaitForASICVLAN (Redis DB 1 polling), convergence table by topology, ResetLabBaseline with 40+ stale keys |
| **TestMain** | Added Section 13: exact InitReport → ResetLabBaseline → m.Run → CloseLabTunnels → WriteReport sequence |
| **Test Utilities** | Expanded Section 9: SSH tunnel pool functions, CONFIG_DB/STATE_DB/ASIC_DB assertions, server helpers |
| **Test ID Allocation** | Added Section 15: VLAN 500-800, PortChannel 200-203, VRF names, L2VNI, subnet ranges per test file |
| **Cleanup Strategy** | Added Section 18: ASIC-dependent ordering (IP→SVI→VNI→member→VLAN→VRF), LabCleanupChanges, raw Redis DEL |
| **Redis Schema** | Added ASIC_DB tables (DB 1, SAI_OBJECT_TYPE_VLAN) and STATE_DB tables (DB 6, BGP_NEIGHBOR_TABLE) |
| **Test Catalog** | Updated Section 14: exact test names, node targets, CONFIG_DB tables per test, dataplane tests with server IPs |
| **Report Generation** | Added Section 9.5: InitReport, WriteReport, Track, TrackComment, SetNode API; PARTIAL detection |
| **Repository Layout** | Added tunnel.go, lab.go, report.go, frr.conf, .lab-state, e2e-report.md; added labgen package |
| **Dependencies** | Added golang.org/x/crypto for SSH; added sshpass to external tools |

**Lines:** ~800 (v1) → 1133 (v2) | All v1 sections preserved and expanded.

---

## 1. Repository Layout

```
newtron/
+-- cmd/labgen/main.go               # Lab generation CLI
+-- configlets/                       # SONiC startup config templates
|   +-- sonic-baseline.json
|   +-- sonic-evpn-spine.json
|   +-- sonic-evpn-leaf.json
|   +-- sonic-evpn.json
|   +-- sonic-acl-copp.json
|   +-- sonic-qos-8q.json
+-- internal/testutil/                # Test helper library
|   +-- testutil.go                   #   Redis helpers, paths, contexts
|   +-- redis.go                      #   Seed/flush/read/write Redis
|   +-- fixtures.go                   #   Device & network fixtures
|   +-- lab.go                        #   Lab discovery, SSH tunnels, assertions, servers
|   +-- report.go                     #   E2E test report generation
+-- pkg/
|   +-- network/                      # Device, Network, Interface types
|   +-- operations/                   # Operation types (VLAN, LAG, ACL, ...)
|   +-- labgen/                       # Lab artifact generation logic
|   +-- device/                       # Low-level device connection
|   |   +-- tunnel.go                 #   SSH tunnel implementation
|   +-- spec/                         # Specification types
|   +-- audit/                        # Audit logging
|   +-- health/                       # Health-check infrastructure
+-- test/e2e/                         # E2E test files
|   +-- main_test.go                  #   TestMain: baseline reset, tunnel cleanup
|   +-- connectivity_test.go
|   +-- operations_test.go
|   +-- multidevice_test.go
|   +-- dataplane_test.go
+-- testlab/
|   +-- setup.sh                      # Lab lifecycle script
|   +-- docker-compose.yml            # Standalone Redis container
|   +-- topologies/
|   |   +-- spine-leaf.yml            # 4-node + 2-server topology
|   |   +-- minimal.yml               # 2-node topology
|   +-- seed/
|   |   +-- configdb.json             # CONFIG_DB seed data (DB 4)
|   |   +-- statedb.json              # STATE_DB seed data (DB 6)
|   +-- images/
|   |   +-- common/vrnetlab.py        # vrnetlab base (QEMU, hostfwd)
|   |   +-- sonic-ngdp/docker/
|   |       +-- backup.sh             # Config backup/restore over SSH
|   |       +-- launch.py             # VM launcher (entrypoint)
|   |       +-- healthcheck.py        # Container healthcheck
|   +-- .generated/                   # Runtime artifacts (gitignored)
|       +-- .lab-state                # Current topology name
|       +-- labgen                    # Built binary
|       +-- <topo>.clab.yml           # Generated containerlab file
|       +-- <node>/config_db.json     # Per-node startup config
|       +-- <node>/frr.conf           # Per-node FRR config
|       +-- specs/
|       |   +-- network.json
|       |   +-- site.json
|       |   +-- platforms.json
|       |   +-- profiles/<node>.json
|       +-- e2e-results.txt           # Test output log
|       +-- e2e-report.md             # Markdown test summary
+-- Makefile
+-- go.mod
```

## 2. Dependencies

From `go.mod` (module: `github.com/newtron-network/newtron`, Go 1.21):

| Dependency | Version | Usage |
|---|---|---|
| `github.com/go-redis/redis/v8` | v8.11.5 | Redis client for CONFIG_DB / STATE_DB / ASIC_DB |
| `github.com/sirupsen/logrus` | v1.9.3 | Structured logging |
| `github.com/spf13/cobra` | v1.8.0 | CLI framework (newtron CLI) |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML parsing (topologies, specs) |
| `golang.org/x/crypto` | (indirect) | SSH client for tunnel.go |

External tools required at runtime:

| Tool | Version | Purpose |
|---|---|---|
| `containerlab` | any recent | Topology deployment and lifecycle |
| `docker` | 20+ | Container runtime |
| `python3` | 3.8+ | JSON/YAML processing in setup.sh |
| `sshpass` | any | SSH authentication for lab setup and baseline reset |

Note: `redis-cli` is NOT needed on the host. All Redis access goes
through SSH tunnels (Go code) or `sshpass + ssh` with `redis-cli` running
inside the VM (shell scripts).

## 3. Build Tags

| Tag | Scope | Activated by |
|---|---|---|
| (none) | Unit tests | `go test ./...` |
| `integration` | Unit + integration (requires Redis) | `go test -tags integration ./...` |
| `e2e` | E2E only (requires running lab) | `go test -tags e2e ./...` |

Files and their tags:

| File | Tag |
|---|---|
| `internal/testutil/testutil.go` | `integration \|\| e2e` |
| `internal/testutil/redis.go` | `integration \|\| e2e` |
| `internal/testutil/fixtures.go` | `integration \|\| e2e` |
| `internal/testutil/lab.go` | `e2e` |
| `internal/testutil/report.go` | `e2e` |
| `test/e2e/*.go` | `e2e` |

## 4. Makefile Targets

```makefile
# Testing
make test                  # go test ./... (unit only)
make test-integration      # Start Redis, seed, run -tags integration, stop Redis
make test-e2e              # go test -tags e2e -v -count=1 -timeout 10m -p 1 ./...
make test-e2e-full         # lab-start -> test-e2e -> lab-stop
make test-all              # test + test-integration

# Redis management (standalone container for integration tests)
make redis-start           # Docker run redis:7-alpine
make redis-stop            # Docker rm -f
make redis-seed            # Load seed/*.json into DB 4 and DB 6
make redis-ip              # Print Redis container IP

# Lab management
make labgen                # go build -o testlab/.generated/labgen ./cmd/labgen/
make lab-start             # labgen + containerlab deploy + wait + patch
make lab-start TOPO=minimal  # Use minimal topology instead of default
make lab-stop              # containerlab destroy --cleanup
make lab-status            # containerlab inspect + SSH-based Redis checks

# Other
make build                 # go build ./cmd/...
make lint                  # golangci-lint run ./...
make clean                 # Remove .generated/, coverage files, test cache
make coverage              # Integration tests with -coverprofile
make coverage-html         # HTML coverage report
```

## 5. Topology File Format

```yaml
name: <topology-name>          # Used for clab prefix and state file

defaults:                       # Inherited by all non-server nodes
  image: <docker-image>
  username: <ssh-user>
  password: <ssh-password>
  platform: <platform-string>
  site: <site-string>
  hwsku: <hardware-sku>
  ntp_server_1: <ip>
  ntp_server_2: <ip>
  syslog_server: <ip>

network:                        # Network-level settings
  as_number: <bgp-asn>
  region: <region-name>

nodes:
  <node-name>:
    role: spine | leaf | server
    loopback_ip: <ip>           # Omitted for servers
    image: <override-image>     # Optional per-node override
    variables:                  # Passed to configlet templates
      <key>: <value>

links:
  - endpoints: ["<node>:<intf>", "<node>:<intf>"]

role_defaults:                  # List of configlets per role
  spine:
    - <configlet-name>
  leaf:
    - <configlet-name>
```

### spine-leaf Topology Detail

```
                 +---------+     +---------+
                 | spine1  |     | spine2  |
                 | 10.0.0.1|     | 10.0.0.2|
                 +-+---+---+     +---+---+-+
          Eth0 ---+   +-- Eth4  Eth0+   +-- Eth4
            |               |    |               |
          Eth0            Eth0  Eth4            Eth4
                 +---------+     +---------+
                 |  leaf1  |     |  leaf2  |
                 |10.0.0.11|     |10.0.0.12|
                 +----+----+     +----+----+
                    Eth8            Eth8
                      |               |
                    eth1            eth1
                 +---------+     +---------+
                 | server1 |     | server2 |
                 |netshoot |     |netshoot |
                 +---------+     +---------+
```

**Configlets applied:**

| Node | Configlets |
|---|---|
| spine1, spine2 | `sonic-baseline`, `sonic-evpn-spine` |
| leaf1, leaf2 | `sonic-baseline`, `sonic-evpn-leaf`, `sonic-acl-copp`, `sonic-qos-8q` |
| server1, server2 | (none -- Linux containers) |

## 6. QEMU Port Forwarding (vrnetlab)

The vrnetlab base class (`testlab/images/common/vrnetlab.py`) configures
QEMU SLiRP user-mode networking with host-forwarded TCP ports:

```python
self.mgmt_tcp_ports = [80, 443, 830, 6030, 8080, 9339, 32767, 50051, 57400]
```

Each port is forwarded as:
```
hostfwd=tcp:0.0.0.0:<port>-10.0.0.15:<port>
```

This makes services on the VM accessible at the container's management IP.
Port 22 (SSH) is handled separately by vrnetlab's base configuration.

**CRITICAL: Port 6379 is NOT in this list.** Redis is NOT port-forwarded.
All Redis access MUST use SSH tunnels through port 22.

**Key ports for testing:**

| Port | Service | Forwarded by QEMU? | Access Method |
|---|---|---|---|
| 22 | SSH | Yes (vrnetlab base) | Direct TCP to mgmt IP |
| 6379 | Redis | **NO** | SSH tunnel through port 22 |
| 80 | HTTP | Yes | Direct TCP to mgmt IP |
| 443 | HTTPS | Yes | Direct TCP to mgmt IP |
| 830 | NETCONF | Yes | Direct TCP to mgmt IP |
| 8080 | gNMI/HTTP API | Yes | Direct TCP to mgmt IP |
| 9339 | gNMI (IANA) | Yes | Direct TCP to mgmt IP |

## 7. SSH Tunnel Implementation

### 7.1 `pkg/device/tunnel.go` -- SSHTunnel

The `SSHTunnel` struct forwards a local TCP port to `127.0.0.1:6379`
inside a SONiC VM via SSH. This is the sole mechanism for Go-based Redis
access to lab nodes.

```go
// SSHTunnel forwards a local TCP port to a remote address through an SSH connection.
// Used to access Redis (127.0.0.1:6379) inside SONiC containers via SSH,
// since Redis has no authentication and port 6379 is not forwarded by QEMU.
type SSHTunnel struct {
    localAddr string // "127.0.0.1:<port>"
    sshClient *ssh.Client
    listener  net.Listener
    done      chan struct{}
    wg        sync.WaitGroup
}
```

**Creation flow:**

```go
func NewSSHTunnel(host, user, pass string, port int) (*SSHTunnel, error) {
    // 1. Dial SSH on host:port (default 22) with password auth
    sshClient, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", host, port), config)

    // 2. Listen on a random local port
    listener, err := net.Listen("tcp", "127.0.0.1:0")

    // 3. Start accept loop in background goroutine
    go t.acceptLoop()
}
```

**Forwarding flow:**

```go
func (t *SSHTunnel) forward(local net.Conn) {
    // Dial 127.0.0.1:6379 through the SSH connection
    remote, err := t.sshClient.Dial("tcp", "127.0.0.1:6379")
    // Bidirectional copy between local and remote
    go io.Copy(remote, local)
    go io.Copy(local, remote)
}
```

**Lifecycle:**

- `NewSSHTunnel(host, user, pass, port)` -- Creates tunnel, returns local address
- `tunnel.LocalAddr()` -- Returns `"127.0.0.1:<port>"` for Redis client
- `tunnel.Close()` -- Closes listener, SSH connection, waits for goroutines

### 7.2 SSH Tunnel Pool (`internal/testutil/lab.go`)

Lab tests share SSH tunnels across all test functions via a pool:

```go
var (
    labTunnelsMu sync.Mutex
    labTunnels   map[string]*device.SSHTunnel  // keyed by node name
)
```

**`labTunnelAddr(t, nodeName, nodeIP)`** -- Returns a Redis address:

1. Reads SSH credentials from the node's profile JSON:
   ```go
   func labSSHConfig(t *testing.T, nodeName string) (user, pass string) {
       profilePath := filepath.Join(testlabDir(), ".generated", "specs",
           "profiles", nodeName+".json")
       // Parse ssh_user and ssh_pass fields
   }
   ```

2. If credentials exist, creates or reuses an SSH tunnel:
   ```go
   if tun, ok := labTunnels[nodeName]; ok {
       return tun.LocalAddr()  // reuse existing tunnel
   }
   tun, err := device.NewSSHTunnel(nodeIP, user, pass, sshPort)
   labTunnels[nodeName] = tun
   return tun.LocalAddr()      // e.g., "127.0.0.1:54321"
   ```

3. If no credentials (should not happen in normal lab), falls back to
   direct `nodeIP:6379` (which will fail since port 6379 is not forwarded).

**`CloseLabTunnels()`** -- Called from `TestMain` after `m.Run()`:

```go
func CloseLabTunnels() {
    labTunnelsMu.Lock()
    defer labTunnelsMu.Unlock()
    for _, tun := range labTunnels {
        tun.Close()
    }
    labTunnels = nil
}
```

## 8. Lab Lifecycle State Machine

```
                   make lab-start [TOPO]
                        |
                        v
               +--------------------+
               |  Build labgen      |  go build ./cmd/labgen/
               +--------+-----------+
                        |
                        v
               +--------------------+
               | Generate artifacts |  labgen -topology <file> -output <dir>
               |                    |  -> config_db.json, clab.yml, specs/, frr.conf
               +--------+-----------+
                        |
                        v
               +--------------------+
               | containerlab       |  containerlab deploy -t <clab.yml> --reconfigure
               | deploy             |  -> creates containers, networks, links
               +--------+-----------+
                        |
                        v
               +--------------------+
               | Wait healthy       |  Poll containerlab inspect for status != "starting"
               | (timeout 5m)       |  Checks only SONiC nodes (skips linux servers)
               +--------+-----------+
                        |
                        v
               +--------------------+
               | Wait Redis         |  SSH into each SONiC node:
               | (timeout 5m)       |  sshpass ssh <user>@<ip> "redis-cli -n 4 PING"
               |                    |  NOT direct TCP to port 6379
               +--------+-----------+
                        |
                        v
               +--------------------+
               | Apply MACs         |  SSH: disable warm restart + restart swss
               | (restart swss)     |  Sleep 30s for reinitialization
               +--------+-----------+
                        |
                        v
               +--------------------+
               | Push FRR config    |  SCP frr.conf to each node
               |                    |  docker exec bgp vtysh -f /etc/frr/lab_frr.conf
               +--------+-----------+
                        |
                        v
               +--------------------+
               | Bridge NICs        |  SSH: tc mirred redirect ethN <-> swvethN
               |                    |  Required for NGDP ASIC data plane
               +--------+-----------+
                        |
                        v
               +--------------------+
               | Patch profiles     |  Inject actual mgmt IPs + SSH credentials
               |                    |  into specs/profiles/<node>.json
               +--------+-----------+
                        |
                        v
                   Lab is ready
                   (.lab-state written)
```

### 8.1 `lab_wait_redis` Implementation

The shell script checks Redis via SSH, not direct TCP:

```bash
lab_wait_redis() {
    local nodes=$(clab_sonic_nodes "${clab_file}")
    local creds=$(clab_ssh_creds "${clab_file}")
    local ssh_user="${creds%% *}"
    local ssh_pass="${creds##* }"

    while IFS=' ' read -r name ip; do
        # Poll via SSH until redis-cli PING returns PONG
        while true; do
            if sshpass -p "$ssh_pass" ssh -o StrictHostKeyChecking=no \
                -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
                "$ssh_user@$ip" "redis-cli -n 4 PING" < /dev/null 2>/dev/null \
                | grep -q PONG; then
                break
            fi
            sleep 5
        done
    done <<< "$nodes"
}
```

### 8.2 `lab_patch_profiles` Implementation

Injects both management IPs and SSH credentials into profile files:

```python
data['mgmt_ip'] = '<ip>'
creds = node_ssh_creds.get('<node_name>', {})
if creds.get('user'):
    data['ssh_user'] = creds['user']
    data['ssh_pass'] = creds['pass']
```

These `ssh_user` and `ssh_pass` fields are later read by `labSSHConfig()`
to create SSH tunnels.

## 9. Test Utility API Reference

### 9.1 `testutil.go` -- Core Helpers

```go
// Redis connection (standalone container, for integration tests)
func RedisAddr() string                         // "host:6379" from env or docker inspect
func RedisIP() string                           // IP portion only
func RedisClient(t *testing.T, db int) *redis.Client

// Skip guards
func SkipIfNoRedis(t *testing.T)                // Skip if Redis unreachable
func RequireRedis(t *testing.T)                 // Fatal if Redis unreachable

// Database operations (standalone Redis)
func FlushAll(t *testing.T)                     // FLUSHDB on DB 4 and DB 6
func KeyCount(t *testing.T, db int) int
func DumpKeys(t *testing.T, db int) []string

// Context
func Context(t *testing.T) context.Context      // 30-second timeout
func ContextWithCancel() (context.Context, context.CancelFunc)

// Paths
func ProjectRoot() string                       // Repository root
func SeedPath(name string) string               // testlab/seed/<name>
func SpecsPath() string                         // testlab/.generated/specs

// Environment
func MustEnv(t *testing.T, key string) string
func WaitForRedis(timeout time.Duration) error
```

### 9.2 `redis.go` -- Redis Seeding

```go
// Bulk operations from JSON seed files
func SeedRedis(t *testing.T, addr string, db int, seedFile string)
func FlushDB(t *testing.T, addr string, db int)
func SetupConfigDB(t *testing.T)                // FlushDB(4) + SeedRedis(4, configdb.json)
func SetupStateDB(t *testing.T)                 // FlushDB(6) + SeedRedis(6, statedb.json)
func SetupBothDBs(t *testing.T)

// Individual entry operations
func WriteSingleEntry(t *testing.T, addr string, db int,
    table, key string, fields map[string]string)
func DeleteEntry(t *testing.T, addr string, db int, table, key string)
func ReadEntry(t *testing.T, addr string, db int,
    table, key string) map[string]string
func EntryExists(t *testing.T, addr string, db int, table, key string) bool
```

**Seed file JSON format:**
```json
{
  "TABLE_NAME": {
    "entry_key": {
      "field1": "value1",
      "field2": "value2"
    }
  }
}
```

Redis key format: `TABLE_NAME|entry_key` stored as a hash with the given
fields.

### 9.3 `fixtures.go` -- Device Fixtures

```go
// Profile creation (for integration tests using standalone Redis)
func TestProfile() *spec.ResolvedProfile

// Device fixtures (connect to standalone Redis container)
func ConnectedDevice(t *testing.T) *device.Device
func LockedDevice(t *testing.T) *device.Device
func ReconnectDevice(t *testing.T, d *device.Device)

// Network fixtures
func TestNetwork(t *testing.T) *network.Network
func ConnectedNetworkDevice(t *testing.T) *network.Device
func LockedNetworkDevice(t *testing.T) *network.Device

// State management
func WithCleanState(t *testing.T)               // Flush + seed both DBs

// Assertions
func AssertNoError(t *testing.T, err error, msg string)
func AssertError(t *testing.T, err error, msg string)
func Must[T any](t *testing.T, val T, err error) T
```

### 9.4 `lab.go` -- E2E Lab Helpers

```go
// Lab discovery
func SkipIfNoLab(t *testing.T)
func LabTopologyName() string                    // Read from env or .lab-state
func LabSpecsDir(t *testing.T) string

// Node types
type LabNode struct {
    Name string   // Short name (e.g., "leaf1")
    IP   string   // Management IP (e.g., "172.17.0.3")
}
func LabNodes(t *testing.T) []LabNode            // ALL nodes (including servers)
func LabSonicNodes(t *testing.T) []LabNode       // SONiC only (has profile file)
func LabNodeNames(t *testing.T) []string
func LabNodeIP(t *testing.T, name string) string

// SSH tunnel pool (used internally by LabRedisClient and device connections)
func labSSHConfig(t, nodeName) (user, pass string)  // unexported
func labTunnelAddr(t, nodeName, nodeIP) string       // unexported
func CloseLabTunnels()                           // Close all; call from TestMain

// Device connections (uses lab profiles with SSH tunnel for Redis)
func LabNetwork(t *testing.T) *network.Network
func LabConnectedDevice(t *testing.T, name string) *network.Device
func TryLabConnectedDevice(t *testing.T, name string) (*network.Device, error)
func LabLockedDevice(t *testing.T, name string) *network.Device
func LabContext(t *testing.T) context.Context    // 2-minute timeout

// CONFIG_DB assertions (all via SSH tunnel)
func LabRedisClient(t *testing.T, name string, db int) *redis.Client
func AssertConfigDBEntry(t *testing.T, name, table, key string,
    expectedFields map[string]string)
func AssertConfigDBEntryExists(t *testing.T, name, table, key string)
func AssertConfigDBEntryAbsent(t *testing.T, name, table, key string)

// STATE_DB access (via SSH tunnel)
func LabStateDBEntry(t *testing.T, name, table, key string) map[string]string
func PollStateDB(ctx context.Context, t *testing.T,
    name, table, key, field, want string) error

// ASIC_DB convergence (via SSH tunnel, DB 1)
func WaitForASICVLAN(ctx context.Context, t *testing.T,
    name string, vlanID int) error

// Baseline reset (SSH + sshpass, called from TestMain)
func ResetLabBaseline() error

// Redis wait (SSH tunnel based)
func WaitForLabRedis(t *testing.T, timeout time.Duration)

// Cleanup
func LabCleanupChanges(t *testing.T, nodeName string,
    fn func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error))

// Server containers (via docker exec, no SSH tunnel needed)
func SkipIfNoServers(t *testing.T, names ...string)
func LabServerNode(t *testing.T, name string) LabNode
func ServerExec(t *testing.T, serverName string, args ...string) (string, error)
func EnsureServerTools(t *testing.T, serverName string)
func ServerConfigureInterface(t *testing.T, serverName, iface, ipCIDR, gateway string)
func ServerPing(t *testing.T, serverName, targetIP string, count int) bool
func ServerCleanupInterface(t *testing.T, serverName, iface string)
```

### 9.5 `report.go` -- E2E Test Report (build tag: `e2e`)

```go
// Lifecycle (called from TestMain)
func InitReport()                                // Create global report; call before m.Run()
func WriteReport(path string) error              // Compute PARTIAL statuses, write markdown

// Per-test tracking (call after SkipIfNoLab)
func Track(t *testing.T, category, node string)  // Register test, start timer, capture outcome
func TrackComment(t *testing.T, msg string)      // Attach comment (call before t.Skip/t.Fatal)
func SetNode(t *testing.T, node string)          // Update node when not known at Track() time
```

**Report output:** `testlab/.generated/e2e-report.md`

**PARTIAL detection:** When a parent test has subtests with mixed outcomes
(e.g., some pass, some skip), `WriteReport` walks the `t.Name()` hierarchy
bottom-up and marks the parent as PARTIAL. Non-passing subtests are listed
in the Comments column.

## 10. Operations Interface

All configuration operations implement:

```go
type Operation interface {
    Validate(ctx context.Context, d *network.Device) error
    Preview(ctx context.Context, d *network.Device) (*ChangeSet, error)
    Execute(ctx context.Context, d *network.Device) error
    Name() string
    Description() string
}
```

### 10.1 Operation Types

**VLAN operations** (`pkg/operations/vlan.go`):

| Struct | Key Fields |
|---|---|
| `CreateVLANOp` | `ID int`, `VLANName string`, `Desc string` |
| `DeleteVLANOp` | `ID int` |
| `AddVLANMemberOp` | `VLANID int`, `Port string`, `Tagged bool` |
| `RemoveVLANMemberOp` | `VLANID int`, `Port string` |
| `ConfigureSVIOp` | `VLANID int`, `VRF string`, `IPAddress string`, `AnycastGateway string` |

**LAG operations** (`pkg/operations/lag.go`):

| Struct | Key Fields |
|---|---|
| `CreateLAGOp` | `LAGName string`, `Members []string`, `MTU int`, `MinLinks int` |
| `DeleteLAGOp` | `LAGName string` |
| `AddLAGMemberOp` | `LAGName string`, `Member string` |
| `RemoveLAGMemberOp` | `LAGName string`, `Member string` |

**Interface operations** (`pkg/operations/interface.go`):

| Struct | Key Fields |
|---|---|
| `ConfigureInterfaceOp` | `Interface string`, `Desc string`, `MTU int` |
| `SetInterfaceVRFOp` | `Interface string`, `VRF string`, `IPAddress string` |
| `SetInterfaceIPOp` | `Interface string`, `IPAddress string` |

**ACL operations** (`pkg/operations/acl.go`):

| Struct | Key Fields |
|---|---|
| `CreateACLTableOp` | `TableName string`, `Type string`, `Stage string`, `Desc string` |
| `DeleteACLTableOp` | `TableName string` |
| `AddACLRuleOp` | `TableName string`, `RuleName string`, `Priority int`, `Action string`, `SrcIP string` |
| `DeleteACLRuleOp` | `TableName string`, `RuleName string` |
| `BindACLOp` | `Interface string`, `ACLName string`, `Direction string` |

**EVPN/VRF operations** (`pkg/operations/evpn.go`):

| Struct | Key Fields |
|---|---|
| `CreateVRFOp` | `VRFName string`, `L3VNI int` |
| `DeleteVRFOp` | `VRFName string` |
| `CreateVTEPOp` | `VTEPName string`, `SourceIP string` |
| `MapL2VNIOp` | `VLANID int`, `VNI int`, `ARPSuppression bool` |
| `UnmapL2VNIOp` | `VLANID int` |

**Service operations** (`pkg/operations/service.go`):

| Struct | Key Fields |
|---|---|
| `ApplyServiceOp` | `Interface string`, `ServiceName string`, `IPAddress string` |
| `RemoveServiceOp` | `Interface string` |

### 10.2 ChangeSet Structure

```go
type ChangeSet struct {
    Device    string
    Operation string
    Changes   []Change
    Timestamp time.Time
    DryRun    bool
}

type Change struct {
    Table     string       // CONFIG_DB table name
    Key       string       // Entry key within the table
    Operation string       // "add", "modify", "delete"
    OldValue  interface{}
    NewValue  interface{}
}
```

## 11. Redis Database Schema

All SONiC CONFIG_DB data lives in Redis DB 4. STATE_DB uses DB 6.
ASIC_DB uses DB 1. All access from the test process goes through SSH
tunnels.

### 11.1 Key Format

```
TABLE_NAME|entry_key
```

Stored as Redis hashes where each field is a config attribute.

### 11.2 Tables Used by E2E Tests

| Table | Key Pattern | Example Fields |
|---|---|---|
| `DEVICE_METADATA` | `localhost` | `hostname`, `hwsku`, `platform`, `mac` |
| `VLAN` | `Vlan<id>` | `vlanid`, `admin_status`, `description` |
| `VLAN_MEMBER` | `Vlan<id>\|<port>` | `tagging_mode` (`tagged`/`untagged`) |
| `VLAN_INTERFACE` | `Vlan<id>` or `Vlan<id>\|<ip/mask>` | `vrf_name` |
| `PORTCHANNEL` | `PortChannel<id>` | `mtu`, `admin_status`, `min_links` |
| `PORTCHANNEL_MEMBER` | `PortChannel<id>\|<port>` | (empty hash) |
| `INTERFACE` | `<port>` or `<port>\|<ip/mask>` | `vrf_name` |
| `LOOPBACK_INTERFACE` | `Loopback0` or `Loopback0\|<ip/mask>` | (empty hash) |
| `ACL_TABLE` | `<name>` | `type`, `stage`, `policy_desc`, `ports` |
| `ACL_RULE` | `<table>\|<rule>` | `priority`, `packet_action`, `src_ip` |
| `VRF` | `<name>` | `vrf_reg_mask` |
| `VXLAN_TUNNEL` | `<vtep>` | `src_ip` |
| `VXLAN_EVPN_NVO` | `<nvo>` | `source_vtep` |
| `VXLAN_TUNNEL_MAP` | `<vtep>\|map_<vni>_Vlan<id>` | `vlan`, `vni` |
| `SUPPRESS_VLAN_NEIGH` | `Vlan<id>` | `suppress` |
| `BGP_GLOBALS` | `default` | `local_asn`, `router_id` |
| `BGP_NEIGHBOR` | `<ip>` | `asn`, `name`, `admin_status` |
| `WARM_RESTART` | `swss` | `enable` |

### 11.3 STATE_DB Tables (DB 6)

| Table | Key Pattern | Example Fields |
|---|---|---|
| `BGP_NEIGHBOR_TABLE` | `<ip>` | `state` (`Established`, `Active`, ...) |

### 11.4 ASIC_DB Tables (DB 1)

| Table | Key Pattern | Example Fields |
|---|---|---|
| `ASIC_STATE:SAI_OBJECT_TYPE_VLAN` | `oid:0x...` | `SAI_VLAN_ATTR_VLAN_ID` |

Used by `WaitForASICVLAN()` to verify orchagent convergence.

## 12. ASIC Convergence Verification

### 12.1 `WaitForASICVLAN()`

Polls ASIC_DB (Redis DB 1) for a `SAI_OBJECT_TYPE_VLAN` entry with a
matching `SAI_VLAN_ATTR_VLAN_ID`. Returns nil on convergence, error on
context timeout.

```go
func WaitForASICVLAN(ctx context.Context, t *testing.T, name string, vlanID int) error {
    client := LabRedisClient(t, name, 1) // ASIC_DB via SSH tunnel
    want := fmt.Sprintf("%d", vlanID)

    for {
        select {
        case <-ctx.Done():
            return fmt.Errorf("timeout waiting for VLAN %d in ASIC_DB on %s", vlanID, name)
        default:
        }
        keys, err := client.Keys(ctx, "ASIC_STATE:SAI_OBJECT_TYPE_VLAN:*").Result()
        if err == nil {
            for _, key := range keys {
                vid, _ := client.HGet(ctx, key, "SAI_VLAN_ATTR_VLAN_ID").Result()
                if vid == want {
                    return nil
                }
            }
        }
        time.Sleep(1 * time.Second)
    }
}
```

Usage:
```go
asicCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()
err := testutil.WaitForASICVLAN(asicCtx, t, nodeName, 700)
```

**Convergence behavior by topology:**

| Topology | Converges on VS? | Recommended handling |
|---|---|---|
| Simple VLAN | Yes (< 5s) | Hard fail on timeout |
| VLAN + members | Yes (< 10s) | Hard fail on timeout |
| VRF + SVI + VNI (IRB) | Often not | Soft fail (`t.Skip`) |

### 12.2 `ResetLabBaseline()` Implementation

Called from `TestMain` before `m.Run()`. Deletes known stale CONFIG_DB
keys from all SONiC nodes to prevent orchagent/vxlanmgrd crashes.

**Implementation details:**

1. Discovers nodes via `containerlab inspect` (JSON output).
2. Reads SSH credentials from each node's profile JSON file.
3. Builds a `redis-cli -n 4 DEL '<key>'` command string for all
   `staleE2EKeys` entries, joined by `&&`.
4. Executes the command on each node in parallel via `sshpass + ssh`.
5. Sleeps 5 seconds for orchagent to process deletions.

**Complete stale key list** (from `lab.go`):

```go
var staleE2EKeys = []string{
    // DataPlane_L2Bridged
    "VXLAN_TUNNEL_MAP|vtep1|map_10700_Vlan700",
    "VLAN_MEMBER|Vlan700|Ethernet2",
    "VLAN|Vlan700",
    // DataPlane_IRBSymmetric
    "VLAN_INTERFACE|Vlan800|10.80.0.1/24",
    "VLAN_INTERFACE|Vlan800",
    "VXLAN_TUNNEL_MAP|vtep1|map_10800_Vlan800",
    "VLAN_MEMBER|Vlan800|Ethernet2",
    "VLAN|Vlan800",
    "VRF|Vrf_e2e_irb",
    // DataPlane_L3Routed
    "INTERFACE|Ethernet2|10.90.1.1/30",
    "INTERFACE|Ethernet2|10.90.2.1/30",
    "INTERFACE|Ethernet2",
    "VRF|Vrf_e2e_l3",
    // Operations: VLAN tests
    "VLAN|Vlan500", "VLAN|Vlan501", "VLAN|Vlan502", "VLAN|Vlan503",
    "VLAN_MEMBER|Vlan502|Ethernet2", "VLAN_MEMBER|Vlan503|Ethernet2",
    // Operations: SVI test
    "VLAN_INTERFACE|Vlan504|10.99.1.1/24", "VLAN_INTERFACE|Vlan504",
    "VLAN|Vlan504", "VRF|Vrf_e2e_svi",
    // Operations: EVPN tests
    "VXLAN_TUNNEL_MAP|vtep1|map_10505_Vlan505", "SUPPRESS_VLAN_NEIGH|Vlan505",
    "VLAN|Vlan505",
    "VXLAN_TUNNEL_MAP|vtep1|map_10506_Vlan506", "SUPPRESS_VLAN_NEIGH|Vlan506",
    "VLAN|Vlan506",
    "VXLAN_TUNNEL_MAP|vtep1|map_99999_Vrf_e2e_test", "VRF|Vrf_e2e_test",
    "VRF|Vrf_e2e_delete",
    // Operations: VRF/interface tests
    "VRF|Vrf_e2e_iface", "VRF|Vrf_e2e_l3",
    // Operations: ACL tests
    "ACL_RULE|E2E_RULE_ACL|RULE_200", "ACL_TABLE|E2E_RULE_ACL",
    "ACL_RULE|E2E_DELRULE_ACL|RULE_300", "ACL_TABLE|E2E_DELRULE_ACL",
    "ACL_TABLE|E2E_TEST_ACL", "ACL_TABLE|E2E_DELTABLE_ACL",
    "ACL_TABLE|E2E_BIND_ACL",
    // Operations: VTEP test (on spine)
    "VXLAN_EVPN_NVO|nvo1", "VXLAN_TUNNEL|e2e_vtep",
    // Operations: LAG tests
    "PORTCHANNEL_MEMBER|PortChannel200|Ethernet2", "PORTCHANNEL|PortChannel200",
    "PORTCHANNEL_MEMBER|PortChannel201|Ethernet2", "PORTCHANNEL|PortChannel201",
    "PORTCHANNEL_MEMBER|PortChannel202|Ethernet2", "PORTCHANNEL|PortChannel202",
    "PORTCHANNEL_MEMBER|PortChannel203|Ethernet2", "PORTCHANNEL|PortChannel203",
}
```

### 12.3 `CloseLabTunnels()` Implementation

Called from `TestMain` after `m.Run()`. Closes all shared SSH tunnels:

```go
func CloseLabTunnels() {
    labTunnelsMu.Lock()
    defer labTunnelsMu.Unlock()
    for _, tun := range labTunnels {
        tun.Close()  // closes listener + SSH client + waits for goroutines
    }
    labTunnels = nil
}
```

## 13. TestMain Implementation

```go
func TestMain(m *testing.M) {
    testutil.InitReport()

    // Reset all SONiC nodes to baseline config before running tests.
    fmt.Fprintf(os.Stderr, "Resetting lab to baseline config...\n")
    if err := testutil.ResetLabBaseline(); err != nil {
        fmt.Fprintf(os.Stderr, "WARNING: baseline reset: %v\n", err)
    }

    code := m.Run()

    testutil.CloseLabTunnels()

    reportPath := filepath.Join(testutil.ProjectRoot(),
        "testlab", ".generated", "e2e-report.md")
    if err := testutil.WriteReport(reportPath); err != nil {
        fmt.Fprintf(os.Stderr, "WARNING: failed to write E2E report: %v\n", err)
    }

    os.Exit(code)
}
```

**Execution order:**

1. `InitReport()` -- prepare report tracking
2. `ResetLabBaseline()` -- SSH into all nodes, delete stale CONFIG_DB keys
3. `m.Run()` -- execute all test functions (baseline is clean)
4. `CloseLabTunnels()` -- close all pooled SSH tunnels
5. `WriteReport()` -- generate markdown report
6. `os.Exit(code)` -- exit with test result code

## 14. E2E Test Catalog

### 14.1 connectivity_test.go

| Test | Nodes | Verifies |
|---|---|---|
| `TestE2E_ConnectAllNodes` | All SONiC | Device connection succeeds (via SSH tunnel) |
| `TestE2E_VerifyStartupConfig` | All SONiC | `DEVICE_METADATA\|localhost` hostname matches node name |
| `TestE2E_VerifyLoopbackInterface` | All SONiC | `LOOPBACK_INTERFACE` contains `Loopback0` |
| `TestE2E_ListInterfaces` | All SONiC | `ListInterfaces()` returns non-empty, `GetInterface()` works |

### 14.2 operations_test.go

| Test | Node | CONFIG_DB Tables Modified |
|---|---|---|
| `TestE2E_CreateVLAN` | leaf | `VLAN\|Vlan500` |
| `TestE2E_DeleteVLAN` | leaf | `VLAN\|Vlan501` (create then delete) |
| `TestE2E_AddVLANMember` | leaf | `VLAN\|Vlan502`, `VLAN_MEMBER\|Vlan502\|<port>` |
| `TestE2E_RemoveVLANMember` | leaf | `VLAN_MEMBER` (create then remove) |
| `TestE2E_ConfigureSVI` | leaf | `VLAN_INTERFACE`, `VRF` |
| `TestE2E_CreateLAG` | leaf | `PORTCHANNEL\|PortChannel200` |
| `TestE2E_DeleteLAG` | leaf | `PORTCHANNEL\|PortChannel201` |
| `TestE2E_AddLAGMember` | leaf | `PORTCHANNEL_MEMBER` |
| `TestE2E_RemoveLAGMember` | leaf | `PORTCHANNEL_MEMBER` |
| `TestE2E_ConfigureInterface` | leaf | `INTERFACE` description/MTU |
| `TestE2E_SetInterfaceVRF` | leaf | `INTERFACE`, `VRF` |
| `TestE2E_SetInterfaceIP` | leaf | `INTERFACE\|<port>\|<ip/mask>` |
| `TestE2E_CreateACLTable` | leaf | `ACL_TABLE` |
| `TestE2E_DeleteACLTable` | leaf | `ACL_TABLE` |
| `TestE2E_AddACLRule` | leaf | `ACL_RULE` |
| `TestE2E_DeleteACLRule` | leaf | `ACL_RULE` |
| `TestE2E_BindACL` | leaf | `ACL_TABLE` ports field |
| `TestE2E_CreateVRF` | leaf | `VRF` |
| `TestE2E_DeleteVRF` | leaf | `VRF` |
| `TestE2E_CreateVTEP` | leaf | `VXLAN_TUNNEL` |
| `TestE2E_MapL2VNI` | leaf | `VXLAN_TUNNEL_MAP`, `SUPPRESS_VLAN_NEIGH` |
| `TestE2E_UnmapL2VNI` | leaf | `VXLAN_TUNNEL_MAP` |
| `TestE2E_ApplyService` | leaf | Multiple (service-dependent) |
| `TestE2E_RemoveService` | leaf | Reverse of ApplyService |

Helper functions in `operations_test.go`:

```go
func leafNodeName(t *testing.T) string           // Find first leaf node
func spineNodeName(t *testing.T) string          // Find first spine node
func findFreePhysicalInterface(t, dev) string    // Find unbound port
func findFreePhysicalInterfaces(t, dev, count) []string
```

### 14.3 multidevice_test.go

| Test | Nodes | Verifies |
|---|---|---|
| `TestE2E_BGPNeighborState` | All leaves | `BGP_NEIGHBOR_TABLE` state = `Established` (soft fail) |
| `TestE2E_VLANAcrossTwoLeaves` | 2 leaves | Same VLAN 600 created on both; verified via fresh connections |
| `TestE2E_EVPNFabricHealth` | All SONiC | Hostname, BGP config, VTEP existence, `RunHealthChecks()` |

### 14.4 dataplane_test.go

| Test | Nodes | Server IPs | Verifies |
|---|---|---|---|
| `TestE2E_DataPlane_L2Bridged` | 2 leaves + 2 servers | `10.70.0.1/24`, `10.70.0.2/24` | VLAN 700 + L2VNI 10700 + ASIC convergence + ping (soft fail) |
| `TestE2E_DataPlane_IRBSymmetric` | 2 leaves + 2 servers | `10.80.0.10/24`, `10.80.0.20/24` (gw `10.80.0.1`) | VRF + VLAN 800 + SVI + anycast GW + ping (soft fail) |
| `TestE2E_DataPlane_L3Routed` | 2 leaves + 2 servers | `10.90.1.2/30`, `10.90.2.2/30` | VRF + L3 interface + inter-subnet routing + ping (soft fail) |

Helper function:
```go
func twoLeafNames(t *testing.T) (string, string)  // Find two leaf nodes
```

## 15. Test ID Allocation

To avoid collisions between concurrent test functions, each test uses
dedicated resource IDs:

| Resource | ID Range | Used By |
|---|---|---|
| VLANs | 500-506 | operations_test.go |
| VLANs | 600 | multidevice_test.go |
| VLANs | 700 | dataplane L2Bridged |
| VLANs | 800 | dataplane IRBSymmetric |
| PortChannels | 200-203 | operations_test.go |
| L2VNIs | 10505, 10506 | operations_test.go |
| L2VNIs | 10700 | dataplane L2Bridged |
| L2VNIs | 10800 | dataplane IRBSymmetric |
| VRF names | `Vrf_e2e_test` | operations_test.go |
| VRF names | `Vrf_e2e_svi` | operations_test.go |
| VRF names | `Vrf_e2e_delete` | operations_test.go |
| VRF names | `Vrf_e2e_iface` | operations_test.go |
| VRF names | `Vrf_e2e_irb` | dataplane IRBSymmetric |
| VRF names | `Vrf_e2e_l3` | dataplane L3Routed |
| Subnets | `10.70.0.0/24` | dataplane L2Bridged |
| Subnets | `10.80.0.0/24` | dataplane IRBSymmetric |
| Subnets | `10.90.1.0/30`, `10.90.2.0/30` | dataplane L3Routed |
| ACL table names | `E2E_TEST_ACL`, `E2E_*_ACL` | operations_test.go |

## 16. Test Execution Parameters

```
go test -tags e2e -v -count=1 -timeout 10m -p 1 ./...
```

| Flag | Value | Reason |
|---|---|---|
| `-tags e2e` | -- | Include E2E test files |
| `-v` | -- | Verbose output (step logging) |
| `-count=1` | -- | Disable test caching |
| `-timeout 10m` | -- | Allow for slow VM operations |
| `-p 1` | -- | Serialize test packages (device locking) |

Output is tee'd to `testlab/.generated/e2e-results.txt`.

## 17. Container Naming Convention

Containerlab prefixes container names with `clab-<topology>-`:

| Topology Node | Container Name | Short Name |
|---|---|---|
| `spine1` | `clab-spine-leaf-spine1` | `spine1` |
| `spine2` | `clab-spine-leaf-spine2` | `spine2` |
| `leaf1` | `clab-spine-leaf-leaf1` | `leaf1` |
| `leaf2` | `clab-spine-leaf-leaf2` | `leaf2` |
| `server1` | `clab-spine-leaf-server1` | `server1` |
| `server2` | `clab-spine-leaf-server2` | `server2` |

Test utilities strip the prefix to get the short name used for device
profiles and node references:

```go
name := strings.TrimPrefix(c.Name, "clab-"+topoName+"-")
```

## 18. Cleanup Strategy

### 18.1 Cleanup Ordering for ASIC-Dependent Topologies

When a test creates multi-layer state (VRF + VLAN + VNI + SVI), cleanup
MUST delete in reverse dependency order to avoid orchagent errors:

```
IP address -> SVI -> VNI mapping -> VLAN member -> VLAN -> VRF
```

Example from dataplane tests:

```go
t.Cleanup(func() {
    c := context.Background()
    client := testutil.LabRedisClient(t, name, 4)
    // Reverse dependency order:
    client.Del(c, "VLAN_INTERFACE|Vlan800|10.80.0.1/24")   // 1. IP first
    client.Del(c, "VLAN_INTERFACE|Vlan800")                  // 2. SVI
    client.Del(c, "VXLAN_TUNNEL_MAP|vtep1|map_10800_Vlan800") // 3. VNI mapping
    client.Del(c, "VLAN_MEMBER|Vlan800|Ethernet2")           // 4. Member
    client.Del(c, "VLAN|Vlan800")                            // 5. VLAN
    client.Del(c, "VRF|Vrf_tenant1")                         // 6. VRF last
})
```

### 18.2 `LabCleanupChanges()` -- Operation-Based

Registers a `t.Cleanup` function that acquires a fresh device connection
and calls a reverse operation:

```go
testutil.LabCleanupChanges(t, nodeName,
    func(ctx context.Context, d *network.Device) (*network.ChangeSet, error) {
        return d.DeleteVLAN(ctx, 500)
    })
```

**Implementation:** Creates a new network, connects, locks, executes the
cleanup function, then disconnects and unlocks -- all within a 30-second
timeout.

### 18.3 Raw Redis `DEL` -- Direct

For multi-step or cross-device cleanups, tests delete keys directly via
`LabRedisClient()` (which uses the SSH tunnel pool):

```go
t.Cleanup(func() {
    c := context.Background()
    client := testutil.LabRedisClient(t, name, 4)
    client.Del(c, "VXLAN_TUNNEL_MAP|vtep1|map_10700_Vlan700")
    client.Del(c, "VLAN_MEMBER|Vlan700|Ethernet8")
    client.Del(c, "VLAN|Vlan700")
})
```

Cleanup functions are registered immediately after creation, before
verification. Go's `t.Cleanup` runs in LIFO order, ensuring reverse
dependency ordering within a single test.

## 19. Related Documentation

- [e2e-hld.md](e2e-hld.md) -- High-Level Design (architecture overview)
- [e2e-howto.md](e2e-howto.md) -- Practical guide for running and writing tests
- [NGDP_DEBUGGING.md](../NGDP_DEBUGGING.md) -- ASIC emulator internals
- [SONIC_VS_PITFALLS.md](../SONIC_VS_PITFALLS.md) -- VS limitations catalog
- [CONFIGDB_GUIDE.md](../CONFIGDB_GUIDE.md) -- CONFIG_DB schema reference
- [VERIFICATION_TOOLKIT.md](../VERIFICATION_TOOLKIT.md) -- Verification tools
