# Testing

Newtron uses a three-tier testing strategy: unit tests that run without any
infrastructure, integration tests that run against a real Redis instance
seeded with SONiC-format data, and E2E tests that run against containerlab
virtual SONiC topologies.

## Quick Reference

```bash
make test                # unit tests only
make test-integration    # start Redis, seed, run integration tests, stop Redis
make test-all            # unit + integration
make test-e2e            # e2e tests (requires running lab)
make test-e2e-full       # start lab, run e2e tests, stop lab
make coverage            # integration tests with coverage report
make coverage-html       # HTML coverage report
```

## Unit Tests

Unit tests have no build tags and no external dependencies:

```bash
go test ./...
```

## Integration Tests

### How It Works

Newtron devices communicate exclusively through Redis -- there is no SSH or
CLI involved. `device.Connect()` creates two Redis clients:

- **CONFIG_DB** (database 4) -- switch configuration (ports, VLANs, VRFs, BGP, ACLs, etc.)
- **STATE_DB** (database 6) -- operational state (port status, BGP sessions, VXLAN tunnels)

The integration tests exploit this by running a plain Redis container and
seeding it with SONiC-format hash data. From the code's perspective, the
Redis container is indistinguishable from a real switch.

The connection path is `device.Connect() → redis.NewClient({Addr: "<MgmtIP>:6379", DB: 4|6})`,
so the test Redis container must be reachable on port 6379. Tests discover
the container's Docker bridge IP via `docker inspect` and use that as the
management IP -- no host port mapping is needed.

### Prerequisites

- Docker
- `redis-cli` (for seeding)
- `python3` (used by the seed script to parse JSON)

### Running

The easiest way is through Make:

```bash
make test-integration
```

This runs `redis-start`, `redis-seed`, the test suite, and `redis-stop` in
sequence. Redis is always stopped afterward, even if tests fail.

To manage the lifecycle manually:

```bash
./testlab/setup.sh redis-start    # start container
./testlab/setup.sh redis-seed     # flush and seed both DBs
go test -tags integration -v -count=1 -p 1 ./...
./testlab/setup.sh redis-stop     # remove container
```

The `-p 1` flag is required because all packages share a single Redis
instance and each test flushes/re-seeds the databases. Running packages in
parallel causes state interference.

### Lab Management

```bash
./testlab/setup.sh redis-start    # start the newtron-test-redis container
./testlab/setup.sh redis-stop     # stop and remove it
./testlab/setup.sh redis-seed     # flush DBs 4 and 6, then re-seed from JSON
./testlab/setup.sh redis-ip       # print the container's bridge IP
./testlab/setup.sh status         # show container state and key counts
```

### Seed Data

Seed files live in `testlab/seed/` and use the SONiC Redis schema:

```
{ "TABLE_NAME": { "entry_key": { "field": "value", ... }, ... }, ... }
```

Each entry becomes a Redis hash at key `TABLE_NAME|entry_key`.

**`configdb.json`** (DB 4) contains a realistic SONiC leaf switch:

| Table | Entries | Description |
|-------|---------|-------------|
| DEVICE_METADATA | 1 | Hostname, BGP ASN, MAC |
| PORT | 8 | Ethernet0-7 (6 up, 2 admin-down) |
| PORTCHANNEL | 1 | PortChannel100 |
| PORTCHANNEL_MEMBER | 2 | Ethernet4, Ethernet5 |
| VLAN | 2 | Vlan100 (Servers), Vlan200 (Storage) |
| VLAN_MEMBER | 3 | Tagged/untagged members |
| VLAN_INTERFACE | 1 | Vlan100 SVI in Vrf_CUST1 |
| VRF | 1 | Vrf_CUST1 with L3VNI 10001 |
| INTERFACE | 2 | Ethernet0 (plain), Ethernet1 (VRF + IP) |
| LOOPBACK_INTERFACE | 1 | Loopback0 |
| VXLAN_TUNNEL | 1 | vtep1 |
| VXLAN_EVPN_NVO | 1 | nvo1 |
| VXLAN_TUNNEL_MAP | 2 | L2VNI + L3VNI mappings |
| SUPPRESS_VLAN_NEIGH | 1 | ARP suppression on Vlan100 |
| BGP_GLOBALS | 1 | AS 13908, router-id 10.0.0.10 |
| BGP_NEIGHBOR | 2 | 10.0.0.1, 10.0.0.2 |
| BGP_NEIGHBOR_AF | 2 | IPv4 unicast activated |
| ACL_TABLE | 2 | customer-l3-in, customer-l3-out |
| ACL_RULE | 2 | Permit/deny rules |
| NEWTRON_SERVICE_BINDING | 1 | Ethernet1 → customer-l3 |

**`statedb.json`** (DB 6) contains matching operational state:

| Table | Entries | Description |
|-------|---------|-------------|
| PORT_TABLE | 8 | Oper status for all ports |
| LAG_TABLE | 1 | PortChannel100 oper status |
| LAG_MEMBER_TABLE | 2 | Member status |
| BGP_NEIGHBOR_TABLE | 2 | Both peers Established |
| VXLAN_TUNNEL_TABLE | 1 | vtep1 up |

### Test Helpers

Test utilities live in `internal/testutil/` (build tag: `integration || e2e`).

**Fixtures** (`fixtures.go`) -- pre-built objects with cleanup:

- `ConnectedDevice(t)` -- seeds both DBs, connects, registers `t.Cleanup`
- `LockedDevice(t)` -- connected + locked
- `ConnectedNetworkDevice(t)` -- full `network.Device` backed by Redis
- `LockedNetworkDevice(t)` -- connected + locked network device
- `TestProfile()` -- `spec.ResolvedProfile` pointing at the Redis container
- `TestNetwork(t)` -- `network.Network` loaded from `testlab/specs/`

**Redis helpers** (`redis.go`):

- `SetupBothDBs(t)` -- flush and re-seed both DBs (most tests call this)
- `WriteSingleEntry(t, addr, db, table, key, fields)` -- inject data mid-test
- `DeleteEntry(t, addr, db, table, key)` -- remove a key mid-test
- `ReadEntry(t, addr, db, table, key)` -- read back hash fields
- `EntryExists(t, addr, db, table, key)` -- check key existence

**Environment** (`testutil.go`):

- `RedisAddr()` -- discovers the container IP via `docker inspect`, or reads `NEWTRON_TEST_REDIS_ADDR`
- `SkipIfNoRedis(t)` -- skips the test if Redis is unreachable
- `Context(t)` -- returns a context with 30-second timeout, cancel registered via `t.Cleanup`

### Test Coverage

107 integration tests across 7 files:

| Package | File | Tests | What it covers |
|---------|------|-------|----------------|
| `pkg/device` | `device_integration_test.go` | 28 | Connect/disconnect, all ConfigDB tables, StateDB, existence checks, lock/unlock |
| `pkg/device` | `configdb_integration_test.go` | 12 | ConfigDBClient CRUD, ApplyChanges (add/modify/delete), Reload |
| `pkg/device` | `statedb_integration_test.go` | 8 | Port/LAG/BGP/VXLAN state tables, RefreshState |
| `pkg/health` | `checker_integration_test.go` | 8 | All 5 health checks, full Checker.Run, degraded state |
| `pkg/network` | `device_integration_test.go` | 14 | ConnectDevice, list/get interfaces/VLANs/VRFs/port channels/BGP |
| `pkg/network` | `interface_integration_test.go` | 8 | Properties, VRF/IP, service binding, LAG membership, ACLs, type detection |
| `pkg/operations` | `operation_integration_test.go` | 29 | PreconditionChecker (all Require* methods), DependencyChecker |

### Writing New Tests

1. Use the `integration` build tag:
   ```go
   //go:build integration

   package mypackage_test
   ```

2. Use fixtures for common setup:
   ```go
   func TestSomething(t *testing.T) {
       dev := testutil.ConnectedDevice(t) // seeds DBs, connects, auto-cleanup
       // ... test against dev ...
   }
   ```

3. For tests that modify Redis state, call `testutil.WithCleanState(t)` in
   subtests to re-seed between cases.

4. For ad-hoc data, use `WriteSingleEntry` / `DeleteEntry`:
   ```go
   func TestCustomState(t *testing.T) {
       dev := testutil.ConnectedDevice(t)
       addr := testutil.RedisAddr()

       testutil.WriteSingleEntry(t, addr, 6, "PORT_TABLE", "Ethernet0",
           map[string]string{"oper_status": "down", "admin_status": "up"})

       testutil.ReconnectDevice(t, dev) // reload state from Redis
       // ... assert on the changed state ...
   }
   ```

### Environment Variables

| Variable | Purpose | Default |
|----------|---------|---------|
| `NEWTRON_TEST_REDIS_ADDR` | Override Redis address (host:port) | Auto-discovered via `docker inspect` |
| `NEWTRON_TESTLAB_DIR` | Override testlab directory path | Derived from source file location |

### Directory Layout

```
testlab/
├── docker-compose.yml      # Redis container definition
├── setup.sh                # Lab management script
├── seed/
│   ├── configdb.json       # CONFIG_DB seed data (DB 4)
│   └── statedb.json        # STATE_DB seed data (DB 6)
└── specs/                  # Minimal spec files for network.Network tests
    ├── network.json
    ├── site.json
    ├── platforms.json
    └── profiles/
        ├── test-leaf1.json
        └── test-spine1.json

internal/testutil/
├── testutil.go             # Redis discovery, skip helpers, context
├── redis.go                # Seed/flush/read/write Redis entries
├── fixtures.go             # Pre-built Device/Network fixtures
├── lab.go                  # E2E lab helpers (build tag: e2e)
└── report.go               # E2E test report generator (build tag: e2e)

test/e2e/
├── main_test.go            # TestMain: baseline reset, report, tunnel cleanup
├── connectivity_test.go    # Connect, verify startup config, interfaces
├── operations_test.go      # VLAN/LAG/ACL/VRF/service operations
├── multidevice_test.go     # Cross-device BGP, multi-leaf VLAN, fabric health
└── dataplane_test.go       # L2 bridged, IRB symmetric, L3 routed data-plane
```

## E2E Tests (Containerlab)

E2E tests deploy a virtual SONiC fabric using containerlab and exercise
newtron operations against real SONiC devices. For the complete guide, see
**[E2E_TESTING.md](E2E_TESTING.md)**.

### Quick Reference

```bash
make lab-start          # deploy 2-spine + 2-leaf topology
make lab-status         # verify nodes and Redis connectivity
make test-e2e           # run all 31 E2E tests
make lab-stop           # tear down

make test-e2e-full      # start lab, run tests, stop lab (all-in-one)
```

Test results are saved to `testlab/.generated/e2e-results.txt`.
A markdown summary report is generated at `testlab/.generated/e2e-report.md`.

### E2E Test Coverage

34 tests across 4 files covering all 24 operations:

| File | Tests | What it covers |
|------|-------|----------------|
| `connectivity_test.go` | 4 | Connect all nodes, verify hostnames, loopback interfaces, interface listing |
| `operations_test.go` | 24 | VLAN CRUD, LAG CRUD, interface config/VRF/IP, ACL table/rule/bind, VRF/VTEP/VNI, service apply/remove |
| `multidevice_test.go` | 3 | BGP neighbor state (STATE_DB polling), VLAN across leaves, EVPN fabric health checks |
| `dataplane_test.go` | 3 | L2 bridged forwarding, IRB symmetric routing, L3 routed forwarding |

**Data-plane note:** SONiC-VS is a control-plane simulator. Data-plane tests
verify CONFIG_DB and ASIC_DB (hard fail) then attempt pings (soft fail /
skip). See [SONIC_VS_PITFALLS.md](SONIC_VS_PITFALLS.md) for details.

## Related Documentation

| Document | Description |
|----------|-------------|
| [E2E_TESTING.md](E2E_TESTING.md) | Complete E2E testing guide (lab lifecycle, helpers API, debugging) |
| [LEARNINGS.md](LEARNINGS.md) | Systematized debugging learnings from E2E test development |
| [SONIC_VS_PITFALLS.md](SONIC_VS_PITFALLS.md) | All known SONiC-VS pitfalls and workarounds |
| [NGDP_DEBUGGING.md](NGDP_DEBUGGING.md) | NGDP ASIC emulator debugging guide |
| [CONFIGDB_GUIDE.md](CONFIGDB_GUIDE.md) | CONFIG_DB schema discovery and verification methodology |
| [CONTAINERLAB_HOWTO.md](CONTAINERLAB_HOWTO.md) | Containerlab setup guide with tripping hazards |
| [VERIFICATION_TOOLKIT.md](VERIFICATION_TOOLKIT.md) | Multi-layer verification tooling architecture |
| [DESIGN_PRINCIPLES.md](DESIGN_PRINCIPLES.md) | Design principles for robust SONiC management |
| [testing/test-plan.md](testing/test-plan.md) | Formal test plan with success criteria (functional, perf, scale, chaos) |
